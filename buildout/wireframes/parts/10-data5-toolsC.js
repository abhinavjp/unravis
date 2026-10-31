// ---------- manual tools group C: evidence-card data and helpers ----------
// Manual tools audit (2026-10-29), group C: the dummy placeholder tables of mongo-health and
// host-resources are replaced by the real evidence cards named here, and suspect-summary gains
// an evidence section. The cards themselves live in SCEN (parts/14-scenarios.js); this part only
// names which cards each tool shows and how to render them. ES5 only; every global is prefixed TC.

// mongo-health: slow ops, queues and tickets, replication lag, cache eviction, connections,
// then the two measures this collector cannot read (collection scans, oplog window).
var TC_MONGO_IDS = ['MG-OK-SLOWOPS', 'MG-OK-TICKETS', 'MG-OK-REPLAG', 'MG-WATCH-CACHE', 'MG-WATCH-CONNS', 'MG-NONE-COLLSCAN', 'MG-NONE-OPLOG'];

// host-resources: CPU, disk and memory, then the swap, TCP and run-queue detail cards, which
// read Not collected where the collector cannot read them yet.
var TC_HOST_IDS = ['GS-01-host-cpu-ok', 'GS-02-host-disk-ok', 'GS-03-host-mem-ok', 'GS-12-host-swap-gray', 'GS-11-host-tcp-gray', 'GS-10-host-runqueue-gray'];

// suspect-summary: suspect id -> the evidence card behind it. Only suspects the sources could
// test have a card; D1 (blocking on the Orders tables) is backed by the blocking-chain card.
var TC_SUSPECT_CARDS = [['D1', 'sql-blocking-chain']];

// Render the evidence cards for a list of card ids, in order, under the colour legend.
// Ids with no card in SCEN are skipped; with no cards at all a note is returned instead.
// Returns '' when the evidence helpers are not loaded.
function TCcards(ids){
  if (typeof evFind !== 'function' || typeof evidenceCard !== 'function') return '';
  var out = '';
  for (var i = 0; i < ids.length; i++) {
    var c = evFind(ids[i]);
    if (c) out += evidenceCard(c);
  }
  if (!out) return '<p class="note">No evidence card is available for this tool.</p>';
  return (typeof evidenceLegend === 'function' ? evidenceLegend() : '')
    + '<div style="display:grid;gap:12px">' + out + '</div>';
}

// The suspect-summary extra section: one evidence card per suspect the sources could test,
// each with its chart and a working Why this colour? drill-down (evWhy sets W.why; the tool
// body answers with whyView).
function TCsuspectEvidence(){
  var ids = [];
  for (var i = 0; i < TC_SUSPECT_CARDS.length; i++) ids.push(TC_SUSPECT_CARDS[i][1]);
  return '<section><h4 style="font-size:14px">The evidence behind the colours</h4>'
    + '<p class="note">One card per suspect the sources could test, with its chart and a Why this colour? view. The other suspects (C1, S1, M1, H1, L1, Q1) have no per-suspect evidence chart in this case.</p>'
    + TCcards(ids)
    + '</section>';
}
