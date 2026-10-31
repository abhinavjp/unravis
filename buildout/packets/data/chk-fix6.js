// usage (from buildout/wireframes) after apply: node ../packets/data/chk-fix6.js   prints PASS or FAIL <reason>
var fs = require('fs'), vm = require('vm'), bad = [];
function rd(p) { return fs.readFileSync(p, 'utf8'); }
function cnt(s, t) { return String(s).split(t).length - 1; }
function has(l, s, t) { if (String(s).indexOf(t) < 0) bad.push(l + ' lacks ' + JSON.stringify(t)); }
function not(l, s, t) { if (String(s).indexOf(t) >= 0) bad.push(l + ' must not contain ' + JSON.stringify(t)); }
function eq(l, g, w) { if (g !== w) bad.push(l + ' -> ' + JSON.stringify(g) + ' wanted ' + JSON.stringify(w)); }

var EDITED = ['parts/12-evidence-f.js', 'parts/12-evidence-b.js'];
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
['evBars', 'evChartCaption', 'evChart'].forEach(function (f) { if (typeof c[f] !== 'function') bad.push('missing ' + f); });
if (bad.length) { console.log('FAIL ' + bad.join('; ')); process.exit(0); }
function run(s) { return vm.runInContext(s, c); }
run('function findC(id){ var g=["sql","mongo","host"]; for (var i=0;i<g.length;i++){ var l=SCEN[g[i]]; for (var j=0;j<l.length;j++){ if (l[j].id===id) return l[j]; } } return null; }');

// ---------------- item 1: call-duration caption, single legend key, others unchanged ----------------
var callCard = { chart: 'bars', series: [{ name: 'Call duration (ms)', win: 'incident', pts: [{ t: 0, v: 100 }, { t: 1, v: 200 }] }] };
var capCall = c.evChartCaption(callCard);
eq('call-duration caption', capCall, '<p class="ev-cap">Each bar is one call. A taller bar is a slower call.</p>');
not('call-duration caption no reference hour', capCall, 'reference hour');
not('call-duration caption not incident-only text', capCall, 'These bars are the incident hour.');
var bh = c.evBars(callCard.series, '', null);
has('call-duration legend kept', bh, '<p class="legend"><span class="key incident"></span> Call duration</p></figure>');
var slowops = run('findC("MG-BAD-SLOWOPS")');
has('both-win line caption kept', c.evChartCaption(slowops), 'Left part: the reference hour (a normal period). Right part: the incident hour.');
var block = run('findC("sql-blocking-chain")');
eq('incident-only caption kept', c.evChartCaption(block), '<p class="ev-cap">These bars are the incident hour.</p>');

// ---------------- item 2: incident marker drawn only with both windows ----------------
var bhI = c.evBars(block.series, '', 60);
eq('incident-only bars no marker', cnt(bhI, 'class="marker"'), 0);
eq('incident-only evChart no marker', cnt(c.evChart(block), 'class="marker"'), 0);
var topq = run('findC("sql-topqueries-orders")');
eq('two-window bars marker kept', cnt(c.evBars(topq.series, '', 60), 'class="marker"'), 1);
eq('two-window evChart marker kept', cnt(c.evChart(topq), 'class="marker"'), 1);
eq('normal-only bars no marker', cnt(c.evBars([{ name: 'a (ms)', win: 'normal', pts: [{ t: 0, v: 5 }, { t: 30, v: 8 }] }], '', 60), 'class="marker"'), 0);

// ---------------- wiring of the edits ----------------
var evF = rd('parts/12-evidence-f.js');
eq('call-duration branch wired', cnt(evF, "if (c.chart === 'bars' && oneCallDur) {"), 1);
var evB = rd('parts/12-evidence-b.js');
eq('both-window guard wired', cnt(evB, "  if (typeof incT === 'number' && hasN && hasI) {"), 1);
eq('marker line kept', cnt(evB, 'ax += axIncidentLine(PAD + bi * sw, PAD, H - PAD);'), 1);
eq('one hasN computation', cnt(evB, 'var hasN = false, hasI = false;'), 1);
var axAt = evB.indexOf('var ax = axYLabels(top');
var hasAt = evB.indexOf('var hasN = false');
if (axAt < 0 || hasAt < 0 || hasAt > axAt) bad.push('hasN/hasI must be computed before the axis block');

// ---------------- item 3: ready chip opaque ----------------
var css = rd('parts/02-css-fix.html');
has('css ready opaque', css, '.B .chip i.ready{background:var(--suspect-green)}');
not('css ready no soft', css, '.B .chip i.ready{background:var(--suspect-green-soft)}');

// ---------------- item 4: chk-fix1 pinned to the new behaviour ----------------
var fx1 = rd('../packets/data/chk-fix1.js');
has('chk-fix1 caption assertion updated', fx1, "eq('caption call-duration new text', capCall, '<p class=\"ev-cap\">Each bar is one call. A taller bar is a slower call.</p>');");
not('chk-fix1 old caption assertion gone', fx1, "has('caption call-duration kept old text', capCall, 'Left part: the reference hour');");
has('chk-fix1 css assertion updated', fx1, "has('css ready override present', css, '.B .chip i.ready{background:var(--suspect-green)}');");
not('chk-fix1 old css assertion gone', fx1, "has('css ready override present', css, '.B .chip i.ready{background:var(--suspect-green-soft)}');");

console.log(bad.length ? 'FAIL ' + bad.join('; ') : 'PASS');
