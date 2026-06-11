package main

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
