# Dynamic RCON Password Detection Design

**Date:** 2026-08-04  
**Status:** Approved  

## Overview
Currently, `mc-backup` requires `rcon_password` to be explicitly specified in `config.toml` or `-auto.toml` for issuing RCON commands (`save-off`, `save-on`, `save-all flush`, and `list`). Container setups like `itzg/minecraft-server` auto-generate a fresh RCON password in `server.properties` on each container startup when `RCON_PASSWORD` is not explicitly set in environment variables.

This design introduces dynamic detection of RCON passwords directly from the server's `server.properties` file at runtime whenever `rcon_password` is omitted or empty in configuration.

## Requirements & Behavior
1. **Dynamic Runtime Resolution**:
   - When `rcon_password` in `ServerConfig` is configured (non-empty), `mc-backup` uses that explicit password.
   - When `rcon_password` is empty (`""`), `mc-backup` dynamically reads `rcon.password` from `<data_dir>/server.properties` right before issuing RCON commands.
2. **`server.properties` Parsing**:
   - Reads `<data_dir>/server.properties` line by line.
   - Ignores comment lines starting with `#` or `!`.
   - Parses key-value pair `rcon.password=<value>`.
   - Returns the trimmed password value, or `""` if absent or unreadable.
3. **Data Directory Resolution**:
   - Uses `s.DataDir` if explicitly configured.
   - Defaults to `<watch_path>/<server_name>/mc-data` if `s.DataDir` is empty.
4. **Auto-Provisioning**:
   - Newly discovered servers provisioned into `-auto.toml` leave `rcon_password = ""`, allowing dynamic runtime reading of `server.properties` without locking in stale passwords across server restarts.

## Affected Functions & Files
* `internal/engine/rcon.go`:
  - Add `readServerPropertiesPassword(dataDir string) string`.
  - Add `resolveRconPassword(s ServerConfig, w WatchConfig, serverName string) string`.
* `internal/engine/backup.go`:
  - Update `BackupServer` to resolve RCON password via `resolveRconPassword` for `save-off`, `save-on`, and `save-all flush`.
* `internal/engine/daemon.go`:
  - Update player count query in `runBackupCycle` to use `resolveRconPassword`.

## Testing Strategy
* Unit test for `readServerPropertiesPassword` with various `server.properties` content (valid password, empty password, missing key, comments, missing file).
* Unit test for `resolveRconPassword` confirming explicit config overrides `server.properties`, and empty config falls back to `server.properties`.
