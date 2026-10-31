// usage (from buildout/wireframes) after apply: node ../packets/data/chk-s2c.js   prints PASS or FAIL <reason>
var fs = require('fs'), vm = require('vm'), bad = [];
function rd(p) { return fs.readFileSync(p, 'utf8'); }
['parts/10-data2-calls.js', 'parts/30-pager.js'].forEach(function (p) { if (/=>|`|\blet\b|\bconst\b/.test(rd(p))) bad.push(p + ' not ES5'); });
var c = { renders: 0 }; c.render = function () { c.renders++; }; vm.createContext(c);
vm.runInContext(rd('parts/10-data.js'), c);
try { vm.runInContext(rd('parts/10-data2-calls.js'), c); vm.runInContext(rd('parts/30-pager.js'), c); } catch (e) { console.log('FAIL load ' + e.message); process.exit(0); }
vm.runInContext(rd('parts/30-frame-helpers.js'), c);
function run(s) { return vm.runInContext(s, c); }
function eq(l, g, w) { if (g !== w) bad.push(l + ' -> ' + JSON.stringify(g) + ' wanted ' + JSON.stringify(w)); }
eq('calls length', run('CALLS.length'), 62);
eq('calls row shape', run('CALLS.every(function(r){return typeof r[0]==="string"&&/^09:\\d\\d:\\d\\d$/.test(r[1])&&typeof r[2]==="number"})'), true);
eq('calls desc', run('CALLS.slice(10).every(function(r,i,a){return i===0||a[i-1][2]>=r[2]})'), true);
run('W = { frame: { incidentStart: "2026-10-26T09:00", incidentEnd: "2026-10-26T10:00", referenceStart: "2026-10-26T07:00", referenceEnd: "2026-10-26T08:00", releases: [] } };');
eq('slice p1', run('slowPageRows(CALLS).length'), 25);
eq('pager p1', run('slowPager(62)'), '<div class="pager" role="navigation" aria-label="Slow calls pages"><span class="note" role="status">Showing 1 to 25 of 62.</span><button class="btn sm" onclick="slowPageGo(-1)" disabled>Previous</button><button class="btn sm" onclick="slowPageGo(1)">Next</button></div>');
run('slowPageGo(1); slowPageGo(1);');
eq('page idx', run('W.slowPg'), 2);
eq('slice last', run('slowPageRows(CALLS).length'), 12);
eq('pager last', run('slowPager(62)').indexOf('Showing 51 to 62 of 62.') > 0 && run('slowPager(62)').indexOf('onclick="slowPageGo(1)" disabled>Next') > 0, true);
run('slowPageGo(5);');
eq('clamped high', run('slowPageRows(CALLS), W.slowPg'), 2);
run('W.slowPg = -4;');
eq('clamped low', run('slowPageRows(CALLS), W.slowPg'), 0);
eq('empty pager', run('slowPager(0)'), '');
eq('small pager', run('slowPager(7)').indexOf('Showing 1 to 7 of 7.') > 0, true);
eq('rollup none', run('fpRollupAny()'), '');
run('W.frame.referenceStart = "2026-10-20T00:00";');
eq('rollup text', run('fpRollupAny()'), '<p class="note" role="status">Showing hourly averages because the window is longer than 24 hours. Spikes shorter than an hour may not show as a peak at this scale.</p>');
var s = rd('parts/30-steps-0-4.js');
eq('map edit', s.split('    + slowPageRows(rows).map(function(p){').length - 1, 1);
eq('old map gone', s.split('    + rows.map(function(p){').length - 1, 0);
eq('pager call', s.split("</tbody></table></div>' + slowPager(rows.length) + '<p class=\"note\">The longest calls that started in the incident window.</p>'));").length - 1, 1);
var e = rd('parts/12-evidence-c.js');
eq('rollup wired', e.split("(typeof fpRollupAny === 'function' ? fpRollupAny() : '') + evChartCaption(c)").length - 1, 2);
eq('css', rd('parts/02-css-pager.html').indexOf('.pager{display:flex') > 0, true);
try { new Function(s); new Function(e); } catch (x) { bad.push('syntax ' + x.message); }
console.log(bad.length ? 'FAIL ' + bad.join('; ') : 'PASS');
