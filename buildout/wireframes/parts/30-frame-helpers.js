// Step 0 helpers: time formatting, window-sanity banner, release lines, estimate line, DST notice.
var FP_MON = ['Jan','Feb','Mar','Apr','May','Jun','Jul','Aug','Sep','Oct','Nov','Dec'];

function fpMs(s) {
  return Date.parse(String(s || '') + 'Z');
}

function fpIso(ms) {
  return new Date(ms).toISOString().slice(0, 16);
}

function fpPad(n) {
  return n < 10 ? '0' + n : '' + n;
}

function fpWhen(ms) {
  var d = new Date(ms);
  return d.getUTCDate() + ' ' + FP_MON[d.getUTCMonth()] + ' ' + fpPad(d.getUTCHours()) + ':' + fpPad(d.getUTCMinutes()) + ' UTC';
}

function fpDate(ms) {
  var d = new Date(ms);
  return d.getUTCDate() + ' ' + FP_MON[d.getUTCMonth()] + ' ' + d.getUTCFullYear();
}

function fpDur(ms) {
  var m = Math.round(ms / 60000);
  var n;
  if (m >= 1440 && m % 1440 === 0) {
    n = m / 1440;
    return n + ' day' + (n === 1 ? '' : 's');
  }
  if (m >= 60 && m % 60 === 0) {
    n = m / 60;
    return n + ' hour' + (n === 1 ? '' : 's');
  }
  if (m < 60) {
    return m + ' minute' + (m === 1 ? '' : 's');
  }
  var h = Math.floor(m / 60);
  var mm = m % 60;
  return h + ' h ' + mm + ' min';
}

function fpShort(ms) {
  var m = Math.round(ms / 60000);
  var n;
  if (m >= 1440 && m % 1440 === 0) {
    n = m / 1440;
    return n + 'd';
  }
  if (m >= 60 && m % 60 === 0) {
    n = m / 60;
    return n + 'h';
  }
  if (m < 60) {
    return m + 'm';
  }
  var h = Math.floor(m / 60);
  var mm = m % 60;
  return h + 'h ' + mm + 'm';
}

function fpKey(kind) {
  return kind + '|' + W.frame.incidentStart + '|' + W.frame.incidentEnd + '|' + W.frame.referenceStart + '|' + W.frame.referenceEnd;
}

function frameBannerKind() {
  var f = W.frame;
  var a = fpMs(f.incidentStart);
  var b = fpMs(f.incidentEnd);
  var c = fpMs(f.referenceStart);
  var d = fpMs(f.referenceEnd);
  if (isNaN(a) || isNaN(b) || isNaN(c) || isNaN(d) || b <= a || d <= c) {
    return '';
  }
  if (c < b && a < d) {
    return 'overlap';
  }
  if (d === a) {
    return 'touching';
  }
  if ((d - c) > 2 * (b - a) || (b - a) > 2 * (d - c)) {
    return 'ratio';
  }
  return '';
}

function frameKeepTouching() {
  W.frameKeep = fpKey('touching');
}

function frameBanner() {
  var kind = frameBannerKind();
  if (kind === '') {
    return '';
  }
  if (typeof W.frameKeep !== 'string') {
    W.frameKeep = fpKey('touching');
  }
  if (W.frameKeep === fpKey(kind)) {
    return '';
  }
  var f = W.frame;
  var a = fpMs(f.incidentStart);
  var b = fpMs(f.incidentEnd);
  var c = fpMs(f.referenceStart);
  var d = fpMs(f.referenceEnd);
  var fix, label, text;
  if (kind === 'overlap') {
    fix = 'move';
    label = 'Move reference before the incident';
    text = 'The reference window overlaps the incident window by ' + fpDur(Math.min(b, d) - Math.max(a, c)) + '. Comparing against a period that includes the problem is not a fair baseline.';
  } else if (kind === 'touching') {
    fix = 'gap';
    label = 'Add a 5-minute gap';
    text = 'The reference window ends the instant the incident starts, with no gap between them. Calls right at the boundary may fall in either window.';
  } else {
    fix = 'match';
    label = 'Match the incident window length';
    var li = b - a;
    var lr = d - c;
    var ratio, r;
    if (lr > li) {
      ratio = lr / li;
      r = (ratio === Math.round(ratio)) ? String(Math.round(ratio)) : ratio.toFixed(1);
      text = 'The reference window is ' + r + '&times; the length of the incident window (' + fpShort(lr) + ' vs ' + fpShort(li) + '). Rates are compared per minute, but very different lengths make rare events harder to compare fairly.';
    } else {
      ratio = li / lr;
      r = (ratio === Math.round(ratio)) ? String(Math.round(ratio)) : ratio.toFixed(1);
      text = 'The incident window is ' + r + '&times; the length of the reference window (' + fpShort(li) + ' vs ' + fpShort(lr) + '). Rates are compared per minute, but very different lengths make rare events harder to compare fairly.';
    }
  }
  return '<div class="notice" role="status"><p>' + text + '</p><div class="row"><button class="btn sm" onclick="frameFix(\'' + fix + '\')">' + label + '</button><button class="btn sm" onclick="frameFix(\'keep\')">Keep anyway</button></div></div>';
}

function frameFix(kind) {
  var f = W.frame;
  var a = fpMs(f.incidentStart);
  var b = fpMs(f.incidentEnd);
  var c = fpMs(f.referenceStart);
  var d = fpMs(f.referenceEnd);
  if (isNaN(a) || isNaN(b) || isNaN(c) || isNaN(d)) {
    render();
    return;
  }
  var li = b - a;
  var lr = d - c;
  if (kind === 'keep') {
    W.frameKeep = fpKey(frameBannerKind());
  } else if (kind === 'move') {
    var e = a - 300000;
    f.referenceEnd = fpIso(e);
    f.referenceStart = fpIso(e - lr);
  } else if (kind === 'gap') {
    f.referenceStart = fpIso(c - 300000);
    f.referenceEnd = fpIso(d - 300000);
  } else if (kind === 'match') {
    f.referenceStart = fpIso(d - li);
  }
  render();
}

function frameReleaseHint() {
  if (W.frame.releases.length === 0) {
    return '<p class="note">If you know of a deploy, flag flip or config change near the start of the slowdown, add it here. Unravis does not assume one happened.</p>';
  }
  return '';
}

function frameReleaseLine(r) {
  var t = fpMs(r.at);
  var a = fpMs(W.frame.incidentStart);
  var b = fpMs(W.frame.incidentEnd);
  if (!(t >= a && t <= b)) {
    return '';
  }
  return '<p class="note" role="status">This change is inside the incident window (' + fpWhen(t) + '). It may be the cause rather than just a marker &mdash; Step 5 will line it up against the onset.</p>';
}

function frameEstimateLine() {
  var f = W.frame;
  var a = fpMs(f.incidentStart);
  var b = fpMs(f.incidentEnd);
  var c = fpMs(f.referenceStart);
  var d = fpMs(f.referenceEnd);
  if (isNaN(a) || isNaN(b) || isNaN(c) || isNaN(d) || b <= a || d <= c) {
    return '';
  }
  var n = (typeof W.srcCount === 'number' && W.srcCount > 0) ? W.srcCount : 2;
  var mins = ((b - a) + (d - c)) / 60000;
  var rows = Math.round(mins * 20 * n);
  var rowsText;
  if (rows < 1000) {
    rowsText = String(rows);
  } else if (rows < 1000000) {
    var p = Math.pow(10, String(rows).length - 3);
    var r = Math.round(rows / p) * p;
    rowsText = String(r).replace(/\B(?=(\d{3})+(?!\d))/g, ',');
  } else {
    rowsText = (Math.floor(rows / 100000) / 10).toFixed(1) + 'M';
  }
  var timeText;
  if (rows < 10000) {
    timeText = '3 s';
  } else if (rows < 200000) {
    timeText = '20 s';
  } else if (rows < 1000000) {
    timeText = '1 min';
  } else {
    timeText = '4 min';
  }
  var long = (b - a) > 86400000 || (d - c) > 86400000;
  return '<p class="note" role="status">This reads &asymp; ' + rowsText + ' calls from ' + n + ' source' + (n === 1 ? '' : 's') + ' and takes about ' + timeText + '.' + (long ? ' Shown as hourly averages &mdash; see below.' : '') + '</p>';
}

function fpNthSunday(y, m, n) {
  var first = new Date(Date.UTC(y, m, 1)).getUTCDay();
  return Date.UTC(y, m, 1 + ((7 - first) % 7) + (n - 1) * 7);
}

function fpLastSunday(y, m) {
  var last = new Date(Date.UTC(y, m + 1, 0));
  return Date.UTC(y, m, last.getUTCDate() - last.getUTCDay());
}

function frameDst() {
  var f = W.frame;
  var vals = [fpMs(f.incidentStart), fpMs(f.incidentEnd), fpMs(f.referenceStart), fpMs(f.referenceEnd)];
  var valid = [];
  var i;
  for (i = 0; i < vals.length; i++) {
    if (!isNaN(vals[i])) {
      valid.push(vals[i]);
    }
  }
  if (valid.length === 0) {
    return '';
  }
  var lo = valid[0], hi = valid[0];
  for (i = 1; i < valid.length; i++) {
    if (valid[i] < lo) { lo = valid[i]; }
    if (valid[i] > hi) { hi = valid[i]; }
  }
  var y0 = new Date(lo).getUTCFullYear();
  var y1 = new Date(hi).getUTCFullYear();
  var dates = [];
  for (var y = y0; y <= y1; y++) {
    dates.push(fpNthSunday(y, 2, 2));
    dates.push(fpNthSunday(y, 10, 1));
    dates.push(fpLastSunday(y, 2));
    dates.push(fpLastSunday(y, 9));
  }
  dates.sort(function (x, z) { return x - z; });
  for (i = 0; i < dates.length; i++) {
    var d = dates[i];
    if (d < hi && d + 86400000 > lo) {
      return '<p class="note" role="status">This window crosses a daylight-saving change on ' + fpDate(d) + '. All times above stay in UTC; the same UTC time may map to a different local clock time before and after this date.</p>';
    }
  }
  return '';
}
