# TSK-20: Migration, key re-wrap and back-compat proof
risk: medium — proves R13 and R15 on a real old case.

## Outcome
A case created by the pre-change build (on `master`, sealed by DPAPI) opens on this branch's build with no master key set; its events, imports, series and ledger read identically; it migrates to schema 4 exactly once; sources added to it afterwards are tagged normally. After the master key is set, the same case re-wraps, still reads identically, and opens only while the key is unlocked; `case.db` bytes are unchanged by the re-wrap.

## Write scope
- `internal/casestore/sqlite/migrate_v3v4_test.go` (new), `internal/casestore/sqlite/testdata/` (a small v3 case fixture, no real customer data)
- `.forge/cold-start-wizard/phases/06-harden-and-ship/progress.md` (evidence)

## Must not change
Production code (this task only proves; defects go back to TSK-06 as a recorded deviation).

## Changes
1. Fixture: a tiny synthetic case written by the v3 code path (generated once, committed as bytes with its key handling documented, or created by a test helper that pins schema version 3).
2. Test opens it, asserts counts and a sample query before/after, asserts version 4, reopens twice.
3. Manual: copy one real local case folder to a scratch dir, open it with the new binary on a spare port, compare the overview numbers to the master build; then set a master key in the scratch data folder, re-wrap, restart, unlock and compare again. Never run this against the real case folder.

## Proof
`go test ./internal/casestore/...` green; manual comparison recorded (numbers identical) or marked UNMEASURED if no real case exists on this machine.

## Depends on
TSK-06, TSK-10, TSK-19
