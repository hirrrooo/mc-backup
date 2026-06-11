package main

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
	pct, err := diskUsagePct(watch.LocalPath)
	if err != nil {
		slog.Warn("archive: cannot check disk usage", "path", watch.LocalPath, "error", err)
		return
	}
	if pct < float64(watch.MaxDiskPct) {
		return
	}

	localDir := filepath.Join(watch.LocalPath, watch.Namespace, serverName)
	entries, err := os.ReadDir(localDir)
	if err != nil {
		return
	}
	var snapshots []string
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), serverName+"-") {
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
	checkArgs := []string{"ssh"}
	if ae.cfg.NAS.SSHPort != 0 && ae.cfg.NAS.SSHPort != 22 {
		checkArgs = append(checkArgs, "-p", fmt.Sprintf("%d", ae.cfg.NAS.SSHPort))
	}
	if ae.cfg.NAS.SSHKey != "" {
		checkArgs = append(checkArgs, "-i", os.ExpandEnv(ae.cfg.NAS.SSHKey))
	}
	checkArgs = append(checkArgs, "-o", "BatchMode=yes",
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

	localSrc := filepath.Join(watch.LocalPath, watch.Namespace, serverName, snapshot)
	nasDest := fmt.Sprintf("%s/%s/%s/%s", ae.cfg.NAS.DestRoot, watch.Namespace, serverName, snapshot)
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
