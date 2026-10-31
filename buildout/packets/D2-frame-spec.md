# Packet D2: Frame step (Step 0) behaviour and copy, including long windows (spec only)

Goal: write the behaviour and the exact user-facing copy for the Frame step so a later packet can build it without questions. Output is a Markdown spec in your reply. No code edits.
Depends on: nothing. Read-only.

## 1. Context (read these files only)
- `buildout/specs/2026-10-29-research-long-windows.md` (R2): long windows, rollups, DST and timezone handling, data volume, recurrence ("last Thursday of every month").
- `buildout/specs/2026-10-29-owner-issues-triage.md`: owner issues 5 and 7 (frame, long windows, recurrence, release).
- `buildout/specs/2026-10-29-wireframe-v2-implementation-plan.md` rows D2, S2, S3 (section 2) and section 10 (Slice 3).
- `buildout/wireframes/parts/30-steps-0-4.js`: function `step0` and `window.stepNotes0` show what Step 0 draws today (incident window, reference window, Edit windows, Run again).
- Plain words: readers are engineers, not statisticians. No jargon without a one-line explanation.

## 2. Task
Write a spec with these sections; every piece of copy is a final string in quotes:
1. **Release (optional)**: the field, the default state, the scenario "no release notice given" (what the page says and does), the scenario "release time inside the incident window".
2. **Estimate line**: one sentence shown before Run telling how much data will be read and how long it will take (inputs: window length, number of sources); 3 example values (1 h, 1 day, 30 days).
3. **Overlap warning**: copy for incident and reference windows that overlap, touch, or differ in length by more than 2x; what the buttons offer.
4. **Long windows**: from R2, at which window length the page switches to rollups, the label shown ("Showing hourly averages because the window is longer than ..."), what is lost, and pagination of the slow-calls table (page size, "Showing 1 to 25 of N").
5. **Recurrence selector**: options as a list (for example: One window, Same hours on the previous days, Same weekday on previous weeks, Last Thursday of every month), the inputs each one needs, the copy of the summary sentence after choosing, and what the calendar heatmap and overlay of N occurrences must show (what each cell colour means, max N).
6. **Time zone and DST**: one paragraph of rules from R2 and the exact copy of the notice shown when a window crosses a DST change.
7. **Test scenarios**: a table `| scenario | setup | what the page must show |` with at least 10 rows covering sections 1 to 6.

## 3. Do not
Edit files, change the Step 1 to 13 copy, or invent limits that R2 contradicts (when R2 gives no number, propose one and mark it `proposed`).

## 4. Output
The spec Markdown (sections 1 to 7), then 3 status lines: `Status: PASS|FAIL`, `Checks: n/a`, `Concerns: <none or one line>`. Reply is capped near 16,000 output tokens: be terse.
