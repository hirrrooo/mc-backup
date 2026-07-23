package engine

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestComposeFileCandidates(t *testing.T) {
	files := composeFileCandidates()
	foundDocker := false
	foundCompose := false
	for _, f := range files {
		if f == "docker-compose.yml" {
			foundDocker = true
		}
		if f == "compose.yml" {
			foundCompose = true
		}
	}
	if !foundDocker || !foundCompose {
		t.Error("missing expected compose file names")
	}
}

func TestFallbackContainerName(t *testing.T) {
	name := fallbackContainerName("creative")
	if name != "creative-mc-1" {
		t.Errorf("expected creative-mc-1, got %q", name)
	}
}

func TestIsValidServerName(t *testing.T) {
	tests := []struct {
		name  string
		valid bool
	}{
		{"creative", true},
		{"my-server_v2", true},
		{"hello-world", true},
		{"../../etc", false},
		{"hello';rm -rf /", false},
		{"foo bar", false},
		{"", false},
	}
	for _, tt := range tests {
		got := isValidServerName(tt.name)
		if got != tt.valid {
			t.Errorf("isValidServerName(%q) = %v, want %v", tt.name, got, tt.valid)
		}
	}
}

func TestWarnLegacyBackupDir(t *testing.T) {
	tests := []struct {
		name       string
		entries    []string
		wantWarn   bool
		wantPhrase string
	}{
		{name: "missing", wantWarn: false},
		{name: "empty", entries: []string{}, wantWarn: false},
		{name: "nonempty", entries: []string{"20240101-1200"}, wantWarn: true, wantPhrase: "no longer managed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			legacy := filepath.Join(root, "creative", "backups")
			if tt.entries != nil {
				if err := os.MkdirAll(legacy, 0755); err != nil {
					t.Fatal(err)
				}
				for _, entry := range tt.entries {
					if err := os.WriteFile(filepath.Join(legacy, entry), nil, 0600); err != nil {
						t.Fatal(err)
					}
				}
			}
			var logs bytes.Buffer
			old := slog.Default()
			slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
			defer slog.SetDefault(old)
			warnLegacyBackupDir(WatchConfig{Path: root}, "creative")
			if got := logs.String(); (got != "") != tt.wantWarn {
				t.Fatalf("warning output = %q, want warning=%v", got, tt.wantWarn)
			}
			if tt.wantWarn && !bytes.Contains(logs.Bytes(), []byte(tt.wantPhrase)) {
				t.Fatalf("warning output = %q, missing %q", logs.String(), tt.wantPhrase)
			}
		})
	}
}

type mockOutputCommand struct {
	recordingCommand
	out []byte
}

func (c *mockOutputCommand) Output() ([]byte, error) {
	return c.out, c.err
}

func TestDetectContainerNameWithComposeFile(t *testing.T) {
	tmpDir := t.TempDir()
	serverDir := filepath.Join(tmpDir, "creative")
	if err := os.MkdirAll(serverDir, 0755); err != nil {
		t.Fatal(err)
	}
	composeFile := filepath.Join(serverDir, "docker-compose.yml")
	if err := os.WriteFile(composeFile, []byte("services:\n  mc:\n"), 0600); err != nil {
		t.Fatal(err)
	}

	runner := commandRunnerFunc(func(c context.Context, name string, args ...string) command {
		if name == "docker" && len(args) > 0 && args[0] == "compose" {
			return &mockOutputCommand{
				recordingCommand: recordingCommand{name: name, args: args},
				out:              []byte(`{"Name":"custom-creative-container"}`),
			}
		}
		return &recordingCommand{name: name, args: args}
	})

	withCommandRunner(runner, func() {
		got := detectContainerName(serverDir, "creative")
		if got != "custom-creative-container" {
			t.Errorf("detectContainerName = %q, want 'custom-creative-container'", got)
		}
	})
}

func TestDetectContainerNameDockerPSFallback(t *testing.T) {
	tmpDir := t.TempDir()
	serverDir := filepath.Join(tmpDir, "survival")
	if err := os.MkdirAll(serverDir, 0755); err != nil {
		t.Fatal(err)
	}

	runner := commandRunnerFunc(func(c context.Context, name string, args ...string) command {
		if name == "docker" && len(args) > 0 && args[0] == "ps" {
			return &mockOutputCommand{
				recordingCommand: recordingCommand{name: name, args: args},
				out:              []byte("survival-server-mc-1"),
			}
		}
		return &recordingCommand{name: name, args: args}
	})

	withCommandRunner(runner, func() {
		got := detectContainerName(serverDir, "survival")
		if got != "survival-server-mc-1" {
			t.Errorf("detectContainerName = %q, want 'survival-server-mc-1'", got)
		}
	})
}

func TestContainerUptimeAndRunning(t *testing.T) {
	nowStr := time.Now().Add(-10 * time.Minute).Format(time.RFC3339Nano)

	runner := commandRunnerFunc(func(c context.Context, name string, args ...string) command {
		if name == "docker" && len(args) > 1 && args[0] == "inspect" {
			return &mockOutputCommand{
				recordingCommand: recordingCommand{name: name, args: args},
				out:              []byte(nowStr),
			}
		}
		if name == "docker" && len(args) > 1 && args[0] == "ps" {
			return &mockOutputCommand{
				recordingCommand: recordingCommand{name: name, args: args},
				out:              []byte("mc-server"),
			}
		}
		return &recordingCommand{name: name, args: args}
	})

	withCommandRunner(runner, func() {
		uptime, err := containerUptime("mc-server")
		if err != nil {
			t.Fatalf("containerUptime error = %v", err)
		}
		if uptime < 9*time.Minute || uptime > 11*time.Minute {
			t.Errorf("unexpected uptime = %v", uptime)
		}

		running := containerRunning("mc-server")
		if !running {
			t.Error("containerRunning returned false, want true")
		}

		notRunning := containerRunning("other-server")
		if notRunning {
			t.Error("containerRunning returned true for other-server, want false")
		}
	})
}
