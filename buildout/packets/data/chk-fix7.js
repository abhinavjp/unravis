// usage (from buildout/wireframes) after apply: node ../packets/data/chk-fix7.js   prints PASS or FAIL <reason>
var fs = require('fs'), vm = require('vm'), bad = [];
function rd(p) { return fs.readFileSync(p, 'utf8'); }
function cnt(s, t) { return String(s).split(t).length - 1; }
function has(l, s, t) { if (String(s).indexOf(t) < 0) bad.push(l + ' lacks ' + JSON.stringify(t)); }
function not(l, s, t) { if (String(s).indexOf(t) >= 0) bad.push(l + ' must not contain ' + JSON.stringify(t)); }
function eq(l, g, w) { if (g !== w) bad.push(l + ' -> ' + JSON.stringify(g) + ' wanted ' + JSON.stringify(w)); }

var EDITED = ['parts/20-shell.js', 'parts/42-tools-b.js', 'parts/30-steps-0-4.js', 'parts/31-steps-5-9.js'];
EDITED.forEach(function (p) {
  var s = rd(p);
  if (/=>|`|\blet\b|\bconst\b/.test(s)) bad.push(p + ' not ES5');
  try { new Function(s); } catch (e) { bad.push(p + ' syntax ' + e.message); }
});

// --- load the real chain with a stubbed DOM, same approach chk-fix4.js uses for 20-shell.js ---
var c = { renders: 0 };
c.render = function () { c.renders++; };
c.openTool = function (id) { c.W.tool = id; c.render(); };
function fakeEl() { return { innerHTML: '', scrollTop: 0, onclick: null, dataset: {}, classList: { toggle: function () {} } }; }
c.document = { getElementById: function () { return fakeEl(); }, documentElement: { dataset: {}, classList: { toggle: function () {} } }, querySelectorAll: function () { return []; } };
c.addEventListener = function () {};
c.history = { replaceState: function () {} };
c.window = c;
vm.createContext(c);
var PARTS = ['parts/10-data.js', 'parts/10-data2-calls.js', 'parts/10-data3-near.js', 'parts/10-data5-toolsA.js',
  'parts/11-charts-c.js', 'parts/20-shell.js', 'parts/30-frame-helpers.js', 'parts/30-pager.js',
  'parts/30-steps-0-4.js', 'parts/31-steps-5-9.js', 'parts/41-tools-a.js', 'parts/42-tools-b.js'];
try { PARTS.forEach(function (p) { vm.runInContext(rd(p), c); }); } catch (e) { console.log('FAIL load ' + e.message); process.exit(0); }
function run(s) { return vm.runInContext(s, c); }
['nearbyPanel', 'slowCallsTable', 'step3', 'step4', 'step6'].forEach(function (f) { if (typeof c[f] !== 'function') bad.push('missing ' + f); });
if (typeof c.TOOL_BODIES === 'undefined' || typeof c.TOOL_BODIES['nearby-activity'] !== 'function') bad.push('missing TOOL_BODIES[nearby-activity]');
if (bad.length) { console.log('FAIL ' + bad.join('; ')); process.exit(0); }

function hdr(html) {
  var a = String(html).indexOf('<h4>');
  if (a < 0) return '';
  var b = String(html).indexOf('</h4>', a);
  return b < 0 ? '' : String(html).slice(a, b);
}

// --- 1. the default state (as 20-shell.js sets it, untouched by any test) ---
eq('default nearbyRow', run('W.nearbyRow'), null);
eq('default nearbyPicked', run('W.nearbyPicked'), false);

// --- 2. the tool page at default: Around time, no call name, no " at ", no Close ---
var na = c.TOOL_BODIES['nearby-activity']();
var h0 = hdr(na);
has('tool header around time', h0, '09:14');
not('tool header no call name', h0, 'Orders.Create');
not('tool header no call moment', h0, ' at ');
not('tool page no close', na, 'Close</button>');
eq('tool marks fresh open not picked', run('W.nearbyPicked'), false);

// --- 3. drive the real Step-3 row button, then the header shows the picked call ---
var tbl = c.slowCallsTable(true);
var m = /onclick="(W\.nearbyRow=[^"]*)"[^>]*>Nearby activity/.exec(tbl);
if (!m) {
  bad.push('nearby row onclick not found in slowCallsTable');
} else {
  run(m[1]);
  eq('row click marks picked', run('W.nearbyPicked'), true);
  var i = run('W.nearbyRow');
  var na2 = c.TOOL_BODIES['nearby-activity']();
  has('tool header picked call name', hdr(na2), run('CALLS[' + i + '][0]'));
  has('tool header picked call moment', hdr(na2), run('CALLS[' + i + '][1]'));
  eq('tool keeps picked flag', run('W.nearbyPicked'), true);
}

// --- 4. Step 3's own panel still has a working Close button ---
run("W.slowShown = true; W.nearbyOpen = true;");
var s3 = c.step3('Ready');
has('step3 panel has close', s3, 'onclick="W.nearbyOpen=false;render()">Close</button>');

// --- 5. Step 4's note follows the pool limit, both ways ---
run("W.frame.poolLimit = '';");
var s4empty = c.step4('Ready');
has('step4 empty note', s4empty, 'Calls in flight peaked at 34. No pool limit was entered in Step 0, so there is nothing to compare it against.');
not('step4 empty no old text', s4empty, 'of the 100 entered as the pool limit');
run("W.frame.poolLimit = '50';");
var s4fifty = c.step4('Ready');
has('step4 50 note', s4fifty, 'Calls in flight peaked at 34 of the 50 entered as the pool limit. The pool was not filled.');

// --- 6. Step 6's finding follows the pool limit too, short forms ---
run("W.frame.poolLimit = '';");
var s6empty = c.step6('Ready');
has('step6 empty finding', s6empty, 'Calls in flight peaked at 34; no pool limit was entered.');
run("W.frame.poolLimit = '100';");
var s6hundred = c.step6('Ready');
has('step6 100 finding', s6hundred, 'Calls in flight peaked at 34 of the pool limit 100: the pool was not filled.');
run("W.frame.poolLimit = '40';");
var s6forty = c.step6('Ready');
has('step6 40 finding', s6forty, 'Calls in flight peaked at 34 of the pool limit 40: the pool was not filled.');

// --- 7. the inert(...) entity fix: only the three inert() calls change, the two dt() copy lines do not ---
var s30 = rd('parts/30-steps-0-4.js');
eq('no inert call keeps &rsquo;', cnt(s30, "inert(\\'Opens the case&rsquo;"), 0);
eq('three inert calls use an escaped apostrophe', cnt(s30, "inert(\\'Opens the case\\\\\\'s Add data flow\\')"), 3);
has('unrelated dt copy 1 untouched', s30, 'Add data opens the case&rsquo;s Add data flow (inert).');
has('unrelated dt copy 2 untouched', s30, 'the app&rsquo;s series colours.');

// --- wiring greps ---
var s20 = rd('parts/20-shell.js');
eq('shell default nearbyRow null', cnt(s20, 'nearbyRow:null'), 1);
eq('shell default nearbyPicked false', cnt(s20, 'nearbyPicked:false'), 1);
eq('shell old default gone', cnt(s20, 'nearbyRow:0'), 0);

eq('nearbyPanel takes noClose', cnt(s30, 'function nearbyPanel(noClose){'), 1);
eq('nearbyPanel null-safe read', cnt(s30, 'CALLS[W.nearbyRow == null ? 0 : W.nearbyRow]'), 1);
eq('old unguarded read gone', cnt(s30, 'var c = CALLS[W.nearbyRow];'), 0);
eq('close button guarded', cnt(s30, "(noClose ? '' : '<button class=\"btn sm\" onclick=\"W.nearbyOpen=false;render()\">Close</button>')"), 1);
eq('row button marks picked', cnt(s30, "W.nearbyOpen=true;W.nearbyPicked=true;W.nbAround="), 1);
eq('step4 note is conditional', cnt(s30, "W.frame.poolLimit && W.frame.poolLimit.trim() !== '' ? '<p class=\"note\">Calls in flight peaked at 34 of the '"), 1);
eq('step4 old hardcoded 100 gone', cnt(s30, 'Calls in flight peaked at 34 of the 100 entered as the pool limit.'), 0);

var s31 = rd('parts/31-steps-5-9.js');
eq('step6 finding is conditional', cnt(s31, "Calls in flight peaked at 34 of the pool limit ' + W.frame.poolLimit.trim() + ': the pool was not filled."), 1);
eq('step6 empty finding text', cnt(s31, 'Calls in flight peaked at 34; no pool limit was entered.'), 1);
eq('step6 old hardcoded 100 gone', cnt(s31, "'Calls in flight peaked at 34 of the pool limit 100: the pool was not filled.'"), 0);

var s42 = rd('parts/42-tools-b.js');
eq('tool body sets nearbyPicked from read', cnt(s42, 'W.nearbyPicked = W.nearbyRow != null;'), 1);
eq('tool body calls nearbyPanel(true)', cnt(s42, 'nearbyPanel(true)'), 1);
eq('old forcing-row body gone', cnt(s42, 'if (W.nearbyRow == null) { W.nearbyRow = 0; W.nearbyPicked = false; } else { W.nearbyPicked = true; }'), 0);

console.log(bad.length ? 'FAIL ' + bad.join('; ') : 'PASS');
