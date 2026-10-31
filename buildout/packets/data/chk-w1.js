// usage (from buildout/wireframes) after apply: node ../packets/data/chk-w1.js   prints PASS or FAIL <reason>
var fs = require('fs'), vm = require('vm'), bad = [];
function rd(p) { return fs.readFileSync(p, 'utf8'); }
var c = { renders: 0 }; c.render = function () { c.renders++; }; vm.createContext(c);
var files = fs.readdirSync('parts').filter(function (f) { return /^1[0-4]-.*\.js$/.test(f) || f === '33-step7-evidence.js'; }).sort();
try { files.forEach(function (f) { vm.runInContext(rd('parts/' + f), c); }); } catch (e) { console.log('FAIL load ' + e.message); process.exit(0); }
['evFind', 'evidenceCard', 'step7Evidence', 'evDepEffVerdict', 'evDepBlocked', 'evDepBanner', 'evImpact', 'evDepToggles', 'EV_SETS', 'EVDEP_MAP', 'EVDEP_DEFS'].forEach(function (f) {
  if (typeof c[f] === 'undefined') bad.push('missing ' + f);
});
if (bad.length) { console.log('FAIL ' + bad.join('; ')); process.exit(0); }
function run(s) { return vm.runInContext(s, c); }
function eq(l, g, w) { if (g !== w) bad.push(l + ' -> ' + JSON.stringify(g) + ' wanted ' + JSON.stringify(w)); }
function has(l, h, t) { if (t && String(h).indexOf(t) < 0) bad.push(l + ' lacks ' + JSON.stringify(t)); }
function not(l, h, t) { if (t && String(h).indexOf(t) >= 0) bad.push(l + ' must not contain ' + JSON.stringify(t)); }

run('W = { deps: {} };');

// ok card: natural verdict 'ok', nothing blocked -> plain body, impact shown (if any), no banner, no fold
eq('ok card verdict', run('evFind("sql-waits-benign").verdict'), 'ok');
var okImpact = run('evImpact(evFind("sql-waits-benign"))');
var ok = run('evidenceCard(evFind("sql-waits-benign"))');
has('ok card', ok, 'class="ev ok"');
has('ok card', ok, 'class="ev-body"');
not('ok card', ok, 'ev-blocked');
not('ok card', ok, '<details class="ev-fold"><summary>Show the chart');
if (okImpact) { has('ok card', ok, okImpact); }

// watch card: effective verdict 'watch' -> chart+table collapsed behind a details/summary
eq('watch card verdict', run('evFind("sql-lock-waits-rising").verdict'), 'watch');
var watchImpact = run('evImpact(evFind("sql-lock-waits-rising"))');
var watch = run('evidenceCard(evFind("sql-lock-waits-rising"))');
has('watch card', watch, 'class="ev watch"');
has('watch card', watch, '<details class="ev-fold"><summary>Show the chart and table</summary>');
has('watch card', watch, '</details>');
not('watch card', watch, 'ev-blocked');
if (watchImpact) { has('watch card', watch, watchImpact); }

// none card (already not collected, nothing explicitly toggled off): forced banner may show,
// but the body is NOT replaced by the blocked message since evDepBlocked requires an explicit toggle
eq('none card verdict', run('evFind("sql-plan-regression").verdict'), 'none');
var none = run('evidenceCard(evFind("sql-plan-regression"))');
has('none card', none, 'class="ev none"');
has('none card', none, 'class="ev-body"');
not('none card', none, 'ev-blocked');

// explicit blocking: find a hard (non-soft) dependency key the 'ok' card actually depends on
run('var __keys = (EVDEP_MAP && EVDEP_MAP["sql-waits-benign"]) || []; var __k = null;' +
  'for (var __i = 0; __i < __keys.length; __i++) { if (EVDEP_DEFS[__keys[__i]] && EVDEP_DEFS[__keys[__i]].soft === false) { __k = __keys[__i]; break; } }');
var k = run('__k');
if (!k) {
  bad.push('sql-waits-benign has no hard dependency key in EVDEP_MAP to test blocking against');
} else {
  run('W.deps = {}; W.deps[__k] = true;');
  eq('blocked effective verdict', run('evDepEffVerdict(evFind("sql-waits-benign"))'), 'none');
  var blockedHtml = run('evidenceCard(evFind("sql-waits-benign"))');
  has('blocked card', blockedHtml, 'class="ev none"');
  has('blocked card', blockedHtml, 'class="ev-blocked"');
  has('blocked card', blockedHtml, 'No chart or table: the data this card needs was not collected.');
  not('blocked card', blockedHtml, 'class="ev-body"');
  not('blocked card', blockedHtml, '<details class="ev-fold"><summary>Show the chart');
  if (okImpact) { not('blocked card', blockedHtml, okImpact); } // impact hidden: blocked and own verdict is not 'none'
  var bannerNow = run('evDepBanner(evFind("sql-waits-benign"))');
  if (bannerNow) { has('blocked card', blockedHtml, bannerNow); }
}

// step7 wiring: toggle buttons and legend text present
var s7 = run('step7Evidence()');
has('step7', s7, 'Pretend off:');
has('step7', s7, 'Not collected');

// effective verdicts reshuffle step7's grouping: blocking the 'ok' card moves it out of the
// ruled-out drawer and into the main list as a "None" section (quiet set contains sql-waits-benign)
run('W.evSet = "quiet"; W.deps = {};');
var quietBefore = run('step7Evidence()');
if (k) { run('W.deps = {}; W.deps[__k] = true;'); }
var quietAfter = run('step7Evidence()');
run('W.deps = {};');
if (k) {
  var noneBefore = quietBefore.split('class="ev none"').length - 1;
  var noneAfter = quietAfter.split('class="ev none"').length - 1;
  if (!(noneAfter > noneBefore)) {
    bad.push('blocking sql-waits-benign must add a None section to the quiet set (' + noneBefore + ' -> ' + noneAfter + ')');
  }
}

// static wiring / syntax checks on the edited source
var d = rd('parts/12-evidence-d.js');
var t = rd('parts/33-step7-evidence.js');
if (/=>|`|\blet\b|\bconst\b/.test(d + t)) bad.push('not ES5');
eq('ver2 uses', t.split('ver2').length - 1, 5);
eq('evDepToggles wired', t.split("((typeof evDepToggles === 'function') ? evDepToggles('sql') : '')").length - 1, 1);
eq('evDepEffVerdict wired in card', d.split('evDepEffVerdict').length - 1, 2);
eq('evDepBlocked wired in card', d.split('evDepBlocked').length - 1, 2);
eq('evDepBanner wired in card', d.split('evDepBanner').length - 1, 2);
eq('evImpact wired in card', d.split('evImpact').length - 1, 2);
eq('blocked message wired once', d.split('No chart or table: the data this card needs was not collected.').length - 1, 1);
if (rd('parts/02-css-card.html').indexOf('.ev-blocked{') < 0) bad.push('css missing .ev-blocked');
if (rd('parts/02-css-card.html').indexOf('.ev .ev-dep{') < 0) bad.push('css missing .ev .ev-dep');
if (rd('parts/02-css-card.html').indexOf('.ev .ev-impact{') < 0) bad.push('css missing .ev .ev-impact');
try { new Function(d); new Function(t); } catch (x) { bad.push('syntax ' + x.message); }

console.log(bad.length ? 'FAIL ' + bad.join('; ') : 'PASS');
