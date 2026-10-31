# Review packet R-code: verify W2-0 (CSS) and C1 (chart helpers)

You are an independent reviewer. Do not edit files. Read only: `buildout/packets/W2-0-evidence-css.md`, `buildout/packets/C1-chart-helpers.md` (the requirements), `buildout/wireframes/parts/01-css.html` (block starting `/* evidence cards (W2-0) */`) and `buildout/wireframes/parts/11-charts-a.js`.

## Check each item and answer with evidence (file and line, or the node command output)
1. W2-0: every class and rule the packet lists exists, uses only existing CSS variables (`--ok --warn --bad --accent --text-2 --line --surface --surface-2 --r`; grep that no other `--` variable is used in the block), adds no `.B` or `.C` rules, and edits no existing rule.
2. C1: ES5 only (no `=>`, backticks, `let`, `const`, spread, `Array.from`, default params); pure (no `document`, `window`); text escaped through `esc`; no top-level globals except `tlChart` and `stackedArea` (run `node -e` with `vm` and list `Object.keys(context)`).
3. `tlChart`: with the packet's test data, 3 paths; a `null` splits the line; one series with fewer than 2 numeric points draws no path; markers draw `<line class="marker">`; y scale never divides by zero for all-zero data.
4. `stackedArea`: percent mode sums every column to 100; raw mode scales to the highest column total; an all-zero column gives no NaN; fewer than 2 columns returns the "Not enough data" note; values of different lengths do not throw.
5. Try to break it: pass `undefined` series, empty arrays, names containing `<script>`, a negative value, a value above the y range. Report any exception or any output containing `NaN`, `undefined` or unescaped `<script>`.
6. List CSS classes the helpers emit that have no rule in `parts/01-css.html` (for example `area`, `s-a`, `s-c`, `s-p`, `s-o`, `marker`, `mk-label`).

## Output
A table `| # | pass/fail | evidence |` for items 1 to 5, a bullet list for item 6, then a verdict line `Verdict: ACCEPT | FIX` and the list of fixes (file, line, one-line change). Be terse; the reply is capped near 16,000 output tokens.
