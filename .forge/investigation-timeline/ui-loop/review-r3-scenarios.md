# UI loop review R3: scenario sweep of the Investigation Timeline

Reviewer: scenario reviewer subagent (round 3). Build: `.dev/run/g2/unravis-gate.exe --demo --no-browser` with a scratch config dir, `UNRAVIS_DEMO_NOW=2026-10-20T09:00:00Z`, HEAD 996aaed. In-app browser at 1280x900, 768x1024 and 375x812, light and dark.
No source edited, nothing committed. Findings were reproduced at least once. UI-R2-03 (closed-case Timeline button, owner decision) and UI-R2-13 (demo-only provenance time) are known and not repeated. Fixed R1/R2 findings are not repeated.
Failures and delays were injected with a fetch wrapper in the page (503 or delay on the tracing call).

| ID | Sev | Scenario | Expected | Observed | Evidence | Suspected file |
|---|---|---|---|---|---|---|
| UI-R3-01 | low | SQL panel title and lane data | The title promises only what the lane can show | The SQL panel title says "Slow database calls (over 1 s) and failed calls", but the SQL lane never carries error counts, so no SQL bar is ever shaded and no tooltip has an error line. The server leaves `errors` out for SQL on purpose. The wireframe (screenshot 08a) shows SQL bars with a rose error part, so either the title or the data is out of step | SQL lane buckets have no `errors` key; title text in the lane head | `web/src/lib/tracing/lanes.ts:25`, `internal/server/tracing.go` (`bucketLane`, errors only for IIS) |
| UI-R3-02 | low | Fully empty case (no imports), run the trace | Controls that need data are off or hidden, and the empty state says how to add files | Zoom in and the P50/P95 buttons stay enabled (P95 pressed), and "No latency figures were returned for this window" and "Drag across the chart to zoom into those bars." show with nothing to zoom. No hint to add files to the case. Residual of R1-18. In demo the empty case still shows the audit lane's global demo requests (demo only) | Empty case screen after Start tracing at 1280 | `TracingPaper.svelte`, `TracingChart.svelte` toolbar and hint |
| UI-R3-03 | low | Screen-reader audit of the chart region after Try again or Cancel | The element that receives focus has a role and an accessible name | The `.chart-region` focus target (`tabindex=-1`, added by R2 fix UI-R2-02) has no role and no name, so a screen reader announces nothing meaningful when focus lands there. The live region does not compensate for the error case | `read_page` shows an unnamed generic node as the focus target after Try again | `TracingPaper.svelte` (`keepFocusInChart`, chart region markup) |
| UI-R3-04 | low | Tooltip range wording, any bucket, UTC or Local | The time zone is named once | In Local the range repeats the zone three times, for example "... GMT+4 to ... GMT+4 (Asia/Dubai)". Residual of R1-11 (the UTC case was fixed) | Tooltip text in Local (Asia/Dubai) | `web/src/lib/tracing/tooltip.ts` |
| UI-R3-05 | low | 768 px touch targets | The 40 px rule from R2-08 holds up to tablet width | At 768 the Start tracing, Zoom, P50/P95, Back, All cases, More and Theme buttons measure 32-33 px; only the lane toggles, privacy pill and UTC/Local buttons got 40 px. At 375 the buttons are 44 px, so a tablet gets smaller targets than a phone. The datetime inputs are 34.8 px at 375 | Computed heights at 768x1024 and 375x812 | `TracingPaper.svelte`, `TraceToolbar.svelte`, shared button styles |
| UI-R3-06 | medium | Tap or click a bar at 375 to read its counts | Tapping a bar shows its tooltip and a second action opens the analysis | A click on a bar calls `hideTip()` and hands off to the analysis at once, so the tooltip never stays on screen for a tap. A touch user cannot read bucket counts without leaving the screen. Reproduced with a mouse click at 375; real touch events were not available, so the touch behaviour is inferred from the code | Click at 375: tooltip hidden, analysis opens; `onOverlayDown` hides the tip | `TracingChart.svelte` (`onOverlayDown`, `onOverlayClick`) |
| UI-R3-07 | low | Click a bucket that holds only SQL or IIS events | The analysis explains why there are no API requests | The hand-off lands on "No requests in the whole period." with no mention that the window had SQL or IIS events but no API audit rows | Hand-off from an IIS-only bucket in a case with no audit rows in that bucket | analysis hand-off copy, `TracingPaper.svelte` |
| UI-R3-08 | low | Keyboard-only: find the busy buckets | A way to jump to the next bucket that has events | Arrow keys step through every slot including empty ones, Home and End jump to the ends, and there is no jump to the next non-empty bucket. On 120 slots a keyboard user steps up to 119 times | Key walk on a 30-day window | `TracingChart.svelte` (`onOverlayKey`) |
| UI-R3-09 | low | Archived case, explanation text | The explanation says what the user cannot do, including the timeline | The case shows no Timeline entry and explains the case is read-only, but never mentions the timeline, so a user looking for it gets no answer | Archived case page text | case page archive notice |

Counts: 0 high, 1 medium, 8 low.

## Verified OK (R2 fixes in the real UI and new coverage)

- Striped error overlay (`.bar-err`) is legible in dark mode and light mode at 1280, 768 and 375.
- 503 error panel: red edge, icon, text, Try again re-sends the same window, and focus moves to the chart region (naming of that target is UI-R3-03).
- Escape during a brush drag cancels the zoom on release; Escape with a tooltip still hides the tooltip.
- Per-case window persistence: typed windows survive reload and switching between cases, each case keeps its own.
- Cancel no longer shifts the layout (note appears below the chart).
- Touch targets at 375: 40/44 px for the toggles, pill and buttons (inputs 34.8 px, see UI-R3-05).
- Local time zone with a brush zoom: the axis, caption and tooltips follow the zone and the zoom relabels correctly.
- Rapid case switching during a trace load, in two race setups (delayed response then switch, switch twice): stale responses are ignored, the screen always shows the case that was selected last.
- No horizontal overflow at 768 or 375, in light or dark. Dark mode at 768 and 375 renders cleanly.
- Keyboard: tab order is logical with no dead stops, chart arrow keys, Home, End and Enter work, the polite live region announces the tooltip, and Back from the analysis hand-off returns to the timeline with its windows.
- Archived case: no Timeline entry, read-only explanation shown (see UI-R3-09 for wording).
- No console errors in any run.

## Wireframe comparison (scenario 9)

Compared with `wireframe-match-tracing.md`, `wireframe-gap-triage.md` and `prototype/screenshots/08a-tracing-lane-charts.png`:
- Region order (picker, toolbar, chart, notes), lane order (API audit, SQL, IIS) and the shared axis match.
- Accepted deviations still hold and are not re-raised: bars use the accent token rather than per-lane colour; no range chips, comparison rows, 4xx or HTTP.sys panels, or P95 right axis.
- The screenshot's SQL panel shows rose error segments and the caption "rose errors"; the build has neither (UI-R3-01).
- The screenshot has a per-lane "1.0x the comparison period rate" line and an "Add evidence" button; not re-checked here (already covered by the accepted gap list).
- Pixel spacing was not compared.

## Not covered

- Real touch events (only mouse clicks at 375); UI-R3-06 is partly code-inferred.
- Archived case by deep link or history restore, and a case closed or archived in another tab while the timeline is open.
- Light mode at 768 and 375 for the full flow (dark was done in full).
- Pixel comparison against screenshot 08a.
- Real screen reader software; the audit used the accessibility tree from `read_page`.

## Fix results (round 3)

No commit made. Web only (`web/src`, `web/tests`, one e2e spec); no Go files touched.

| ID | Result | Files | Test |
|---|---|---|---|
| UI-R3-01 | skipped | none | skipped: title versus data is an owner or Go decision (the server leaves SQL errors out on purpose) |
| UI-R3-02 | fixed | `TracingPaper.svelte`, `windows.ts` (`windowHasNoData`, `WINDOW_TEXT.noData`) | `tracingWindows`, `tracingPaper`, `tracingChart` |
| UI-R3-03 | fixed | `TracingPaper.svelte` (`role="region"`, `aria-label`), `tracingCopy.ts` (`chartLabel`) | `tracingPaper` (TRACE-12 selector `[data-region=chart]` unchanged) |
| UI-R3-04 | fixed | `tooltip.ts` (`barTipLines` drops the zone word the start shares with the end) | `tracingTooltips`; e2e TRACE-10 text updated |
| UI-R3-05 | fixed | `app.css`, `TraceToolbar.svelte`, `TimeZoneToggle.svelte`, `TracingPaper.svelte` | `traceToolbar`, `tracingPaper` |
| UI-R3-06 | fixed | `TracingChart.svelte`, `tooltip.ts` (`isTouchPointer`, `tapAgain`) | `tracingTooltips`, `tracingChart` |
| UI-R3-07 | fixed | `handoff.ts` (`otherLanesNote`), `appState.svelte.ts`, `TracingPaper.svelte`, `App.svelte`, `ZoomStrip.svelte` | `tracingHandoff` |
| UI-R3-08 | skipped | none | skipped: the next-non-empty-bucket key is deferred |
| UI-R3-09 | fixed | `archiveCopy.ts` | `p3.archive` (copy lint), `p3.fix` |

Notes:
- UI-R3-02: the Zoom, P50/P95 and hint controls were already behind `hasData`; the reviewer saw them because the demo audit lane always has data. The fix is the one empty-state line, "Nothing was found in this window. Add log files to the case, or try a wider window.", shown when lanes came back and every bucket is empty (and not already covered by the "No events in this window" line).
- UI-R3-06: for a finger or pen (or an unknown pointer on a coarse screen) the first tap on a bar shows the tooltip and keeps it (it is not hidden on press or on the pointer leaving after the tap) with the line "Tap this bar again to open it in the analysis." when the bar has events. Tapping the same bar again opens the analysis. A mouse press and click is unchanged (one click hands off), so the HAND specs pass as before. Real touch events were not run in a browser; the behaviour is covered by source checks and the `isTouchPointer` unit test. A tooltip left by a tap goes away on another tap on a bar, a drag, Escape, or a mouse leaving.
- UI-R3-05: the 40 px rule now applies at 1024 px or less or on a coarse pointer, for Start tracing, Zoom, P50/P95, Back, All cases, More, Theme, the lane toggles, the privacy pill, the UTC/Local buttons and the datetime inputs. Not measured in a browser at 768 and 375 (CSS only; the phone rule of 44 px still wins at 720 px or less).
- UI-R3-07: the note is kept with the hand-off window and shows only while the analysis is on that same bar window, in place of "No requests in the whole period.".
- UI-R3-09: the archived sentence now reads "The analysis, the Investigation Timeline and the source checks are not available. You cannot add files. Restore the case first." (the first attempt at a single long sentence failed the reading-level lint).

Tests added or updated: `tracingTooltips`, `tracingHandoff`, `tracingWindows`, `tracingPaper`, `tracingChart`, `traceToolbar` (`web/tests/*.test.cjs`). E2E spec updated: `tools/e2e/specs/tracing-screen.spec.ts` (TRACE-10 range text).

Gate: `npm run check` 0 errors 0 warnings; `node --test tests/*.test.cjs` 2165 pass 0 fail; build and `distChunks` 2 pass. E2E TRACE/CASE/HAND against a fresh unstamped exe: 80 passed first run, TRACE-10 failed on the old range text (spec updated) and HAND-17 failed (flaky as before); both pass on re-run.
