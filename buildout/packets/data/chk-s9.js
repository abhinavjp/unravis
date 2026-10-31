// usage (from buildout/wireframes) after apply: node ../packets/data/chk-s9.js   prints PASS or FAIL <reason>
var fs = require('fs'), vm = require('vm'), bad = [];
function rd(p) { return fs.readFileSync(p, 'utf8'); }
function cnt(s, t) { return String(s).split(t).length - 1; }
var NEWP = 'parts/14-scenarios-origin.js';
if (/=>|`|\blet\b|\bconst\b/.test(rd(NEWP))) bad.push(NEWP + ' not ES5');
var c = { renders: 0 }; c.render = function () { c.renders++; }; vm.createContext(c); c.window = c;
['parts/10-data.js', 'parts/11-charts-axis.js', 'parts/11-charts-a.js', 'parts/11-charts-b.js', 'parts/12-evidence-a.js', 'parts/12-evidence-b.js', 'parts/12-evidence-c.js', 'parts/12-evidence-d.js', 'parts/12-evidence-e.js', 'parts/12-evidence-f.js', 'parts/12-evidence-h.js', 'parts/13-why-a.js', 'parts/13-why-b.js', NEWP, 'parts/14-scenarios.js'].forEach(function (p) {
  try { vm.runInContext(rd(p), c); } catch (e) { console.log('FAIL load ' + p + ' ' + e.message); process.exit(0); }
});
function run(s) { return vm.runInContext(s, c); }
function eq(l, g, w) { if (g !== w) bad.push(l + ' -> ' + JSON.stringify(g) + ' wanted ' + JSON.stringify(w)); }
eq('origin is array', run('Object.prototype.toString.call(ORIGIN_CARDS) === "[object Array]"'), true);
eq('origin count', run('ORIGIN_CARDS.length'), 3);
eq('origin shape', run('ORIGIN_CARDS.every(function(k){ return k && typeof k.id === "string" && typeof k.label === "string" && (k.verdict === "ok" || k.verdict === "watch" || k.verdict === "bad" || k.verdict === "none") && Object.prototype.toString.call(k.series) === "[object Array]" && k.table && Object.prototype.toString.call(k.table.rows) === "[object Array]" && typeof k.unit === "string"; })'), true);
eq('ids unique', run('(function(){ var ids = [], d = []; function add(v){ if (ids.indexOf(v) >= 0) { d.push(v); } else { ids.push(v); } } ["sql", "mongo", "host"].forEach(function(k){ var l = SCEN[k] || []; for (var i = 0; i < l.length; i++) { add(l[i].id); } }); for (var j = 0; j < ORIGIN_CARDS.length; j++) { add(ORIGIN_CARDS[j].id); } return d.length; })()'), 0);
run('W = {};');
var html = c.step10Evidence();
['<p class="lead" style="color:var(--text)">', 'class="ev bad"', 'class="ev watch"', 'class="ev none"', 'Likely cause', 'Worth a look', 'Not collected', 'SQL Server waits moved first', 'App tier followed within the minute', 'MongoDB suspects could not be tested', 'class="ev-legend"', 'Unravis does not name a culprit'].forEach(function (t) { if (html.indexOf(t) < 0) bad.push('step10Evidence lacks ' + t); });
eq('card sections', cnt(html, '<section class="ev '), 3);
eq('why buttons', cnt(html, 'class="ev-why"'), 3);
eq('chart figures', cnt(html, '<figure class="ml">'), 2);
if (html.indexOf('ev-why" type="button" onclick="evWhy(\'ORI-01-sql-waits\')"') < 0) bad.push('sql why id missing');
run('W.why = ORIGIN_CARDS[1].id;');
var wv = c.step10Evidence();
if (wv.indexOf('why-view') < 0) bad.push('whyView not used');
if (wv.indexOf('&larr; Back to the evidence') < 0) bad.push('why back missing');
if (rd(NEWP).indexOf("typeof whyView === 'function'") < 0) bad.push('whyView guard missing');
run('W.why = "no-such-id";');
if (c.step10Evidence().indexOf('why-view') >= 0) bad.push('unknown why must fall through');
var s = rd('parts/32-steps-10-13.js');
eq('wire call', cnt(s, "(typeof step10Evidence === 'function' ? step10Evidence() : '')"), 1);
if (/=>|`/.test(s)) bad.push('edited file not ES5');
try { new Function(s); } catch (x) { bad.push('syntax ' + x.message); }
console.log(bad.length ? 'FAIL ' + bad.join('; ') : 'PASS');
