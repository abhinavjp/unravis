# Packet W89: wire steps 8 and 9 to their evidence cards and Why view

Do exactly this. Do not explore the repo. Read ONLY `parts/31-steps-5-9.js`, `parts/34-step8-evidence.js` and `parts/35-step9-evidence.js` to confirm line numbers if you want, but every string below is already exact: copy it byte for byte. ES5 only (`var`, `function`, `for`; no arrows, template literals, `let`, `const`). No new globals: `step8`, `stepNotes8`, `step9`, `stepNotes9` already exist and are only being replaced in place.

This is 4 edits to the single existing file `parts/31-steps-5-9.js`. No new files. For each edit, the SEARCH text below is the exact, complete, current text of that function (copy it byte for byte, including indentation and the trailing `}` or `};`). Verify each SEARCH occurs EXACTLY ONCE with `grep -c -F` before you finish; all 4 must print `1`.

## Edit 1 of 4: replace `function step8(st){...}`

SEARCH:
```
function step8(st){
  if (st === 'Waiting' || st === 'Failed') return statePrefix(8, st);
  if (st === 'Cannot test yet') return cannotBody(8, ['This case has no MongoDB source.'], 'Add a source');
  // a pretend Ready state, for the day a MongoDB source exists
  return statePrefix(8, st)
  + nutshell('No slow operation above 100 ms was recorded. The replica set was stable.')
  + '<p class="lead" style="color:var(--text)">Checks on MongoDB: slow operations, queues and the replica set.</p>'
  + findingsList(['No slow operation above 100 ms was recorded in the window.', 'No operation queued behind a lock.', 'The replica set held its primary through the window.'])
  + suspectsHere(['M1'])
  + reasonsBox([]);
}
```

REPLACE:
```
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
```

## Edit 2 of 4: replace `window.stepNotes8 = function(){...};`

SEARCH:
```
window.stepNotes8 = function(){
  return dt('Data shown', 'Engine findings for the Mongo group (<code>steps[7].findings</code>), suspects of group M. The Ready content above is invented placeholder: the case story has no MongoDB source, so the real default is Cannot test yet.')
    + dt('Chart', 'None in the repo UI.')
    + dt('Empty', 'No findings block; suspect list reads "No suspect belongs to this step."')
    + dt('Cannot test yet', 'The default here: "This case has no MongoDB source." plus Add a source.')
    + dt('Failed', 'Alert "The steps could not run." with Retry reading.')
    + dt('Out of date', 'Same content plus the out-of-date notice.')
    + dt('Buttons', 'Add a source (inert).') + stateSeg(8);
};
```

REPLACE:
```
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
```

## Edit 3 of 4: replace `function step9(st){...}`

SEARCH:
```
function step9(st){
  if (st === 'Waiting' || st === 'Failed') return statePrefix(9, st);
  if (st === 'Cannot test yet') return cannotBody(9, ['This case has no Blackbox on a host.'], 'Place a Blackbox');
  return statePrefix(9, st)
  + nutshell('Processor, memory, disk and network of the hosts stayed inside their normal range.')
  + '<p class="lead" style="color:var(--text)">Checks on the hosts: processor, memory, disk and network.</p>'
  + findingsList(['Processor stayed under 60 % on both hosts.', 'No disk queue over 20 ms was recorded.', 'Network errors: none recorded.'])
  + suspectsHere(['H1'])
  + reasonsBox([]);
}
```

REPLACE:
```
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
```

## Edit 4 of 4: replace `window.stepNotes9 = function(){...};`

SEARCH:
```
window.stepNotes9 = function(){
  return dt('Data shown', 'Engine findings for the host group (<code>steps[8].findings</code>), suspects of group H. Ready content is invented placeholder: the case story has no Blackbox, so the real default is Cannot test yet.')
    + dt('Chart', 'None in the repo UI.')
    + dt('Empty', 'No findings block; suspect list reads "No suspect belongs to this step."')
    + dt('Cannot test yet', 'The default here: "This case has no Blackbox on a host." plus Place a Blackbox.')
    + dt('Failed', 'Alert "The steps could not run." with Retry reading.')
    + dt('Out of date', 'Same content plus the out-of-date notice.')
    + dt('Buttons', 'Place a Blackbox (inert).') + stateSeg(9);
};
```

REPLACE:
```
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
```

## Check (you run it; the owner applies the blocks and runs `data/chk-w89.js`)
```
node --check parts/31-steps-5-9.js
grep -c -F "function step8(st){" parts/31-steps-5-9.js
grep -c -F "window.stepNotes8 = function(){" parts/31-steps-5-9.js
grep -c -F "function step9(st){" parts/31-steps-5-9.js
grep -c -F "window.stepNotes9 = function(){" parts/31-steps-5-9.js
```
Expected: no syntax error, then `1` four times.

## Block format
Edit: `### parts/31-steps-5-9.js` then `<<<<<<< SEARCH`, old text, `=======`, new text, `>>>>>>> REPLACE` (no code fence, no blank line after the `###` line). Output the 4 edits in file order (Edit 1, Edit 2, Edit 3, Edit 4 above).

## Output
The blocks in one plain reply, then exactly 3 lines: `Status: PASS|FAIL`, `Checks: <printed output>`, `Concerns: none` (or one line).
