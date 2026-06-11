package engine

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
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

func rconOutput(ctx context.Context, container, password, command string) (string, error) {
	args := rconCommand(container, password, command)
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func countPlayers(output string) int {
	i := strings.Index(output, "There are ")
	if i < 0 {
		return -1
	}
	rest := output[i+len("There are "):]
	j := strings.Index(rest, " of a max of ")
	if j < 0 {
		return -1
	}
	countStr := rest[:j]
	n, err := strconv.Atoi(strings.TrimSpace(countStr))
	if err != nil {
		return -1
	}
	return n
}
