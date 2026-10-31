// usage (from buildout/wireframes) after apply: node ../packets/data/chk-s1C.js   prints PASS or FAIL <reason>
var fs = require('fs'), vm = require('vm'), bad = [];
function rd(p) { try { return fs.readFileSync(p, 'utf8'); } catch (e) { console.log('FAIL cannot read ' + p + ': ' + e.message); process.exit(0); } }
function cnt(s, t) { return String(s).split(t).length - 1; }
function eq(l, g, w) { if (g !== w) bad.push(l + ' -> ' + JSON.stringify(g) + ' wanted ' + JSON.stringify(w)); }
function has(l, s, t) { if (String(s).indexOf(t) < 0) bad.push(l + ' lacks ' + JSON.stringify(t)); }
function not(l, s, t) { if (String(s).indexOf(t) >= 0) bad.push(l + ' still has ' + JSON.stringify(t)); }
var d5 = rd('parts/10-data5-toolsC.js'), s43 = rd('parts/43-tools-c.js');
// ES5 scan of the new part and the edited file
[['parts/10-data5-toolsC.js', d5], ['parts/43-tools-c.js', s43]].forEach(function (p) {
  if (/=>|`|\blet\b|\bconst\b/.test(p[1])) bad.push(p[0] + ' not ES5');
});
// every top-level global in the new data part is TC-prefixed
(d5.match(/^(?:var|function) [A-Za-z_$][\w$]*/gm) || []).forEach(function (d) {
  var n = d.replace(/^(?:var|function) /, '');
  if (n.indexOf('TC') !== 0) bad.push('10-data5 global not TC-prefixed: ' + n);
});
// wiring greps on 43-tools-c.js
eq('wire mongo cards', cnt(s43, "(typeof TCcards === 'function' ? TCcards(TC_MONGO_IDS) : '')"), 1);
eq('wire host cards', cnt(s43, "(typeof TCcards === 'function' ? TCcards(TC_HOST_IDS) : '')"), 1);
eq('wire suspect evidence', cnt(s43, "(typeof TCsuspectEvidence === 'function' ? TCsuspectEvidence() : '')"), 1);
eq('wire sql-host link', cnt(s43, "openTool(" + String.fromCharCode(92) + "'host-resources" + String.fromCharCode(92) + "')"), 1);
eq('why guard x3', cnt(s43, "if (W.why && typeof evFind === 'function' && typeof whyView === 'function') {"), 3);
eq('mongo dummy gone', cnt(s43, 'dummy; the repo has no Mongo table component'), 0);
eq('host dummy gone', cnt(s43, 'dummy; the repo has no host-resource table component'), 0);
eq('sql-host dummy gone', cnt(s43, 'dummy; the repo has no sql-host table component'), 0);
eq('placeholder note gone', cnt(s43, 'labelled placeholder'), 0);
eq('after-fix untouched', cnt(s43, 'return afterFixBody();'), 1);
try { new Function(d5); new Function(s43); } catch (x) { bad.push('syntax ' + x.message); }
// load parts 10-14 + 40 + 43-tools-c.js, with stubs for the step helpers the tool bodies use
var c = {
  renders: 0,
  render: function () { c.renders++; },
  dt: function (t, h) { return '<dt>' + t + '</dt><dd>' + h + '</dd>'; },
  findingsList: function (items) { return '<div class="findings-stub">' + items.join('|') + '</div>'; },
  suspectsHere: function (ids) { return '<div class="suspects-stub">' + ids.join(',') + '</div>'; },
  originContent: function () { return '<div class="origin-stub">ORIGIN</div>'; },
  afterFixBody: function () { return '<div class="afterfix-stub">AFTERFIX</div>'; }
};
c.window = c;
vm.createContext(c);
var names = fs.readdirSync('parts').filter(function (n) { return /^1[0-4]-.*\.js$/.test(n); }).sort();
names.push('40-manual-home.js', '43-tools-c.js');
try { names.forEach(function (n) { vm.runInContext(rd('parts/' + n), c); }); }
catch (e) { console.log('FAIL load ' + e.message); process.exit(0); }
['TC_MONGO_IDS', 'TC_HOST_IDS', 'TC_SUSPECT_CARDS', 'TCcards', 'TCsuspectEvidence'].forEach(function (g) {
  if (typeof c[g] === 'undefined') bad.push('missing global ' + g);
});
['mongo-health', 'host-resources', 'sql-host', 'after-fix', 'suspect-summary'].forEach(function (t) {
  if (typeof c.TOOL_BODIES[t] !== 'function') bad.push('missing body ' + t);
  if (typeof c.TOOL_NOTES[t] !== 'function') bad.push('missing notes ' + t);
});
if (bad.length) { console.log('FAIL ' + bad.join('; ')); process.exit(0); }
function run(s) { return vm.runInContext(s, c); }
try {
  run('W = { ranSteps: true, why: null, have: { audit: true, iis: true, sql: true, mongo: true, host: true }, frame: { incidentStart: "2026-10-26T09:00", incidentEnd: "2026-10-26T10:00", referenceStart: "2026-10-26T07:00", referenceEnd: "2026-10-26T08:00", releases: [] } };');
  eq('mongo id count', run('TC_MONGO_IDS.length'), 7);
  eq('host id count', run('TC_HOST_IDS.length'), 6);
  eq('suspect pair count', run('TC_SUSPECT_CARDS.length'), 1);
  eq('suspect pair', run('TC_SUSPECT_CARDS[0].join(">")'), 'D1>sql-blocking-chain');
  eq('mongo ids resolve', run('TC_MONGO_IDS.every(function(id){ return !!evFind(id); })'), true);
  eq('host ids resolve', run('TC_HOST_IDS.every(function(id){ return !!evFind(id); })'), true);
  eq('suspect ids resolve', run('TC_SUSPECT_CARDS.every(function(p){ return !!evFind(p[1]); })'), true);
  eq('TCcards skips unknown', run('TCcards(["NO-SUCH", "MG-OK-REPLAG"])').split('<section class="ev ').length - 1, 1);
  has('TCcards empty note', run('TCcards(["NO-SUCH"])'), 'No evidence card is available for this tool.');

  var mh = run("TOOL_BODIES['mongo-health']()");
  has('mongo lead', mh, 'Checks on MongoDB: slow operations, queues and tickets, replication lag, cache eviction and connections.');
  has('mongo findings kept', mh, 'No slow operation above 100 ms was recorded in the window.');
  has('mongo profiler copy', mh, 'profiler and currentOp');
  has('mongo mongostat copy', mh, 'mongostat and mongotop');
  has('mongo rs.status copy', mh, 'rs.status');
  eq('mongo card count', cnt(mh, '<section class="ev '), 7);
  has('mongo slow ops card', mh, 'Slow ops by namespace (Orders.Create)');
  has('mongo tickets card', mh, 'WiredTiger read/write tickets');
  has('mongo replag card', mh, 'Replication lag');
  eq('mongo not-collected chips', cnt(mh, 'ev-chip none">Not collected<'), 2);
  has('mongo legend', mh, 'ev-legend');
  has('mongo why buttons', mh, 'Why this colour?');
  has('mongo suspect', mh, '<div class="suspects-stub">M1</div>');
  not('mongo no dummy', mh, 'dummy');
  not('mongo no bare table', mh, '<table>');
  eq('mongo why drill', run("W.why = 'MG-OK-REPLAG'; TOOL_BODIES['mongo-health']()").indexOf('&larr; Back to the evidence') >= 0, true);
  eq('mongo why miss clears', run("W.why = 'no-such-card'; TOOL_BODIES['mongo-health'](); W.why"), null);
  run('W.why = null; W.ranSteps = false;');
  has('mongo not run', run("TOOL_BODIES['mongo-health']()"), 'Run the steps first to fill it in.');
  run('W.ranSteps = true;');

  var hr = run("TOOL_BODIES['host-resources']()");
  has('host lead', hr, 'Checks on the hosts: processor, memory and swap, disk and network.');
  has('host collectors copy', hr, 'iostat, vmstat, sar and netstat -s');
  eq('host card count', cnt(hr, '<section class="ev '), 6);
  has('host cpu card', hr, 'CPU busy time is normal');
  has('host disk card', hr, 'Disk is fast with no queue');
  has('host mem card', hr, 'Memory has headroom');
  has('host swap card', hr, 'Swap activity (not collected)');
  has('host tcp card', hr, 'TCP connection counts (not collected)');
  has('host runqueue card', hr, 'CPU run queue (not collected)');
  eq('host not-collected chips', cnt(hr, 'ev-chip none">Not collected<'), 3);
  has('host suspect', hr, '<div class="suspects-stub">H1</div>');
  not('host no dummy', hr, 'dummy');
  not('host no bare table', hr, '<table>');
  eq('host why drill', run("W.why = 'GS-02-host-disk-ok'; TOOL_BODIES['host-resources']()").indexOf('&larr; Back to the evidence') >= 0, true);
  run('W.why = null;');

  var sh = run("TOOL_BODIES['sql-host']()");
  has('sql-host lead', sh, 'This is the same Blackbox source as Host CPU, memory and disk; Step 9 reads both hosts from it.');
  has('sql-host dedupe copy', sh, 'There is no separate SQL-host evidence set');
  has('sql-host collectors copy', sh, 'iostat, vmstat, sar and netstat -s');
  has('sql-host open button', sh, 'onclick="openTool(\'host-resources\')"');
  has('sql-host suspect', sh, '<div class="suspects-stub">D1</div>');
  not('sql-host no table', sh, '<table');
  not('sql-host no dummy', sh, 'dummy');

  eq('after-fix passthrough', run("TOOL_BODIES['after-fix']()"), '<div class="afterfix-stub">AFTERFIX</div>');
  run('W.ranSteps = false;');
  has('after-fix not run', run("TOOL_BODIES['after-fix']()"), 'Run the steps first. The comparison needs the incident day.');
  run('W.ranSteps = true;');

  var ss = run("TOOL_BODIES['suspect-summary']()");
  has('suspect origin kept', ss, '<div class="origin-stub">ORIGIN</div>');
  has('suspect section', ss, 'The evidence behind the colours');
  has('suspect section note', ss, 'One card per suspect the sources could test');
  has('suspect card label', ss, 'Head blocker / blocking chains');
  eq('suspect bad card count', cnt(ss, '<section class="ev bad"'), 1);
  has('suspect why button', ss, 'Why this colour?');
  eq('suspect why drill', run("W.why = 'sql-blocking-chain'; TOOL_BODIES['suspect-summary']()").indexOf('&larr; Back to the evidence') >= 0, true);
  eq('suspect why miss clears', run("W.why = 'no-such-card'; TOOL_BODIES['suspect-summary'](); W.why"), null);
  run('W.why = null; W.ranSteps = false;');
  has('suspect not run', run("TOOL_BODIES['suspect-summary']()"), 'Run the steps first to fill it in.');
  run('W.ranSteps = true;');

  has('notes mongo chart', run("TOOL_NOTES['mongo-health']()"), 'One evidence card per measure');
  has('notes mongo buttons', run("TOOL_NOTES['mongo-health']()"), 'Why this colour? on each evidence card (live).');
  has('notes host ids', run("TOOL_NOTES['host-resources']()"), 'TC_HOST_IDS');
  has('notes sql-host dedupe', run("TOOL_NOTES['sql-host']()"), 'No evidence set of its own');
  has('notes suspect cards', run("TOOL_NOTES['suspect-summary']()"), 'TC_SUSPECT_CARDS');
  has('notes after-fix unchanged', run("TOOL_NOTES['after-fix']()"), 'classifyDay');
} catch (e) { bad.push('run ' + e.message); }
// typeof guards: with no evidence helpers loaded, nothing throws and no cards render
try {
  var bare = { render: function () {}, dt: c.dt, findingsList: c.findingsList, suspectsHere: c.suspectsHere, originContent: c.originContent, afterFixBody: c.afterFixBody };
  bare.window = bare;
  vm.createContext(bare);
  vm.runInContext(d5, bare);
  vm.runInContext(s43, bare);
  eq('guard TCcards', vm.runInContext("TCcards(TC_MONGO_IDS)", bare), '');
  vm.runInContext('W = { ranSteps: true, why: null };', bare);
  var gmh = vm.runInContext("TOOL_BODIES['mongo-health']()", bare);
  eq('guard mongo renders no cards', cnt(gmh, '<section class="ev '), 0);
  has('guard mongo keeps findings', gmh, 'No slow operation above 100 ms was recorded');
  var gss = vm.runInContext("TOOL_BODIES['suspect-summary']()", bare);
  has('guard suspect section still shows', gss, 'The evidence behind the colours');
  eq('guard suspect no cards', cnt(gss, '<section class="ev '), 0);
} catch (e) { bad.push('guard ' + e.message); }
console.log(bad.length ? 'FAIL ' + bad.join('; ') : 'PASS');
