# Update From Anywhere Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task.

**Goal:** `mc-backup update` works from any directory by caching the source repo and building in-place.

**Architecture:** Replace `findRepoRoot` with a repo cache in `~/.cache/mc-backup/source`. Embed the upstream URL at build time. Old `update.sh`-style git pull from CWD becomes the fallback.

**Tech Stack:** Go `os/exec`, `os.Executable()`, `-ldflags`.

---

## File Structure

- Modify: `cmd/mc-backup/main.go` — new repo caching, build, replace logic
- Modify: `cmd/mc-backup/main_test.go` — updated tests
- Modify: `Makefile` — ldflags for repo URL

---

### Task 1: Update From Anywhere

**Files:**
- Modify: `cmd/mc-backup/main.go`
- Modify: `cmd/mc-backup/main_test.go`
- Modify: `Makefile`

- [ ] **Step 1: Add repoURL ldflag in Makefile**

Change the `build` target in `Makefile`:

```makefile
REPO_URL ?= https://github.com/anomalyco/mc-backup

build:
	GOOS=$(GOOS) GOARCH=$(GOARCH) go build -ldflags "-X main.repoURL=$(REPO_URL)" -o $(BINARY) ./cmd/mc-backup
```

- [ ] **Step 2: Update tests for new flow**

Update `cmd/mc-backup/main_test.go`:

```go
func TestUpdateCmdRunsExpectedSteps(t *testing.T) {
	var calls []string
	oldFindRepoRoot := findRepoRoot
	oldRunUpdateStep := runUpdateStep
	oldEnsureRepo := ensureRepo
	t.Cleanup(func() {
		findRepoRoot = oldFindRepoRoot
		runUpdateStep = oldRunUpdateStep
		ensureRepo = oldEnsureRepo
	})

	ensureRepo = func(cacheDir, repoURL string) (string, error) {
		calls = append(calls, "ensureRepo:"+cacheDir)
		return "/tmp/cache/mc-backup/source", nil
	}
	findRepoRoot = func() (string, error) {
		calls = append(calls, "findRepoRoot-fallback")
		return "", fmt.Errorf("fallback")
	}
	runUpdateStep = func(dir, name string, command string, args ...string) error {
		calls = append(calls, dir+":"+name+":"+command+" "+strings.Join(args, " "))
		return nil
	}

	if err := runUpdate(); err != nil {
		t.Fatalf("runUpdate() error = %v", err)
	}

	want := []string{
		"ensureRepo:/home/testuser/.cache/mc-backup/source",
		"/tmp/cache/mc-backup/source:Pulling latest source:git pull --ff-only",
		"/tmp/cache/mc-backup/source:Building mc-backup:go build -o /usr/local/bin/mc-backup ./cmd/mc-backup",
		"/tmp/cache/mc-backup/source:Restarting mc-backup service:sudo systemctl restart mc-backup",
		"/tmp/cache/mc-backup/source:mc-backup service status:systemctl status mc-backup --no-pager",
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %#v, want %#v", calls, want)
	}
}
```

Also add imports for `"os/user"` and `"fmt"`.

- [ ] **Step 3: Run tests to verify failure**

Run: `go test ./cmd/mc-backup` — expected FAIL.

- [ ] **Step 4: Implement new update flow in main.go**

Add `var repoURL = ""` after the imports.

Replace `findRepoRoot` with:

```go
var osUserHomeDir = os.UserHomeDir

var ensureRepo = func(cacheDir, repoURL string) (string, error) {
	if repoURL == "" {
		return "", fmt.Errorf("update requires a built binary with embedded repo URL; use ./update.sh from the source repo instead")
	}
	if _, err := os.Stat(cacheDir); os.IsNotExist(err) {
		fmt.Printf("Cloning %s into %s\n", repoURL, cacheDir)
		cmd := exec.Command("git", "clone", repoURL, cacheDir)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return "", fmt.Errorf("clone: %w", err)
		}
	} else {
		fmt.Printf("Pulling latest into %s\n", cacheDir)
		cmd := exec.Command("git", "-C", cacheDir, "pull", "--ff-only")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return "", fmt.Errorf("pull: %w", err)
		}
	}
	return cacheDir, nil
}
```

Replace `runUpdate` with:

```go
func runUpdate() error {
	home, err := osUserHomeDir()
	if err != nil {
		return fmt.Errorf("cannot determine home directory: %w", err)
	}
	cacheDir := filepath.Join(home, ".cache", "mc-backup", "source")

	// Try cached repo first, fall back to current directory
	var repoRoot string
	if repoURL != "" {
		repoRoot, err = ensureRepo(cacheDir, repoURL)
		if err != nil {
			return err
		}
	} else {
		repoRoot, err = findRepoRoot()
		if err != nil {
			return fmt.Errorf("not inside a git repository and no embedded repo URL: %w", err)
		}
	}

	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("cannot determine binary path: %w", err)
	}

	steps := []struct {
		name    string
		command string
		args    []string
	}{
		{"Pulling latest source", "git", []string{"-C", repoRoot, "pull", "--ff-only"}},
		{"Building mc-backup", "go", []string{"build", "-o", execPath, "./cmd/mc-backup"}},
		{"Restarting mc-backup service", "sudo", []string{"systemctl", "restart", "mc-backup"}},
		{"mc-backup service status", "systemctl", []string{"status", "mc-backup", "--no-pager"}},
	}

	for _, step := range steps {
		if err := runUpdateStep(repoRoot, step.name, step.command, step.args...); err != nil {
			return fmt.Errorf("%s: %w", step.name, err)
		}
	}

	return nil
}
```

Note: `go build -o <execPath>` will fail if the binary is running (text file busy). The daemon must be stopped first, or a temp file + rename approach must be used. Add a step before build: `sudo systemctl stop mc-backup`, and change the restart step to `sudo systemctl start mc-backup`.

- [ ] **Step 5: Add stop-before-build and temp file binary replacement**

Add a `"io"` import. Change the build step to:

```go
		{"Stopping mc-backup service", "sudo", []string{"systemctl", "stop", "mc-backup"}},
		{"Building mc-backup", "go", []string{"build", "-o", tmpBin, "./cmd/mc-backup"}},
		{"Installing mc-backup", "sudo", []string{"mv", tmpBin, execPath}},
		{"Starting mc-backup service", "sudo", []string{"systemctl", "start", "mc-backup"}},
```

And compute `tmpBin` as `execPath + ".new"` with a cleanup defer.

- [ ] **Step 6: Run tests**

Run: `go test ./cmd/mc-backup` — expected PASS.
Run: `go test ./...` — expected PASS.

- [ ] **Step 7: Verify with `go run`**

Run: `go run ./cmd/mc-backup` — prints help including `update` command.

- [ ] **Step 8: Do NOT run real update**

Do not run `mc-backup update` or `go build` the update flow.

---
## Self-Review
- Spec coverage: cache repo in `~/.cache/mc-backup/source`, clone if missing, pull if exists, build, stop service, replace binary, start service, show status.
- Placeholder scan: none.
- Type consistency: `ensureRepo`, `runUpdate` signatures match test expectations.
