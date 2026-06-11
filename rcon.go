package main

import (
	"context"
	"fmt"
	"os/exec"
	"time"

	"log/slog"
)

func rconCommand(container, password, cmd string) []string {
	return []string{
		"docker", "exec",
		"-e", fmt.Sprintf("RCON_PASSWORD=%s", password),
		container,
		"rcon-cli",
		cmd,
	}
}

func runRcon(ctx context.Context, container, password, command string, retries int, retryInterval time.Duration) error {
	args := rconCommand(container, password, command)
	for i := 0; i < retries; i++ {
		cmd := exec.CommandContext(ctx, args[0], args[1:]...)
		out, err := cmd.CombinedOutput()
		if err == nil {
			return nil
		}
		slog.Warn("rcon command failed, retrying",
			"command", command,
			"attempt", i+1,
			"max", retries,
			"output", string(out),
			"error", err,
		)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(retryInterval):
		}
	}
	return fmt.Errorf("rcon %q failed after %d retries", command, retries)
}
