var EV_SETS = {
  incident: { label: 'Real incident', ids: ['sql-cpu-high', 'sql-waits-bottleneck', 'sql-blocking-chain', 'sql-tempdb-contention', 'sql-lock-waits-rising', 'sql-waits-benign', 'sql-log-growth', 'sql-plan-regression'] },
  quiet: { label: 'Quiet window', ids: ['sql-waits-benign', 'sql-topqueries-orders', 'sql-log-growth', 'sql-plan-regression', 'sql-deadlocks'] },
  watch: { label: 'Pressure building', ids: ['sql-lock-waits-rising', 'sql-memory-grants', 'sql-index-maintenance', 'sql-waits-benign', 'sql-topqueries-orders', 'sql-deadlocks'] }
};

function evSetPick(key) {
  W.evSet = key;
  W.why = null;
  render();
}

function step7Evidence() {
  if (W.why) {
    var card = evFind(W.why);
    if (card) {
      return whyView(card);
    }
    W.why = null;
  }

  var key = (EV_SETS[W.evSet]) ? W.evSet : 'incident';
  var set = EV_SETS[key];

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
    var card2 = cards[j];
    var ver2 = (typeof evDepEffVerdict === 'function') ? evDepEffVerdict(card2) : card2.verdict;
    if (ver2 === 'bad') {
      bad.push(card2);
    } else if (ver2 === 'watch') {
      watch.push(card2);
    } else if (ver2 === 'none') {
      none.push(card2);
    } else if (ver2 === 'ok') {
      okCards.push(card2);
    }
  }
  var shown = bad.concat(watch).concat(none);

  var switcher = '<div class="seg" role="group" aria-label="Pretend case">';
  for (var k in EV_SETS) {
    switcher += '<button type="button" class="btn" aria-pressed="' + (k === key ? 'true' : 'false') + '" onclick="evSetPick(\'' + k + '\')">' + EV_SETS[k].label + '</button>';
  }
  switcher += '</div>';

  var cardsHtml = '';
  for (var m = 0; m < shown.length; m++) {
    cardsHtml += evidenceCard(shown[m]);
  }

  return '<p class="note">Wireframe only: pick a pretend case to see how the colours change.</p>' +
    switcher +
    ((typeof evDepToggles === 'function') ? evDepToggles('sql') : '') +
    evidenceLegend() +
    '<div style="display:grid;gap:12px">' + cardsHtml + '</div>' +
    ruledOutDrawer(okCards);
}
