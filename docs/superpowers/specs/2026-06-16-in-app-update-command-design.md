# In-App Update Command Design

## Goal

Add `mc-backup update` so users can update the application from the CLI without running `./update.sh` directly.

## Approach

Implement the update flow directly in Go inside `cmd/mc-backup/main.go`. This keeps the command discoverable in the normal CLI, avoids depending on the shell script file being present, and follows the existing command dispatch style.

## Behavior

`mc-backup update` will:

1. Resolve the current git repository root from the process working directory.
2. Change to that repository root.
3. Run `git pull --ff-only`.
4. Run `sudo make install`.
5. Run `sudo systemctl restart mc-backup`.
6. Run `systemctl status mc-backup --no-pager`.

Each command will stream stdout and stderr directly to the terminal so users can see progress and failures.

## Error Handling

If the current directory is not inside a git repository, the command exits with a clear error. If any update step fails, the command stops immediately, prints which step failed, and exits non-zero.

The command will not run destructive git commands, resolve merge conflicts, or attempt non-fast-forward pulls.

## CLI Help

The usage output will include:

```text
update     Pull latest source, install, and restart service
```

## Testing

Verification will include `go test ./...`. The full update flow will not be executed during tests because it pulls, installs, and restarts the local system service.
