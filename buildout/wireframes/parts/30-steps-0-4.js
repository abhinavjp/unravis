// ---------- steps 0 to 4 ----------
function signed(n){ n = String(n); return n.charAt(0) === '-' ? n : '+' + n; }
function statePrefix(n, st){
  if (st === 'Waiting') return '<p class="note">This step shows the last run. Run the steps first to fill it in.</p>';
  if (st === 'Failed') return '<div class="err" role="alert"><b>The steps could not run.</b><span>The audit source could not be read.</span><span><b>Next:</b> check the source and run again.</span><div><button class="btn sm" onclick="W.stepState[' + n + ']=\'Ready\';render()">Retry reading</button></div></div>';
  if (st === 'Out of date') return '<p class="notice" role="status">The windows, sources or lists changed after this ran. The numbers below are from the earlier run.</p>';
  return '';
}
function cannotBody(n, rows, cta){
  return '<p class="note">This step could not test what it reads. Nothing is shown rather than a guess.</p>' + reasonsBox(rows)
    + (cta ? '<div class="row"><button class="btn sm" onclick="inert(\'Opens the case\\\'s Add data flow\')">' + cta + '</button></div>' : '');
}

// ----- Step 0: Frame the question (always a form; states change the frame around it) -----
function frameProblems(){
  var f = W.frame, out = [];
  var p = function(s){ return Date.parse(s); };
  if (!p(f.incidentStart) || !p(f.incidentEnd) || p(f.incidentEnd) <= p(f.incidentStart)) out.push('The incident window needs a start before its end, in UTC.');
  if (!p(f.referenceStart) || !p(f.referenceEnd) || p(f.referenceEnd) <= p(f.referenceStart)) out.push('The reference window needs a start before its end, in UTC.');
  if (f.poolLimit.trim() !== '' && (!/^\d{1,6}$/.test(f.poolLimit.trim()) || Number(f.poolLimit.trim()) < 1 || Number(f.poolLimit.trim()) > 100000)) out.push('The pool limit is a whole number from 1 to 100000, or empty.');
  if (f.releases.some(function(r){ return !Date.parse(r.at); })) out.push('Each change needs a time, in UTC.');
  if (f.releases.some(function(r){ return r.label.trim() === ''; })) out.push('Each change needs a name.');
  return out;
}
function quickPick(name){
  var now = Date.now(), h = 3600000, d = new Date(now), f = W.frame;
  var iso = function(ms){ return new Date(ms).toISOString().slice(0, 16); };
  if (name === 'last-hour') { f.incidentStart = iso(now - h); f.incidentEnd = iso(now); f.referenceStart = iso(now - 2 * h); f.referenceEnd = iso(now - h); }
  if (name === 'today') { var s = Date.UTC(d.getUTCFullYear(), d.getUTCMonth(), d.getUTCDate()); f.incidentStart = iso(s); f.incidentEnd = iso(now); f.referenceStart = iso(s - 86400000); f.referenceEnd = iso(s - 86400000 + (now - s)); }
  if (name === 'yesterday') { var y0 = Date.UTC(d.getUTCFullYear(), d.getUTCMonth(), d.getUTCDate()) - 86400000; f.incidentStart = iso(y0); f.incidentEnd = iso(y0 + 86340000); f.referenceStart = iso(y0 - 86400000); f.referenceEnd = iso(y0 + 86340000 - 86400000); }
  frameKeepTouching();
  render();
}
function frameForm(){
  var f = W.frame;
  var problems = frameProblems();
  var changes = f.releases.map(function(r, i){
    return '<div class="change-row"><label class="wf">When (UTC)<input class="wf" type="datetime-local" value="' + r.at + '" onchange="W.frame.releases[' + i + '].at=this.value"></label>'
      + '<label class="wf">What changed<input class="wf" type="text" maxlength="200" value="' + esc(r.label) + '" onchange="W.frame.releases[' + i + '].label=this.value"></label>'
      + '<button class="quiet" onclick="W.frame.releases.splice(' + i + ',1);render()">Remove</button></div>' + frameReleaseLine(r);
  }).join('');
  return '<p class="lead" style="color:var(--text);font-size:14px">Slow compared to what? Pick a normal period to compare with. Then Unravis looks for the minute things changed.</p>'
  + '<fieldset class="wf"><legend>Incident window</legend><div class="pair">'
  + '<label class="wf">From<input class="wf" type="datetime-local" value="' + f.incidentStart + '" onchange="W.frame.incidentStart=this.value;render()"></label>'
  + '<label class="wf">To<input class="wf" type="datetime-local" value="' + f.incidentEnd + '" onchange="W.frame.incidentEnd=this.value;render()"></label></div></fieldset>'
  + '<fieldset class="wf"><legend>Reference window (normal)</legend><div class="pair">'
  + '<label class="wf">From<input class="wf" type="datetime-local" value="' + f.referenceStart + '" onchange="W.frame.referenceStart=this.value;render()"></label>'
  + '<label class="wf">To<input class="wf" type="datetime-local" value="' + f.referenceEnd + '" onchange="W.frame.referenceEnd=this.value;render()"></label></div>'
  + '<p class="note">The normal period to compare with. It must not share time with the incident.</p></fieldset>'
  + frameBanner() + frameDst()
  + (typeof frameRecurrence === 'function' ? frameRecurrence() : '')
  + '<div class="picks" role="group" aria-label="Quick pick"><span class="note">Quick pick</span>'
  + '<button class="btn sm" onclick="quickPick(\'last-hour\')">Last hour</button><button class="btn sm" onclick="quickPick(\'today\')">Today</button><button class="btn sm" onclick="quickPick(\'yesterday\')">Yesterday</button></div>'
  + '<p class="note">A quick pick sets the incident window and a normal period of the same length just before it. Times are UTC. Times from your own clock or from a ticket are not converted: enter them as UTC.</p>'
  + '<div class="pair"><label class="wf">Focus on one API (optional)<input class="wf" type="text" list="guided-routes" placeholder="All APIs" maxlength="256" value="' + esc(f.route) + '" onchange="W.frame.route=this.value">'
  + '<datalist id="guided-routes">' + ROUTES.map(function(r){ return '<option value="' + r[0] + '"></option>'; }).join('') + '</datalist></label>'
  + '<label class="wf">SqlClient pool limit (optional)<input class="wf" type="text" inputmode="numeric" maxlength="6" value="' + esc(f.poolLimit) + '" onchange="W.frame.poolLimit=this.value;render()"></label></div>'
  + '<p class="note">An API is the URL path of one endpoint. Steps 3 and 4 show only that API. The rest still read every API. If you know the pool limit, enter it. Step 4 then says whether the calls in flight fill it.</p>'
  + '<fieldset class="wf"><legend>Changes you know about (optional)</legend>' + frameReleaseHint() + '<p class="note">Releases or changes with a time in UTC. Step 5 sets them beside the onset.</p>'
  + (f.releases.length === 0 ? '<p class="note">No change listed.</p>' : changes)
  + '<div class="row"><button class="btn sm" ' + (f.releases.length >= 50 ? 'disabled' : '') + ' onclick="W.frame.releases.push({at:\'\',label:\'\'});render()">+ Add a change</button>'
  + '<label class="check"><input type="checkbox" ' + (f.releasesEntered ? 'checked' : '') + ' onchange="W.frame.releasesEntered=this.checked;render()"> I have listed every change I know of</label></div>'
  + (f.releases.length >= 50 ? '<p class="note" role="status">The change list holds at most 50 changes.</p>' : '')
  + (f.releases.length > 0 && !f.releasesEntered ? '<p class="note" role="status">These changes are used only when the box beside Add a change is ticked.</p>' : '')
  + '<p class="note">Leave this off if you have not looked yet. An empty list entered on purpose reads "No change listed".</p></fieldset>'
  + '<div class="onset"><strong>Onset t0</strong><p>Unravis found the minute the slow end first stayed above twice the reference: 26 Oct 09:14 UTC.</p>'
  + '<label class="wf">My onset (UTC)<input class="wf" type="datetime-local" value="' + f.myOnset + '" onchange="W.frame.myOnset=this.value;render()"></label>'
  + (f.myOnset ? '<p class="note">My onset: ' + f.myOnset.replace('T', ' ') + '. Your time is shown beside the suggested one. The steps still use the minute Unravis found.</p>' : '')
  + '<p class="note">Scope: 73,648 calls. The incident window is 60 minutes. The reference window is 60 minutes.</p></div>'
  + (problems.length ? '<ul class="problems" role="alert"><li>' + problems.join('</li><li>') + '</li></ul><p class="note">Fix the windows to run the steps.</p>' : '')
  + frameEstimateLine()
  + '<p class="note">These windows are kept in one place. Changing them marks any step already run as out of date.</p>';
}
function step0(st){
  if (st === 'Failed') return statePrefix(0, st);
  var pre = st === 'Waiting' ? '<p class="note">The onset is found when the steps run.</p>' : '';
  if (st === 'Cannot test yet') pre = '<p class="notice">This case has no source yet, so there is nothing to read. Add data first. <button class="btn sm" onclick="inert(\'Opens the case\\\'s Add data flow\')">Add data</button></p>';
  return pre + statePrefix(0, st) + frameForm();
}
window.stepNotes0 = function(){
  return dt('Data shown', 'The frame itself: <code>frame.incidentStart/End</code>, <code>referenceStart/End</code>, <code>route</code>, <code>poolLimit</code>, <code>releases[]</code>, <code>releasesEntered</code>, <code>myOnset</code>; after a run <code>facts.onset.t0</code>, <code>facts.counts.used</code> for the scope line.')
    + dt('Chart', 'None. Form only (Step0Frame.svelte).')
    + dt('Empty', 'Empty windows: the onset box reads "The onset is found when the steps run." and the change list reads "No change listed".')
    + dt('Cannot test yet', 'No source in the case: the no-sources line and an Add data button replace the onset result.')
    + dt('Failed', 'The run failed: the alert box "The steps could not run." with Retry reading.')
    + dt('Out of date', 'The form always edits the live windows; editing them is what marks steps 1 to 10 out of date.')
    + dt('Repeat pattern', 'Optional. Compare against the same hours on earlier days, the same weekday on earlier weeks, or the last Thursday of each month. The heatmap shows which hours the rule matches; the overlay lines up each occurrence at 0. Times stay in UTC. This preview does not change the windows used to run the analysis.')
    + dt('Guidance', 'Under the windows: a banner when the reference window overlaps, touches or is much longer than the incident (Keep anyway dismisses it), a daylight-saving notice, and an estimate line of rows and time. None of them blocks Run. A change inside the incident window gets its own line. All of it is computed in <code>30-frame-helpers.js</code>.')
    + dt('Buttons', 'Quick picks fill the four dates (live here). Add a change adds a row (live), Remove deletes it (live). Use suggested / Use my time / Widen the incident window / Carry on without an onset appear only when no onset is found (switch me to see: they are in the real component).')
    + stateSeg(0);
};

// ----- Step 1: Can I trust the data? -----
function step1(st){
  if (st === 'Waiting' || st === 'Failed') return statePrefix(1, st);
  var stop = (st === 'Cannot test yet') ? '<div class="stop"><strong>Not enough data to continue</strong><p>One of the windows holds no call. Possible reasons are listed below.</p>'
    + '<ul><li>The window is outside the time the source covers.</li><li>The source is missing, or it stopped before the window.</li><li>The window or the API you chose has no calls.</li></ul>'
    + '<div class="row"><button class="btn sm" onclick="go(0)">Change the window</button><button class="btn sm" onclick="inert(\'Opens the case\\\'s Add data flow\')">Add data</button></div></div>' : '';
  if (st === 'Cannot test yet') return stop;
  return statePrefix(1, st)
  + nutshell('Both windows hold calls. One 13-minute stretch (09:31 to 09:44) has no data and is left out. The clocks agree.')
  + '<p class="lead" style="color:var(--text)">This step checks that the data is good enough to draw conclusions from: that both windows hold enough calls, that no stretch of time is missing, and that the clocks of the sources agree. If something here is wrong, read the later steps with care.</p>'
  + '<div class="cards">'
  + '<div class="card"><span class="note">Calls in the incident window</span><span class="fig">36,910</span><span class="note">Calls that started in the window.</span></div>'
  + '<div class="card"><span class="note">Calls in the reference window</span><span class="fig">36,738</span><span class="note">The normal period.</span></div>'
  + '<div class="card"><span class="note">Time gaps</span><span class="fig">1</span><span class="note">1 long gap: those minutes are left out.</span></div>'
  + '<div class="card"><span class="note">Clock difference</span><span class="fig">Looks fine</span><span class="note">7,204 calls checked.</span></div></div>'
  + '<div class="tscroll"><table><caption>Sources</caption><thead><tr><th>Source</th><th>State</th></tr></thead><tbody>'
  + '<tr><td>Audit</td><td>Ready</td></tr><tr><td>IIS</td><td>Ready</td></tr><tr><td>SQL Server</td><td>Ready</td></tr>'
  + '<tr><td>MongoDB</td><td>Cannot test yet</td></tr><tr><td>Host</td><td>Cannot test yet</td></tr></tbody></table></div>'
  + '<h4>Gaps in the data</h4><ul style="margin:0;padding-left:18px"><li>26 Oct 09:31 to 09:44 UTC (13 minutes, incident window): no calls were recorded. These minutes are left out of every comparison, so a change inside them cannot be seen.</li></ul>'
  + '<p class="note">This run read 74,120 audit calls and 58,204 IIS rows.</p>'
  + '<p class="note">Scope counts 73,648 of the 74,120 calls read. Left out: 312 not completed, 98 with no start time, 62 with a clock anomaly.</p>'
  + reasonsBox(['The minutes 09:31 to 09:44 have no recorded calls, so they are left out of the comparison.', 'MongoDB and Host are missing: their checks are Cannot test yet.'])
  ;
}
window.stepNotes1 = function(){
  return dt('Data shown', '<code>facts.counts.incident/reference</code>, <code>facts.gaps[]</code>, <code>facts.clock.checked/anomalous</code>, <code>confidence.sourcesUsed/sourcesMissing</code> (the Sources table), <code>answer.read.requests/iisRows</code>.')
    + dt('Chart', 'None. Four trust cards, a Sources table and a list of gaps.')
    + dt('Empty', 'A window with no calls swaps the whole step for the "Not enough data to continue" stop box (try the state switch).')
    + dt('Cannot test yet', 'The stop box above, or per-source rows reading Cannot test yet.')
    + dt('Failed', 'Alert "The steps could not run." with Retry reading; in the app a failed audit read names its reason and next action.')
    + dt('Out of date', 'Same content plus the out-of-date notice; the sticker reads "Out of date, rerun".')
    + dt('Buttons', 'Change the window goes to Step 0 (live). Add data opens the case&rsquo;s Add data flow (inert).') + stateSeg(1);
};

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
  var rows = limit ? ROUTES.slice(0, 8) : ROUTES;
  return '<div class="tscroll"><table><caption>By API. 3 of 12 moved.</caption><thead><tr><th>API</th><th>Calls</th><th>P95</th><th>Reference P95</th><th>Change</th><th></th></tr></thead><tbody>'
    + rows.map(function(r){
      return '<tr><td>' + r[0] + '</td><td>' + fmtCount(r[1]) + '</td><td' + (r[6] === 'bad' ? ' class="slow"' : '') + '>' + fmtMs(r[2]) + '</td><td>' + fmtMs(r[3]) + '</td>'
        + '<td><span class="mark ' + r[6] + '"><span aria-hidden="true">' + (r[6] === 'bad' ? '▲' : '●') + '</span> ' + r[7] + '</span> ' + fmtPct(r[4]) + ' (' + signed(fmtCount(r[5])) + ' ms)</td>'
        + '<td><button class="quiet" onclick="nearbyForApi(\'' + esc(r[0]).replace(/'/g, "\\'") + '\')">Nearby activity</button></td></tr>';
    }).join('') + '</tbody></table></div>';
}
function step2(st){
  if (st === 'Waiting' || st === 'Failed') return statePrefix(2, st);
  if (st === 'Cannot test yet') return cannotBody(2, ['The incident or reference window held no calls to compare.']);
  return statePrefix(2, st)
  + nutshell('Three APIs moved. Orders.Create rose the most: its slow end went from 420 ms to 2,100 ms. The shape is a step change at 09:14.')
  + '<p class="lead" style="color:var(--text)">Which APIs moved, and by how much? A change counts only when it passes both marks.</p>'
  + byApiTable(!W.showAllApis)
  + '<div><button class="quiet" onclick="W.showAllApis=!W.showAllApis;render()">' + (W.showAllApis ? 'Show fewer' : 'Show every API') + '</button></div>'
  + '<p class="note">The rule: a change counts when it is at least 30 % and at least 100 ms.</p>'
  + '<p class="note">Calls sent again by the same client within seconds: 41 in the incident, 12 in the reference.</p>'
  + '<p class="note">Shape of the slowdown: Step change.</p>'
  + minuteLine('p95', 'P95 by minute (ms)', 'ms')
  + reasonsBox(['The minutes 09:31 to 09:44 have no data: the line breaks there.']);
}
window.stepNotes2 = function(){
  return dt('Data shown', '<code>facts.routes[]</code> (route, incident.n, incident.p95, reference.p95, p95Change), <code>facts.retries</code>, <code>facts.shape.kind</code>, <code>thresholds.changeMinPercent/changeMinMs</code>.')
    + dt('Chart', '<code>MinuteLine</code> of <code>series[].p95</code>: one point per minute, ms on the y axis (no gridlines, scaled to the peak), minutes 08:00&ndash;09:59 UTC on x. Normal = blue, incident = red; a minute with no data breaks the line (see 09:31&ndash;09:44). Caption gives the range and the peak.')
    + dt('Empty', 'No audit records in the window: "This window holds no audit records, so there is nothing to list. Cannot test yet." No chart is drawn.')
    + dt('Cannot test yet', 'Same as empty: the step cannot compare windows with no calls.')
    + dt('Failed', 'Alert "The steps could not run." with Retry reading.')
    + dt('Out of date', 'Same content plus the out-of-date notice; numbers are from the earlier run.')
    + dt('Buttons', 'Show every API / Show fewer expands the table past 8 rows (live).') + stateSeg(2);
};

// ----- Step 3: Time split (holds Slowest calls + Nearby activity) -----
function partsTable(){
  var parts = selParts();
  return '<div class="tscroll"><table><thead><tr><th>Part</th><th>Normal</th><th>Incident</th><th>Change</th><th>Share of the extra time</th></tr></thead><tbody>'
    + parts.map(function(p){
      var d = p.inc - p.normal;
      return '<tr><td>' + p.label + '</td><td>' + fmtMs(p.normal) + '</td><td' + (p.k === 'O' ? ' class="slow"' : '') + '>' + fmtMs(p.inc) + '</td><td>' + fmtPct(d / p.normal * 100) + ' (' + signed(fmtMs(d)) + ')</td><td>' + Math.round(p.share * 100) + ' %</td></tr>';
    }).join('') + '</tbody></table></div>';
}
function partsBars(){
  var parts = selParts();
  var top = parts.reduce(function(t, p){ return t + p.inc; }, 0); // incident sum is the longer bar: full width
  var row = function(name, field){
    return '<div class="bar-row"><span class="bar-name">' + name + '</span><span class="bar-track">'
      + parts.map(function(p){ return '<span class="pseg part-' + p.k + '" style="width:' + (p[field] / top * 100).toFixed(1) + '%" title="' + p.label + ': ' + p[field] + ' ms"></span>'; }).join('') + '</span></div>';
  };
  return '<div class="bars" role="img" aria-label="Bars of the average time of a call, on one scale.">' + row('Normal', 'normal') + row('Incident', 'inc') + '</div>'
    + '<p class="note">Bars of the average time of a call, on one scale. <span class="pseg part-A" style="display:inline-block;width:10px;height:10px"></span> A audit set-up <span class="pseg part-C" style="display:inline-block;width:10px;height:10px"></span> C action time <span class="pseg part-P" style="display:inline-block;width:10px;height:10px"></span> P payload record <span class="pseg part-O" style="display:inline-block;width:10px;height:10px"></span> O outside time</p>';
}
function slowCallsTable(withNearby, names){
  var apiNames = (names instanceof Array) ? names : selRoutes().map(function(r){ return r[0]; });
  var rows = CALLS.map(function(c, i){ return [c, i]; }).filter(function(p){ return apiNames.indexOf(p[0][0]) !== -1; });
  return '<div><h4 style="font-size:14px">Slowest calls in the incident window</h4>'
  + '<p class="note">Pick a call to see what ran around it, right below.</p>'
  + (W.slowShown ? '' : '<div><button class="btn sm" onclick="W.slowShown=true;render()">Show the slowest calls</button></div>')
  + (!W.slowShown ? '' : (!rows.length ? '<p class="note">No slow call in this selection.</p>' : '<div class="tscroll"><table><thead><tr><th>API</th><th>Started (UTC)</th><th>Time</th>' + (withNearby ? '<th></th>' : '') + '</tr></thead><tbody>'
    + slowPageRows(rows).map(function(p){
      var c = p[0], i = p[1];
      return '<tr class="' + (withNearby && W.nearbyOpen && i === W.nearbyRow ? 'sel' : '') + '"' + (withNearby && W.nearbyOpen && i === W.nearbyRow ? ' aria-selected="true"' : '') + '><td>' + c[0] + '</td><td>' + c[1] + '</td><td class="slow">' + fmtMs(c[2]) + '</td>'
        + (withNearby ? '<td><button class="btn sm" onclick="W.nearbyRow=' + i + ';W.nearbyOpen=true;W.nearbyPicked=true;W.nbAround=\'2026-10-26T' + c[1].slice(0, 5) + '\';render();if(W.mode===\'manual\')openTool(\'nearby-activity\');">Nearby activity</button></td>' : '');
    }).join('') + '</tbody></table></div>' + slowPager(rows.length) + '<p class="note">The longest calls that started in the incident window.</p>'));
}
function nearbyPanel(noClose){
  var c = CALLS[W.nearbyRow == null ? 0 : W.nearbyRow];
  var bars = [['09:12:58', 'Basket.Get', 205], ['09:13:44', 'Orders.Get', 388], ['09:14:02', 'Orders.Create', 4812], ['09:14:07', 'Orders.Create', 4350],
    ['09:15:31', 'Payments.Authorize', 1240], ['09:17:12', 'Catalog.Search', 312], ['09:18:58', 'Orders.Create', 3611]];
  var longest = 4812;
  var band = function(ms){ return ms > 2000 ? 'very-slow' : ms >= 1000 ? 'slow' : 'fast'; };
  var lane = function(title, inner){ return '<section class="lane"><h4>' + title + '</h4>' + inner + '</section>'; };
  var apiLane = '<ul class="calls">' + bars.map(function(b){
    return '<li><span>' + b[0] + '</span><span>' + b[1] + '</span><span class="bar" role="img" aria-label="' + fmtMs(b[2]) + '"><span class="fill band-' + band(b[2]) + '" style="width:' + Math.max(2, Math.round(b[2] / longest * 100)) + '%"></span></span><span class="ms">' + fmtMs(b[2]) + '</span></li>';
  }).join('') + '</ul><p class="note">Bar colours: red striped over 2 seconds, orange 1 to 2 seconds, green under 1 second. These are not the suspect colours.</p>';
  var sqlLane = '<ul class="calls"><li><span>09:14:01</span><span>Lock wait on Orders (session 88)</span><span class="bar"><span class="fill band-very-slow" style="width:100%"></span></span><span class="ms">9,020 ms</span></li>'
    + '<li><span>09:16:40</span><span>Lock wait on OrderLines (session 91)</span><span class="bar"><span class="fill band-slow" style="width:18%"></span></span><span class="ms">1,640 ms</span></li></ul>';
  var none = '<p class="note">Cannot test yet: this case has no source of this kind.</p>';
  return '<section class="card nearby" aria-label="Nearby activity"><div class="sthead"><h4>Nearby activity &middot; ' + c[0] + ' at ' + c[1] + '</h4><span class="sp"></span>' + (noClose ? '' : '<button class="btn sm" onclick="W.nearbyOpen=false;render()">Close</button>') + '</div>'
  + '<p class="lead" style="color:var(--text)">What each source recorded around one moment. It shows what overlapped in time. It does not say why.</p>'
  + '<div class="pair"><label class="wf">Around (UTC)<input class="wf" type="datetime-local" value="' + W.nbAround + '" onchange="W.nbAround=this.value;W.nbTouched=true"></label>'
  + '<label class="wf">Minutes before<input class="wf" type="number" min="0" max="120" value="' + W.nbBefore + '" onchange="W.nbBefore=this.value;W.nbTouched=true"></label>'
  + '<label class="wf">Minutes after<input class="wf" type="number" min="0" max="120" value="' + W.nbAfter + '" onchange="W.nbAfter=this.value;W.nbTouched=true"></label></div>'
  + '<div class="picks" role="group" aria-label="Quick windows"><span class="note">Quick windows</span>'
  + '<button class="btn sm" onclick="W.nbBefore=2;W.nbAfter=2;render()">2 min</button><button class="btn sm" onclick="W.nbBefore=5;W.nbAfter=5;render()">5 min</button><button class="btn sm" onclick="W.nbBefore=15;W.nbAfter=15;render()">15 min</button></div>'
  + '<div><button class="btn primary sm" onclick="render()">Show activity</button></div>'
  + lane('API calls', apiLane) + lane('SQL Server', sqlLane) + lane('MongoDB', none) + lane('Host', none) + '</section>';
}
function selRoutes(){
  var mode = W.apiMode || 'all';
  if (mode === 'one') {
    var one = W.apiOne || ROUTES[0][0];
    return ROUTES.filter(function(r){ return r[0] === one; });
  }
  if (mode === 'several') {
    var set = W.apiSet || [];
    if (!set.length) return ROUTES;
    return ROUTES.filter(function(r){ return set.indexOf(r[0]) !== -1; });
  }
  return ROUTES;
}
function selParts(){
  var sel = selRoutes();
  if (sel.length === ROUTES.length) return PARTS;
  var mean = function(rows, idx){ return rows.reduce(function(t, r){ return t + r[idx]; }, 0) / rows.length; };
  var fi = mean(sel, 2) / mean(ROUTES, 2);
  var fn = mean(sel, 3) / mean(ROUTES, 3);
  var parts = PARTS.map(function(p){
    return { k:p.k, label:p.label, normal:Math.max(1, Math.round(p.normal * fn)), inc:Math.max(1, Math.round(p.inc * fi)), share:0, tested:p.tested };
  });
  var total = parts.reduce(function(t, p){ return t + Math.max(0, p.inc - p.normal); }, 0);
  parts.forEach(function(p){ p.share = total ? Math.max(0, p.inc - p.normal) / total : 0; });
  return parts;
}
function toggleApi(name){
  var set = W.apiSet || (W.apiSet = []);
  var i = set.indexOf(name);
  if (i === -1) set.push(name); else set.splice(i, 1);
  render();
}
function apiPicker(){
  var mode = W.apiMode || 'all';
  var btn = function(val, label){
    return '<button class="btn sm' + (mode === val ? ' primary' : '') + '" onclick="W.apiMode=\'' + val + '\';render()">' + label + '</button>';
  };
  var out = '<div class="picks" role="group" aria-label="APIs included"><span class="note">APIs included</span>'
    + btn('all', 'All APIs') + btn('one', 'One API') + btn('several', 'Several') + '</div>';
  if (mode === 'one') {
    var one = W.apiOne || ROUTES[0][0];
    out += '<label class="wf">API<select class="wf" onchange="W.apiOne=this.value;render()">'
      + ROUTES.map(function(r){ return '<option value="' + r[0] + '"' + (r[0] === one ? ' selected' : '') + '>' + r[0] + '</option>'; }).join('')
      + '</select></label>';
  }
  if (mode === 'several') {
    var set = W.apiSet || [];
    out += ROUTES.map(function(r){
      return '<label class="wf"><input type="checkbox" ' + (set.indexOf(r[0]) !== -1 ? 'checked' : '') + ' onchange="toggleApi(\'' + r[0] + '\')"> ' + r[0] + '</label>';
    }).join('');
  }
  out += '<div><button class="quiet" onclick="W.callsApi=(W.apiMode||\'all\')===\'one\'?(W.apiOne||ROUTES[0][0]):\'\';W.slowPg=0;render()">Use this filter in the Slowest calls tool too</button></div>'
    + '<p class="note">Each place keeps its own filter. This button copies this one to the Slowest calls tool.</p>';
  var who = 'All 12 APIs';
  if (mode === 'one') who = W.apiOne || ROUTES[0][0];
  if (mode === 'several') { var set2 = W.apiSet || []; who = set2.length ? set2.join(', ') : 'All 12 APIs'; }
  return out + '<p class="note">These figures are the average of the calls of: ' + who + '.</p>';
}
function step3(st){
  if (st === 'Waiting' || st === 'Failed') return statePrefix(3, st);
  if (st === 'Cannot test yet') return cannotBody(3, ['The window held no calls with an IIS row, so no part of the split could be tested.'], 'Add the IIS logs');
  var parts = selParts();
  var top = parts.reduce(function(a, b){ return b.share > a.share ? b : a; });
  var nut = top.share === 0 ? 'This selection did not slow down.'
    : (top.label + ' holds ' + Math.round(top.share * 100) + ' % of the extra time: ' + (top.k === 'O' ? 'the wait is before or beside the app code, not in the audit or the payload write.' : 'most of the extra time is inside the app.'));
  var note = top.share === 0 ? '' : '<p class="note">' + top.label + ' grew the most: it holds ' + Math.round(top.share * 100) + ' % of the extra time.</p>';
  return statePrefix(3, st)
  + nutshell(nut)
  + '<p class="lead" style="color:var(--text)">Where did the time of a call go? Each call is split into audit set-up, action time, payload record and outside time. The figures are the average of the APIs you select below.</p>'
  + apiPicker()
  + partsTable() + partsBars()
  + note
  + slowCallsTable(true)
  + (W.slowShown && W.nearbyOpen ? nearbyPanel() : '')
  + reasonsBox(['The minutes 09:31 to 09:44 could not be tested (long gap in the audit).']);
}
window.stepNotes3 = function(){
  return dt('Data shown', '<code>facts.parts.items[]</code> (incMeanMs, refMeanMs, change, share, tested), <code>facts.parts.dominantWhy</code>; slow calls from the audit read (route, initiatedAt, durationMs); the nearby panel reads every source around <code>around &plusmn; before/after</code>.')
    + dt('Chart', 'Two stacked horizontal bars (Normal, Incident) of the average time of a call on one scale; segments A/C/P/O use the app&rsquo;s series colours. Nearby activity: duration bars per call, green under 1 s, orange 1&ndash;2 s, red striped over 2 s, width relative to the longest call shown.')
    + dt('Empty', 'Slowest calls before the button: only "Show the slowest calls" (toggle with the switch: hide via Cannot test yet). An empty window reads "This window holds no audit records&hellip;". Nearby with no rows: "Nothing was recorded in this window."')
    + dt('Cannot test yet', 'Whole step: the reason list. Per part: "Not drawn: parts that could not be tested." Missing lanes read "Cannot test yet: this case has no source of this kind."')
    + dt('Failed', 'Step: alert "The steps could not run." Nearby read: "The activity could not be read." Slowest calls: "The tool could not run."')
    + dt('Out of date', 'Same content plus the out-of-date notice.')
    + dt('API picker', 'Chooses which APIs the time split averages: All APIs (default), One API (a drop-down) or Several (check boxes). The table, the bars, the nutshell and the slowest calls follow the choice (live). Wireframe figures for a single API are scaled from the all-API split.')
    + dt('Buttons', 'Nearby activity on a row opens the panel under the table and marks the row (live); Close removes it (live). Quick windows 2/5/15 min set before/after (live). Add the IIS logs (inert).') + stateSeg(3);
};

// ----- Step 4: Load -----
function step4(st){
  if (st === 'Waiting' || st === 'Failed') return statePrefix(4, st);
  if (st === 'Cannot test yet') return cannotBody(4, ['The window held too few calls to count calls in flight.']);
  return statePrefix(4, st)
  + nutshell('Calls did not arrive faster (10.2 against 10.4 per second). Each call stayed longer, so 21 calls were open at once instead of 4.')
  + '<p class="lead" style="color:var(--text)">How many calls were open at once? More calls in flight can mean the system was busier, or that each call stayed longer.</p>'
  + '<div class="tscroll"><table><thead><tr><th>Measure</th><th>Normal</th><th>Incident</th><th>Change</th></tr></thead><tbody>'
  + LOADROWS.map(function(r){ return '<tr><td>' + r[0] + '</td><td>' + r[1] + '</td><td' + (r[3] === 'Higher' ? ' class="slow"' : '') + '>' + r[2] + '</td><td>' + r[3] + '</td></tr>'; }).join('') + '</tbody></table></div>'
  + '<p class="finding">The system filled because calls took longer, not because more calls arrived.</p>'
  + (W.frame.poolLimit && W.frame.poolLimit.trim() !== '' ? '<p class="note">Calls in flight peaked at 34 of the ' + W.frame.poolLimit.trim() + ' entered as the pool limit. The pool was not filled.</p>' : '<p class="note">Calls in flight peaked at 34. No pool limit was entered in Step 0, so there is nothing to compare it against.</p>')
  + minuteLine('inFlight', 'Calls in flight by minute', 'calls')
  + (typeof nearView === 'function' ? nearView() : '')
  + reasonsBox(['The minutes 09:31 to 09:44 have no data: the line breaks there.']);
}
window.stepNotes4 = function(){
  return dt('Data shown', '<code>facts.load</code> (refPerSec/incPerSec, refWms/incWms, refL/incL, refPeak/incPeak, reading), <code>signals.pool</code> (limit, plannedFill, reading).')
    + dt('Chart', '<code>MinuteLine</code> of <code>series[].inFlight</code>: average calls in flight per minute (unit "calls"), same axes and colours as the P95 chart; caption reads "Highest minute average: n calls".')
    + dt('Empty', 'No records: the table cells read "No data" and no chart is drawn.')
    + dt('Cannot test yet', 'Too few calls: the reason list replaces the table and chart.')
    + dt('Failed', 'Alert "The steps could not run." with Retry reading.')
    + dt('Out of date', 'Same content plus the out-of-date notice.')
    + dt('Buttons', 'None of its own. The pool figure appears because Step 0 holds the pool limit 100.') + stateSeg(4);
};
