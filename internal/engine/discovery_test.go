package engine

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
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
