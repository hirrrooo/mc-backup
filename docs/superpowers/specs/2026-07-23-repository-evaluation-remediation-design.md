# Repository Evaluation Remediation Design

**Date:** 2026-07-23
**Source:** `docs/repo-evaluation-2026-07-23.md` (evaluated commit `87b9dc6`)
**Status:** Approved design for a later implementation plan

## Purpose and scope

This design resolves every actionable finding in the evaluation that remains
applicable when implementation begins. The implementing agent must re-check
each evaluation anchor first; a finding may be omitted only when the current
tree proves it obsolete or inapplicable, and that decision must be recorded in
the phase commit or review notes. The evaluation document itself remains
tracked and unchanged by this work.

The work is deliberately incremental: repository-only changes precede runtime
changes, behavioral tests precede broad tooling changes, and every phase is an
atomic commit with its own verification. Existing behavior is preserved unless
the finding requires a security, correctness, or explicit configuration change.

## Decisions

### Repository and toolchain policy

- Track `docs/superpowers/plans/` and `docs/superpowers/specs/`; remove both
  ignore rules. Existing design history is intentional repository content.
- Remove the staged `venv/` tree from version control and disk, add `venv/` to
  `.gitignore`, and remove the root `test_tz.go` scratch file. Do not replace
  the scratch file with a test unless rechecking proves its calculation is not
  already covered and the test is needed for a listed behavior.
- Use Go 1.21 as the supported minimum because it is the module and README
  contract. Set `.tool-versions` to `golang 1.21` and keep `go.mod`, README,
  CI, and local build instructions consistent. Do not upgrade the module just
  to match the currently installed toolchain.

### RCON secret handling

Keep `rcon-cli`'s existing `RCON_PASSWORD` environment variable, but never put
the password value in an argument. Extend the command abstraction with an
environment-setting operation backed by `exec.Cmd.Env`; construct RCON argv as
`docker exec -e RCON_PASSWORD <container> rcon-cli <command>` and set
`RCON_PASSWORD=<secret>` on the child command. The concrete `exec.Cmd`
implementation must append injected variables to `os.Environ()` (e.g.
`cmd.Env = append(os.Environ(), env...)`), not replace `cmd.Env` with only
`RCON_PASSWORD`, so that `PATH` and other inherited environment values remain
available. Preserve the existing `commandRunner` seam and add assertions that
both argv and debug output omit the secret. Do not use stdin because that
changes the `rcon-cli` contract and is less compatible with the current
invocation.

### Status API defense in depth

Add an optional `global.api_token`. The default remains loopback-only and
tokenless for backwards compatibility. The daemon must reject startup when
`listen_addr` resolves to a non-loopback bind and `api_token` is empty. When a
token is configured, require `Authorization: Bearer <token>` on `POST
/backup`, `POST /cancel`, and `POST /scan`; `/status` and `/health` remain
readable for local monitoring. Compare supplied and configured tokens with
`crypto/subtle.ConstantTimeCompare`, return `401`, and do not invoke callbacks
on failure. The CLI reads the same config and adds the header for mutating
requests. Document that a non-loopback listener requires a token and that
tokens are shared secrets; do not log them. Treat `api_token` like
`listen_addr`: changing it requires a daemon restart, while other config
changes retain live reload behavior.

This choice prevents accidental network exposure while retaining unattended
local use. It avoids inventing a second authentication system and makes an
explicitly configured remote listener safe by default.

### Configurable rsync excludes

Add `global.excludes []string` as the single default policy, initialized to the
current `*.jar`, `cache`, `logs`, and `*.tmp` list when omitted. Add optional
`server.<name>.excludes []string`. Explicitly specify Go/TOML presence
semantics: an omitted server `excludes` is `nil` and falls back to global
excludes, while an explicit `excludes = []` is non-nil empty and deliberately
disables all excludes for that server. Resolution logic must use
`server.Excludes == nil` to choose fallback, not `len(server.Excludes) == 0`.
Thread the selected list through both local and NAS argument builders. Preserve
ordering and one `--exclude=` argument per entry. Include the fields in TOML
encoding, environment override handling, auto-server serialization, example
config, and reference documentation.

## Ordered phase boundaries

The evaluation's findings are regrouped into six implementation phases so the
approved order is unambiguous. Each phase ends with focused checks and one
atomic conventional commit; later phases must not be mixed into an earlier
commit.

### Phase 0 — Hygiene and documentation truth

Re-check and resolve evaluation items 0.1–0.4 and 1.1–1.3. Remove `venv/` and
`test_tz.go`, repair `.gitignore`, reconcile Go versions, and update `AGENTS.md`
to describe the release workflow, rolling `latest` tag, and `Daemon.cycleMu`
serialization instead of the nonexistent `nasWriteLock`. Spot-check README
claims against code, retaining the accurate `max_mbps` claim. Preserve
`docs/repo-evaluation-2026-07-23.md`.

Verify tracked-file absence, ignore behavior, version consistency,
`go build ./...`, and documentation diff scope.

### Phase 1 — CI safety net

Add a separate `.github/workflows/ci.yml` for pull requests and pushes
(including main) to provide required feedback. It must run formatting failure
detection (`test -z "$(gofmt -l internal cmd)"`), `go vet ./...`, and `go test
-race -cover ./...`. `release.yml` keeps its own release verification and,
before publishing, must independently run the same Go quality gates relevant to
its environment (`gofmt` check, `go vet ./...`, `go test -race ./...`, and
`golangci-lint` after it is adopted). Release only occurs after those in-workflow
steps succeed. Its existing release build and rolling-tag permissions remain
unchanged. Release publishing remains gated by the successful verification in
`release.yml` itself, since the separately triggered CI workflow can run
concurrently. Do not make release publication depend on `ci.yml` or redesign
this as a `workflow_run` chain.

Update `AGENTS.md` and the Makefile only as needed to name the checks. Verify
workflow YAML statically, run all local equivalents, and confirm the release
build still uses the module Go version and ldflags.

### Phase 2 — Security

Implement the RCON environment change and API token/listener policy above.
Update README and `config.example.toml` with the loopback default, non-loopback
restriction, token header contract, and safe operational examples. Keep
auto-provisioned config permissions at 0600 and add no secret to logs,
arguments, error strings, or test output.

Recheck evaluation item 2.3 and preserve its current behavior: auto-server
files remain plaintext TOML secrets but are created through 0600 temporary
files and atomic rename; no encryption migration is part of this remediation.

Behavioral tests must cover secret-free argv, environment propagation, denied
unauthenticated mutation, accepted valid bearer token, rejected invalid token,
no callback on rejection, and non-loopback-without-token startup validation.

### Phase 3 — Correctness and configuration flexibility

Re-check evaluation items 3.1–3.5 and implement all applicable fixes:

1. Add global/per-server exclude selection and argument-builder tests. Test
   requirements must explicitly cover both `nil` (fallback to global excludes) and
   non-nil empty `[]` (deliberately disabling all excludes) server `excludes`
   values.
2. Add `-r`/`--no-run-if-empty` to NAS count pruning and test the exact remote
   command contract.
3. Run `sync` with the cycle context, check its error, and log a warning with
   server/cycle context rather than silently discarding it.
4. Replace CLI single reads with a cap-plus-one read using
   `io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))` and a 1 MiB
   response cap. Reject a response when `len(body) > maxResponseBytes` before
   presenting it; accepted responses use the body as-is, since it is known to
   fit. This detects over-cap responses rather than silently truncating them. Check
   HTTP status before presenting a body as success.
5. Confirm `lastBackups` remains protected by `cycleMu`; retain the explicit
   comment documenting that invariant and let the race suite be the gate. If
   rechecking finds any access outside that critical section, move the access
   behind the same mutex before proceeding.

Behavioral tests come before refactoring completion: save-on defer behavior,
sync failure logging/context, empty NAS prune safety, chunked/long CLI response
handling, and race coverage must pass.

### Phase 4 — Linting and coverage

Adopt a pinned, repository-configured `golangci-lint` version and committed
`.golangci.yml` with `errcheck`, `ineffassign`, `staticcheck`, and `gosec`.
Wire the same version/configuration into `ci.yml`, `release.yml` (so `release.yml`
independently runs `golangci-lint` after it is adopted, ensuring release only
occurs after those steps succeed), and a Makefile target. Fix findings that are in
scope, including ignored errors and unsafe shell-command concerns; do not suppress
a finding without a narrowly documented reason.

Raise tests in this order: `BackupServer` success and save-on-on-error,
local/NAS prune day/count paths, update/checksum mismatch paths, config
environment overrides and `saveSplit` round trips, then API/auth and command
seams introduced earlier. Target at least 65% engine coverage and preserve
explicit invariant tests; report actual package coverage rather than claiming
an unmeasured threshold.

### Phase 5 — Polish and operational documentation

Make `make build` use the same `GOOS`, `GOARCH`, `CGO_ENABLED`, and version/repo
ldflags contract as the release artifact. Document the `sudo systemctl` and
`sudo mv` requirements of self-update; do not add `update --dry-run` in this
remediation because it is not required to address the finding. Leave checksum
verification unchanged. Add fail-fast
configuration validation for malformed `listen_addr` and NAS configurations
missing `ssh_host`, `ssh_user`, or `dest_root`, with precise errors that do not
expose secrets. Add `docs/README.md` linking the plans/specs history after the
ignore policy is fixed.

Verify build parity, validation tests, update tests, docs links, and a final
full test/race/vet/format/lint pass.

## Data, concurrency, and error strategy

Configuration remains an immutable snapshot loaded into `atomicConfig`; new
fields are copied by `cloneConfig`, included in TOML and environment paths,
and selected once per backup cycle. Server excludes override global excludes
without mutating shared slices. API tokens are read from the same snapshot at
server construction; a listener/config restart is required when the token or
listen address changes, while ordinary config reload semantics remain intact.

`Daemon.cycleMu` remains the single serialization boundary for backup cycles,
including NAS writes and `lastBackups`. API callbacks may continue spawning
goroutines, but they cannot bypass that boundary. `JobTracker` retains its
existing mutex. No new global lock or channel is introduced. The race test is
mandatory evidence, not a substitute for documenting the invariant.

Errors must be wrapped with operation and server context, returned when the
caller can act on them, and logged at warning/error level when an operation is
best-effort. `save-on` remains deferred and must still run after every failure
after `save-off`. Cancellation uses the active cycle context for cancellable
work; only the autosave restoration timeout remains detached so cleanup is
attempted during shutdown/cancellation. Secrets are excluded from all error
messages and logs.

## Testing and verification contract

Each phase runs its focused tests before its commit, then the cumulative suite.
The final gate is:

```text
gofmt -l internal cmd       # must print nothing
go vet ./...
go test ./...
go test -race ./...
go test -cover ./...
golangci-lint run
make build
```

Repository checks also require `git ls-files venv` and `git ls-files
test_tz.go` to be empty, `git check-ignore` to report neither tracked design
directory as ignored, and workflow/config documentation to agree with the
implemented token, excludes, and Go version contracts. A build-work review is
performed after the CI, security, correctness, and tooling changes and before
Phase 5; findings are fixed and the affected verification is rerun. A final
independent review rechecks every evaluation anchor and explicitly lists any
proven obsolete/inapplicable item.

## Out of scope

Do not rewrite the backup architecture, change snapshot naming, migrate the
plaintext config secret format, add a remote authentication protocol beyond
the bearer token, change default target semantics, alter retention semantics,
or modify release publication policy beyond separating its test responsibility
from CI. Do not add unrelated refactors, dependencies, UI, or platform
support. The evaluation document is not edited, deleted, or ignored.

## Success criteria and risks

Success means all applicable Phase 0–5 findings are resolved in the ordered
atomic commits; each behavioral fix has a test; secrets do not appear in
process arguments or logs; remote API exposure is rejected without explicit
authentication; configuration and documentation describe the real behavior;
CI catches formatting, vet, race, coverage, and lint regressions; and the final
verification contract passes.

Primary risks are Go-version availability in CI, golangci-lint version drift,
breaking clients when a token is configured, and shell quoting regressions in
remote prune commands. Mitigate these with a pinned tool version, explicit
config/example updates, CLI header propagation tests, exact command-string
tests using hostile path characters, and phase-local verification before any
later phase depends on the change.
