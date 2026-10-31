function evChartCaption(c) {
  function x(s) {
    return String(s == null ? '' : s)
      .replace(/&/g, '&amp;')
      .replace(/</g, '&lt;')
      .replace(/>/g, '&gt;')
      .replace(/"/g, '&quot;');
  }
  if (!c || (c.chart !== 'line' && c.chart !== 'stackedArea' && c.chart !== 'bars')) {
    return '';
  }
  var series = Object.prototype.toString.call(c.series) === '[object Array]' ? c.series : [];
  var hasNormal = false, hasIncident = false;
  for (var hci = 0; hci < series.length; hci++) {
    if (!series[hci]) { continue; }
    if (series[hci].win === 'incident') { hasIncident = true; } else { hasNormal = true; }
  }
  var oneCallDur = series.length === 1 && series[0] && String(series[0].name).replace(/\s*\([^()]*\)\s*$/, '') === 'Call duration';
  if (c.chart === 'bars' && oneCallDur) {
    return '<p class="ev-cap">Each bar is one call. A taller bar is a slower call.</p>';
  }
  if (c.chart === 'bars' && hasIncident && !hasNormal && !oneCallDur) {
    return '<p class="ev-cap">These bars are the incident hour.</p>';
  }
  function unitOf(name) {
    var s = String(name == null ? '' : name);
    var m = s.match(/\(([^()]*)\)\s*$/);
    if (m && /^[A-Za-z%\/ ]{1,10}$/.test(m[1])) {
      return String(m[1]).replace(/^\s+|\s+$/g, '');
    }
    if (/\s%$/.test(s)) {
      return '%';
    }
    return '';
  }
  var unit = '';
  var same = true;
  var seen = false;
  for (var i = 0; i < series.length; i++) {
    if (!series[i]) { continue; }
    var u = unitOf(series[i].name);
    if (!seen) {
      seen = true;
      unit = u;
    } else if (u !== unit) {
      same = false;
    }
  }
  if (!seen) { same = false; }
  var max = 0;
  if (c.chart === 'stackedArea') {
    var cols = evAreaSeries(series);
    var n = 0;
    if (Object.prototype.toString.call(cols) === '[object Array]' && cols.length &&
        Object.prototype.toString.call(cols[0].vals) === '[object Array]') {
      n = cols[0].vals.length;
    }
    for (var k = 0; k < n; k++) {
      var total = 0;
      for (var j = 0; j < cols.length; j++) {
        var vj = cols[j].vals[k];
        if (typeof vj === 'number' && !isNaN(vj)) {
          total += vj;
        }
      }
      if (total > max) { max = total; }
    }
  } else {
    for (var a = 0; a < series.length; a++) {
      var s = series[a];
      if (!s || Object.prototype.toString.call(s.pts) !== '[object Array]') { continue; }
      for (var b = 0; b < s.pts.length; b++) {
        var p = s.pts[b];
        if (typeof p === 'object' && p !== null && typeof p.v === 'number' && !isNaN(p.v)) {
          if (p.v > max) { max = p.v; }
        }
      }
    }
  }
  var t = 'Left part: the reference hour (a normal period). Right part: the incident hour. ';
  if (c.chart === 'line') {
    t += 'The dashed line marks when the incident starts. ';
  } else if (c.chart === 'stackedArea') {
    t += 'The bands are stacked, so the top edge is the total. ';
  } else {
    t += 'Each bar is one reading; the highlighted bars are from the incident hour. ';
  }
  if (max > 0) {
    if (same && unit !== '') {
      t += 'The bottom is 0 and the top is about ' + (Math.round(max * 10) / 10) + ' ' + unit + '.';
    } else {
      t += 'The bottom is 0 and the heights are relative; read the exact numbers in the table.';
    }
  }
  return '<p class="ev-cap">' + x(t.replace(/\s+$/, '')) + '</p>';
}
