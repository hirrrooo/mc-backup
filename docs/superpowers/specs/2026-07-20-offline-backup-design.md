# Offline Backup Design

## Problem

`mc-backup backup` requires the target server's container to be running. If the
container is down, the backup is skipped. Users want to trigger a backup from
within a server directory even when the server is offline.

## Design

Add `--offline` to `mc-backup backup`. When set, the daemon skips all
container checks and RCON commands and rsyncs the data directory as-is. When
no server argument is given, the server name is inferred from the basename of
the current working directory (matched against known servers in config).

### Changes

4 files, all within existing patterns:

**`cmd/mc-backup/main.go`** — CLI entry point
- Add `--offline` flag to `backupCmd`
- When no server arg: read CWD basename, verify it's a known server in config
- Send `?offline=true&server=X` to daemon

**`internal/engine/status.go`** — HTTP API
- `StatusCallbacks.OnBackup` signature: add `offline bool`
- `/backup` handler: parse `?offline=true` query param and pass through

**`internal/engine/daemon.go`** — backup orchestration
- `runBackupCycle(ctx, onlyServer, offline)`:
  - `offline==true`: skip `containerRunning()` check, skip `PauseIfNoPlayers` check
  - Pass `offline` to `BackupServer()`

**`internal/engine/backup.go`** — core backup logic
- `BackupServer(ctx, watch, name, server, prevLocal, prevNAS, offline)`:
  - `offline==true`: skip `save-off`/`save-all flush`/`save-on` defer entirely
  - Proceed directly to routing + rsync

### What stays the same

- Routing (local vs NAS based on SSHOnly + disk usage)
- Rsync args, excludes, progress callbacks
- Snapshot naming (`YYYYMMDD-HHMM`)
- Pruning (local by count, NAS by days/count)
- Archive logic
- Last-snapshot tracking
