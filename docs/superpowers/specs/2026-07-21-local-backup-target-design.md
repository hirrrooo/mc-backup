# Local Backup Target Design

## Summary

Add an explicit per-server backup target so a backup can be written directly to
the local machine instead of through SSH to the NAS. Both targets continue to
use rsync snapshots and the existing RCON safety sequence. The target is a
server routing decision; it is not a fallback between local storage and NAS.

## Architecture and configuration

Add a `[local]` configuration section:

```toml
[local]
dest_root = "/var/lib/mc-backup"
```

Replace `server.<name>.ssh_only` with:

```toml
[server.survival]
target = "local" # or "nas"
```

`target` accepts only `local` and `nas`. An omitted target resolves to `nas`,
so the default is deterministic and does not depend on disk pressure. A local
target must have a non-empty `[local].dest_root`. Invalid targets and missing
local destinations are configuration/runtime errors reported with the server
name and target; validation must happen before any SSH command is attempted.

The generated configuration, example configuration, config setters, and
documentation must expose `target` and `[local].dest_root`, and must no longer
generate or document `ssh_only`.

The existing global `retention.prune_days` and `retention.prune_count` values
apply to local-target snapshots in their local target directory. NAS retention
continues to use its existing NAS pruning behavior. `watch.local_keep` remains
the SSD/archive-engine setting for the existing local working backup area; it
does not control local-target retention.

## Paths and target-specific behavior

For a local target, the snapshot path is:

```
<local.dest_root>/<watch.namespace>/<server>/<timestamp>/
```

The implementation creates the destination directory locally and invokes the
existing local rsync form. It must not invoke SSH, check `.nas-ready`, apply a
NAS bandwidth limit, run archive migration, or run NAS pruning. Local pruning
enumerates timestamp-shaped directories below the local target server path and
applies `prune_days` and `prune_count` without affecting NAS or working-copy
backups.

For a NAS target, preserve the current direct-to-NAS path and behavior:

```
<nas.dest_root>/<watch.namespace>/<server>/<timestamp>/
```

The daemon checks `<nas.dest_root>/.nas-ready`, creates the remote parent over
SSH, uses the NAS rsync arguments (including the global bandwidth limit), and
uses the existing NAS retention commands. No local target directory is
created for this route.

Disk-pressure routing and archive migration are not part of target selection.
An explicit `target = "nas"` always uses NAS, and `target = "local"` always
uses the local target.

## Runtime flow

1. Resolve and validate the server target and its required destination before
   starting target-specific work.
2. Resolve the world data directory and snapshot timestamp.
3. For an online backup, issue `save-off`, then `save-all flush`; register a
   deferred `save-on` immediately after `save-off` succeeds so it runs on
   success, rsync failure, cancellation, or panic. Offline backups retain their
   existing behavior of skipping RCON.
4. Run the existing global `sync` before rsync.
5. Select the target-specific destination, previous snapshot, directory
   creation, and rsync arguments.
6. Run rsync and report the resulting snapshot path.
7. Apply retention for the selected target only.

The target branch must be selected before any NAS readiness or SSH helper is
called. The local branch uses local filesystem operations and local rsync
arguments, including `--timeout=300` and `--link-dest` when history exists.
The NAS branch retains SSH rsync arguments, NAS timeout/bandwidth behavior, and
NAS serialization/readiness rules.

## Independent link-dest history

Previous-snapshot tracking is maintained independently for each combination of
watch, server, and target. A successful local snapshot can therefore be the
link-dest source only for a later local snapshot; a NAS snapshot can be the
source only for a later NAS snapshot. Never pass a local path to NAS rsync or a
NAS path to local rsync, and do not let a failed rsync advance either history.

Discovery/status bookkeeping may still show the latest snapshot for each
available location, but backup argument construction must use the selected
target's history exclusively. The first snapshot for a target has no
`--link-dest` argument.

## Errors and safety

- Unknown target: fail clearly, naming the server and invalid value; do not
  call `checkNASReady`, `ensureNASDir`, `runSSH`, or rsync.
- Local target with empty `local.dest_root`: fail clearly before SSH or rsync.
- Local directory creation or local rsync failure: return a local-specific
  error and leave NAS state untouched.
- NAS readiness, remote directory, or NAS rsync failure: retain current NAS
  errors and do not create a local-target snapshot.
- RCON/save-on failures retain current error precedence and logging. In
  particular, a deferred save-on failure must still be surfaced and must warn
  that autosave may remain disabled.

Destination roots should be normalized consistently with the existing NAS root
(trailing separators removed) while preserving absolute and relative local
paths as configured. Timestamp directory validation remains the existing
`YYYYMMDD-HHMM` rule.

## Migration compatibility and documentation

Configs without `target` load as `target = "nas"`. This is an intentional
compatibility default for the new schema: operators who want local snapshots
must add `target = "local"` and configure `[local].dest_root`. The obsolete
`ssh_only` field is not emitted or documented; if it remains in an older file,
it must not override the explicit target resolution. Existing NAS settings and
NAS snapshot layout remain unchanged.

Update the example TOML, README configuration/reference text, CLI config help,
and any generated auto-provisioned server output to describe `target` rather
than `ssh_only`. Include local restore/path examples and state that local
targets do not require NAS connectivity or `.nas-ready`.

## Test plan

### Configuration

- Parse `[local].dest_root` and `target = "local"`.
- Resolve an omitted target to `nas`.
- Reject an invalid target and a local target without `local.dest_root` with
  actionable errors.
- Verify generated/example/config-set output contains `target` and local
  configuration and omits `ssh_only`.

### Paths and rsync arguments

- Build the exact local path from `dest_root`, namespace, server, and timestamp.
- Verify local rsync uses a local destination, timeout, excludes, and local
  `--link-dest`, but no SSH or NAS bandwidth arguments.
- Verify NAS rsync retains SSH destination, NAS bandwidth, and NAS
  `--link-dest` behavior.
- Verify the first snapshot for either target omits `--link-dest`.

### Isolation and runtime safety

- Use the command-runner seam to prove local backups invoke no `ssh` command,
  readiness check, remote mkdir, or NAS prune command.
- Prove NAS backups retain readiness and SSH behavior and do not create local
  target directories.
- Exercise save-off/flush/sync/rsync/deferred save-on for both targets,
  including rsync failure.
- Prove invalid configuration fails before any SSH attempt.

### History and retention

- Verify local and NAS successful snapshots maintain separate link-dest
  histories and failures do not advance history.
- Verify local `prune_days` and `prune_count` remove only local-target
  snapshots.
- Verify NAS retention remains remote and does not prune local-target
  snapshots.
