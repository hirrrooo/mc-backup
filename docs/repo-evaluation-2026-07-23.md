# mc-backup — Repository Evaluation & Phased Improvement Plan

**Date:** 2026-07-23
**Evaluated commit:** `87b9dc6` (branch `main`)
**Audience:** This document is written to be consumed by another LLM acting on the
repository. Findings are concrete, include `file:line` anchors, and each carries a
recommended action and a verification step. Do not assume any finding is still
present without re-checking the anchor first — the tree may have moved.

---

## 1. What this project is

`mc-backup` is a single-module Go daemon that takes RCON-coordinated rsync
snapshots of Docker-hosted Minecraft servers to either a local directory or a
remote NAS over SSH. It auto-discovers servers under watched directories,
auto-provisions their config, exposes a small localhost HTTP status API, ships as
a systemd service, and self-updates from GitHub releases.

- **Module:** `mc-backup` (Go 1.21)
- **Packages:** `cmd/mc-backup` (CLI) and `internal/engine` (all logic)
- **Source size:** ~2,900 LoC production + ~1,000 LoC tests across 11 files
- **Deps:** `BurntSushi/toml`, `fsnotify/fsnotify` (both minimal, current)

### Baseline health (verified this session)

| Check | Result |
|---|---|
| `go build ./...` | passes |
| `go vet ./...` | clean |
| `go test ./...` | all pass |
| `gofmt -l internal cmd` | clean (no diffs) |
| Test coverage | `internal/engine` **44.9%**, `cmd/mc-backup` **17.1%** |

The code itself is in good shape: idiomatic Go, a clean command-runner test seam,
atomic config writes (temp-file + rename), a deferred `save-on` guarantee, and
sensible structured logging. **The most urgent problems are not in the Go code —
they are in repository hygiene and documentation drift.**

### Severity legend

- **P0 — Blocker/hygiene:** wrong or embarrassing state that should be fixed
  before anything else; near-zero risk to fix.
- **P1 — Correctness/security:** can cause data loss, secret exposure, or wrong
  behavior under realistic conditions.
- **P2 — Robustness/maintainability:** hardening, testing, and CI gaps.
- **P3 — Polish:** ergonomics and nice-to-haves.

---

## 2. Phase 0 — Repository hygiene (P0, do first)

These are safe, mechanical, and currently make the repo look unmaintained. None
require design decisions.

### 0.1 A Python virtualenv is committed (`venv/`, 1,489 files)
- **Evidence:** `git ls-files venv | wc -l` → **1489**. The entire `venv/`
  (pip, site-packages, `.pyc` files, interpreter symlinks) is staged/tracked.
- **Why it matters:** This is a Go project — there is no Python. The venv bloats
  the repo, leaks the author's local toolchain, and pollutes diffs and clones.
- **Action:** `git rm -r --cached venv` and add `venv/` to `.gitignore`. Delete
  the directory from disk if it is not intentionally used for a local script.
- **Verify:** `git ls-files venv` returns nothing; `git status` is clean of venv.

### 0.2 Stray debug scratch file `test_tz.go` at repo root
- **Evidence:** `git status` shows `A test_tz.go`; it is a throwaway `package main`
  with a `func main()` printing timezone/prune math (`test_tz.go:1`).
- **Why it matters:** It is dead scratch code sitting in the module root, unrelated
  to the CLI in `cmd/`. It confuses readers and could shadow build expectations.
- **Action:** `git rm --cached test_tz.go` and delete the file. If the timezone/prune
  logic it probes is worth keeping, fold it into `internal/engine/prune_test.go`
  as a real table test instead.
- **Verify:** file is gone; `go build ./...` still passes.

### 0.3 `.gitignore` contradicts what is actually tracked (docs)
- **Evidence:** `.gitignore:9-10` ignore `docs/superpowers/plans/` and
  `docs/superpowers/specs/`, yet **8** files under those paths are committed
  (`git ls-tree -r HEAD | grep docs/superpowers` → 8). The current change set even
  shows `M docs/superpowers/specs/2026-07-21-local-backup-target-design.md` — a
  modification to a file that is simultaneously git-ignored.
- **Why it matters:** Contributors (human or LLM) cannot tell whether design docs
  belong in version control. New docs silently won't be added; edits to tracked
  ones are confusing.
- **Action:** Pick one policy. Recommended: **track the design docs** — remove
  lines 9–10 from `.gitignore` so `plans/` and `specs/` are versioned normally.
  (Alternative: `git rm --cached` them and keep them local — but they look like
  intentional project history worth keeping.)
- **Verify:** `git check-ignore docs/superpowers/specs/<file>` returns nothing
  under the chosen policy, and `git status` has no ignored-but-tracked files.

### 0.4 Go toolchain version is stated three different ways
- **Evidence:** `.tool-versions` → `golang 1.26.3`; `go.mod:3` → `go 1.21`;
  `README.md:18` → "Go 1.21+".
- **Why it matters:** `.tool-versions` (asdf/mise) drives local toolchain
  selection; a value that disagrees with `go.mod` misleads contributors and CI is
  pinned to `go.mod` via `go-version-file` (`.github/workflows/release.yml:23`).
- **Action:** Reconcile. Either bump `go.mod` + README to the intended minimum, or
  set `.tool-versions` to a real, matching version. Keep all three in agreement.
- **Verify:** the three values are mutually consistent.

---

## 3. Phase 1 — Documentation truth (P0/P1, do second)

`AGENTS.md` is the file another LLM reads first. It currently contains false
statements that will actively misdirect an agent.

### 1.1 `AGENTS.md` claims "No CI" — but CI exists
- **Evidence:** `AGENTS.md:9` "No CI, no pre-commit hooks." vs
  `.github/workflows/release.yml` (a Release workflow that runs `go test ./...` and
  publishes a rolling `latest` release on every push to `main`).
- **Action:** Update `AGENTS.md` to describe the release workflow and the rolling
  `latest` tag scheme.

### 1.2 `AGENTS.md` documents a `nasWriteLock` that does not exist
- **Evidence:** `AGENTS.md:35` "NAS writes are serialized via a buffered channel
  (`nasWriteLock`, size 1)". `grep -rn nasWriteLock --include='*.go'` → **no
  matches**. NAS-write serialization actually happens implicitly because
  `runBackupCycle` holds `d.cycleMu` (`daemon.go:362`) so only one cycle runs at a
  time.
- **Why it matters:** An agent told about a lock that isn't there may "preserve" or
  reason about non-existent invariants, or fail to realize concurrency is
  guaranteed by `cycleMu` instead.
- **Action:** Replace the bullet with the real mechanism: backup cycles are
  mutually exclusive via `Daemon.cycleMu`; there is no dedicated NAS lock.

### 1.3 README rate-limit / behavior claims should be spot-checked
- README states NAS writes are rate-limited by `global.max_mbps`; confirmed in
  `backup.go:46-48` (`--bwlimit`). This one is accurate — noted so the doc pass
  doesn't "fix" a correct statement. Re-verify any other README claim against code
  during the doc pass rather than trusting prose.

---

## 4. Phase 2 — Security (P1)

### 2.1 RCON password exposed in the host process list
- **Evidence:** `rcon.go:13-21` builds the argv
  `docker exec -e RCON_PASSWORD=<password> <container> rcon-cli <cmd>`. The
  password is a literal command-line argument of the `docker` process, visible to
  **any local user** via `ps aux` / `/proc/<pid>/cmdline` for the lifetime of each
  exec (every `save-off`, `save-all flush`, `save-on`, and `list`).
- **Why it matters:** Local secret disclosure. On a shared host this leaks the RCON
  password to unprivileged users.
- **Action:** Pass the secret through the child process environment instead of
  argv. Options: set `-e RCON_PASSWORD` (name only, no value) and provide the value
  via `exec.Cmd.Env` (requires extending the `command`/`commandRunner` seam in
  `command.go` to set env), or pipe it via stdin if `rcon-cli` supports it. Keep the
  existing test seam working.
- **Verify:** while a backup runs, `ps aux | grep RCON_PASSWORD` shows no value; add
  a unit test asserting the password is not present in the constructed argv.

### 2.2 Status HTTP API is unauthenticated (state-changing endpoints)
- **Evidence:** `status.go:60-98` exposes `POST /backup`, `POST /cancel`,
  `POST /scan` with no authentication. Default `listen_addr` is `127.0.0.1:47990`
  (`config.go:92`), which mitigates this — but the value is user-configurable and
  live-reloadable, and nothing prevents binding to `0.0.0.0`.
- **Why it matters:** If `listen_addr` is ever set to a non-loopback interface,
  anyone on the network can trigger or cancel backups (a cancel can abort a backup
  mid-flight; a flood of `/backup` is a DoS).
- **Action (defense in depth):** (a) document loudly that `listen_addr` must stay on
  loopback; and/or (b) reject non-loopback binds unless an explicit
  auth token is configured; and/or (c) add a shared-secret header check on
  state-changing endpoints. At minimum, add the warning to README and
  `config.example.toml`.
- **Verify:** attempting a non-loopback bind without auth fails or logs a prominent
  warning.

### 2.3 Auto-provisioned secrets file — confirm permissions (looks OK)
- `SaveAutoServers` writes `rcon_password` in plaintext to `<config>-auto.toml`
  (`config.go:247-256`). This is expected for this tool, and the temp-file is
  created via `os.CreateTemp` (mode 0600) then renamed, so perms are fine. **No
  action needed** — recorded so a future reviewer doesn't flag it as new.

---

## 5. Phase 3 — Correctness & robustness (P2)

### 3.1 rsync excludes are hardcoded, not configurable
- **Evidence:** `backup.go:170` `excludes := []string{"*.jar", "cache", "logs",
  "*.tmp"}` — identical for every server, both targets.
- **Why it matters:** Different servers legitimately need different excludes (mod
  data, plugin caches, dynmap tiles). No way to override without recompiling.
- **Action:** Add an optional `excludes []string` (global and/or per-server) to the
  config with the current list as the default. Thread it into `localRsyncArgs` /
  `nasRsyncArgs`.
- **Verify:** a configured exclude appears in the rsync argv (unit-testable via the
  existing arg builders).

### 3.2 NAS count-prune can run `rm -rf` with no operands
- **Evidence:** `prune.go:97-103` builds
  `ls -dt <dir>/[0-9]*-[0-9]* 2>/dev/null | tail -n +N | xargs rm -rf`. When the
  pipeline is empty, GNU `xargs` (without `-r`) still runs `rm -rf` once with no
  args. It's harmless today (errors out), but fragile.
- **Action:** Add `xargs --no-run-if-empty` (`-r`) to the remote command.
- **Verify:** with zero snapshots present, no `rm` is invoked.

### 3.3 `sync` invocation ignores errors and uses a detached context
- **Evidence:** `backup.go:204`
  `commandRunner.CommandContext(context.Background(), "sync").Run()` — result
  discarded, not tied to the cycle context.
- **Action:** Capture and at least `slog.Warn` on error; consider using the cycle
  `ctx` so a cancel during the (usually brief) sync is honored. Low risk, low
  effort.

### 3.4 CLI truncates daemon responses to 64 bytes
- **Evidence:** `main.go:201-205` and `main.go:238-240` read into a fixed
  `[64]byte` with a single `Read`. Longer or chunked responses are truncated, and a
  single `Read` may return fewer bytes than sent.
- **Why it matters:** Cosmetic today (responses are short like "backup triggered"),
  but brittle if response text grows.
- **Action:** Use `io.ReadAll(io.LimitReader(resp.Body, N))` with a sane cap.

### 3.5 Confirm `d.lastBackups` access is fully serialized
- **Evidence:** `d.lastBackups` is written in `Run()` setup (`daemon.go:258-267`)
  and read/written in `runBackupCycle` (`daemon.go:440-478`). Cycles are serialized
  by `cycleMu`, and setup completes before the ticker loop — so this is *likely*
  race-free, but `runDiscovery` can `go d.runBackupCycle(...)` (`daemon.go:507`) and
  the status server callbacks also spawn cycles (`daemon.go:244-246`).
- **Action:** Run `go test -race ./...` in CI (see 4.1). If clean, add a short
  comment documenting that `cycleMu` is what protects `lastBackups`. If not, guard
  the map.
- **Verify:** `go test -race ./...` passes.

---

## 6. Phase 4 — CI, testing, and tooling (P2)

### 4.1 The only workflow both tests *and* releases, on `main` only
- **Evidence:** `.github/workflows/release.yml` triggers on `push` to `main`,
  runs `go test ./...`, then force-pushes a `latest` tag and publishes a release.
  There is **no workflow for pull requests or non-main branches** — so proposed
  changes are never tested before merge, and `go vet`, `gofmt`, and `-race` are not
  run anywhere.
- **Action:** Add a separate `ci.yml` triggered on `pull_request` and `push` that
  runs, as required checks: `gofmt -l` (fail on output), `go vet ./...`,
  `go test -race -cover ./...`. Keep `release.yml` for publishing only.
- **Verify:** opening a PR runs the checks; a `gofmt`/`vet` violation fails CI.

### 4.2 Add a linter
- **Evidence:** `AGENTS.md:8` lists only `gofmt -d .` as "lint".
- **Action:** Adopt `golangci-lint` (errcheck, ineffassign, staticcheck,
  gosec) with a committed `.golangci.yml`, wired into `ci.yml` and the `Makefile`.
  Expect it to flag the ignored errors in 3.3 and possibly the shell-command
  construction in `prune.go`.

### 4.3 Raise coverage on the risky paths
- **Evidence:** coverage is 44.9% (engine) / 17.1% (cmd). The command-runner seam
  (`command.go`) and function-variable seams (`main.go` `downloadFile`,
  `verifyChecksum`, `runUpdateStep`) make most paths testable without real docker/
  ssh.
- **Action (priority order):** `backup.go` `BackupServer` happy-path + `save-on`
  defer-on-failure; `prune.go` day/count logic (local and NAS command builders);
  `main.go` `runUpdate` / `verifyChecksum` (checksum-mismatch path); config
  env-override + `saveSplit` round-trips.
- **Verify:** engine coverage climbs past ~65%; the `save-on`-still-runs-on-error
  invariant has an explicit test.

---

## 7. Phase 5 — Polish (P3)

- **5.1 `Makefile`/README onboarding:** ensure `make build` matches the CI build
  flags (ldflags for `version`/`repoURL`) so local and released binaries behave the
  same for `mc-backup update`.
- **5.2 Self-update hardening:** `main.go:runUpdate` shells out to `sudo systemctl`
  and `sudo mv`. Document the sudo requirement and consider a `--dry-run`. The
  checksum verification (`verifyChecksum`, `main.go:61`) is good — keep it.
- **5.3 Config validation on load:** `LoadConfig` doesn't validate that a `nas`
  target has `ssh_host`/`ssh_user`/`dest_root` set, or that `listen_addr` parses.
  Fail fast with a clear message instead of at first backup.
- **5.4 Structured docs index:** once Phase 0.3 is resolved, add a short
  `docs/README.md` pointing at the `plans/` and `specs/` history so the design
  trail is discoverable.

---

## 8. Suggested execution order (for an implementing agent)

1. **Phase 0** (hygiene) — one commit, mechanical, unblocks everything. Do
   `git rm --cached` for `venv/` and `test_tz.go`, fix `.gitignore`, reconcile Go
   versions.
2. **Phase 1** (doc truth) — fix `AGENTS.md` before doing code work that relies on
   it.
3. **Phase 4.1** (add `ci.yml` with `-race`) — get a safety net in place *before*
   the security/correctness refactors so regressions are caught.
4. **Phase 2.1** (RCON secret) then **Phase 2.2** (API auth/docs).
5. **Phase 3** correctness items, each with a test.
6. **Phase 4.2–4.3** linter + coverage.
7. **Phase 5** polish.

Each phase should be its own commit/PR with the "Verify" step run and its output
recorded in the PR description. Nothing here requires a large rewrite — the
codebase is fundamentally sound; the work is hygiene, hardening, and honesty in
the docs.
