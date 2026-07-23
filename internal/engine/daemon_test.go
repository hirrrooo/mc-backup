package engine

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
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
