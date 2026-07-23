package engine

import (
	"context"
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

func TestNASDaysPrunePipelineServerRootProtection(t *testing.T) {
	tmp := t.TempDir()
	destRoot := filepath.Join(tmp, "backups")
	namespace := "minecraft"
	serverName := "20200101-1200"

	destDir := filepath.Join(destRoot, namespace, serverName)
	if err := os.MkdirAll(destDir, 0755); err != nil {
		t.Fatal(err)
	}

	oldTime := time.Now().Add(-30 * 24 * time.Hour)

	oldChild := filepath.Join(destDir, "20200102-1200")
	if err := os.Mkdir(oldChild, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(oldChild, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}

	newChild := filepath.Join(destDir, "20260101-1200")
	if err := os.Mkdir(newChild, 0755); err != nil {
		t.Fatal(err)
	}

	malformedChild := filepath.Join(destDir, "not-a-snapshot")
	if err := os.Mkdir(malformedChild, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(malformedChild, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}

	if err := os.Chtimes(destDir, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}

	cmdStr := pruneNASByDaysCommand(destRoot, namespace, serverName, 7)
	cmd := exec.Command("bash", "-c", cmdStr)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("prune NAS days pipeline failed: %v, output: %s", err, string(output))
	}

	if _, err := os.Stat(destDir); os.IsNotExist(err) {
		t.Fatalf("server directory %s was deleted by day pruning", destDir)
	}
	if _, err := os.Stat(oldChild); !os.IsNotExist(err) {
		t.Errorf("expected old child snapshot %s to be pruned", oldChild)
	}
	if _, err := os.Stat(newChild); err != nil {
		t.Errorf("expected new child snapshot %s to exist, err = %v", newChild, err)
	}
	if _, err := os.Stat(malformedChild); err != nil {
		t.Errorf("expected malformed child directory %s to exist, err = %v", malformedChild, err)
	}
}

func TestPruneNASByDays(t *testing.T) {
	nas := NASConfig{
		SSHUser:  "backup",
		SSHHost:  "nas.local",
		SSHPort:  2222,
		SSHKey:   "/keys/id_ed25519",
		DestRoot: "/volume1/backups",
	}

	t.Run("days <= 0 returns nil without ssh", func(t *testing.T) {
		runner := &recordingRunner{}
		withCommandRunner(runner, func() {
			err := pruneNASByDays(context.Background(), nas, nas.DestRoot, "minecraft", "survival", 0)
			if err != nil {
				t.Fatalf("expected nil error, got %v", err)
			}
		})
		if len(runner.commands) != 0 {
			t.Fatalf("expected 0 commands when days <= 0, got %d", len(runner.commands))
		}
	})

	t.Run("days > 0 executes ssh find command", func(t *testing.T) {
		runner := &recordingRunner{}
		withCommandRunner(runner, func() {
			err := pruneNASByDays(context.Background(), nas, nas.DestRoot, "minecraft", "survival", 7)
			if err != nil {
				t.Fatalf("pruneNASByDays failed: %v", err)
			}
		})
		if len(runner.commands) != 1 {
			t.Fatalf("expected 1 command, got %d", len(runner.commands))
		}
		cmd := runner.commands[0]
		if cmd.name != "ssh" {
			t.Errorf("expected ssh command, got %s", cmd.name)
		}
		cmdLine := strings.Join(cmd.args, " ")
		if !strings.Contains(cmdLine, "-p 2222") || !strings.Contains(cmdLine, "backup@nas.local") {
			t.Errorf("ssh command missing port or user@host: %s", cmdLine)
		}
		if !strings.Contains(cmdLine, "find '/volume1/backups/minecraft/survival'") || !strings.Contains(cmdLine, "-mtime +7") {
			t.Errorf("ssh command missing find or -mtime +7: %s", cmdLine)
		}
	})
}

func TestPruneNASByCount(t *testing.T) {
	nas := NASConfig{
		SSHUser:  "backup",
		SSHHost:  "nas.local",
		SSHPort:  22,
		DestRoot: "/volume1/backups",
	}

	t.Run("count <= 0 returns nil without ssh", func(t *testing.T) {
		runner := &recordingRunner{}
		withCommandRunner(runner, func() {
			err := pruneNASByCount(context.Background(), nas, nas.DestRoot, "minecraft", "survival", 0)
			if err != nil {
				t.Fatalf("expected nil error, got %v", err)
			}
		})
		if len(runner.commands) != 0 {
			t.Fatalf("expected 0 commands when count <= 0, got %d", len(runner.commands))
		}
	})

	t.Run("count > 0 executes ssh ls count command", func(t *testing.T) {
		runner := &recordingRunner{}
		withCommandRunner(runner, func() {
			err := pruneNASByCount(context.Background(), nas, nas.DestRoot, "minecraft", "survival", 5)
			if err != nil {
				t.Fatalf("pruneNASByCount failed: %v", err)
			}
		})
		if len(runner.commands) != 1 {
			t.Fatalf("expected 1 command, got %d", len(runner.commands))
		}
		cmdLine := strings.Join(runner.commands[0].args, " ")
		if !strings.Contains(cmdLine, "tail -n +6") {
			t.Errorf("count command should specify tail -n +6 for count=5, got: %s", cmdLine)
		}
	})
}

func TestPruneLocalByDaysExactBoundary(t *testing.T) {
	tmp := t.TempDir()
	now := time.Date(2025, 6, 20, 12, 0, 0, 0, time.UTC)
	// Cutoff for 7 days is 2025-06-13 12:00:00
	older := filepath.Join(tmp, "20250613-1159")   // Before cutoff -> prune
	exact := filepath.Join(tmp, "20250613-1200")   // Exactly at cutoff -> keep
	newer := filepath.Join(tmp, "20250613-1201")   // After cutoff -> keep
	fileInDir := filepath.Join(tmp, "regular.txt") // Regular file -> skip

	for _, p := range []string{older, exact, newer} {
		if err := os.Mkdir(p, 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(fileInDir, []byte("test"), 0600); err != nil {
		t.Fatal(err)
	}

	pruneLocalByDays(tmp, 7, now)

	if _, err := os.Stat(older); !os.IsNotExist(err) {
		t.Errorf("older snapshot should be pruned, err=%v", err)
	}
	if _, err := os.Stat(exact); err != nil {
		t.Errorf("exact boundary snapshot should be kept, err=%v", err)
	}
	if _, err := os.Stat(newer); err != nil {
		t.Errorf("newer snapshot should be kept, err=%v", err)
	}
	if _, err := os.Stat(fileInDir); err != nil {
		t.Errorf("regular file should be ignored and kept, err=%v", err)
	}
}

func TestPruneLocalByDaysInvalidDirAndReadError(t *testing.T) {
	// Non-existent directory
	pruneLocalByDays(filepath.Join(t.TempDir(), "nonexistent"), 7, time.Now())

	// Directory with invalid timestamp dir
	tmp := t.TempDir()
	invalidTimestamp := filepath.Join(tmp, "20259999-9999") // Invalid month/day
	if err := os.Mkdir(invalidTimestamp, 0755); err != nil {
		t.Fatal(err)
	}
	pruneLocalByDays(tmp, 7, time.Now())
	if _, err := os.Stat(invalidTimestamp); err != nil {
		t.Errorf("invalid timestamp dir should be kept (not parsed as backup), err=%v", err)
	}
}

func TestPruneLocalByCountReadErrorAndEdgeCases(t *testing.T) {
	// Non-existent directory
	pruneLocalByCount(filepath.Join(t.TempDir(), "nonexistent"), 5)

	// Keep <= 0
	tmp := t.TempDir()
	d := filepath.Join(tmp, "20250611-1000")
	if err := os.Mkdir(d, 0755); err != nil {
		t.Fatalf("Mkdir failed: %v", err)
	}
	pruneLocalByCount(tmp, 0)
	if _, err := os.Stat(d); err != nil {
		t.Errorf("snapshot should remain when keep=0, err=%v", err)
	}
}
