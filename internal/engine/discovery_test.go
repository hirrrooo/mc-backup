package engine

import "testing"

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
		{"hello.world", true},
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
