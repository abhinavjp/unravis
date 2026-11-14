# BBX-1-03 Owner-selected targets in config
Backlog: BL-117 | Group: 1 | Model: efficient | Reason: config fields + plain table tests, one file area, no logic or security surface.
Depends on: none | Blocks: BBX-1-08, BBX-1-10

## Goal
Add an owner-selected target list (IIS app pools and Windows services) to `blackbox.json` with strict validation. Selected targets drive per-process detail later; machine-wide counters stay machine-wide.

## Context
- File `internal/blackbox/config.go` (Config struct lines 72-84, `Validate()` 168-210, bounds 40-48). Test `internal/blackbox/config_test.go` (table-driven style already used).
- `blackbox.json` is world-readable (0o644, `config.go:317`) and shipped in the pack (`pack.go:122`). Targets are pool/service NAMES only — never credentials, never command lines.
- Rules that apply: **owner-selected targets only** for per-process detail; **no text** (names are config identifiers, allowed); **unreadable means missing** (a selected target that is not running is handled by the resolver later, not here).
- Existing validation helpers: `invalidf(...)` returns a formatted validation error; name regex used for `Config.Name` is `^[A-Za-z0-9][A-Za-z0-9-]*$`.

## Do (exact edits)
1. In `config.go`, add a bound constant near line 40-48: `maxTargets = 32`.
2. Add a new type above `Config`:
   ```go
   // Target is one owner-selected app pool or Windows service for per-process detail.
   type Target struct {
       Kind string `json:"kind"` // "appPool" or "service"
       Name string `json:"name"`
   }
   ```
3. Add a field to `Config` (after `Probe Probe`): `Targets []Target `json:"targets,omitempty"``.
4. In `Validate()`, after the logPaths checks (~line 205) and BEFORE the probe refusal (206-208), add target validation:
   - `len(c.Targets) > maxTargets` → `invalidf("too many targets (max %d)", maxTargets)`.
   - For each target: `Kind` must be exactly `"appPool"` or `"service"` else `invalidf("target kind must be appPool or service")`.
   - `Name` must be non-empty, <= 128 chars, and contain no path separators or control chars: reject if it contains any of `\ / : * ? " < > |` or chars < 0x20 → `invalidf("target name has invalid characters")`. (App pool names may contain spaces, dots, dashes, parentheses — allow those.)
   - Reject duplicate (Kind, Name) pairs → `invalidf("duplicate target")`.
5. Do NOT change `Default()` (targets default empty = machine-wide only). Do NOT touch the probe refusal.

## Do not
- Do not add credentials, paths, or command lines to Target.
- Do not change `probe.enabled` refusal (config.go:206-208) — it stays.
- Do not relax `DisallowUnknownFields` (parseDraft) — new field is known.
- Do not make targets required; empty list is valid.

## Tests to write first (add to config_test.go, table-driven)
- `TestTargetsValid` — appPool "Payroll API" and service "EmailSvc" pass Validate.
- `TestTargetsBadKind` — kind "process" fails.
- `TestTargetsBadName` — name with `\`, `:`, or empty fails; name with spaces/dots passes.
- `TestTargetsTooMany` — 33 targets fails.
- `TestTargetsDuplicate` — same (kind,name) twice fails.
- `TestTargetsEmptyOK` — no targets passes (default).
- `TestTargetsRoundTrip` — Marshal then ParseConfig keeps targets (json tags correct).

## Verification checklist
- [ ] go build ./... passes
- [ ] go vet ./... passes
- [ ] go test ./internal/blackbox/... -count=1 passes, tests above included
- [ ] Both build-tag variants compile (GOOS=windows and GOOS=linux go vet ./internal/blackbox/...)
- [ ] Footprint: no goroutine/ticker added
- [ ] Missing reading is omitted, never 0 (n/a — config only)
- [ ] No text captured beyond pool/service names (Target has only Kind+Name)
- [ ] probe.enabled refusal unchanged (config.go:206-208)
- [ ] Existing config tests still pass (no regression to defaults)

## Live-check needed?
No. Validation is pure. The wizard picker that fills these names is BBX-1-10/BBX-1-12.

## Report back
Files changed, each checklist item ticked or failed with output, anything unconfirmed. Full report to `.forge/blackbox-expansion/reports/BBX-1-03.md`.