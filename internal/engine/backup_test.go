package engine

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type recordingCommand struct {
	name string
	args []string
	env  []string
	err  error
}
type errTestReader struct{}

func (errTestReader) Read(p []byte) (int, error) {
	return 0, errors.New("read error")
}

func (c *recordingCommand) Run() error                      { return c.err }
func (c *recordingCommand) Output() ([]byte, error)         { return nil, nil }
func (c *recordingCommand) CombinedOutput() ([]byte, error) { return nil, c.err }
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
		_, gotErr = NewBackupEngine(Config{}).BackupServer(
			context.Background(), WatchConfig{Path: "/watch", Namespace: "ns"}, "creative",
			ServerConfig{Target: "invalid"}, "", "", "", true,
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

	res, err := func() (backupResult, error) {
		var result backupResult
		var resultErr error
		withCommandRunner(runner, func() {
			result, resultErr = NewBackupEngine(Config{
				Local: LocalConfig{DestRoot: localRoot},
			}).BackupServer(context.Background(), WatchConfig{Namespace: "survival"}, "creative",
				ServerConfig{Target: "local", DataDir: source}, "", "", "/nas/previous", true)
		})
		return result, resultErr
	}()
	if err != nil {
		t.Fatalf("local BackupServer failed: %v", err)
	}
	if res.offloadPending {
		t.Fatal("direct local target reported offloadPending")
	}
	path := res.destination
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
func TestFolderBlacklistRsyncArgs(t *testing.T) {
	excludes := []string{"*.jar", "cache", "logs", "*.tmp", "bluemap/*", "distant horizons/*", "plugins/bluemap"}

	// Local rsync args
	localArgs := localRsyncArgs("/data/mc/paper", "/backups/prev", "/backups/curr", excludes)
	for _, ex := range excludes {
		expectedFlag := "--exclude=" + ex
		found := false
		for _, arg := range localArgs {
			if arg == expectedFlag {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("localRsyncArgs missing flag %q; args = %v", expectedFlag, localArgs)
		}
	}

	// NAS rsync args
	nas := NASConfig{SSHUser: "backup", SSHHost: "nas.local", SSHPort: 22}
	nasArgs := nasRsyncArgs("/data/mc/paper", "/backups/prev", "/backups/curr", nas, 10.0, excludes)
	for _, ex := range excludes {
		expectedFlag := "--exclude=" + ex
		found := false
		for _, arg := range nasArgs {
			if arg == expectedFlag {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("nasRsyncArgs missing flag %q; args = %v", expectedFlag, nasArgs)
		}
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
		{"20250611-1200.inprogress", false},
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
		_, _ = NewBackupEngine(cfg).BackupServer(
			context.Background(), WatchConfig{Namespace: "mc"}, "creative",
			ServerConfig{Target: "local", DataDir: source, Excludes: &serverExcludes}, "", "", "", true,
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

func TestBackupServerSyncHonorsCallerContext(t *testing.T) {
	type ctxKey struct{}
	ctx := context.WithValue(context.Background(), ctxKey{}, "test-value")

	localRoot := t.TempDir()
	source := t.TempDir()

	var capturedCtx context.Context
	var syncCalled bool

	runner := commandRunnerFunc(func(c context.Context, name string, args ...string) command {
		if name == "sync" {
			syncCalled = true
			capturedCtx = c
		}
		return &recordingCommand{name: name, args: args}
	})

	previousRsyncRunner := rsyncRunner
	rsyncRunner = func(_ context.Context, _ []string, _ func(int64, int64)) error {
		return nil
	}
	defer func() { rsyncRunner = previousRsyncRunner }()

	withCommandRunner(runner, func() {
		_, err := NewBackupEngine(Config{
			Local: LocalConfig{DestRoot: localRoot},
		}).BackupServer(ctx, WatchConfig{Namespace: "mc"}, "survival",
			ServerConfig{Target: "local", DataDir: source}, "", "", "", true)
		if err != nil {
			t.Fatalf("BackupServer failed: %v", err)
		}
	})

	if !syncCalled {
		t.Fatal("sync was not called")
	}
	if capturedCtx == nil {
		t.Fatal("sync received nil context")
	}
	if capturedCtx.Value(ctxKey{}) != "test-value" {
		t.Errorf("sync context value = %v, want 'test-value'", capturedCtx.Value(ctxKey{}))
	}
}

func TestBackupServerSyncFailureEmitsWarningAndContinues(t *testing.T) {
	localRoot := t.TempDir()
	source := t.TempDir()

	syncErr := errors.New("disk sync timeout")
	runner := commandRunnerFunc(func(ctx context.Context, name string, args ...string) command {
		if name == "sync" {
			return &recordingCommand{name: name, args: args, err: syncErr}
		}
		return &recordingCommand{name: name, args: args}
	})

	previousRsyncRunner := rsyncRunner
	rsyncRunner = func(_ context.Context, _ []string, _ func(int64, int64)) error {
		return nil
	}
	defer func() { rsyncRunner = previousRsyncRunner }()

	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, nil))
	previousLogger := slog.Default()
	slog.SetDefault(logger)
	defer slog.SetDefault(previousLogger)

	withCommandRunner(runner, func() {
		res, err := NewBackupEngine(Config{
			Local: LocalConfig{DestRoot: localRoot},
		}).BackupServer(context.Background(), WatchConfig{Namespace: "mc"}, "survival",
			ServerConfig{Target: "local", DataDir: source}, "", "", "", true)
		destPath := res.destination
		if err != nil {
			t.Fatalf("BackupServer failed when sync failed: %v", err)
		}
		if destPath == "" {
			t.Fatal("expected non-empty destPath on sync error")
		}
	})

	logStr := logBuf.String()
	if !strings.Contains(logStr, "LEVEL=WARN") && !strings.Contains(logStr, "level=WARN") {
		t.Errorf("expected WARN level log in output: %s", logStr)
	}
	if !strings.Contains(logStr, "disk sync timeout") {
		t.Errorf("expected log output to contain sync error message 'disk sync timeout': %s", logStr)
	}
}

func TestBackupServerDeferredSaveOnRunsWhenFlushFails(t *testing.T) {
	localRoot := t.TempDir()
	source := t.TempDir()

	ctx, cancel := context.WithCancel(context.Background())

	var commands []string
	runner := commandRunnerFunc(func(c context.Context, name string, args ...string) command {
		cmdStr := name + " " + strings.Join(args, " ")
		commands = append(commands, cmdStr)
		rec := &recordingCommand{name: name, args: args}
		if strings.Contains(cmdStr, "save-all flush") {
			rec.err = errors.New("flush failed")
			cancel() // cancel context so runRcon fails fast
		}
		return rec
	})

	previousRsyncRunner := rsyncRunner
	rsyncRunner = func(_ context.Context, _ []string, _ func(int64, int64)) error {
		return nil
	}
	defer func() { rsyncRunner = previousRsyncRunner }()

	withCommandRunner(runner, func() {
		engine := NewBackupEngine(Config{Local: LocalConfig{DestRoot: localRoot}})
		_, err := engine.BackupServer(
			ctx, WatchConfig{Namespace: "survival"}, "creative",
			ServerConfig{Target: "local", DataDir: source}, "", "", "", false,
		)
		if err == nil {
			t.Fatal("expected error on flush failure, got nil")
		}
		if !strings.Contains(err.Error(), "save-all flush") {
			t.Fatalf("expected error containing 'save-all flush', got %v", err)
		}
	})

	if len(commands) < 3 {
		t.Fatalf("expected at least 3 commands (save-off, save-all flush, save-on), got %d: %v", len(commands), commands)
	}
	hasSaveOff := false
	hasFlush := false
	hasSaveOn := false
	for _, cmd := range commands {
		if strings.Contains(cmd, "save-off") {
			hasSaveOff = true
		}
		if strings.Contains(cmd, "save-all flush") {
			hasFlush = true
		}
		if strings.Contains(cmd, "save-on") {
			hasSaveOn = true
		}
	}
	if !hasSaveOff || !hasFlush || !hasSaveOn {
		t.Fatalf("commands missing required sequence, got: %v", commands)
	}
}

func TestBackupServerDeferredSaveOnRunsWhenRsyncFails(t *testing.T) {
	localRoot := t.TempDir()
	source := t.TempDir()

	var commands []string
	runner := commandRunnerFunc(func(c context.Context, name string, args ...string) command {
		cmdStr := name + " " + strings.Join(args, " ")
		commands = append(commands, cmdStr)
		return &recordingCommand{name: name, args: args}
	})

	previousRsyncRunner := rsyncRunner
	rsyncRunner = func(_ context.Context, _ []string, _ func(int64, int64)) error {
		return errors.New("rsync connection lost")
	}
	defer func() { rsyncRunner = previousRsyncRunner }()

	withCommandRunner(runner, func() {
		engine := NewBackupEngine(Config{Local: LocalConfig{DestRoot: localRoot}})
		_, err := engine.BackupServer(
			context.Background(), WatchConfig{Namespace: "survival"}, "creative",
			ServerConfig{Target: "local", DataDir: source}, "", "", "", false,
		)
		if err == nil {
			t.Fatal("expected error on rsync failure, got nil")
		}
		if !strings.Contains(err.Error(), "rsync connection lost") {
			t.Fatalf("expected error containing 'rsync connection lost', got %v", err)
		}
	})

	hasSaveOn := false
	for _, cmd := range commands {
		if strings.Contains(cmd, "save-on") {
			hasSaveOn = true
		}
	}
	if !hasSaveOn {
		t.Fatalf("expected deferred save-on to execute after rsync failure, commands: %v", commands)
	}
}

func TestBackupServerOnlineHappyPathCommandOrdering(t *testing.T) {
	localRoot := t.TempDir()
	source := t.TempDir()

	var commands []string
	runner := commandRunnerFunc(func(c context.Context, name string, args ...string) command {
		cmdStr := name + " " + strings.Join(args, " ")
		commands = append(commands, cmdStr)
		return &recordingCommand{name: name, args: args}
	})

	rsyncCalled := false
	previousRsyncRunner := rsyncRunner
	rsyncRunner = func(_ context.Context, _ []string, _ func(int64, int64)) error {
		rsyncCalled = true
		return nil
	}
	defer func() { rsyncRunner = previousRsyncRunner }()

	withCommandRunner(runner, func() {
		engine := NewBackupEngine(Config{Local: LocalConfig{DestRoot: localRoot}})
		res, err := engine.BackupServer(
			context.Background(), WatchConfig{Namespace: "survival"}, "creative",
			ServerConfig{Target: "local", DataDir: source}, "", "", "", false,
		)
		if err != nil {
			t.Fatalf("BackupServer failed: %v", err)
		}
		if res.target == "nas" {
			t.Error("expected res.target != nas for local target")
		}
		if res.destination == "" {
			t.Error("expected non-empty destination")
		}
	})

	if !rsyncCalled {
		t.Error("expected rsyncRunner to be called")
	}

	// Verify order: save-off -> save-all flush -> sync -> save-on
	if len(commands) < 4 {
		t.Fatalf("expected at least 4 commands, got %d: %v", len(commands), commands)
	}
	if !strings.Contains(commands[0], "save-off") {
		t.Errorf("command 0 = %q, want save-off", commands[0])
	}
	if !strings.Contains(commands[1], "save-all flush") {
		t.Errorf("command 1 = %q, want save-all flush", commands[1])
	}
	if commands[2] != "sync " && commands[2] != "sync" {
		t.Errorf("command 2 = %q, want sync", commands[2])
	}
	if !strings.Contains(commands[3], "save-on") {
		t.Errorf("command 3 = %q, want save-on", commands[3])
	}
}

func TestBackupServerNASReadyAndEnsureDir(t *testing.T) {
	nas := NASConfig{
		SSHUser:  "backup",
		SSHHost:  "nas.local",
		SSHPort:  2222,
		DestRoot: "/volume1/backups",
	}

	t.Run("checkNASReady sentinel present", func(t *testing.T) {
		var executed []string
		runner := commandRunnerFunc(func(c context.Context, name string, args ...string) command {
			executed = append(executed, name+" "+strings.Join(args, " "))
			return &recordingCommand{name: name, args: args}
		})
		withCommandRunner(runner, func() {
			err := checkNASReady(context.Background(), nas)
			if err != nil {
				t.Fatalf("checkNASReady failed: %v", err)
			}
		})
		if len(executed) != 1 || !strings.Contains(executed[0], "test -f") {
			t.Fatalf("unexpected executed commands: %v", executed)
		}
	})

	t.Run("checkNASReady sentinel missing error", func(t *testing.T) {
		runner := commandRunnerFunc(func(c context.Context, name string, args ...string) command {
			return &recordingCommand{name: name, args: args, err: errors.New("file not found")}
		})
		withCommandRunner(runner, func() {
			err := checkNASReady(context.Background(), nas)
			if err == nil || !strings.Contains(err.Error(), "NAS sentinel") {
				t.Fatalf("expected NAS sentinel error, got %v", err)
			}
		})
	})

	t.Run("ensureNASDir runs mkdir -p", func(t *testing.T) {
		var executed []string
		runner := commandRunnerFunc(func(c context.Context, name string, args ...string) command {
			executed = append(executed, name+" "+strings.Join(args, " "))
			return &recordingCommand{name: name, args: args}
		})
		withCommandRunner(runner, func() {
			err := ensureNASDir(context.Background(), nas, "/volume1/backups/mc/survival")
			if err != nil {
				t.Fatalf("ensureNASDir failed: %v", err)
			}
		})
		if len(executed) != 1 || !strings.Contains(executed[0], "mkdir -p") {
			t.Fatalf("unexpected executed commands: %v", executed)
		}
	})
}

func TestRunRsyncExecution(t *testing.T) {
	ctx := context.Background()
	// Test runRsync without progress using standard true command
	err := runRsync(ctx, []string{"true"}, nil)
	if err != nil {
		t.Fatalf("runRsync failed: %v", err)
	}

	// Test runRsync with progress callback using a dummy script that ignores flags
	script := filepath.Join(t.TempDir(), "dummy-rsync.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho \"    1,048,576  50%   10.00MB/s    0:00:05\"\n"), 0600); err != nil {
		t.Fatalf("failed to write dummy script: %v", err)
	}
	if err := os.Chmod(script, 0700); err != nil {
		t.Fatalf("failed to chmod dummy script: %v", err)
	}

	progressCalled := false
	err = runRsync(ctx, []string{script}, func(b, tot int64) {
		progressCalled = true
	})
	if err != nil {
		t.Fatalf("runRsync with progress failed: %v", err)
	}
	if !progressCalled {
		t.Error("expected progress callback to be invoked")
	}
}
func TestRunRsyncErrorAndProgressEdgeCases(t *testing.T) {
	ctx := context.Background()
	if err := runRsync(ctx, []string{"false"}, nil); err == nil {
		t.Error("expected runRsync error when command exits non-zero")
	}

	// parseRsyncProgress invalid lines
	b, tot, ok := parseRsyncProgress("invalid line")
	if ok || b != 0 || tot != 0 {
		t.Errorf("parseRsyncProgress invalid line = (%d, %d, %v), want (0, 0, false)", b, tot, ok)
	}
}
func TestBackupServerAllBranches(t *testing.T) {
	tmp := t.TempDir()
	oldInterval := rconRetryInterval
	rconRetryInterval = 1 * time.Millisecond
	t.Cleanup(func() { rconRetryInterval = oldInterval })
	watchDir := filepath.Join(tmp, "watch")
	_ = os.MkdirAll(filepath.Join(watchDir, "s1", "mc-data"), 0755)

	oldRsync := rsyncRunner
	rsyncRunner = func(ctx context.Context, args []string, progress func(int64, int64)) error {
		if progress != nil {
			progress(100, 200)
		}
		return nil
	}
	t.Cleanup(func() { rsyncRunner = oldRsync })

	cfg := Config{
		Local: LocalConfig{DestRoot: filepath.Join(tmp, "local")},
		NAS: NASConfig{
			SSHUser:  "user",
			SSHHost:  "nas.local",
			DestRoot: "/volume1/backups",
		},
		Watch: []WatchConfig{{Path: watchDir, Namespace: "mc"}},
		Servers: map[string]ServerConfig{
			"s1": {Enabled: true, Target: "local"},
			"s2": {Enabled: true, Target: "nas"},
		},
	}

	engine := NewBackupEngine(cfg)

	// Offline local backup success
	resLocal, err := engine.BackupServer(context.Background(), cfg.Watch[0], "s1", cfg.Servers["s1"], "", "", "", true)
	if err != nil || resLocal.destination == "" || resLocal.target == "nas" {
		t.Errorf("offline local backup failed: res=%+v, err=%v", resLocal, err)
	}

	// Online local backup success with mock RCON
	runnerSuccess := commandRunnerFunc(func(c context.Context, name string, args ...string) command {
		return &mockOutputCommand{
			recordingCommand: recordingCommand{name: name, args: args},
			out:              []byte("Success"),
		}
	})
	withCommandRunner(runnerSuccess, func() {
		resLocal, err := engine.BackupServer(context.Background(), cfg.Watch[0], "s1", cfg.Servers["s1"], "", "", "", false)
		if err != nil || resLocal.destination == "" || resLocal.target == "nas" {
			t.Errorf("online local backup failed: res=%+v, err=%v", resLocal, err)
		}
	})

	// Online local backup save-off error
	runnerSaveOffErr := commandRunnerFunc(func(c context.Context, name string, args ...string) command {
		return &mockOutputCommand{
			recordingCommand: recordingCommand{name: name, args: args},
			err:              errors.New("save-off failed"),
		}
	})
	withCommandRunner(runnerSaveOffErr, func() {
		if _, err := engine.BackupServer(context.Background(), cfg.Watch[0], "s1", cfg.Servers["s1"], "", "", "", false); err == nil || !strings.Contains(err.Error(), "save-off") {
			t.Errorf("expected save-off error, got %v", err)
		}
	})

	// Online local backup save-all flush error
	runnerFlushErr := commandRunnerFunc(func(c context.Context, name string, args ...string) command {
		for _, a := range args {
			if a == "save-all flush" {
				return &mockOutputCommand{
					recordingCommand: recordingCommand{name: name, args: args},
					err:              errors.New("flush failed"),
				}
			}
		}
		return &mockOutputCommand{
			recordingCommand: recordingCommand{name: name, args: args},
			out:              []byte("Success"),
		}
	})
	withCommandRunner(runnerFlushErr, func() {
		if _, err := engine.BackupServer(context.Background(), cfg.Watch[0], "s1", cfg.Servers["s1"], "", "", "", false); err == nil || !strings.Contains(err.Error(), "flush") {
			t.Errorf("expected flush error, got %v", err)
		}
	})
	// Online local backup rsync error
	rsyncRunner = func(ctx context.Context, args []string, progress func(int64, int64)) error {
		return errors.New("rsync failed")
	}
	withCommandRunner(runnerSuccess, func() {
		if _, err := engine.BackupServer(context.Background(), cfg.Watch[0], "s1", cfg.Servers["s1"], "", "", "", false); err == nil || !strings.Contains(err.Error(), "rsync") {
			t.Errorf("expected rsync error, got %v", err)
		}
	})
	// Local mkdir error
	parentFile := filepath.Join(tmp, "parent_file")
	_ = os.WriteFile(parentFile, []byte("file"), 0600)
	badCfg := cfg
	badCfg.Local.DestRoot = parentFile
	badEngine := NewBackupEngine(badCfg)
	if _, err = badEngine.BackupServer(context.Background(), badCfg.Watch[0], "s1", badCfg.Servers["s1"], "", "", "", true); err == nil || !strings.Contains(err.Error(), "local mkdir") {
		t.Errorf("expected local mkdir error, got %v", err)
	}

	// Save-on failure (backup succeeded)
	runnerSaveOnErr := commandRunnerFunc(func(c context.Context, name string, args ...string) command {
		for _, a := range args {
			if a == "save-on" {
				return &mockOutputCommand{
					recordingCommand: recordingCommand{name: name, args: args},
					err:              errors.New("save-on failed"),
				}
			}
		}
		return &mockOutputCommand{
			recordingCommand: recordingCommand{name: name, args: args},
			out:              []byte("Success"),
		}
	})
	rsyncRunner = func(ctx context.Context, args []string, progress func(int64, int64)) error { return nil }
	withCommandRunner(runnerSaveOnErr, func() {
		if _, err := engine.BackupServer(context.Background(), cfg.Watch[0], "s1", cfg.Servers["s1"], "", "", "", false); err == nil || !strings.Contains(err.Error(), "save-on") {
			t.Errorf("expected save-on error, got %v", err)
		}
	})

	// Save-on failure (backup also failed)
	rsyncRunnerErr := func(ctx context.Context, args []string, progress func(int64, int64)) error {
		return errors.New("rsync failed")
	}
	rsyncRunner = rsyncRunnerErr
	withCommandRunner(runnerSaveOnErr, func() {
		if _, err := engine.BackupServer(context.Background(), cfg.Watch[0], "s1", cfg.Servers["s1"], "", "", "", false); err == nil || !strings.Contains(err.Error(), "rsync") {
			t.Errorf("expected rsync error, got %v", err)
		}
	})

	// NAS backup success & error branches
	runnerNASSuccess := commandRunnerFunc(func(c context.Context, name string, args ...string) command {
		if name == "ssh" {
			for _, a := range args {
				if strings.Contains(a, "test -f") {
					return &mockOutputCommand{
						recordingCommand: recordingCommand{name: name, args: args},
						out:              []byte("ready"),
					}
				}
			}
		}
		return &mockOutputCommand{
			recordingCommand: recordingCommand{name: name, args: args},
			out:              []byte("Success"),
		}
	})

	rsyncRunner = func(ctx context.Context, args []string, progress func(int64, int64)) error { return nil }
	withCommandRunner(runnerNASSuccess, func() {
		resNAS, err := engine.BackupServer(context.Background(), cfg.Watch[0], "s2", cfg.Servers["s2"], "", "", "", true)
		if err != nil || resNAS.destination == "" || resNAS.target != "nas" {
			t.Errorf("NAS backup success failed: res=%+v, err=%v", resNAS, err)
		}
	})

	// NAS ensureNASDir error
	runnerNASEnsureErr := commandRunnerFunc(func(c context.Context, name string, args ...string) command {
		if name == "ssh" {
			for _, a := range args {
				if strings.Contains(a, "mkdir") {
					return &mockOutputCommand{
						recordingCommand: recordingCommand{name: name, args: args, err: errors.New("mkdir failed")},
						err:              errors.New("mkdir failed"),
					}
				}
			}
		}
		return &mockOutputCommand{
			recordingCommand: recordingCommand{name: name, args: args},
			out:              []byte("ready"),
		}
	})
	withCommandRunner(runnerNASEnsureErr, func() {
		if _, err := engine.BackupServer(context.Background(), cfg.Watch[0], "s2", cfg.Servers["s2"], "", "", "", true); err == nil || !strings.Contains(err.Error(), "create NAS dir") {
			t.Errorf("expected create NAS dir error, got %v", err)
		}
	})

	// NAS rsync error
	rsyncRunner = func(ctx context.Context, args []string, progress func(int64, int64)) error {
		return errors.New("nas rsync failed")
	}
	withCommandRunner(runnerNASSuccess, func() {
		if _, err := engine.BackupServer(context.Background(), cfg.Watch[0], "s2", cfg.Servers["s2"], "", "", "", true); err == nil || !strings.Contains(err.Error(), "NAS rsync") {
			t.Errorf("expected NAS rsync error, got %v", err)
		}
	})
}

func TestBackupServerNASReadyFailAndRsyncRunner(t *testing.T) {
	tmp := t.TempDir()
	oldInterval := rconRetryInterval
	rconRetryInterval = 1 * time.Millisecond
	t.Cleanup(func() { rconRetryInterval = oldInterval })
	watchDir := filepath.Join(tmp, "watch")

	cfg := &Config{
		NAS:   NASConfig{SSHHost: "nas.local", SSHUser: "user", DestRoot: "/nas"},
		Watch: []WatchConfig{{Path: watchDir, Namespace: "mc"}},
		Servers: map[string]ServerConfig{
			"nas_server": {
				Enabled: true,
				Target:  "nas",
			},
		},
	}

	engine := NewBackupEngine(*cfg)

	// checkNASReady failure -> returns error
	runnerNASFail := commandRunnerFunc(func(c context.Context, name string, args ...string) command {
		return &mockOutputCommand{
			recordingCommand: recordingCommand{name: name, args: args},
			err:              errors.New("ssh failed"),
		}
	})

	withCommandRunner(runnerNASFail, func() {
		if _, err := engine.BackupServer(context.Background(), WatchConfig{Path: watchDir, Namespace: "mc"}, "nas_server", cfg.Servers["nas_server"], "", "", "", false); err == nil {
			t.Error("expected BackupServer error when checkNASReady fails")
		}
	})
}
func TestRsyncScannerErrors(t *testing.T) {
	// parseRsyncProgress empty / single field
	if b, tot, ok := parseRsyncProgress("single"); ok || b != 0 || tot != 0 {
		t.Errorf("parseRsyncProgress single = (%d, %d, %v), want (0, 0, false)", b, tot, ok)
	}

	// scanRsyncLines CRLF
	advance, token, err := scanRsyncLines([]byte("line1\r\nline2"), false)
	if advance != 7 || string(token) != "line1" || err != nil {
		t.Fatalf("scanRsyncLines CRLF failed: adv=%d, token=%q, err=%v", advance, token, err)
	}

	// streamRsyncProgress error on broken reader
	streamRsyncProgress(errTestReader{}, nil)
}

func TestBackupServerAutodetectsRconPassword(t *testing.T) {
	tmpWatch := filepath.Join(t.TempDir(), "watch")
	propsDir := filepath.Join(tmpWatch, "cone-create", "mc-data")
	if err := os.MkdirAll(propsDir, 0755); err != nil {
		t.Fatalf("failed to create propsDir: %v", err)
	}
	propsFile := filepath.Join(propsDir, "server.properties")
	if err := os.WriteFile(propsFile, []byte("rcon.password=detected_pass\n"), 0600); err != nil {
		t.Fatalf("failed to write server.properties: %v", err)
	}

	localRoot := t.TempDir()
	runner := &recordingRunner{}
	previousRsyncRunner := rsyncRunner
	rsyncRunner = func(_ context.Context, _ []string, _ func(int64, int64)) error {
		return nil
	}
	defer func() { rsyncRunner = previousRsyncRunner }()

	watch := WatchConfig{Path: tmpWatch, Namespace: "test-ns"}
	server := ServerConfig{Target: "local", RconPassword: ""}
	withCommandRunner(runner, func() {
		_, err := NewBackupEngine(Config{
			Local: LocalConfig{DestRoot: localRoot},
		}).BackupServer(context.Background(), watch, "cone-create", server, "", "", "", false)
		if err != nil {
			t.Fatalf("BackupServer failed: %v", err)
		}
	})

	foundDetectedPass := false
	for _, cmd := range runner.commands {
		for _, env := range cmd.env {
			if env == "RCON_PASSWORD=detected_pass" {
				foundDetectedPass = true
			}
		}
	}
	if !foundDetectedPass {
		t.Errorf("expected RCON_PASSWORD=detected_pass in commands, got commands: %#v", runner.commands)
	}
}
func TestTieredBackupHotCopyIsAtomic(t *testing.T) {
	hotRoot := t.TempDir()
	source := t.TempDir()
	runner := &recordingRunner{}

	previousRsyncRunner := rsyncRunner
	defer func() { rsyncRunner = previousRsyncRunner }()

	var capturedDest string
	var inProgressExistedDuringRsync bool
	var finalExistedDuringRsync bool

	rsyncRunner = func(_ context.Context, args []string, _ func(int64, int64)) error {
		capturedDest = args[len(args)-1]
		dest := strings.TrimSuffix(capturedDest, "/")
		if _, err := os.Stat(dest); err == nil {
			inProgressExistedDuringRsync = true
		}
		finalDir := strings.TrimSuffix(dest, ".inprogress")
		if _, err := os.Stat(finalDir); err == nil {
			finalExistedDuringRsync = true
		}
		return nil
	}

	engine := NewBackupEngine(Config{Local: LocalConfig{HotRoot: hotRoot, DestRoot: t.TempDir()}})
	watch := WatchConfig{Namespace: "survival"}
	server := ServerConfig{Target: "tiered-local", DataDir: source}

	var res backupResult
	var err error
	withCommandRunner(runner, func() {
		res, err = engine.BackupServer(context.Background(), watch, "creative", server, "/prev/hot", "", "", true)
	})
	if err != nil {
		t.Fatalf("BackupServer failed: %v", err)
	}
	if !res.offloadPending {
		t.Fatal("expected offloadPending = true for tiered-local")
	}
	if !inProgressExistedDuringRsync {
		t.Error("expected .inprogress directory to exist during rsync")
	}
	if finalExistedDuringRsync {
		t.Error("final timestamp directory existed during rsync, want atomic rename after")
	}
	if _, err := os.Stat(res.destination); err != nil {
		t.Fatalf("final destination directory %q does not exist: %v", res.destination, err)
	}
	if _, err := os.Stat(res.destination + ".inprogress"); err == nil {
		t.Fatalf(".inprogress directory still exists after promotion: %s.inprogress", res.destination)
	}

	t.Run("hot rsync failure cleans up and re-enables save-on", func(t *testing.T) {
		var commands []string
		failRunner := commandRunnerFunc(func(c context.Context, name string, args ...string) command {
			cmdStr := name + " " + strings.Join(args, " ")
			commands = append(commands, cmdStr)
			return &recordingCommand{name: name, args: args}
		})

		rsyncRunner = func(_ context.Context, args []string, _ func(int64, int64)) error {
			dest := strings.TrimSuffix(args[len(args)-1], "/")
			_ = os.WriteFile(filepath.Join(dest, "stale.tmp"), []byte("stale"), 0644)
			return errors.New("hot rsync disk full")
		}

		withCommandRunner(failRunner, func() {
			_, err := engine.BackupServer(context.Background(), watch, "creative", server, "", "", "", false)
			if err == nil || !strings.Contains(err.Error(), "hot rsync disk full") {
				t.Fatalf("expected hot rsync error, got: %v", err)
			}
		})

		hasSaveOn := false
		for _, cmd := range commands {
			if strings.Contains(cmd, "save-on") {
				hasSaveOn = true
			}
		}
		if !hasSaveOn {
			t.Errorf("deferred save-on did not execute on hot rsync failure, commands: %v", commands)
		}
	})
}

func TestTieredBackupReenablesAutosaveBeforeOffload(t *testing.T) {
	hotRoot := t.TempDir()
	source := t.TempDir()

	var events []string
	var eventsMu sync.Mutex
	addEvent := func(e string) {
		eventsMu.Lock()
		events = append(events, e)
		eventsMu.Unlock()
	}

	runner := commandRunnerFunc(func(c context.Context, name string, args ...string) command {
		cmdStr := name + " " + strings.Join(args, " ")
		if strings.Contains(cmdStr, "save-off") {
			addEvent("save-off")
		} else if strings.Contains(cmdStr, "save-all flush") {
			addEvent("save-all flush")
		} else if name == "sync" {
			addEvent("sync")
		} else if strings.Contains(cmdStr, "save-on") {
			addEvent("save-on")
		}
		return &recordingCommand{name: name, args: args}
	})

	previousRsyncRunner := rsyncRunner
	defer func() { rsyncRunner = previousRsyncRunner }()

	rsyncRunner = func(_ context.Context, args []string, _ func(int64, int64)) error {
		dest := args[len(args)-1]
		if strings.Contains(dest, hotRoot) {
			addEvent("hot rsync")
		}
		return nil
	}

	cfg := Config{
		Local: LocalConfig{HotRoot: hotRoot, DestRoot: t.TempDir()},
	}
	engine := NewBackupEngine(cfg)
	watch := WatchConfig{Namespace: "ns"}
	server := ServerConfig{Target: "tiered-local", DataDir: source}

	withCommandRunner(runner, func() {
		res, err := engine.BackupServer(context.Background(), watch, "srv1", server, "", "", "", false)
		if err != nil {
			t.Fatalf("BackupServer failed: %v", err)
		}
		addEvent("BackupServer return")

		if res.offloadPending {
			_, err := offloadSnapshot(context.Background(), cfg, watch, "srv1", res.destination, res.timestamp, "local", "", nil)
			if err != nil {
				t.Fatalf("offloadSnapshot failed: %v", err)
			}
			addEvent("cold rsync")
		}
	})

	wantEvents := []string{"save-off", "save-all flush", "sync", "hot rsync", "save-on", "BackupServer return", "cold rsync"}
	if len(events) != len(wantEvents) {
		t.Fatalf("events = %v, want %v", events, wantEvents)
	}
	for i := range wantEvents {
		if events[i] != wantEvents[i] {
			t.Errorf("event %d = %q, want %q (full sequence: %v)", i, events[i], wantEvents[i], events)
		}
	}
}

func TestOffloadSnapshotLocalPromotesAtomically(t *testing.T) {
	coldRoot := t.TempDir()
	hotSnap := t.TempDir()
	_ = os.WriteFile(filepath.Join(hotSnap, "world.dat"), []byte("data"), 0644)

	cfg := Config{
		Local: LocalConfig{DestRoot: coldRoot},
	}
	watch := WatchConfig{Namespace: "mc"}

	previousRsyncRunner := rsyncRunner
	defer func() { rsyncRunner = previousRsyncRunner }()

	var passedLinkDest string
	var inProgressExistedDuringOffload bool
	var finalExistedDuringOffload bool

	rsyncRunner = func(_ context.Context, args []string, _ func(int64, int64)) error {
		for _, arg := range args {
			if strings.HasPrefix(arg, "--link-dest=") {
				passedLinkDest = strings.TrimPrefix(arg, "--link-dest=")
			}
		}
		dest := strings.TrimSuffix(args[len(args)-1], "/")
		if _, err := os.Stat(dest); err == nil {
			inProgressExistedDuringOffload = true
			_ = os.WriteFile(filepath.Join(dest, "marker.txt"), []byte("marker"), 0644)
		}
		finalDir := strings.TrimSuffix(dest, ".inprogress")
		if _, err := os.Stat(finalDir); err == nil {
			finalExistedDuringOffload = true
		}
		return nil
	}

	prevCold := filepath.Join(coldRoot, "mc", "s1", "20260101-1000")
	finalPath, err := offloadSnapshot(context.Background(), cfg, watch, "s1", hotSnap, "20260101-1100", "local", prevCold, nil)
	if err != nil {
		t.Fatalf("offloadSnapshot failed: %v", err)
	}

	if passedLinkDest != prevCold {
		t.Errorf("passed --link-dest = %q, want %q", passedLinkDest, prevCold)
	}
	if !inProgressExistedDuringOffload {
		t.Error("expected .inprogress to exist during offload rsync")
	}
	if finalExistedDuringOffload {
		t.Error("final path existed during offload rsync, want atomic rename after")
	}
	markerPath := filepath.Join(finalPath, "marker.txt")
	if _, err := os.Stat(markerPath); err != nil {
		t.Errorf("marker file missing in promoted path %q: %v", finalPath, err)
	}
	if _, err := os.Stat(finalPath + ".inprogress"); err == nil {
		t.Errorf(".inprogress directory still exists after offload promotion")
	}
}

func TestOffloadSnapshotFailureRemovesInProgress(t *testing.T) {
	coldRoot := t.TempDir()
	hotSnap := t.TempDir()

	cfg := Config{
		Local: LocalConfig{DestRoot: coldRoot},
		NAS:   NASConfig{SSHUser: "backup", SSHHost: "nas.local", DestRoot: "/volume1/backups"},
	}
	watch := WatchConfig{Namespace: "mc"}

	previousRsyncRunner := rsyncRunner
	defer func() { rsyncRunner = previousRsyncRunner }()

	rsyncRunner = func(_ context.Context, args []string, _ func(int64, int64)) error {
		dest := strings.TrimSuffix(args[len(args)-1], "/")
		_ = os.WriteFile(filepath.Join(dest, "partial.tmp"), []byte("partial"), 0644)
		return errors.New("offload connection lost")
	}

	prevCold := filepath.Join(coldRoot, "mc", "s1", "20260101-1000")
	_ = os.MkdirAll(prevCold, 0755)
	_ = os.WriteFile(filepath.Join(prevCold, "prev.dat"), []byte("prev"), 0644)

	_, err := offloadSnapshot(context.Background(), cfg, watch, "s1", hotSnap, "20260101-1100", "local", prevCold, nil)
	if err == nil {
		t.Fatal("expected local offload error, got nil")
	}

	inProgressLocal := filepath.Join(coldRoot, "mc", "s1", "20260101-1100.inprogress")
	finalLocal := filepath.Join(coldRoot, "mc", "s1", "20260101-1100")
	if _, err := os.Stat(inProgressLocal); err == nil {
		t.Errorf("local .inprogress still exists after offload failure: %s", inProgressLocal)
	}
	if _, err := os.Stat(finalLocal); err == nil {
		t.Errorf("local final directory created despite offload failure: %s", finalLocal)
	}
	if _, err := os.Stat(filepath.Join(prevCold, "prev.dat")); err != nil {
		t.Errorf("previous cold snapshot modified/deleted on offload failure: %v", err)
	}

	runner := &recordingRunner{}
	withCommandRunner(runner, func() {
		_, err = offloadSnapshot(context.Background(), cfg, watch, "s1", hotSnap, "20260101-1100", "nas", prevCold, nil)
	})
	if err == nil {
		t.Fatal("expected NAS offload error, got nil")
	}
	hasRemoveCmd := false
	hasRenameCmd := false
	for _, cmd := range runner.commands {
		cmdStr := cmd.name + " " + strings.Join(cmd.args, " ")
		if strings.Contains(cmdStr, "rm -rf") && strings.Contains(cmdStr, "20260101-1100.inprogress") {
			hasRemoveCmd = true
		}
		if strings.Contains(cmdStr, "mv --") {
			hasRenameCmd = true
		}
	}
	if !hasRemoveCmd {
		t.Errorf("NAS offload failure did not run rm -rf cleanup on remote .inprogress: commands = %#v", runner.commands)
	}
	if hasRenameCmd {
		t.Errorf("NAS offload failure unexpectedly ran mv promotion command: commands = %#v", runner.commands)
	}
}
