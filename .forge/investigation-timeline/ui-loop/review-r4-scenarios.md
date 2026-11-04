# UI loop review R4: scenario sweep of the Investigation Timeline

Reviewer: scenario reviewer subagent (round 4). Build: `.dev/run/r4/unravis-gate.exe --demo --no-browser` (unstamped, `GOTOOLCHAIN=local`, `CGO_ENABLED=0`) with three scratch config dirs, `UNRAVIS_DEMO_NOW=2026-10-20T09:00:00Z`, HEAD 428be4c. In-app browser at 1280x900, 768x1024, 375x812 and 640x450 (stand-in for 200% zoom), light and dark.
No source edited, nothing committed. Findings were reproduced at least once. Known items are not repeated: UI-R2-03 (closed-case Timeline button, owner decision), UI-R2-13 (demo-only provenance time), UI-R3-01 (SQL title against data, owner decision), UI-R3-08 (next-busy-bucket key, deferred). Fixed R1 to R3 findings are not repeated.
Cases were seeded through the real import API (dummy data only): XSS strings in the title, file names, API paths and SQL file names; an 80-character unbroken title with API paths of about 250 characters; edge dates (2001, 2026-03-29, 2026-10-25, the 2026 year end, 2099); a 200,000-row IIS case spread over 30 days with 4,000 SQL events; small cases for the close-while-open runs. The seeded `demoarchived` and `democlosed` cases were used for the restore runs. The pane's time zone could not be changed, so a daylight-saving change was emulated by mapping Asia/Dubai to Europe/London in a patched `Intl.DateTimeFormat`.

| ID | Sev | Scenario | Expected | Observed | Evidence | Suspected file |
|---|---|---|---|---|---|---|
| UI-R4-01 | medium | Closed or archived case restored into the timeline: reload on the timeline (`unravis.caseView` = tracing), or Back/Forward to a timeline history entry, for `democlosed` and `demoarchived` | The user is returned to the case page, which explains the state, or the timeline opens read-only with a working way out | The timeline screen opens as a dead end. All four date inputs and Start tracing are disabled, and the only message is "This case is closed. You can read it, but nothing can be added." For an archived case that wording is wrong, because an archived case cannot be read at all and the R3 archive copy says the timeline is unavailable. "Try again" (shown in the error panel) does nothing: no request is sent and focus drops to BODY. The only exits are "Back to the case" and "All cases" | `inputsDisabled` all true, `start` disabled, message text as above; with a network log, "Try again" made no request; `document.activeElement` is BODY after Enter | `web/src/components/TracingPaper.svelte` (closed branch, error panel), `web/src/components/CaseHome.svelte` (line 399, `closed` and `readOnly` props), `web/src/lib/appState.svelte.ts` (`chooseFirstScreen`, lines 573-586) |
| UI-R4-02 | low | Windows that cross a year or sit far from now: 2026-12-31 22:00 to 2027-01-01 02:00, 2001, 2099 | The axis and tooltips make the year clear when it is not the current one or when the window spans two years | Axis labels and tooltips never print a year ("31 Dec 23:00", "1 Jan 00:00", "1 Jan 00:00" for 2001 and for 2099). A window that crosses a year boundary, and windows in 2001 and 2099, cannot be told apart from the current year by reading the chart | Axis text and tooltip text for the three windows contain no four-digit year | `web/src/lib/chartTime.ts` (`partsFormatter`, `axisLabels`, `chartRange`) |
| UI-R4-03 | low | Case closed from another tab (or the API) while the case page and then the timeline are open | The case page notices that the case closed, disables the Timeline button and shows the closed note, or the next action says so | The case page stays stale until a reload: the Timeline button stays enabled, no closed note appears, and the timeline still traces with the old state. A later trace works because tracing is read-only, so there is no wrong data, only a page that does not match the case | After `POST /api/cases/{id}/close` the page text and button state did not change until reload | `web/src/components/CaseHome.svelte` (`info` is loaded once), `appState.svelte.ts` |
| UI-R4-04 | low | Pixel comparison with `prototype/screenshots/08a-tracing-lane-charts.png` (1440x1000, light) | Layout widths and the banner style follow the wireframe | At 1440 the page column is 880 px against the wireframe's 1232 px, so the plot is 612 px against about 998 px and the lane bars are 54 px high against about 110 px. The note banner differs: a grey box with a 1 px border and two sentences, against the wireframe's beige tint with a thick amber left border and one bold line. The narrow column comes from `.case-home { max-width: 880px }`, which the timeline inherits | Measured plot, lane and column widths at 1440x1000; screenshot read side by side | `web/src/lib/components/CaseHome.svelte` (line 594), `web/src/components/TracingChart.svelte`, notes banner style in `TracingPaper.svelte` |
| UI-R4-05 | low | Axis labels in a 4 h window (and a zoomed window) | Labels sit on round clock times (for example 08:00, 09:00, 10:00) as `axisLabels` intends | Labels sit on every Nth slot, so the 4 h window shows 08:00, 08:50, 09:40, 10:30, 11:20 and the edge window 00:00, 00:55, 01:50. Readers cannot line a bar up with an hour. Counts and tooltips are right; only label placement is off | Axis text for 4 h windows in UTC and Local | `web/src/lib/tracing/canvas.ts` (`tickStep`, `tickOffsets`), `web/src/lib/chartTime.ts` (`axisLabels`) |
| UI-R4-06 | low | Open an archived case page and watch the console | No console errors | One "Failed to load resource: 409" error is logged when the archived case page loads (the ledger call). The page itself is fine. Not specific to the timeline | Console shows the 409 on each archived page load | the ledger request on the case page (`CaseHome.svelte` or its ledger loader); the Go ledger route for archived cases |
| UI-R4-07 | low | 375 px, light and dark, timeline picker | The datetime inputs fill the fieldset like the other controls | The datetime inputs are about 230 px wide inside a 346 px fieldset, so the two inputs of a pair stack with a gap on the right and do not line up with the buttons below them | Computed input width 230 against fieldset width 346 at 375x812 | `TracingPaper.svelte` window picker styles |
| UI-R4-08 | low | Timeline screen with several cases | The screen names the case it is tracing | The timeline screen shows "Investigation Timeline" and "Back to the case" but never the case title or id, so with several cases a user who restored the screen by reload cannot tell which case is being traced. The long title and the XSS title only appear on the case page and the case list | Text of the timeline screen at 1280 contains no case title (checked for the 80-character title) | `TracingPaper.svelte` header, `CaseHome.svelte` (line 399) |

Counts: 0 high, 1 medium, 7 low.

## Verified OK

- Light mode at 768 and 375, full flow (list, case page, timeline, run, hover, zoom, hand-off, back): no horizontal overflow, touch targets 40 px or more, layout clean. The R3 tablet touch-target fix holds.
- Security: the title, file name, API path and SQL file name XSS strings render as text; `window.__xss` stayed undefined in every run; no `innerHTML` injection was found in tooltips or lane heads. The only value in sessionStorage besides the active-case, view and window keys is the session token, which is by design. No token, launch code or cookie appears in any console message or in the page URL.
- Closed while open: the timeline keeps tracing (read-only) and nothing breaks; only the stale case page remains (UI-R4-03).
- Edge windows: year boundary, 2001, 2099, zero-length window (refused with the start-before-end message), 30 and 31 day windows (31-day comparison refused), spring-forward on 2026-03-29 and fall-back on 2026-10-25 with the zone changed. Across a change the tooltip names both zones ("01:55 GMT+1 to 01:00 GMT+0 (Asia/Dubai)" when the end falls after the change), and bucket stepping after the change is correct. No NaN, no crash, no console error.
- Long text: an 80-character unbroken title wraps (`overflow-wrap: anywhere`) on the case page and in the case list at 375 and 1280 with no overflow. The timeline tooltips carry no API paths (only counts, ranges and provenance), so very long paths cannot overflow them. A 250-character path in the lanes does not widen the page.
- Browser zoom 200%, emulated by a 640x450 viewport: no horizontal overflow, plot 546 px, every control at least 40 px high, the picker and toolbar remain reachable and the header stays on one row.
- prefers-reduced-motion: `app.css` (line 695) has a global rule that sets `animation-duration`, `transition-duration` and `scroll-behavior` to zero, and the Timeline files declare no motion of their own, so the rule covers the screen. The pane cannot emulate the media query, so this is a source check only.
- No console errors in any run except the archived-page 409 (UI-R4-06).

## Perf numbers

Case: 200,000 IIS rows spread across 30 days (four 50,000-row files, with a busy day at 12 Oct) plus 4,000 SQL events. Window: the 30-day default (120 buckets of 6 h). Browser pane at 1280x900, measured in the page, server on loopback.

- Time to first chart: about 1.5 s from Start tracing to the painted lanes. The tracing responses took 1.4 s each (about 60 KB), the audit response 0.25 s (18 KB). No long tasks (over 50 ms) were recorded on the page.
- 20 re-runs on the same window: 1.46 to 1.73 s each (median about 1.6 s), no console errors, DOM node count constant at 728 before and after.
- Memory over the 20 re-runs: used JS heap rose from 18.3 MB to 22.3 MB (about 0.2 MB per run, no forced garbage collection available). It does not look like a leak of DOM or chart objects (nodes stay constant), but a plateau was not proven.
- Tooltip hover (keyboard and pointer): median 4.6 ms, p95 5.6 ms, max 6.5 ms per move.
- Zoom in 5 to 8 ms, zoom out 5 to 8 ms; a drag-brush gesture 195 ms in total with a worst single move of 6 ms; the zoom started no new request (the data was already in the page).
- Axis relabel after zoom is immediate and reads "Showing 34 of 120 bars." for the sample brush.

## Wireframe comparison (against 08a, 1440x1000, light)

- Region order (picker, toolbar, lane charts, notes) and lane order (API audit, SQL, IIS) still match. The accepted deviations remain accepted and are not re-raised: bars use the accent token, no per-lane colour, no range chips, comparison rows, 4xx or HTTP.sys panels, P95 right axis, per-lane comparison rate line or "Add evidence" button.
- Measured differences: page column 880 px against 1232 px, plot 612 px against about 998 px, lane bar area 54 px against about 110 px, banner style (beige tint, thick amber left border, one bold line, against a grey box, 1 px border and two sentences). These are UI-R4-04.
- Type sizes, lane head wording and the colour of the error stripes were not compared at pixel level.

## Not covered

- Real touch events (only mouse clicks and keyboard).
- Real browser zoom at 200%: a 640x450 viewport was used, so text scaling and the hidden-pane timing differences were not tested.
- prefers-reduced-motion was checked in source only; the pane cannot emulate the media query.
- A true system time zone change: daylight saving was emulated with a patched `Intl.DateTimeFormat`, so server-side zone handling was not tested.
- The SQL-XE warning "Events from sql-xe cannot be tied to one source because two selected files use it." was not seen on the timeline screen; whether the UI should show it is an open question.
- Residual of R1-03 (zoom focus after repeated zoom in on a very short window) was not re-measured.
- A one-off `scrollWidth` of 486 px at 375 was read right after a viewport change from 1280 on the timeline screen; it did not reproduce on the next measurement (375) and is treated as a stale layout read, not a finding.
- Real screen reader software.
- The archive API call was not run on a seeded case (it needs a case-file folder and passphrase); the seeded `demoarchived` case was used instead.

## Fix results (round 4)

No commit made. Web only (`web/src`, `web/tests`); no Go, no `web/dist`, no e2e spec edited. UI-R4-03 was not in scope (deferred).

| ID | Result | Files | Test |
|---|---|---|---|
| UI-R4-01 | fixed | `CaseHome.svelte` (an `$effect` closes the timeline when the case turns out archived; the timeline branch is `tracingOpen && !isArchived`), `TracingPaper.svelte` ("Try again" only when the case is not closed) | `tracingRound4.test.cjs`: archived restore returns to the case page; closed case keeps its read-only timeline with no Try again |
| UI-R4-02 | fixed | `chartTime.ts` (new `needsYear`), `TracingChart.svelte` (`showYear`, passed as `year` to `formatTime` for axis and tooltip text) | `tracingRound4.test.cjs`: `needsYear` cases (other year, year change, zone-dependent year, bad input); labels and tooltip lines with and without the year |
| UI-R4-04 | fixed | `CaseHome.svelte` (`.case-home.wide { max-width: 1232px }` only while the timeline is open), `TracingChart.svelte` (`.notice`: amber left border, warn tint on the surface, bold title) | `tracingRound4.test.cjs`: wide class only on the timeline; notice has the amber edge and tint |
| UI-R4-05 | fixed | `canvas.ts` (`tickOffsets` takes an optional alignment and prefers round clock slots, falls back to every Nth), `TracingChart.svelte` (passes start, slot width and zone) | `tracingRound4.test.cjs`: hours, non-round first slot, half hours, 6 h and day marks, Asia/Kolkata, fallback, right-edge rule, London clock change |
| UI-R4-06 | fixed (frontend only) | `ledgerStore.svelte.ts` (`setLedgerCase(caseId, read = true)`), `appState.svelte.ts` (no eager ledger read when a stored case view is restored), `LedgerCount.svelte` (no mount fetch on the case screen) | `tracingRound4.test.cjs`: source checks for the three guards |
| UI-R4-07 | fixed | `TracingPaper.svelte` (phone block: `.window-set` full width, date inputs `width: 100%`) | `tracingRound4.test.cjs`: phone CSS block |
| UI-R4-08 | fixed | `CaseHome.svelte` (passes `title`), `TracingPaper.svelte` (`<p class="case-name" data-testid="tp-case-name">`, `overflow-wrap: anywhere`, text only) | `tracingRound4.test.cjs`: title passed, rendered as text, wraps |
| UI-R4-03 | skipped | none | none (deferred as briefed) |

### Notes

- UI-R4-01: a closed case still opens its read-only timeline (UI-R2-03 is an open owner decision); only the useless "Try again" is gone. The archived case is sent back by `closeTracing()`, which also fixes the history entry, so both reload and Back/Forward are covered.
- UI-R4-02: `needsYear` reads the real clock, so in demo mode (`UNRAVIS_DEMO_NOW` in October 2026) years appear only if the real date leaves 2026. The year shows when the window is not in the current year or spans two years.
- UI-R4-04: the banner text is unchanged; only its style moved toward the wireframe (tokens only, both themes through `color-mix`). The plot height is not changed.
- UI-R4-05: the e2e `.axtick` assertions in `tools/e2e/specs/tracing-screen.spec.ts` (for example `19 Oct 15:00 UTC`, `19 Oct 15:20 UTC`, `/^19 Oct 20:30 /`) were not rerun and may be affected by round ticks. No spec was edited.
- UI-R4-06: this removes the 409 on the reload path and for the header badge. A direct open of an archived case page by any other route that reads the ledger was not traced. The Go route is unchanged.
- Nothing was checked in a browser; the fixes are covered by source tests, TypeScript logic tests and `svelte-check` only.
- Line endings: working-tree files are CRLF under `core.autocrlf=true` (index LF), unchanged from before.

Tests and gate (from `web/`): `npm run check` 0 errors, 0 warnings (409 files); `node --test tests/*.test.cjs` 2181 pass, 0 fail (new file `web/tests/tracingRound4.test.cjs`, 16 tests).
