package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
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
	LocalPath  string `toml:"local_path"`
	LocalKeep  int    `toml:"local_keep"`
	MaxDiskPct int    `toml:"max_disk_pct"`
}

type ServerConfig struct {
	Enabled       bool   `toml:"enabled"`
	SSHOnly       bool   `toml:"ssh_only"`
	ContainerName string `toml:"container_name"`
	RconPassword  string `toml:"rcon_password"`
	DataDir       string `toml:"data_dir"`
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
	}
}
