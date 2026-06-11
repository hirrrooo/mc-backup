package engine

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"log/slog"
)

var nasWriteLock = make(chan struct{}, 1)

type ArchiveEngine struct {
	cfg Config
}

func NewArchiveEngine(cfg Config) *ArchiveEngine {
	return &ArchiveEngine{cfg: cfg}
}

func (ae *ArchiveEngine) ArchiveIfNeeded(ctx context.Context, watch WatchConfig, serverName string) {
	backupDir := watch.backupDir(serverName)
	pct, err := diskUsagePct(backupDir)
	if err != nil {
		slog.Warn("archive: cannot check disk usage", "path", backupDir, "error", err)
		return
	}
	if pct < float64(watch.MaxDiskPct) {
		return
	}

	entries, err := os.ReadDir(backupDir)
	if err != nil {
		return
	}
	var snapshots []string
	for _, e := range entries {
		if e.IsDir() && isBackupDir(e.Name()) {
			snapshots = append(snapshots, e.Name())
		}
	}
	if len(snapshots) <= watch.LocalKeep {
		return
	}
	sort.Strings(snapshots)

	slog.Info("archive: SSD above threshold, migrating oldest local backup to NAS",
		"server", serverName, "disk_pct", pct, "threshold", watch.MaxDiskPct)

	for _, snap := range snapshots[:len(snapshots)-watch.LocalKeep] {
		ae.migrateOne(ctx, watch, serverName, snap)
	}
}

func (ae *ArchiveEngine) migrateOne(ctx context.Context, watch WatchConfig, serverName, snapshot string) {
	nasSentinel := fmt.Sprintf("%s/.nas-ready", ae.cfg.NAS.DestRoot)
	checkArgs := sshBaseArgs(ae.cfg.NAS)
	checkArgs = append(checkArgs,
		fmt.Sprintf("%s@%s", ae.cfg.NAS.SSHUser, ae.cfg.NAS.SSHHost),
		fmt.Sprintf("test -f %s", nasSentinel),
	)
	cmd := exec.CommandContext(ctx, checkArgs[0], checkArgs[1:]...)
	if err := cmd.Run(); err != nil {
		slog.Warn("archive: NAS sentinel not found, skipping", "sentinel", nasSentinel)
		return
	}

	select {
	case nasWriteLock <- struct{}{}:
		defer func() { <-nasWriteLock }()
	case <-ctx.Done():
		return
	}

	localSrc := filepath.Join(watch.backupDir(serverName), snapshot)
	nasDest := fmt.Sprintf("%s/%s/%s/%s", ae.cfg.NAS.DestRoot, watch.Namespace, serverName, snapshot)
	parent := nasDest[:strings.LastIndex(nasDest, "/")]
	if err := ensureNASDir(ctx, ae.cfg.NAS, parent); err != nil {
		slog.Error("archive: failed to create NAS dir", "path", parent, "error", err)
		return
	}
	args := nasRsyncArgs(localSrc, "", nasDest, ae.cfg.NAS, ae.cfg.Global.MaxMBps, nil)

	slog.Info("archiving to NAS", "server", serverName, "snapshot", snapshot)
	if err := runRsync(ctx, args); err != nil {
		slog.Error("archive: NAS rsync failed", "snapshot", snapshot, "error", err)
		return
	}

	if err := os.RemoveAll(localSrc); err != nil {
		slog.Warn("archive: failed to remove local after migration", "path", localSrc, "error", err)
	} else {
		slog.Info("archive: local snapshot removed after NAS migration", "path", localSrc)
	}
}
