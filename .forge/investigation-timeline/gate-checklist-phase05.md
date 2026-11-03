# Phase 05 gate checklist (guided unlisted, bar hand-off)

Run: `cd tools/e2e; powershell -NoProfile -ExecutionPolicy Bypass -File run-e2e.ps1 -Exe <gate exe> -Only "HAND-01,...,HAND-19"` (spec `specs/tracing-handoff.spec.ts`, demo case "Orders API stall, 19 October").
Result of the last run (3 Nov 2026, after the fixes): 19 of 19 HAND specs pass, none is `test.fixme`. The two app defects found at first are fixed (see the end).

## Gate checks

| Gate check (phase.md section 7) | Spec id | Result | A human must still eyeball |
|---|---|---|---|
| Case home: Investigation Timeline after Analysis, no Guided, Open the analysis unchanged | HAND-08, HAND-16 | pass | How the section looks (spacing, weight) |
| Timeline button opens the tracing screen, windows typed, Start tracing draws it | HAND-01 | pass | Windows form layout |
| Click a bar with events: 5-minute window, analysis opens, URL hash empty, Run analysis applies | HAND-02, HAND-03 | pass (real mouse click, HAND-18, defect 1 fixed) | Nothing extra |
| Reference window is equal length and just before the slot | none | not covered | Open Filters and read the comparison range (unit test `tracingHandoff` covers the maths) |
| Empty bar: plain sentence, nothing opens | HAND-04 | pass | Sentence position and tone |
| Bar of a hidden lane does nothing and says why | HAND-05 | pass | Nothing extra |
| Double click: one analysis screen | HAND-06 | pass | Nothing extra |
| Reload on the analysis keeps the window | HAND-07 | pass | Case tools after reload (the demo drops the active case on reload by design, so "This case" is gone) |
| Back to case home from the analysis | HAND-08 | pass | Nothing extra |
| 375 px: chart, selector, case home, hand-off | HAND-09 | pass (no sideways scroll, hand-off works) | Label and panel overlap, see defect 2 |
| Light and dark | HAND-10, HAND-11 | pass | Contrast of faint text, lane colours |
| Keyboard only: arrows, Enter | HAND-12 | pass | Focus ring is visible and readable |
| Closed case: Analysis kept, timeline disabled with reason | HAND-13 | pass | Wording of the reason |
| Archived case home: no timeline or analysis | HAND-14 | pass | Nothing extra |
| No /investigation, /views, /checklist, /report call | HAND-15 | pass (request paths) | Console clean on a real browser run |
| Timeline on a case with no sources says why | none | not covered | Open a case with no data |
| No case id or source name in the URL hash | HAND-02, HAND-07 | pass (hash is empty) | Nothing extra |

## Wireframe match (prototype/tracing-paper-PROTOTYPE.html, screenshots 08, 09, 13)

| Screen | Property | Covered by | Gap |
|---|---|---|---|
| Case home | Section order: Analysis, Timeline, Story, Theories, Health, Sources, Files, Key, Erase | HAND-16 asserts it | none |
| Case home | Analysis button filled accent, timeline button outlined | HAND-16 asserts fill and border | Exact outline colour and radius: eyeball |
| Case home | Headings and copy read "Analysis", "Investigation Timeline" | HAND-16 asserts text | Spacing between blocks: eyeball |
| Tracing chart | Order: windows, toolbar, canvas bar, audit, SQL, IIS, time axis, caption | HAND-17 asserts vertical order | none |
| Tracing chart | Lane names, toolbar names, caption text, first tick | HAND-17 asserts text | none |
| Tracing chart | Shared time axis: panels, overlay and 36 columns share one width | HAND-17 asserts within 2 px | none |
| Tracing chart | Tokens: panel surface, hairline, bar accent, hint text, lane swatches match toolbar | HAND-17 asserts computed colours | Whether the lane colours look right together: eyeball |
| Tracing chart | Selected bar outlined in accent (dark and light) | HAND-10, HAND-11 assert | none |
| Tracing chart | Latency line, percentile buttons, zoom and brush, tooltip look | not covered here (TRACE specs) | Eyeball |
| Hand-off result | "Filters changed" note, then "Showing 19 Oct HH:MM:SS to HH:MM:SS UTC." after Run | HAND-02, HAND-03, HAND-12 assert text | Placement of the note: eyeball |
| Hand-off result | Run analysis is the primary button | HAND-02 asserts class | Nothing extra |
| Narrow (13) | Label above panel, ticks readable | HAND-19 | passes after the fix (defect 2); eyeball |

## App defects found by the specs (fixed in Phase 05)

1. HAND-18: a real mouse click on a bar does nothing. `onOverlayDown` calls `setPointerCapture` on the overlay, so Chromium sends pointerup and click to the overlay and the column `onclick` never runs. Keyboard and dispatched clicks work. A person with a mouse cannot hand off.
2. HAND-19: under 480 px the lane label, panel and axis share one grid row (inline `grid-row`), so the label is drawn under the panel and the time-axis labels overprint (screenshot compared with wireframe 13).
