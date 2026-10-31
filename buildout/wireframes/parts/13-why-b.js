function whyView(c) {
  function x(s) {
    return String(s == null ? '' : s)
      .replace(/&/g, '&amp;')
      .replace(/</g, '&lt;')
      .replace(/>/g, '&gt;')
      .replace(/"/g, '&quot;');
  }
  if (!c) { return ''; }
  var ev0 = (typeof evDepEffVerdict === 'function') ? evDepEffVerdict(c) : c.verdict;
  var v = (ev0 === 'ok' || ev0 === 'watch' || ev0 === 'bad' || ev0 === 'none') ? ev0 : 'none';
  var w = c.why || {};
  var out = '';
  out += '<div class="why-view">';
  out += '<button class="btn" type="button" onclick="evBack()">&larr; Back to the evidence</button>';
  out += '<h3>' + x(c.label) + ' <span class="ev-chip ' + v + '">' + evChip(v) + '</span></h3>';
  out += '<p class="ev-rule">' + x(c.rule) + '</p>';
  out += evHelpLook(c);
  if (typeof evDepBlocked === 'function' && evDepBlocked(c)) {
    out += '<p class="ev-blocked">No chart or table: the data this card needs was not collected.</p>';
  } else {
    out += evChart(c);
  }
  out += evTable(c);
  out += evHelpMore(c, true);
  out += '<p>' + x(w.summary) + '</p>';
  out += '<ol class="why-steps">';
  if (Object.prototype.toString.call(w.steps) === '[object Array]') {
    for (var i = 0; i < w.steps.length; i++) {
      out += '<li>' + x(w.steps[i]) + '</li>';
    }
  }
  out += '</ol>';
  if (w.dataNote) {
    out += '<p class="note">' + x(w.dataNote) + '</p>';
  }
  out += '</div>';
  return out;
}

function ruledOutDrawer(cards) {
  function x(s) {
    return String(s == null ? '' : s)
      .replace(/&/g, '&amp;')
      .replace(/</g, '&lt;')
      .replace(/>/g, '&gt;')
      .replace(/"/g, '&quot;');
  }
  if (Object.prototype.toString.call(cards) !== '[object Array]') { return ''; }
  var list = [];
  for (var i = 0; i < cards.length; i++) {
    var c = cards[i];
    if (c && c.verdict === 'ok') { list.push(c); }
  }
  if (list.length === 0) { return ''; }
  var out = '<details class="ruled-out"><summary>Ruled out (' + list.length + ')</summary><ul>';
  for (var j = 0; j < list.length; j++) {
    var card = list[j];
    var id = String(card.id == null ? '' : card.id).replace(/[^A-Za-z0-9_-]/g, '');
    out += '<li><span class="ev-chip ok">' + evChip('ok') + '</span><strong>' + x(card.label) + '</strong><span class="ev-rule">' + x(card.rule) + '</span>' + evHelpLook(card) + '<button class="ev-why" type="button" onclick="evWhy(\'' + id + '\')">Why this colour?</button></li>';
  }
  out += '</ul></details>';
  return out;
}
