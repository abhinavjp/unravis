# Build progress (updated 2026-11-03)

| Phase | State | Commit |
|---|---|---|
| 01 iis-privacy-export + 02 tooltip-dictionary | DONE, gates passed (browser, mobile, perf, Opus security review + fixes, clumsy-user pass), main-tree go/vet/check/npm test green | db99a01 on feat/investigation-timeline (local, not pushed) |
| 03 tracing-api-lanes | DONE (local commit; gates: Opus security review, year-range fix; audit-id format check owed by phase 04) | see git log |
| 04 tracing-ui | DONE (browser gate at 1280/375, light+dark, one POST per run, toggles send no request, no IP/UA/cookie/query in UI or response; main-tree go/vet/check/2088 web tests green; lazy chunk TracingPaper +10.6 KB gzip; also fixes Phase 03 demo case seeding on non-UTC clocks) | see git log |
| 05, 07, 06 | pending | - |

Mechanics: scratch branch hub/pNN-wN in .dev/wt/hub-pNN; hub patches applied locally; hand-off = `git diff <base> hub/...` applied to main tree, explicit-path commit, churn check (`--numstat` vs scratch).
Open items for the owner (surface at the end): see delegation.md plus: saved export hash differs from preview hash (WP-65 rebuild by design); numeric-form referer hosts (2130706433, 0x7f.0.0.1) are kept; preview id is deterministic/replayable; Escape with a tooltip open closes the whole Import dialog; no scrub toggle in the UI; allowlisted cookie >512 B or past the 20th pair is dropped silently; flaky TestV3CaseStillOpensWhenAMasterKeyExistsButIsNotApplied under load; int_test.go commit carries one pre-existing gofmt alignment line.
