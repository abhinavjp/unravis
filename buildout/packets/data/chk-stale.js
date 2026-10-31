// usage (from buildout/wireframes) after apply: node ../packets/data/chk-stale.js   prints PASS or FAIL <reason>
var fs = require('fs'), vm = require('vm'), bad = [];
function rd(p) { return fs.readFileSync(p, 'utf8'); }
function cnt(s, t) { return String(s).split(t).length - 1; }
// the five edited files, plus 14-help-mongo2.js which must not be touched at all
var HELP = ['parts/14-help-host1.js', 'parts/14-help-host2.js', 'parts/14-help-mongo1.js', 'parts/14-help-mongo2.js', 'parts/14-help-sql1.js', 'parts/14-help-sql2.js'];
var EDITED = ['parts/14-help-host1.js', 'parts/14-help-host2.js', 'parts/14-help-mongo1.js', 'parts/14-help-sql1.js', 'parts/14-help-sql2.js'];
// one row per edit: file, EVHELP card id, stale clause (must be gone), corrected clause (must appear exactly once)
var EDITS = [
  ['parts/14-help-host1.js', 'GS-01-host-cpu-ok', 'right half the incident hour; no marker line is drawn. The band', 'right half the incident hour; a dashed vertical line marks the incident start. The band'],
  ['parts/14-help-host1.js', 'GS-04-host-cpu-watch', 'right is the incident hour; no marker line is drawn. The band', 'right is the incident hour; a dashed vertical line marks the incident start. The band'],
  ['parts/14-help-host2.js', 'GS-07-host-cpu-red', 'no marker line is drawn. The shaded area is CPU used', 'a dashed vertical line marks the incident start. The shaded area is CPU used'],
  ['parts/14-help-mongo1.js', 'MG-WATCH-GLOCK', 'No marker line is drawn; look for the writer band getting thicker.', 'A dashed vertical line marks the incident start; look for the writer band getting thicker.'],
  ['parts/14-help-sql1.js', 'sql-waits-benign', 'no marker line is drawn here. Each band is one wait type: SLEEP_TASK', 'a dashed vertical line marks the incident start. Each band is one wait type: SLEEP_TASK'],
  ['parts/14-help-sql1.js', 'sql-lock-waits-rising', 'no marker line is drawn here. Each band is one lock wait type', 'a dashed vertical line marks the incident start. Each band is one lock wait type'],
  ['parts/14-help-sql2.js', 'sql-waits-bottleneck', 'no marker line is drawn here. Each band is one wait type, sized', 'a dashed vertical line marks the incident start. Each band is one wait type, sized']
];
// every edited file parses as a function body and stays ES5
EDITED.forEach(function (p) {
  var s = rd(p);
  if (/=>|`|\blet\b|\bconst\b/.test(s)) bad.push(p + ' not ES5');
  try { new Function(s); } catch (e) { bad.push('syntax ' + p + ' ' + e.message); }
});
// each stale clause is gone, each corrected clause appears exactly once, in the raw sources
EDITS.forEach(function (ed) {
  var s = rd(ed[0]);
  if (cnt(s, ed[2]) !== 0) bad.push(ed[0] + ' still has the stale clause for ' + ed[1]);
  if (cnt(s, ed[3]) !== 1) bad.push(ed[0] + ' corrected clause for ' + ed[1] + ' appears ' + cnt(s, ed[3]) + ' times, wanted 1');
});
// no help file may still claim a marker line is not drawn, anywhere
HELP.forEach(function (p) { if (rd(p).toLowerCase().indexOf('no marker line') >= 0) bad.push(p + ' still says no marker line'); });
// load the help files and check the copy inside the live EVHELP data
var c = {}; vm.createContext(c);
try { HELP.forEach(function (p) { vm.runInContext(rd(p), c); }); } catch (e) { console.log('FAIL load ' + e.message); process.exit(0); }
if (!c.EVHELP) bad.push('EVHELP missing after load');
EDITS.forEach(function (ed) {
  var h = c.EVHELP ? c.EVHELP[ed[1]] : null;
  if (!h) { bad.push('EVHELP entry ' + ed[1] + ' missing'); return; }
  if (String(h.chart).indexOf(ed[2]) >= 0) bad.push('EVHELP[' + ed[1] + '].chart still has the stale clause');
  if (String(h.chart).indexOf(ed[3]) < 0) bad.push('EVHELP[' + ed[1] + '].chart lacks the corrected clause');
});
// the corrected claim must be true: every edited card really draws the marker now
vm.runInContext(rd('parts/11-charts-axis.js'), c);
vm.runInContext(rd('parts/14-scenarios.js'), c);
var byId = {};
['sql', 'mongo', 'host'].forEach(function (g) { (c.SCEN && c.SCEN[g] ? c.SCEN[g] : []).forEach(function (k) { byId[k.id] = k; }); });
EDITS.forEach(function (ed) {
  var k = byId[ed[1]];
  if (!k) { bad.push('fixture card ' + ed[1] + ' missing'); return; }
  if (k.chart !== 'stackedArea') { bad.push(ed[1] + ' is not a stackedArea chart'); return; }
  if (typeof c.axIncidentT(k.series) !== 'number') bad.push(ed[1] + ' draws no marker, so the corrected clause would be wrong');
});
// untouched neighbours stay byte-identical
if (cnt(rd('parts/14-help-mongo2.js'), 'the dashed line marks when the incident starts.') !== 1) bad.push('line-chart copy in 14-help-mongo2.js was touched');
if (cnt(rd('parts/12-evidence-f.js'), 'The dashed line marks when the incident starts. ') !== 1) bad.push('caption in 12-evidence-f.js was touched');
console.log(bad.length ? 'FAIL ' + bad.join('; ') : 'PASS');
