package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"mc-backup/internal/engine"
)

const version = "0.1.0"

var defaultConfigPaths = []string{
	"/etc/mc-backup/config.toml",
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
	case "version", "--version", "-v":
		fmt.Printf("mc-backup %s\n", version)
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", os.Args[1])
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Fprintf(os.Stderr, `mc-backup %s — Minecraft server backup daemon

Usage: mc-backup <command> [flags]

Commands:
  run        Start the daemon (backup loop + status API)
  status     Show live backup/archive job dashboard
  config     Read or write config values
  version    Print version

run flags:
  --config   Path to config file (default: /etc/mc-backup/config.toml)
  --debug    Enable debug logging (rsync args, SSH commands, etc.)

config actions:
  get <key>   Read a config value (e.g. "global.backup_interval")
  set <key> <value>   Write a config value (e.g. "server.creative.pause_if_no_players true")

Config files: /etc/mc-backup/config.toml, ~/.config/mc-backup/config.toml
Environment overrides: MC_BACKUP_<SECTION>_<KEY> (e.g. MC_BACKUP_GLOBAL_MAX_MBPS=20)

Server discovery: drops a directory under a watch path — auto-provisioned within 1 min.
  curl -X POST http://localhost:47990/scan   triggers immediate discovery
  curl -X POST http://localhost:47990/backup runs a backup cycle on demand
  curl -X POST http://localhost:47990/cancel aborts the current backup cycle
  curl http://localhost:47990/status         JSON job status

`, version)
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
