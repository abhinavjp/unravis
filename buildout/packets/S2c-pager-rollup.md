# Packet S2c: slow-calls pagination, long-window rollup note, extra demo rows

Do exactly this. Do not explore the repo. Read ONLY the files named below at the lines named. ES5 only (`var`, `function`, `for`; no arrows, template literals, `let`, `const`). Output the new files as fenced blocks and the edits as SEARCH/REPLACE blocks (format at the bottom). Never define a global that is not listed here.

## New file 1: `parts/10-data-calls.js` (extra demo rows)
At load, append 52 generated rows to the existing global `CALLS` (it already has 10 rows `[apiName, 'HH:MM:SS', ms]`, defined in `parts/10-data.js`). Use `for (var i = 0; i < 52; i++)`. Row i: API name = `['Orders.Create','Orders.List','Payments.Authorize','Basket.Get','Catalog.Search'][i % 5]`; start time = `'09:' + pad2(10 + (i * 7) % 50) + ':' + pad2((i * 13) % 60)` where `pad2(n)` returns two digits (inline, no new global; use a local `function` inside an IIFE `(function(){ ... })();`); ms = `2290 - i * 17` (always a number, descending). The file must contain no other top-level names.

## New file 2: `parts/30-pager.js`
Globals (and only these): `SLOW_PAGE`, `slowPageRows`, `slowPager`, `slowPageGo`, `fpRollupAny`.
- `var SLOW_PAGE = 25;`
- `slowPageRows(rows)`: `rows` is an array. `n = rows.length`; `pages = Math.max(1, Math.ceil(n / SLOW_PAGE))`; `W.slowPg = Math.min(Math.max(0, W.slowPg || 0), pages - 1)`; return `rows.slice(W.slowPg * SLOW_PAGE, W.slowPg * SLOW_PAGE + SLOW_PAGE)`. (`W` is read only inside functions.)
- `slowPager(total)`: if `total < 1` return `''`. `pages = Math.max(1, Math.ceil(total / SLOW_PAGE))`; `pg = Math.min(Math.max(0, W.slowPg || 0), pages - 1)`; `a = pg * SLOW_PAGE + 1`; `b = Math.min(total, a + SLOW_PAGE - 1)`. Return exactly `'<div class="pager" role="navigation" aria-label="Slow calls pages"><span class="note" role="status">Showing ' + a + ' to ' + b + ' of ' + total + '.</span><button class="btn sm" onclick="slowPageGo(-1)"' + (pg === 0 ? ' disabled' : '') + '>Previous</button><button class="btn sm" onclick="slowPageGo(1)"' + (pg >= pages - 1 ? ' disabled' : '') + '>Next</button></div>'`.
- `slowPageGo(d)`: `W.slowPg = (W.slowPg || 0) + d; render();`
- `fpRollupAny()`: needs `fpMs` from another file (exists at call time; guard with `typeof fpMs !== 'function'` returning `''`). `f = W.frame`; `li = fpMs(f.incidentEnd) - fpMs(f.incidentStart)`; `lr = fpMs(f.referenceEnd) - fpMs(f.referenceStart)`. If `li > 86400000 || lr > 86400000` (NaN compares false) return `'<p class="note" role="status">Showing hourly averages because the window is longer than 24 hours. Spikes shorter than an hour may not show as a peak at this scale.</p>'`, else `''`.

## New file 3: `parts/02-css-pager.html`
Exactly: `<style>.pager{display:flex;gap:8px;align-items:center;flex-wrap:wrap;margin-top:8px}.pager .note{margin:0 auto 0 0}</style>` followed by a newline.

## Edits to `parts/30-steps-0-4.js` (function `slowCallsTable`, about lines 176 to 186; read only those lines)
Make these two exact edits (each SEARCH occurs once in the file):
   - SEARCH the line `    + rows.map(function(p){` (inside `slowCallsTable`; it is the line that starts the row map and occurs once in the function; verify with `grep -c -F "    + rows.map(function(p){" parts/30-steps-0-4.js` = 1) and REPLACE it with `    + slowPageRows(rows).map(function(p){`.
   - SEARCH `</tbody></table></div><p class="note">The longest calls that started in the incident window.</p>'));` and REPLACE with `</tbody></table></div>' + slowPager(rows.length) + '<p class="note">The longest calls that started in the incident window.</p>'));`.

## Edits to `parts/12-evidence-c.js` (function `evChart`, lines 10 to 12)
Replace the two lines with the same text but with `+ (typeof fpRollupAny === 'function' ? fpRollupAny() : '')` inserted just before `+ evChartCaption(c)` in the `line` and the `stackedArea` branches (not the `bars` branch).
- SEARCH `tlChart(c.series, [{t:60, label:'Incident starts'}]) + evChartCaption(c);` -> REPLACE `tlChart(c.series, [{t:60, label:'Incident starts'}]) + (typeof fpRollupAny === 'function' ? fpRollupAny() : '') + evChartCaption(c);`
- SEARCH `stackedArea(evAreaSeries(c.series), false) + evChartCaption(c);` -> REPLACE `stackedArea(evAreaSeries(c.series), false) + (typeof fpRollupAny === 'function' ? fpRollupAny() : '') + evChartCaption(c);`

## Check (the owner applies the blocks and runs the full check; you only run this)
```
node --check parts/10-data-calls.js && node --check parts/30-pager.js && grep -c -F "    + rows.map(function(p){" parts/30-steps-0-4.js
```
Expected: no syntax error, then `1`.

## Block format
New file: `### parts/<name>` then a fenced code block. Edit: `### parts/<file>` then `<<<<<<< SEARCH`, old text, `=======`, new text, `>>>>>>> REPLACE` (no code fence, no blank line after the `###` line). New-file blocks first, then edit blocks in file order.

## Output
The blocks in one plain reply, then exactly 3 lines: `Status: PASS|FAIL`, `Checks: <printed output>`, `Concerns: none` (or one line).
