package main

import (
	"crypto/sha256"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"mc-backup/internal/engine"
)

var version = "0.1.0"

var defaultConfigPaths = []string{
	"/etc/mc-backup/config.toml",
}

var usageOutput io.Writer = os.Stderr

var repoURL = "" // set via -ldflags

var osExecutable = os.Executable

var downloadFile = func(url, dest string) error {
	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Get(url)
	if err != nil {
		return fmt.Errorf("download %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: HTTP %d", url, resp.StatusCode)
	}
	f, err := os.Create(dest)
	if err != nil {
		return fmt.Errorf("create %s: %w", dest, err)
	}
	if err := f.Chmod(0755); err != nil {
		f.Close()
		os.Remove(dest)
		return fmt.Errorf("chmod %s: %w", dest, err)
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		os.Remove(dest)
		return fmt.Errorf("write %s: %w", dest, err)
	}
	if err := f.Close(); err != nil {
		os.Remove(dest)
		return fmt.Errorf("close %s: %w", dest, err)
	}
	return nil
}

var verifyChecksum = func(binaryPath, checksumURL string) error {
	client := &http.Client{Timeout: 1 * time.Minute}
	resp, err := client.Get(checksumURL)
	if err != nil {
		return fmt.Errorf("fetch checksum %s: %w", checksumURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("fetch checksum %s: HTTP %d", checksumURL, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return fmt.Errorf("read checksum: %w", err)
	}
	fields := strings.Fields(strings.TrimSpace(string(body)))
	if len(fields) == 0 {
		return fmt.Errorf("checksum %s is empty", checksumURL)
	}
	expected := fields[0]

	f, err := os.Open(binaryPath)
	if err != nil {
		return fmt.Errorf("open %s: %w", binaryPath, err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return fmt.Errorf("hash %s: %w", binaryPath, err)
	}
	got := fmt.Sprintf("%x", h.Sum(nil))
	if !strings.EqualFold(got, expected) {
		return fmt.Errorf("checksum mismatch: want %s, got %s", expected, got)
	}
	return nil
}

func deriveReleaseURL(repoURL string) string {
	base := strings.TrimSuffix(repoURL, ".git")
	return base + "/releases/latest/download/mc-backup-linux-amd64"
}

var runUpdateStep = func(dir, name string, command string, args ...string) error {
	fmt.Printf("\n==> %s\n", name)
	cmd := exec.Command(command, args...)
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
}

func findConfig() string {
	if home, err := os.UserHomeDir(); err == nil {
		homePath := filepath.Join(home, ".config", "mc-backup", "config.toml")
		if _, err := os.Stat(homePath); err == nil {
			return homePath
		}
	}
	for _, p := range defaultConfigPaths {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return defaultConfigPaths[0]
}

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "run":
		runCmd()
	case "status":
		statusCmd()
	case "config":
		configCmd()
	case "backup":
		backupCmd()
	case "scan":
		postCmd("scan")
	case "cancel":
		postCmd("cancel")
	case "update":
		updateCmd()
	case "version", "--version", "-v":
		fmt.Printf("mc-backup %s\n", version)
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", os.Args[1])
		printUsage()
		os.Exit(1)
	}
}

func backupCmd() {
	fs := flag.NewFlagSet("backup", flag.ExitOnError)
	cfgPath := fs.String("config", findConfig(), "config file path")
	offline := fs.Bool("offline", false, "backup without RCON (works when container is offline)")
	fs.Parse(os.Args[2:])

	cfg, err := engine.LoadConfig(*cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "config error: %v\n", err)
		os.Exit(1)
	}

	server := fs.Arg(0)

	if server == "" {
		cwd, err := os.Getwd()
		if err == nil {
			candidate := filepath.Base(cwd)
			if _, ok := cfg.Servers[strings.ToLower(candidate)]; ok {
				server = candidate
			}
		}
	}

	req, err := http.NewRequest(http.MethodPost, fmt.Sprintf("http://%s/backup", cfg.Global.ListenAddr), nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "backup: %v\n", err)
		os.Exit(1)
	}
	if cfg.Global.APIToken != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.Global.APIToken)
	}
	q := req.URL.Query()
	if server != "" {
		q.Set("server", server)
	}
	if *offline {
		q.Set("offline", "true")
	}
	req.URL.RawQuery = q.Encode()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "backup failed: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusOK:
		var buf [64]byte
		n, _ := resp.Body.Read(buf[:])
		fmt.Printf("backup: %s\n", buf[:n])
	case resp.StatusCode == http.StatusUnauthorized:
		fmt.Fprintf(os.Stderr, "backup: unauthorized (check global.api_token)\n")
		os.Exit(1)
	default:
		fmt.Fprintf(os.Stderr, "backup: daemon returned %d\n", resp.StatusCode)
		os.Exit(1)
	}
}

func postCmd(endpoint string) {
	fs := flag.NewFlagSet(endpoint, flag.ExitOnError)
	cfgPath := fs.String("config", findConfig(), "config file path")
	fs.Parse(os.Args[2:])

	cfg, err := engine.LoadConfig(*cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "config error: %v\n", err)
		os.Exit(1)
	}

	url := fmt.Sprintf("http://%s/%s", cfg.Global.ListenAddr, endpoint)
	req, err := http.NewRequest(http.MethodPost, url, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", endpoint, err)
		os.Exit(1)
	}
	if cfg.Global.APIToken != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.Global.APIToken)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s failed: %v\n", endpoint, err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusOK:
		var buf [64]byte
		n, _ := resp.Body.Read(buf[:])
		fmt.Printf("%s: %s\n", endpoint, buf[:n])
	case resp.StatusCode == http.StatusUnauthorized:
		fmt.Fprintf(os.Stderr, "%s: unauthorized (check global.api_token)\n", endpoint)
		os.Exit(1)
	case resp.StatusCode == http.StatusMethodNotAllowed:
		fmt.Fprintf(os.Stderr, "%s: daemon not responding to POST\n", endpoint)
		os.Exit(1)
	default:
		fmt.Fprintf(os.Stderr, "%s: unexpected status %d\n", endpoint, resp.StatusCode)
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Fprintf(usageOutput, `mc-backup %s — Minecraft server backup daemon

Usage: mc-backup <command> [flags]

Commands:
  run        Start the daemon (backup loop + status API)
  status     Show live backup job dashboard
  backup     Trigger an immediate backup cycle [server]
  scan       Trigger immediate server discovery
  cancel     Abort the current backup cycle
  config     Read or write config values
  update     Download and install the latest binary from GitHub
  version    Print version

run flags:
  --config   Path to config file (default: /etc/mc-backup/config.toml)
  --debug    Enable debug logging (rsync args, SSH commands, etc.)

config actions:
  get <key>   Read a config value (e.g. "global.backup_interval")
  set <key> <value>   Write a config value (e.g. "server.creative.pause_if_no_players true")

Config keys:
  target      Per-server backup target: "local" or "nas"
  local.dest_root      Root directory for local snapshots

Config files: /etc/mc-backup/config.toml, ~/.config/mc-backup/config.toml
Environment overrides: MC_BACKUP_<SECTION>_<KEY> (e.g. MC_BACKUP_GLOBAL_MAX_MBPS=20)

`, version)
}

func updateCmd() {
	if err := runUpdate(); err != nil {
		fmt.Fprintf(os.Stderr, "update failed: %v\n", err)
		os.Exit(1)
	}
}

func runUpdate() error {
	if repoURL == "" {
		return fmt.Errorf("update requires a built binary with embedded repo URL; use ./update.sh from the source repo instead")
	}

	execPath, err := osExecutable()
	if err != nil {
		return fmt.Errorf("cannot determine binary path: %w", err)
	}
	tmpBin := execPath + ".new"

	releaseURL := deriveReleaseURL(repoURL)
	fmt.Printf("Downloading %s\n", releaseURL)
	if err := downloadFile(releaseURL, tmpBin); err != nil {
		return fmt.Errorf("download: %w", err)
	}

	if err := verifyChecksum(tmpBin, releaseURL+".sha256"); err != nil {
		os.Remove(tmpBin)
		return fmt.Errorf("checksum: %w", err)
	}

	steps := []struct {
		name    string
		command string
		args    []string
	}{
		{"Stopping mc-backup service", "sudo", []string{"systemctl", "stop", "mc-backup"}},
		{"Installing mc-backup", "sudo", []string{"mv", tmpBin, execPath}},
		{"Starting mc-backup service", "sudo", []string{"systemctl", "start", "mc-backup"}},
		{"mc-backup service status", "systemctl", []string{"status", "mc-backup", "--no-pager"}},
	}

	for _, step := range steps {
		if err := runUpdateStep("", step.name, step.command, step.args...); err != nil {
			return fmt.Errorf("%s: %w", step.name, err)
		}
	}

	return nil
}

func runCmd() {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	cfgPath := fs.String("config", findConfig(), "config file path")
	debug := fs.Bool("debug", false, "enable debug logging")
	fs.Parse(os.Args[2:])

	cfg, err := engine.LoadConfig(*cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "config error: %v\n", err)
		os.Exit(1)
	}

	d := engine.NewDaemon(*cfgPath, cfg)
	d.Debug = *debug
	if err := d.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "daemon error: %v\n", err)
		os.Exit(1)
	}
}

func statusCmd() {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	cfgPath := fs.String("config", findConfig(), "config file path")
	fs.Parse(os.Args[2:])

	cfg, err := engine.LoadConfig(*cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "config error: %v\n", err)
		os.Exit(1)
	}

	if err := engine.PrintDashboard(cfg.Global.ListenAddr); err != nil {
		fmt.Fprintf(os.Stderr, "status error: %v\n", err)
		os.Exit(1)
	}
}

func configCmd() {
	fs := flag.NewFlagSet("config", flag.ExitOnError)
	cfgPath := fs.String("config", findConfig(), "config file path")
	fs.Parse(os.Args[2:])

	args := fs.Args()
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: mc-backup config <get|set> <key> [value]")
		os.Exit(1)
	}

	switch args[0] {
	case "get":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: mc-backup config get <key>")
			os.Exit(1)
		}
		cfg, err := engine.LoadConfig(*cfgPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "config error: %v\n", err)
			os.Exit(1)
		}
		val := engine.GetConfigValue(cfg, args[1])
		fmt.Println(val)
	case "set":
		if len(args) < 3 {
			fmt.Fprintln(os.Stderr, "usage: mc-backup config set <key> <value>")
			os.Exit(1)
		}
		if err := engine.SetConfigValue(*cfgPath, args[1], args[2]); err != nil {
			fmt.Fprintf(os.Stderr, "config error: %v\n", err)
			os.Exit(1)
		}
	default:
		fmt.Fprintf(os.Stderr, "unknown config action: %s\n", args[0])
		os.Exit(1)
	}
}
