package main

import (
	"fmt"
	"os"
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

	applyEnvOverrides(cfg)
	return cfg, nil
}

func applyEnvOverrides(cfg *Config) {
	for _, e := range os.Environ() {
		kv := strings.SplitN(e, "=", 2)
		if len(kv) != 2 || !strings.HasPrefix(kv[0], "MC_BACKUP_") {
			continue
		}
		_ = kv
	}
}
