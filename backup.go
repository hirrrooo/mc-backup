package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"log/slog"
)

func localRsyncArgs(dataDir, prevBackup, destDir string, excludes []string) []string {
	args := []string{"rsync", "-a"}
	if prevBackup != "" {
		args = append(args, fmt.Sprintf("--link-dest=%s", prevBackup))
	}
	for _, ex := range excludes {
		args = append(args, fmt.Sprintf("--exclude=%s", ex))
	}
	args = append(args, dataDir+"/", destDir+"/")
	return args
}

func nasRsyncArgs(dataDir, prevBackup, destDir string, nas NASConfig, maxMbps float64, excludes []string) []string {
	sshRemote := fmt.Sprintf("%s@%s", nas.SSHUser, nas.SSHHost)
	args := []string{"rsync", "-a"}
	if maxMbps > 0 {
		args = append(args, fmt.Sprintf("--bwlimit=%d", int(maxMbps*1024)))
	}
	if prevBackup != "" {
		args = append(args, fmt.Sprintf("--link-dest=%s", prevBackup))
	}
	for _, ex := range excludes {
		args = append(args, fmt.Sprintf("--exclude=%s", ex))
	}
	sshArgs := []string{"ssh"}
	if nas.SSHPort != 0 && nas.SSHPort != 22 {
		sshArgs = append(sshArgs, "-p", fmt.Sprintf("%d", nas.SSHPort))
	}
	if nas.SSHKey != "" {
		sshArgs = append(sshArgs, "-i", os.ExpandEnv(nas.SSHKey))
	}
	sshArgs = append(sshArgs, "-o", "BatchMode=yes", "-o", "ConnectTimeout=10")
	args = append(args, "-e", strings.Join(sshArgs, " "))
	args = append(args, dataDir+"/", fmt.Sprintf("%s:%s/", sshRemote, destDir))
	return args
}

func runRsync(ctx context.Context, args []string) error {
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	slog.Debug("running rsync", "args", args)
	return cmd.Run()
}
