function evBars(series, unit, incT) {
  function esc(s) {
    return String(s)
      .replace(/&/g, '&amp;')
      .replace(/</g, '&lt;')
      .replace(/>/g, '&gt;')
      .replace(/"/g, '&quot;');
  }

  var EMPTY = '<p class="note">Not enough data to draw this chart.</p>';

  if (Object.prototype.toString.call(series) !== '[object Array]') {
    return EMPTY;
  }

  var bars = [];
  var i, j, s, p;
  for (i = 0; i < series.length; i++) {
    s = series[i];
    if (!s || !s.pts) continue;
    for (j = 0; j < s.pts.length; j++) {
      p = s.pts[j];
      if (!p) continue;
      if (typeof p.t !== 'number' || typeof p.v !== 'number') continue;
      if (isNaN(p.t) || isNaN(p.v)) continue;
      bars.push({ name: s.name, win: s.win, t: p.t, v: p.v });
    }
  }

  if (bars.length === 0) {
    return EMPTY;
  }

  var W = 640, H = 140, PAD = 4;
  var top = 1;
  for (i = 0; i < bars.length; i++) {
    if (bars[i].v > top) top = bars[i].v;
  }

  var sw = (W - 2 * PAD) / bars.length;
  var bw = sw * 0.7;
  if (bw < 3) bw = 3;

  var rects = '';
  var bar, x, h, y, fill, name, capNames, seen, key, cap;

  for (i = 0; i < bars.length; i++) {
    bar = bars[i];
    x = PAD + i * sw + (sw - bw) / 2;
    h = (bar.v > 0 ? bar.v : 0) / top * (H - 2 * PAD);
    y = H - PAD - h;
    fill = bar.win === 'incident' ? 'var(--series-4)' : 'var(--series-1)';
    name = esc(bar.name);
    rects += '<rect x="' + x.toFixed(1) + '" y="' + y.toFixed(1) +
      '" width="' + bw.toFixed(1) + '" height="' + h.toFixed(1) +
      '" rx="2" style="fill:' + fill + '"><title>' + name +
      ', minute ' + bar.t.toFixed(1) + ': ' + bar.v.toFixed(1) +
      '</title></rect>';
  }

  capNames = [];
  seen = {};
  for (i = 0; i < bars.length; i++) {
    key = String(bars[i].name);
    if (!seen[key]) {
      seen[key] = true;
      capNames.push(esc(bars[i].name));
    }
  }
  cap = capNames.join(' and ');

  var hasN = false, hasI = false;
  for (i = 0; i < bars.length; i++) { if (bars[i].win === 'incident') hasI = true; else hasN = true; }

  var ax = axYLabels(top, unit || '', W, H, PAD);
  var midB = Math.floor((bars.length - 1) / 2);
  ax += axXLabels([
    { t: bars[0].t, x: PAD + sw / 2 },
    { t: bars[midB].t, x: PAD + midB * sw + sw / 2 },
    { t: bars[bars.length - 1].t, x: PAD + (bars.length - 1) * sw + sw / 2 }
  ], H, PAD);
  if (typeof incT === 'number' && hasN && hasI) {
    for (var bi = 0; bi < bars.length; bi++) {
      if (bars[bi].win === 'incident') {
        ax += axIncidentLine(PAD + bi * sw, PAD, H - PAD);
        break;
      }
    }
  }

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
