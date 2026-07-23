package engine

import (
	"bytes"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
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

[local]
dest_root = "relative/local///"

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

[server.creative]
enabled = true
target = "local"
container_name = "creative-mc-1"
rcon_password = "hunter2"

[server.skyblock]
enabled = true
`)
	if err := os.WriteFile(cfgPath, content, 0600); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

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
	if cfg.Local.DestRoot != "relative/local" {
		t.Errorf("local.dest_root: got %q", cfg.Local.DestRoot)
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
	if cfg.Servers["creative"].Target != "local" {
		t.Errorf("server.creative.target: got %q", cfg.Servers["creative"].Target)
	}
	if cfg.Servers["skyblock"].Target != "" {
		t.Errorf("server.skyblock.target: got %q, want omitted", cfg.Servers["skyblock"].Target)
	}
}

func TestTargetConfig(t *testing.T) {
	if got, err := resolveBackupTarget("skyblock", ServerConfig{}, LocalConfig{}); err != nil || got != "nas" {
		t.Fatalf("omitted target = %q, %v; want nas", got, err)
	}
	if got, err := resolveBackupTarget("creative", ServerConfig{Target: "local"}, LocalConfig{DestRoot: "relative/local"}); err != nil || got != "local" {
		t.Fatalf("local target = %q, %v; want local", got, err)
	}
}

func TestResolveBackupTargetRejectsInvalidTarget(t *testing.T) {
	_, err := resolveBackupTarget("creative", ServerConfig{Target: "s3"}, LocalConfig{})
	want := `server "creative" has invalid backup target "s3" (want "local" or "nas")`
	if err == nil || err.Error() != want {
		t.Fatalf("error = %v, want %q", err, want)
	}
}

func TestResolveBackupTargetRequiresLocalRoot(t *testing.T) {
	_, err := resolveBackupTarget("creative", ServerConfig{Target: "local"}, LocalConfig{})
	want := `server "creative" target "local" requires local.dest_root`
	if err == nil || err.Error() != want {
		t.Fatalf("error = %v, want %q", err, want)
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
	if err := os.WriteFile(cfgPath, content, 0600); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	t.Setenv("MC_BACKUP_NAS_SSH_HOST", "override.local")
	t.Setenv("MC_BACKUP_NAS_SSH_PORT", "2222")
	t.Setenv("MC_BACKUP_SERVER_CREATIVE_RCON_PASSWORD", "envpass")
	t.Setenv("MC_BACKUP_LOCAL_DEST_ROOT", "/env///")
	t.Setenv("MC_BACKUP_SERVER_CREATIVE_TARGET", "local")

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
	if cfg.Local.DestRoot != "/env" {
		t.Errorf("Local.DestRoot: got %q, want /env", cfg.Local.DestRoot)
	}
	if got := GetConfigValue(cfg, "server.creative.target"); got != "local" {
		t.Errorf("target getter: got %q, want local", got)
	}
}

func TestEnvOverrideCaseInsensitiveServerName(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "config.toml")
	content := []byte(`
[local]
dest_root = "/tmp"

[server.Creative]
enabled = false
target = "local"
rcon_password = "filepass"
`)
	if err := os.WriteFile(cfgPath, content, 0600); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
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
ssh_user = "backup"
ssh_host = "nas.local"
dest_root = "/volume1/backups"
`)
	if err := os.WriteFile(cfgPath, content, 0600); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

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
[local]
dest_root = "/tmp"

[server.my_creative]
enabled = true
target = "local"
rcon_password = "filepass"
`)
	if err := os.WriteFile(cfgPath, content, 0600); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

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

func TestSetConfigValueDoesNotDuplicateAutoServers(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "config.toml")
	autoPath := autoServersPath(cfgPath)

	if err := os.WriteFile(cfgPath, []byte("[global]\nmax_mbps = 40.0\n[local]\ndest_root = \"/tmp\"\n"), 0600); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	if err := os.WriteFile(autoPath, []byte("[server.creative]\nenabled = true\ntarget = \"local\"\ncontainer_name = \"creative-mc-1\"\nrcon_password = \"secret\"\n"), 0600); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	if err := SetConfigValue(cfgPath, "global.max_mbps", "20"); err != nil {
		t.Fatalf("SetConfigValue: %v", err)
	}

	mainBytes, _ := os.ReadFile(cfgPath)
	if strings.Contains(string(mainBytes), "creative") {
		t.Fatalf("auto server leaked into main config:\n%s", mainBytes)
	}

	cfg, err := LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Servers["creative"].RconPassword != "secret" {
		t.Errorf("auto server lost rcon_password: %#v", cfg.Servers["creative"])
	}
	if cfg.Global.MaxMBps != 20 {
		t.Errorf("max_mbps not applied: %v", cfg.Global.MaxMBps)
	}
}

func TestSetConfigValueUpdatesAutoServerInAutoFile(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "config.toml")
	autoPath := autoServersPath(cfgPath)

	if err := os.WriteFile(cfgPath, []byte("[global]\n[local]\ndest_root = \"/tmp\"\n"), 0600); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	if err := os.WriteFile(autoPath, []byte("[server.creative]\nenabled = true\ntarget = \"local\"\ncontainer_name = \"creative-mc-1\"\nrcon_password = \"old\"\n"), 0600); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	if err := SetConfigValue(cfgPath, "server.creative.rcon_password", "new"); err != nil {
		t.Fatalf("SetConfigValue: %v", err)
	}

	mainBytes, _ := os.ReadFile(cfgPath)
	if strings.Contains(string(mainBytes), "creative") {
		t.Fatalf("auto server written to main config:\n%s", mainBytes)
	}
	cfg, err := LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Servers["creative"].RconPassword != "new" {
		t.Errorf("rcon_password not updated: %#v", cfg.Servers["creative"])
	}
	if cfg.Servers["creative"].ContainerName != "creative-mc-1" {
		t.Errorf("container_name lost: %#v", cfg.Servers["creative"])
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
	if err := os.WriteFile(lastPath, content, 0600); err != nil {
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

func TestSaveAutoServersAtomicRoundTrip(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "config.toml")
	if err := os.WriteFile(cfgPath, []byte("[global]\n\n[nas]\nssh_user = \"backup\"\nssh_host = \"nas.local\"\ndest_root = \"/volume1/backups\"\n"), 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	servers := map[string]ServerConfig{
		"creative": {
			Enabled:          true,
			Target:           "nas",
			ContainerName:    "creative-mc-1",
			RconPassword:     "secret",
			PauseIfNoPlayers: true,
		},
	}
	if err := SaveAutoServers(cfgPath, servers); err != nil {
		t.Fatalf("SaveAutoServers: %v", err)
	}
	autoBytes, err := os.ReadFile(autoServersPath(cfgPath))
	if err != nil {
		t.Fatalf("read auto file: %v", err)
	}
	if !strings.Contains(string(autoBytes), "target = \"nas\"") || strings.Contains(string(autoBytes), "ssh_only") {
		t.Errorf("auto server target serialization = %s", autoBytes)
	}

	// No leftover temp files in the config directory.
	entries, err := os.ReadDir(tmp)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Fatalf("leftover temp file: %s", e.Name())
		}
	}

	// The auto file round-trips through LoadConfig with values intact.
	cfg, err := LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	got, ok := cfg.Servers["creative"]
	if !ok {
		t.Fatal("creative server missing after reload")
	}
	if got.ContainerName != "creative-mc-1" || got.RconPassword != "secret" || !got.PauseIfNoPlayers {
		t.Errorf("creative server not persisted correctly: %#v", got)
	}
}

func TestSaveAutoServersEmptyRemovesFile(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "config.toml")
	autoPath := autoServersPath(cfgPath)
	if err := os.WriteFile(cfgPath, []byte("[global]\n"), 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := os.WriteFile(autoPath, []byte("[server.creative]\nenabled = true\n"), 0600); err != nil {
		t.Fatalf("write auto: %v", err)
	}

	if err := SaveAutoServers(cfgPath, map[string]ServerConfig{}); err != nil {
		t.Fatalf("SaveAutoServers: %v", err)
	}

	if _, err := os.Stat(autoPath); !os.IsNotExist(err) {
		t.Fatalf("auto file should be removed, got err=%v", err)
	}
}

func TestSaveAutoServersPermissions(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "config.toml")
	autoPath := autoServersPath(cfgPath)

	if err := os.WriteFile(cfgPath, []byte("[global]\n"), 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	servers := map[string]ServerConfig{
		"creative": {
			Enabled:       true,
			ContainerName: "creative-mc-1",
			RconPassword:  "secret",
		},
	}

	// Initial creation check
	if err := SaveAutoServers(cfgPath, servers); err != nil {
		t.Fatalf("SaveAutoServers creation failed: %v", err)
	}

	info, err := os.Stat(autoPath)
	if err != nil {
		t.Fatalf("stat auto file: %v", err)
	}

	if perm := info.Mode().Perm(); perm&0077 != 0 {
		t.Errorf("created auto file permissions = %04o, want no group/other bits (perm & 0077 == 0)", perm)
	}

	// Pre-create auto file with permissive mode (0666) to test that replacement resets permissions
	//nolint:gosec // G306: test intentionally creates file with loose permissions to verify permission fixing
	if err := os.WriteFile(autoPath, []byte("loose permissions file"), 0666); err != nil {
		t.Fatalf("pre-write auto file: %v", err)
	}

	if err := SaveAutoServers(cfgPath, servers); err != nil {
		t.Fatalf("SaveAutoServers replacement failed: %v", err)
	}

	info, err = os.Stat(autoPath)
	if err != nil {
		t.Fatalf("stat auto file after replace: %v", err)
	}

	if perm := info.Mode().Perm(); perm&0077 != 0 {
		t.Errorf("replaced auto file permissions = %04o, want no group/other bits (perm & 0077 == 0)", perm)
	}

	// Verify atomic write behavior (no leftover .tmp files)
	entries, err := os.ReadDir(tmp)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Fatalf("leftover temp file: %s", e.Name())
		}
	}
}

func TestAPITokenConfig(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "config.toml")

	content := []byte(`
[global]
listen_addr = "127.0.0.1:47990"
api_token = "toml-secret"
`)
	if err := os.WriteFile(cfgPath, content, 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Global.APIToken != "toml-secret" {
		t.Errorf("APIToken from TOML = %q, want %q", cfg.Global.APIToken, "toml-secret")
	}
	if got := GetConfigValue(cfg, "global.api_token"); got != "toml-secret" {
		t.Errorf("GetConfigValue = %q, want %q", got, "toml-secret")
	}

	t.Setenv("MC_BACKUP_GLOBAL_API_TOKEN", "env-secret")
	cfgEnv, err := LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig with env: %v", err)
	}
	if cfgEnv.Global.APIToken != "env-secret" {
		t.Errorf("APIToken from env = %q, want %q", cfgEnv.Global.APIToken, "env-secret")
	}

	if err := SetConfigValue(cfgPath, "global.api_token", "new-secret"); err != nil {
		t.Fatalf("SetConfigValue: %v", err)
	}
	os.Unsetenv("MC_BACKUP_GLOBAL_API_TOKEN")
	cfgSaved, err := LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig saved: %v", err)
	}
	if cfgSaved.Global.APIToken != "new-secret" {
		t.Errorf("APIToken after set = %q, want %q", cfgSaved.Global.APIToken, "new-secret")
	}
}

func TestConfigValidation(t *testing.T) {
	tests := []struct {
		name        string
		listenAddr  string
		apiToken    string
		wantErr     bool
		errContains string
	}{
		{
			name:       "valid loopback ipv4 without token",
			listenAddr: "127.0.0.1:47990",
			apiToken:   "",
			wantErr:    false,
		},
		{
			name:       "valid loopback localhost without token",
			listenAddr: "localhost:47990",
			apiToken:   "",
			wantErr:    false,
		},
		{
			name:       "valid loopback ipv6 without token",
			listenAddr: "[::1]:47990",
			apiToken:   "",
			wantErr:    false,
		},
		{
			name:       "valid loopback ipv4 with token",
			listenAddr: "127.0.0.1:47990",
			apiToken:   "secret",
			wantErr:    false,
		},
		{
			name:       "valid non-loopback 0.0.0.0 with token",
			listenAddr: "0.0.0.0:47990",
			apiToken:   "secret",
			wantErr:    false,
		},
		{
			name:       "valid non-loopback specific ip with token",
			listenAddr: "192.168.1.100:47990",
			apiToken:   "secret",
			wantErr:    false,
		},
		{
			name:        "invalid non-loopback 0.0.0.0 without token",
			listenAddr:  "0.0.0.0:47990",
			apiToken:    "",
			wantErr:     true,
			errContains: "invalid config:",
		},
		{
			name:        "invalid non-loopback colon port without token",
			listenAddr:  ":47990",
			apiToken:    "",
			wantErr:     true,
			errContains: "invalid config:",
		},
		{
			name:        "invalid malformed address no port",
			listenAddr:  "127.0.0.1",
			apiToken:    "",
			wantErr:     true,
			errContains: "invalid config:",
		},
		{
			name:        "invalid malformed address string port",
			listenAddr:  "127.0.0.1:abc",
			apiToken:    "",
			wantErr:     true,
			errContains: "invalid config:",
		},
		{
			name:        "invalid malformed port out of range",
			listenAddr:  "127.0.0.1:999999",
			apiToken:    "",
			wantErr:     true,
			errContains: "invalid config:",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.Global.ListenAddr = tt.listenAddr
			cfg.Global.APIToken = tt.apiToken
			err := cfg.Validate()
			if (err != nil) != tt.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr && err != nil {
				if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("error %q should contain %q", err.Error(), tt.errContains)
				}
			}
		})
	}
}

func TestLoadConfigValidationIntegration(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "config.toml")

	content := []byte(`
[global]
listen_addr = "0.0.0.0:47990"
`)
	if err := os.WriteFile(cfgPath, content, 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	_, err := LoadConfig(cfgPath)
	if err == nil {
		t.Fatal("LoadConfig should fail for non-loopback listen_addr without api_token")
	}
	if !strings.Contains(err.Error(), "invalid config:") {
		t.Errorf("LoadConfig error %q should contain 'invalid config:'", err.Error())
	}
}

func TestValidateConfigNAS(t *testing.T) {
	tests := []struct {
		name        string
		local       LocalConfig
		nas         NASConfig
		servers     map[string]ServerConfig
		wantErr     bool
		errContains string
	}{
		{
			name: "missing ssh_host for nas target",
			nas: NASConfig{
				SSHUser:  "backup",
				DestRoot: "/volume1/backups",
			},
			servers: map[string]ServerConfig{
				"srv1": {Enabled: true, Target: "nas"},
			},
			wantErr:     true,
			errContains: "ssh_host",
		},
		{
			name: "missing ssh_user for nas target",
			nas: NASConfig{
				SSHHost:  "nas.local",
				DestRoot: "/volume1/backups",
			},
			servers: map[string]ServerConfig{
				"srv1": {Enabled: true, Target: "nas"},
			},
			wantErr:     true,
			errContains: "ssh_user",
		},
		{
			name: "missing dest_root for nas target",
			nas: NASConfig{
				SSHHost: "nas.local",
				SSHUser: "backup",
			},
			servers: map[string]ServerConfig{
				"srv1": {Enabled: true, Target: "nas"},
			},
			wantErr:     true,
			errContains: "dest_root",
		},
		{
			name: "all valid fields for nas target",
			nas: NASConfig{
				SSHHost:  "nas.local",
				SSHUser:  "backup",
				DestRoot: "/volume1/backups",
			},
			servers: map[string]ServerConfig{
				"srv1": {Enabled: true, Target: "nas"},
			},
			wantErr: false,
		},
		{
			name:  "local target only without nas fields is valid",
			local: LocalConfig{DestRoot: "/tmp"},
			nas:   NASConfig{},
			servers: map[string]ServerConfig{
				"srv1": {Enabled: true, Target: "local"},
			},
			wantErr: false,
		},
		{
			name: "omitted/default target defaults to nas and requires fields",
			nas:  NASConfig{},
			servers: map[string]ServerConfig{
				"srv1": {Enabled: true, Target: ""},
			},
			wantErr:     true,
			errContains: "ssh_host",
		},
		{
			name:    "no servers configured without nas fields is valid",
			nas:     NASConfig{},
			servers: map[string]ServerConfig{},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.Local = tt.local
			cfg.NAS = tt.nas
			cfg.Servers = tt.servers
			err := cfg.Validate()
			if (err != nil) != tt.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr && err != nil && tt.errContains != "" {
				if !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("error %q should contain %q", err.Error(), tt.errContains)
				}
			}
		})
	}
}

func TestLoadConfigNASValidationWithEnvOverrides(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "config.toml")

	content := []byte(`
[global]
listen_addr = "127.0.0.1:47990"

[nas]
ssh_user = "backup"
dest_root = "/volume1/backups"

[server.creative]
enabled = true
target = "nas"
`)
	if err := os.WriteFile(cfgPath, content, 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	// First load should fail because ssh_host is missing
	_, err := LoadConfig(cfgPath)
	if err == nil {
		t.Fatal("LoadConfig should fail when NAS ssh_host is missing")
	}

	// Set env override for missing NAS ssh_host
	t.Setenv("MC_BACKUP_NAS_SSH_HOST", "nas.local")

	// Now load should succeed
	cfg, err := LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig with env override failed: %v", err)
	}
	if cfg.NAS.SSHHost != "nas.local" {
		t.Errorf("cfg.NAS.SSHHost = %q, want 'nas.local'", cfg.NAS.SSHHost)
	}
}

func TestValidateActiveTargetsOnly(t *testing.T) {
	// Disabled NAS server with empty NAS section -> valid
	cfgDisabledNAS := DefaultConfig()
	cfgDisabledNAS.Servers = map[string]ServerConfig{
		"srv1": {Enabled: false, Target: "nas"},
	}
	if err := cfgDisabledNAS.Validate(); err != nil {
		t.Errorf("disabled NAS server should not require NAS config, got: %v", err)
	}

	// Disabled local server with empty local.dest_root -> valid
	cfgDisabledLocal := DefaultConfig()
	cfgDisabledLocal.Servers = map[string]ServerConfig{
		"srv1": {Enabled: false, Target: "local"},
	}
	if err := cfgDisabledLocal.Validate(); err != nil {
		t.Errorf("disabled local server should not require local.dest_root, got: %v", err)
	}

	// Enabled local server with empty local.dest_root -> invalid
	cfgEnabledLocalMissingRoot := DefaultConfig()
	cfgEnabledLocalMissingRoot.Servers = map[string]ServerConfig{
		"srv1": {Enabled: true, Target: "local"},
	}
	if err := cfgEnabledLocalMissingRoot.Validate(); err == nil {
		t.Error("enabled local server without local.dest_root should fail validation")
	} else if !strings.Contains(err.Error(), "local.dest_root") {
		t.Errorf("error %q should contain 'local.dest_root'", err.Error())
	}

	// Enabled local server with local.dest_root -> valid
	cfgEnabledLocalValid := DefaultConfig()
	cfgEnabledLocalValid.Local = LocalConfig{DestRoot: "/tmp"}
	cfgEnabledLocalValid.Servers = map[string]ServerConfig{
		"srv1": {Enabled: true, Target: "local"},
	}
	if err := cfgEnabledLocalValid.Validate(); err != nil {
		t.Errorf("enabled local server with local.dest_root failed validation: %v", err)
	}
}

func TestValidateServerTarget(t *testing.T) {
	validNAS := NASConfig{
		SSHUser:  "backup",
		SSHHost:  "nas.local",
		DestRoot: "/volume1/backups",
	}
	validLocal := LocalConfig{DestRoot: "/tmp"}

	tests := []struct {
		name        string
		serverName  string
		server      ServerConfig
		nas         NASConfig
		local       LocalConfig
		wantErr     bool
		errContains []string
	}{
		{
			name:       "valid nas target uppercase with spaces",
			serverName: "srv1",
			server:     ServerConfig{Enabled: true, Target: " NAS "},
			nas:        validNAS,
			wantErr:    false,
		},
		{
			name:       "valid local target lowercase",
			serverName: "srv1",
			server:     ServerConfig{Enabled: true, Target: "local"},
			local:      validLocal,
			wantErr:    false,
		},
		{
			name:       "valid empty target defaults to nas",
			serverName: "srv1",
			server:     ServerConfig{Enabled: true, Target: ""},
			nas:        validNAS,
			wantErr:    false,
		},
		{
			name:        "invalid target s3 on enabled server",
			serverName:  "srv1",
			server:      ServerConfig{Enabled: true, Target: "s3"},
			nas:         validNAS,
			wantErr:     true,
			errContains: []string{"srv1", "s3", "nas", "local"},
		},
		{
			name:        "invalid target preserving raw input whitespace and casing",
			serverName:  "srv_creative",
			server:      ServerConfig{Enabled: true, Target: " InvalidTarget "},
			nas:         validNAS,
			wantErr:     true,
			errContains: []string{"srv_creative", " InvalidTarget ", "nas", "local"},
		},
		{
			name:       "disabled server with invalid target is ignored",
			serverName: "srv_disabled",
			server:     ServerConfig{Enabled: false, Target: " invalid_target "},
			wantErr:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.NAS = tt.nas
			cfg.Local = tt.local
			cfg.Servers = map[string]ServerConfig{tt.serverName: tt.server}
			err := cfg.Validate()
			if (err != nil) != tt.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr && err != nil {
				for _, sub := range tt.errContains {
					if !strings.Contains(err.Error(), sub) {
						t.Errorf("error %q should contain %q", err.Error(), sub)
					}
				}
			}
		})
	}
}

func TestLoadConfigInvalidTargetIntegration(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "config.toml")

	content := []byte(`
[global]
listen_addr = "127.0.0.1:47990"

[server.creative]
enabled = true
target = " Invalid "
`)
	if err := os.WriteFile(cfgPath, content, 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	_, err := LoadConfig(cfgPath)
	if err == nil {
		t.Fatal("LoadConfig should fail for invalid server target")
	}
	for _, sub := range []string{"creative", " Invalid ", "nas", "local"} {
		if !strings.Contains(err.Error(), sub) {
			t.Errorf("LoadConfig error %q should contain %q", err.Error(), sub)
		}
	}
}

func TestResolveBackupTargetAndValidationNormalization(t *testing.T) {
	// Whitespace and case normalization in resolveBackupTarget
	got, err := resolveBackupTarget("creative", ServerConfig{Target: " NAS "}, LocalConfig{})
	if err != nil || got != "nas" {
		t.Errorf("resolveBackupTarget(' NAS ') = %q, %v; want 'nas', nil", got, err)
	}

	got, err = resolveBackupTarget("creative", ServerConfig{Target: " LOCAL "}, LocalConfig{DestRoot: "/tmp"})
	if err != nil || got != "local" {
		t.Errorf("resolveBackupTarget(' LOCAL ') = %q, %v; want 'local', nil", got, err)
	}

	// Normalization in Validate()
	cfg := DefaultConfig()
	cfg.NAS = NASConfig{
		SSHUser:  "backup",
		SSHHost:  "nas.local",
		DestRoot: "/volume1/backups",
	}
	cfg.Servers = map[string]ServerConfig{
		"srv1": {Enabled: true, Target: " Nas "},
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("Validate with target ' Nas ' failed: %v", err)
	}
}

func TestNASDestRootEnvOverrideNormalized(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "config.toml")
	content := []byte(`
[global]
listen_addr = "127.0.0.1:47990"

[nas]
ssh_user = "backup"
ssh_host = "nas.local"
dest_root = "/volume1/backups"

[server.creative]
enabled = true
target = "nas"
`)
	if err := os.WriteFile(cfgPath, content, 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	t.Setenv("MC_BACKUP_NAS_DEST_ROOT", "/volume2/backups/")

	cfg, err := LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.NAS.DestRoot != "/volume2/backups" {
		t.Errorf("cfg.NAS.DestRoot = %q, want '/volume2/backups' (normalized without trailing slash)", cfg.NAS.DestRoot)
	}
}

func TestLoadConfigDisabledAutoServer(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "config.toml")
	autoPath := autoServersPath(cfgPath)

	if err := os.WriteFile(cfgPath, []byte("[global]\nlisten_addr = \"127.0.0.1:47990\"\n"), 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	// Disabled auto server with default/empty target
	if err := os.WriteFile(autoPath, []byte("[server.creative]\nenabled = false\ncontainer_name = \"creative-mc-1\"\n"), 0600); err != nil {
		t.Fatalf("write auto config: %v", err)
	}

	cfg, err := LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig should succeed for disabled auto server without NAS config, got: %v", err)
	}
	if cfg.Servers["creative"].Enabled {
		t.Error("creative server should be disabled")
	}
}

func TestExcludesResolution(t *testing.T) {
	// 1. Omitted global and server -> default excludes
	cfgDefault := &Config{}
	resDefault := cfgDefault.ResolveServerExcludes(ServerConfig{})
	wantDefault := []string{"*.jar", "cache", "logs", "*.tmp"}
	if strings.Join(resDefault, ",") != strings.Join(wantDefault, ",") {
		t.Errorf("default server excludes = %v, want %v", resDefault, wantDefault)
	}

	// 2. Global excludes configured, server omitted -> global excludes
	globalList := []string{"*.bak", "temp"}
	cfgGlobal := &Config{
		Global: GlobalConfig{Excludes: &globalList},
	}
	resGlobal := cfgGlobal.ResolveServerExcludes(ServerConfig{})
	if strings.Join(resGlobal, ",") != "%.bak,temp" && strings.Join(resGlobal, ",") != "*.bak,temp" {
		t.Errorf("server inheriting global excludes = %v, want %v", resGlobal, globalList)
	}

	// 3. Server explicitly configured with list -> server excludes
	serverList := []string{"*.iso"}
	resServer := cfgGlobal.ResolveServerExcludes(ServerConfig{Excludes: &serverList})
	if strings.Join(resServer, ",") != "*.iso" {
		t.Errorf("server explicit excludes = %v, want [*.iso]", resServer)
	}

	// 4. Explicit empty server excludes -> empty slice (no excludes)
	emptyList := []string{}
	resExplicitEmpty := cfgGlobal.ResolveServerExcludes(ServerConfig{Excludes: &emptyList})
	if resExplicitEmpty == nil || len(resExplicitEmpty) != 0 {
		t.Errorf("explicit empty server excludes = %v (len %d), want empty slice (len 0)", resExplicitEmpty, len(resExplicitEmpty))
	}

	// 5. Explicit empty global excludes with omitted server -> empty slice
	cfgGlobalEmpty := &Config{
		Global: GlobalConfig{Excludes: &emptyList},
	}
	resGlobalEmpty := cfgGlobalEmpty.ResolveServerExcludes(ServerConfig{})
	if resGlobalEmpty == nil || len(resGlobalEmpty) != 0 {
		t.Errorf("explicit empty global excludes = %v (len %d), want empty slice (len 0)", resGlobalEmpty, len(resGlobalEmpty))
	}
}

func TestExcludesTOMLRoundTripAndNilPreservation(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "config.toml")

	content := []byte(`
[global]
listen_addr = "127.0.0.1:47990"
excludes = ["*.tmp", "cache"]

[nas]
ssh_user = "backup"
ssh_host = "nas.local"
dest_root = "/volume1/backups"

[server.creative]
enabled = true
excludes = []

[server.survival]
enabled = true
`)
	if err := os.WriteFile(cfgPath, content, 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	if cfg.Global.Excludes == nil || strings.Join(*cfg.Global.Excludes, ",") != "*.tmp,cache" {
		t.Errorf("global excludes = %v, want [*.tmp, cache]", cfg.Global.Excludes)
	}

	sCreative, ok := cfg.Servers["creative"]
	if !ok || sCreative.Excludes == nil {
		t.Fatalf("creative server excludes should be non-nil pointer, got %#v", sCreative)
	}
	if len(*sCreative.Excludes) != 0 {
		t.Errorf("creative server excludes len = %d, want 0", len(*sCreative.Excludes))
	}

	sSurvival, ok := cfg.Servers["survival"]
	if !ok {
		t.Fatal("survival server missing")
	}
	if sSurvival.Excludes != nil {
		t.Errorf("survival server excludes = %v, want nil", sSurvival.Excludes)
	}

	// Save auto servers and test auto file round-trip
	autoServers := map[string]ServerConfig{
		"creative": sCreative,
		"survival": sSurvival,
	}
	if err := SaveAutoServers(cfgPath, autoServers); err != nil {
		t.Fatalf("SaveAutoServers: %v", err)
	}

	reloaded, err := LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig reloaded: %v", err)
	}

	sCreativeReloaded := reloaded.Servers["creative"]
	if sCreativeReloaded.Excludes == nil || len(*sCreativeReloaded.Excludes) != 0 {
		t.Errorf("reloaded creative excludes = %v, want non-nil empty", sCreativeReloaded.Excludes)
	}

	sSurvivalReloaded := reloaded.Servers["survival"]
	if sSurvivalReloaded.Excludes != nil {
		t.Errorf("reloaded survival excludes = %v, want nil", sSurvivalReloaded.Excludes)
	}
}

func TestExcludesEscapingInSaveAutoServers(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "config.toml")
	if err := os.WriteFile(cfgPath, []byte("[global]\nlisten_addr = \"127.0.0.1:47990\"\n\n[nas]\nssh_user = \"backup\"\nssh_host = \"nas.local\"\ndest_root = \"/volume1/backups\"\n"), 0600); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	ex := []string{"*.jar", "file with \"quotes\" and \\backslash", "logs,cache"}
	servers := map[string]ServerConfig{
		"creative": {
			Enabled:  true,
			Excludes: &ex,
		},
	}

	if err := SaveAutoServers(cfgPath, servers); err != nil {
		t.Fatalf("SaveAutoServers: %v", err)
	}

	reloaded, err := LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	got := reloaded.Servers["creative"].Excludes
	if got == nil {
		t.Fatal("reloaded excludes is nil")
	}
	if len(*got) != 3 || (*got)[0] != "*.jar" || (*got)[1] != "file with \"quotes\" and \\backslash" || (*got)[2] != "logs,cache" {
		t.Errorf("escaped excludes round trip failed, got %#v", *got)
	}
}

func TestExcludesEnvOverrides(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "config.toml")
	content := []byte(`
[global]
listen_addr = "127.0.0.1:47990"

[local]
dest_root = "/tmp"

[server.creative]
enabled = true
target = "local"

[server.survival]
enabled = true
target = "local"
excludes = ["*.tmp"]
`)
	if err := os.WriteFile(cfgPath, content, 0600); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	t.Setenv("MC_BACKUP_GLOBAL_EXCLUDES", "*.jar, cache")
	t.Setenv("MC_BACKUP_SERVER_CREATIVE_EXCLUDES", "none")
	t.Setenv("MC_BACKUP_SERVER_SURVIVAL_EXCLUDES", "inherit")

	cfg, err := LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	if cfg.Global.Excludes == nil || strings.Join(*cfg.Global.Excludes, ",") != "*.jar,cache" {
		t.Errorf("global excludes env override = %v, want [*.jar, cache]", cfg.Global.Excludes)
	}

	sCreative := cfg.Servers["creative"]
	if sCreative.Excludes == nil || len(*sCreative.Excludes) != 0 {
		t.Errorf("creative excludes env override 'none' = %v, want non-nil empty", sCreative.Excludes)
	}

	sSurvival := cfg.Servers["survival"]
	if sSurvival.Excludes != nil {
		t.Errorf("survival excludes env override 'inherit' = %v, want nil", sSurvival.Excludes)
	}
}

func TestExcludesGetSetConfig(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "config.toml")
	content := []byte(`
[global]
listen_addr = "127.0.0.1:47990"

[local]
dest_root = "/tmp"

[server.creative]
enabled = true
target = "local"
`)
	if err := os.WriteFile(cfgPath, content, 0600); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	cfg, err := LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	if got := GetConfigValue(cfg, "global.excludes"); got != "default" {
		t.Errorf("GetConfigValue global.excludes = %q, want %q", got, "default")
	}
	if got := GetConfigValue(cfg, "server.creative.excludes"); got != "inherit" {
		t.Errorf("GetConfigValue server.creative.excludes = %q, want %q", got, "inherit")
	}
	if got := GetConfigValue(cfg, "server.nonexistent.excludes"); got != "" {
		t.Errorf("GetConfigValue absent server = %q, want empty", got)
	}

	// Set global excludes to list
	if err := SetConfigValue(cfgPath, "global.excludes", "*.jar, cache"); err != nil {
		t.Fatalf("SetConfigValue global.excludes: %v", err)
	}

	// Set creative excludes to none
	if err := SetConfigValue(cfgPath, "server.creative.excludes", "none"); err != nil {
		t.Fatalf("SetConfigValue server.creative.excludes: %v", err)
	}

	reloaded, err := LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig reloaded: %v", err)
	}

	if got := GetConfigValue(reloaded, "global.excludes"); got != "*.jar,cache" {
		t.Errorf("GetConfigValue after set = %q, want %q", got, "*.jar,cache")
	}
	if got := GetConfigValue(reloaded, "server.creative.excludes"); got != "none" {
		t.Errorf("GetConfigValue creative after set = %q, want %q", got, "none")
	}

	// Reset creative excludes to inherit
	if err := SetConfigValue(cfgPath, "server.creative.excludes", "inherit"); err != nil {
		t.Fatalf("SetConfigValue inherit: %v", err)
	}

	reloaded2, err := LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig reloaded2: %v", err)
	}

	if got := GetConfigValue(reloaded2, "server.creative.excludes"); got != "inherit" {
		t.Errorf("GetConfigValue creative after inherit = %q, want %q", got, "inherit")
	}
}

func TestGetConfigValueNormalizesServerName(t *testing.T) {
	ex := []string{"*.tmp"}
	cfg := &Config{
		Servers: map[string]ServerConfig{
			"creative": {
				Enabled:  true,
				Target:   "local",
				Excludes: &ex,
			},
		},
	}

	if got := GetConfigValue(cfg, "server.Creative.excludes"); got != "*.tmp" {
		t.Errorf("GetConfigValue server.Creative.excludes = %q, want %q", got, "*.tmp")
	}
	if got := GetConfigValue(cfg, "server.Creative.target"); got != "local" {
		t.Errorf("GetConfigValue server.Creative.target = %q, want %q", got, "local")
	}
	if got := GetConfigValue(cfg, "server.Creative.enabled"); got != "true" {
		t.Errorf("GetConfigValue server.Creative.enabled = %q, want %q", got, "true")
	}
}

func TestRetentionEnvOverridesAndGettersSetters(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "config.toml")
	content := []byte(`
[global]
listen_addr = "127.0.0.1:47990"
backup_interval = "12h"
initial_delay = "1m"
max_mbps = 50.0

[local]
dest_root = "/backups/local"

[nas]
ssh_user = "user1"
ssh_host = "host1"
ssh_port = 22
ssh_key = "key1"
dest_root = "/backups/nas"

[retention]
prune_days = 7
prune_count = 14

[server.creative]
enabled = true
target = "local"
container_name = "creative-mc-1"
rcon_password = "pass"
data_dir = "/data"
pause_if_no_players = false
`)
	if err := os.WriteFile(cfgPath, content, 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	t.Setenv("MC_BACKUP_RETENTION_PRUNE_DAYS", "30")
	t.Setenv("MC_BACKUP_RETENTION_PRUNE_COUNT", "50")
	t.Setenv("MC_BACKUP_GLOBAL_LISTEN_ADDR", "127.0.0.1:47991")
	t.Setenv("MC_BACKUP_GLOBAL_MAX_MBPS", "100.0")
	t.Setenv("MC_BACKUP_GLOBAL_BACKUP_INTERVAL", "6h")
	t.Setenv("MC_BACKUP_GLOBAL_INITIAL_DELAY", "30s")
	t.Setenv("MC_BACKUP_NAS_SSH_USER", "newuser")
	t.Setenv("MC_BACKUP_NAS_SSH_KEY", "/new/key")
	t.Setenv("MC_BACKUP_NAS_DEST_ROOT", "/new/nas")
	t.Setenv("MC_BACKUP_SERVER_CREATIVE_ENABLED", "false")
	t.Setenv("MC_BACKUP_SERVER_CREATIVE_CONTAINER_NAME", "new-container")
	t.Setenv("MC_BACKUP_SERVER_CREATIVE_DATA_DIR", "/new/data")

	cfg, err := LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	if cfg.Retention.PruneDays != 30 {
		t.Errorf("PruneDays env override = %d, want 30", cfg.Retention.PruneDays)
	}
	if cfg.Retention.PruneCount != 50 {
		t.Errorf("PruneCount env override = %d, want 50", cfg.Retention.PruneCount)
	}
	if cfg.Global.ListenAddr != "127.0.0.1:47991" {
		t.Errorf("ListenAddr env override = %q", cfg.Global.ListenAddr)
	}
	if cfg.Global.MaxMBps != 100.0 {
		t.Errorf("MaxMBps env override = %v", cfg.Global.MaxMBps)
	}
	if cfg.Global.BackupInterval.Duration != 6*time.Hour {
		t.Errorf("BackupInterval env override = %v", cfg.Global.BackupInterval.Duration)
	}
	if cfg.Global.InitialDelay.Duration != 30*time.Second {
		t.Errorf("InitialDelay env override = %v", cfg.Global.InitialDelay.Duration)
	}

	// Test GetConfigValue for all sections
	if got := GetConfigValue(cfg, "global.listen_addr"); got != "127.0.0.1:47991" {
		t.Errorf("GetConfigValue global.listen_addr = %q", got)
	}
	if got := GetConfigValue(cfg, "global.max_mbps"); got != "100.0" {
		t.Errorf("GetConfigValue global.max_mbps = %q", got)
	}
	if got := GetConfigValue(cfg, "global.backup_interval"); got != "6h0m0s" {
		t.Errorf("GetConfigValue global.backup_interval = %q", got)
	}
	if got := GetConfigValue(cfg, "global.initial_delay"); got != "30s" {
		t.Errorf("GetConfigValue global.initial_delay = %q", got)
	}
	if got := GetConfigValue(cfg, "local.dest_root"); got != "/backups/local" {
		t.Errorf("GetConfigValue local.dest_root = %q", got)
	}
	if got := GetConfigValue(cfg, "nas.ssh_user"); got != "newuser" {
		t.Errorf("GetConfigValue nas.ssh_user = %q", got)
	}
	if got := GetConfigValue(cfg, "nas.ssh_host"); got != "host1" {
		t.Errorf("GetConfigValue nas.ssh_host = %q", got)
	}
	if got := GetConfigValue(cfg, "nas.ssh_port"); got != "22" {
		t.Errorf("GetConfigValue nas.ssh_port = %q", got)
	}
	if got := GetConfigValue(cfg, "nas.ssh_key"); got != "/new/key" {
		t.Errorf("GetConfigValue nas.ssh_key = %q", got)
	}
	if got := GetConfigValue(cfg, "nas.dest_root"); got != "/new/nas" {
		t.Errorf("GetConfigValue nas.dest_root = %q", got)
	}
	if got := GetConfigValue(cfg, "retention.prune_days"); got != "30" {
		t.Errorf("GetConfigValue retention.prune_days = %q", got)
	}
	if got := GetConfigValue(cfg, "retention.prune_count"); got != "50" {
		t.Errorf("GetConfigValue retention.prune_count = %q", got)
	}
	if got := GetConfigValue(cfg, "server.creative.enabled"); got != "false" {
		t.Errorf("GetConfigValue server.creative.enabled = %q", got)
	}
	if got := GetConfigValue(cfg, "server.creative.container_name"); got != "new-container" {
		t.Errorf("GetConfigValue server.creative.container_name = %q", got)
	}
	if got := GetConfigValue(cfg, "server.creative.rcon_password"); got != "pass" {
		t.Errorf("GetConfigValue server.creative.rcon_password = %q", got)
	}
	if got := GetConfigValue(cfg, "server.creative.data_dir"); got != "/new/data" {
		t.Errorf("GetConfigValue server.creative.data_dir = %q", got)
	}
	if got := GetConfigValue(cfg, "server.creative.pause_if_no_players"); got != "false" {
		t.Errorf("GetConfigValue server.creative.pause_if_no_players = %q", got)
	}
	if got := GetConfigValue(cfg, "invalid"); got != "" {
		t.Errorf("GetConfigValue single-part key = %q, want empty", got)
	}
	if got := GetConfigValue(cfg, "unknown.field"); got != "" {
		t.Errorf("GetConfigValue unknown section = %q, want empty", got)
	}

	// Unset env overrides so SetConfigValue round-trip can be verified
	os.Unsetenv("MC_BACKUP_RETENTION_PRUNE_DAYS")
	os.Unsetenv("MC_BACKUP_RETENTION_PRUNE_COUNT")
	os.Unsetenv("MC_BACKUP_GLOBAL_LISTEN_ADDR")
	os.Unsetenv("MC_BACKUP_GLOBAL_MAX_MBPS")
	os.Unsetenv("MC_BACKUP_GLOBAL_BACKUP_INTERVAL")
	os.Unsetenv("MC_BACKUP_GLOBAL_INITIAL_DELAY")
	os.Unsetenv("MC_BACKUP_NAS_SSH_USER")
	os.Unsetenv("MC_BACKUP_NAS_SSH_KEY")
	os.Unsetenv("MC_BACKUP_NAS_DEST_ROOT")
	os.Unsetenv("MC_BACKUP_SERVER_CREATIVE_ENABLED")
	os.Unsetenv("MC_BACKUP_SERVER_CREATIVE_CONTAINER_NAME")
	os.Unsetenv("MC_BACKUP_SERVER_CREATIVE_DATA_DIR")

	// Test SetConfigValue for retention, nas, local
	if err := SetConfigValue(cfgPath, "retention.prune_days", "15"); err != nil {
		t.Fatalf("SetConfigValue retention.prune_days: %v", err)
	}
	if err := SetConfigValue(cfgPath, "nas.ssh_port", "2222"); err != nil {
		t.Fatalf("SetConfigValue nas.ssh_port: %v", err)
	}
	if err := SetConfigValue(cfgPath, "local.dest_root", "/new/local"); err != nil {
		t.Fatalf("SetConfigValue local.dest_root: %v", err)
	}

	reloaded, err := LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	if reloaded.Retention.PruneDays != 15 {
		t.Errorf("reloaded PruneDays = %d, want 15", reloaded.Retention.PruneDays)
	}
	if reloaded.NAS.SSHPort != 2222 {
		t.Errorf("reloaded SSHPort = %d, want 2222", reloaded.NAS.SSHPort)
	}
	if reloaded.Local.DestRoot != "/new/local" {
		t.Errorf("reloaded DestRoot = %q, want /new/local", reloaded.Local.DestRoot)
	}
}

func TestSetConfigValueValidation(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "config.toml")
	autoPath := autoServersPath(cfgPath)

	mainContent := []byte("[global]\nlisten_addr = \"127.0.0.1:47990\"\n\n[local]\ndest_root = \"/tmp\"\n")
	autoContent := []byte("[server.creative]\nenabled = true\ntarget = \"local\"\ncontainer_name = \"creative-mc-1\"\n")

	if err := os.WriteFile(cfgPath, mainContent, 0600); err != nil {
		t.Fatalf("write main config: %v", err)
	}
	if err := os.WriteFile(autoPath, autoContent, 0600); err != nil {
		t.Fatalf("write auto config: %v", err)
	}

	mainBefore, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read main before: %v", err)
	}
	autoBefore, err := os.ReadFile(autoPath)
	if err != nil {
		t.Fatalf("read auto before: %v", err)
	}

	// 1. Attempt non-loopback listen address without token -> expect error and no file changes
	err = SetConfigValue(cfgPath, "global.listen_addr", "0.0.0.0:47990")
	if err == nil {
		t.Fatal("SetConfigValue should fail for non-loopback listen_addr without api_token")
	}

	mainAfter1, _ := os.ReadFile(cfgPath)
	autoAfter1, _ := os.ReadFile(autoPath)
	if !bytes.Equal(mainBefore, mainAfter1) {
		t.Errorf("main config file changed after invalid listen_addr update:\nBefore: %s\nAfter: %s", mainBefore, mainAfter1)
	}
	if !bytes.Equal(autoBefore, autoAfter1) {
		t.Errorf("auto config file changed after invalid listen_addr update:\nBefore: %s\nAfter: %s", autoBefore, autoAfter1)
	}
	if _, err := LoadConfig(cfgPath); err != nil {
		t.Fatalf("LoadConfig failed after rejected update: %v", err)
	}

	// 2. Attempt invalid server target -> expect error and no file changes
	err = SetConfigValue(cfgPath, "server.creative.target", "s3")
	if err == nil {
		t.Fatal("SetConfigValue should fail for invalid server target")
	}

	mainAfter2, _ := os.ReadFile(cfgPath)
	autoAfter2, _ := os.ReadFile(autoPath)
	if !bytes.Equal(mainBefore, mainAfter2) {
		t.Errorf("main config file changed after invalid target update:\nBefore: %s\nAfter: %s", mainBefore, mainAfter2)
	}
	if !bytes.Equal(autoBefore, autoAfter2) {
		t.Errorf("auto config file changed after invalid target update:\nBefore: %s\nAfter: %s", autoBefore, autoAfter2)
	}
	if _, err := LoadConfig(cfgPath); err != nil {
		t.Fatalf("LoadConfig failed after rejected update: %v", err)
	}

	// 3. Valid update ordering: set api_token first, then set non-loopback listen_addr -> should succeed
	if err := SetConfigValue(cfgPath, "global.api_token", "secret123"); err != nil {
		t.Fatalf("SetConfigValue api_token: %v", err)
	}
	if err := SetConfigValue(cfgPath, "global.listen_addr", "0.0.0.0:47990"); err != nil {
		t.Fatalf("SetConfigValue listen_addr with token: %v", err)
	}

	reloaded, err := LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig after valid updates: %v", err)
	}
	if reloaded.Global.ListenAddr != "0.0.0.0:47990" || reloaded.Global.APIToken != "secret123" {
		t.Errorf("reloaded config mismatch: listen_addr=%q api_token=%q", reloaded.Global.ListenAddr, reloaded.Global.APIToken)
	}

	// 4. Invalid key / unknown section error handling
	if err := SetConfigValue(cfgPath, "singlekey", "val"); err == nil {
		t.Error("expected error for single key, got nil")
	}
	if err := SetConfigValue(cfgPath, "server.creative", "val"); err == nil {
		t.Error("expected error for missing server field, got nil")
	}
	if err := SetConfigValue(cfgPath, "unknownsection.field", "val"); err == nil {
		t.Error("expected error for unknown section, got nil")
	}
}

func TestSetConfigValueStrictValidation(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "config.toml")
	autoPath := autoServersPath(cfgPath)

	mainContent := []byte("[global]\nlisten_addr = \"127.0.0.1:47990\"\n\n[local]\ndest_root = \"/tmp\"\n")
	autoContent := []byte("[server.creative]\nenabled = true\ntarget = \"local\"\ncontainer_name = \"creative-mc-1\"\n")

	if err := os.WriteFile(cfgPath, mainContent, 0600); err != nil {
		t.Fatalf("write main config: %v", err)
	}
	if err := os.WriteFile(autoPath, autoContent, 0600); err != nil {
		t.Fatalf("write auto config: %v", err)
	}

	invalidCases := []struct {
		name string
		key  string
		val  string
	}{
		// Unknown section fields
		{"unknown global field", "global.unknown_field", "val"},
		{"unknown nas field", "nas.unknown_field", "val"},
		{"unknown local field", "local.unknown_field", "val"},
		{"unknown retention field", "retention.unknown_field", "val"},
		{"unknown server field", "server.creative.unknown_field", "val"},

		// Invalid values & bounds
		{"invalid duration backup_interval", "global.backup_interval", "not_a_duration"},
		{"invalid duration initial_delay", "global.initial_delay", "bad_delay"},
		{"invalid float max_mbps str", "global.max_mbps", "not_a_float"},
		{"negative float max_mbps", "global.max_mbps", "-5.0"},
		{"invalid port str", "nas.ssh_port", "not_a_port"},
		{"zero port", "nas.ssh_port", "0"},
		{"out of range port", "nas.ssh_port", "70000"},
		{"negative port", "nas.ssh_port", "-22"},
		{"invalid int prune_days", "retention.prune_days", "bad_int"},
		{"negative prune_days", "retention.prune_days", "-1"},
		{"invalid int prune_count", "retention.prune_count", "bad_int"},
		{"negative prune_count", "retention.prune_count", "-1"},
		{"invalid bool enabled", "server.creative.enabled", "not_a_bool"},
		{"invalid bool pause_if_no_players", "server.creative.pause_if_no_players", "not_a_bool"},
	}

	for _, tc := range invalidCases {
		t.Run(tc.name, func(t *testing.T) {
			mainBefore, _ := os.ReadFile(cfgPath)
			autoBefore, _ := os.ReadFile(autoPath)

			err := SetConfigValue(cfgPath, tc.key, tc.val)
			if err == nil {
				t.Fatalf("SetConfigValue(%q, %q) expected error, got nil", tc.key, tc.val)
			}

			mainAfter, _ := os.ReadFile(cfgPath)
			autoAfter, _ := os.ReadFile(autoPath)

			if !bytes.Equal(mainBefore, mainAfter) {
				t.Errorf("main config changed on invalid update %s: before=%s after=%s", tc.name, mainBefore, mainAfter)
			}
			if !bytes.Equal(autoBefore, autoAfter) {
				t.Errorf("auto config changed on invalid update %s: before=%s after=%s", tc.name, autoBefore, autoAfter)
			}
		})
	}
}

func TestApplyEnvOverridesIgnoresInvalidValues(t *testing.T) {
	cfg := &Config{
		Global: GlobalConfig{
			MaxMBps:        40.0,
			BackupInterval: Duration{24 * time.Hour},
		},
		NAS: NASConfig{
			SSHPort: 22,
		},
		Retention: RetentionConfig{
			PruneDays: 7,
		},
		Servers: map[string]ServerConfig{
			"creative": {Enabled: true},
		},
	}

	t.Setenv("MC_BACKUP_GLOBAL_MAX_MBPS", "invalid_float")
	t.Setenv("MC_BACKUP_GLOBAL_BACKUP_INTERVAL", "invalid_duration")
	t.Setenv("MC_BACKUP_NAS_SSH_PORT", "99999")
	t.Setenv("MC_BACKUP_RETENTION_PRUNE_DAYS", "-5")
	t.Setenv("MC_BACKUP_SERVER_CREATIVE_ENABLED", "not_a_bool")

	applyEnvOverrides(cfg)

	if cfg.Global.MaxMBps != 40.0 {
		t.Errorf("MaxMBps changed on invalid env override: got %f, want 40.0", cfg.Global.MaxMBps)
	}
	if cfg.Global.BackupInterval.Duration != 24*time.Hour {
		t.Errorf("BackupInterval changed on invalid env override: got %v, want 24h", cfg.Global.BackupInterval.Duration)
	}
	if cfg.NAS.SSHPort != 22 {
		t.Errorf("SSHPort changed on invalid env override: got %d, want 22", cfg.NAS.SSHPort)
	}
	if cfg.Retention.PruneDays != 7 {
		t.Errorf("PruneDays changed on invalid env override: got %d, want 7", cfg.Retention.PruneDays)
	}
	if !cfg.Servers["creative"].Enabled {
		t.Errorf("Enabled changed on invalid env override: got false, want true")
	}
}

func TestSaveSplitRollbackOnAutoFailure(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "config.toml")
	autoPath := autoServersPath(cfgPath)

	mainContent := []byte("[global]\nlisten_addr = \"127.0.0.1:47990\"\n\n[local]\ndest_root = \"/tmp\"\n")
	autoContent := []byte("[server.creative]\nenabled = true\ntarget = \"local\"\ncontainer_name = \"creative-mc-1\"\n")

	if err := os.WriteFile(cfgPath, mainContent, 0600); err != nil {
		t.Fatalf("write main config: %v", err)
	}
	if err := os.WriteFile(autoPath, autoContent, 0600); err != nil {
		t.Fatalf("write auto config: %v", err)
	}

	mainBefore, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	autoBefore, err := os.ReadFile(autoPath)
	if err != nil {
		t.Fatal(err)
	}

	cfg, err := loadConfigFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Global.MaxMBps = 99.0

	// Inject seam failure on replacing auto sidecar
	origRename := renameFile
	failedOnce := false
	renameFile = func(src, dst string) error {
		if !failedOnce && strings.HasSuffix(dst, "-auto.toml") {
			failedOnce = true
			return errors.New("simulated auto sidecar rename failure")
		}
		return origRename(src, dst)
	}
	defer func() { renameFile = origRename }()

	err = saveSplit(cfgPath, cfg)
	if err == nil {
		t.Fatal("expected saveSplit to fail when auto sidecar replacement fails")
	}

	mainAfter, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read mainAfter: %v", err)
	}
	autoAfter, err := os.ReadFile(autoPath)
	if err != nil {
		t.Fatalf("read autoAfter: %v", err)
	}

	if !bytes.Equal(mainBefore, mainAfter) {
		t.Errorf("main config file changed after failed saveSplit:\nBefore:\n%s\nAfter:\n%s", mainBefore, mainAfter)
	}
	if !bytes.Equal(autoBefore, autoAfter) {
		t.Errorf("auto config file changed after failed saveSplit:\nBefore:\n%s\nAfter:\n%s", autoBefore, autoAfter)
	}
}

func TestSaveSplitRollbackOnAutoRemovalFailure(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "config.toml")
	autoPath := autoServersPath(cfgPath)

	mainContent := []byte("[global]\nlisten_addr = \"127.0.0.1:47990\"\n\n[local]\ndest_root = \"/tmp\"\n")
	autoContent := []byte("[server.creative]\nenabled = true\ntarget = \"local\"\ncontainer_name = \"creative-mc-1\"\n")

	if err := os.WriteFile(cfgPath, mainContent, 0600); err != nil {
		t.Fatalf("write main config: %v", err)
	}
	if err := os.WriteFile(autoPath, autoContent, 0600); err != nil {
		t.Fatalf("write auto config: %v", err)
	}

	mainBefore, _ := os.ReadFile(cfgPath)
	autoBefore, _ := os.ReadFile(autoPath)

	cfg, err := loadConfigFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	// Remove creative server so len(auto) becomes 0 and autoPath should be removed
	delete(cfg.Servers, "creative")

	origRename := renameFile
	failedOnce := false
	renameFile = func(src, dst string) error {
		if !failedOnce && src == autoPath {
			failedOnce = true
			return errors.New("simulated auto sidecar removal failure")
		}
		return origRename(src, dst)
	}
	defer func() { renameFile = origRename }()

	err = saveSplit(cfgPath, cfg)
	if err == nil {
		t.Fatal("expected saveSplit to fail when auto sidecar removal fails")
	}

	mainAfter, _ := os.ReadFile(cfgPath)
	autoAfter, _ := os.ReadFile(autoPath)

	if !bytes.Equal(mainBefore, mainAfter) {
		t.Errorf("main config changed when auto removal failed:\nBefore:\n%s\nAfter:\n%s", mainBefore, mainAfter)
	}
	if !bytes.Equal(autoBefore, autoAfter) {
		t.Errorf("auto config changed when auto removal failed:\nBefore:\n%s\nAfter:\n%s", autoBefore, autoAfter)
	}
}

func TestSaveSplitRollbackPreservesBackupOnRollbackFailure(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "config.toml")
	autoPath := autoServersPath(cfgPath)

	mainContent := []byte("[global]\nlisten_addr = \"127.0.0.1:47990\"\n\n[local]\ndest_root = \"/tmp\"\n")
	autoContent := []byte("[server.creative]\nenabled = true\ntarget = \"local\"\ncontainer_name = \"creative-mc-1\"\n")

	if err := os.WriteFile(cfgPath, mainContent, 0600); err != nil {
		t.Fatalf("write main config: %v", err)
	}
	if err := os.WriteFile(autoPath, autoContent, 0600); err != nil {
		t.Fatalf("write auto config: %v", err)
	}

	cfg, err := loadConfigFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Global.MaxMBps = 99.0

	origRename := renameFile
	renameFile = func(src, dst string) error {
		// Fail when replacing auto sidecar
		if strings.HasSuffix(dst, "-auto.toml") && !strings.Contains(src, ".bak.") {
			return errors.New("simulated auto sidecar rename failure")
		}
		// Fail when rolling back main backup to main config path
		if strings.HasSuffix(dst, "config.toml") && strings.Contains(src, ".bak.") {
			return errors.New("simulated main rollback rename failure")
		}
		return origRename(src, dst)
	}
	defer func() { renameFile = origRename }()

	err = saveSplit(cfgPath, cfg)
	if err == nil {
		t.Fatal("expected saveSplit to fail")
	}

	// 1. Error must mention both original failure and rollback failure
	errStr := err.Error()
	if !strings.Contains(errStr, "simulated auto sidecar rename failure") {
		t.Errorf("expected error to contain original failure, got: %v", errStr)
	}
	if !strings.Contains(errStr, "simulated main rollback rename failure") {
		t.Errorf("expected error to contain rollback failure, got: %v", errStr)
	}

	// 2. Original main backup must remain on disk for recovery and defer must not delete it
	entries, err := os.ReadDir(tmp)
	if err != nil {
		t.Fatal(err)
	}
	var mainBackupFile string
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".config.toml.bak.") {
			mainBackupFile = filepath.Join(tmp, entry.Name())
			break
		}
	}
	if mainBackupFile == "" {
		t.Fatal("expected main backup file to remain on disk after rollback failure, but none was found")
	}

	backupData, err := os.ReadFile(mainBackupFile)
	if err != nil {
		t.Fatalf("failed to read preserved main backup file: %v", err)
	}
	if !bytes.Equal(mainContent, backupData) {
		t.Errorf("preserved backup content mismatch:\nGot:\n%s\nWant:\n%s", backupData, mainContent)
	}
}

func TestNonPositiveBackupIntervalAndDelayValidation(t *testing.T) {
	validCfg := func() *Config {
		return &Config{
			Global: GlobalConfig{
				ListenAddr:     "127.0.0.1:47990",
				BackupInterval: Duration{1 * time.Hour},
				InitialDelay:   Duration{2 * time.Minute},
			},
		}
	}

	t.Run("zero backup interval fails validation", func(t *testing.T) {
		cfg := validCfg()
		cfg.Global.BackupInterval = Duration{0}
		if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "backup_interval") {
			t.Errorf("expected error containing 'backup_interval', got: %v", err)
		}
	})

	t.Run("negative backup interval fails validation", func(t *testing.T) {
		cfg := validCfg()
		cfg.Global.BackupInterval = Duration{-1 * time.Hour}
		if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "backup_interval") {
			t.Errorf("expected error containing 'backup_interval', got: %v", err)
		}
	})

	t.Run("negative initial delay fails validation", func(t *testing.T) {
		cfg := validCfg()
		cfg.Global.InitialDelay = Duration{-1 * time.Minute}
		if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "initial_delay") {
			t.Errorf("expected error containing 'initial_delay', got: %v", err)
		}
	})

	t.Run("zero initial delay passes validation", func(t *testing.T) {
		cfg := validCfg()
		cfg.Global.InitialDelay = Duration{0}
		if err := cfg.Validate(); err != nil {
			t.Errorf("expected zero initial delay to be valid, got: %v", err)
		}
	})
}

func TestLoadConfigAndSetConfigValueRejectsInvalidIntervalAndDelay(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "config.toml")

	writeCfg := func(content string) {
		t.Helper()
		if err := os.WriteFile(cfgPath, []byte(content), 0600); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
	}

	writeCfg("[global]\nlisten_addr = \"127.0.0.1:47990\"\nbackup_interval = \"0s\"\n")
	if _, err := LoadConfig(cfgPath); err == nil || !strings.Contains(err.Error(), "backup_interval") {
		t.Errorf("LoadConfig zero backup_interval expected error, got: %v", err)
	}

	writeCfg("[global]\nlisten_addr = \"127.0.0.1:47990\"\nbackup_interval = \"-1h\"\n")
	if _, err := LoadConfig(cfgPath); err == nil || !strings.Contains(err.Error(), "backup_interval") {
		t.Errorf("LoadConfig negative backup_interval expected error, got: %v", err)
	}

	writeCfg("[global]\nlisten_addr = \"127.0.0.1:47990\"\ninitial_delay = \"-5s\"\n")
	if _, err := LoadConfig(cfgPath); err == nil || !strings.Contains(err.Error(), "initial_delay") {
		t.Errorf("LoadConfig negative initial_delay expected error, got: %v", err)
	}

	writeCfg("[global]\nlisten_addr = \"127.0.0.1:47990\"\nbackup_interval = \"1h\"\ninitial_delay = \"0s\"\n")
	if cfg, err := LoadConfig(cfgPath); err != nil {
		t.Errorf("LoadConfig zero initial_delay unexpected error: %v", err)
	} else if cfg.Global.InitialDelay.Duration != 0 {
		t.Errorf("expected 0s initial_delay, got %v", cfg.Global.InitialDelay.Duration)
	}

	// Test SetConfigValue strict setter rejection
	if err := SetConfigValue(cfgPath, "global.backup_interval", "0s"); err == nil {
		t.Error("SetConfigValue global.backup_interval 0s expected error, got nil")
	}
	if err := SetConfigValue(cfgPath, "global.backup_interval", "-10s"); err == nil {
		t.Error("SetConfigValue global.backup_interval -10s expected error, got nil")
	}
	if err := SetConfigValue(cfgPath, "global.initial_delay", "-1s"); err == nil {
		t.Error("SetConfigValue global.initial_delay -1s expected error, got nil")
	}
	if err := SetConfigValue(cfgPath, "global.initial_delay", "0s"); err != nil {
		t.Errorf("SetConfigValue global.initial_delay 0s unexpected error: %v", err)
	}
}

func TestSaveSplitNormalizesAutoServerNames(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "config.toml")
	autoPath := autoServersPath(cfgPath)

	mainContent := []byte("[global]\nlisten_addr = \"127.0.0.1:47990\"\n\n[local]\ndest_root = \"/tmp\"\n")
	autoContent := []byte("[server.Creative]\nenabled = true\ntarget = \"local\"\ncontainer_name = \"creative-mc-1\"\nrcon_password = \"secret\"\n")

	if err := os.WriteFile(cfgPath, mainContent, 0600); err != nil {
		t.Fatalf("write main config: %v", err)
	}
	if err := os.WriteFile(autoPath, autoContent, 0600); err != nil {
		t.Fatalf("write auto config: %v", err)
	}

	// Update global value, which calls saveSplit
	if err := SetConfigValue(cfgPath, "global.max_mbps", "20"); err != nil {
		t.Fatalf("SetConfigValue: %v", err)
	}

	mainBytes, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("ReadFile main: %v", err)
	}
	if strings.Contains(strings.ToLower(string(mainBytes)), "creative") {
		t.Fatalf("mixed-case auto server leaked into main config:\n%s", mainBytes)
	}

	cfg, err := LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if s, ok := cfg.Servers["creative"]; !ok || s.RconPassword != "secret" {
		t.Fatalf("auto server creative not loaded correctly from sidecar: %#v", s)
	}
}

func TestNonFiniteAndNegativeMaxMBpsValidation(t *testing.T) {
	validCfg := func() *Config {
		cfg := DefaultConfig()
		cfg.Global.MaxMBps = 40.0
		return cfg
	}

	t.Run("Validate rejects NaN, +Inf, -Inf, and negative", func(t *testing.T) {
		invalidValues := []float64{
			math.NaN(),
			math.Inf(1),
			math.Inf(-1),
			-5.0,
		}
		for _, val := range invalidValues {
			cfg := validCfg()
			cfg.Global.MaxMBps = val
			if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "max_mbps") {
				t.Errorf("Validate(%v) expected error containing 'max_mbps', got: %v", val, err)
			}
		}

		// 0.0 and positive finite are valid
		for _, val := range []float64{0.0, 100.0} {
			cfg := validCfg()
			cfg.Global.MaxMBps = val
			if err := cfg.Validate(); err != nil {
				t.Errorf("Validate(%v) expected valid, got: %v", val, err)
			}
		}
	})

	t.Run("LoadConfig TOML rejects NaN, Inf, and negative", func(t *testing.T) {
		tmp := t.TempDir()
		cfgPath := filepath.Join(tmp, "config.toml")

		invalidTOMLs := []string{
			"[global]\nlisten_addr = \"127.0.0.1:47990\"\nmax_mbps = nan\n",
			"[global]\nlisten_addr = \"127.0.0.1:47990\"\nmax_mbps = inf\n",
			"[global]\nlisten_addr = \"127.0.0.1:47990\"\nmax_mbps = -inf\n",
			"[global]\nlisten_addr = \"127.0.0.1:47990\"\nmax_mbps = -10.0\n",
		}
		for _, content := range invalidTOMLs {
			if err := os.WriteFile(cfgPath, []byte(content), 0600); err != nil {
				t.Fatalf("WriteFile: %v", err)
			}
			if _, err := LoadConfig(cfgPath); err == nil || !strings.Contains(err.Error(), "max_mbps") {
				t.Errorf("LoadConfig for TOML %q expected error containing 'max_mbps', got: %v", content, err)
			}
		}

		// 0.0 is valid
		if err := os.WriteFile(cfgPath, []byte("[global]\nlisten_addr = \"127.0.0.1:47990\"\nmax_mbps = 0.0\n"), 0600); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		if cfg, err := LoadConfig(cfgPath); err != nil {
			t.Errorf("LoadConfig 0.0 max_mbps expected valid, got: %v", err)
		} else if cfg.Global.MaxMBps != 0.0 {
			t.Errorf("expected 0.0 max_mbps, got %v", cfg.Global.MaxMBps)
		}
	})

	t.Run("SetConfigValue rejects NaN, +Inf, -Inf, and negative", func(t *testing.T) {
		tmp := t.TempDir()
		cfgPath := filepath.Join(tmp, "config.toml")
		if err := os.WriteFile(cfgPath, []byte("[global]\nlisten_addr = \"127.0.0.1:47990\"\n"), 0600); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}

		invalidInputs := []string{"nan", "NaN", "+inf", "-inf", "inf", "-5.0"}
		for _, input := range invalidInputs {
			if err := SetConfigValue(cfgPath, "global.max_mbps", input); err == nil {
				t.Errorf("SetConfigValue global.max_mbps %q expected error, got nil", input)
			}
		}

		// 0 and positive finite are valid
		if err := SetConfigValue(cfgPath, "global.max_mbps", "0"); err != nil {
			t.Errorf("SetConfigValue global.max_mbps 0 unexpected error: %v", err)
		}
		if err := SetConfigValue(cfgPath, "global.max_mbps", "100.5"); err != nil {
			t.Errorf("SetConfigValue global.max_mbps 100.5 unexpected error: %v", err)
		}
	})
}

func TestDurationMarshalUnmarshal(t *testing.T) {
	d := Duration{Duration: 5 * time.Minute}
	bytes, err := d.MarshalText()
	if err != nil {
		t.Fatalf("MarshalText failed: %v", err)
	}
	if string(bytes) != "5m0s" {
		t.Errorf("MarshalText = %q, want '5m0s'", string(bytes))
	}

	var d2 Duration
	if err := d2.UnmarshalText([]byte("10m")); err != nil {
		t.Fatalf("UnmarshalText failed: %v", err)
	}
	if d2.Duration != 10*time.Minute {
		t.Errorf("UnmarshalText = %v, want 10m", d2.Duration)
	}
}

func TestWriteLastSnapshotHelper(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "config.toml")

	writeLastSnapshot(cfgPath, "creative", "/local/path", "/nas/path")
	m := readLastSnapshots(cfgPath)
	if entry, ok := m["creative"]; !ok || entry.Local != "/local/path" || entry.NAS != "/nas/path" {
		t.Fatalf("writeLastSnapshot failed: %#v", entry)
	}
}
