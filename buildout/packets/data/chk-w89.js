// usage (from buildout/wireframes) after apply: node ../packets/data/chk-w89.js   prints PASS or FAIL <reason>
var fs = require('fs'), vm = require('vm'), bad = [];
function rd(p) { return fs.readFileSync(p, 'utf8'); }
var src = rd('parts/31-steps-5-9.js');
if (/=>|`|\blet\b|\bconst\b/.test(src)) bad.push('not ES5');

// Dependency files (10-14, 30-steps-0-4, the edited 31, and the 34/35 evidence files).
// 20-shell.js is NOT loaded: it runs top-level DOM wiring with no document in this sandbox.
// nutshell() and reasonsBox() are stable, already-shipped functions from 20-shell.js, reproduced
// here verbatim so step8/step9 can run; this packet does not touch either function.
var files = [
  '10-data.js', '11-charts-a.js', '11-charts-axis.js', '11-charts-b.js',
  '12-evidence-a.js', '12-evidence-b.js', '12-evidence-c.js', '12-evidence-d.js',
  '12-evidence-e.js', '12-evidence-f.js', '12-evidence-g.js',
  '13-why-a.js', '13-why-b.js',
  '14-deps-data.js', '14-scenarios.js',
  '30-steps-0-4.js', '31-steps-5-9.js',
  '34-step8-evidence.js', '35-step9-evidence.js'
];
var c = { renders: 0 };
c.render = function () { c.renders++; };
c.window = c;
c.stateSeg = function () { return ''; };
c.inert = function () {};
c.dt = function (t, h) { return '<dt>' + t + '</dt><dd>' + h + '</dd>'; };
c.nutshell = function (text) { return '<div class="nutshell"><p class="nl">In a nutshell</p><p>' + text + '</p></div>'; };
c.reasonsBox = function (list) {
  if (!list.length) return '';
  return '<div><p class="nl" style="color:var(--text-2);font-size:12px;font-weight:600;letter-spacing:.04em;text-transform:uppercase">What could not be tested</p><ul style="margin:0;padding-left:18px;color:var(--text-2)"><li>' + list.join('</li><li>') + '</li></ul></div>';
};
vm.createContext(c);
try {
  files.forEach(function (f) { vm.runInContext(rd('parts/' + f), c); });
} catch (e) { console.log('FAIL load ' + e.message); process.exit(0); }
['step8', 'step9', 'step8Evidence', 'step9Evidence'].forEach(function (f) { if (typeof c[f] !== 'function') bad.push('missing ' + f); });
if (bad.length) { console.log('FAIL ' + bad.join('; ')); process.exit(0); }

function run(s) { return vm.runInContext(s, c); }
function has(l, h, t) { if (String(h).indexOf(t) < 0) bad.push(l + ' lacks ' + JSON.stringify(t)); }
function not(l, h, t) { if (String(h).indexOf(t) >= 0) bad.push(l + ' must not contain ' + JSON.stringify(t)); }

run('W = { why: null, evSet: "incident", checklist: [], deps: {} };');

// ---- step8: incident (bad cards) ----
var s8i = c.step8('Ready');
has('step8 incident nutshell', s8i, 'WiredTiger tickets ran out while the secondary fell over 20 seconds behind');
has('step8 incident findings', s8i, 'Orders.Create p95 latency rose from 41 ms to 191 ms and slow ops rose from 3 to 48 a minute.');
has('step8 incident bad card', s8i, 'class="ev bad"');
has('step8 incident switcher', s8i, 'aria-label="Pretend case"');
has('step8 incident toggles', s8i, 'aria-label="Pretend a source is missing"');
has('step8 incident legend', s8i, 'class="ev-legend"');
has('step8 incident suspects', s8i, 'Suspects checked in this step');
has('step8 incident why button', s8i, 'Why this colour?');

// ---- step8: quiet ----
run('W.evSet = "quiet";');
var s8q = c.step8('Ready');
has('step8 quiet nutshell', s8q, 'No slow operation above 100 ms was recorded. The replica set was stable.');
has('step8 quiet findings', s8q, 'The replica set held its primary through the window.');

// ---- step8: watch ----
run('W.evSet = "watch";');
var s8w = c.step8('Ready');
has('step8 watch nutshell', s8w, 'nothing reached the danger line yet');
has('step8 watch findings', s8w, 'Connections in use rose from 120 to 149, with plenty of the pool still free.');

// ---- step8: unknown evSet falls back to incident ----
run('W.evSet = "nonsense";');
var s8n = c.step8('Ready');
has('step8 unknown evSet falls back', s8n, 'WiredTiger tickets ran out while the secondary fell over 20 seconds behind');

// ---- step8: Why view replaces the card list ----
run('W.evSet = "incident"; W.why = "MG-BAD-SLOWOPS";');
var s8y = c.step8('Ready');
has('step8 why view', s8y, 'class="why-view"');
has('step8 why back button', s8y, 'Back to the evidence');
not('step8 why hides switcher', s8y, 'aria-label="Pretend case"');
not('step8 why hides findings', s8y, 'What the data shows');

// ---- step8: unknown why id resets W.why and falls back to the card list ----
run('W.why = "no-such-card";');
var s8r = c.step8('Ready');
if (run('W.why') !== null) bad.push('step8 did not reset an unknown why id');
has('step8 after reset shows cards', s8r, 'aria-label="Pretend case"');

// ---- step8: other states untouched ----
has('step8 waiting', c.step8('Waiting'), 'Run the steps first to fill it in.');
has('step8 cannot test yet', c.step8('Cannot test yet'), 'This case has no MongoDB source.');

// ---- step9: incident (bad cards) ----
run('W.why = null; W.evSet = "incident";');
var s9i = c.step9('Ready');
has('step9 incident nutshell', s9i, 'the hosts were saturated');
has('step9 incident findings', s9i, 'Available memory fell from 11.8 GB to under 300 MB with one OOM kill.');
has('step9 incident bad card', s9i, 'class="ev bad"');
has('step9 incident switcher', s9i, 'aria-label="Pretend case"');
has('step9 incident toggles', s9i, 'aria-label="Pretend a source is missing"');

// ---- step9: quiet ----
run('W.evSet = "quiet";');
var s9q = c.step9('Ready');
has('step9 quiet nutshell', s9q, 'Processor, memory, disk and network of the hosts stayed inside their normal range.');
has('step9 quiet findings', s9q, 'Network errors: none recorded.');

// ---- step9: watch ----
run('W.evSet = "watch";');
var s9w = c.step9('Ready');
has('step9 watch nutshell', s9w, 'short of saturation');
has('step9 watch findings', s9w, 'Network traffic more than doubled from 4.2 MB/s to 9.2 MB/s, but error counters are not collected.');

// ---- step9: Why view ----
run('W.why = "GS-07-host-cpu-red";');
var s9y = c.step9('Ready');
has('step9 why view', s9y, 'class="why-view"');
not('step9 why hides switcher', s9y, 'aria-label="Pretend case"');

// ---- step9: other states untouched ----
run('W.why = null;');
has('step9 waiting', c.step9('Waiting'), 'Run the steps first to fill it in.');
has('step9 cannot test yet', c.step9('Cannot test yet'), 'This case has no Blackbox on a host.');
has('step9 failed', c.step9('Failed'), 'The steps could not run.');

// ---- stepNotes8 / stepNotes9: plain-words copy for the three new contract items ----
var n8 = run('window.stepNotes8()');
has('stepNotes8 evidence cards', n8, '<dt>Evidence cards</dt>');
has('stepNotes8 evidence cards text', n8, 'Each card is colour-coded: green means it looked normal, amber means worth a look, red means it is a likely cause, and gray means it was not collected for this case.');
has('stepNotes8 pretend off', n8, '<dt>Pretend off toggles</dt>');
has('stepNotes8 pretend off text', n8, 'click one to pretend a MongoDB source is missing, and its card turns gray with a "Could not check" note');
has('stepNotes8 why view', n8, '<dt>Why view</dt>');
has('stepNotes8 why view text', n8, 'Click "Why this colour?" on any card to open the Why view');

var n9 = run('window.stepNotes9()');
has('stepNotes9 evidence cards', n9, '<dt>Evidence cards</dt>');
has('stepNotes9 pretend off', n9, '<dt>Pretend off toggles</dt>');
has('stepNotes9 pretend off text', n9, 'click one to pretend a host signal was never collected, and its card turns gray with a "Could not check" note');
has('stepNotes9 why view', n9, '<dt>Why view</dt>');

// ---- wiring: old literal bodies must be gone, step8Evidence/step9Evidence calls must be guarded ----
not('old step8 body gone', src, "nutshell('No slow operation above 100 ms was recorded. The replica set was stable.')\n  + '<p class=\"lead\" style=\"color:var(--text)\">Checks on MongoDB: slow operations, queues and the replica set.</p>'\n  + findingsList(");
not('old step9 body gone', src, "nutshell('Processor, memory, disk and network of the hosts stayed inside their normal range.')");
if (src.split("(typeof step8Evidence === 'function' ? step8Evidence() : '')").length - 1 !== 2) bad.push('step8Evidence must be called guarded exactly twice (Why branch + Ready branch)');
if (src.split("(typeof step9Evidence === 'function' ? step9Evidence() : '')").length - 1 !== 2) bad.push('step9Evidence must be called guarded exactly twice (Why branch + Ready branch)');

try { new Function(src); } catch (x) { bad.push('syntax ' + x.message); }

console.log(bad.length ? 'FAIL ' + bad.join('; ') : 'PASS');
