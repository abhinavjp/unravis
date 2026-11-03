# Build progress (updated 2026-11-03)

| Phase | State | Commit |
|---|---|---|
| 01 iis-privacy-export + 02 tooltip-dictionary | DONE, gates passed (browser, mobile, perf, Opus security review + fixes, clumsy-user pass), main-tree go/vet/check/npm test green | db99a01 on feat/investigation-timeline (local, not pushed) |
| 03 tracing-api-lanes | DONE (local commit; gates: Opus security review, year-range fix; audit-id format check owed by phase 04) | see git log |
| 04 tracing-ui | DONE (browser gate at 1280/375, light+dark, one POST per run, toggles send no request, no IP/UA/cookie/query in UI or response; main-tree go/vet/check/2088 web tests green; lazy chunk TracingPaper +10.6 KB gzip; also fixes Phase 03 demo case seeding on non-UTC clocks) | see git log |
| 07, 06 | pending | - |
| 05 guided-unlisting | DONE (WP-60..64). Gates 2026-11-03: web check 0 errors (409 files), 2107 web tests pass, build + distChunks pass (index 520.59 kB / 168.62 gzip, charts 654.44 / 218.39, unchanged), go ./internal/server pass, main e2e 329 tests, 0 unexpected (HELP-08 and HAND-16 flaked once under load, pass alone; DOC-02 and KEEP-09 stay known defects). New specs: tracing-screen TRACE-01..16 and tracing-handoff HAND-01..19, all live (no fixme). Three app defects found by the e2e and fixed: bar click lost to pointer capture, `.tip[hidden]` still drawn, lane labels overlapping under 480 px; lane colours on bars now match the wireframe. Guided archive suite (informational) cannot run on a post-WP-64 build (its entry button is gone; 22 of 22 timed out, then stopped) and needs an owner decision on an entry hook. Record: e2e-pass-record.md. | see git log |

Mechanics: scratch branch hub/pNN-wN in .dev/wt/hub-pNN; hub patches applied locally; hand-off = `git diff <base> hub/...` applied to main tree, explicit-path commit, churn check (`--numstat` vs scratch).
Open items for the owner (surface at the end): see delegation.md plus: saved export hash differs from preview hash (WP-65 rebuild by design); numeric-form referer hosts (2130706433, 0x7f.0.0.1) are kept; preview id is deterministic/replayable; Escape with a tooltip open closes the whole Import dialog; no scrub toggle in the UI; allowlisted cookie >512 B or past the 20th pair is dropped silently; flaky TestV3CaseStillOpensWhenAMasterKeyExistsButIsNotApplied under load; int_test.go commit carries one pre-existing gofmt alignment line.

## UI loop and quality sweep (2026-11-04)
- Round 1 UI review: 19 findings (UI-R1-01..19). Fixed in 45cb98e: IIS error bars (new `errors` wire field), per-lane 50,000 row cap, zoom focus, min bar height, axis tick format, cancel and re-run behaviour, window validation a11y, timeline history entry and reload. Go sweep round 1 in b9bc42d.
- Open: WEB-R1-25 closed-case branch (owner decision), UI-R1-16 touch targets, tests fixer (WEB-R1-08/24), round 2 review of areas R1 did not cover.

## UI loop round 2 and Go sweep round 2 (2026-11-04)

Go round 2 committed (`5eb00ea`). Web UI-R2-01..12 fixed (see ui-loop/review-r2-scenarios.md "Fix results"); gates: check 0 errors, 2156 node tests pass, build + distChunks pass, e2e subset 81/81. Open: UI-R2-03 owner decision, UI-R2-13 server-side, GO-R2-14/15/17/18.
