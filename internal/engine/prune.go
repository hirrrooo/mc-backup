package engine

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"

	"log/slog"
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

func pruneNASByDays(ctx context.Context, nas NASConfig, destRoot, namespace, serverName string, days int) error {
	if days <= 0 {
		return nil
	}
	destDir := fmt.Sprintf("%s/%s/%s", destRoot, namespace, serverName)
	remoteCmd := fmt.Sprintf(
		"find %s -maxdepth 1 -type d -regex '.*/[0-9]\\{8\\}-[0-9]\\{4\\}' -mtime +%d -exec rm -rf {} +",
		destDir, days,
	)
	return runSSH(ctx, nas, remoteCmd)
}

func pruneNASByCount(ctx context.Context, nas NASConfig, destRoot, namespace, serverName string, count int) error {
	if count <= 0 {
		return nil
	}
	destDir := fmt.Sprintf("%s/%s/%s", destRoot, namespace, serverName)
	remoteCmd := fmt.Sprintf(
		"ls -dt %s/[0-9]*-[0-9]* 2>/dev/null | tail -n +%d | xargs rm -rf",
		destDir, count+1,
	)
	return runSSH(ctx, nas, remoteCmd)
}

func runSSH(ctx context.Context, nas NASConfig, remoteCmd string) error {
	args := sshBaseArgs(nas)
	args = append(args, fmt.Sprintf("%s@%s", nas.SSHUser, nas.SSHHost))
	args = append(args, remoteCmd)

	slog.Debug("prune: running SSH", "args", args)
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
