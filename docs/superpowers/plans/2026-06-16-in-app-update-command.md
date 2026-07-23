# In-App Update Command Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `mc-backup update` so users can update the application from the CLI.

**Architecture:** Implement the update command in `cmd/mc-backup/main.go`, following the existing command switch style. Add small helper functions for repository-root detection and command execution so command sequencing is testable without running `git pull`, `sudo make install`, or `systemctl restart` in tests.

**Tech Stack:** Go standard library, `os/exec`, Go unit tests.

---

## File Structure

- Modify: `cmd/mc-backup/main.go` to add the `update` command, helper functions, and usage text.
- Create: `cmd/mc-backup/main_test.go` to verify command sequencing and usage wiring without executing real update commands.

---

### Task 1: Add Testable Update Command

**Files:**
- Modify: `cmd/mc-backup/main.go`
- Create: `cmd/mc-backup/main_test.go`

- [ ] **Step 1: Write failing tests**

Create `cmd/mc-backup/main_test.go`:

```go
package main

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
)

func TestUpdateCmdRunsExpectedSteps(t *testing.T) {
	var calls []string
	oldFindRepoRoot := findRepoRoot
	oldRunUpdateStep := runUpdateStep
	t.Cleanup(func() {
		findRepoRoot = oldFindRepoRoot
		runUpdateStep = oldRunUpdateStep
	})

	findRepoRoot = func() (string, error) {
		calls = append(calls, "findRepoRoot")
		return "/repo", nil
	}
	runUpdateStep = func(dir, name string, command string, args ...string) error {
		calls = append(calls, dir+":"+name+":"+command+" "+strings.Join(args, " "))
		return nil
	}

	if err := runUpdate(); err != nil {
		t.Fatalf("runUpdate() error = %v", err)
	}

	want := []string{
		"findRepoRoot",
		"/repo:Pulling latest source:git pull --ff-only",
		"/repo:Installing mc-backup:sudo make install",
		"/repo:Restarting mc-backup service:sudo systemctl restart mc-backup",
		"/repo:mc-backup service status:systemctl status mc-backup --no-pager",
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %#v, want %#v", calls, want)
	}
}

func TestPrintUsageIncludesUpdate(t *testing.T) {
	var stderr bytes.Buffer
	oldStderr := usageOutput
	t.Cleanup(func() { usageOutput = oldStderr })
	usageOutput = &stderr

	printUsage()

	if !strings.Contains(stderr.String(), "update     Pull latest source, install, and restart service") {
		t.Fatalf("usage output does not include update command:\n%s", stderr.String())
	}
}
```

- [ ] **Step 2: Run tests to verify failure**

Run:

```bash
go test ./cmd/mc-backup
```

Expected: FAIL because `findRepoRoot`, `runUpdateStep`, `runUpdate`, and `usageOutput` do not exist yet.

- [ ] **Step 3: Implement the update command**

Modify `cmd/mc-backup/main.go`:

1. Add imports:

```go
	"io"
	"os/exec"
```

2. Add package-level seams after `defaultConfigPaths`:

```go
var usageOutput io.Writer = os.Stderr

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
```

Also add `"strings"` to imports.

3. Add the switch case:

```go
	case "update":
		updateCmd()
```

4. Add the command implementation:

```go
func updateCmd() {
	if err := runUpdate(); err != nil {
		fmt.Fprintf(os.Stderr, "update failed: %v\n", err)
		os.Exit(1)
	}
}

func runUpdate() error {
	repoRoot, err := findRepoRoot()
	if err != nil {
		return err
	}

	steps := []struct {
		name    string
		command string
		args    []string
	}{
		{"Pulling latest source", "git", []string{"pull", "--ff-only"}},
		{"Installing mc-backup", "sudo", []string{"make", "install"}},
		{"Restarting mc-backup service", "sudo", []string{"systemctl", "restart", "mc-backup"}},
		{"mc-backup service status", "systemctl", []string{"status", "mc-backup", "--no-pager"}},
	}

	for _, step := range steps {
		if err := runUpdateStep(repoRoot, step.name, step.command, step.args...); err != nil {
			return fmt.Errorf("%s: %w", step.name, err)
		}
	}

	return nil
}
```

5. Change `printUsage()` to write to `usageOutput` and include the new command:

```go
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
```

- [ ] **Step 4: Run command package tests**

Run:

```bash
go test ./cmd/mc-backup
```

Expected: PASS.

- [ ] **Step 5: Run full test suite**

Run:

```bash
go test ./...
```

Expected: PASS.

- [ ] **Step 6: Do not execute real update flow**

Do not run:

```bash
mc-backup update
```

Reason: it would pull source, install the binary, and restart the local service.

---

## Self-Review

- Spec coverage: The plan adds `mc-backup update`, resolves the repo root, runs the four update commands in order, streams stdio, updates help text, and avoids executing the real update flow in tests.
- Placeholder scan: No placeholders remain.
- Type consistency: Helper names and signatures are consistent across tests and implementation.
