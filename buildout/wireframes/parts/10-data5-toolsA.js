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
