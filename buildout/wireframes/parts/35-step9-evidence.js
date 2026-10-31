var EV_SETS9 = {
  incident: { label: 'Real incident', ids: ['GS-07-host-cpu-red', 'GS-08-host-mem-red', 'GS-09-host-disk-red', 'GS-05-host-disk-watch', 'GS-10-host-runqueue-gray'] },
  quiet: { label: 'Quiet window', ids: ['GS-01-host-cpu-ok', 'GS-02-host-disk-ok', 'GS-03-host-mem-ok', 'GS-11-host-tcp-gray', 'GS-12-host-swap-gray'] },
  watch: { label: 'Pressure building', ids: ['GS-04-host-cpu-watch', 'GS-05-host-disk-watch', 'GS-06-host-net-watch', 'GS-01-host-cpu-ok', 'GS-03-host-mem-ok', 'GS-10-host-runqueue-gray'] }
};

function step9Evidence() {
  if (W.why) {
    var whyCard = evFind(W.why);
    if (whyCard) {
      return whyView(whyCard);
    }
    W.why = null;
  }

  var key = EV_SETS9[W.evSet] ? W.evSet : 'incident';
  var set = EV_SETS9[key];

  var cards = [];
  for (var i = 0; i < set.ids.length; i++) {
    var c = evFind(set.ids[i]);
    if (c) {
      cards.push(c);
    }
  }

  var bad = [];
  var watch = [];
  var none = [];
  var okCards = [];
  for (var j = 0; j < cards.length; j++) {
    var v = evDepEffVerdict(cards[j]);
    if (v === 'ok') {
      okCards.push(cards[j]);
    } else if (v === 'bad') {
      bad.push(cards[j]);
    } else if (v === 'watch') {
      watch.push(cards[j]);
    } else {
      none.push(cards[j]);
    }
  }
  var shown = bad.concat(watch, none);

  var switcher = '<div class="seg" role="group" aria-label="Pretend case">';
  for (var k in EV_SETS9) {
    switcher += '<button type="button" class="btn" aria-pressed="' + (k === key ? 'true' : 'false') + '" onclick="evSetPick(\'' + k + '\')">' + EV_SETS9[k].label + '</button>';
  }
  switcher += '</div>';

  var out = '<p class="note">Wireframe only: pick a pretend case, or pretend a source is missing, to see how the cards change.</p>';
  out += switcher;
  out += evDepToggles('host');
  out += evidenceLegend();
  out += '<div style="display:grid;gap:12px">';
  for (var m = 0; m < shown.length; m++) {
    out += evidenceCard(shown[m]);
  }
  out += '</div>';
  out += ruledOutDrawer(okCards);
  return out;
}
