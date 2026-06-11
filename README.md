# mc-backup

Minecraft server backup daemon — RCON-controlled backup cycles with rsync snapshots to local storage and/or a remote NAS over SSH. Auto-discovers servers from watched directories, auto-provisions config, and runs as a systemd service.

## Quick Start

```bash
# Build and install
make install

# Edit config for your servers
nano /etc/mc-backup/config.toml

# Start the service
systemctl start mc-backup

# Check status
mc-backup status
```

## Prerequisites

- **Go 1.21+** to build from source
- **Docker** (running on the same host — must be up before the service starts)
- **rsync** and **ssh** on the gameserver host
- **SSH key auth** from the gameserver to the NAS (passwordless)
- A **Minecraft server** managed by Docker (tested with `itzg/minecraft-server`)

## How It Works

Every backup cycle:

1. `docker exec rcon-cli save-off` — disables autosave
2. `docker exec rcon-cli save-all flush` — flushes world data to disk
3. `sync` — flushes OS filesystem buffers
4. **rsync** copies the world directory to a timestamped snapshot (either local SSD or direct to NAS via SSH)
5. `docker exec rcon-cli save-on` — re-enables autosave (guaranteed even on crash)
6. **Prune** — removes old snapshots beyond the configured limits
7. If SSD usage exceeds the threshold, the **archive engine** migrates old local snapshots to NAS (rate-limited)

Each snapshot is a full directory using rsync's `--link-dest` — unchanged files are hard-linked to the previous snapshot, so storage is incremental while each snapshot is a browseable, restorable directory.

```
/opt/minecraft/backups/minecraft/survival/
  20250611-1200/   ← full directory (hard links to 1100 for unchanged files)
  20250611-1100/   ← full directory
  20250611-1000/   ← full directory (will be archived to NAS if SSD > 90%)
```

## Installation

### From Source

```bash
git clone <repo-url> mc-backup
cd mc-backup
sudo make install
```

This builds the binary, installs it to `/usr/local/bin/mc-backup`, places the systemd unit at `/etc/systemd/system/mc-backup.service`, copies the example config to `/etc/mc-backup/config.toml`, and enables the service.

### Manual Install

```bash
go build -o mc-backup .
sudo install -m 755 mc-backup /usr/local/bin/mc-backup
sudo install -d /etc/mc-backup
sudo install -m 644 config.example.toml /etc/mc-backup/config.toml
sudo install -m 644 mc-backup.service /etc/systemd/system/mc-backup.service
sudo systemctl daemon-reload
sudo systemctl enable mc-backup
```

## Configuration

Config lives at `/etc/mc-backup/config.toml` (or `~/.config/mc-backup/config.toml` for user runs). Environment variable overrides use the prefix `MC_BACKUP_`, e.g. `MC_BACKUP_NAS_SSH_HOST=nas2.local`.

### Required Settings

At minimum, you must configure:

```toml
[global]
backup_interval = "1h"          # how often to run a backup cycle (e.g. "30m", "6h")

[nas]
ssh_user = "myuser"             # SSH user for NAS access
ssh_host = "192.168.1.50"       # NAS hostname or IP
ssh_key = "~/.ssh/id_ed25519"   # path to private SSH key
dest_root = "/volume1/backups"  # NAS directory for backups

[[watch]]
path = "/opt/minecraft/servers/docker/servers"   # directory containing per-server subdirs
namespace = "minecraft"                          # used for NAS subfolder naming
local_path = "/opt/minecraft/backups"            # local SSD backup directory
```

### Full Reference

```toml
[global]
listen_addr = "127.0.0.1:47990"   # HTTP API for mc-backup status
backup_interval = "1h"            # interval between backup cycles
initial_delay = "2m"              # delay before first backup on startup
max_mbps = 40.0                   # rate limit for ALL rsync to NAS (env: MC_BACKUP_GLOBAL_MAX_MBPS)

[nas]
ssh_user = "backup"               # SSH username for NAS
ssh_host = "nas.local"            # NAS hostname or IP
ssh_port = 22                     # SSH port (default 22)
ssh_key = "~/.ssh/id_ed25519"     # SSH private key path
dest_root = "/volume1/backups"    # root directory on NAS for all backups

[retention]
prune_days = 7                    # delete NAS backups older than N days (0 = disabled)
prune_count = 0                   # keep only N most recent NAS backups (0 = disabled)

[[watch]]
path = "/opt/minecraft/servers/docker/servers"   # watched directory
namespace = "minecraft"                          # namespace for NAS path: <dest_root>/<namespace>/<server>/
local_path = "/opt/minecraft/backups"            # local SSD backup root
local_keep = 3                  # number of recent snapshots to keep on SSD
max_disk_pct = 90               # archive to NAS when SSD usage exceeds this %
```

### Per-Server Overrides

The daemon auto-discovers server directories under each `[[watch]].path`. It detects the Docker container name via `docker compose ps`, falling back to `<dirname>-mc-1`. Config entries are auto-written. You can override settings per server:

```toml
[server.survival]
enabled = true                  # set to false to skip backups for this server
ssh_only = true                 # skip local SSD, rsync directly to NAS
container_name = "survival-mc-1"  # override auto-detected container name
rcon_password = "hunter2"       # RCON password (required for backup to work)
```

Set `enabled = false` to stop backing up a server without deleting its config.

Set `ssh_only = true` for servers with large worlds — backups go straight to NAS, never touch local SSD.

## CLI Usage

```bash
mc-backup run              # start the daemon (used by systemd)
mc-backup status           # live dashboard of active jobs
mc-backup config get <key> # read a config value
mc-backup config set <key> <value>  # write a config value (live reload)
mc-backup version          # print version
```

Config keys use dot-separated paths:
```bash
mc-backup config get nas.ssh_host
mc-backup config set server.survival.ssh_only true
mc-backup config set global.backup_interval 2h
```

The daemon watches the config file via inotify — changes take effect within seconds without restart (except `listen_addr`, which requires a service restart).

## NAS Setup

### SSH Key Auth

```bash
# On the gameserver, generate a key if you don't have one
ssh-keygen -t ed25519 -f ~/.ssh/id_ed25519_backup

# Copy it to the NAS
ssh-copy-id -i ~/.ssh/id_ed25519_backup backup@nas.local
```

### NAS Sentinel File

The archive engine checks for a sentinel file before writing to the NAS. Create it once:

```bash
ssh backup@nas.local "touch /volume1/backups/.nas-ready"
```

Without this file, archive migrations and all `ssh_only` backups are skipped (NAS treated as unavailable).

## Storage Architecture

```
Gameserver (SSD)                          NAS (HDD)
─────────────────                         ──────────
/opt/minecraft/backups/                   /volume1/backups/
  minecraft/                                minecraft/
    survival/                                 survival/
      20250611-1200/  ← --link-dest→           20250611-1000/
      20250611-1100/  ← --link-dest→           20250611-0900/
      20250611-1000/  ← (archived to NAS)      
                                                 .nas-ready
```

- Recent snapshots stay on fast SSD for quick restores
- When SSD hits `max_disk_pct`, the archive engine migrates oldest snapshots to NAS
- `ssh_only` servers never use local SSD — data goes directly to NAS
- All NAS transfers are rate-limited by `max_mbps` to avoid I/O contention with the game server

## Service Management

```bash
systemctl start mc-backup     # start the daemon
systemctl stop mc-backup      # graceful stop (waits for current cycle)
systemctl restart mc-backup   # restart
systemctl status mc-backup    # check if running
journalctl -u mc-backup -f    # follow logs
```

The service sends `SIGTERM` on stop, which triggers the deferred `save-on` before exiting. Timeout is 60 seconds — if the current backup still hasn't finished, systemd force-kills.

## Restoring a Backup

Each snapshot is a complete directory. To restore:

```bash
# From local SSD
rsync -a /opt/minecraft/backups/minecraft/survival/20250611-1100/ \
  /opt/minecraft/servers/docker/servers/survival/

# From NAS
rsync -a -e ssh backup@nas.local:/volume1/backups/minecraft/survival/20250611-1000/ \
  /opt/minecraft/servers/docker/servers/survival/
```

Then restart the Minecraft container.

## Environment Variable Overrides

Any config value can be set via environment variables. Use the prefix `MC_BACKUP_` with the config path in uppercase, dots replaced with underscores:

```bash
MC_BACKUP_NAS_SSH_HOST=nas2.local
MC_BACKUP_GLOBAL_MAX_MBPS=20.0
MC_BACKUP_SERVER_SURVIVAL_RCON_PASSWORD=secret
MC_BACKUP_SERVER_SURVIVAL_SSH_ONLY=true
```

Server names are case-insensitive for env var lookup.

## Uninstall

```bash
sudo make uninstall
```
