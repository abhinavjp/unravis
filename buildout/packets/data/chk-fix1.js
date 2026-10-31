// usage (from buildout/wireframes) after apply: node ../packets/data/chk-fix1.js   prints PASS or FAIL <reason>
var fs = require('fs'), vm = require('vm'), bad = [];
function rd(p) { return fs.readFileSync(p, 'utf8'); }
function cnt(s, t) { return String(s).split(t).length - 1; }
function has(l, s, t) { if (String(s).indexOf(t) < 0) bad.push(l + ' lacks ' + JSON.stringify(t)); }
function not(l, s, t) { if (String(s).indexOf(t) >= 0) bad.push(l + ' must not contain ' + JSON.stringify(t)); }
function eq(l, g, w) { if (g !== w) bad.push(l + ' -> ' + JSON.stringify(g) + ' wanted ' + JSON.stringify(w)); }

var EDITED = ['parts/11-charts-a.js', 'parts/11-charts-b.js', 'parts/11-charts-c.js', 'parts/12-evidence-b.js', 'parts/12-evidence-f.js', 'parts/14-scenarios.js'];
EDITED.forEach(function (p) {
  var s = rd(p);
  if (/=>|`|\blet\b|\bconst\b/.test(s)) bad.push(p + ' not ES5');
  try { new Function(s); } catch (x) { bad.push(p + ' syntax ' + x.message); }
});

var LOAD = ['11-charts-axis.js', '11-charts-a.js', '11-charts-b.js', '11-charts-c.js', '12-evidence-a.js', '12-evidence-b.js', '12-evidence-f.js', '12-evidence-c.js', '14-scenarios.js'];
var c = { renders: 0 }; c.render = function () { c.renders++; };
vm.createContext(c);
try {
  LOAD.forEach(function (f) { vm.runInContext(rd('parts/' + f), c); });
} catch (e) { console.log('FAIL load ' + e.message); process.exit(0); }
['tlChart', 'heatmap', 'swimlane', 'evBars', 'evChartCaption', 'evChart'].forEach(function (f) { if (typeof c[f] !== 'function') bad.push('missing ' + f); });
if (bad.length) { console.log('FAIL ' + bad.join('; ')); process.exit(0); }
function run(s) { return vm.runInContext(s, c); }
run('function findC(id){ var g=["sql","mongo","host"]; for (var i=0;i<g.length;i++){ var l=SCEN[g[i]]; for (var j=0;j<l.length;j++){ if (l[j].id===id) return l[j]; } } return null; }');

// ---------------- item 1 + 2: evBars fill colour, conditional legend, caption ----------------
var topq = run('findC("sql-topqueries-orders")'); // both windows present
var bh1 = c.evBars(topq.series, '', null);
has('bars both-win fill incident', bh1, 'style="fill:var(--series-4)"');
has('bars both-win fill normal', bh1, 'style="fill:var(--series-1)"');
not('bars old accent fill gone', bh1, 'var(--accent)');
not('bars old text-2 fill gone', bh1, 'var(--text-2)');
has('bars both-win legend kept', bh1, '<span class="key normal"></span> Normal <span class="key incident"></span> Incident');

var block = run('findC("sql-blocking-chain")'); // incident-only, example from the brief
var bh2 = c.evBars(block.series, '', null);
has('bars incident-only fill', bh2, 'style="fill:var(--series-4)"');
not('bars incident-only has normal key', bh2, 'Normal');
eq('bars incident-only legend', bh2.slice(bh2.indexOf('<p class="legend">')), '<p class="legend"><span class="key incident"></span> Incident</p></figure>');
var capBlock = c.evChartCaption(block);
eq('caption incident-only', capBlock, '<p class="ev-cap">These bars are the incident hour.</p>');

// TA_callBars shape: a single series named "Call duration (ms)", win incident, chart bars
var callCard = { chart: 'bars', series: [{ name: 'Call duration (ms)', win: 'incident', pts: [{ t: 0, v: 100 }, { t: 1, v: 200 }] }] };
var bh3 = c.evBars(callCard.series, '', null);
eq('bars call-duration legend', bh3.slice(bh3.indexOf('<p class="legend">')), '<p class="legend"><span class="key incident"></span> Call duration</p></figure>');
has('bars call-duration fill', bh3, 'style="fill:var(--series-4)"');
var capCall = c.evChartCaption(callCard);
eq('caption call-duration new text', capCall, '<p class="ev-cap">Each bar is one call. A taller bar is a slower call.</p>');
not('caption call-duration not overridden', capCall, 'These bars are the incident hour.');

// caption unaffected for a both-window line chart
var slowops = run('findC("MG-BAD-SLOWOPS")');
has('caption both-win line kept', c.evChartCaption(slowops), 'Left part: the reference hour (a normal period). Right part: the incident hour.');

// ---------------- item 3: tlChart dashed second series, per-window legend ----------------
function dashCount(h) { return cnt(h, 'stroke-dasharray:6 4'); }
var memg = run('findC("sql-memory-grants")');
var h1 = c.tlChart(memg.series, [], '');
eq('memory-grants dashed paths', dashCount(h1), 2); // one dashed run per window (incident+normal, each 1 segment)
has('memory-grants legend normal solid', h1, '<span class="key normal"></span> RESOURCE_SEMAPHORE delta ms (solid)');
has('memory-grants legend normal dashed', h1, '<span class="key normal"></span> Grant queue depth (dashed)');
has('memory-grants legend incident solid', h1, '<span class="key incident"></span> RESOURCE_SEMAPHORE delta ms (solid)');
has('memory-grants legend incident dashed', h1, '<span class="key incident"></span> Grant queue depth (dashed)');

var hostMem = run('findC("GS-08-host-mem-red")');
var h2 = c.tlChart(hostMem.series, [], '');
has('host-mem normal stays Normal key', h2, '<span class="key normal"></span> Normal');
has('host-mem incident dashed series named', h2, '<span class="key incident"></span> Swap out/sec (dashed)');
has('host-mem incident solid series named', h2, '<span class="key incident"></span> Available memory (solid)');

var hostDisk = run('findC("GS-09-host-disk-red")');
var h3 = c.tlChart(hostDisk.series, [], '');
has('host-disk dashed series named', h3, '<span class="key incident"></span> Disk queue length (dashed)');

// one series per window must render exactly as before (no dashed, old Normal/Incident legend)
var h4 = c.tlChart(slowops.series, [{ t: 60, label: 'Incident starts' }], '');
eq('single-series-per-window no dash', dashCount(h4), 0);
has('single-series-per-window legend kept', h4, '<span class="key normal"></span> Normal <span class="key incident"></span> Incident');

// ---------------- item 4: heatmap legend classes + matching CSS ----------------
var hm = c.heatmap([{ r: 0, c: 0, v: 5 }], ['00'], ['Mon']);
eq('heatmap legend', hm.slice(hm.indexOf('<p class="legend">')), '<p class="legend"><span class="key hm0"></span> No data <span class="key hm1"></span> Low <span class="key hm2"></span> Medium <span class="key hm3"></span> High <span class="key hm4"></span> Very high</p></figure>');
var css = rd('parts/02-css-fix.html');
['figure.ml .key.hm0{background:var(--surface-2)}',
 'figure.ml .key.hm1{background:color-mix(in srgb, var(--bad) 20%, var(--surface))}',
 'figure.ml .key.hm2{background:color-mix(in srgb, var(--bad) 40%, var(--surface))}',
 'figure.ml .key.hm3{background:color-mix(in srgb, var(--bad) 65%, var(--surface))}',
 'figure.ml .key.hm4{background:color-mix(in srgb, var(--bad) 90%, var(--surface))}'
].forEach(function (r) { if (css.indexOf(r) < 0) bad.push('css lacks ' + r); });

// ---------------- item 5: swimlane fill/background + good colour ----------------
var sw = c.swimlane([{ name: 'API', events: [{ t: 10, dur: 2, sev: 'ok', label: 'x' }, { t: 40, sev: 'warn', label: 'y' }, { t: 70, sev: 'bad', label: 'z' }] }], { from: 0, to: 119 });
has('swimlane ok bar uses good', sw, 'style="fill:var(--good)"');
not('swimlane ok bar no accent', sw, 'style="fill:var(--accent)"');
has('swimlane legend ok background', sw, '<span class="key" style="background:var(--good)"></span> ok');
has('swimlane legend warn background', sw, '<span class="key" style="background:var(--warn)"></span> warn');
has('swimlane legend bad background', sw, '<span class="key" style="background:var(--bad)"></span> bad');
not('swimlane legend no fill left', sw, 'style="fill:var(--accent)"></span> ok');

// ---------------- item 6: sql-cpu-high duplicate normal points removed ----------------
var cpuHigh = run('findC("sql-cpu-high")');
var normalPts = null, incidentPts = null;
cpuHigh.series.forEach(function (s) { if (s.name === 'Top wait share (%)') { if (s.win === 'normal') normalPts = s.pts; else incidentPts = s.pts; } });
eq('sql-cpu-high normal pts count', normalPts.length, 2);
normalPts.forEach(function (p) { if (p.t >= 60) bad.push('sql-cpu-high normal still has t>=60 point ' + p.t); });
eq('sql-cpu-high incident pts count', incidentPts.length, 3);
eq('sql-cpu-high incident t60 kept', incidentPts[0].t, 60);
eq('sql-cpu-high incident v60 kept', incidentPts[0].v, 44);

// ---------------- item 7: Variant B ready-chip colour override ----------------
has('css ready override present', css, '.B .chip i.ready{background:var(--suspect-green)}');
var css01 = rd('parts/01-css.html');
has('original ready rule untouched', css01, '.B .chip i.ready { background:var(--ok); }');

// ---------------- old fragments gone from edited files ----------------
var cA = rd('parts/11-charts-a.js');
not('charts-a old fill gone', cA, 'class="line ' + "' + cls + '" + '"/>\'');
var cB = rd('parts/11-charts-b.js');
eq('charts-b old legend gone', cnt(cB, '<span class="key"></span> No data'), 0);
var cC = rd('parts/11-charts-c.js');
eq('charts-c old ok fill gone', cnt(cC, "return 'fill:var(--accent)';"), 0);
eq('charts-c old legend style=fill gone', cnt(cC, 'style="fill:var(--accent)"'), 0);
var evB = rd('parts/12-evidence-b.js');
eq('evidence-b old fill gone', cnt(evB, "'var(--accent)' : 'var(--text-2)'"), 0);
eq('evidence-b old static legend gone', cnt(evB, '<p class="legend"><span class="key normal"></span> Normal <span class="key incident"></span> Incident</p></figure>'), 0);
var evF = rd('parts/12-evidence-f.js');
has('evidence-f incident-only guard wired', evF, "c.chart === 'bars' && hasIncident && !hasNormal && !oneCallDur");
var scn = rd('parts/14-scenarios.js');
eq('scenarios old dup pts gone', cnt(scn, '{ "t": 0, "v": 14 }, { "t": 30, "v": 15 }, { "t": 60, "v": 44 }, { "t": 90, "v": 51 }, { "t": 110, "v": 48 }'), 0);
eq('scenarios new pts present', cnt(scn, '"pts": [ { "t": 0, "v": 14 }, { "t": 30, "v": 15 } ]'), 1);

console.log(bad.length ? 'FAIL ' + bad.join('; ') : 'PASS');
