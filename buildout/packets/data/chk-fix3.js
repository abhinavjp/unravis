// usage (from buildout/wireframes) after apply: node ../packets/data/chk-fix3.js   prints PASS or FAIL <reason>
var fs = require('fs'), vm = require('vm'), bad = [];
function rd(p) { return fs.readFileSync(p, 'utf8').split(String.fromCharCode(13)).join(''); }
function cnt(s, t) { return String(s).split(t).length - 1; }
var FILES = ['parts/31-steps-5-9.js', 'parts/32-steps-10-13.js', 'parts/30-steps-0-4.js', 'parts/30-recurrence.js'];
var SRC = {};
FILES.forEach(function (p) {
  SRC[p] = rd(p);
  if (/=>|`|\blet\b|\bconst\b/.test(SRC[p])) bad.push(p + ' not ES5');
  try { new Function(SRC[p]); } catch (x) { bad.push(p + ' syntax ' + x.message); }
});
// wiring greps on the edited files
var s31 = SRC['parts/31-steps-5-9.js'], s32 = SRC['parts/32-steps-10-13.js'], s30 = SRC['parts/30-steps-0-4.js'], s30r = SRC['parts/30-recurrence.js'];
if (cnt(s31, 'function addCheck(id){\n  if (W.closed) return;') !== 1) bad.push('FIX3-1 closed guard missing or duplicated');
if (cnt(s32, '▼ Still above normal') !== 1) bad.push('FIX3-2 compare-table mark missing or duplicated');
if (cnt(s32, "r[3].charAt(0) === '-' || r[3].charAt(0) === '−'") !== 1) bad.push('FIX3-2 negative-change test missing');
if (cnt(s32, 'aria-label="Move this item to another list"') !== 1) bad.push('FIX3-3 aria-label missing or duplicated');
if (cnt(s30, "function signed(n){ n = String(n); return n.charAt(0) === '-' ? n : '+' + n; }") !== 1) bad.push('FIX3-4a signed helper missing');
if (cnt(s30, 'signed(fmtCount(r[5]))') !== 1) bad.push('FIX3-4a byApiTable not using signed');
if (cnt(s30, 'signed(fmtMs(d))') !== 1) bad.push('FIX3-4a partsTable not using signed');
if (cnt(s30, "Number(f.poolLimit.trim()) < 1 || Number(f.poolLimit.trim()) > 100000") !== 1) bad.push('FIX3-4b pool limit range missing');
if (cnt(s30, "(withNearby ? '<th></th>' : '')") !== 1) bad.push('FIX3-4c header-cell guard missing');
if (cnt(s30r, 'Darker red = slower than normal for that hour.') !== 1) bad.push('FIX3-5a legend not red');
if (cnt(s30r, 'Darker amber') !== 0) bad.push('FIX3-5a legend still amber');
if (cnt(s30r, "    if (isNaN(n)) n = 1;\n    var max = recurLimit(r.mode);\n    if (n < 1) n = 1;\n    if (n > max) n = max;\n    r.n = n;") !== 1) bad.push('FIX3-5b clamp not stored back');
if (bad.length) { console.log('FAIL ' + bad.join('; ')); process.exit(0); }

var c = { renders: 0 };
c.render = function () { c.renders++; };
c.window = c;
vm.createContext(c);
['parts/10-data.js', 'parts/11-charts-b.js', 'parts/30-frame-helpers.js', 'parts/30-pager.js',
 'parts/30-steps-0-4.js', 'parts/31-steps-5-9.js', 'parts/32-steps-10-13.js', 'parts/30-recurrence.js'].forEach(function (p) {
  try { vm.runInContext(rd(p), c); } catch (e) { console.log('FAIL load ' + p + ' ' + e.message); process.exit(0); }
});
function run(s) { return vm.runInContext(s, c); }
function eq(l, g, w) { if (g !== w) bad.push(l + ' -> ' + JSON.stringify(g) + ' wanted ' + JSON.stringify(w)); }
function has(l, h, t) { if (String(h).indexOf(t) < 0) bad.push(l + ' lacks ' + JSON.stringify(t)); }

run("W = { closed: false, flash: '', checklist: [], views: [], showAllApis: false, apiMode: 'all', apiOne: 'Catalog.Get', slowShown: true, slowPg: 0, nearbyOpen: false, nearbyRow: 0, ckAdd: false, ckNote: '', ckText: '', ckList: 'todo', fix: { day: '2026-10-28', route: '', verdictOn: true, recorded: false, problem: '' }, frame: { incidentStart: '2026-10-26T09:00', incidentEnd: '2026-10-26T10:00', referenceStart: '2026-10-26T07:00', referenceEnd: '2026-10-26T08:00', releases: [], releasesEntered: false, poolLimit: '' } };");

// FIX3-1: addCheck closed-case guard
run("W.checklist = [{ id: 'c0', list: 'todo', text: 'x', suspect: '' }]; W.closed = true;");
var len0 = run('W.checklist.length'), ren0 = c.renders;
c.addCheck('D1');
eq('closed addCheck keeps length', run('W.checklist.length'), len0);
eq('closed addCheck no render', c.renders, ren0);
eq('closed addCheck no flash', run('W.flash'), '');
run('W.closed = false;');
c.addCheck('D1');
eq('open addCheck adds one', run('W.checklist.length'), len0 + 1);
eq('open addCheck flash', run('W.flash'), 'Added to the checklist (Need to do).');
eq('open addCheck renders', c.renders, ren0 + 1);
c.addCheck('D1');
eq('open addCheck no duplicate', run('W.checklist.length'), len0 + 1);

// FIX3-2: compare-table mark for a negative change
var af = c.afterFixBody();
eq('P95 row', cnt(af, '<tr><td>P95 (slow end)</td><td>2,100 ms</td><td>790 ms</td><td><span class="mark bad">▼ Still above normal</span> -62 %</td></tr>'), 1);
eq('P50 row unchanged', cnt(af, '<tr><td>P50 (middle)</td><td>380 ms</td><td>210 ms</td><td><span class="mark grey">● No change</span> -45 %</td></tr>'), 1);
eq('D row', cnt(af, '<tr><td>D (whole call, average)</td><td>2,150 ms</td><td>820 ms</td><td><span class="mark bad">▼ Still above normal</span> -62 %</td></tr>'), 1);
eq('L row unchanged', cnt(af, '<tr><td>L (calls in flight)</td><td>21.4</td><td>8.1</td><td>-62 %</td></tr>'), 1);
if (af.indexOf('▲ Slower') >= 0) bad.push('FIX3-2 a fall must not be marked Slower');

// FIX3-3: per-item Move to select has an accessible name
run("W.checklist = [{ id: 'c1', list: 'todo', text: 'Read the blocking chain', suspect: 'D1' }];");
var s12 = c.step12('Ready');
eq('ckMove aria-label count', cnt(s12, 'aria-label="Move this item to another list"'), 1);
has('ckMove aria-label on select', s12, '<select class="wf" style="width:auto" aria-label="Move this item to another list" onchange="ckMove(');
run('W.closed = true;');
if (c.step12('Ready').indexOf('onchange="ckMove(') >= 0) bad.push('FIX3-3 closed case must render no move select');
run('W.closed = false;');

// FIX3-4a: signed deltas
eq('signed -2', c.signed(-2), '-2');
eq('signed 2', c.signed(2), '+2');
eq('signed 0', c.signed(0), '+0');
eq('signed -2 ms', c.signed(c.fmtMs(-2)), '-2 ms');
eq('signed 2 ms', c.signed(c.fmtMs(2)), '+2 ms');
eq('signed 1,680', c.signed(c.fmtCount(1680)), '+1,680');
var byApi = c.byApiTable(false);
eq('byApi negative delta', cnt(byApi, '(-2 ms)'), 1);
eq('byApi positive delta', cnt(byApi, '(+1,680 ms)'), 1);
eq('byApi zero delta', cnt(byApi, '(+0 ms)'), 1);
eq('byApi no (+-', cnt(byApi, '(+-'), 0);
run("W.apiMode = 'one'; W.apiOne = 'Catalog.Get';");
var pt = c.partsTable();
eq('parts negative delta', cnt(pt, '(-1 ms)'), 1);
eq('parts positive delta', cnt(pt, '(+30 ms)'), 1);
eq('parts no (+-', cnt(pt, '(+-'), 0);
run("W.apiMode = 'all';");

// FIX3-4b: pool limit accepts only integers 1..100000
['0', '00', '100001', '999999', '12.5', 'abc', '-5'].forEach(function (v) {
  run("W.frame.poolLimit = '" + v + "';");
  var probs = c.frameProblems();
  eq('pool ' + v + ' one problem', probs.length, 1);
  eq('pool ' + v + ' message', probs[0], 'The pool limit is a whole number from 1 to 100000, or empty.');
});
['', '   ', '1', '99', '100000'].forEach(function (v) {
  run("W.frame.poolLimit = '" + v + "';");
  eq('pool "' + v + '" accepted', c.frameProblems().length, 0);
});

// FIX3-4c: slowCallsTable header cell only with nearby
eq('slow th with nearby', cnt(c.slowCallsTable(true), '<th></th>'), 1);
eq('slow th without nearby', cnt(c.slowCallsTable(false), '<th></th>'), 0);
eq('slow rows listed', cnt(c.slowCallsTable(true), '<tr class=""'), 10);
eq('slow nearby buttons', cnt(c.slowCallsTable(true), '>Nearby activity</button>'), 10);
eq('slow no nearby buttons', cnt(c.slowCallsTable(false), '>Nearby activity</button>'), 0);

// FIX3-5a: heatmap legend says red
c.recurMode('days');
var heat = c.recurHeat(c.recurOcc());
has('legend red', heat, 'Darker red = slower than normal for that hour. Light = close to normal. Gray = no data yet for that hour.');
if (heat.indexOf('Darker amber') >= 0) bad.push('FIX3-5a legend must not say amber');
eq('hit cells unchanged', cnt(heat, 'class="cell hit '), 7);

// FIX3-5b: clamped count stored back and displayed
run('recurSet("n", "99")');
eq('clamp high stored', run('W.recur.n'), 30);
eq('clamp high in occ', c.recurOcc().n, 30);
has('input shows 30', c.frameRecurrence(), 'value="30"');
run('recurSet("n", "0")');
eq('clamp low stored', run('W.recur.n'), 1);
has('input shows 1', c.frameRecurrence(), 'value="1"');
run('recurSet("n", "abc")');
eq('NaN stored as 1', run('W.recur.n'), 1);
run('recurSet("n", "6")');
eq('in-range kept', run('W.recur.n'), 6);
eq('days list still 6', c.recurOcc().list.length, 6);

console.log(bad.length ? 'FAIL ' + bad.join('; ') : 'PASS');
