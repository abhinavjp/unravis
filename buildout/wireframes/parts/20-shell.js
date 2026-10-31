// ---------- wireframe state ----------
var W = {
  variant:'A', mode:'guided', step:3, openFold:3, tool:'',
  notesOn:true, notesOpen:{}, flash:'',
  stepState:{},                                  // per-step override; default comes from STEPS
  have:{ audit:true, iis:false, sql:true, mongo:false, host:false },  // manual "pretend" sources (see 40-manual-home.js note)
  ranSteps:false,                                // manual "pretend the steps have run"
  closed:false, full200:false, stale:false,
  views: VIEWS0.map(function(v,i){ return { id:'v'+(i+1), name:v.name, tool:v.tool, win:v.win, saved:v.saved }; }),
  viewSeq: 3, savingView:false, viewName:'',
  checklist: CHECKLIST0.map(function(c,i){ return { id:'c'+i, list:c[0], text:c[1], suspect:c[2] }; }),
  ckAdd:false, ckText:'', ckList:'todo', ckNote:'',
  frame:{ incidentStart:'2026-10-26T09:00', incidentEnd:'2026-10-26T10:00', referenceStart:'2026-10-26T08:00', referenceEnd:'2026-10-26T09:00',
    route:'', poolLimit:'100', releasesEntered:true, myOnset:'2026-10-26T09:12',
    releases: CHANGES0.map(function(c){ return { at:c.at, label:c.label }; }) },
  carryOn:false, showAllApis:false, showSetAside:false,
  slowShown:true, nearbyRow:null, nearbyPicked:false, nearbyOpen:true, nbAround:'2026-10-26T09:14', nbBefore:5, nbAfter:5, nbTouched:false,
  callsApi:'',
  minCalls:20, match:'',
  windowsOpen:{}, noteDraft:'', noted:{},
  fix:{ day:'2026-10-28', route:'', verdictOn:false, recorded:false, problem:'' },
  exp:{ figures:true, suspects:true, checklist:true, sources:true, preview:false, built:'', saved:false },
  backNote:false,
  why:null, evSet:'incident'
};
var ST_WORDS = ['Ready','Partly ready','Cannot test yet','Failed','Waiting','Out of date'];
var ST_TONE = { 'Ready':'ready', 'Partly ready':'partly', 'Cannot test yet':'cannot', 'Failed':'failed', 'Waiting':'waiting', 'Out of date':'stale' };
var STEP11_WORD = { 11:'Not started', 12:'4 of 9 done', 13:'Not started' };
function stepState(n){ return W.stepState[n] || STEPS[n].st; }
function sticker(word, tone){ return '<span class="sticker ' + (tone || ST_TONE[word] || 'cannot') + '">' + word + '</span>'; }
function stepSticker(n){
  var st = stepState(n);
  if (st === 'Out of date') return '<span class="sticker stale">Out of date, rerun</span>';
  var word = st;
  if (n >= 11 && !W.stepState[n]) word = STEP11_WORD[n];
  return sticker(word, ST_TONE[st]);
}
function inert(what){ W.flash = what + ' (inert in wireframe)'; render(); }
function flashLine(){
  return W.flash ? '<p class="notice" role="status">' + esc(W.flash) + ' <button class="quiet" onclick="W.flash=\'\';render()">Dismiss</button></p>' : '';
}

// ---------- chrome ----------
function topHeader(){
  return '<header class="hdr"><div class="brand"><b>Un</b>ravis</div>'
    + '<nav aria-label="Main"><span>Cases</span><span class="on">Case 0042</span><span>Sources</span><span>Help</span></nav><div class="sp"></div>'
    + '<label class="check" style="font-size:12px;color:var(--text-2)"><input type="checkbox" id="notesw" ' + (W.notesOn ? 'checked' : '')
    + ' onchange="W.notesOn=this.checked;document.documentElement.classList.toggle(\'notes-off\',!this.checked)"> Show wireframe notes</label>'
    + '<button class="btn sm" onclick="toggleTheme()">Theme</button></header>';
}
function caseBar(){
  var guided = W.mode === 'guided';
  return '<div class="bar">'
    + '<button class="btn" aria-label="Back to the case" onclick="inert(\'Back to the case: returns to the case page and keeps the chosen step for next time\')">&larr; Case 0042</button>'
    + '<div class="crumb"><small>Case 0042 &middot; Orders slowdown 26 Oct' + (W.mode === 'manual' ? ' &middot; You are using Manual tools.' : ' &middot; You are using Guided analysis.') + '</small>'
    + '<h2>' + (guided ? 'Guided analysis' : 'Manual tools') + '</h2></div>'
    + '<button class="btn sm" onclick="setMode(\'' + (guided ? 'manual' : 'guided') + '\')">' + (guided ? 'Switch to Manual tools' : 'Switch to Guided analysis') + '</button></div>';
}
function runBar(){
  var primary = !(W.mode === 'guided' && W.step === 11);
  return '<div class="runbar"><b>Incident</b><span class="win"><b>26 Oct 09:00&ndash;10:00</b> UTC</span><b>Reference</b><span class="win"><b>26 Oct 08:00&ndash;09:00</b> UTC</span>'
    + (W.stepState[3] === 'Out of date' ? '<span class="stale">The windows, sources or lists changed after this ran.</span>' : '')
    + '<span style="flex:1"></span><button class="btn" onclick="go(0)">Edit windows</button>'
    + '<button class="btn ' + (primary ? 'primary' : '') + '" onclick="runSteps()">Run again</button></div>';
}
function runSteps(){
  for (var n = 1; n <= 10; n++) W.stepState[n] = STEPS[n].st === 'Waiting' ? 'Ready' : STEPS[n].st;
  if (W.fix) { W.fix.verdictOn = false; W.fix.recorded = false; }
  W.ranSteps = true;
  W.flash = 'Reading the data. This can take a little while. In the wireframe the run finishes at once and every step shows its Ready data again.';
  render();
}

// ---------- wireframe notes strip ----------
function wfn(id, rowsHtml, extra){
  var open = W.notesOpen[id] !== false;
  return '<details class="wfn" data-wfn="' + id + '"' + (open ? ' open' : '') + '><summary>Wireframe notes</summary><div class="wfn-in">'
    + (rowsHtml ? '<dl>' + rowsHtml + '</dl>' : '') + (extra || '') + (typeof wfnExtra === 'function' ? (wfnExtra(id) || '') : '') + '</div></details>';
}
function dt(t, h){ return '<dt>' + t + '</dt><dd>' + h + '</dd>'; }
// per-step state switcher, lives in the notes strip
function stateSeg(n){
  var b = ST_WORDS.map(function(s){
    return '<button class="' + (stepState(n) === s ? 'on' : '') + '" onclick="W.stepState[' + n + ']=\'' + s + '\';render()">' + s + '</button>';
  }).join('');
  return '<div><span class="wftag">Show this step as:</span><div class="seg">' + b + '</div></div>';
}

// ---------- step frame (StepShell look) ----------
function stepHead(n){
  return '<div class="sthead"><h3 id="step-title-' + n + '" tabindex="-1">' + (n <= 10 ? 'Step ' + n + '. ' : '') + STEPS[n].title + '</h3>'
    + stepSticker(n) + '<span class="sp"></span></div>';
}
function nutshell(text){
  return '<div class="nutshell"><p class="nl">In a nutshell</p><p>' + text + '</p></div>';
}
function reasonsBox(list){
  if (!list.length) return '';
  return '<div><p class="nl" style="color:var(--text-2);font-size:12px;font-weight:600;letter-spacing:.04em;text-transform:uppercase">What could not be tested</p><ul style="margin:0;padding-left:18px;color:var(--text-2)"><li>' + list.join('</li><li>') + '</li></ul></div>';
}
function prevNext(){
  return '<div class="nav"><button class="btn" ' + (W.step === 0 ? 'disabled' : '') + ' onclick="go(' + Math.max(0, W.step - 1) + ')">&larr; Previous step</button>'
    + '<span class="note">' + STEPS[W.step].label + ' &middot; ' + (W.step + 1) + ' of ' + STEPS.length + '</span>'
    + '<button class="btn" ' + (W.step === 13 ? 'disabled' : '') + ' onclick="go(' + Math.min(13, W.step + 1) + ')">Next step &rarr;</button></div>';
}

// ---------- guided variants ----------
function railHtml(){
  var last = '', out = '';
  STEPS.forEach(function(s){
    if (s.grp !== last) { last = s.grp; out += '<div class="grp">' + s.grp + '</div>'; }
    out += '<button class="row' + (s.n === W.step ? ' cur' : '') + '"' + (s.n === W.step ? ' aria-current="step"' : '') + ' onclick="go(' + s.n + ')">'
      + '<span class="n">' + s.n + '</span><span>' + s.label + '</span>' + stepSticker(s.n) + '</button>';
  });
  out += '<p class="note" style="padding:4px 2px">Each step has a status. Waiting means it has not run yet.</p>';
  return out;
}
function renderA(){
  return '<div class="work A">' + caseBar() + '<div class="body"><nav class="rail" aria-label="Guided analysis steps">' + railHtml() + '</nav>'
    + '<main class="pane" id="pane" tabindex="0">' + runBar() + flashLine() + stepWrap(W.step) + '</main></div></div>';
}
function renderB(){
  var bar = GROUPS.map(function(g){
    return '<div class="ph"><small>' + g + '</small><div class="chips">' + STEPS.filter(function(s){ return s.grp === g; }).map(function(s){
      var tone = stepState(s.n); var dot = tone === 'Out of date' ? 'partly' : (ST_TONE[tone] === 'waiting' ? '' : ST_TONE[tone]);
      return '<button class="chip' + (s.n === W.step ? ' cur' : '') + '" onclick="go(' + s.n + ')"><i class="' + dot + '"></i>' + s.n + ' ' + s.label + '</button>';
    }).join('') + '</div></div>';
  }).join('');
  return '<div class="work B">' + caseBar() + '<div class="stepper" role="navigation" aria-label="Steps">' + bar + '</div>'
    + '<main class="pane" id="pane" tabindex="0">' + runBar() + flashLine() + stepWrap(W.step) + '</main>' + prevNext() + '</div>';
}
function renderC(){
  var done = STEPS.filter(function(s){ return stepState(s.n) === 'Ready'; }).length;
  var prog = '<nav class="prog" aria-label="Progress"><div class="note">' + done + ' of ' + STEPS.length + ' answered</div><div class="meter"><div style="width:' + Math.round(done / STEPS.length * 100) + '%"></div></div>'
    + STEPS.map(function(s){ return '<button class="' + (s.n === W.openFold ? 'cur' : '') + '" onclick="fold(' + s.n + ')">' + s.n + '. ' + s.label + '</button>'; }).join('') + '</nav>';
  var folds = STEPS.map(function(s){
    return '<section class="fold' + (s.n === W.openFold ? ' cur' : '') + '"><button onclick="fold(' + s.n + ')" aria-expanded="' + (s.n === W.openFold) + '"><span class="n">' + s.n + '</span><span>' + s.title + '</span><span class="sp"></span>' + stepSticker(s.n) + '</button>'
      + (s.n === W.openFold ? '<div class="inner">' + stepWrap(s.n) + '</div>' : '') + '</section>';
  }).join('');
  return '<div class="work C">' + caseBar() + '<div class="body">' + prog + '<main class="pane" id="pane" tabindex="0">' + runBar() + flashLine() + folds + '</main></div></div>';
}
// one step page: shell + body for the current state + notes strip
function stepWrap(n){
  var st = stepState(n);
  var body = (typeof window['step' + n] === 'function') ? window['step' + n](st) : '<p class="note">No renderer.</p>';
  return '<div class="stepbody">' + stepHead(n) + body + wfn('step' + n, window['stepNotes' + n] ? window['stepNotes' + n]() : '', null) + '</div>';
}

// ---------- navigation and mode ----------
function keepScroll(){
  var pane = document.getElementById('pane') || document.getElementById('mpane');
  var top = pane ? pane.scrollTop : 0;
  document.getElementById('app').innerHTML = topHeader() + (W.mode === 'guided'
    ? (W.variant === 'A' ? renderA() : W.variant === 'B' ? renderB() : renderC())
    : renderManual());
  var p2 = document.getElementById('pane') || document.getElementById('mpane');
  if (p2 && top) p2.scrollTop = top;
}
function render(){
  keepScroll();
  document.getElementById('vlabel').innerHTML = W.variant + ' <small>' + ({ A:'Fixed rail', B:'Top stepper', C:'One flow' })[W.variant] + '</small>';
  try {
    var q = '?variant=' + W.variant + '&mode=' + W.mode + (W.mode === 'guided' ? '&step=' + W.step : (W.tool ? '&tool=' + W.tool : ''));
    history.replaceState(null, '', q);
  } catch (e) {}
}
function go(n){ W.step = n; W.openFold = n; W.flash = ''; render(); var p = document.getElementById('pane'); if (p) p.scrollTop = 0; }
function fold(n){ W.openFold = n; W.step = n; render(); }
function setMode(m){ W.mode = m; W.flash = ''; render(); var p = document.getElementById('pane') || document.getElementById('mpane'); if (p) p.scrollTop = 0; }
function cycle(d){ var k = ['A','B','C']; W.variant = k[(k.indexOf(W.variant) + d + 3) % 3]; render(); }
function toggleTheme(){ var r = document.documentElement; r.dataset.theme = r.dataset.theme === 'dark' ? 'light' : 'dark'; }

// ---------- keyboard ----------
document.getElementById('prev').onclick = function(){ cycle(-1); };
document.getElementById('next').onclick = function(){ cycle(1); };
addEventListener('keydown', function(e){
  var tag = document.activeElement && document.activeElement.tagName;
  if (/INPUT|TEXTAREA|SELECT/.test(tag)) return;
  if (e.key === 'ArrowLeft' && !e.target.closest('.rail')) cycle(-1);
  else if (e.key === 'ArrowRight' && !e.target.closest('.rail')) cycle(1);
  else if (e.key === 't' || e.key === 'T') toggleTheme();
});
// rail arrow keys: Up/Down/Home/End move between rows, Enter selects (native button)
addEventListener('keydown', function(e){
  var row = e.target.closest && e.target.closest('.rail .row');
  if (!row) return;
  var rows = Array.prototype.slice.call(document.querySelectorAll('.rail .row'));
  var i = rows.indexOf(row), j = -1;
  if (e.key === 'ArrowDown') j = i + 1; else if (e.key === 'ArrowUp') j = i - 1;
  else if (e.key === 'Home') j = 0; else if (e.key === 'End') j = rows.length - 1;
  if (j < 0 || j >= rows.length) return;
  e.preventDefault(); rows[j].focus();
});
// remember which notes strips are open
addEventListener('toggle', function(e){
  var d = e.target;
  if (d && d.classList && d.classList.contains('wfn')) W.notesOpen[d.getAttribute('data-wfn')] = d.open;
}, true);
