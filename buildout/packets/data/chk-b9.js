// usage (from buildout/wireframes): node ../packets/data/chk-b9.js   prints PASS or FAIL <reason>
var fs = require('fs'), vm = require('vm');
var src = fs.readFileSync('parts/12-evidence-h.js', 'utf8'), bad = [];
if (/=>|`|\blet\b|\bconst\b/.test(src)) bad.push('not ES5');
var c = {}; vm.createContext(c);
try { vm.runInContext(src, c); } catch (e) { console.log('FAIL load ' + e.message); process.exit(0); }
['evImpact', 'evTip', 'evQueryId'].forEach(function (f) { if (typeof c[f] !== 'function') bad.push('missing ' + f); });
if (bad.length) { console.log('FAIL ' + bad.join('; ')); process.exit(0); }
// no data globals: all guarded
if (c.evImpact({ id: 'x' }) !== '' || c.evTip({ id: 'x' }, 'a < b') !== 'a &lt; b') bad.push('guards without data');
vm.runInContext("var EVIMPACT={'k1':'Users \"saw\" <slow> pages.'};var EVHELP={'k1':{terms:[{t:'ms',d:'millisecond.'},{t:'PAGEIOLATCH_SH',d:'a page read wait.'},{t:'IO',d:'input output'}]}};", c);
var h = c.evImpact({ id: 'k1' });
if (h !== '<p class="ev-impact"><b>In plain words:</b> Users &quot;saw&quot; &lt;slow&gt; pages.</p>') bad.push('evImpact ' + h);
if (c.evImpact({ id: 'k2' }) !== '') bad.push('evImpact unknown id');
var t = c.evTip({ id: 'k1' }, 'Waited 5 ms on PAGEIOLATCH_SH & items, ms again');
var exp = 'Waited 5 <span class="ev-tt" tabindex="0" data-tip="ms: millisecond.">ms</span> on <span class="ev-tt" tabindex="0" data-tip="PAGEIOLATCH_SH: a page read wait.">PAGEIOLATCH_SH</span> &amp; items, ms again';
if (t !== exp) bad.push('evTip ' + t);
if (c.evTip({ id: 'k1' }, 'items only') !== 'items only') bad.push('evTip must not match inside a word');
if (c.evTip({ id: 'k1' }, 'a "q" <b>') !== 'a &quot;q&quot; &lt;b&gt;') bad.push('evTip escaping');
var q = c.evQueryId('0xAB12');
if (q.indexOf('class="ev-qa"') < 0 || q.indexOf('<code>0xAB12</code>') < 0 || q.indexOf('data-copy="0xAB12"') < 0 || q.indexOf('onclick="evCopy(this)"') < 0 || q.indexOf('Copy query ID') < 0 || q.indexOf('SSMS') < 0 || q.indexOf('Azure Data Studio') < 0) bad.push('evQueryId ' + q);
if (c.evQueryId('a"b<').indexOf('a"b<') >= 0) bad.push('evQueryId escaping');
console.log(bad.length ? 'FAIL ' + bad.join('; ') : 'PASS');
