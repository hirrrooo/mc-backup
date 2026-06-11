package engine

import (
	"strings"
	"testing"
)

func TestLocalRsyncArgs(t *testing.T) {
	args := localRsyncArgs("/opt/mc/data", "/backups/mc/creative", "/backups/mc/20250611-1200", []string{"*.jar", "cache"})
	if args[0] != "rsync" {
		t.Errorf("expected rsync, got %q", args[0])
	}
	hasLinkDest := false
	hasSrc := false
	hasDest := false
	for _, a := range args {
		if strings.HasPrefix(a, "--link-dest=") {
			hasLinkDest = true
		}
		if strings.HasSuffix(a, "/opt/mc/data/") {
			hasSrc = true
		}
		if strings.HasSuffix(a, "/backups/mc/20250611-1200/") {
			hasDest = true
		}
	}
	if !hasLinkDest {
		t.Error("missing --link-dest flag")
	}
	if !hasSrc || !hasDest {
		t.Error("missing source or destination")
	}
}

func TestNASRsyncArgs(t *testing.T) {
	nas := NASConfig{SSHUser: "backup", SSHHost: "nas.local", SSHPort: 22, SSHKey: "~/.ssh/id_ed25519"}
	args := nasRsyncArgs("/opt/mc/data", "/backups/mc/creative", "/backups/mc/20250611-1200", nas, 40.0, []string{"*.jar"})
	if args[0] != "rsync" {
		t.Errorf("expected rsync, got %q", args[0])
	}
	hasBwLimit := false
	hasSSH := false
	for _, a := range args {
		if strings.HasPrefix(a, "--bwlimit=") {
			hasBwLimit = true
		}
		if strings.HasPrefix(a, "-e") || strings.Contains(a, "ssh") {
			hasSSH = true
		}
	}
	if !hasBwLimit {
		t.Error("missing --bwlimit flag for NAS rsync")
	}
	if !hasSSH {
		t.Error("missing SSH remote path")
	}
}

func TestNoLinkDestFirstRun(t *testing.T) {
	args := localRsyncArgs("/data", "", "/backups/local", nil)
	for _, a := range args {
		if strings.HasPrefix(a, "--link-dest=") {
			t.Error("--link-dest should not be present when prevBackup is empty")
		}
	}
}
