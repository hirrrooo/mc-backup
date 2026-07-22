# Rolling GitHub Release Design

## Goal

Make `mc-backup update` install the newest validated `main` commit by publishing the expected binary and checksum at GitHub's stable latest-release download URLs.

## Scope

The GitHub Actions release workflow runs on each push to `main`. It runs the Go test suite, builds a statically linked Linux amd64 binary, generates its SHA-256 sidecar, and replaces the assets of one rolling pre-release.

The rolling release uses the `latest` tag and is marked as a prerelease. Its title identifies it as the latest `main` build. Immutable versioned releases are not part of this change.

## Build Contract

The workflow produces these release assets:

- `mc-backup-linux-amd64`
- `mc-backup-linux-amd64.sha256`

The binary is built with `GOOS=linux`, `GOARCH=amd64`, and `CGO_ENABLED=0`. Linker flags embed the repository URL and the short commit SHA as the version.

`mc-backup update` continues to use:

`https://github.com/hirrrooo/mc-backup/releases/latest/download/mc-backup-linux-amd64`

The existing checksum verification continues to fetch the matching `.sha256` asset before installation.

## Workflow Behavior

```text
push to main
    |
    v
go test ./...
    |
    v
build linux/amd64 binary and checksum
    |
    v
upsert prerelease tagged latest
    |
    v
mc-backup update downloads latest assets
```

The release upload executes only after tests and the build succeed. A failed workflow does not modify the rolling release, so clients retain access to the preceding valid assets.

## Validation

- Run `go test ./...` locally.
- Validate the workflow's YAML structure, triggers, build environment, linker flags, release tag, prerelease status, and uploaded asset names.
- After the workflow is deployed and a `main` push completes, verify the GitHub release exposes both assets and that the existing `mc-backup update` checksum path resolves.
