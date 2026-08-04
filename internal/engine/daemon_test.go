package engine

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func TestWarnLegacyBackupDirOnce(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "creative", "backups"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "creative", "backups", "old"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	d := NewDaemon(filepath.Join(root, "config.toml"), &Config{})
	var logs bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	defer slog.SetDefault(old)
	d.warnLegacyBackupDirOnce(WatchConfig{Path: root}, "creative")
	d.warnLegacyBackupDirOnce(WatchConfig{Path: root}, "creative")
	if got := bytes.Count(logs.Bytes(), []byte("legacy backup directory is no longer managed")); got != 1 {
		t.Fatalf("warning count = %d, want 1; logs=%q", got, logs.String())
	}
}

func TestDiscoverSnapshotsUsesLocalDestinationRoot(t *testing.T) {
	root := t.TempDir()
	cfgPath := filepath.Join(root, "config.toml")
	if err := os.WriteFile(cfgPath, []byte("[global]\n"), 0600); err != nil {
		t.Fatal(err)
	}
	watchRoot := filepath.Join(root, "watch")
	if err := os.MkdirAll(filepath.Join(watchRoot, "creative"), 0755); err != nil {
		t.Fatal(err)
	}
	localRoot := filepath.Join(root, "local")
	localServer := filepath.Join(localRoot, "survival", "creative")
	if err := os.MkdirAll(filepath.Join(localServer, "20240101-1200"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(localServer, "20240102-1200"), 0755); err != nil {
		t.Fatal(err)
	}
	d := NewDaemon(cfgPath, &Config{
		Local:   LocalConfig{DestRoot: localRoot},
		Watch:   []WatchConfig{{Path: watchRoot, Namespace: "survival"}},
		Servers: map[string]ServerConfig{"creative": {Enabled: true, Target: "local"}},
	})
	d.discoverSnapshots(context.Background(), d.ac.Load())
	got := readLastSnapshots(cfgPath)["creative"]
	want := filepath.Join(localServer, "20240102-1200")
	if got.Local != want || got.NAS != "" {
		t.Fatalf("snapshot = %#v, want local=%q and no NAS", got, want)
	}
}

func TestDiscoverSnapshotsSkipsEmptyLocalRootAndLegacyHistory(t *testing.T) {
	root := t.TempDir()
	cfgPath := filepath.Join(root, "config.toml")
	if err := os.WriteFile(cfgPath, []byte("[global]\n"), 0600); err != nil {
		t.Fatal(err)
	}
	watchRoot := filepath.Join(root, "watch")
	legacy := filepath.Join(watchRoot, "creative", "backups", "20240109-1200")
	if err := os.MkdirAll(legacy, 0755); err != nil {
		t.Fatal(err)
	}
	runner := &recordingRunner{}
	d := NewDaemon(cfgPath, &Config{
		Watch:   []WatchConfig{{Path: watchRoot, Namespace: "survival"}},
		Servers: map[string]ServerConfig{"creative": {Enabled: true, Target: "local"}},
	})
	withCommandRunner(runner, func() { d.discoverSnapshots(context.Background(), d.ac.Load()) })
	if _, ok := readLastSnapshots(cfgPath)["creative"]; ok {
		t.Fatal("legacy watch backup was selected as history")
	}
	if len(runner.commands) != 0 {
		t.Fatalf("commands = %#v, local discovery should not use SSH", runner.commands)
	}
}

func TestProvisionServersConcurrent(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "config.toml")
	if err := os.WriteFile(cfgPath, []byte("[global]\n"), 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	d := NewDaemon(cfgPath, &Config{Servers: map[string]ServerConfig{}})

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ns := []struct {
				Name   string
				Server ServerConfig
			}{{Name: fmt.Sprintf("srv%d", i), Server: ServerConfig{Enabled: true}}}
			d.provisionServers(d.ac.Load(), ns)
		}(i)
	}
	wg.Wait()

	d.autoMu.Lock()
	got := len(d.autoServers)
	d.autoMu.Unlock()
	if got != 16 {
		t.Fatalf("expected 16 auto servers, got %d", got)
	}
}

func TestServerMatches(t *testing.T) {
	cases := []struct {
		onlyServer, name string
		want             bool
	}{
		{"", "creative", true},
		{"", "", true},
		{"creative", "creative", true},
		{"Creative", "creative", true},
		{"CREATIVE", "creative", true},
		{"creative", "Creative", true},
		{"creative", "survival", false},
		{"creative", "creative-survival", false},
	}
	for _, c := range cases {
		if got := serverMatches(c.onlyServer, c.name); got != c.want {
			t.Errorf("serverMatches(%q, %q) = %v, want %v", c.onlyServer, c.name, got, c.want)
		}
	}
}

func TestDaemonCancel(t *testing.T) {
	d := NewDaemon("/tmp/config.toml", &Config{})
	canceled := false
	d.cancelMu.Lock()
	d.cancelFn = func() { canceled = true }
	d.cancelMu.Unlock()

	d.Cancel()

	if !canceled {
		t.Error("Cancel() did not invoke cancelFn")
	}
}

func TestSnapshotTimeAndWatchKey(t *testing.T) {
	t.Run("snapshotTime", func(t *testing.T) {
		if !snapshotTime("invalid").IsZero() {
			t.Error("snapshotTime('invalid') should be zero")
		}
		if !snapshotTime("20250611_1200").IsZero() {
			t.Error("snapshotTime('20250611_1200') should be zero")
		}
		if !snapshotTime("20259999-9999").IsZero() {
			t.Error("snapshotTime('20259999-9999') should be zero")
		}
		st := snapshotTime("/backups/20250611-1200")
		if st.IsZero() {
			t.Error("snapshotTime('/backups/20250611-1200') should not be zero")
		}
	})

	t.Run("watchKey", func(t *testing.T) {
		tmp := t.TempDir()
		if err := os.MkdirAll(filepath.Join(tmp, "survival"), 0755); err != nil {
			t.Fatal(err)
		}
		cfg := &Config{
			Watch: []WatchConfig{{Path: tmp, Namespace: "minecraft"}},
		}
		key := watchKey(cfg, "survival")
		if key != "minecraft/survival" {
			t.Errorf("watchKey = %q, want 'minecraft/survival'", key)
		}
		if keyAbs := watchKey(cfg, "absent"); keyAbs != "" {
			t.Errorf("watchKey absent = %q, want empty", keyAbs)
		}
	})

	t.Run("serverNames", func(t *testing.T) {
		servers := []struct {
			Watch  WatchConfig
			Name   string
			Server ServerConfig
		}{
			{Name: "creative"},
			{Name: "survival"},
		}
		names := serverNames(servers)
		if len(names) != 2 || names[0] != "creative" || names[1] != "survival" {
			t.Errorf("serverNames = %v, want [creative survival]", names)
		}
	})
}

func TestRunBackupCycleLocalServer(t *testing.T) {
	root := t.TempDir()
	cfgPath := filepath.Join(root, "config.toml")
	if err := os.WriteFile(cfgPath, []byte("[global]\nlisten_addr = \"127.0.0.1:47990\"\n"), 0600); err != nil {
		t.Fatal(err)
	}

	watchRoot := filepath.Join(root, "watch")
	if err := os.MkdirAll(filepath.Join(watchRoot, "survival"), 0755); err != nil {
		t.Fatal(err)
	}
	localRoot := filepath.Join(root, "local")

	cfg := &Config{
		Local: LocalConfig{DestRoot: localRoot},
		Watch: []WatchConfig{{Path: watchRoot, Namespace: "minecraft"}},
		Servers: map[string]ServerConfig{
			"survival": {
				Enabled:       true,
				Target:        "local",
				ContainerName: "survival-mc-1",
			},
		},
	}

	d := NewDaemon(cfgPath, cfg)

	runner := commandRunnerFunc(func(c context.Context, name string, args ...string) command {
		if name == "docker" && len(args) > 0 && args[0] == "ps" {
			return &mockOutputCommand{
				recordingCommand: recordingCommand{name: name, args: args},
				out:              []byte("survival-mc-1"),
			}
		}
		return &recordingCommand{name: name, args: args}
	})

	previousRsyncRunner := rsyncRunner
	rsyncRunner = func(_ context.Context, _ []string, _ func(int64, int64)) error {
		return nil
	}
	defer func() { rsyncRunner = previousRsyncRunner }()

	withCommandRunner(runner, func() {
		d.runBackupCycle(context.Background(), "survival", false)
	})

	snapshots := readLastSnapshots(cfgPath)
	if entry, ok := snapshots["survival"]; !ok || entry.Local == "" {
		t.Fatalf("runBackupCycle failed to write snapshot entry: %#v", snapshots)
	}
}

func TestWaitForContainersNoServers(t *testing.T) {
	root := t.TempDir()
	cfgPath := filepath.Join(root, "config.toml")
	d := NewDaemon(cfgPath, &Config{})
	d.waitForContainers(context.Background(), d.ac.Load())
}
func TestDaemonRunContextCancelled(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "config.toml")
	_ = os.WriteFile(cfgPath, []byte("[global]\nlisten_addr = \"127.0.0.1:8080\"\nbackup_interval = \"1ms\"\ninitial_delay = \"0s\"\n"), 0600)

	// Write recent and due snapshots to test initial backup branches
	writeLastSnapshotAt(cfgPath, "s_recent", "/local/a", "", time.Now())
	writeLastSnapshotAt(cfgPath, "s_due", "/local/b", "", time.Now().Add(-200*time.Hour))

	cfg, err := LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}
	cfg.Servers = map[string]ServerConfig{
		"s_recent": {Enabled: true, Target: "local"},
		"s_due":    {Enabled: true, Target: "local"},
	}
	d := NewDaemon(cfgPath, cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	err = d.RunContext(ctx)
	if err != nil && !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
		t.Fatalf("RunContext unexpected error: %v", err)
	}
}
func TestWaitForContainersCheckableReady(t *testing.T) {
	tmp := t.TempDir()
	watchDir := filepath.Join(tmp, "watch")
	serverDir := filepath.Join(watchDir, "creative")
	_ = os.MkdirAll(serverDir, 0755)

	cfg := &Config{
		Global: GlobalConfig{
			InitialDelay: Duration{Duration: 1 * time.Second},
		},
		Watch: []WatchConfig{{Path: watchDir, Namespace: "mc"}},
		Servers: map[string]ServerConfig{
			"creative": {Enabled: true, ContainerName: "creative-mc-1"},
		},
	}

	d := NewDaemon(filepath.Join(tmp, "config.toml"), cfg)

	startTime := time.Now().Add(-1 * time.Hour).Format(time.RFC3339Nano)
	runner := commandRunnerFunc(func(ctx context.Context, name string, args ...string) command {
		return &mockOutputCommand{
			recordingCommand: recordingCommand{name: name, args: args},
			out:              []byte(startTime),
		}
	})

	withCommandRunner(runner, func() {
		d.waitForContainers(context.Background(), cfg)
	})
}
func TestRunDiscovery(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "config.toml")
	_ = os.WriteFile(cfgPath, []byte("[global]\nlisten_addr = \"127.0.0.1:8080\"\n"), 0600)

	cfg, _ := LoadConfig(cfgPath)
	d := NewDaemon(cfgPath, cfg)

	d.runDiscovery(context.Background())
}
func TestWaitForContainersUnreachableAndError(t *testing.T) {
	tmp := t.TempDir()
	watchDir := filepath.Join(tmp, "watch")
	_ = os.MkdirAll(filepath.Join(watchDir, "creative"), 0755)

	cfg := &Config{
		Global: GlobalConfig{
			InitialDelay: Duration{Duration: 1 * time.Millisecond},
		},
		Watch: []WatchConfig{{Path: watchDir, Namespace: "mc"}},
		Servers: map[string]ServerConfig{
			"creative": {Enabled: true, ContainerName: "creative-mc-1"},
		},
	}

	d := NewDaemon(filepath.Join(tmp, "config.toml"), cfg)

	// Runner that fails on inspect
	runnerErr := commandRunnerFunc(func(ctx context.Context, name string, args ...string) command {
		return &mockOutputCommand{
			recordingCommand: recordingCommand{name: name, args: args},
			err:              context.DeadlineExceeded,
		}
	})

	withCommandRunner(runnerErr, func() {
		d.waitForContainers(context.Background(), cfg)
	})
}

func TestDiscoverSnapshotsNAS(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "config.toml")
	_ = os.WriteFile(cfgPath, []byte("[global]\nlisten_addr = \"127.0.0.1:8080\"\n"), 0600)

	watchDir := filepath.Join(tmp, "watch")
	_ = os.MkdirAll(filepath.Join(watchDir, "nas_server"), 0755)

	cfg := &Config{
		Watch: []WatchConfig{{Path: watchDir, Namespace: "mc"}},
		NAS: NASConfig{
			SSHUser:  "user",
			SSHHost:  "nas.local",
			DestRoot: "/backups",
		},
		Servers: map[string]ServerConfig{
			"nas_server": {Enabled: true, Target: "nas"},
		},
	}

	d := NewDaemon(cfgPath, cfg)

	runner := commandRunnerFunc(func(ctx context.Context, name string, args ...string) command {
		return &mockOutputCommand{
			recordingCommand: recordingCommand{name: name, args: args},
			out:              []byte("20240105-1200\n"),
		}
	})

	withCommandRunner(runner, func() {
		d.discoverSnapshots(context.Background(), cfg)
	})

	snaps := readLastSnapshots(cfgPath)
	if entry, ok := snaps["nas_server"]; !ok || entry.NAS == "" {
		t.Errorf("discoverSnapshots NAS = %v, want NAS snapshot set", snaps)
	}
}

func TestRunDiscoveryWithNewServers(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "config.toml")
	_ = os.WriteFile(cfgPath, []byte("[global]\nlisten_addr = \"127.0.0.1:8080\"\n"), 0600)

	watchDir := filepath.Join(tmp, "watch")
	_ = os.MkdirAll(filepath.Join(watchDir, "discovered_server"), 0755)

	cfg := &Config{
		Watch: []WatchConfig{{Path: watchDir, Namespace: "mc"}},
	}

	d := NewDaemon(cfgPath, cfg)

	oldTrigger := triggerDiscoveryBackup
	called := false
	triggerDiscoveryBackup = func(d *Daemon, ctx context.Context) {
		called = true
	}
	t.Cleanup(func() { triggerDiscoveryBackup = oldTrigger })

	runner := commandRunnerFunc(func(c context.Context, name string, args ...string) command {
		return &mockOutputCommand{
			recordingCommand: recordingCommand{name: name, args: args},
			out:              []byte("discovered_server-mc-1"),
		}
	})
	withCommandRunner(runner, func() {
		d.runDiscovery(context.Background())
	})

	if !called {
		t.Error("expected triggerDiscoveryBackup to be called when new servers are discovered")
	}
}

func TestRunBackupCycleCanceledAndFiltering(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "config.toml")
	_ = os.WriteFile(cfgPath, []byte("[global]\nlisten_addr = \"127.0.0.1:8080\"\n"), 0600)

	cfg := &Config{
		Servers: map[string]ServerConfig{
			"disabled_server": {Enabled: false},
			"other_server":    {Enabled: true},
		},
	}
	d := NewDaemon(cfgPath, cfg)

	// Filtered out by name
	d.runBackupCycle(context.Background(), "nonexistent_server", false)

	// Canceled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	d.runBackupCycle(ctx, "", false)
}

func TestDaemonRunSignal(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "config.toml")
	_ = os.WriteFile(cfgPath, []byte("[global]\nlisten_addr = \"127.0.0.1:8080\"\nbackup_interval = \"100h\"\ninitial_delay = \"0s\"\n"), 0600)

	cfg, err := LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}
	d := NewDaemon(cfgPath, cfg)

	go func() {
		time.Sleep(30 * time.Millisecond)
		p, err := os.FindProcess(os.Getpid())
		if err == nil {
			_ = p.Signal(syscall.SIGINT)
		}
	}()

	_ = d.Run()
}
func TestRunBackupCyclePauseIfNoPlayers(t *testing.T) {
	tmp := t.TempDir()
	oldRconInterval := rconRetryInterval
	rconRetryInterval = 1 * time.Millisecond
	t.Cleanup(func() { rconRetryInterval = oldRconInterval })
	watchDir := filepath.Join(tmp, "watch")
	_ = os.MkdirAll(filepath.Join(watchDir, "creative"), 0755)

	cfgPath := filepath.Join(tmp, "config.toml")
	_ = os.WriteFile(cfgPath, []byte("[global]\nlisten_addr = \"127.0.0.1:8080\"\n"), 0600)

	cfg := &Config{
		Local: LocalConfig{DestRoot: filepath.Join(tmp, "local")},
		Watch: []WatchConfig{{Path: watchDir, Namespace: "mc"}},
		Servers: map[string]ServerConfig{
			"creative": {
				Enabled:          true,
				Target:           "local",
				PauseIfNoPlayers: true,
			},
		},
	}

	d := NewDaemon(cfgPath, cfg)

	rsyncCalled := false
	oldRsync := rsyncRunner
	rsyncRunner = func(ctx context.Context, args []string, progress func(int64, int64)) error {
		rsyncCalled = true
		return nil
	}
	t.Cleanup(func() { rsyncRunner = oldRsync })

	var executedCommands []string
	runner := commandRunnerFunc(func(c context.Context, name string, args ...string) command {
		cmdStr := name + " " + strings.Join(args, " ")
		executedCommands = append(executedCommands, cmdStr)

		if name == "docker" && len(args) > 0 {
			if args[0] == "ps" {
				return &mockOutputCommand{
					recordingCommand: recordingCommand{name: name, args: args},
					out:              []byte("creative-mc-1"),
				}
			}
			if args[0] == "exec" {
				for _, a := range args {
					if a == "rcon-cli" {
						return &mockOutputCommand{
							recordingCommand: recordingCommand{name: name, args: args},
							out:              []byte("There are 0 of a max of 20 players online:"),
						}
					}
				}
			}
		}
		return &recordingCommand{name: name, args: args}
	})

	withCommandRunner(runner, func() {
		d.runBackupCycle(context.Background(), "creative", false)
	})

	if rsyncCalled {
		t.Error("expected rsync NOT to be called when 0 players are online and PauseIfNoPlayers is true")
	}

	hasRconCount := false
	for _, cmd := range executedCommands {
		if strings.Contains(cmd, "rcon-cli") {
			hasRconCount = true
		}
	}
	if !hasRconCount {
		t.Errorf("expected rcon-cli player count check in executed commands, got: %v", executedCommands)
	}
}

func TestRunBackupCycleContainerNotRunningAndRconErrorAndFullRun(t *testing.T) {
	tmp := t.TempDir()
	oldRconInterval := rconRetryInterval
	rconRetryInterval = 1 * time.Millisecond
	t.Cleanup(func() { rconRetryInterval = oldRconInterval })
	watchDir := filepath.Join(tmp, "watch")
	_ = os.MkdirAll(filepath.Join(watchDir, "s1"), 0755)
	cfgPath := filepath.Join(tmp, "config.toml")
	_ = os.WriteFile(cfgPath, []byte("[global]\nlisten_addr = \"127.0.0.1:8080\"\n"), 0600)

	cfg := &Config{
		Local: LocalConfig{DestRoot: filepath.Join(tmp, "local")},
		Watch: []WatchConfig{{Path: watchDir, Namespace: "mc"}},
		Servers: map[string]ServerConfig{
			"s1": {
				Enabled:          true,
				Target:           "local",
				PauseIfNoPlayers: true,
			},
		},
	}

	d := NewDaemon(cfgPath, cfg)

	// Container not running -> skips
	runnerNotRunning := commandRunnerFunc(func(c context.Context, name string, args ...string) command {
		return &mockOutputCommand{
			recordingCommand: recordingCommand{name: name, args: args},
			out:              []byte(""),
		}
	})
	withCommandRunner(runnerNotRunning, func() {
		d.runBackupCycle(context.Background(), "s1", false)
	})

	// RCON error -> skips
	runnerRconErr := commandRunnerFunc(func(c context.Context, name string, args ...string) command {
		if name == "docker" && len(args) > 0 {
			if args[0] == "ps" {
				return &mockOutputCommand{
					recordingCommand: recordingCommand{name: name, args: args},
					out:              []byte("s1-mc-1"),
				}
			}
			if args[0] == "exec" {
				return &mockOutputCommand{
					recordingCommand: recordingCommand{name: name, args: args},
					err:              errors.New("rcon exec error"),
				}
			}
		}
		return &recordingCommand{name: name, args: args}
	})
	withCommandRunner(runnerRconErr, func() {
		d.runBackupCycle(context.Background(), "s1", false)
	})

	// Full run with 1 player online -> backup proceeds
	oldRsync := rsyncRunner
	rsyncRunner = func(ctx context.Context, args []string, progress func(int64, int64)) error { return nil }
	t.Cleanup(func() { rsyncRunner = oldRsync })

	runner1Player := commandRunnerFunc(func(c context.Context, name string, args ...string) command {
		if name == "docker" && len(args) > 0 {
			if args[0] == "ps" {
				return &mockOutputCommand{
					recordingCommand: recordingCommand{name: name, args: args},
					out:              []byte("s1-mc-1"),
				}
			}
			if args[0] == "exec" {
				return &mockOutputCommand{
					recordingCommand: recordingCommand{name: name, args: args},
					out:              []byte("There are 1 of a max of 20 players online: player1"),
				}
			}
		}
		return &recordingCommand{name: name, args: args}
	})
	withCommandRunner(runner1Player, func() {
		d.runBackupCycle(context.Background(), "s1", false)
	})
}

func TestDaemonRunBackupCycleAutodetectsRconPassword(t *testing.T) {
	tmp := t.TempDir()
	watchDir := filepath.Join(tmp, "watch")
	mcDataDir := filepath.Join(watchDir, "cone-create", "mc-data")
	if err := os.MkdirAll(mcDataDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mcDataDir, "server.properties"), []byte("rcon.password=player_check_pass\n"), 0600); err != nil {
		t.Fatal(err)
	}

	cfgPath := filepath.Join(tmp, "config.toml")
	_ = os.WriteFile(cfgPath, []byte("[global]\n"), 0600)

	cfg := &Config{
		Local: LocalConfig{DestRoot: filepath.Join(tmp, "local")},
		Watch: []WatchConfig{{Path: watchDir, Namespace: "mc"}},
		Servers: map[string]ServerConfig{
			"cone-create": {
				Enabled:          true,
				Target:           "local",
				PauseIfNoPlayers: true,
				RconPassword:     "",
			},
		},
	}

	d := NewDaemon(cfgPath, cfg)

	var recordedCmds []*mockOutputCommand
	runnerInterceptor := commandRunnerFunc(func(c context.Context, name string, args ...string) command {
		mc := &mockOutputCommand{
			recordingCommand: recordingCommand{name: name, args: args},
		}
		if name == "docker" && len(args) > 0 {
			if args[0] == "ps" {
				mc.out = []byte("cone-create-mc-1")
			} else if args[0] == "exec" {
				mc.out = []byte("There are 0 of a max of 20 players online:")
			}
		}
		recordedCmds = append(recordedCmds, mc)
		return mc
	})

	withCommandRunner(runnerInterceptor, func() {
		d.runBackupCycle(context.Background(), "cone-create", false)
	})

	var capturedPass string
	for _, cmd := range recordedCmds {
		for _, e := range cmd.env {
			if strings.HasPrefix(e, "RCON_PASSWORD=") {
				capturedPass = strings.TrimPrefix(e, "RCON_PASSWORD=")
			}
		}
	}

	if capturedPass != "player_check_pass" {
		t.Fatalf("captured password = %q, want %q", capturedPass, "player_check_pass")
	}
}
