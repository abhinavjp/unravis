// ---------- tool bodies: calls-in-flight, nearby-activity, changes, sql-waits ----------
var TOOL_BODIES = window.TOOL_BODIES = window.TOOL_BODIES || {};
var TOOL_NOTES = window.TOOL_NOTES = window.TOOL_NOTES || {};

TOOL_BODIES['calls-in-flight'] = function(){
  if (!W.ranSteps) return '<p class="note">This step shows the last run. Run the steps first to fill it in.</p>';
  return '<p class="lead" style="color:var(--text)">How many calls were open at once? More calls in flight can mean the system was busier, or that each call stayed longer.</p>'
    + '<div class="tscroll"><table><thead><tr><th>Measure</th><th>Normal</th><th>Incident</th><th>Change</th></tr></thead><tbody>'
    + LOADROWS.map(function(r){ return '<tr><td>' + r[0] + '</td><td>' + r[1] + '</td><td' + (r[3] === 'Higher' ? ' class="slow"' : '') + '>' + r[2] + '</td><td>' + r[3] + '</td></tr>'; }).join('') + '</tbody></table></div>'
    + '<p class="finding">The system filled because calls took longer, not because more calls arrived.</p>'
    + (W.frame.poolLimit && W.frame.poolLimit.trim() !== '' ? '<p class="note">Calls in flight peaked at 34 of the ' + W.frame.poolLimit.trim() + ' entered as the pool limit. The pool was not filled.</p>' : '<p class="note">Calls in flight peaked at 34. No pool limit was entered in Step 0, so there is nothing to compare it against.</p>')
    + minuteLine('inFlight', 'Calls in flight by minute', 'calls');
};
TOOL_NOTES['calls-in-flight'] = function(){
  return dt('Data shown', 'Mirrors Step 4: <code>facts.load</code> (refPerSec/incPerSec, refWms/incWms, refL/incL, refPeak/incPeak) and <code>signals.pool</code>.')
    + dt('Chart', '<code>MinuteLine</code> of <code>series[].inFlight</code>, unit "calls". Same colours and gap as the P95 chart.')
    + dt('Empty', 'Not run: jumpedAhead. Too few calls: "The window held too few calls to count calls in flight."')
    + dt('Cannot test yet', 'No audit: missing box.')
    + dt('Failed', '"The tool could not run."')
    + dt('Out of date', 'Sticker; numbers from the earlier run.')
    + dt('Buttons', 'None of its own. Pool figure comes from the windows panel (SqlClient pool limit).');
};

TOOL_BODIES['nearby-activity'] = function(){
  W.nearbyOpen = true;
  W.nearbyPicked = W.nearbyRow != null;
  return nearbyPanel(true) + (typeof nearView === 'function' ? nearView() : '');
};
TOOL_NOTES['nearby-activity'] = function(){
  return dt('Data shown', 'Own read around <code>around &plusmn; before/after</code>. Lanes: API calls, SQL Server, MongoDB, Host, IIS, Windows events. No Run bar (READS_ITSELF).')
    + dt('Chart', 'Duration bars per call, green under 1 s, orange 1&ndash;2 s, red striped over 2 s, width relative to the longest call shown.')
    + dt('Empty', '"Nothing was recorded in this window." Missing lanes: "Cannot test yet: this case has no source of this kind."')
    + dt('Cannot test yet', 'No audit: missing box. Other lanes still cannot-test when those sources are off.')
    + dt('Failed', '"The activity could not be read."')
    + dt('Out of date', 'Not a state of this tool.')
    + dt('Buttons', 'Quick windows 2/5/15 min (live). Show activity (live, redraws). Close is hidden here because this page is the panel. No Run bar.');
};

TOOL_BODIES['changes'] = function(){
  if (!W.ranSteps) return '<p class="note">This step shows the last run. Run the steps first to fill it in.</p>';
  var entered = W.frame.releasesEntered && W.frame.releases.length > 0;
  if (!entered) {
    return '<p class="lead" style="color:var(--text)">Did something change just before the onset? Only changes you list are compared.</p>'
      + '<p class="note">' + (W.frame.releasesEntered ? 'You said there was no change to list.' : 'No change listed. Add the releases or changes you know of in Step 0.') + '</p>'
      + '<div><button class="btn sm" onclick="setMode(\'guided\');go(0)">Change the list</button></div>';
  }
  return '<p class="lead" style="color:var(--text)">Did something change just before the onset? Only changes you list are compared.</p>'
    + '<div class="tscroll"><table><thead><tr><th>When</th><th>What</th><th>Against the onset</th></tr></thead><tbody>'
    + CHANGES0.map(function(c){ return '<tr><td>' + c.at.replace('T', ' ') + ' UTC</td><td>' + c.label + '</td><td>' + c.rel + '</td></tr>'; }).join('') + '</tbody></table></div>'
    + '<p class="note">Release 2026.10.26.2 of Orders.Api is the closest change before the onset (09:14).</p>'
    + '<p class="note">Only changes you listed are compared. Unlisted changes are invisible to this step.</p>'
    + (typeof TBWindowMs === 'function' && (TBWindowMs(W.frame.incidentEnd) - TBWindowMs(W.frame.incidentStart)) > 86400000 ? '<p class="note">This window spans more than a day. A change from early in a long window is easy to miss if it is not already in the list above.</p>' : '');
};
TOOL_NOTES['changes'] = function(){
  return dt('Data shown', 'Mirrors Step 5: <code>signals.changes.items[]</code> (at, label, relation). The list is <code>frame.releases[]</code>.')
    + dt('Chart', 'None. Timeline table: When, What, Against the onset.')
    + dt('Empty', '"No change listed." Tick I have listed every change with an empty list: "You said there was no change to list."')
    + dt('Cannot test yet', 'No audit: missing box. No onset: reason list.')
    + dt('Failed', '"The tool could not run."')
    + dt('Out of date', 'Editing the list in the windows panel marks a previous run stale.')
    + dt('Buttons', 'Change the list jumps to Guided Step 0 (live).');
};

TOOL_BODIES['sql-waits'] = function(){
  if (!W.ranSteps) return '<p class="note">This step shows the last run. Run the steps first to fill it in.</p>';
  return '<p class="lead" style="color:var(--text)">Checks on SQL Server: blocking, waits and capacity.</p>'
    + findingsList(['Lock waits on Orders and OrderLines rose from 0.4 s to 31 s per minute at 09:14.', 'Blocking chains of up to 9 s were recorded between 09:14 and 09:45.', 'No deadlocks were recorded in the window.', 'CPU on the SQL host is not in this data: it needs a Blackbox.'])
    + '<div class="tscroll"><table><caption>Wait classes in the incident window (dummy; the repo has no per-wait table)</caption><thead><tr><th>Wait class</th><th>Normal (s / min)</th><th>Incident (s / min)</th><th>Change</th></tr></thead><tbody>'
    + '<tr><td>LCK_M_X on Orders</td><td>0.4</td><td class="slow">31.0</td><td>Rose at 09:14</td></tr>'
    + '<tr><td>LCK_M_X on OrderLines</td><td>0.1</td><td class="slow">8.6</td><td>Rose at 09:14</td></tr>'
    + '<tr><td>PAGEIOLATCH_SH</td><td>0.2</td><td>0.3</td><td>About the same</td></tr>'
    + '<tr><td>WRITELOG</td><td>0.3</td><td>0.4</td><td>About the same</td></tr>'
    + '</tbody></table></div>'
    + '<p class="note">The product UI for Step 7 shows findings and suspects, not this wait table. The table is a labelled placeholder for the tool.</p>'
    + suspectsHere(['D1']);
};
TOOL_NOTES['sql-waits'] = function(){
  return dt('Data shown', 'Mirrors Step 7: engine findings for the SQL group (<code>steps[6].findings</code>), suspects of group D. Wait-class table is a labelled placeholder: the repo has no per-wait component.')
    + dt('Chart', 'None in the repo UI. Placeholder table: Wait class, Normal (s / min), Incident (s / min), Change.')
    + dt('Empty', 'A quiet SQL window: findings say no blocking was recorded.')
    + dt('Cannot test yet', 'No SQL source: missing box (default dummy has SQL on, so Ready after a run).')
    + dt('Failed', '"The tool could not run."')
    + dt('Out of date', 'Sticker; numbers from the earlier run.')
    + dt('Buttons', 'Add its check on D1 (live).');
};

// FIX5: with no call chosen, the panel header shows the Around moment, not CALLS[W.nearbyRow].
(function(){
  if (typeof nearbyPanel !== 'function') return;
  var base = nearbyPanel;
  nearbyPanel = function(){
    var html = base.apply(null, arguments);
    if (W.nearbyPicked) return html;
    var around = String(W.nbAround == null ? '' : W.nbAround);
    var i = around.indexOf('T');
    var hhmm = i >= 0 ? around.slice(i + 1, i + 6) : around;
    var call = (typeof CALLS !== 'undefined' && CALLS) ? CALLS[W.nearbyRow] : null;
    if (call && String(call[1]).slice(0, 5) === hhmm) return html;
    return html.replace(/<h4>Nearby activity &middot; [^<]*<\/h4>/, '<h4>Nearby activity &middot; ' + hhmm + ' UTC</h4>');
  };
})();
