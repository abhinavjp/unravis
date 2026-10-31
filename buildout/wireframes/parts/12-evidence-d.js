function evidenceLegend() {
  return '<p class="ev-legend"><span><i class="ok"></i>Looked normal</span><span><i class="watch"></i>Worth a look</span><span><i class="bad"></i>Likely cause</span><span><i class="none"></i>Not collected</span></p>';
}

function evidenceCard(c) {
  function x(s) {
    return String(s == null ? '' : s)
      .split('&').join('&amp;')
      .split('<').join('&lt;')
      .split('>').join('&gt;')
      .split('"').join('&quot;');
  }
  if (!c) {
    return '';
  }
  var ev0 = (typeof evDepEffVerdict === 'function') ? evDepEffVerdict(c) : c.verdict;
  var v = (ev0 === 'ok' || ev0 === 'watch' || ev0 === 'bad' || ev0 === 'none') ? ev0 : 'none';
  var blocked = (typeof evDepBlocked === 'function') ? evDepBlocked(c) : false;
  var chip = evChip(v);
  var rawId = String(c.id == null ? '' : c.id);
  var id = '';
  for (var i = 0; i < rawId.length; i++) {
    var ch = rawId.charAt(i);
    if (/[A-Za-z0-9_-]/.test(ch)) {
      id += ch;
    }
  }
  var extra = ((typeof evDepBanner === 'function') ? evDepBanner(c) : '') +
    ((typeof evImpact === 'function' && (!blocked || c.verdict === 'none')) ? evImpact(c) : '');
  var body = '<div class="ev-body">' + evChart(c) + evTable(c) + '</div>';
  if (blocked) {
    body = '<p class="ev-blocked">No chart or table: the data this card needs was not collected.</p>';
  } else if (v === 'watch') {
    body = '<details class="ev-fold"><summary>Show the chart and table</summary>' + body + '</details>';
  }
  return '<section class="ev ' + v + '" aria-label="' + x(c.label) + ': ' + chip + '">' +
    '<div class="ev-head"><span class="ev-title">' + x(c.label) + '</span><span class="ev-chip ' + v + '">' + chip + '</span></div>' +
    '<p class="ev-rule">' + x(c.rule) + '</p>' +
    evHelpLook(c) +
    extra + body +
    evHelpMore(c, false) +
    '<div class="ev-foot"><button class="ev-why" type="button" onclick="evWhy(\'' + id + '\')">Why this colour?</button>' + (c.sampled === true ? '<span class="ev-sampled">Sampled data: short spikes can be missed.</span>' : '') + '</div>' +
    '</section>';
}
