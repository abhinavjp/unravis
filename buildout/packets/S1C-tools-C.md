# Packet S1C: apply the manual-tools audit to group C (`parts/43-tools-c.js`) and add `parts/10-data5-toolsC.js`

Do exactly this. Do not explore the repo. Create the one new file below and make the 8 edits to `parts/43-tools-c.js`. ES5 only (`var`, `function`, `for`; no arrows, template literals, `let`, `const`). Code runs inside one concatenated script; parts load in sorted filename order ('-' sorts before '.'), so `parts/10-data5-toolsC.js` loads after `parts/10-data3-near.js` and before `parts/11-charts-a.js`, hence before `parts/43-tools-c.js`. Every existing global below is USED inside functions only, never read at load time.

This packet applies the wireframe-only findings of the manual tools audit (2026-10-29) for the five group-C tools mongo-health, host-resources, sql-host, after-fix and suspect-summary. No tool moves between files. No new collectors are added. The helpers `swimlane`, `heatmap` and `nearView` exist, but none of these five rows needs them; do not add them.

Globals that exist elsewhere and you only USE (inside functions, never at load time): `W`, `render()`, `findingsList(items)`, `suspectsHere(ids)`, `dt(t, h)`, `originContent()`, `afterFixBody()`, `openTool(id)`, `evFind(id)` (the card in `SCEN` with that id, or null), `evidenceCard(c)` (a `<section class="ev ...">` with chart, table and a `Why this colour?` button wired to `evWhy(id)`), `evidenceLegend()`, `whyView(c)` (the drill-down page whose back button reads `&larr; Back to the evidence`), `evWhy(id)` (sets `W.why`), `evBack()` (clears it).

New globals (only these, all in the new file, all TC-prefixed): `TC_MONGO_IDS`, `TC_HOST_IDS`, `TC_SUSPECT_CARDS`, `TCcards`, `TCsuspectEvidence`.

## New file `parts/10-data5-toolsC.js`

Exactly the content in the block below; the block is the contract. In short: `TC_MONGO_IDS` is the 7 mongo evidence card ids (slow ops, tickets, replication lag, cache eviction, connections, then the two not-collected cards collection scans and oplog window); `TC_HOST_IDS` is the 6 host card ids (CPU, disk, memory, then the swap, TCP and run-queue detail cards); `TC_SUSPECT_CARDS` is the one pair `['D1', 'sql-blocking-chain']`. `TCcards(ids)` returns `''` when `evFind` or `evidenceCard` is not a function, renders the found cards in order under `evidenceLegend()` inside `<div style="display:grid;gap:12px">`, skips unknown ids, and returns `'<p class="note">No evidence card is available for this tool.</p>'` when no id resolves. `TCsuspectEvidence()` returns the section `<section><h4 style="font-size:14px">The evidence behind the colours</h4>` + the exact note `One card per suspect the sources could test, with its chart and a Why this colour? view. The other suspects (C1, S1, M1, H1, L1, Q1) have no per-suspect evidence chart in this case.` + `TCcards` of the mapped card ids + `</section>`.

## Edits to `parts/43-tools-c.js`

Each SEARCH below is copied byte-for-byte from the current file and occurs exactly once (verified with the greps under Check). Make all 8.

1. `TOOL_BODIES['mongo-health']`: keep the not-run line and the findings list; add the `W.why` drill-down guard (`if (W.why && typeof evFind === 'function' && typeof whyView === 'function') { var card = evFind(W.why); if (card) return whyView(card); W.why = null; }`); new lead copy names slow operations, queues and tickets, replication lag, cache eviction and connections; the dummy table becomes `(typeof TCcards === 'function' ? TCcards(TC_MONGO_IDS) : '')`; the new note names the sources (profiler and currentOp samples, mongostat and mongotop, rs.status) and the per-card chart and Why this colour? view. `suspectsHere(['M1'])` stays.
2. `TOOL_NOTES['mongo-health']`: Data shown now names the `TC_MONGO_IDS` coverage; Chart reads "One evidence card per measure, the same cards as Step 8..." and records that the dummy table is gone; Buttons adds the live Why this colour? button. The other dt lines are unchanged.
3. `TOOL_BODIES['host-resources']`: same `W.why` guard; lead adds swap; the dummy table becomes `TCcards(TC_HOST_IDS)`; the new note names the collectors iostat, vmstat, sar and netstat -s and says the swap, TCP and run-queue cards read Not collected where the collector cannot read them yet. `suspectsHere(['H1'])` stays.
4. `TOOL_NOTES['host-resources']`: Data shown names the `TC_HOST_IDS` coverage; Chart and Buttons match edit 2.
5. `TOOL_BODIES['sql-host']`: the audit marks this tool a duplicate of host-resources and merging is a move, so the wireframe-only fix is copy plus a link. New lead: `What the SQL Server host recorded. This is the same Blackbox source as Host CPU, memory and disk; Step 9 reads both hosts from it.` Findings stay. The dummy table becomes a plain-English note (no separate SQL-host evidence set; the same collectors feed Host CPU, memory and disk; the suspect this host evidence would clear or confirm is D1), then a live button `<button class="btn sm" onclick="openTool('host-resources')">Open Host CPU, memory and disk</button>` inside a `<div>`, then `suspectsHere(['D1'])`.
6. `TOOL_NOTES['sql-host']`: Data shown records the de-duplication and the D1 tie; Chart reads "None of its own. The charts live on the Host CPU, memory and disk evidence cards."; Buttons adds the live Open link and the suspect card button. The other dt lines are unchanged.
7. `TOOL_BODIES['suspect-summary']`: keep the not-run line; add the same `W.why` drill-down guard; return `originContent()` + `(typeof TCsuspectEvidence === 'function' ? TCsuspectEvidence() : '')`.
8. `TOOL_NOTES['suspect-summary']`: Data shown adds the `TC_SUSPECT_CARDS` cards; Chart reads "An evidence card per tested suspect under The evidence behind the colours: chart, colour rule and a Why this colour? drill-down per card (theme E)."; Buttons adds the live Why this colour? button. The other dt lines are unchanged.

`after-fix` is not edited: the audit records nothing wireframe-only for it (missing "none blocking", nothing useless to remove, nothing to add).

## Check (you run it; the owner applies the blocks and runs the full check)

```
node --check parts/10-data5-toolsC.js
grep -c -F "TOOL_BODIES['mongo-health'] = function(){" parts/43-tools-c.js
grep -c -F "TOOL_NOTES['mongo-health'] = function(){" parts/43-tools-c.js
grep -c -F "TOOL_BODIES['host-resources'] = function(){" parts/43-tools-c.js
grep -c -F "TOOL_NOTES['host-resources'] = function(){" parts/43-tools-c.js
grep -c -F "TOOL_BODIES['sql-host'] = function(){" parts/43-tools-c.js
grep -c -F "TOOL_NOTES['sql-host'] = function(){" parts/43-tools-c.js
grep -c -F "TOOL_BODIES['suspect-summary'] = function(){" parts/43-tools-c.js
grep -c -F "TOOL_NOTES['suspect-summary'] = function(){" parts/43-tools-c.js
```
Expected: no syntax error, then `1` eight times. The owner then runs `node ../packets/data/chk-s1C.js` from `buildout/wireframes`.

## Block format

New file: `### parts/<name>` then a fenced code block. Edit: `### parts/43-tools-c.js` then `<<<<<<< SEARCH`, old text, `=======`, new text, `>>>>>>> REPLACE` (no code fence, no blank line after the `###` line). New-file block first, then the 8 edit blocks in file order.

## Blocks

````
### parts/10-data5-toolsC.js
```
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
```

### parts/43-tools-c.js
<<<<<<< SEARCH
TOOL_BODIES['mongo-health'] = function(){
  if (!W.ranSteps) return '<p class="note">This step shows the last run. Run the steps first to fill it in.</p>';
  return '<p class="lead" style="color:var(--text)">Checks on MongoDB: slow operations, queues and the replica set.</p>'
    + findingsList(['No slow operation above 100 ms was recorded in the window.', 'No operation queued behind a lock.', 'The replica set held its primary through the window.'])
    + '<div class="tscroll"><table><caption>Replica and slow ops (dummy; the repo has no Mongo table component)</caption><thead><tr><th>Measure</th><th>Normal</th><th>Incident</th><th>Change</th></tr></thead><tbody>'
    + '<tr><td>Slow operations (&gt; 100 ms)</td><td>0</td><td>0</td><td>About the same</td></tr>'
    + '<tr><td>Queued behind a lock</td><td>0</td><td>0</td><td>About the same</td></tr>'
    + '<tr><td>Primary stepdowns</td><td>0</td><td>0</td><td>About the same</td></tr>'
    + '</tbody></table></div>'
    + '<p class="note">The product UI for Step 8 shows findings and suspects, not this table. The table is a labelled placeholder for the tool.</p>'
    + suspectsHere(['M1']);
};
=======
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
>>>>>>> REPLACE

### parts/43-tools-c.js
<<<<<<< SEARCH
TOOL_NOTES['mongo-health'] = function(){
  return dt('Data shown', 'Mirrors Step 8: engine findings for the Mongo group, suspects of group M. The default dummy case has no MongoDB source, so the live card is Cannot test yet until the MongoDB checkbox is on.')
    + dt('Chart', 'None in the repo UI. Placeholder table: Measure, Normal, Incident, Change.')
    + dt('Empty', 'No findings block when the window is quiet.')
    + dt('Cannot test yet', 'Default: missing box until Pretend this case has MongoDB.')
    + dt('Failed', '"The tool could not run."')
    + dt('Out of date', 'Sticker after a run when windows change.')
    + dt('Buttons', 'Add by location from the missing box (inert).');
};
=======
TOOL_NOTES['mongo-health'] = function(){
  return dt('Data shown', 'Mirrors Step 8: engine findings for the Mongo group, suspects of group M, and the evidence cards named in <code>TC_MONGO_IDS</code> (slow ops, tickets, replication lag, cache eviction, connections, collection scans, oplog window). The default dummy case has no MongoDB source, so the live card is Cannot test yet until the MongoDB checkbox is on.')
    + dt('Chart', 'One evidence card per measure, the same cards as Step 8: chart, small table, colour rule and a Why this colour? drill-down per card. The dummy Measure/Normal/Incident/Change table is gone.')
    + dt('Empty', 'No findings block when the window is quiet.')
    + dt('Cannot test yet', 'Default: missing box until Pretend this case has MongoDB.')
    + dt('Failed', '"The tool could not run."')
    + dt('Out of date', 'Sticker after a run when windows change.')
    + dt('Buttons', 'Add by location from the missing box (inert). Why this colour? on each evidence card (live).');
};
>>>>>>> REPLACE

### parts/43-tools-c.js
<<<<<<< SEARCH
TOOL_BODIES['host-resources'] = function(){
  if (!W.ranSteps) return '<p class="note">This step shows the last run. Run the steps first to fill it in.</p>';
  return '<p class="lead" style="color:var(--text)">Checks on the hosts: processor, memory, disk and network.</p>'
    + findingsList(['Processor stayed under 60 % on both hosts.', 'No disk queue over 20 ms was recorded.', 'Network errors: none recorded.'])
    + '<div class="tscroll"><table><caption>App host (dummy; the repo has no host-resource table component)</caption><thead><tr><th>Measure</th><th>Normal</th><th>Incident</th><th>Change</th></tr></thead><tbody>'
    + '<tr><td>CPU %</td><td>41</td><td>48</td><td>About the same</td></tr>'
    + '<tr><td>Memory used %</td><td>62</td><td>64</td><td>About the same</td></tr>'
    + '<tr><td>Disk queue (ms)</td><td>4</td><td>6</td><td>About the same</td></tr>'
    + '<tr><td>Network errors / min</td><td>0</td><td>0</td><td>About the same</td></tr>'
    + '</tbody></table></div>'
    + '<p class="note">The product UI for Step 9 shows findings and suspects, not this table. The table is a labelled placeholder for the app-host tool.</p>'
    + suspectsHere(['H1']);
};
=======
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
>>>>>>> REPLACE

### parts/43-tools-c.js
<<<<<<< SEARCH
TOOL_NOTES['host-resources'] = function(){
  return dt('Data shown', 'Mirrors Step 9 for the application host. Default dummy has no Blackbox, so the live card is Cannot test yet until Host is ticked.')
    + dt('Chart', 'None in the repo UI. Placeholder table: Measure, Normal, Incident, Change for CPU, memory, disk, network.')
    + dt('Empty', 'No findings when the Blackbox window is quiet.')
    + dt('Cannot test yet', 'Default: missing box (needs a Blackbox source).')
    + dt('Failed', '"The tool could not run."')
    + dt('Out of date', 'Sticker after a run when windows change.')
    + dt('Buttons', 'Add by location / Place a Blackbox (inert).');
};
=======
TOOL_NOTES['host-resources'] = function(){
  return dt('Data shown', 'Mirrors Step 9 for the application host: findings, suspects of group H, and the evidence cards named in <code>TC_HOST_IDS</code> (CPU, disk, memory, swap, TCP, run queue). Default dummy has no Blackbox, so the live card is Cannot test yet until Host is ticked.')
    + dt('Chart', 'One evidence card per measure, the same cards as Step 9: chart, small table, colour rule and a Why this colour? drill-down per card. The dummy Measure/Normal/Incident/Change table is gone.')
    + dt('Empty', 'No findings when the Blackbox window is quiet.')
    + dt('Cannot test yet', 'Default: missing box (needs a Blackbox source).')
    + dt('Failed', '"The tool could not run."')
    + dt('Out of date', 'Sticker after a run when windows change.')
    + dt('Buttons', 'Add by location / Place a Blackbox (inert). Why this colour? on each evidence card (live).');
};
>>>>>>> REPLACE

### parts/43-tools-c.js
<<<<<<< SEARCH
TOOL_BODIES['sql-host'] = function(){
  if (!W.ranSteps) return '<p class="note">This step shows the last run. Run the steps first to fill it in.</p>';
  return '<p class="lead" style="color:var(--text)">What the SQL Server host recorded. Same host checks as Step 9, on the SQL host.</p>'
    + findingsList(['Processor on the SQL host stayed under 55 %.', 'Disk queue on data and log volumes stayed under 12 ms.', 'Network errors: none recorded.'])
    + '<div class="tscroll"><table><caption>SQL host (dummy; the repo has no sql-host table component)</caption><thead><tr><th>Measure</th><th>Normal</th><th>Incident</th><th>Change</th></tr></thead><tbody>'
    + '<tr><td>CPU %</td><td>33</td><td>51</td><td>Higher, under 60 %</td></tr>'
    + '<tr><td>Memory used %</td><td>71</td><td>74</td><td>About the same</td></tr>'
    + '<tr><td>Data volume queue (ms)</td><td>5</td><td>9</td><td>About the same</td></tr>'
    + '<tr><td>Log volume queue (ms)</td><td>3</td><td>4</td><td>About the same</td></tr>'
    + '<tr><td>Network errors / min</td><td>0</td><td>0</td><td>About the same</td></tr>'
    + '</tbody></table></div>'
    + '<p class="note">The product UI has no separate sql-host page: Step 9 findings cover both hosts. This table is a labelled placeholder.</p>';
};
=======
TOOL_BODIES['sql-host'] = function(){
  if (!W.ranSteps) return '<p class="note">This step shows the last run. Run the steps first to fill it in.</p>';
  return '<p class="lead" style="color:var(--text)">What the SQL Server host recorded. This is the same Blackbox source as Host CPU, memory and disk; Step 9 reads both hosts from it.</p>'
    + findingsList(['Processor on the SQL host stayed under 55 %.', 'Disk queue on data and log volumes stayed under 12 ms.', 'Network errors: none recorded.'])
    + '<p class="note">There is no separate SQL-host evidence set: the same collectors (iostat, vmstat, sar and netstat -s) feed Host CPU, memory and disk, so this tool points there instead of repeating the same numbers. The suspect this host evidence would clear or confirm is D1, the SQL Server suspect.</p>'
    + '<div><button class="btn sm" onclick="openTool(\'host-resources\')">Open Host CPU, memory and disk</button></div>'
    + suspectsHere(['D1']);
};
>>>>>>> REPLACE

### parts/43-tools-c.js
<<<<<<< SEARCH
TOOL_NOTES['sql-host'] = function(){
  return dt('Data shown', 'Same source as host-resources (Blackbox) but for the SQL host. Needs <code>host</code>. No dedicated component in the repo.')
    + dt('Chart', 'None. Placeholder table: Measure, Normal, Incident, Change.')
    + dt('Empty', 'Quiet Blackbox: findings say nothing sat outside its range.')
    + dt('Cannot test yet', 'Default: missing box until Host is ticked.')
    + dt('Failed', '"The tool could not run."')
    + dt('Out of date', 'Sticker after a run.')
    + dt('Buttons', 'Add by location (inert).');
};
=======
TOOL_NOTES['sql-host'] = function(){
  return dt('Data shown', 'Same source as host-resources (Blackbox) but for the SQL host. Needs <code>host</code>. No evidence set of its own: the body links to Host CPU, memory and disk and ties the host evidence to the SQL suspect D1.')
    + dt('Chart', 'None of its own. The charts live on the Host CPU, memory and disk evidence cards.')
    + dt('Empty', 'Quiet Blackbox: findings say nothing sat outside its range.')
    + dt('Cannot test yet', 'Default: missing box until Host is ticked.')
    + dt('Failed', '"The tool could not run."')
    + dt('Out of date', 'Sticker after a run.')
    + dt('Buttons', 'Add by location (inert). Open Host CPU, memory and disk (live). Add its check on the suspect card (live).');
};
>>>>>>> REPLACE

### parts/43-tools-c.js
<<<<<<< SEARCH
TOOL_BODIES['suspect-summary'] = function(){
  if (!W.ranSteps) return '<p class="note">This step shows the last run. Run the steps first to fill it in.</p>';
  return originContent();
};
=======
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
>>>>>>> REPLACE

### parts/43-tools-c.js
<<<<<<< SEARCH
TOOL_NOTES['suspect-summary'] = function(){
  return dt('Data shown', 'Mirrors Step 10: <code>origin.roles[]</code>, <code>suspects[]</code> grouped ranked / not tested / set aside, <code>confidence</code>, <code>cheapestCheck</code>.')
    + dt('Chart', 'None. Origin table, colour legend, suspect cards, How sure are we table.')
    + dt('Empty', 'No red or orange: "No suspect is red or orange." Not enough data: "Not enough data to date any tier."')
    + dt('Cannot test yet', 'No audit: missing box.')
    + dt('Failed', '"The tool could not run."')
    + dt('Out of date', 'Sticker; cards from the earlier run.')
    + dt('Buttons', 'Show the suspects set aside (live). Add its check (live). Go to step 12 switches to Guided (live).');
};
=======
TOOL_NOTES['suspect-summary'] = function(){
  return dt('Data shown', 'Mirrors Step 10: <code>origin.roles[]</code>, <code>suspects[]</code> grouped ranked / not tested / set aside, <code>confidence</code>, <code>cheapestCheck</code>, plus the evidence cards named in <code>TC_SUSPECT_CARDS</code> for the suspects the sources could test.')
    + dt('Chart', 'An evidence card per tested suspect under The evidence behind the colours: chart, colour rule and a Why this colour? drill-down per card (theme E).')
    + dt('Empty', 'No red or orange: "No suspect is red or orange." Not enough data: "Not enough data to date any tier."')
    + dt('Cannot test yet', 'No audit: missing box.')
    + dt('Failed', '"The tool could not run."')
    + dt('Out of date', 'Sticker; cards from the earlier run.')
    + dt('Buttons', 'Show the suspects set aside (live). Add its check (live). Go to step 12 switches to Guided (live). Why this colour? on an evidence card (live).');
};
>>>>>>> REPLACE
````

## Output

The blocks in one plain reply, then exactly 3 lines: `Status: PASS|FAIL`, `Checks: <printed output>`, `Concerns: none` (or one line).
