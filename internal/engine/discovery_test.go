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
	err error
}

func (c *mockOutputCommand) Output() ([]byte, error) {
	return c.out, c.err
}

func (c *mockOutputCommand) CombinedOutput() ([]byte, error) {
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
func TestContainerUptimeAndRunningErrors(t *testing.T) {
	// Command error for inspect
	runnerErr := commandRunnerFunc(func(c context.Context, name string, args ...string) command {
		return &mockOutputCommand{
			recordingCommand: recordingCommand{name: name, args: args},
			err:              context.DeadlineExceeded,
		}
	})

	withCommandRunner(runnerErr, func() {
		if _, err := containerUptime("mc-server"); err == nil {
			t.Error("expected containerUptime error when command fails")
		}
		if containerRunning("mc-server") {
			t.Error("expected containerRunning false when command fails")
		}
	})

	// Parse error for invalid timestamp
	runnerParseErr := commandRunnerFunc(func(c context.Context, name string, args ...string) command {
		return &mockOutputCommand{
			recordingCommand: recordingCommand{name: name, args: args},
			out:              []byte("not-a-timestamp"),
		}
	})

	withCommandRunner(runnerParseErr, func() {
		if _, err := containerUptime("mc-server"); err == nil {
			t.Error("expected containerUptime error for invalid timestamp")
		}
	})
}
func TestDiscoverServersAllBranches(t *testing.T) {
	tmp := t.TempDir()
	watchDir := filepath.Join(tmp, "watch")
	_ = os.MkdirAll(filepath.Join(watchDir, "valid_server"), 0755)
	_ = os.MkdirAll(filepath.Join(watchDir, "new_server"), 0755)
	_ = os.MkdirAll(filepath.Join(watchDir, "disabled_server"), 0755)
	_ = os.MkdirAll(filepath.Join(watchDir, "invalid server name!"), 0755)
	_ = os.MkdirAll(filepath.Join(watchDir, ".dot_dir"), 0755)
	_ = os.WriteFile(filepath.Join(watchDir, "regular_file.txt"), []byte("data"), 0600)

	known := map[string]ServerConfig{
		"valid_server":    {Enabled: true},
		"disabled_server": {Enabled: false},
	}

	watches := []WatchConfig{
		{Path: watchDir, Namespace: "mc"},
		{Path: filepath.Join(tmp, "nonexistent_path"), Namespace: "mc"},
	}

	runner := commandRunnerFunc(func(c context.Context, name string, args ...string) command {
		return &mockOutputCommand{
			recordingCommand: recordingCommand{name: name, args: args},
			out:              []byte("new_server-mc-1"),
		}
	})

	withCommandRunner(runner, func() {
		results, newServers := discoverServers(watches, known)

		if len(results) != 2 {
			t.Fatalf("expected 2 discovered results (valid_server & new_server), got %d", len(results))
		}

		names := map[string]bool{}
		for _, r := range results {
			names[r.Name] = true
		}
		if !names["valid_server"] || !names["new_server"] {
			t.Errorf("expected valid_server and new_server in results, got %v", names)
		}
		if names["disabled_server"] || names["invalid server name!"] || names[".dot_dir"] {
			t.Errorf("found unexpected server in results: %v", names)
		}

		if len(newServers) != 1 || newServers[0].Name != "new_server" {
			t.Fatalf("expected 1 new server (new_server), got %v", newServers)
		}
		if newServers[0].Server.ContainerName != "new_server-mc-1" {
			t.Errorf("container name = %q, want new_server-mc-1", newServers[0].Server.ContainerName)
		}
		if newServers[0].Server.Target != "nas" {
			t.Errorf("target = %q, want nas", newServers[0].Server.Target)
		}
	})
}

func TestDetectContainerNameNoComposeMatch(t *testing.T) {
	tmp := t.TempDir()
	serverDir := filepath.Join(tmp, "creative")
	_ = os.MkdirAll(serverDir, 0755)
	_ = os.WriteFile(filepath.Join(serverDir, "docker-compose.yml"), []byte("services:\n  other:\n    image: nginx\n"), 0600)

	runner := commandRunnerFunc(func(c context.Context, name string, args ...string) command {
		return &mockOutputCommand{
			recordingCommand: recordingCommand{name: name, args: args},
			out:              []byte(""),
		}
	})
	withCommandRunner(runner, func() {
		got := detectContainerName(serverDir, "creative")
		if got != "creative-mc-1" {
			t.Errorf("detectContainerName = %q, want creative-mc-1", got)
		}
	})
}
