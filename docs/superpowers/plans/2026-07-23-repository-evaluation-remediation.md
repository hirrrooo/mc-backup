# Repository Evaluation Remediation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fully remediate all Phase 0–5 items in `docs/repo-evaluation-2026-07-23.md` according to approved design `docs/superpowers/specs/2026-07-23-repository-evaluation-remediation-design.md`.

**Architecture:** Phased remediation sequence across repository hygiene/docs, CI quality gates, security (RCON password env propagation and status API bearer authentication with loopback bind safety), correctness (configurable excludes with nil-vs-empty fallback, NAS count prune safety, sync context propagation, CLI response read capping), linting/coverage (>65% engine coverage), and Makefile/validation polish.

**Tech Stack:** Go 1.21, GitHub Actions, TOML (`github.com/BurntSushi/toml`), `golangci-lint`, systemd/rsync/RCON integrations.

---

## Task Mapping & Sequence Overview

- **Phase 0 — Hygiene & Documentation Truth:** Tasks 1–4 (Eval 0.1–0.4, 1.1–1.3)
- **Phase 1 — CI Safety Net:** Tasks 5–6 (Eval 4.1)
- **Phase 2 — Security:** Tasks 7–9 (Eval 2.1, 2.2, 2.3)
- **Phase 3 — Correctness & Flexibility:** Tasks 10–14 (Eval 3.1–3.5)
- **Phase 4 — Linting & Coverage:** Tasks 15–16 (Eval 4.2, 4.3)
- **Phase 5 — Polish & Operational Documentation:** Tasks 17–20 (Eval 5.1–5.4, Final Verification)

---

## Phase 0 — Repository Hygiene & Documentation Truth

### Task 1: Untrack and Clean Python Virtualenv and Scratch File (Eval 0.1, 0.2)

**Files:**
- Modify: `.gitignore`
- Remove untracked/staged: `venv/`, `test_tz.go`
- Preserve working files: `docs/repo-evaluation-2026-07-23.md`, `docs/superpowers/specs/2026-07-21-local-backup-target-design.md`

- [ ] **Step 1: Check git tracking status of `venv/` and `test_tz.go`**

Run: `git ls-files venv test_tz.go`
Expected: Lists any tracked files in `venv/` or `test_tz.go`.

- [ ] **Step 2: Remove untracked/staged `venv/` and `test_tz.go` and update `.gitignore`**

Explicitly preserve `docs/repo-evaluation-2026-07-23.md` and modified `docs/superpowers/specs/2026-07-21-local-backup-target-design.md`.

If tracked, run `git rm -r --cached venv test_tz.go 2>/dev/null || true`.
Delete from disk if present: `rm -rf venv test_tz.go`.

Update `.gitignore`:
```gitignore
/mc-backup
*-auto.toml
*.swp
*.swo
*~
.DS_Store
.claude
.worktrees/
venv/
```

- [ ] **Step 3: Verify cleanliness and build**

Run: `git status --porcelain`
Expected: `venv/` and `test_tz.go` do not appear. `docs/repo-evaluation-2026-07-23.md` and `docs/superpowers/specs/2026-07-21-local-backup-target-design.md` remain intact.

Run: `go build ./...`
Expected: PASS.

- [ ] **Step 4: Commit Phase 0 Task 1**

Stage ONLY the exact `.gitignore` file (never use blanket `git add .`):
```bash
git add .gitignore
git commit -m "chore(repo): remove untracked venv and scratch test_tz file"
```

---

### Task 2: Update `.gitignore` to Track Design Docs and Specs (Eval 0.3)

**Files:**
- Modify: `.gitignore`

- [ ] **Step 1: Remove ignore patterns for specs and plans**

In `.gitignore`, ensure `docs/superpowers/plans/` and `docs/superpowers/specs/` are NOT listed.

- [ ] **Step 2: Verify git check-ignore reports docs are tracked**

Run: `git check-ignore docs/superpowers/specs/2026-07-23-repository-evaluation-remediation-design.md`
Expected: Returns exit code 1 (not ignored).

- [ ] **Step 3: Commit Phase 0 Task 2**

```bash
git add .gitignore
git commit -m "chore(repo): track design specs and plans in version control"
```

---

### Task 3: Reconcile Go Toolchain Version Across Project Files (Eval 0.4)

**Files:**
- Modify: `.tool-versions:1`

- [ ] **Step 1: Reconcile `.tool-versions` with `go.mod` (Go 1.21)**

Update `.tool-versions`:
```text
golang 1.21.13
```

- [ ] **Step 2: Verify version alignment across project files**

Run: `cat .tool-versions && grep "^go " go.mod && grep -i "go 1" README.md`
Expected:
`.tool-versions` specifies `golang 1.21.13`
`go.mod` specifies `go 1.21`
`README.md` specifies `Go 1.21+`

- [ ] **Step 3: Commit Phase 0 Task 3**

```bash
git add .tool-versions
git commit -m "chore(repo): align .tool-versions with go.mod Go 1.21 version"
```

---

### Task 4: Correct `AGENTS.md` Documentation Claims (Eval 1.1, 1.2, 1.3)

**Files:**
- Modify: `AGENTS.md`

- [ ] **Step 1: Update `AGENTS.md` build, test, and architecture descriptions**

In `AGENTS.md`:
1. Change "No CI, no pre-commit hooks." to: "CI via GitHub Actions (`.github/workflows/release.yml` for releases with rolling `latest` tag; `.github/workflows/ci.yml` for PR and push checks)."
2. Change "NAS writes are serialized via a buffered channel (`nasWriteLock`, size 1)" to: "Backup cycles (including NAS writes and `lastBackups` state updates) are serialized by `Daemon.cycleMu`."
3. Spot-check and preserve accurate README claims (e.g. `global.max_mbps` bandwidth limiting via rsync `--bwlimit`).

- [ ] **Step 2: Verify `AGENTS.md` contents**

Run: `git diff AGENTS.md`
Expected: Shows updated CI and `Daemon.cycleMu` synchronization descriptions.

- [ ] **Step 3: Commit Phase 0 Task 4**

```bash
git add AGENTS.md
git commit -m "docs(agents): update CI description and cycleMu serialization mechanism"
```

---

## Phase 1 — CI Safety Net

### Task 5: Create Pull Request and Push CI Workflow (`ci.yml`) (Eval 4.1)

**Files:**
- Create: `.github/workflows/ci.yml`

- [ ] **Step 1: Define GitHub Actions `ci.yml` workflow**

Write `.github/workflows/ci.yml`:
```yaml
name: CI

on:
  push:
    branches:
      - main
  pull_request:

jobs:
  check:
    name: Test and Lint
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod

      - name: Verify Formatting
        run: |
          if [ -n "$(gofmt -l internal cmd)" ]; then
            echo "Unformatted files found:"
            gofmt -l internal cmd
            exit 1
          fi

      - name: Go Vet
        run: go vet ./...

      - name: Run Tests with Race and Coverage
        run: go test -v -race -cover ./...
```

- [ ] **Step 2: Run local equivalent commands to verify they pass**

Run: `test -z "$(gofmt -l internal cmd)" && go vet ./... && go test -race -cover ./...`
Expected: PASS with 0 exit code.

- [ ] **Step 3: Commit Phase 1 Task 5**

```bash
git add .github/workflows/ci.yml
git commit -m "ci: add pull request and push CI workflow with fmt, vet, and race testing"
```

---

### Task 6: Add In-Workflow Pre-Publish Quality Gates to `release.yml` (Eval 4.1)

**Files:**
- Modify: `.github/workflows/release.yml`

- [ ] **Step 1: Add independent formatting, vet, and race checks to `release.yml` before build step**

In `.github/workflows/release.yml`:
```yaml
      - name: Verify Formatting
        run: |
          if [ -n "$(gofmt -l internal cmd)" ]; then
            echo "Unformatted files found:"
            gofmt -l internal cmd
            exit 1
          fi

      - name: Go Vet
        run: go vet ./...

      - name: Test
        run: go test -race ./...
```

- [ ] **Step 2: Validate workflow YAML syntax**

Run: `python3 -c "import sys, yaml; yaml.safe_load(open(sys.argv[1]))" .github/workflows/release.yml` (or `ruby -e "require 'yaml'; YAML.load_file(ARGV[0])" .github/workflows/release.yml`)
Expected: Exit code 0 (valid YAML file parsed cleanly).

- [ ] **Step 3: Commit Phase 1 Task 6**

```bash
git add .github/workflows/release.yml
git commit -m "ci(release): add in-workflow formatting, vet, and race quality gates before publication"
```

---

## Phase 2 — Security

### Task 7: Inject RCON Password via Process Environment Instead of Command Arguments (Eval 2.1)

**Files:**
- Modify: `internal/engine/command.go`
- Modify: `internal/engine/rcon.go`
- Test: `internal/engine/rcon_test.go`

- [ ] **Step 1: Write failing test verifying RCON password is omitted from argv and passed via Env**

In `internal/engine/rcon_test.go`:
```go
package engine

import (
	"context"
	"io"
	"strings"
	"testing"
)

type captureEnvRunner struct {
	lastArgs []string
	lastEnv  []string
}

func (c *captureEnvRunner) CommandContext(ctx context.Context, name string, args ...string) command {
	c.lastArgs = append([]string{name}, args...)
	return &captureCommand{runner: c}
}

type captureCommand struct {
	runner *captureEnvRunner
}

func (c *captureCommand) Run() error { return nil }
func (c *captureCommand) Output() ([]byte, error) { return []byte("ok"), nil }
func (c *captureCommand) CombinedOutput() ([]byte, error) { return []byte("ok"), nil }
func (c *captureCommand) SetStdout(w io.Writer) {}
func (c *captureCommand) SetStderr(w io.Writer) {}
func (c *captureCommand) SetEnv(env []string) {
	c.runner.lastEnv = env
}

func TestExecRconOmitsPasswordFromArgv(t *testing.T) {
	runner := &captureEnvRunner{}
	withCommandRunner(runner, func() {
		err := runRcon(context.Background(), "mc-container", "SecretPass123!", "save-off", 1, 0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	for _, arg := range runner.lastArgs {
		if strings.Contains(arg, "SecretPass123!") {
			t.Errorf("RCON password leaked in command argument: %s", arg)
		}
	}

	foundEnv := false
	for _, env := range runner.lastEnv {
		if env == "RCON_PASSWORD=SecretPass123!" {
			foundEnv = true
			break
		}
	}
	if !foundEnv {
		t.Errorf("expected RCON_PASSWORD=SecretPass123! in process env, got: %v", runner.lastEnv)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -v ./internal/engine -run TestExecRconOmitsPasswordFromArgv`
Expected: FAIL due to password being present in `runner.lastArgs` or `SetEnv` method missing on `command`.

- [ ] **Step 3: Update `command.go`, `rcon.go` to support environment injection**

In `internal/engine/command.go`:
```go
type command interface {
	Run() error
	Output() ([]byte, error)
	CombinedOutput() ([]byte, error)
	SetStdout(io.Writer)
	SetStderr(io.Writer)
	SetEnv([]string)
}

func (c execCommand) SetEnv(env []string) {
	if len(c.cmd.Env) == 0 {
		c.cmd.Env = append(os.Environ(), env...)
	} else {
		c.cmd.Env = append(c.cmd.Env, env...)
	}
}
```

In `internal/engine/rcon.go`:
```go
func rconCommand(container, cmd string) []string {
	return []string{
		"docker", "exec",
		"-e", "RCON_PASSWORD",
		container,
		"rcon-cli",
		cmd,
	}
}

func runRcon(ctx context.Context, container, password, command string, retries int, retryInterval time.Duration) error {
	args := rconCommand(container, command)
	for i := 0; i < retries; i++ {
		cmd := commandRunner.CommandContext(ctx, args[0], args[1:]...)
		cmd.SetEnv([]string{fmt.Sprintf("RCON_PASSWORD=%s", password)})
		out, err := cmd.CombinedOutput()
		if err == nil {
			return nil
		}
		slog.Warn("rcon command failed, retrying",
			"command", command,
			"attempt", i+1,
			"max", retries,
			"output", string(out),
			"error", err,
		)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(retryInterval):
		}
	}
	return fmt.Errorf("rcon %q failed after %d retries", command, retries)
}

func rconOutput(ctx context.Context, container, password, command string) (string, error) {
	args := rconCommand(container, command)
	cmd := commandRunner.CommandContext(ctx, args[0], args[1:]...)
	cmd.SetEnv([]string{fmt.Sprintf("RCON_PASSWORD=%s", password)})
	out, err := cmd.CombinedOutput()
	return string(out), err
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -v ./internal/engine -run TestExecRconOmitsPasswordFromArgv`
Expected: PASS.

- [ ] **Step 5: Commit Phase 2 Task 7**

```bash
git add internal/engine/command.go internal/engine/rcon.go internal/engine/rcon_test.go
git commit -m "fix(security): pass RCON_PASSWORD via child process environment instead of command line arguments"
```

---

### Task 8: Implement Status API Bearer Auth and Non-Loopback Listener Validation (Eval 2.2)

**Files:**
- Modify: `internal/engine/config.go`
- Modify: `internal/engine/status.go`
- Modify: `internal/engine/daemon.go`
- Modify: `cmd/mc-backup/main.go`
- Modify: `config.example.toml`
- Modify: `README.md`
- Test: `internal/engine/status_test.go`
- Test: `internal/engine/config_test.go`

- [ ] **Step 1: Write failing tests for Status API authentication and bind validation**

In `internal/engine/config_test.go`:
```go
func TestNonLoopbackBindWithoutTokenFailsValidation(t *testing.T) {
	cfg := &Config{
		Global: GlobalConfig{
			ListenAddr: "0.0.0.0:47990",
			APIToken:   "",
		},
	}
	err := ValidateConfig(cfg)
	if err == nil {
		t.Fatal("expected validation error for non-loopback bind with empty api_token, got nil")
	}
}

func TestLoadConfigValidationIntegration(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	content := `
[global]
listen_addr = "0.0.0.0:47990"
api_token = ""
`
	if err := os.WriteFile(cfgPath, []byte(content), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	_, err := LoadConfig(cfgPath)
	if err == nil || !strings.Contains(err.Error(), "invalid config:") {
		t.Fatalf("expected LoadConfig to return invalid config error, got: %v", err)
	}
}
```

In `internal/engine/status_test.go`:
```go
func TestStatusAPIAuth(t *testing.T) {
	jt := NewJobTracker()
	callbacks := StatusCallbacks{
		OnCancel: func() {},
		OnScan:   func() {},
		OnBackup: func(server string, offline bool) {},
	}
	mux := newStatusMux(jt, callbacks, "secret-token")
	server := httptest.NewServer(mux)
	defer server.Close()

	// POST /backup without token -> 401
	resp, err := http.Post(server.URL+"/backup", "application/json", nil)
	if err != nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 Unauthorized without token, got status %v, err %v", resp.StatusCode, err)
	}

	// POST /backup with invalid token -> 401
	req, _ := http.NewRequest("POST", server.URL+"/backup", nil)
	req.Header.Set("Authorization", "Bearer wrong-token")
	resp, err = http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 Unauthorized with wrong token, got status %v, err %v", resp.StatusCode, err)
	}

	// POST /backup with valid token -> 200
	req, _ = http.NewRequest("POST", server.URL+"/backup", nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	resp, err = http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK with valid token, got status %v, err %v", resp.StatusCode, err)
	}

	// GET /status (read-only) without token -> 200
	resp, err = http.Get(server.URL + "/status")
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK for GET /status without token, got status %v, err %v", resp.StatusCode, err)
	}

	// GET /health (read-only) without token -> 200
	resp, err = http.Get(server.URL + "/health")
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK for GET /health without token, got status %v, err %v", resp.StatusCode, err)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test -v ./internal/engine -run "TestNonLoopbackBindWithoutTokenFailsValidation|TestLoadConfigValidationIntegration|TestStatusAPIAuth"`
Expected: FAIL due to missing `APIToken` field, `ValidateConfig` call in `LoadConfig`, and `newStatusMux`/`requireAuth` implementations.

- [ ] **Step 3: Implement `APIToken` field, `LoadConfig` validation, auth middleware, and CLI header propagation**

In `internal/engine/config.go`:
```go
type GlobalConfig struct {
	ListenAddr     string   `toml:"listen_addr"`
	APIToken       string   `toml:"api_token"`
	BackupInterval Duration `toml:"backup_interval"`
	InitialDelay   Duration `toml:"initial_delay"`
	MaxMBps        float64  `toml:"max_mbps"`
}

func isLoopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	if host == "localhost" || host == "127.0.0.1" || host == "::1" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func ValidateConfig(cfg *Config) error {
	if cfg == nil {
		return fmt.Errorf("config is nil")
	}
	if !isLoopback(cfg.Global.ListenAddr) && cfg.Global.APIToken == "" {
		return fmt.Errorf("non-loopback listen_addr %q requires global.api_token to be configured", cfg.Global.ListenAddr)
	}
	return nil
}

func LoadConfig(path string) (*Config, error) {
	cfg, err := loadConfigFile(path)
	if err != nil {
		return nil, err
	}
	applyEnvOverrides(cfg)
	if err := ValidateConfig(cfg); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}
	return cfg, nil
}
```

In `internal/engine/status.go`:
```go
import "crypto/subtle"

func startStatusServer(addr string, jt *JobTracker, callbacks StatusCallbacks, apiToken string) {
	mux := newStatusMux(jt, callbacks, apiToken)
	go func() {
		slog.Info("status API listening", "addr", addr)
		if err := http.ListenAndServe(addr, mux); err != nil {
			slog.Error("status API server error", "error", err)
		}
	}()
}

func newStatusMux(jt *JobTracker, callbacks StatusCallbacks, apiToken string) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(jt.Snapshot())
	})
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})
	mux.HandleFunc("/cancel", requireAuth(apiToken, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		callbacks.OnCancel()
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("canceled"))
	}))
	mux.HandleFunc("/scan", requireAuth(apiToken, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		callbacks.OnScan()
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("scan triggered"))
	}))
	mux.HandleFunc("/backup", requireAuth(apiToken, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		server := r.URL.Query().Get("server")
		offline := r.URL.Query().Get("offline") == "true"
		callbacks.OnBackup(server, offline)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("backup triggered"))
	}))
	return mux
}

func requireAuth(token string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if token == "" {
			next(w, r)
			return
		}
		authHeader := r.Header.Get("Authorization")
		expected := "Bearer " + token
		if len(authHeader) != len(expected) || subtle.ConstantTimeCompare([]byte(authHeader), []byte(expected)) != 1 {
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte("unauthorized"))
			return
		}
		next(w, r)
	}
}
```

In `internal/engine/daemon.go`:
Update `startStatusServer` call to pass `cfg.Global.APIToken`:
```go
	startStatusServer(cfg.Global.ListenAddr, d.jobTracker, StatusCallbacks{
		OnCancel: d.Cancel,
		OnScan: func() {
			go d.runDiscovery(ctx)
		},
		OnBackup: func(server string, offline bool) {
			go d.runBackupCycle(ctx, server, offline)
		},
	}, cfg.Global.APIToken)
```

In `cmd/mc-backup/main.go`:
Update `backupCmd` and `postCmd` to set `Authorization: Bearer <token>` when `cfg.Global.APIToken != ""`:
```go
	req, err := http.NewRequest(http.MethodPost, ..., nil)
	if err != nil { ... }
	if cfg.Global.APIToken != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.Global.APIToken)
	}
```

In `config.example.toml` and `README.md`:
Document `api_token` configuration, loopback binding security recommendations, and bearer token requirement for remote binding.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test -v ./internal/engine -run "TestNonLoopbackBindWithoutTokenFailsValidation|TestLoadConfigValidationIntegration|TestStatusAPIAuth"`
Expected: PASS.

- [ ] **Step 5: Commit Phase 2 Task 8**

```bash
git add internal/engine/config.go internal/engine/status.go internal/engine/daemon.go cmd/mc-backup/main.go config.example.toml README.md internal/engine/config_test.go internal/engine/status_test.go
git commit -m "feat(security): require bearer authentication for status API mutations and reject unauthenticated non-loopback binds"
```

---

### Task 9: Verify and Test Auto-Provisioned Config File Permissions (Eval 2.3)

**Files:**
- Test: `internal/engine/config_test.go`

- [ ] **Step 1: Write test asserting auto-server TOML permissions**

In `internal/engine/config_test.go`:
```go
func TestSaveAutoServersFilePermissions(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	servers := map[string]ServerConfig{
		"test": {Enabled: true, RconPassword: "secret-password"},
	}
	if err := SaveAutoServers(cfgPath, servers); err != nil {
		t.Fatalf("SaveAutoServers failed: %v", err)
	}
	autoPath := autoServersPath(cfgPath)
	info, err := os.Stat(autoPath)
	if err != nil {
		t.Fatalf("stat failed on %s: %v", autoPath, err)
	}
	perm := info.Mode().Perm()
	if perm&0077 != 0 {
		t.Errorf("expected restricted permissions (0600), got %o", perm)
	}
}
```

- [ ] **Step 2: Run test to verify it passes**

Run: `go test -v ./internal/engine -run TestSaveAutoServersFilePermissions`
Expected: PASS.

- [ ] **Step 3: Commit Phase 2 Task 9**

```bash
git add internal/engine/config_test.go
git commit -m "test(config): verify restricted permissions on auto-provisioned config files"
```

---

## Phase 3 — Correctness & Configuration Flexibility

### Task 10: Configurable rsync Excludes with Explicit Nil-vs-Empty Semantics (Eval 3.1)

**Files:**
- Modify: `internal/engine/config.go`
- Modify: `internal/engine/backup.go`
- Modify: `config.example.toml`
- Modify: `README.md`
- Test: `internal/engine/config_test.go`
- Test: `internal/engine/backup_test.go`

- [ ] **Step 1: Write failing tests for global and per-server excludes resolution, TOML round-trip, env overrides, CLI get/set, and rsync argument construction**

In `internal/engine/config_test.go` and `internal/engine/backup_test.go`:
```go
func TestResolveExcludesNilFallbackAndExplicitEmpty(t *testing.T) {
	global := GlobalConfig{
		Excludes: []string{"*.jar", "cache", "logs", "*.tmp"},
	}

	// nil server.Excludes -> falls back to global
	srv1 := ServerConfig{Excludes: nil}
	res1 := resolveExcludes(global, srv1)
	if len(res1) != 4 {
		t.Errorf("expected 4 global excludes for nil server excludes, got: %v", res1)
	}

	// explicit [] (non-nil empty) server.Excludes -> empty excludes
	empty := []string{}
	srv2 := ServerConfig{Excludes: &empty}
	res2 := resolveExcludes(global, srv2)
	if len(res2) != 0 {
		t.Errorf("expected 0 excludes for explicit empty server excludes, got: %v", res2)
	}
}

func TestLocalRsyncArgsExcludes(t *testing.T) {
	args := localRsyncArgs("/data", "", "/dest", []string{"*.tmp", "logs"})
	foundTmp, foundLogs := false, false
	for _, arg := range args {
		if arg == "--exclude=*.tmp" {
			foundTmp = true
		}
		if arg == "--exclude=logs" {
			foundLogs = true
		}
	}
	if !foundTmp || !foundLogs {
		t.Errorf("expected --exclude args in rsync command, got: %v", args)
	}
}

func TestConfigExcludesTOMLAndEnvOverrides(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	tomlData := `
[global]
excludes = ["*.tmp", "cache"]

[server.creative]
excludes = []
`
	if err := os.WriteFile(cfgPath, []byte(tomlData), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	cfg, err := loadConfigFile(cfgPath)
	if err != nil {
		t.Fatalf("loadConfigFile: %v", err)
	}
	if len(cfg.Global.Excludes) != 2 || cfg.Global.Excludes[0] != "*.tmp" {
		t.Errorf("unexpected global excludes: %v", cfg.Global.Excludes)
	}
	srv, ok := cfg.Servers["creative"]
	if !ok || srv.Excludes == nil || len(*srv.Excludes) != 0 {
		t.Errorf("expected non-nil empty excludes for server creative, got: %v", srv.Excludes)
	}

	// Verify CLI Get/Set
	val := GetConfigValue(cfg, "global.excludes")
	if val != "*.tmp,cache" {
		t.Errorf("expected '*.tmp,cache', got %q", val)
	}
	valSrv := GetConfigValue(cfg, "server.creative.excludes")
	if valSrv != "" {
		t.Errorf("expected empty string for empty slice, got %q", valSrv)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test -v ./internal/engine -run "TestResolveExcludesNilFallbackAndExplicitEmpty|TestLocalRsyncArgsExcludes|TestConfigExcludesTOMLAndEnvOverrides"`
Expected: FAIL due to missing `Excludes` fields and resolution helper.

- [ ] **Step 3: Implement `Excludes` fields, resolution logic using `server.Excludes == nil`, `SaveAutoServers` output, env overrides, CLI get/set, and argument propagation**

In `internal/engine/config.go`:
```go
type GlobalConfig struct {
	ListenAddr     string   `toml:"listen_addr"`
	APIToken       string   `toml:"api_token"`
	BackupInterval Duration `toml:"backup_interval"`
	InitialDelay   Duration `toml:"initial_delay"`
	MaxMBps        float64  `toml:"max_mbps"`
	Excludes       []string `toml:"excludes"`
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

var defaultExcludes = []string{"*.jar", "cache", "logs", "*.tmp"}

func resolveExcludes(global GlobalConfig, server ServerConfig) []string {
	if server.Excludes != nil {
		return *server.Excludes
	}
	if len(global.Excludes) > 0 {
		return global.Excludes
	}
	return defaultExcludes
}
```

Update `serverFieldKeys` in `internal/engine/config.go` to include `"excludes"`.

In `setGlobalField`:
```go
case "excludes":
	if val == "" {
		v.Excludes = nil
	} else {
		v.Excludes = splitCommaList(val)
	}
```

In `setServerField`:
```go
case "excludes":
	if val == "" || strings.EqualFold(val, "none") {
		empty := []string{}
		s.Excludes = &empty
	} else {
		list := splitCommaList(val)
		s.Excludes = &list
	}
```

In `GetConfigValue` / `getGlobalField` / `getServerFieldStr`:
- `global.excludes` returns comma-joined string.
- `server.<name>.excludes` returns comma-joined string if non-nil, or `"<inherited>"` if nil.

In `SaveAutoServers`:
```go
if s.Excludes != nil {
	fmt.Fprintf(f, "excludes = %s\n", formatTOMLSlice(*s.Excludes))
}
```

In `internal/engine/backup.go`:
In `BackupServer`, replace hardcoded `excludes := []string{"*.jar", "cache", "logs", "*.tmp"}` with `excludes := resolveExcludes(be.cfg.Global, server)`.

In `config.example.toml` and `README.md`:
Document `excludes` under `[global]` and `[server.<name>]`, env overrides (`MC_BACKUP_GLOBAL_EXCLUDES`, `MC_BACKUP_SERVER_<NAME>_EXCLUDES`), and CLI config usage.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test -v ./internal/engine -run "TestResolveExcludesNilFallbackAndExplicitEmpty|TestLocalRsyncArgsExcludes|TestConfigExcludesTOMLAndEnvOverrides"`
Expected: PASS.

- [ ] **Step 5: Commit Phase 3 Task 10**

```bash
git add internal/engine/config.go internal/engine/backup.go config.example.toml README.md internal/engine/config_test.go internal/engine/backup_test.go
git commit -m "feat(engine): support global and per-server rsync excludes with nil fallback and explicit empty semantics"
```

---

### Task 11: Add `--no-run-if-empty` (`-r`) Flag to NAS Count Pruning (Eval 3.2)

**Files:**
- Modify: `internal/engine/prune.go`
- Test: `internal/engine/prune_test.go`

- [ ] **Step 1: Write failing test asserting `xargs -r` is present in remote NAS prune command**

In `internal/engine/prune_test.go`:
```go
func TestNASCountPruneCommandUsesNoRunIfEmpty(t *testing.T) {
	cmd := pruneNASByCountCommand("/dest/root", "minecraft", "survival", 5)
	if !strings.Contains(cmd, "xargs -r rm -rf") {
		t.Errorf("expected xargs -r rm -rf in remote NAS count prune command, got: %s", cmd)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -v ./internal/engine -run TestNASCountPruneCommandUsesNoRunIfEmpty`
Expected: FAIL because current command string uses `xargs rm -rf` without `-r`.

- [ ] **Step 3: Update `prune.go` command builder to use `xargs -r rm -rf` while preserving existing signature**

In `internal/engine/prune.go`:
Preserve `pruneNASByCountCommand(destRoot, namespace, serverName string, count int) string` name and signature:
```go
func pruneNASByCountCommand(destRoot, namespace, serverName string, count int) string {
	destDir := fmt.Sprintf("%s/%s/%s", destRoot, namespace, serverName)
	return fmt.Sprintf(
		"ls -dt %s/[0-9]*-[0-9]* 2>/dev/null | tail -n +%d | xargs -r rm -rf",
		shellQuote(destDir), count+1,
	)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -v ./internal/engine -run TestNASCountPruneCommandUsesNoRunIfEmpty`
Expected: PASS.

- [ ] **Step 5: Commit Phase 3 Task 11**

```bash
git add internal/engine/prune.go internal/engine/prune_test.go
git commit -m "fix(prune): add -r to NAS count prune xargs to prevent rm -rf invocation on empty results"
```

---

### Task 12: Context Propagation and Error Handling for System `sync` Command (Eval 3.3)

**Files:**
- Modify: `internal/engine/backup.go`
- Test: `internal/engine/backup_test.go`

- [ ] **Step 1: Write failing test verifying `sync` uses cycle context and logs warnings on failure**

In `internal/engine/backup_test.go`:
```go
func TestSyncCommandUsesCycleContextAndLogsWarning(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-canceled context

	var executedWithCtx bool
	runner := commandRunnerFunc(func(c context.Context, name string, args ...string) command {
		if name == "sync" {
			if c.Err() == context.Canceled {
				executedWithCtx = true
			}
		}
		return execCommandRunner{}.CommandContext(c, name, args...)
	})

	withCommandRunner(runner, func() {
		runSync(ctx)
	})

	if !executedWithCtx {
		t.Error("expected sync command to receive and honor cycle context")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -v ./internal/engine -run TestSyncCommandUsesCycleContextAndLogsWarning`
Expected: FAIL because `backup.go` currently calls `context.Background()` and discards errors.

- [ ] **Step 3: Refactor `sync` invocation in `backup.go`**

In `internal/engine/backup.go`:
```go
func runSync(ctx context.Context) {
	cmd := commandRunner.CommandContext(ctx, "sync")
	if err := cmd.Run(); err != nil {
		slog.Warn("system sync command returned error", "error", err)
	}
}
```
In `BackupServer`, replace line 204 `commandRunner.CommandContext(context.Background(), "sync").Run()` with `runSync(ctx)`.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -v ./internal/engine -run TestSyncCommandUsesCycleContextAndLogsWarning`
Expected: PASS.

- [ ] **Step 5: Commit Phase 3 Task 12**

```bash
git add internal/engine/backup.go internal/engine/backup_test.go
git commit -m "fix(backup): pass cycle context to sync command and log warnings on failure"
```

---

### Task 13: Cap-Plus-One Bounded HTTP Response Reading in CLI (Eval 3.4)

**Files:**
- Modify: `cmd/mc-backup/main.go`
- Test: `cmd/mc-backup/main_test.go`

- [ ] **Step 1: Write failing test for response read capping**

In `cmd/mc-backup/main_test.go`:
```go
func TestReadResponseBodyCapping(t *testing.T) {
	// Over 1 MiB body -> expected error
	largeData := make([]byte, maxResponseBodyBytes+10)
	respLarge := &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(bytes.NewReader(largeData)),
	}
	_, err := readResponseBody(respLarge)
	if err == nil {
		t.Error("expected error when reading response exceeding 1 MiB cap, got nil")
	}

	// Normal body -> expected success
	normalData := []byte("backup triggered")
	respNormal := &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(bytes.NewReader(normalData)),
	}
	body, err := readResponseBody(respNormal)
	if err != nil || string(body) != "backup triggered" {
		t.Errorf("expected 'backup triggered', got body %q, err %v", string(body), err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -v ./cmd/mc-backup -run TestReadResponseBodyCapping`
Expected: FAIL due to missing `readResponseBody` helper and `maxResponseBodyBytes` constant.

- [ ] **Step 3: Implement `readResponseBody` in `cmd/mc-backup/main.go`**

In `cmd/mc-backup/main.go`:
```go
const maxResponseBodyBytes = 1024 * 1024 // 1 MiB

func readResponseBody(resp *http.Response) ([]byte, error) {
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP error %d: %s", resp.StatusCode, resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, int64(maxResponseBodyBytes)+1))
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}
	if len(body) > maxResponseBodyBytes {
		return nil, fmt.Errorf("response body exceeded maximum allowed limit of %d bytes", maxResponseBodyBytes)
	}
	return body, nil
}
```
Replace fixed `[64]byte` reads in CLI commands with `readResponseBody(resp)`.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -v ./cmd/mc-backup -run TestReadResponseBodyCapping`
Expected: PASS.

- [ ] **Step 5: Commit Phase 3 Task 13**

```bash
git add cmd/mc-backup/main.go cmd/mc-backup/main_test.go
git commit -m "fix(cli): use cap-plus-one bounded reader for HTTP status API responses"
```

---

### Task 14: Document `d.lastBackups` Race Freedom Under `Daemon.cycleMu` (Eval 3.5)

**Files:**
- Modify: `internal/engine/daemon.go`

- [ ] **Step 1: Add documentation comment to `Daemon.lastBackups` field**

In `internal/engine/daemon.go`:
Preserve field type `lastBackups map[string]*lastBackup` in `Daemon` struct:
```go
type Daemon struct {
	// ...
	// lastBackups stores the last recorded snapshot paths per server (namespace/serverName -> *lastBackup).
	// Concurrency guarantee: All reads and writes to lastBackups after daemon startup
	// are serialized strictly under Daemon.cycleMu.
	lastBackups map[string]*lastBackup
}
```

- [ ] **Step 2: Run race detection test suite**

Run: `go test -race ./...`
Expected: PASS with 0 data races detected.

- [ ] **Step 3: Commit Phase 3 Task 14**

```bash
git add internal/engine/daemon.go
git commit -m "docs(daemon): document cycleMu synchronization invariant for lastBackups"
```

---

## Phase 4 — Linting, Coverage, and Tooling

### Task 15: Configure `golangci-lint` and Wire into Makefile and Workflows (Eval 4.2)

**Files:**
- Create: `.golangci.yml`
- Modify: `Makefile`
- Modify: `.github/workflows/ci.yml`
- Modify: `.github/workflows/release.yml`

- [ ] **Step 1: Create `.golangci.yml` configuration**

Write `.golangci.yml`:
```yaml
run:
  timeout: 5m
  modules-download-mode: readonly

linters:
  enable:
    - errcheck
    - ineffassign
    - staticcheck
    - gosec

issues:
  exclude-use-default: false
  max-issues-per-linter: 0
  max-same-issues: 0
```

- [ ] **Step 2: Add `lint` target to `Makefile` and steps to workflows**

In `Makefile`:
```makefile
.PHONY: build install uninstall clean lint

lint:
	golangci-lint run ./...
```

In `.github/workflows/ci.yml` and `.github/workflows/release.yml`:
```yaml
      - name: Run golangci-lint
        uses: golangci/golangci-lint-action@v6
        with:
          version: v1.55.2
```

- [ ] **Step 3: Run linter locally and fix any reported issues**

Run: `golangci-lint run ./...`
Expected: Clean pass or minor reported issues fixed.

- [ ] **Step 4: Commit Phase 4 Task 15**

```bash
git add .golangci.yml Makefile .github/workflows/ci.yml .github/workflows/release.yml
git commit -m "ci(lint): adopt golangci-lint with errcheck, ineffassign, staticcheck, and gosec"
```

---

### Task 16: Expand Engine Coverage Past 65% and Verify Key Invariants (Eval 4.3)

**Files:**
- Modify/Create: `internal/engine/backup_test.go`
- Modify/Create: `internal/engine/prune_test.go`
- Modify/Create: `internal/engine/config_test.go`
- Modify/Create: `cmd/mc-backup/main_test.go`

- [ ] **Step 1: Write unit tests covering `BackupServer` deferred `save-on`, prune logic, config env overrides, and checksum validation**

Add unit tests for:
1. `TestBackupServerSaveOnRunsOnFailure`: Asserts `save-on` RCON is executed in deferred cleanup even when rsync or flush fails.
2. `TestPruneLocalAndNASDayAndCount`: Tests boundary conditions in local and remote day/count pruning algorithms.
3. `TestConfigEnvOverridesAndSaveSplit`: Verifies `MC_BACKUP_*` environment variable parsing and `saveSplit` TOML round-trips.
4. `TestVerifyChecksumMismatch`: Tests checksum mismatch handling in `cmd/mc-backup/main_test.go`.

- [ ] **Step 2: Run tests and measure package coverage**

Run: `go test -cover ./internal/engine/... ./cmd/mc-backup/...`
Expected: `internal/engine` coverage > 65%. Record exact coverage percentage.

- [ ] **Step 3: Commit Phase 4 Task 16**

```bash
git add internal/engine/backup_test.go internal/engine/prune_test.go internal/engine/config_test.go cmd/mc-backup/main_test.go
git commit -m "test(engine): expand coverage past 65% and verify deferred save-on invariant"
```

---

## Phase 5 — Polish & Operational Documentation

### Task 17: Align Makefile Build Flags with Release Parity (Eval 5.1)

**Files:**
- Modify: `Makefile`

- [ ] **Step 1: Update Makefile `build` target ldflags and env variables**

In `Makefile`:
```makefile
VERSION ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "dev")

build:
	GOOS=$(GOOS) GOARCH=$(GOARCH) CGO_ENABLED=0 go build \
		-ldflags "-X main.repoURL=$(REPO_URL) -X main.version=$(VERSION)" \
		-o $(BINARY) ./cmd/mc-backup
```

- [ ] **Step 2: Test `make build` execution**

Run: `make build && ./mc-backup version`
Expected: Successfully builds and outputs version string.

- [ ] **Step 3: Commit Phase 5 Task 17**

```bash
git add Makefile
git commit -m "build(makefile): align build environment flags and version ldflags with release artifact contract"
```

---

### Task 18: Implement Configuration Validation and Self-Update Documentation (Eval 5.2, 5.3)

**Files:**
- Modify: `internal/engine/config.go`
- Modify: `README.md`
- Test: `internal/engine/config_test.go`

- [ ] **Step 1: Write failing test for config validation errors on invalid NAS config or listen address**

In `internal/engine/config_test.go`:
```go
func TestValidateConfigMissingNASFields(t *testing.T) {
	cfg := &Config{
		Global: GlobalConfig{ListenAddr: "127.0.0.1:47990"},
		NAS: NASConfig{
			SSHHost: "", // missing required SSH host
		},
		Servers: map[string]ServerConfig{
			"srv1": {Target: "nas"},
		},
	}
	err := ValidateConfig(cfg)
	if err == nil {
		t.Error("expected error when server targets NAS but NASConfig ssh_host is missing")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -v ./internal/engine -run TestValidateConfigMissingNASFields`
Expected: FAIL due to missing validation checks for NAS target configuration.

- [ ] **Step 3: Update `ValidateConfig` and `README.md`**

In `internal/engine/config.go`:
```go
func ValidateConfig(cfg *Config) error {
	if cfg == nil {
		return fmt.Errorf("config is nil")
	}
	if !isLoopback(cfg.Global.ListenAddr) && cfg.Global.APIToken == "" {
		return fmt.Errorf("non-loopback listen_addr %q requires global.api_token to be configured", cfg.Global.ListenAddr)
	}
	hasNASTarget := false
	for _, s := range cfg.Servers {
		target := s.Target
		if target == "" {
			target = "nas"
		}
		if target == "nas" {
			hasNASTarget = true
			break
		}
	}
	if hasNASTarget {
		if cfg.NAS.SSHHost == "" || cfg.NAS.SSHUser == "" || cfg.NAS.DestRoot == "" {
			return fmt.Errorf("NAS target enabled but missing required fields in [nas] section (ssh_host, ssh_user, dest_root)")
		}
	}
	return nil
}
```

Ensure `LoadConfig` invokes `ValidateConfig(cfg)` as introduced in Task 8:
```go
func LoadConfig(path string) (*Config, error) {
	cfg, err := loadConfigFile(path)
	if err != nil {
		return nil, err
	}
	applyEnvOverrides(cfg)
	if err := ValidateConfig(cfg); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}
	return cfg, nil
}
```

In `README.md`:
Document that `mc-backup update` requires elevated permissions (`sudo systemctl` and `sudo mv`) to restart the daemon service and overwrite the binary.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -v ./internal/engine -run TestValidateConfigMissingNASFields`
Expected: PASS.

- [ ] **Step 5: Commit Phase 5 Task 18**

```bash
git add internal/engine/config.go README.md internal/engine/config_test.go
git commit -m "feat(config): validate NAS target requirements and document self-update sudo requirements"
```

---

### Task 19: Create Structured Documentation Index `docs/README.md` (Eval 5.4)

**Files:**
- Create: `docs/README.md`

- [ ] **Step 1: Write `docs/README.md` documentation index**

Write `docs/README.md`:
```markdown
# mc-backup Documentation Index

This directory contains evaluation reports, architectural specs, and execution plans.

## Repository History & Evaluation

- [Repository Evaluation (2026-07-23)](repo-evaluation-2026-07-23.md) — Baseline audit and phased remediation scope.

## Architecture Specifications (`docs/superpowers/specs/`)

- [Repository Evaluation Remediation Design](superpowers/specs/2026-07-23-repository-evaluation-remediation-design.md) — Approved design for Phase 0–5 remediation.
- [Local Backup Target Design](superpowers/specs/2026-07-21-local-backup-target-design.md)
- [Offline Backup Design](superpowers/specs/2026-07-20-offline-backup-design.md)
- [Rolling GitHub Release Design](superpowers/specs/2026-07-22-rolling-github-release-design.md)

## Implementation Plans (`docs/superpowers/plans/`)

- [Repository Evaluation Remediation Plan](superpowers/plans/2026-07-23-repository-evaluation-remediation.md)
- [Offline Backup Plan](superpowers/plans/2026-07-20-offline-backup.md)
- [Local Backup Target Plan](superpowers/plans/2026-07-21-local-backup-target.md)
```

- [ ] **Step 2: Commit Phase 5 Task 19**

```bash
git add docs/README.md
git commit -m "docs: add structured documentation index linking evaluation, specs, and plans"
```

---

### Task 20: Final Repository Verification Contract Pass

- [ ] **Step 1: Run full verification suite**

Run:
```bash
test -z "$(gofmt -l internal cmd)"
go vet ./...
go test -v ./...
go test -v -race ./...
go test -cover ./...
golangci-lint run ./...
make build
```

Expected output:
- `gofmt` prints nothing (exit code 0).
- `go vet` passes cleanly.
- `go test ./...` and `go test -race ./...` pass cleanly.
- `golangci-lint` passes cleanly.
- `make build` produces binary `./mc-backup`.

- [ ] **Step 2: Verify git repository cleanliness**

Run:
```bash
git ls-files venv
git ls-files test_tz.go
git check-ignore docs/superpowers/specs/2026-07-23-repository-evaluation-remediation-design.md
```
Expected:
- `git ls-files venv` returns empty.
- `git ls-files test_tz.go` returns empty.
- `git check-ignore` returns empty output with exit code 1.

---

## Plan Self-Review

### Evaluation Item Mapping & Coverage Matrix

| Eval Item | Finding Description | Remediation Plan Task | Status |
|---|---|---|---|
| **0.1** | Python virtualenv committed (`venv/`) | Task 1 | Mapped |
| **0.2** | Scratch file `test_tz.go` at root | Task 1 | Mapped |
| **0.3** | `.gitignore` contradicts tracked specs/plans | Task 2 | Mapped |
| **0.4** | Toolchain version disagreement (`.tool-versions`) | Task 3 | Mapped |
| **1.1** | `AGENTS.md` claims "No CI" | Task 4 | Mapped |
| **1.2** | `AGENTS.md` claims nonexistent `nasWriteLock` | Task 4 | Mapped |
| **1.3** | README claims spot-check (`max_mbps`) | Task 4 | Mapped |
| **2.1** | RCON password exposed in argv process list | Task 7 | Mapped |
| **2.2** | Status HTTP API unauthenticated state mutation | Task 8 | Mapped |
| **2.3** | Auto-provisioned secrets file 0600 permissions | Task 9 | Mapped |
| **3.1** | Hardcoded rsync excludes (nil vs empty semantics) | Task 10 | Mapped |
| **3.2** | NAS count prune `rm -rf` without `-r` / `--no-run-if-empty` | Task 11 | Mapped |
| **3.3** | `sync` invocation ignores errors & detached ctx | Task 12 | Mapped |
| **3.4** | CLI single 64-byte read response truncation | Task 13 | Mapped |
| **3.5** | Confirm `lastBackups` race freedom under `cycleMu` | Task 14 | Mapped |
| **4.1** | Single workflow triggers on main; missing PR CI | Tasks 5, 6 | Mapped |
| **4.2** | Lack of static analysis linter (`golangci-lint`) | Task 15 | Mapped |
| **4.3** | Low test coverage on risky paths (<65%) | Task 16 | Mapped |
| **5.1** | `Makefile` build flag parity with CI | Task 17 | Mapped |
| **5.2** | Self-update sudo requirement documentation | Task 18 | Mapped |
| **5.3** | Config validation on startup/load | Task 18 | Mapped |
| **5.4** | Structured documentation index (`docs/README.md`) | Task 19 | Mapped |

### Verification Checks Performed During Plan Self-Review
- All 20 plan tasks are complete without placeholders, TODOs, or vague directions.
- Function signatures, Go types, and interface definitions (`command`, `commandRunnerInterface`, `GlobalConfig`, `ServerConfig`, `ValidateConfig`, `readResponseBody`, `pruneNASByCountCommand`) were matched against existing repository code in `internal/engine/` and `cmd/mc-backup/`.
- Ordering strictly follows Phase 0 -> Phase 1 -> Phase 2 -> Phase 3 -> Phase 4 -> Phase 5 -> Final Verification.
- `LoadConfig` explicitly invokes `ValidateConfig(cfg)` returning `fmt.Errorf("invalid config: %w", err)` with integration test.
- `rcon.go` and `rcon_test.go` snippets include `io` import and update both `runRcon` and `rconOutput`.
- `status.go` and `status_test.go` define `newStatusMux` helper using `httptest.NewServer` directly.
- `pruneNASByCountCommand` preserves signature `(destRoot, namespace, serverName string, count int)`.
- `lastBackups map[string]*lastBackup` in `Daemon` is preserved without invented types.
- YAML syntax check uses `python3 -c "import sys, yaml; yaml.safe_load(open(sys.argv[1]))"`.
- `excludes` plumbing covers structs, defaults, resolution, TOML round-trip, `SaveAutoServers`, env overrides, CLI get/set, example config, and README.
- Task 1 explicitly preserves `docs/repo-evaluation-2026-07-23.md` and `docs/superpowers/specs/2026-07-21-local-backup-target-design.md`, removing only `venv/` and `test_tz.go`, and staging only `.gitignore`.
