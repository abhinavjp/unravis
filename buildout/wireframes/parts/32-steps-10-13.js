// ---------- steps 10 to 13 ----------
// Step 10 body is shared with the suspect-summary tool.
function originContent(){
  var ranked = SUSPECTS.filter(function(s){ return s.colour === 'red' || s.colour === 'orange'; });
  var grey = SUSPECTS.filter(function(s){ return s.colour === 'grey'; });
  var green = SUSPECTS.filter(function(s){ return s.colour === 'green'; });
  return '<p class="lead" style="color:var(--text)">What moved first, and which suspects fit the data. Unravis does not name a culprit. It says what fits and what could not be tested.</p>'
  + '<section><h4 style="font-size:14px">What moved first</h4>'
  + '<p class="finding">The slow end moved first on SQL Server at 09:14 UTC.</p>'
  + '<div class="tscroll"><table><thead><tr><th>Tier</th><th>First moved (UTC)</th><th>What moved</th><th>Role</th></tr></thead><tbody>'
  + ORIGINROWS.map(function(r){ return '<tr><td>' + r[0] + '</td><td>' + r[1] + '</td><td>' + r[2] + '</td><td>' + r[3] + '</td></tr>'; }).join('') + '</tbody></table></div>'
  + '<p class="note">The app tier followed within the same minute, so the order reads from the waits, not from the slow calls.</p>'
  + '<p class="note">Clock difference between the sources was under 2 s for the checked calls.</p></section>'
  + '<section><h4 style="font-size:14px">How to read the colours</h4><ul class="legend-list">'
  + '<li><span class="dot" style="background:var(--suspect-red)"></span>Red: most likely suspect</li>'
  + '<li><span class="dot" style="background:var(--suspect-orange)"></span>Orange: likely suspect</li>'
  + '<li><span class="dot" style="background:var(--suspect-green)"></span>Green: ruled out by the data we have</li>'
  + '<li><span class="dot" style="background:var(--suspect-grey)"></span>Grey: not sure. It is not a pass.</li></ul></section>'
  + '<section><h4 style="font-size:14px">Suspects, most likely first</h4>' + ranked.map(function(s){ return suspectCard(s, false); }).join('') + '</section>'
  + '<section><h4 style="font-size:14px">Could not be tested (grey, never a pass)</h4>' + grey.map(function(s){ return suspectCard(s, true); }).join('') + '</section>'
  + '<section><h4 style="font-size:14px">Set aside: ' + green.length + ' green suspects</h4>'
  + '<div><button class="quiet" aria-expanded="' + W.showSetAside + '" onclick="W.showSetAside=!W.showSetAside;render()">' + (W.showSetAside ? 'Show fewer suspects' : 'Show the suspects set aside') + '</button></div>'
  + (W.showSetAside ? green.map(function(s){ return suspectCard(s, true); }).join('') : '') + '</section>'
  + '<section><h4 style="font-size:14px">Cheapest check</h4><p class="finding">D1: Read the blocking chain around 09:14 in the SQL source and name the head blocker.</p></section>'
  + '<section><h4 style="font-size:14px">How sure are we</h4>'
  + '<p class="finding"><strong>Confidence: ' + SURE.word + '.</strong> ' + SURE.why + '</p>'
  + '<div class="tscroll"><table><thead><tr><th>Item</th><th>State</th></tr></thead><tbody>'
  + '<tr><td>Sources used</td><td>' + SURE.used + '</td></tr><tr><td>Sources missing</td><td>' + SURE.missing + '</td></tr><tr><td>Suspects not tested</td><td>' + SURE.notTested + '</td></tr></tbody></table></div>'
  + '<p class="finding"><strong>The one check that would overturn the answer:</strong> ' + SURE.overturn + '</p></section>'
  + '<div class="row"><button class="btn sm" onclick="W.mode=\'guided\';go(12)">Go to step 12</button></div>';
}
function step10(st){
  if (st === 'Waiting' || st === 'Failed') return statePrefix(10, st);
  if (st === 'Cannot test yet') return cannotBody(10, ['Not enough data to date any tier. Add the sources listed under How sure are we.']);
  return statePrefix(10, st)
  + nutshell('SQL Server moved first, at 09:14. One suspect fits all the data; two could not be tested.')
  + originContent()
  + (typeof step10Evidence === 'function' ? step10Evidence() : '')
  + reasonsBox(['MongoDB and Host suspects could not be tested: their sources are missing.']);
}
window.stepNotes10 = function(){
  return dt('Data shown', '<code>origin.roles[]</code> (tier, at, why, role), <code>suspects[]</code> grouped red+orange / grey / green (<code>suspectGroups</code>), <code>confidence</code> (word, why, sourcesUsed, sourcesMissing, notTested, overturn), <code>cheapestCheck</code>.')
    + dt('Chart', 'None. Origin table (Tier, First moved (UTC), What moved, Role), colour legend, suspect cards, How sure are we table (Item, State).')
    + dt('Empty', 'No red or orange: "No suspect is red or orange. See the grey ones: they could not be tested." Not enough data: "Not enough data to date any tier&hellip;".')
    + dt('Cannot test yet', 'The not-enough-data line replaces the origin table (try the switch).')
    + dt('Failed', 'Alert "The steps could not run." with Retry reading.')
    + dt('Out of date', 'Same content plus the out-of-date notice.')
    + dt('Buttons', 'Show the suspects set aside / Show fewer suspects (live). Add its check to the checklist (live; reads "On the checklist" after). Go to step 12 (live).') + stateSeg(10);
};

// ----- Step 11: After the fix (shared with the after-fix tool) -----
function afterFixBody(){
  var f = W.fix;
  var out = '<p class="lead" style="color:var(--text)">Compare the same hours on another day with the incident day.</p>'
    + '<p class="note">Pick hours on a day after the fix. Unravis compares them with the incident day.</p>'
    + '<div class="pair"><label class="wf">The later day (UTC)<input class="wf" type="date" value="' + f.day + '" onchange="W.fix.day=this.value;W.fix.verdictOn=false;W.fix.problem=\'\';render()"></label>'
    + '<label class="wf">Compare one route only (optional)<select class="wf" onchange="W.fix.route=this.value"><option value="">All APIs</option>'
    + ROUTES.map(function(r){ return '<option' + (f.route === r[0] ? ' selected' : '') + '>' + r[0] + '</option>'; }).join('') + '</select></label></div>'
    + '<div><button class="btn ' + (f.verdictOn ? '' : 'primary') + '" onclick="fixCompare()">Compare</button></div>';
  if (f.problem) out += '<p class="err" role="alert">' + f.problem + '</p>';
  if (f.verdictOn) {
    out += '<div><h4 style="font-size:14px">Better, not fixed</h4><p class="finding">The slow end fell by 62 % against the incident day, but at 790 ms it stays above the normal range of 420 ms.</p></div>'
      + '<div class="tscroll"><table><thead><tr><th>Measure</th><th>Earlier day</th><th>Later day</th><th>Change vs the earlier day</th></tr></thead><tbody>'
      + FIXROWS.map(function(r){ return '<tr><td>' + r[0] + '</td><td>' + r[1] + '</td><td>' + r[2] + '</td><td>' + (r[4] ? '<span class="mark ' + r[4] + '">' + (r[4] === 'bad' && (r[3].charAt(0) === '-' || r[3].charAt(0) === '−') ? '▼ Still above normal' : (r[4] === 'bad' ? '▲' : '●') + ' ' + r[5]) + '</span> ' : '') + r[3] + '</td></tr>'; }).join('') + '</tbody></table></div>'
      + '<div class="warn" role="alert"><strong>Warnings</strong><ul><li>The mix of APIs differs by 18 % between the two days.</li></ul></div>'
      + (W.closed ? '<p class="notice" role="status">This case is closed, so the result cannot be recorded. You can still compare.</p>'
        : f.recorded ? '<p class="note" role="status">Recorded under What we learned.</p>'
        : '<div class="row"><button class="btn primary" onclick="fixRecord()">Record with this warning</button><button class="quiet" onclick="W.fix.verdictOn=false;render()">Not now</button></div>');
  }
  return out;
}
function fixCompare(){
  var f = W.fix; f.recorded = false;
  if (!f.day) { f.problem = 'Pick the later day first.'; }
  else if (f.day <= '2026-10-26') { f.problem = 'The later day must come after the incident day. Pick a later date.'; }
  else { f.problem = ''; f.verdictOn = true; }
  render();
}
function fixRecord(){
  W.checklist.push({ id:'cfx' + W.views.length, list:'learned', text:'After the fix, 28 Oct 09:00 to 10:00 UTC: P95 2,100 ms to 790 ms. Better, not fixed.', suspect:'' });
  W.fix.recorded = true; render();
}
function step11(st){
  if (st === 'Failed') return statePrefix(11, st);
  if (st === 'Waiting') return statePrefix(11, '') + '<p class="notice">Run the steps first. The comparison needs the incident day.</p>' + afterFixDisabled();
  if (st === 'Cannot test yet') return cannotBody(11, ['The steps have not run, so the incident day is unknown.']);
  return statePrefix(11, st)
  + nutshell('The fix brought the slow end down 62 %. It is better, not fixed: 790 ms is still above the normal 420 ms.')
  + afterFixBody();
}
function afterFixDisabled(){
  return '<p class="note">The later day, the route picker and Compare stay disabled until a run exists.</p>';
}
window.stepNotes11 = function(){
  return dt('Data shown', 'A second engine run for the later day: <code>facts.change.p95/p50/mean</code> and <code>facts.load.incL</code> of both days, classified by <code>classifyDay</code> into a verdict word, sentence, rows and warnings.')
    + dt('Chart', 'None. Comparison table: Measure, Earlier day, Later day, Change vs the earlier day, with the ▲/▼/● marks.')
    + dt('Empty', 'Before the steps run: "Run the steps first. The comparison needs the incident day." A day with no calls: verdict "No data for that day", nothing is compared against zero.')
    + dt('Cannot test yet', 'Same as not run.')
    + dt('Failed', '"The later day could not be compared."')
    + dt('Out of date', 'A new main run makes an old verdict stale: it leaves the page (not shown as stale).')
    + dt('Buttons', 'Compare validates the day (live: try a day before 27 Oct) and shows the verdict. Record with this warning writes one line under What we learned (live). Not now dismisses. On this step the run bar&rsquo;s button is secondary: only one primary button per screen.') + stateSeg(11);
};

// ----- Step 12: Checklist -----
function step12(st){
  if (st === 'Failed') return statePrefix(12, st);
  var groups = {};
  LISTS.forEach(function(l){ groups[l[0]] = W.checklist.filter(function(i){ return i.list === l[0]; }); });
  var done = groups.learned.length + groups.ruled_out.length, open = groups.todo.length + groups.doing.length;
  var head = '<div class="sthead"><h4 style="font-size:16px">Checklist</h4><span class="sp"></span><span class="sticker ready" aria-live="polite">Done ' + done + ' &middot; Open ' + open + '</span></div>'
    + '<p class="lead" style="color:var(--text)">Keep four lists: Need to do, Working on, What we learned and Ruled out.</p>'
    + (W.closed ? '<p class="notice" role="status">This case is closed. You can read the checklist but not change it.</p>' : '');
  var actions = W.closed ? '' : '<div class="row"><button class="btn sm" aria-expanded="' + W.ckAdd + '" onclick="W.ckAdd=!W.ckAdd;render()">+ Add item</button>'
    + '<button class="btn sm" onclick="addSuggested()">Add suggested checks from Step 10</button></div>'
    + (W.ckNote ? '<p class="note" role="status">' + W.ckNote + '</p>' : '')
    + (W.ckAdd ? '<div class="card"><label class="wf">What is the item?<input class="wf" type="text" id="cktext" value="' + esc(W.ckText) + '" onchange="W.ckText=this.value"></label>'
      + '<label class="wf">Move to<select class="wf" onchange="W.ckList=this.value">' + LISTS.map(function(l){ return '<option value="' + l[0] + '"' + (W.ckList === l[0] ? ' selected' : '') + '>' + l[1] + '</option>'; }).join('') + '</select></label>'
      + '<div class="row"><button class="btn sm" onclick="ckAddItem()">Add</button><button class="quiet" onclick="W.ckAdd=false;render()">Cancel</button></div></div>' : '');
  var lists = '<div class="cklists">' + LISTS.map(function(l){
    var items = groups[l[0]];
    return '<section class="cklist"><h4 style="font-size:14px">' + l[1] + ' <span class="note">(' + items.length + ')</span></h4>'
      + (items.length === 0 ? '<p class="note">Nothing here yet.</p>'
        : '<ul>' + items.map(function(it){
          return '<li><span>' + esc(it.text) + '</span>' + (it.suspect ? '<span class="cktag">Suspect ' + it.suspect + '</span>' : '')
            + (W.closed ? '' : '<span class="row"><select class="wf" style="width:auto" aria-label="Move this item to another list" onchange="ckMove(\'' + it.id + '\',this.value)">' + LISTS.map(function(t){ return '<option value="' + t[0] + '"' + (it.list === t[0] ? ' selected' : '') + '>' + t[1] + '</option>'; }).join('') + '</select>'
            + '<button class="quiet" onclick="W.checklist=W.checklist.filter(function(x){return x.id!==\'' + it.id + '\'});render()">Delete</button></span>') + '</li>';
        }).join('') + '</ul>') + '</section>';
  }).join('') + '</div>';
  return (st === 'Out of date' ? statePrefix(12, st) : '') + head + actions + lists;
}
function ckAddItem(){
  var el = document.getElementById('cktext');
  var t = (el ? el.value : W.ckText).trim();
  if (!t) return;
  W.checklist.push({ id:'c' + Date.now() % 100000, list:W.ckList, text:t.slice(0, 300), suspect:'' });
  W.ckText = ''; W.ckAdd = false; render();
}
function ckMove(id, list){ W.checklist.forEach(function(i){ if (i.id === id) i.list = list; }); render(); }
function addSuggested(){
  var ranked = SUSPECTS.filter(function(s){ return (s.colour === 'red' || s.colour === 'orange') && s.nextCheck; });
  var have = W.checklist.map(function(i){ return i.text.trim().toLowerCase(); });
  var add = ranked.filter(function(s){ return have.indexOf((s.id + ': ' + s.nextCheck).toLowerCase()) < 0; });
  if (!add.length) { W.ckNote = 'Step 10 has no check that is not on the list already.'; }
  else { add.forEach(function(s){ W.checklist.push({ id:'cs' + s.id, list:'todo', text:(s.id + ': ' + s.nextCheck).slice(0, 300), suspect:s.id }); }); W.ckNote = add.length === 1 ? 'Added 1 check.' : 'Added ' + add.length + ' checks.'; }
  render();
}
window.stepNotes12 = function(){
  return dt('Data shown', '<code>ChecklistItem[]</code> (list, text, suspect), counters from <code>counters()</code>: Done = learned + ruled out, Open = todo + doing.')
    + dt('Chart', 'None. Four list boxes with counters, an add form, and the suggested-checks action.')
    + dt('Empty', 'A list with no items reads "Nothing here yet."')
    + dt('Cannot test yet', 'Not a state of this step; the checklist is always usable.')
    + dt('Failed', 'A failed save reads as an inline error above the lists (e.g. "That item could not be saved. Check the text and try again.").')
    + dt('Out of date', 'Not a state of this step.')
    + dt('Buttons', '+ Add item opens the form (live; Add disabled for empty text or over 300 characters). Add suggested checks from Step 10 adds the red and orange suspects&rsquo; checks not already listed (live). Per item: Move to select and Delete (live). Closed case: everything read-only (toggle in the Manual tools notes strip).') + stateSeg(12);
};

// ----- Step 13: Export -----
function step13(st){
  if (st === 'Failed') return statePrefix(13, st);
  if (st === 'Waiting') return '<p class="notice" role="status">Nothing has been analysed yet. The report will say so and hold only the checklist, if you include it.</p>' + exportBody(true);
  if (st === 'Cannot test yet') return cannotBody(13, ['The steps have not run, so there is nothing to put in a report.']);
  return statePrefix(13, st)
  + nutshell('A report of this analysis, built on this computer. The stale step makes it carry one warning.')
  + exportBody(false);
}
function exportBody(empty){
  var e = W.exp;
  var stale = Object.keys(W.stepState).some(function(k){ return W.stepState[k] === 'Out of date'; });
  var opts = [['figures','Figures and charts'],['suspects','Suspects with their cheapest checks'],['checklist','Checklist'],['sources','Sources and what could not be tested']];
  var out = '<p class="lead" style="color:var(--text)">Build a report from what is on screen. The report is made on this computer and held in this window until you save it.</p>'
    + (empty0() ? '' : '')
    + '<fieldset class="wf"><legend>Include</legend>'
    + opts.map(function(o){ return '<label class="check"><input type="checkbox" ' + (e[o[0]] ? 'checked' : '') + ' onchange="W.exp.' + o[0] + '=this.checked;render()"> ' + o[1] + '</label>'; }).join('')
    + '<label class="check" style="color:var(--text-2)"><input type="checkbox" disabled> Nearby activity for chosen calls</label>'
    + '<p class="note">Pick calls first: this needs calls chosen in Nearby activity.</p></fieldset>'
    + (stale ? '<div class="warn" role="alert"><strong>This report has warnings</strong><ul><li>The windows or lists changed after the last run. The report holds the earlier run, as it is on screen.</li></ul></div>' : '')
    + '<div class="row"><button class="btn primary" onclick="expBuild(\'html\')">' + (stale ? 'Download report (HTML) with these warnings' : 'Download report (HTML)') + '</button>'
    + '<button class="btn" onclick="expBuild(\'csv\')">Download data (CSV)</button>'
    + '<button class="btn" onclick="W.exp.preview=!W.exp.preview;render()">Preview</button></div>'
    + '<p class="note">No request payloads, no user ids, no connection details, no passwords. Dates in UTC.</p>'
    + (e.built ? '<p class="note" role="status">' + e.built + '</p>' : '')
    + (e.preview ? '<div class="pvframe"><p class="wftag">Preview (fake report content)</p><h4 style="font-size:15px">Guided analysis &middot; Case 0042 &middot; Orders slowdown 26 Oct</h4>'
      + '<p>Windows: 26 Oct 09:00&ndash;10:00 UTC against 08:00&ndash;09:00 UTC. Onset 09:14 UTC.</p>'
      + '<p>Shape: three APIs moved; Orders.Create +400 %. Origin: SQL Server moved first at 09:14. Confidence: Medium.</p>'
      + (e.suspects ? '<p>Suspects: D1 (red), C1 and S1 (orange), M1 and H1 (grey, not tested), L1 and Q1 (green, set aside).</p>' : '')
      + (e.checklist ? '<p>Checklist: Done ' + W.checklist.filter(function(i){ return i.list === 'learned' || i.list === 'ruled_out'; }).length + ', Open ' + W.checklist.filter(function(i){ return i.list === 'todo' || i.list === 'doing'; }).length + '.</p>' : '')
      + (e.sources ? '<p>Sources used: Audit, IIS, SQL Server. Missing: MongoDB, Host.</p>' : '')
      + '<div><button class="quiet" onclick="W.exp.preview=false;render()">Close preview</button></div></div>' : '');
  return out;
}
function empty0(){ return false; }
function expBuild(fmt){
  W.exp.built = (fmt === 'html' ? 'report-case-0042.html is ready, 84 KB.' : 'report-case-0042.csv is ready, 12 KB.')
    + ' Saved. SHA-256 of the saved file: 9f2c41aa&hellip; (inert in wireframe: no file is written)';
  render();
}
window.stepNotes13 = function(){
  return dt('Data shown', 'The run on screen plus <code>ReportInclude</code> (figures, suspects, checklist, sources). The report is built server-side; the page shows readiness, warnings and the SHA-256 of the saved file.')
    + dt('Chart', 'None here; the report embeds the charts as SVG.')
    + dt('Empty', 'Nothing analysed: "Nothing has been analysed yet. The report will say so and hold only the checklist, if you include it." (try Waiting).')
    + dt('Cannot test yet', 'Same as empty.')
    + dt('Failed', '"The report could not be built." A saved file whose checksum does not match: "The report did not match its checksum, so it was not saved. Try again."')
    + dt('Out of date', 'A warning box lists "The windows or lists changed after the last run&hellip;" and the HTML button gains "with these warnings".')
    + dt('Buttons', 'Include checkboxes (live; they change the preview). Download buttons show the ready + saved lines (inert: no file). Preview opens and closes the report preview (live).') + stateSeg(13);
};
