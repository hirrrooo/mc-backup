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
