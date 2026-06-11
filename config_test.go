package main

import (
	"os"
	"path/filepath"
	"testing"
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
