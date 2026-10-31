// usage (from buildout/wireframes) after apply: node ../packets/data/chk-axis.js   prints PASS or FAIL <reason>
var fs = require('fs'), vm = require('vm'), bad = [];
function rd(p) { return fs.readFileSync(p, 'utf8'); }
['parts/11-charts-axis.js', 'parts/11-charts-a.js', 'parts/12-evidence-a.js', 'parts/12-evidence-b.js', 'parts/12-evidence-c.js'].forEach(function (p) {
  var s = rd(p);
  if (/=>|`|\blet\b|\bconst\b/.test(s)) bad.push(p + ' not ES5');
  try { new Function(s); } catch (x) { bad.push(p + ' syntax ' + x.message); }
});
var c = { renders: 0 }; c.render = function () { c.renders++; };
vm.createContext(c);
try {
  vm.runInContext(rd('parts/11-charts-a.js'), c);
  vm.runInContext(rd('parts/11-charts-axis.js'), c);
  vm.runInContext(rd('parts/12-evidence-a.js'), c);
  vm.runInContext(rd('parts/12-evidence-b.js'), c);
  vm.runInContext(rd('parts/12-evidence-f.js'), c);
  vm.runInContext(rd('parts/12-evidence-c.js'), c);
  vm.runInContext(rd('parts/14-scenarios.js'), c);
} catch (e) { console.log('FAIL load ' + e.message); process.exit(0); }
['axUnit', 'axFmt', 'axYLabels', 'axXLabels', 'axIncidentT', 'axIncidentLine', 'evAreaCols', 'tlChart', 'stackedArea', 'evBars', 'evChart'].forEach(function (f) { if (typeof c[f] !== 'function') bad.push('missing ' + f); });
if (bad.length) { console.log('FAIL ' + bad.join('; ')); process.exit(0); }
function run(s) { return vm.runInContext(s, c); }
function eq(l, g, w) { if (g !== w) bad.push(l + ' -> ' + JSON.stringify(g) + ' wanted ' + JSON.stringify(w)); }
function cnt(s, t) { return String(s).split(t).length - 1; }
function has(l, s, t) { if (String(s).indexOf(t) < 0) bad.push(l + ' lacks ' + t); }
run('function findC(id){ var g=["sql","mongo","host"]; for (var i=0;i<g.length;i++){ var l=SCEN[g[i]]; for (var j=0;j<l.length;j++){ if (l[j].id===id) return l[j]; } } return null; }');
// helper formatting and unit rules
eq('fmt zero', run('axFmt(0, "ms")'), '0 ms');
eq('fmt k', run('axFmt(1200, "ms")'), '1.2k ms');
eq('fmt k rounds', run('axFmt(1260, "")'), '1.3k');
eq('fmt decimal', run('axFmt(47.5, "%")'), '47.5 %');
eq('fmt whole k', run('axFmt(2000, "")'), '2k');
eq('unit parens', run('axUnit([{name:"p95 op latency (ms)"},{name:"p95 op latency (ms)"}])'), 'ms');
eq('unit pct tail', run('axUnit([{name:"Log used %"}])'), '%');
eq('unit mixed', run('axUnit([{name:"a (ms)"},{name:"b (s)"}])'), '');
eq('unit absent', run('axUnit([{name:"SLEEP_TASK"},{name:"LAZYWRITER"}])'), '');
eq('unit not array', run('axUnit(null)'), '');
eq('cols from fixture', run('evAreaCols(findC("sql-waits-benign").series).join(",")'), '0,30,60,90,119');
eq('incT fixture', run('axIncidentT(findC("sql-waits-benign").series)'), 60);
eq('incT none', run('axIncidentT([{win:"normal",pts:[{t:5,v:1}]}])'), null);
// line chart, real fixture MG-BAD-SLOWOPS (unit ms, max 210, incident from t 60)
run('var LC = findC("MG-BAD-SLOWOPS"); var LH = evChart(LC);');
eq('line label count', cnt(c.LH, 'class="axis-label"'), 6);
var mx = (4 + (60 / 119) * 632).toFixed(1);
has('line y max', c.LH, '<text class="axis-label" x="6" y="13.0">210 ms</text>');
has('line y mid', c.LH, '<text class="axis-label" x="6" y="67.0">105 ms</text>');
has('line y zero', c.LH, '<text class="axis-label" x="6" y="134.0">0 ms</text>');
has('line x first', c.LH, '<text class="axis-label" x="4.0" y="122.0" text-anchor="start">+0 min</text>');
has('line x mid', c.LH, '<text class="axis-label" x="' + mx + '" y="122.0" text-anchor="middle">+60 min</text>');
has('line x last', c.LH, '<text class="axis-label" x="636.0" y="122.0" text-anchor="end">+119 min</text>');
eq('line marker count', cnt(c.LH, 'class="marker"'), 1);
has('line marker line', c.LH, '<line x1="' + mx + '" y1="4" x2="' + mx + '" y2="136" class="marker"/>');
has('line marker label', c.LH, 'Incident starts');
has('line legend kept', c.LH, 'A break in a line is a minute with no data.');
has('line caption kept', c.LH, '<p class="ev-cap">');
// stacked area, real fixture sql-waits-benign (no unit, cols 0,30,60,90,119)
run('var AC = findC("sql-waits-benign"); var AH = evChart(AC);');
eq('area label count', cnt(c.AH, 'class="axis-label"'), 6);
run('var AS = evAreaSeries(AC.series); var AT = 0; for (var ci=0; ci<AS[0].vals.length; ci++){ var tt=0; for (var sj=0; sj<AS.length; sj++){ tt += AS[sj].vals[ci]; } if (tt>AT) AT=tt; }');
var acols = run('evAreaCols(AC.series)');
var ai = acols.indexOf(60);
var axx = (4 + (ai / (acols.length - 1)) * 632).toFixed(1);
has('area y max', c.AH, '<text class="axis-label" x="6" y="13.0">' + run('axFmt(AT, "")') + '</text>');
has('area y mid', c.AH, '<text class="axis-label" x="6" y="67.0">' + run('axFmt(AT / 2, "")') + '</text>');
has('area y zero', c.AH, '<text class="axis-label" x="6" y="134.0">0</text>');
has('area x first', c.AH, '<text class="axis-label" x="4.0" y="122.0" text-anchor="start">+0 min</text>');
has('area x mid', c.AH, '>+60 min</text>');
has('area x last', c.AH, '<text class="axis-label" x="636.0" y="122.0" text-anchor="end">+119 min</text>');
eq('area marker count', cnt(c.AH, 'class="marker"'), 1);
has('area marker line', c.AH, '<line x1="' + axx + '" y1="4" x2="' + axx + '" y2="136" class="marker"/>');
has('area legend kept', c.AH, '<p class="legend">');
// bars, real fixture sql-topqueries-orders (no unit, max 190)
run('var BC = findC("sql-topqueries-orders"); var BH = evChart(BC);');
eq('bars label count', cnt(c.BH, 'class="axis-label"'), 6);
run('var BB = []; BC.series.forEach(function (s) { (s.pts || []).forEach(function (p) { if (typeof p.t === "number" && typeof p.v === "number" && !isNaN(p.t) && !isNaN(p.v)) BB.push({ win: s.win, t: p.t, v: p.v }); }); });');
var nb = c.BB.length;
var sw = 632 / nb;
var midB = Math.floor((nb - 1) / 2);
has('bars y max', c.BH, '<text class="axis-label" x="6" y="13.0">190</text>');
has('bars y mid', c.BH, '<text class="axis-label" x="6" y="67.0">95</text>');
has('bars y zero', c.BH, '<text class="axis-label" x="6" y="134.0">0</text>');
has('bars x first', c.BH, '<text class="axis-label" x="' + (4 + sw / 2).toFixed(1) + '" y="122.0" text-anchor="start">+0 min</text>');
has('bars x mid', c.BH, '<text class="axis-label" x="' + (4 + midB * sw + sw / 2).toFixed(1) + '" y="122.0" text-anchor="middle">+' + c.BB[midB].t + ' min</text>');
has('bars x last', c.BH, '<text class="axis-label" x="' + (4 + (nb - 1) * sw + sw / 2).toFixed(1) + '" y="122.0" text-anchor="end">+119 min</text>');
var bi = -1;
for (var q = 0; q < c.BB.length; q++) { if (c.BB[q].win === 'incident') { bi = q; break; } }
var bx = (4 + bi * sw).toFixed(1);
eq('bars marker count', cnt(c.BH, 'class="marker"'), 1);
has('bars marker line', c.BH, '<line x1="' + bx + '" y1="4" x2="' + bx + '" y2="136" class="marker"/>');
has('bars legend kept', c.BH, '<span class="key normal"></span> Normal <span class="key incident"></span> Incident');
// unit plumbing for bars and stacked area (no real fixture has a unit for these kinds)
run('var SYB = evChart({ chart: "bars", series: [{ name: "lag (ms)", win: "normal", pts: [{ t: 0, v: 10 }, { t: 30, v: 20 }] }, { name: "lag (ms)", win: "incident", pts: [{ t: 60, v: 1200 }, { t: 90, v: 900 }] }] });');
has('syn bars y max', c.SYB, '<text class="axis-label" x="6" y="13.0">1.2k ms</text>');
has('syn bars y mid', c.SYB, '<text class="axis-label" x="6" y="67.0">600 ms</text>');
has('syn bars y zero', c.SYB, '<text class="axis-label" x="6" y="134.0">0 ms</text>');
eq('syn bars marker', cnt(c.SYB, 'class="marker"'), 1);
run('var SYA = evChart({ chart: "stackedArea", series: [{ name: "cpu (%)", win: "normal", pts: [{ t: 0, v: 30 }, { t: 30, v: 40 }] }, { name: "cpu (%)", win: "incident", pts: [{ t: 60, v: 50 }, { t: 119, v: 60 }] }] });');
has('syn area y max', c.SYA, '<text class="axis-label" x="6" y="13.0">60 %</text>');
has('syn area y mid', c.SYA, '<text class="axis-label" x="6" y="67.0">30 %</text>');
has('syn area y zero', c.SYA, '<text class="axis-label" x="6" y="134.0">0 %</text>');
eq('syn area marker', cnt(c.SYA, 'class="marker"'), 1);
// no-data and no-incident behaviour
run('var ND1 = evChart({ chart: "line", series: [] });');
eq('line empty note first', c.ND1.indexOf('<p class="note">Not enough data to draw this chart.</p>'), 0);
eq('line empty no labels', cnt(c.ND1, 'class="axis-label"'), 0);
eq('line empty no marker', cnt(c.ND1, 'class="marker"'), 0);
run('var ND2 = evChart({ chart: "stackedArea", series: [] });');
eq('area empty note first', c.ND2.indexOf('<p class="note">Not enough data to draw this chart.</p>'), 0);
eq('area empty no labels', cnt(c.ND2, 'class="axis-label"'), 0);
run('var ND3 = evChart({ chart: "bars", series: [] });');
eq('bars empty note first', c.ND3.indexOf('<p class="note">Not enough data to draw this chart.</p>'), 0);
eq('bars empty no labels', cnt(c.ND3, 'class="axis-label"'), 0);
run('var ND4 = evChart({ chart: "none" });');
eq('none text', c.ND4, '<p class="note">This was not collected for this case, so there is no chart.</p>');
run('var NI = evChart({ chart: "line", series: [{ name: "a (ms)", win: "normal", pts: [{ t: 0, v: 5 }, { t: 30, v: 8 }] }] });');
eq('line no incident marker', cnt(c.NI, 'class="marker"'), 0);
eq('line no incident labels', cnt(c.NI, 'class="axis-label"'), 6);
run('var NB = evChart({ chart: "bars", series: [{ name: "a (ms)", win: "normal", pts: [{ t: 0, v: 5 }, { t: 30, v: 8 }] }] });');
eq('bars no incident marker', cnt(c.NB, 'class="marker"'), 0);
run('var NA2 = evChart({ chart: "stackedArea", series: [{ name: "a (ms)", win: "normal", pts: [{ t: 0, v: 5 }, { t: 30, v: 8 }, { t: 60, v: 6 }] }] });');
eq('area no incident marker', cnt(c.NA2, 'class="marker"'), 0);
eq('area no incident labels', cnt(c.NA2, 'class="axis-label"'), 6);
// wiring
var sa = rd('parts/11-charts-a.js');
eq('tl sig', cnt(sa, 'function tlChart(series, markers, unit) {'), 1);
eq('area sig', cnt(sa, 'function stackedArea(series, pct, unit, colsT, incT) {'), 1);
eq('y wire charts-a', cnt(sa, "axYLabels(top, unit || '', W, H, PAD)"), 2);
eq('x wire tl', cnt(sa, 'axXLabels([{ t: 0, x: x(0) }, { t: 60, x: x(60) }, { t: TMAX, x: x(TMAX) }], H, PAD)'), 1);
eq('marker wire area', cnt(sa, 'ax += axIncidentLine(x(mi), PAD, H - PAD);'), 1);
var ea2 = rd('parts/12-evidence-a.js');
eq('cols fn', cnt(ea2, 'function evAreaCols(series) {'), 1);
var eb2 = rd('parts/12-evidence-b.js');
eq('bars sig', cnt(eb2, 'function evBars(series, unit, incT) {'), 1);
eq('y wire bars', cnt(eb2, "axYLabels(top, unit || '', W, H, PAD)"), 1);
eq('marker wire bars', cnt(eb2, 'ax += axIncidentLine(PAD + bi * sw, PAD, H - PAD);'), 1);
var ec2 = rd('parts/12-evidence-c.js');
eq('ev unit wire', cnt(ec2, 'axUnit(c.series)'), 3);
eq('ev incT wire', cnt(ec2, 'axIncidentT(c.series)'), 3);
eq('ev cols wire', cnt(ec2, 'evAreaCols(c.series)'), 1);
eq('ev old marker gone', cnt(ec2, "[{t:60, label:'Incident starts'}]"), 0);
eq('rollup kept', cnt(ec2, "(typeof fpRollupAny === 'function' ? fpRollupAny() : '')"), 2);
console.log(bad.length ? 'FAIL ' + bad.join('; ') : 'PASS');
