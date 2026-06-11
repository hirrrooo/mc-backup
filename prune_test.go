package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPruneLocalByCount(t *testing.T) {
	tmp := t.TempDir()
	dirs := []string{"20250611-1000", "20250611-1100", "20250611-1200", "20250611-1300", "20250611-1400"}
	for _, d := range dirs {
		os.Mkdir(filepath.Join(tmp, d), 0755)
	}

	pruneLocalByCount(tmp, "server", 3)

	remaining, _ := os.ReadDir(tmp)
	if len(remaining) != 3 {
		t.Errorf("expected 3 remaining dirs, got %d", len(remaining))
	}
	for _, r := range remaining {
		t.Log("remaining:", r.Name())
	}
}

func TestPruneLocalByCountDisabled(t *testing.T) {
	tmp := t.TempDir()
	os.Mkdir(filepath.Join(tmp, "server-20250611-1000"), 0755)

	pruneLocalByCount(tmp, "server", 0)

	remaining, _ := os.ReadDir(tmp)
	if len(remaining) != 1 {
		t.Errorf("expected 1 remaining dir, got %d", len(remaining))
	}
}

func TestPruneLocalByCountNoMatch(t *testing.T) {
	tmp := t.TempDir()
	os.Mkdir(filepath.Join(tmp, "other-dir"), 0755)

	pruneLocalByCount(tmp, "server", 2)

	remaining, _ := os.ReadDir(tmp)
	if len(remaining) != 1 {
		t.Errorf("expected 1 remaining dir, got %d", len(remaining))
	}
}
