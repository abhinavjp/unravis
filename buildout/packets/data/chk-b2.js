// usage (from buildout/wireframes): node ../packets/data/chk-b2.js   prints PASS or FAIL <reason>
var fs = require('fs'), vm = require('vm');
var bad = [], renders = 0;
var src = fs.readFileSync('parts/12-evidence-g.js', 'utf8');
if (/=>|`|\blet\b|\bconst\b/.test(src)) bad.push('not ES5');
var c = { W: {}, render: function () { renders++; }, document: { createElement: function () { return { style: {}, select: function () {}, setAttribute: function () {} }; }, body: { appendChild: function () {}, removeChild: function () {} }, execCommand: function () { return true; } }, setTimeout: function () {}, navigator: {} };
vm.createContext(c);
try {
  vm.runInContext(fs.readFileSync('parts/14-deps-data.js', 'utf8') + ';' + src + ';this.F={off:evDepsOff,dep:evDepFor,blk:evDepBlocked,eff:evDepEffVerdict,ban:evDepBanner,tog:evDepToggle,pick:evDepToggles,copy:evCopy};', c);
} catch (e) { console.log('FAIL load ' + e.message); process.exit(0); }
var F = c.F;
var none = { id: 'sql-plan-regression', verdict: 'none' }, ok = { id: 'sql-waits-benign', verdict: 'ok' }, noMap = { id: 'sql-log-growth', verdict: 'ok' }, soft = { id: 'sql-topqueries-orders', verdict: 'ok' };
var d1 = F.dep(none);
if (!d1 || d1.key !== 'querystore' || d1.forced !== true) bad.push('forced none card');
if (F.dep(ok) !== null || F.dep(noMap) !== null) bad.push('ok card must have no dep');
if (F.blk(ok) !== false || F.eff(ok) !== 'ok') bad.push('not blocked by default');
F.tog('viewstate');
if (renders !== 1 || F.off().viewstate !== true) bad.push('toggle on');
if (F.blk(ok) !== true || F.eff(ok) !== 'none' || F.dep(ok).key !== 'viewstate' || F.dep(ok).forced !== false) bad.push('blocked after toggle');
F.tog('viewstate');
if (F.blk(ok) !== false || renders !== 2) bad.push('toggle off');
F.tog('qswaits');
if (F.blk(soft) !== false || !F.dep(soft) || F.dep(soft).key !== 'qswaits') bad.push('soft dep');
F.tog('qswaits');
var b = F.ban(none);
if (b.indexOf('class="ev-dep"') < 0 || b.indexOf('Could not check') < 0 || b.indexOf('Query Store is off') < 0 || b.indexOf('Copy command') < 0 || b.indexOf('data-copy="ALTER DATABASE') < 0 || b.indexOf('onclick="evCopy(this)"') < 0) bad.push('banner html ' + b.slice(0, 200));
if (F.ban(ok) !== '') bad.push('banner for ok card');
var bb = F.ban({ id: 'GS-01-host-cpu-ok', verdict: 'ok' });
if (bb !== '') bad.push('host ok banner');
F.tog('blackbox');
var hb = F.ban({ id: 'GS-01-host-cpu-ok', verdict: 'ok' });
if (hb.indexOf('No Blackbox') < 0 || hb.indexOf('Copy command') >= 0) bad.push('host banner without fix');
F.tog('blackbox');
var p = F.pick('sql');
if (p.indexOf('aria-label="Pretend a source is missing"') < 0 || p.indexOf('evDepToggle(\'querystore\')') < 0 || p.indexOf('evDepToggle(\'viewstate\')') < 0 || p.indexOf('evDepToggle(\'profiler\')') >= 0 || p.indexOf('aria-pressed="false"') < 0) bad.push('picker html');
F.tog('querystore');
if (F.pick('sql').indexOf('aria-pressed="true"') < 0) bad.push('picker pressed');
console.log(bad.length ? 'FAIL ' + bad.join('; ') : 'PASS');
