# Real-time Rsync Progress in Status Dashboard

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the static "Calculating..." status display with live rsync progress (bytes transferred / total size) during backup operations.

**Architecture:** Add `--info=progress2` to rsync invocations during backups, parse the structured progress lines from rsync's stdout in real time via a goroutine, and feed parsed byte counts through a callback that updates the `JobTracker`. The daemon pre-computes `dirSize` for the world data directory and passes it as `TotalSize` in the initial `JobInfo`, enabling the dashboard to render `X.XXG / Y.YYG` progress instead of "Calculating...".

**Tech Stack:** Go 1.26, rsync `--info=progress2` (available since rsync 3.1.0 / 2013), `bufio.Scanner` with custom split function, `exec.Cmd.StdoutPipe`

---

### Task 1: Add `--info=progress2` flag insertion to rsync args

**Files:**
- Modify: `internal/engine/backup.go:27-37` (localRsyncArgs)
- Modify: `internal/engine/backup.go:39-55` (nasRsyncArgs)

- [ ] **Step 1: Add the flag insertion helper in `runRsync`**

Modify `runRsync` to accept a progress callback and conditionally insert `--info=progress2` into the args. Replace the existing function with:

```go
func runRsync(ctx context.Context, args []string, onProgress func(bytesMoved int64)) error {
	if onProgress != nil {
		// Insert --info=progress2 after the -a flag (args[1])
		newArgs := make([]string, 0, len(args)+1)
		newArgs = append(newArgs, args[0], args[1])
		newArgs = append(newArgs, "--info=progress2")
		newArgs = append(newArgs, args[2:]...)
		args = newArgs
	}

	slog.Debug("running rsync", "args", args)
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)

	if onProgress != nil {
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			return err
		}
		cmd.Stderr = os.Stderr
		if err := cmd.Start(); err != nil {
			return err
		}
		go streamRsyncProgress(stdout, onProgress)
		return cmd.Wait()
	}

	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
```

- [ ] **Step 2: Run tests to check compilation**

```bash
go build ./...
```

Expected: compilation FAILS because `parseRsyncProgress` is not defined yet, and existing callers of `runRsync` don't pass the new parameter. This is expected — we'll fix callers in subsequent tasks.

- [ ] **Step 3: Update all existing `runRsync` callers to pass `nil`**

In `internal/engine/backup.go:182` (NAS rsync call) and `internal/engine/backup.go:193` (local rsync call), change:

```go
if err := runRsync(ctx, args); err != nil {
```

to:

```go
if err := runRsync(ctx, args, nil); err != nil {
```

In `internal/engine/archive.go:89`, change:

```go
if err := runRsync(ctx, args); err != nil {
```

to:

```go
if err := runRsync(ctx, args, nil); err != nil {
```

- [ ] **Step 4: Verify compilation**

```bash
go build ./...
```

Expected: FAILS because `parseRsyncProgress` is still undefined (defined in Task 2). But `runRsync` callers should now compile.

### Task 2: Implement rsync progress parser

**Files:**
- Create: No new file — add to: `internal/engine/backup.go`
- Test: `internal/engine/backup_test.go`

- [ ] **Step 1: Write failing test for progress parser**

Add to `internal/engine/backup_test.go`:

```go
func TestParseRsyncProgress(t *testing.T) {
	tests := []struct {
		line     string
		expected int64
		ok       bool
	}{
		{"    1,048,576  50%   10.00MB/s    0:00:05 (xfr#5, to-chk=50/100)", 1048576, true},
		{"       32768   0%    0.00kB/s    0:00:00 (xfr#1, to-chk=99/100)", 32768, true},
		{"  2,147,483,648 100%   40.00MB/s    0:01:30 (xfr#100, to-chk=0/100)", 2147483648, true},
		{"Sent 1,048,576 bytes  Received 500 bytes  10,240,000 bytes/sec", 0, false},
		{"total size is 2,048,000  speedup is 2.00", 0, false},
		{"", 0, false},
		{"   some random text", 0, false},
	}

	for _, tt := range tests {
		bytes, ok := parseRsyncProgress(tt.line)
		if ok != tt.ok || bytes != tt.expected {
			t.Errorf("parseRsyncProgress(%q) = (%d, %v), want (%d, %v)",
				tt.line, bytes, ok, tt.expected, tt.ok)
		}
	}
}
```

- [ ] **Step 2: Run test to confirm it fails**

```bash
go test ./internal/engine/ -run TestParseRsyncProgress -v
```

Expected: FAIL — `parseRsyncProgress` not defined.

- [ ] **Step 3: Implement the progress parser**

Add to `internal/engine/backup.go` (before or after `runRsync`):

```go
func parseRsyncProgress(line string) (int64, bool) {
	line = strings.TrimSpace(line)
	if line == "" {
		return 0, false
	}
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return 0, false
	}
	cleaned := strings.ReplaceAll(fields[0], ",", "")
	n, err := strconv.ParseInt(cleaned, 10, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}
```

The `strconv` import is already in `backup.go` (no new import needed).

- [ ] **Step 4: Run test to verify it passes**

```bash
go test ./internal/engine/ -run TestParseRsyncProgress -v
```

Expected: PASS

- [ ] **Step 5: Implement custom scanner for rsync `\r`-delimited lines**

Rsync outputs progress with `\r` (carriage return) when piped, or `\n` on some systems. Add a custom split function and the `parseRsyncProgress` reader function. Add to `internal/engine/backup.go`:

```go
func scanRsyncLines(data []byte, atEOF bool) (advance int, token []byte, err error) {
	for i, b := range data {
		if b == '\r' || b == '\n' {
			return i + 1, data[:i], nil
		}
	}
	if atEOF && len(data) > 0 {
		return len(data), data, nil
	}
	return 0, nil, nil
}

func streamRsyncProgress(r io.Reader, onProgress func(bytesMoved int64)) {
	scanner := bufio.NewScanner(r)
	scanner.Split(scanRsyncLines)
	for scanner.Scan() {
		if bytes, ok := parseRsyncProgress(scanner.Text()); ok {
			onProgress(bytes)
		}
	}
}
```

Also add the `"bufio"` and `"io"` imports to `internal/engine/backup.go`.

- [ ] **Step 6: Verify compilation**

```bash
go build ./...
```

Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add internal/engine/backup.go internal/engine/backup_test.go
git commit -m "feat: add rsync progress parsing with --info=progress2 support"
```

### Task 3: Thread progress callback through BackupEngine to daemon

**Files:**
- Modify: `internal/engine/backup.go:108-114` (BackupEngine struct)
- Modify: `internal/engine/backup.go:116-200` (BackupServer)
- Modify: `internal/engine/daemon.go:370-411` (runBackupCycle)

- [ ] **Step 1: Add `OnProgress` field to `BackupEngine`**

In `internal/engine/backup.go`, add to the struct:

```go
type BackupEngine struct {
	cfg        Config
	OnProgress func(bytesMoved int64)
}
```

- [ ] **Step 2: Pass `OnProgress` to `runRsync` in `BackupServer`**

In `internal/engine/backup.go`, in the `BackupServer` method, change both `runRsync` calls.

Line 182 (NAS rsync):
```go
if err := runRsync(ctx, args, nil); err != nil {
```
Change to:
```go
if err := runRsync(ctx, args, be.OnProgress); err != nil {
```

Line 193 (local rsync):
```go
if err := runRsync(ctx, args, nil); err != nil {
```
Change to:
```go
if err := runRsync(ctx, args, be.OnProgress); err != nil {
```

- [ ] **Step 3: Pre-compute `dirSize` in daemon and wire up callback**

In `internal/engine/daemon.go`, in `runBackupCycle`, replace the block at lines 370-397:

Current code:
```go
be := NewBackupEngine(*cfg)
key := s.Watch.Namespace + "/" + s.Name
prev := d.lastBackups[key]
if prev == nil {
    prev = &lastBackup{}
}

d.jobTracker.Add(key, &JobInfo{
    ServerName: s.Name,
    Snapshot:   time.Now().Format("20060102-1504"),
    State:      "Saving",
})

destPath, usedSSH, err := be.BackupServer(ctx, s.Watch, s.Name, s.Server, prev.local, prev.nas)
if err != nil {
    slog.Error("backup failed", "server", s.Name, "error", err)
    d.jobTracker.Remove(key)
    continue
}
```

Replace with:
```go
be := NewBackupEngine(*cfg)
key := s.Watch.Namespace + "/" + s.Name
prev := d.lastBackups[key]
if prev == nil {
    prev = &lastBackup{}
}

dataDir := s.Server.DataDir
if dataDir == "" {
    dataDir = filepath.Join(s.Watch.Path, s.Name, "mc-data")
}
totalSize, _ := dirSize(dataDir)

ts := time.Now().Format("20060102-1504")
d.jobTracker.Add(key, &JobInfo{
    ServerName: s.Name,
    Snapshot:   ts,
    State:      "Saving",
    TotalSize:  totalSize,
})

be.OnProgress = func(bytesMoved int64) {
    d.jobTracker.Add(key, &JobInfo{
        ServerName: s.Name,
        Snapshot:   ts,
        State:      "Saving",
        BytesMoved: bytesMoved,
        TotalSize:  totalSize,
    })
}

destPath, usedSSH, err := be.BackupServer(ctx, s.Watch, s.Name, s.Server, prev.local, prev.nas)
if err != nil {
    slog.Error("backup failed", "server", s.Name, "error", err)
    d.jobTracker.Remove(key)
    continue
}
```

- [ ] **Step 4: Verify compilation**

```bash
go build ./...
```

Expected: PASS

- [ ] **Step 5: Run all tests**

```bash
go test ./internal/engine/ -v
```

Expected: All tests PASS

- [ ] **Step 6: Commit**

```bash
git add internal/engine/backup.go internal/engine/daemon.go
git commit -m "feat: wire rsync progress callback from daemon to BackupEngine"
```

### Task 4: End-to-end verification

**Files:** None (verification only)

- [ ] **Step 1: Build the binary**

```bash
go build -o mc-backup ./cmd/mc-backup
```

- [ ] **Step 2: Quick smoke test with a mock rsync**

Create a test script that simulates rsync progress output:

```bash
cat > /tmp/fake-rsync << 'EOF'
#!/bin/bash
echo "    1,048,576  50%   10.00MB/s    0:00:01 (xfr#5, to-chk=50/100)"
sleep 0.5
echo "    2,097,152 100%   10.00MB/s    0:00:01 (xfr#10, to-chk=0/100)"
EOF
chmod +x /tmp/fake-rsync
```

We can't easily replace rsync at runtime, but we can verify the parsing logic by running the unit tests — which we already did in Task 2.

- [ ] **Step 3: Review the complete diff**

```bash
git diff HEAD~2
```

Verify:
- `runRsync` accepts `onProgress func(bytesMoved int64)` parameter
- `--info=progress2` inserted when `onProgress != nil`
- `parseRsyncProgress` correctly extracts byte count from various line formats
- `scanRsyncLines` handles both `\r` and `\n` delimiters
- `BackupEngine.OnProgress` flows through to `runRsync`
- Daemon pre-computes `dirSize` and sets `TotalSize` in `JobInfo`
- Progress callback updates `BytesMoved` in `JobTracker` during transfer
- All existing `runRsync` callers in `archive.go` pass `nil` (unchanged behavior)

- [ ] **Step 4: Run final test suite**

```bash
go test ./... -v
```

Expected: PASS

- [ ] **Step 5: Commit verification**

```bash
git add -A
git diff --cached
```

---

### Design Notes

**Why `--info=progress2` and not `--progress`:**
- `--progress` outputs per-file progress (one line per file, overwhelming for large worlds)
- `--info=progress2` outputs a single aggregated progress line that updates in-place
- Available since rsync 3.1.0 (2013)

**Why no debounce on progress updates:**
- `JobTracker.Add` is an O(1) map write under a mutex — fast enough for rsync's ~100ms update cadence
- If lock contention becomes an issue in production, a simple `time.Now()` check in the daemon callback (skip if <500ms since last update) can be added without changing the interface

**Why `dirSize` is computed in the daemon, not in `BackupEngine`:**
- The daemon needs `TotalSize` before calling `BackupServer` to initialize the `JobInfo`
- `BackupEngine.BackupServer` also calls `dirSize` internally for the disk space routing check — this is a second call but acceptable since `du -sb` is cached by the filesystem after the first run

**NAS rsync compatibility:**
- `--info=progress2` is a local rsync flag controlling local output — the remote rsync server invoked via SSH does not interpret it, so NAS transfers work identically

**Archive engine unaffected:**
- `archive.go` calls `runRsync` with `nil` callback — no `--info=progress2` added, stdout passthrough unchanged
