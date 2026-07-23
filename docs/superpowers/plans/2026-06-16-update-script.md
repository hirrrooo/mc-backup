# Update Script Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a root-level `update.sh` script that pulls the latest `mc-backup`, installs it, restarts the service, and shows service status.

**Architecture:** Use a single standalone Bash script in the repository root for end-user convenience. The script resolves its own directory, validates that it is inside a git repository, runs only non-destructive update commands, and relies on the existing `Makefile` install target and systemd service name.

**Tech Stack:** Bash, git, GNU Make, sudo, systemd.

---

## File Structure

- Create: `update.sh` for the end-user update flow.

---

### Task 1: Root-Level Update Script

**Files:**
- Create: `update.sh`

- [ ] **Step 1: Create the update script**

Create `update.sh` with this exact content:

```bash
#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

step() {
  printf '\n==> %s\n' "$1"
}

if ! git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
  printf 'error: update.sh must be run from the mc-backup git repository\n' >&2
  exit 1
fi

REPO_ROOT="$(git rev-parse --show-toplevel)"
cd "$REPO_ROOT"

step "Pulling latest source"
git pull --ff-only

step "Installing mc-backup"
sudo make install

step "Restarting mc-backup service"
sudo systemctl restart mc-backup

step "mc-backup service status"
systemctl status mc-backup --no-pager
```

- [ ] **Step 2: Make the script executable**

Run:

```bash
chmod +x update.sh
```

Expected: command exits successfully and `update.sh` has executable permissions.

- [ ] **Step 3: Check Bash syntax**

Run:

```bash
bash -n update.sh
```

Expected: command exits successfully with no output.

- [ ] **Step 4: Inspect the script content**

Run:

```bash
git diff -- update.sh
```

Expected: diff shows only `update.sh`, with `git pull --ff-only`, `sudo make install`, `sudo systemctl restart mc-backup`, and `systemctl status mc-backup --no-pager` in that order.

- [ ] **Step 5: Commit**

Only commit if the user explicitly requests it. If requested, run:

```bash
git add update.sh docs/superpowers/specs/2026-06-16-update-script-design.md docs/superpowers/plans/2026-06-16-update-script.md
git commit -m "feat: add update script"
```

Expected: commit succeeds and includes only the update script plus its spec and plan files.

---

## Self-Review

- Spec coverage: The plan creates a root-level standalone script, resolves the repository root, validates git context, pulls with `--ff-only`, installs with `sudo make install`, restarts the service, and shows status.
- Placeholder scan: No placeholders remain.
- Type consistency: Not applicable; this is a shell script-only change.
