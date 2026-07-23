# High-Priority Correctness Fixes Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking. Each task is independent and ends in its own commit; they may be dispatched to separate sub-agents, but Task 4 touches `config.go` and so does no other task, so there are no cross-task merge conflicts.

**Goal:** Fix the five high-priority correctness issues found in the code evaluation: (1) a data race on the daemon's auto-provisioned-server map, (2) the `update` command stopping the service before the build succeeds, (3) env overrides breaking for server names that contain underscores, (4) `config set` duplicating auto-provisioned servers into the main config and writing it non-atomically, and (5) the SSD→NAS disk projection over-estimating snapshot size.

**Architecture:** Behavior is preserved except for the bugs being fixed. Reuse the existing `commandRunner` seam (`internal/engine/command.go`) and the overridable package vars in `cmd/mc-backup/main.go` for testing — do not introduce new test frameworks. Synchronization uses `sync.Mutex`; atomic file writes mirror the existing `writeLastSnapshotAt` temp-file-and-rename pattern in `config.go`. Comment preservation in TOML is explicitly out of scope (the `BurntSushi/toml` encoder cannot round-trip comments); the fix only stops data duplication/loss and makes the write atomic.

**Tech Stack:** Go, standard library (`sync`, `os`, `os/exec`), `github.com/BurntSushi/toml`, Go unit tests (including `go test -race`).

---

## Issue List

1. **Data race** — `Daemon.autoServers` (a `map[string]bool`) is written by both `runBackupCycle` and `runDiscovery` with no mutex. `runDiscovery` runs from the discovery ticker, from the `/scan` HTTP callback (goroutine), and a `/backup` callback runs `runBackupCycle` (goroutine) concurrently. `saveAutoServers` also ranges the map unguarded. (`internal/engine/daemon.go`)
2. **Update ordering** — `runUpdate` runs steps in the order stop-service → build → install → start. A build failure leaves the service stopped. Build must happen before the service is stopped. (`cmd/mc-backup/main.go`)
3. **Env-override parsing** — `applyEnvOverrides` takes `parts[3]` as the server name, so `MC_BACKUP_SERVER_<NAME>_<KEY>` is mis-parsed whenever `<NAME>` contains an underscore (allowed by `isValidServerName`). Single-token names already work; only underscore-containing names are broken. (`internal/engine/config.go`)
4. **`config set` corruption** — `SetConfigValue` loads the merged (main + `-auto.toml`) config, then `SaveConfig` truncates and rewrites everything to the *main* path, duplicating auto-provisioned servers into it and growing/polluting the file on every `set`. `SaveConfig` also uses `os.Create` (non-atomic), unlike the atomic `.last-backup` writer. (`internal/engine/config.go`)
5. **Disk projection over-estimate** — `dirSize` shells out to `du -sb` over the full data dir, ignoring the rsync `--exclude` patterns (`*.jar`, `cache`, `logs`, `*.tmp`). The estimate over-states snapshot size and routes servers to the NAS earlier than necessary. (`internal/engine/disk.go`, `internal/engine/backup.go`)

## File Structure

- Modify: `internal/engine/daemon.go` — add `autoMu sync.Mutex`, centralize auto-server provisioning behind a locked helper, delete `saveAutoServers`.
- Modify: `internal/engine/daemon_test.go` (create if absent) — add a `-race` concurrency test for the provisioning helper.
- Modify: `cmd/mc-backup/main.go` — reorder `runUpdate` steps so build precedes stop.
- Modify: `cmd/mc-backup/main_test.go` — update the expected step order in `TestUpdateCmdCachesRepoAndRunsSteps`.
- Modify: `internal/engine/config.go` — fix `applyEnvOverrides` server parsing; make `SaveConfig` atomic; add `saveSplit`; route `SetConfigValue` writes through it.
- Modify: `internal/engine/config_test.go` — add env-underscore and `config set` no-duplication tests.
- Modify: `internal/engine/disk.go` — add `excludes` parameter to `dirSize` and pass `--exclude` to `du`.
- Modify: `internal/engine/disk_test.go` — update the `dirSize` call site for the new signature.
- Modify: `internal/engine/backup.go` — pass `excludes` to `dirSize`.

---

### Task 1: Fix the data race on `autoServers`

**Files:**
- Modify: `internal/engine/daemon.go`
- Create: `internal/engine/daemon_test.go`

> **Note on TDD here:** the bug is a missing lock, not a missing function, and the racing paths require real watch directories, so a clean red-green is impractical. Instead, add a `-race` concurrency test against the new helper and rely on `go test -race ./...` as the regression gate. Verify the original bug by code inspection of the two unguarded `d.autoServers[...] = true` writes.

- [ ] **Step 1: Add the mutex field**

In `internal/engine/daemon.go`, add a field to the `Daemon` struct (next to `cycleMu`/`cancelMu`):

```go
	autoMu sync.Mutex
```

- [ ] **Step 2: Add a locked provisioning helper and delete `saveAutoServers`**

Delete the existing `saveAutoServers` method. Add:

```go
// provisionServers records newly discovered servers in the auto-server set,
// returns a clone of cfg with those servers merged in, and persists the
// auto-provisioned config. It is safe for concurrent use.
func (d *Daemon) provisionServers(cfg *Config, newServers []struct {
	Name   string
	Server ServerConfig
}) *Config {
	if len(newServers) == 0 {
		return cfg
	}
	cfg = cloneConfig(cfg)
	d.autoMu.Lock()
	for _, ns := range newServers {
		d.autoServers[ns.Name] = true
		cfg.Servers[ns.Name] = ns.Server
	}
	auto := make(map[string]ServerConfig)
	for name := range d.autoServers {
		if s, ok := cfg.Servers[name]; ok {
			auto[name] = s
		}
	}
	d.autoMu.Unlock()

	if err := SaveAutoServers(d.cfgPath, auto); err != nil {
		slog.Error("failed to save auto-provisioned config", "error", err)
	}
	return cfg
}
```

The `newServers` parameter type must match the second return value of `discoverServers` exactly (`[]struct { Name string; Server ServerConfig }`).

- [ ] **Step 3: Use the helper in `runBackupCycle`**

Replace the block in `runBackupCycle` that reads:

```go
	if len(newServers) > 0 {
		cfg = cloneConfig(cfg)
		for _, ns := range newServers {
			d.autoServers[ns.Name] = true
			cfg.Servers[ns.Name] = ns.Server
		}
		d.saveAutoServers(cfg)
		slog.Info("auto-provisioned new servers in backup cycle")
	}
```

with:

```go
	cfg = d.provisionServers(cfg, newServers)
	if len(newServers) > 0 {
		slog.Info("auto-provisioned new servers in backup cycle")
	}
```

- [ ] **Step 4: Use the helper in `runDiscovery`**

Replace the block in `runDiscovery` that reads:

```go
	if len(newServers) > 0 {
		cfg = cloneConfig(cfg)
		for _, ns := range newServers {
			d.autoServers[ns.Name] = true
			cfg.Servers[ns.Name] = ns.Server
		}
		d.saveAutoServers(cfg)
		slog.Info("new servers discovered, triggering immediate backup cycle")
		go d.runBackupCycle(ctx, "")
	}
```

with:

```go
	cfg = d.provisionServers(cfg, newServers)
	if len(newServers) > 0 {
		slog.Info("new servers discovered, triggering immediate backup cycle")
		go d.runBackupCycle(ctx, "")
	}
```

(`cfg` becomes unused after this in `runDiscovery` since the trigger reloads via `d.ac.Load()`; if the compiler complains about an unused assignment, drop the `cfg =` and call `d.provisionServers(cfg, newServers)` for its side effects only.)

- [ ] **Step 5: Add a concurrency regression test**

Create `internal/engine/daemon_test.go`:

```go
package engine

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestProvisionServersConcurrent(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "config.toml")
	if err := os.WriteFile(cfgPath, []byte("[global]\n"), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	d := NewDaemon(cfgPath, &Config{Servers: map[string]ServerConfig{}})

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ns := []struct {
				Name   string
				Server ServerConfig
			}{{Name: fmt.Sprintf("srv%d", i), Server: ServerConfig{Enabled: true}}}
			d.provisionServers(d.ac.Load(), ns)
		}(i)
	}
	wg.Wait()

	d.autoMu.Lock()
	got := len(d.autoServers)
	d.autoMu.Unlock()
	if got != 16 {
		t.Fatalf("expected 16 auto servers, got %d", got)
	}
}
```

- [ ] **Step 6: Run with the race detector**

Run: `go test -race ./internal/engine -run TestProvisionServersConcurrent`

Expected: PASS with no race report.

- [ ] **Step 7: Run the full suite under `-race`**

Run: `go test -race ./...`

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/engine/daemon.go internal/engine/daemon_test.go
git commit -m "fix: guard auto-provisioned server map against data race"
```

---

### Task 2: Build before stopping the service in `update`

**Files:**
- Modify: `cmd/mc-backup/main.go`
- Modify: `cmd/mc-backup/main_test.go`

- [ ] **Step 1: Update the existing test to expect the new order (red)**

In `cmd/mc-backup/main_test.go`, in `TestUpdateCmdCachesRepoAndRunsSteps`, change the `want` slice so "Building" comes before "Stopping":

```go
	want := []string{
		"ensureRepo:/home/test/.cache/mc-backup/source",
		"Building mc-backup:go build -ldflags -X main.repoURL=https://github.com/hirrrooo/mc-backup.git -o /usr/local/bin/mc-backup.new ./cmd/mc-backup",
		"Stopping mc-backup service:sudo systemctl stop mc-backup",
		"Installing mc-backup:sudo mv /usr/local/bin/mc-backup.new /usr/local/bin/mc-backup",
		"Starting mc-backup service:sudo systemctl start mc-backup",
		"mc-backup service status:systemctl status mc-backup --no-pager",
	}
```

- [ ] **Step 2: Run the test to verify failure**

Run: `go test ./cmd/mc-backup -run TestUpdateCmdCachesRepoAndRunsSteps`

Expected: FAIL (order mismatch).

- [ ] **Step 3: Reorder the steps in `runUpdate`**

In `cmd/mc-backup/main.go`, reorder the `steps` slice in `runUpdate` so the build (to `tmpBin`) runs first and the service is stopped only after a successful build:

```go
	steps := []struct {
		name    string
		command string
		args    []string
	}{
		{"Building mc-backup", "go", []string{"build", "-ldflags", fmt.Sprintf("-X main.repoURL=%s", repoURL), "-o", tmpBin, "./cmd/mc-backup"}},
		{"Stopping mc-backup service", "sudo", []string{"systemctl", "stop", "mc-backup"}},
		{"Installing mc-backup", "sudo", []string{"mv", tmpBin, execPath}},
		{"Starting mc-backup service", "sudo", []string{"systemctl", "start", "mc-backup"}},
		{"mc-backup service status", "systemctl", []string{"status", "mc-backup", "--no-pager"}},
	}
```

- [ ] **Step 4: Run the test to verify pass**

Run: `go test ./cmd/mc-backup -run TestUpdateCmdCachesRepoAndRunsSteps`

Expected: PASS.

- [ ] **Step 5: Run the full suite**

Run: `go test ./...`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add cmd/mc-backup/main.go cmd/mc-backup/main_test.go
git commit -m "fix: build before stopping service during update"
```

---

### Task 3: Fix env overrides for server names with underscores

**Files:**
- Modify: `internal/engine/config.go`
- Modify: `internal/engine/config_test.go`

- [ ] **Step 1: Write a failing test**

Add to `internal/engine/config_test.go`:

```go
func TestEnvOverrideServerNameWithUnderscore(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "config.toml")
	content := []byte(`
[server.my_creative]
enabled = true
rcon_password = "filepass"
`)
	os.WriteFile(cfgPath, content, 0644)

	t.Setenv("MC_BACKUP_SERVER_MY_CREATIVE_RCON_PASSWORD", "envpass")
	t.Setenv("MC_BACKUP_SERVER_MY_CREATIVE_PAUSE_IF_NO_PLAYERS", "true")

	cfg, err := LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}
	if cfg.Servers["my_creative"].RconPassword != "envpass" {
		t.Errorf("RconPassword: got %q, want envpass", cfg.Servers["my_creative"].RconPassword)
	}
	if !cfg.Servers["my_creative"].PauseIfNoPlayers {
		t.Error("PauseIfNoPlayers: expected true")
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/engine -run TestEnvOverrideServerNameWithUnderscore`

Expected: FAIL (server name parsed as `my`, key as `creative_rcon_password`, so nothing is overridden).

- [ ] **Step 3: Add a suffix-matching parser**

In `internal/engine/config.go`, add near `setServerField`:

```go
var serverFieldKeys = []string{
	"enabled",
	"ssh_only",
	"container_name",
	"rcon_password",
	"data_dir",
	"pause_if_no_players",
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
```

- [ ] **Step 4: Rewrite the server branch of `applyEnvOverrides`**

Replace the body of `applyEnvOverrides` with:

```go
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
				setServerField(&s, field, val)
				cfg.Servers[name] = s
			}
			continue
		}

		key := strings.Join(parts[3:], "_")
		switch section {
		case "global":
			setGlobalField(&cfg.Global, key, val)
		case "nas":
			setNASField(&cfg.NAS, key, val)
		case "retention":
			setRetentionField(&cfg.Retention, key, val)
		}
	}
}
```

- [ ] **Step 5: Run the new test and the existing env tests**

Run: `go test ./internal/engine -run TestEnvOverride`

Expected: PASS (covers `TestEnvOverride`, `TestEnvOverrideCaseInsensitiveServerName`, and the new `TestEnvOverrideServerNameWithUnderscore`).

- [ ] **Step 6: Run the full suite**

Run: `go test ./...`

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/engine/config.go internal/engine/config_test.go
git commit -m "fix: parse server env overrides for names with underscores"
```

---

### Task 4: Stop `config set` from duplicating auto servers; make saves atomic

**Files:**
- Modify: `internal/engine/config.go`
- Modify: `internal/engine/config_test.go`

**Approach:** `SetConfigValue` keeps loading the merged config (so edits see the full picture), but the final write splits state by destination: auto-provisioned servers (those present in `<config>-auto.toml`) are written back to the auto file via the existing `SaveAutoServers`, and everything else is written to the main file. `SaveConfig` becomes atomic (temp file + `fsync` + rename), mirroring `writeLastSnapshotAt`. Comment preservation is out of scope — the encoder cannot round-trip comments — but duplication, auto-field loss, and torn writes are eliminated.

- [ ] **Step 1: Write failing tests**

Add to `internal/engine/config_test.go`:

```go
func TestSetConfigValueDoesNotDuplicateAutoServers(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "config.toml")
	autoPath := autoServersPath(cfgPath)

	os.WriteFile(cfgPath, []byte("[global]\nmax_mbps = 40.0\n"), 0644)
	os.WriteFile(autoPath, []byte("[server.creative]\nenabled = true\ncontainer_name = \"creative-mc-1\"\nrcon_password = \"secret\"\n"), 0644)

	if err := SetConfigValue(cfgPath, "global.max_mbps", "20"); err != nil {
		t.Fatalf("SetConfigValue: %v", err)
	}

	mainBytes, _ := os.ReadFile(cfgPath)
	if strings.Contains(string(mainBytes), "creative") {
		t.Fatalf("auto server leaked into main config:\n%s", mainBytes)
	}

	// The auto server and its fields must survive in the auto file.
	cfg, err := LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Servers["creative"].RconPassword != "secret" {
		t.Errorf("auto server lost rcon_password: %#v", cfg.Servers["creative"])
	}
	if cfg.Global.MaxMBps != 20 {
		t.Errorf("max_mbps not applied: %v", cfg.Global.MaxMBps)
	}
}

func TestSetConfigValueUpdatesAutoServerInAutoFile(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "config.toml")
	autoPath := autoServersPath(cfgPath)

	os.WriteFile(cfgPath, []byte("[global]\n"), 0644)
	os.WriteFile(autoPath, []byte("[server.creative]\nenabled = true\ncontainer_name = \"creative-mc-1\"\nrcon_password = \"old\"\n"), 0644)

	if err := SetConfigValue(cfgPath, "server.creative.rcon_password", "new"); err != nil {
		t.Fatalf("SetConfigValue: %v", err)
	}

	mainBytes, _ := os.ReadFile(cfgPath)
	if strings.Contains(string(mainBytes), "creative") {
		t.Fatalf("auto server written to main config:\n%s", mainBytes)
	}
	cfg, err := LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Servers["creative"].RconPassword != "new" {
		t.Errorf("rcon_password not updated: %#v", cfg.Servers["creative"])
	}
	if cfg.Servers["creative"].ContainerName != "creative-mc-1" {
		t.Errorf("container_name lost: %#v", cfg.Servers["creative"])
	}
}
```

Add `"strings"` to the `config_test.go` imports if it is not already present.

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/engine -run 'TestSetConfigValue'`

Expected: FAIL (`creative` is currently rewritten into the main config by `SaveConfig`).

- [ ] **Step 3: Make `SaveConfig` atomic**

Replace `SaveConfig` in `internal/engine/config.go` with:

```go
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
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("replace %s: %w", path, err)
	}
	return nil
}
```

- [ ] **Step 4: Add `saveSplit` and route `SetConfigValue` through it**

Add `saveSplit` near `SaveConfig`:

```go
// saveSplit writes cfg back to disk, sending auto-provisioned servers (those
// present in <path>-auto.toml) to the auto file and everything else to the
// main config. This prevents auto servers from being duplicated into the main
// config and prevents their auto-only fields from being lost.
func saveSplit(path string, cfg *Config) error {
	autoNames := loadAutoServerNames(path)

	main := cloneConfig(cfg)
	auto := make(map[string]ServerConfig)
	for name := range autoNames {
		if s, ok := main.Servers[name]; ok {
			auto[name] = s
			delete(main.Servers, name)
		}
	}
	if err := SaveConfig(path, main); err != nil {
		return err
	}
	return SaveAutoServers(path, auto)
}
```

In `SetConfigValue`, change the final line from `return SaveConfig(path, cfg)` to `return saveSplit(path, cfg)`. Leave the rest of `SetConfigValue` (the merged `loadConfigFile(path)` load and the section switch) unchanged.

- [ ] **Step 5: Run the new tests**

Run: `go test ./internal/engine -run 'TestSetConfigValue'`

Expected: PASS.

- [ ] **Step 6: Run the full suite**

Run: `go test ./...`

Expected: PASS — including the pre-existing `TestSaveConfig`, which writes a non-auto server and reloads it.

- [ ] **Step 7: Commit**

```bash
git add internal/engine/config.go internal/engine/config_test.go
git commit -m "fix: write atomically and keep auto servers out of main config on set"
```

---

### Task 5: Exclude non-data files from the disk-size estimate

**Files:**
- Modify: `internal/engine/disk.go`
- Modify: `internal/engine/disk_test.go`
- Modify: `internal/engine/backup.go`

**Note:** This makes the estimate honor the same `--exclude` patterns rsync uses. It is still an upper bound (it cannot predict `--link-dest` hardlink savings), but over-estimation is the safe direction — it only routes to the NAS slightly early, never loses data.

- [ ] **Step 1: Update the `dirSize` test for the new signature (red)**

In `internal/engine/disk_test.go`, change the call in `TestDirSize` from `dirSize(tmp)` to `dirSize(tmp, nil)`. Then add a test asserting excludes reach `du` via the command seam:

```go
func TestDirSizePassesExcludesToDu(t *testing.T) {
	var gotArgs []string
	withCommandRunner(commandRunnerFunc(func(ctx context.Context, name string, args ...string) command {
		gotArgs = append([]string{name}, args...)
		return fakeCommand{}
	}), func() {
		dirSize("/data", []string{"*.jar", "cache"})
	})

	joined := strings.Join(gotArgs, " ")
	if !strings.Contains(joined, "--exclude=*.jar") || !strings.Contains(joined, "--exclude=cache") {
		t.Fatalf("du args missing excludes: %v", gotArgs)
	}
}
```

Add `"context"` and `"strings"` to the `disk_test.go` imports. If `fakeCommand`'s `Output()` returns `nil, nil`, `dirSize` will error parsing the empty output — that is fine here because the test only inspects the captured args (ignore `dirSize`'s return values). Confirm `fakeCommand` (defined in `command_test.go`) satisfies the `command` interface including `SetStdout`/`SetStderr`; if not, add no-op methods to it.

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/engine -run 'TestDirSize'`

Expected: FAIL to compile (`dirSize` takes one argument) / missing test helper.

- [ ] **Step 3: Add the `excludes` parameter to `dirSize`**

In `internal/engine/disk.go`, change `dirSize` to:

```go
func dirSize(path string, excludes []string) (int64, error) {
	args := []string{"du", "-sb"}
	for _, ex := range excludes {
		args = append(args, "--exclude="+ex)
	}
	args = append(args, path)
	cmd := commandRunner.CommandContext(context.Background(), args[0], args[1:]...)
	out, err := cmd.Output()
	if err != nil {
		return 0, err
	}
	fields := strings.Fields(string(out))
	if len(fields) < 1 {
		return 0, fmt.Errorf("du: unexpected output: %s", string(out))
	}
	size, err := strconv.ParseInt(fields[0], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("du: parse size: %w", err)
	}
	return size, nil
}
```

- [ ] **Step 4: Pass the excludes at the call site**

In `internal/engine/backup.go`, inside `BackupServer`, the `excludes` slice is already defined above the routing decision. Change the estimate call from:

```go
				estSize, _ := dirSize(dataDir)
```

to:

```go
				estSize, _ := dirSize(dataDir, excludes)
```

- [ ] **Step 5: Run the disk tests**

Run: `go test ./internal/engine -run 'TestDirSize'`

Expected: PASS.

- [ ] **Step 6: Run the full suite**

Run: `go test ./...`

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/engine/disk.go internal/engine/disk_test.go internal/engine/backup.go
git commit -m "fix: honor rsync excludes in disk-usage projection"
```

---

### Task 6: Final verification

- [ ] **Step 1: Vet and race-test everything**

Run:

```bash
go vet ./...
go test -race ./...
```

Expected: both clean.

- [ ] **Step 2: Confirm the diff is scoped**

Run: `git diff --stat main`

Expected: only the files named in this plan are touched.

- [ ] **Step 3: Confirm each issue is resolved**

- `autoServers` reads/writes are guarded by `autoMu`; `go test -race` is clean.
- `runUpdate` builds before stopping the service.
- `MC_BACKUP_SERVER_<NAME>_<FIELD>` works for underscore-containing names.
- `config set` no longer duplicates auto servers into the main config, preserves auto-only fields, and writes atomically.
- The SSD→NAS projection passes rsync excludes to `du`.

---

## Self-Review

**Issue coverage:** Issues 1–5 map directly to Tasks 1–5; Task 6 is final verification. No issue is left unaddressed.

**Scope discipline:** Out-of-scope items (HTTP timeouts, endpoint auth, RCON password in argv, `Bfree` vs `Bavail`, version injection, the verbose anonymous-struct types, `xargs --no-run-if-empty`, TOML comment preservation) are deliberately excluded — they are lower-priority robustness/style items from the evaluation, not the high-priority correctness bugs this plan targets.

**Type consistency:** Helper names are used consistently across implementation and tests: `provisionServers`, `autoMu`, `parseServerEnvKey`, `serverFieldKeys`, `saveSplit`, and the new `dirSize(path, excludes)` signature. The `newServers` parameter and `discoverServers`'s second return share the exact anonymous struct type.

**Test seams:** All tests reuse existing seams — overridable package vars in `main_test.go`, `withCommandRunner`/`commandRunnerFunc`/`fakeCommand` in the engine package, and `t.Setenv`/`t.TempDir` for config. No new dependencies are introduced.

**Placeholder scan:** No `TBD`/`TODO`/unspecified steps; every change has an exact file path and concrete code.
