# Critical Correctness Fixes Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking. Each task is independent and ends in its own commit. Task 3 touches both `cmd/mc-backup/main.go` and `.github/workflows/release.yml`; no other task touches those files. Task 4 touches only `internal/engine/config.go`. Tasks 1 and 2 touch disjoint files (`cmd/mc-backup/main.go` vs `internal/engine/daemon.go`), so all four tasks may be dispatched to separate sub-agents without merge conflicts.

**Goal:** Fix the four most critical correctness/safety issues found in the project evaluation: (1) `mc-backup update` writes the downloaded binary with mode `0644`, breaking the service on next start; (2) `mc-backup backup <Name>` matches server names case-sensitively while the rest of the system normalizes them to lowercase, so a capitalized target silently no-ops; (3) self-update has no checksum verification, so a compromised or corrupted release silently replaces the daemon; (4) `SaveAutoServers` writes the `-auto.toml` sidecar non-atomically with `os.Create` + `fmt.Fprintf` and no `fsync`/rename, so a crash mid-write corrupts the auto-provisioned server config.

**Architecture:** Behavior is preserved except for the bugs being fixed. Reuse the existing overridable package-level function variables in `cmd/mc-backup/main.go` (`downloadFile`, `runUpdateStep`, `osExecutable`) and add one new overridable var (`verifyChecksum`) following the same pattern — do not introduce new test frameworks or HTTP clients. Atomic file writes mirror the existing `SaveConfig` temp-file + `Sync` + `Close` + `Rename` pattern already in `internal/engine/config.go`. Server-name matching is extracted into a small pure helper so it can be unit-tested in isolation without spinning up the full backup cycle (which requires docker/rcon/rsync).

**Tech Stack:** Go 1.21, standard library (`crypto/sha256`, `net/http`, `net/http/httptest`, `os`, `path/filepath`, `strings`), `github.com/BurntSushi/toml`, Go unit tests. Run `go test -race ./...` and `gofmt -d .` as the regression gate.

---

## File Structure

- Modify: `cmd/mc-backup/main.go` — (Task 1) make `downloadFile` create the destination with mode `0755` so the replaced binary stays executable; (Task 3) add `verifyChecksum` overridable var and call it from `runUpdate` before any service stop/install.
- Modify: `cmd/mc-backup/main_test.go` — (Task 1) add `TestDownloadFileSetsExecutableBit` using `httptest`; (Task 3) update `TestUpdateCmdDownloadsAndInstalls` to stub `verifyChecksum` and assert the new call order, plus add focused `verifyChecksum` tests.
- Modify: `internal/engine/daemon.go` — (Task 2) extract `serverMatches` helper and use it in `runBackupCycle` so `onlyServer` is matched case-insensitively.
- Create: `internal/engine/daemon_test.go` already exists — (Task 2) add `TestServerMatches` covering empty, exact, case-differing, and non-matching inputs.
- Modify: `internal/engine/config.go` — (Task 4) rewrite `SaveAutoServers` to be atomic (temp file in same dir, `Sync`, `Close`, `Rename`), mirroring `SaveConfig`.
- Modify: `internal/engine/config_test.go` — (Task 4) add `TestSaveAutoServersAtomicRoundTrip` and `TestSaveAutoServersEmptyRemovesFile`.
- Modify: `.github/workflows/release.yml` — (Task 3) add a step generating `mc-backup-linux-amd64.sha256` and add it to the release assets.

---

### Task 1: Make `downloadFile` preserve the executable bit

**Files:**
- Modify: `cmd/mc-backup/main.go` (the `downloadFile` var, lines 29–47)
- Modify: `cmd/mc-backup/main_test.go` (append new test)

**Why:** `os.Create` creates files with mode `0644` (masked by umask). `runUpdate` does `sudo mv <tmp> <execPath>` over `/usr/local/bin/mc-backup`, so the next `systemctl start mc-backup` fails with "permission denied" because the binary is no longer executable. The fix is to create the destination with mode `0755`. While here, remove the redundant `defer f.Close()` that double-closes the file after the explicit `return f.Close()`.

- [ ] **Step 1: Write the failing test**

Append to `cmd/mc-backup/main_test.go`:

```go
func TestDownloadFileSetsExecutableBit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("binary-bytes"))
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "mc-backup")
	if err := downloadFile(srv.URL, dest); err != nil {
		t.Fatalf("downloadFile: %v", err)
	}

	info, err := os.Stat(dest)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode()&0100 == 0 {
		t.Fatalf("downloaded file is not executable: mode %s", info.Mode())
	}
}
```

Add the required imports to `cmd/mc-backup/main_test.go` (merge with the existing import block): `"net/http"`, `"net/http/httptest"`, `"os"`, `"path/filepath"`.

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./cmd/mc-backup/ -run TestDownloadFileSetsExecutableBit -v`
Expected: FAIL — `downloaded file is not executable: mode -rw-r--r--`.

- [ ] **Step 3: Fix `downloadFile` to create the file executable**

In `cmd/mc-backup/main.go`, replace the entire `downloadFile` var (lines 29–47) with:

```go
var downloadFile = func(url, dest string) error {
	resp, err := http.Get(url)
	if err != nil {
		return fmt.Errorf("download %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: HTTP %d", url, resp.StatusCode)
	}
	f, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0755)
	if err != nil {
		return fmt.Errorf("create %s: %w", dest, err)
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		return fmt.Errorf("write %s: %w", dest, err)
	}
	return f.Close()
}
```

(Note: `os.OpenFile` with `0755` is subject to the process umask, but `0755` preserves the owner-execute bit under any standard umask of `022`/`002`. The redundant `defer f.Close()` is removed; each error path closes explicitly, and the success path returns the result of `f.Close()`.)

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./cmd/mc-backup/ -run TestDownloadFileSetsExecutableBit -v`
Expected: PASS.

- [ ] **Step 5: Run the full suite and formatter**

Run: `go test ./... && gofmt -l .`
Expected: all tests pass, `gofmt` prints nothing.

- [ ] **Step 6: Commit**

```bash
git add cmd/mc-backup/main.go cmd/mc-backup/main_test.go
git commit -m "fix: preserve executable bit on self-update download"
```

---

### Task 2: Match `backup <server>` target case-insensitively

**Files:**
- Modify: `internal/engine/daemon.go` (add `serverMatches` helper; update the filter in `runBackupCycle`)
- Modify: `internal/engine/daemon_test.go` (append new test)

**Why:** Server names are normalized to lowercase at config load (`config.go:114`) and discovery uses lowercase directory names, but `runBackupCycle` compares `s.Name != onlyServer` verbatim. `mc-backup backup Creative` therefore skips every server and silently does nothing. Extract a case-insensitive matcher so the behavior is unit-testable without the full backup cycle.

- [ ] **Step 1: Write the failing test**

Append to `internal/engine/daemon_test.go`:

```go
func TestServerMatches(t *testing.T) {
	cases := []struct {
		onlyServer, name string
		want             bool
	}{
		{"", "creative", true},
		{"", "", true},
		{"creative", "creative", true},
		{"Creative", "creative", true},
		{"CREATIVE", "creative", true},
		{"creative", "Creative", true},
		{"creative", "survival", false},
		{"creative", "creative-survival", false},
	}
	for _, c := range cases {
		if got := serverMatches(c.onlyServer, c.name); got != c.want {
			t.Errorf("serverMatches(%q, %q) = %v, want %v", c.onlyServer, c.name, got, c.want)
		}
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/engine/ -run TestServerMatches -v`
Expected: FAIL — `undefined: serverMatches`.

- [ ] **Step 3: Add the `serverMatches` helper**

In `internal/engine/daemon.go`, add this helper immediately above `runBackupCycle` (after the `serverNames` function):

```go
// serverMatches reports whether a discovered server should be processed in a
// backup cycle targeted at onlyServer. An empty onlyServer selects every
// server; otherwise the comparison is case-insensitive because server names
// are normalized to lowercase at config load and discovery uses lowercase
// directory names.
func serverMatches(onlyServer, name string) bool {
	if onlyServer == "" {
		return true
	}
	return strings.EqualFold(onlyServer, name)
}
```

- [ ] **Step 4: Use the helper in `runBackupCycle`**

In `internal/engine/daemon.go`, inside `runBackupCycle`, replace the existing filter block:

```go
		if onlyServer != "" && s.Name != onlyServer {
			continue
		}
```

with:

```go
		if !serverMatches(onlyServer, s.Name) {
			continue
		}
```

- [ ] **Step 5: Run the test to verify it passes**

Run: `go test ./internal/engine/ -run TestServerMatches -v`
Expected: PASS.

- [ ] **Step 6: Run the full suite, race detector, and formatter**

Run: `go test -race ./... && gofmt -l .`
Expected: all tests pass, `gofmt` prints nothing.

- [ ] **Step 7: Commit**

```bash
git add internal/engine/daemon.go internal/engine/daemon_test.go
git commit -m "fix: match backup target server name case-insensitively"
```

---

### Task 3: Verify the self-update binary checksum before install

**Files:**
- Modify: `cmd/mc-backup/main.go` (add `verifyChecksum` var; call it from `runUpdate`)
- Modify: `cmd/mc-backup/main_test.go` (update existing update test; add checksum tests)
- Modify: `.github/workflows/release.yml` (generate and publish `.sha256` sidecar)

**Why:** `runUpdate` downloads a binary over HTTPS and `mv`s it into place with no integrity check. A compromised or truncated release silently replaces the daemon. Fetch a `mc-backup-linux-amd64.sha256` sidecar from the same release, compute the SHA-256 of the downloaded file, and refuse to install on mismatch or missing checksum. The release workflow must produce the sidecar.

- [ ] **Step 1: Update the release workflow to produce the checksum sidecar**

In `.github/workflows/release.yml`, replace the `Build` step with:

```yaml
      - name: Build
        run: |
          go build \
            -ldflags "-X main.repoURL=https://github.com/hirrrooo/mc-backup.git -X main.version=${{ github.ref_name }}" \
            -o mc-backup-linux-amd64 \
            ./cmd/mc-backup

      - name: Generate checksum
        run: sha256sum mc-backup-linux-amd64 > mc-backup-linux-amd64.sha256
```

And update the `Release` step's `files` list to include both assets:

```yaml
      - name: Release
        uses: softprops/action-gh-release@v2
        with:
          files: |
            mc-backup-linux-amd64
            mc-backup-linux-amd64.sha256
```

- [ ] **Step 2: Write the failing tests for `verifyChecksum`**

Append to `cmd/mc-backup/main_test.go`:

```go
func TestVerifyChecksumSuccess(t *testing.T) {
	body := []byte("not-a-real-binary-but-fine-for-hashing")
	binPath := filepath.Join(t.TempDir(), "mc-backup")
	if err := os.WriteFile(binPath, body, 0644); err != nil {
		t.Fatalf("write bin: %v", err)
	}
	sum := sha256.Sum256(body)
	checksum := fmt.Sprintf("%x  mc-backup-linux-amd64\n", sum)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(checksum))
	}))
	defer srv.Close()

	if err := verifyChecksum(binPath, srv.URL); err != nil {
		t.Fatalf("verifyChecksum: %v", err)
	}
}

func TestVerifyChecksumMismatch(t *testing.T) {
	binPath := filepath.Join(t.TempDir(), "mc-backup")
	if err := os.WriteFile(binPath, []byte("real-bytes"), 0644); err != nil {
		t.Fatalf("write bin: %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("0000000000000000000000000000000000000000000000000000000000000000  mc-backup-linux-amd64\n"))
	}))
	defer srv.Close()

	err := verifyChecksum(binPath, srv.URL)
	if err == nil {
		t.Fatal("expected checksum mismatch error, got nil")
	}
	if !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("expected checksum mismatch error, got: %v", err)
	}
}

func TestVerifyChecksumMissingSidecar(t *testing.T) {
	binPath := filepath.Join(t.TempDir(), "mc-backup")
	if err := os.WriteFile(binPath, []byte("real-bytes"), 0644); err != nil {
		t.Fatalf("write bin: %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	err := verifyChecksum(binPath, srv.URL)
	if err == nil {
		t.Fatal("expected error on missing checksum sidecar, got nil")
	}
	if !strings.Contains(err.Error(), "HTTP 404") {
		t.Fatalf("expected HTTP 404 error, got: %v", err)
	}
}
```

Add the required imports to `cmd/mc-backup/main_test.go`: `"crypto/sha256"`, `"fmt"` (if not already present — note the existing file does not import `fmt`).

- [ ] **Step 3: Run the tests to verify they fail**

Run: `go test ./cmd/mc-backup/ -run TestVerifyChecksum -v`
Expected: FAIL — `undefined: verifyChecksum`.

- [ ] **Step 4: Add the `verifyChecksum` var**

In `cmd/mc-backup/main.go`, update the import block to add `"crypto/sha256"`. Then add this new overridable var immediately after the `downloadFile` var (before `deriveReleaseURL`):

```go
var verifyChecksum = func(binaryPath, checksumURL string) error {
	resp, err := http.Get(checksumURL)
	if err != nil {
		return fmt.Errorf("fetch checksum %s: %w", checksumURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("fetch checksum %s: HTTP %d", checksumURL, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return fmt.Errorf("read checksum: %w", err)
	}
	fields := strings.Fields(strings.TrimSpace(string(body)))
	if len(fields) == 0 {
		return fmt.Errorf("checksum %s is empty", checksumURL)
	}
	expected := fields[0]

	f, err := os.Open(binaryPath)
	if err != nil {
		return fmt.Errorf("open %s: %w", binaryPath, err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return fmt.Errorf("hash %s: %w", binaryPath, err)
	}
	got := fmt.Sprintf("%x", h.Sum(nil))
	if !strings.EqualFold(got, expected) {
		return fmt.Errorf("checksum mismatch: want %s, got %s", expected, got)
	}
	return nil
}
```

- [ ] **Step 5: Call `verifyChecksum` from `runUpdate`**

In `cmd/mc-backup/main.go`, inside `runUpdate`, locate the block right after the successful `downloadFile` call:

```go
	if err := downloadFile(releaseURL, tmpBin); err != nil {
		return fmt.Errorf("download: %w", err)
	}
```

Insert the checksum verification immediately after it (before the `steps :=` slice):

```go
	if err := verifyChecksum(tmpBin, releaseURL+".sha256"); err != nil {
		os.Remove(tmpBin)
		return fmt.Errorf("checksum: %w", err)
	}
```

- [ ] **Step 6: Update the existing `TestUpdateCmdDownloadsAndInstalls` to stub `verifyChecksum`**

In `cmd/mc-backup/main_test.go`, modify `TestUpdateCmdDownloadsAndInstalls`. Add `verifyChecksum` to the saved/restored vars at the top of the test:

```go
	oldVerifyChecksum := verifyChecksum
	t.Cleanup(func() {
		repoURL = oldRepoURL
		downloadFile = oldDownloadFile
		runUpdateStep = oldRunUpdateStep
		osExecutable = oldOsExecutable
		verifyChecksum = oldVerifyChecksum
	})
```

And after the `downloadFile = ...` assignment, add:

```go
	verifyChecksum = func(binaryPath, checksumURL string) error {
		calls = append(calls, "verify:"+binaryPath+" "+checksumURL)
		return nil
	}
```

Update the `want` slice so it records the verify call between download and the service stop. The new `want`:

```go
	want := []string{
		"download:https://github.com/hirrrooo/mc-backup/releases/latest/download/mc-backup-linux-amd64 /usr/local/bin/mc-backup.new",
		"verify:/usr/local/bin/mc-backup.new https://github.com/hirrrooo/mc-backup/releases/latest/download/mc-backup-linux-amd64.sha256",
		"Stopping mc-backup service:sudo systemctl stop mc-backup",
		"Installing mc-backup:sudo mv /usr/local/bin/mc-backup.new /usr/local/bin/mc-backup",
		"Starting mc-backup service:sudo systemctl start mc-backup",
		"mc-backup service status:systemctl status mc-backup --no-pager",
	}
```

- [ ] **Step 7: Run the tests to verify they pass**

Run: `go test ./cmd/mc-backup/ -run 'TestVerifyChecksum|TestUpdateCmd' -v`
Expected: PASS for all four tests.

- [ ] **Step 8: Run the full suite and formatter**

Run: `go test ./... && gofmt -l .`
Expected: all tests pass, `gofmt` prints nothing.

- [ ] **Step 9: Commit**

```bash
git add cmd/mc-backup/main.go cmd/mc-backup/main_test.go .github/workflows/release.yml
git commit -m "fix: verify SHA-256 checksum of self-update binary before install"
```

---

### Task 4: Make `SaveAutoServers` atomic

**Files:**
- Modify: `internal/engine/config.go` (rewrite `SaveAutoServers`, lines 206–228)
- Modify: `internal/engine/config_test.go` (append new tests)

**Why:** `SaveAutoServers` uses `os.Create` followed by a series of `fmt.Fprintf` calls with no temp file, no `fsync`, and no atomic rename. A crash or power loss mid-write leaves a truncated/corrupt `-auto.toml` that breaks config load on next daemon start. The main `SaveConfig` already uses the temp + `Sync` + `Close` + `Rename` pattern; `SaveAutoServers` should mirror it. The hand-written TOML format (including the `# defaults to ...` comment) is preserved so round-tripping stays byte-compatible with existing files.

- [ ] **Step 1: Write the failing tests**

Append to `internal/engine/config_test.go`:

```go
func TestSaveAutoServersAtomicRoundTrip(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "config.toml")
	if err := os.WriteFile(cfgPath, []byte("[global]\n"), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	servers := map[string]ServerConfig{
		"creative": {
			Enabled:          true,
			ContainerName:    "creative-mc-1",
			RconPassword:     "secret",
			PauseIfNoPlayers: true,
		},
	}
	if err := SaveAutoServers(cfgPath, servers); err != nil {
		t.Fatalf("SaveAutoServers: %v", err)
	}

	// No leftover temp files in the config directory.
	entries, err := os.ReadDir(tmp)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Fatalf("leftover temp file: %s", e.Name())
		}
	}

	// The auto file round-trips through LoadConfig with values intact.
	cfg, err := LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	got, ok := cfg.Servers["creative"]
	if !ok {
		t.Fatal("creative server missing after reload")
	}
	if got.ContainerName != "creative-mc-1" || got.RconPassword != "secret" || !got.PauseIfNoPlayers {
		t.Errorf("creative server not persisted correctly: %#v", got)
	}
}

func TestSaveAutoServersEmptyRemovesFile(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "config.toml")
	autoPath := autoServersPath(cfgPath)
	if err := os.WriteFile(cfgPath, []byte("[global]\n"), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := os.WriteFile(autoPath, []byte("[server.creative]\nenabled = true\n"), 0644); err != nil {
		t.Fatalf("write auto: %v", err)
	}

	if err := SaveAutoServers(cfgPath, map[string]ServerConfig{}); err != nil {
		t.Fatalf("SaveAutoServers: %v", err)
	}

	if _, err := os.Stat(autoPath); !os.IsNotExist(err) {
		t.Fatalf("auto file should be removed, got err=%v", err)
	}
}
```

- [ ] **Step 2: Run the tests to verify they pass (they should already pass)**

Run: `go test ./internal/engine/ -run 'TestSaveAutoServers' -v`
Expected: PASS — the current non-atomic implementation happens to produce a valid file when nothing crashes, so the round-trip test passes. The atomicity property is verified by code inspection (temp + rename) and the no-leftover-temp-files assertion; a deterministic crash-mid-write test is impractical in Go without OS-level fault injection, so we rely on the structural mirror of the proven `SaveConfig` pattern.

- [ ] **Step 3: Rewrite `SaveAutoServers` to be atomic**

In `internal/engine/config.go`, replace the entire `SaveAutoServers` function (lines 206–228) with:

```go
func SaveAutoServers(cfgPath string, servers map[string]ServerConfig) error {
	autoPath := autoServersPath(cfgPath)
	if len(servers) == 0 {
		os.Remove(autoPath)
		return nil
	}
	dir := filepath.Dir(autoPath)
	f, err := os.CreateTemp(dir, filepath.Base(autoPath)+".*.tmp")
	if err != nil {
		return fmt.Errorf("create temp for %s: %w", autoPath, err)
	}
	tmp := f.Name()
	defer os.Remove(tmp)

	for name, s := range servers {
		fmt.Fprintf(f, "\n[server.%s]\n", name)
		fmt.Fprintf(f, "enabled = %v\n", s.Enabled)
		fmt.Fprintf(f, "ssh_only = %v\n", s.SSHOnly)
		fmt.Fprintf(f, "container_name = %q\n", s.ContainerName)
		fmt.Fprintf(f, "rcon_password = %q\n", s.RconPassword)
		fmt.Fprintf(f, "# defaults to <watch.path>/<server>/mc-data if empty\n")
		fmt.Fprintf(f, "data_dir = %q\n", s.DataDir)
		fmt.Fprintf(f, "pause_if_no_players = %v\n", s.PauseIfNoPlayers)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return fmt.Errorf("sync %s: %w", tmp, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, autoPath); err != nil {
		return fmt.Errorf("replace %s: %w", autoPath, err)
	}
	return nil
}
```

- [ ] **Step 4: Run the tests to verify they still pass**

Run: `go test ./internal/engine/ -run 'TestSaveAutoServers' -v`
Expected: PASS.

- [ ] **Step 5: Run the full suite, race detector, and formatter**

Run: `go test -race ./... && gofmt -l .`
Expected: all tests pass, `gofmt` prints nothing.

- [ ] **Step 6: Commit**

```bash
git add internal/engine/config.go internal/engine/config_test.go
git commit -m "fix: write auto-provisioned server config atomically"
```

---

## Self-Review Notes

- **Spec coverage:** All four selected issues (#1 exec bit, #2 case-insensitive target, #5 checksum, #11 atomic auto config) have a dedicated task. #1 is fixed in Task 1 Step 3. #2 in Task 2 Steps 3–4. #5 in Task 3 Steps 1–5 (workflow sidecar + runtime verification). #11 in Task 4 Step 3.
- **Placeholder scan:** No TBD/TODO/“add error handling” placeholders. Every code step contains complete code. The one intentional non-red test (Task 4 Step 2) is explained in-line with justification rather than left as a placeholder.
- **Type/name consistency:** `verifyChecksum(binaryPath, checksumURL string) error` is defined in Task 3 Step 4 and called with matching arguments in Step 5 and stubbed with matching signature in Step 6. `serverMatches(onlyServer, name string) bool` is defined in Task 2 Step 3 and used in Step 4 and tested in Step 1 with matching arity. `SaveAutoServers(cfgPath string, servers map[string]ServerConfig) error` keeps the existing signature; the new body matches.
- **Import additions:** Task 1 adds `net/http`, `net/http/httptest`, `os`, `path/filepath` to `main_test.go`. Task 3 adds `crypto/sha256` and `fmt` to `main_test.go` and `crypto/sha256` to `main.go`. Task 4 reuses already-imported `strings`/`filepath`/`fmt`/`os` in `config_test.go`. `strings` is already imported in `daemon.go`. All within existing module deps — no new external imports.
