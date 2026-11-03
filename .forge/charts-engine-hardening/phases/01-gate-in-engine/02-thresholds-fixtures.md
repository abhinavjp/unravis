# TSK-02 thresholds-fixtures

risk: medium — regenerates the history file and 5 goldens; wrong regeneration hides a real behaviour change.

## Outcome

`testdata/investigate/thresholds.history.json` holds both versions; the 5 goldens match the new table; the two files that quote the version literal are updated. `go test ./internal/investigate` is green.

## Write scope

- `internal/investigate/testdata/investigate/thresholds.history.json` — NOTE: locate the real path first with Glob `**/thresholds.history.json` (the repo path is `testdata/investigate/` relative to the package or repo root; use whichever exists).
- `testdata/investigate/{missing-iis-logs,no-incident,sql-pool-exhausted,gap-in-data,demo-patterns}.golden.json` (same directory as the history file).
- `tools/e2e/specs/plan.spec.ts` (line ~94)
- `docs/user/guided-analysis.md` (line ~50)

## Must not change

- Any Go source file. Any other golden or testdata file.
- The old history entry `{"version":"2026-10-27","fingerprint":"3ccee624d8e4e32a"}` must stay.
- Do not hand-edit a golden. Do not commit. Do not push. Never touch `sql/`.

## Changes

1. Run: `.dev\toolchains\go\bin\go.exe test ./internal/investigate -run TestThresholdsHistory -update-history`
2. Read the history file. It must be an array of exactly two entries: the old one (unchanged) and `{"version":"2026-11-02","fingerprint":"<16 hex chars>"}`. If not → BLOCKED, report the file.
3. Run: `.dev\toolchains\go\bin\go.exe test ./internal/investigate -update` (rewrites the 5 goldens).
4. Run `git diff -- <goldens directory>` and read every changed line. Allowed changes ONLY: the `version` value, the `fingerprint` value, and 8 added threshold entries named `gateRefMinEvents`, `gateRefMinEventsP95`, `gateIncMinEvents`, `gateSpreadMinShare`, `gateSpreadMaxMinuteShare`, `gateSpreadSkipBelowPerMin`, `laneClockToleranceMs`, `originPersistShare`. Anything else → BLOCKED: stop, paste the unexpected diff into `progress.md`, change nothing further.
5. In `tools/e2e/specs/plan.spec.ts` line ~94 and `docs/user/guided-analysis.md` line ~50 replace `2026-10-27` with `2026-11-02`. Nothing else on those lines.
6. Use the Grep tool for `2026-10-27` over `internal`, `tools`, `docs`, `web`, `testdata`, `cmd` (never `sql/`, never `.forge/`, never `prototype/research/`). List every other hit in `progress.md` under "Not fixed" without editing it (the history file's old entry is expected).

## Proof

```
.dev\toolchains\go\bin\go.exe test ./internal/investigate
.dev\toolchains\go\bin\go.exe build ./...
```

Both PASS. Evidence line: the `git diff --stat` of the goldens and the 2-entry history file.

## Depends on

TSK-01.
