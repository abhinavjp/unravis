// usage (from buildout/wireframes) after apply: node ../packets/data/chk-s3.js   prints PASS or FAIL <reason>
var fs = require('fs'), vm = require('vm'), bad = [];
function rd(p) { return fs.readFileSync(p, 'utf8'); }
var src = rd('parts/30-recurrence.js');
if (/=>|`|\blet\b|\bconst\b/.test(src)) bad.push('not ES5');
var c = { renders: 0 }; c.render = function () { c.renders++; }; vm.createContext(c);
try {
  vm.runInContext(rd('parts/11-charts-b.js'), c); vm.runInContext(rd('parts/30-frame-helpers.js'), c); vm.runInContext(src, c);
} catch (e) { console.log('FAIL load ' + e.message); process.exit(0); }
['recurInit', 'recurSet', 'recurMode', 'recurOcc', 'recurSummary', 'recurHeat', 'recurOverlay', 'frameRecurrence'].forEach(function (f) { if (typeof c[f] !== 'function') bad.push('missing ' + f); });
if (bad.length) { console.log('FAIL ' + bad.join('; ')); process.exit(0); }
function run(s) { return vm.runInContext(s, c); }
function eq(l, g, w) { if (g !== w) bad.push(l + ' -> ' + JSON.stringify(g) + ' wanted ' + JSON.stringify(w)); }
function cnt(s, t) { return String(s).split(t).length - 1; }
run('W = { frame: { incidentStart: "2026-10-26T09:00", incidentEnd: "2026-10-26T10:00", referenceStart: "2026-10-26T07:00", referenceEnd: "2026-10-26T08:00", releases: [] } };');
eq('default mode', run('recurInit().mode'), 'one');
var one = c.frameRecurrence();
['One window', 'Same hours on the previous days', 'Same weekday on previous weeks', 'Last Thursday of every month', 'The reference window above is used as it is.', 'role="radiogroup"'].forEach(function (t) { if (one.indexOf(t) < 0) bad.push('mode one lacks ' + t); });
if (one.indexOf('<svg') >= 0) bad.push('mode one must not draw charts');
c.recurMode('days');
eq('days list', run('recurOcc().list.length'), 6);
eq('days first', run('new Date(recurOcc().list[0]).toISOString().slice(0,10)'), '2026-10-25');
run('recurSet("n", "99")');
eq('days clamp', run('recurOcc().n'), 30);
run('recurSet("n", "6")');
c.recurMode('weeks');
eq('weeks dates', run('recurOcc().list.slice(0,3).map(function(m){return new Date(m).toISOString().slice(0,10)}).join(",")'), '2026-10-22,2026-10-15,2026-10-08');
c.recurMode('month');
eq('month dates', run('recurOcc().list.map(function(m){return new Date(m).toISOString().slice(0,10)}).join(",")'), '2026-09-24,2026-08-27,2026-07-30,2026-06-25,2026-05-28,2026-04-30');
eq('month summary', c.recurSummary(c.recurOcc()), '<p class="note" role="status">Comparing 09:00&ndash;10:00 UTC on the last Thursday of the month, for 6 months (30 Apr 2026 to 24 Sep 2026).</p>');
var h = c.recurHeat(c.recurOcc());
eq('month hit cells', cnt(h, 'class="cell hit dash '), 1);
if (h.indexOf('Darker red = slower than normal for that hour. Light = close to normal. Gray = no data yet for that hour.') < 0) bad.push('legend text missing');
if (h.indexOf('Dashed border: the hours the rule matches, on only one such weekday a month (the last).') < 0) bad.push('dashed note missing');
c.recurMode('days');
eq('days hit cells', cnt(c.recurHeat(c.recurOcc()), 'class="cell hit '), 7);
run('recurSet("en", "11:30")');
eq('days hit cells 2h', cnt(c.recurHeat(c.recurOcc()), 'class="cell hit '), 21);
run('recurSet("en", "10:00")');
c.recurMode('weeks'); run('recurSet("wd", "4")');
eq('weeks hit cells', cnt(c.recurHeat(c.recurOcc()), 'class="cell hit '), 1);
eq('weeks hit not dashed', cnt(c.recurHeat(c.recurOcc()), 'hit dash'), 0);
// overlay cap
c.recurMode('days'); run('recurSet("n", "10")');
var ov = c.recurOverlay(c.recurOcc());
eq('overlay lines', cnt(ov, '<polyline'), 8);
if (ov.indexOf('Showing the most recent 8 of 10 occurrences; all 10 appear in the heatmap.') < 0) bad.push('cap note missing');
if (ov.indexOf('<details class="ev-fold"><summary>Occurrence dates</summary>') < 0) bad.push('dates fold missing');
eq('overlay dates', cnt(ov, '<li>'), 10);
run('recurSet("n", "5")');
ov = c.recurOverlay(c.recurOcc());
eq('overlay 5 lines', cnt(ov, '<polyline'), 5);
if (ov.indexOf('Showing the most recent') >= 0) bad.push('cap note must be absent for 5');
// invalid input
run('recurSet("en", "08:00")');
eq('end before start', run('recurOcc().msg'), 'The end time must be after the start time.');
if (c.frameRecurrence().indexOf('role="alert"') < 0) bad.push('alert missing');
run('recurSet("st", "9am")');
eq('bad time', run('recurOcc().msg'), 'Enter the start and end as HH:MM.');
// wiring
var s = rd('parts/30-steps-0-4.js');
eq('wire form', cnt(s, "  + (typeof frameRecurrence === 'function' ? frameRecurrence() : '')"), 1);
eq('wire note', cnt(s, "dt('Repeat pattern'"), 1);
try { new Function(s); } catch (x) { bad.push('syntax ' + x.message); }
if (rd('parts/02-css-recur.html').indexOf('rect.cell.hit.dash') < 0) bad.push('css missing');
console.log(bad.length ? 'FAIL ' + bad.join('; ') : 'PASS');
