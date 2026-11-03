# Wireframe match: Investigation Timeline screen

Sources: `prototype/tracing-paper-PROTOTYPE.html`, `TRACING-SCREEN-EXPLAINER.md` section 5, `screenshots/08a-tracing-lane-charts.png`, WP-23 "Wireframe check".
Spec: `tools/e2e/specs/tracing-screen.spec.ts` (TRACE-01 to TRACE-16), run against `unravis-gate.exe`, demo case "Orders API stall, 19 October".
Result: 15 pass, 1 `test.fixme` (TRACE-16, real defect, see below).

| Property | Wireframe says | Checked by | Result |
|---|---|---|---|
| Region order | picker, toolbar, chart, notes | TRACE-01 | pass |
| Picker boxes | incident then comparison, four UTC date-times, "Times are typed in UTC", one start button | TRACE-01 | pass |
| Way back | "Back to the case", focus returns to the open button | TRACE-01 | pass |
| Row toggles | three buttons API audit, SQL, IIS, all on, in lane order | TRACE-01, 05 | pass |
| Lane count and order | three lanes: API, SQL, IIS | TRACE-02, 05 | pass |
| Lane names and questions | plain names plus one plain question each | TRACE-02 | pass |
| Panel titles | what each panel plots | TRACE-02 | pass |
| Origin line | "Read just now" / "From saved logs" with as-of time | TRACE-02, 04 | pass |
| One shared UTC axis | same columns for every lane, ticks in UTC, caption "Time (UTC)..." | TRACE-02, 04, 15 | pass |
| Bar columns | 36 columns for 3 h at 5 min; 48 for 4 h | TRACE-02, 04 | pass |
| Audit lane | 11 event bars; tallest fills the plot; axis 0 to max | TRACE-03 | pass |
| Empty lane | "No events in this period", no bars | TRACE-03, 12 | pass |
| Lane colours | lane swatch and tooltip swatch equal the `--lane-*` tokens (computed style) | TRACE-04 | pass |
| Mixed-time note | shown, cannot be dismissed | TRACE-02 | pass |
| Hide / show rows | removes the panel, keeps order, "All rows are hidden" sentence, no new request | TRACE-05 | pass |
| Zoom controls | in, out, reset, disabled at the ends, "Showing N of M bars", keys + - 0, no new request | TRACE-06 | pass |
| Brush | drag selects bars, zooms, axis re-labels, not a click | TRACE-07 | pass |
| Scrub pill | export setting in plain words, expandable, never says "masked" | TRACE-08 | pass |
| Latency line | P50/P95 selector, line only when data exists, max label, `--text` stroke | TRACE-09 (mocked data) | pass |
| Tooltips | range, count, origin, IIS caveat, keyboard use, lane hints | TRACE-10 | pass |
| Tooltip hidden state | nothing drawn when no tip | TRACE-16 | FAIL (defect) |
| Window refusals | plain sentences, nothing sent; overlap only warns, one-click fix | TRACE-11 | pass |
| Error and cancel | store-unavailable sentence + Try again; Cancel while reading | TRACE-12 | pass |
| No sources | "No files have been added..." | TRACE-13 | pass |
| Privacy | no IP, cookie, query string, user agent; no `.unravis.db` | TRACE-14 | pass |
| Local time switch | labels move, window and data stay | TRACE-15 | pass |

## Defect found (not fixed)
`.tip` in `TracingChart.svelte` sets `display: grid`, which overrides the `hidden` attribute, so an empty bordered box stays on the chart when no tooltip shows. TRACE-16 is `test.fixme`.

## Deviations from the prototype (need an owner's eye)
- Bars use the one accent token, not the lane colour the prototype uses; lane colour is on the swatch only. Not asserted.
- Prototype has Range chips, Sources chips, "Trace again", comparison rows, 4xx / 5xx / HTTP.sys panels, "What Unravis sees", suspects, a P95 right axis. Absent in this build.

## Could NOT be checked automatically
- Error-share (`--bad`) shading on bars: the demo has no 5xx in the window.
- Pixel spacing, type sizes, row heights, alignment against screenshot 08a.
- Contrast and look in light and dark themes; 375 px layout (the hand-off spec covers some).
- Real P50/P95 values (server sends none yet; only a mocked answer).
- Wording quality and tone of the plain sentences.
