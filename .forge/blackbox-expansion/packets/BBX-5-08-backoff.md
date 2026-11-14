# BBX-5-08 Back-off and auto-off that survives restarts (C7)
Backlog: BL-124 | Group: 5 | Model: performance | Reason: failure state machine + persisted auto-off; high security/availability impact.
Depends on: none | Blocks: BBX-5-10
Reviewer: Ultimate (SQL).

## Goal
Implement the SQL failure back-off and a persisted auto-off that Windows service restarts cannot clear; only the owner re-enables. Also slow-query demotion (15 s items → 60 s).

## Context
- New files `internal/bbsql/backoff.go` + `backoff_test.go`. Persistence: `<folder>\sql\state.json` (the SQL service's own folder, DACL-protected). Use a small read/write seam (injectable) so tests need no real disk.
- C7 (security review): each failure doubles the interval (15 s up to 15 min). Ten failures in a row, ANY permission error, or a C8 failure sets auto-off, saved in the state file so Windows restart actions (`service_windows.go:95-99`) cannot clear it; only the owner turns it back on. If a query uses over 50% of its time limit three times in a row, 15 s items drop to 60 s.
- Rules: **auto-off survives restarts**; **only the owner re-enables**; **fixed states** (on/backing-off/auto-off/denied); **no error text persisted**.

## Do
1. `type State struct { Interval time.Duration; Failures int; AutoOff bool; SlowStrikes int; State string }` (State ∈ on/backing-off/auto-off/denied).
2. `func (s *State) OnSuccess()` — reset Failures/SlowStrikes; Interval steps back toward base; State on (unless AutoOff).
3. `func (s *State) OnFailure(permission bool, ceiling bool)` — Failures++; Interval doubles (cap 15 min); State backing-off; if Failures>=10 OR permission OR ceiling → AutoOff=true, State auto-off (or denied for permission/ceiling).
4. `func (s *State) OnSlow(used, limit time.Duration)` — if used > limit/2 → SlowStrikes++ else reset; SlowStrikes>=3 → demote 15 s items to 60 s.
5. `func Load(path) (State, error)` / `func Save(path, State) error` — JSON; on load, if AutoOff was persisted it STAYS auto-off (restart cannot clear). Provide `func OwnerEnable(s *State)` (only an explicit owner action calls this) to clear AutoOff.
6. The runner (BBX-5-10) loads state at start, saves after every change.

## Do not
- Do NOT clear AutoOff on service start/restart (only OwnerEnable).
- Do NOT persist error text, instance names, or driver messages (fixed states/codes only).
- Do NOT exceed the 15 min cap or go below the base interval.
- Do NOT re-enable automatically after calm cycles (auto-off is owner-only; back-off may recover on success but auto-off does not).

## Tests to write first
- `TestBackoffDoubles` — failures double 15→30→60→…→15min cap.
- `TestBackoffTenFailuresAutoOff` — 10 failures → AutoOff, State auto-off.
- `TestBackoffPermissionAutoOff` — one permission failure → AutoOff/denied immediately.
- `TestBackoffCeilingAutoOff` — C8 failure → AutoOff.
- `TestAutoOffSurvivesReload` — Save auto-off, Load → still auto-off (restart cannot clear).
- `TestOwnerEnableClears` — OwnerEnable clears AutoOff; success does NOT.
- `TestSlowDemotion` — 3 consecutive >50% limit → 15s items at 60s.
- `TestSuccessRecoversBackoff` — success after back-off (not auto-off) steps interval back.

## Verification checklist
- [ ] go build ./... passes
- [ ] go vet ./... passes
- [ ] go test ./internal/bbsql/... -count=1 passes, tests above included
- [ ] Both build-tag variants compile (GOOS=windows and GOOS=linux go vet ./internal/bbsql/...)
- [ ] Footprint: state machine + one small file write; no goroutine/ticker
- [ ] No text captured — fixed states only
- [ ] C7 auto-off persists across restart; owner-only re-enable (TestAutoOffSurvivesReload, TestOwnerEnableClears)
- [ ] Back-off doubles to 15 min cap; slow demotion (tests above)

## Live-check needed?
YES — owner stops/starts the SQL service after an auto-off and confirms it stays off until manually re-enabled. Fake tests verify persistence logic only.

## Report back
Files changed, each checklist item ticked or failed with output, anything unconfirmed. Full report to `.forge/blackbox-expansion/reports/BBX-5-08.md`.