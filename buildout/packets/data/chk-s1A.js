// usage (from buildout/wireframes) after apply: node ../packets/data/chk-s1A.js   prints PASS or FAIL <reason>
var fs = require('fs'), vm = require('vm'), bad = [];
function rd(p) { return fs.readFileSync(p, 'utf8'); }
function cnt(s, t) { return String(s).split(t).length - 1; }
var PARTS = ['parts/10-data.js', 'parts/10-data2-calls.js', 'parts/10-data3-near.js', 'parts/10-data5-toolsA.js',
  'parts/11-charts-a.js', 'parts/11-charts-axis.js', 'parts/11-charts-b.js', 'parts/11-charts-c.js',
  'parts/12-evidence-a.js', 'parts/12-evidence-b.js', 'parts/12-evidence-c.js', 'parts/12-evidence-d.js',
  'parts/12-evidence-e.js', 'parts/12-evidence-f.js', 'parts/12-evidence-g.js', 'parts/12-evidence-h.js',
  'parts/13-why-a.js', 'parts/13-why-b.js',
  'parts/14-deps-data.js', 'parts/14-impact-host.js', 'parts/14-impact-mongo.js', 'parts/14-impact-sql.js',
  'parts/14-help-host1.js', 'parts/14-help-host2.js', 'parts/14-help-mongo1.js', 'parts/14-help-mongo2.js',
  'parts/14-help-sql1.js', 'parts/14-help-sql2.js', 'parts/14-scenarios.js',
  'parts/40-manual-home.js', 'parts/41-tools-a.js'];
['parts/10-data5-toolsA.js', 'parts/41-tools-a.js'].forEach(function (p) {
  if (/=>|`|\blet\b|\bconst\b/.test(rd(p))) bad.push(p + ' not ES5');
  try { new Function(rd(p)); } catch (e) { bad.push(p + ' syntax ' + e.message); }
});
var c = { renders: 0 }; c.render = function () { c.renders++; }; c.window = c; vm.createContext(c);
c.dt = function (t, h) { return '<dt>' + t + '</dt><dd>' + h + '</dd>'; };
c.slowCallsTable = function (w) { return '<div class="tscroll">' + 'SLOW-TABLE' + (w ? ' WITH-NEARBY' : '') + '</div>'; };
c.byApiTable = function () { return '<div class="tscroll">BY-API</div>'; };
c.signed = function (n) { n = String(n); return n.charAt(0) === '-' ? n : '+' + n; };
c.partsTable = function () { return '<div class="tscroll">PARTS-T</div>'; };
c.partsBars = function () { return '<div class="tscroll">PARTS-B</div>'; };
try {
  for (var i = 0; i < PARTS.length; i++) vm.runInContext(rd(PARTS[i]), c);
} catch (e) { console.log('FAIL load ' + e.message); process.exit(0); }
function run(s) { return vm.runInContext(s, c); }
function eq(l, g, w) { if (g !== w) bad.push(l + ' -> ' + JSON.stringify(g) + ' wanted ' + JSON.stringify(w)); }
function has(l, s, t) { if (String(s).indexOf(t) < 0) bad.push(l + ' lacks ' + JSON.stringify(t)); }
run('W = { ranSteps: true, have: { audit: true, iis: false, sql: true, mongo: false, host: false }, noted: {}, windowsOpen: {}, tool: "response-time", frame: { incidentStart: "2026-10-26T09:00", incidentEnd: "2026-10-26T10:00", referenceStart: "2026-10-26T08:00", referenceEnd: "2026-10-26T09:00" } };');
['TA_SPLIT', 'TA_MOVED', 'TA_gapShade', 'TA_whyMoved', 'TA_callBars', 'TA_splitByApi'].forEach(function (g) { if (typeof c[g] === 'undefined') bad.push('missing global ' + g); });
eq('split rows', run('TA_SPLIT.length'), 12);
// response-time
var rt = c.TOOL_BODIES['response-time']();
has('rt band', rt, '<rect class="ta-gap" x="487.3" y="4" width="69.0" height="132" rx="2"/>');
eq('rt band count', cnt(rt, '<rect class="ta-gap"'), 1);
eq('rt svg opens', cnt(rt, 'preserveAspectRatio="none">'), 1);
eq('rt lines intact', cnt(rt, 'class="line '), 3);
has('rt gap note', rt, 'The gray band marks the gap 09:31&ndash;09:44 UTC: 14 minutes with no audit records, so the line breaks inside it.');
has('rt by-api', rt, 'BY-API');
has('rt why fold', rt, '<details class="ev-fold"><summary>Why each API moved</summary>');
eq('rt why items', cnt(rt, 'See the evidence'), 3);
eq('rt why links', cnt(rt, "onclick=\"openTool('sql-waits')\""), 3);
['Orders.Create', 'Orders.List', 'Payments.Authorize'].forEach(function (a) { has('rt why row', rt, a); });
has('rt notes', c.TOOL_NOTES['response-time'](), 'Why it moved');
run('W.ranSteps = false;');
var rtOff = c.TOOL_BODIES['response-time']();
has('rt not-run', rtOff, 'This step shows the last run. Run the steps first to fill it in.');
if (rtOff.indexOf('ta-gap') >= 0 || rtOff.indexOf('Why each API moved') >= 0) bad.push('rt not-run must not draw the new parts');
run('W.ranSteps = true;');
// slowest-apis
run('W.minCalls = 20;');
var sa = c.TOOL_BODIES['slowest-apis']();
has('sa form', sa, 'Minimum calls');
eq('sa thead', cnt(sa, '<th>Slow calls</th>'), 1);
eq('sa rows', cnt(sa, '<tr><td>'), 11);
eq('sa call links', cnt(sa, '<button class="quiet" onclick="openCallsFor(\''), 11);
has('sa note', sa, 'Every row links to the individual slow calls behind it, in the Slowest calls tool.');
run('W.minCalls = "5000";');
eq('sa three rows', cnt(c.TOOL_BODIES['slowest-apis'](), '<tr><td>'), 3);
run('W.minCalls = "20"; W.match = "Pay";');
eq('sa pay rows', cnt(c.TOOL_BODIES['slowest-apis'](), '<tr><td>Payments.Authorize</td>'), 1);
run('W.match = "Zzz";');
var saNo = c.TOOL_BODIES['slowest-apis']();
has('sa no match', saNo, 'No API matches "Zzz".');
eq('sa no match rows', cnt(saNo, 'The calls</button>'), 0);
run('W.match = ""; W.ranSteps = false;');
var saOff = c.TOOL_BODIES['slowest-apis']();
has('sa not-run', saOff, 'This step shows the last run. Run the steps first to fill it in.');
eq('sa not-run no thead', cnt(saOff, '<th>Slow calls</th>'), 0);
run('W.ranSteps = true;');
has('sa notes', c.TOOL_NOTES['slowest-apis'](), 'link to the slow calls behind each row');
// slowest-calls
run('W.slowShown = true;');
var sc = c.TOOL_BODIES['slowest-calls']();
has('sc table', sc, 'SLOW-TABLE');
has('sc table note', sc, 'The longest calls that started in the incident window.');
eq('sc bars', cnt(sc, '<rect '), 62);
has('sc caption', sc, 'Each bar is one call. A taller bar is a slower call.');
has('sc note', sc, 'One bar per listed call: the taller the bar, the longer the call took. Read the exact start times in the table.');
run('W.slowShown = false;');
var scOff = c.TOOL_BODIES['slowest-calls']();
eq('sc hidden bars', cnt(scOff, '<rect '), 0);
if (scOff.indexOf('One bar per listed call') >= 0) bad.push('sc must hide the bars before Show');
has('sc notes', c.TOOL_NOTES['slowest-calls'](), 'Duration bars: one bar per listed call');
// time-split
var ts = c.TOOL_BODIES['time-split']();
has('ts pooled table', ts, 'PARTS-T');
has('ts pooled bars', ts, 'PARTS-B');
has('ts iis note', ts, 'The audit part runs. The outside time needs an IIS log source.');
eq('ts picker', cnt(ts, '<select class="wf" onchange="W.taSplitApi=this.value;render()">'), 1);
eq('ts options', cnt(ts, '<option'), 13);
eq('ts rows all', cnt(ts, '<tr><td>'), 12);
has('ts create row', ts, '<tr><td>Orders.Create</td><td>18 &rarr; 22 ms</td><td>240 &rarr; 480 ms</td><td>12 &rarr; 14 ms</td><td>150 &rarr; 1584 ms</td><td>Outside time (O)</td></tr>');
has('ts get row', ts, '<tr><td>Orders.Get</td><td>16 &rarr; 17 ms</td><td>217 &rarr; 223 ms</td><td>11 &rarr; 11 ms</td><td>136 &rarr; 139 ms</td><td>Action time (C)</td></tr>');
eq('ts nothing grew', cnt(ts, '<td>Nothing grew</td>'), 2);
has('ts note', ts, 'Per-API rows: each part of the call, normal against incident. Pick one API, or type to match several; the pooled chart above stays as it is.');
run('W.taSplitApi = "Orders.List";');
var ts1 = c.TOOL_BODIES['time-split']();
eq('ts one rows', cnt(ts1, '<tr><td>'), 1);
has('ts list row', ts1, '<tr><td>Orders.List</td><td>19 &rarr; 10 ms</td><td>257 &rarr; 224 ms</td><td>13 &rarr; 7 ms</td><td>161 &rarr; 739 ms</td><td>Outside time (O)</td></tr>');
run('W.taSplitApi = "all"; W.taSplitMatch = "orders";');
eq('ts several rows', cnt(c.TOOL_BODIES['time-split'](), '<tr><td>'), 3);
run('W.taSplitMatch = "zzz";');
var tsNo = c.TOOL_BODIES['time-split']();
has('ts no match', tsNo, 'No API matches. Clear the picker to see all APIs.');
eq('ts no match rows', cnt(tsNo, '<tr><td>'), 0);
run('W.taSplitMatch = ""; W.ranSteps = false;');
if (c.TOOL_BODIES['time-split']().indexOf('<select') >= 0) bad.push('ts not-run must not draw the picker');
run('W.ranSteps = true;');
has('ts notes', c.TOOL_NOTES['time-split'](), 'API picker');
// wiring greps
var s41 = rd('parts/41-tools-a.js');
eq('wire gap shade', cnt(s41, 'TA_gapShade(minuteLine('), 1);
eq('wire why', cnt(s41, 'TA_whyMoved()'), 1);
eq('wire bars', cnt(s41, 'TA_callBars()'), 1);
eq('wire split', cnt(s41, 'TA_splitByApi();'), 1);
eq('wire ta refs', cnt(s41, 'TA_'), 4);
eq('wire calls link', cnt(s41, "openCallsFor(\\'"), 1);
eq('wire slow col', cnt(s41, '<th>Slow calls</th>'), 1);
var d5 = rd('parts/10-data5-toolsA.js');
eq('data split', cnt(d5, 'var TA_SPLIT'), 1);
eq('data moved', cnt(d5, 'var TA_MOVED'), 1);
eq('data fns', cnt(d5, 'function TA_'), 4);
eq('css', rd('parts/02-css-ta.html'), '<style>.ta-gap{fill:var(--surface-2);stroke:var(--line);stroke-width:1;stroke-dasharray:3 2}</style>\n');
console.log(bad.length ? 'FAIL ' + bad.join('; ') : 'PASS');
