# Packet FIX10: a Nearby activity button wherever an API list is, and every log kind in the Nearby view (4 edited files, no new files)

Do exactly this. Do not explore the repo. Make NO new files: apply the 13 SEARCH/REPLACE blocks below to the 4 named files, in the order given. Each SEARCH was copied byte-for-byte from the current file and occurs exactly once; replace that one match. ES5 only (`var`, `function`, `for`; no arrows, template literals, `let`, `const`). Plain apostrophes; no HTML entities inside `inert(` literals (no edit here touches one).

One new global, and only this one: `nearbyForApi` in `parts/30-steps-0-4.js`. Globals you only USE (inside functions, never at load time; all already exist): `W`, `CALLS`, `ROUTES`, `render()`, `go(n)`, `openTool(id)`, `esc(s)`, `fmtCount(n)`, `fmtMs(n)`, `fmtPct(n)`, `signed(n)`, `dt(t, h)`.

## The contract

1. `nearbyForApi(api)` (new, in `parts/30-steps-0-4.js`, placed directly above `byApiTable`, after the `// ----- Step 2: Shape -----` comment). It finds the first index `i` in `CALLS` whose `CALLS[i][0] === api` (`CALLS` is sorted slowest first, so that is the slowest call of that API; `i = 0` when no call matches), then sets `W.nearbyRow = i; W.nearbyPicked = true; W.nearbyOpen = true; W.nbAround = '2026-10-26T' + CALLS[i][1].slice(0, 5);`. Then: when `W.mode === 'manual'` it calls `openTool('nearby-activity')`, otherwise it goes to Step 3 through `go(3)`, the same helper every other step button uses. Exact code is in block 1 below.

2. `byApiTable(limit)` in `parts/30-steps-0-4.js` (used by Step 2 and the Response time tool, so both change with this one edit): the header row gains a final empty `<th></th>` after `<th>Change</th>`, and every row gains one final cell `<td><button class="quiet" onclick="nearbyForApi('API')">Nearby activity</button></td>`, where the API name is written into the attribute exactly the way the `openCallsFor` button does it (`esc(r[0]).replace(/'/g, "\\'")`; no route name contains a quote). 6 headers, 6 cells per row. Nothing else in the function changes.

3. The `slowest-apis` table in `parts/41-tools-a.js`: the header row gains a final empty `<th></th>` after `<th>Slow calls</th>` (7 headers), and every row gains one final cell after the existing The calls cell: `<td><button class="quiet" onclick="nearbyForApi('API')">Nearby activity</button></td>`, same name writing as item 2. The existing The calls button, its cell and its header are untouched. 7 headers, 7 cells per row.

4. The Slowest calls tool in `parts/41-tools-a.js` calls `slowCallsTable(true)` instead of `slowCallsTable(false)`, so its rows get the Nearby activity column. The row onclick inside `slowCallsTable` in `parts/30-steps-0-4.js` gains the manual-mode openTool: the attribute value that ended `;render()` now ends `;render();if(W.mode==='manual')openTool('nearby-activity');`. Step 3 keeps its current behaviour (guided mode never enters the new branch). The tool notes sentence `No Nearby activity column here: that lives in Step 3 and the nearby-activity tool.` is now false; in the same `dt('Buttons', ...)` line it becomes `Nearby activity on a row sets the moment and opens the nearby-activity tool (live).`

5. `parts/10-data3-near.js`: the Nearby view must cover every log kind the case considers.
   - The lane list in `nearView` becomes `['API', 'SQL', 'Mongo', 'Host', 'IIS', 'Events']` (no new fixture rows; the two new lanes stay empty in this case).
   - The note under the title becomes exactly: `Every source we have, on one time line: the shaded band is the slice the buttons choose, and the table lists what falls inside it. MongoDB, Host, IIS and Windows events have no source in this case, so those lanes stay empty.`
   - The header comment changes the same way: `src = lane (API, SQL, Mongo, Host, IIS, Events)` and `The case has no MongoDB, Host, IIS or Windows events source, so those lanes stay empty.` The `nearView` comment says `one swim-lane of all six lanes` instead of `all four lanes`.
   - `swimlane(lanes, window, brush)` in `parts/11-charts-c.js` is generic over the lane array (it loops `lanes` and draws one lane per entry); it is NOT edited and six lanes render without any change to it.

6. `parts/42-tools-b.js`: in the `nearby-activity` tool notes, `Lanes: API calls, SQL Server, MongoDB, Host.` becomes `Lanes: API calls, SQL Server, MongoDB, Host, IIS, Windows events.`

## Edits

### parts/30-steps-0-4.js
<<<<<<< SEARCH
// ----- Step 2: Shape -----
function byApiTable(limit){
=======
// ----- Step 2: Shape -----
function nearbyForApi(api){
  var i = 0;
  for (var k = 0; k < CALLS.length; k++) {
    if (CALLS[k][0] === api) { i = k; break; }
  }
  W.nearbyRow = i;
  W.nearbyPicked = true;
  W.nearbyOpen = true;
  W.nbAround = '2026-10-26T' + CALLS[i][1].slice(0, 5);
  if (W.mode === 'manual') openTool('nearby-activity'); else go(3);
}
function byApiTable(limit){
>>>>>>> REPLACE

<<<<<<< SEARCH
  return '<div class="tscroll"><table><caption>By API. 3 of 12 moved.</caption><thead><tr><th>API</th><th>Calls</th><th>P95</th><th>Reference P95</th><th>Change</th></tr></thead><tbody>'
    + rows.map(function(r){
      return '<tr><td>' + r[0] + '</td><td>' + fmtCount(r[1]) + '</td><td' + (r[6] === 'bad' ? ' class="slow"' : '') + '>' + fmtMs(r[2]) + '</td><td>' + fmtMs(r[3]) + '</td>'
        + '<td><span class="mark ' + r[6] + '"><span aria-hidden="true">' + (r[6] === 'bad' ? '▲' : '●') + '</span> ' + r[7] + '</span> ' + fmtPct(r[4]) + ' (' + signed(fmtCount(r[5])) + ' ms)</td></tr>';
    }).join('') + '</tbody></table></div>';
=======
  return '<div class="tscroll"><table><caption>By API. 3 of 12 moved.</caption><thead><tr><th>API</th><th>Calls</th><th>P95</th><th>Reference P95</th><th>Change</th><th></th></tr></thead><tbody>'
    + rows.map(function(r){
      return '<tr><td>' + r[0] + '</td><td>' + fmtCount(r[1]) + '</td><td' + (r[6] === 'bad' ? ' class="slow"' : '') + '>' + fmtMs(r[2]) + '</td><td>' + fmtMs(r[3]) + '</td>'
        + '<td><span class="mark ' + r[6] + '"><span aria-hidden="true">' + (r[6] === 'bad' ? '▲' : '●') + '</span> ' + r[7] + '</span> ' + fmtPct(r[4]) + ' (' + signed(fmtCount(r[5])) + ' ms)</td>'
        + '<td><button class="quiet" onclick="nearbyForApi(\'' + esc(r[0]).replace(/'/g, "\\'") + '\')">Nearby activity</button></td></tr>';
    }).join('') + '</tbody></table></div>';
>>>>>>> REPLACE

<<<<<<< SEARCH
        + (withNearby ? '<td><button class="btn sm" onclick="W.nearbyRow=' + i + ';W.nearbyOpen=true;W.nearbyPicked=true;W.nbAround=\'2026-10-26T' + c[1].slice(0, 5) + '\';render()">Nearby activity</button></td>' : '');
=======
        + (withNearby ? '<td><button class="btn sm" onclick="W.nearbyRow=' + i + ';W.nearbyOpen=true;W.nearbyPicked=true;W.nbAround=\'2026-10-26T' + c[1].slice(0, 5) + '\';render();if(W.mode===\'manual\')openTool(\'nearby-activity\');">Nearby activity</button></td>' : '');
>>>>>>> REPLACE

### parts/41-tools-a.js
<<<<<<< SEARCH
  return form + '<div class="tscroll"><table><thead><tr><th>API</th><th>Calls</th><th>P95</th><th>Reference P95</th><th>Change</th><th>Slow calls</th></tr></thead><tbody>'
=======
  return form + '<div class="tscroll"><table><thead><tr><th>API</th><th>Calls</th><th>P95</th><th>Reference P95</th><th>Change</th><th>Slow calls</th><th></th></tr></thead><tbody>'
>>>>>>> REPLACE

<<<<<<< SEARCH
      return '<tr><td>' + r[0] + '</td><td>' + fmtCount(r[1]) + '</td><td' + (r[6] === 'bad' ? ' class="slow"' : '') + '>' + fmtMs(r[2]) + '</td><td>' + fmtMs(r[3]) + '</td>'
        + '<td><span class="mark ' + r[6] + '"><span aria-hidden="true">' + (r[6] === 'bad' ? '▲' : '●') + '</span> ' + r[7] + '</span> ' + fmtPct(r[4]) + ' (' + signed(fmtCount(r[5])) + ' ms)</td>'
        + '<td><button class="quiet" onclick="openCallsFor(\'' + esc(r[0]).replace(/'/g, "\\'") + '\')">The calls</button></td></tr>';
=======
      return '<tr><td>' + r[0] + '</td><td>' + fmtCount(r[1]) + '</td><td' + (r[6] === 'bad' ? ' class="slow"' : '') + '>' + fmtMs(r[2]) + '</td><td>' + fmtMs(r[3]) + '</td>'
        + '<td><span class="mark ' + r[6] + '"><span aria-hidden="true">' + (r[6] === 'bad' ? '▲' : '●') + '</span> ' + r[7] + '</span> ' + fmtPct(r[4]) + ' (' + signed(fmtCount(r[5])) + ' ms)</td>'
        + '<td><button class="quiet" onclick="openCallsFor(\'' + esc(r[0]).replace(/'/g, "\\'") + '\')">The calls</button></td>'
        + '<td><button class="quiet" onclick="nearbyForApi(\'' + esc(r[0]).replace(/'/g, "\\'") + '\')">Nearby activity</button></td></tr>';
>>>>>>> REPLACE

<<<<<<< SEARCH
  return slowCallsTable(false) + (W.slowShown ? '<p class="note">The longest calls that started in the incident window.</p>'
=======
  return slowCallsTable(true) + (W.slowShown ? '<p class="note">The longest calls that started in the incident window.</p>'
>>>>>>> REPLACE

<<<<<<< SEARCH
    + dt('Buttons', 'Show the slowest calls (live). No Nearby activity column here: that lives in Step 3 and the nearby-activity tool. No Run bar.');
=======
    + dt('Buttons', 'Show the slowest calls (live). Nearby activity on a row sets the moment and opens the nearby-activity tool (live). No Run bar.');
>>>>>>> REPLACE

### parts/10-data3-near.js
<<<<<<< SEARCH
// One row per recorded event. at = UTC clock time, src = lane (API, SQL, Mongo, Host),
=======
// One row per recorded event. at = UTC clock time, src = lane (API, SQL, Mongo, Host, IIS, Events),
>>>>>>> REPLACE

<<<<<<< SEARCH
// sev = ok, warn or bad. The case has no MongoDB or Host source, so those lanes stay empty.
=======
// sev = ok, warn or bad. The case has no MongoDB, Host, IIS or Windows events source, so those lanes stay empty.
>>>>>>> REPLACE

<<<<<<< SEARCH
// The "Nearby activity" block of step 4: one swim-lane of all four lanes on the
// incident hour, a brush of m minutes around the onset, and the events inside it.
=======
// The "Nearby activity" block of step 4: one swim-lane of all six lanes on the
// incident hour, a brush of m minutes around the onset, and the events inside it.
>>>>>>> REPLACE

<<<<<<< SEARCH
  var lanes = ['API', 'SQL', 'Mongo', 'Host'].map(function(src){
=======
  var lanes = ['API', 'SQL', 'Mongo', 'Host', 'IIS', 'Events'].map(function(src){
>>>>>>> REPLACE

<<<<<<< SEARCH
  + '<p class="note">Every source we have, on one time line: the shaded band is the slice the buttons choose, and the table lists what falls inside it. MongoDB and Host have no source in this case, so those lanes stay empty.</p>'
=======
  + '<p class="note">Every source we have, on one time line: the shaded band is the slice the buttons choose, and the table lists what falls inside it. MongoDB, Host, IIS and Windows events have no source in this case, so those lanes stay empty.</p>'
>>>>>>> REPLACE

### parts/42-tools-b.js
<<<<<<< SEARCH
  return dt('Data shown', 'Own read around <code>around &plusmn; before/after</code>. Lanes: API calls, SQL Server, MongoDB, Host. No Run bar (READS_ITSELF).')
=======
  return dt('Data shown', 'Own read around <code>around &plusmn; before/after</code>. Lanes: API calls, SQL Server, MongoDB, Host, IIS, Windows events. No Run bar (READS_ITSELF).')
>>>>>>> REPLACE

## Check (the owner applies the blocks and runs the full check; you only run this)
```
node --check parts/30-steps-0-4.js && node --check parts/41-tools-a.js && node --check parts/10-data3-near.js && node --check parts/42-tools-b.js \
&& grep -c -F "// ----- Step 2: Shape -----" parts/30-steps-0-4.js \
&& grep -c -F "<caption>By API. 3 of 12 moved.</caption>" parts/30-steps-0-4.js \
&& grep -c -F "';render()\">Nearby activity</button></td>' : '');" parts/30-steps-0-4.js \
&& grep -c -F "<th>Reference P95</th><th>Change</th><th>Slow calls</th></tr></thead><tbody>'" parts/41-tools-a.js \
&& grep -c -F "The calls</button></td></tr>';" parts/41-tools-a.js \
&& grep -c -F "return slowCallsTable(false) + (W.slowShown ?" parts/41-tools-a.js \
&& grep -c -F "No Nearby activity column here: that lives in Step 3 and the nearby-activity tool." parts/41-tools-a.js \
&& grep -c -F "src = lane (API, SQL, Mongo, Host)," parts/10-data3-near.js \
&& grep -c -F "The case has no MongoDB or Host source, so those lanes stay empty." parts/10-data3-near.js \
&& grep -c -F "one swim-lane of all four lanes on the" parts/10-data3-near.js \
&& grep -c -F "var lanes = ['API', 'SQL', 'Mongo', 'Host'].map(function(src){" parts/10-data3-near.js \
&& grep -c -F "MongoDB and Host have no source in this case, so those lanes stay empty.</p>'" parts/10-data3-near.js \
&& grep -c -F "Lanes: API calls, SQL Server, MongoDB, Host. No Run bar (READS_ITSELF)." parts/42-tools-b.js
```
Expected: no output from the four `node --check` runs, then 13 lines each reading `1`.

After the owner applies the blocks, from `buildout/wireframes`: `node ../packets/data/chk-fix10.js` prints `PASS`.

## Output
The blocks in one plain reply, then exactly 3 lines: `Status: PASS|FAIL`, `Checks: <printed output>`, `Concerns: none` (or one line).
