// usage (from buildout/wireframes) after apply: node ../packets/data/chk-helpcpu.js   prints PASS or FAIL <reason>
var fs = require('fs'), vm = require('vm'), bad = [];
function rd(p) { return fs.readFileSync(p, 'utf8'); }
function cnt(s, t) { return String(s).split(t).length - 1; }

var src = rd('parts/14-help-sql3.js');
if (/=>|`|\blet\b|\bconst\b/.test(src)) bad.push('14-help-sql3.js not ES5');
if (/\bW\./.test(src)) bad.push('14-help-sql3.js must not read W');

var c = { renders: 0, W: {} };
c.render = function () { c.renders++; };
c.evCopy = function () {};
vm.createContext(c);

var load = [
  'parts/10-data.js',
  'parts/11-charts-a.js', 'parts/11-charts-axis.js', 'parts/11-charts-b.js', 'parts/11-charts-c.js',
  'parts/12-evidence-a.js', 'parts/12-evidence-b.js', 'parts/12-evidence-c.js', 'parts/12-evidence-d.js',
  'parts/12-evidence-e.js', 'parts/12-evidence-f.js', 'parts/12-evidence-g.js', 'parts/12-evidence-h.js',
  'parts/12-evidence-i.js',
  'parts/13-why-a.js', 'parts/13-why-b.js',
  'parts/14-deps-data.js', 'parts/14-scenarios.js'
];
load.forEach(function (p) {
  try { vm.runInContext(rd(p), c); } catch (e) { bad.push('load ' + p + ' ' + e.message); }
});
var helpFiles = fs.readdirSync('parts').filter(function (n) { return /^14-help-.*\.js$/.test(n); }).sort();
helpFiles.forEach(function (n) {
  try { vm.runInContext(rd('parts/' + n), c); } catch (e) { bad.push('load parts/' + n + ' ' + e.message); }
});
if (bad.length) { console.log('FAIL ' + bad.join('; ')); process.exit(0); }

// every key the sql1 entries share must be present on the new entry
var refIds = ['sql-waits-benign', 'sql-topqueries-orders', 'sql-log-growth', 'sql-lock-waits-rising', 'sql-memory-grants', 'sql-index-maintenance'];
var refKeys = {};
refIds.forEach(function (id) {
  var e = c.EVHELP[id];
  if (e && typeof e === 'object') { Object.keys(e).forEach(function (k) { refKeys[k] = true; }); }
});
var keysWanted = Object.keys(refKeys);
if (!keysWanted.length) bad.push('no sql1 reference keys');
var help = c.EVHELP['sql-cpu-high'];
if (!help || typeof help !== 'object') bad.push('no EVHELP entry for sql-cpu-high');
keysWanted.forEach(function (k) { if (!help || help[k] === undefined) bad.push('entry missing key ' + k); });
if (help && Object.prototype.toString.call(help.terms) !== '[object Array]') bad.push('terms not an array');

// render the whole evidence card for the card
var card = null, list = c.SCEN.sql, i;
for (i = 0; i < list.length; i++) { if (list[i].id === 'sql-cpu-high') { card = list[i]; } }
if (!card) bad.push('sql-cpu-high card missing');
var h = '';
if (card) {
  h = c.evidenceCard(card);
  if (h.indexOf('undefined') >= 0) bad.push('evidenceCard shows undefined');
  if (cnt(h, 'class="ev-tt"') < 1) bad.push('evidenceCard has no ev-tt tooltip');
  if (!/data-tip="[^"]+: [^"]+"/.test(h)) bad.push('tooltip lacks term: meaning');
  if (h.indexOf('class="ev-look"') < 0) bad.push('evidenceCard has no ev-look');
  if (h.indexOf('class="ev-help"') < 0) bad.push('evidenceCard has no ev-help');
  if (h.indexOf('showplan in SSMS or Azure Data Studio') < 0) bad.push('showplan text missing');
}

// plain-English words and the real numbers from the card, in the help text only
var helpText = help ? c.evHelpLook(card) + c.evHelpMore(card) : '';
['CPU', 'wait', 'query ID', 'p95'].forEach(function (w) { if (helpText.indexOf(w) < 0) bad.push('help text lacks ' + w); });
['44%', '+124 ms', '31%', '+104 ms', 'PAGEIOLATCH_SH', 'WRITELOG', 'SLEEP_TASK', '2017+', 'showplan in SSMS or Azure Data Studio', 'top waits and the top queries'].forEach(function (w) { if (helpText.indexOf(w) < 0) bad.push('help text lacks ' + w); });

console.log(bad.length ? 'FAIL ' + bad.join('; ') : 'PASS');
