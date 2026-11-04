# Phase 05 end-to-end pass record (3 Nov 2026)

Exe: `.dev\run\scn\unravis-gate.exe` (demo build with the latest web build). Branch feat/investigation-timeline.

## Results

| Suite | Ids | Result | Date |
|---|---|---|---|
| Web type check (`npm run check`) | 409 files | 0 errors, 0 warnings | 2026-11-03 |
| Web unit tests (`node --test tests/*.test.cjs`) | 2107 tests | 2107 pass | 2026-11-03 |
| Web build + `distChunks.test.cjs` | 2 tests | pass; index 520.59 kB (gzip 168.62), charts 654.44 kB (gzip 218.39), same as before Phase 05 | 2026-11-03 |
| Go `./internal/server/...` | all | pass (105 s) | 2026-11-03 |
| Main e2e, full | 329 tests | 327 pass; HELP-08 and HAND-16 failed once under load and passed on re-run alone (HAND-16 now waits for the page before measuring). Known defects DOC-02, KEEP-09 stay expected-fail. 0 skipped, no `fixme` | 2026-11-03 |
| Timeline screen | TRACE-01..16 | all pass | 2026-11-03 |
| Bar hand-off and case home | HAND-01..19 | all pass | 2026-11-03 |
| Guided archive (informational) | `-Archive` | cannot run on this build: the archived specs open Guided through the case-home button "Open guided analysis", which WP-64 removed, so each times out after 60 s (22 of 22 tried failed; run stopped). The old 75 of 80 baseline was before WP-64. Needs the owner decision on an entry hook (see docs/guided-unlisted.md) | 2026-11-03 |

## Re-run only the new screens

```
cd tools/e2e
powershell -NoProfile -ExecutionPolicy Bypass -File run-e2e.ps1 -Exe <path to unravis exe> -Only "TRACE-01,TRACE-02,TRACE-03"
```

Name every id you want, comma separated (TRACE-01 to TRACE-16, HAND-01 to HAND-19); a range is not accepted. Add `-Archive` for the guided archive suite.

## What each spec file covers

- `specs/tracing-screen.spec.ts` (TRACE-01..16): the Investigation Timeline screen. Region order, three lanes and the shared UTC axis, bars, lane toggles, zoom and brush, scrub pill, P50/P95 line, tooltips, window checks, empty/failed/cancel states, no-source case, privacy (one read-only request, windows only), UTC/Local switch, hidden tooltip not drawn.
- `specs/tracing-handoff.spec.ts` (HAND-01..19): case home and hand-off. Start tracing, click a bar to open the analysis with a 5-minute window, empty and hidden-lane bars, double click, reload, back to case home, 375 px, keyboard, closed and saved cases, no forbidden endpoints, wireframe order and colours, a real mouse click, narrow lane labels.
- The other 30 files in `specs/` are the earlier suites, unchanged in purpose. The old guided specs moved to `archive/guided/`.

## Wireframe match

Verdict: the screens match the wireframe in order, text, tokens and now lane colours (bars use the `--lane-*` tokens). Remaining gaps are the triage "B" items (out of scope or superseded), HTTP.sys markers are not drawn and wait for the owner to confirm, and the P95 right axis has no `0` tick (latent: the server sends no latency figures yet).

## Defects found by these specs and fixed

Bar click lost to pointer capture, hidden tooltip box still drawn (`.tip[hidden]`), lane labels overlapping panels under 480 px. Lane colours were added at the same time.

## Related notes

- [gate-checklist-phase05.md](gate-checklist-phase05.md)
- [wireframe-match-tracing.md](wireframe-match-tracing.md)
- [wireframe-gap-triage.md](wireframe-gap-triage.md)

## Phase 07 (background-run)

Exe: `dist\unravis.exe` from `build.ps1` (GUI subsystem, stamped `v0.1.0-rc.10-5-g3a3fe96-dirty`). Build ran the web check/build, `go test ./...` and `go vet ./...` clean and passed the PE subsystem checks. Branch feat/investigation-timeline. Nothing committed.

| Check | Result | Date |
|---|---|---|
| `build.ps1` (npm ci, check, build, go test, go vet, five exes, subsystem check) | PASS | 2026-11-03 |
| Main + tracing e2e, `run-e2e.ps1 -Exe dist\unravis.exe` (--demo --no-browser) | 324 of 329 pass; 5 fail, all because this exe is version-stamped, not Phase 07 (see below) | 2026-11-03 |
| Re-run of those 5 ids (HELP-03, UPD-04, UPD-05, UPD-05a, UPD-06) on an unstamped GUI build of the same tree (`-Only`) | 5 of 5 PASS | 2026-11-03 |
| Smoke a: `--version` prints and exits 0 (redirected stdout, 21 ms) | PASS | 2026-11-03 |
| Smoke e: `--background` starts, listens, writes session.json, no browser child process, support log says `launch=none` | PASS | 2026-11-03 |
| Smoke b: second `--background` exits 0 in 17 ms, no new server or process, session port unchanged | PASS | 2026-11-03 |
| Smoke c: `--quit` from a second process: exit 0, first copy exits 0 within 10 s, session.json removed | PASS | 2026-11-03 |
| Smoke d: `--quit` with nothing running: exit 0 and "Unravis is not running." | PASS | 2026-11-03 |

The 5 stamped-build failures: the UPD-04/05/05a/06 specs expect a development build (`data-state` "dev") and get "unreachable" because a stamped version tries the network update check; HELP-03 compares the build line "(build abc, date)" to `--version` "(commit abc, built date)". The same 5 specs pass on an unstamped build, and none touches Phase 07 code. Known defects DOC-02 and KEEP-09 stay expected-fail; the archive suite was not run (cannot run, see above).

Smoke notes: `tools/e2e/phase07-smoke.ps1 [-Exe <path>]`. `--no-browser` never reaches the single-instance gate (main.go), so checks b, c, d use `--background` for the first and second copy. The script uses a temp `AUDIT_CONFIG_DIR` and `--port 0`, and removes its processes and temp dir. If another Unravis already holds the per-user instance name, it marks b, c, d, e BLOCKED instead of running `--quit` against that copy. Manual tray, shortcut and sign-in checks were not run here and stay PENDING in `docs/background-run-acceptance.md`.

## UI loop round 1 (2026-11-04)

Unstamped gate exe rebuilt after the round-1 fixes (commit 45cb98e). Full e2e: 327 of 329 pass; 2 flaky (KEY-07 archive-folder race, HAND-17 geometry) pass alone 3 of 3 each. TRACE/CASE/HAND subset 81 of 81. Go `go test ./...` and Linux vet clean; web check 0 errors, 2143 node tests, build and distChunks pass. Findings table: `ui-loop/review-r1-scenarios.md`. Known defects DOC-02, KEEP-09 unchanged.

## UI loop round 2 (2026-11-04)

Build: unstamped plain `go build` after Go sweep round 2 (`5eb00ea`) and UI-R2-01..12 web fixes. TRACE, CASE and HAND subset: 81 of 81 pass. First run had CASE-02 fail on a Windows `ERR_NO_BUFFER_SPACE` asset load; re-run of CASE-02 and HAND-17 passed. HAND-17 is flaky (measures right after first paint). UI-R2-13 (demo-only provenance time) skipped; UI-R2-03 (closed-case timeline) awaits owner decision.

## UI loop round 3 (2026-11-04)

Fresh unstamped exe after UI-R3 fixes. Full run: 326 pass, 3 fail: DOC-02 and KEEP-09 (known defects) and HAND-17 (flaky, passes on re-run). Unit: 2165 node tests pass.

## Perf sweep gate (2026-11-04)

Full release gate on the dirty working tree (branch feat/investigation-timeline), unstamped `go build ./cmd/audit-explorer` exe, web built first.
- Web: `npm run check` 414 files, 0 errors, 0 warnings. `node --test tests/*.test.cjs` 2237 of 2237 pass. `npm run build` ok; `distChunks.test.cjs` 2 of 2 pass.
- Go: `go vet ./internal/... ./cmd/...` clean; Linux vet clean (no output). `go test ./... -count=1`: 70 packages ok, 0 failures, 4 with no test files. No timing-flaky test failed; no re-run needed. UNRAVIS_STRESS_DIR tests skipped (env not set).
- e2e (`npx playwright test`, 1 worker): 338 tests, 336 passed, 2 skipped, 0 failed, 0 flaky, 4.3 min. The 2 skipped are PERF-01 and PERF-02 in tracing-perf.spec.ts (need UNRAVIS_LIVE_EXE / UNRAVIS_PERF_CFG; not run in the gate). DOC-02 and KEEP-09 print as "x" but are counted as passed (known-defect specs); re-run alone: 2 passed. All 16 Investigation Timeline specs in tracing-screen.spec.ts (TRACE-01..16) pass; HAND-01..19 pass; partial-notices and large-window specs pass. HELP-08, CASE-02, KEY-07, HAND-17 did not flake this run.
- Cleanup: gate exe removed; no stray unravis*.exe in the repo root.

## Loop 3 re-gate (2026-11-04)

After the internal/casestore/sqlite change (perf-sweep/fix-loop3.md). Same dirty tree, fresh `go build ./cmd/audit-explorer` gate exe, web already built.
- Vet: `go vet ./internal/... ./cmd/...` clean; Linux vet clean (no output).
- `go test ./... -count=1`: 69 packages ok, 1 FAIL on the first run: internal/casestore/sqlite TestConcurrentImportsAreSerialised ("import 0: an import is running on this case", 1.16 s). Re-run of the package alone: ok (21.5 s); the test alone x10: ok; whole package -count=2: ok. Not a listed flake. Cause: 6 imports queue on one case and the last waiter hits importWaitMax (1 s) while the package ran beside all other packages; lockImport polls every 5 ms with no queue order, so under CPU load a waiter can run out its second. Test timing risk, not a data fault. No other failures.
- e2e (`npx playwright test`, 1 worker): 338 tests, 336 passed, 2 skipped (PERF-01, PERF-02 need a dataset), 0 failed, 0 flaky, 4.2 min.
- Perf on copies (originals untouched), TestTracingStress, 28-29 calls each, sums = manifest, responses under 55 KB, no 5xx:
  | set | median | max | 1 d | 186 d | timeouts | first call |
  |---|---|---|---|---|---|---|
  | S (2.0M rows) | 2.4 s | 7.7 s | 0.8-7.7 s | 2.4-2.8 s | 0 (was 21 of 28) | 7.7 s (1d-end) |
  | M (6.0M) | 6.4 s | 18.2 s | 5.3-18.2 s | 6.3-6.5 s | 0 (was 22 of 29) | 18.2 s (1d-end) |
  | L (12.1M) | 7.1 s | 27.3 s | 7.7-27.3 s | 11.3-11.7 s | 1 (7d-end, IIS lane, 27.2 s) | 27.3 s (1d-end) |
  Lanes now report rowcap (IIS, plus SQL on 90 d and wider on M and L) instead of timeout. The slowest calls are the first ones on a cold file (1d-end, 7d-end, 14d-end on L).
- Playwright tracing-perf.spec.ts on S: 2 passed (PERF-01 186 d 4.9 s, PERF-02 7 d 1.3 s; was 33 s and 17 s).
- L through the real exe (default 4 GiB cap): the case opens (about 18 s) and the 186 d timeline draws (10.8 s, HTTP 200, 49.6 KB), but PERF-01 fails its "no browser errors" check: the page's PUT /api/cases/<id>/workspace answers 413 (the case file, 4.78 GB, is over MaxDBBytes so the SQLite page cap refuses the write). No refusal to open. PERF-02 did not run (serial after the failure). The stress test sets the cap high, so it did not meet this.
- Cleanup: copies, .dev\run\gate and stray test temp dirs removed; no exe in repo root; .dev\perf-data originals kept; C: untouched.
