// ---------- Manual tools home and shared chrome ----------
var TOOL_BODIES = window.TOOL_BODIES = window.TOOL_BODIES || {};
var TOOL_NOTES = window.TOOL_NOTES = window.TOOL_NOTES || {};
var MAX_VIEWS = 200, MAX_VIEW_NAME = 60, MAX_ITEM_TEXT = 300;

function haveNeed(n){ return !!W.have[n]; }
function liveToolState(tool){
  var soft = function(n){ return n === 'iis' && tool.needs.length > 1; };
  var absent = tool.needs.filter(function(n){ return !soft(n) && !haveNeed(n); });
  if (absent.length) return { word:'Cannot test yet', missing:absent, why:'This tool needs ' + NEED_WORD[absent[0]] + ', and this case has none.' };
  var partly = tool.needs.some(function(n){ return soft(n) && !haveNeed(n); });
  if (partly) return { word:'Partly ready', missing:[], why:'The audit part runs. The outside time needs an IIS log source.' };
  if (!W.ranSteps && !READS_ITSELF[tool.id]) return { word:'Waiting', missing:[], why:'' };
  if (tool.step !== undefined && W.ranSteps && stepState(tool.step) === 'Failed') return { word:'Failed', missing:[], why:'' };
  return { word:'Ready', missing:[], why:'' };
}
function liveToolSticker(st){
  var mark = W.stale && st.word !== 'Cannot test yet' && st.word !== 'Waiting';
  if (mark) return { word:'Out of date, rerun', tone:'stale' };
  return { word:st.word, tone:ST_TONE[st.word] || 'cannot' };
}
function toolById(id){ for (var i = 0; i < TOOLS.length; i++) if (TOOLS[i].id === id) return TOOLS[i]; return null; }
function openTool(id){ W.tool = id; W.noted[id] = false; W.flash = ''; render(); var p = document.getElementById('mpane'); if (p) p.scrollTop = 0; }
function backToTools(){ W.tool = ''; W.flash = ''; render(); var p = document.getElementById('mpane'); if (p) p.scrollTop = 0; }
function toolBody(id){ return TOOL_BODIES[id] ? TOOL_BODIES[id]() : '<p class="note">No body.</p>'; }

function chars(str){
  var s = String(str), out = [], i, cu, next;
  for (i = 0; i < s.length; i++) {
    cu = s.charCodeAt(i);
    if (cu >= 0xD800 && cu <= 0xDBFF && i + 1 < s.length) {
      next = s.charCodeAt(i + 1);
      if (next >= 0xDC00 && next <= 0xDFFF) { out.push(s.substr(i, 2)); i++; continue; }
    }
    out.push(s.charAt(i));
  }
  return out;
}
function cleanViewName(raw){ return chars(String(raw).replace(/[\u0000-\u001f\u007f]+/g, ' ').replace(/\s+/g, ' ').trim()).slice(0, MAX_VIEW_NAME).join(''); }
function viewNameTooLong(raw){ return chars(String(raw).replace(/[\u0000-\u001f\u007f]+/g, ' ').replace(/\s+/g, ' ').trim()).length > MAX_VIEW_NAME; }
function noteTooLong(raw){ return chars(String(raw).trim()).length > MAX_ITEM_TEXT; }
function viewWindowsLine(){
  var f = W.frame;
  return f.incidentStart.replace('T', ' ') + ' to ' + f.incidentEnd.replace('T', ' ');
}
function viewList(){
  if (!W.full200) return W.views;
  var out = W.views.slice(), i;
  for (i = out.length; i < MAX_VIEWS; i++) out.push({ id:'full' + i, name:'View ' + (i + 1), tool:'response-time', win:'2026-10-26 09:00 to 2026-10-26 10:00', saved:'2026-10-26 16:00' });
  return out;
}
function saveCurrentView(){
  if (W.closed) { W.flash = 'This case is closed. You can open views but not change them.'; render(); return; }
  if (W.full200 || W.views.length >= MAX_VIEWS) { W.flash = 'This case holds 200 saved views. Delete one to save another.'; render(); return; }
  if (viewNameTooLong(W.viewName)) { render(); return; }
  var name = cleanViewName(W.viewName);
  if (!name) { W.flash = 'Give the view a name.'; render(); return; }
  W.viewSeq += 1;
  W.views.push({ id:'v' + W.viewSeq, name:name, tool:W.tool || 'response-time', win:viewWindowsLine(), saved:'2026-10-26 16:40' });
  W.viewName = ''; W.savingView = false; W.flash = 'Saved view "' + name + '".'; render();
}
function deleteView(id){
  if (W.closed) { W.flash = 'This case is closed. You can open views but not change them.'; render(); return; }
  W.views = W.views.filter(function(v){ return v.id !== id; });
  render();
}
function openView(id){
  var v = W.views.filter(function(x){ return x.id === id; })[0];
  if (!v) return;
  W.tool = toolById(v.tool) ? v.tool : '';
  W.flash = 'Opened view "' + v.name + '". Windows and filters are those of the view.';
  render();
}

function pretendStrip(){
  var keys = [['audit','audit'],['iis','IIS'],['sql','SQL'],['mongo','MongoDB'],['host','Host']];
  var boxes = keys.map(function(k){
    return '<label class="check"><input type="checkbox" ' + (W.have[k[0]] ? 'checked' : '') + ' onchange="W.have.' + k[0] + '=this.checked;render()"> ' + k[1] + '</label>';
  }).join('');
  return '<div><span class="wftag">Pretend this case has</span><div class="row">' + boxes + '</div>'
    + '<div class="row"><label class="check"><input type="checkbox" ' + (W.ranSteps ? 'checked' : '') + ' onchange="W.ranSteps=this.checked;render()"> Pretend the steps have run</label>'
    + '<label class="check"><input type="checkbox" ' + (W.closed ? 'checked' : '') + ' onchange="W.closed=this.checked;render()"> Case is closed</label>'
    + '<label class="check"><input type="checkbox" ' + (W.full200 ? 'checked' : '') + ' onchange="W.full200=this.checked;render()"> Saved views full (200)</label>'
    + '<label class="check"><input type="checkbox" ' + (W.stale ? 'checked' : '') + ' onchange="W.stale=this.checked;render()"> Out of date</label></div></div>';
}
function wfnExtra(id){
  if (id === 'manual-home' || (id && id.indexOf('tool-') === 0)) return pretendStrip();
  return '';
}

function toolRunBar(){
  var label = W.ranSteps ? 'Run again' : 'Run the steps';
  return '<div class="runbar"><b>Incident</b><span class="win"><b>26 Oct 09:00&ndash;10:00</b> UTC</span><b>Reference</b><span class="win"><b>26 Oct 08:00&ndash;09:00</b> UTC</span>'
    + (W.stale ? '<span class="stale">The windows, sources or lists changed after this ran.</span>' : '')
    + '<span style="flex:1"></span><button class="btn" onclick="W.windowsOpen[W.tool]=true;render()">Edit windows</button>'
    + '<button class="btn primary" onclick="W.ranSteps=true;W.flash=\'Reading the data. This can take a little while.\';render()">' + label + '</button></div>';
}
function windowsPanel(){
  var open = !!W.windowsOpen[W.tool];
  return '<details class="winpanel"' + (open ? ' open' : '') + '><summary onclick="event.preventDefault();W.windowsOpen[W.tool]=!W.windowsOpen[W.tool];render()">Incident window and Reference window (normal)</summary>'
    + (open ? '<div class="wf-in">' + frameForm() + '</div>' : '') + '</details>';
}
function noteForm(){
  if (W.closed) return '<p class="notice" role="status">This case is closed, so notes and checks cannot be added here. You can still read everything.</p>';
  var over = noteTooLong(W.noteDraft);
  var empty = String(W.noteDraft || '').trim() === '';
  return '<form class="card" onsubmit="event.preventDefault();addToolNote()"><label class="wf">Add a note to the checklist'
    + '<input class="wf" type="text" value="' + esc(W.noteDraft) + '" oninput="W.noteDraft=this.value" aria-invalid="' + over + '"></label>'
    + (over ? '<p class="note" role="status">An item holds up to 300 characters.</p>' : '')
    + '<div class="row"><button class="btn" type="submit" ' + (empty || over ? 'disabled' : '') + '>Add</button>'
    + (W.noted[W.tool] ? '<span class="note" role="status">Added to the checklist.</span>' : '') + '</div></form>';
}
function addToolNote(){
  if (W.closed || noteTooLong(W.noteDraft) || String(W.noteDraft).trim() === '') return;
  W.checklist.push({ id:'n' + Date.now() % 100000, list:'learned', text:chars(String(W.noteDraft).trim()).slice(0, MAX_ITEM_TEXT).join(''), suspect:'' });
  W.noteDraft = ''; W.noted[W.tool] = true; render();
}

function savedViewsHtml(){
  var list = viewList();
  var n = W.full200 ? MAX_VIEWS : W.views.length;
  var over = viewNameTooLong(W.viewName);
  var out = '<section class="card" aria-labelledby="views-title"><h3 id="views-title" style="font-size:16px">Saved views (' + n + ')</h3>';
  if (W.closed) out += '<p class="notice" role="status">This case is closed. You can open views but not change them.</p>';
  if (W.full200) out += '<p class="notice" role="status">This case holds 200 saved views. Delete one to save another.</p>';
  if (!W.closed) {
    out += '<div><button class="btn" aria-expanded="' + W.savingView + '" onclick="W.savingView=!W.savingView;render()">Save this view</button></div>';
    if (W.savingView) {
      out += '<div class="card" style="background:var(--surface-2)"><label class="wf">Name<input class="wf" type="text" value="' + esc(W.viewName) + '" oninput="W.viewName=this.value;render()" aria-invalid="' + over + '"></label>'
        + (over ? '<p class="note" role="status">A view name holds up to 60 characters. Shorten it to save.</p>' : '')
        + '<p class="note">Up to 60 characters. Not a secret: it is shown in lists and in exports.</p>'
        + '<p class="note">Saves: the windows, the API and the tool filters. Not saved: results.</p>'
        + '<div class="row"><button class="btn" ' + (over ? 'disabled' : '') + ' onclick="saveCurrentView()">Save the view</button>'
        + '<button class="quiet" onclick="W.savingView=false;W.viewName=\'\';render()">Cancel</button></div></div>';
    }
  }
  if (list.length === 0) {
    out += '<p class="note">No view is saved yet.</p>';
  } else {
    out += '<div class="tscroll" style="max-height:420px"><table><thead><tr><th>Name</th><th>Tool</th><th>Windows (UTC)</th><th>Saved</th><th>Actions</th></tr></thead><tbody>'
      + list.map(function(v){
        var t = toolById(v.tool);
        return '<tr><td>' + esc(v.name) + '</td><td>' + esc(t ? t.title : v.tool) + '</td><td>' + esc(v.win) + '</td><td>' + esc(v.saved) + '</td>'
          + '<td class="row"><button class="quiet" onclick="openView(\'' + v.id + '\')">Open</button>'
          + (W.closed ? '' : '<button class="quiet" onclick="deleteView(\'' + v.id + '\')">Delete</button>') + '</td></tr>';
      }).join('') + '</tbody></table></div>';
  }
  return out + '</section>';
}

function missingBox(st){
  var need = NEED_WORD[st.missing[0]] || 'a source';
  return '<div class="missing"><h4>This tool cannot run yet</h4>'
    + '<p>This tool needs ' + need + ', and this case has none. Nothing is shown rather than a guess.</p>'
    + '<div><button class="btn" onclick="inert(\'Opens Add by location\')">Add by location</button></div></div>';
}

function toolCard(t){
  var st = liveToolState(t);
  var sk = liveToolSticker(st);
  return '<li class="tcard"><div class="top"><h4>' + t.title + '</h4>' + sticker(sk.word, sk.tone) + '</div>'
    + '<p style="margin:0;font-size:13px">' + t.blurb + '</p>'
    + '<p class="note">Sources: ' + t.sources + '</p>'
    + (st.word === 'Cannot test yet' && st.missing.length ? '<p class="note">Nothing is shown rather than a guess.</p>' : '')
    + (st.word === 'Waiting' ? '<p class="note">Press Run the steps first. This tool shows what that run found.</p>' : '')
    + '<div><button class="btn" onclick="openTool(\'' + t.id + '\')">Open: ' + t.title + '</button></div></li>';
}

function renderToolsHome(){
  return '<header><h3 id="tools-title" style="font-size:20px">Manual tools</h3></header>'
    + '<p class="lead">The same facts as the steps, one tool at a time, in any order.</p>'
    + '<ul class="tcards">' + TOOLS.map(toolCard).join('') + '</ul>'
    + savedViewsHtml()
    + wfn('manual-home',
      dt('Data shown', '13 cards from <code>TOOLS</code> in <code>tools.ts</code> order. Status from <code>toolState</code>: hard needs missing &rarr; Cannot test yet; IIS is a soft need on time-split (Partly ready); tools that need a run and have not run are Waiting; Slowest calls and Nearby activity read themselves so they are Ready.')
      + dt('Chart', 'None on the home. Each tool has its own.')
      + dt('Empty', 'Saved views: "No view is saved yet." when <code>views[]</code> is empty.')
      + dt('Cannot test yet', 'A card whose hard source is off. Default dummy: MongoDB and Host off, IIS off so time-split is Partly ready, audit and SQL on.')
      + dt('Failed', 'Not a home-card word unless the matching step failed after a run (toggle Pretend the steps have run and a step Failed).')
      + dt('Out of date', 'Ready / Partly ready / Failed cards take the Out of date, rerun sticker when the Out of date box is ticked.')
      + dt('Buttons', 'Open: &lt;title&gt; opens the tool (live). Save this view / Open / Delete on saved views (live). Closed case and 200-full are toggles in this strip.'));
}

function renderTool(id){
  var t = toolById(id);
  if (!t) return renderToolsHome();
  var st = liveToolState(t);
  var sk = liveToolSticker(st);
  var noBar = t.id === 'slowest-calls' || t.id === 'nearby-activity';
  var body;
  if (st.missing.length) body = missingBox(st);
  else body = windowsPanel() + (noBar ? '' : toolRunBar()) + toolBody(id);
  var notes = TOOL_NOTES[id] ? TOOL_NOTES[id]() : dt('TOOLS entry', '<code>' + t.id + '</code> &middot; needs ' + t.needs.join(', ') + ' &middot; sources ' + t.sources);
  return '<header class="sthead"><button class="quiet" onclick="backToTools()">&larr; Back to tools</button>'
    + '<h3 id="tools-title" style="font-size:20px">' + t.title + '</h3>' + sticker(sk.word, sk.tone) + '</header>'
    + '<p class="lead">' + t.blurb + '</p>'
    + body
    + noteForm()
    + savedViewsHtml()
    + '<div><button class="quiet" onclick="setMode(\'guided\')">Run Guided analysis instead</button></div>'
    + wfn('tool-' + id, notes
      + dt('TOOLS entry', '<code>' + t.id + '</code>. step ' + (t.step !== undefined ? t.step : 'none (own page)') + '. needs <code>' + t.needs.join(', ') + '</code>. sources: ' + t.sources + '.')
      + dt('Chrome', 'Back to tools, sticker, blurb, missing-source box when a hard source is absent, collapsible windows (Step0Frame), RunBar except slowest-calls and nearby-activity, note form, saved views, Run Guided analysis instead.'));
}

function renderManual(){
  return '<div class="work">' + caseBar()
    + '<main class="manual-body" id="mpane" tabindex="0">' + flashLine() + '<div class="mwrap">'
    + (W.tool ? renderTool(W.tool) : renderToolsHome())
    + '</div></main></div>';
}
window.renderManual = renderManual;
window.openTool = openTool;
window.backToTools = backToTools;
