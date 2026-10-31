# Packet S1A: tools-A audit fixes (gap shading, why-it-moved links, slow-call links and duration bars, time-split API picker)

Do exactly this. Do not explore the repo. Create the 2 new files below and make the 11 edits to `parts/41-tools-a.js`. ES5 only (`var`, `function`, `for`; no arrows, template literals, `let`, `const`). Never define a global that is not listed here. Do not move tools between files and do not touch any other tool.

Globals that exist elsewhere and you only USE: `W`, `ROUTES` (read at load time by the new data part, which loads after `parts/10-data.js`), `CALLS`, `esc(s)`, `minuteLine(field, label, unit)`, `evChart(c)`, `dt(t, h)`, `slowCallsTable(withNearby)`, `byApiTable(limit)`, `partsTable()`, `partsBars()` (all read inside functions only), and `openTool(id)` / `render()` (only inside `onclick`/`onchange` strings).

New globals (only these, all defined in the new file `parts/10-data5-toolsA.js`): `TA_SPLIT`, `TA_MOVED`, `TA_gapShade`, `TA_whyMoved`, `TA_callBars`, `TA_splitByApi`.

## What this packet does (the audit rows for the four tools in `41-tools-a.js`)
- **response-time** (missing: link to why each API moved; gap shading): the P95-by-minute chart gets a shaded band over the 09:31&ndash;09:44 UTC gap plus a plain-English note naming it, and a fold under the per-API table gives a one-line why for each API that moved, each with a button that opens the tool holding the evidence.
- **slowest-apis** (missing: link to the slow calls behind each row): the ranked table gets a last column `Slow calls` with a button that opens the slowest-calls tool, plus a note. The row's other cells do not change. (The Query Store / sp_BlitzCache addition needs a new collector; it is not part of this packet.)
- **slowest-calls** (missing: duration bars): one bar per listed call (height = duration in ms) drawn with the shared evidence bar chart, shown only when the calls are shown, plus a plain-English note. (The tie-in to SQL/Mongo ops needs new collectors; it is not part of this packet.)
- **time-split** (missing: API picker (all/one/several), per-API rows): a picker above a new per-API rows table. `All APIs` (default), one API from the select, or several by a case-insensitive substring match; an API pick wins over the match. The table shows A, C, P and O per API, normal against incident, plus the part that grew most. The pooled table and bars above stay exactly as they are.

## New file 1: `parts/10-data5-toolsA.js`
Loads after `parts/10-data.js` (so `ROUTES` exists; `-` sorts before `.`, so `10-data.js`, `10-data2-calls.js`, `10-data3-near.js` all load before it) and before `parts/11-*`. Nothing in it reads `W` at load time. Contents, in this order (the complete file is in the Blocks section; this is the contract):
- `TA_SPLIT`: one entry per route in `ROUTES` order, `{ api, parts: { A: [normal, incident], C: [...], P: [...], O: [...] } }`. Fixed rule: the pooled normal shape (A 18, C 240, P 12, O 150; total 420 ms) is scaled to each API's reference P95 (`row[3]`, so `normal = Math.round(shape_n * row[3] / 420)`); incident parts use the pooled incident shape (A 22, C 480, P 14, O 1584; total 2100 ms) scaled to the incident P95 for the APIs marked `bad` (`Math.round(inc * row[2] / 2100)`), and the normal shape scaled to the incident P95 for the rest (`Math.round(shape_n * row[2] / 420)`).
- `TA_MOVED`: exactly three rows, one per API marked `bad` in `ROUTES`, each `{ api, tool, why }` with `tool: 'sql-waits'`; the exact `why` copy is in the block.
- `TA_gapShade(html)`: inserts the band `<rect class="ta-gap" x="487.3" y="4" width="69.0" height="132" rx="2"/>` (the gap is incident minutes 31 to 44, points 91 and 104 of the 120-point axis, positioned with the same formula `minuteLine` uses: `x(i) = 4 + (i / 119) * 632`) right after the single `preserveAspectRatio="none">` of the figure, so the lines draw over it. Returns the html unchanged when that marker is missing.
- `TA_whyMoved()`: returns `''` when `TA_MOVED` is missing, else the fold `<details class="ev-fold"><summary>Why each API moved</summary>` + one `<p class="note">` per row (`<b>` API, the why copy, a `<button class="quiet" onclick="openTool('sql-waits')">See the evidence</button>`) + `</details>`.
- `TA_callBars()`: returns `''` unless `typeof evChart === 'function'` and `CALLS` is a non-empty array; builds one point per call `{ t, v }` where `v` is the duration ms and `t` is the shared minute axis (minute 60 is 09:00 UTC: `t = hh * 60 + mm + ss / 60 - 480`, rounded to 2 decimals), then returns `evChart({ chart: 'bars', series: [{ name: 'Call duration (ms)', win: 'incident', pts: pts }] })`.
- `TA_splitByApi()`: returns `''` when `TA_SPLIT` is missing. State: `W.taSplitApi` (`'all'` or an API name, default `'all'`) and `W.taSplitMatch` (substring, default `''`); an API pick wins over the match. Renders the picker (a `pair` div with the select `All APIs` + one option per route, and the `Only APIs matching` text input, both live via `render()`), then either the no-match note `No API matches. Clear the picker to see all APIs.` or the per-API table (columns: API, Audit set-up (A), Action time (C), Payload record (P), Outside time (O), Grew most; each part cell reads `NN &rarr; NN ms`; Grew most is the part label of the largest positive incident-minus-normal delta, else `Nothing grew`) and the note `Per-API rows: each part of the call, normal against incident. Pick one API, or type to match several; the pooled chart above stays as it is.`

## New file 2: `parts/02-css-ta.html`
Exactly `<style>.ta-gap{fill:var(--surface-2);stroke:var(--line);stroke-width:1;stroke-dasharray:3 2}</style>` followed by a newline.

## Edits to `parts/41-tools-a.js` (11 SEARCH/REPLACE blocks, applied in the order given; each SEARCH occurs exactly once in the file, verified with `grep -c -F` before this packet was written; re-verify each with `grep -c -F` before replacing)
1. `response-time` body: wrap the P95 chart in `TA_gapShade(...)` and add the gap note under it.
2. `response-time` body: append `TA_whyMoved()` after the rule note.
3. `response-time` notes: add the `Why it moved` help line.
4. `slowest-apis` body: add the `Slow calls` column header.
5. `slowest-apis` body: add the per-row `The calls` button cell.
6. `slowest-apis` body: add the note after the table.
7. `slowest-apis` notes: mention the link column in `Chart`.
8. `slowest-calls` body: add `TA_callBars()` and its note inside the shown branch.
9. `slowest-calls` notes: replace `Chart` with the duration-bars wording.
10. `time-split` body: append `TA_splitByApi()` after `extra`.
11. `time-split` notes: add the `API picker` help line.

## Check (you run it; the owner applies the blocks and runs the full check)
```
node --check parts/10-data5-toolsA.js && node --check parts/41-tools-a.js && grep -c -F "TA_splitByApi();" parts/41-tools-a.js
```
Expected: no syntax error, then `1`.

## Block format
New file: `### parts/<name>` then a fenced code block. Edit: `### parts/41-tools-a.js` then `<<<<<<< SEARCH`, old text, `=======`, new text, `>>>>>>> REPLACE` (no code fence, no blank line after the `###` line). New files first, then edits in file order.

## Output
The blocks in one plain reply, then exactly 3 lines: `Status: PASS|FAIL`, `Checks: <printed output>`, `Concerns: none` (or one line).

## Blocks

### parts/10-data5-toolsA.js
```js
// ---------- tools-A extras: gap shading, why-it-moved links, slow-call duration bars, time-split per-API rows ----------
// Loads after parts/10-data.js (ROUTES) and before parts/41-tools-a.js; nothing here reads W at load time.
// New globals: TA_SPLIT, TA_MOVED, TA_gapShade, TA_whyMoved, TA_callBars, TA_splitByApi.

// Per-API A/C/P/O rows for the time-split picker. The pooled normal shape (A 18, C 240,
// P 12, O 150; total 420 ms) is scaled to each API's reference P95. APIs marked bad get
// the pooled incident shape (A 22, C 480, P 14, O 1584; total 2100 ms) scaled to their
// incident P95; the rest keep the normal shape scaled to their incident P95.
var TA_SPLIT = (function(){
  var SHAPE = [ ['A', 18, 22], ['C', 240, 480], ['P', 12, 14], ['O', 150, 1584] ];
  var out = [];
  for (var r = 0; r < ROUTES.length; r++) {
    var row = ROUTES[r], parts = {};
    for (var i = 0; i < SHAPE.length; i++) {
      var k = SHAPE[i][0], n = SHAPE[i][1], inc = SHAPE[i][2];
      var normal = Math.round(n * row[3] / 420);
      var incident = Math.round((row[6] === 'bad' ? inc : n) * row[2] / (row[6] === 'bad' ? 2100 : 420));
      parts[k] = [normal, incident];
    }
    out.push({ api: row[0], parts: parts });
  }
  return out;
})();

// One plain-English line per API that moved, with the tool that holds the evidence.
var TA_MOVED = [
  { api: 'Orders.Create', tool: 'sql-waits', why: 'Outside time (O) grew from 150 ms to 1,584 ms behind it, and lock waits on the Orders tables rose in the same minute, 09:14 UTC.' },
  { api: 'Orders.List', tool: 'sql-waits', why: 'It reads the same Orders tables, so it queued behind the lock waits that Orders.Create triggered at 09:14 UTC.' },
  { api: 'Payments.Authorize', tool: 'sql-waits', why: 'It waits on the same write batch that the log flush held back, so it slowed in the same minute as the other two.' }
];

// Shade the 09:31-09:44 UTC gap inside a P95 minuteLine figure: the gap is incident
// minutes 31 to 44, points 91 to 104 of the 120-point x axis, positioned with the same
// formula minuteLine uses. The band goes right after the opening svg tag, so the lines
// draw over it.
function TA_gapShade(html){
  var MARK = 'preserveAspectRatio="none">';
  var at = html.indexOf(MARK);
  if (at < 0) return html;
  var CW = 640, PAD = 4, N = 120;
  var x = function(i){ return PAD + (i / (N - 1)) * (CW - 2 * PAD); };
  var band = '<rect class="ta-gap" x="' + x(91).toFixed(1) + '" y="' + PAD + '" width="' + (x(104) - x(91)).toFixed(1) + '" height="' + (140 - 2 * PAD) + '" rx="2"/>';
  return html.slice(0, at + MARK.length) + band + html.slice(at + MARK.length);
}

// The "Why each API moved" fold for the response-time tool.
function TA_whyMoved(){
  if (typeof TA_MOVED === 'undefined' || !TA_MOVED.length) return '';
  var out = '<details class="ev-fold"><summary>Why each API moved</summary>';
  for (var i = 0; i < TA_MOVED.length; i++) {
    var m = TA_MOVED[i];
    out += '<p class="note"><b>' + esc(m.api) + '</b> ' + esc(m.why)
      + ' <button class="quiet" onclick="openTool(\'' + m.tool + '\')">See the evidence</button></p>';
  }
  return out + '</details>';
}

// Duration bars for the slowest-calls tool: one bar per listed call, height = duration
// in ms, drawn with the shared evidence bar chart. The time axis is the shared minute
// axis (minute 60 is 09:00 UTC).
function TA_callBars(){
  if (typeof evChart !== 'function' || typeof CALLS === 'undefined' || !CALLS.length) return '';
  var pts = [];
  for (var i = 0; i < CALLS.length; i++) {
    var t = String(CALLS[i][1]).split(':');
    var mins = Number(t[0]) * 60 + Number(t[1]) + Number(t[2]) / 60 - 480;
    pts.push({ t: Math.round(mins * 100) / 100, v: CALLS[i][2] });
  }
  return evChart({ chart: 'bars', series: [{ name: 'Call duration (ms)', win: 'incident', pts: pts }] });
}

// The API picker and per-API rows for the time-split tool. W.taSplitApi is 'all' or one
// API name; W.taSplitMatch is a case-insensitive substring. An API pick wins over the
// match. The pooled table and bars above are untouched.
function TA_splitByApi(){
  if (typeof TA_SPLIT === 'undefined' || !TA_SPLIT.length) return '';
  var WORD = { A: 'Audit set-up (A)', C: 'Action time (C)', P: 'Payload record (P)', O: 'Outside time (O)' };
  var KEYS = ['A', 'C', 'P', 'O'];
  var api = W.taSplitApi || 'all';
  var raw = String(W.taSplitMatch || '');
  var match = raw.trim().toLowerCase();
  var rows = TA_SPLIT.filter(function(r){
    if (api !== 'all') return r.api === api;
    if (match) return r.api.toLowerCase().indexOf(match) >= 0;
    return true;
  });
  var picker = '<div class="pair"><label class="wf">API'
    + '<select class="wf" onchange="W.taSplitApi=this.value;render()">'
    + '<option value="all"' + (api === 'all' ? ' selected' : '') + '>All APIs</option>'
    + TA_SPLIT.map(function(r){ return '<option value="' + r.api + '"' + (api === r.api ? ' selected' : '') + '>' + r.api + '</option>'; }).join('')
    + '</select></label>'
    + '<label class="wf">Only APIs matching<input class="wf" type="text" maxlength="100" value="' + esc(raw) + '" oninput="W.taSplitMatch=this.value;render()"></label></div>';
  if (!rows.length) return picker + '<p class="note">No API matches. Clear the picker to see all APIs.</p>';
  var body = rows.map(function(r){
    var cells = '', best = 'Nothing grew', bestD = 0;
    for (var i = 0; i < KEYS.length; i++) {
      var p = r.parts[KEYS[i]];
      cells += '<td>' + p[0] + ' &rarr; ' + p[1] + ' ms</td>';
      if (p[1] - p[0] > bestD) { bestD = p[1] - p[0]; best = WORD[KEYS[i]]; }
    }
    return '<tr><td>' + r.api + '</td>' + cells + '<td>' + best + '</td></tr>';
  }).join('');
  return picker
    + '<div class="tscroll"><table><thead><tr><th>API</th><th>Audit set-up (A)</th><th>Action time (C)</th><th>Payload record (P)</th><th>Outside time (O)</th><th>Grew most</th></tr></thead><tbody>'
    + body + '</tbody></table></div>'
    + '<p class="note">Per-API rows: each part of the call, normal against incident. Pick one API, or type to match several; the pooled chart above stays as it is.</p>';
}
```

### parts/02-css-ta.html
```html
<style>.ta-gap{fill:var(--surface-2);stroke:var(--line);stroke-width:1;stroke-dasharray:3 2}</style>
```

### parts/41-tools-a.js
<<<<<<< SEARCH
    + minuteLine('p95', 'P95 by minute (ms)', 'ms')
=======
    + TA_gapShade(minuteLine('p95', 'P95 by minute (ms)', 'ms'))
    + '<p class="note">The gray band marks the gap 09:31&ndash;09:44 UTC: 14 minutes with no audit records, so the line breaks inside it.</p>'
>>>>>>> REPLACE

### parts/41-tools-a.js
<<<<<<< SEARCH
    + '<p class="note">The rule: a change counts when it is at least 30 % and at least 100 ms.</p>';
=======
    + '<p class="note">The rule: a change counts when it is at least 30 % and at least 100 ms.</p>'
    + TA_whyMoved();
>>>>>>> REPLACE

### parts/41-tools-a.js
<<<<<<< SEARCH
    + dt('Buttons', 'Show every API / Show fewer (live). Run the steps fills the body (live via Pretend the steps have run, or Run).');
=======
    + dt('Buttons', 'Show every API / Show fewer (live). Run the steps fills the body (live via Pretend the steps have run, or Run).')
    + dt('Why it moved', 'Under the table, a fold gives a one-line why for each API that moved, with a link to the tool that holds the evidence. The gray band on the chart marks the 09:31&ndash;09:44 UTC gap, where the audit recorded nothing.');
>>>>>>> REPLACE

### parts/41-tools-a.js
<<<<<<< SEARCH
<th>API</th><th>Calls</th><th>P95</th><th>Reference P95</th><th>Change</th></tr></thead><tbody>
=======
<th>API</th><th>Calls</th><th>P95</th><th>Reference P95</th><th>Change</th><th>Slow calls</th></tr></thead><tbody>
>>>>>>> REPLACE

### parts/41-tools-a.js
<<<<<<< SEARCH
        + '<td><span class="mark ' + r[6] + '"><span aria-hidden="true">' + (r[6] === 'bad' ? '▲' : '●') + '</span> ' + r[7] + '</span> ' + fmtPct(r[4]) + ' (+' + fmtCount(r[5]) + ' ms)</td></tr>';
=======
        + '<td><span class="mark ' + r[6] + '"><span aria-hidden="true">' + (r[6] === 'bad' ? '▲' : '●') + '</span> ' + r[7] + '</span> ' + fmtPct(r[4]) + ' (+' + fmtCount(r[5]) + ' ms)</td>'
        + '<td><button class="quiet" onclick="openTool(\'slowest-calls\')">The calls</button></td></tr>';
>>>>>>> REPLACE

### parts/41-tools-a.js
<<<<<<< SEARCH
    }).join('') + '</tbody></table></div>';
=======
    }).join('') + '</tbody></table></div><p class="note">Every row links to the individual slow calls behind it, in the Slowest calls tool.</p>';
>>>>>>> REPLACE

### parts/41-tools-a.js
<<<<<<< SEARCH
    + dt('Chart', 'None. Ranked table: API, Calls, P95, Reference P95, Change.')
=======
    + dt('Chart', 'None. Ranked table: API, Calls, P95, Reference P95, Change, plus a link to the slow calls behind each row.')
>>>>>>> REPLACE

### parts/41-tools-a.js
<<<<<<< SEARCH
TOOL_BODIES['slowest-calls'] = function(){
  return slowCallsTable(false) + (W.slowShown ? '<p class="note">The longest calls that started in the incident window.</p>' : '');
};
=======
TOOL_BODIES['slowest-calls'] = function(){
  return slowCallsTable(false) + (W.slowShown ? '<p class="note">The longest calls that started in the incident window.</p>'
    + TA_callBars()
    + '<p class="note">One bar per listed call: the taller the bar, the longer the call took. Read the exact start times in the table.</p>' : '');
};
>>>>>>> REPLACE

### parts/41-tools-a.js
<<<<<<< SEARCH
    + dt('Chart', 'None. Table: API, Started (UTC), Time.')
=======
    + dt('Chart', 'Duration bars: one bar per listed call, the height is how long the call took in ms. Table: API, Started (UTC), Time.')
>>>>>>> REPLACE

### parts/41-tools-a.js
<<<<<<< SEARCH
    + extra;
=======
    + extra
    + TA_splitByApi();
>>>>>>> REPLACE

### parts/41-tools-a.js
<<<<<<< SEARCH
    + dt('Buttons', 'Add the IIS logs (inert). IIS checkbox in the notes strip switches Partly ready / Ready live.');
=======
    + dt('Buttons', 'Add the IIS logs (inert). IIS checkbox in the notes strip switches Partly ready / Ready live.')
    + dt('API picker', 'All APIs, one API, or several by a match: the per-API rows under the pooled chart show A, C, P and O for each picked API, normal against incident, plus the part that grew most.');
>>>>>>> REPLACE
