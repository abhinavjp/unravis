// Query table for evidence cards that carry a per-query list (packet CPU).
// Globals used from elsewhere: evEsc2 and evQueryId (parts/12-evidence-h.js),
// EVDEP_MAP and EVDEP_DEFS (parts/14-deps-data.js), evDepsOff (parts/12-evidence-g.js).

function evQueryWaitsOn(c) {
  if (typeof EVDEP_MAP !== 'object' || !EVDEP_MAP || typeof EVDEP_DEFS !== 'object' || !EVDEP_DEFS || !EVDEP_DEFS.qswaits) { return false; }
  var keys = EVDEP_MAP[c.id] || [];
  var has = false;
  var i;
  for (i = 0; i < keys.length; i++) {
    if (keys[i] === 'qswaits') { has = true; }
  }
  if (!has) { return false; }
  var off = (typeof evDepsOff === 'function') ? evDepsOff() : null;
  return !(off && off.qswaits === true);
}

function evQueryTable(c) {
  if (!c || Object.prototype.toString.call(c.queries) !== '[object Array]') { return ''; }
  var waitOn = evQueryWaitsOn(c);
  var h = '<table class="wf"><caption>Top queries in the incident window</caption><thead><tr><th>Query ID</th><th>Calls</th><th>Avg ms</th><th>p95 ms</th>';
  if (waitOn) { h += '<th>Wait type</th>'; }
  h += '</tr></thead><tbody>';
  var i;
  for (i = 0; i < c.queries.length; i++) {
    var q = c.queries[i];
    h += '<tr><td>' + evQueryId(q.id) + '</td><td>' + evEsc2(q.calls) + '</td><td>' + evEsc2(q.avg) + '</td><td>' + evEsc2(q.p95) + '</td>';
    if (waitOn) { h += '<td>' + evEsc2(q.wait) + '</td>'; }
    h += '</tr>';
  }
  h += '</tbody></table>';
  if (!waitOn) {
    h += '<p class="note">Per-query wait types require SQL Server 2017+ with Query Store wait stats enabled.</p>';
  }
  return h;
}
