# Update Script Design

## Goal

Create a root-level update helper for end users that pulls the latest `mc-backup` source, installs it, and restarts the `mc-backup` systemd service.

## User-Facing Command

The script will live at `./update.sh` so users can run it from the repository root:

```bash
./update.sh
```

## Approach

Use a standalone shell script instead of a Makefile target or systemd timer. This keeps the update flow easy to discover, easy to run manually, and suitable for end users who do not need to know the individual `git`, `make`, and `systemctl` commands.

## Behavior

The script will:

1. Run with strict shell settings so failures stop the update.
2. Resolve the repository root from the script location, then change to that directory.
3. Verify the directory is a git repository.
4. Pull latest changes with `git pull --ff-only` to avoid accidental merge commits.
5. Install with `sudo make install`.
6. Restart the service with `sudo systemctl restart mc-backup`.
7. Show service status with `systemctl status mc-backup --no-pager`.

## Error Handling

Any failed command will stop the script and return a non-zero exit code. The script will print short step labels before each major action so users can see where a failure happened.

The script will not run destructive git commands, overwrite local changes, or attempt to resolve non-fast-forward pulls automatically.

## Testing

Verification will include:

1. Shell syntax check with `bash -n update.sh`.
2. Manual inspection of the script for safe command ordering and root-relative execution.
