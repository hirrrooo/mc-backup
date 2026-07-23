# mc-backup

Minecraft server backup daemon — RCON-controlled rsync snapshots stored on the
local host or on a remote NAS. It auto-discovers servers under watched
directories, auto-provisions config, and runs as a systemd service.

## Quick Start

```bash
sudo make install
nano ~/.config/mc-backup/config.toml
systemctl start mc-backup
mc-backup status
```

## Prerequisites

- **Go 1.21+** to build from source
- **Docker** running on the same host
- **rsync**
- **ssh** and passwordless SSH key authentication when using a NAS target
- A Minecraft server managed by Docker (tested with `itzg/minecraft-server`)

## How It Works

Each backup cycle disables autosave, flushes the world, syncs filesystem
buffers, copies a timestamped snapshot with rsync, and re-enables autosave even
when the cycle fails. Old snapshots are pruned according to the retention
settings.

Each server explicitly selects a backup target with `target = "local"` or
`target = "nas"`. An omitted target defaults to NAS; this preserves the
previous behavior for existing configurations. Target validation happens
before any RCON or SSH command is run.

### Local target

Set `target = "local"` to store snapshots at:

```
<local.dest_root>/<namespace>/<server>/<timestamp>/
```

Local rsync requires neither SSH, NAS connectivity, nor the `.nas-ready`
sentinel. The local root is configured globally in `[local]`.

### NAS target

Set `target = "nas"` (or omit `target`) to store snapshots at:

```
<nas.dest_root>/<namespace>/<server>/<timestamp>/
```

NAS snapshots still require SSH access and the `.nas-ready` sentinel directly
under `nas.dest_root`. Without that sentinel, NAS work is skipped. NAS writes
are rate-limited by `global.max_mbps`.

Retention applies independently to snapshots on each target: `prune_days`
removes snapshots older than the configured number of days and `prune_count`
keeps only the configured number of newest snapshots. A value of zero disables
that limit.

## Installation

### From Source

```bash
git clone <repo-url> mc-backup
cd mc-backup
sudo make install
```

This builds the binary, installs it to `/usr/local/bin/mc-backup`, installs the
systemd unit, copies the example config to the user config (or system fallback)
without overwriting an existing file, and enables the service.

### Manual Install

```bash
go build -o mc-backup ./cmd/mc-backup
sudo install -m 755 mc-backup /usr/local/bin/mc-backup
sudo install -d /etc/mc-backup ~/.config/mc-backup
sudo install -m 644 config.example.toml ~/.config/mc-backup/config.toml
sudo install -m 644 mc-backup.service /etc/systemd/system/mc-backup.service
sudo systemctl daemon-reload
sudo systemctl enable mc-backup
```

## Configuration

Config is read from `~/.config/mc-backup/config.toml`, falling back to
`/etc/mc-backup/config.toml`. Auto-provisioned servers are written to the
sidecar `<config path>-auto.toml`; hand-edited config is not modified.
Environment variables use the `MC_BACKUP_` prefix.

### Required Settings

```toml
[global]
backup_interval = "1h"

[local]
dest_root = "/var/lib/mc-backup"

[nas]
ssh_user = "myuser"
ssh_host = "192.168.1.50"
ssh_key = "~/.ssh/id_ed25519"
dest_root = "/volume1/backups"

[[watch]]
path = "/opt/minecraft/servers/docker/servers"
namespace = "minecraft"
```

### Full Reference

```toml
[global]
listen_addr = "127.0.0.1:47990"  # default loopback listener
api_token = ""                 # bearer token for status API mutations (optional on loopback, required on remote interfaces)
backup_interval = "1h"
initial_delay = "2m"
max_mbps = 40.0             # rate limit for NAS rsync

[local]
dest_root = "/var/lib/mc-backup"

[nas]
ssh_user = "backup"
ssh_host = "nas.local"
ssh_port = 22
ssh_key = "~/.ssh/id_ed25519"
dest_root = "/volume1/backups"

[retention]
prune_days = 7
prune_count = 0

[[watch]]
path = "/opt/minecraft/servers/docker/servers"
namespace = "minecraft"
```

The daemon auto-discovers server directories under each watch path. Servers
are disabled by default; enable one and set its RCON password:

```bash
mc-backup config set server.survival.enabled true
mc-backup config set server.survival.rcon_password hunter2
mc-backup config set server.survival.target local
```

Manual server configuration:

```toml
[server.survival]
enabled = true
target = "nas"                         # or "local"
container_name = "survival-mc-1"
rcon_password = "hunter2"
pause_if_no_players = true
```

`target = "local"` uses the local layout and does not contact the NAS.
`target = "nas"` uses the NAS layout and its SSH/sentinel requirements.

## CLI Usage

```bash
mc-backup run
mc-backup status
mc-backup backup [server]
mc-backup scan
mc-backup cancel
mc-backup config get <key>
mc-backup config set <key> <value>
mc-backup version
```

Config keys use dot-separated paths, for example:

```bash
mc-backup config get server.survival.target
mc-backup config set server.survival.target local
mc-backup config set global.backup_interval 2h
```

Changes take effect within seconds through inotify, except `listen_addr`,
which requires a service restart.

### API Security & Authentication

The daemon exposes an HTTP status API on `global.listen_addr`.
- **Read-only endpoints** (`/status`, `/health`) remain unauthenticated for monitoring.
- **Mutating endpoints** (`/backup`, `/scan`, `/cancel`) require an `Authorization: Bearer <token>` header when `global.api_token` is configured.
- **Loopback binding** (`127.0.0.1`, `localhost`, `::1`) is recommended and default. `api_token` is optional when bound to loopback.
- **Remote interface binding** (non-loopback IP/interface) **requires** `global.api_token` to be set; daemon configuration validation will reject non-loopback bindings without a token.
- When `global.api_token` is set, CLI mutating commands (`mc-backup backup`, `mc-backup scan`, `mc-backup cancel`) automatically attach the configured bearer header.

## NAS Setup

Generate and install a key when using NAS targets:

```bash
ssh-keygen -t ed25519 -f ~/.ssh/id_ed25519_backup
ssh-copy-id -i ~/.ssh/id_ed25519_backup backup@nas.local
```

Create the sentinel once, directly in the configured NAS root:

```bash
ssh backup@nas.local "touch /volume1/backups/.nas-ready"
```

The sentinel is required for NAS writes and NAS retention operations. Local
targets do not need it.

## Legacy Backup Directory

Older versions may have snapshots under
`<watch.path>/<server>/backups`. That directory is not part of the current
target layouts and is not automatically migrated. Review its contents, use
the restoration commands below if needed, then manually migrate wanted
snapshots to the selected target or delete the old directory.

## Service Management

```bash
systemctl start mc-backup
systemctl stop mc-backup
systemctl restart mc-backup
systemctl status mc-backup
journalctl -u mc-backup -f
```

Stopping gracefully allows the current cycle to finish and re-enables autosave.

## Restoring a Backup

Snapshots are complete directories. Restore a local snapshot with:

```bash
rsync -a /var/lib/mc-backup/minecraft/survival/20250611-1100/ \
  /opt/minecraft/servers/docker/servers/survival/
```

Restore a NAS snapshot with:

```bash
rsync -a -e ssh backup@nas.local:/volume1/backups/minecraft/survival/20250611-1000/ \
  /opt/minecraft/servers/docker/servers/survival/
```

Restart the Minecraft container after restoring.

## Environment Variable Overrides

Any config value can be set with `MC_BACKUP_` and uppercase, underscore-
separated keys:

```bash
MC_BACKUP_LOCAL_DEST_ROOT=/srv/mc-backups
MC_BACKUP_NAS_SSH_HOST=nas2.local
MC_BACKUP_SERVER_SURVIVAL_TARGET=local
```

Server names are case-insensitive.

## FAQ

**How do I add a server?**  Drop its directory under a watch path. It will be
discovered and written to the auto config sidecar; enable it and set its RCON
password.

**What happens if the NAS is unavailable?**  NAS backups and NAS retention
operations require the sentinel and are skipped or fail before rsync as
appropriate. Local backups continue independently.

**Does rsync re-upload unchanged files?**  No. Snapshots use `--link-dest`
where a previous snapshot exists, so unchanged files are hard-linked on the
destination.

**Can I skip backups when nobody is online?**  Yes. Set
`pause_if_no_players = true`; the daemon checks the player count through RCON.

**Why is the first backup slow?**  The first snapshot has no previous
snapshot for `--link-dest`, so it copies the complete world.

**Does backup stop the server?**  No. It temporarily disables autosave,
flushes the world, copies it, and re-enables autosave while the server remains
running.

**What does `namespace` do?**  It separates snapshots from different watch
paths or host groups under each target's `dest_root`.
