// usage (from buildout/wireframes): node ../packets/data/chk-b1.js   prints PASS or FAIL <reason>
var fs = require('fs'), vm = require('vm');
var bad = [];
var src = fs.readFileSync('parts/14-deps-data.js', 'utf8');
if (/=>|`|\blet\b|\bconst\b/.test(src)) bad.push('not ES5');
var c = {}; vm.createContext(c);
vm.runInContext(fs.readFileSync('parts/14-scenarios.js', 'utf8') + ';this.S=SCEN', c);
try { vm.runInContext(src + ';this.D=EVDEP_DEFS;this.M=EVDEP_MAP;', c); } catch (e) { console.log('FAIL load ' + e.message); process.exit(0); }
var keys = Object.keys(c.D).join();
if (keys !== 'querystore,qswaits,systemhealth,viewstate,profiler,monitorrole,oplogread,blackbox,perfaccess') bad.push('def keys ' + keys);
Object.keys(c.D).forEach(function (k) {
  var d = c.D[k];
  ['group', 'title', 'why', 'effect', 'note'].forEach(function (f) { if (typeof d[f] !== 'string' || !d[f]) bad.push(k + '.' + f); else if (f !== 'note' && /[<>"]/.test(d[f])) bad.push(k + '.' + f + ' bad char'); });
  if (!Array.isArray(d.fix)) bad.push(k + '.fix'); else d.fix.forEach(function (l) { if (typeof l !== 'string' || !l) bad.push(k + '.fix line'); });
  if (typeof d.soft !== 'boolean') bad.push(k + '.soft');
});
var n = 0;
['sql', 'mongo', 'host'].forEach(function (g) { c.S[g].forEach(function (card) {
  n++; var m = c.M[card.id];
  if (!Array.isArray(m)) { bad.push('map missing ' + card.id); return; }
  m.forEach(function (k) { if (!c.D[k]) bad.push(card.id + ' unknown dep ' + k); else if (c.D[k].group !== g) bad.push(card.id + ' wrong group ' + k); });
}); });
if (Object.keys(c.M).length !== n) bad.push('map size ' + Object.keys(c.M).length + ' vs ' + n);
console.log(bad.length ? 'FAIL ' + bad.join('; ') : 'PASS');
