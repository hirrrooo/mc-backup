# mc-backup: Minecraft Backup & Archive Manager

**Date:** 2026-06-11  
**Type:** Single Go binary, systemd-managed daemon  

## Overview

A single binary (`mc-backup`) that replaces the existing `backup.sh` / `backup-loop.sh` bash scripts and the `mc-archive.go` tiered-storage daemon. Runs on the Minecraft gameserver host, performing:

1. **Backup** — RCON-controlled save-and-sync, then rsync snapshots (directory-based, hard-link deduplicated) to local SSD and/or a remote NAS over SSH.  
2. **Archive** — Rate-limited migration of older local backups to NAS when SSD usage exceeds a threshold.  
3. **Status API** — HTTP endpoint for live job progress, usable by the `status` CLI subcommand.  
4. **Auto-discovery** — Scans watched directory roots for new Minecraft server directories, auto-provisions config, backup paths, and NAS destinations.

## Architecture

```
┌── CLI ──────────────────────────────────────────────────────────┐
│  mc-backup status             # HTTP dashboard                  │
│  mc-backup config set ...     # dynamic config edits            │
│  mc-backup config get ...     # read config values              │
│  mc-backup run                # start the daemon                │
└─────────────────────────────────────────────────────────────────┘

┌── Daemon (mc-backup run) ───────────────────────────────────────┐
│                                                                 │
│  ┌─────────────────┐  ┌──────────────────┐  ┌───────────────┐  │
│  │ Auto-Discovery  │  │  Backup Engine   │  │ Archive Engine│  │
│  │ (per scan tick) │  │  (per interval)  │  │ (on threshold)│  │
│  └────────┬────────┘  └────────┬─────────┘  └───────┬───────┘  │
│           │                    │                     │          │
│           │  detects new       │  docker exec       │ rate-     │
│           │  server dirs       │  rcon + rsync      │ limited   │
│           │  writes config     │  to local/NAS      │ move to   │
│           │                    │                     │ NAS       │
│           ▼                    ▼                     ▼          │
│  ┌─────────────────────────────────────────────────────────┐    │
│  │                    Config State (TOML)                   │    │
│  │              inotify-watched for live reload             │    │
│  └─────────────────────────────────────────────────────────┘    │
│           │                    │                     │          │
│           ▼                    ▼                     ▼          │
│  ┌─────────────────────────────────────────────────────────┐    │
│  │                Status API (:47990)                       │    │
│  │            /status  /config  /servers                     │    │
│  └─────────────────────────────────────────────────────────┘    │
│                                                                 │
│  ┌─────────────────────────────────────────────────────────┐    │
│  │                  Pruning Engine                          │    │
│  │       local (by count) + NAS (by days + count)           │    │
│  └─────────────────────────────────────────────────────────┘    │
└─────────────────────────────────────────────────────────────────┘
```

## Components

### 1. Config (`config.go`)
- Primary source: TOML file at `~/.config/mc-backup/config.toml` or `/etc/mc-backup/config.toml`
- Environment variable overrides: `MC_BACKUP_<SECTION>_<KEY>` (e.g. `MC_BACKUP_NAS_SSH_HOST`)
- Inotify watcher reloads config within 5s of file change — active cycle finishes before new config takes effect
- Auto-provisioning writes new `[server.<name>]` blocks when discovering new directories

**Config schema:**
```toml
[global]
listen_addr = "127.0.0.1:47990"
backup_interval = "1h"          # human-readable duration
initial_delay = "2m"
max_mbps = 40.0                 # global rate limit for ALL NAS rsync (env: MC_BACKUP_GLOBAL_MAX_MBPS)

[nas]
ssh_user = "backup"
ssh_host = "nas.local"
ssh_port = 22
ssh_key = "~/.ssh/id_ed25519"
dest_root = "/volume1/backups"

# Global retention defaults (overridable per-server)
[retention]
prune_days = 7
prune_count = 0

[[watch]]
path = "/opt/minecraft/servers/docker/servers"
namespace = "minecraft"
local_path = "/opt/minecraft/backups"
local_keep = 3                  # recent backups kept on SSD
max_disk_pct = 90               # trigger archive to NAS above this

# Per-server overrides — populated automatically, manually editable
[server.creative]
enabled = true
ssh_only = false
container_name = "creative-mc-1" # auto-detected via docker compose ps
rcon_password = "hunter2"
data_dir = "/opt/mc/creative"   # defaults to watch.path/server_name

[server.skyblock]
enabled = true
ssh_only = true

[server.cone-ftb-evo]
enabled = false                 # discovered but backups skipped
```

### 2. Backup Engine (`backup.go`)

**Per-server backup cycle:**

1. `docker exec <container> rcon-cli save-off` — disable autosave. Fails cycle if unreachable.
2. `docker exec <container> rcon-cli save-all flush` — force world save + flush to disk
3. `sync` — flush OS filesystem buffers (host-level)
4. **Route decision:**
   - If `server.ssh_only == true` OR SSD usage would exceed `max_disk_pct` → rsync directly to NAS over SSH
   - Otherwise → rsync to local `local_path/<server>/<yyyymmdd-hhmm>/`
5. `docker exec <container> rcon-cli save-on` — **guaranteed via defer.** If this deferred call fails after 5 retries, log FATAL and exit (server left with autosave OFF is dangerous)
6. Prune local backups (keep `local_keep` most recent)
7. Prune NAS backups (by `prune_days` and `prune_count`)

**Rsync behavior:**
- Local: `rsync -a --link-dest=../<previous> <data_dir>/ <local_path>/<server>/<yyyymmdd-hhmm>/`
- NAS: `rsync -a -e ssh --bwlimit=<max_mbps*1024> --link-dest=../<previous> <data_dir>/ user@nas:<dest_root>/<namespace>/<server>/<yyyymmdd-hhmm>/`
- `--bwlimit` applies the global `max_mbps` rate limit (KB/s) to EVERY rsync bound for the NAS — both direct `ssh_only` backups and archive migrations. No local-only transfer is rate-limited.
- `--link-dest` points to the previous backup directory. First run has no link-dest (full copy).
- Unchanged files share the same inodes; each snapshot appears full but uses near-zero additional space.

**Excludes:** `--exclude=*.jar --exclude=cache --exclude=logs --exclude=*.tmp` (configurable per-server via `excludes` array).

**Error handling:**
| Scenario | Behavior |
|---|---|
| RCON unreachable (5 retries) | Log error, skip cycle |
| `save-off` fails | Abort immediately, never touch autosave |
| `save-on` defer fails | Log FATAL, exit with code 1 |
| rsync fails (SSH down, full disk) | Log error, save-on still deferred, skip prune |
| SIGTERM / systemd stop | Context cancelled, save-on fires via defer, clean shutdown |

### 3. Archive Engine (`archive.go`)
Migrates older local backups to NAS when SSD usage exceeds `max_disk_pct`.

- **Trigger:** After a local backup completes, if SSD usage exceeds `max_disk_pct`, select oldest local snapshots beyond `local_keep` for migration
- **Transfer:** `rsync -a -e ssh --bwlimit=<max_mbps*1024> <local_snapshot>/ user@nas:<dest_dir>/<snapshot_name>/` — rate-limited by the global `max_mbps` via rsync's built-in `--bwlimit` (no custom throttle needed)
- **Growth detection:** If the snapshot directory is being written to by a concurrent backup cycle, skip it
- **Single writer:** One global `nasWriteLock` channel (capacity 1) ensures only one NAS transfer at a time across both backup and archive engines
- **Cleanup:** After successful rsync, delete the local snapshot, freeing SSD space
- **NAS sentinel:** Before any NAS operation, check `<dest_root>/.nas-ready` — skip if missing

### 4. Auto-Discovery (`discovery.go`)

Runs every scan cycle (default 1 min):

1. Read each `[[watch]].path` directory
2. For each subdirectory:
   - If already in `[server.<name>]` with `enabled = false` → skip
   - If not present in `[server.<name>]` config → auto-provision:
     - **Container discovery:** Look for `docker-compose.yml` or `compose.yml` in the server directory. Run `docker compose ps --format json` to extract the actual container name. If not found or compose unavailable, fall back to `<name>-mc-1`.
     ```toml
     [server.<name>]
     enabled = true
     container_name = "cone-ftb-evo-mc-1"  # auto-detected
     rcon_password = ""
     ssh_only = false
     ```
   - Create local backup directory: `<watch.local_path>/<namespace>/<name>/`
   - Create NAS directory: `user@nas:<dest_root>/<namespace>/<name>/`
   - Log `[PROVISION] Discovered new server: <name> (container: cone-ftb-evo-mc-1)`
3. Write config file (preserving existing manual entries, comments not preserved on write)

### 5. Status API & CLI (`status.go`, `main.go`)

**API endpoints:**
- `GET /status` — JSON map of active jobs (backup + archive transfers)
- `GET /config` — current effective config (JSON)
- `GET /servers` — list of known servers and their last backup time

**CLI subcommands:**
```
mc-backup run          # Start the daemon (systemd uses this)
mc-backup status       # HTTP client → GET /status → formatted table
mc-backup config set <key> <value>   # Write to TOML file
mc-backup config get <key>           # Read from TOML
```

### 6. Pruning Engine (`prune.go`)

- **Local:** `find <local_path> -maxdepth 1 -type d -name '<server>-*'` sorted by mtime/newest-first, delete all beyond `local_keep`
- **NAS:** `ssh nas "find <dest_root>/<namespace>/<server> -maxdepth 1 -type d -name '*'-mtime +<prune_days>"` + optional count-based pruning using the same approach

### 7. Systemd Integration

```ini
[Unit]
Description=Minecraft Backup & Archive Service
After=network-online.target docker.service
Wants=network-online.target
Requires=docker.service

[Service]
Type=simple
User=root
ExecStart=/usr/local/bin/mc-backup run --config /etc/mc-backup/config.toml
Restart=on-failure
RestartSec=30
KillSignal=SIGTERM
TimeoutStopSec=60

[Install]
WantedBy=multi-user.target
```

- 60s stop timeout allows current backup cycle + `save-on` defer to complete
- `Requires=docker.service` ensures Docker is running first
- `Restart=on-failure` with 30s cooldown prevents crash loops

## File Layout

```
/usr/local/bin/mc-backup              # Binary
/etc/mc-backup/config.toml            # Config (primary location)
~/.config/mc-backup/config.toml       # Config (alt location)

/home/hiro/code/Personal Work/mc-backup/   # Source repo
├── main.go
├── config.go
├── backup.go
├── archive.go
├── discovery.go
├── status.go
├── prune.go
├── Makefile
├── mc-backup.service
├── docs/superpowers/specs/
│   └── 2026-06-11-mc-backup-design.md
└── backup.sh, backup-loop.sh, mc-archive.go  # (kept for reference)
```

## Key Design Decisions

1. **Shell out to `ssh` and `rsync` binaries** rather than using Go SSH/rsync libraries. Lowers complexity, inherits system SSH config, known_hosts, ProxyJump, and agent forwarding for free.
2. **Directory snapshots with hard links** over tarballs. Enables fast incremental storage without a custom format, fully browseable on the NAS, and trivially restorable.
3. **Single binary, subcommand-driven.** One `mc-backup` binary with `run`/`status`/`config`/`blacklist` subcommands. No separate daemon + CLI process split.
4. **Dynamic config via TOML + inotify.** Config is the source of truth, auto-provisioned entries look the same as manual ones, and changes take effect without restart.
5. **Storage safety gate.** Before each local backup, check current SSD usage + estimated data dir size. If projected > max_disk_pct, skip local storage and rsync directly to NAS.
6. **Global NAS rate limit.** ALL rsync transfers to the NAS (backup + archive) are capped at `max_mbps` via `rsync --bwlimit`. Controlled by the TOML config or the `MC_BACKUP_GLOBAL_MAX_MBPS` env var.

## Things NOT Included (Yet)

- Pre/post backup hook scripts (can be added later)
- Multiple NAS destinations per server
- Encrypted backup support (restic-like)
- Email/webhook notifications
- Per-server backup intervals (all servers share the global interval)
- Config comments preserved on auto-write (TOML library limitation)
