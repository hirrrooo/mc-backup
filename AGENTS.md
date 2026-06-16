# AGENTS.md

## Build & Test

- **Build:** `make build` (cross-compiles `linux/amd64` binary to `./mc-backup`)
- **Test all:** `go test ./...`
- **Test single package:** `go test ./internal/engine/...` or `go test ./cmd/...`
- **Lint:** `gofmt -d .` (Go's built-in formatter)
- No CI, no pre-commit hooks.

## Architecture

- Single Go module (`mc-backup`), two packages:
  - `cmd/mc-backup/` — CLI entrypoint (`main` package); subcommands are `run`, `status`, `backup`, `scan`, `cancel`, `config`, `update`, `version`
  - `internal/engine/` — all business logic; called directly by `main`
- The CLI communicates with the running daemon via HTTP calls to its local status API (`http://<listen_addr>/<endpoint>`)

## Config

- Config search: `~/.config/mc-backup/config.toml`, then `/etc/mc-backup/config.toml`
- Auto-provisioned servers go to a sidecar `<path>-auto.toml` (loaded first, main config overwrites)
- `listen_addr` changes require daemon restart; all other config reloads live via inotify
- Server names are case-insensitive (normalized to lowercase)
- Env overrides: `MC_BACKUP_*` (e.g. `MC_BACKUP_LISTEN_ADDR`)

## Test Seams

- `internal/engine/command.go`: global `commandRunner` variable (interface) — use `withCommandRunner()` to swap in tests
- `cmd/mc-backup/main.go`: `usageOutput` (`io.Writer`), `findRepoRoot`, and `runUpdateStep` are package-level function variables that can be replaced in tests

## Key Behaviors

- Backups: RCON `save-off` → flush → rsync → RCON `save-on` (deferred guarantee even on panic)
- NAS operations require a `.nas-ready` sentinel file on the NAS; without it, all SSH-based work is skipped
- NAS writes are serialized via a buffered channel (`nasWriteLock`, size 1)
- Snapshot directories use format `YYYYMMDD-HHMM` (13 chars, dash at position 8)
