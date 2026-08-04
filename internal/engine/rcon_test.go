package engine

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRconCommand(t *testing.T) {
	cmd := rconCommand("mc-server", "save-off")
	if len(cmd) < 4 {
		t.Fatal("cmd too short")
	}
	if cmd[0] != "docker" {
		t.Errorf("expected docker, got %q", cmd[0])
	}
	if cmd[1] != "exec" {
		t.Errorf("expected exec, got %q", cmd[1])
	}

	foundFlag := false
	foundVarName := false
	foundContainer := false
	for i, arg := range cmd {
		if arg == "-e" {
			foundFlag = true
			if i+1 < len(cmd) && cmd[i+1] == "RCON_PASSWORD" {
				foundVarName = true
			}
		}
		if arg == "mc-server" {
			foundContainer = true
		}
		if strings.Contains(arg, "hunter2") {
			t.Errorf("secret password found in docker argv: %q", arg)
		}
	}
	if !foundFlag || !foundVarName {
		t.Error("missing '-e RCON_PASSWORD' in docker args")
	}
	if !foundContainer {
		t.Error("missing container name in args")
	}

	if cmd[len(cmd)-1] != "save-off" {
		t.Errorf("expected save-off, got %q", cmd[len(cmd)-1])
	}
	if cmd[len(cmd)-2] != "rcon-cli" {
		t.Errorf("expected rcon-cli, got %q", cmd[len(cmd)-2])
	}
}

func TestRconCommandSecurity(t *testing.T) {
	runner := &recordingRunner{}

	withCommandRunner(runner, func() {
		_ = runRcon(context.Background(), "mc-server", "secretPass123", "save-off", 1, 0)
		_, _ = rconOutput(context.Background(), "mc-server", "secretPass123", "list")
	})

	if len(runner.commands) != 2 {
		t.Fatalf("expected 2 recorded commands, got %d", len(runner.commands))
	}

	for i, cmd := range runner.commands {
		for _, arg := range cmd.args {
			if strings.Contains(arg, "secretPass123") {
				t.Fatalf("cmd %d: secret password found in process arguments: %v", i, cmd.args)
			}
		}

		foundEnvSecret := false
		for _, env := range cmd.env {
			if env == "RCON_PASSWORD=secretPass123" {
				foundEnvSecret = true
			}
		}
		if !foundEnvSecret {
			t.Errorf("cmd %d: expected RCON_PASSWORD=secretPass123 in child environment, got: %v", i, cmd.env)
		}
	}
}

func TestCountPlayers(t *testing.T) {
	tests := []struct {
		output string
		want   int
	}{
		{"There are 0 of a max of 20 players online:", 0},
		{"There are 1 of a max of 20 players online: player1", 1},
		{"There are 3 of a max of 20 players online: a, b, c", 3},
		{"There are 12 of a max of 100 players online: many, players, here", 12},
		{"no match here", -1},
		{"", -1},
	}
	for _, tt := range tests {
		got := countPlayers(tt.output)
		if got != tt.want {
			t.Errorf("countPlayers(%q) = %d, want %d", tt.output, got, tt.want)
		}
	}
}

func TestCountPlayersMalformed(t *testing.T) {
	tests := []struct {
		output string
		want   int
	}{
		{"There are  of a max of 20", -1},
		{"There are invalid of a max of 20", -1},
		{"There are 5 players online", -1}, // missing " of a max of "
	}
	for _, tt := range tests {
		if got := countPlayers(tt.output); got != tt.want {
			t.Errorf("countPlayers(%q) = %d, want %d", tt.output, got, tt.want)
		}
	}
}

func TestRunRconRetriesFailure(t *testing.T) {
	attempts := 0
	runner := commandRunnerFunc(func(c context.Context, name string, args ...string) command {
		attempts++
		return &recordingCommand{name: name, args: args, err: errors.New("rcon connection refused")}
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately so sleep won't happen

	withCommandRunner(runner, func() {
		err := runRcon(ctx, "container", "pass", "list", 3, 0)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})

	if attempts != 1 {
		t.Fatalf("expected 1 attempt before context cancel return, got %d", attempts)
	}
}

func TestReadServerPropertiesPassword(t *testing.T) {
	tmpDir := t.TempDir()

	// Missing file returns empty string
	if got := readServerPropertiesPassword(tmpDir); got != "" {
		t.Errorf("expected empty string for missing file, got %q", got)
	}

	// File with missing rcon.password key returns empty string
	noKeyContent := "# Minecraft server properties\nserver-port=25565\nenable-rcon=true\n"
	if err := os.WriteFile(filepath.Join(tmpDir, "server.properties"), []byte(noKeyContent), 0644); err != nil {
		t.Fatalf("failed to write server.properties: %v", err)
	}
	if got := readServerPropertiesPassword(tmpDir); got != "" {
		t.Errorf("expected empty string for missing rcon.password key, got %q", got)
	}

	// File with comments, spaces, values with '=' signs, and rcon.password
	validContent := `
# Minecraft server properties
! Java properties comment
   
server-port=25565
  rcon.password = my=secret=pass  
enable-rcon=true
`
	if err := os.WriteFile(filepath.Join(tmpDir, "server.properties"), []byte(validContent), 0644); err != nil {
		t.Fatalf("failed to write server.properties: %v", err)
	}
	if got := readServerPropertiesPassword(tmpDir); got != "my=secret=pass" {
		t.Errorf("expected %q, got %q", "my=secret=pass", got)
	}
}

func TestResolveRconPassword(t *testing.T) {
	tmpDir := t.TempDir()
	watchDir := filepath.Join(tmpDir, "watch")
	serverName := "myserver"
	defaultDataDir := filepath.Join(watchDir, serverName, "mc-data")
	if err := os.MkdirAll(defaultDataDir, 0755); err != nil {
		t.Fatalf("failed to create default data dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(defaultDataDir, "server.properties"), []byte("rcon.password=filepass\n"), 0644); err != nil {
		t.Fatalf("failed to write server.properties: %v", err)
	}

	customDataDir := filepath.Join(tmpDir, "custom-data")
	if err := os.MkdirAll(customDataDir, 0755); err != nil {
		t.Fatalf("failed to create custom data dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(customDataDir, "server.properties"), []byte("rcon.password=custompass\n"), 0644); err != nil {
		t.Fatalf("failed to write server.properties: %v", err)
	}

	w := WatchConfig{Path: watchDir}

	// Case 1: Explicit config password overrides server.properties
	sExplicit := ServerConfig{RconPassword: "configpass", DataDir: defaultDataDir}
	if got := resolveRconPassword(sExplicit, w, serverName); got != "configpass" {
		t.Errorf("expected explicit config password to override, got %q", got)
	}

	// Case 2: Empty config falls back to server.properties in default dataDir
	sFallback := ServerConfig{}
	if got := resolveRconPassword(sFallback, w, serverName); got != "filepass" {
		t.Errorf("expected fallback to default dataDir server.properties, got %q", got)
	}

	// Case 3: Custom DataDir is respected when resolving server.properties
	sCustomDir := ServerConfig{DataDir: customDataDir}
	if got := resolveRconPassword(sCustomDir, w, serverName); got != "custompass" {
		t.Errorf("expected custom DataDir server.properties password, got %q", got)
	}
}
