# Phase 04: tracing-ui

risk: medium (WP-17a is a high-risk packet: it is the only place the audit store is read for this feature)

## Goal
Add the Investigation Timeline screen: a case-home entry, a window picker, one stacked three-lane chart on a UTC floor-aligned axis with plain missing/partial wording, the lane toggles with the real export scrub pill, and chart controls (zoom, reset, P50/P95). It is read-only, additive, and leaves the per-call tray (`CallTrace.svelte`) and the Guided code untouched (INV-7, INV-8).

## Packets

Waves and dependencies live only in plan.md §3 and in each packet header (`wave:`) and `## Depends on`; check-plan.ps1 fails if a phase.md table carries them.

| Packet | Title |
|---|---|
| WP-10 | Trace toolbar: lane toggles and scrub pill |
| WP-11 | Mount the toolbar in the timeline screen only |
| WP-16a | Tracing client, types and shell copy |
| WP-16b | Tracing shell and case-home entry |
| WP-17a | Audit lane: one bucketed read per source |
| WP-17b | Stacked three-lane tracing chart |
| WP-17c | Tracing chart tooltips from the dictionary |
| WP-22 | Window picker with overlap and length warnings |
| WP-23 | Chart controls: zoom, reset, hidden rows, P50/P95 line |

Notes for the executor: WP-10 does not touch the chart packets and meets them only in WP-11; WP-17a is the Go read and needs WP-15's and WP-18b's handler, not the web packets. The spec packets `WP-16` and `WP-17` were each split in two (16a/16b, 17a/17b), and WP-17c (tooltips) was added because no spec packet carried TR-22 for the chart. WP-17b (four files) and WP-23 are at the upper bound of size M and are accepted unsplit (round 2 ruling); if a run overflows, WP-17b delivers `lanes.ts` and the chart first and WP-23 delivers zoom/reset/`visible` before the latency line (WP-23 says so), and the caller opens a follow-up packet. WP-22 no longer waits for the audit read or the tooltips: it edits only the `windows` region of `TracingPaper.svelte`.

## Phase proof
Run from the repo root unless noted.
```
go build ./... && go vet ./internal/server
go test ./internal/server -run 'TestAuditLane|Tracing' -count=1
cd web && npm run check
cd web && npm test
cd web && npm run build && node --test tests/distChunks.test.cjs
cd web && node --test tests/tracingModel.test.cjs tests/tracingPaper.test.cjs tests/tracingChart.test.cjs tests/tracingTooltips.test.cjs tests/tracingWindows.test.cjs tests/traceToolbar.test.cjs tests/traceToolbarMount.test.cjs
git ls-files -- "*.unravis.db"
rg "TraceToolbar" web/src --glob "*.svelte" -l
rg "callTrace|guided" web/src/components/TracingPaper.svelte web/src/components/TracingChart.svelte web/src/components/TraceToolbar.svelte web/src/lib/tracing
```
Expected: build and tests pass; `git ls-files` shows no `.unravis.db` file (and no code path creates one, per the plan.md section 7 check); the `TraceToolbar` search lists only `TracingPaper.svelte` and `TraceToolbar.svelte`; the last search prints nothing. `git diff --stat -- web/src/components/CallTrace.svelte web/src/lib/callTrace.ts web/src/lib/guided` is empty.

## Additions to plan.md section 7
2. Browser (dev server from `.claude/launch.json`, seeded demo case, in-app browser; never print its token):
   - Walk: case home -> new "Investigation Timeline" block (after Analysis, before Guided, Guided still listed) -> open -> loading with Cancel -> chart; Back to the case returns focus to the open button. Then: edit windows (31 days refused in plain words and no request in the network log; overlap warns and Start tracing stays enabled), toggle each lane, zoom by drag and reset, switch UTC/Local in the header (labels move, window unchanged), a closed case (button disabled with the closed sentence), a locked key (plain unlock sentence).
   - Accepted deviations from the prototype (the lane contract has no data for them; do not fail the visual check on these): the comparison-period rows, the 4xx/5xx splits (only the 5xx share is drawn, as darker bars on the API row), and the HTTP.sys and blocking panels. The prototype's floating tooltip is present (WP-17c) but only with the lines the real data supports.
   - Compare with `prototype/tracing-paper-PROTOTYPE.html` at 1280 px and 375 px, light and dark: row order API requests, Database, Web server; lane colours; the width caption; the mixed-time note (cannot be closed); the sentences for no-data, out-of-range, clock-unchecked and incomplete lanes; toolbar wraps at 375 px; no sideways scroll. Keyboard once: Tab order (lane heads show their hint on focus), bucket selector arrows/Enter/Escape, `+`/`-`/`0`. Hover a bar, then a lane head: the tooltip text comes from the dictionary and an incomplete lane's bars end with its plain reason.
   - Console must be clean; network shows one `POST /api/cases/{id}/tracing` per run, no repeat on lane toggle or zoom, and no request for a refused window.
3. Security canaries: the tracing response and the rendered page contain no raw cookie, server IP, query string or user agent (INV-3); `lane.events` is never rendered here; no scrub value in storage, URL or request body; the scrub pill never says stored data is masked; the screen sends no write request (it only POSTs the read-only tracing route); `CaseTools.svelte` export behaviour unchanged.
4. Performance: at most 120 bars per lane (assert on the 30-day window at 00:00 -> 6 h and at 00:01 -> 8 h, 91 bars); the audit lane is one `$match`+`$group` per source (the `TestAuditLaneOneAggregateCall` test), never raw paging; no ECharts import in the tracing files; chunk-cycle test passes; record the bundle delta of the new lazy tracing chunk in `progress.md`. No timing assertions.
5. Clumsy-user review: open and close rapidly, double-click Start tracing, change the window while loading, Cancel then retry, reload mid-load, back button, reversed window, 30 days + 1 minute, a window with no sources (empty-case sentence), a closed case, a stale answer arriving after a newer request, hide all three lanes, zoom to the minimum and reset, `scrubSensitive` missing from the preview. Write tests for what breaks.
6. Diff review: stale-response guard (`requestSeq`), no cohort summing in the web, the audit read cannot widen beyond the request, `reason` text never contains raw error text, copy lint, no import of `guided` or `callTrace`. High-risk packet WP-17a gets a second (Opus) reviewer.

## Commit file list
Stage these explicit paths only (the working tree has many unrelated changes; never `git add -A`):
- `web/src/lib/tracing/model.ts`
- `web/src/lib/tracing/lanes.ts`
- `web/src/lib/tracing/windows.ts`
- `web/src/lib/tracing/toolbar.ts`
- `web/src/lib/tracing/canvas.ts`
- `web/src/lib/tracing/tooltip.ts`
- `web/src/lib/copy/tracingCopy.ts`
- `web/src/lib/exportScrub.svelte.ts`
- `web/src/components/TracingPaper.svelte`
- `web/src/components/TracingChart.svelte`
- `web/src/components/TraceToolbar.svelte`
- `web/src/lib/components/CaseHome.svelte`
- `web/src/lib/components/CaseTools.svelte`
- `web/tests/tracingModel.test.cjs`
- `web/tests/tracingPaper.test.cjs`
- `web/tests/tracingChart.test.cjs`
- `web/tests/tracingTooltips.test.cjs`
- `web/tests/tracingWindows.test.cjs`
- `web/tests/traceToolbar.test.cjs`
- `web/tests/traceToolbarMount.test.cjs`
- `internal/server/tracing_audit.go`
- `internal/server/tracing_audit_test.go`
- `internal/server/tracing.go`

Commit on the feature branch only (plan.md section 5). Push, merge and tag need a fresh ask. Never add `.unravis.db`.
