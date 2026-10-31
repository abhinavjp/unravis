// Swimlane: one horizontal lane per series (API, SQL, Mongo, Host...), events as bars or dots on a shared minute axis, an optional time brush.
function swimlane(lanes, window, brush) {
  var W = 640, GUT = 80, LANE_H = 28, AXIS_H = 24, PAD = 4;

  function esc(s) {
    return String(s == null ? '' : s)
      .replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;');
  }

  var win = window || {};
  var from = typeof win.from === 'number' ? win.from : 0;
  var to = typeof win.to === 'number' ? win.to : 119;
  if (!(to > from)) { from = 0; to = 119; }

  var hasBrush = !!(brush && typeof brush.from === 'number' && typeof brush.to === 'number' && brush.to >= brush.from);
  var bFrom = hasBrush ? brush.from : 0;
  var bTo = hasBrush ? brush.to : 0;

  var list = lanes || [];
  var span = to - from;

  function x(m) { return GUT + ((m - from) / span) * (W - GUT); }
  function sevStyle(sev) {
    if (sev === 'ok') return 'fill:var(--good)';
    if (sev === 'warn') return 'fill:var(--warn)';
    if (sev === 'bad') return 'fill:var(--bad)';
    return 'fill:var(--text-2)';
  }

  var H = AXIS_H + list.length * LANE_H + PAD;
  var body = '';
  var any = false;
  for (var i = 0; i < list.length; i++) {
    var lane = list[i] || {};
    var evs = lane.events || [];
    var yTop = AXIS_H + i * LANE_H;
    body += '<text x="' + PAD + '" y="' + (yTop + LANE_H / 2) + '" class="axis-label">' + esc(lane.name) + '</text>';
    body += '<line x1="' + GUT + '" y1="' + (yTop + LANE_H) + '" x2="' + W + '" y2="' + (yTop + LANE_H) + '" class="axis"/>';
    for (var j = 0; j < evs.length; j++) {
      var e = evs[j];
      if (!e || typeof e.t !== 'number') continue;
      var t = e.t, dur = e.dur || 0, end = t + dur;
      if (t > to || end < from) continue;
      any = true;
      var cls = (hasBrush && t <= bTo && end >= bFrom) ? 'ev in-brush' : 'ev';
      var title = esc(lane.name) + ': ' + esc(e.label) + ' (minute ' + t + (dur ? ' for ' + dur + ' min' : '') + ')';
      if (dur > 0) {
        var w = x(end) - x(t);
        if (!(w >= 3)) w = 3;
        body += '<rect class="' + cls + '" x="' + x(t).toFixed(1) + '" y="' + (yTop + 5) + '" width="' + w.toFixed(1)
          + '" height="' + (LANE_H - 10) + '" rx="2" style="' + sevStyle(e.sev) + '"><title>' + title + '</title></rect>';
      } else {
        body += '<circle class="' + cls + '" cx="' + x(t).toFixed(1) + '" cy="' + (yTop + LANE_H / 2)
          + '" r="4" style="' + sevStyle(e.sev) + '"><title>' + title + '</title></circle>';
      }
    }
  }

  if (!any) return '<p class="note">No activity to show.</p>';

  var axis = '<line x1="' + GUT + '" y1="' + AXIS_H + '" x2="' + W + '" y2="' + AXIS_H + '" class="axis"/>';
  for (var m = from; m <= to; m += 15) {
    var tx = x(m).toFixed(1);
    axis += '<line x1="' + tx + '" y1="' + (AXIS_H - 5) + '" x2="' + tx + '" y2="' + AXIS_H + '" class="axis"/>'
      + '<text x="' + tx + '" y="' + (AXIS_H - 8) + '" class="axis-label" text-anchor="middle">+' + (m - from) + '</text>';
  }

  var band = '';
  if (hasBrush) {
    var bx1 = x(Math.max(bFrom, from)), bx2 = x(Math.min(bTo, to));
    var bw = bx2 - bx1;
    if (!(bw > 0)) bw = 1;
    band = '<rect class="brush" x="' + bx1.toFixed(1) + '" y="' + AXIS_H + '" width="' + bw.toFixed(1)
      + '" height="' + (list.length * LANE_H) + '" style="fill:var(--accent);fill-opacity:.15"/>';
  }

  var legend = '<p class="legend"><span class="key" style="background:var(--good)"></span> ok '
    + '<span class="key" style="background:var(--warn)"></span> warn '
    + '<span class="key" style="background:var(--bad)"></span> bad'
    + (hasBrush ? ' Shaded band is the selected time slice' : '') + '</p>';

  return '<figure class="ml"><figcaption>Nearby activity <span class="range">minute +' + from + ' to +' + to + '</span></figcaption>'
    + '<svg viewBox="0 0 ' + W + ' ' + H + '" role="img" aria-label="Nearby activity" preserveAspectRatio="none">'
    + axis + band + body + '</svg>' + legend + '</figure>';
}
