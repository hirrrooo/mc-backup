package engine

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/fsnotify/fsnotify"
)

var (
	renameFile = os.Rename
	removeFile = os.Remove
	chmodFile  = os.Chmod
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
	ListenAddr     string    `toml:"listen_addr"`
	BackupInterval Duration  `toml:"backup_interval"`
	InitialDelay   Duration  `toml:"initial_delay"`
	MaxMBps        float64   `toml:"max_mbps"`
	APIToken       string    `toml:"api_token"`
	Excludes       *[]string `toml:"excludes"`
}

type NASConfig struct {
	SSHUser  string `toml:"ssh_user"`
	SSHHost  string `toml:"ssh_host"`
	SSHPort  int    `toml:"ssh_port"`
	SSHKey   string `toml:"ssh_key"`
	DestRoot string `toml:"dest_root"`
}

type LocalConfig struct {
	DestRoot string `toml:"dest_root"`
}

type RetentionConfig struct {
	PruneDays  int `toml:"prune_days"`
	PruneCount int `toml:"prune_count"`
}

type WatchConfig struct {
	Path      string `toml:"path"`
	Namespace string `toml:"namespace"`
}

func (w WatchConfig) backupDir(serverName string) string {
	return filepath.Join(w.Path, serverName, "backups")
}

type ServerConfig struct {
	Enabled          bool      `toml:"enabled"`
	Target           string    `toml:"target"`
	ContainerName    string    `toml:"container_name"`
	RconPassword     string    `toml:"rcon_password"`
	DataDir          string    `toml:"data_dir"`
	PauseIfNoPlayers bool      `toml:"pause_if_no_players"`
	Excludes         *[]string `toml:"excludes"`
}

type Config struct {
	Global    GlobalConfig            `toml:"global"`
	Local     LocalConfig             `toml:"local"`
	NAS       NASConfig               `toml:"nas"`
	Retention RetentionConfig         `toml:"retention"`
	Watch     []WatchConfig           `toml:"watch"`
	Servers   map[string]ServerConfig `toml:"server"`
}

func DefaultExcludes() []string {
	return []string{"*.jar", "cache", "logs", "*.tmp"}
}

func DefaultConfig() *Config {
	return &Config{
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
}

func (c *Config) ResolveGlobalExcludes() []string {
	if c.Global.Excludes != nil {
		res := make([]string, len(*c.Global.Excludes))
		copy(res, *c.Global.Excludes)
		return res
	}
	return DefaultExcludes()
}

func (c *Config) ResolveServerExcludes(server ServerConfig) []string {
	if server.Excludes != nil {
		res := make([]string, len(*server.Excludes))
		copy(res, *server.Excludes)
		return res
	}
	return c.ResolveGlobalExcludes()
}

func parseExcludesValue(val string) *[]string {
	trimmed := strings.TrimSpace(val)
	if strings.EqualFold(trimmed, "inherit") || strings.EqualFold(trimmed, "default") {
		return nil
	}
	if trimmed == "" || strings.EqualFold(trimmed, "none") {
		res := []string{}
		return &res
	}
	parts := strings.Split(val, ",")
	res := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			res = append(res, p)
		}
	}
	return &res
}

func normalizeTarget(t string) string {
	return strings.ToLower(strings.TrimSpace(t))
}

func (c *Config) Validate() error {
	if c.Global.ListenAddr == "" {
		return fmt.Errorf("invalid config: global.listen_addr is required")
	}
	if c.Global.BackupInterval.Duration <= 0 {
		return fmt.Errorf("invalid config: global.backup_interval must be greater than 0")
	}
	if c.Global.InitialDelay.Duration < 0 {
		return fmt.Errorf("invalid config: global.initial_delay must be non-negative")
	}
	host, portStr, err := net.SplitHostPort(c.Global.ListenAddr)
	if err != nil {
		return fmt.Errorf("invalid config: malformed global.listen_addr %q: %w", c.Global.ListenAddr, err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port <= 0 || port > 65535 {
		return fmt.Errorf("invalid config: invalid port in global.listen_addr %q", c.Global.ListenAddr)
	}

	if !isLoopbackHost(host) && c.Global.APIToken == "" {
		return fmt.Errorf("invalid config: non-loopback global.listen_addr %q requires global.api_token to be set", c.Global.ListenAddr)
	}

	hasNASTarget := false
	hasLocalTarget := false
	for name, s := range c.Servers {
		if !s.Enabled {
			continue
		}
		t := normalizeTarget(s.Target)
		switch t {
		case "", "nas":
			hasNASTarget = true
		case "local":
			hasLocalTarget = true
		default:
			return fmt.Errorf("invalid config: server %q has invalid target %q (must be \"nas\" or \"local\")", name, s.Target)
		}
	}

	if hasNASTarget {
		var missing []string
		if strings.TrimSpace(c.NAS.SSHHost) == "" {
			missing = append(missing, "ssh_host")
		}
		if strings.TrimSpace(c.NAS.SSHUser) == "" {
			missing = append(missing, "ssh_user")
		}
		if strings.TrimSpace(c.NAS.DestRoot) == "" {
			missing = append(missing, "dest_root")
		}
		if len(missing) > 0 {
			return fmt.Errorf("invalid config: NAS target enabled but missing required fields in [nas] section (%s)", strings.Join(missing, ", "))
		}
	}

	if hasLocalTarget {
		if strings.TrimSpace(c.Local.DestRoot) == "" {
			return fmt.Errorf("invalid config: local target enabled but missing required field local.dest_root")
		}
	}

	return nil
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	if ip != nil && ip.IsLoopback() {
		return true
	}
	return false
}

func LoadConfig(path string) (*Config, error) {
	cfg, err := loadConfigFile(path)
	if err != nil {
		return nil, err
	}
	applyEnvOverrides(cfg)
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func loadConfigFile(path string) (*Config, error) {
	cfg := DefaultConfig()

	autoPath := strings.TrimSuffix(path, ".toml") + "-auto.toml"
	if _, err := toml.DecodeFile(autoPath, cfg); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("parse %s: %w", autoPath, err)
	}

	if _, err := toml.DecodeFile(path, cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}

	normalized := make(map[string]ServerConfig, len(cfg.Servers))
	for k, v := range cfg.Servers {
		normalized[strings.ToLower(k)] = v
	}
	cfg.Servers = normalized

	cfg.Local.DestRoot = normalizeDestRoot(cfg.Local.DestRoot)
	cfg.NAS.DestRoot = normalizeDestRoot(cfg.NAS.DestRoot)

	return cfg, nil
}

func normalizeDestRoot(root string) string {
	trimmed := strings.TrimRight(root, "/")
	if trimmed == "" && root != "" {
		return "/"
	}
	return trimmed
}

func resolveBackupTarget(serverName string, server ServerConfig, local LocalConfig) (string, error) {
	target := normalizeTarget(server.Target)
	if target == "" {
		target = "nas"
	}
	if target != "local" && target != "nas" {
		return "", fmt.Errorf("server %q has invalid backup target %q (want %q or %q)", serverName, server.Target, "local", "nas")
	}
	if target == "local" && strings.TrimSpace(local.DestRoot) == "" {
		return "", fmt.Errorf("server %q target %q requires local.dest_root", serverName, target)
	}
	return target, nil
}

func SaveConfig(path string, cfg *Config) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, filepath.Base(path)+".*.tmp")
	if err != nil {
		return fmt.Errorf("create temp for %s: %w", path, err)
	}
	tmp := f.Name()
	defer os.Remove(tmp)

	enc := toml.NewEncoder(f)
	enc.Indent = ""
	if err := enc.Encode(cfg); err != nil {
		f.Close()
		return fmt.Errorf("encode %s: %w", path, err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return fmt.Errorf("sync %s: %w", tmp, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close %s: %w", tmp, err)
	}
	if err := renameFile(tmp, path); err != nil {
		return fmt.Errorf("replace %s: %w", path, err)
	}
	return nil
}

func autoServersPath(cfgPath string) string {
	return strings.TrimSuffix(cfgPath, ".toml") + "-auto.toml"
}

// saveSplit writes cfg back to disk, sending auto-provisioned servers (those
// present in <path>-auto.toml) to the auto file and everything else to the
// main config. It handles main and auto file updates transactionally: both temp
// files are written and closed before replacement, and any replacement failure
// rolls back already-replaced files to prevent partial state.
func saveSplit(path string, cfg *Config) (err error) {
	autoNames := loadAutoServerNames(path)

	main := cloneConfig(cfg)
	auto := make(map[string]ServerConfig)
	for name := range autoNames {
		if s, ok := main.Servers[name]; ok {
			auto[name] = s
			delete(main.Servers, name)
		}
	}

	mainPath := path
	autoPath := autoServersPath(path)
	dir := filepath.Dir(mainPath)

	mainExisted := false
	mainMode := os.FileMode(0600)
	if info, statErr := os.Stat(mainPath); statErr == nil {
		mainExisted = true
		mainMode = info.Mode().Perm()
	}

	autoExisted := false
	if _, statErr := os.Stat(autoPath); statErr == nil {
		autoExisted = true
	}

	// Step 1: Write and sync temp files for both main and auto
	mainFile, createErr := os.CreateTemp(dir, filepath.Base(mainPath)+".*.tmp")
	if createErr != nil {
		return fmt.Errorf("create temp for %s: %w", mainPath, createErr)
	}
	mainTemp := mainFile.Name()
	defer func() { _ = removeFile(mainTemp) }()

	if chmodErr := mainFile.Chmod(mainMode); chmodErr != nil {
		mainFile.Close()
		return fmt.Errorf("chmod temp %s: %w", mainTemp, chmodErr)
	}

	enc := toml.NewEncoder(mainFile)
	enc.Indent = ""
	if encErr := enc.Encode(main); encErr != nil {
		mainFile.Close()
		return fmt.Errorf("encode %s: %w", mainPath, encErr)
	}
	if syncErr := mainFile.Sync(); syncErr != nil {
		mainFile.Close()
		return fmt.Errorf("sync %s: %w", mainTemp, syncErr)
	}
	if closeErr := mainFile.Close(); closeErr != nil {
		return fmt.Errorf("close %s: %w", mainTemp, closeErr)
	}

	var autoTemp string
	if len(auto) > 0 {
		autoFile, createErr := os.CreateTemp(dir, filepath.Base(autoPath)+".*.tmp")
		if createErr != nil {
			return fmt.Errorf("create temp for %s: %w", autoPath, createErr)
		}
		autoTemp = autoFile.Name()
		defer func() { _ = removeFile(autoTemp) }()

		if chmodErr := autoFile.Chmod(0600); chmodErr != nil {
			autoFile.Close()
			return fmt.Errorf("chmod temp %s: %w", autoTemp, chmodErr)
		}

		writeAutoServersTo(autoFile, auto)

		if syncErr := autoFile.Sync(); syncErr != nil {
			autoFile.Close()
			return fmt.Errorf("sync %s: %w", autoTemp, syncErr)
		}
		if closeErr := autoFile.Close(); closeErr != nil {
			return fmt.Errorf("close %s: %w", autoTemp, closeErr)
		}
	}

	// Step 2: Perform replacements with best-effort rollback
	nowNano := time.Now().UnixNano()
	mainBackup := filepath.Join(dir, fmt.Sprintf(".%s.bak.%d", filepath.Base(mainPath), nowNano))
	autoBackup := filepath.Join(dir, fmt.Sprintf(".%s.bak.%d", filepath.Base(autoPath), nowNano))

	mainBackupCreated := false
	autoBackupCreated := false

	defer func() {
		if err == nil {
			if mainBackupCreated {
				_ = removeFile(mainBackup)
			}
			if autoBackupCreated {
				_ = removeFile(autoBackup)
			}
		}
	}()

	rollback := func(origErr error) error {
		var rbErrs []error
		if autoBackupCreated {
			if rbErr := renameFile(autoBackup, autoPath); rbErr == nil {
				autoBackupCreated = false
			} else {
				rbErrs = append(rbErrs, fmt.Errorf("rollback %s: %w", autoPath, rbErr))
			}
		} else if len(auto) > 0 && !autoExisted {
			_ = removeFile(autoPath)
		}

		if mainBackupCreated {
			if rbErr := renameFile(mainBackup, mainPath); rbErr == nil {
				mainBackupCreated = false
			} else {
				rbErrs = append(rbErrs, fmt.Errorf("rollback %s: %w", mainPath, rbErr))
			}
		} else if !mainExisted {
			_ = removeFile(mainPath)
		}

		if len(rbErrs) > 0 {
			return errors.Join(append([]error{origErr}, rbErrs...)...)
		}
		return origErr
	}

	if mainExisted {
		if renameErr := renameFile(mainPath, mainBackup); renameErr != nil {
			return fmt.Errorf("backup %s: %w", mainPath, renameErr)
		}
		mainBackupCreated = true
	}

	if renameErr := renameFile(mainTemp, mainPath); renameErr != nil {
		return rollback(fmt.Errorf("replace %s: %w", mainPath, renameErr))
	}

	if len(auto) > 0 {
		if autoExisted {
			if renameErr := renameFile(autoPath, autoBackup); renameErr != nil {
				return rollback(fmt.Errorf("backup %s: %w", autoPath, renameErr))
			}
			autoBackupCreated = true
		}

		if renameErr := renameFile(autoTemp, autoPath); renameErr != nil {
			return rollback(fmt.Errorf("replace %s: %w", autoPath, renameErr))
		}

		if chmodErr := chmodFile(autoPath, 0600); chmodErr != nil {
			return rollback(fmt.Errorf("chmod %s: %w", autoPath, chmodErr))
		}
	} else if autoExisted {
		if renameErr := renameFile(autoPath, autoBackup); renameErr != nil {
			return rollback(fmt.Errorf("remove auto %s: %w", autoPath, renameErr))
		}
		autoBackupCreated = true
	}

	return nil
}

func writeAutoServersTo(f io.Writer, servers map[string]ServerConfig) {
	for name, s := range servers {
		fmt.Fprintf(f, "\n[server.%s]\n", name)
		fmt.Fprintf(f, "enabled = %v\n", s.Enabled)
		fmt.Fprintf(f, "target = %q\n", s.Target)
		fmt.Fprintf(f, "container_name = %q\n", s.ContainerName)
		fmt.Fprintf(f, "rcon_password = %q\n", s.RconPassword)
		fmt.Fprintf(f, "# defaults to <watch.path>/<server>/mc-data if empty\n")
		fmt.Fprintf(f, "data_dir = %q\n", s.DataDir)
		fmt.Fprintf(f, "pause_if_no_players = %v\n", s.PauseIfNoPlayers)
		if s.Excludes != nil {
			fmt.Fprintf(f, "excludes = %s\n", formatTOMLStringSlice(*s.Excludes))
		}
	}
}

func cloneConfig(src *Config) *Config {
	dst := &Config{
		Global:    src.Global,
		Local:     src.Local,
		NAS:       src.NAS,
		Retention: src.Retention,
		Watch:     src.Watch,
		Servers:   make(map[string]ServerConfig, len(src.Servers)),
	}
	if src.Global.Excludes != nil {
		ex := make([]string, len(*src.Global.Excludes))
		copy(ex, *src.Global.Excludes)
		dst.Global.Excludes = &ex
	}
	for k, v := range src.Servers {
		sc := v
		if v.Excludes != nil {
			ex := make([]string, len(*v.Excludes))
			copy(ex, *v.Excludes)
			sc.Excludes = &ex
		}
		dst.Servers[k] = sc
	}
	return dst
}

func loadAutoServerNames(cfgPath string) map[string]bool {
	names := make(map[string]bool)
	autoPath := autoServersPath(cfgPath)
	var tmp struct {
		Servers map[string]ServerConfig `toml:"server"`
	}
	if _, err := toml.DecodeFile(autoPath, &tmp); err != nil && !os.IsNotExist(err) {
		slog.Warn("cannot read auto config", "path", autoPath, "error", err)
		return names
	}
	for k := range tmp.Servers {
		names[strings.ToLower(k)] = true
	}
	return names
}

func SaveAutoServers(cfgPath string, servers map[string]ServerConfig) error {
	autoPath := autoServersPath(cfgPath)
	if len(servers) == 0 {
		_ = removeFile(autoPath)
		return nil
	}
	dir := filepath.Dir(autoPath)
	f, err := os.CreateTemp(dir, filepath.Base(autoPath)+".*.tmp")
	if err != nil {
		return fmt.Errorf("create temp for %s: %w", autoPath, err)
	}
	tmp := f.Name()
	defer func() { _ = removeFile(tmp) }()

	if err := chmodFile(tmp, 0600); err != nil {
		f.Close()
		return fmt.Errorf("chmod temp %s: %w", tmp, err)
	}

	writeAutoServersTo(f, servers)

	if err := f.Sync(); err != nil {
		f.Close()
		return fmt.Errorf("sync %s: %w", tmp, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close %s: %w", tmp, err)
	}
	if err := renameFile(tmp, autoPath); err != nil {
		return fmt.Errorf("replace %s: %w", autoPath, err)
	}
	return chmodFile(autoPath, 0600)
}

func formatTOMLStringSlice(items []string) string {
	elems := make([]string, len(items))
	for i, item := range items {
		elems[i] = fmt.Sprintf("%q", item)
	}
	return "[" + strings.Join(elems, ", ") + "]"
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
		val := kv[1]

		if section == "server" {
			name, field, ok := parseServerEnvKey(strings.Join(parts[3:], "_"))
			if !ok {
				continue
			}
			if s, exists := cfg.Servers[name]; exists {
				if err := setServerField(&s, field, val); err != nil {
					slog.Warn("ignoring invalid environment override", "var", kv[0], "error", err)
					continue
				}
				cfg.Servers[name] = s
			}
			continue
		}

		key := strings.Join(parts[3:], "_")
		var setErr error
		switch section {
		case "global":
			setErr = setGlobalField(&cfg.Global, key, val)
		case "nas":
			setErr = setNASField(&cfg.NAS, key, val)
		case "local":
			setErr = setLocalField(&cfg.Local, key, val)
		case "retention":
			setErr = setRetentionField(&cfg.Retention, key, val)
		}
		if setErr != nil {
			slog.Warn("ignoring invalid environment override", "var", kv[0], "error", setErr)
		}
	}
}

func setLocalField(v *LocalConfig, key, val string) error {
	if key == "dest_root" {
		v.DestRoot = normalizeDestRoot(val)
		return nil
	}
	return fmt.Errorf("unknown local field: %s", key)
}

func setGlobalField(v *GlobalConfig, key, val string) error {
	switch key {
	case "listen_addr":
		v.ListenAddr = val
	case "api_token":
		v.APIToken = val
	case "max_mbps":
		f, err := strconv.ParseFloat(val, 64)
		if err != nil || f < 0 {
			return fmt.Errorf("invalid max_mbps: %q", val)
		}
		v.MaxMBps = f
	case "backup_interval":
		d, err := time.ParseDuration(val)
		if err != nil || d <= 0 {
			return fmt.Errorf("invalid backup_interval duration: %q", val)
		}
		v.BackupInterval = Duration{d}
	case "initial_delay":
		d, err := time.ParseDuration(val)
		if err != nil || d < 0 {
			return fmt.Errorf("invalid initial_delay duration: %q", val)
		}
		v.InitialDelay = Duration{d}
	case "excludes":
		v.Excludes = parseExcludesValue(val)
	default:
		return fmt.Errorf("unknown global field: %s", key)
	}
	return nil
}

func setNASField(v *NASConfig, key, val string) error {
	switch key {
	case "ssh_user":
		v.SSHUser = val
	case "ssh_host":
		v.SSHHost = val
	case "ssh_port":
		i, err := strconv.Atoi(val)
		if err != nil || i <= 0 || i > 65535 {
			return fmt.Errorf("invalid ssh_port: %q", val)
		}
		v.SSHPort = i
	case "ssh_key":
		v.SSHKey = val
	case "dest_root":
		v.DestRoot = normalizeDestRoot(val)
	default:
		return fmt.Errorf("unknown nas field: %s", key)
	}
	return nil
}

func setRetentionField(v *RetentionConfig, key, val string) error {
	switch key {
	case "prune_days":
		i, err := strconv.Atoi(val)
		if err != nil || i < 0 {
			return fmt.Errorf("invalid prune_days: %q", val)
		}
		v.PruneDays = i
	case "prune_count":
		i, err := strconv.Atoi(val)
		if err != nil || i < 0 {
			return fmt.Errorf("invalid prune_count: %q", val)
		}
		v.PruneCount = i
	default:
		return fmt.Errorf("unknown retention field: %s", key)
	}
	return nil
}

func setServerField(s *ServerConfig, key, val string) error {
	switch key {
	case "enabled":
		b, err := strconv.ParseBool(val)
		if err != nil {
			return fmt.Errorf("invalid bool for enabled: %q", val)
		}
		s.Enabled = b
	case "target":
		s.Target = val
	case "container_name":
		s.ContainerName = val
	case "rcon_password":
		s.RconPassword = val
	case "data_dir":
		s.DataDir = val
	case "pause_if_no_players":
		b, err := strconv.ParseBool(val)
		if err != nil {
			return fmt.Errorf("invalid bool for pause_if_no_players: %q", val)
		}
		s.PauseIfNoPlayers = b
	case "excludes":
		s.Excludes = parseExcludesValue(val)
	default:
		return fmt.Errorf("unknown server field: %s", key)
	}
	return nil
}

var serverFieldKeys = []string{
	"enabled",
	"target",
	"container_name",
	"rcon_password",
	"data_dir",
	"pause_if_no_players",
	"excludes",
}

// parseServerEnvKey splits the lowercased remainder of a
// MC_BACKUP_SERVER_<NAME>_<FIELD> variable (everything after "server_") into a
// server name and field by matching a known field as the suffix. This is
// unambiguous because no field key is a suffix of another.
func parseServerEnvKey(rest string) (name, field string, ok bool) {
	for _, f := range serverFieldKeys {
		suffix := "_" + f
		if strings.HasSuffix(rest, suffix) {
			name = strings.TrimSuffix(rest, suffix)
			if name == "" {
				return "", "", false
			}
			return name, f, true
		}
	}
	return "", "", false
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
	dir := filepath.Dir(path)
	base := filepath.Base(path)
	autoBase := filepath.Base(autoServersPath(path))
	if err := watcher.Add(dir); err != nil {
		watcher.Close()
		return fmt.Errorf("watch %s: %w", dir, err)
	}

	go func() {
		defer watcher.Close()
		for {
			select {
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				name := filepath.Base(event.Name)
				if name != base && name != autoBase {
					continue
				}
				if event.Op&fsnotify.Write == 0 && event.Op&fsnotify.Create == 0 && event.Op&fsnotify.Rename == 0 {
					continue
				}
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
	case "local":
		return getLocalField(cfg.Local, parts[1])
	case "retention":
		return getRetentionField(cfg.Retention, parts[1])
	case "server":
		if len(parts) < 3 {
			return ""
		}
		serverName := strings.ToLower(parts[1])
		field := parts[2]
		if s, ok := cfg.Servers[serverName]; ok {
			return getServerFieldStr(s, field)
		}
		return ""
	}
	return ""
}

func SetConfigValue(path, key, val string) error {
	cfg, err := loadConfigFile(path)
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
		if err := setGlobalField(&cfg.Global, parts[1], val); err != nil {
			return err
		}
	case "nas":
		if err := setNASField(&cfg.NAS, parts[1], val); err != nil {
			return err
		}
	case "local":
		if err := setLocalField(&cfg.Local, parts[1], val); err != nil {
			return err
		}
	case "retention":
		if err := setRetentionField(&cfg.Retention, parts[1], val); err != nil {
			return err
		}
	case "server":
		if len(parts) < 3 {
			return fmt.Errorf("server key requires <name>.<field>")
		}
		serverName := strings.ToLower(parts[1])
		field := parts[2]
		s := cfg.Servers[serverName]
		if err := setServerField(&s, field, val); err != nil {
			return err
		}
		cfg.Servers[serverName] = s
	default:
		return fmt.Errorf("unknown section: %s", section)
	}
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("invalid config update: %w", err)
	}
	return saveSplit(path, cfg)
}

func getLocalField(l LocalConfig, key string) string {
	if key == "dest_root" {
		return l.DestRoot
	}
	return ""
}

func getGlobalField(g GlobalConfig, key string) string {
	switch key {
	case "listen_addr":
		return g.ListenAddr
	case "api_token":
		return g.APIToken
	case "max_mbps":
		return fmt.Sprintf("%.1f", g.MaxMBps)
	case "backup_interval":
		return g.BackupInterval.Duration.String()
	case "initial_delay":
		return g.InitialDelay.Duration.String()
	case "excludes":
		if g.Excludes == nil {
			return "default"
		}
		if len(*g.Excludes) == 0 {
			return "none"
		}
		return strings.Join(*g.Excludes, ",")
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
	case "target":
		return s.Target
	case "container_name":
		return s.ContainerName
	case "rcon_password":
		return s.RconPassword
	case "data_dir":
		return s.DataDir
	case "pause_if_no_players":
		return fmt.Sprintf("%t", s.PauseIfNoPlayers)
	case "excludes":
		if s.Excludes == nil {
			return "inherit"
		}
		if len(*s.Excludes) == 0 {
			return "none"
		}
		return strings.Join(*s.Excludes, ",")
	}
	return ""
}

func lastBackupPath(cfgPath string) string {
	return filepath.Join(filepath.Dir(cfgPath), ".last-backup")
}

type lastSnapshotEntry struct {
	Time  time.Time
	Local string
	NAS   string
}

func readLastSnapshots(cfgPath string) map[string]lastSnapshotEntry {
	m := make(map[string]lastSnapshotEntry)
	data, err := os.ReadFile(lastBackupPath(cfgPath))
	if err != nil {
		return m
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "=", 4)
		if len(parts) < 2 {
			continue
		}
		ts, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil {
			continue
		}
		entry := lastSnapshotEntry{Time: time.Unix(ts, 0)}
		if len(parts) >= 3 {
			entry.Local = parts[2]
		}
		if len(parts) >= 4 {
			entry.NAS = parts[3]
		}
		m[parts[0]] = entry
	}
	return m
}

func writeLastSnapshot(cfgPath, server, localPath, nasPath string) {
	writeLastSnapshotAt(cfgPath, server, localPath, nasPath, time.Now())
}

func writeLastSnapshotAt(cfgPath, server, localPath, nasPath string, t time.Time) {
	m := readLastSnapshots(cfgPath)
	m[server] = lastSnapshotEntry{Time: t, Local: localPath, NAS: nasPath}
	dst := lastBackupPath(cfgPath)
	f, err := os.CreateTemp(filepath.Dir(dst), filepath.Base(dst)+".*.tmp")
	if err != nil {
		slog.Warn("failed to create last-backup temp file", "path", dst, "error", err)
		return
	}
	tmp := f.Name()
	defer os.Remove(tmp)

	for name, e := range m {
		if _, err := fmt.Fprintf(f, "%s=%d=%s=%s\n", name, e.Time.Unix(), e.Local, e.NAS); err != nil {
			slog.Warn("failed to write last-backup temp file", "path", tmp, "error", err)
			f.Close()
			return
		}
	}
	if err := f.Sync(); err != nil {
		slog.Warn("failed to sync last-backup temp file", "path", tmp, "error", err)
		f.Close()
		return
	}
	if err := f.Close(); err != nil {
		slog.Warn("failed to close last-backup temp file", "path", tmp, "error", err)
		return
	}
	if err := os.Rename(tmp, dst); err != nil {
		slog.Warn("failed to replace last-backup file", "src", tmp, "dst", dst, "error", err)
	}
}
