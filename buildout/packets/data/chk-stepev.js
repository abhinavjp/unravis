// usage (from buildout/wireframes): node ../packets/data/chk-stepev.js <file> <fn> <setsVar> <group>   prints PASS or FAIL <reason>
var fs = require('fs'), vm = require('vm');
var file = process.argv[2], fn = process.argv[3], setsVar = process.argv[4], group = process.argv[5];
var bad = [], renders = 0;
var src = fs.readFileSync(file, 'utf8');
if (/=>|`|\blet\b|\bconst\b/.test(src)) bad.push('not ES5');
if (/function\s+evSetPick/.test(src)) bad.push('must not define evSetPick');
var sc = {}; vm.createContext(sc);
vm.runInContext(fs.readFileSync('parts/14-scenarios.js', 'utf8') + ';this.S=SCEN', sc);
var byId = {};
sc.S[group].forEach(function (x) { byId[x.id] = x; });
var eff = function (x) { return x.verdict; };
var c = {
  W: { why: null, evSet: 'incident' }, render: function () { renders++; },
  evFind: function (id) { return byId[id] || null; },
  evidenceLegend: function () { return '<LG>'; },
  evidenceCard: function (x) { return '<C:' + x.id + '>'; },
  whyView: function (x) { return '<WV:' + x.id + '>'; },
  ruledOutDrawer: function (a) { return '<RO' + a.length + '>'; },
  evDepToggles: function (g) { return '<DT:' + g + '>'; },
  evDepEffVerdict: function (x) { return eff(x); }
};
vm.createContext(c);
try { vm.runInContext(src + ';this.F=' + fn + ';this.SETS=' + setsVar + ';', c); } catch (e) { console.log('FAIL load ' + e.message); process.exit(0); }
if (Object.keys(c.SETS).join() !== 'incident,quiet,watch') bad.push('set keys');
['incident', 'quiet', 'watch'].forEach(function (k) {
  var s = c.SETS[k];
  if (!s || typeof s.label !== 'string' || !Array.isArray(s.ids)) { bad.push('set ' + k); return; }
  s.ids.forEach(function (id) { if (!byId[id]) bad.push('unknown id ' + id); });
  c.W.why = null; c.W.evSet = k;
  var h = c.F();
  var exp = [];
  ['bad', 'watch', 'none'].forEach(function (v) { s.ids.forEach(function (id) { if (byId[id] && byId[id].verdict === v) exp.push('<C:' + id + '>'); }); });
  var got = h.match(/<C:[^>]*>/g) || [];
  if (got.join('') !== exp.join('')) bad.push(k + ' cards ' + got.join('') + ' vs ' + exp.join(''));
  var okN = s.ids.filter(function (id) { return byId[id] && byId[id].verdict === 'ok'; }).length;
  if (h.indexOf('<RO' + okN + '>') < 0) bad.push(k + ' ruled-out count');
  if (h.indexOf('<DT:' + group + '>') < 0 || h.indexOf('<LG>') < 0) bad.push(k + ' toggles or legend');
  if (h.indexOf('aria-label="Pretend case"') < 0 || h.indexOf('onclick="evSetPick(\'' + k + '\')"') < 0) bad.push(k + ' switcher');
});
c.W.evSet = 'nonsense';
if (c.F().indexOf('aria-pressed="true" onclick="evSetPick(\'incident\')"') < 0) bad.push('fallback key');
var firstId = c.SETS.incident.ids[0];
c.W.why = firstId;
if (c.F() !== '<WV:' + firstId + '>') bad.push('why view');
c.W.why = 'no-such-card';
if (c.F().indexOf('<LG>') < 0 || c.W.why !== null) bad.push('why reset');
eff = function () { return 'none'; };
c.W.evSet = 'quiet';
if (c.F().indexOf('<RO0>') < 0) bad.push('blocked cards leave ruled-out empty');
console.log(bad.length ? 'FAIL ' + bad.join('; ') : 'PASS');
