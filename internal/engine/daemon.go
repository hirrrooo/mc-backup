package engine

import (
	"context"
	"os"
	"os/signal"
	"path/filepath"
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
	autoServers map[string]bool
	cycleMu     sync.Mutex
	cancelMu    sync.Mutex
	cancelFn    context.CancelFunc
	Debug       bool
}

func NewDaemon(cfgPath string, cfg *Config) *Daemon {
	d := &Daemon{
		cfgPath:     cfgPath,
		jobTracker:  NewJobTracker(),
		lastBackups: make(map[string]*lastBackup),
		autoServers: loadAutoServerNames(cfgPath),
	}
	d.ac.Store(cfg)
	return d
}

func (d *Daemon) waitForContainers(ctx context.Context, cfg *Config) {
	servers, _ := discoverServers(cfg.Watch, cfg.Servers)
	if len(servers) == 0 {
		slog.Info("no servers found, skipping container uptime check")
		return
	}

	deadline := time.Now().Add(5 * cfg.Global.InitialDelay.Duration)

	for {
		if time.Now().After(deadline) {
			slog.Warn("container readiness deadline exceeded, proceeding anyway")
			return
		}
		allReady := true
		anyCheckable := false
		for _, s := range servers {
			container := s.Server.ContainerName
			if container == "" {
				container = s.Name + "-mc-1"
			}
			uptime, err := containerUptime(container)
			if err != nil {
				slog.Warn("cannot check container uptime, skipping readiness check",
					"server", s.Name, "container", container, "error", err)
				continue
			}
			anyCheckable = true
			if uptime < cfg.Global.InitialDelay.Duration {
				remaining := cfg.Global.InitialDelay.Duration - uptime
				slog.Info("container started recently, waiting",
					"server", s.Name, "uptime", uptime.Round(time.Second), "remaining", remaining.Round(time.Second))
				allReady = false
			}
		}
		if !anyCheckable {
			slog.Warn("no containers reachable, falling back to initial_delay")
			select {
			case <-time.After(cfg.Global.InitialDelay.Duration):
			case <-ctx.Done():
			}
			return
		}
		if allReady {
			slog.Info("all containers ready")
			return
		}
		select {
		case <-time.After(5 * time.Second):
		case <-ctx.Done():
			return
		}
	}
}

func (d *Daemon) Cancel() {
	d.cancelMu.Lock()
	defer d.cancelMu.Unlock()
	if d.cancelFn != nil {
		d.cancelFn()
	}
}

func (d *Daemon) saveAutoServers(cfg *Config) {
	auto := make(map[string]ServerConfig)
	for name := range d.autoServers {
		if s, ok := cfg.Servers[name]; ok {
			auto[name] = s
		}
	}
	if err := SaveAutoServers(d.cfgPath, auto); err != nil {
		slog.Error("failed to save auto-provisioned config", "error", err)
	}
}

func (d *Daemon) Run() error {
	cfg := d.ac.Load()
	setupLogging(d.Debug)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if err := watchConfig(d.cfgPath, &d.ac); err != nil {
		slog.Warn("config watcher failed, live reload disabled", "error", err)
	}

	startStatusServer(cfg.Global.ListenAddr, d.jobTracker, StatusCallbacks{
		OnCancel: d.Cancel,
		OnScan: func() {
			go d.runDiscovery(ctx)
		},
		OnBackup: func(server string) {
			go d.runBackupCycle(ctx, server)
		},
	})

	slog.Info("mc-backup daemon starting",
		"initial_delay", cfg.Global.InitialDelay.Duration,
		"backup_interval", cfg.Global.BackupInterval.Duration,
	)

	d.waitForContainers(ctx, cfg)

	snapshots := readLastSnapshots(d.cfgPath)
	for name, snap := range snapshots {
		key := watchKey(cfg, name)
		if key != "" && d.lastBackups[key] == nil {
			d.lastBackups[key] = &lastBackup{local: snap.Local, nas: snap.NAS}
		}
	}

	var recent, due []string
	for name, snap := range snapshots {
		if time.Since(snap.Time) < cfg.Global.BackupInterval.Duration {
			recent = append(recent, name)
		} else {
			due = append(due, name)
		}
	}
	if len(due) == 0 && len(recent) > 0 {
		slog.Info("skipping initial backup, all servers backed up within interval",
			"recent", recent, "interval", cfg.Global.BackupInterval.Duration)
	} else if len(due) > 0 {
		slog.Info("running initial backup for servers past interval",
			"due", due, "recent", recent)
		d.runBackupCycle(ctx, "")
	} else {
		d.runBackupCycle(ctx, "")
	}

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
			d.runBackupCycle(ctx, "")
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

func watchKey(cfg *Config, serverName string) string {
	for _, w := range cfg.Watch {
		if _, err := os.ReadDir(filepath.Join(w.Path, serverName)); err == nil {
			return w.Namespace + "/" + serverName
		}
	}
	return ""
}

func serverNames(servers []struct {
	Watch  WatchConfig
	Name   string
	Server ServerConfig
}) []string {
	names := make([]string, len(servers))
	for i, s := range servers {
		names[i] = s.Name
	}
	return names
}

func (d *Daemon) runBackupCycle(parent context.Context, onlyServer string) {
	d.cycleMu.Lock()
	defer d.cycleMu.Unlock()

	ctx, cancel := context.WithCancel(parent)
	d.cancelMu.Lock()
	d.cancelFn = cancel
	d.cancelMu.Unlock()
	defer func() {
		cancel()
		d.cancelMu.Lock()
		d.cancelFn = nil
		d.cancelMu.Unlock()
	}()

	cfg := d.ac.Load()
	servers, newServers := discoverServers(cfg.Watch, cfg.Servers)

	if onlyServer == "" {
		slog.Info("backup cycle starting",
			"servers", serverNames(servers),
		)
	} else {
		slog.Info("backup cycle starting", "server", onlyServer)
	}
	startTime := time.Now()

	if len(newServers) > 0 {
		cfg = cloneConfig(cfg)
		for _, ns := range newServers {
			d.autoServers[ns.Name] = true
			cfg.Servers[ns.Name] = ns.Server
		}
		d.saveAutoServers(cfg)
		slog.Info("auto-provisioned new servers in backup cycle")
	}

	for _, s := range servers {
		select {
		case <-ctx.Done():
			slog.Info("backup cycle canceled")
			return
		default:
		}

		if !s.Server.Enabled {
			continue
		}

		if onlyServer != "" && s.Name != onlyServer {
			continue
		}

		container := s.Server.ContainerName
		if container == "" {
			container = s.Name + "-mc-1"
		}
		if !containerRunning(container) {
			slog.Info("container not running, skipping backup", "server", s.Name, "container", container)
			continue
		}

		if s.Server.PauseIfNoPlayers {
			out, err := rconOutput(ctx, container, s.Server.RconPassword, "list")
			if err != nil {
				slog.Warn("cannot query player count, skipping backup", "server", s.Name, "error", err)
				continue
			}
			if countPlayers(out) == 0 {
				slog.Info("no players online, skipping backup", "server", s.Name)
				continue
			}
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
			State:      "Saving",
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

		writeLastSnapshot(d.cfgPath, s.Name, prev.local, prev.nas)

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

	slog.Info("backup cycle complete", "duration", time.Since(startTime).Round(time.Second))
}

func (d *Daemon) runDiscovery(ctx context.Context) {
	cfg := d.ac.Load()
	_, newServers := discoverServers(cfg.Watch, cfg.Servers)

	if len(newServers) > 0 {
		cfg = cloneConfig(cfg)
		for _, ns := range newServers {
			d.autoServers[ns.Name] = true
			cfg.Servers[ns.Name] = ns.Server
		}
		d.saveAutoServers(cfg)
		slog.Info("new servers discovered, triggering immediate backup cycle")
		go d.runBackupCycle(ctx, "")
	}
}
