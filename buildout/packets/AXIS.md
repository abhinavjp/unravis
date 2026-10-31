# Packet AXIS: axis numbers with units and a dashed incident marker on evidence charts

Do exactly this. Do not explore the repo. Read ONLY the files named below. ES5 only (`var`, `function`, `for`; no arrows, template literals, `let`, `const`). Output the new file as a fenced block and the edits as SEARCH/REPLACE blocks (format at the bottom). Never define a global that is not listed here.

What changes: every `line`, `stackedArea` and `bars` evidence chart (rendered by `evChart` in `parts/12-evidence-c.js` from the `SCEN` fixture cards in `parts/14-scenarios.js`) gets: (a) three y-axis tick labels (max, half max, 0) compactly formatted with the series unit when every series name agrees on one; (b) three x-axis time labels (first, middle, last) written `+N min`; (c) one vertical dashed incident-window marker at the fixture's first incident minute, omitted when no series entry has `win:'incident'`. All tick text uses `class="axis-label"` and the marker uses `class="marker"`; both styles already exist in `parts/01-css.html` (`text.axis-label` is styled, `line.marker` is already dashed), so add NO CSS. Charts with no data keep returning `'<p class="note">Not enough data to draw this chart.</p>'` with no axes or marker; `chart:'none'` cards keep their current text. The only callers of `tlChart`, `stackedArea` and `evBars` are inside `evChart`.

## New file: `parts/11-charts-axis.js`
New globals (only these): `axUnit`, `axFmt`, `axYLabels`, `axXLabels`, `axIncidentT`, `axIncidentLine`. Pure string builders; no DOM; never touch `W`; nothing but function definitions at load time. Contract:

- `axUnit(series)`: one shared unit for a card. For each entry, take the trailing `(...)` group of its `name` when that group matches `/^[A-Za-z%\/ ]{1,10}$/` (trimmed), else `'%'` when the name ends in ` %`, else `''`. Return the unit only when every entry agrees; mixed units (or a non-array argument) return `''`. This is the same rule as `unitOf` in `parts/12-evidence-f.js`.
- `axFmt(v, unit)`: when `Math.abs(v) >= 1000`, `s = String(Math.round(v / 100) / 10).replace(/\.0$/, '') + 'k'`; else `s = String(Math.round(v * 10) / 10).replace(/\.0$/, '')`; return `unit ? s + ' ' + unit : s`. Examples: `axFmt(0, 'ms')` is `'0 ms'`; `axFmt(1200, 'ms')` is `'1.2k ms'`; `axFmt(47.5, '%')` is `'47.5 %'`.
- `axIncidentT(series)`: the smallest numeric `p.t` over all points of entries with `win === 'incident'`, or `null` when there are none.
- `axYLabels(top, unit, W, H, PAD)`: three labels, with `y(v) = H - PAD - (v / top) * (H - 2 * PAD)`: max as `'<text class="axis-label" x="' + (PAD + 2) + '" y="' + (y(top) + 9).toFixed(1) + '">' + axFmt(top, unit) + '</text>'`, half max at `(y(top / 2) - 3).toFixed(1)`, zero at `(y(0) - 2).toFixed(1)`. For the standard W=640, H=140, PAD=4 plot the y values are `13.0`, `67.0`, `134.0`.
- `axXLabels(items, H, PAD)`: `items` is an array of `{t, x}`; one label per item: `'<text class="axis-label" x="' + items[i].x.toFixed(1) + '" y="' + (H - PAD - 14).toFixed(1) + '" text-anchor="' + anchor + '">+' + items[i].t + ' min</text>'` where `anchor` is `'start'` for the first item, `'end'` for the last, `'middle'` for any between.
- `axIncidentLine(x, yTop, yBot)`: `'<line x1="' + x.toFixed(1) + '" y1="' + yTop + '" x2="' + x.toFixed(1) + '" y2="' + yBot + '" class="marker"/>'`.

### parts/11-charts-axis.js
```js
// Axis helpers for the evidence charts: shared unit detection, compact tick formatting,
// y tick labels (0, half, max), x time labels (first, middle, last) and the dashed
// incident-window marker. Pure string builders: no DOM access, no W, ES5 only.

function axUnit(series) {
  if (Object.prototype.toString.call(series) !== '[object Array]') return '';
  var unit = '';
  var same = true;
  var seen = false;
  for (var i = 0; i < series.length; i++) {
    var s = series[i];
    if (!s) continue;
    var name = String(s.name == null ? '' : s.name);
    var u = '';
    var m = name.match(/\(([^()]*)\)\s*$/);
    if (m && /^[A-Za-z%\/ ]{1,10}$/.test(m[1])) {
      u = m[1].replace(/^\s+|\s+$/g, '');
    } else if (/\s%$/.test(name)) {
      u = '%';
    }
    if (!seen) {
      seen = true;
      unit = u;
    } else if (u !== unit) {
      same = false;
    }
  }
  if (!seen || !same) return '';
  return unit;
}

function axFmt(v, unit) {
  var s;
  if (Math.abs(v) >= 1000) {
    s = String(Math.round(v / 100) / 10).replace(/\.0$/, '') + 'k';
  } else {
    s = String(Math.round(v * 10) / 10).replace(/\.0$/, '');
  }
  return unit ? s + ' ' + unit : s;
}

function axIncidentT(series) {
  if (Object.prototype.toString.call(series) !== '[object Array]') return null;
  var best = null;
  for (var i = 0; i < series.length; i++) {
    var s = series[i];
    if (!s || s.win !== 'incident') continue;
    var pts = s.pts;
    if (Object.prototype.toString.call(pts) !== '[object Array]') continue;
    for (var j = 0; j < pts.length; j++) {
      var p = pts[j];
      if (p && typeof p.t === 'number' && (best === null || p.t < best)) best = p.t;
    }
  }
  return best;
}

function axYLabels(top, unit, W, H, PAD) {
  function y(v) { return H - PAD - (v / top) * (H - 2 * PAD); }
  return '<text class="axis-label" x="' + (PAD + 2) + '" y="' + (y(top) + 9).toFixed(1) + '">' + axFmt(top, unit) + '</text>'
    + '<text class="axis-label" x="' + (PAD + 2) + '" y="' + (y(top / 2) - 3).toFixed(1) + '">' + axFmt(top / 2, unit) + '</text>'
    + '<text class="axis-label" x="' + (PAD + 2) + '" y="' + (y(0) - 2).toFixed(1) + '">' + axFmt(0, unit) + '</text>';
}

function axXLabels(items, H, PAD) {
  var out = [];
  for (var i = 0; i < items.length; i++) {
    var anchor = i === 0 ? 'start' : (i === items.length - 1 ? 'end' : 'middle');
    out.push('<text class="axis-label" x="' + items[i].x.toFixed(1) + '" y="' + (H - PAD - 14).toFixed(1) + '" text-anchor="' + anchor + '">+' + items[i].t + ' min</text>');
  }
  return out.join('');
}

function axIncidentLine(x, yTop, yBot) {
  return '<line x1="' + x.toFixed(1) + '" y1="' + yTop + '" x2="' + x.toFixed(1) + '" y2="' + yBot + '" class="marker"/>';
}
```

## Edit 1 of 5: `parts/11-charts-a.js`, `tlChart` (2 SEARCH/REPLACE blocks)
`tlChart` gains a third parameter `unit` and draws the y labels (against its own `top`) plus fixed x labels at minutes 0, 60 and 119 (the shared 120-minute timeline). Markers are unchanged: the caller passes the incident marker, drawn with `class="marker"`.

### parts/11-charts-a.js
<<<<<<< SEARCH
function tlChart(series, markers) {
=======
function tlChart(series, markers, unit) {
>>>>>>> REPLACE
### parts/11-charts-a.js
<<<<<<< SEARCH
  return '<figure class="ml"><figcaption>' + esc(cap) + '</figcaption>'
    + '<svg viewBox="0 0 ' + W + ' ' + H + '" role="img" aria-label="' + esc(cap) + '" preserveAspectRatio="none">'
    + '<line x1="' + PAD + '" y1="' + (H - PAD) + '" x2="' + (W - PAD) + '" y2="' + (H - PAD) + '" class="axis"/>'
    + mkLines.join('') + paths.join('') + mkTexts.join('') + '</svg>' + legend + '</figure>';
=======
  return '<figure class="ml"><figcaption>' + esc(cap) + '</figcaption>'
    + '<svg viewBox="0 0 ' + W + ' ' + H + '" role="img" aria-label="' + esc(cap) + '" preserveAspectRatio="none">'
    + '<line x1="' + PAD + '" y1="' + (H - PAD) + '" x2="' + (W - PAD) + '" y2="' + (H - PAD) + '" class="axis"/>'
    + axYLabels(top, unit || '', W, H, PAD)
    + axXLabels([{ t: 0, x: x(0) }, { t: 60, x: x(60) }, { t: TMAX, x: x(TMAX) }], H, PAD)
    + mkLines.join('') + paths.join('') + mkTexts.join('') + '</svg>' + legend + '</figure>';
>>>>>>> REPLACE

## Edit 2 of 5: `parts/11-charts-a.js`, `stackedArea` (2 SEARCH/REPLACE blocks)
`stackedArea` gains `unit`, `colsT`, `incT`. `colsT` is the sorted array of minute values behind the stacked columns (from `evAreaCols`); when it is an array whose length equals the column count, draw x labels for the first, middle (`Math.floor((cols - 1) / 2)`) and last columns, and when `incT` is a number draw one marker at the first column whose minute is `>= incT`. Y labels always drawn against `top`.

### parts/11-charts-a.js
<<<<<<< SEARCH
function stackedArea(series, pct) {
=======
function stackedArea(series, pct, unit, colsT, incT) {
>>>>>>> REPLACE
### parts/11-charts-a.js
<<<<<<< SEARCH
  var cap = 'Stacked ' + names.join(', ');
  return '<figure class="ml"><figcaption>' + cap + '</figcaption>'
    + '<svg viewBox="0 0 ' + W + ' ' + H + '" role="img" aria-label="' + cap + '" preserveAspectRatio="none">'
    + '<line x1="' + PAD + '" y1="' + (H - PAD) + '" x2="' + (W - PAD) + '" y2="' + (H - PAD) + '" class="axis"/>'
    + paths.join('') + '</svg><p class="legend">' + items.join(' ') + '</p></figure>';
=======
  var cap = 'Stacked ' + names.join(', ');
  var ax = axYLabels(top, unit || '', W, H, PAD);
  if (Object.prototype.toString.call(colsT) === '[object Array]' && colsT.length === cols) {
    var midC = Math.floor((cols - 1) / 2);
    ax += axXLabels([
      { t: colsT[0], x: x(0) },
      { t: colsT[midC], x: x(midC) },
      { t: colsT[cols - 1], x: x(cols - 1) }
    ], H, PAD);
    if (typeof incT === 'number') {
      for (var mi = 0; mi < cols; mi++) {
        if (colsT[mi] >= incT) {
          ax += axIncidentLine(x(mi), PAD, H - PAD);
          break;
        }
      }
    }
  }
  return '<figure class="ml"><figcaption>' + cap + '</figcaption>'
    + '<svg viewBox="0 0 ' + W + ' ' + H + '" role="img" aria-label="' + cap + '" preserveAspectRatio="none">'
    + '<line x1="' + PAD + '" y1="' + (H - PAD) + '" x2="' + (W - PAD) + '" y2="' + (H - PAD) + '" class="axis"/>'
    + ax + paths.join('') + '</svg><p class="legend">' + items.join(' ') + '</p></figure>';
>>>>>>> REPLACE

## Edit 3 of 5: `parts/12-evidence-a.js`, add `evAreaCols` (1 SEARCH/REPLACE block)
New global `evAreaCols(series)`: every distinct numeric `t` from points that have numeric `t` and `v`, sorted ascending. This is the same column set `evAreaSeries` stacks onto. The SEARCH is the last two lines of the file; the REPLACE appends the new function after them.

### parts/12-evidence-a.js
<<<<<<< SEARCH
  return result;
}
=======
  return result;
}

function evAreaCols(series) {
  var cols = [];
  if (Object.prototype.toString.call(series) !== '[object Array]') {
    return cols;
  }
  for (var i = 0; i < series.length; i++) {
    var s = series[i];
    if (!s || Object.prototype.toString.call(s.pts) !== '[object Array]') {
      continue;
    }
    for (var k = 0; k < s.pts.length; k++) {
      var p = s.pts[k];
      if (!p) {
        continue;
      }
      if (typeof p.t !== 'number' || typeof p.v !== 'number') {
        continue;
      }
      var seen = false;
      for (var j = 0; j < cols.length; j++) {
        if (cols[j] === p.t) {
          seen = true;
          break;
        }
      }
      if (!seen) {
        cols.push(p.t);
      }
    }
  }
  cols.sort(function (a, b) { return a - b; });
  return cols;
}
>>>>>>> REPLACE

## Edit 4 of 5: `parts/12-evidence-b.js`, `evBars` (2 SEARCH/REPLACE blocks)
`evBars` gains `unit` and `incT`. It draws y labels against `top`, x labels at the centres of the first, middle (`Math.floor((bars.length - 1) / 2)`) and last bars, and when `incT` is a number one marker at the left edge (`PAD + bi * sw`) of the first bar with `win === 'incident'`.

### parts/12-evidence-b.js
<<<<<<< SEARCH
function evBars(series) {
=======
function evBars(series, unit, incT) {
>>>>>>> REPLACE
### parts/12-evidence-b.js
<<<<<<< SEARCH
  cap = capNames.join(' and ');

  return '<figure class="ml"><figcaption>' + cap +
    '</figcaption><svg viewBox="0 0 640 140" role="img" aria-label="' + cap +
    '" preserveAspectRatio="none"><line x1="4" y1="136" x2="636" y2="136" class="axis"/>' +
    rects +
    '</svg><p class="legend"><span class="key normal"></span> Normal <span class="key incident"></span> Incident</p></figure>';
=======
  cap = capNames.join(' and ');

  var ax = axYLabels(top, unit || '', W, H, PAD);
  var midB = Math.floor((bars.length - 1) / 2);
  ax += axXLabels([
    { t: bars[0].t, x: PAD + sw / 2 },
    { t: bars[midB].t, x: PAD + midB * sw + sw / 2 },
    { t: bars[bars.length - 1].t, x: PAD + (bars.length - 1) * sw + sw / 2 }
  ], H, PAD);
  if (typeof incT === 'number') {
    for (var bi = 0; bi < bars.length; bi++) {
      if (bars[bi].win === 'incident') {
        ax += axIncidentLine(PAD + bi * sw, PAD, H - PAD);
        break;
      }
    }
  }

  return '<figure class="ml"><figcaption>' + cap +
    '</figcaption><svg viewBox="0 0 640 140" role="img" aria-label="' + cap +
    '" preserveAspectRatio="none"><line x1="4" y1="136" x2="636" y2="136" class="axis"/>' +
    ax +
    rects +
    '</svg><p class="legend"><span class="key normal"></span> Normal <span class="key incident"></span> Incident</p></figure>';
>>>>>>> REPLACE

## Edit 5 of 5: `parts/12-evidence-c.js`, `evChart` (1 SEARCH/REPLACE block)
`evChart` computes the unit with `axUnit(c.series)` and the incident minute with `axIncidentT(c.series)` and passes them down. The line branch uses the computed incident minute for its marker (instead of the hardcoded 60) and passes no markers when the fixture has no incident points; the stacked-area branch also passes `evAreaCols(c.series)`. The `fpRollupAny` inserts and the `chart:'none'` text stay byte-identical.

### parts/12-evidence-c.js
<<<<<<< SEARCH
function evChart(c) {
  if (!c) return '';
  if (c.chart === 'line') return tlChart(c.series, [{t:60, label:'Incident starts'}]) + (typeof fpRollupAny === 'function' ? fpRollupAny() : '') + evChartCaption(c);
  if (c.chart === 'stackedArea') return stackedArea(evAreaSeries(c.series), false) + (typeof fpRollupAny === 'function' ? fpRollupAny() : '') + evChartCaption(c);
  if (c.chart === 'bars') return evBars(c.series) + evChartCaption(c);
  return '<p class="note">This was not collected for this case, so there is no chart.</p>';
}
=======
function evChart(c) {
  if (!c) return '';
  if (c.chart === 'line') {
    var lt = axIncidentT(c.series);
    return tlChart(c.series, lt === null ? [] : [{t:lt, label:'Incident starts'}], axUnit(c.series)) + (typeof fpRollupAny === 'function' ? fpRollupAny() : '') + evChartCaption(c);
  }
  if (c.chart === 'stackedArea') return stackedArea(evAreaSeries(c.series), false, axUnit(c.series), evAreaCols(c.series), axIncidentT(c.series)) + (typeof fpRollupAny === 'function' ? fpRollupAny() : '') + evChartCaption(c);
  if (c.chart === 'bars') return evBars(c.series, axUnit(c.series), axIncidentT(c.series)) + evChartCaption(c);
  return '<p class="note">This was not collected for this case, so there is no chart.</p>';
}
>>>>>>> REPLACE

## Check (you run it; the owner applies the blocks and runs the full check)
```
node --check parts/11-charts-axis.js && node --check parts/11-charts-a.js && node --check parts/12-evidence-a.js && node --check parts/12-evidence-b.js && node --check parts/12-evidence-c.js
```
Expected: no syntax errors. After the blocks are applied the owner runs, from `buildout/wireframes`: `node ../packets/data/chk-axis.js` and it prints `PASS` (it checks the tick labels, units and marker against the real `SCEN` fixtures for all three chart kinds, the no-data and no-incident cases, ES5 and the wiring).

## Block format
New file: `### parts/<name>` then a fenced code block. Edit: `### parts/<file>` then `<<<<<<< SEARCH`, old text, `=======`, new text, `>>>>>>> REPLACE` (no code fence, no blank line after the `###` line). New-file block first, then edits in file order (`11-charts-a.js`, `12-evidence-a.js`, `12-evidence-b.js`, `12-evidence-c.js`).

## Output
The blocks in one plain reply, then exactly 3 lines: `Status: PASS|FAIL`, `Checks: <printed output>`, `Concerns: none` (or one line).
