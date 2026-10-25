# Phase 06 evidence: harden and ship

## Done
- TSK-20: three migration tests (`internal/casestore/sqlite/migrate_v3v4_test.go`) pass. A schema 3 case sealed the old way opens with no master key, migrates to 4 once, and survives the master-key re-wrap byte for byte.
- TSK-21 e2e: `internal/server/e2e_wizard_test.go` (7 tests, 17 route subtests). No defect.
- TSK-21 security review: 0 Critical/High/Medium, 6 Low, 7 Info (TSK-21-security.md).
- TSK-21 clumsy review: 0 High, 1 Medium, 4 Low, 4 Info (TSK-21-clumsy.md).
- Fixed: security L1, L2, L3, L4, L5, L6; clumsy M1, L1, L2, L3, L4 (TSK-21-go-fixes.md, TSK-21-web-fixes.md).

## Evidence (this machine, 2026-10-26)
- `go vet ./...` clean. `go test ./... -count=1` green. `internal/sampler` failed once (`TestStartValidatesTheConfig`, known load flake) and passed on rerun.
- `npm test` 1324 of 1324. `npm run check` 0 errors, 0 warnings. `npm run build` ok.
- Browser (built-in pane, isolated scratch instance, narrow width): start screen renders, master-key set-up shows the edge-space note and the mismatch line, no console errors.

## Not fixed (decision needed)
- Security L4 residual: a deleted `legacyset.dat` is not fail-closed (closing it needs a marker in `masterkey.dat`, a format change). Fixed otherwise: DPAPI cases not on the sealed legacy set are refused (409 `untrusted-case`) and shown as not opened.
- Security L6 residual: a worker stuck inside one filesystem call holds its slot; the existence oracle is accepted. Fixed otherwise: cap of 4 workers (429 when full) and cancellation at the 10 s deadline.
- Info items in TSK-21-security.md and TSK-21-clumsy.md.
- The web client has no special handling of a 409 `needs-rewrap` on case open; the Home re-wrap notice is the recovery path.

## UNMEASURED (cannot be done from this machine)
- `-race` (needs gcc).
- Real IIS, Redis, Mongo and SQL probes.
- P4c scheduled task (needs approval).
- ETW, .nettrace and .blg parsers (need real files).
- A real second PC for case-file restore.
- TSK-20 manual step: a real case folder compared between `master` and this build, and real DPAPI sealing.
- Browser: set-up completion, unlock, 423 locked screen and Home Lock were not driven (the set-up acknowledgement is the user's to tick). Covered by Go e2e and web store tests only. Desktop width and light/Terminal theme passes were not repeated.
- Partial: F2 real adapter (`findRecentCase` is tested against a fake list route only).
