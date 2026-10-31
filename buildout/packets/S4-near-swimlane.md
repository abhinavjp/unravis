# Packet S4: "Nearby activity" swim-lane for Step 4 (Load), new files `parts/10-data3-near.js` and `parts/02-css-near.html`, one edit to `parts/30-steps-0-4.js`

Do exactly this. Do not explore the repo. Create the 2 new files below and make the 1 edit. ES5 only (`var`, `function`, `for`; no arrows, template literals, `let`, `const`). Globals that exist elsewhere and you only USE (always inside functions, never at load time): `W`, `render()`, and `swimlane(lanes, window, brush)` from `parts/11-charts-c.js` (that part loads after this one, so `swimlane` may only be called inside `nearView`). For the arguments used here, `swimlane` returns `<figure class="ml"><figcaption>Nearby activity <span class="range">minute +60 to +119</span></figcaption>` + one `<svg viewBox="0 0 640 140" ...>` holding one `<text class="axis-label">NAME</text>` and one `<line class="axis">` per lane, one element per event (`<rect class="ev ...">` when `dur > 0`, else `<circle class="ev ...">`; class `ev in-brush` when the event span `[t, t + dur]` overlaps the brush `[from, to]`; each with a `<title>`), a `<rect class="brush">` band across the lanes, and a `<p class="legend">` ending `Shaded band is the selected time slice`. With no event inside the window it returns `<p class="note">No activity to show.</p>` instead of the figure.
New globals (only these, all in the new part): `NEAR_ROWS`, `nearSet`, `nearView`.

## `NEAR_ROWS` (deterministic fixture, 14 rows, new global)
`var NEAR_ROWS = [...]` with one object per recorded event, in time order. Fields: `at` = UTC clock time `HH:MM:SS` (the table column Time (UTC)); `src` = `'API'` or `'SQL'` (this case has no MongoDB or Host source, so no row uses them and those lanes stay empty); `what` = the table text; `t` = start minute on the shared chart axis (minute 60 is 09:00 UTC; the onset 09:14 is minute 74); `dur` = length in minutes (0 = a point in time); `sev` = `'ok'`, `'warn'` or `'bad'`. The rows sit around the onset so each brush width selects a different set: the 2-min band lists 4 rows, 5-min lists 7, 15-min lists 12. The file block below is the contract.

## State (lazy, never at load)
The brush width in minutes lives in `W.near.min`, created on first use as `W.near = { min: 5 }` (inside `nearSet` and `nearView`, never at load time). Only 2, 5 and 15 are valid; any other value reads as 5.

## `nearSet(m)`
`nearSet(m)`: if `W.near` is missing or not an object, set `W.near = { min: 5 }`; then `W.near.min = m; render();`.

## `nearView()`
Returns one section, in this order:
1. `'<section class="nearby near" aria-label="Nearby activity">'`
2. heading `'<div class="sthead"><h4>Nearby activity around the onset (09:14 UTC)</h4></div>'`
3. help line `'<p class="note">Every source we have, on one time line: the shaded band is the slice the buttons choose, and the table lists what falls inside it. MongoDB and Host have no source in this case, so those lanes stay empty.</p>'`
4. radio group: `'<div class="picks" role="radiogroup" aria-label="Minutes around the onset"><span class="note">Minutes around the onset</span>'` then for V = 2, 5, 15 one `'<label class="wf"><input type="radio" name="near-min" value="' + V + '"' + (m === V ? ' checked' : '') + ' onchange="nearSet(' + V + ')"> ' + V + ' min</label>'`, then `'</div>'`
5. the chart `swimlane(lanes, { from: 60, to: 119 }, { from: bFrom, to: bTo })`: `lanes` is `['API', 'SQL', 'Mongo', 'Host']` mapped to `{ name: src, events: [...] }` with, per source, the `NEAR_ROWS` rows of that source as `{ t: r.t, dur: r.dur, label: r.what, sev: r.sev }` (so Mongo and Host render as empty lanes); the brush is `m` minutes around the onset minute 74: `bFrom = 74 - Math.floor(m / 2)`, `bTo = 74 + Math.ceil(m / 2)` (2 -> 73 to 75, 5 -> 72 to 77, 15 -> 67 to 82)
6. `'<p class="note">Events inside the shaded band, ' + hhmm(bFrom) + ' to ' + hhmm(bTo) + ' UTC (' + rows.length + ' of ' + NEAR_ROWS.length + ').</p>'` with a local helper `hhmm(min)` returning the UTC `HH:MM` of a chart minute (minute 0 is 08:00; `480 + min`, zero-padded)
7. the table of the events inside the brush: `rows` = the `NEAR_ROWS` rows whose span overlaps the brush (`r.t <= bTo && r.t + (r.dur || 0) >= bFrom`, exactly the events the chart marks `in-brush`); when non-empty `'<div class="tscroll"><table><thead><tr><th>Time (UTC)</th><th>Source</th><th>What happened</th></tr></thead><tbody>'` + one `'<tr><td>' + r.at + '</td><td>' + r.src + '</td><td>' + r.what + '</td></tr>'` per row + `'</tbody></table></div>'`, else `'<p class="note">Nothing was recorded inside the band.</p>'`
8. `'</section>'`
The whole function is in the file block below; the block is the contract.

## New file `parts/10-data3-near.js`
### parts/10-data3-near.js
```
// Nearby activity (step 4): deterministic fixture rows for the swim-lane and its table.
// One row per recorded event. at = UTC clock time, src = lane (API, SQL, Mongo, Host),
// what = the table text, t = start minute on the shared chart axis (minute 60 is 09:00 UTC,
// the onset 09:14 is minute 74), dur = length in minutes (0 = a point in time),
// sev = ok, warn or bad. The case has no MongoDB or Host source, so those lanes stay empty.
var NEAR_ROWS = [
  { at:'09:07:10', src:'API', what:'Orders.List call, 450 ms',               t:67, dur:0, sev:'ok' },
  { at:'09:09:44', src:'API', what:'Catalog.Search call, 312 ms',            t:69, dur:0, sev:'ok' },
  { at:'09:11:20', src:'SQL', what:'Log flush held the write batch, 900 ms',  t:71, dur:0, sev:'warn' },
  { at:'09:12:58', src:'API', what:'Basket.Get call, 205 ms',                t:72, dur:0, sev:'ok' },
  { at:'09:13:44', src:'API', what:'Orders.Get call, 388 ms',                t:73, dur:0, sev:'ok' },
  { at:'09:14:02', src:'API', what:'Orders.Create call, 4,812 ms',           t:74, dur:0, sev:'bad' },
  { at:'09:14:01', src:'SQL', what:'Lock wait on Orders (session 88)',      t:74, dur:9, sev:'bad' },
  { at:'09:15:31', src:'API', what:'Payments.Authorize call, 1,240 ms',      t:75, dur:0, sev:'warn' },
  { at:'09:16:40', src:'SQL', what:'Lock wait on OrderLines (session 91)',   t:76, dur:0, sev:'warn' },
  { at:'09:17:12', src:'API', what:'Catalog.Search call, 312 ms',            t:77, dur:0, sev:'ok' },
  { at:'09:18:58', src:'API', what:'Orders.Create call, 3,611 ms',           t:78, dur:0, sev:'bad' },
  { at:'09:21:40', src:'API', what:'Orders.List call, 3,904 ms',             t:81, dur:0, sev:'bad' },
  { at:'09:23:00', src:'SQL', what:'Lock wait on Orders (session 88) ended', t:83, dur:0, sev:'ok' },
  { at:'09:24:11', src:'API', what:'Orders.Create call, 4,812 ms',           t:84, dur:0, sev:'bad' }
];

// Brush width in minutes lives in W.near.min, created on first use.
function nearSet(m){
  if (!W.near || typeof W.near !== 'object') W.near = { min: 5 };
  W.near.min = m;
  render();
}

// The "Nearby activity" block of step 4: one swim-lane of all four lanes on the
// incident hour, a brush of m minutes around the onset, and the events inside it.
function nearView(){
  if (!W.near || typeof W.near !== 'object') W.near = { min: 5 };
  var m = W.near.min === 2 || W.near.min === 15 ? W.near.min : 5;
  var anchor = 74; // 09:14 UTC: the minute the slow end moved
  var bFrom = anchor - Math.floor(m / 2), bTo = anchor + Math.ceil(m / 2);
  var hhmm = function(min){
    var t = 480 + Math.round(min), h = Math.floor(t / 60), mm = t % 60;
    return (h < 10 ? '0' : '') + h + ':' + (mm < 10 ? '0' : '') + mm;
  };
  var lanes = ['API', 'SQL', 'Mongo', 'Host'].map(function(src){
    return { name: src, events: NEAR_ROWS.filter(function(r){ return r.src === src; }).map(function(r){
      return { t: r.t, dur: r.dur, label: r.what, sev: r.sev };
    }) };
  });
  var rows = NEAR_ROWS.filter(function(r){ return r.t <= bTo && r.t + (r.dur || 0) >= bFrom; });
  return '<section class="nearby near" aria-label="Nearby activity">'
  + '<div class="sthead"><h4>Nearby activity around the onset (09:14 UTC)</h4></div>'
  + '<p class="note">Every source we have, on one time line: the shaded band is the slice the buttons choose, and the table lists what falls inside it. MongoDB and Host have no source in this case, so those lanes stay empty.</p>'
  + '<div class="picks" role="radiogroup" aria-label="Minutes around the onset"><span class="note">Minutes around the onset</span>'
  + [2, 5, 15].map(function(v){
    return '<label class="wf"><input type="radio" name="near-min" value="' + v + '"' + (m === v ? ' checked' : '') + ' onchange="nearSet(' + v + ')"> ' + v + ' min</label>';
  }).join('')
  + '</div>'
  + swimlane(lanes, { from: 60, to: 119 }, { from: bFrom, to: bTo })
  + '<p class="note">Events inside the shaded band, ' + hhmm(bFrom) + ' to ' + hhmm(bTo) + ' UTC (' + rows.length + ' of ' + NEAR_ROWS.length + ').</p>'
  + (rows.length
    ? '<div class="tscroll"><table><thead><tr><th>Time (UTC)</th><th>Source</th><th>What happened</th></tr></thead><tbody>'
    + rows.map(function(r){ return '<tr><td>' + r.at + '</td><td>' + r.src + '</td><td>' + r.what + '</td></tr>'; }).join('')
    + '</tbody></table></div>'
    : '<p class="note">Nothing was recorded inside the band.</p>')
  + '</section>';
}
```

## New file `parts/02-css-near.html`
### parts/02-css-near.html
```
<style>
.near{margin-top:12px}
.near .picks{margin:8px 0}
.near .picks label.wf{display:flex;align-items:center;gap:6px;color:var(--text)}
.near table td:first-child{white-space:nowrap}
figure.ml rect.brush{stroke:var(--accent);stroke-width:1}
</style>
```
(one rule per line as shown; the file ends with a newline after `</style>`; `--text` and `--accent` are the only CSS tokens used)

## Edit to `parts/30-steps-0-4.js` (verify the SEARCH occurs once with `grep -c -F`)
Step 4 is `function step4(st){` under the `// ----- Step 4: Load -----` marker. The edit puts `nearView()` at the end of the Step 4 body, right after the minute chart and before the reasons box (step 3 places its panel the same way).

### parts/30-steps-0-4.js
<<<<<<< SEARCH
  + minuteLine('inFlight', 'Calls in flight by minute', 'calls')
=======
  + minuteLine('inFlight', 'Calls in flight by minute', 'calls')
  + (typeof nearView === 'function' ? nearView() : '')
>>>>>>> REPLACE

## Check (you run it; the owner applies the blocks and runs the full check)
```
node --check parts/10-data3-near.js && grep -c -F "  + minuteLine('InFlight', 'Calls in flight by minute', 'calls')" parts/30-steps-0-4.js
```
Expected: no syntax error, then `1`.

## Block format
New file: `### parts/<name>` then a fenced code block. Edit: `### parts/30-steps-0-4.js` then `<<<<<<< SEARCH`, old text, `=======`, new text, `>>>>>>> REPLACE` (no code fence, no blank line after the `###` line). New-file blocks first, then the edit block.

## Output
The blocks in one plain reply, then exactly 3 lines: `Status: PASS|FAIL`, `Checks: <printed output>`, `Concerns: none` (or one line).
