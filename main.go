package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
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
	fmt.Fprintf(os.Stderr, `Usage: mc-backup <command>

Commands:
  run        Start the daemon (backup loop + status API)
  status     Show live backup/archive job dashboard
  config     Read or write config values

Flags:
  --config   Path to config file (default: /etc/mc-backup/config.toml)
`)
}

func runCmd() {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	cfgPath := fs.String("config", findConfig(), "config file path")
	debug := fs.Bool("debug", false, "enable debug logging")
	fs.Parse(os.Args[2:])

	cfg, err := LoadConfig(*cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "config error: %v\n", err)
		os.Exit(1)
	}

	d := NewDaemon(*cfgPath, cfg)
	d.debug = *debug
	if err := d.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "daemon error: %v\n", err)
		os.Exit(1)
	}
}

func statusCmd() {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	cfgPath := fs.String("config", findConfig(), "config file path")
	fs.Parse(os.Args[2:])

	cfg, err := LoadConfig(*cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "config error: %v\n", err)
		os.Exit(1)
	}

	if err := printDashboard(cfg.Global.ListenAddr); err != nil {
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
		cfg, err := LoadConfig(*cfgPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "config error: %v\n", err)
			os.Exit(1)
		}
		val := getConfigValue(cfg, args[1])
		fmt.Println(val)
	case "set":
		if len(args) < 3 {
			fmt.Fprintln(os.Stderr, "usage: mc-backup config set <key> <value>")
			os.Exit(1)
		}
		if err := setConfigValue(*cfgPath, args[1], args[2]); err != nil {
			fmt.Fprintf(os.Stderr, "config error: %v\n", err)
			os.Exit(1)
		}
	default:
		fmt.Fprintf(os.Stderr, "unknown config action: %s\n", args[0])
		os.Exit(1)
	}
}
