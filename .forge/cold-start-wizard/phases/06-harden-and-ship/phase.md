# Phase 06: harden-and-ship
risk: medium — reasons: no new design, but it is the proof phase; its findings may send work back to earlier phases.

## Outcome
Old cases open, the whole flow works end to end on real data from this PC, security review of new routes is clean, the clumsy-user review has no open high finding, and the build and both instances (18770 demo, 18771 live) run the new binary.

## Tasks
| Task | Risk | Depends on |
|---|---|---|
| TSK-20 migration, key re-wrap and back-compat proof | medium | TSK-06, TSK-10, TSK-19 |
| TSK-21 end-to-end, security, clumsy-user review, rebuild | medium | TSK-20 |

## Edges
Sequential. Fixes found here are made inside the owning earlier task's Write scope and noted in that phase's `progress.md` as a deviation.

## Review focus
Unmeasurable items must be reported as UNMEASURED (no SQL Server, Redis or IIS server on this machine; no gcc for `-race`; scheduled-task approval pending); never claimed as passed.

## Phase proof
`.\build.ps1` green (npm ci, check, build, go test); manual proof recorded in progress.md.
Commit: one commit for the phase, after a stop. Never push.
