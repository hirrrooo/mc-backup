package engine

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"log/slog"
)

type offloadJob struct {
	key        string
	namespace  string
	serverName string
	hotPath    string
	timestamp  string
	coldTarget string
	cfg        Config
	watch      WatchConfig
}

type lastBackup struct {
	hot   string
	local string
	nas   string
}

type Daemon struct {
	cfgPath    string
	ac         atomicConfig
	jobTracker *JobTracker
	// lastBackups stores the last recorded snapshot paths per server (namespace/serverName -> *lastBackup).
	// All reads and writes are guarded by stateMu.
	lastBackups  map[string]*lastBackup
	stateMu      sync.Mutex
	offloadCh    chan offloadJob
	offloadWg    sync.WaitGroup
	autoServers  map[string]bool
	autoMu       sync.Mutex
	legacyMu     sync.Mutex
	legacyWarned map[string]struct{}
	cycleMu      sync.Mutex
	cancelMu     sync.Mutex
	cancelFn     context.CancelFunc
	Debug        bool
}

func NewDaemon(cfgPath string, cfg *Config) *Daemon {
	chCap := len(cfg.Servers)
	if chCap < 1 {
		chCap = 16
	}
	d := &Daemon{
		cfgPath:      cfgPath,
		jobTracker:   NewJobTracker(),
		lastBackups:  make(map[string]*lastBackup),
		offloadCh:    make(chan offloadJob, chCap),
		autoServers:  loadAutoServerNames(cfgPath),
		legacyWarned: make(map[string]struct{}),
	}
	d.ac.Store(cfg)
	return d
}
func (d *Daemon) warnLegacyBackupDirOnce(w WatchConfig, serverName string) {
	path := w.backupDir(serverName)
	entries, err := os.ReadDir(path)
	if err != nil || len(entries) == 0 {
		return
	}

	d.legacyMu.Lock()
	if d.legacyWarned == nil {
		d.legacyWarned = make(map[string]struct{})
	}
	if _, warned := d.legacyWarned[path]; warned {
		d.legacyMu.Unlock()
		return
	}
	d.legacyWarned[path] = struct{}{}
	d.legacyMu.Unlock()

	warnLegacyBackupDir(w, serverName)
}

func (d *Daemon) waitForContainers(ctx context.Context, cfg *Config) {
	servers, _ := discoverServersWithWarning(cfg.Watch, cfg.Servers, d.warnLegacyBackupDirOnce)
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

func (d *Daemon) discoverSnapshots(ctx context.Context, cfg *Config) {
	stored := readLastSnapshots(d.cfgPath)
	servers, _ := discoverServersWithWarning(cfg.Watch, cfg.Servers, d.warnLegacyBackupDirOnce)

	for _, s := range servers {
		target, err := resolveBackupTarget(s.Name, s.Server, cfg.Local, cfg.NAS)
		if err != nil {
			slog.Error("snapshot discovery: invalid backup target", "server", s.Name, "error", err)
			continue
		}

		key := s.Watch.Namespace + "/" + s.Name
		st := stored[s.Name]

		var discoveredHot, discoveredLocal, discoveredNAS string

		if target == "tiered-local" || target == "tiered-nas" {
			hotDir := filepath.Join(cfg.Local.HotRoot, s.Watch.Namespace, s.Name)
			if entries, readErr := os.ReadDir(hotDir); readErr == nil {
				latest := ""
				for _, e := range entries {
					if e.IsDir() && isBackupDir(e.Name()) && e.Name() > latest {
						latest = e.Name()
					}
				}
				if latest != "" {
					discoveredHot = filepath.Join(hotDir, latest)
				}
			}
		}

		if target == "local" || target == "tiered-local" {
			localDir := filepath.Join(cfg.Local.DestRoot, s.Watch.Namespace, s.Name)
			if entries, readErr := os.ReadDir(localDir); readErr == nil {
				latest := ""
				for _, e := range entries {
					if e.IsDir() && isBackupDir(e.Name()) && e.Name() > latest {
						latest = e.Name()
					}
				}
				if latest != "" {
					discoveredLocal = filepath.Join(localDir, latest)
				}
			}
		}

		if target == "nas" || target == "tiered-nas" {
			nasDir := fmt.Sprintf("%s/%s/%s", cfg.NAS.DestRoot, s.Watch.Namespace, s.Name)
			nasArgs := sshBaseArgs(cfg.NAS)
			nasArgs = append(nasArgs,
				fmt.Sprintf("%s@%s", cfg.NAS.SSHUser, cfg.NAS.SSHHost),
				latestNASSnapshotCommand(nasDir),
			)
			cmd := commandRunner.CommandContext(ctx, nasArgs[0], nasArgs[1:]...)
			if out, err := cmd.Output(); err == nil {
				nasSnap := strings.TrimSpace(string(out))
				if nasSnap != "" {
					discoveredNAS = nasDir + "/" + filepath.Base(nasSnap)
				}
			} else {
				slog.Debug("cannot list NAS snapshots for discovery", "server", s.Name, "error", err)
			}
		}

		finalHot := st.Hot
		if discoveredHot != "" && (finalHot == "" || filepath.Base(discoveredHot) > filepath.Base(finalHot)) {
			finalHot = discoveredHot
		}

		finalLocal := st.Local
		if discoveredLocal != "" && (finalLocal == "" || filepath.Base(discoveredLocal) > filepath.Base(finalLocal)) {
			finalLocal = discoveredLocal
		}

		finalNAS := st.NAS
		if discoveredNAS != "" && (finalNAS == "" || filepath.Base(discoveredNAS) > filepath.Base(finalNAS)) {
			finalNAS = discoveredNAS
		}

		if finalHot != st.Hot || finalLocal != st.Local || finalNAS != st.NAS || st.Time.IsZero() {
			t := snapshotTime(finalHot)
			if t.IsZero() {
				t = snapshotTime(finalLocal)
			}
			if t.IsZero() {
				t = snapshotTime(filepath.Base(finalNAS))
			}
			if t.IsZero() {
				t = time.Now()
			}

			if finalHot != "" || finalLocal != "" || finalNAS != "" {
				writeLastSnapshotAt(d.cfgPath, s.Name, finalLocal, finalNAS, finalHot, t)
				slog.Info("reconciled/discovered existing snapshot",
					"server", s.Name, "hot", finalHot, "local", finalLocal, "nas", finalNAS)
			}
		}

		d.stateMu.Lock()
		d.lastBackups[key] = &lastBackup{
			hot:   finalHot,
			local: finalLocal,
			nas:   finalNAS,
		}
		d.stateMu.Unlock()
	}
}

func latestNASSnapshotCommand(nasDir string) string {
	return fmt.Sprintf("ls -dt %s/[0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9]-[0-9][0-9][0-9][0-9] 2>/dev/null | head -1", shellQuote(nasDir))
}
func (d *Daemon) provisionServers(cfg *Config, newServers []struct {
	Name   string
	Server ServerConfig
}) *Config {
	if len(newServers) == 0 {
		return cfg
	}
	cfg = cloneConfig(cfg)
	d.autoMu.Lock()
	for _, ns := range newServers {
		d.autoServers[ns.Name] = true
		cfg.Servers[ns.Name] = ns.Server
	}
	auto := make(map[string]ServerConfig)
	for name := range d.autoServers {
		if s, ok := cfg.Servers[name]; ok {
			auto[name] = s
		}
	}
	d.autoMu.Unlock()

	if err := SaveAutoServers(d.cfgPath, auto); err != nil {
		slog.Error("failed to save auto-provisioned config", "error", err)
	}
	return cfg
}

func (d *Daemon) Run() error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	return d.RunContext(ctx)
}

func (d *Daemon) RunContext(ctx context.Context) error {
	cfg := d.ac.Load()
	setupLogging(d.Debug)
	if err := watchConfig(d.cfgPath, &d.ac); err != nil {
		slog.Warn("config watcher failed, live reload disabled", "error", err)
	}

	startStatusServer(cfg.Global.ListenAddr, func() string {
		if c := d.ac.Load(); c != nil {
			return c.Global.APIToken
		}
		return ""
	}, d.jobTracker, StatusCallbacks{
		OnCancel: d.Cancel,
		OnScan: func() {
			go d.runDiscovery(ctx)
		},
		OnBackup: func(server string, offline bool) {
			go d.runBackupCycle(ctx, server, offline)
		},
	})

	slog.Info("mc-backup daemon starting",
		"initial_delay", cfg.Global.InitialDelay.Duration,
		"backup_interval", cfg.Global.BackupInterval.Duration,
	)

	d.waitForContainers(ctx, cfg)

	d.discoverSnapshots(ctx, cfg)

	snapshots := readLastSnapshots(d.cfgPath)
	d.stateMu.Lock()
	for name, snap := range snapshots {
		if s, ok := cfg.Servers[name]; ok && !s.Enabled {
			continue
		}
		key := watchKey(cfg, name)
		if key != "" && d.lastBackups[key] == nil {
			d.lastBackups[key] = &lastBackup{hot: snap.Hot, local: snap.Local, nas: snap.NAS}
		}
	}
	d.stateMu.Unlock()

	d.offloadWg.Add(1)
	go d.runOffloadWorker(ctx)

	var recent, due []string
	for name, snap := range snapshots {
		if s, ok := cfg.Servers[name]; ok && !s.Enabled {
			continue
		}
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
		d.runBackupCycle(ctx, "", false)
	} else {
		d.runBackupCycle(ctx, "", false)
	}

	backupTicker := time.NewTicker(cfg.Global.BackupInterval.Duration)
	defer backupTicker.Stop()

	discoveryTicker := time.NewTicker(1 * time.Minute)
	defer discoveryTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			slog.Info("daemon shutting down")
			d.offloadWg.Wait()
			return ctx.Err()
		case <-backupTicker.C:
			d.runBackupCycle(ctx, "", false)
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

func (d *Daemon) runOffloadWorker(ctx context.Context) {
	defer d.offloadWg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case job, ok := <-d.offloadCh:
			if !ok {
				return
			}
			if err := d.processOffloadJob(ctx, job); err != nil {
				slog.Warn("offload job failed", "server", job.serverName, "error", err)
			}
		}
	}
}

func (d *Daemon) processOffloadJob(ctx context.Context, job offloadJob) error {
	hotDir := filepath.Join(job.cfg.Local.HotRoot, job.namespace, job.serverName)
	selectedHot := ""
	if entries, err := os.ReadDir(hotDir); err == nil {
		for _, e := range entries {
			if e.IsDir() && isBackupDir(e.Name()) && e.Name() > selectedHot {
				selectedHot = e.Name()
			}
		}
	}
	if selectedHot != "" {
		selectedHot = filepath.Join(hotDir, selectedHot)
	}

	d.stateMu.Lock()
	b := d.lastBackups[job.key]
	prevCold := ""
	if b != nil {
		if job.coldTarget == "nas" {
			prevCold = b.nas
		} else {
			prevCold = b.local
		}
	}
	d.stateMu.Unlock()

	if selectedHot == "" || (prevCold != "" && filepath.Base(selectedHot) <= filepath.Base(prevCold)) {
		d.jobTracker.RemoveIfSnapshot(job.key, job.timestamp)
		return nil
	}

	if delay := job.cfg.Global.TransferDelay.Duration; delay > 0 {
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			d.jobTracker.RemoveIfSnapshot(job.key, job.timestamp)
			return ctx.Err()
		}
	}

	selectedTs := filepath.Base(selectedHot)
	coldState := "Transferring (Local HDD)"
	if job.coldTarget == "nas" {
		coldState = "Transferring (NAS)"
	}

	var maxTotal int64
	onProgress := func(bytesMoved, totalSize int64) {
		if totalSize > maxTotal {
			maxTotal = totalSize
		}
		d.jobTracker.UpdateIfSnapshot(job.key, selectedTs, &JobInfo{
			ServerName: job.serverName,
			Snapshot:   selectedTs,
			State:      coldState,
			BytesMoved: bytesMoved,
			TotalSize:  maxTotal,
		})
		if selectedTs != job.timestamp {
			d.jobTracker.UpdateIfSnapshot(job.key, job.timestamp, &JobInfo{
				ServerName: job.serverName,
				Snapshot:   job.timestamp,
				State:      coldState,
				BytesMoved: bytesMoved,
				TotalSize:  maxTotal,
			})
		}
	}

	d.jobTracker.UpdateIfSnapshot(job.key, job.timestamp, &JobInfo{
		ServerName: job.serverName,
		Snapshot:   job.timestamp,
		State:      coldState,
	})
	if selectedTs != job.timestamp {
		d.jobTracker.UpdateIfSnapshot(job.key, selectedTs, &JobInfo{
			ServerName: job.serverName,
			Snapshot:   selectedTs,
			State:      coldState,
		})
	}

	coldPath, err := offloadSnapshot(ctx, job.cfg, job.watch, job.serverName, selectedHot, selectedTs, job.coldTarget, prevCold, onProgress)
	if err != nil {
		d.jobTracker.RemoveIfSnapshot(job.key, job.timestamp)
		d.jobTracker.RemoveIfSnapshot(job.key, selectedTs)
		return err
	}

	d.stateMu.Lock()
	cur := d.lastBackups[job.key]
	if cur == nil {
		cur = &lastBackup{}
		d.lastBackups[job.key] = cur
	}
	if job.coldTarget == "nas" {
		cur.nas = coldPath
	} else {
		cur.local = coldPath
	}
	if cur.hot == "" || filepath.Base(selectedHot) >= filepath.Base(cur.hot) {
		cur.hot = selectedHot
	}
	savedHot := cur.hot
	savedLocal := cur.local
	savedNAS := cur.nas
	d.stateMu.Unlock()

	writeLastSnapshotAt(d.cfgPath, job.serverName, savedLocal, savedNAS, savedHot, snapshotTime(selectedHot))

	d.jobTracker.RemoveIfSnapshot(job.key, job.timestamp)
	d.jobTracker.RemoveIfSnapshot(job.key, selectedTs)

	if job.coldTarget == "nas" {
		if err := pruneNASByDays(ctx, job.cfg.NAS, job.cfg.NAS.DestRoot, job.namespace, job.serverName, job.cfg.Retention.PruneDays); err != nil {
			slog.Warn("NAS day pruning failed", "server", job.serverName, "error", err)
		}
		if err := pruneNASByCount(ctx, job.cfg.NAS, job.cfg.NAS.DestRoot, job.namespace, job.serverName, job.cfg.Retention.PruneCount); err != nil {
			slog.Warn("NAS count pruning failed", "server", job.serverName, "error", err)
		}
	} else {
		localPath := filepath.Join(job.cfg.Local.DestRoot, job.namespace, job.serverName)
		pruneLocalByDays(localPath, job.cfg.Retention.PruneDays, time.Now())
		pruneLocalByCount(localPath, job.cfg.Retention.PruneCount)
	}

	hotPath := filepath.Join(job.cfg.Local.HotRoot, job.namespace, job.serverName)
	pruneLocalByDays(hotPath, job.cfg.Retention.HotPruneDays, time.Now())
	pruneLocalByCount(hotPath, job.cfg.Retention.HotPruneCount)

	return nil
}

func snapshotTime(path string) time.Time {
	name := filepath.Base(path)
	if len(name) != 13 || name[8] != '-' {
		return time.Time{}
	}
	t, err := time.ParseInLocation("20060102-1504", name, time.Local)
	if err != nil {
		return time.Time{}
	}
	return t
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

// serverMatches reports whether a discovered server should be processed in a
// backup cycle targeted at onlyServer. An empty onlyServer selects every
// server; otherwise the comparison is case-insensitive because server names
// are normalized to lowercase at config load and discovery uses lowercase
// directory names.
func serverMatches(onlyServer, name string) bool {
	if onlyServer == "" {
		return true
	}
	return strings.EqualFold(onlyServer, name)
}

func (d *Daemon) runBackupCycle(parent context.Context, onlyServer string, offline bool) {
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
	servers, newServers := discoverServersWithWarning(cfg.Watch, cfg.Servers, d.warnLegacyBackupDirOnce)

	if onlyServer == "" {
		slog.Info("backup cycle starting",
			"servers", serverNames(servers),
		)
	} else {
		slog.Info("backup cycle starting", "server", onlyServer)
	}
	startTime := time.Now()

	cfg = d.provisionServers(cfg, newServers)
	if len(newServers) > 0 {
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

		if !serverMatches(onlyServer, s.Name) {
			continue
		}

		container := s.Server.ContainerName
		if container == "" {
			container = s.Name + "-mc-1"
		}
		if offline {
			if containerRunning(container) {
				slog.Warn("offline backup requested but container is running, data may be inconsistent",
					"server", s.Name, "container", container)
			}
			slog.Info("offline backup, skipping container checks", "server", s.Name)
		} else {
			if !containerRunning(container) {
				slog.Info("container not running, skipping backup", "server", s.Name, "container", container)
				continue
			}

			if s.Server.PauseIfNoPlayers {
				rconPass := resolveRconPassword(s.Server, s.Watch, s.Name)
				out, err := rconOutput(ctx, container, rconPass, "list")
				if err != nil {
					slog.Warn("cannot query player count, skipping backup", "server", s.Name, "error", err)
					continue
				}
				if countPlayers(out) == 0 {
					slog.Info("no players online, skipping backup", "server", s.Name)
					continue
				}
			}
		}

		be := NewBackupEngine(*cfg)
		key := s.Watch.Namespace + "/" + s.Name

		d.stateMu.Lock()
		prevPtr := d.lastBackups[key]
		var prev lastBackup
		if prevPtr != nil {
			prev = *prevPtr
		}
		d.stateMu.Unlock()

		ts := time.Now().Format("20060102-1504")
		d.jobTracker.Add(key, &JobInfo{
			ServerName: s.Name,
			Snapshot:   ts,
			State:      "Saving",
		})

		var maxTotal int64
		be.OnProgress = func(bytesMoved, totalSize int64) {
			if totalSize > maxTotal {
				maxTotal = totalSize
			}
			d.jobTracker.Add(key, &JobInfo{
				ServerName: s.Name,
				Snapshot:   ts,
				State:      "Saving",
				BytesMoved: bytesMoved,
				TotalSize:  maxTotal,
			})
		}

		res, err := be.BackupServer(ctx, s.Watch, s.Name, s.Server, prev.hot, prev.local, prev.nas, offline)
		if err != nil {
			slog.Error("backup failed", "server", s.Name, "error", err)
			d.jobTracker.Remove(key)
			continue
		}

		if !res.offloadPending {
			d.stateMu.Lock()
			cur := d.lastBackups[key]
			if cur == nil {
				cur = &lastBackup{}
				d.lastBackups[key] = cur
			}
			if res.target == "nas" {
				cur.nas = res.destination
			} else {
				cur.local = res.destination
			}
			savedLocal := cur.local
			savedNAS := cur.nas
			savedHot := cur.hot
			d.stateMu.Unlock()

			if res.target == "nas" {
				if err := pruneNASByDays(ctx, cfg.NAS, cfg.NAS.DestRoot, s.Watch.Namespace, s.Name, cfg.Retention.PruneDays); err != nil {
					slog.Warn("NAS day pruning failed", "server", s.Name, "error", err)
				}
				if err := pruneNASByCount(ctx, cfg.NAS, cfg.NAS.DestRoot, s.Watch.Namespace, s.Name, cfg.Retention.PruneCount); err != nil {
					slog.Warn("NAS count pruning failed", "server", s.Name, "error", err)
				}
			} else {
				localPath := filepath.Join(cfg.Local.DestRoot, s.Watch.Namespace, s.Name)
				pruneLocalByDays(localPath, cfg.Retention.PruneDays, time.Now())
				pruneLocalByCount(localPath, cfg.Retention.PruneCount)
			}
			d.jobTracker.Remove(key)
			writeLastSnapshot(d.cfgPath, s.Name, savedLocal, savedNAS, savedHot)
		} else {
			d.stateMu.Lock()
			cur := d.lastBackups[key]
			if cur == nil {
				cur = &lastBackup{}
				d.lastBackups[key] = cur
			}
			cur.hot = res.destination
			savedLocal := cur.local
			savedNAS := cur.nas
			savedHot := cur.hot
			d.stateMu.Unlock()

			writeLastSnapshot(d.cfgPath, s.Name, savedLocal, savedNAS, savedHot)

			d.jobTracker.Add(key, &JobInfo{
				ServerName: s.Name,
				Snapshot:   res.timestamp,
				State:      "Waiting Offload",
			})

			coldTarget := "local"
			if res.target == "tiered-nas" {
				coldTarget = "nas"
			}

			job := offloadJob{
				key:        key,
				namespace:  s.Watch.Namespace,
				serverName: s.Name,
				hotPath:    res.destination,
				timestamp:  res.timestamp,
				coldTarget: coldTarget,
				cfg:        *cfg,
				watch:      s.Watch,
			}

			select {
			case d.offloadCh <- job:
			default:
				slog.Warn("offload queue full, dropping offload job", "server", s.Name)
				d.jobTracker.RemoveIfSnapshot(key, res.timestamp)
			}
		}
	}

	slog.Info("backup cycle complete", "duration", time.Since(startTime).Round(time.Second))
}

var triggerDiscoveryBackup = func(d *Daemon, ctx context.Context) {
	go d.runBackupCycle(ctx, "", false)
}

func (d *Daemon) runDiscovery(ctx context.Context) {
	cfg := d.ac.Load()
	_, newServers := discoverServersWithWarning(cfg.Watch, cfg.Servers, d.warnLegacyBackupDirOnce)

	_ = d.provisionServers(cfg, newServers)
	if len(newServers) > 0 {
		slog.Info("new servers discovered, triggering immediate backup cycle")
		triggerDiscoveryBackup(d, ctx)
	}
}
