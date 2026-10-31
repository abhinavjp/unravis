// ---------- tool bodies: response-time, slowest-apis, slowest-calls, time-split ----------
var TOOL_BODIES = window.TOOL_BODIES = window.TOOL_BODIES || {};
var TOOL_NOTES = window.TOOL_NOTES = window.TOOL_NOTES || {};

function openCallsFor(api){
  W.callsApi = api;
  W.slowPg = 0;
  W.slowShown = true;
  openTool('slowest-calls');
}
function slowestApiFiltered(){
  var min = Number(W.minCalls); if (!(min >= 0)) min = 20;
  var needle = String(W.match || '').trim().toLowerCase();
  return ROUTES.filter(function(r){
    if (r[1] < min) return false;
    if (needle && r[0].toLowerCase().indexOf(needle) < 0) return false;
    return true;
  });
}
function anyMatchIgnoringMin(){
  var needle = String(W.match || '').trim().toLowerCase();
  if (!needle) return true;
  return ROUTES.some(function(r){ return r[0].toLowerCase().indexOf(needle) >= 0; });
}

TOOL_BODIES['response-time'] = function(){
  if (!W.ranSteps) return '<p class="note">This step shows the last run. Run the steps first to fill it in.</p>';
  return '<p class="lead" style="color:var(--text)">Which APIs moved, and by how much? A change counts only when it passes both marks.</p>'
    + TA_gapShade(minuteLine('p95', 'P95 by minute (ms)', 'ms'))
    + '<p class="note">The gray band marks the gap 09:31&ndash;09:44 UTC: 14 minutes with no audit records, so the line breaks inside it.</p>'
    + byApiTable(!W.showAllApis)
    + '<div><button class="quiet" onclick="W.showAllApis=!W.showAllApis;render()">' + (W.showAllApis ? 'Show fewer' : 'Show every API') + '</button></div>'
    + '<p class="note">The rule: a change counts when it is at least 30 % and at least 100 ms.</p>'
    + TA_whyMoved();
};
TOOL_NOTES['response-time'] = function(){
  return dt('Data shown', 'Mirrors Step 2: <code>facts.series[].p95</code> and <code>facts.routes[]</code> (route, incident.n, incident.p95, reference.p95, p95Change).')
    + dt('Chart', '<code>MinuteLine</code> of P95 (ms). Normal = blue, incident = red. X: minutes 08:00&ndash;09:59 UTC. Y: ms to the peak. Gap 09:31&ndash;09:44 breaks the line.')
    + dt('Empty', 'No audit records: "This window holds no audit records, so there is nothing to list. Cannot test yet." No chart.')
    + dt('Cannot test yet', 'No audit source: missing box. No calls in a window: empty line above.')
    + dt('Failed', '"The tool could not run."')
    + dt('Out of date', 'Sticker Out of date, rerun when the windows changed after the last run.')
    + dt('Buttons', 'Show every API / Show fewer (live). Run the steps fills the body (live via Pretend the steps have run, or Run).')
    + dt('Why it moved', 'Under the table, a fold gives a one-line why for each API that moved, with a link to the tool that holds the evidence. The gray band on the chart marks the 09:31&ndash;09:44 UTC gap, where the audit recorded nothing.');
};

TOOL_BODIES['slowest-apis'] = function(){
  var min = Number(W.minCalls); if (!(min >= 0)) min = 20;
  var form = '<div class="pair"><label class="wf">Minimum calls<input class="wf" type="number" min="0" step="1" value="' + min + '" onchange="W.minCalls=this.value;render()"></label>'
    + '<label class="wf">Only APIs matching<input class="wf" type="text" maxlength="100" value="' + esc(W.match) + '" oninput="W.match=this.value;render()"></label></div>';
  if (!W.ranSteps) return form + '<p class="note">This step shows the last run. Run the steps first to fill it in.</p>';
  if (ROUTES.length === 0) return form + '<p class="note">This window holds no audit records, so there is nothing to list. Cannot test yet.</p>';
  var rows = slowestApiFiltered();
  if (rows.length === 0) {
    var msg = !anyMatchIgnoringMin()
      ? 'No API matches "' + esc(String(W.match).trim()) + '".'
      : 'No API had ' + min.toLocaleString('en-US') + ' or more calls. Lower Minimum calls to see more.';
    return form + '<p class="note">' + msg + '</p>';
  }
  return form + '<div class="tscroll"><table><thead><tr><th>API</th><th>Calls</th><th>P95</th><th>Reference P95</th><th>Change</th><th>Slow calls</th><th></th></tr></thead><tbody>'
    + rows.map(function(r){
      return '<tr><td>' + r[0] + '</td><td>' + fmtCount(r[1]) + '</td><td' + (r[6] === 'bad' ? ' class="slow"' : '') + '>' + fmtMs(r[2]) + '</td><td>' + fmtMs(r[3]) + '</td>'
        + '<td><span class="mark ' + r[6] + '"><span aria-hidden="true">' + (r[6] === 'bad' ? '▲' : '●') + '</span> ' + r[7] + '</span> ' + fmtPct(r[4]) + ' (' + signed(fmtCount(r[5])) + ' ms)</td>'
        + '<td><button class="quiet" onclick="openCallsFor(\'' + esc(r[0]).replace(/'/g, "\\'") + '\')">The calls</button></td>'
        + '<td><button class="quiet" onclick="nearbyForApi(\'' + esc(r[0]).replace(/'/g, "\\'") + '\')">Nearby activity</button></td></tr>';
    }).join('') + '</tbody></table></div><p class="note">Every row links to the individual slow calls behind it, in the Slowest calls tool.</p>';
};
TOOL_NOTES['slowest-apis'] = function(){
  return dt('Data shown', '<code>slowestApiRows(result, minCalls, match)</code> from <code>facts.routes[]</code>. Default Minimum calls is 20. Match is a case-insensitive substring.')
    + dt('Chart', 'None. Ranked table: API, Calls, P95, Reference P95, Change, plus a link to the slow calls behind each row.')
    + dt('Empty', 'Four messages: jumpedAhead (not run); noRecords (no routes); noApiMatch (match hits nothing even at min 0); noApiRows (min too high).')
    + dt('Cannot test yet', 'No audit source: missing box.')
    + dt('Failed', '"The tool could not run."')
    + dt('Out of date', 'Sticker only; filters still apply to the last run.')
    + dt('Buttons', 'Filters live. Try Minimum calls 5000 (only Orders.Create) or matching "Pay".');
};

TOOL_BODIES['slowest-calls'] = function(){
  var callNames = W.callsApi ? [W.callsApi] : ROUTES.map(function(r){ return r[0]; });
  var callsFilter = '<div class="pair"><label class="wf">API<select class="wf" onchange="W.callsApi=this.value;W.slowPg=0;render()">'
    + '<option value=""' + (W.callsApi ? '' : ' selected') + '>All APIs</option>'
    + ROUTES.map(function(r){ return '<option value="' + esc(r[0]) + '"' + (r[0] === W.callsApi ? ' selected' : '') + '>' + esc(r[0]) + '</option>'; }).join('')
    + '</select></label></div>'
    + '<div><button class="quiet" onclick="if(W.callsApi){W.apiMode=\'one\';W.apiOne=W.callsApi;}else{W.apiMode=\'all\';}render()">Use this filter in Step 3 too</button></div>'
    + '<p class="note">Each place keeps its own filter. This button copies this one to Step 3.</p>';
  return callsFilter + slowCallsTable(true, callNames) + (W.slowShown ? '<p class="note">The longest calls that started in the incident window.</p>'
    + TA_callBars()
    + '<p class="note">One bar per listed call: the taller the bar, the longer the call took. Read the exact start times in the table.</p>' : '');
};
TOOL_NOTES['slowest-calls'] = function(){
  return dt('Data shown', 'Audit slowest-calls read: route, initiatedAt, durationMs. Own button, no Run bar (READS_ITSELF).')
    + dt('Chart', 'Duration bars: one bar per listed call, the height is how long the call took in ms. Table: API, Started (UTC), Time.')
    + dt('Empty', 'Before Show: only the button. After a quiet window: "This window holds no audit records, so there is nothing to list. Cannot test yet."')
    + dt('Cannot test yet', 'No audit source: missing box.')
    + dt('Failed', '"The tool could not run."')
    + dt('Out of date', 'Not a state of this tool (it reads itself).')
    + dt('Buttons', 'Show the slowest calls (live). Nearby activity on a row sets the moment and opens the nearby-activity tool (live). No Run bar.');
};

TOOL_BODIES['time-split'] = function(){
  if (!W.ranSteps) return '<p class="note">This step shows the last run. Run the steps first to fill it in.</p>';
  var extra = !W.have.iis ? '<p class="note">The audit part runs. The outside time needs an IIS log source.</p><div><button class="btn sm" onclick="inert(\'Opens the case\'s Add data flow\')">Add the IIS logs</button></div>' : '';
  return '<p class="lead" style="color:var(--text)">Where did the time of a call go? Each call is split into audit set-up, action time, payload record and outside time.</p>'
    + partsTable() + partsBars()
    + '<p class="note">Outside time (O) grew the most: it holds 85 % of the extra time.</p>'
    + extra
    + TA_splitByApi();
};
TOOL_NOTES['time-split'] = function(){
  return dt('Data shown', 'Mirrors Step 3 without Slowest calls: <code>facts.parts.items[]</code> (incMeanMs, refMeanMs, change, share, tested).')
    + dt('Chart', 'Stacked horizontal bars Normal vs Incident, segments A/C/P/O. Plus the parts table (Part, Normal, Incident, Change, Share of the extra time).')
    + dt('Empty', 'Not run: jumpedAhead. Quiet window: "The window held no calls with an IIS row&hellip;"')
    + dt('Cannot test yet', 'No audit: missing box. No IIS: Partly ready, O untested, Add the IIS logs.')
    + dt('Failed', '"The tool could not run."')
    + dt('Out of date', 'Sticker; numbers from the earlier run.')
    + dt('Buttons', 'Add the IIS logs (inert). IIS checkbox in the notes strip switches Partly ready / Ready live.')
    + dt('API picker', 'All APIs, one API, or several by a match: the per-API rows under the pooled chart show A, C, P and O for each picked API, normal against incident, plus the part that grew most.');
};
