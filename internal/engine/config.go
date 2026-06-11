package engine

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/fsnotify/fsnotify"
)

type Duration struct{ time.Duration }

func (d *Duration) UnmarshalText(text []byte) error {
	var err error
	d.Duration, err = time.ParseDuration(string(text))
	return err
}

func (d Duration) MarshalText() ([]byte, error) {
	return []byte(d.Duration.String()), nil
}

type GlobalConfig struct {
	ListenAddr     string   `toml:"listen_addr"`
	BackupInterval Duration `toml:"backup_interval"`
	InitialDelay   Duration `toml:"initial_delay"`
	MaxMBps        float64  `toml:"max_mbps"`
}

type NASConfig struct {
	SSHUser  string `toml:"ssh_user"`
	SSHHost  string `toml:"ssh_host"`
	SSHPort  int    `toml:"ssh_port"`
	SSHKey   string `toml:"ssh_key"`
	DestRoot string `toml:"dest_root"`
}

type RetentionConfig struct {
	PruneDays  int `toml:"prune_days"`
	PruneCount int `toml:"prune_count"`
}

type WatchConfig struct {
	Path       string `toml:"path"`
	Namespace  string `toml:"namespace"`
	LocalKeep  int    `toml:"local_keep"`
	MaxDiskPct int    `toml:"max_disk_pct"`
}

func (w WatchConfig) backupDir(serverName string) string {
	return filepath.Join(w.Path, serverName, "backups")
}

type ServerConfig struct {
	Enabled             bool   `toml:"enabled"`
	SSHOnly             bool   `toml:"ssh_only"`
	ContainerName       string `toml:"container_name"`
	RconPassword        string `toml:"rcon_password"`
	DataDir             string `toml:"data_dir"`
	PauseIfNoPlayers    bool   `toml:"pause_if_no_players"`
}

type Config struct {
	Global    GlobalConfig            `toml:"global"`
	NAS       NASConfig               `toml:"nas"`
	Retention RetentionConfig         `toml:"retention"`
	Watch     []WatchConfig           `toml:"watch"`
	Servers   map[string]ServerConfig `toml:"server"`
}

func LoadConfig(path string) (*Config, error) {
	cfg := &Config{
		Global: GlobalConfig{
			ListenAddr:     "127.0.0.1:47990",
			BackupInterval: Duration{24 * time.Hour},
			InitialDelay:   Duration{2 * time.Minute},
			MaxMBps:        40.0,
		},
		NAS: NASConfig{
			SSHPort: 22,
		},
		Retention: RetentionConfig{
			PruneDays: 7,
		},
		Servers: make(map[string]ServerConfig),
	}

	if _, err := toml.DecodeFile(path, cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}

	normalized := make(map[string]ServerConfig, len(cfg.Servers))
	for k, v := range cfg.Servers {
		normalized[strings.ToLower(k)] = v
	}
	cfg.Servers = normalized

	applyEnvOverrides(cfg)
	return cfg, nil
}

func SaveConfig(path string, cfg *Config) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	defer f.Close()

	enc := toml.NewEncoder(f)
	enc.Indent = ""
	return enc.Encode(cfg)
}

func applyEnvOverrides(cfg *Config) {
	for _, e := range os.Environ() {
		kv := strings.SplitN(e, "=", 2)
		if len(kv) != 2 || !strings.HasPrefix(kv[0], "MC_BACKUP_") {
			continue
		}
		parts := strings.Split(strings.ToLower(kv[0]), "_")
		if len(parts) < 4 {
			continue
		}
		section := parts[2]
		serverName := ""
		keyIdx := 3
		if section == "server" && len(parts) >= 5 {
			serverName = parts[3]
			keyIdx = 4
		}
		keyParts := parts[keyIdx:]
		key := strings.Join(keyParts, "_")
		val := kv[1]

		switch section {
		case "global":
			setGlobalField(&cfg.Global, key, val)
		case "nas":
			setNASField(&cfg.NAS, key, val)
		case "retention":
			setRetentionField(&cfg.Retention, key, val)
		case "server":
			if serverName != "" {
				if s, ok := cfg.Servers[serverName]; ok {
					setServerField(&s, key, val)
					cfg.Servers[serverName] = s
				}
			}
		}
	}
}

func setGlobalField(v *GlobalConfig, key, val string) {
	switch key {
	case "listen_addr":
		v.ListenAddr = val
	case "max_mbps":
		if f, err := strconv.ParseFloat(val, 64); err == nil {
			v.MaxMBps = f
		}
	case "backup_interval":
		d, err := time.ParseDuration(val)
		if err == nil {
			v.BackupInterval = Duration{d}
		}
	case "initial_delay":
		d, err := time.ParseDuration(val)
		if err == nil {
			v.InitialDelay = Duration{d}
		}
	}
}

func setNASField(v *NASConfig, key, val string) {
	switch key {
	case "ssh_user":
		v.SSHUser = val
	case "ssh_host":
		v.SSHHost = val
	case "ssh_port":
		if i, err := strconv.Atoi(val); err == nil {
			v.SSHPort = i
		}
	case "ssh_key":
		v.SSHKey = val
	case "dest_root":
		v.DestRoot = val
	}
}

func setRetentionField(v *RetentionConfig, key, val string) {
	switch key {
	case "prune_days":
		if i, err := strconv.Atoi(val); err == nil {
			v.PruneDays = i
		}
	case "prune_count":
		if i, err := strconv.Atoi(val); err == nil {
			v.PruneCount = i
		}
	}
}

func setServerField(s *ServerConfig, key, val string) {
	switch key {
	case "enabled":
		s.Enabled = strings.ToLower(val) == "true"
	case "ssh_only":
		s.SSHOnly = strings.ToLower(val) == "true"
	case "container_name":
		s.ContainerName = val
	case "rcon_password":
		s.RconPassword = val
	case "data_dir":
		s.DataDir = val
	case "pause_if_no_players":
		s.PauseIfNoPlayers = strings.ToLower(val) == "true"
	}
}

type atomicConfig struct {
	ptr atomic.Pointer[Config]
}

func (ac *atomicConfig) Load() *Config {
	return ac.ptr.Load()
}

func (ac *atomicConfig) Store(cfg *Config) {
	ac.ptr.Store(cfg)
}

func watchConfig(path string, ac *atomicConfig) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("watcher: %w", err)
	}
	if err := watcher.Add(path); err != nil {
		watcher.Close()
		return fmt.Errorf("watch %s: %w", path, err)
	}

	go func() {
		defer watcher.Close()
		for {
			select {
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				if event.Op&fsnotify.Write != 0 || event.Op&fsnotify.Create != 0 {
					slog.Info("config file changed, reloading", "path", path)
					oldCfg := ac.Load()
					cfg, err := LoadConfig(path)
					if err != nil {
						slog.Error("config reload failed", "error", err)
						continue
					}
					if oldCfg != nil && oldCfg.Global.ListenAddr != cfg.Global.ListenAddr {
						slog.Warn("listen_addr changed, requires restart to take effect",
							"old", oldCfg.Global.ListenAddr, "new", cfg.Global.ListenAddr)
					}
					ac.Store(cfg)
					slog.Info("config reloaded successfully")
				}
			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				slog.Error("config watcher error", "error", err)
			}
		}
	}()
	return nil
}

func GetConfigValue(cfg *Config, key string) string {
	parts := strings.Split(key, ".")
	if len(parts) < 2 {
		return ""
	}
	section := parts[0]

	switch section {
	case "global":
		return getGlobalField(cfg.Global, parts[1])
	case "nas":
		return getNASField(cfg.NAS, parts[1])
	case "retention":
		return getRetentionField(cfg.Retention, parts[1])
	case "server":
		if len(parts) < 3 {
			return ""
		}
		serverName := parts[1]
		field := parts[2]
		if s, ok := cfg.Servers[serverName]; ok {
			return getServerFieldStr(s, field)
		}
		return ""
	}
	return ""
}

func SetConfigValue(path, key, val string) error {
	cfg, err := LoadConfig(path)
	if err != nil {
		return err
	}
	parts := strings.Split(key, ".")
	if len(parts) < 2 {
		return fmt.Errorf("invalid key: %s", key)
	}
	section := parts[0]

	switch section {
	case "global":
		setGlobalField(&cfg.Global, parts[1], val)
	case "nas":
		setNASField(&cfg.NAS, parts[1], val)
	case "retention":
		setRetentionField(&cfg.Retention, parts[1], val)
	case "server":
		if len(parts) < 3 {
			return fmt.Errorf("server key requires <name>.<field>")
		}
		serverName := strings.ToLower(parts[1])
		field := parts[2]
		s := cfg.Servers[serverName]
		setServerField(&s, field, val)
		cfg.Servers[serverName] = s
	default:
		return fmt.Errorf("unknown section: %s", section)
	}
	return SaveConfig(path, cfg)
}

func getGlobalField(g GlobalConfig, key string) string {
	switch key {
	case "listen_addr":
		return g.ListenAddr
	case "max_mbps":
		return fmt.Sprintf("%.1f", g.MaxMBps)
	case "backup_interval":
		return g.BackupInterval.Duration.String()
	case "initial_delay":
		return g.InitialDelay.Duration.String()
	}
	return ""
}

func getNASField(n NASConfig, key string) string {
	switch key {
	case "ssh_user":
		return n.SSHUser
	case "ssh_host":
		return n.SSHHost
	case "ssh_port":
		return fmt.Sprintf("%d", n.SSHPort)
	case "ssh_key":
		return n.SSHKey
	case "dest_root":
		return n.DestRoot
	}
	return ""
}

func getRetentionField(r RetentionConfig, key string) string {
	switch key {
	case "prune_days":
		return fmt.Sprintf("%d", r.PruneDays)
	case "prune_count":
		return fmt.Sprintf("%d", r.PruneCount)
	}
	return ""
}

func getServerFieldStr(s ServerConfig, key string) string {
	switch key {
	case "enabled":
		return fmt.Sprintf("%t", s.Enabled)
	case "ssh_only":
		return fmt.Sprintf("%t", s.SSHOnly)
	case "container_name":
		return s.ContainerName
	case "rcon_password":
		return s.RconPassword
	case "data_dir":
		return s.DataDir
	case "pause_if_no_players":
		return fmt.Sprintf("%t", s.PauseIfNoPlayers)
	}
	return ""
}
