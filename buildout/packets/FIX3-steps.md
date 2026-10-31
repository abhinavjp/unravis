# Packet FIX3: QA defect fixes in the step pages (10 SEARCH/REPLACE blocks in 4 files, no new files)

Do exactly this. Do not explore the repo. Apply the 10 SEARCH/REPLACE blocks in the "Blocks" section below (the whole edit set sits inside one four-backtick fence). ES5 only (`var`, `function`, `for`; no arrows, template literals, `let`, `const`). Edited files, nothing else: `parts/31-steps-5-9.js`, `parts/32-steps-10-13.js`, `parts/30-steps-0-4.js`, `parts/30-recurrence.js`. Exactly one new global is added: `signed(n)` in `parts/30-steps-0-4.js`. No data file changes (`FIXROWS` in `parts/10-data.js` is NOT edited). Every SEARCH block is copied byte-for-byte from the current file and occurs exactly once (verified with `grep -c -F`). Apply with `node apply-packet.js <reply.md>` from `buildout/wireframes` (all blocks apply or none). Do not read `W` at load time. The product stays read-only: no SQL probes, fixed-rule engine, no runtime LLM.

## Contract
1. `parts/31-steps-5-9.js`, `function addCheck(id)`: the first statement becomes `if (W.closed) return;` (the same closed-case guard style as `addToolNote`/`saveCurrentView` in `parts/40-manual-home.js`). On a closed case `addCheck` changes nothing, sets no flash and does not render. The rest of the function is untouched; on an open case it still pushes once (no duplicate text) and still sets `W.flash = 'Added to the checklist (Need to do).';` then `render();`.
2. `parts/32-steps-10-13.js`, the after-fix compare table in `afterFixBody()` (the `FIXROWS.map` line): the mark span reads `▼ Still above normal` (same class, `mark bad`) exactly when `r[4] === 'bad'` and `r[3].charAt(0)` is `'-'` or `'−'` (U+2212); every other marked row keeps the old `(r[4] === 'bad' ? '▲' : '●') + ' ' + r[5]`, and an empty `r[4]` still renders no span. With the current FIXROWS data the four rows render exactly:
   - `<tr><td>P95 (slow end)</td><td>2,100 ms</td><td>790 ms</td><td><span class="mark bad">▼ Still above normal</span> -62 %</td></tr>`
   - `<tr><td>P50 (middle)</td><td>380 ms</td><td>210 ms</td><td><span class="mark grey">● No change</span> -45 %</td></tr>`
   - `<tr><td>D (whole call, average)</td><td>2,150 ms</td><td>820 ms</td><td><span class="mark bad">▼ Still above normal</span> -62 %</td></tr>`
   - `<tr><td>L (calls in flight)</td><td>21.4</td><td>8.1</td><td>-62 %</td></tr>`
   A fall in the "Change vs the earlier day" column is no longer marked `▲ Slower`; nothing else in the table changes.
3. Same file, the per-item "Move to" select in the checklist rows (`step12`, the select wired to `ckMove`): it gets `aria-label="Move this item to another list"` before its `onchange`. The Add-item form's "Move to" select and the closed-case behaviour (no select at all when `W.closed`) are untouched.
4. `parts/30-steps-0-4.js`:
   (a) new global, placed directly under the file's header comment, above `statePrefix`: `function signed(n){ n = String(n); return n.charAt(0) === '-' ? n : '+' + n; }` — it takes the already formatted value: a leading ASCII hyphen is kept as-is, everything else gets `+` in front. It is used at exactly two sites: the By API table change cell becomes `' (' + signed(fmtCount(r[5])) + ' ms)'` and the parts table change cell becomes `' (' + signed(fmtMs(d)) + ')'` (`fmtCount`, `fmtMs`, `fmtPct` are not edited). So the Catalog.Get row prints `-1 % (-2 ms)` where it printed `-1 % (+-2 ms)`, Orders.Create keeps `+400 % (+1,680 ms)`, Ping.Health keeps `0 % (+0 ms)`, and the one-API Catalog.Get parts table prints `-20 % (-1 ms)`.
   (b) `frameProblems`, pool limit rule: empty or whitespace-only still passes; otherwise the digit test `/^\d{1,6}$/` must pass AND `Number(f.poolLimit.trim())` must be at least 1 and at most 100000; same message `'The pool limit is a whole number from 1 to 100000, or empty.'`. So `0`, `00`, `100001`, `999999`, `12.5`, `abc` and `-5` are rejected; `1`, `99` and `100000` are accepted.
   (c) `slowCallsTable(withNearby)`: the empty 4th header cell is emitted only when `withNearby` is true; the header becomes `<th>API</th><th>Started (UTC)</th><th>Time</th>' + (withNearby ? '<th></th>' : '') + '</tr></thead><tbody>`. The row cells (the Nearby activity button) are unchanged.
5. `parts/30-recurrence.js`:
   (a) the heatmap legend note now reads `'Darker red = slower than normal for that hour. Light = close to normal. Gray = no data yet for that hour.'` (only the word amber becomes red; the rest of the note and the notes after it are unchanged).
   (b) `recurSet`, key `'n'`: after `parseInt(val, 10)`, NaN becomes 1, the value is clamped to `1 .. recurLimit(r.mode)` and the clamped value is STORED in `r.n` before `render()`, so the count input displays the clamped value after the next render (`recurSet("n", "99")` in days mode shows `value="30"`; `"0"` and `"abc"` show `value="1"`). The `'wd'` branch, `recurOcc` and `frameRecurrence` are not edited.

## Blocks
````
### parts/31-steps-5-9.js
<<<<<<< SEARCH
function addCheck(id){
  var s = SUSPECTS.filter(function(x){ return x.id === id; })[0];
=======
function addCheck(id){
  if (W.closed) return;
  var s = SUSPECTS.filter(function(x){ return x.id === id; })[0];
>>>>>>> REPLACE

### parts/32-steps-10-13.js (edit 1 of 2)
<<<<<<< SEARCH
      + FIXROWS.map(function(r){ return '<tr><td>' + r[0] + '</td><td>' + r[1] + '</td><td>' + r[2] + '</td><td>' + (r[4] ? '<span class="mark ' + r[4] + '">' + (r[4] === 'bad' ? '▲' : '●') + ' ' + r[5] + '</span> ' : '') + r[3] + '</td></tr>'; }).join('') + '</tbody></table></div>'
=======
      + FIXROWS.map(function(r){ return '<tr><td>' + r[0] + '</td><td>' + r[1] + '</td><td>' + r[2] + '</td><td>' + (r[4] ? '<span class="mark ' + r[4] + '">' + (r[4] === 'bad' && (r[3].charAt(0) === '-' || r[3].charAt(0) === '−') ? '▼ Still above normal' : (r[4] === 'bad' ? '▲' : '●') + ' ' + r[5]) + '</span> ' : '') + r[3] + '</td></tr>'; }).join('') + '</tbody></table></div>'
>>>>>>> REPLACE

### parts/32-steps-10-13.js (edit 2 of 2)
<<<<<<< SEARCH
            + (W.closed ? '' : '<span class="row"><select class="wf" style="width:auto" onchange="ckMove(\'' + it.id + '\',this.value)">' + LISTS.map(function(t){ return '<option value="' + t[0] + '"' + (it.list === t[0] ? ' selected' : '') + '>' + t[1] + '</option>'; }).join('') + '</select>'
=======
            + (W.closed ? '' : '<span class="row"><select class="wf" style="width:auto" aria-label="Move this item to another list" onchange="ckMove(\'' + it.id + '\',this.value)">' + LISTS.map(function(t){ return '<option value="' + t[0] + '"' + (it.list === t[0] ? ' selected' : '') + '>' + t[1] + '</option>'; }).join('') + '</select>'
>>>>>>> REPLACE

### parts/30-steps-0-4.js (edit 1 of 5)
<<<<<<< SEARCH
// ---------- steps 0 to 4 ----------
function statePrefix(n, st){
=======
// ---------- steps 0 to 4 ----------
function signed(n){ n = String(n); return n.charAt(0) === '-' ? n : '+' + n; }
function statePrefix(n, st){
>>>>>>> REPLACE

### parts/30-steps-0-4.js (edit 2 of 5)
<<<<<<< SEARCH
  if (f.poolLimit.trim() !== '' && !/^\d{1,6}$/.test(f.poolLimit.trim())) out.push('The pool limit is a whole number from 1 to 100000, or empty.');
=======
  if (f.poolLimit.trim() !== '' && (!/^\d{1,6}$/.test(f.poolLimit.trim()) || Number(f.poolLimit.trim()) < 1 || Number(f.poolLimit.trim()) > 100000)) out.push('The pool limit is a whole number from 1 to 100000, or empty.');
>>>>>>> REPLACE

### parts/30-steps-0-4.js (edit 3 of 5)
<<<<<<< SEARCH
        + '<td><span class="mark ' + r[6] + '"><span aria-hidden="true">' + (r[6] === 'bad' ? '▲' : '●') + '</span> ' + r[7] + '</span> ' + fmtPct(r[4]) + ' (+' + fmtCount(r[5]) + ' ms)</td></tr>';
=======
        + '<td><span class="mark ' + r[6] + '"><span aria-hidden="true">' + (r[6] === 'bad' ? '▲' : '●') + '</span> ' + r[7] + '</span> ' + fmtPct(r[4]) + ' (' + signed(fmtCount(r[5])) + ' ms)</td></tr>';
>>>>>>> REPLACE

### parts/30-steps-0-4.js (edit 4 of 5)
<<<<<<< SEARCH
      return '<tr><td>' + p.label + '</td><td>' + fmtMs(p.normal) + '</td><td' + (p.k === 'O' ? ' class="slow"' : '') + '>' + fmtMs(p.inc) + '</td><td>' + fmtPct(d / p.normal * 100) + ' (+' + fmtMs(d) + ')</td><td>' + Math.round(p.share * 100) + ' %</td></tr>';
=======
      return '<tr><td>' + p.label + '</td><td>' + fmtMs(p.normal) + '</td><td' + (p.k === 'O' ? ' class="slow"' : '') + '>' + fmtMs(p.inc) + '</td><td>' + fmtPct(d / p.normal * 100) + ' (' + signed(fmtMs(d)) + ')</td><td>' + Math.round(p.share * 100) + ' %</td></tr>';
>>>>>>> REPLACE

### parts/30-steps-0-4.js (edit 5 of 5)
<<<<<<< SEARCH
  + (!W.slowShown ? '' : (!rows.length ? '<p class="note">No slow call in this selection.</p>' : '<div class="tscroll"><table><thead><tr><th>API</th><th>Started (UTC)</th><th>Time</th><th></th></tr></thead><tbody>'
=======
  + (!W.slowShown ? '' : (!rows.length ? '<p class="note">No slow call in this selection.</p>' : '<div class="tscroll"><table><thead><tr><th>API</th><th>Started (UTC)</th><th>Time</th>' + (withNearby ? '<th></th>' : '') + '</tr></thead><tbody>'
>>>>>>> REPLACE

### parts/30-recurrence.js (edit 1 of 2)
<<<<<<< SEARCH
    + '<p class="note">Darker amber = slower than normal for that hour. Light = close to normal. Gray = no data yet for that hour.</p>'
=======
    + '<p class="note">Darker red = slower than normal for that hour. Light = close to normal. Gray = no data yet for that hour.</p>'
>>>>>>> REPLACE

### parts/30-recurrence.js (edit 2 of 2)
<<<<<<< SEARCH
  if (key === 'n') {
    var n = parseInt(val, 10);
    r.n = isNaN(n) ? 1 : n;
  } else if (key === 'wd') {
=======
  if (key === 'n') {
    var n = parseInt(val, 10);
    if (isNaN(n)) n = 1;
    var max = recurLimit(r.mode);
    if (n < 1) n = 1;
    if (n > max) n = max;
    r.n = n;
  } else if (key === 'wd') {
>>>>>>> REPLACE
````

## Check (you run it; the owner applies the blocks and runs the full check)
```
node --check parts/31-steps-5-9.js && node --check parts/32-steps-10-13.js && node --check parts/30-steps-0-4.js && node --check parts/30-recurrence.js
```
(from `buildout/wireframes`, after the blocks are applied; the owner also runs `node ../packets/data/chk-fix3.js`, which prints `PASS`.)

## Block format
Edit: `### parts/<file>` then `<<<<<<< SEARCH`, old text, `=======`, new text, `>>>>>>> REPLACE` (no code fence, no blank line after the `###` line). Blocks apply in the order written; every SEARCH must occur exactly once; all apply or none.

## Output
The blocks in one plain reply, then exactly 3 lines: `Status: PASS|FAIL`, `Checks: <printed output>`, `Concerns: none` (or one line).
