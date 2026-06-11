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

func pruneLocalByCount(localPath, prefix string, keep int) {
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
		if e.IsDir() && strings.HasPrefix(e.Name(), prefix+"-") {
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
		"find %s -maxdepth 1 -type d -name '%s-*' -mtime +%d -exec rm -rf {} +",
		destDir, serverName, days,
	)
	return runSSH(ctx, nas, remoteCmd)
}

func pruneNASByCount(ctx context.Context, nas NASConfig, destRoot, namespace, serverName string, count int) error {
	if count <= 0 {
		return nil
	}
	destDir := fmt.Sprintf("%s/%s/%s", destRoot, namespace, serverName)
	remoteCmd := fmt.Sprintf(
		"ls -dt %s/%s-* 2>/dev/null | tail -n +%d | xargs rm -rf",
		destDir, serverName, count+1,
	)
	return runSSH(ctx, nas, remoteCmd)
}

func runSSH(ctx context.Context, nas NASConfig, remoteCmd string) error {
	args := []string{"ssh"}
	if nas.SSHPort != 0 && nas.SSHPort != 22 {
		args = append(args, "-p", fmt.Sprintf("%d", nas.SSHPort))
	}
	if nas.SSHKey != "" {
		args = append(args, "-i", os.ExpandEnv(nas.SSHKey))
	}
	args = append(args, "-o", "BatchMode=yes", "-o", "ConnectTimeout=10")
	args = append(args, fmt.Sprintf("%s@%s", nas.SSHUser, nas.SSHHost))
	args = append(args, remoteCmd)

	slog.Debug("prune: running SSH", "args", args)
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
