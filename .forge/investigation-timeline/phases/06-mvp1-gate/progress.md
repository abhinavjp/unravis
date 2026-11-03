# Phase 06 MVP 1 gate: progress record (WP-58, steps 1 to 10a)

Run date: 2026-11-04 (local), repo root `D:\Projects\api-audit-explorer`, PowerShell. Steps 11 (browser walk) and 12 (verdict line) are NOT in this section; they are added after the browser walk. Wall times are information only (INV-5). No source file was edited, nothing committed, `build.ps1` not run, tray app not started, no `.unravis.db` created.

Go environment for every Go command: `GOTOOLCHAIN=local`, `CGO_ENABLED=0`, `GOCACHE/GOMODCACHE/GOPATH` under `.dev\toolchains\cache` (`build`, `mod`, `gopath`); go = `.dev\toolchains\go\bin\go.exe`.

## Step 1: What is under test
- Branch: `feat/investigation-timeline`
- `git log --oneline -8`:
  - 76a42c8 Background run phase 07: tray app, single-instance, quit, start-at-sign-in, installer shortcuts, applog tee, docs
  - 3a3fe96 Investigation timeline phase 05: bar hand-off to analysis, guided unlisted, e2e suites for the new screen, chart click/label/colour fixes
  - 6e31516 Investigation timeline phase 04: tracing UI (chart, toolbar, scrub pill, zoom/brush, latency line), audit lane, demo-clock UTC fix
  - dc431dc Investigation timeline phase 03: tracing API, sql/iis lanes, bucket counts, HTTP.sys markers, demo case
  - db99a01 Investigation timeline phases 01-02: IIS privacy, scrub-off export, revision-checked confirm, tooltip dictionary
  - 94ea98e Installer: window fills at any size, own browser profile so 1024x768 is honoured, Unravis icon on the setup page and in unravis-setup.exe / unravis.exe
  - a2ede7d Reword import banner (no internal codes, old wording still accepted), installer window 1024x768, fix stale screenshot tests
  - 3bdd3ea Fix flaky pidstat test: dump JSON instead of %+v so pointer addresses cannot match a banned string
- Lines of `git status --short` (before the web build): 323 (information only, tree is not clean; many unrelated modified files)
- Last line of `git diff --stat -- web internal tools cmd` (before the web build): ` 34 files changed, 21 insertions(+), 1591 deletions(-)`; the same line after all steps: identical (` 34 files changed, 21 insertions(+), 1591 deletions(-)`). Note: git printed LF/CRLF warnings for the modified files; they are not errors.
- Phase 04, 05 and 07 commit file lists (from their `phase.md`) with `git status --short -- <those paths>`: printed nothing for each of the three (all committed). Result: PASS. 0.2 s.

## Step 2: Web
- `cd web; npx vite build`: exit 0 (3.5 s). Only the usual "some chunks are larger than 500 kB" warning.
- `npm test`: exit 0 (10.5 s). `# tests 2107`, `# pass 2107`, `# fail 0`, `# cancelled 0`, `# skipped 0`, `# todo 0`.
- `npm run check`: exit 0 (7.9 s). `COMPLETED 409 FILES 0 ERRORS 0 WARNINGS 0 FILES_WITH_PROBLEMS`.
- Result: PASS. (The build dirtied tracked `web/dist` and `cmd/unravis-setup/ui/assets/index.css`; expected, not committed or reverted.)

## Step 3: Go
- `go build ./...`: exit 0, no output (2.9 s).
- `go vet ./...`: exit 0, no output (0.9 s).
- `go test ./... -count=1`: exit 0 (124.1 s). 72 output lines: 69 `ok`, 3 `[no test files]`, 0 `FAIL` lines.
- Result: PASS.

## Step 4: `.unravis.db` check
- `git ls-files -- "*.unravis.db"`: printed nothing.
- `Get-ChildItem -LiteralPath D:\Projects\api-audit-explorer -Recurse -Force -Filter *.unravis.db ... (excluding node_modules and .forge)`: printed nothing.
- `git diff master...HEAD -- '*.go' '*.ts' '*.svelte' | Select-String 'unravis\.db'`: PRINTED 3 LINES (the packet expects nothing). All three are in one new e2e spec, `tools/e2e/specs/tracing-screen.spec.ts`, TRACE-14 (lines 445, 474, 479 at HEAD): the test title text "... no .unravis.db", the comment "INV-4: no case-store file named .unravis.db was created while tracing." and the assertion line `if (name.toLowerCase().endsWith('.unravis.db')) found.push(name);`. They are a canary that asserts the file is absent; no Go, Svelte or product TypeScript path creates or opens such a file (the only matches in all three globs are in that spec). By the letter of the pass rule ("all three print nothing") the third command is NOT empty: recorded as a literal deviation for the owner to rule on; the run continued because the matches are test-only. 

## Step 5: Branch exe
- `go build -o .dev\run\scn\unravis-gate.exe ./cmd/audit-explorer` (after the step 2 web build, plain build, unstamped): exit 0 (1.3 s). Size 52,152,832 bytes, written 2026-11-04 00:17:52. Left in place for the browser walk.
- Result: PASS.

## Step 6: Main e2e
- `cd tools/e2e; powershell -File run-e2e.ps1 -Exe ..\..\.dev\run\scn\unravis-gate.exe`: exit 0 (243.9 s wall; Playwright 4.0 m).
- Printed `329 passed (4.0m)`.
- `report/results.json` stats: `expected 329`, `unexpected 0`, `flaky 0`, `skipped 0`; duration 242,137 ms.
- `tools/e2e/known-defects.json` still lists only `DOC-02` and `KEEP-09`.
- Result: PASS.

## Step 7: Guided leftovers
- `git grep -n -i "Open guided analysis" -- tools/e2e/specs`: no output. Result: PASS.

## Step 8: Archive inventory
- `cd tools/e2e; npx playwright test --list -c playwright.archive.config.ts`: exit 0 (1.3 s). Last line: `Total: 80 tests in 7 files`. Result: PASS.

## Archive (known gap)
(Step 9, informational only; does not decide pass or fail.)
- `powershell -File run-e2e.ps1 -Archive -Exe ..\..\.dev\run\scn\unravis-gate.exe`: exit 1, 4,694 s wall (about 78 min; most failures wait out a 1 minute timeout each).
- Printed `77 failed`, `3 passed (1.3h)`. `report/archive.json` stats: `expected 3`, `unexpected 77`, `skipped 0`, `flaky 0`.
- The phase 05 baseline line "Archive baseline before WP-64: 80 passed" is NOT present: `.forge/investigation-timeline/phases/05-guided-unlisting/progress.md` does not exist (that directory holds only `phase.md` and the WP files), so there is no baseline line to sit next to. Recorded as a fact; the baseline is stated only in the WP-63 text (expected 80 passed).
- Expected cause: after WP-64 the case home has no Guided button the archived specs click (plan section 10.4, owner decision pending). The failure cause of the 77 was not investigated here.

## Step 10: WP-45 and WP-59 outputs
- `go test ./internal/importer/iis/ -run "TestVerifyNotes|TestVerifyW3CTimeIsEndMatchesNote" -count=1 -v`: exit 0 (1.9 s). `--- PASS: TestVerifyNotes`, `--- PASS: TestVerifyW3CTimeIsEndMatchesNote`, `ok`.
- `go test ./internal/importer/sqlserver/ -run TestQuerystoreMicrosecondsToMilliseconds -count=1 -v`: exit 0 (1.8 s). `--- PASS: TestQuerystoreMicrosecondsToMilliseconds`, `ok`.
- VF-01 statuses from `internal/importer/iis/verify_notes.go` (reasons: `noSampleReason` = no sample / paired capture on this machine; `liveServiceReason` = "needs a live Mongo or SQL Server and a deliberate read-only check; not a file sample"):

| id | status | reason |
|---|---|---|
| VF-01a | UNMEASURED | noSampleReason |
| VF-01b | UNMEASURED | noSampleReason |
| VF-01c | UNMEASURED | noSampleReason |
| VF-01d | UNMEASURED | noSampleReason |
| VF-01e | UNMEASURED | noSampleReason |
| VF-01f | UNMEASURED | liveServiceReason |
| VF-01g | UNMEASURED | liveServiceReason |
| VF-01h | UNMEASURED | noSampleReason |

- `internal/importer/sqlserver/querystore.go` line 42: `//	VF-01h: UNMEASURED. no real sample or paired capture on this machine (looked in UNRAVIS_VF01_SAMPLES and .dev/samples/vf01); the division above is kept as coded.` Status agrees with `Vf01h` (UNMEASURED, noSampleReason). The `noSampleReason` constant text was not printed by the packet's Select-String, so "word for word" was compared on status and reason identity only; `Vf01h` is not REFUTED.
- Measuring tests (`-run TestVerifyMeasure -v`, exit 0): `TestVerifyMeasureW3C` SKIP, `TestVerifyMeasureHTTPERR` SKIP, `TestVerifyMeasureQuerystore` SKIP, all with "no VF-01 samples: set UNRAVIS_VF01_SAMPLES". All eight claims stay UNMEASURED. The `sqlserver` package has no `TestVerifyMeasure*` test ("no tests to run"). Skips are not a failure.
- Result: PASS.

## Step 10a: Phase 07 record
- `docs/background-run-acceptance.md` exists.
- Last line: `ACCEPTANCE: AUTOMATED PASS, MANUAL PENDING`
- Automated rows by result (counted from the file): table "Automated" A1 to A9: 9 PASS, 0 FAIL. "A5 detail": 26 rows, 25 PASS, 1 `PASS with note`, 0 FAIL. "Automated proof per packet": 35 rows, 34 PASS, 1 not a PASS/FAIL value (WP-77, result cell reads "see the proof lines in the packet", the file's own proof). Totals over the 70 automated rows: 68 PASS, 1 PASS with note, 1 WP-77 self-referential, 0 FAIL.
- Manual rows: M1 to M23, all 23 `PENDING - NOT RUN` (recorded by id, do not fail the gate).
- Result: PASS (no FAIL in automated rows; last line is not `ACCEPTANCE: AUTOMATED FAIL`).

## Summary of steps 1 to 10a
Steps 1, 2, 3, 5, 6, 7, 8, 10, 10a passed. Step 4: two of three checks print nothing; the third prints 3 lines that are all in the TRACE-14 e2e canary (see above), a literal deviation awaiting the owner's ruling. Step 9 informational: 3 passed, 77 failed. Step 11 (browser walk) and the verdict line follow.

## Step 4 ruling
Step 4 ruling: the 3 `unravis.db` matches are in tools/e2e/specs/tracing-screen.spec.ts TRACE-14 (title, comment, an assertion that no *.unravis.db file exists); no product code matches; accepted.

## Step 11: wireframe walk
Walk date 2026-11-04. App: unstamped `.dev\run\scn\unravis-gate.exe --demo --no-browser --port 0` with a scratch `AUDIT_CONFIG_DIR` under `%TEMP%`, `UNRAVIS_DEMO_NOW=2026-10-20T09:00:00Z`, `UNRAVIS_DEMO_CASES=1` (never the real `%APPDATA%\APIAuditExplorer`; no `--background`, no tray app, no `build.ps1`). In-app browser, demo case "Orders API stall, 19 October", Investigation Timeline screen, windows WIN_A (19 Oct 15:00-18:00, audit has 11 bars) and WIN_B (19 Oct 08:00-12:00, all three lanes have bars, 48 columns). Viewports 1280x900 and 375x812, light and dark (colour scheme emulation). Wireframes: `prototype/screenshots/08-tracing.png`, `08a-tracing-lane-charts.png`, `09-analysis-from-tracing.png`, `13-narrow-tracing.png`. The in-app browser's own time zone is Asia/Dubai; the screen is UTC by default and all times below are UTC.

What worked as designed (observed): picker with four UTC boxes and "Times are typed in UTC."; row toggles API audit / SQL / IIS in lane order; scrub pill "Default: export hides IP, referer and cookie hash" expands to "This is the setting the next evidence export will use. Nothing saved in the case is changed here."; mixed-time note present, no close button; lane heads with plain question and origin line; hover tooltip on a SQL bar shows lane, range, count and "From saved logs (as of 19 Oct 09:10 UTC)"; Zoom in gives "Showing 24 of 48 bars.", Reset zoom clears it; a drag over the SQL panel zooms to "Showing 10 of 48 bars." and re-labels the axis; an end-before-start window gives "The incident window ends before it starts." and sends nothing; empty lanes say "No events in this period" with no bars; the hidden tooltip box is gone (`.tip` is `display: none`, 0x0); a click on an audit bar (09:15-09:20 slot, count 4) opens the analysis and, after Run analysis, it shows "Showing 19 Oct 09:15:00 to 09:20:00 UTC" with 4 requests. Per-lane bar colours are drawn (audit green, SQL purple, IIS amber) in the screenshots, which is better than the triage's C row says (the audit bar computes to the same value as `--lane-audit`); not a defect.

Network (read_network_requests over the whole walk): every request went to the app origin `http://127.0.0.1:<app port>`; no other host. Six failed `GET http://127.0.0.1:58231/` attempts at the start are my own scratch redirect helper on a reserved loopback port (refused; loopback only, not part of the app). Four `POST /api/client-log` show `204 ... ERR_ABORTED` (the page navigated while the beacon was in flight; benign). Console: no messages, no errors.

Not reproduced by hand: the store-unavailable error sentence and "Try again", Cancel while reading, the no-sources sentence (all covered by TRACE-12 and TRACE-13 in the step 6 run, 329 passed); real P50/P95 values (the server sends none); the red error-share shading on bars (the demo window has no 5xx); keyboard-only use.

### Differences table

| Element | Wireframe | Built | Class | Severity | Evidence |
|---|---|---|---|---|---|
| Comparison-period rows (dashed line, "1.0x the comparison period rate") | 08a, 08 | not drawn | accepted-B | n/a | known |
| 4xx / 5xx split panels; error share as red band | 08a | no split; 5xx would be darker bars only (demo has none) | accepted-B | n/a | known |
| HTTP.sys panel, blocking-process panel | 08a | not present | accepted-B | n/a | known |
| Suspects panel, "What Unravis sees" | 08 | not present | accepted-B | n/a | known |
| Range chips (Last 15 min ... Last 30 days) | 08 | four typed UTC boxes only | accepted-B | n/a | known |
| Sources chips (API audit - Payroll API audit, ...) | 08, 13 | three row toggles (API audit, SQL, IIS) | accepted-B | n/a | known |
| "Trace again" button label | 08, 08a, 13 | "Start tracing" | accepted-B | n/a | known |
| Reference window shown as a date-range button with duration | 08 | two plain datetime-local boxes titled "Comparison period (UTC)" | accepted-B | n/a | same row as the range chips (typed fields) |
| P95 right axis lacks a `0` tick; latency line | 08a, 09 | no right axis; selector "Typical (P50) / Slow end (P95)" with "No latency figures were returned for this window." in every window tried | minor-C | low | known; server sends no p50/p95 |
| Per-lane bar colours | 08a (lane tokens) | lane colours ARE used (audit green, SQL purple, IIS amber; 1280 light and dark) | resolved (was minor-C) | n/a | the triage row is out of date |
| HTTP.sys markers (diamonds) on lanes | 08a | not drawn | minor-C | low | known |
| Axis tick labels at 1280 px | 08a: labels spaced apart | six labels, each about 88 px wide, spaced about 71 px apart, so neighbouring labels overlap by 16-17 px ("19 Oct 08:40 UTC" runs into "19 Oct 09:20 UTC"; "UTC" and "19 Oct" visibly touch). Same in light and dark, WIN_A and WIN_B. At 375 px only two labels show and they are clean. | NEW | medium | text ranges of `.axtick` measured, gaps -16, -17, -16, -17, -17 px; screenshots at 1280 light and dark |
| Chart width at 1280 px | 08a: plot about 80% of the content width | content column 880 px, lane label column 317 px, left axis 49 px, so the plot is 429 px (49%); a blank gap sits beside the short lane text. This is the cause of the tick overlap above. | NEW | medium | measured `.lane-label` 317 px and `.panel` 429 px at innerWidth 1280 |
| Lane head: event total ("1042 events"), "Events" status tag, "Bars: ... line: ..." legend | 08a, 13 | lane head has name, question and origin line only; the panel title says what is plotted | NEW | low | 1280 light and 375 |
| Case title and "Layout A . Report" subtitle under the screen title | 08 | screen title "Investigation Timeline" only | NEW | low | 1280 light (the wireframe subtitle is partly prototype chrome, so arguably B) |
| Bucket caption "5 minutes buckets . 36 on the axis . window 3 h" above the chart | 08, 13 | the equivalent sentence is below the chart ("Time (UTC). Each bar covers 5 minutes, ...") | NEW | low | 1280 light |
| Hand-off banner "Opened from Tracing. Incident ... reference ... [Back to tracing]" and a "Compare in Tracing" button on the analysis | 09 | none of these. The analysis opens with the previous run's figures and "Filters changed. Click Run analysis to use the new window."; Run analysis then shows the clicked bar's window correctly; the only way back is "This case" or "All cases" | NEW | medium | 1280 light, click on audit bar 09:15-09:20; page text has no "Tracing", "Timeline" or "Opened from"; no phase 01-05 packet asks for it (WP-61 only sets the window and opens the analysis) |
| Narrow layout (375 px) | 13: lane label beside a small chart | lane heads stack above their chart; no sideways scroll (scrollWidth 375); the chart starts 75 px from the left (axis column) and ends 47 px short of the right edge; empty lanes draw a plain box without axis | NEW | low | 375 light and dark; a reasonable adaptation, listed for the owner |
| Light and dark themes | 08a (light only) | dark theme legible: bars, toolbar, pill, note, tooltip all readable | same | n/a | 1280 and 375 dark |

### Step 11 counts
NEW defects: high 0, medium 3 (tick-label overlap at 1280, plot only 49% of the content width, no hand-off banner or way back to the timeline), low 4 (lane head lacks event total, tag and legend; no case subtitle; caption below the chart; 375 px layout adaptation). No NEW high-severity defect.
Cleanup: the exe, the scratch redirect helper and the scratch config dir were stopped and removed; no source file was touched.

## Verdict
Steps 1 to 3, 5 to 8, 10 and 10a passed; step 4 accepted by the owner ruling above; step 9 is informational (3 passed, 77 failed, known gap); step 11 shows no NEW high-severity defect.
GATE PASS
