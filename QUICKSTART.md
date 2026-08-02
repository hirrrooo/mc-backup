# Quick Start Manual for mc-backup

`mc-backup` is an automated, zero-downtime Minecraft server backup daemon. It connects to Minecraft containers via Docker and RCON, temporarily disables autosave, flushes world data, and uses `rsync` to create timestamped snapshots locally or on a remote NAS.

This guide covers getting `mc-backup` running quickly with step-by-step examples for both **Local** and **NAS** backup targets.

---

## Important: Configuration Location & Systemd

The `mc-backup` systemd service runs as the **`root`** user (`User=root`).

When the service starts, `mc-backup` searches for its configuration in the following order:
1. `~/.config/mc-backup/config.toml` (resolves to `/root/.config/mc-backup/config.toml` for systemd)
2. `/etc/mc-backup/config.toml` (system-wide fallback)

> **Important**: When running `mc-backup` as a systemctl service, put your configuration in **/etc/mc-backup/config.toml**. Editing `/home/<user>/.config/mc-backup/config.toml` will **not** affect the systemd daemon running as `root`.

---

## 1. Installation

### Option A: Build and Install from Source (Recommended)

```bash
git clone https://github.com/hirrrooo/mc-backup.git
cd mc-backup
sudo make install
```

This command:
- Builds the binary to `./mc-backup`
- Installs the executable to `/usr/local/bin/mc-backup`
- Copies default configuration to `/etc/mc-backup/config.toml` (without overwriting an existing config)
- Installs and enables the systemd service unit (`mc-backup.service`)

### Option B: Manual Binary Installation

```bash
go build -o mc-backup ./cmd/mc-backup
sudo install -m 755 mc-backup /usr/local/bin/mc-backup
sudo install -d /etc/mc-backup
sudo install -m 644 config.example.toml /etc/mc-backup/config.toml
sudo install -m 644 mc-backup.service /etc/systemd/system/mc-backup.service
sudo systemctl daemon-reload
sudo systemctl enable mc-backup
```

---

## 2. Core Concepts & Auto-Discovery

1. **Watch Paths**: `mc-backup` scans directories configured under `[[watch]]` blocks (e.g. `/opt/minecraft/servers/docker/servers`).
2. **Auto-Discovery**: Server subdirectories found under watch paths are automatically detected and saved to `/etc/mc-backup/config-auto.toml` with `enabled = false`.
3. **Enabling Servers**: Discovered servers require `enabled = true` and an RCON password before backups occur.
4. **Live Reload**: Editing `/etc/mc-backup/config.toml` or running `mc-backup config set ...` updates the running daemon dynamically without requiring a service restart (except for `listen_addr` changes).

---

## 3. Example 1: Local Backup Setup

Local backups store complete, timestamped snapshots on the host's filesystem (e.g. at `/var/lib/mc-backup`). Local target backups do **not** require SSH keys or NAS sentinel files.

### Step 1: Configure `/etc/mc-backup/config.toml`

Edit `/etc/mc-backup/config.toml` to configure local storage:

```toml
[global]
listen_addr = "127.0.0.1:47990"
backup_interval = "1h"
initial_delay = "1m"
excludes = ["*.jar", "cache", "logs", "*.tmp"]

[local]
dest_root = "/var/lib/mc-backup"

[retention]
prune_days = 7
prune_count = 0

[[watch]]
path = "/opt/minecraft/servers/docker/servers"
namespace = "minecraft"
```

### Step 2: Create Destination Directory & Start Daemon

```bash
sudo mkdir -p /var/lib/mc-backup
sudo systemctl start mc-backup
```

### Step 3: Enable the Minecraft Server

Assume your server directory is at `/opt/minecraft/servers/docker/servers/survival`. Configure it via CLI:

```bash
# Set target to local first
sudo mc-backup config set server.survival.target local

# Set RCON password (matching RCON_PASSWORD in your Docker container)
sudo mc-backup config set server.survival.rcon_password "your_rcon_password"

# Optional: explicitly specify container name if different from directory name
sudo mc-backup config set server.survival.container_name survival-mc-1

# Enable server
sudo mc-backup config set server.survival.enabled true
```

### Step 4: Verify Status and Test Backup

```bash
# Check daemon status
sudo mc-backup status

# Trigger an immediate manual backup
sudo mc-backup backup survival
```

Snapshot directory layout created by the backup (formatted as `YYYYMMDD-HHMM`):
```
/var/lib/mc-backup/minecraft/survival/<timestamp>/
└── mc-data/
    ├── world/
    ├── server.properties
    └── ...
```
### Step 5: How to Restore a Local Snapshot

To restore a local snapshot:

```bash
# 1. Stop the Minecraft container
docker stop survival-mc-1

# 2. Restore world data using rsync
sudo rsync -av /var/lib/mc-backup/minecraft/survival/<timestamp>/ /opt/minecraft/servers/docker/servers/survival/

# 3. Restart the Minecraft container
docker start survival-mc-1
```

> **Note**: Each snapshot contains the `mc-data/` folder. Syncing `<timestamp>/` into the server directory restores `mc-data/` while leaving root server files (such as `docker-compose.yml` or `.env`) untouched. You can also target `mc-data/` directly with `sudo rsync -av /var/lib/mc-backup/minecraft/survival/<timestamp>/mc-data/ /opt/minecraft/servers/docker/servers/survival/mc-data/`.
---

## 4. Example 2: Remote NAS Backup Setup

NAS backups transfer snapshots over SSH/rsync to a remote NAS server.

### Step 1: Set Up Passwordless SSH for Root

Since the systemd daemon runs as `root`, generate an SSH key for `root` and copy it to the NAS user (`backup@nas.local`):

```bash
# Ensure root SSH directory exists with restricted permissions
sudo install -d -m 700 /root/.ssh

# Generate SSH key for root
sudo ssh-keygen -t ed25519 -f /root/.ssh/id_ed25519_mcbackup -N ""

# Copy SSH public key to NAS
sudo ssh-copy-id -i /root/.ssh/id_ed25519_mcbackup.pub backup@nas.local

# Test SSH connection as root
sudo ssh -i /root/.ssh/id_ed25519_mcbackup backup@nas.local "echo SSH connection successful"

### Step 2: Create the NAS Sentinel File (`.nas-ready`)

`mc-backup` requires a `.nas-ready` sentinel file inside `nas.dest_root` on the remote NAS. This safety feature prevents `mc-backup` from writing to an unmounted directory or offline storage.

```bash
sudo ssh -i /root/.ssh/id_ed25519_mcbackup backup@nas.local "mkdir -p /volume1/backups && touch /volume1/backups/.nas-ready"
```

### Step 3: Configure `/etc/mc-backup/config.toml` for NAS

Edit `/etc/mc-backup/config.toml`:

```toml
[global]
listen_addr = "127.0.0.1:47990"
backup_interval = "1h"
initial_delay = "1m"
max_mbps = 40.0
excludes = ["*.jar", "cache", "logs", "*.tmp"]

[nas]
ssh_user = "backup"
ssh_host = "nas.local"
ssh_port = 22
ssh_key = "/root/.ssh/id_ed25519_mcbackup"
dest_root = "/volume1/backups"

[retention]
prune_days = 14
prune_count = 10

[[watch]]
path = "/opt/minecraft/servers/docker/servers"
namespace = "minecraft"
```

### Step 4: Configure Server Target to NAS and Test

```bash
# Set server target to NAS and RCON password first
sudo mc-backup config set server.survival.target nas
sudo mc-backup config set server.survival.rcon_password "your_rcon_password"

# Enable server
sudo mc-backup config set server.survival.enabled true
# Restart service if listen_addr changed, or check status
sudo systemctl restart mc-backup

# Trigger manual backup test
sudo mc-backup backup survival
```
NAS Snapshot layout on the remote storage (formatted as `YYYYMMDD-HHMM`):
```
/volume1/backups/minecraft/survival/<timestamp>/
└── mc-data/
    ├── world/
    ├── server.properties
    └── ...
```
### Step 5: How to Restore a NAS Snapshot

To restore a snapshot stored on the remote NAS:

```bash
# 1. Stop the Minecraft container
docker stop survival-mc-1

# 2. Restore world data over SSH using rsync
sudo rsync -av -e "ssh -i /root/.ssh/id_ed25519_mcbackup" backup@nas.local:/volume1/backups/minecraft/survival/<timestamp>/ /opt/minecraft/servers/docker/servers/survival/

# 3. Restart the Minecraft container
docker start survival-mc-1
```

> **Note**: Each snapshot contains the `mc-data/` folder. Syncing `<timestamp>/` into the server directory restores `mc-data/` while leaving root server files (such as `docker-compose.yml` or `.env`) untouched. You can also target `mc-data/` directly with `sudo rsync -av -e "ssh -i /root/.ssh/id_ed25519_mcbackup" backup@nas.local:/volume1/backups/minecraft/survival/<timestamp>/mc-data/ /opt/minecraft/servers/docker/servers/survival/mc-data/`.
## 5. Service Management & Commands

### Managing the Systemd Service

```bash
# Start service
sudo systemctl start mc-backup

# Stop service
sudo systemctl stop mc-backup

# Restart service
sudo systemctl restart mc-backup

# Check systemd service status
sudo systemctl status mc-backup

# View live daemon logs
sudo journalctl -u mc-backup -f
```

### CLI Command Reference

All CLI commands communicate directly with the daemon:

- `mc-backup status` — Display daemon status, server states, and recent backup history.
- `mc-backup backup [server]` — Trigger an immediate backup cycle (all enabled servers or specific server).
- `mc-backup backup --offline [server]` — Trigger backup without RCON (useful for stopped containers).
- `mc-backup scan` — Force an immediate scan of watched directories for new servers.
- `mc-backup cancel` — Cancel the currently running backup cycle.
- `mc-backup config get <key>` — Get configuration value (e.g., `mc-backup config get global.backup_interval`).
- `mc-backup config set <key> <value>` — Update configuration key dynamically.
- `mc-backup update` — Download and install the latest release binary from GitHub.
- `mc-backup version` — Print version information.
