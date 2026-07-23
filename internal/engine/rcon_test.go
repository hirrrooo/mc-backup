package engine

import (
	"context"
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
