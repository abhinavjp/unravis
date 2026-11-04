# UI loop review R5 (final): scenario sweep of the Investigation Timeline

Reviewer: scenario reviewer subagent (round 5). Build: my own unstamped exe from HEAD 6e04474, run with `--demo --no-browser --port 0`, a scratch config dir, `UNRAVIS_DEMO_NOW=2026-10-20T09:00:00Z`, `UNRAVIS_DEMO_CASES=1`. In-app browser at 1440x1000, 1280x900, 768x1024 and 375x812, light and dark.
No source edited, nothing committed. Every finding was reproduced at least once. Seeded cases (scratch only, through the real import API): an XSS and 80-character title case, years 2001 and 2099, a cross-year case (31 Dec 2026 to 1 Jan 2027), a mixed October case and a 30-day case; plus the demo cases (Orders API stall, closed, stale, archived).
Not re-reported (known or open): UI-R2-03, UI-R2-13, UI-R3-01, UI-R3-08, UI-R4-03. Fixed R1 to R4 findings are not repeated.

| ID | Sev | Scenario | Expected | Observed | Evidence | Suspected file |
|---|---|---|---|---|---|---|
| UI-R5-01 | medium | Open the case page, activate "Open the Investigation Timeline" with a mouse click and with Enter | The new screen takes focus (heading or chart) and the document title names the screen, as the case page and the analysis do | `document.activeElement` is BODY after the screen opens, so a keyboard or screen-reader user starts at the top of the page with no announcement. `document.title` is "Unravis" on home, case and timeline alike. Back to the case does return focus to `#home-tracing-open`; only the forward move is missing. Reproduced twice (click and keyboard) | `document.activeElement.tagName` is BODY after open; title unchanged on every screen | `web/src/components/TracingPaper.svelte` (no focus on mount, only `keepFocusInChart`), title handling in `appState.svelte.ts` / `App.svelte`; compare `CaseHome.svelte` (`headingEl` focus) |
| UI-R5-02 | medium | Run a window of 7, 10, 14 or 30 days (UTC and Local, 1440, 768, 375) and read the axis labels | The R4 fix gives round clock-time labels at every window size | 1 to 6 days and a bucket-aligned 30 days are round. Multi-day windows still step in whole buckets (a step of N slots), so labels are odd: 7 days from 13 Oct 00:00 gives "13 Oct 00:00, 14 Oct 04:00, 15 Oct 08:00, 16 Oct 12:00, 17 Oct 16:00, 18 Oct 20:00" (28 h steps); 14 days gives 56 h steps; 10 days 40 h steps; 30 days from 20 Sep 09:17 gives "20 Sep 09:00, 25 Sep 17:00, 1 Oct 01:00" (128 h steps); 7 days from 13 Oct 09:17 gives "13 Oct 09:00, 14 Oct 15:00, 15 Oct 21:00" (30 h steps). At 375 the 7-day window gives "13 Oct 00:00, 16 Oct 04:00" and 30 days "20 Sep 00:00, 3 Oct 06:00". At 768 the 7-day step is 34 h. 21 days gives 12 h multiples, which is acceptable. Reproduced at 1440, 768 and 375 | `.axtick` label text read per window | `web/src/lib/tracing/canvas.ts` (`tickOffsets` / `tickStep`), `web/src/lib/chartTime.ts` |
| UI-R5-03 | low | Start screen: type the code of an archived case (listed under "Saved cases") into "Case code" and press Enter | A message that tells the user the case is archived and where to find it | "That code does not match a case on this computer." The server answers 409 `case-archived` ("Archived cases are in the Archived list on the start screen.") but the screen shows the generic not-found line. The single sentence for all bad codes is by design ("the screen never says which"), so this is a design-versus-clarity conflict for the owner; a user who owns the case is told it does not exist. Reproduced once | `GET /api/cases/{archived}` is 200 with state archived; imports call is 409; UI text is the not-found sentence | `web/src/lib/copy/enterCopy.ts` (`CODE_NOT_FOUND`), open-by-code logic on the start screen |
| UI-R5-04 | low | Closed case: reload while the saved screen state is timeline (`unravis.caseView=tracing` for a closed case) | The R4 archived fallback also applies to closed cases, or the timeline is read-only and shows what exists | The timeline opens for the closed case: windows greyed, Start tracing disabled (no `aria-describedby`), no chart, and a red panel "This case is closed. You can read it, but nothing can be added." There is nothing to read, so the sentence misleads. The case page Timeline button is disabled for the same case (R2-03 decision), so this is a reachable dead end the button was meant to prevent. Reproduced on two closed cases (closed and stale demo cases) | Screen after reload: disabled Start, red panel text, no lanes | `web/src/components/TracingPaper.svelte`, screen-restore logic in `appState.svelte.ts` / `CaseHome.svelte` |
| UI-R5-05 | low | Lane head at 1440 and 768, compare with wireframe 08a | The colour swatch sits inline beside the lane name | The swatch is a block dot on its own row above the lane title (computed `display: block`, title about 12 px lower). `.swatch` is `inline-block` with `margin-right`, but the parent `.lane-label` is `display: grid`, so the dot becomes a grid row. The wireframe shows it inline left of the name. Reproduced at 1440 and 768 (also visible at 375) | Computed display of `.swatch` and y offset of the `strong` | `web/src/components/TracingChart.svelte` (lines 498-499, `.lane-label` and `.lane-label .swatch`) |

Counts: 0 high, 2 medium, 3 low.

## Verified OK (round 4 fixes and new coverage)

Round 4 fixes:
- Case name header: a title with `<img onerror>`, `<script>` and 80 characters renders as text (0 img elements, `window.__xss` undefined). It is 1232 px wide at 1440 and wraps to three lines at 375 with no overflow.
- Wider timeline column: 1232 px with the wide class at 1440; the case page stays 880 px.
- Year on the axis and in tooltips: correct across 31 Dec 2026 / 1 Jan 2027, and for years 2001 and 2099.
- Round clock-time ticks for 15 minutes to 6 days, and for 30 days when bucket-aligned: at most 6 labels, no overlap, no clipping, UTC and Local, also after a brush zoom (the multi-day exception is UI-R5-02).
- Archived case: reload, Back and Forward land on the case page; no 409 and no console error when opened from "Saved cases"; no Timeline button.
- 375 datetime inputs: 321x40 inside a 347 px fieldset; no buttons under 40 px at 375; no horizontal overflow at 375, 768 or 1440 in light or dark.
- Amber notes banner is legible in light and dark.

Accessibility:
- Contrast (computed): light, notice text 15.64:1, notice edge 4.84:1, axis text 7.25:1, lane note 6.81:1; dark, notice text 15.03:1, notice edge 10.33:1, axis text 7.62:1, lane note 6.89:1. The error stripe colour passes 3:1 against the page background in both themes.
- Tab order is logical with no dead stops; every control shows a 2 px solid accent focus ring; the new header has no focusable element.
- Landmarks: header, main, the section named by its h2, and the "Timeline chart" region. Headings are H1 "Unravis", H2 "Investigation Timeline". Live regions present: a role=status line, the polite `.zoom-note`, and the polite atomic region for tooltips. (Focus on open: UI-R5-01.)

Regression sweep and clumsy-user:
- Case list, case page, timeline run, hover, brush zoom ("Showing 12 of 60 bars"), hand-off to the analysis (4 requests, 09:17-09:18) and browser Back (windows kept, chart re-run) all work.
- Real hover shows the tooltip, including the "1 server error (5xx)" line on the IIS bar.
- Double-click on Start tracing sends one request.
- Resize (375 then 768) and a theme switch during a 3 s delayed load end in a clean render without overflow.
- Editing a window and then Back and Forward keeps the edited value (sessionStorage).
- Rapid switching over 5 cases always shows the last chosen case's name and a correct axis.
- Light and dark at 768 and 375: clean layout, three lanes, no horizontal overflow, no small buttons (the main-flow screenshots at 768 light and dark and 375 light and dark showed no clipping).
- "Local(Asia/Dubai)" label matches the wireframe style ("Local(UTC)").
- No console errors in any run.

## Wireframe comparison (08a at 1440)

Compared with `prototype/screenshots/08a-tracing-lane-charts.png`:
- Region order, lane order, shared axis and IBM Plex Sans typography match (h1 16/600, h2 19.5/600, notice title 14/900, axis ticks 11, lane notes 13).
- Swatch placement differs (UI-R5-05).
- Accepted deviations, not re-raised: lane head wording (question text and "Read just now" provenance instead of "Events" chip, count and "1.0x comparison" line), a two-line cream notice with an amber edge instead of a one-line bold banner on tan, striped error overlay instead of rose segments, per-lane bar colours.
- P50 and P95 stay enabled while the toolbar says no latency figures came back (residual of R3-02, not new).
- Pixel spacing was compared by eye only, not by overlay.

## Not covered

- Real touch events (mouse only; touch behaviour of tap-twice relies on the R3 source checks).
- Real screen reader software; the audit used the accessibility tree and computed styles.
- Time zone other than the pane's Asia/Dubai; `prefers-reduced-motion`.
- Local-time labels for 7 to 30 day windows were only read at 375 for non-aligned windows (UI-R5-02 is confirmed in UTC at all three widths).
- A pixel-diff overlay against 08a.
- Case closed or archived in another tab (UI-R4-03 is known).

## Fix results (round 5)

No commit made. Web only (`web/src`, `web/tests`); no Go, no e2e spec edited. UI-R5-03 was not in scope (owner decision).

| ID | Result | Files | Test |
|---|---|---|---|
| UI-R5-01 | fixed | `TracingPaper.svelte` (h2 gets `tabindex=-1`; `onMount` focuses it once, only when nothing else holds focus), `CaseHome.svelte` (one `$effect` sets the tab title: "Investigation Timeline - Unravis" or "<case name> - Unravis"; cleanup restores "Unravis"), new `lib/docTitle.ts` | `tracingRound5.test.cjs`: heading focus source checks, `setDocTitle` set and restore; `tracingWindows.test.cjs` mount pattern updated |
| UI-R5-02 | fixed | `canvas.ts` (new `axisTicks`: windows of a day or more use 3/6/12 hour marks, then midnight every 1, 2, 3, 4, 7, 14 ... calendar days; `tickOffsets` kept as a whole-slot wrapper), `chartTime.ts` (`dayTicks`, `wallToMs` follows a clock change), `TracingChart.svelte` (labels placed at the time, not the bar edge) | `tracingRound5.test.cjs`: 7/10/14/30 days aligned and from 09:00 in five zones (UTC, Dubai, Kolkata, London, New York), London and New York clock changes, expected UTC dates, 1 to 3 day marks |
| UI-R5-04 | fixed | `CaseHome.svelte` (`isClosed`, `timelineShown`; the same `closeTracing()` fallback as archived). Case page Timeline button unchanged (UI-R2-03 stays open) | `tracingRound5.test.cjs`; `tracingRound4.test.cjs` patterns updated |
| UI-R5-05 | fixed | `TracingChart.svelte` (swatch and name in a `.lane-title` flex row inside the grid; no other row moved) | `tracingRound5.test.cjs`: markup and CSS checks |
| UI-R5-03 | skipped | none | none (owner decision) |

### Notes

- UI-R5-02: bars sit on an hour grid that rarely starts at midnight (for example 2 hour bars from 09:00), so multi-day labels are no longer tied to bar edges: each label names the round time and sits where that time falls (a fraction of a bar from the edge). Under one day nothing changed (labels stay on bar edges). A 7 day window at 1440 now reads 13, 15, 17, 19 Oct 00:00; at 375 (280 px plot) 13 and 17 Oct. The first label is the first round time inside the window.
- UI-R5-01: the heading takes focus on every mount of the timeline (open from the case page, reload restore, Back from the analysis) but never on re-runs, Try again or Cancel.
- UI-R5-04: a case that turns out closed while its timeline is open also returns to the case page. The old TracingPaper closed-case checks in `tracingRound4.test.cjs` still hold for the component, but the page no longer reaches that state.
- The e2e `.axtick` assertions all use 3 hour windows (sub-day path, unchanged); no spec edited and none run. Nothing checked in a browser.
- `npm run build` rewrites tracked `web/dist` and `cmd/unravis-setup/ui/assets` outputs as in earlier rounds.
- Line endings kept: CRLF files stay CRLF, the LF `.cjs` tests stay LF.

Tests and gate (from `web/`): `npm run check` 0 errors, 0 warnings (410 files); `node --test tests/*.test.cjs` 2195 pass, 0 fail (new file `tests/tracingRound5.test.cjs`, 11 tests); `npm run build` then `node --test tests/distChunks.test.cjs` 2 pass.
