// usage: node check-help.js <file> <id1,id2,...>   prints PASS or FAIL <reason>
var fs = require('fs'), vm = require('vm');
var file = process.argv[2], ids = process.argv[3].split(',');
var src = fs.readFileSync(file, 'utf8');
var ctx = {}; vm.createContext(ctx);
try { vm.runInContext(src + ';this.H=EVHELP;', ctx); } catch (e) { console.log('FAIL load ' + e.message); process.exit(0); }
var caps = { look: 140, what: 260, normal: 200, issue: 260, check: 260, chart: 220 };
var bad = [];
if (/=>|`|\blet\b|\bconst\b/.test(src)) bad.push('not ES5');
if (!/^var EVHELP = \(typeof EVHELP === 'object' && EVHELP\) \|\| \{\};/.test(src)) bad.push('first line');
var got = Object.keys(ctx.H);
if (got.join() !== ids.join()) bad.push('ids ' + got.join());
ids.forEach(function (id) {
  var h = ctx.H[id]; if (!h) return;
  Object.keys(caps).forEach(function (k) {
    if (typeof h[k] !== 'string' || !h[k].trim()) bad.push(id + '.' + k + ' empty');
    else if (h[k].length > caps[k]) bad.push(id + '.' + k + ' len ' + h[k].length);
    else if (/[<>"]/.test(h[k])) bad.push(id + '.' + k + ' bad char');
  });
  if (!Array.isArray(h.terms) || h.terms.length < 1 || h.terms.length > 4) bad.push(id + '.terms count');
  else h.terms.forEach(function (t) { if (!t || !t.t || !t.d || t.d.length > 130 || /[<>"]/.test(t.t + t.d)) bad.push(id + '.terms item'); });
});
console.log(bad.length ? 'FAIL ' + bad.join('; ') : 'PASS');
