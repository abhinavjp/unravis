function recurInit() {
  if (typeof W.recur !== 'object' || W.recur === null) {
    W.recur = { mode: 'one', st: '09:00', en: '10:00', n: 6, wd: 4 };
  }
  return W.recur;
}

var RECUR_WD = ['Sunday', 'Monday', 'Tuesday', 'Wednesday', 'Thursday', 'Friday', 'Saturday'];

function recurSet(key, val) {
  var r = recurInit();
  if (key === 'n') {
    var n = parseInt(val, 10);
    if (isNaN(n)) n = 1;
    var max = recurLimit(r.mode);
    if (n < 1) n = 1;
    if (n > max) n = max;
    r.n = n;
  } else if (key === 'wd') {
    var wd = parseInt(val, 10);
    r.wd = isNaN(wd) ? 4 : wd;
  } else {
    r[key] = String(val);
  }
  render();
}

function recurMode(m) {
  recurInit().mode = m;
  render();
}

function recurUnit(mode, n) {
  if (mode === 'days') return n === 1 ? 'day' : 'days';
  if (mode === 'weeks') return n === 1 ? 'week' : 'weeks';
  if (mode === 'month') return n === 1 ? 'month' : 'months';
  return '';
}

function recurLimit(mode) {
  if (mode === 'days') return 30;
  if (mode === 'weeks') return 26;
  if (mode === 'month') return 24;
  return 1;
}

function recurOcc() {
  var r = recurInit();
  var timeRe = /^([01]\d|2[0-3]):[0-5]\d$/;
  if (!timeRe.test(r.st) || !timeRe.test(r.en)) {
    return { ok: false, msg: 'Enter the start and end as HH:MM.' };
  }
  var stMin = parseInt(r.st.slice(0, 2), 10) * 60 + parseInt(r.st.slice(3, 5), 10);
  var enMin = parseInt(r.en.slice(0, 2), 10) * 60 + parseInt(r.en.slice(3, 5), 10);
  if (!(enMin > stMin)) {
    return { ok: false, msg: 'The end time must be after the start time.' };
  }
  var a = fpMs(W.frame.incidentStart);
  if (isNaN(a)) {
    return { ok: false, msg: 'Set the incident window first.' };
  }
  var day0 = Math.floor(a / 86400000) * 86400000;
  var max = recurLimit(r.mode);
  var n = r.n;
  if (n < 1) n = 1;
  if (n > max) n = max;
  var list = [];
  var k;
  if (r.mode === 'days') {
    for (k = 1; k <= n; k++) {
      list.push(day0 - k * 86400000);
    }
  } else if (r.mode === 'weeks') {
    var d = day0 - 86400000;
    while (new Date(d).getUTCDay() !== r.wd) {
      d -= 86400000;
    }
    var first = d;
    for (k = 1; k <= n; k++) {
      list.push(first - (k - 1) * 7 * 86400000);
    }
  } else if (r.mode === 'month') {
    var base = new Date(day0);
    var y = base.getUTCFullYear();
    var m = base.getUTCMonth();
    for (k = 1; k <= n; k++) {
      var norm = new Date(Date.UTC(y, m - k, 1));
      var ny = norm.getUTCFullYear();
      var nm = norm.getUTCMonth();
      var last = new Date(Date.UTC(ny, nm + 1, 0));
      var dl = last.getUTCDate();
      var wl = last.getUTCDay();
      var day = dl - ((wl - 4 + 7) % 7);
      list.push(Date.UTC(ny, nm, day));
    }
  }
  return {
    ok: true,
    list: list,
    n: n,
    unit: recurUnit(r.mode, n),
    mode: r.mode,
    st: r.st,
    en: r.en
  };
}

function recurSummary(o) {
  var oldest = o.list[0];
  var newest = o.list[0];
  for (var i = 0; i < o.list.length; i++) {
    if (o.list[i] < oldest) oldest = o.list[i];
    if (o.list[i] > newest) newest = o.list[i];
  }
  var desc;
  if (o.mode === 'days') {
    desc = 'each of the previous days';
  } else if (o.mode === 'weeks') {
    var r = recurInit();
    desc = 'the same weekday (' + RECUR_WD[r.wd] + ') on previous weeks';
  } else if (o.mode === 'month') {
    desc = 'the last Thursday of the month';
  }
  return '<p class="note" role="status">Comparing ' + o.st + '&ndash;' + o.en + ' UTC on ' + desc + ', for ' + o.n + ' ' + o.unit + ' (' + fpDate(oldest) + ' to ' + fpDate(newest) + ').</p>';
}

function recurHeat(o) {
  var rows = ['Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat', 'Sun'];
  var cols = [];
  var c;
  for (c = 0; c < 24; c++) {
    cols.push(fpPad(c));
  }
  var cells = [];
  var r;
  for (r = 0; r < 7; r++) {
    for (c = 0; c < 24; c++) {
      var v = 80 + (c >= 8 && c <= 18 ? 120 : 0) + r * 6 + ((r * 31 + c * 17) % 40);
      if (r === 3 && c === 9) v += 260;
      if (r >= 5 && c < 6) v = null;
      cells.push({ r: r, c: c, v: v, tip: 'p95 latency, ms' });
    }
  }
  var html = heatmap(cells, cols, rows);

  var sh = parseInt(o.st.slice(0, 2), 10);
  var eh = parseInt(o.en.slice(0, 2), 10);
  var enMinPart = parseInt(o.en.slice(3, 5), 10);
  var lastH = enMinPart === 0 ? eh - 1 : eh;

  var r2 = recurInit();
  var matchWd = o.mode === 'month' ? 4 : r2.wd;

  var hitIdx = {};
  for (r = 0; r < 7; r++) {
    var rowMatches = o.mode === 'days' ? true : ((r + 1) % 7 === matchWd);
    if (!rowMatches) continue;
    for (c = 0; c < 24; c++) {
      if (c >= sh && c <= lastH) {
        hitIdx[r * 24 + c + 1] = true;
      }
    }
  }

  var parts = html.split('<rect class="cell ');
  var normalDelim = '<rect class="cell ';
  var hitDelim = '<rect class="cell hit' + (o.mode === 'month' ? ' dash' : '') + ' ';
  var out = parts[0];
  for (var pi = 1; pi < parts.length; pi++) {
    out += (hitIdx[pi] ? hitDelim : normalDelim) + parts[pi];
  }
  html = out;
  return html
    + '<p class="note">Darker red = slower than normal for that hour. Light = close to normal. Gray = no data yet for that hour.</p>'
    + '<p class="note">' + (o.mode === 'month' ? 'Dashed border: the hours the rule matches, on only one such weekday a month (the last).' : 'Outlined cells: the hours the rule matches.') + '</p>';
}

function recurOverlay(o) {
  var shown = o.list.length < 8 ? o.list.length : 8;
  var list = o.list.slice(0, shown);
  var stMin = parseInt(o.st.slice(0, 2), 10) * 60 + parseInt(o.st.slice(3, 5), 10);
  var enMin = parseInt(o.en.slice(0, 2), 10) * 60 + parseInt(o.en.slice(3, 5), 10);
  var L = enMin - stMin;

  var html = '<svg role="img" aria-label="Overlay of ' + shown + ' occurrences, aligned to the window start" viewBox="0 0 640 140" class="recur-ov">';
  var k, i;
  for (k = 0; k < shown; k++) {
    var pts = '';
    for (i = 0; i <= 12; i++) {
      var x = Math.round((600 / 12) * i);
      var y = 110 - ((k * 37 + i * 17) % 23) * 3 - (k === 0 ? 14 : 0);
      pts += (i === 0 ? '' : ' ') + x + ',' + y;
    }
    html += '<polyline class="' + (k === 0 ? 'recur-line now' : 'recur-line') + '" points="' + pts + '"></polyline>';
  }
  html += '<text class="axis-label" x="0" y="136">0</text>';
  html += '<text class="axis-label" x="600" y="136" text-anchor="end">' + L + ' min</text>';
  html += '</svg>';
  html += '<p class="note">One line per occurrence, aligned so each starts at 0. The darkest line is the most recent.</p>';
  if (o.list.length > 8) {
    html += '<p class="note">Showing the most recent 8 of ' + o.list.length + ' occurrences; all ' + o.list.length + ' appear in the heatmap.</p>';
  }
  html += '<details class="ev-fold"><summary>Occurrence dates</summary><ul>';
  for (i = 0; i < o.list.length; i++) {
    html += '<li>' + fpDate(o.list[i]) + '</li>';
  }
  html += '</ul></details>';
  return html;
}

function frameRecurrence() {
  var r = recurInit();
  var html = '<fieldset class="wf recur"><legend>Compare with a repeating pattern (optional)</legend>';
  html += '<div role="radiogroup" aria-label="Repeat pattern">';

  var modes = [
    ['one', 'One window'],
    ['days', 'Same hours on the previous days'],
    ['weeks', 'Same weekday on previous weeks'],
    ['month', 'Last Thursday of every month']
  ];
  var i;
  for (i = 0; i < modes.length; i++) {
    var mv = modes[i][0];
    var mt = modes[i][1];
    html += '<label class="wf"><input type="radio" name="recur-mode" value="' + mv + '"' + (r.mode === mv ? ' checked' : '') + ' onchange="recurMode(\'' + mv + '\')"> ' + mt + '</label>';
  }
  html += '</div>';

  if (r.mode === 'one') {
    html += '<p class="note">The reference window above is used as it is.</p>';
    html += '</fieldset>';
    return html;
  }

  html += '<div class="pair">';
  html += '<label class="wf">Start (UTC)<input class="wf" type="time" value="' + r.st + '" onchange="recurSet(\'st\', this.value)"></label>';
  html += '<label class="wf">End (UTC)<input class="wf" type="time" value="' + r.en + '" onchange="recurSet(\'en\', this.value)"></label>';
  if (r.mode === 'weeks') {
    html += '<label class="wf">Weekday<select class="wf" onchange="recurSet(\'wd\', this.value)">';
    for (i = 0; i < RECUR_WD.length; i++) {
      html += '<option value="' + i + '"' + (r.wd === i ? ' selected' : '') + '>' + RECUR_WD[i] + '</option>';
    }
    html += '</select></label>';
  }
  var max = recurLimit(r.mode);
  var unitPlural = recurUnit(r.mode, 2);
  html += '<label class="wf">How many ' + unitPlural + ' back<input class="wf" type="number" min="1" max="' + max + '" value="' + r.n + '" onchange="recurSet(\'n\', this.value)"></label>';
  html += '</div>';

  var o = recurOcc();
  if (!o.ok) {
    html += '<p class="note" role="alert">' + o.msg + '</p>';
  } else {
    html += recurSummary(o) + recurHeat(o) + recurOverlay(o);
  }

  html += '</fieldset>';
  return html;
}
