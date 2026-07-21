package engine

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPruneLocalByCount(t *testing.T) {
	tmp := t.TempDir()
	dirs := []string{"20250611-1000", "20250611-1100", "20250611-1200", "20250611-1300", "20250611-1400"}
	for _, d := range dirs {
		os.Mkdir(filepath.Join(tmp, d), 0755)
	}

	pruneLocalByCount(tmp, 3)

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

	pruneLocalByCount(tmp, 0)

	remaining, _ := os.ReadDir(tmp)
	if len(remaining) != 1 {
		t.Errorf("expected 1 remaining dir, got %d", len(remaining))
	}
}

func TestPruneLocalByCountNoMatch(t *testing.T) {
	tmp := t.TempDir()
	os.Mkdir(filepath.Join(tmp, "other-dir"), 0755)

	pruneLocalByCount(tmp, 2)

	remaining, _ := os.ReadDir(tmp)
	if len(remaining) != 1 {
		t.Errorf("expected 1 remaining dir, got %d", len(remaining))
	}
}

func TestPruneLocalByDays(t *testing.T) {
	tmp := t.TempDir()
	location := time.FixedZone("UTC+2", 2*60*60)
	now := time.Date(2025, 6, 20, 12, 0, 0, 0, location)
	old := filepath.Join(tmp, "20250610-1000")
	boundary := filepath.Join(tmp, "20250613-1200")
	recent := filepath.Join(tmp, "20250619-1000")
	nonSnapshot := filepath.Join(tmp, "notes")
	for _, dir := range []string{old, boundary, recent, nonSnapshot} {
		if err := os.Mkdir(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	pruneLocalByDays(tmp, 7, now)

	for _, test := range []struct {
		name string
		want bool
	}{
		{name: old, want: false},
		{name: boundary, want: true},
		{name: recent, want: true},
		{name: nonSnapshot, want: true},
	} {
		if _, err := os.Stat(test.name); (err == nil) != test.want {
			t.Errorf("%s exists = %v, want %v", test.name, err == nil, test.want)
		}
	}
}

func TestPruneLocalByDaysDisabled(t *testing.T) {
	tmp := t.TempDir()
	dir := filepath.Join(tmp, "20250610-1000")
	if err := os.Mkdir(dir, 0755); err != nil {
		t.Fatal(err)
	}

	pruneLocalByDays(tmp, 0, time.Now())

	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("disabled pruning removed snapshot: %v", err)
	}
}

func TestLocalRetentionCombinesDaysAndCount(t *testing.T) {
	localPath := filepath.Join(t.TempDir(), "namespace", "server")
	if err := os.MkdirAll(localPath, 0755); err != nil {
		t.Fatal(err)
	}
	location := time.FixedZone("UTC+2", 2*60*60)
	now := time.Date(2025, 6, 20, 12, 0, 0, 0, location)
	snapshots := []string{
		"20250610-1000",
		"20250618-1000",
		"20250619-1000",
	}
	for _, snapshot := range snapshots {
		dir := filepath.Join(localPath, snapshot)
		if err := os.Mkdir(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}

	pruneLocalByDays(localPath, 7, now)
	pruneLocalByCount(localPath, 1)

	if _, err := os.Stat(filepath.Join(localPath, "20250610-1000")); !os.IsNotExist(err) {
		t.Errorf("old snapshot still exists, err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(localPath, "20250618-1000")); !os.IsNotExist(err) {
		t.Errorf("count-pruned snapshot still exists, err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(localPath, "20250619-1000")); err != nil {
		t.Errorf("newest snapshot missing: %v", err)
	}
}
