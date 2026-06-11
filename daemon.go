package main

import (
	"context"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"log/slog"
)

type lastBackup struct {
	local string
	nas   string
}

type Daemon struct {
	cfgPath     string
	ac          atomicConfig
	jobTracker  *JobTracker
	lastBackups map[string]*lastBackup
	cycleMu     sync.Mutex
	debug       bool
}

func NewDaemon(cfgPath string, cfg *Config) *Daemon {
	d := &Daemon{
		cfgPath:     cfgPath,
		jobTracker:  NewJobTracker(),
		lastBackups: make(map[string]*lastBackup),
	}
	d.ac.Store(cfg)
	return d
}

func (d *Daemon) Run() error {
	cfg := d.ac.Load()
	setupLogging(d.debug)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if err := watchConfig(d.cfgPath, &d.ac); err != nil {
		slog.Warn("config watcher failed, live reload disabled", "error", err)
	}

	startStatusServer(cfg.Global.ListenAddr, d.jobTracker)

	slog.Info("mc-backup daemon starting",
		"initial_delay", cfg.Global.InitialDelay.Duration,
		"backup_interval", cfg.Global.BackupInterval.Duration,
	)

	select {
	case <-time.After(cfg.Global.InitialDelay.Duration):
	case <-ctx.Done():
		return ctx.Err()
	}

	d.runBackupCycle(ctx)

	backupTicker := time.NewTicker(cfg.Global.BackupInterval.Duration)
	defer backupTicker.Stop()

	discoveryTicker := time.NewTicker(1 * time.Minute)
	defer discoveryTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			slog.Info("daemon shutting down")
			return ctx.Err()
		case <-backupTicker.C:
			d.runBackupCycle(ctx)
			newCfg := d.ac.Load()
			newInterval := newCfg.Global.BackupInterval.Duration
			if newInterval != cfg.Global.BackupInterval.Duration {
				backupTicker.Reset(newInterval)
				cfg = newCfg
			}
		case <-discoveryTicker.C:
			d.runDiscovery(ctx)
		}
	}
}

func (d *Daemon) runBackupCycle(ctx context.Context) {
	d.cycleMu.Lock()
	defer d.cycleMu.Unlock()

	cfg := d.ac.Load()
	oldLen := len(cfg.Servers)
	servers := discoverServers(cfg.Watch, cfg)

	if len(cfg.Servers) > oldLen {
		if err := SaveConfig(d.cfgPath, cfg); err != nil {
			slog.Error("failed to save auto-provisioned config", "error", err)
		}
		slog.Info("auto-provisioned new servers in backup cycle")
	}

	for _, s := range servers {
		if !s.Server.Enabled {
			continue
		}

		be := NewBackupEngine(*cfg)
		key := s.Watch.Namespace + "/" + s.Name
		prev := d.lastBackups[key]
		if prev == nil {
			prev = &lastBackup{}
		}

		d.jobTracker.Add(key, &JobInfo{
			ServerName: s.Name,
			Snapshot:   time.Now().Format("20060102-1504"),
			State:      "Starting",
		})

		destPath, usedSSH, err := be.BackupServer(ctx, s.Watch, s.Name, s.Server, prev.local, prev.nas)
		if err != nil {
			slog.Error("backup failed", "server", s.Name, "error", err)
			d.jobTracker.Remove(key)
			continue
		}

		if usedSSH {
			prev.nas = destPath
		} else {
			prev.local = destPath
		}
		d.lastBackups[key] = prev
		d.jobTracker.Remove(key)

		pruneLocalByCount(
			s.Watch.backupDir(s.Name),
			s.Watch.LocalKeep,
		)

		ae := NewArchiveEngine(*cfg)
		ae.ArchiveIfNeeded(ctx, s.Watch, s.Name)

		pruneRet := cfg.Retention
		pruneNASByDays(ctx, cfg.NAS, cfg.NAS.DestRoot, s.Watch.Namespace, s.Name, pruneRet.PruneDays)
		pruneNASByCount(ctx, cfg.NAS, cfg.NAS.DestRoot, s.Watch.Namespace, s.Name, pruneRet.PruneCount)
	}
}

func (d *Daemon) runDiscovery(ctx context.Context) {
	cfg := d.ac.Load()
	cfgNeedsSave := false
	oldServers := make(map[string]bool)
	for k := range cfg.Servers {
		oldServers[k] = true
	}

	discoverServers(cfg.Watch, cfg)

	for k := range cfg.Servers {
		if !oldServers[k] {
			cfgNeedsSave = true
			break
		}
	}

	if cfgNeedsSave {
		if err := SaveConfig(d.cfgPath, cfg); err != nil {
			slog.Error("failed to save auto-provisioned config", "error", err)
		}
	}
}
