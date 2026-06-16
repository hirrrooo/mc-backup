# CI Release Workflow Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** GitHub Actions workflow that builds the `mc-backup` binary on tag push and publishes it as a GitHub Release asset.

**Architecture:** A single `.github/workflows/release.yml` triggered by `v*` tags. One job: test → build → release. The `softprops/action-gh-release@v2` action handles release creation and asset upload. A one-line code change in `main.go` makes `version` overridable via ldflags.

**Tech Stack:** Go 1.21, GitHub Actions, `softprops/action-gh-release@v2`

---

### Task 1: Make version overridable via ldflags

**Files:**
- Modify: `cmd/mc-backup/main.go:18`

- [ ] **Step 1: Change `const version` to `var version`**

In `cmd/mc-backup/main.go`, line 18:

```go
// Before:
const version = "0.1.0"

// After:
var version = "0.1.0"
```

- [ ] **Step 2: Verify tests still pass**

Run: `go test ./...`
Expected: all tests pass (no behavioral change — `var` vs `const` is invisible to existing tests)

- [ ] **Step 3: Verify `mc-backup version` still reports "0.1.0" when built without ldflags**

Run: `go build -o /tmp/mc-backup-test ./cmd/mc-backup && /tmp/mc-backup-test version 2>&1`
Expected: output contains `0.1.0`

- [ ] **Step 4: Verify ldflags override works**

Run: `go build -ldflags "-X main.version=v9.9.9" -o /tmp/mc-backup-test-ldflags ./cmd/mc-backup && /tmp/mc-backup-test-ldflags version 2>&1`
Expected: output contains `v9.9.9`, not `0.1.0`

- [ ] **Step 5: Commit**

```bash
git add cmd/mc-backup/main.go
git commit -m "fix: change version from const to var for ldflags override"
```

---

### Task 2: Create GitHub Actions release workflow

**Files:**
- Create: `.github/workflows/release.yml`

- [ ] **Step 1: Create `.github/workflows/release.yml`**

```yaml
name: Release

on:
  push:
    tags:
      - "v*"

permissions:
  contents: write

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
          go build \
            -ldflags "-X main.repoURL=https://github.com/hirrrooo/mc-backup.git -X main.version=${{ github.ref_name }}" \
            -o mc-backup-linux-amd64 \
            ./cmd/mc-backup

      - name: Release
        uses: softprops/action-gh-release@v2
        with:
          files: mc-backup-linux-amd64
```

- [ ] **Step 2: Verify workflow syntax (dry run)**

Run: `cat .github/workflows/release.yml` (visual review — GitHub validates on push)

- [ ] **Step 3: Commit**

```bash
git add .github/workflows/release.yml
git commit -m "feat: add GitHub Actions release workflow on tag push"
```
