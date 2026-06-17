# Binary Artifact Update Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking. Task 1 and Task 2 are independent (different files); they may be dispatched in parallel.

**Goal:** Replace the `update` command's git-clone + `go build` pipeline with a direct HTTP download of a pre-built binary from GitHub Releases.

**Architecture:** `runUpdate` derives the download URL from the embedded `repoURL` ldflag, downloads the `mc-backup-linux-amd64` asset to a temp path, then executes the existing stop → install → start → status steps. No git or Go toolchain required on the production host.

**Tech Stack:** Go standard library (`net/http`, `os`, `os/exec`, `strings`), GitHub Releases API (no auth needed for public repos).

---

## File Structure

- Modify: `cmd/mc-backup/main.go` — remove `ensureRepo`, `findRepoRoot`, `osUserHomeDir`; add `deriveReleaseURL`, `downloadFile`; rewrite `runUpdate`
- Modify: `cmd/mc-backup/main_test.go` — rewrite `TestUpdateCmdCachesRepoAndRunsSteps` → `TestUpdateCmdDownloadsAndInstalls`; remove `TestUpdateCmdEnsureRepoHomeDirError`; update `TestUpdateCmdFallbackNoRepoURL`
- Modify: `AGENTS.md` — update test seam docs

---

### Task 1: Rewrite runUpdate to download binary artifact

**Files:**
- Modify: `cmd/mc-backup/main.go`
- Modify: `cmd/mc-backup/main_test.go`

- [ ] **Step 1: Update test expectations and rename test (red)**

Replace the body of `TestUpdateCmdCachesRepoAndRunsSteps` in `cmd/mc-backup/main_test.go` with the new test. Also rename it to `TestUpdateCmdDownloadsAndInstalls`:

```go
func TestUpdateCmdDownloadsAndInstalls(t *testing.T) {
	var calls []string
	oldRepoURL := repoURL
	oldDownloadFile := downloadFile
	oldRunUpdateStep := runUpdateStep
	oldOsExecutable := osExecutable
	t.Cleanup(func() {
		repoURL = oldRepoURL
		downloadFile = oldDownloadFile
		runUpdateStep = oldRunUpdateStep
		osExecutable = oldOsExecutable
	})

	repoURL = "https://github.com/hirrrooo/mc-backup.git"
	osExecutable = func() (string, error) { return "/usr/local/bin/mc-backup", nil }

	downloadFile = func(url, dest string) error {
		calls = append(calls, "download:"+url+" "+dest)
		return nil
	}
	runUpdateStep = func(dir, name string, command string, args ...string) error {
		calls = append(calls, name+":"+command+" "+strings.Join(args, " "))
		return nil
	}

	if err := runUpdate(); err != nil {
		t.Fatalf("runUpdate() error = %v", err)
	}

	want := []string{
		"download:https://github.com/hirrrooo/mc-backup/releases/latest/download/mc-backup-linux-amd64 /usr/local/bin/mc-backup.new",
		"Stopping mc-backup service:sudo systemctl stop mc-backup",
		"Installing mc-backup:sudo mv /usr/local/bin/mc-backup.new /usr/local/bin/mc-backup",
		"Starting mc-backup service:sudo systemctl start mc-backup",
		"mc-backup service status:systemctl status mc-backup --no-pager",
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls =\n%#v\nwant =\n%#v", calls, want)
	}
}
```

Also update `TestUpdateCmdFallbackNoRepoURL`: remove the `osUserHomeDir` override/save/restore from it (it no longer needs to mock the home dir):

```go
func TestUpdateCmdFallbackNoRepoURL(t *testing.T) {
	oldRepoURL := repoURL
	t.Cleanup(func() {
		repoURL = oldRepoURL
	})

	repoURL = ""

	err := runUpdate()
	if err == nil {
		t.Fatal("expected error when repoURL is empty")
	}
	if !strings.Contains(err.Error(), "embedded repo URL") {
		t.Fatalf("expected embedded repo URL error, got: %v", err)
	}
}
```

Delete the entire `TestUpdateCmdEnsureRepoHomeDirError` function (it tests the home-dir error path, which no longer exists).

Also remove `"errors"` from the test file imports (it was only used by `TestUpdateCmdEnsureRepoHomeDirError`).

- [ ] **Step 2: Run tests to verify failure**

Run: `go test ./cmd/mc-backup -run 'TestUpdateCmd' -v`

Expected: FAIL with compilation errors (`ensureRepo` still referenced in main.go, `downloadFile`/`deriveReleaseURL` not defined).

- [ ] **Step 3: Add `deriveReleaseURL`, `downloadFile`; remove `ensureRepo`/`findRepoRoot`/`osUserHomeDir`; rewrite `runUpdate`**

In `cmd/mc-backup/main.go`, remove the three package-level vars `ensureRepo` (lines 32-51), `findRepoRoot` (lines 53-61), and `osUserHomeDir` (line 28).

Remove unused imports: `"errors"`, `"path/filepath"`. Add `"io"` if not already present.

Add `deriveReleaseURL` and `downloadFile` near the remaining package-level vars:

```go
var repoURL = "" // set via -ldflags

var osExecutable = os.Executable

var downloadFile = func(url, dest string) error {
	resp, err := http.Get(url)
	if err != nil {
		return fmt.Errorf("download %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: HTTP %d", url, resp.StatusCode)
	}
	f, err := os.Create(dest)
	if err != nil {
		return fmt.Errorf("create %s: %w", dest, err)
	}
	defer f.Close()
	if _, err := io.Copy(f, resp.Body); err != nil {
		return fmt.Errorf("write %s: %w", dest, err)
	}
	return f.Close()
}

func deriveReleaseURL(repoURL string) string {
	base := strings.TrimSuffix(repoURL, ".git")
	return base + "/releases/latest/download/mc-backup-linux-amd64"
}
```

Replace the entire `runUpdate` function with:

```go
func runUpdate() error {
	if repoURL == "" {
		return fmt.Errorf("update requires a built binary with embedded repo URL; use ./update.sh from the source repo instead")
	}

	execPath, err := osExecutable()
	if err != nil {
		return fmt.Errorf("cannot determine binary path: %w", err)
	}
	tmpBin := execPath + ".new"

	releaseURL := deriveReleaseURL(repoURL)
	fmt.Printf("Downloading %s\n", releaseURL)
	if err := downloadFile(releaseURL, tmpBin); err != nil {
		return fmt.Errorf("download: %w", err)
	}

	steps := []struct {
		name    string
		command string
		args    []string
	}{
		{"Stopping mc-backup service", "sudo", []string{"systemctl", "stop", "mc-backup"}},
		{"Installing mc-backup", "sudo", []string{"mv", tmpBin, execPath}},
		{"Starting mc-backup service", "sudo", []string{"systemctl", "start", "mc-backup"}},
		{"mc-backup service status", "systemctl", []string{"status", "mc-backup", "--no-pager"}},
	}

	for _, step := range steps {
		if err := runUpdateStep("", step.name, step.command, step.args...); err != nil {
			return fmt.Errorf("%s: %w", step.name, err)
		}
	}

	return nil
}
```

- [ ] **Step 4: Run the update tests to verify pass**

Run: `go test ./cmd/mc-backup -run 'TestUpdateCmd' -v`

Expected: PASS.

- [ ] **Step 5: Run the full suite**

Run: `go test ./...`

Expected: PASS.

- [ ] **Step 6: Run go vet**

Run: `go vet ./...`

Expected: clean (no output).

- [ ] **Step 7: Commit**

```bash
git add cmd/mc-backup/main.go cmd/mc-backup/main_test.go
git commit -m "feat: download binary artifact instead of building from source during update"
```

---

### Task 2: Update AGENTS.md test seam docs

**Files:**
- Modify: `AGENTS.md`

- [ ] **Step 1: Update the test seams section**

In `AGENTS.md`, line 29, replace the test seams entry:

```diff
- - `cmd/mc-backup/main.go`: `usageOutput` (`io.Writer`), `findRepoRoot`, and `runUpdateStep` are package-level function variables that can be replaced in tests
+ - `cmd/mc-backup/main.go`: `usageOutput` (`io.Writer`), `downloadFile`, and `runUpdateStep` are package-level function variables that can be replaced in tests
```

- [ ] **Step 2: Commit**

```bash
git add AGENTS.md
git commit -m "docs: update AGENTS.md test seams for binary artifact update"
```

---

### Task 3: Final verification

- [ ] **Step 1: Vet and test everything**

Run:

```bash
go vet ./...
go test -race ./...
```

Expected: both clean.

- [ ] **Step 2: Confirm the diff is scoped**

Run: `git diff --stat main`

Expected: only `cmd/mc-backup/main.go`, `cmd/mc-backup/main_test.go`, and `AGENTS.md` are touched.

- [ ] **Step 3: Confirm each requirement**

- `update` command downloads from `https://github.com/<owner>/<repo>/releases/latest/download/mc-backup-linux-amd64`
- No `git` or `go` needed on the production host
- Stop → install → start → status flow preserved
- Build-before-stop ordering preserved (download happens before stop, so stale binary briefly runs but download failure leaves service running)
- Empty `repoURL` still errors out early
- `findRepoRoot` removed (dead code)
- `osUserHomeDir` removed (dead code)
- `ensureRepo` removed (replaced by `downloadFile`)

---

## Self-Review

**Spec coverage:**
- Artifact source + URL derivation → `deriveReleaseURL` in Task 1 Step 3
- Download flow → `downloadFile` + `runUpdate` in Task 1 Step 3
- Stop → install → start → status preserved → `runUpdate` steps in Task 1 Step 3
- Testing with overridable vars → Task 1 Steps 1, 3
- No git/Go dependency → removals in Task 1 Step 3
- AGENTS.md update → Task 2

**Placeholder scan:** No TBD/TODO/unspecified code. Every step has concrete code and commands.

**Type consistency:** `downloadFile` signature `func(url, dest string) error` matches in test (Step 1) and implementation (Step 3). `deriveReleaseURL` returns `string`, called in `runUpdate` as `releaseURL := deriveReleaseURL(repoURL)`. `runUpdateStep` signature unchanged throughout.
