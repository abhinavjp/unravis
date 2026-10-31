// ---------- tool bodies: mongo-health, host-resources, sql-host, after-fix, suspect-summary ----------
var TOOL_BODIES = window.TOOL_BODIES = window.TOOL_BODIES || {};
var TOOL_NOTES = window.TOOL_NOTES = window.TOOL_NOTES || {};

TOOL_BODIES['mongo-health'] = function(){
  if (!W.ranSteps) return '<p class="note">This step shows the last run. Run the steps first to fill it in.</p>';
  if (W.why && typeof evFind === 'function' && typeof whyView === 'function') {
    var card = evFind(W.why);
    if (card) return whyView(card);
    W.why = null;
  }
  return '<p class="lead" style="color:var(--text)">Checks on MongoDB: slow operations, queues and tickets, replication lag, cache eviction and connections.</p>'
    + findingsList(['No slow operation above 100 ms was recorded in the window.', 'No operation queued behind a lock.', 'The replica set held its primary through the window.'])
    + (typeof TCcards === 'function' ? TCcards(TC_MONGO_IDS) : '')
    + '<p class="note">The same evidence cards as Step 8, one per measure. Slow operations come from the profiler and currentOp samples, queues and tickets from mongostat and mongotop, and the replica set state from rs.status. Each card has a chart and a Why this colour? view; a measure the collector cannot read shows a Not collected card instead of a made-up number.</p>'
    + suspectsHere(['M1']);
};
TOOL_NOTES['mongo-health'] = function(){
  return dt('Data shown', 'Mirrors Step 8: engine findings for the Mongo group, suspects of group M, and the evidence cards named in <code>TC_MONGO_IDS</code> (slow ops, tickets, replication lag, cache eviction, connections, collection scans, oplog window). The default dummy case has no MongoDB source, so the live card is Cannot test yet until the MongoDB checkbox is on.')
    + dt('Chart', 'One evidence card per measure, the same cards as Step 8: chart, small table, colour rule and a Why this colour? drill-down per card. The dummy Measure/Normal/Incident/Change table is gone.')
    + dt('Empty', 'No findings block when the window is quiet.')
    + dt('Cannot test yet', 'Default: missing box until Pretend this case has MongoDB.')
    + dt('Failed', '"The tool could not run."')
    + dt('Out of date', 'Sticker after a run when windows change.')
    + dt('Buttons', 'Add by location from the missing box (inert). Why this colour? on each evidence card (live).');
};

TOOL_BODIES['host-resources'] = function(){
  if (!W.ranSteps) return '<p class="note">This step shows the last run. Run the steps first to fill it in.</p>';
  if (W.why && typeof evFind === 'function' && typeof whyView === 'function') {
    var card = evFind(W.why);
    if (card) return whyView(card);
    W.why = null;
  }
  return '<p class="lead" style="color:var(--text)">Checks on the hosts: processor, memory and swap, disk and network.</p>'
    + findingsList(['Processor stayed under 60 % on both hosts.', 'No disk queue over 20 ms was recorded.', 'Network errors: none recorded.'])
    + (typeof TCcards === 'function' ? TCcards(TC_HOST_IDS) : '')
    + '<p class="note">The same evidence cards as Step 9, read by the Blackbox collectors (iostat, vmstat, sar and netstat -s). The swap, TCP and run-queue cards read Not collected where the collector cannot read them yet. Each card has a chart and a Why this colour? view.</p>'
    + suspectsHere(['H1']);
};
TOOL_NOTES['host-resources'] = function(){
  return dt('Data shown', 'Mirrors Step 9 for the application host: findings, suspects of group H, and the evidence cards named in <code>TC_HOST_IDS</code> (CPU, disk, memory, swap, TCP, run queue). Default dummy has no Blackbox, so the live card is Cannot test yet until Host is ticked.')
    + dt('Chart', 'One evidence card per measure, the same cards as Step 9: chart, small table, colour rule and a Why this colour? drill-down per card. The dummy Measure/Normal/Incident/Change table is gone.')
    + dt('Empty', 'No findings when the Blackbox window is quiet.')
    + dt('Cannot test yet', 'Default: missing box (needs a Blackbox source).')
    + dt('Failed', '"The tool could not run."')
    + dt('Out of date', 'Sticker after a run when windows change.')
    + dt('Buttons', 'Add by location / Place a Blackbox (inert). Why this colour? on each evidence card (live).');
};

TOOL_BODIES['sql-host'] = function(){
  if (!W.ranSteps) return '<p class="note">This step shows the last run. Run the steps first to fill it in.</p>';
  return '<p class="lead" style="color:var(--text)">What the SQL Server host recorded. This is the same Blackbox source as Host CPU, memory and disk; Step 9 reads both hosts from it.</p>'
    + findingsList(['Processor on the SQL host stayed under 55 %.', 'Disk queue on data and log volumes stayed under 12 ms.', 'Network errors: none recorded.'])
    + '<p class="note">There is no separate SQL-host evidence set: the same collectors (iostat, vmstat, sar and netstat -s) feed Host CPU, memory and disk, so this tool points there instead of repeating the same numbers. The suspect this host evidence would clear or confirm is D1, the SQL Server suspect.</p>'
    + '<div><button class="btn sm" onclick="openTool(\'host-resources\')">Open Host CPU, memory and disk</button></div>'
    + suspectsHere(['D1']);
};
TOOL_NOTES['sql-host'] = function(){
  return dt('Data shown', 'Same source as host-resources (Blackbox) but for the SQL host. Needs <code>host</code>. No evidence set of its own: the body links to Host CPU, memory and disk and ties the host evidence to the SQL suspect D1.')
    + dt('Chart', 'None of its own. The charts live on the Host CPU, memory and disk evidence cards.')
    + dt('Empty', 'Quiet Blackbox: findings say nothing sat outside its range.')
    + dt('Cannot test yet', 'Default: missing box until Host is ticked.')
    + dt('Failed', '"The tool could not run."')
    + dt('Out of date', 'Sticker after a run.')
    + dt('Buttons', 'Add by location (inert). Open Host CPU, memory and disk (live). Add its check on the suspect card (live).');
};

TOOL_BODIES['after-fix'] = function(){
  if (!W.ranSteps) return '<p class="notice">Run the steps first. The comparison needs the incident day.</p><p class="note">The later day, the route picker and Compare stay disabled until a run exists.</p>';
  return afterFixBody();
};
TOOL_NOTES['after-fix'] = function(){
  return dt('Data shown', 'Mirrors Step 11: later-day run classified by <code>classifyDay</code> into a verdict word, comparison rows and warnings.')
    + dt('Chart', 'None. Comparison table: Measure, Earlier day, Later day, Change vs the earlier day.')
    + dt('Empty', 'Not run: "Run the steps first. The comparison needs the incident day." A day with no calls: "No data for that day".')
    + dt('Cannot test yet', 'No audit: missing box. No main run: same as empty.')
    + dt('Failed', '"The later day could not be compared."')
    + dt('Out of date', 'A new main run leaves an old verdict; this page is not shown as stale.')
    + dt('Buttons', 'Compare validates the later day (live). Record with this warning writes under What we learned (live). Not now dismisses.');
};

TOOL_BODIES['suspect-summary'] = function(){
  if (!W.ranSteps) return '<p class="note">This step shows the last run. Run the steps first to fill it in.</p>';
  if (W.why && typeof evFind === 'function' && typeof whyView === 'function') {
    var card = evFind(W.why);
    if (card) return whyView(card);
    W.why = null;
  }
  return originContent()
    + (typeof TCsuspectEvidence === 'function' ? TCsuspectEvidence() : '');
};
TOOL_NOTES['suspect-summary'] = function(){
  return dt('Data shown', 'Mirrors Step 10: <code>origin.roles[]</code>, <code>suspects[]</code> grouped ranked / not tested / set aside, <code>confidence</code>, <code>cheapestCheck</code>, plus the evidence cards named in <code>TC_SUSPECT_CARDS</code> for the suspects the sources could test.')
    + dt('Chart', 'An evidence card per tested suspect under The evidence behind the colours: chart, colour rule and a Why this colour? drill-down per card (theme E).')
    + dt('Empty', 'No red or orange: "No suspect is red or orange." Not enough data: "Not enough data to date any tier."')
    + dt('Cannot test yet', 'No audit: missing box.')
    + dt('Failed', '"The tool could not run."')
    + dt('Out of date', 'Sticker; cards from the earlier run.')
    + dt('Buttons', 'Show the suspects set aside (live). Add its check (live). Go to step 12 switches to Guided (live). Why this colour? on an evidence card (live).');
};
