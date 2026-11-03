# UI loop review R2: scenario sweep of the Investigation Timeline

Reviewer: scenario reviewer subagent (round 2). Build: `.dev/run/scn/unravis-gate.exe --demo --no-browser --port 0` with a scratch config dir, `UNRAVIS_DEMO_NOW=2026-10-20T09:00:00Z`, in-app browser at 1280x900, 768x1024 and 375x812, light and dark (dark measured mainly at 1280).
No source edited, nothing committed. Every finding was reproduced at least once. Fixed R1 findings are not repeated; UI-R2-01 is a residual of R1-01.

Seeded cases (scratch only, imported through the real import API): S1 all three lanes (IIS 148 rows, SQL 21, HTTP.sys 2, 5xx at 10:20 and 10:30), S2 IIS only, S3 empty, S4 spike (60,000 IIS rows plus 30 singles, 3,001 SQL events).
Failures and delays were injected with a fetch wrapper in the page (503 on the tracing call, delayed response).

| ID | Sev | Scenario | Expected | Observed | Evidence | Suspected file |
|---|---|---|---|---|---|---|
| UI-R2-01 | medium | S1, hover the IIS bar at 10:20 that has a darker 5xx part | Tooltip gives the error count and the 5xx explanation, as the audit lane does | The darker overlay is drawn, but the tooltip says only "17 requests" (errors = 3 in the API bucket). No error line, no 5xx wording. Residual of R1-01: the bars were fixed, the tooltip was not | S1 IIS bucket 10:20-10:25: API `errors` 3, tooltip text has no "server errors" line | `web/src/lib/tracing/tooltip.ts` (`barTipLines` only adds errors when `input.key === 'audit'`) |
| UI-R2-02 | medium | Keyboard: activate "Try again" on the 503 error, and Cancel during a re-run | Focus stays on or moves to a sensible control (the retry result, the chart, or the Start button) | After Enter on "Try again" the button is removed and focus drops to BODY (reproduced twice). After Cancel focus also drops to BODY, so a keyboard user has to tab from the top of the page again | `document.activeElement` is BODY after both actions | `web/src/components/TracingPaper.svelte` (error and loading branches) |
| UI-R2-03 | medium | Closed case: open the case page, look at the Timeline entry | A closed case can still be read, so the timeline stays openable read-only, or the reason matches what the button does | The Timeline button is disabled with the text "You can read it, but nothing can be added". The timeline adds nothing, so the sentence contradicts the disabled state. The reason is plain text next to the button with no `aria-describedby`. Owner decision WEB-R1-25 is still open, listed here so it is not lost | Closed case in S-closed run: button `disabled`, no description link | case page timeline entry component; `progress.md` WEB-R1-25 |
| UI-R2-04 | low | Dark and light, IIS panel with 5xx, compare overlay to bar | The "errors shown darker" overlay is clearly different from the bar | Overlay `--bad` against the bar colour is 1.14:1 in dark and 1.43:1 in light. In dark the overlay is a lighter coral, so "darker" is by hue only and a thin overlay is hard to see. Not a text-contrast failure (text and notes pass 4.5:1) | Contrast of overlay against `.bar` background measured in both themes | `TracingChart.svelte` (`.bar-err`), `web/src/brand/tokens.css` |
| UI-R2-05 | low | Drag the brush, then press Escape before releasing | Escape cancels the selection | Escape only hides the tooltip. The drag cannot be cancelled and releasing still zooms | `onOverlayKey` Escape branch is gated on `tipSlot != null` | `TracingChart.svelte` (`onOverlayKey`, `endDrag`) |
| UI-R2-06 | low | S4, truncation note | The note names the lane that was cut | The note says "The list of events was cut at its limit..." without saying which lane. With three panels the reader cannot tell if it is audit, SQL or IIS | S4 notes region text | `TracingPaper.svelte` notes / `tracingCopy.ts` |
| UI-R2-07 | low | Keyboard Tab order | Every tab stop does something | The three lane labels (`role=group`, `tabindex=0`) are still tab stops with no action (R1-15 half-fixed: Home, End and Enter feedback were fixed, the dead stops were not) | Focus walk: three lane labels, then the chart overlay | `TracingChart.svelte` (lane label markup) |
| UI-R2-08 | low | 375 px touch targets | At least 32 px | Lane toggles and the privacy pill are 32 px now, but the UTC/Local toggles are still 28 px. At 768 the lane toggles and pill are 29 px, lower than at 375 (32 px) | Measured at 375x812 and 768x1024 | header toggle component, `TraceToolbar.svelte` |
| UI-R2-09 | low | API 503 on the tracing call | A clearly styled error with a Retry; the old chart stays, or the window stays editable | The message is plain `role=status` text with no error styling (no colour, no icon) and it replaces the whole chart region. "Try again" works and re-sends the same window | Injected 503: chart region replaced by one sentence and a button | `TracingPaper.svelte` (error branch) |
| UI-R2-10 | low | Type a year 0001 date in a window box | A message that names the bad date | Message is "needs a start and an end" (treated as empty). Misleading, since both are filled in | Start = 0001-01-01 00:00 | `web/src/lib/tracing/windows.ts` |
| UI-R2-11 | low | Type a custom window, then reload | Typed windows survive reload, or reload is stated to reset them | Reload now returns to the timeline (R1-14 fixed) but the typed windows reset to the defaults and the result is gone | Typed 19 Oct 09:00-11:00, reload, defaults shown | `TracingPaper.svelte`, `windows.ts` |
| UI-R2-12 | low | Cancel while a re-run is in progress | The page stays still | The Cancel message appears above the old chart and shifts the chart down by about 25 px | Chart top before and after Cancel differs by 25 px | `TracingPaper.svelte` |
| UI-R2-13 | low (demo only) | Demo clock | Lane provenance and default window follow the demo reference time | Provenance reads "Read just now - 3 Nov 22:5x UTC" (real clock) while the header says 20 Oct 09:00. At 768 the header puts the demo reference time under "More". Same root as R1-19 | S1 lane heads | `windows.ts`, lane head copy |

## Verified OK (R1 fixes and new coverage)

- R1-02: every non-empty bucket in the S4 spike now has a visible bar (2 px minimum).
- R1-04: Cancel keeps the old chart and shows a message (placement shift is UI-R2-12; focus is UI-R2-02).
- R1-05: the SQL lane note is correct per lane; truncation is reported per lane, and an uncut lane shows none.
- R1-06: no layout jump on re-run, the veil keeps the chart height.
- R1-07, R1-08: the incomplete note no longer distorts the axis; lanes with "no file for this source" show no provenance.
- R1-09: window alerts are tied to the inputs and use the UI's own words (start >= end, 31-day comparison, missing values, invalid dates all refused with nothing sent).
- R1-10: the axis labels use comma formatting.
- R1-13, R1-14: Back, Forward and reload keep the timeline (history entries are stamped `uvScreen = case/tracing`).
- R1-15: Home and End jump to the first and last bucket; Enter on an empty bucket gives feedback; Escape hides the tooltip.
- R1-17: the plot is now 281 px at 375 and 644 px at 768; no horizontal overflow at 375 or 768.
- R1-19 style fix of the IIS error bars: darker bars render in S1 and S2 on the right buckets.
- Drag-brush zoom works left to right, and right to left with real multi-move pointer events. The axis relabels and "Showing N of M bars" updates. Zoom is announced.
- Window end is exclusive and the picker says so; a 31-day comparison window is refused.
- Text contrast is at least 4.5:1 in light and dark, including the incomplete note and the error overlay text. Focus rings are visible.
- TRACE-16 from the wireframe match doc is fixed: the empty tooltip box is `display: none` with zero size when no tip shows.
- No console errors in any run.

## Wireframe comparison (S12)

Compared with `wireframe-match-tracing.md` and `wireframe-gap-triage.md`:
- Region order (picker, toolbar, chart, notes), lane order (API audit, SQL, IIS) and the shared UTC axis match.
- Known accepted deviations still hold: bars use the accent token, not the per-lane colour; no range chips, comparison rows, 4xx panel, HTTP.sys panel or P95 right axis. Not re-raised.
- The "HTTP.sys panel" caveat stands: the S1 lane carried 2 HTTP.sys rows that the screen counts into the IIS bucket total but draws nowhere (see R1-11 note on "14 requests").
- No new visual mismatch against the written wireframe notes; pixel spacing against screenshot 08a was not compared.

## Not covered

- Archived case: no archived case is listed in the demo, so the archive branch (HAND-14) was not exercised; the closed-case branch was.
- A fully empty case: the demo audit lane always returns requests, so "all lanes empty" was only seen on S3 for SQL and IIS.
- SQL error shading ("failed calls" never produced darker bars with the seed used).
- Real mouse hover and click at 375 and 768 (touch-style input was not emulated beyond viewport size).
- Dark mode at 768 and 375 (measured at 1280 only; light was used for the narrow passes).
- Local time zone combined with the brush.
- Screen-reader testing.

## Fix results (web fixer, Nov 4)

No commit made. Web only (`web/src`, `web/tests`, one e2e spec); no Go files touched.

| ID | Result | What changed |
|---|---|---|
| UI-R2-01 | fixed | `barTipLines` now adds the "N server errors (5xx)" line and the 5xx explanation for the IIS lane too (same dictionary text as audit). |
| UI-R2-02 | fixed | Try again and both Cancel buttons go through `keepFocusInChart`: when the keyboard was inside the chart region, focus moves to that region (`tabindex=-1`) after the button is removed. |
| UI-R2-03 | left as is (owner decision) | Only added `aria-describedby` from the disabled Timeline button to the reason text. No behaviour or copy change. |
| UI-R2-04 | fixed | `.bar-err` is now a diagonal stripe of `--bad` and `--bg`, so it differs from the bar by pattern and lightness, not hue alone. The IIS panel title says "errors shown striped". A test checks `--bg` against each lane colour at 3:1 or more in all three theme blocks. |
| UI-R2-05 | fixed | Escape during a brush drag cancels it (no zoom on release); the existing Escape-hides-tooltip path is unchanged. |
| UI-R2-06 | fixed | `truncatedNote(lanes)` names the cut lane(s); the generic sentence stays as the fallback. |
| UI-R2-07 | fixed | Lane labels are no longer tab stops. They keep `role=group`, an accessible name and `aria-describedby` to the hint; the hint shows on hover. |
| UI-R2-08 | fixed | At 768 px or less, or a coarse pointer, lane toggles, the privacy pill and the UTC/Local buttons are 40 px high. |
| UI-R2-09 | fixed | The 503 state is an error panel (red edge, icon, same sentence in `role=status`, Try again). |
| UI-R2-10 | fixed | A typed date that cannot be used (year 0001 and the like) is named: "The incident window start (value) is not a date that can be used. Check the year." An empty box keeps the old sentence. |
| UI-R2-11 | fixed | Typed windows are kept per case in sessionStorage after the first edit and restored on open. Malformed data or blocked storage falls back to the defaults. |
| UI-R2-12 | fixed | The Cancelled note moved to the notes region below the chart, so the chart no longer shifts. |
| UI-R2-13 | skipped | skipped: the "as of" time in lane provenance is supplied by the server (Go code, out of scope for this pass) and is demo-only; faking it in the browser would show a time the data does not have. Left for a Go-side demo-clock fix. |

Tests added or updated: `tracingTooltips`, `tracingChart`, `traceToolbar`, `tracingWindows`, `tracingPaper` (`web/tests/*.test.cjs`). E2E spec updated: `tools/e2e/specs/tracing-screen.spec.ts` (IIS panel title, lane hint on hover and no tab stop, focus after Try again).

Gate: `npm run check` 0 errors 0 warnings; `node --test tests/*.test.cjs` 2156 pass 0 fail; build and `distChunks` 2 pass. E2E TRACE/CASE/HAND against a fresh unstamped exe: 80 passed, 1 failed (HAND-17 column-sum check). HAND-17 is flaky and not caused by this change: it failed 4 of 4 and passed once against the older reviewer build (`.dev/run/scn/unravis-gate.exe`) too, and passed 1 of 2 against the new build. It measures column widths right after the first paint.
