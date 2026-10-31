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
