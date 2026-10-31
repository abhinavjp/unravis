function evEsc2(s) {
  var r = String(s == null ? '' : s);
  r = r.split('&').join('&amp;');
  r = r.split('<').join('&lt;');
  r = r.split('>').join('&gt;');
  r = r.split('"').join('&quot;');
  return r;
}

function evImpact(c) {
  if (typeof EVIMPACT === 'object' && EVIMPACT && typeof EVIMPACT[c.id] === 'string' && EVIMPACT[c.id]) {
    return '<p class="ev-impact"><b>In plain words:</b> ' + evEsc2(EVIMPACT[c.id]) + '</p>';
  }
  return '';
}

function evTip(c, text) {
  var terms = [];
  if (typeof EVHELP === 'object' && EVHELP && typeof EVHELP[c.id] === 'object' && EVHELP[c.id] && Array.isArray(EVHELP[c.id].terms)) {
    terms = EVHELP[c.id].terms;
  }
  var s = evEsc2(text);
  if (!terms.length) {
    return s;
  }
  var list = [];
  for (var i = 0; i < terms.length; i++) {
    if (terms[i] && typeof terms[i] === 'object' && typeof terms[i].t === 'string' && terms[i].t) {
      list.push(terms[i]);
    }
  }
  list.sort(function(a, b) { return b.t.length - a.t.length; });
  var marks = [];
  var re = /\u0001\d+\u0001/g;
  for (var j = 0; j < list.length; j++) {
    var t = list[j].t;
    var d = list[j].d;
    var et = evEsc2(t);
    if (!et) {
      continue;
    }
    var pos = 0;
    for (;;) {
      var idx = s.indexOf(et, pos);
      if (idx < 0) {
        break;
      }
      var before = idx > 0 ? s.charAt(idx - 1) : '';
      var after = idx + et.length < s.length ? s.charAt(idx + et.length) : '';
      var okB = !before || !/[A-Za-z0-9_]/.test(before);
      var okA = !after || !/[A-Za-z0-9_]/.test(after);
      var overlaps = false;
      re.lastIndex = 0;
      var m;
      while ((m = re.exec(s)) !== null) {
        if (idx < m.index + m[0].length && m.index < idx + et.length) {
          overlaps = true;
          break;
        }
      }
      if (okB && okA && !overlaps) {
        s = s.slice(0, idx) + '\u0001' + marks.length + '\u0001' + s.slice(idx + et.length);
        marks.push('<span class="ev-tt" tabindex="0" data-tip="' + evEsc2(t + ': ' + d) + '">' + evEsc2(t) + '</span>');
        break;
      }
      pos = idx + 1;
    }
  }
  for (var k = 0; k < marks.length; k++) {
    s = s.split('\u0001' + k + '\u0001').join(marks[k]);
  }
  return s;
}

function evQueryId(id) {
  return '<span class="ev-qa"><code>' + evEsc2(id) + '</code><button type="button" class="ev-copy" onclick="evCopy(this)" data-copy="' + evEsc2(id) + '">Copy query ID</button><span>Open the plan with showplan in SSMS or Azure Data Studio using this query ID.</span></span>';
}
