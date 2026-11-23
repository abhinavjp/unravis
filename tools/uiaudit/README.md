# UI audit

Opens every Unravis screen of the **demo app** (`--demo`, fixed clock, demo cases, no real sources) in four looks
(desktop 1440 px and phone 390 px wide, each in dark and light), checks the layout, and writes a report anyone can read:

- `report/REPORT.md` is the report (Markdown). It starts with a PASS or FAIL verdict, then lists every problem with a screenshot.
- `report/shots/` holds one full-height screenshot per screen and look. Red boxes mark errors, orange boxes mark warnings, and the
  number is the row number in the table above the picture.
- `report/data/*.json` holds the raw findings (including the notes the report only counts).

## Run it

```
cd tools/uiaudit
npm ci
npx playwright install chromium          # once
node run-audit.mjs                       # all screens, about 20 minutes
node run-audit.mjs --only p1-home,x-where-is-it
node run-audit.mjs --report-only         # rebuild REPORT.md from the last run's data
```

`run-audit.mjs` builds the demo exe with Go when `UNRAVIS_EXE` is not set, and the setup window's capture build
(`go build -tags capture ./cmd/unravis-setup`) so the setup screens (p11-*) are checked too. With no Go, give it `UNRAVIS_EXE`.
It also builds two more programs that have a page of their own and starts them next to the demo: the Releases dashboard
(`./cmd/unravis-releases`, on made-up release files, kept offline; screens r1-r7) and a stand-in for Unravis Lite
(`./tools/uiaudit/litefixture`; screens l1-l3). The stand-in records a Blackbox folder with the real recorder and serves it through
the same handlers and the same built page as Lite. The real `unravis-lite` only accepts a Windows folder, and its own Go tests cover it.
Give them `UNRAVIS_RELEASES_EXE` and `UNRAVIS_LITE_FIXTURE_EXE` when there is no Go.
`PW_CHROMIUM` points at a Chromium to use instead of the one Playwright installed.
Exit code: 0 = PASS (no new errors), 1 = FAIL, 2 = could not run.

## What it checks

Sideways scroll, elements past the window edge, cut-off or spilling text, overlapping controls and headings, tap targets
(under 24 px, and under 44 px on a phone as a note), text contrast (WCAG AA), text under 11 px, controls in a row or blocks in a
stack that are a few pixels off line, broken pictures, code words on screen (`undefined`, `NaN`), buttons and fields with no name,
duplicate ids, heading jumps, big surfaces of the wrong brightness for the theme, console errors, failed requests and server errors.
A tap target smaller than 24 px may be grown by an absolutely positioned `::before` or `::after` of the control, but only when the browser confirms it (`elementFromPoint` at the centre and the four corners of that rectangle, all inside the window, must answer with the control) and the rectangle touches the control's box: a far-off, clipped, covered or shrunk pseudo-element does not count.
`lib/page-checks.js` does the looking inside the page. Closed `<details>` and hidden things are skipped.
The window is stretched to the height of the content before the checks, because the app is a fixed-height shell with inner scrollers.

Not checked: text drawn on a canvas (charts), colour behind gradients and pictures, motion, real data, and anything that needs
a person's taste. The report says so too.

## Which screens

`tools/screens/screens.json` (the user-guide screens, so the guide and the audit cannot drift apart) plus `extra-screens.json`
(screens the guide does not show: Where is it?, All operations, Patterns, Ledger, the case Timeline, Blackbox charts, and so on).
Steps may also `clickCentre` (a mouse click on the middle of an element that takes no pointer events itself, such as the red diamonds of the Timeline that open the Slow call screen, x-slow-call), `upload` made-up fixture files (`lib/fixtures.ts`, generated in memory) to a real file input, use `macro` (steps listed once under `macros` in `extra-screens.json`), and mark a part of a screen that only newer builds have with `feature` (`[selector, what]`): when it is not there the screen is listed under "Screens not reachable on this build" and does not fail the run. A screen with `"restoreHosts": true` changes the demo's list of computers and has it put back afterwards. The add-files screens are p10-files-1 to 7 (BL-481).
A screen whose steps fail (a renamed button, a screen only Windows can reach) is listed as **not reached**: that is a gap, not a pass.

To add a screen: add an entry to `extra-screens.json` (same step language as `tools/screens/capture.spec.ts`), run it with
`--only <id>`, read the report.

## The baseline

`baseline.json` lists problems that are known and tracked. Each entry says why, and ideally names a backlog item:

```json
{ "screen": "p1-home", "size": "phone", "check": "small-target", "label": "More detail", "why": "reason", "item": "BL-nnn" }
```

`screen`, `size` (`desktop` or `phone`), `check` and `label` may be left out or use `*`. A screen that cannot be reached is
listed with `"check": "unreachable"`. The run fails only for an error that is **not** on the baseline, or a screen that is newly
unreachable. Warnings never fail the run, but they are all in the report. Do not add an entry to make a run green: fix the screen
or file the backlog item first.

## Tests of the checker

```
npm test                                   # report logic (node --test)
npx playwright test -c playwright.selftest.config.ts   # lib/page-checks.js finds the faults planted in fixtures/bad.html and nothing in good.html
```

## CI

`.github/workflows/ui-audit.yml` runs the audit on Linux (ubuntu-24.04) and on Windows (windows-2022): by hand, weekly, and on
pull requests that change `tools/uiaudit` or the workflow itself. It uploads `report/` as an artifact per system and prints the
verdict in the job summary. It is not a check on every pull request (about 20 minutes). On Linux the Windows-only screens are
reported as not reached (see BL-441 for the Linux test baseline).
