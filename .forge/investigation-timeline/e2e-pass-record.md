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

## log4net importer

Agent C. A generic importer for application logs written as log4net text blocks: each entry is a few labelled lines (Logger name, Type, Server Time, UTC Time, Application URL, User IP), then the message and, for an error, the exception text. Importer kind: `log4net-blocks`. The single-line default layouts stay with `log4net-text`; the new importer does not claim them. The label spellings were taken from a one-line description, not from a real file, so the importer accepts common variants and anything else is a limit (see below).

What was built:
- internal/importer/log4net/blocks.go: Detect scores 90 / 70 / 0 so it never takes a file another kind owns. Time: UTC Time first; Server Time only when there is no readable UTC Time, with the host zone (known or unknown zone handling the same as log4net-text). Level, logger, exception types are read; message, exception text, URL and user IP are used to delimit the block but never stored (the app-log privacy rule). Bounded memory and a 64 KiB line limit; a block with no readable time is one rejected row, so Partly used / Rejected follow the usual 5 percent rule.
- Wiring: importer/all, supplied keys, collect, discover (content detection), evidence export names, resource rules (rc345 app-log sources), server discover-locate, timeline names.
- Web: importCopy labels and help, cases.ts 2 GiB cap, fileKindsCopy (app-log text, app card guide), fileKinds.ts suggests an app log for log.txt, log.txt.N, app-log.txt and the like.
- Tests: internal/importer/log4net/blocks_test.go (conformance harness, fixtures, time shapes, DST, limits, privacy), all/log4net_detect_test.go (auto-detect picks it and nothing else; the default layout still goes to log4net-text; prose is not claimed), prober and golden-stall cases for the new kind, discover content test, web/tests/log4net.kinds.test.cjs (7 tests), e2e spec L4N-01..04.
- E2E: tools/e2e/specs/log4net-import.spec.ts with fixture tools/e2e/fixtures/files/app-log4net.txt (synthetic, 40 blocks, one with an unreadable time so the file is Partly used, a Redis timeout warning and a SQL timeout error block). L4N-01 case drawer, chosen as app log, Partly used, imports list names log4net-blocks and holds no URL or IP. L4N-02 a file named log.txt is suggested as app-log, a prose file chosen as app log is Rejected. L4N-03 create wizard Application logs card, case made, file read as log4net-blocks. L4N-04 Investigation Timeline lists the chip "File - log4net-blocks" under Sources traced.

Fixture lesson: one bad block in eight is 12.5 percent and the whole file is refused ("Too many rows were rejected"); the limit is 5 percent, so a Partly used fixture needs at least 20 blocks per bad one. The first fixture version also had a generator bug (minute 60 and over), which the importer correctly rejected.

Results (all on the current tree, Windows, PowerShell 5.1, GOTOOLCHAIN=local, no race detector available):
| Check | Result |
|---|---|
| go build ./..., go vet ./internal/... ./cmd/..., GOOS=linux go vet | clean, cross-vet printed nothing |
| go test -p 2 -timeout 25m ./internal/... | 56 package lines ok, none failed (internal/server also passed on this run) |
| log4net package | 86 top-level tests pass |
| web npm run check | 0 errors, 0 warnings |
| web node --test tests/*.test.cjs | 2562 of 2562 pass |
| web build then distChunks.test.cjs | 2 of 2 pass |
| Playwright log4net-import | 4 of 4 pass |
| Playwright imports, sources-list, timeline-sources, import-drawer, import-grid | 47 of 47 pass |
| Playwright wizard-import-files + wizard-cards | 13 pass, 6 fail (see below) |

The 6 wizard failures (WCARD-01, FILES-02, FILES-07, FILES-08, FILES-09, FILES-10) are not from this work: they fail on assertions about the wizard Import files markup (compressed-file row text, the "Rows read" results table, dropped-folder drop target). The same 6 failed on an exe rebuilt from the current tree, and the files that own that markup (ImportFilesPanel.svelte, CreateProgress.svelte, SourceGallery.svelte, FileGuides.svelte) are in agent A's uncommitted work. The log4net edits touch none of them. I did not re-run those 6 against a clean checkout, so this is a judgement from the diff, not a bisect. Known flakes were not seen.

Limitations:
- Label spellings are guessed. A file that uses other labels is not recognised and falls to the other importers or is Rejected.
- URL and user IP are recognised only to delimit blocks; they are not stored or shown.
- There is no application-log lane on the Investigation Timeline (lanes are audit, SQL and IIS). The file shows as a Sources traced chip; its events feed timeline.Build overlap, resource rules and evidence export.
- A text log has no machine name: the server refuses the file without a computer (ErrUnknownHost), the same as log4net-text, although the web catalog calls the computer "optional" for app logs. The e2e picks web-a. Not changed here.
- The log.txt name rule in fileKinds.ts is a convenience guess; the server still reads the content.
- go test -fuzz on the new importer stalls at 0 execs/sec in this environment, as it does for the existing importers, so it was not used as evidence.
- Screenshots: .dev/l4n-shots (drawer before/after, names, wizard card, timeline sources). Exe: .dev/run/l4n/audit-explorer.exe.

## Timeline gaps closed (2026-11-04)

Owner order: clear the remaining Investigation Timeline gaps against prototype/tracing-paper-PROTOTYPE.html, tested from the UI. The 6-month cap, its "up to 6 months" wording and the chunked 24h reads are unchanged.

| Gap | What changed |
|---|---|
| 1 One card | Quick range, both windows, the UTC readout, the Sources traced row, Add evidence and Refresh timeline now sit in one card (section.trctl). The 375px Sources traced indentation is fixed (grids use minmax(0,1fr); the UTC echo wraps under 720px). |
| 2 Readout and 12-hour boxes | A UTC readout sits under the pickers. Time boxes are three selects (hour 1-12, minute 00-59, AM/PM) over the same 24-hour value; typed 24-hour and 12-hour text is still read. Choosing "--" for hour or minute clears the whole time. |
| 3 Bar size | "Each bar = 1 minute" line from the drawn grid; longer periods say that they group more time into each bar. |
| 4 Suspects rail | Client-side (web/src/lib/tracing/suspects.ts, SuspectsRail.svelte), no second request. Beside the chart at 1280 (sticky 380px), below it at 375. Works for the live API-audit lane and the case-stored SQL and IIS lanes. Pinned Q2 plus top 5, See all, ruled-out list, Show on chart (focuses the bucket selector, shows the tooltip), honest empty states. Limits: counts only, first versus second half, so nothing is red and most cards are grey. |
| 5 Look here typed path | Not built, on purpose. It is the wizard Import files control (another agent's panel), not the Timeline, and the real app never reads a typed server-side path. |
| 6 TLPAR-06 | Settle fixed with expect.poll after two animation frames. AppHeader needed no change: it does not overflow at 375px. |
| 7 Other differences | Compared at 1280 and 375, light and dark (.dev/tlgap-shots/proto-* against timeline-*). |

Remaining differences from the prototype (not closed): Start tracing, Select all, Clear all and "Tracing all N sources" stay in the card (the prototype has only Add evidence and Refresh timeline); page heading chrome differs (All cases / Back to the case against Case home); the lower prototype sections (Overall figures tiles, What Unravis sees, Where does the evidence point, comparison rows, 4xx/5xx/HTTP.sys panels, P95 axis) are not on the Timeline screen and need server data; the chart is not inside a .card.chartcard and has no loading-reveal animation; the rail has no red tier because the timeline holds no call times.

Spec fixes caused by the new layout: TRACE-01 now expects six regions (suspects last); TRACE-07 scrolls the chart into view before the mouse drag.

Results:
| Check | Result |
|---|---|
| web npm run check | 0 errors, 0 warnings |
| web node --test tests/*.test.cjs | 2562 of 2562 pass (new: timelineE.suspects 14, timelineE.controls 6) |
| web build then distChunks.test.cjs | 2 of 2 pass |
| go build, go vet, GOOS=linux go vet | clean, cross-vet printed nothing |
| go test ./internal/server -p 2 -timeout 25m | ok |
| Playwright timeline-gaps (TLGAP-01..09) | 9 of 9 pass |
| Playwright investigate, timeline-parity, timeline-sources, tracing-screen, tracing-handoff, tracing-perf, large-window, partial-notices, bb-change, window-picker | first pass: 104 pass, 2 skipped, 3 fail (TRACE-01, TRACE-07, PICK-08); after the fixes tracing-screen, window-picker, timeline-gaps and bb-change: 51 of 51 pass |

Exe: .dev/run/tlgap/audit-explorer.exe. Screenshots: .dev/tlgap-shots.

## Source-side gaps closed

Agent A, branch feat/investigation-timeline, nothing committed. Exe used for every check: .dev/run/srcgap/unravis.exe (demo build). Pictures: .dev/srcgap-shots.

| Gap | What changed | Covered by |
|---|---|---|
| 1 Wizard files step (screens 37 and 38) | One card per file type with what it gives, where it sits and what it takes, a Choose files route, staged files, per-file result | wizard-cards, wizard-import-files, wizard-two-iis |
| 2 Case drawer Add data grid (screen 112) | Five cards, Files card says how many files the case holds, Another day card is on when a source can be read by day | import-grid (GRID-06 at 375 px, GRID-07 the enabled Another day route) |
| 3 Sources list | Document count and age per audit database (GET /api/sources/{id}/stats), inline Edit labels (PUT /api/cases/{id}/sources/{sourceId}), computer time zone edit for files that write local time | source-gaps SRCGAP-01..03, sources-list, hubfixE.sourcelist test, Go tests in audit, store, server, casestore |
| 4 Timeline after the drawer | A hand-picked list also switches on the file just added when the drawer closes. A switch past 50 is refused with a short note | source-gaps SRCGAP-04 and 05 |
| 5 Sideways scroll at 375 px | Wizard gallery step and the page behind the drawer do not scroll sideways; WCARD-05 and GRID-06 now assert it | wizard-cards, import-grid |
| 6 Screens and docs | screens.json: p1-create-files, p7-import-drawer, p7-sources-list, with pictures and text in docs/user/cases.md and sources.md. PENDING in docs-check is unchanged | docs-check: 13 pages, 94 screens |

Results:
| Check | Result |
|---|---|
| web npm run check | 0 errors, 0 warnings |
| web node --test tests/*.test.cjs | 2562 of 2562 pass |
| web build then distChunks.test.cjs | 2 of 2 pass |
| go build, go vet, GOOS=linux go vet | clean, cross-vet printed nothing |
| go test -p 2 audit, store, keeper, casestore, casestore/sqlite, server | all ok; TestA1_FolderRulesGiveM8AndChangeNothing fails only with the long temp path of the dev PC (path length 240) and passes with a short TEMP |
| Playwright sources-steps, sources-list, timeline-sources, wizard-cards, wizard-import-files, wizard-two-iis, import-grid, source-gaps | 71 of 72 pass on the first combined run; the one failure (GUIDE-21, now expects 1 found here) was fixed and sources-steps re-ran 19 of 19 |

Limits: Manage sources (screen 51) not built, the owner said Not now. A file whose time rule was fixed at import (no rule or no computer) cannot have its zone edited in place; the row offers Add files. The Import files heading is kept where the wireframe says Add files. SRCGAP-03 and SRCGAP-05 answer part of the data in the page because the demo has no MongoDB and few sources. The new pictures were made on a developer PC, not the reference renderer.

## Leftover gaps closed

Order: clear every gap against the wireframes and the prototype, test from the real UI, cover with e2e. Wireframe wording wins, except that the owner's 6-month cap wording stays and Manage sources is not built.

| Item | What changed |
|---|---|
| Failing tests | WCARD-01, FILES-02, FILES-07..10 all pass on a fresh exe (the earlier failures came from a stale dist at build time). WCARD-03 and FILES-02 now scope the disabled Add files check to the wizard's primary button, because the card button has the same name. TestA1_FolderRulesGiveM8AndChangeNothing builds its folder to fit a long TEMP path and passes with the default long TEMP |
| Add files | Heading and wizard label read Add files (fileKindsCopy, wizardSourcesCopy, AddDataPanel eyebrow removed). Specs updated |
| Wizard card and guide order | Per wireframe 37; app-log guide step 1 asks for the folder the app writes logs to |
| Drawer (screen 112) | Add data grid shows no gate notice and no case list; a Use another case link opens the list. The files view keeps the list |
| Files card, Blackbox, Another day | None yet / N found here, per wireframe |
| Sources list rows | Quiet sticker and quiet button styling in sources.css |
| App-log computer | Every app-log importer (log4net, serilog, nlog, mel) refuses a file with no computer, so fileKinds.ts marks app-log as host required; node tests cover needsHost, offersHost and hostProblem |
| Timeline | Back link reads Case home; Start tracing, Select all, Clear all and Tracing all N sources sit in the Sources traced box (Select all and Clear all inside the disclosure); Refresh timeline is secondary; chart sits in a card with a short reveal |
| Look here typed path | Not built. Wireframe 37 says Drop files or pick a folder; the typed path is prototype only and the server has no folder-scan endpoint |

Results:
| Check | Result |
|---|---|
| web npm run check | 0 errors, 0 warnings |
| web node --test tests/*.test.cjs | 2562 of 2562 pass |
| web build then distChunks.test.cjs | 2 of 2 pass |
| go build, go vet, GOOS=linux go vet | clean |
| go test -p 2 internal/server and internal/importer/... | all ok |
| Playwright, full suite on the fresh exe | 438 passed, 2 skipped (PERF), 0 failed |

## Gate 4 2026-11-05

Dirty working tree on feat/investigation-timeline (HEAD d34cc29), nothing staged, committed or fixed by the verifier. Fresh `go build` demo exe at .dev/run/gate4/audit-explorer.exe (built after the last source edit), web built first. Commit set: 122 paths in .dev/gate4-commit-paths.txt, checked on a throwaway index (5418 insertions, 318 deletions, no line-ending churn, no secrets or forbidden content).

| Stage | Result |
|---|---|
| Go build, vet, Linux cross-vet | clean, cross-vet printed nothing |
| go test -p 2 -timeout 25m ./... | 70 packages ok, 4 without test files, 0 failed |
| web npm run check | 0 errors, 0 warnings (447 files) |
| web node --test tests/*.test.cjs | 2562 of 2562 pass |
| web build then distChunks.test.cjs | 2 of 2 pass |
| Playwright full suite on the gate4 exe | 440 tests: 438 passed, 2 skipped (PERF-01, PERF-02 need a generated dataset), 0 failed, 0 flaky, 7.5 min |
| New tools/e2e/specs/gate4-break.spec.ts | 3 tests: G4-01 and G4-03 expected failures (test.fail), G4-02 test.fixme; exit 0 |

Flakes: none; no test needed a re-run. Failures: none.

Break-it pass on the gate4 exe (double clicks, Escape in every dialog, rapid source toggles, empty, garbage, huge and duplicate files, 12-hour picker edge input, Suspects rail by keyboard, back, forward and reload, 375 px sideways scroll on every screen, light and dark): no blocker. Every chip, All and None, calendar day and file kind select can be put back; a double click on a chip returns it to its start state; a 186-day window runs, a 187-day window is refused with the owner wording and keeps the old chart; hour or minute `--` keeps the picker open; a reversed or zero-length window is refused; across-midnight and one-minute windows are accepted; a 30 MB IIS log is recognised in about 3 s; a log4net file with no computer cannot be created (wizard Review keeps Create case off, drawer keeps Import off). No sideways scroll at 375 px on home, case home, wizard (all steps), drawer, Timeline, picker, Suspects See all and Add evidence. Minor defects, each with a test in gate4-break.spec.ts: G4-01 the same file chosen twice is listed twice and becomes two sources (the server then refuses the second as "Nothing new to add", so nothing is double counted); G4-02 a double click on Remove drops two files (judgement call, fixme); G4-03 a second Escape does not answer the drawer's Keep editing / Leave question.

Wireframe comparison (screens 37, 38, 112 and the prototype Timeline) at 1280 and 375 px, light and dark: no blocking difference. Remaining visible differences: the Timeline has no overall-figures strip, per-lane KPI chips or "What Unravis sees" panel of the prototype; the file result table shows the reason under the Result pill and ends with Open the timeline / Open the case / Add more files instead of Continue / Choose the files again / Remove rejected files; the Add data drawer is a slide-over, not a tab; the window help reads Up to 6 months by the owner's ruling.

Screenshots (wireframe, prototype and real app, light and dark, 1280 and 375 px, plus the break-it states): .dev/gate4-shots.

## Gate 4 fixes 2026-11-05

Web and e2e only (no Go). Items 1-7 and 9 were written on the remote hub (droid-az-fw-kimi-k3, job 37945), reviewed and applied; the rest was done here. Nothing committed or staged; the build dirties tracked web/dist and cmd/unravis-setup/ui/assets, which stay unstaged.

- Fixed: riseOf dates a rise from the whole series (an early onset reads slot 3, not 6); a failed collection read marks the database size unknown instead of dropping the part; the size lines sit in a Map ("constructor" no longer renders a function); Enter on an hour, minute or AM/PM drop-down no longer applies the window (text boxes still do); the AM/PM box has "--" and choosing it clears the time; SourceEdit focuses the first field on open and returns focus to the Edit button on close or save; the environment input is maxlength 60 (server limit); "Show on chart" also switches the suspect's lane on; connReady has its plural back (held is a count, so it is unchanged); the wizard shows one IIS guide (the card guide); the supported line no longer says to extract a zip (a .zip is opened for you).
- Gate 4: G4-01 (the same file chosen twice in the wizard is listed once, with a quiet "already in the list" note; matched by name, size and last-modified), G4-02 (a double click on Remove removes one file: clicks with detail above 1 are ignored), G4-03 (a second Escape on the Import drawer's leave question counts as Keep editing). The two test.fail lines are gone and G4-02 is no longer fixme.
- Plausible items: the bar-size line now says bars group more time whenever a bar holds more than a minute, as the axis caption does (it waited for a window over 24 h). Not changed: the chart focus effect (the grid only changes with a new response, which also clears the focus, so nothing is lost), the "See all (26)" count and the Local-zone metaline wording (tests lock the text and the reading is a wording choice, not a bug).
- Specs updated for the new behaviour: PICK-10 (Enter on a drop-down leaves the picker open and starts no read; Enter on the end date box applies), WCARD-02 (the auto-detect picker gets a differently named copy, since the same file twice is now listed once), FILES-01 (one IIS guide).
- Results: `npm run check` 0 errors; `node --test tests/*.test.cjs` 2577 of 2577 (new `gate4fix.test.cjs`, 7 tests); `npm run build` and `distChunks.test.cjs` 2 of 2. Playwright full suite against `.dev/run/fix2/audit-explorer.exe`: 439 passed, 2 skipped, 2 failed on the first run (PICK-10 and WCARD-02, both stale specs, fixed and re-run with the gate4, PICK and WCARD specs: 18 of 18 pass); DOC-02 and KEEP-09 print as failed and are the known-defect specs. No known flake failed.

## Final gate 2026-11-05

Dirty working tree on feat/investigation-timeline (HEAD d34cc29), after the Gate 4 fixes; the verifier fixed no product source. Web built first, then a fresh unstamped `go build ./cmd/audit-explorer` demo exe at .dev/run/final/audit-explorer.exe. Commit set: 127 paths in .dev/final-commit-paths.txt, checked on a throwaway index (6153 insertions, 331 deletions, no line-ending churn, no secrets; the only `password=` literals are obvious fake test fixtures).

| Stage | Result |
|---|---|
| web npm run check | 0 errors, 0 warnings (447 files) |
| web node --test tests/*.test.cjs | 2577 of 2577 pass |
| web build then distChunks.test.cjs | 2 of 2 pass |
| Go build ./..., vet ./..., GOOS=linux vet ./... | clean, cross-vet printed nothing |
| go test -p 2 -timeout 25m ./... | 70 packages ok, 4 without test files, 0 failed |
| Playwright full suite on the final exe (1 worker) | 443 tests: 441 passed, 2 skipped (PERF-01, PERF-02 need a generated dataset), 0 failed, 0 flaky, 7.9 min |

DOC-02 and KEEP-09 print as failed and are the known-defect specs, counted as passed. No known flake failed; nothing needed a re-run.
## rc.15 install smoke (2026-11-05)
- Silent upgrade rc.14 -> rc.15 ok (exit 0); Desktop and Start Menu shortcuts resolve to unravis.exe and launch it.
- Full e2e on the tree: 443 pass, 2 fail (AUDIT-06 stale spec, fixed and rerun green; SEC-07 environmental).

## rc.16 - wizard file tags paired to imports
TLWIZ-01/02 added: fail on the rc.15 server (warning 'A selected source has no file yet'), pass with server-side pairing. Regression subset (timeline, tracing, wizard specs) 85/85 pass; go test ./internal/server -run 'Tracing|PairFileTags' and ./internal/calltrace pass.

