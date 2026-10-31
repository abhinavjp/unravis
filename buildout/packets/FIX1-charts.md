# Packet FIX1: chart and legend defects found by QA

Do exactly this. Do not explore the repo. ES5 only (`var`, `function`, `for`; no arrows, template literals, `let`, `const`). CSS tokens only: `--series-1` (blue), `--series-4` (red), `--good`, `--accent`, `--text-2`, `--bad`, `--suspect-green-soft`; never hard-code a hex colour. Apply the blocks below exactly: the new file first, then the edits in file order. Every SEARCH is copied byte-for-byte from the current file and occurs exactly once (verify with `grep -c -F` before you finish); the content inside each block is the complete, final text to apply, nothing is abbreviated.

Seven defects are fixed:
1. Evidence bar chart fill colour (`parts/12-evidence-b.js`): bars used `var(--accent)`/`var(--text-2)` instead of the series tokens the legend keys already paint.
2. Evidence bar/line legend + caption must only name the windows actually present in the series; a bars card with only an incident window (e.g. `sql-blocking-chain`) shows one Incident key and the caption "These bars are the incident hour." The slowest-calls duration bars (`TA_callBars`, a single series literally named "Call duration (ms)") instead get one key labelled "Call duration" and keep their old caption untouched.
3. `tlChart` (`parts/11-charts-a.js`): when a window has two or more series (sql-memory-grants, GS-08-host-mem-red, GS-09-host-disk-red), the second series of that window draws dashed and both are named in the legend; windows with one series are unchanged.
4. Recurrence/host heatmap legend (`parts/11-charts-b.js`): the five legend keys get classes `hm0`..`hm4`, painted in the new CSS file with the same `color-mix` stops the heatmap cells use.
5. Swimlane (`parts/11-charts-c.js`): legend keys used `style="fill:..."` on spans, which paints nothing; switched to `style="background:..."`, and the `ok` colour (bar fill and legend key) moves from `var(--accent)` to `var(--good)`.
6. `parts/14-scenarios.js`, card `sql-cpu-high`: the normal-window series "Top wait share (%)" had 3 points (t 60, 90, 110) that duplicate the incident series; removed.
7. `.B .chip i.ready` (Variant B stepper) is `var(--ok)`, teal, while the Ready sticker beside it is `var(--suspect-green-soft)`, green. A same-specificity override in the new CSS file (loaded after `01-css.html`) matches them.

## New file: `parts/02-css-fix.html`

### parts/02-css-fix.html
```
<style>
figure.ml .key.hm0{background:var(--surface-2)}
figure.ml .key.hm1{background:color-mix(in srgb, var(--bad) 20%, var(--surface))}
figure.ml .key.hm2{background:color-mix(in srgb, var(--bad) 40%, var(--surface))}
figure.ml .key.hm3{background:color-mix(in srgb, var(--bad) 65%, var(--surface))}
figure.ml .key.hm4{background:color-mix(in srgb, var(--bad) 90%, var(--surface))}
.B .chip i.ready{background:var(--suspect-green-soft)}
</style>
```

## Edits to `parts/11-charts-a.js` (function `tlChart`, item 3)

### parts/11-charts-a.js
<<<<<<< SEARCH
  var paths = [];
  for (i = 0; i < series.length; i++) {
    pts = series[i].pts || [];
    cls = series[i].win === 'incident' ? 'incident' : 'normal';
    run = [];
    for (j = 0; j <= pts.length; j++) {
      if (j < pts.length && typeof pts[j].t === 'number' && typeof pts[j].v === 'number') {
        run.push((run.length ? 'L' : 'M') + x(pts[j].t).toFixed(1) + ' ' + y(pts[j].v).toFixed(1));
        continue;
      }
      if (run.length >= 2) paths.push('<path d="' + run.join(' ') + '" class="line ' + cls + '"/>');
      run = [];
    }
  }
=======
  var paths = [];
  var winSeen = { normal: 0, incident: 0 };
  for (i = 0; i < series.length; i++) {
    pts = series[i].pts || [];
    cls = series[i].win === 'incident' ? 'incident' : 'normal';
    var dashed = winSeen[cls] > 0;
    winSeen[cls]++;
    run = [];
    for (j = 0; j <= pts.length; j++) {
      if (j < pts.length && typeof pts[j].t === 'number' && typeof pts[j].v === 'number') {
        run.push((run.length ? 'L' : 'M') + x(pts[j].t).toFixed(1) + ' ' + y(pts[j].v).toFixed(1));
        continue;
      }
      if (run.length >= 2) paths.push('<path d="' + run.join(' ') + '" class="line ' + cls + '"' + (dashed ? ' style="stroke-dasharray:6 4"' : '') + '/>');
      run = [];
    }
  }
>>>>>>> REPLACE

### parts/11-charts-a.js
<<<<<<< SEARCH
  var names = [];
  for (i = 0; i < series.length; i++) if (names.indexOf(series[i].name) < 0) names.push(series[i].name);
  var cap = series.length === 1 ? series[0].name : names.join(' and ');
  var hasN = false, hasI = false;
  for (i = 0; i < series.length; i++) { if (series[i].win === 'incident') hasI = true; else hasN = true; }
  var keys = [];
  if (hasN) keys.push('<span class="key normal"></span> Normal');
  if (hasI) keys.push('<span class="key incident"></span> Incident');
  var legend = keys.length ? '<p class="legend">' + keys.join(' ') + '. A break in a line is a minute with no data.</p>' : '<p class="legend"></p>';
=======
  var names = [];
  for (i = 0; i < series.length; i++) if (names.indexOf(series[i].name) < 0) names.push(series[i].name);
  var cap = series.length === 1 ? series[0].name : names.join(' and ');
  var winNames = { normal: [], incident: [] };
  for (i = 0; i < series.length; i++) {
    var wn = series[i].win === 'incident' ? 'incident' : 'normal';
    winNames[wn].push(series[i].name);
  }
  var winOrder = ['normal', 'incident'];
  var keys = [];
  for (var wo = 0; wo < winOrder.length; wo++) {
    var wkey = winOrder[wo], warr = winNames[wkey];
    if (!warr.length) continue;
    if (warr.length === 1) {
      keys.push('<span class="key ' + wkey + '"></span> ' + (wkey === 'normal' ? 'Normal' : 'Incident'));
    } else {
      for (var wk = 0; wk < warr.length; wk++) {
        keys.push('<span class="key ' + wkey + '"></span> ' + esc(warr[wk]) + ' (' + (wk === 0 ? 'solid' : 'dashed') + ')');
      }
    }
  }
  var legend = keys.length ? '<p class="legend">' + keys.join(' ') + '. A break in a line is a minute with no data.</p>' : '<p class="legend"></p>';
>>>>>>> REPLACE

## Edit to `parts/11-charts-b.js` (function `heatmap`, item 4)

### parts/11-charts-b.js
<<<<<<< SEARCH
  var legend = '<p class="legend"><span class="key"></span> No data <span class="key"></span> Low <span class="key"></span> Medium <span class="key"></span> High <span class="key"></span> Very high</p>';
=======
  var legend = '<p class="legend"><span class="key hm0"></span> No data <span class="key hm1"></span> Low <span class="key hm2"></span> Medium <span class="key hm3"></span> High <span class="key hm4"></span> Very high</p>';
>>>>>>> REPLACE

## Edits to `parts/11-charts-c.js` (function `swimlane`, item 5)

### parts/11-charts-c.js
<<<<<<< SEARCH
    if (sev === 'ok') return 'fill:var(--accent)';
=======
    if (sev === 'ok') return 'fill:var(--good)';
>>>>>>> REPLACE

### parts/11-charts-c.js
<<<<<<< SEARCH
  var legend = '<p class="legend"><span class="key" style="fill:var(--accent)"></span> ok '
    + '<span class="key" style="fill:var(--warn)"></span> warn '
    + '<span class="key" style="fill:var(--bad)"></span> bad'
    + (hasBrush ? ' Shaded band is the selected time slice' : '') + '</p>';
=======
  var legend = '<p class="legend"><span class="key" style="background:var(--good)"></span> ok '
    + '<span class="key" style="background:var(--warn)"></span> warn '
    + '<span class="key" style="background:var(--bad)"></span> bad'
    + (hasBrush ? ' Shaded band is the selected time slice' : '') + '</p>';
>>>>>>> REPLACE

## Edits to `parts/12-evidence-b.js` (function `evBars`, items 1 and 2)

### parts/12-evidence-b.js
<<<<<<< SEARCH
    fill = bar.win === 'incident' ? 'var(--accent)' : 'var(--text-2)';
=======
    fill = bar.win === 'incident' ? 'var(--series-4)' : 'var(--series-1)';
>>>>>>> REPLACE

### parts/12-evidence-b.js
<<<<<<< SEARCH
  return '<figure class="ml"><figcaption>' + cap +
    '</figcaption><svg viewBox="0 0 640 140" role="img" aria-label="' + cap +
    '" preserveAspectRatio="none"><line x1="4" y1="136" x2="636" y2="136" class="axis"/>' +
    ax +
    rects +
    '</svg><p class="legend"><span class="key normal"></span> Normal <span class="key incident"></span> Incident</p></figure>';
}
=======
  var hasN = false, hasI = false;
  for (i = 0; i < bars.length; i++) { if (bars[i].win === 'incident') hasI = true; else hasN = true; }
  var legend;
  if (capNames.length === 1 && series.length === 1 && String(capNames[0]).replace(/\s*\([^()]*\)\s*$/, '') === 'Call duration') {
    legend = '<span class="key incident"></span> Call duration';
  } else {
    var lkeys = [];
    if (hasN) lkeys.push('<span class="key normal"></span> Normal');
    if (hasI) lkeys.push('<span class="key incident"></span> Incident');
    legend = lkeys.join(' ');
  }

  return '<figure class="ml"><figcaption>' + cap +
    '</figcaption><svg viewBox="0 0 640 140" role="img" aria-label="' + cap +
    '" preserveAspectRatio="none"><line x1="4" y1="136" x2="636" y2="136" class="axis"/>' +
    ax +
    rects +
    '</svg><p class="legend">' + legend + '</p></figure>';
}
>>>>>>> REPLACE

## Edit to `parts/12-evidence-f.js` (function `evChartCaption`, item 2)

### parts/12-evidence-f.js
<<<<<<< SEARCH
  var series = Object.prototype.toString.call(c.series) === '[object Array]' ? c.series : [];
=======
  var series = Object.prototype.toString.call(c.series) === '[object Array]' ? c.series : [];
  var hasNormal = false, hasIncident = false;
  for (var hci = 0; hci < series.length; hci++) {
    if (!series[hci]) { continue; }
    if (series[hci].win === 'incident') { hasIncident = true; } else { hasNormal = true; }
  }
  var oneCallDur = series.length === 1 && series[0] && String(series[0].name).replace(/\s*\([^()]*\)\s*$/, '') === 'Call duration';
  if (c.chart === 'bars' && hasIncident && !hasNormal && !oneCallDur) {
    return '<p class="ev-cap">These bars are the incident hour.</p>';
  }
>>>>>>> REPLACE

## Edit to `parts/14-scenarios.js` (card `sql-cpu-high`, item 6)

### parts/14-scenarios.js
<<<<<<< SEARCH
     "pts": [ { "t": 0, "v": 14 }, { "t": 30, "v": 15 }, { "t": 60, "v": 44 }, { "t": 90, "v": 51 }, { "t": 110, "v": 48 } ]
=======
     "pts": [ { "t": 0, "v": 14 }, { "t": 30, "v": 15 } ]
>>>>>>> REPLACE

## Check (you run it; the owner applies the blocks and runs the full check)
```
node --check parts/11-charts-a.js && node --check parts/11-charts-b.js && node --check parts/11-charts-c.js && node --check parts/12-evidence-b.js && node --check parts/12-evidence-f.js && node --check parts/14-scenarios.js
```

## Block format
New file: `### parts/<name>` then a fenced code block. Edit: `### parts/<file>` then `<<<<<<< SEARCH`, old text, `=======`, new text, `>>>>>>> REPLACE` (no code fence, no blank line after the `###` line). New files first, then edits.

## Output
The blocks in one plain reply, then exactly 3 lines: `Status: PASS|FAIL`, `Checks: <printed output>`, `Concerns: none` (or one line).
