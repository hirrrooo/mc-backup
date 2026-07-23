# Documentation Index

This directory contains repository evaluations, design specifications, and implementation plans for `mc-backup`.

---

## Repository Evaluation & History

- [`repo-evaluation-2026-07-23.md`](repo-evaluation-2026-07-23.md) — Comprehensive repository evaluation and phased improvement plan for `mc-backup`.

---

## Architecture Specifications

- [`superpowers/specs/2026-06-16-in-app-update-command-design.md`](superpowers/specs/2026-06-16-in-app-update-command-design.md) — Initial design for the in-app `update` command inside `main.go`.
- [`superpowers/specs/2026-06-16-update-script-design.md`](superpowers/specs/2026-06-16-update-script-design.md) — Design for root-level `update.sh` user helper script.
- [`superpowers/specs/2026-06-16-update-from-anywhere-design.md`](superpowers/specs/2026-06-16-update-from-anywhere-design.md) — Design for source-caching updater operating from any directory.
- [`superpowers/specs/2026-06-16-binary-artifact-update-design.md`](superpowers/specs/2026-06-16-binary-artifact-update-design.md) — Direct HTTP binary download architecture for in-place updates.
- [`superpowers/specs/2026-06-16-ci-release-workflow-design.md`](superpowers/specs/2026-06-16-ci-release-workflow-design.md) — GitHub Actions CI release build and asset upload workflow specification.
- [`superpowers/specs/2026-07-20-offline-backup-design.md`](superpowers/specs/2026-07-20-offline-backup-design.md) — Design for `--offline` backups bypassing container/RCON requirements.
- [`superpowers/specs/2026-07-21-local-backup-target-design.md`](superpowers/specs/2026-07-21-local-backup-target-design.md) — Per-server local backup target configuration and routing specification.
- [`superpowers/specs/2026-07-22-rolling-github-release-design.md`](superpowers/specs/2026-07-22-rolling-github-release-design.md) — Automated rolling GitHub release architecture triggered on pushes to `main`.
- [`superpowers/specs/2026-07-23-repository-evaluation-remediation-design.md`](superpowers/specs/2026-07-23-repository-evaluation-remediation-design.md) — Master design for phased remediation of all repository evaluation findings.

---

## Implementation Plans

- [`superpowers/plans/2026-06-11-rsync-progress-status.md`](superpowers/plans/2026-06-11-rsync-progress-status.md) — Implementation plan for live rsync progress monitoring in the status API/dashboard.
- [`superpowers/plans/2026-06-16-in-app-update-command.md`](superpowers/plans/2026-06-16-in-app-update-command.md) — Task breakdown for introducing `mc-backup update` CLI command.
- [`superpowers/plans/2026-06-16-update-script.md`](superpowers/plans/2026-06-16-update-script.md) — Implementation plan for creating the root `update.sh` script.
- [`superpowers/plans/2026-06-16-update-from-anywhere.md`](superpowers/plans/2026-06-16-update-from-anywhere.md) — Plan for caching repo source and executing in-place builds from any directory.
- [`superpowers/plans/2026-06-16-binary-artifact-update.md`](superpowers/plans/2026-06-16-binary-artifact-update.md) — Plan for transitioning updater to direct HTTP release binary downloads.
- [`superpowers/plans/2026-06-16-ci-release-workflow.md`](superpowers/plans/2026-06-16-ci-release-workflow.md) — Implementation plan for tag-triggered GitHub Actions release workflow.
- [`superpowers/plans/2026-06-16-high-priority-fixes.md`](superpowers/plans/2026-06-16-high-priority-fixes.md) — Plan addressing concurrency, env override, and TOML atomic writing fixes.
- [`superpowers/plans/2026-06-16-resolve-evaluation-concerns.md`](superpowers/plans/2026-06-16-resolve-evaluation-concerns.md) — Task breakdown for shell quoting, Makefile, and Go version alignment.
- [`superpowers/plans/2026-06-17-critical-correctness-fixes.md`](superpowers/plans/2026-06-17-critical-correctness-fixes.md) — Plan for executable permissions, case-insensitive server matching, and SHA-256 verification.
- [`superpowers/plans/2026-07-20-offline-backup.md`](superpowers/plans/2026-07-20-offline-backup.md) — Step-by-step plan for implementing offline server backups and directory detection.
- [`superpowers/plans/2026-07-21-local-backup-target.md`](superpowers/plans/2026-07-21-local-backup-target.md) — Plan for per-server local backup targets, storage routing, and prune isolation.
- [`superpowers/plans/2026-07-22-rolling-github-release.md`](superpowers/plans/2026-07-22-rolling-github-release.md) — Implementation plan for continuous rolling GitHub release publish pipeline.
- [`superpowers/plans/2026-07-23-repository-evaluation-remediation.md`](superpowers/plans/2026-07-23-repository-evaluation-remediation.md) — Phased implementation plan executing full repository evaluation remediation.
