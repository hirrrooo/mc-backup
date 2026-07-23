# Offline Backup Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Allow `mc-backup backup --offline` to backup a server's data directory without RCON or container checks, with CWD-based server name detection.

**Architecture:** Plumb an `offline` bool through the CLI → HTTP → daemon → backup engine chain. When true, skip container checks and RCON; rsync data directory directly.

**Tech Stack:** Go, HTTP, Docker (container checks)

---

### Task 1: Add offline support to BackupServer

**Files:**
- Modify: `internal/engine/backup.go:158-242`

- [ ] **Step 1: Add `offline bool` parameter and skip RCON when true**

In `internal/engine/backup.go`, change the `BackupServer` function signature and guard the RCON block:

```go
func (be *BackupEngine) BackupServer(ctx context.Context, watch WatchConfig, serverName string, server ServerConfig, prevLocalBackup, prevNASBackup string, offline bool) (destPath string, usedSSH bool, rerr error) {
	dataDir := server.DataDir
	if dataDir == "" {
		dataDir = filepath.Join(watch.Path, serverName, "mc-data")
	}
	excludes := []string{"*.jar", "cache", "logs", "*.tmp"}

	container := server.ContainerName
	if container == "" {
		container = serverName + "-mc-1"
	}

	if !offline {
		if err := runRcon(ctx, container, server.RconPassword, "save-off", rconRetries, rconRetryInterval); err != nil {
			return "", false, fmt.Errorf("save-off: %w", err)
		}

		defer func() {
			slog.Info("re-enabling autosave", "server", serverName)
			detachedCtx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
			defer cancel()
			if err := runRcon(detachedCtx, container, server.RconPassword, "save-on", rconRetries, rconRetryInterval); err != nil {
				saveOnErr := fmt.Errorf("FATAL: save-on failed for %s: %w", serverName, err)
				slog.Error(saveOnErr.Error())
				if rerr == nil {
					rerr = saveOnErr
				} else {
					slog.Error("save-on failed after backup error, server may have autosave OFF", "server", serverName, "backup_error", rerr)
				}
			}
		}()

		if err := runRcon(ctx, container, server.RconPassword, "save-all flush", rconRetries, rconRetryInterval); err != nil {
			return "", false, fmt.Errorf("save-all flush: %w", err)
		}

		commandRunner.CommandContext(context.Background(), "sync").Run()
	} else {
		slog.Info("offline backup, skipping RCON and sync", "server", serverName)
	}

	// rest of function unchanged...
```

- [ ] **Step 2: Verify compilation**

Run: `go build ./...`
Expected: Build succeeds

- [ ] **Step 3: Run existing tests**

Run: `go test ./internal/engine/... -run TestLocalRsyncArgs\|TestNASRsyncArgs\|TestNoLinkDestFirstRun\|TestParseRsyncProgress\|TestIsBackupDirRequiresTimestampFormat\|TestStreamRsyncProgressHandlesCarriageReturns -v`
Expected: All pass (these test isolated helpers, not BackupServer directly)

- [ ] **Step 4: Commit**

```bash
git add internal/engine/backup.go
git commit -m "feat(backup): add offline flag to BackupServer, skip RCON when offline"
```

---

### Task 2: Update status API for offline param

**Files:**
- Modify: `internal/engine/status.go:54-96`

- [ ] **Step 1: Update StatusCallbacks and /backup handler**

```go
type StatusCallbacks struct {
	OnCancel func()
	OnScan   func()
	OnBackup func(server string, offline bool)
}
```

Update the handler:

```go
mux.HandleFunc("/backup", func(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	server := r.URL.Query().Get("server")
	offline := r.URL.Query().Get("offline") == "true"
	callbacks.OnBackup(server, offline)
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("backup triggered"))
})
```

- [ ] **Step 2: Verify compilation**

Run: `go build ./...`
Expected: Build fails — daemon.go's callbacks.OnBackup calls need updating (fixed in Task 3)

- [ ] **Step 3: Commit**

```bash
git add internal/engine/status.go
git commit -m "feat(status): add offline param to OnBackup callback and handler"
```

---

### Task 3: Update daemon orchestration

**Files:**
- Modify: `internal/engine/daemon.go:210-211, 252-254, 269-270, 327-459, 468`

- [ ] **Step 1: Update OnBackup callback and runBackupCycle signature**

In `d.Run()` around line 210, update the callback:

```go
OnBackup: func(server string, offline bool) {
	go d.runBackupCycle(ctx, server, offline)
},
```

Change `runBackupCycle` signature and add offline handling:

```go
func (d *Daemon) runBackupCycle(parent context.Context, onlyServer string, offline bool) {
	// ... existing preamble unchanged ...

	for _, s := range servers {
		// ... existing select/cancel check unchanged ...

		if !s.Server.Enabled {
			continue
		}

		if !serverMatches(onlyServer, s.Name) {
			continue
		}

		if !offline {
			container := s.Server.ContainerName
			if container == "" {
				container = s.Name + "-mc-1"
			}
			if !containerRunning(container) {
				slog.Info("container not running, skipping backup", "server", s.Name, "container", container)
				continue
			}

			if s.Server.PauseIfNoPlayers {
				out, err := rconOutput(ctx, container, s.Server.RconPassword, "list")
				if err != nil {
					slog.Warn("cannot query player count, skipping backup", "server", s.Name, "error", err)
					continue
				}
				if countPlayers(out) == 0 {
					slog.Info("no players online, skipping backup", "server", s.Name)
					continue
				}
			}
		} else {
			slog.Info("offline backup, skipping container checks", "server", s.Name)
		}

		// ... create BackupEngine ...
		destPath, usedSSH, err := be.BackupServer(ctx, s.Watch, s.Name, s.Server, prev.local, prev.nas, offline)
		// ... rest unchanged ...
	}
}
```

Update all existing calls to pass `false`:
- Line 252: `d.runBackupCycle(ctx, "", false)`
- Line 254: `d.runBackupCycle(ctx, "", false)`
- Line 269: `d.runBackupCycle(ctx, "", false)`
- Line 468: `go d.runBackupCycle(ctx, "", false)`

- [ ] **Step 2: Verify compilation**

Run: `go build ./...`
Expected: Build succeeds

- [ ] **Step 3: Commit**

```bash
git add internal/engine/daemon.go
git commit -m "feat(daemon): add offline param to runBackupCycle, skip container checks"
```

---

### Task 4: CLI — --offline flag and CWD detection

**Files:**
- Modify: `cmd/mc-backup/main.go:158-196`

- [ ] **Step 1: Update backupCmd with --offline flag and CWD detection**

```go
func backupCmd() {
	fs := flag.NewFlagSet("backup", flag.ExitOnError)
	cfgPath := fs.String("config", findConfig(), "config file path")
	offline := fs.Bool("offline", false, "backup without RCON (works when container is offline)")
	fs.Parse(os.Args[2:])

	cfg, err := engine.LoadConfig(*cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "config error: %v\n", err)
		os.Exit(1)
	}

	server := fs.Arg(0)

	if server == "" {
		cwd, err := os.Getwd()
		if err != nil {
			fmt.Fprintf(os.Stderr, "backup: cannot get current directory: %v\n", err)
			os.Exit(1)
		}
		candidate := filepath.Base(cwd)
		if _, ok := cfg.Servers[strings.ToLower(candidate)]; ok {
			server = candidate
		} else {
			fmt.Fprintf(os.Stderr, "backup: no server specified and %q is not a known server\n", candidate)
			os.Exit(1)
		}
	}

	backendURL := fmt.Sprintf("http://%s/backup?server=%s", cfg.Global.ListenAddr, url.QueryEscape(server))
	if *offline {
		backendURL += "&offline=true"
	}
	req, err := http.NewRequest(http.MethodPost, backendURL, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "backup: %v\n", err)
		os.Exit(1)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "backup failed: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	var buf [64]byte
	n, _ := resp.Body.Read(buf[:])
	switch {
	case resp.StatusCode == http.StatusOK:
		fmt.Printf("backup: %s\n", buf[:n])
	default:
		fmt.Fprintf(os.Stderr, "backup: daemon returned %d\n", resp.StatusCode)
		os.Exit(1)
	}
}
```

Add imports if needed at the top of main.go:

```go
import (
	"crypto/sha256"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"mc-backup/internal/engine"
)
```

(Note: `strings` and `path/filepath` may already be imported — just verify at the top of the file.)

- [ ] **Step 2: Verify compilation**

Run: `go build ./...`
Expected: Build succeeds

- [ ] **Step 3: Commit**

```bash
git add cmd/mc-backup/main.go
git commit -m "feat(cli): add --offline flag and CWD-based server name detection"
```

---

### Task 5: Verify full build and tests

**Files:**
- All modified files

- [ ] **Step 1: Full build check**

Run: `make build`
Expected: Binary compiles to `./mc-backup`

- [ ] **Step 2: Run all tests**

Run: `go test ./...`
Expected: All pass

- [ ] **Step 3: Final commit if any fixes needed**

```bash
git add -A
git commit -m "fix: address review feedback"
```
