// usage (from buildout/wireframes) after apply: node ../packets/data/chk-s8.js   prints PASS or FAIL <reason>
var fs = require('fs'), vm = require('vm'), bad = [];
function rd(p) { return fs.readFileSync(p, 'utf8').split('\r\n').join('\n'); }
var src = rd('parts/10-data4-suspects.js');
if (/=>|`|\blet\b|\bconst\b/.test(src)) bad.push('not ES5');
function cnt(s, t) { return String(s).split(t).length - 1; }

// --- no global `go` ---
var c1 = {}; vm.createContext(c1);
try { vm.runInContext(src, c1); } catch (e) { console.log('FAIL load (no go) ' + e.message); process.exit(0); }
['SUSPECTS5', 'RULED_OUT5', 'suspectCards', 'ruledOutDrawer5'].forEach(function (n) { if (typeof c1[n] === 'undefined') bad.push('missing ' + n); });
if (bad.length) { console.log('FAIL ' + bad.join('; ')); process.exit(0); }

if (!(c1.SUSPECTS5.length >= 3 && c1.SUSPECTS5.length <= 4)) bad.push('SUSPECTS5 length ' + c1.SUSPECTS5.length);
c1.SUSPECTS5.forEach(function (s, i) {
  if (typeof s.name !== 'string' || !s.name) bad.push('suspect ' + i + ' name');
  if (typeof s.why !== 'string' || !s.why) bad.push('suspect ' + i + ' why');
  if (['High', 'Medium', 'Low'].indexOf(s.confidence) < 0) bad.push('suspect ' + i + ' confidence ' + s.confidence);
  if (!(s.evidence.length >= 2 && s.evidence.length <= 3)) bad.push('suspect ' + i + ' evidence count ' + s.evidence.length);
  s.evidence.forEach(function (ev, j) { if (!/\d/.test(ev)) bad.push('suspect ' + i + ' evidence ' + j + ' has no number'); });
});
if (c1.RULED_OUT5.length !== 3) bad.push('RULED_OUT5 length ' + c1.RULED_OUT5.length);
c1.RULED_OUT5.forEach(function (r, i) {
  if (typeof r.name !== 'string' || !r.name) bad.push('ruled-out ' + i + ' name');
  if (typeof r.reason !== 'string' || !r.reason) bad.push('ruled-out ' + i + ' reason');
});

var out1 = c1.suspectCards();
if (cnt(out1, '<article class="scard') !== c1.SUSPECTS5.length) bad.push('card count');
if (cnt(out1, 'Open the evidence') !== c1.SUSPECTS5.length) bad.push('link text count');
if (cnt(out1, 'onclick="go(7)') !== 0) bad.push('onclick present without a global go');
if (cnt(out1, '<a href="#">Open the evidence') !== c1.SUSPECTS5.length) bad.push('plain link missing when go absent');
['conf-high', 'conf-medium', 'conf-low'].forEach(function (cls) {
  var want = c1.SUSPECTS5.filter(function (s) { return s.confidence.toLowerCase() === cls.slice(5); }).length;
  if (cnt(out1, 'class="scard ' + cls + '"') !== want) bad.push(cls + ' count');
});
['High', 'Medium', 'Low'].forEach(function (lvl) {
  var want = c1.SUSPECTS5.filter(function (s) { return s.confidence === lvl; }).length;
  if (cnt(out1, 'Confidence:</strong> ' + lvl) !== want) bad.push(lvl + ' confidence text count');
});

var ro1 = c1.ruledOutDrawer5();
if (ro1.indexOf('<details class="ev-fold"><summary>Ruled out</summary>') !== 0) bad.push('ruled-out drawer header');
if (cnt(ro1, '<li>') !== 3) bad.push('ruled-out li count');
c1.RULED_OUT5.forEach(function (r) { if (ro1.indexOf(r.name) < 0) bad.push('ruled-out missing name ' + r.name); });

// --- global `go` present: onclick must call go(7) ---
var c2 = { go: function (n) { this.goArg = n; } }; vm.createContext(c2);
vm.runInContext(src, c2);
var out2 = c2.suspectCards();
if (c2.SUSPECTS5.some(function (x) { return cnt(out2, 'onclick="go(' + x.step + ');return false;"') < 1; }) || cnt(out2, 'onclick="go(') !== c2.SUSPECTS5.length) bad.push('onclick count with go present');
if (c2.SUSPECTS5.some(function (x) { return cnt(out2, '<a href="#" onclick="go(' + x.step + ');return false;">Open the evidence') < 1; })) bad.push('onclick link shape');

// --- CSS tokens only, matches brief's allowed token set ---
var css = rd('parts/02-css-suspects.html');
var allowed = ['--ok', '--warn', '--bad', '--good', '--accent', '--text', '--text-2', '--line', '--surface', '--surface-2', '--r'];
var toks = css.match(/var\(--[a-z0-9-]+\)/g) || [];
if (!toks.length) bad.push('css has no tokens');
toks.forEach(function (t) { var name = t.slice(4, -1); if (allowed.indexOf(name) < 0) bad.push('css token not allowed: ' + name); });
if (css.indexOf('.scard.conf-high') < 0 || css.indexOf('.scard.conf-medium') < 0 || css.indexOf('.scard.conf-low') < 0) bad.push('css missing confidence rules');

// --- wiring edit in parts/31-steps-5-9.js ---
var s = rd('parts/31-steps-5-9.js');
if (cnt(s, "  + body + suspectsHere(['C1']);") !== 0) bad.push('old return line still present unedited');
if (cnt(s, "  + body + suspectsHere(['C1'])\n  + (typeof nearView === 'function' ? nearView() : '')\n  + suspectCards() + ruledOutDrawer5();") !== 1) bad.push('new return block missing or duplicated');
try { new Function(s); } catch (x) { bad.push('syntax steps-5-9 ' + x.message); }
try { new Function(src); } catch (x) { bad.push('syntax data4-suspects ' + x.message); }

console.log(bad.length ? 'FAIL ' + bad.join('; ') : 'PASS');
