# Binary Artifact Update — Design Spec

**Date:** 2026-06-16
**Goal:** Replace the `update` command's git-clone + `go build` pipeline with a direct HTTP download of a pre-built binary.

---

## Motivation

The current `update` command clones/updates a git repo into `~/.cache/mc-backup/source` then runs `go build` on the production host. This requires both `git` and a Go toolchain on the production machine — heavyweight dependencies for an in-place updater. A pre-built binary artifact is simpler and faster.

## Prerequisites (Out of Scope)

This spec covers the **client side** (the `update` command). The update cannot succeed without a **CI/CD pipeline** that builds the binary and uploads it as a GitHub Release asset named `mc-backup-linux-amd64`. That pipeline is a separate deliverable and will be specified in its own design.

## Design

### Artifact Source

- **Platform:** `https://github.com/<owner>/<repo>/releases/latest/download/mc-backup-linux-amd64`
- URL derived from the existing `-ldflags "-X main.repoURL=<url>"` build injection
- `deriveReleaseURL(repoURL)` strips `.git` suffix, appends `/releases/latest/download/mc-backup-linux-amd64`
- Error if `repoURL` is empty (same guard as today)

### Update Flow

1. Derive `releaseURL` from embedded `repoURL`
2. `http.Get(releaseURL)` — stream response body to `<execPath>.new`
3. Stop service (`systemctl stop mc-backup`)
4. Install binary (`mv <execPath>.new <execPath>`)
5. Start service (`systemctl start mc-backup`)
6. Show status (`systemctl status mc-backup --no-pager`)

### What Is Removed

- `ensureRepo` package-level var (git clone/pull)
- `findRepoRoot` package-level var (never called)
- Git cache directory (`~/.cache/mc-backup/source`)
- `go build` step
- `context` import in main.go (currently only used transitively — verify)

### What Is Preserved

- `repoURL` ldflag — reused for URL derivation
- `runUpdateStep` package-level var — signature unchanged, `dir` param passed as `""`
- Atomic install pattern (build-to-temp, then stop → install → start)
- Error semantics: any step failure stops the flow and returns an error

### Testing

`TestUpdateCmdCachesRepoAndRunsSteps` renamed to `TestUpdateCmdDownloadsAndInstalls`. A new overridable var `downloadFile` (signature: `func(url, dest string) error`) replaces `ensureRepo`. Updated expectations:
1. `"Downloading mc-backup:https://github.com/.../releases/latest/download/mc-backup-linux-amd64 /usr/local/bin/mc-backup.new"`
2. `"Stopping mc-backup service:sudo systemctl stop mc-backup"`
3. `"Installing mc-backup:sudo mv /usr/local/bin/mc-backup.new /usr/local/bin/mc-backup"`
4. `"Starting mc-backup service:sudo systemctl start mc-backup"`
5. `"mc-backup service status:systemctl status mc-backup --no-pager"`

Test seam: `runUpdateStep` captures command steps; `downloadFile` captures the download step. The `deriveReleaseURL` function is not overridden — it's tested implicitly via the URL value passed to `downloadFile`.

## Files Changed

- `cmd/mc-backup/main.go` — rewrite `runUpdate`, add `deriveReleaseURL`, add `downloadFile` var, remove `ensureRepo` and `findRepoRoot`
- `cmd/mc-backup/main_test.go` — update test expectations and overrides
- `AGENTS.md` — update test seam docs (remove `findRepoRoot`)

## Edge Cases

- **Redirect:** `http.Get` follows redirects (`latest` → version-tagged URL). OK.
- **Partial download:** If the download fails mid-stream, the `.new` file is incomplete. The subsequent `mv` will still replace the binary. Mitigation: verify HTTP status before writing, or check `Content-Length` against bytes written. For now, fail on non-200 status and let partial writes get caught by the next run.
- **Permissions:** The downloaded file inherits default umask. `sudo mv` does not change ownership. OK — same as current `go build` output.
