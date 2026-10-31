// Evidence banners: pretend a dependency source is missing (packet B2).
// Globals used from elsewhere: W (state), render(), EVDEP_DEFS, EVDEP_MAP.

var EVDEP_NAMES = {
  querystore: 'Query Store',
  qswaits: 'Query Store wait stats',
  systemhealth: 'system_health capture',
  viewstate: 'VIEW SERVER STATE',
  profiler: 'Profiler trace',
  monitorrole: 'Monitor role',
  oplogread: 'oplog read',
  blackbox: 'Blackbox',
  perfaccess: 'performance counter access'
};

function evEsc(s) {
  return String(s)
    .split('&').join('&amp;')
    .split('<').join('&lt;')
    .split('>').join('&gt;')
    .split('"').join('&quot;');
}

function evDepsOff() {
  if (!W.deps || typeof W.deps !== 'object') { W.deps = {}; }
  return W.deps;
}

function evDepFor(c) {
  var keys = EVDEP_MAP[c.id] || [];
  var o = evDepsOff();
  var i, k;
  for (i = 0; i < keys.length; i++) {
    k = keys[i];
    if (o[k] === true && EVDEP_DEFS[k].soft === false) {
      return { key: k, def: EVDEP_DEFS[k], forced: false, soft: false };
    }
  }
  if (c.verdict === 'none') {
    for (i = 0; i < keys.length; i++) {
      k = keys[i];
      if (EVDEP_DEFS[k].soft === false) {
        return { key: k, def: EVDEP_DEFS[k], forced: true, soft: false };
      }
    }
  }
  for (i = 0; i < keys.length; i++) {
    k = keys[i];
    if (o[k] === true && EVDEP_DEFS[k].soft === true) {
      return { key: k, def: EVDEP_DEFS[k], forced: false, soft: true };
    }
  }
  return null;
}

function evDepBlocked(c) {
  var keys = EVDEP_MAP[c.id] || [];
  var o = evDepsOff();
  var i;
  for (i = 0; i < keys.length; i++) {
    if (o[keys[i]] === true && EVDEP_DEFS[keys[i]].soft === false) { return true; }
  }
  return false;
}

function evDepEffVerdict(c) {
  return evDepBlocked(c) ? 'none' : c.verdict;
}

function evDepBanner(c) {
  var d = evDepFor(c);
  if (d === null) { return ''; }
  var def = d.def;
  var h = '<div class="ev-dep" role="note"><p class="ev-dep-title"><b>Could not check:</b> '
    + evEsc(def.title) + '</p><p class="ev-dep-why">' + evEsc(def.why)
    + '</p><p class="ev-dep-effect">' + evEsc(def.effect) + '</p>';
  if (def.fix && def.fix.length > 0) {
    var cmd = evEsc(def.fix.join('\n'));
    h += '<div class="ev-dep-fix"><p class="ev-dep-fixlabel">To turn it on, an administrator can run:</p><pre class="ev-cmd">'
      + cmd + '</pre><button type="button" class="ev-copy" onclick="evCopy(this)" data-copy="'
      + cmd + '">Copy command</button></div>';
  }
  h += '<p class="ev-dep-note">' + evEsc(def.note) + '</p></div>';
  return h;
}

function evDepToggle(key) {
  var o = evDepsOff();
  if (o[key]) { delete o[key]; } else { o[key] = true; }
  render();
}

function evDepToggles(group) {
  var o = evDepsOff();
  var h = '<div class="seg ev-dep-pick" role="group" aria-label="Pretend a source is missing">';
  var keys = Object.keys(EVDEP_DEFS);
  var i, k;
  for (i = 0; i < keys.length; i++) {
    k = keys[i];
    if (EVDEP_DEFS[k].group !== group) { continue; }
    h += '<button type="button" class="btn" aria-pressed="' + (o[k] ? 'true' : 'false')
      + '" onclick="evDepToggle(\'' + k + '\')">' + (o[k] ? 'Stop pretending: ' : 'Pretend off: ') + (EVDEP_NAMES[k] || k) + '</button>';
  }
  return h + '</div>';
}

function evCopy(btn) {
  var t = btn.getAttribute('data-copy') || '';
  var orig = btn.textContent;
  try {
    if (navigator.clipboard && navigator.clipboard.writeText) {
      navigator.clipboard.writeText(t);
    } else {
      var ta = document.createElement('textarea');
      ta.value = t;
      document.body.appendChild(ta);
      ta.select();
      document.execCommand('copy');
      document.body.removeChild(ta);
    }
  } catch (e) {}
  btn.textContent = 'Copied';
  setTimeout(function () { btn.textContent = orig; }, 1500);
}
