package engine

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type recordingCommand struct {
	name string
	args []string
	env  []string
}

func (c *recordingCommand) Run() error                      { return nil }
func (c *recordingCommand) Output() ([]byte, error)         { return nil, nil }
func (c *recordingCommand) CombinedOutput() ([]byte, error) { return nil, nil }
func (c *recordingCommand) SetStdout(_ io.Writer)           {}
func (c *recordingCommand) SetStderr(_ io.Writer)           {}
func (c *recordingCommand) SetEnv(env []string)             { c.env = append(c.env, env...) }

type recordingRunner struct {
	commands []*recordingCommand
}

func (r *recordingRunner) CommandContext(_ context.Context, name string, args ...string) command {
	c := &recordingCommand{name: name, args: args}
	r.commands = append(r.commands, c)
	return c
}

func TestLocalRsyncArgs(t *testing.T) {
	args := localRsyncArgs("/opt/mc/data", "/backups/mc/creative", "/backups/mc/20250611-1200", []string{"*.jar", "cache"})
	if args[0] != "rsync" {
		t.Errorf("expected rsync, got %q", args[0])
	}
	hasLinkDest := false
	hasTimeout := false
	hasSrc := false
	hasDest := false
	for _, a := range args {
		if strings.HasPrefix(a, "--link-dest=") {
			hasLinkDest = true
		}
		if a == "--timeout=300" {
			hasTimeout = true
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
	for _, a := range args {
		if strings.HasPrefix(a, "--bwlimit=") || a == "-e" || strings.Contains(a, "ssh") {
			t.Errorf("local rsync unexpectedly contains NAS option %q", a)
		}
	}
	if !hasTimeout {
		t.Error("missing --timeout flag")
	}
	if !hasSrc || !hasDest {
		t.Error("missing source or destination")
	}
}

func TestBackupServerRejectsInvalidTargetBeforeCommands(t *testing.T) {
	runner := &recordingRunner{}
	var gotErr error
	withCommandRunner(runner, func() {
		_, _, gotErr = NewBackupEngine(Config{}).BackupServer(
			context.Background(), WatchConfig{Path: "/watch", Namespace: "ns"}, "creative",
			ServerConfig{Target: "invalid"}, "", "", true,
		)
	})
	if gotErr == nil || !strings.Contains(gotErr.Error(), `server "creative"`) {
		t.Fatalf("BackupServer error = %v, want server name", gotErr)
	}
	if len(runner.commands) != 0 {
		t.Fatalf("invalid target ran %d commands, want zero: %#v", len(runner.commands), runner.commands)
	}
}

func TestBackupServerLocalTargetUsesLocalHierarchy(t *testing.T) {
	localRoot := t.TempDir()
	source := t.TempDir()
	runner := &recordingRunner{}
	var rsyncArgs []string
	previousRsyncRunner := rsyncRunner
	rsyncRunner = func(_ context.Context, args []string, _ func(int64, int64)) error {
		rsyncArgs = append([]string(nil), args...)
		return nil
	}
	defer func() { rsyncRunner = previousRsyncRunner }()

	path, usedSSH, err := func() (string, bool, error) {
		var resultPath string
		var resultSSH bool
		var resultErr error
		withCommandRunner(runner, func() {
			resultPath, resultSSH, resultErr = NewBackupEngine(Config{
				Local: LocalConfig{DestRoot: localRoot},
			}).BackupServer(context.Background(), WatchConfig{Namespace: "survival"}, "creative",
				ServerConfig{Target: "local", DataDir: source}, "", "/nas/previous", true)
		})
		return resultPath, resultSSH, resultErr
	}()
	if err != nil {
		t.Fatalf("local BackupServer failed: %v", err)
	}
	if usedSSH {
		t.Fatal("local target reported SSH use")
	}
	if filepath.Dir(path) != filepath.Join(localRoot, "survival", "creative") {
		t.Fatalf("destination = %q, want local hierarchy", path)
	}
	if !isBackupDir(filepath.Base(path)) {
		t.Fatalf("destination %q does not end in a timestamp directory", path)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("local destination was not created: %v", err)
	}
	for _, arg := range rsyncArgs {
		if strings.Contains(arg, "ssh") || strings.HasPrefix(arg, "--bwlimit=") {
			t.Errorf("local rsync unexpectedly contains NAS option %q", arg)
		}
	}
	if len(runner.commands) != 1 || runner.commands[0].name != "sync" {
		t.Fatalf("commands = %#v, want only sync", runner.commands)
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
	hasTimeout := false
	hasKeepalive := false
	for _, a := range args {
		if strings.HasPrefix(a, "--bwlimit=") {
			hasBwLimit = true
		}
		if a == "--timeout=300" {
			hasTimeout = true
		}
		if strings.HasPrefix(a, "-e") || strings.Contains(a, "ssh") {
			hasSSH = true
		}
		if strings.Contains(a, "ServerAliveInterval=15") && strings.Contains(a, "ServerAliveCountMax=3") {
			hasKeepalive = true
		}
	}
	if !hasBwLimit {
		t.Error("missing --bwlimit flag for NAS rsync")
	}
	if !hasTimeout {
		t.Error("missing --timeout flag for NAS rsync")
	}
	if !hasSSH {
		t.Error("missing SSH remote path")
	}
	if !hasKeepalive {
		t.Error("missing SSH keepalive options")
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

func TestIsBackupDirRequiresTimestampFormat(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{"20250611-1200", true},
		{"2025061-1200", false},
		{"20250611_1200", false},
		{"20250611-120", false},
		{"20250611-12000", false},
		{"2025061a-1200", false},
		{"snapshot-1200", false},
	}

	for _, tt := range tests {
		got := isBackupDir(tt.name)
		if got != tt.want {
			t.Errorf("isBackupDir(%q) = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestBackupServerUsesResolvedExcludes(t *testing.T) {
	localRoot := t.TempDir()
	source := t.TempDir()
	runner := &recordingRunner{}
	var rsyncArgs []string
	previousRsyncRunner := rsyncRunner
	rsyncRunner = func(_ context.Context, args []string, _ func(int64, int64)) error {
		rsyncArgs = append([]string(nil), args...)
		return nil
	}
	defer func() { rsyncRunner = previousRsyncRunner }()

	serverExcludes := []string{"custom.dat"}
	cfg := Config{
		Global: GlobalConfig{
			Excludes: &[]string{"global.tmp"},
		},
		Local: LocalConfig{DestRoot: localRoot},
	}

	withCommandRunner(runner, func() {
		NewBackupEngine(cfg).BackupServer(
			context.Background(), WatchConfig{Namespace: "mc"}, "creative",
			ServerConfig{Target: "local", DataDir: source, Excludes: &serverExcludes}, "", "", true,
		)
	})

	hasCustomExclude := false
	hasGlobalExclude := false
	for _, arg := range rsyncArgs {
		if arg == "--exclude=custom.dat" {
			hasCustomExclude = true
		}
		if arg == "--exclude=global.tmp" {
			hasGlobalExclude = true
		}
	}
	if !hasCustomExclude {
		t.Errorf("rsync args missing server custom exclude '--exclude=custom.dat': %v", rsyncArgs)
	}
	if hasGlobalExclude {
		t.Errorf("rsync args unexpectedly contained overridden global exclude '--exclude=global.tmp': %v", rsyncArgs)
	}
}

func TestStreamRsyncProgressHandlesCarriageReturns(t *testing.T) {
	input := bytes.NewBufferString("1,000 10% 1.00MB/s\r2,000 20% 1.00MB/s\nnot progress\r3,000 50% 1.00MB/s")
	var got []struct {
		bytes int64
		total int64
	}

	streamRsyncProgress(input, func(bytesMoved, totalSize int64) {
		got = append(got, struct {
			bytes int64
			total int64
		}{bytesMoved, totalSize})
	})

	want := []struct {
		bytes int64
		total int64
	}{
		{1000, 10000},
		{2000, 10000},
		{3000, 6000},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d progress updates, want %d: %#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("progress update %d = %#v, want %#v", i, got[i], want[i])
		}
	}
}
