function evHelpLook(c) {
  function x(s) {
    return String(s == null ? '' : s)
      .replace(/&/g, '&amp;')
      .replace(/</g, '&lt;')
      .replace(/>/g, '&gt;')
      .replace(/"/g, '&quot;');
  }
  function get(c) {
    var e = (typeof EVHELP === 'object' && EVHELP);
    if (!c || c.id == null || !e) return null;
    var h = e[c.id];
    if (h && typeof h === 'object') return h;
    return null;
  }
  var h = get(c);
  if (!h || typeof h.look !== 'string' || h.look === '') return '';
  return '<p class="ev-look"><b>What to look for:</b> ' + evTip(c, h.look) + '</p>';
}

function evHelpMore(c, open) {
  function x(s) {
    return String(s == null ? '' : s)
      .replace(/&/g, '&amp;')
      .replace(/</g, '&lt;')
      .replace(/>/g, '&gt;')
      .replace(/"/g, '&quot;');
  }
  function get(c) {
    var e = (typeof EVHELP === 'object' && EVHELP);
    if (!c || c.id == null || !e) return null;
    var h = e[c.id];
    if (h && typeof h === 'object') return h;
    return null;
  }
  var h = get(c);
  if (!h) return '';
  var out = '<details class="ev-help"' + (open === true ? ' open' : '') + '><summary>Explain it simply</summary><dl>';
  var hadDt = false;
  var pairs = [
    ['What is this?', 'what'],
    ['What normal looks like', 'normal'],
    ['How to tell it is a real problem', 'issue'],
    ['Check it yourself', 'check'],
    ['How to read the chart', 'chart']
  ];
  for (var i = 0; i < pairs.length; i++) {
    var title = pairs[i][0];
    var key = pairs[i][1];
    if (typeof h[key] === 'string' && h[key] !== '') {
      out += '<dt>' + title + '</dt><dd>' + evTip(c, h[key]) + '</dd>';
      hadDt = true;
    }
  }
  if (h.terms && Object.prototype.toString.call(h.terms) === '[object Array]') {
    var valid = [];
    for (var j = 0; j < h.terms.length; j++) {
      var term = h.terms[j];
      if (term && typeof term === 'object' && typeof term.t === 'string' && term.t !== '') {
        valid.push(term);
      }
    }
    if (valid.length > 0) {
      out += '<dt>Words used</dt><dd><ul class="ev-terms">';
      for (var k = 0; k < valid.length; k++) {
        out += '<li><b>' + x(valid[k].t) + '</b>: ' + x(valid[k].d) + '</li>';
      }
      out += '</ul></dd>';
      hadDt = true;
    }
  }
  out += '</dl></details>';
  if (!hadDt) return '';
  return out;
}
