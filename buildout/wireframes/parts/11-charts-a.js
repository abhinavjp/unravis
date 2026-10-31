// Chart helpers A: pure SVG string builders (no DOM access, ES5 only). tlChart draws the shared 120-minute
// timeline with per-window runs and optional event markers; stackedArea draws a stacked (optionally 100%) area chart.

function tlChart(series, markers, unit) {
  function esc(s) { return String(s).replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;'); }
  var W = 640, H = 140, PAD = 4, TMAX = 119;
  if (!series || !series.length) return '<p class="note">Not enough data to draw this chart.</p>';
  var i, j, pts, cls, run;
  var top = 1;
  for (i = 0; i < series.length; i++) {
    pts = series[i].pts || [];
    for (j = 0; j < pts.length; j++) if (typeof pts[j].v === 'number' && pts[j].v > top) top = pts[j].v;
  }
  var x = function(t){ return PAD + (t / TMAX) * (W - 2 * PAD); };
  var y = function(v){ return H - PAD - (v / top) * (H - 2 * PAD); };
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
  var mkLines = [], mkTexts = [], ms = markers || [];
  for (i = 0; i < ms.length; i++) {
    if (typeof ms[i].t !== 'number') continue;
    var mx = x(ms[i].t).toFixed(1);
    mkLines.push('<line x1="' + mx + '" y1="' + PAD + '" x2="' + mx + '" y2="' + (H - PAD) + '" class="marker"/>');
    mkTexts.push('<text x="' + mx + '" y="' + (PAD + 10) + '" class="mk-label">' + esc(ms[i].label == null ? '' : ms[i].label) + '</text>');
  }
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
  return '<figure class="ml"><figcaption>' + esc(cap) + '</figcaption>'
    + '<svg viewBox="0 0 ' + W + ' ' + H + '" role="img" aria-label="' + esc(cap) + '" preserveAspectRatio="none">'
    + '<line x1="' + PAD + '" y1="' + (H - PAD) + '" x2="' + (W - PAD) + '" y2="' + (H - PAD) + '" class="axis"/>'
    + axYLabels(top, unit || '', W, H, PAD)
    + axXLabels([{ t: 0, x: x(0) }, { t: 60, x: x(60) }, { t: TMAX, x: x(TMAX) }], H, PAD)
    + mkLines.join('') + paths.join('') + mkTexts.join('') + '</svg>' + legend + '</figure>';
}

function stackedArea(series, pct, unit, colsT, incT) {
  function esc(s) { return String(s).replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;'); }
  var W = 640, H = 140, PAD = 4;
  var cols = (series && series[0] && series[0].vals) ? series[0].vals.length : 0;
  if (!series || !series.length || cols < 2) return '<p class="note">Not enough data to draw this chart.</p>';
  var i, j, c;
  var cell = function(j, c) {
    var v = (series[j].vals || [])[c];
    return typeof v === 'number' ? v : 0;
  };
  var totals = [];
  for (c = 0; c < cols; c++) { totals.push(0); for (j = 0; j < series.length; j++) totals[c] += cell(j, c); }
  var top = pct ? 100 : 1;
  if (!pct) for (c = 0; c < cols; c++) if (totals[c] > top) top = totals[c];
  var val = function(j, c) {
    if (pct) return totals[c] > 0 ? (cell(j, c) / totals[c]) * 100 : 0;
    return cell(j, c);
  };
  var x = function(c){ return PAD + (c / (cols - 1)) * (W - 2 * PAD); };
  var y = function(v){ return H - PAD - (v / top) * (H - 2 * PAD); };
  var lo = [], hi = [];
  for (j = 0; j < series.length; j++) { lo.push([]); hi.push([]); }
  for (c = 0; c < cols; c++) {
    var cum = 0;
    for (j = 0; j < series.length; j++) { lo[j].push(cum); cum += val(j, c); hi[j].push(cum); }
  }
  var paths = [], names = [], items = [];
  for (j = 0; j < series.length; j++) {
    var d = [], k;
    for (c = 0; c < cols; c++) d.push((c ? 'L' : 'M') + x(c).toFixed(1) + ' ' + y(lo[j][c]).toFixed(1));
    for (k = cols - 1; k >= 0; k--) d.push('L' + x(k).toFixed(1) + ' ' + y(hi[j][k]).toFixed(1));
    paths.push('<path d="' + d.join(' ') + ' Z" class="area s-' + esc(series[j].cls) + '"/>');
    names.push(esc(series[j].name));
    items.push('<span class="key s-' + esc(series[j].cls) + '"></span> ' + esc(series[j].name));
  }
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
}
