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

## Audit lane chunked read (2026-11-04)

Go only (no web, no exe). `readAuditLane` reads a window over 24 h as sequential chunks of at most 24 h cut on the UTC slot grid (the store's 24 h gate and `--allow-broad-scans` default are unchanged).
- New tests in `internal/server/tracing_audit_chunks_test.go` (7): TestAuditLaneChunksElevenDays (11 calls aligned, 12 unaligned, totals = rows, no double count at cuts), TestAuditLaneChunks186Days (186 calls), TestAuditLaneChunksSingleCallUpTo24h (1 call, no chunk fields on the wire), TestAuditLaneSoftDeadlineReturnsPartial (3 of 11 read, timeout, partial text, readThrough), TestAuditLaneChunkErrorKeepsEarlierChunks (chunk 3 error / failed source keeps chunks 1-2), TestAuditLaneChunksCancelStopsPromptly, TestTracingRouteReadsWindowOver24hInChunks (route, 3-day window). TestAuditLaneOneAggregateCall updated (29 d is now 29 calls).
- `go build ./...`, `go vet ./internal/... ./cmd/...`, Linux vet: clean. `go test -p 2 -timeout 25m ./internal/server ./internal/audit ./internal/store`: all ok (server 97.8 s, audit 2.6 s, store 13.6 s).

## Window picker replaces datetime-local (2026-11-04)

Every window field is now WindowPicker.svelte (popover on the Investigation Timeline, inline "dtr" row on guided Step 0 and the Blackbox host card) or StampField.svelte (two UTC boxes, a date and a time). Values are still 'YYYY-MM-DDTHH:mm' read as UTC. Run against a freshly built demo exe (`tools\e2e\run-e2e.ps1`), one worker. The specs fill windows through `tools\e2e\support\window-picker.ts` (open the picker, type the four boxes, Apply).

| Id | What it checks | Result (2026-11-04) |
|---|---|---|
| PICK-01 | A quick range ("Last 4 hours") applies to the incident window; the comparison period becomes the equal period before it; the comparison picker offers its own two ranges and "Same time yesterday" moves it exactly 24 h | pass |
| PICK-02 | Calendar: first click is the start, second the end, an earlier second click swaps; day buttons are named "..., start" / "..., end" with aria-pressed; times stay; the button changes only after Apply; month arrows | pass |
| PICK-03 | Cancel, Escape and a click outside discard the draft; focus returns to the button; the button toggles the popover | pass |
| PICK-04 | A typed time updates the live summary, the calendar follows a typed date, and Apply updates the button | pass |
| PICK-05 | End before (or equal to) the start: "The end must be after the start.", aria-invalid, Apply refused; the screen's own "The incident window end must be later than the start." for a stored bad value; no request | pass |
| PICK-06 | 186 days and 1 minute refused by Start tracing in plain words with no request; exactly 186 days runs (guided allows 7 days; the 30/31 day figures in the request text are not the app's limits) | pass |
| PICK-07 | Overlapping windows still warn only; Start tracing stays enabled and runs; the fix button moves the comparison period before the incident | pass |
| PICK-08 | Bad date or time text marks aria-invalid, shows the copy line, is not applied and keeps the last good value; an emptied box gives "needs a start and an end, in UTC" | pass |
| PICK-09 | 375 px: no sideways scroll with the pickers closed or open, popover inside the page, day buttons at least 36 px | pass |
| PICK-10 | Keyboard: Enter opens, arrows / PageUp / PageDown move over days, Enter selects; Enter in a text box applies and starts no read; every control has a name; dialog has aria-label; focus ring on the button | pass |
| PICK-10b | Dark mode: the popover has its own opaque background | pass |
| TRACE-01 | Pickers (not browser date boxes) in order with the toolbar, chart and notes regions | pass |
| TRACE-11 | Incomplete, reversed and too-long windows are refused by the screen in plain words, with no request (bad values arrive through the kept windows) | pass |
| TRACE-15 | UTC / Local switch: the picker still says UTC. The axis check `/^19 Oct 20:30 /` fails before it reaches the picker: first local tick is "19 Oct 21:00 GMT+5:30" (rounded local ticks). TracingChart and the request window (15:00Z) are unchanged by this work, so this looks like a stale expectation, not the picker | fail (not picker-related, not fixed) |
| LW-01..LW-04 | Large-window reads: windows are filled through the pickers | pass (4 of 4) |
| PART-01..PART-03 | Partial-result notices: windows are filled through the pickers | pass (3 of 3) |
| HAND-01..HAND-09, HAND-12..HAND-19 | Hand-off: windows typed through the pickers; HAND-09 also checks no sideways scroll after the pickers are used; HAND-08 failed once with net::ERR_NO_BUFFER_SPACE on a script file (machine flake) and passed on re-run | pass |
| PERF-01, PERF-02 | Updated to fill windows through the pickers | not run (no UNRAVIS_PERF_CFG dataset here) |

Not reachable in the running app, so covered by tests\windowPicker.test.cjs only: the inline picker on guided Step 0 and the Blackbox host card (case home no longer opens the guided analysis and `#/case/guided/N` is not routed). Step 0 was screenshot through a throwaway build that opened it, then removed (`.dev\picker-shots\step0-*.png`).

## API audit database from a saved connection, P6 and P7 (2026-11-04)

Gaps G4 and G5 of the wireframe gap list: the create wizard ticks an audit-looking database as "API audit logs (added to the analysis)" by default, and Add data > MongoDB on an existing case adds an API audit database from a saved connection (connected with addDatabaseSource, tagged to the case as audit). Run against a demo exe built from `./cmd/audit-explorer` in a repo copy under `.dev\audit-build`, one worker. The demo cannot reach a real MongoDB, so `tools\e2e\specs\audit-db-source.spec.ts` answers three edges with page.route (the connection test, POST /api/sources, POST /api/cases/{id}/sources); everything else is the real demo. Screenshots are in `.dev\audit-shots\`.

| Suite | Ids | Result | Date |
|---|---|---|---|
| audit-db-source | AUDIT-01 (saved connection listed, audit-looking database starts ticked with the hint, ordinary database starts unticked, Add stays off, nothing sent) | pass | 2026-11-04 |
| audit-db-source | AUDIT-02 (connect + tag kind mongo-database / classification audit; database shows in the Audit sources dialog; the Timeline sends no sourceIds) | pass | 2026-11-04 |
| audit-db-source | AUDIT-03 (tag fails 503 after connect: connected-not-tagged text, retry only tags, connects 1 and tags 2) | pass | 2026-11-04 |
| audit-db-source | AUDIT-04 (closed case: Add Mongo source is off) | pass | 2026-11-04 |
| audit-db-source | AUDIT-05 (wizard: audit database ticked by default, review warns "No API audit source will be read." when unticked and not when ticked, Create case connects once and tags as audit) | pass | 2026-11-04 |
| sources-steps | GUIDE-21 (updated: the Mongo card opens the new page, Step 8 stays its own tab) | pass | 2026-11-04 |
| sources-ui, case-wizard | existing specs re-run after the change (32 of 32 with audit-db-source and sources-steps) | pass | 2026-11-04 |
| unit | `node --test tests/*.test.cjs` from web (auditAddSource 6 tests, wizardSources 7 audit-database tests) | pass (2317 of 2317) | 2026-11-04 |
| svelte-check | `npm run check` | pass (0 errors, 0 warnings, 424 files) | 2026-11-04 |

Limit: the server-side choice of audit sources is not exercised against a live Mongo here. The Timeline sends no `sourceIds` unless some are picked, so the server reads every live listed audit source (selectAuditSources, explicit=false, at most 50); a connected source joins the audit lane without a tag. The case audit tags drive the guided screens (caseSourceIds). The Timeline does not list lane sources on screen, so that part rests on the no-sourceIds assertion and the Go tests.


## Wizard "Import files" panel (gaps G1, G2, G8)

The create wizard's Sources step now has one "Import files" panel: choose many files or a folder, or drop files; each row shows the kind guessed from the name (changeable or "Don't use this file"), its time-zone need and its size; every row has Remove; the IIS guide sits under it; Add holds the files so Review does not ask for them again. Run against a demo exe built from `./cmd/audit-explorer` (`.dev\run\scn\unravis-iisfiles2.exe`).

| Suite | Ids | Result | Date |
|---|---|---|---|
| wizard-import-files | FILES-01 (2 IIS files + 1 unknown: kinds, zone text, size, guide visible, Review shows "File chosen" and no file input) | pass | 2026-11-04 |
| wizard-import-files | FILES-02 (change a kind, compressed and empty files refused, Clear) | pass | 2026-11-04 |
| wizard-import-files | FILES-03 (Remove a source takes its held file; Review has nothing to ask for) | pass | 2026-11-04 |
| wizard-import-files | FILES-04 (case created, held W3C file read once, not asked for again) | pass | 2026-11-04 |
| wizard-import-files | FILES-05 (row Remove, "Don't use this file", source Remove: Review and the case's imports hold only the kept file; one file uploaded) | pass | 2026-11-04 |
| wizard-import-files | FILES-06 (HTTP.sys log asks for a computer, UTC shown; bad name refused before any call; computer added from the row and saved to the server list; kind change lets go of the pick; pick kept on Review) | pass | 2026-11-04 |
| wizard-import-files | FILES-07 (Create disabled until the computer is named; result table with File, Type, Rows read, Skipped, Result; the imported file records the picked computer) | pass | 2026-11-04 |
| wizard-import-files | FILES-08 (refused file shows Not read with Try again / Leave out; Try the failed files again; after adding the computer the retry reads it, one import per file; Open the timeline) | pass | 2026-11-04 |
| wizard-import-files | FILES-09 (Leave out takes the failed row off and keeps the case; Add more files opens the import drawer on the case) | pass | 2026-11-04 |
| wizard-import-files | FILES-10 (dropped folder walked in batches, nested folders keep their path, unrecognised file marked, 2 files addable) | pass | 2026-11-04 |
| case-wizard, case-ui, case-start, case-drawers, case-retries, sources-*, imports | existing specs re-run after the change (77 of 77 with the new spec) | pass | 2026-11-04 |
| unit | `node --test tests/*.test.cjs` from web (fileKinds 46 tests, wizardFiles 5, wizardSubmit importer test) | pass (2317 of 2317) | 2026-11-04 |
| svelte-check | `npm run check` | pass (0 errors, 0 warnings, 425 files) | 2026-11-04 |

Re-run with FILES-06..10: wizard-import-files, case-wizard, case-start and case-ui together, 31 of 31 pass. Unit: `node --test` 2371 of 2371; svelte-check 0 errors; build and distChunks pass.

Notes: FILES-01 now checks the UTC text for HTTP.sys logs (they write UTC, not local time) and the new "needs a computer" line. No typed folder-path box was built: the server has no endpoint that lists a local path. A W3C file refused with `unknown-host` needs the computer added to the server list, then Try again. Screenshots: `.dev\wizard-shots\`.


## Timeline sources, chunk read and banner (packet P8 plus Timeline items)

The Timeline now says what it read and what it did not. The banner no longer says "No events in this window" beside "Some lanes were cut short" (`laneNotFullyRead` decides both). A chunked read shows "Read N of M days" with the unread days hatched and labelled "not read". A "Sources traced" row lists the live audit sources, the saved audit Mongo databases (dashed, with Connect when not connected) and the case's imported SQL/IIS files; each can be turned on and off, Select all / Clear all are offered, and the choice is kept per case (`unravis.tracing.sources.<caseId>`). All off reads nothing: no request is sent, the chart is cleared and "No source is selected" is shown. Each lane says which sources it read. A SQL/IIS lane with no file says so and offers "Add files" (opens the Import files panel).

| Suite | Ids | Result | Date |
|---|---|---|---|
| timeline-sources | TSRC-01 (refused API lane on an 11-day window: the banner says what was not read, no "No events in this window") | pass | 2026-11-04 |
| timeline-sources | TSRC-02 ("Read 4 of 11 days", unread days marked "not read") | pass | 2026-11-04 |
| timeline-sources | TSRC-03 (picker lists the sources; turning Demo East off sends the right sourceIds; lane says "Read: Demo West"; reload keeps the choice) | pass | 2026-11-04 |
| timeline-sources | TSRC-04 (turn SQL off then on: request, lane text "No source for this lane is selected.", storage kept or dropped) | pass | 2026-11-04 |
| timeline-sources | TSRC-05 (all off: no POST, none-selected message, kept after reload; Select all sends no sourceIds; Clear all again sends nothing) | pass | 2026-11-04 |
| timeline-sources | TSRC-06 (saved database, not connected: lane text, Connect opens the Connections dialog) | pass | 2026-11-04 |
| timeline-sources | TSRC-07 (SQL/IIS no-file text, Add files opens "Import files") | pass | 2026-11-04 |
| timeline-sources | TSRC-08a/b/c (screenshots light, dark, 375 px) | pass, `.dev\timeline-shots\timeline-sources-{light,dark,375}.png` | 2026-11-04 |
| partial-notices | PART-03 (updated: new banner text "Some lanes were not read in full..." and the lane name) | pass | 2026-11-04 |
| tracing-screen | TRACE-01 (updated: five regions, `sources` between windows and toolbar) | pass | 2026-11-04 |
| tracing-screen | TRACE-15 (updated, see below) | pass | 2026-11-04 |
| timeline specs | tracing-screen, tracing-handoff, window-picker, partial-notices, large-window, tracing-perf, timeline-sources against a freshly built exe (62 passed, 2 skipped: PERF-01 and PERF-02 need the generated dataset; TRACE-01 fixed after that run and re-run alone) | pass | 2026-11-04 |
| unit | `node --test tests/*.test.cjs` from web (timelineSources 20 tests; 2371 of 2371) | pass | 2026-11-04 |
| svelte-check | `npm run check` | pass (0 errors, 0 warnings) | 2026-11-04 |
| go | `go build`, `go vet`, Linux cross-vet, `go test ./internal/server/...` (tracing_sources_test.go: 6 tests) | pass | 2026-11-04 |

TRACE-15 explanation: the test was stale, not the chart. `TracingChart` is unchanged against HEAD. The request window is 15:00Z, which is 20:30 in IST; `canvas.ts` (`roundOffsets` / `axisTicks`) rounds the local ticks, so the first local tick label is "19 Oct 21:00 GMT+5:30". The expectation is now `/^19 Oct 21:00 /`; the UTC label check is unchanged.

Differences left from the prototype (`prototype/tracing-paper-PROTOTYPE.html`): the Sources row has Select all / Clear all, which the prototype does not; a saved-but-not-connected database is a dashed pill with a Connect button, not a chip; SQL/IIS file chips appear only for the case's imported files, and "Other" only for unclassified files; Add files opens the Import files side panel, not the case-home Add data tab; the hatched "not read" blocks and the read-summary line have no prototype equivalent. The saved-database, refused-lane, chunked-read and no-file states in the e2e rows are made by `page.route` changes to the real answer (the demo cannot make them); the server side is covered by the Go tests.

| sources-list (P11, AUDITGAP) | SRCLIST-01 (five rows, lane sticker, Environment / Portal / API tags, tz state per source) | pass | 2026-11-04 |
| sources-list | SRCLIST-02 (env filter shows one, same button clears, counts line) | pass | 2026-11-04 |
| sources-list | SRCLIST-03 (details and why-text, no write request, Add files again opens Import files) | pass | 2026-11-04 |
| sources-list | SRCLIST-04 / 04b (timeline tz notice for a clock-unchecked lane, See the sources opens the Sources tab; no notice when all lanes read) | pass | 2026-11-04 |
| sources-list | SRCLIST-05a/b/c (screenshots light, dark, 375 px: Sources tab, Add audit database, collection problem) | pass, `.dev\sources-shots\` | 2026-11-04 |
| sources-list | AUDITGAP-01 (ready chip words through the flow, no Attach to a server) | pass | 2026-11-04 |
| sources-list | AUDITGAP-02 (collection name check: wildcard, bad char, system., empty, too long; Add follows) | pass | 2026-11-04 |
| sources-list | AUDITGAP-03 (server warning shown, 250 databases cut to 200 and said so, cleared on retest) | pass | 2026-11-04 |
| sources-list | AUDITGAP-04 (C3 saved connection locked while testing) | pass | 2026-11-04 |
| sources-list | AUDITGAP-05 (C7 retest sends the password again; C4 failed retest, Add gone, good retest adds once) | pass | 2026-11-04 |
| sources-list | AUDITGAP-06 (P7 connected database cannot be swapped, one connect; C8 ticks and collection locked after an app-only add) | pass | 2026-11-04 |
| sources-list | AUDITGAP-07 (Check permission opens Step 8: MongoDB) | pass | 2026-11-04 |
| related specs | sources-list, timeline-sources, tracing-screen, tracing-handoff, sources-ui, sources-steps, audit-db-source, case-ui, partial-notices, sources against `.dev\run\sources-p11\unravis-demo.exe` (110 passed) | pass | 2026-11-04 |
| unit | `node --test tests/*.test.cjs` (sourcesList.test.cjs, auditGap.test.cjs added; 2427 of 2427), `tests/distChunks.test.cjs` (2 of 2) | pass | 2026-11-04 |
| svelte-check | `npm run check` | pass (0 errors, 0 warnings) | 2026-11-04 |

P11 / AUDITGAP notes: no Go change (the server already reports the trace tz state and serves the tags and imports). Skipped: per-database document count and age (needs a new server endpoint running Mongo queries), "Attach to a server" (no UI or server concept; belongs to Manage sources, P12). The collection "pattern" check is a name-shape check only. The time zone is shown read-only (the rule is fixed at import; the fix is to add the file again). Differences left from the wireframe: the list has no inline edit of tags (P12), and on a phone the Details button wraps under the chips.

## Hub-review fix (C1, C2, C5, C6, C9, C10, C12, C13, C14, P1, P5)

| Suite | Ids | Result | Date |
|---|---|---|---|
| timeline-sources | TSRC-01..07 (C1 audit lane, C5 chips, P1 cap) against `.dev/run/hubfix/audit-explorer.exe` | pass | 2026-11-04 |
| tracing-screen | TRACE-01..16 (C1 no "Nothing was found" on warnings, C2 aborted read) | pass | 2026-11-04 |
| partial-notices | PART-01..03 | pass | 2026-11-04 |
| window-picker | PICK-01..10, PICK-10b (C6, C9, C12, C13, C14) | pass | 2026-11-04 |
| playwright total | 37 of 37 | pass | 2026-11-04 |
| go | `go test ./internal/server/... -p 2 -timeout 25m`, build, vet, Linux cross-vet | pass | 2026-11-04 |
| unit | `node --test tests/*.test.cjs` (2427 of 2427), `tests/distChunks.test.cjs` (2 of 2) | pass | 2026-11-04 |
| svelte-check | `npm run check` | pass (0 errors) | 2026-11-04 |

Written by the remote hub (lane droid-az-fw-kimi-k3, jobs 37669, 37674, 37675), reviewed and applied locally. Two corrections by hand: the toggleSource cap uses `>` 50, and its test. No UI note for P1 (TraceSources belongs to another change).
| e2e IMPORT | `import-drawer.spec.ts` IMPORT-01..07, IMPORT-08a/b/c (many files, folder, zip, Remove, per-file Used/Rejected, Try again, closed case, screenshots in `.dev/import-shots`) against own demo build | pass 10 of 10 | 2026-11-04 |
| e2e READY | `import-drawer.spec.ts` READY-01 (wizard chips use the four readiness words, tooltips kept; scan routes mocked), READY-02 (case home) | pass 2 of 2 | 2026-11-04 |
| e2e regression | `wizard-import-files`, `imports`, `case-retries`, `sources` and neighbouring specs (81 tests incl. FILES-01..10) against the same build | pass 81 of 81 | 2026-11-04 |
| unit | `web/tests/importBatch.test.cjs` (24 new) within `node --test tests/*.test.cjs` (2427 of 2427) | pass | 2026-11-04 |

## Hub-review B fix (C1+C5, C2, C3, C4, C6, C7, C8, C9, P1, P2, P3)

| Suite | Ids | Result | Date |
|---|---|---|---|
| e2e TWOIIS | `wizard-two-iis.spec.ts` TWOIIS-01 (two IIS logs, Production and UAT, wizard to Sources tab: 2 rows, right env each, UAT filter finds 1; fails with 4 rows on the old `.dev/run/hubfix` exe), TWOIIS-02 (Remove moves focus to next Remove, else previous, else Choose files) against `.dev/run/hubfixB/audit-explorer.exe` | pass 2 of 2 | 2026-11-04 |
| e2e FILES | `wizard-import-files.spec.ts` FILES-01..10 | pass 10 of 10 | 2026-11-04 |
| e2e regression | `case-wizard`, `case-start`, `case-ui`, `sources-list`, `audit-db-source`, `sources-steps` | pass 59 of 59 | 2026-11-04 |
| unit | `node --test tests/*.test.cjs` (2457 of 2457, includes `hubfixB.logic` 14 and `hubfixB.components` 16), `tests/distChunks.test.cjs` (2 of 2) | pass | 2026-11-04 |
| svelte-check | `npm run check` | pass (0 errors) | 2026-11-04 |
| go | no Go change | n/a | 2026-11-04 |

Written by the remote hub (lane droid-az-fw-kimi-k3, jobs 37693 = C5, C1, P2, C3, P1, C2; 37694 = C7, C8, C9, C6, C4, P3), reviewed and applied locally. Corrections by hand: the `ImportFilesPanel.svelte` hunks were merged by hand because another change had altered the root div (`class:in-drawer`); the C9 test regex was relaxed for that class; `auditAddSource.test.cjs` assertion changed to `postSourceTag(id,`. TWOIIS-01/02 written by hand. Known limits: two files with the same base name in different folders stay unpaired by design (C1); C7 (read cancel) is checked by source-structure tests only.

## Gate 2026-11-04 (full)

Verification gate on uncommitted work, branch feat/investigation-timeline. Nothing committed, staged, pushed or reinstalled. No product code changed.

| Stage | Result |
|---|---|
| Go build, vet, Linux cross-vet | clean, cross-vet printed nothing |
| go test -p 2 -timeout 25m | 69 packages ok, 0 failed |
| web npm run check | 0 errors |
| web node --test tests/*.test.cjs | 2457 of 2457 pass |
| web build then distChunks.test.cjs | 2 of 2 pass |
| Playwright full suite (demo exe, tools/e2e) | 396 passed, 5 failed, 2 skipped; DOC-02 and KEEP-09 expected-fail per known-defects.json |

Playwright failures: bb-change.spec.ts BB-12, BB-12a, BB-12b, BB-12c and BB-13a. The 5 failed again on a lone re-run (deterministic) while the other 10 in the file passed. No known-flake test (HELP-08, CASE-02, KEY-07, TRACE-02, HAND-17, HAND-08) failed.
Cause: stale spec, not a product defect. readHost() in tools/e2e/specs/bb-change.spec.ts fills textboxes named Start and End inside the groups Incident window (UTC) and Reference window (UTC). BlackboxHostCard.svelte now renders the new inline WindowPicker, whose boxes are named like "Incident window start date" and "Incident window start time" (and the end pair), so the old locators hit a strict-mode violation. Fix belongs in the spec (use the four-box labels or support/window-picker.ts). Not done in this gate.

Churn check: nothing staged. Real content changes in Go: internal/blackbox/reader_test.go, tracing.go, tracing_audit.go, tracing_audit_test.go, tracing_warnings_test.go; most other Go files show only line-ending noise (identical with --ignore-cr-at-eol).

screens.json: not updated. Each entry needs a docs/user page with a picture, and tools/screens/docs-check.mjs keeps a hard-coded PENDING set, so adding timeline, wizard and import entries means editing files outside the two allowed.

Wireframe spot check (light, 1280): see the final gate report for the difference list.

## Timeline parity (quick range, headings, Refresh timeline, Add evidence)

Owner asked the Investigation Timeline page to match prototype/tracing-paper-PROTOTYPE.html. Changes (web only, no Go):
- "Quick range" chip row above the windows (Last 15 min, Last hour, Last 4 hours, Last 24 hours, Last 7 days, Last 30 days), from pickerModel.ts (incidentQuickChips, withQuickRange, quickRangeIsActive). A chip sets the incident window and the equal period before it; it never reads by itself. The popover is unchanged. The demo-only prototype chip is skipped.
- Visible headings "Investigation window" and "Compare it with (comparison period)" with the prototype's help text. The old legends stay, visually hidden, so group names ("Incident window (UTC)") and locators work; "Times are typed in UTC." stays visible.
- "Refresh timeline" (enabled after the first trace, re-runs the current windows and sources) and "Add evidence" (openCaseImport) in the Sources traced row (TraceSources gained an optional `actions` snippet). A stale line (tp-stale) says when the windows changed since the chart was drawn. When the import drawer closes the trace reads again. Limitation: with a hand-picked subset of sources a new file is not switched on; Select all picks it up.
- Tests: web/tests/timelineParity.test.cjs (6), tools/e2e/specs/timeline-parity.spec.ts TLPAR-01..06 (06 writes the screenshots to .dev/tlpar-shots). Spec and unit tests were written on the remote hub (droid-az-fw-kimi-k3, job 37732) and reviewed; one invalid toHaveAttribute array assertion was fixed.
- Results: npm run check 0 errors; node --test tests/*.test.cjs all pass; dist chunk test 2 of 2; e2e against .dev/run/tlpar demo exe: timeline-parity, timeline-sources, window-picker, tracing-screen, tracing-handoff, tracing-perf = 62 passed, 2 skipped, 0 failed.
- Two existing source-structure tests needed the layout kept (no `readOnly || closed` text; the 720px `.window-set` rule); both still pass.
- Remaining differences from the prototype: the controls are not wrapped in one card; no UTC readout line ("UTC 2026-10-20T11:00Z - ...") under the pickers; Sources chips, Add evidence and Refresh timeline sit in a separate Sources traced box, not in the same row as the windows card; wording says "up to 6 months" (the app limit) where the prototype says 30 days; no "Each bar = 1 minute" line or Suspects rail on this page; the prototype's demo header and prepared-situations dock are not in the app.

## Hub-review C fix (C1..C24: F9, F8, F1, F19, F7, F13, F2, F10, F14, F20, F23-F26, F28, F31, F6; weak tests F43, F45, F48, F49, F50)

| Suite | Ids | Result | Date |
|---|---|---|---|
| e2e | `import-drawer`, `wizard-import-files`, `wizard-two-iis`, `sources-list`, `audit-db-source`, `case-wizard`, `case-start`, `case-ui`, `sources-steps`, `imports` against `.dev/run/hubfixC/audit-explorer.exe` (IMPORT-01 now asserts the exact Used verdict and `Kept N rows.`; READY-02 seeds a case with an imported IIS file and asserts the readiness stickers) | pass 87 of 87, no flake | 2026-11-04 |
| unit | `node --test tests/*.test.cjs` (2490 of 2490; new `hubfixC.logic` 11 and `hubfixC.components` 15; `hubfixB.components` F45 now counts exact controls; `wizardSubmit` F48 asserts locked/finished/imported/failures; `wizardImportFiles` F49 asserts the api boundary), `tests/distChunks.test.cjs` (2 of 2) | pass | 2026-11-04 |
| svelte-check | `npm run check` | pass (0 errors, 0 warnings) | 2026-11-04 |
| go | no Go change | n/a | 2026-11-04 |

Fixed: F9 (app-log tag pairs with any non-catalog importer), F8 (a tag named like a failed or duplicate file is not given to another file), F19 (a real failure after Stop is recorded), F6 (zip CRC-32 check, damaged on mismatch), F1 (Try again and the batch skip "Don't use this file" rows), F20 (kind and computer of a kept file locked), F2 (notice unticked when files are added or cleared), F10 (the page locks its single-file controls while a batch runs), F13 (ticks per database remembered), F14 (closing the panel stops zip reads), F7 (a stale save does not pick the new computer), F23/F24/F25 (focus after Clear, Save/Cancel, a run, Remove), F26 (separate notices for too many files and too deep folders), F28 (stale environment filter dropped), F31 (lost-session sentence). Skipped on purpose: F40, F41 and all PLAUSIBLE items. F35 and F37 investigated only, no code change.

## Gate 2 2026-11-04 (final)

Dirty working tree on feat/investigation-timeline, nothing staged. Fresh unstamped `go build ./cmd/audit-explorer` demo exe at .dev/run/gate2/audit-explorer.exe, web built first.

| Stage | Result |
|---|---|
| Go build, vet, Linux cross-vet | clean, cross-vet printed nothing |
| go test -p 2 -timeout 25m ./... | 70 packages ok, 0 failed |
| web npm run check | 0 errors, 0 warnings |
| web node --test tests/*.test.cjs | 2490 of 2490 pass |
| web build then distChunks.test.cjs | 2 of 2 pass |
| Playwright full suite (tools/e2e, all specs incl. investigate, timeline-parity, timeline-sources, window-picker, tracing-*, import-drawer, wizard-*, sources-*, audit-db-source, bb-change) | 406 passed, 1 failed, 2 skipped (DOC-02, KEEP-09 expected-fail) |

The one failure, bb-add BB-16c, was a Windows `net::ERR_NO_BUFFER_SPACE` asset load (/assets/time-*.js) caught by the browser-error fixture; it passed on an isolated re-run. The bb-change helper fix holds: BB-12, BB-12a..c and BB-13a pass. No other known-flake test failed.

Screenshots (light theme, 1280 and 375 px) of the timeline, the wizard Import files step, the Sources list and the import drawer: .dev/gate2-shots. Keyboard check: with focus on each Sources traced chip, Space switches it off until all are off (aria-pressed false on every chip), Enter switches one back on.

## Wizard cards + drawer grid (hub fixes A to D)

Run against the demo exe built from the repo copy (`.dev\run\wiz2\audit-explorer.exe`), Playwright report `report/wiz2.json`, one worker, no retries.

New specs:
- `tools/e2e/specs/wizard-cards.spec.ts` WCARD-01..05 (screens 37 and 38: four per-type cards with their own Add button and named file input, a card sets the kind and shows only its own files, the restored kinds Hangfire jobs / Redis slow log / Another file or export and their undo, staged rows survive add-source switching and closing Import files, 375 px).
- `tools/e2e/specs/import-grid.spec.ts` GRID-01..06 (screen 112: five cards as the case drawer's entry view, Files list and Back to Add data with staged files kept and counted, the other cards route to Sources tabs and the Blackbox panel, leave-with-staged-files asks first through Close, Escape and the leaving cards, every card off with a reason while an import sends, 375 px).

Results: 11 new specs pass. Existing specs run against the same exe: case-retries, import-drawer, sources-list, sources-steps, timeline-parity, timeline-sources, wizard-import-files, wizard-two-iis, case-start, case-wizard, case-drawers, a11y, case-ui, sources-ui, sources, imports, help-guide (170 passed), then case-api, exports, explorer, investigate, partial-notices, plan, large-window, tracing-handoff, tracing-screen, tracing-perf, window-picker, bb-add, bb-change, blackbox, audit-db-source, sources-limit, keys-ui, import-grid, wizard-cards (418 passed, 2 skipped as before). After the last card-layout fix the wizard and drawer specs were run again (35 passed). Web unit suite 2523 of 2523, svelte-check clean, distChunks 2 of 2 (in the build copy).

Rulings where the wireframe is ambiguous:
- The drawer opened from the header lists cases first, and the five cards sit under the case list; entries that already chose files (Add a file, Add evidence, the new-case tail) skip the cards and open the list.
- The file list is hidden, not unmounted, behind the cards so staged files survive; the cards show "N files staged, not imported" on the Files card.
- Leaving the drawer with staged files asks first through Close, Escape, the scrim and the cards that leave the drawer; Keep editing returns the focus where it was.
- SQL Server and MongoDB cards stay on with no saved connection; Another day is off with "0 of 0 sources can be read by day" when nothing can be read by day.
- The Another day enabled route is checked by unit tests only (it needs a source readable by day).
- The wizard keeps its staged list in the wizard store, so it survives switching add-source forms and closing Import files; Clear and Remove still work.
- The card grid of the wizard step uses four columns at the panel width; the cards wrap to one column at 375 px.

Known and not caused by this work: at 375 px the wizard gallery step and the page behind the drawer already scrolled sideways; the new specs scope their checks to the cards and the dialog.

## Gate 3 2026-11-04

Dirty working tree on feat/investigation-timeline, nothing staged. Fresh unstamped `go build ./cmd/audit-explorer` demo exe at .dev/run/gate3/audit-explorer.exe, web built first. Gate runner only checked; nothing was fixed or committed.

| Stage | Result |
|---|---|
| Go build, vet, Linux cross-vet | clean, cross-vet printed nothing |
| go test -p 2 -timeout 25m ./... | 70 packages ok, 0 failed |
| web npm run check | 0 errors, 0 warnings |
| web node --test tests/*.test.cjs | 2523 of 2523 pass |
| web build then distChunks.test.cjs | 2 of 2 pass |
| Playwright full suite (tools/e2e, all specs incl. investigate, wizard-cards, import-grid) | 417 passed, 1 failed, 2 skipped (PERF-01, PERF-02 generated-dataset specs) |

The one failure, timeline-parity TLPAR-06 (screenshots, light and dark, 1280 and 375), failed "no sideways scroll at 375 px, dark" and then passed 2 of 3 isolated re-runs. A probe found the overflow in the app header (`.actions` / `.utilities` / zone toggle / theme toggle reaching 459 px) on the first check right after the resize, before the header settled to its narrow layout; it did not repeat in 60 further resizes. Treated as an intermittent race in the instant scrollWidth check (no settle wait), not a regression from the wizard or drawer work. None of the known flakes (HELP-08, CASE-02, KEY-07, TRACE-02, HAND-17, HAND-08, BB-16c) failed this run.

Screenshots (light theme, 1280 and 375 px) of the wizard Import files step (empty and with staged files), the case drawer card grid, the Sources list and the Investigation Timeline, plus renders of wireframe screens 37, 38 and 112: .dev/gate3-shots. Checked in the browser: staged files survive switching add-source forms, every kind select returns to "Don't use this file", Remove and Clear empty the list.

Note: `build.ps1` clears everything in cmd/unravis-setup/payload except README.txt (before and after the installer build), which deletes the tracked payload/.gitignore; that is why it shows as deleted. Restore it (git checkout) or make the clear skip it before committing.
