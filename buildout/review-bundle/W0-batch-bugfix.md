# Packet W0 (batch of two): fix squashed buttons (RC1) and blank charts (RC2)

Goal: stop `.A .row` leaking onto pane button strips; make the reference and incident lines draw.
Edit files: `buildout/wireframes/parts/01-css.html` and `buildout/wireframes/parts/10-data.js` only.
Do not touch: any other part, `build-guided.js`, any `.row` rule outside lines 101-104 of 01-css.html, `minuteLine` (lines 192-213 of 10-data.js).

## Edit 1: parts/01-css.html lines 101-104 (rail rules only)
Find these four lines (they start with `.A .row`) and prefix every selector with `.rail `, nothing else changes:
- `.A .row { display:grid; ...` becomes `.A .rail .row { display:grid; ...`
- `.A .row:hover {` becomes `.A .rail .row:hover {`
- `.A .row.cur {` becomes `.A .rail .row.cur {`
- `.A .row .n {` becomes `.A .rail .row .n {`
Why: the rail button is `<button class="row">` inside `<nav class="rail">` (20-shell.js:109, 116). Pane button strips are also `class="row"` (generic rule at line 53, `display:flex`); line 101 turns them into a 22 px first column, which squashes "Add a change", "Change the window", "Add a source", "+ Add item", "Download report".

## Edit 2: parts/10-data.js lines 175-189 (SERIES)
Currently one loop `for m in 0..59` pushes a reference point then an incident point per minute. `minuteLine` starts a new run whenever the window changes between neighbouring points, so every point becomes its own one-point path and nothing is drawn.
Change to two loops that build identical point objects, in this order: first all 60 reference points (m = 0..59, `at` uses hour 8), then all 60 incident points (m = 0..59, `at` uses hour 9, same `gap` rule for m 31..44). Keep the `jitter`, `refP`, `incP`, `inf` formulas and every field name (`at, win, p95, inFlight, gap`) exactly as they are. `SERIES.length` stays 120. Keep the `hm` helper working for both loops (define it once, taking `m` as a parameter).

## Checks (run in buildout/wireframes)
1. `node build-guided.js` prints no error.
2. In the built `guided-analysis-layout.html`: `grep -c "\.A \.row" ` returns 0 matches for the string `.A .row` that is not preceded by `.rail ` (rail rules only).
3. Evaluate `SERIES` in node: `SERIES.length === 120`, `SERIES.slice(0,60).every(p => p.win==='reference')`, `SERIES.slice(60).every(p => p.win==='incident')`.
4. `minuteLine('p95','P95','ms')` returns exactly 3 `<path` elements: 1 `class="line normal"` (reference, no gap) and 2 `class="line incident"` (incident run split by the long gap at minutes 31-44).

## Output
Unified diff of the two files only, then a 3-line report: status, checks run with results, concerns.
