// usage (from buildout/wireframes) after apply: node ../packets/data/chk-fix4.js   prints PASS or FAIL <reason>
var fs = require('fs'), vm = require('vm'), bad = [];
function rd(p) { return fs.readFileSync(p, 'utf8').split('\r\n').join('\n'); }
function cnt(s, t) { return String(s).split(t).length - 1; }
function eq(l, g, w) { if (g !== w) bad.push(l + ' -> ' + JSON.stringify(g) + ' wanted ' + JSON.stringify(w)); }
function rep(ch, n) { var o = '', i; for (i = 0; i < n; i++) o += ch; return o; }
var EDITED = ['parts/10-data.js', 'parts/20-shell.js', 'parts/40-manual-home.js', 'parts/10-data4-suspects.js'];

// --- ES5 scan + syntax of the edited files ---
EDITED.forEach(function (p) {
  var s = rd(p);
  if (/=>|`|\blet\b|\bconst\b/.test(s)) bad.push(p + ' not ES5');
  try { new Function(s); } catch (e) { bad.push(p + ' syntax ' + e.message); }
});

// --- 1. esc also escapes > and " ---
var d = { renders: 0 }; d.render = function () { d.renders++; }; vm.createContext(d);
try { vm.runInContext(rd('parts/10-data.js'), d); } catch (e) { console.log('FAIL load 10-data ' + e.message); process.exit(0); }
eq('esc a"b<c>', d.esc('a"b<c>'), 'a&quot;b&lt;c&gt;');
eq('esc amp', d.esc('a&b'), 'a&amp;b');
eq('esc plain', d.esc('plain'), 'plain');
eq('esc extended once', cnt(rd('parts/10-data.js'), "replace(/>/g,'&gt;').replace(/\"/g,'&quot;')"), 1);

// --- 2. runSteps clears the comparison flags (stub the DOM as other checks do) ---
var s0 = { renders: 0, window: null }; s0.window = s0;
s0.render = function () { s0.renders++; };
function fakeEl() { return { innerHTML: '', scrollTop: 0, onclick: null, dataset: {}, classList: { toggle: function () {} } }; }
s0.document = { getElementById: function () { return fakeEl(); }, documentElement: { dataset: {} }, querySelectorAll: function () { return []; } };
s0.addEventListener = function () {};
s0.history = { replaceState: function () {} };
vm.createContext(s0);
try { vm.runInContext(rd('parts/10-data.js'), s0); vm.runInContext(rd('parts/20-shell.js'), s0); } catch (e) { console.log('FAIL load 20-shell ' + e.message); process.exit(0); }
if (typeof s0.runSteps !== 'function') { console.log('FAIL runSteps missing'); process.exit(0); }
vm.runInContext('W.fix.verdictOn = true; W.fix.recorded = true;', s0);
s0.runSteps();
eq('runSteps clears verdictOn', vm.runInContext('W.fix.verdictOn', s0), false);
eq('runSteps clears recorded', vm.runInContext('W.fix.recorded', s0), false);
eq('runSteps still runs steps', vm.runInContext('W.ranSteps', s0), true);
eq('runSteps guard once', cnt(rd('parts/20-shell.js'), 'if (W.fix) { W.fix.verdictOn = false; W.fix.recorded = false; }'), 1);

// --- 3. chars() is an ES5 code-point helper; the three functions are unchanged ---
var m = { renders: 0, window: null }; m.window = m; m.render = function () { m.renders++; };
vm.createContext(m);
try { vm.runInContext(rd('parts/40-manual-home.js'), m); } catch (e) { console.log('FAIL load 40-manual-home ' + e.message); process.exit(0); }
var EMOJI = '\uD83D\uDE00\uD83D\uDE00';
if (typeof m.chars !== 'function') {
  bad.push('chars missing');
} else {
  eq('chars ab length', m.chars('ab').length, 2);
  eq('chars ab join', m.chars('ab').join(''), 'ab');
  eq('chars pair length', m.chars(EMOJI).length, 2);
  eq('chars pair join', m.chars(EMOJI).join(''), EMOJI);
  eq('chars one pair length', m.chars('\uD83D\uDE00').length, 1);
  eq('chars lone high', m.chars('\uD83D').length, 1);
  eq('chars lone low', m.chars('\uDE00').length, 1);
  eq('cleanViewName emoji codepoints', m.chars(m.cleanViewName(EMOJI)).length, 2);
}
eq('cleanViewName ascii', m.cleanViewName('  Hello   world  '), 'Hello world');
eq('cleanViewName emoji', m.cleanViewName(EMOJI), EMOJI);
eq('viewNameTooLong 60', m.viewNameTooLong(rep('a', 60)), false);
eq('viewNameTooLong 61', m.viewNameTooLong(rep('a', 61)), true);
eq('viewNameTooLong emoji', m.viewNameTooLong(EMOJI), false);
eq('noteTooLong 300', m.noteTooLong(rep('a', 300)), false);
eq('noteTooLong 301', m.noteTooLong(rep('a', 301)), true);
eq('noteTooLong emoji', m.noteTooLong(EMOJI), false);
var mh = rd('parts/40-manual-home.js');
eq('Array.from gone', cnt(mh, 'Array.from'), 0);
eq('chars defined once', cnt(mh, 'function chars('), 1);
eq('chars call count', cnt(mh, 'chars('), 5);

// --- 4. each SUSPECTS5 entry has its own evidence step and the link uses it ---
var sx = { go: function (n) { sx.goArg = n; } }; vm.createContext(sx);
try { vm.runInContext(rd('parts/10-data4-suspects.js'), sx); } catch (e) { console.log('FAIL load 10-data4-suspects ' + e.message); process.exit(0); }
var out = sx.suspectCards();
eq('card count', cnt(out, '<article class="scard'), sx.SUSPECTS5.length);
eq('link count', cnt(out, 'onclick="go('), sx.SUSPECTS5.length);
sx.SUSPECTS5.forEach(function (s, i) {
  if (!(typeof s.step === 'number' && s.step === Math.floor(s.step) && s.step >= 5 && s.step <= 9)) bad.push('suspect ' + i + ' step ' + s.step);
  if (cnt(out, 'onclick="go(' + s.step + ');return false;"') < 1) bad.push('suspect ' + i + ' link calls its own step');
});
function stepOf(t) { for (var i = 0; i < sx.SUSPECTS5.length; i++) if (sx.SUSPECTS5[i].name.indexOf(t) >= 0) return sx.SUSPECTS5[i].step; return null; }
eq('blocking suspect step', stepOf('Blocking'), 7);
eq('release suspect step', stepOf('Release'), 5);
eq('queueing suspect step', stepOf('Queueing'), 6);
eq('old go(7) gone', cnt(rd('parts/10-data4-suspects.js'), 'onclick="go(7);return false;"'), 0);
var sx1 = {}; vm.createContext(sx1);
vm.runInContext(rd('parts/10-data4-suspects.js'), sx1);
eq('plain link without go', cnt(sx1.suspectCards(), '<a href="#">Open the evidence'), sx1.SUSPECTS5.length);
eq('no onclick without go', cnt(sx1.suspectCards(), 'onclick="go('), 0);

console.log(bad.length ? 'FAIL ' + bad.join('; ') : 'PASS');
