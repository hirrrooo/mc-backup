package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"mc-backup/internal/engine"
)

const version = "0.1.0"

var defaultConfigPaths = []string{
	"/etc/mc-backup/config.toml",
}

var usageOutput io.Writer = os.Stderr

var repoURL = "" // set via -ldflags

var osUserHomeDir = os.UserHomeDir

var osExecutable = os.Executable

var ensureRepo = func(cacheDir, url string) (string, error) {
	if _, err := os.Stat(cacheDir); os.IsNotExist(err) {
		fmt.Printf("Cloning %s into %s\n", url, cacheDir)
		cmd := exec.Command("git", "clone", url, cacheDir)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return "", fmt.Errorf("clone: %w", err)
		}
	} else {
		fmt.Printf("Updating cached repo at %s\n", cacheDir)
		cmd := exec.Command("git", "-C", cacheDir, "pull", "--ff-only")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return "", fmt.Errorf("pull: %w", err)
		}
	}
	return cacheDir, nil
}

var findRepoRoot = func() (string, error) {
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("not inside a git repository")
	}
	return strings.TrimSpace(string(out)), nil
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
	fs.Parse(os.Args[2:])

	cfg, err := engine.LoadConfig(*cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "config error: %v\n", err)
		os.Exit(1)
	}

	server := fs.Arg(0)

	backendURL := fmt.Sprintf("http://%s/backup", cfg.Global.ListenAddr)
	if server != "" {
		backendURL += "?server=" + url.QueryEscape(server)
	}
	req, err := http.NewRequest(http.MethodPost, backendURL, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "backup: %v\n", err)
		os.Exit(1)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "backup failed: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	var buf [64]byte
	n, _ := resp.Body.Read(buf[:])
	switch {
	case resp.StatusCode == http.StatusOK:
		fmt.Printf("backup: %s\n", buf[:n])
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
  status     Show live backup/archive job dashboard
  backup     Trigger an immediate backup cycle [server]
  scan       Trigger immediate server discovery
  cancel     Abort the current backup cycle
  config     Read or write config values
  update     Pull latest source, install, and restart service
  version    Print version

run flags:
  --config   Path to config file (default: /etc/mc-backup/config.toml)
  --debug    Enable debug logging (rsync args, SSH commands, etc.)

config actions:
  get <key>   Read a config value (e.g. "global.backup_interval")
  set <key> <value>   Write a config value (e.g. "server.creative.pause_if_no_players true")

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
	home, err := osUserHomeDir()
	if err != nil {
		return fmt.Errorf("cannot determine home directory: %w", err)
	}
	cacheDir := filepath.Join(home, ".cache", "mc-backup", "source")

	if repoURL == "" {
		return errors.New("update requires a built binary with embedded repo URL; use ./update.sh from the source repo instead")
	}
	repoRoot, err := ensureRepo(cacheDir, repoURL)
	if err != nil {
		return err
	}

	execPath, err := osExecutable()
	if err != nil {
		return fmt.Errorf("cannot determine binary path: %w", err)
	}
	tmpBin := execPath + ".new"

	steps := []struct {
		name    string
		command string
		args    []string
	}{
		{"Stopping mc-backup service", "sudo", []string{"systemctl", "stop", "mc-backup"}},
		{"Building mc-backup", "go", []string{"build", "-ldflags", fmt.Sprintf("-X main.repoURL=%s", repoURL), "-o", tmpBin, "./cmd/mc-backup"}},
		{"Installing mc-backup", "sudo", []string{"mv", tmpBin, execPath}},
		{"Starting mc-backup service", "sudo", []string{"systemctl", "start", "mc-backup"}},
		{"mc-backup service status", "systemctl", []string{"status", "mc-backup", "--no-pager"}},
	}

	for _, step := range steps {
		if err := runUpdateStep(repoRoot, step.name, step.command, step.args...); err != nil {
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
