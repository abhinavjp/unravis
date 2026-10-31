### parts/30-steps-0-4.js
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

### parts/30-steps-0-4.js
<<<<<<< SEARCH
        + (withNearby ? '<td><button class="btn sm" onclick="W.nearbyRow=' + i + ';W.nearbyOpen=true;W.nearbyPicked=true;W.nbAround=\'2026-10-26T' + c[1].slice(0, 5) + '\';render()">Nearby activity</button></td>' : '');
=======
        + (withNearby ? '<td><button class="btn sm" onclick="W.nearbyRow=' + i + ';W.nearbyOpen=true;W.nearbyPicked=true;W.nbAround=\'2026-10-26T' + c[1].slice(0, 5) + '\';render();if(W.mode===\'manual\')openTool(\'nearby-activity\');">Nearby activity</button></td>' : '');
>>>>>>> REPLACE

### parts/41-tools-a.js
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

### parts/41-tools-a.js
<<<<<<< SEARCH
  return callsFilter + slowCallsTable(false, callNames) + (W.slowShown ? '<p class="note">The longest calls that started in the incident window.</p>'
=======
  return callsFilter + slowCallsTable(true, callNames) + (W.slowShown ? '<p class="note">The longest calls that started in the incident window.</p>'
>>>>>>> REPLACE

### parts/41-tools-a.js
<<<<<<< SEARCH
    + dt('Buttons', 'Show the slowest calls (live). No Nearby activity column here: that lives in Step 3 and the nearby-activity tool. No Run bar.');
=======
    + dt('Buttons', 'Show the slowest calls (live). Nearby activity on a row sets the moment and opens the nearby-activity tool (live). No Run bar.');
>>>>>>> REPLACE

### parts/10-data3-near.js
<<<<<<< SEARCH
// sev = ok, warn or bad. The case has no MongoDB or Host source, so those lanes stay empty.
=======
// sev = ok, warn or bad. The case has no MongoDB, Host, IIS or Windows events source, so those lanes stay empty.
>>>>>>> REPLACE

### parts/10-data3-near.js
<<<<<<< SEARCH
// The "Nearby activity" block of step 4: one swim-lane of all four lanes on the
// incident hour, a brush of m minutes around the onset, and the events inside it.
=======
// The "Nearby activity" block of step 4: one swim-lane of all six lanes on the
// incident hour, a brush of m minutes around the onset, and the events inside it.
>>>>>>> REPLACE

### parts/10-data3-near.js
<<<<<<< SEARCH
  var lanes = ['API', 'SQL', 'Mongo', 'Host'].map(function(src){
=======
  var lanes = ['API', 'SQL', 'Mongo', 'Host', 'IIS', 'Events'].map(function(src){
>>>>>>> REPLACE

### parts/10-data3-near.js
<<<<<<< SEARCH
  + '<p class="note">Every source we have, on one time line: the shaded band is the slice the buttons choose, and the table lists what falls inside it. MongoDB and Host have no source in this case, so those lanes stay empty.</p>'
=======
  + '<p class="note">Every source we have, on one time line: the shaded band is the slice the buttons choose, and the table lists what falls inside it. MongoDB, Host, IIS and Windows events have no source in this case, so those lanes stay empty.</p>'
>>>>>>> REPLACE
