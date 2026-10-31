// usage (from buildout/wireframes) after apply: node ../packets/data/chk-cpu.js   prints PASS or FAIL <reason>
var fs = require('fs'), vm = require('vm'), bad = [];
function rd(p) { return fs.readFileSync(p, 'utf8'); }
function cnt(s, t) { return String(s).split(t).length - 1; }
var nf = rd('parts/12-evidence-i.js');
if (/=>|`|\blet\b|\bconst\b/.test(nf)) bad.push('12-evidence-i.js not ES5');
var c = { renders: 0, W: {} };
c.render = function () { c.renders++; };
c.evCopy = function () {};
vm.createContext(c);
['parts/14-deps-data.js', 'parts/12-evidence-g.js', 'parts/12-evidence-h.js', 'parts/12-evidence-i.js', 'parts/12-evidence-c.js', 'parts/14-scenarios.js'].forEach(function (p) {
  try { vm.runInContext(rd(p), c); } catch (e) { bad.push('load ' + p + ' ' + e.message); }
});
if (bad.length) { console.log('FAIL ' + bad.join('; ')); process.exit(0); }
['evQueryWaitsOn', 'evQueryTable'].forEach(function (f) { if (typeof c[f] !== 'function') bad.push('missing ' + f); });
['evQueryId', 'evTable', 'evDepBanner', 'evDepBlocked'].forEach(function (f) { if (typeof c[f] !== 'function') bad.push('missing ' + f); });
var card = null, sql = c.SCEN.sql, i;
for (i = 0; i < sql.length; i++) { if (sql[i].id === 'sql-cpu-high') { card = sql[i]; } }
if (!card) bad.push('sql-cpu-high missing');
if (card) {
  if (String(card.label).indexOf('CPU is high; here are the top waits and queries') < 0) bad.push('label wording');
  if (!card.why || String(card.why.summary).indexOf('CPU is high; here are the top waits and queries') < 0) bad.push('nutshell wording');
  if (Object.prototype.toString.call(card.queries) !== '[object Array]' || card.queries.length !== 4) bad.push('card queries');
}
if (bad.length) { console.log('FAIL ' + bad.join('; ')); process.exit(0); }
// default: qswaits available, Query Store on
var h1 = c.evTable(card);
if (h1.indexOf('Copy query ID') < 0) bad.push('copy action');
if (h1.indexOf('Open the plan with showplan in SSMS or Azure Data Studio using this query ID.') < 0) bad.push('copy text');
function qpart(h) { var i = h.indexOf('Top queries in the incident window'); return i < 0 ? '' : h.slice(i); }
if (qpart(h1).indexOf('<th>Wait type</th>') < 0) bad.push('wait column on');
if (cnt(h1, '<td>') < 4) bad.push('query cells');
if (h1.indexOf('PAGEIOLATCH_SH') < 0) bad.push('wait table');
// qswaits off: omit the column, show the note
c.W.deps = { qswaits: true };
var h2 = c.evTable(card);
if (qpart(h2).indexOf('<th>Wait type</th>') >= 0) bad.push('wait column should be off');
if (h2.indexOf('Per-query wait types require SQL Server 2017+ with Query Store wait stats enabled.') < 0) bad.push('qswaits note');
// Query Store off: banner only
c.W.deps = { querystore: true };
var h3 = c.evTable(card);
if (h3.indexOf('class="ev-dep"') < 0) bad.push('dep banner');
if (h3.indexOf('Query Store is off for this database') < 0) bad.push('dep banner text');
if (h3.indexOf('<table') >= 0) bad.push('table shown with Query Store off');
c.W.deps = {};
if (c.evQueryId('1204').indexOf('Open the plan with showplan in SSMS or Azure Data Studio using this query ID.') < 0) bad.push('evQueryId text');
// wiring
if (cnt(rd('parts/14-deps-data.js'), "'sql-cpu-high': ['querystore', 'qswaits']") !== 1) bad.push('deps map wiring');
if (cnt(rd('parts/33-step7-evidence.js'), "'sql-cpu-high'") !== 1) bad.push('step7 wiring');
if (cnt(rd('parts/12-evidence-c.js'), 'evQueryTable(c)') !== 1) bad.push('evTable wiring');
if (cnt(rd('parts/12-evidence-h.js'), 'Open the plan with showplan in SSMS or Azure Data Studio using this query ID.') !== 1) bad.push('evQueryId wiring');
['parts/12-evidence-i.js', 'parts/12-evidence-c.js', 'parts/12-evidence-h.js', 'parts/14-deps-data.js', 'parts/14-scenarios.js', 'parts/33-step7-evidence.js'].forEach(function (p) {
  try { new Function(rd(p)); } catch (e) { bad.push('syntax ' + p + ' ' + e.message); }
});
console.log(bad.length ? 'FAIL ' + bad.join('; ') : 'PASS');
