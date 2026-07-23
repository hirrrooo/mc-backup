# Update From Anywhere Design

## Goal

`mc-backup update` works from any directory by dynamically cloning/building from a cached source repo instead of requiring the user to be inside a git checkout.

## Behavior

1. Clone or update source into `~/.cache/mc-backup/source`.
2. `go build` the binary from there.
3. Replace the running binary at `os.Executable()`.
4. `sudo systemctl restart mc-backup`.
5. Show `systemctl status mc-backup --no-pager`.

## Repo URL

Embedded at build time via `-ldflags "-X main.repoURL=https://github.com/..."`. Falls back to the current `update.sh`-style git pull if the binary wasn't built with ldflags (e.g. `go run`).

## Error Handling

- If Go is not installed, exit with a clear error.
- If clone/build fails, exit with the underlying error.
- If binary replacement fails (permissions), exit with a clear error.

## Testing

Tests swap `findRepoRoot` and `runUpdateStep` to verify step ordering without real network/filesystem calls. Old tests continue to pass.
