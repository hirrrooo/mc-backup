# Resolve Evaluation Concerns Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Resolve repository evaluation concerns by hardening shell command construction, improving command-dependent testability, aligning Go version documentation, and making installation target the intended user's config directory.

**Architecture:** Keep production behavior unchanged except where safety fixes are required. Add a tiny shell-quoting helper and a package-level command execution seam so SSH/rsync/docker command behavior can be tested without real external services. Keep Makefile changes minimal and verifiable with `make -n` dry-run output.

**Tech Stack:** Go, standard library `os/exec`, Go unit tests, GNU Make, Markdown documentation.

---

## Evaluation Concern List

1. Shell/SSH command strings are assembled from config paths without shell quoting in `backup.go`, `archive.go`, and `prune.go`.
2. Full backup-cycle and command-dependent behavior is hard to test because production code directly calls `exec.CommandContext`, `exec.Command`, and real system tools.
3. `go.mod` says `go 1.26.3`, while `README.md` says Go `1.21+`.
4. `sudo make install` writes `${HOME}/.config/mc-backup`, which can target `/root` instead of the invoking user's home.

## File Structure

- Create: `internal/engine/shell.go` for shell argument quoting used in remote command strings.
- Create: `internal/engine/shell_test.go` for quoting behavior and remote-command construction expectations.
- Create: `internal/engine/command.go` for a package-level command execution seam.
- Create: `internal/engine/command_test.go` for command seam test helpers if needed.
- Modify: `internal/engine/backup.go` to quote NAS sentinel and `mkdir -p` remote paths and to use the command seam.
- Modify: `internal/engine/archive.go` to quote NAS sentinel and parent destination paths and to use the command seam.
- Modify: `internal/engine/prune.go` to quote NAS paths in `find`, `ls`, and `xargs` remote commands and to use the command seam.
- Modify: `internal/engine/discovery.go`, `disk.go`, `daemon.go`, and `rcon.go` only where replacing direct `exec.Command*` calls with the command seam improves testability without broad refactoring.
- Modify: `go.mod` to match the documented minimum supported Go version.
- Modify: `README.md` only if the chosen Go directive requires documentation adjustment.
- Modify: `Makefile` to compute an install config directory from `SUDO_USER` when available.

---

### Task 1: Shell-Quote Remote Paths

**Files:**
- Create: `internal/engine/shell.go`
- Create: `internal/engine/shell_test.go`
- Modify: `internal/engine/backup.go`
- Modify: `internal/engine/archive.go`
- Modify: `internal/engine/prune.go`

- [ ] **Step 1: Write failing tests for shell quoting**

Create `internal/engine/shell_test.go`:

```go
package engine

import "testing"

func TestShellQuoteSingleQuotesUnsafePath(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"/volume1/backups", "'/volume1/backups'"},
		{"/volume 1/backups", "'/volume 1/backups'"},
		{"/volume'1/backups", "'/volume'\''1/backups'"},
		{"", "''"},
	}

	for _, tt := range tests {
		got := shellQuote(tt.in)
		if got != tt.want {
			t.Errorf("shellQuote(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestRemoteNASCommandsQuoteConfiguredPaths(t *testing.T) {
	nas := NASConfig{
		SSHUser:  "backup",
		SSHHost:  "nas.local",
		DestRoot: "/volume 1/backups",
	}

	ready := nasReadyCommand(nas)
	if ready != "test -f '/volume 1/backups/.nas-ready'" {
		t.Fatalf("nasReadyCommand() = %q", ready)
	}

	mkdir := nasMkdirCommand("/volume 1/backups/minecraft/server one")
	if mkdir != "mkdir -p '/volume 1/backups/minecraft/server one'" {
		t.Fatalf("nasMkdirCommand() = %q", mkdir)
	}

	days := pruneNASByDaysCommand("/volume 1/backups", "minecraft", "server one", 7)
	wantDays := "find '/volume 1/backups/minecraft/server one' -maxdepth 1 -type d -regex '.*/[0-9]\\{8\\}-[0-9]\\{4\\}' -mtime +7 -exec rm -rf {} +"
	if days != wantDays {
		t.Fatalf("pruneNASByDaysCommand() = %q, want %q", days, wantDays)
	}

	count := pruneNASByCountCommand("/volume 1/backups", "minecraft", "server one", 3)
	wantCount := "ls -dt '/volume 1/backups/minecraft/server one'/[0-9]*-[0-9]* 2>/dev/null | tail -n +4 | xargs rm -rf"
	if count != wantCount {
		t.Fatalf("pruneNASByCountCommand() = %q, want %q", count, wantCount)
	}
}
```

- [ ] **Step 2: Run tests to verify failure**

Run: `go test ./internal/engine -run 'TestShellQuote|TestRemoteNASCommandsQuoteConfiguredPaths'`

Expected: FAIL because `shellQuote`, `nasReadyCommand`, `nasMkdirCommand`, `pruneNASByDaysCommand`, and `pruneNASByCountCommand` do not exist.

- [ ] **Step 3: Add shell quoting helper**

Create `internal/engine/shell.go`:

```go
package engine

import "strings"

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}
```

- [ ] **Step 4: Extract and use quoted remote command builders**

In `internal/engine/backup.go`, add these helpers near `checkNASReady`:

```go
func nasReadyCommand(nas NASConfig) string {
	return fmt.Sprintf("test -f %s", shellQuote(fmt.Sprintf("%s/.nas-ready", nas.DestRoot)))
}

func nasMkdirCommand(path string) string {
	return fmt.Sprintf("mkdir -p %s", shellQuote(path))
}
```

Update `checkNASReady` to append `nasReadyCommand(nas)` instead of `fmt.Sprintf("test -f %s", sentinel)`.

Update `ensureNASDir` to append `nasMkdirCommand(path)` instead of `fmt.Sprintf("mkdir -p %s", path)`.

In `internal/engine/archive.go`, replace the sentinel check command string with `nasReadyCommand(ae.cfg.NAS)`. Leave `ensureNASDir` as the single mkdir implementation.

In `internal/engine/prune.go`, add helpers above `pruneNASByDays`:

```go
func pruneNASByDaysCommand(destRoot, namespace, serverName string, days int) string {
	destDir := fmt.Sprintf("%s/%s/%s", destRoot, namespace, serverName)
	return fmt.Sprintf(
		"find %s -maxdepth 1 -type d -regex '.*/[0-9]\\{8\\}-[0-9]\\{4\\}' -mtime +%d -exec rm -rf {} +",
		shellQuote(destDir), days,
	)
}

func pruneNASByCountCommand(destRoot, namespace, serverName string, count int) string {
	destDir := fmt.Sprintf("%s/%s/%s", destRoot, namespace, serverName)
	return fmt.Sprintf(
		"ls -dt %s/[0-9]*-[0-9]* 2>/dev/null | tail -n +%d | xargs rm -rf",
		shellQuote(destDir), count+1,
	)
}
```

Update `pruneNASByDays` to call `runSSH(ctx, nas, pruneNASByDaysCommand(destRoot, namespace, serverName, days))`.

Update `pruneNASByCount` to call `runSSH(ctx, nas, pruneNASByCountCommand(destRoot, namespace, serverName, count))`.

- [ ] **Step 5: Run targeted tests to verify pass**

Run: `go test ./internal/engine -run 'TestShellQuote|TestRemoteNASCommandsQuoteConfiguredPaths'`

Expected: PASS.

- [ ] **Step 6: Run full test suite**

Run: `go test ./...`

Expected: PASS.

- [ ] **Step 7: Commit**

Run:

```bash
git add internal/engine/shell.go internal/engine/shell_test.go internal/engine/backup.go internal/engine/archive.go internal/engine/prune.go
git commit -m "fix: quote remote NAS command paths"
```

---

### Task 2: Add Command Execution Seam

**Files:**
- Create: `internal/engine/command.go`
- Modify: `internal/engine/backup.go`
- Modify: `internal/engine/archive.go`
- Modify: `internal/engine/discovery.go`
- Modify: `internal/engine/disk.go`
- Modify: `internal/engine/daemon.go`
- Modify: `internal/engine/rcon.go`
- Modify tests in `internal/engine/*_test.go` only if compile adjustments are needed.

- [ ] **Step 1: Write a failing command seam test**

Create `internal/engine/command_test.go`:

```go
package engine

import (
	"context"
	"testing"
)

func TestWithCommandRunnerRestoresPreviousRunner(t *testing.T) {
	original := commandRunner
	called := false

	withCommandRunner(commandRunnerFunc(func(ctx context.Context, name string, args ...string) command {
		called = true
		return fakeCommand{}
	}), func() {
		if commandRunner == original {
			t.Fatal("commandRunner was not replaced inside withCommandRunner")
		}
	})

	if !called {
		t.Fatal("test runner was not called")
	}
	if commandRunner != original {
		t.Fatal("commandRunner was not restored")
	}
}

type fakeCommand struct{}

func (fakeCommand) Run() error { return nil }

func (fakeCommand) Output() ([]byte, error) { return nil, nil }

func (fakeCommand) CombinedOutput() ([]byte, error) { return nil, nil }
```

- [ ] **Step 2: Run test to verify failure**

Run: `go test ./internal/engine -run TestWithCommandRunnerRestoresPreviousRunner`

Expected: FAIL because `commandRunner`, `commandRunnerFunc`, `command`, and `withCommandRunner` do not exist.

- [ ] **Step 3: Add minimal command seam**

Create `internal/engine/command.go`:

```go
package engine

import (
	"context"
	"os/exec"
	"sync"
)

type command interface {
	Run() error
	Output() ([]byte, error)
	CombinedOutput() ([]byte, error)
}

type commandRunnerFunc func(ctx context.Context, name string, args ...string) command

func (f commandRunnerFunc) CommandContext(ctx context.Context, name string, args ...string) command {
	return f(ctx, name, args...)
}

type commandRunnerInterface interface {
	CommandContext(ctx context.Context, name string, args ...string) command
}

type realCommandRunner struct{}

func (realCommandRunner) CommandContext(ctx context.Context, name string, args ...string) command {
	return exec.CommandContext(ctx, name, args...)
}

var commandRunner commandRunnerInterface = realCommandRunner{}

var commandRunnerMu sync.Mutex

func withCommandRunner(r commandRunnerInterface, fn func()) {
	commandRunnerMu.Lock()
	defer commandRunnerMu.Unlock()
	previous := commandRunner
	commandRunner = r
	defer func() { commandRunner = previous }()
	fn()
}
```

- [ ] **Step 4: Fix the test to call the seam**

Update `TestWithCommandRunnerRestoresPreviousRunner` so the callback invokes the seam:

```go
func TestWithCommandRunnerRestoresPreviousRunner(t *testing.T) {
	original := commandRunner
	called := false

	withCommandRunner(commandRunnerFunc(func(ctx context.Context, name string, args ...string) command {
		called = true
		return fakeCommand{}
	}), func() {
		if commandRunner == original {
			t.Fatal("commandRunner was not replaced inside withCommandRunner")
		}
		if err := commandRunner.CommandContext(context.Background(), "true").Run(); err != nil {
			t.Fatalf("fake command failed: %v", err)
		}
	})

	if !called {
		t.Fatal("test runner was not called")
	}
	if commandRunner != original {
		t.Fatal("commandRunner was not restored")
	}
}
```

- [ ] **Step 5: Replace direct `exec.CommandContext` calls**

In these files, replace `exec.CommandContext(ctx, args[0], args[1:]...)` with `commandRunner.CommandContext(ctx, args[0], args[1:]...)`:

- `internal/engine/backup.go`
- `internal/engine/archive.go`
- `internal/engine/prune.go`
- `internal/engine/rcon.go`
- `internal/engine/daemon.go`

In `internal/engine/discovery.go`, replace context-free calls with `commandRunner.CommandContext(context.Background(), ...)` and add `context` to imports.

In `internal/engine/disk.go`, replace `exec.Command("du", "-sb", path)` with `commandRunner.CommandContext(context.Background(), "du", "-sb", path)` and add `context` to imports.

Remove `os/exec` imports where no longer used.

- [ ] **Step 6: Run command seam test**

Run: `go test ./internal/engine -run TestWithCommandRunnerRestoresPreviousRunner`

Expected: PASS.

- [ ] **Step 7: Run full test suite**

Run: `go test ./...`

Expected: PASS.

- [ ] **Step 8: Commit**

Run:

```bash
git add internal/engine/command.go internal/engine/command_test.go internal/engine/backup.go internal/engine/archive.go internal/engine/discovery.go internal/engine/disk.go internal/engine/daemon.go internal/engine/rcon.go
git commit -m "test: add command execution seam"
```

---

### Task 3: Align Go Version Metadata

**Files:**
- Modify: `go.mod`
- Modify: `README.md` only if the supported minimum changes.

- [ ] **Step 1: Write a failing metadata check**

Run: `test "$(grep '^go ' go.mod)" = "go 1.21"`

Expected: FAIL because `go.mod` currently says `go 1.26.3`.

- [ ] **Step 2: Set Go directive to the documented minimum**

Update `go.mod` line 3:

```go
go 1.21
```

Do not change `README.md` because it already says Go `1.21+`, and the code uses `log/slog`, which requires Go 1.21.

- [ ] **Step 3: Verify metadata alignment**

Run: `test "$(grep '^go ' go.mod)" = "go 1.21"`

Expected: exit 0.

- [ ] **Step 4: Verify build and tests**

Run: `go test ./...`

Expected: PASS.

- [ ] **Step 5: Commit**

Run:

```bash
git add go.mod
git commit -m "chore: align Go version metadata"
```

---

### Task 4: Make Install Use Invoking User Config Directory

**Files:**
- Modify: `Makefile`

- [ ] **Step 1: Write failing dry-run checks**

Run:

```bash
make -n install HOME=/root SUDO_USER=hiro 2>/dev/null | grep '/home/hiro/.config/mc-backup'
```

Expected: FAIL because the current Makefile uses `${HOME}/.config/mc-backup`, which expands to `/root/.config/mc-backup` in this scenario.

- [ ] **Step 2: Add install user home variables**

Modify the variable block at the top of `Makefile` to include:

```make
INSTALL_USER_HOME := $(shell if [ -n "$$SUDO_USER" ] && [ "$$SUDO_USER" != "root" ]; then getent passwd "$$SUDO_USER" | cut -d: -f6; else printf '%s' "$$HOME"; fi)
USER_CONFDIR := $(INSTALL_USER_HOME)/.config/mc-backup
```

- [ ] **Step 3: Use `USER_CONFDIR` in install target**

Change the install target lines from:

```make
	install -d $(DESTDIR)$(BINDIR) $(DESTDIR)$(CONFDIR) ${HOME}/.config/mc-backup
	install -m 755 $(BINARY) $(DESTDIR)$(BINDIR)/$(BINARY)
	[ -f ${HOME}/.config/mc-backup/config.toml ] || install -m 644 config.example.toml ${HOME}/.config/mc-backup/config.toml
```

to:

```make
	install -d $(DESTDIR)$(BINDIR) $(DESTDIR)$(CONFDIR) $(USER_CONFDIR)
	install -m 755 $(BINARY) $(DESTDIR)$(BINDIR)/$(BINARY)
	[ -f $(USER_CONFDIR)/config.toml ] || install -m 644 config.example.toml $(USER_CONFDIR)/config.toml
```

- [ ] **Step 4: Verify sudo-user dry-run path**

Run:

```bash
make -n install HOME=/root SUDO_USER=hiro 2>/dev/null | grep '/home/hiro/.config/mc-backup'
```

Expected: PASS with at least one matching dry-run line.

- [ ] **Step 5: Verify non-sudo dry-run path**

Run:

```bash
make -n install HOME=/tmp/example-home SUDO_USER= 2>/dev/null | grep '/tmp/example-home/.config/mc-backup'
```

Expected: PASS with at least one matching dry-run line.

- [ ] **Step 6: Run full test suite**

Run: `go test ./...`

Expected: PASS.

- [ ] **Step 7: Commit**

Run:

```bash
git add Makefile
git commit -m "fix: install config for invoking user"
```

---

### Task 5: Final Verification and Documentation Review

**Files:**
- Review: `README.md`
- Review: `config.example.toml`
- Review: all modified files from Tasks 1-4.

- [ ] **Step 1: Run full verification**

Run: `go test ./...`

Expected: PASS.

- [ ] **Step 2: Run Makefile dry-run checks**

Run:

```bash
make -n install HOME=/root SUDO_USER=hiro 2>/dev/null | grep '/home/hiro/.config/mc-backup'
```

Expected: PASS.

Run:

```bash
make -n install HOME=/tmp/example-home SUDO_USER= 2>/dev/null | grep '/tmp/example-home/.config/mc-backup'
```

Expected: PASS.

- [ ] **Step 3: Check final diff**

Run: `git diff --stat`

Expected: only files listed in this plan are modified.

- [ ] **Step 4: Confirm concerns are resolved**

Check each item:

- Shell/SSH command strings quote configured remote paths.
- Command-dependent code can be tested through `withCommandRunner` without real Docker, SSH, rsync, or du.
- `go.mod` and `README.md` agree on Go `1.21+` support.
- `make install` targets the invoking user's config directory when run via sudo.

- [ ] **Step 5: Commit final docs adjustment if needed**

If Task 5 changes documentation, run:

```bash
git add README.md config.example.toml
git commit -m "docs: update installation notes"
```

If Task 5 makes no file changes, skip this commit.

---

## Self-Review

**Spec coverage:** All four evaluation concerns map directly to Tasks 1-4, with Task 5 providing final verification.

**Placeholder scan:** No task contains `TBD`, `TODO`, or unspecified implementation language. Each code change has exact file paths and concrete snippets.

**Type consistency:** Planned helper names are consistent across tests and implementation: `shellQuote`, `nasReadyCommand`, `nasMkdirCommand`, `pruneNASByDaysCommand`, `pruneNASByCountCommand`, `commandRunner`, `commandRunnerFunc`, and `withCommandRunner`.
