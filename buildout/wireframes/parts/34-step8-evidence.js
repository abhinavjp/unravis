var EV_SETS8 = {
  incident: { label: 'Real incident', ids: ['MG-BAD-SLOWOPS', 'MG-BAD-TICKETS', 'MG-BAD-REPLAG', 'MG-WATCH-CACHE', 'MG-NONE-COLLSCAN'] },
  quiet: { label: 'Quiet window', ids: ['MG-OK-SLOWOPS', 'MG-OK-TICKETS', 'MG-OK-REPLAG', 'MG-NONE-COLLSCAN', 'MG-NONE-OPLOG'] },
  watch: { label: 'Pressure building', ids: ['MG-WATCH-CACHE', 'MG-WATCH-CONNS', 'MG-WATCH-GLOCK', 'MG-OK-SLOWOPS', 'MG-OK-REPLAG', 'MG-NONE-INDEX'] }
};

function step8Evidence() {
  if (W.why) {
    var card = evFind(W.why);
    if (card) {
      return whyView(card);
    }
    W.why = null;
  }

  var key = EV_SETS8[W.evSet] ? W.evSet : 'incident';
  var set = EV_SETS8[key];

  var cards = [];
  for (var i = 0; i < set.ids.length; i++) {
    var c = evFind(set.ids[i]);
    if (c) {
      cards.push(c);
    }
  }

  var badCards = [];
  var watchCards = [];
  var noneCards = [];
  var okCards = [];
  for (var j = 0; j < cards.length; j++) {
    var verdict = evDepEffVerdict(cards[j]);
    if (verdict === 'bad') {
      badCards.push(cards[j]);
    } else if (verdict === 'watch') {
      watchCards.push(cards[j]);
    } else if (verdict === 'none') {
      noneCards.push(cards[j]);
    } else {
      okCards.push(cards[j]);
    }
  }
  var shown = badCards.concat(watchCards).concat(noneCards);

  var switcher = '<div class="seg" role="group" aria-label="Pretend case">';
  for (var k in EV_SETS8) {
    switcher += '<button type="button" class="btn" aria-pressed="' + (k === key ? 'true' : 'false') + '" onclick="evSetPick(\'' + k + '\')">' + EV_SETS8[k].label + '</button>';
  }
  switcher += '</div>';

  var cardsHtml = '<div style="display:grid;gap:12px">';
  for (var m = 0; m < shown.length; m++) {
    cardsHtml += evidenceCard(shown[m]);
  }
  cardsHtml += '</div>';

  return '<p class="note">Wireframe only: pick a pretend case, or pretend a source is missing, to see how the cards change.</p>' +
    switcher +
    evDepToggles('mongo') +
    evidenceLegend() +
    cardsHtml +
    ruledOutDrawer(okCards);
}
