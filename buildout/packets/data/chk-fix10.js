// usage (from buildout/wireframes) after apply: node ../packets/data/chk-fix10.js   prints PASS or FAIL <reason>
var fs = require('fs'), vm = require('vm'), bad = [];
function rd(p) { return fs.readFileSync(p, 'utf8'); }
function cnt(s, t) { return String(s).split(t).length - 1; }
function eq(l, g, w) { if (g !== w) bad.push(l + ' -> ' + JSON.stringify(g) + ' wanted ' + JSON.stringify(w)); }
function has(l, s, t) { if (String(s).indexOf(t) < 0) bad.push(l + ' lacks ' + JSON.stringify(t)); }
function not(l, s, t) { if (String(s).indexOf(t) >= 0) bad.push(l + ' must not contain ' + JSON.stringify(t)); }
function firstRow(html) {
  var t = String(html).split('<tbody>');
  if (t.length < 2) return '';
  var rest = t[1].split('<tr');
  return rest.length > 1 ? '<tr' + rest[1] : '';
}

// --- ES5 scan + syntax of the four edited files ---
var EDITED = ['parts/10-data3-near.js', 'parts/30-steps-0-4.js', 'parts/41-tools-a.js', 'parts/42-tools-b.js'];
EDITED.forEach(function (p) {
  var s = rd(p);
  if (/=>|`|\blet\b|\bconst\b/.test(s)) bad.push(p + ' not ES5');
  try { new Function(s); } catch (e) { bad.push(p + ' syntax ' + e.message); }
});

// --- source wiring greps ---
var s30 = rd('parts/30-steps-0-4.js');
eq('s30 nearbyForApi defined once', cnt(s30, 'function nearbyForApi(api){'), 1);
has('s30 nearbyForApi sets around', s30, "W.nbAround = '2026-10-26T' + CALLS[i][1].slice(0, 5);");
has('s30 nearbyForApi branch', s30, "if (W.mode === 'manual') openTool('nearby-activity'); else go(3);");
eq('s30 byApi blank th', cnt(s30, '<th>Reference P95</th><th>Change</th><th></th></tr></thead>'), 1);
eq('s30 byApi nearby wire', cnt(s30, "nearbyForApi(\\'"), 1);
eq('s30 slow onclick kept', cnt(s30, "W.nearbyOpen=true;W.nearbyPicked=true;W.nbAround="), 1);
eq('s30 slow onclick manual tail', cnt(s30, ";if(W.mode===\\'manual\\')openTool(\\'nearby-activity\\')"), 1);

var s41 = rd('parts/41-tools-a.js');
eq('s41 sa blank th', cnt(s41, '<th>Slow calls</th><th></th></tr></thead>'), 1);
eq('s41 calls button kept', cnt(s41, "openCallsFor(\\'"), 1);
eq('s41 sa nearby wire', cnt(s41, "nearbyForApi(\\'"), 1);
eq('s41 slowest-calls passes true', cnt(s41, 'return callsFilter + slowCallsTable(true, callNames) + (W.slowShown ?'), 1);
eq('s41 old false call gone', cnt(s41, 'slowCallsTable(false)'), 0);
not('s41 stale note gone', s41, 'No Nearby activity column here');
has('s41 new note', s41, 'Nearby activity on a row sets the moment and opens the nearby-activity tool (live)');

var s10n = rd('parts/10-data3-near.js');
eq('s10 lanes array', cnt(s10n, "['API', 'SQL', 'Mongo', 'Host', 'IIS', 'Events']"), 1);
eq('s10 old lanes array gone', cnt(s10n, "['API', 'SQL', 'Mongo', 'Host']"), 0);
has('s10 header comment lanes', s10n, 'src = lane (API, SQL, Mongo, Host, IIS, Events)');
has('s10 header comment empty lanes', s10n, 'The case has no MongoDB, Host, IIS or Windows events source, so those lanes stay empty.');
has('s10 nearView comment six lanes', s10n, 'one swim-lane of all six lanes');
not('s10 old nearView comment gone', s10n, 'all four lanes');

var s42 = rd('parts/42-tools-b.js');
has('s42 lanes note', s42, 'Lanes: API calls, SQL Server, MongoDB, Host, IIS, Windows events.');
not('s42 old lanes note gone', s42, 'Lanes: API calls, SQL Server, MongoDB, Host. No Run bar');

if (bad.length) { console.log('FAIL ' + bad.join('; ')); process.exit(0); }

// --- load the real chain with a stubbed DOM, same approach as chk-fix7.js ---
var c = { renders: 0, openCalls: [] };
c.render = function () { c.renders++; };
c.openTool = function (id) { c.openCalls.push(id); };
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
['nearbyForApi', 'byApiTable', 'slowCallsTable', 'nearView', 'nearbyPanel', 'step2', 'step3', 'slowestApiFiltered'].forEach(function (f) { if (typeof c[f] !== 'function') bad.push('missing ' + f); });
if (typeof c.TOOL_BODIES === 'undefined') bad.push('missing TOOL_BODIES');
if (bad.length) { console.log('FAIL ' + bad.join('; ')); process.exit(0); }

// the whole guided page still renders end to end with the real render()
try { run('render();'); } catch (e) { bad.push('full guided render threw ' + e.message); }
// from here on, count renders instead of building the whole page
c.render = function () { c.renders++; };

// --- item 1: nearbyForApi picks the first (slowest) call of the API ---
var firstIdx = -1;
for (var fi = 0; fi < c.CALLS.length; fi++) if (c.CALLS[fi][0] === 'Orders.Create') { firstIdx = fi; break; }
if (firstIdx < 0) bad.push('fixture has no Orders.Create call');
var firstAround = '2026-10-26T' + String(c.CALLS[firstIdx][1]).slice(0, 5);
run('W.mode = "guided"; W.step = 0; W.openFold = 0; W.flash = "x"; W.nearbyRow = null; W.nearbyPicked = false; W.nearbyOpen = false; W.nbAround = "";');
c.openCalls.length = 0;
c.nearbyForApi('Orders.Create');
eq('guided nearbyRow', run('W.nearbyRow'), firstIdx);
eq('guided nearbyPicked', run('W.nearbyPicked'), true);
eq('guided nearbyOpen', run('W.nearbyOpen'), true);
eq('guided nbAround', run('W.nbAround'), firstAround);
eq('guided step 3 via go', run('W.step'), 3);
eq('guided openFold 3 via go', run('W.openFold'), 3);
eq('guided flash cleared by go', run('W.flash'), '');
eq('guided no openTool', c.openCalls.length, 0);
run('W.mode = "manual"; W.step = 0; W.nearbyRow = null; W.nearbyPicked = false; W.nearbyOpen = false; W.nbAround = "";');
c.openCalls.length = 0;
c.nearbyForApi('Orders.Create');
eq('manual openTool count', c.openCalls.length, 1);
eq('manual openTool id', c.openCalls[0], 'nearby-activity');
eq('manual step untouched', run('W.step'), 0);
eq('manual nearbyRow', run('W.nearbyRow'), firstIdx);
eq('manual nbAround', run('W.nbAround'), firstAround);
run('W.mode = "guided"; W.nearbyRow = 99; W.nearbyOpen = false; W.nbAround = "";');
c.nearbyForApi('No.Such.Api');
eq('unknown api row 0', run('W.nearbyRow'), 0);
eq('unknown api around', run('W.nbAround'), '2026-10-26T' + String(c.CALLS[0][1]).slice(0, 5));

// --- item 2: byApiTable in Step 2 and the Response time tool ---
run('W.showAllApis = false;');
var bt = c.byApiTable(true);
eq('byApi th count', cnt(bt, '<th>'), 6);
has('byApi blank th last', bt, '<th>Change</th><th></th></tr></thead>');
eq('byApi limited rows', cnt(bt, '<tr><td>'), 8);
eq('byApi nearby buttons', cnt(bt, '>Nearby activity</button>'), 8);
eq('byApi header equals cells', cnt(firstRow(bt), '<td'), 6);
has('byApi wired name', bt, 'onclick="nearbyForApi(\'Orders.Create\')"');
var btf = c.byApiTable(false);
eq('byApi all rows', cnt(btf, '<tr><td>'), c.ROUTES.length);
eq('byApi all buttons', cnt(btf, '>Nearby activity</button>'), c.ROUTES.length);
var s2 = c.step2('Ready');
eq('step2 buttons', cnt(s2, '>Nearby activity</button>'), 8);
has('step2 nearby wire', s2, 'onclick="nearbyForApi(\'Orders.Create\')"');
run('W.ranSteps = true;');
var rt = c.TOOL_BODIES['response-time']();
eq('response-time buttons', cnt(rt, '>Nearby activity</button>'), 8);

// --- item 3: the Slowest APIs table ---
run('W.ranSteps = true; W.minCalls = 20; W.match = "";');
var saRows = c.slowestApiFiltered().length;
if (saRows < 1) bad.push('slowest-apis fixture empty');
var sa = c.TOOL_BODIES['slowest-apis']();
eq('sa th count', cnt(sa, '<th>'), 7);
has('sa blank th last', sa, '<th>Slow calls</th><th></th></tr></thead>');
eq('sa rows', cnt(sa, '<tr><td>'), saRows);
eq('sa calls buttons kept', cnt(sa, '>The calls</button>'), saRows);
eq('sa nearby buttons', cnt(sa, '>Nearby activity</button>'), saRows);
eq('sa header equals cells', cnt(firstRow(sa), '<td'), 7);
has('sa wired name', sa, 'onclick="nearbyForApi(\'Orders.Create\')"');

// --- item 4: the Slowest calls tool passes true; Step 3 keeps its behaviour ---
run('W.mode = "guided"; W.apiMode = "all"; W.slowShown = true; W.slowPg = 0;');
var pageSize = run('SLOW_PAGE');
var scRows = run('(function(){ var names = selRoutes().map(function(r){ return r[0]; }); return CALLS.map(function(cc, i){ return [cc, i]; }).filter(function(p){ return names.indexOf(p[0][0]) !== -1; }).length; })()');
var pageRows = Math.min(pageSize, scRows);
var sc = c.TOOL_BODIES['slowest-calls']();
eq('sc nearby buttons', cnt(sc, '>Nearby activity</button>'), pageRows);
eq('sc blank th', cnt(sc, '<th></th>'), 1);
eq('sc header equals cells', cnt(firstRow(sc), '<td'), 4);
has('sc manual openTool in onclick', sc, "if(W.mode==='manual')openTool('nearby-activity')");
var m = /onclick="(W\.nearbyRow=[^"]*)"[^>]*>Nearby activity/.exec(sc);
if (!m) bad.push('slowest-calls row onclick not found');
else {
  run('W.mode = "manual"; W.nearbyRow = null; W.nearbyPicked = false; W.nearbyOpen = false; W.nbAround = "";');
  c.openCalls.length = 0;
  run(m[1]);
  eq('row click picks', run('W.nearbyPicked'), true);
  eq('row click opens', run('W.nearbyOpen'), true);
  eq('row click around', run('W.nbAround'), '2026-10-26T' + String(c.CALLS[run('W.nearbyRow')][1]).slice(0, 5));
  eq('row click openTool count', c.openCalls.length, 1);
  eq('row click openTool id', c.openCalls[0], 'nearby-activity');
}
run('W.mode = "guided"; W.slowShown = true; W.slowPg = 0; W.nearbyOpen = true; W.nearbyRow = ' + firstIdx + '; W.nearbyPicked = true;');
var s3 = c.step3('Ready');
eq('step3 buttons', cnt(s3, '>Nearby activity</button>'), pageRows);
has('step3 close kept', s3, 'onclick="W.nearbyOpen=false;render()">Close</button>');
var s3tbl = c.slowCallsTable(false);
eq('slow table without nearby has no buttons', cnt(s3tbl, '>Nearby activity</button>'), 0);

// --- items 4 and 5: the nearby-activity tool page and the Nearby view cover every lane ---
run('W.mode = "guided"; W.near = { min: 5 }; W.nearbyRow = ' + firstIdx + '; W.nearbyPicked = true;');
var na = c.TOOL_BODIES['nearby-activity']();
has('na picked header', na, 'Orders.Create at ' + c.CALLS[firstIdx][1]);
['API', 'SQL', 'Mongo', 'Host', 'IIS', 'Events'].forEach(function (n) { has('na lane ' + n, na, 'class="axis-label">' + n + '</text>'); });
eq('na lane label count', cnt(na, 'class="axis-label">'), 6);
has('na new note', na, 'MongoDB, Host, IIS and Windows events have no source in this case, so those lanes stay empty.');
not('na old note gone', na, 'MongoDB and Host have no source in this case, so those lanes stay empty.');
var nv = c.nearView();
eq('nv lane label count', cnt(nv, 'class="axis-label">'), 6);
eq('nv events unchanged', cnt(nv, '<circle class="ev') + cnt(nv, '<rect class="ev'), c.NEAR_ROWS.length);
eq('nv IIS lane empty', cnt(nv, '<title>IIS:'), 0);
eq('nv Events lane empty', cnt(nv, '<title>Events:'), 0);
has('nv band note unchanged', nv, 'Events inside the shaded band, 09:12 to 09:17 UTC (7 of 14).');

console.log(bad.length ? 'FAIL ' + bad.join('; ') : 'PASS');
