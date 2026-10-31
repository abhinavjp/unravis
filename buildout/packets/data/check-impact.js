// usage (from buildout/wireframes): node ../packets/data/check-impact.js <file> <group: sql|mongo|host>   prints PASS or FAIL <reason>
var fs = require('fs'), vm = require('vm');
var file = process.argv[2], group = process.argv[3];
var bad = [];
var src = fs.readFileSync(file, 'utf8');
if (/=>|`|\blet\b|\bconst\b/.test(src)) bad.push('not ES5');
if (src.indexOf("var EVIMPACT = (typeof EVIMPACT === 'object' && EVIMPACT) || {};") < 0) bad.push('missing EVIMPACT header line');
var sc = {}; vm.createContext(sc);
vm.runInContext(fs.readFileSync('parts/14-scenarios.js', 'utf8') + ';this.S=SCEN', sc);
var ids = sc.S[group].map(function (x) { return x.id; });
var c = {}; vm.createContext(c);
try { vm.runInContext(src + ';this.E=EVIMPACT;', c); } catch (e) { console.log('FAIL load ' + e.message); process.exit(0); }
var got = Object.keys(c.E);
ids.forEach(function (id) {
  var s = c.E[id];
  if (typeof s !== 'string') { bad.push('missing ' + id); return; }
  if (s.length < 25 || s.length > 140) bad.push(id + ' length ' + s.length);
  if (/[<>"]/.test(s)) bad.push(id + ' has < > or "');
  if (/\s{2,}/.test(s) || s !== s.trim()) bad.push(id + ' spacing');
  if (!/[.]$/.test(s)) bad.push(id + ' must end with a full stop');
});
got.forEach(function (k) { if (ids.indexOf(k) < 0) bad.push('extra ' + k); });
var seen = {};
ids.forEach(function (id) { var s = c.E[id]; if (s) { if (seen[s]) bad.push('duplicate text ' + id); seen[s] = 1; } });
console.log(bad.length ? 'FAIL ' + bad.join('; ') : 'PASS');
