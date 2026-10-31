// usage (from buildout/wireframes) after apply: node ../packets/data/chk-fix5.js   prints PASS or FAIL <reason>
var fs = require('fs'), vm = require('vm'), bad = [];
function rd(p) { return fs.readFileSync(p, 'utf8'); }
function cnt(s, t) { return String(s).split(t).length - 1; }
function has(l, s, t) { if (String(s).indexOf(t) < 0) bad.push(l + ' lacks ' + JSON.stringify(t)); }
function eq(l, g, w) { if (g !== w) bad.push(l + ' -> ' + JSON.stringify(g) + ' wanted ' + JSON.stringify(w)); }

['parts/41-tools-a.js', 'parts/42-tools-b.js'].forEach(function (p) {
  if (/=>|`|\blet\b|\bconst\b/.test(rd(p))) bad.push(p + ' not ES5');
  try { new Function(rd(p)); } catch (e) { bad.push(p + ' syntax ' + e.message); }
});

var c = { renders: 0 };
c.render = function () { c.renders++; };
c.document = { getElementById: function () { return null; } };
c.openTool = function (id) { c.W.tool = id; c.render(); };
c.window = c;
vm.createContext(c);
var PARTS = ['parts/10-data.js', 'parts/10-data2-calls.js', 'parts/10-data3-near.js', 'parts/10-data5-toolsA.js',
  'parts/11-charts-c.js', 'parts/30-frame-helpers.js', 'parts/30-pager.js', 'parts/30-steps-0-4.js',
  'parts/41-tools-a.js', 'parts/42-tools-b.js'];
try { PARTS.forEach(function (p) { vm.runInContext(rd(p), c); }); } catch (e) { console.log('FAIL load ' + e.message); process.exit(0); }
function run(s) { return vm.runInContext(s, c); }
['openCallsFor', 'slowestApiFiltered', 'nearbyPanel', 'slowCallsTable', 'selRoutes'].forEach(function (f) {
  if (typeof c[f] !== 'function') bad.push('missing ' + f);
});
if (bad.length) { console.log('FAIL ' + bad.join('; ')); process.exit(0); }

run("W = { ranSteps: true, minCalls: 20, match: '', apiMode: 'all', apiOne: '', slowPg: 0, slowShown: false, nearbyRow: 0, nearbyOpen: false, nearbyPicked: false, nbAround: '2026-10-26T09:14', nbBefore: 5, nbAfter: 5, frame: { incidentStart: '2026-10-26T09:00', incidentEnd: '2026-10-26T10:00', referenceStart: '2026-10-26T07:00', referenceEnd: '2026-10-26T08:00', releases: [] } };");

// --- item 1: every Slowest APIs row button calls openCallsFor('<api>') ---
var sa = c.TOOL_BODIES['slowest-apis']();
eq('sa openCallsFor buttons', cnt(sa, 'onclick="openCallsFor('), 11);
eq('sa old openTool gone', cnt(sa, "onclick=\"openTool('slowest-calls')\""), 0);
has('sa Orders.Create button', sa, 'onclick="openCallsFor(\'Orders.Create\')"');
has('sa Payments button', sa, 'onclick="openCallsFor(\'Payments.Authorize\')"');
// the handler sets the Slowest calls filter to the one API and the table follows
run("openCallsFor('Orders.List');");
eq('of callsApi', run('W.callsApi'), 'Orders.List');
eq('of apiMode untouched', run('W.apiMode'), 'all');
eq('of slowShown', run('W.slowShown'), true);
eq('of slowPg', run('W.slowPg'), 0);
eq('of tool', run('W.tool'), 'slowest-calls');
eq('selRoutes untouched', run('selRoutes().length') > 1, true);

var tbl = run('slowCallsTable(false, [W.callsApi])');
if (tbl.indexOf('<td>Orders.List</td>') < 0) bad.push('calls table lacks Orders.List');
if (tbl.indexOf('<td>Orders.Create</td>') >= 0) bad.push('calls table must list only Orders.List');
run("openCallsFor('Basket.Get');");
var tbl2 = run('slowCallsTable(false, [W.callsApi])');
if (tbl2.indexOf('<td>Basket.Get</td>') < 0 || tbl2.indexOf('<td>Orders.List</td>') >= 0) bad.push('calls table must follow openCallsFor to the new API');

// --- item 2: slowestApiFiltered uses the form rule (not a number or negative -> 20) ---
run("W.match = ''; W.minCalls = 20;");
var f20 = run("slowestApiFiltered().map(function(r){return r[0];}).join(',')");
run("W.minCalls = -5;");
eq('min -5 like 20', run("slowestApiFiltered().map(function(r){return r[0];}).join(',')"), f20);
has('form shows 20 for -5', c.TOOL_BODIES['slowest-apis'](), 'value="20"');
run("W.minCalls = 'x';");
eq('min x like 20', run("slowestApiFiltered().map(function(r){return r[0];}).join(',')"), f20);
has('form shows 20 for x', c.TOOL_BODIES['slowest-apis'](), 'value="20"');
run("W.minCalls = 0;");
eq('min 0 keeps every API', run("slowestApiFiltered().length"), 12);
run("W.minCalls = 5000;");
eq('min 5000 keeps only the big APIs', run("slowestApiFiltered().length"), 3);

// --- item 3: the Nearby header moment ---
function hdr(html) {
  var a = String(html).indexOf('<div class="sthead"><h4>');
  if (a < 0) return '';
  var b = String(html).indexOf('</h4>', a);
  return b < 0 ? '' : String(html).slice(a, b);
}
run("W.nearbyOpen = true; W.nearbyPicked = false; W.nearbyRow = 0; W.nbAround = '2026-10-26T09:14';");
var h0 = hdr(c.nearbyPanel());
has('nearby header around time', h0, '09:14');
if (h0.indexOf('Orders.Create') >= 0) bad.push('nearby header without a chosen call must not name a call: ' + h0);
if (h0.indexOf(' at ') >= 0) bad.push('nearby header without a chosen call must not show the call moment: ' + h0);
run("W.nearbyPicked = true; W.nearbyRow = 3;");
var h1 = hdr(c.nearbyPanel());
has('nearby header picked call', h1, run('CALLS[3][0]'));
has('nearby header picked moment', h1, run('CALLS[3][1]'));
// the tool body leaves a fresh open unsigned and treats a pre-chosen row as picked
run("W.nearbyRow = null; W.nearbyPicked = true; W.nbAround = '2026-10-26T09:14';");
var na = c.TOOL_BODIES['nearby-activity']();
eq('tool marks fresh open not picked', run('W.nearbyPicked'), false);
has('tool header around', hdr(na), '09:14');
run("W.nearbyRow = 5; W.nearbyPicked = false;");
var na2 = c.TOOL_BODIES['nearby-activity']();
eq('tool marks chosen call picked', run('W.nearbyPicked'), true);
has('tool header picked call', hdr(na2), run('CALLS[5][0]'));

// --- wiring greps ---
var s41 = rd('parts/41-tools-a.js');
eq('openCallsFor defined once', cnt(s41, 'function openCallsFor('), 1);
eq('row button calls openCallsFor', cnt(s41, "openCallsFor(\\'"), 1);
eq('old call link gone', cnt(s41, "openTool(\\'slowest-calls\\')"), 0);
eq('min rule shared with the form', cnt(s41, 'if (!(min >= 0)) min = 20;'), 2);
eq('old min rule gone', cnt(s41, 'if (!(min > 0)) min = 0;'), 0);
var s42 = rd('parts/42-tools-b.js');
if (cnt(s42, 'W.nearbyPicked') < 2) bad.push('nearbyPicked must be set in the tool body and read in the panel');
// the packet also updates the two chk-s1A assertions that named the old text
var s1a = rd('../packets/data/chk-s1A.js');
if (s1a.indexOf('onclick="openTool(') >= 0) bad.push('chk-s1A old button assertion still present');
if (s1a.indexOf('onclick="openCallsFor(') < 0) bad.push('chk-s1A button assertion not updated');
if (s1a.indexOf('cnt(s41, "openTool(') >= 0) bad.push('chk-s1A old wire assertion still present');
if (s1a.indexOf('cnt(s41, "openCallsFor(') < 0) bad.push('chk-s1A wire assertion not updated');
console.log(bad.length ? 'FAIL ' + bad.join('; ') : 'PASS');
