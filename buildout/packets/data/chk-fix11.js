// usage (from buildout/wireframes) after apply: node ../packets/data/chk-fix11.js   prints PASS or FAIL <reason>
var fs = require('fs'), vm = require('vm'), bad = [];
function rd(p) { return fs.readFileSync(p, 'utf8'); }
function cnt(s, t) { return String(s).split(t).length - 1; }
function eq(l, g, w) { if (g !== w) bad.push(l + ' -> ' + JSON.stringify(g) + ' wanted ' + JSON.stringify(w)); }
var src = rd('parts/12-evidence-c.js');
if (/=>|`|\blet\b|\bconst\b/.test(src)) bad.push('12-evidence-c.js not ES5');
var c = {}; vm.createContext(c);
try { vm.runInContext(src, c); } catch (e) { console.log('FAIL load ' + e.message); process.exit(0); }
if (typeof c.evChip !== 'function') bad.push('missing evChip');
if (!bad.length) {
  eq('ok chip', c.evChip('ok'), 'Looked normal');
  eq('watch chip', c.evChip('watch'), 'Worth a look');
  eq('bad chip', c.evChip('bad'), 'Likely cause');
  eq('none chip', c.evChip('none'), 'Not collected');
  eq('unknown chip', c.evChip('zzz'), 'Not collected');
}
eq('new chip line once', cnt(src, "  if (v === 'ok') return 'Looked normal';"), 1);
eq('old chip line gone', cnt(src, "  if (v === 'ok') return 'Normal';"), 0);
eq('watch line kept', cnt(src, "  if (v === 'watch') return 'Worth a look';"), 1);
eq('bad line kept', cnt(src, "  if (v === 'bad') return 'Likely cause';"), 1);
eq('none line kept', cnt(src, "  return 'Not collected';"), 1);
var s30 = rd('parts/30-steps-0-4.js');
if (s30.indexOf('<th>Normal</th>') < 0) bad.push('reference-window heading <th>Normal</th> missing from 30-steps-0-4.js');
eq('both reference headings kept', cnt(s30, '<th>Normal</th>'), 2);
eq('charts-a window key kept', cnt(rd('parts/11-charts-a.js'), "? 'Normal' : 'Incident'"), 1);
if (rd('parts/12-evidence-d.js').indexOf('Looked normal') < 0) bad.push('12-evidence-d.js legend changed');
try { new Function(src); } catch (x) { bad.push('syntax ' + x.message); }
console.log(bad.length ? 'FAIL ' + bad.join('; ') : 'PASS');
