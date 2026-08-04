package engine

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func rconCommand(container, cmd string) []string {
	return []string{
		"docker", "exec",
		"-e", "RCON_PASSWORD",
		container,
		"rcon-cli",
		cmd,
	}
}

func runRcon(ctx context.Context, container, password, command string, retries int, retryInterval time.Duration) error {
	args := rconCommand(container, command)
	for i := 0; i < retries; i++ {
		cmd := commandRunner.CommandContext(ctx, args[0], args[1:]...)
		cmd.SetEnv([]string{fmt.Sprintf("RCON_PASSWORD=%s", password)})
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
	args := rconCommand(container, command)
	cmd := commandRunner.CommandContext(ctx, args[0], args[1:]...)
	cmd.SetEnv([]string{fmt.Sprintf("RCON_PASSWORD=%s", password)})
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

func readServerPropertiesPassword(dataDir string) string {
	filePath := filepath.Join(dataDir, "server.properties")
	file, err := os.Open(filePath)
	if err != nil {
		return ""
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "!") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 && strings.TrimSpace(parts[0]) == "rcon.password" {
			return strings.TrimSpace(parts[1])
		}
	}
	return ""
}

func resolveRconPassword(s ServerConfig, w WatchConfig, serverName string) string {
	if s.RconPassword != "" {
		return s.RconPassword
	}
	dataDir := s.DataDir
	if dataDir == "" {
		dataDir = filepath.Join(w.Path, serverName, "mc-data")
	}
	return readServerPropertiesPassword(dataDir)
}
