// usage (from buildout/wireframes) after apply: node ../packets/data/chk-s1B.js   prints PASS or FAIL <reason>
var fs = require('fs'), vm = require('vm'), bad = [];
function rd(p) { return fs.readFileSync(p, 'utf8'); }
var files = fs.readdirSync('parts').filter(function (f) {
  return /\.js$/.test(f) && /^(10|11|12|13|14|40|42)-/.test(f);
}).sort();
['parts/10-data5-toolsB.js', 'parts/42-tools-b.js'].forEach(function (p) { if (/=>|`|\blet\b|\bconst\b/.test(rd(p))) bad.push(p + ' not ES5'); });
var c = { renders: 0 };
c.render = function () { c.renders++; };
c.document = { getElementById: function () { return null; } };
c.nearbyPanel = function () { return 'NB_STUB_PANEL'; };
c.window = c;
vm.createContext(c);
try {
  files.forEach(function (f) { vm.runInContext(rd('parts/' + f), c); });
} catch (e) { console.log('FAIL load ' + e.message); process.exit(0); }
['TBWindowMs', 'toolBody'].forEach(function (f) { if (typeof c[f] !== 'function') bad.push('missing ' + f); });
if (bad.length) { console.log('FAIL ' + bad.join('; ')); process.exit(0); }
function run(s) { return vm.runInContext(s, c); }

run("W = { ranSteps: true, nearbyRow: null, nearbyOpen: false, near: null, frame: { incidentStart: '2026-10-26T09:00', incidentEnd: '2026-10-26T10:00', referenceStart: '2026-10-26T07:00', referenceEnd: '2026-10-26T08:00', releases: [], releasesEntered: false, poolLimit: '100' } };");

// --- calls-in-flight: pool-limit context ---
var cif = c.toolBody('calls-in-flight');
if (cif.indexOf('Calls in flight peaked at 34 of the 100 entered as the pool limit. The pool was not filled.') < 0) bad.push('calls-in-flight: limit text missing with limit=100');
if (cif.indexOf('No pool limit was entered') >= 0) bad.push('calls-in-flight: no-limit text must not show when a limit is entered');
run("W.frame.poolLimit = '';");
var cif2 = c.toolBody('calls-in-flight');
if (cif2.indexOf('Calls in flight peaked at 34. No pool limit was entered in Step 0, so there is nothing to compare it against.') < 0) bad.push('calls-in-flight: no-limit text missing with limit empty');
if (cif2.indexOf('entered as the pool limit') >= 0) bad.push('calls-in-flight: limit text must not show when empty');
run("W.frame.poolLimit = '   ';");
var cif3 = c.toolBody('calls-in-flight');
if (cif3.indexOf('No pool limit was entered in Step 0') < 0) bad.push('calls-in-flight: whitespace-only limit must be treated as empty');
run("W.frame.poolLimit = '250';");
var cif4 = c.toolBody('calls-in-flight');
if (cif4.indexOf('Calls in flight peaked at 34 of the 250 entered as the pool limit.') < 0) bad.push('calls-in-flight: entered value 250 not substituted');
run("W.frame.poolLimit = '100';");

// --- nearby-activity: swim-lane chart wired via nearView, guarded by typeof ---
var na = c.toolBody('nearby-activity');
if (na.indexOf('NB_STUB_PANEL') < 0) bad.push('nearby-activity: panel missing');
if (na.indexOf('Nearby activity around the onset (09:14 UTC)') < 0) bad.push('nearby-activity: nearView chart not wired in');
if (na.indexOf('<svg') < 0) bad.push('nearby-activity: no chart svg drawn');
var savedNearView = c.nearView;
c.nearView = undefined;
var naNo = c.toolBody('nearby-activity');
if (naNo !== 'NB_STUB_PANEL') bad.push('nearby-activity: must fall back to the panel alone when nearView is absent, got ' + JSON.stringify(naNo));
c.nearView = savedNearView;

// --- changes: long-window note, guarded by typeof TBWindowMs ---
run("W.frame.releasesEntered = true; W.frame.releases = CHANGES0;");
var chShort = c.toolBody('changes');
if (chShort.indexOf('This window spans more than a day.') >= 0) bad.push('changes: long-window note must not show for a 1h window');
if (chShort.indexOf('Only changes you listed are compared. Unlisted changes are invisible to this step.') < 0) bad.push('changes: base note missing');
run("W.frame.incidentStart = '2026-10-24T09:00'; W.frame.incidentEnd = '2026-10-26T09:00';");
var chLong = c.toolBody('changes');
if (chLong.indexOf('This window spans more than a day. A change from early in a long window is easy to miss if it is not already in the list above.') < 0) bad.push('changes: long-window note missing for a 2-day window');
run("W.frame.incidentStart = '2026-10-26T09:00'; W.frame.incidentEnd = '2026-10-26T10:00';");

// --- sql-waits must be left alone ---
var sw = c.toolBody.toString && rd('parts/42-tools-b.js');
if (sw.indexOf('Wait classes in the incident window (dummy; the repo has no per-wait table)') < 0) bad.push('sql-waits: must be left in place, unmodified');

// --- wiring checks on the edited file ---
var e = rd('parts/42-tools-b.js');
function cnt(s, t) { return s.split(t).length - 1; }
if (cnt(e, "    + '<p class=\"note\">Calls in flight peaked at 34 of the 100 entered as the pool limit. The pool was not filled.</p>'") !== 0) bad.push('old calls-in-flight line still present');
if (cnt(e, "    + (W.frame.poolLimit && W.frame.poolLimit.trim() !== '' ? '<p class=\"note\">Calls in flight peaked at 34 of the ' + W.frame.poolLimit.trim() + ' entered as the pool limit. The pool was not filled.</p>' : '<p class=\"note\">Calls in flight peaked at 34. No pool limit was entered in Step 0, so there is nothing to compare it against.</p>')") !== 1) bad.push('new calls-in-flight line not wired once');
if (cnt(e, '  return nearbyPanel();') !== 0) bad.push('old nearby-activity line still present');
if (cnt(e, "  return nearbyPanel(true) + (typeof nearView === 'function' ? nearView() : '');") !== 1) bad.push('new nearby-activity line not wired once');
if (cnt(e, "    + (typeof TBWindowMs === 'function' && (TBWindowMs(W.frame.incidentEnd) - TBWindowMs(W.frame.incidentStart)) > 86400000 ? '<p class=\"note\">This window spans more than a day. A change from early in a long window is easy to miss if it is not already in the list above.</p>' : '');") !== 1) bad.push('new changes line not wired once');
try { new Function(e); } catch (x) { bad.push('syntax ' + x.message); }
try { new Function(rd('parts/10-data5-toolsB.js')); } catch (x) { bad.push('syntax new file ' + x.message); }

console.log(bad.length ? 'FAIL ' + bad.join('; ') : 'PASS');
