package engine

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestParseConfig(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "config.toml")

	content := []byte(`
[global]
listen_addr = "127.0.0.1:47990"
backup_interval = "1h"
initial_delay = "2m"
max_mbps = 40.0

[nas]
ssh_user = "backup"
ssh_host = "nas.local"
ssh_port = 22
ssh_key = "~/.ssh/id_ed25519"
dest_root = "/volume1/backups"

[retention]
prune_days = 7
prune_count = 0

[[watch]]
path = "/opt/mc/docker/servers"
namespace = "minecraft"
local_keep = 3
max_disk_pct = 90

[server.creative]
enabled = true
ssh_only = false
container_name = "creative-mc-1"
rcon_password = "hunter2"

[server.skyblock]
enabled = true
ssh_only = true
`)
	os.WriteFile(cfgPath, content, 0644)

	cfg, err := LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}

	if cfg.Global.ListenAddr != "127.0.0.1:47990" {
		t.Errorf("listen_addr: got %q", cfg.Global.ListenAddr)
	}
	if cfg.Global.BackupInterval.String() != "1h0m0s" {
		t.Errorf("backup_interval: got %q", cfg.Global.BackupInterval)
	}
	if cfg.NAS.SSHUser != "backup" {
		t.Errorf("ssh_user: got %q", cfg.NAS.SSHUser)
	}
	if len(cfg.Watch) != 1 {
		t.Fatalf("watch: expected 1, got %d", len(cfg.Watch))
	}
	if cfg.Watch[0].Path != "/opt/mc/docker/servers" {
		t.Errorf("watch.path: got %q", cfg.Watch[0].Path)
	}
	if cfg.Servers["creative"].ContainerName != "creative-mc-1" {
		t.Errorf("server.creative.container_name: got %q", cfg.Servers["creative"].ContainerName)
	}
	if cfg.Servers["creative"].RconPassword != "hunter2" {
		t.Errorf("server.creative.rcon_password: got %q", cfg.Servers["creative"].RconPassword)
	}
	if !cfg.Servers["skyblock"].SSHOnly {
		t.Error("server.skyblock.ssh_only: expected true")
	}
}

func TestEnvOverride(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "config.toml")

	content := []byte(`
[global]
listen_addr = "127.0.0.1:47990"

[nas]
ssh_host = "nas.local"

[server.creative]
enabled = true
rcon_password = "filepass"
`)
	os.WriteFile(cfgPath, content, 0644)

	t.Setenv("MC_BACKUP_NAS_SSH_HOST", "override.local")
	t.Setenv("MC_BACKUP_NAS_SSH_PORT", "2222")
	t.Setenv("MC_BACKUP_SERVER_CREATIVE_RCON_PASSWORD", "envpass")

	cfg, err := LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}

	if cfg.NAS.SSHHost != "override.local" {
		t.Errorf("SSHHost: got %q, want override.local", cfg.NAS.SSHHost)
	}
	if cfg.NAS.SSHPort != 2222 {
		t.Errorf("SSHPort: got %d, want 2222", cfg.NAS.SSHPort)
	}
	if cfg.Servers["creative"].RconPassword != "envpass" {
		t.Errorf("RconPassword: got %q, want envpass", cfg.Servers["creative"].RconPassword)
	}
}

func TestEnvOverrideCaseInsensitiveServerName(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "config.toml")
	content := []byte(`
[server.Creative]
enabled = false
rcon_password = "filepass"
`)
	os.WriteFile(cfgPath, content, 0644)
	t.Setenv("MC_BACKUP_SERVER_CREATIVE_RCON_PASSWORD", "envpass")
	cfg, err := LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}
	if cfg.Servers["creative"].RconPassword != "envpass" {
		t.Errorf("case-insensitive override failed: got %q", cfg.Servers["creative"].RconPassword)
	}
}

func TestSaveConfig(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "config.toml")

	content := []byte(`
[global]
listen_addr = "127.0.0.1:47990"

[nas]
ssh_host = "nas.local"
`)
	os.WriteFile(cfgPath, content, 0644)

	cfg, err := LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}

	cfg.Servers["new-server"] = ServerConfig{
		Enabled:       true,
		ContainerName: "new-server-mc-1",
	}

	if err := SaveConfig(cfgPath, cfg); err != nil {
		t.Fatalf("SaveConfig failed: %v", err)
	}

	reloaded, err := LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("reload failed: %v", err)
	}
	s, ok := reloaded.Servers["new-server"]
	if !ok {
		t.Fatal("new server not in reloaded config")
	}
	if s.ContainerName != "new-server-mc-1" {
		t.Errorf("new server not persisted correctly: got %q", s.ContainerName)
	}
}

func TestEnvOverrideServerNameWithUnderscore(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "config.toml")
	content := []byte(`
[server.my_creative]
enabled = true
rcon_password = "filepass"
`)
	os.WriteFile(cfgPath, content, 0644)

	t.Setenv("MC_BACKUP_SERVER_MY_CREATIVE_RCON_PASSWORD", "envpass")
	t.Setenv("MC_BACKUP_SERVER_MY_CREATIVE_PAUSE_IF_NO_PLAYERS", "true")

	cfg, err := LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}
	if cfg.Servers["my_creative"].RconPassword != "envpass" {
		t.Errorf("RconPassword: got %q, want envpass", cfg.Servers["my_creative"].RconPassword)
	}
	if !cfg.Servers["my_creative"].PauseIfNoPlayers {
		t.Error("PauseIfNoPlayers: expected true")
	}
}

func TestLastSnapshotsRoundTripMultipleServers(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "config.toml")
	firstTime := time.Unix(1718107200, 0)
	secondTime := time.Unix(1718110800, 0)

	writeLastSnapshotAt(cfgPath, "creative", "/local/creative/20250611-1200", "/nas/creative/20250611-1200", firstTime)
	writeLastSnapshotAt(cfgPath, "survival", "/local/survival/20250611-1300", "", secondTime)

	got := readLastSnapshots(cfgPath)
	if len(got) != 2 {
		t.Fatalf("readLastSnapshots returned %d entries, want 2", len(got))
	}
	if got["creative"].Time.Unix() != firstTime.Unix() || got["creative"].Local != "/local/creative/20250611-1200" || got["creative"].NAS != "/nas/creative/20250611-1200" {
		t.Errorf("creative snapshot = %#v", got["creative"])
	}
	if got["survival"].Time.Unix() != secondTime.Unix() || got["survival"].Local != "/local/survival/20250611-1300" || got["survival"].NAS != "" {
		t.Errorf("survival snapshot = %#v", got["survival"])
	}
}

func TestReadLastSnapshotsSkipsMalformedLines(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "config.toml")
	lastPath := lastBackupPath(cfgPath)
	content := []byte("missing-timestamp\ncreative=not-a-number=/local=/nas\nsurvival=1718110800=/local/survival=\n")
	if err := os.WriteFile(lastPath, content, 0644); err != nil {
		t.Fatalf("write last-backup: %v", err)
	}

	got := readLastSnapshots(cfgPath)
	if len(got) != 1 {
		t.Fatalf("readLastSnapshots returned %d entries, want 1", len(got))
	}
	if got["survival"].Local != "/local/survival" {
		t.Errorf("survival local path = %q", got["survival"].Local)
	}
}
