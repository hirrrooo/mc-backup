# RCON Password Detection Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Dynamically detect and read `rcon.password` from `server.properties` at runtime whenever `rcon_password` is omitted or empty in server configuration.

**Architecture:** Add `readServerPropertiesPassword` to parse `<data_dir>/server.properties` and `resolveRconPassword` to handle fallback logic when `s.RconPassword` is empty. Update `BackupServer` and `Daemon.runBackupCycle` to resolve the password dynamically before issuing RCON commands.

**Tech Stack:** Go (stdlib `bufio`, `os`, `path/filepath`, `strings`).

---

### Task 1: Add `readServerPropertiesPassword` and `resolveRconPassword` with Unit Tests

**Files:**
- Modify: `internal/engine/rcon.go`
- Test: `internal/engine/rcon_test.go`

- [ ] **Step 1: Write failing tests for `readServerPropertiesPassword` and `resolveRconPassword`**

Add tests to `internal/engine/rcon_test.go`:

```go
func TestReadServerPropertiesPassword(t *testing.T) {
	tmpDir := t.TempDir()

	t.Run("missing file returns empty", func(t *testing.T) {
		got := readServerPropertiesPassword(tmpDir)
		if got != "" {
			t.Errorf("got %q, want empty string", got)
		}
	})

	t.Run("parses valid password", func(t *testing.T) {
		dir := filepath.Join(tmpDir, "valid")
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		content := "# Minecraft server properties\n  # rcon.password=commented\nenable-rcon=true\nrcon.password = secret123 = 456\n"
		if err := os.WriteFile(filepath.Join(dir, "server.properties"), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
		got := readServerPropertiesPassword(dir)
		want := "secret123 = 456"
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("returns empty when key is missing", func(t *testing.T) {
		dir := filepath.Join(tmpDir, "nokey")
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		content := "enable-rcon=true\nserver-port=25565\n"
		if err := os.WriteFile(filepath.Join(dir, "server.properties"), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
		got := readServerPropertiesPassword(dir)
		if got != "" {
			t.Errorf("got %q, want empty string", got)
		}
	})
}

func TestResolveRconPassword(t *testing.T) {
	tmpDir := t.TempDir()
	watchDir := filepath.Join(tmpDir, "watch")
	mcData := filepath.Join(watchDir, "myserver", "mc-data")
	if err := os.MkdirAll(mcData, 0755); err != nil {
		t.Fatal(err)
	}
	content := "rcon.password=autodetected\n"
	if err := os.WriteFile(filepath.Join(mcData, "server.properties"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	watch := WatchConfig{Path: watchDir, Namespace: "main"}

	t.Run("explicit config password takes precedence", func(t *testing.T) {
		server := ServerConfig{RconPassword: "explicit_pass"}
		got := resolveRconPassword(server, watch, "myserver")
		if got != "explicit_pass" {
			t.Errorf("got %q, want explicit_pass", got)
		}
	})

	t.Run("falls back to server.properties when RconPassword is empty", func(t *testing.T) {
		server := ServerConfig{RconPassword: ""}
		got := resolveRconPassword(server, watch, "myserver")
		if got != "autodetected" {
			t.Errorf("got %q, want autodetected", got)
		}
	})

	t.Run("custom DataDir takes precedence for reading properties", func(t *testing.T) {
		customDir := filepath.Join(tmpDir, "custom")
		if err := os.MkdirAll(customDir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(customDir, "server.properties"), []byte("rcon.password=custompass\n"), 0644); err != nil {
			t.Fatal(err)
		}
		server := ServerConfig{RconPassword: "", DataDir: customDir}
		got := resolveRconPassword(server, watch, "myserver")
		if got != "custompass" {
			t.Errorf("got %q, want custompass", got)
		}
	})
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/engine -run TestReadServerPropertiesPassword`
Expected: FAIL (functions undefined).

- [ ] **Step 3: Implement `readServerPropertiesPassword` and `resolveRconPassword`**

Add functions to `internal/engine/rcon.go`:

```go
func readServerPropertiesPassword(dataDir string) string {
	path := filepath.Join(dataDir, "server.properties")
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "!") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 && strings.TrimSpace(parts[0]) == "rcon.password" {
			return strings.TrimSpace(parts[1])
		}
	}
	return ""
}

func resolveRconPassword(s ServerConfig, w WatchConfig, serverName string) string {
	if s.RconPassword != "" {
		return s.RconPassword
	}
	dataDir := s.DataDir
	if dataDir == "" {
		dataDir = filepath.Join(w.Path, serverName, "mc-data")
	}
	return readServerPropertiesPassword(dataDir)
}
```

Ensure imports in `internal/engine/rcon.go` include `"bufio"`, `"os"`, `"path/filepath"`, `"strings"`.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/engine -run "TestReadServerPropertiesPassword|TestResolveRconPassword"`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/engine/rcon.go internal/engine/rcon_test.go
git commit -m "feat(rcon): add server.properties password detection and resolution"
```

---

### Task 2: Update `BackupServer` in `backup.go` to use `resolveRconPassword`

**Files:**
- Modify: `internal/engine/backup.go`
- Modify: `internal/engine/backup_test.go`

- [ ] **Step 1: Write failing test in `backup_test.go`**

Add a test in `internal/engine/backup_test.go` checking that `BackupServer` reads RCON password from `server.properties` when `s.RconPassword` is empty:

```go
func TestBackupServerAutodetectsRconPassword(t *testing.T) {
	tmpDir := t.TempDir()
	watchDir := filepath.Join(tmpDir, "watch")
	mcData := filepath.Join(watchDir, "cone-create", "mc-data")
	if err := os.MkdirAll(mcData, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mcData, "server.properties"), []byte("rcon.password=detected_pass\n"), 0644); err != nil {
		t.Fatal(err)
	}

	var capturedPasses []string
	runner := commandRunnerFunc(func(ctx context.Context, name string, args ...string) command {
		if name == "docker" && len(args) > 0 && args[0] == "exec" {
			for i, arg := range args {
				if arg == "-p" && i+1 < len(args) {
					capturedPasses = append(capturedPasses, args[i+1])
				}
			}
		}
		return fakeCommand{}
	})

	withCommandRunner(runner, func() {
		be := NewBackupEngine(Config{
			Local: LocalConfig{DestRoot: filepath.Join(tmpDir, "backup")},
		})
		watch := WatchConfig{Path: watchDir, Namespace: "main"}
		server := ServerConfig{Target: "local", RconPassword: ""}

		_, _, err := be.BackupServer(context.Background(), watch, "cone-create", server, "", "", false)
		if err != nil {
			t.Fatalf("BackupServer failed: %v", err)
		}
	})

	if len(capturedPasses) == 0 {
		t.Fatal("no rcon commands were run")
	}
	for _, p := range capturedPasses {
		if p != "detected_pass" {
			t.Errorf("captured RCON password = %q, want detected_pass", p)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/engine -run TestBackupServerAutodetectsRconPassword`
Expected: FAIL (captured empty password instead of "detected_pass").

- [ ] **Step 3: Update `BackupServer` in `backup.go`**

In `internal/engine/backup.go` inside `BackupServer`:
Replace direct uses of `server.RconPassword` with `resolveRconPassword(server, watch, serverName)`:

```go
func (be *BackupEngine) BackupServer(ctx context.Context, watch WatchConfig, serverName string, server ServerConfig, prevLocalBackup, prevNASBackup string, offline bool) (destPath string, usedSSH bool, rerr error) {
    ...
    rconPass := resolveRconPassword(server, watch, serverName)

    if !offline {
        if err := runRcon(ctx, container, rconPass, "save-off", rconRetries, rconRetryInterval); err != nil {
            return "", false, fmt.Errorf("save-off: %w", err)
        }
        defer func() {
            detachedCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
            defer cancel()
            if err := runRcon(detachedCtx, container, rconPass, "save-on", rconRetries, rconRetryInterval); err != nil {
                slog.Error("failed to re-enable saving", "server", serverName, "error", err)
            }
        }()

        if err := runRcon(ctx, container, rconPass, "save-all flush", rconRetries, rconRetryInterval); err != nil {
            return "", false, fmt.Errorf("save-all flush: %w", err)
        }
    }
    ...
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/engine -run TestBackupServerAutodetectsRconPassword`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/engine/backup.go internal/engine/backup_test.go
git commit -m "feat(backup): use resolveRconPassword in BackupServer"
```

---

### Task 3: Update Daemon Player Query in `daemon.go` to use `resolveRconPassword`

**Files:**
- Modify: `internal/engine/daemon.go`
- Modify: `internal/engine/daemon_test.go`

- [ ] **Step 1: Write failing test in `daemon_test.go`**

Add a test in `internal/engine/daemon_test.go` verifying that player count check in `runBackupCycle` uses auto-detected RCON password when `s.Server.RconPassword` is empty:

```go
func TestDaemonRunBackupCycleAutodetectsRconPassword(t *testing.T) {
	tmpDir := t.TempDir()
	watchDir := filepath.Join(tmpDir, "watch")
	mcData := filepath.Join(watchDir, "cone-create", "mc-data")
	if err := os.MkdirAll(mcData, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mcData, "server.properties"), []byte("rcon.password=player_check_pass\n"), 0644); err != nil {
		t.Fatal(err)
	}

	var capturedPasses []string
	runner := commandRunnerFunc(func(ctx context.Context, name string, args ...string) command {
		if name == "docker" {
			for i, arg := range args {
				if arg == "-p" && i+1 < len(args) {
					capturedPasses = append(capturedPasses, args[i+1])
				}
			}
		}
		return fakeCommand{}
	})

	cfg := &Config{
		Watch: []WatchConfig{{Path: watchDir, Namespace: "main"}},
		Local: LocalConfig{DestRoot: filepath.Join(tmpDir, "backup")},
		Servers: map[string]ServerConfig{
			"cone-create": {
				Enabled:           true,
				Target:            "local",
				PauseIfNoPlayers: true,
				RconPassword:     "",
			},
		},
	}

	d := NewDaemon(filepath.Join(tmpDir, "config.toml"), cfg)

	withCommandRunner(runner, func() {
		d.runBackupCycle(context.Background(), "cone-create", false)
	})

	found := false
	for _, p := range capturedPasses {
		if p == "player_check_pass" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected player_check_pass in captured RCON passwords %v", capturedPasses)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/engine -run TestDaemonRunBackupCycleAutodetectsRconPassword`
Expected: FAIL

- [ ] **Step 3: Update `daemon.go`**

In `internal/engine/daemon.go` line ~436 inside `runBackupCycle`:
Replace `s.Server.RconPassword` with `resolveRconPassword(s.Server, s.Watch, s.Name)`:

```go
			if s.Server.PauseIfNoPlayers {
				rconPass := resolveRconPassword(s.Server, s.Watch, s.Name)
				out, err := rconOutput(ctx, container, rconPass, "list")
				if err != nil {
					slog.Warn("cannot query player count, skipping backup", "server", s.Name, "error", err)
					continue
				}
				if countPlayers(out) == 0 {
					slog.Info("no players online, skipping backup", "server", s.Name)
					continue
				}
			}
```

- [ ] **Step 4: Run all engine tests to verify passing**

Run: `go test ./internal/engine/...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/engine/daemon.go internal/engine/daemon_test.go
git commit -m "feat(daemon): use resolveRconPassword for player count check"
```

---

### Task 4: Run Full Test Suite & Verification

**Files:**
- None (verification)

- [ ] **Step 1: Run full package tests**

Run: `go test ./...`
Expected: PASS (all tests in `cmd/...` and `internal/engine/...`)

- [ ] **Step 2: Check formatting**

Run: `gofmt -d .`
Expected: empty output

- [ ] **Step 3: Commit any final cleanup if needed**
