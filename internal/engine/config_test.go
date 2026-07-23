package engine

import (
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
	os.WriteFile(cfgPath, content, 0644)

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

func TestSetConfigValueDoesNotDuplicateAutoServers(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "config.toml")
	autoPath := autoServersPath(cfgPath)

	os.WriteFile(cfgPath, []byte("[global]\nmax_mbps = 40.0\n"), 0644)
	os.WriteFile(autoPath, []byte("[server.creative]\nenabled = true\ncontainer_name = \"creative-mc-1\"\nrcon_password = \"secret\"\n"), 0644)

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

	os.WriteFile(cfgPath, []byte("[global]\n"), 0644)
	os.WriteFile(autoPath, []byte("[server.creative]\nenabled = true\ncontainer_name = \"creative-mc-1\"\nrcon_password = \"old\"\n"), 0644)

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

func TestSaveAutoServersAtomicRoundTrip(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "config.toml")
	if err := os.WriteFile(cfgPath, []byte("[global]\n"), 0644); err != nil {
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
	if err := os.WriteFile(cfgPath, []byte("[global]\n"), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := os.WriteFile(autoPath, []byte("[server.creative]\nenabled = true\n"), 0644); err != nil {
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

	if err := os.WriteFile(cfgPath, []byte("[global]\n"), 0644); err != nil {
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
	if err := os.WriteFile(cfgPath, content, 0644); err != nil {
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
			cfg := &Config{
				Global: GlobalConfig{
					ListenAddr: tt.listenAddr,
					APIToken:   tt.apiToken,
				},
			}
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
	if err := os.WriteFile(cfgPath, content, 0644); err != nil {
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
