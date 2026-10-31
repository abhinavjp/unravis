// ---------- steps 5 to 9 ----------
function suspectCard(s, compact){
  var onList = W.checklist.some(function(i){ return i.text.trim().toLowerCase() === (s.id + ': ' + s.nextCheck).toLowerCase(); });
  return '<article class="suspect c-' + s.colour + '"><header><span class="cword"><span class="dot" aria-hidden="true"></span>' + COLOUR_WORD[s.colour] + '</span>'
    + '<h4 style="font-size:14px">' + s.id + '. ' + s.title + '</h4><span class="result">' + s.result + ' &middot; ' + s.obs + '</span></header>'
    + '<p><strong>Why:</strong> ' + s.why + '</p>'
    + (s.colour === 'grey' && s.needs ? '<p><strong>Needs:</strong> ' + s.needs + '</p>' : '')
    + (!compact && s.evidence.length ? '<div><strong>Evidence</strong><ul><li>' + s.evidence.join('</li><li>') + '</li></ul></div>' : '')
    + (s.nextCheck ? '<p><strong>Cheapest check:</strong> ' + s.nextCheck + '</p>' : '')
    + (!compact && s.overturn ? '<p><strong>What would overturn it:</strong> ' + s.overturn + '</p>' : '')
    + (s.nextCheck ? '<div>' + (onList ? '<span class="note">On the checklist</span>' : '<button class="quiet" onclick="addCheck(\'' + s.id + '\')">+ Add its check to the checklist</button>') + '</div>' : '')
    + '</article>';
}
function addCheck(id){
  if (W.closed) return;
  var s = SUSPECTS.filter(function(x){ return x.id === id; })[0];
  var text = (s.id + ': ' + s.nextCheck).slice(0, 300);
  if (!W.checklist.some(function(i){ return i.text.trim().toLowerCase() === text.toLowerCase(); })) W.checklist.push({ id:'c' + W.checklist.length + Math.floor(W.views.length), list:'todo', text:text, suspect:s.id });
  W.flash = 'Added to the checklist (Need to do).'; render();
}
function findingsList(items){
  return '<div><h4 style="font-size:14px">What the data shows</h4><ul style="margin:0;padding-left:18px"><li>' + items.join('</li><li>') + '</li></ul></div>';
}
function suspectsHere(ids){
  var list = SUSPECTS.filter(function(s){ return ids.indexOf(s.id) >= 0; });
  return '<div><h4 style="font-size:14px">Suspects checked in this step</h4>'
    + (list.length ? list.map(function(s){ return suspectCard(s, true); }).join('') : '<p class="note">No suspect belongs to this step.</p>') + '</div>';
}

// ----- Step 5: Changes -----
function step5(st){
  if (st === 'Waiting' || st === 'Failed') return statePrefix(5, st);
  if (st === 'Cannot test yet') return cannotBody(5, ['The onset could not be dated, so no change could be set beside it.']);
  var entered = W.frame.releasesEntered && W.frame.releases.length > 0;
  var body;
  if (!entered) {
    body = '<p class="note">' + (W.frame.releasesEntered ? 'You said there was no change to list.' : 'No change listed. Add the releases or changes you know of in Step 0.') + '</p>'
      + '<div><button class="btn sm" onclick="go(0)">Change the list</button></div>';
  } else {
    body = '<div class="tscroll"><table><thead><tr><th>When</th><th>What</th><th>Against the onset</th></tr></thead><tbody>'
      + CHANGES0.map(function(c){ return '<tr><td>' + c.at.replace('T', ' ') + ' UTC</td><td>' + c.label + '</td><td>' + c.rel + '</td></tr>'; }).join('') + '</tbody></table></div>'
      + '<p class="note">Release 2026.10.26.2 of Orders.Api is the closest change before the onset (09:14).</p>'
      + '<p class="note">Only changes you listed are compared. Unlisted changes are invisible to this step.</p>';
  }
  return statePrefix(5, st)
  + nutshell('A release of Orders.Api went out 9 minutes before the slow end moved. Timing fits; the release contents are not in the data.')
  + '<p class="lead" style="color:var(--text)">Did something change just before the onset? Only changes you list are compared.</p>'
  + body + suspectsHere(['C1'])
  + (typeof nearView === 'function' ? nearView() : '')
  + suspectCards() + ruledOutDrawer5();
}
window.stepNotes5 = function(){
  return dt('Data shown', '<code>signals.changes.items[]</code> (at, label, relation, minutesBefore), <code>signals.changes.why/caveat/verdict</code>; the list itself is <code>frame.releases[]</code> from Step 0.')
    + dt('Chart', 'None. Table: When, What, Against the onset.')
    + dt('Empty', 'No list: "No change listed. Add the releases or changes you know of in Step 0." Tick "I have listed every change I know of" with an empty list and it reads "You said there was no change to list." (Untick the box in Step 0 to see the first.)')
    + dt('Cannot test yet', 'No onset: the reason list replaces the table.')
    + dt('Failed', 'Alert "The steps could not run." with Retry reading.')
    + dt('Out of date', 'Same content plus the out-of-date notice; editing the list in Step 0 is what marks this step out of date.')
    + dt('Buttons', 'Change the list goes to Step 0 (live). Add its check on the suspect adds to the checklist (live).') + stateSeg(5);
};

// ----- Step 6: App tier -----
function step6(st){
  if (st === 'Waiting' || st === 'Failed') return statePrefix(6, st);
  if (st === 'Cannot test yet') return cannotBody(6, ['The audit held no per-call timing, so queues and pools could not be read.']);
  return statePrefix(6, st)
  + nutshell('The pool was never full. The wait is outside the app code: in the queue in front of it.')
  + '<p class="lead" style="color:var(--text)">Checks on the application tier: queues, threads and connection pools.</p>'
  + findingsList([(W.frame.poolLimit && W.frame.poolLimit.trim() !== '' ? 'Calls in flight peaked at 34 of the pool limit ' + W.frame.poolLimit.trim() + ': the pool was not filled.' : 'Calls in flight peaked at 34; no pool limit was entered.'), 'Outside time (O) rose from 150 ms to 1,584 ms per call.', 'Threads were not the limit: the work waited before it started.'])
  + suspectsHere(['S1'])
  + reasonsBox(['Thread counts are not in the audit: they come from a Blackbox, and this case has none.']);
}
window.stepNotes6 = function(){
  return dt('Data shown', '<code>facts.load</code> + <code>signals.pool</code> rewritten as findings by the engine (<code>steps[5].findings</code>), suspects of group S (<code>suspects[]</code>).')
    + dt('Chart', 'None. Findings list plus suspect cards.')
    + dt('Empty', 'No findings: the findings block is left out and the suspect list reads "No suspect belongs to this step."')
    + dt('Cannot test yet', 'The reason list replaces findings.')
    + dt('Failed', 'Alert "The steps could not run." with Retry reading.')
    + dt('Out of date', 'Same content plus the out-of-date notice.')
    + dt('Buttons', 'Add its check on a suspect card (live).') + stateSeg(6);
};

// ----- Step 7: SQL Server -----
function step7(st){
  if (st === 'Waiting' || st === 'Failed') return statePrefix(7, st);
  if (st === 'Cannot test yet') return cannotBody(7, ['This case has no SQL Server source.'], 'Add a source');
  if (W.why) return statePrefix(7, st) + step7Evidence();
  var inc = (W.evSet || 'incident') === 'incident';
  return statePrefix(7, st)
  + (inc ? nutshell('Lock waits on the Orders tables rose at 09:14, the same minute the slow end moved. Blocking chains lasted up to 9 s.') : '')
  + '<p class="lead" style="color:var(--text)">Checks on SQL Server: blocking, waits and capacity.</p>'
  + (inc ? findingsList(['Lock waits on Orders and OrderLines rose from 0.4 s to 31 s per minute at 09:14.', 'Blocking chains of up to 9 s were recorded between 09:14 and 09:45.', 'No deadlocks were recorded in the window.', 'CPU on the SQL host is not in this data: it needs a Blackbox.']) : '')
  + step7Evidence()
  + suspectsHere(['D1'])
  + reasonsBox(['Processor, memory and disk of the SQL host: no Blackbox on that host.']);
}
window.stepNotes7 = function(){
  return dt('Data shown', 'Engine findings for the SQL group (<code>steps[6].findings</code>), suspects of group D. The repo has no per-wait table component: the detail lives in the engine answer, the step shows findings and suspects.')
    + dt('Chart', 'Evidence cards (new in this wireframe): each card has a chart, a small table, a colour and a Why this colour? view. Green means looked normal, amber worth a look, red likely cause, gray not collected. Green cards are kept in the Ruled out drawer. The pretend-case buttons are a wireframe aid, not a product control.')
    + dt('Empty', 'A SQL source with a quiet window: findings say no blocking was recorded.')
    + dt('Cannot test yet', 'No SQL source: "This case has no SQL Server source." plus Add a source (try the switch).')
    + dt('Failed', 'Alert "The steps could not run." with Retry reading.')
    + dt('Out of date', 'Same content plus the out-of-date notice.')
    + dt('Buttons', 'Add a source (inert). Add its check (live).') + stateSeg(7);
};

// ----- Step 8: MongoDB -----
function step8(st){
  if (st === 'Waiting' || st === 'Failed') return statePrefix(8, st);
  if (st === 'Cannot test yet') return cannotBody(8, ['This case has no MongoDB source.'], 'Add a source');
  if (W.why) return statePrefix(8, st) + (typeof step8Evidence === 'function' ? step8Evidence() : '');
  // a pretend Ready state, for the day a MongoDB source exists
  var set8 = (W.evSet === 'quiet' || W.evSet === 'watch') ? W.evSet : 'incident';
  var nut8 = { incident:'Orders.Create operations ran slow and WiredTiger tickets ran out while the secondary fell over 20 seconds behind.', quiet:'No slow operation above 100 ms was recorded. The replica set was stable.', watch:'Cache pressure and the write lock queue crept up, and connections rose, but nothing reached the danger line yet.' };
  var find8 = {
    incident:['Orders.Create p95 latency rose from 41 ms to 191 ms and slow ops rose from 3 to 48 a minute.', 'WiredTiger tickets out rose from 25 to 126 of 128, leaving only 2 free.', 'Replica lag rose from 1.3 s to 22 s and the oplog window shrank from 48 h to 6 h.'],
    quiet:['No slow operation above 100 ms was recorded in the window.', 'No operation queued behind a lock.', 'The replica set held its primary through the window.'],
    watch:['WiredTiger cache use rose from about 61% to 74% of max and eviction more than doubled.', 'The global lock write queue grew from near zero to about 4 writers.', 'Connections in use rose from 120 to 149, with plenty of the pool still free.']
  };
  return statePrefix(8, st)
  + nutshell(nut8[set8])
  + '<p class="lead" style="color:var(--text)">Checks on MongoDB: slow operations, queues and the replica set.</p>'
  + findingsList(find8[set8])
  + (typeof step8Evidence === 'function' ? step8Evidence() : '')
  + suspectsHere(['M1'])
  + reasonsBox([]);
}
window.stepNotes8 = function(){
  return dt('Data shown', 'Engine findings for the Mongo group (<code>steps[7].findings</code>), suspects of group M. The Ready content above is invented placeholder: the case story has no MongoDB source, so the real default is Cannot test yet.')
    + dt('Evidence cards', 'Below the findings sit evidence cards, one per MongoDB signal (slow operations, tickets, replication lag and more). Each card is colour-coded: green means it looked normal, amber means worth a look, red means it is a likely cause, and gray means it was not collected for this case. The "Pretend case" buttons swap between three made-up scenarios (Real incident, Quiet window, Pressure building); the nutshell, the findings above and the cards all change together when you pick one.')
    + dt('Pretend off toggles', 'The "Pretend off" buttons are a wireframe-only aid: click one to pretend a MongoDB source is missing, and its card turns gray with a "Could not check" note, as if nothing had been collected. Click the same button again to turn that source back on.')
    + dt('Why view', 'Click "Why this colour?" on any card to open the Why view: it replaces the whole card list with one page that explains, step by step, how that single card reached its colour, plus its chart and table. Click "Back to the evidence" to return to the card list.')
    + dt('Chart', 'None in the repo UI outside the evidence cards.')
    + dt('Empty', 'No findings block; suspect list reads "No suspect belongs to this step."')
    + dt('Cannot test yet', 'The default here: "This case has no MongoDB source." plus Add a source.')
    + dt('Failed', 'Alert "The steps could not run." with Retry reading.')
    + dt('Out of date', 'Same content plus the out-of-date notice.')
    + dt('Buttons', 'Add a source (inert). Pretend case, Pretend off and Why this colour? (all live, wireframe only).') + stateSeg(8);
};

// ----- Step 9: Host -----
function step9(st){
  if (st === 'Waiting' || st === 'Failed') return statePrefix(9, st);
  if (st === 'Cannot test yet') return cannotBody(9, ['This case has no Blackbox on a host.'], 'Place a Blackbox');
  if (W.why) return statePrefix(9, st) + (typeof step9Evidence === 'function' ? step9Evidence() : '');
  var set9 = (W.evSet === 'quiet' || W.evSet === 'watch') ? W.evSet : 'incident';
  var nut9 = { incident:'CPU hit 92% for six minutes, memory ran out with an OOM kill, and the disk queue backed up to 13 requests: the hosts were saturated.', quiet:'Processor, memory, disk and network of the hosts stayed inside their normal range.', watch:'CPU and disk latency climbed into the watch band and network traffic more than doubled, but nothing reached saturation.' };
  var find9 = {
    incident:['CPU stayed above 90% for six minutes, up from 45%.', 'Available memory fell from 11.8 GB to under 300 MB with one OOM kill.', 'The disk queue held at about 13 requests with latency up to 53 ms.'],
    quiet:['Processor stayed under 60 % on both hosts.', 'No disk queue over 20 ms was recorded.', 'Network errors: none recorded.'],
    watch:['CPU rose from 62% to 76%, past the watch line but short of saturation.', 'Disk latency rose from 8 ms to 20 ms with a queue of about 1.', 'Network traffic more than doubled from 4.2 MB/s to 9.2 MB/s, but error counters are not collected.']
  };
  return statePrefix(9, st)
  + nutshell(nut9[set9])
  + '<p class="lead" style="color:var(--text)">Checks on the hosts: processor, memory, disk and network.</p>'
  + findingsList(find9[set9])
  + (typeof step9Evidence === 'function' ? step9Evidence() : '')
  + suspectsHere(['H1'])
  + reasonsBox([]);
}
window.stepNotes9 = function(){
  return dt('Data shown', 'Engine findings for the host group (<code>steps[8].findings</code>), suspects of group H. Ready content is invented placeholder: the case story has no Blackbox, so the real default is Cannot test yet.')
    + dt('Evidence cards', 'Below the findings sit evidence cards, one per host signal (CPU, memory, disk and more). Each card is colour-coded: green means it looked normal, amber means worth a look, red means it is a likely cause, and gray means it was not collected for this host. The "Pretend case" buttons swap between three made-up scenarios (Real incident, Quiet window, Pressure building); the nutshell, the findings above and the cards all change together when you pick one.')
    + dt('Pretend off toggles', 'The "Pretend off" buttons are a wireframe-only aid: click one to pretend a host signal was never collected, and its card turns gray with a "Could not check" note, as if nothing had been read. Click the same button again to turn that source back on.')
    + dt('Why view', 'Click "Why this colour?" on any card to open the Why view: it replaces the whole card list with one page that explains, step by step, how that single card reached its colour, plus its chart and table. Click "Back to the evidence" to return to the card list.')
    + dt('Chart', 'None in the repo UI outside the evidence cards.')
    + dt('Empty', 'No findings block; suspect list reads "No suspect belongs to this step."')
    + dt('Cannot test yet', 'The default here: "This case has no Blackbox on a host." plus Place a Blackbox.')
    + dt('Failed', 'Alert "The steps could not run." with Retry reading.')
    + dt('Out of date', 'Same content plus the out-of-date notice.')
    + dt('Buttons', 'Place a Blackbox (inert). Pretend case, Pretend off and Why this colour? (all live, wireframe only).') + stateSeg(9);
};
