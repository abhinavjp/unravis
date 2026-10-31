// Heatmap: a rows x cols grid of value cells, used by the host-resource heatmap and the
// recurrence day-by-hour calendar. Pure function, returns an SVG string, no DOM access.
function heatmap(cells, cols, rows) {
  function esc(s) {
    return String(s).replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;');
  }
  if (!cols || !rows || !cols.length || !rows.length) {
    return '<p class="note">Not enough data to draw this chart.</p>';
  }
  var GUT = 56, TOP = 18, CH = 16, W = 640;
  var H = Math.max(80, TOP + rows.length * CH);
  var cw = (W - GUT) / cols.length;
  cells = cells || [];
  var i, max = 0;
  for (i = 0; i < cells.length; i++) {
    var cv = cells[i] ? cells[i].v : null;
    if (typeof cv === 'number' && cv > max) max = cv;
  }
  function level(v) {
    if (typeof v !== 'number') return 0;
    if (max <= 0 || v <= 0) return 1;
    var l = Math.ceil((v / max) * 4);
    return l < 1 ? 1 : (l > 4 ? 4 : l);
  }
  var fills = {
    0: 'var(--surface-2)',
    1: 'color-mix(in srgb, var(--bad) 20%, var(--surface))',
    2: 'color-mix(in srgb, var(--bad) 40%, var(--surface))',
    3: 'color-mix(in srgb, var(--bad) 65%, var(--surface))',
    4: 'color-mix(in srgb, var(--bad) 90%, var(--surface))'
  };
  var nth = Math.max(1, Math.ceil(cols.length / 12));
  var labels = [];
  for (i = 0; i < cols.length; i += nth) {
    labels.push('<text class="axis-label" x="' + (GUT + i * cw + cw / 2).toFixed(1) + '" y="12" text-anchor="middle">' + esc(cols[i]) + '</text>');
  }
  for (i = 0; i < rows.length; i++) {
    labels.push('<text class="axis-label" x="' + (GUT - 4) + '" y="' + (TOP + i * CH + 12) + '" text-anchor="end">' + esc(rows[i]) + '</text>');
  }
  var map = {};
  for (i = 0; i < cells.length; i++) {
    var cc = cells[i];
    if (cc && typeof cc.r === 'number' && typeof cc.c === 'number') map[cc.r + ':' + cc.c] = cc;
  }
  var rects = [];
  for (var r = 0; r < rows.length; r++) {
    for (var c = 0; c < cols.length; c++) {
      var cell = map[r + ':' + c];
      var v = cell ? cell.v : null;
      var tip = cell && cell.tip ? cell.tip : '';
      var lv = level(v);
      var t = rows[r] + ', ' + cols[c] + ': ' + (v == null ? 'no data' : v) + (tip ? ' (' + tip + ')' : '');
      rects.push('<rect class="cell l' + lv + '" x="' + (GUT + c * cw).toFixed(1) + '" y="' + (TOP + r * CH)
        + '" width="' + cw.toFixed(1) + '" height="' + CH + '" rx="2" style="fill:' + fills[lv] + '">'
        + '<title>' + esc(t) + '</title></rect>');
    }
  }
  var svg = '<svg role="img" aria-label="Heatmap" viewBox="0 0 ' + W + ' ' + H + '" preserveAspectRatio="none">'
    + labels.join('') + rects.join('') + '</svg>';
  var legend = '<p class="legend"><span class="key hm0"></span> No data <span class="key hm1"></span> Low <span class="key hm2"></span> Medium <span class="key hm3"></span> High <span class="key hm4"></span> Very high</p>';
  return '<figure class="ml"><figcaption>Heatmap</figcaption>' + svg + legend + '</figure>';
}
