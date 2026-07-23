package engine

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"time"
)

func pruneLocalByCount(localPath string, keep int) {
	if keep <= 0 {
		return
	}
	entries, err := os.ReadDir(localPath)
	if err != nil {
		slog.Warn("prune: cannot read local dir", "path", localPath, "error", err)
		return
	}
	var dirs []string
	for _, e := range entries {
		if e.IsDir() && isBackupDir(e.Name()) {
			dirs = append(dirs, e.Name())
		}
	}
	if len(dirs) <= keep {
		return
	}
	sort.Sort(sort.Reverse(sort.StringSlice(dirs)))
	for _, d := range dirs[keep:] {
		path := filepath.Join(localPath, d)
		slog.Info("pruning local backup", "path", path)
		if err := os.RemoveAll(path); err != nil {
			slog.Warn("prune: failed to remove", "path", path, "error", err)
		}
	}
}

func pruneLocalByDays(localPath string, days int, now time.Time) {
	if days <= 0 {
		return
	}
	entries, err := os.ReadDir(localPath)
	if err != nil {
		slog.Warn("prune: cannot read local dir", "path", localPath, "error", err)
		return
	}
	cutoff := now.Add(-time.Duration(days) * 24 * time.Hour)
	for _, entry := range entries {
		if !entry.IsDir() || !isBackupDir(entry.Name()) {
			continue
		}
		snapshotTime, err := time.ParseInLocation("20060102-1504", entry.Name(), now.Location())
		if err != nil {
			slog.Warn("prune: cannot parse local backup name", "path", filepath.Join(localPath, entry.Name()), "error", err)
			continue
		}
		path := filepath.Join(localPath, entry.Name())
		if _, err := entry.Info(); err != nil {
			slog.Warn("prune: cannot stat local backup", "path", path, "error", err)
			continue
		}
		if !snapshotTime.Before(cutoff) {
			continue
		}
		slog.Info("pruning local backup", "path", path)
		if err := os.RemoveAll(path); err != nil {
			slog.Warn("prune: failed to remove", "path", path, "error", err)
		}
	}
}

func pruneNASByDays(ctx context.Context, nas NASConfig, destRoot, namespace, serverName string, days int) error {
	if days <= 0 {
		return nil
	}
	return runSSH(ctx, nas, pruneNASByDaysCommand(destRoot, namespace, serverName, days))
}

func pruneNASByDaysCommand(destRoot, namespace, serverName string, days int) string {
	destDir := fmt.Sprintf("%s/%s/%s", destRoot, namespace, serverName)
	return fmt.Sprintf(
		"find %s -mindepth 1 -maxdepth 1 -type d -name '[0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9]-[0-9][0-9][0-9][0-9]' -mtime +%d -exec rm -rf {} +",
		shellQuote(destDir), days,
	)
}

func pruneNASByCount(ctx context.Context, nas NASConfig, destRoot, namespace, serverName string, count int) error {
	if count <= 0 {
		return nil
	}
	return runSSH(ctx, nas, pruneNASByCountCommand(destRoot, namespace, serverName, count))
}

func pruneNASByCountCommand(destRoot, namespace, serverName string, count int) string {
	destDir := fmt.Sprintf("%s/%s/%s", destRoot, namespace, serverName)
	return fmt.Sprintf(
		"ls -dt %s/[0-9]*-[0-9]* 2>/dev/null | tail -n +%d | tr '\\n' '\\0' | xargs -0 -r rm -rf --",
		shellQuote(destDir), count+1,
	)
}

func runSSH(ctx context.Context, nas NASConfig, remoteCmd string) error {
	args := sshBaseArgs(nas)
	args = append(args, fmt.Sprintf("%s@%s", nas.SSHUser, nas.SSHHost))
	args = append(args, remoteCmd)

	slog.Debug("prune: running SSH", "args", args)
	cmd := commandRunner.CommandContext(ctx, args[0], args[1:]...)
	cmd.SetStdout(os.Stdout)
	cmd.SetStderr(os.Stderr)
	return cmd.Run()
}
