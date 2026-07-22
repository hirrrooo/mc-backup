# Rolling GitHub Release Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Publish a checksum-verified Linux amd64 binary to a normal rolling GitHub `latest` release after every successful push to `main`.

**Architecture:** Replace the tag-only release workflow with a `main` workflow that tests, statically cross-compiles, and checksums the binary. It force-moves the `latest` tag to the triggering commit, then upserts the normal GitHub release and overwrites its two assets, preserving the CLI's existing `releases/latest/download/...` URLs.

**Tech Stack:** GitHub Actions, Go, `softprops/action-gh-release@v2`, GitHub Releases.

---

### Task 1: Publish the Rolling Release

**Files:**
- Modify: `.github/workflows/release.yml`
- Test: `cmd/mc-backup/main_test.go`

- [ ] **Step 1: Confirm the updater's required release asset contract**

Run:

```bash
go test ./cmd/mc-backup -run 'TestUpdateCmdDownloadsAndInstalls|TestVerifyChecksumSuccess' -count=1
```

Expected: `PASS`; the tests require `mc-backup-linux-amd64` and `mc-backup-linux-amd64.sha256` from the `releases/latest/download` URL.

- [ ] **Step 2: Replace the workflow with the main-push rolling-release workflow**

Replace `.github/workflows/release.yml` with:

```yaml
name: Release

on:
  push:
    branches:
      - main

permissions:
  contents: write

concurrency:
  group: rolling-release
  cancel-in-progress: false

jobs:
  release:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod

      - name: Test
        run: go test ./...

      - name: Build
        run: |
          version="${GITHUB_SHA::7}"
          GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build \
            -ldflags "-X main.repoURL=https://github.com/hirrrooo/mc-backup.git -X main.version=${version}" \
            -o mc-backup-linux-amd64 \
            ./cmd/mc-backup

      - name: Generate checksum
        run: sha256sum mc-backup-linux-amd64 > mc-backup-linux-amd64.sha256

      - name: Move rolling release tag
        run: git push --force origin "${GITHUB_SHA}:refs/tags/latest"

      - name: Publish rolling release
        uses: softprops/action-gh-release@v2
        with:
          tag_name: latest
          name: Latest main build
          body: Automated build from ${{ github.sha }}.
          prerelease: false
          make_latest: true
          fail_on_unmatched_files: true
          files: |
            mc-backup-linux-amd64
            mc-backup-linux-amd64.sha256
```

- [ ] **Step 3: Verify the workflow's static release contract**

Run:

```bash
go test ./...
```

Expected: `PASS` for all packages. The workflow itself runs the same suite before it moves the `latest` tag or updates release assets.

- [ ] **Step 4: Inspect the committed workflow**

Run:

```bash
git diff --check
git diff -- .github/workflows/release.yml
```

Expected: no whitespace errors; the diff shows only the main-push trigger, static Linux build, checksum generation, forced `latest` tag update, and normal release asset upload.

- [ ] **Step 5: Commit the workflow**

```bash
git add .github/workflows/release.yml
git commit -m "ci(release): publish rolling main build"
```

### Task 2: Validate the Published Release

**Files:**
- Modify: none
- Test: GitHub Actions `Release` workflow run

- [ ] **Step 1: Push the committed main branch**

Run:

```bash
git push origin main
```

Expected: the `Release` workflow starts for the pushed commit.

- [ ] **Step 2: Verify the workflow completes successfully**

Run:

```bash
gh run list --repo hirrrooo/mc-backup --workflow release.yml --limit 1
```

Expected: the newest run has `success` status.

- [ ] **Step 3: Verify release metadata and assets**

Run:

```bash
gh release view latest --repo hirrrooo/mc-backup --json tagName,isPrerelease,isLatest,assets
```

Expected: `tagName` is `latest`, `isPrerelease` is `false`, `isLatest` is `true`, and assets include `mc-backup-linux-amd64` plus `mc-backup-linux-amd64.sha256`.

- [ ] **Step 4: Verify the stable updater URL**

Run:

```bash
curl -fL -o /dev/null https://github.com/hirrrooo/mc-backup/releases/latest/download/mc-backup-linux-amd64
curl -fL -o /dev/null https://github.com/hirrrooo/mc-backup/releases/latest/download/mc-backup-linux-amd64.sha256
```

Expected: both commands exit successfully with HTTP 200 responses.
