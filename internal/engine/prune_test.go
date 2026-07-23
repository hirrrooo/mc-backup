package engine

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPruneLocalByCount(t *testing.T) {
	tmp := t.TempDir()
	dirs := []string{"20250611-1000", "20250611-1100", "20250611-1200", "20250611-1300", "20250611-1400"}
	for _, d := range dirs {
		if err := os.Mkdir(filepath.Join(tmp, d), 0755); err != nil {
			t.Fatalf("Mkdir failed: %v", err)
		}
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
	if err := os.Mkdir(filepath.Join(tmp, "server-20250611-1000"), 0755); err != nil {
		t.Fatalf("Mkdir failed: %v", err)
	}

	pruneLocalByCount(tmp, 0)

	remaining, _ := os.ReadDir(tmp)
	if len(remaining) != 1 {
		t.Errorf("expected 1 remaining dir, got %d", len(remaining))
	}
}

func TestPruneLocalByCountNoMatch(t *testing.T) {
	tmp := t.TempDir()
	if err := os.Mkdir(filepath.Join(tmp, "other-dir"), 0755); err != nil {
		t.Fatalf("Mkdir failed: %v", err)
	}

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

func TestNASCountPruneCommandUsesNoRunIfEmpty(t *testing.T) {
	cmd := pruneNASByCountCommand("/dest/root", "minecraft", "survival", 5)
	if !strings.Contains(cmd, "tr '\\n' '\\0'") {
		t.Errorf("expected NUL byte conversion (tr '\\n' '\\0') in remote NAS count prune command, got: %s", cmd)
	}
	if !strings.Contains(cmd, "xargs -0 -r rm -rf --") {
		t.Errorf("expected xargs -0 -r rm -rf -- in remote NAS count prune command, got: %s", cmd)
	}
	if strings.Contains(cmd, "| xargs -r") {
		t.Errorf("found insecure plain whitespace-splitting | xargs -r in command: %s", cmd)
	}
}

func TestNASCountPrunePipelineWithSpaces(t *testing.T) {
	tmp := t.TempDir()
	destRoot := filepath.Join(tmp, "volume 1", "backups")
	namespace := "minecraft world"
	serverName := "survival server"

	destDir := filepath.Join(destRoot, namespace, serverName)
	if err := os.MkdirAll(destDir, 0755); err != nil {
		t.Fatal(err)
	}

	snapshots := []struct {
		name  string
		mtime time.Time
	}{
		{"20260101-1000", time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)},
		{"20260102-1000", time.Date(2026, 1, 2, 10, 0, 0, 0, time.UTC)},
		{"20260103-1000", time.Date(2026, 1, 3, 10, 0, 0, 0, time.UTC)},
		{"20260104-1000", time.Date(2026, 1, 4, 10, 0, 0, 0, time.UTC)},
		{"20260105-1000", time.Date(2026, 1, 5, 10, 0, 0, 0, time.UTC)},
	}
	for _, snap := range snapshots {
		dirPath := filepath.Join(destDir, snap.name)
		if err := os.Mkdir(dirPath, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(dirPath, snap.mtime, snap.mtime); err != nil {
			t.Fatal(err)
		}
	}

	cmdStr := pruneNASByCountCommand(destRoot, namespace, serverName, 3)
	cmd := exec.Command("bash", "-c", cmdStr)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("prune NAS count pipeline failed: %v, output: %s", err, string(output))
	}

	for _, snap := range []string{"20260101-1000", "20260102-1000"} {
		if _, err := os.Stat(filepath.Join(destDir, snap)); !os.IsNotExist(err) {
			t.Errorf("expected snapshot %s to be pruned", snap)
		}
	}
	for _, snap := range []string{"20260103-1000", "20260104-1000", "20260105-1000"} {
		if _, err := os.Stat(filepath.Join(destDir, snap)); err != nil {
			t.Errorf("expected snapshot %s to exist, err = %v", snap, err)
		}
	}

	cmdStrEmpty := pruneNASByCountCommand(destRoot, namespace, serverName, 5)
	cmdEmpty := exec.Command("bash", "-c", cmdStrEmpty)
	outputEmpty, err := cmdEmpty.CombinedOutput()
	if err != nil {
		t.Fatalf("prune NAS count empty pipeline failed: %v, output: %s", err, string(outputEmpty))
	}
	for _, snap := range []string{"20260103-1000", "20260104-1000", "20260105-1000"} {
		if _, err := os.Stat(filepath.Join(destDir, snap)); err != nil {
			t.Errorf("expected snapshot %s to still exist after empty prune, err = %v", snap, err)
		}
	}
}
