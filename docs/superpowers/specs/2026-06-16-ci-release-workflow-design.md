# CI Release Workflow — Design Spec

**Date:** 2026-06-16
**Goal:** GitHub Actions workflow that builds the `mc-backup` binary and publishes it as a GitHub Release asset, enabling the binary artifact update system.

---

## Prerequisites

- Repository: `https://github.com/hirrrooo/mc-backup.git`
- Binary artifact update client spec: `docs/superpowers/specs/2026-06-16-binary-artifact-update-design.md`

## Trigger

- **Event:** `push` of tags matching `v*` (e.g. `v1.0.0`, `v0.2.1`)
- Non-tag pushes do not trigger a release

## Workflow Structure

**File:** `.github/workflows/release.yml`

**Single job** `release` on `ubuntu-latest`:

1. `actions/checkout@v4` — clone repo
2. `actions/setup-go@v5` — install Go (reads version from `go.mod`)
3. `go test ./...` — gate: fail fast if any test fails
4. `go build` — compile `linux/amd64` binary with ldflags:
   - `-X main.repoURL=https://github.com/hirrrooo/mc-backup.git`
   - `-X main.version=${{ github.ref_name }}`
   - Output: `mc-backup-linux-amd64`
5. `softprops/action-gh-release@v2` — create release from tag, upload artifact

**Permissions:** `contents: write`

**Artifact name:** `mc-backup-linux-amd64` — matches the download URL pattern specified in the binary artifact update design (`/releases/latest/download/mc-backup-linux-amd64`).

## Code Change Required

`cmd/mc-backup/main.go` line 18:

```
-const version = "0.1.0"
+var version = "0.1.0"
```

ldflags can only set `var` values, not `const`. The default `"0.1.0"` remains as a fallback for manual local builds. CI builds override it with the tag name.

## Edge Cases

- **Broken tests:** Tests run before build — no release is created for failing code
- **Release creation failure:** The binary still exists as a workflow artifact for manual recovery
- **Re-running a tag:** `softprops/action-gh-release@v2` is idempotent by tag; re-running won't duplicate the release
- **URL redirect:** `https://github.com/hirrrooo/mc-backup/releases/latest/download/mc-backup-linux-amd64` is a permanent redirect to the latest release's asset. `http.Get` follows it per the update client spec.

## Files Changed

- `.github/workflows/release.yml` — new file
- `cmd/mc-backup/main.go` — `const version` → `var version`
