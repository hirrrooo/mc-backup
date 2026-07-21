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
	if err := os.WriteFile(filepath.Join(root, "creative", "backups", "old"), nil, 0644); err != nil {
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
	if err := os.WriteFile(cfgPath, []byte("[global]\n"), 0644); err != nil {
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
	if err := os.WriteFile(cfgPath, []byte("[global]\n"), 0644); err != nil {
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
	if err := os.WriteFile(cfgPath, []byte("[global]\n"), 0644); err != nil {
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
