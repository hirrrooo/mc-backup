package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"log/slog"
)

type composeService struct {
	Name string `json:"Name"`
}

func composeFileCandidates() []string {
	return []string{"docker-compose.yml", "compose.yml", "docker-compose.yaml", "compose.yaml"}
}

func isValidServerName(name string) bool {
	if len(name) == 0 || len(name) > 64 {
		return false
	}
	for _, c := range name {
		if c >= 'a' && c <= 'z' {
			continue
		}
		if c >= 'A' && c <= 'Z' {
			continue
		}
		if c >= '0' && c <= '9' {
			continue
		}
		if c == '-' || c == '_' {
			continue
		}
		return false
	}
	return true
}

func fallbackContainerName(serverName string) string {
	return serverName + "-mc-1"
}

func warnLegacyBackupDir(w WatchConfig, serverName string) {
	entries, err := os.ReadDir(w.backupDir(serverName))
	if err != nil || len(entries) == 0 {
		return
	}
	slog.Warn("discovery: legacy backup directory is no longer managed",
		"path", w.backupDir(serverName),
		"server", serverName,
		"message", "migrate snapshots to the desired target or delete them manually")
}

func detectContainerName(serverDir, serverName string) string {
	for _, fname := range composeFileCandidates() {
		composePath := filepath.Join(serverDir, fname)
		if _, err := os.Stat(composePath); os.IsNotExist(err) {
			continue
		}
		cmd := commandRunner.CommandContext(context.Background(), "docker", "compose", "-f", composePath, "ps", "--format", "json")
		out, err := cmd.Output()
		if err != nil {
			slog.Warn("discovery: docker compose ps failed", "file", composePath, "error", err)
			continue
		}
		decoder := json.NewDecoder(strings.NewReader(string(out)))
		for decoder.More() {
			var svc composeService
			if err := decoder.Decode(&svc); err != nil {
				continue
			}
			if svc.Name != "" {
				return svc.Name
			}
		}
	}

	cmd := commandRunner.CommandContext(context.Background(), "docker", "ps", "--filter", fmt.Sprintf("name=^/%s-", serverName), "--format", "{{.Names}}")
	out, err := cmd.Output()
	if err == nil {
		names := strings.Fields(string(out))
		for _, n := range names {
			if strings.HasPrefix(n, serverName+"-") {
				return n
			}
		}
	}

	return fallbackContainerName(serverName)
}

func containerUptime(container string) (time.Duration, error) {
	cmd := commandRunner.CommandContext(context.Background(), "docker", "inspect", "--format", "{{.State.StartedAt}}", container)
	out, err := cmd.Output()
	if err != nil {
		return 0, fmt.Errorf("inspect %s: %w", container, err)
	}
	startedAt := strings.TrimSpace(string(out))
	t, err := time.Parse(time.RFC3339Nano, startedAt)
	if err != nil {
		return 0, fmt.Errorf("parse startedAt %q: %w", startedAt, err)
	}
	return time.Since(t), nil
}

func containerRunning(container string) bool {
	cmd := commandRunner.CommandContext(context.Background(), "docker", "ps", "--filter", fmt.Sprintf("name=^/%s$", container), "--format", "{{.Names}}")
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) == container
}

func discoverServers(watches []WatchConfig, knownServers map[string]ServerConfig) ([]struct {
	Watch  WatchConfig
	Name   string
	Server ServerConfig
}, []struct {
	Name   string
	Server ServerConfig
}) {
	return discoverServersWithWarning(watches, knownServers, nil)
}

func discoverServersWithWarning(watches []WatchConfig, knownServers map[string]ServerConfig, warn func(WatchConfig, string)) ([]struct {
	Watch  WatchConfig
	Name   string
	Server ServerConfig
}, []struct {
	Name   string
	Server ServerConfig
}) {
	var results []struct {
		Watch  WatchConfig
		Name   string
		Server ServerConfig
	}
	var newServers []struct {
		Name   string
		Server ServerConfig
	}
	for _, w := range watches {
		entries, err := os.ReadDir(w.Path)
		if err != nil {
			slog.Warn("discovery: cannot read watch path", "path", w.Path, "error", err)
			continue
		}
		for _, e := range entries {
			if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
				continue
			}
			name := e.Name()
			if !isValidServerName(name) {
				slog.Warn("discovery: skipping invalid server name", "name", name, "path", w.Path)
				continue
			}
			if warn != nil {
				warn(w, name)
			}
			server, exists := knownServers[name]
			if exists && !server.Enabled {
				continue
			}
			if exists {
				results = append(results, struct {
					Watch  WatchConfig
					Name   string
					Server ServerConfig
				}{Watch: w, Name: name, Server: server})
				continue
			}
			containerName := detectContainerName(filepath.Join(w.Path, name), name)
			newServer := ServerConfig{
				Enabled:       false,
				ContainerName: containerName,
			}
			slog.Info("discovery: provisioning new server",
				"name", name,
				"container", containerName,
				"namespace", w.Namespace,
			)
			newServers = append(newServers, struct {
				Name   string
				Server ServerConfig
			}{Name: name, Server: newServer})
			results = append(results, struct {
				Watch  WatchConfig
				Name   string
				Server ServerConfig
			}{Watch: w, Name: name, Server: newServer})
		}
	}
	return results, newServers
}
