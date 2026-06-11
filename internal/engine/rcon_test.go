package engine

import "testing"

func TestRconCommand(t *testing.T) {
	cmd := rconCommand("mc-server", "hunter2", "save-off")
	if len(cmd) < 4 {
		t.Fatal("cmd too short")
	}
	if cmd[0] != "docker" {
		t.Errorf("expected docker, got %q", cmd[0])
	}
	if cmd[1] != "exec" {
		t.Errorf("expected exec, got %q", cmd[1])
	}

	foundPassword := false
	foundContainer := false
	for _, arg := range cmd {
		if arg == "RCON_PASSWORD=hunter2" {
			foundPassword = true
		}
		if arg == "mc-server" {
			foundContainer = true
		}
	}
	if !foundPassword {
		t.Error("missing RCON_PASSWORD env var in args")
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
