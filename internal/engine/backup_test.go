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
		if a == "/opt/mc/data" {
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

func TestParseRsyncProgress(t *testing.T) {
	tests := []struct {
		line          string
		expectedBytes int64
		expectedTotal int64
		ok            bool
	}{
		{"    1,048,576  50%   10.00MB/s    0:00:05 (xfr#5, to-chk=50/100)", 1048576, 2097152, true},
		{"       32768   0%    0.00kB/s    0:00:00 (xfr#1, to-chk=99/100)", 32768, 0, true},
		{"  2,147,483,648 100%   40.00MB/s    0:01:30 (xfr#100, to-chk=0/100)", 2147483648, 2147483648, true},
		{"  1,000,000  33%   10.00MB/s    0:00:01 (xfr#1, to-chk=99/100)", 1000000, 3030303, true},
		{"Sent 1,048,576 bytes  Received 500 bytes  10,240,000 bytes/sec", 0, 0, false},
		{"total size is 2,048,000  speedup is 2.00", 0, 0, false},
		{"", 0, 0, false},
		{"   some random text", 0, 0, false},
	}

	for _, tt := range tests {
		bytes, total, ok := parseRsyncProgress(tt.line)
		if ok != tt.ok || bytes != tt.expectedBytes || total != tt.expectedTotal {
			t.Errorf("parseRsyncProgress(%q) = (%d, %d, %v), want (%d, %d, %v)",
				tt.line, bytes, total, ok, tt.expectedBytes, tt.expectedTotal, tt.ok)
		}
	}
}
