### FIX6-chartfix.md
# Packet FIX6: three round-2 chart defects

Do exactly this. Do not explore the repo. ES5 only (`var`, `function`, `for`; no arrows, template literals, `let`, `const`). The only CSS token used here is `--suspect-green` (opaque, defined in `parts/01-css.html`); never hard-code a hex colour. Apply the blocks below exactly, in file order. Every SEARCH is copied byte-for-byte from the current file and occurs exactly once (verify each with `grep -c -F` before you finish); the content inside each block is the complete, final text to apply, nothing is abbreviated.

Three defects are fixed:
1. `parts/12-evidence-f.js` (function `evChartCaption`): the slowest-calls duration bars (`TA_callBars` in `parts/10-data5-toolsA.js`, a single incident-only series literally named "Call duration (ms)") get the generic caption "Left part: the reference hour (a normal period). Right part: the incident hour. ..." although there are no reference bars. That single-series call-duration bars chart now gets the caption "Each bar is one call. A taller bar is a slower call." Its single "Call duration" legend key (`evBars`) is unchanged, and every other chart's caption is unchanged.
2. `parts/12-evidence-b.js` (function `evBars`): when the bars contain no normal-window bar (every bar is incident) the dashed "incident starts" marker was drawn on the chart's left border. The two window booleans are now computed before the axis block and the marker is drawn only when at least one normal bar and one incident bar exist. Charts with both windows render exactly as they do now.
3. `parts/02-css-fix.html`: `.B .chip i.ready` was a 12% tint (`var(--suspect-green-soft)`), nearly invisible on an 8 px dot. It is now opaque `var(--suspect-green)`. `data/chk-fix1.js` is updated to pin the new behaviour of items 1 and 3; nothing else in it changes.

## Edits to `parts/12-evidence-f.js` (function `evChartCaption`, item 1)

### parts/12-evidence-f.js
<<<<<<< SEARCH
  var oneCallDur = series.length === 1 && series[0] && String(series[0].name).replace(/\s*\([^()]*\)\s*$/, '') === 'Call duration';
  if (c.chart === 'bars' && hasIncident && !hasNormal && !oneCallDur) {
    return '<p class="ev-cap">These bars are the incident hour.</p>';
  }
=======
  var oneCallDur = series.length === 1 && series[0] && String(series[0].name).replace(/\s*\([^()]*\)\s*$/, '') === 'Call duration';
  if (c.chart === 'bars' && oneCallDur) {
    return '<p class="ev-cap">Each bar is one call. A taller bar is a slower call.</p>';
  }
  if (c.chart === 'bars' && hasIncident && !hasNormal && !oneCallDur) {
    return '<p class="ev-cap">These bars are the incident hour.</p>';
  }
>>>>>>> REPLACE

## Edits to `parts/12-evidence-b.js` (function `evBars`, item 2)

### parts/12-evidence-b.js
<<<<<<< SEARCH
  var ax = axYLabels(top, unit || '', W, H, PAD);
=======
  var hasN = false, hasI = false;
  for (i = 0; i < bars.length; i++) { if (bars[i].win === 'incident') hasI = true; else hasN = true; }

  var ax = axYLabels(top, unit || '', W, H, PAD);
>>>>>>> REPLACE

### parts/12-evidence-b.js
<<<<<<< SEARCH
  if (typeof incT === 'number') {
=======
  if (typeof incT === 'number' && hasN && hasI) {
>>>>>>> REPLACE

### parts/12-evidence-b.js
<<<<<<< SEARCH
  var hasN = false, hasI = false;
  for (i = 0; i < bars.length; i++) { if (bars[i].win === 'incident') hasI = true; else hasN = true; }
  var legend;
=======
  var legend;
>>>>>>> REPLACE

## Edit to `parts/02-css-fix.html` (item 3)

### parts/02-css-fix.html
<<<<<<< SEARCH
.B .chip i.ready{background:var(--suspect-green-soft)}
=======
.B .chip i.ready{background:var(--suspect-green)}
>>>>>>> REPLACE

## Edits to `data/chk-fix1.js` (item 4)

### ../packets/data/chk-fix1.js
<<<<<<< SEARCH
has('caption call-duration kept old text', capCall, 'Left part: the reference hour');
=======
eq('caption call-duration new text', capCall, '<p class="ev-cap">Each bar is one call. A taller bar is a slower call.</p>');
>>>>>>> REPLACE

### ../packets/data/chk-fix1.js
<<<<<<< SEARCH
has('css ready override present', css, '.B .chip i.ready{background:var(--suspect-green-soft)}');
=======
has('css ready override present', css, '.B .chip i.ready{background:var(--suspect-green)}');
>>>>>>> REPLACE

## Check (the owner applies the blocks and runs the full check; you only run these)
```
grep -c -F "  var ax = axYLabels(top, unit || '', W, H, PAD);" parts/12-evidence-b.js
grep -c -F "  if (typeof incT === 'number') {" parts/12-evidence-b.js
grep -c -F ".B .chip i.ready{background:var(--suspect-green-soft)}" parts/02-css-fix.html
node --check parts/12-evidence-f.js && node --check parts/12-evidence-b.js
```

## Block format
New file: `### parts/<name>` then a fenced code block. Edit: `### parts/<file>` then `<<<<<<< SEARCH`, old text, `=======`, new text, `>>>>>>> REPLACE` (no code fence, no blank line after the `###` line). New files first, then edits in file order.

## Output
The blocks in one plain reply, then exactly 3 lines: `Status: PASS|FAIL`, `Checks: <printed output>`, `Concerns: none` (or one line).
