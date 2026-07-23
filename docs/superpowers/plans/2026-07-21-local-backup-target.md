# Local Backup Target Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace disk-pressure/`ssh_only` routing with an explicit per-server `target` that writes snapshots either to a configured local root or directly to the NAS, with isolated history, retention, validation, migration warnings, and updated documentation.

**Architecture:** Add `LocalConfig` and `ServerConfig.Target`, resolving an omitted target to `nas` and validating the selected destination before RCON or SSH. Keep one `BackupEngine` with target-specific local/NAS branches, use the existing `lastBackup.local` and `.nas` fields as independent histories, and prune only the selected target. Discovery reads history from the local target tree (when configured), continues to read NAS history, and warns about unmanaged legacy watch backups.

**Tech Stack:** Go 1.21+, `BurntSushi/toml`, `fsnotify`, `os/exec`, `rsync`, SSH, table-driven Go tests, Markdown/TOML documentation.

---

## File structure

Files to create or modify:

- Modify `internal/engine/config.go`: add `[local]`, replace `ssh_only`, remove legacy watch retention fields, normalize both destination roots, resolve/validate target, and update config get/set/env/generated output.
- Modify `internal/engine/backup.go`: add target resolution and target-specific snapshot routing; remove disk-pressure selection and obsolete disk helpers.
- Modify `internal/engine/daemon.go`: use selected-target history and retention, discover local-target history, and remove archive invocation.
- Modify `internal/engine/discovery.go`: warn about nonempty legacy backup directories whenever a server is discovered.
- Modify `internal/engine/prune.go`: add local prune-by-days alongside local prune-by-count.
- Delete `internal/engine/archive.go`: archive migration is no longer part of the product.
- Delete `internal/engine/disk.go`: disk-pressure helpers are dead after routing removal.
- Modify `internal/engine/config_test.go`, `backup_test.go`, `discovery_test.go`, `daemon_test.go`, and `prune_test.go`: replace old schema tests and add target, safety, history, discovery, and retention coverage.
- Delete `internal/engine/disk_test.go`: tests only obsolete disk-pressure utilities.
- Modify `cmd/mc-backup/main.go`: remove archive wording from help.
- Modify `config.example.toml`: expose `[local].dest_root`, `target`, and only current retention/watch settings.
- Modify `README.md`: document deterministic target behavior, local paths/restoration, local retention, and NAS sentinel scope; remove archive/SSD/`ssh_only` references.

## Task 1: Replace configuration schema and target resolution

**Files:**
- Modify: `internal/engine/config.go:36-75,112-120,206-355,439-555`
- Test: `internal/engine/config_test.go`

- [ ] **Step 1: Write failing configuration tests.** Add these table-driven tests after the existing config parsing tests; use `os.WriteFile` error checks rather than ignoring errors:

```go
func TestTargetConfigAndDefaults(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "config.toml")
	if err := os.WriteFile(path, []byte("[local]\ndest_root = \"relative/local///\"\n[server.local]\nenabled = true\ntarget = \"local\"\n[server.legacy]\nenabled = true\n"), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil { t.Fatal(err) }
	if cfg.Local.DestRoot != "relative/local" { t.Fatalf("local root = %q", cfg.Local.DestRoot) }
	if cfg.Servers["local"].Target != "local" { t.Fatalf("local target = %q", cfg.Servers["local"].Target) }
	if got, err := resolveBackupTarget("legacy", cfg.Servers["legacy"], cfg.Local); err != nil || got != "nas" { t.Fatalf("legacy target = %q, %v", got, err) }
}

func TestResolveBackupTargetRejectsInvalidAndMissingLocalDestination(t *testing.T) {
	cases := []struct{ name, server, root, want string }{
		{"invalid", "bogus", "/local", `server "invalid" has invalid backup target "bogus" (want "local" or "nas")`},
		{"missing root", "local", "", `server "missing root" target "local" requires local.dest_root`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := resolveBackupTarget(tc.name, ServerConfig{Target: tc.server}, LocalConfig{DestRoot: tc.root})
			if err == nil || err.Error() != tc.want { t.Fatalf("error = %v, want %q", err, tc.want) }
		})
	}
}

func TestTargetConfigOutputAndEnvironment(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "config.toml")
	if err := os.WriteFile(path, []byte("[local]\ndest_root = \"/file///\"\n[server.creative]\nenabled = true\ntarget = \"nas\"\n"), 0644); err != nil { t.Fatal(err) }
	t.Setenv("MC_BACKUP_LOCAL_DEST_ROOT", "/env///")
	t.Setenv("MC_BACKUP_SERVER_CREATIVE_TARGET", "local")
	cfg, err := LoadConfig(path)
	if err != nil { t.Fatal(err) }
	if cfg.Local.DestRoot != "/env" || cfg.Servers["creative"].Target != "local" { t.Fatalf("overrides not applied: %#v", cfg) }
	if got := GetConfigValue(cfg, "server.creative.target"); got != "local" { t.Fatalf("target = %q", got) }
}
```

- [ ] **Step 2: Run the focused tests to verify they fail.**

Run: `go test ./internal/engine -run 'TestTargetConfig|TestResolveBackupTarget' -count=1`

Expected: FAIL to compile because `LocalConfig`, `ServerConfig.Target`, and `resolveBackupTarget` do not exist.

- [ ] **Step 3: Implement the schema and resolver.** Replace the relevant declarations with:

```go
type LocalConfig struct { DestRoot string `toml:"dest_root"` }

type WatchConfig struct {
	Path      string `toml:"path"`
	Namespace string `toml:"namespace"`
}

type ServerConfig struct {
	Enabled          bool   `toml:"enabled"`
	Target           string `toml:"target"`
	ContainerName    string `toml:"container_name"`
	RconPassword     string `toml:"rcon_password"`
	DataDir          string `toml:"data_dir"`
	PauseIfNoPlayers bool   `toml:"pause_if_no_players"`
}

type Config struct {
	Global GlobalConfig `toml:"global"`
	Local LocalConfig `toml:"local"`
	NAS NASConfig `toml:"nas"`
	Retention RetentionConfig `toml:"retention"`
	Watch []WatchConfig `toml:"watch"`
	Servers map[string]ServerConfig `toml:"server"`
}

func resolveBackupTarget(serverName string, server ServerConfig, local LocalConfig) (string, error) {
	target := server.Target
	if target == "" { target = "nas" }
	if target != "local" && target != "nas" {
		return "", fmt.Errorf("server %q has invalid backup target %q (want %q or %q)", serverName, target, "local", "nas")
	}
	if target == "local" && local.DestRoot == "" {
		return "", fmt.Errorf("server %q target %q requires local.dest_root", serverName, target)
	}
	return target, nil
}
```

Initialize `Local` in `loadConfigFile` and normalize both roots after TOML decoding with this helper so `/` remains a valid root:

```go
func normalizeDestRoot(root string) string {
	trimmed := strings.TrimRight(root, "/")
	if trimmed == "" && strings.HasPrefix(root, "/") { return "/" }
	return trimmed
}
```

In `applyEnvOverrides`, add `case "local": setLocalField(&cfg.Local, key, val)` and implement `setLocalField` for `dest_root`; add `target` to `setServerField`, `serverFieldKeys`, and `getServerFieldStr`. Add `Local` to `cloneConfig`. In `SaveAutoServers`, emit `target = %q` and never emit `ssh_only`. Do not retain compatibility fields in the Go schema; TOML's unknown keys are ignored, so old fields cannot influence routing.

- [ ] **Step 4: Run the focused tests to verify they pass.**

Run: `gofmt -w internal/engine/config.go internal/engine/config_test.go && go test ./internal/engine -run 'TestTargetConfig|TestResolveBackupTarget' -count=1`

Expected: PASS.

- [ ] **Step 5: Commit command (do not execute).**

```bash
git add internal/engine/config.go internal/engine/config_test.go
git commit -m "feat(config): add explicit local and NAS backup targets"
```

## Task 2: Add target-specific backup routing and pre-RCON validation

**Files:**
- Modify: `internal/engine/backup.go:149-246`
- Test: `internal/engine/backup_test.go`

- [ ] **Step 1: Write failing path and routing tests.** Add tests that assert exact destination and arguments:

```go
func TestLocalBackupPathAndArgs(t *testing.T) {
	args := localRsyncArgs("/data", "/local/minecraft/survival/20260721-1200", "/local/minecraft/survival/20260721-1300", []string{"*.jar"})
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--timeout=300") || !strings.Contains(joined, "--link-dest=/local/minecraft/survival/20260721-1200") { t.Fatal(joined) }
	if strings.Contains(joined, "ssh") || strings.Contains(joined, "bwlimit") { t.Fatal(joined) }
}

func TestBackupTargetValidationPrecedesRCON(t *testing.T) {
	var calls []string
	withCommandRunner(commandRunnerFunc(func(ctx context.Context, name string, args ...string) command {
		calls = append(calls, name)
		return fakeCommand{}
	}), func() {
		be := NewBackupEngine(Config{})
		_, _, err := be.BackupServer(context.Background(), WatchConfig{}, "broken", ServerConfig{Target: "invalid"}, "", "", false)
		if err == nil || !strings.Contains(err.Error(), `server "broken"`) { t.Fatalf("error = %v", err) }
	})
	if len(calls) != 0 { t.Fatalf("commands before validation: %v", calls) }
}
```

Also add a successful local integration test using `t.TempDir`, a fake command runner that records commands and returns `fakeCommand{}`, and a `LocalConfig{DestRoot: tmp}`; assert the returned path is `filepath.Join(tmp, "minecraft", "survival", timestamp)`, no recorded command is `ssh`, and no `.nas-ready` check appears. Add the equivalent NAS test asserting `ssh`, remote mkdir, sentinel, and NAS rsync are present and the local root remains absent.

- [ ] **Step 2: Run tests to verify the new routing test fails.**

Run: `go test ./internal/engine -run 'TestLocalBackupPathAndArgs|TestBackupTargetValidationPrecedesRCON' -count=1`

Expected: FAIL to compile because `BackupServer` does not validate/resolve `Target` and still uses `SSHOnly`.

- [ ] **Step 3: Implement the minimal target branch.** At the beginning of `BackupServer`, before `dataDir` resolution and before the `if !offline` RCON block, add:

```go
target, err := resolveBackupTarget(serverName, server, be.cfg.Local)
if err != nil { return "", false, err }
```

Add `var rsyncCommand = exec.CommandContext` beside the command helpers and change `runRsync` to call `rsyncCommand(ctx, args[0], args[1:]...)`; this preserves production behavior while allowing the integration tests to replace rsync without invoking the host binary.

Replace disk-pressure and `server.SSHOnly` selection with `target == "nas"`. Keep the existing RCON/`sync` sequence unchanged after validation. Use these exact destination branches after timestamp creation:

```go
if target == "nas" {
	if err := checkNASReady(ctx, be.cfg.NAS); err != nil { return "", false, fmt.Errorf("NAS not ready: %w", err) }
	destDir := filepath.Join(be.cfg.NAS.DestRoot, watch.Namespace, serverName, ts)
	parent := filepath.Dir(destDir)
	if err := ensureNASDir(ctx, be.cfg.NAS, parent); err != nil { return "", false, fmt.Errorf("create NAS dir: %w", err) }
	if err := runRsync(ctx, nasRsyncArgs(dataDir, prevNASBackup, destDir, be.cfg.NAS, be.cfg.Global.MaxMBps, excludes), be.OnProgress); err != nil { return "", false, fmt.Errorf("NAS rsync: %w", err) }
	return destDir, true, nil
}

destDir := filepath.Join(be.cfg.Local.DestRoot, watch.Namespace, serverName, ts)
if err := os.MkdirAll(destDir, 0755); err != nil { return "", false, fmt.Errorf("local mkdir: %w", err) }
if err := runRsync(ctx, localRsyncArgs(dataDir, prevLocalBackup, destDir, excludes), be.OnProgress); err != nil { return "", false, fmt.Errorf("local rsync: %w", err) }
return destDir, false, nil
```

Remove `diskUsagePct`, `totalDiskSpace`, and `dirSize` calls/imports and change the completion log field from `ssh_only` to `target`. Preserve deferred `save-on` error precedence and the offline path.

- [ ] **Step 4: Run backup tests.**

Run: `gofmt -w internal/engine/backup.go internal/engine/backup_test.go && go test ./internal/engine -run 'Test(Local|NAS|BackupTarget|NoLinkDest|Rsync|IsBackupDir)' -count=1`

Expected: PASS; local tests show no SSH command and NAS tests retain sentinel/remote behavior.

- [ ] **Step 5: Commit command (do not execute).**

```bash
git add internal/engine/backup.go internal/engine/backup_test.go
git commit -m "feat(backup): route snapshots by explicit target"
```

## Task 3: Make discovery and daemon bookkeeping target-aware

**Files:**
- Modify: `internal/engine/discovery.go:110-176`
- Modify: `internal/engine/daemon.go:109-160,327-464`
- Test: `internal/engine/discovery_test.go`, `internal/engine/daemon_test.go`

- [ ] **Step 1: Write failing discovery tests.** Add a test that creates `<watch.path>/survival/backups/20260721-1200`, calls `discoverServers`, captures slog output with a temporary text handler, and asserts the warning contains `no longer managed`, `migrate`, and `delete`. Repeat with missing and empty `backups` and assert no warning. Add a test with `[local].dest_root` populated that creates `local/minecraft/survival/20260721-1300` and verifies `discoverSnapshots` writes that path as the local history; with an empty local root, verify no local path is written.

- [ ] **Step 2: Run discovery tests to verify they fail.**

Run: `go test ./internal/engine -run 'Test(Legacy|Discover)' -count=1`

Expected: FAIL because legacy warning and local-target discovery do not exist.

- [ ] **Step 3: Implement legacy warning and local history discovery.** Add:

```go
func warnLegacyBackupDir(w WatchConfig, serverName string) {
	path := w.backupDir(serverName)
	entries, err := os.ReadDir(path)
	if err != nil || len(entries) == 0 { return }
	slog.Warn("legacy backup directory is no longer managed; migrate snapshots to the desired target or delete them manually", "path", path, "server", serverName)
}
```

Call it in `discoverServers` immediately after validating each server directory, before known-server handling. In `discoverSnapshots`, call `resolveBackupTarget` before any local or NAS history command; on error, log the server/target error and continue to the next server without issuing SSH. For a valid target, if `cfg.Local.DestRoot != ""`, scan `filepath.Join(cfg.Local.DestRoot, namespace, name)` for timestamp directories and set `latestLocal` there; if the root is empty, leave `latestLocal` empty. Never scan `watch.backupDir` and never include it in cleanup/bookkeeping. Keep NAS discovery unchanged after validation.

- [ ] **Step 4: Write failing daemon retention/history tests.** Add a test that runs the daemon backup bookkeeping with a local target and asserts only `prev.local` is updated; add a test that exercises a NAS target and asserts no local target directory is created. Add a source-level regression test or direct test setup proving `watch.backupDir` legacy snapshots are not selected as `prev.local`.

- [ ] **Step 5: Implement selected-target history updates.** In `runBackupCycle`, remove `pruneLocalByCount(s.Watch.backupDir(...))` and archive construction. Keep the existing returned `usedSSH` boolean only as the target result: update `prev.nas` when true and `prev.local` when false, then invoke target-specific retention in Task 4. Ensure a failed `BackupServer` returns before mutating `prev` or `.last-backup`.

- [ ] **Step 6: Run daemon/discovery tests.**

Run: `gofmt -w internal/engine/discovery.go internal/engine/daemon.go internal/engine/discovery_test.go internal/engine/daemon_test.go && go test ./internal/engine -run 'Test(Legacy|Discover|Provision|ServerMatches)' -count=1`

Expected: PASS; old watch backups are warned about but never discovered, local history is skipped without a local root, and target histories remain independent.

- [ ] **Step 7: Commit command (do not execute).**

```bash
git add internal/engine/discovery.go internal/engine/discovery_test.go internal/engine/daemon.go internal/engine/daemon_test.go
git commit -m "fix(daemon): isolate target history and warn about legacy snapshots"
```

## Task 4: Implement local prune-by-days and selected-target retention

**Files:**
- Modify: `internal/engine/prune.go:13-39`
- Modify: `internal/engine/daemon.go:439-463`
- Test: `internal/engine/prune_test.go`

- [ ] **Step 1: Write failing local day-pruning tests.** Add:

```go
func TestPruneLocalByDays(t *testing.T) {
	tmp := t.TempDir()
	old := filepath.Join(tmp, "20260701-1200")
	newer := filepath.Join(tmp, "20260720-1200")
	legacy := filepath.Join(tmp, "not-a-snapshot")
	for _, path := range []string{old, newer, legacy} { if err := os.Mkdir(path, 0755); err != nil { t.Fatal(err) } }
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.Local)
	if err := os.Chtimes(old, now.Add(-48*time.Hour), now.Add(-48*time.Hour)); err != nil { t.Fatal(err) }
	if err := os.Chtimes(newer, now.Add(-2*time.Hour), now.Add(-2*time.Hour)); err != nil { t.Fatal(err) }
	pruneLocalByDays(tmp, 1, now)
	if _, err := os.Stat(old); !os.IsNotExist(err) { t.Fatalf("old snapshot remains: %v", err) }
	if _, err := os.Stat(newer); err != nil { t.Fatalf("newer snapshot removed: %v", err) }
	if _, err := os.Stat(legacy); err != nil { t.Fatalf("legacy directory changed: %v", err) }
}
```

Also test `days <= 0` is disabled and add a combined local retention test showing count and days only remove timestamp-shaped directories under `<local.dest_root>/<namespace>/<server>`.

- [ ] **Step 2: Run prune tests to verify failure.**

Run: `go test ./internal/engine -run 'TestPruneLocal' -count=1`

Expected: FAIL to compile because `pruneLocalByDays` does not exist.

- [ ] **Step 3: Implement local pruning.** Add `time` import and:

```go
func pruneLocalByDays(localPath string, days int, now time.Time) {
	if days <= 0 { return }
	entries, err := os.ReadDir(localPath)
	if err != nil { slog.Warn("prune: cannot read local dir", "path", localPath, "error", err); return }
	cutoff := now.Add(-time.Duration(days) * 24 * time.Hour)
	for _, e := range entries {
		if !e.IsDir() || !isBackupDir(e.Name()) { continue }
		info, err := e.Info()
		if err != nil { slog.Warn("prune: cannot stat local backup", "path", filepath.Join(localPath, e.Name()), "error", err); continue }
		if info.ModTime().Before(cutoff) {
			path := filepath.Join(localPath, e.Name())
			slog.Info("pruning local backup", "path", path)
			if err := os.RemoveAll(path); err != nil { slog.Warn("prune: failed to remove", "path", path, "error", err) }
		}
	}
}
```

In `runBackupCycle`, after a successful local backup call both `pruneLocalByDays(localDir, cfg.Retention.PruneDays, time.Now())` and `pruneLocalByCount(localDir, cfg.Retention.PruneCount)`; after a successful NAS backup call only the existing NAS day/count functions. `localDir` must be the target root path, not `watch.backupDir`.

- [ ] **Step 4: Run retention tests.**

Run: `gofmt -w internal/engine/prune.go internal/engine/prune_test.go internal/engine/daemon.go && go test ./internal/engine -run 'TestPrune(Local|NAS)' -count=1`

Expected: PASS; local days/count affect only local target snapshots and NAS pruning remains SSH-based.

- [ ] **Step 5: Commit command (do not execute).**

```bash
git add internal/engine/prune.go internal/engine/prune_test.go internal/engine/daemon.go
git commit -m "feat(retention): prune local snapshots by days and count"
```

## Task 5: Remove archive and disk-pressure code

**Files:**
- Delete: `internal/engine/archive.go`
- Delete: `internal/engine/disk.go`
- Delete: `internal/engine/disk_test.go`
- Modify: `internal/engine/backup.go`, `internal/engine/daemon.go`, `internal/engine/config.go`
- Test: all `internal/engine/*_test.go`

- [ ] **Step 1: Write the failing legacy-schema regression test.** Add this to `internal/engine/config_test.go`; it must prove obsolete TOML fields cannot override the explicit target:

```go
func TestLegacyRoutingFieldsDoNotInfluenceTarget(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "config.toml")
	if err := os.WriteFile(path, []byte("[local]\ndest_root = \"/local\"\n[server.creative]\ntarget = \"local\"\nssh_only = true\n[watch]\npath = \"/servers\"\nlocal_keep = 1\nmax_disk_pct = 90\n"), 0644); err != nil { t.Fatal(err) }
	cfg, err := LoadConfig(path)
	if err != nil { t.Fatal(err) }
	got, err := resolveBackupTarget("creative", cfg.Servers["creative"], cfg.Local)
	if err != nil || got != "local" { t.Fatalf("target = %q, %v", got, err) }
}
```

- [ ] **Step 2: Run the regression test before deleting obsolete code.**

Run: `go test ./internal/engine -run TestLegacyRoutingFieldsDoNotInfluenceTarget -count=1`

Expected: PASS, confirming the explicit target already wins over ignored legacy TOML fields before the deletion cleanup.

- [ ] **Step 3: Add a repository regression check before deletion.** Run:

```bash
git grep -n -E 'ArchiveEngine|NewArchiveEngine|ArchiveIfNeeded|diskUsagePct|totalDiskSpace|dirSize|SSHOnly|ssh_only|LocalKeep|local_keep|MaxDiskPct|max_disk_pct'
```

Expected before implementation: matches in the files listed above; after this task, no production/test Go match remains.

- [ ] **Step 4: Delete obsolete files and fields.** Delete `archive.go`, `disk.go`, and `disk_test.go`; remove all remaining archive calls, disk-pressure imports/code, `watch.local_keep`, `watch.max_disk_pct`, and `ssh_only` accessors/emitters. Do not add replacement disk logic: `target` is always deterministic.

- [ ] **Step 5: Run the full engine suite.**

Run: `gofmt -w internal/engine && go test ./internal/engine -count=1`

Expected: PASS with no undefined archive/disk symbols.

- [ ] **Step 6: Commit command (do not execute).**

```bash
git add -A internal/engine
git commit -m "refactor(engine): remove archive and disk-pressure routing"
```

## Task 6: Update CLI, example configuration, and README

**Files:**
- Modify: `cmd/mc-backup/main.go:250-276`
- Modify: `config.example.toml`
- Modify: `README.md`
- Test: `cmd/mc-backup/main_test.go`, `internal/engine/config_test.go`

- [ ] **Step 1: Write documentation/config regression tests.** Add a test that captures `printUsage()` and asserts it contains `target` and does not contain `ssh_only`, `local_keep`, `max_disk_pct`, or `archive`. Add a config serialization test that calls `SaveAutoServers` and asserts `target =` is present and the obsolete names are absent. Add a shell regression command for docs:

```bash
! git grep -n -E 'ssh_only|local_keep|max_disk_pct|archive engine|archive migrations|disk threshold' -- README.md config.example.toml cmd/mc-backup/main.go
```

- [ ] **Step 2: Run the tests to verify failure.**

Run: `go test ./cmd/mc-backup ./internal/engine -run 'Test(Usage|SaveAuto|Target)' -count=1`

Expected: FAIL because help/example/generated output still exposes obsolete names.

- [ ] **Step 3: Update exact user-facing configuration.** In `config.example.toml`, add:

```toml
[local]
dest_root = "/var/lib/mc-backup"
```

Remove `local_keep` and `max_disk_pct`; replace the server comment with `target = "nas" # or "local"`; retain NAS and retention fields. In CLI help, change the status description to `Show live backup job dashboard` and add the target/local-root concepts to the config help text without mentioning obsolete fields.

- [ ] **Step 4: Rewrite README sections.** Replace the archive flow step with “Prune snapshots for the selected target”; document local snapshots at `<local.dest_root>/<namespace>/<server>/<timestamp>/`, NAS snapshots at the existing path, `target = "local"` versus `target = "nas"`, omitted target resolving to NAS, and validation before RCON/SSH. State that local targets need neither NAS connectivity nor `.nas-ready`; retain `.nas-ready` only for NAS. Remove all archive engine, disk-pressure, SSD working-copy, `ssh_only`, `local_keep`, and `max_disk_pct` instructions. Add local restore and NAS restore commands, and explain that legacy `<watch.path>/<server>/backups` directories are warned about but never managed and must be migrated/deleted manually.

- [ ] **Step 5: Run documentation checks.**

Run: `gofmt -w cmd/mc-backup/main.go cmd/mc-backup/main_test.go && go test ./cmd/mc-backup ./internal/engine -count=1 && ! git grep -n -E 'ssh_only|local_keep|max_disk_pct|archive engine|archive migrations|disk threshold' -- README.md config.example.toml cmd/mc-backup/main.go`

Expected: PASS and the final grep returns no matches.

- [ ] **Step 6: Commit command (do not execute).**

```bash
git add cmd/mc-backup/main.go cmd/mc-backup/main_test.go config.example.toml README.md internal/engine/config_test.go
git commit -m "docs(config): document explicit local backup targets"
```

## Task 7: Full verification and self-review

**Files:**
- Verify: all files named above; no production/test modifications are made while writing this plan.

- [ ] **Step 1: Run formatting and all tests.**

Run: `gofmt -d .`

Expected: no output.

Run: `go test ./... -count=1`

Expected: PASS for `./cmd/mc-backup` and `./internal/engine`.

Run: `make build`

Expected: successful cross-compile producing `./mc-backup`.

- [ ] **Step 2: Verify spec coverage manually.** Confirm the implementation has tests/tasks for: local config parsing and root normalization; omitted/invalid/missing target validation before RCON/SSH; exact local path and local rsync arguments; NAS behavior preservation; independent local/NAS history and failure behavior; local days/count retention; no local NAS pruning and no NAS local directory; archive/disk utility removal; legacy directory warning at startup/discovery; and all CLI/example/README/generated-output removals.

- [ ] **Step 3: Perform placeholder and type-consistency scans.**

Review this document manually for unresolved placeholders, vague implementation directions, and references to functions or fields that were never defined.

Review that every task uses the same names and signatures: `LocalConfig`, `ServerConfig.Target`, `resolveBackupTarget(string, ServerConfig, LocalConfig) (string, error)`, `pruneLocalByDays(string, int, time.Time)`, and `BackupServer(...)(string, bool, error)`.

- [ ] **Step 4: Confirm no unrelated files are staged or committed.**

Run: `git status --short && git diff --check`

Expected: only intentional implementation files are changed by the engineer; this planning task itself changes only `docs/superpowers/plans/2026-07-21-local-backup-target.md`. Do not commit this plan or any implementation from this task.
