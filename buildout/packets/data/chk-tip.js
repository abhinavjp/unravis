// usage (from buildout/wireframes) after apply: node ../packets/data/chk-tip.js   prints PASS or FAIL <reason>
var fs = require('fs'), vm = require('vm'), bad = [];
function rd(p) { return fs.readFileSync(p, 'utf8'); }
function cnt(s, t) { return String(s).split(t).length - 1; }
function rdo(p) { try { return rd(p); } catch (e) { return ''; } }

var src = rd('parts/12-evidence-e.js');
if (/=>|`|\blet\b|\bconst\b/.test(src)) bad.push('12-evidence-e.js not ES5');
if (/\bW\./.test(src)) bad.push('12-evidence-e.js must not read W');

var c = {};
vm.createContext(c);
try {
  vm.runInContext(rd('parts/12-evidence-h.js'), c);
  ['parts/14-help-sql1.js', 'parts/14-help-sql2.js', 'parts/14-help-sql3.js', 'parts/14-help-mongo1.js', 'parts/14-help-mongo2.js', 'parts/14-help-host1.js', 'parts/14-help-host2.js'].forEach(function (p) { vm.runInContext(rd(p), c); });
  vm.runInContext(rd('parts/14-scenarios.js') + ';this.S=SCEN', c);
  vm.runInContext(src, c);
} catch (e) { console.log('FAIL load ' + e.message); process.exit(0); }
['evHelpLook', 'evHelpMore'].forEach(function (f) { if (typeof c[f] !== 'function') bad.push('missing ' + f); });
if (bad.length) { console.log('FAIL ' + bad.join('; ')); process.exit(0); }

var total = 0;
['sql', 'mongo', 'host'].forEach(function (set) {
  var list = c.S[set];
  if (Object.prototype.toString.call(list) !== '[object Array]' || !list.length) { bad.push('set ' + set + ' empty'); return; }
  list.forEach(function (card) {
    total++;
    var h = c.evHelpLook(card) + c.evHelpMore(card);
    if (h.indexOf('undefined') >= 0) bad.push(set + ' ' + card.id + ' shows undefined');
    if (cnt(h, 'class="ev-tt"') < 1) bad.push(set + ' ' + card.id + ' has no tooltip');
    if (h.indexOf('tabindex="0"') < 0) bad.push(set + ' ' + card.id + ' tooltip not keyboard-focusable');
    if (!/data-tip="[^"]+: [^"]+"/.test(h)) bad.push(set + ' ' + card.id + ' tooltip lacks "term: meaning"');
    if (h.indexOf('class="ev-look"') < 0) bad.push(set + ' ' + card.id + ' has no look paragraph');
    if (h.indexOf('class="ev-help"') < 0) bad.push(set + ' ' + card.id + ' has no help detail');
  });
});
if (total < 3) bad.push('too few cards');

var look = c.evHelpLook(c.S.sql[0]);
var want = '<span class="ev-tt" tabindex="0" data-tip="SLEEP_TASK: a normal wait when a background task is idle, waiting for its next job.">SLEEP_TASK</span>';
if (look.indexOf(want) < 0) bad.push('SLEEP_TASK tooltip ' + look);
if (look.indexOf('SLEEP_TASKs') >= 0) bad.push('matched inside a word');
if (look.indexOf('<b>What to look for:</b>') < 0) bad.push('look heading missing');

if (c.evHelpLook({ id: 'no-such-card' }) !== '' || c.evHelpMore({ id: 'no-such-card' }) !== '') bad.push('guard for unknown card');
if (c.evHelpLook({}) !== '' || c.evHelpMore({}) !== '') bad.push('guard for bare card');

vm.runInContext("EVHELP['tip-plain'] = { what: 'plain words only', chart: 'x < y & z' };", c);
var plain = c.evHelpMore({ id: 'tip-plain' });
if (plain.indexOf('plain words only') < 0 || plain.indexOf('ev-tt') >= 0) bad.push('no-term card not plain');
if (plain.indexOf('x &lt; y &amp; z') < 0) bad.push('no-term card not escaped');
vm.runInContext("EVHELP['tip-esc'] = { look: 'a <b> & \"c\"' };", c);
var esc = c.evHelpLook({ id: 'tip-esc' });
if (esc !== '<p class="ev-look"><b>What to look for:</b> a &lt;b&gt; &amp; &quot;c&quot;</p>') bad.push('evHelpLook escaping ' + esc);

if (cnt(src, 'evTip(c, h.look)') !== 1) bad.push('evHelpLook not routed to evTip');
if (cnt(src, 'evTip(c, h[key])') !== 1) bad.push('evHelpMore not routed to evTip');
if (cnt(src, '+ x(h.look) +') !== 0) bad.push('old look escape remains');
if (cnt(src, '+ x(h[key]) +') !== 0) bad.push('old body escape remains');
try { new Function(src); } catch (x) { bad.push('12-evidence-e.js syntax ' + x.message); }

var css = rdo('parts/02-css-tip.html');
if (css.indexOf('.ev-tt{') < 0) bad.push('css .ev-tt rule missing');
if (css.indexOf('.ev-tt[data-tip]:hover::after') < 0 || css.indexOf('.ev-tt[data-tip]:focus::after') < 0) bad.push('css tooltip rule missing');
if (css.indexOf('cursor:help') < 0) bad.push('css help cursor missing');
if (css.indexOf('.ev-tt:focus-visible') < 0) bad.push('css focus ring missing');

console.log(bad.length ? 'FAIL ' + bad.join('; ') : 'PASS');
