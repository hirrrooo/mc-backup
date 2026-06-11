package main

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
