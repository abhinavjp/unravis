// usage (from buildout/wireframes): node ../packets/data/chk-s2a.js   prints PASS or FAIL <reason>
var fs = require('fs'), vm = require('vm');
var src = fs.readFileSync('parts/30-frame-helpers.js', 'utf8'), bad = [];
if (/=>|`|\blet\b|\bconst\b/.test(src)) bad.push('not ES5');
var c = { renders: 0, esc: function (s) { return String(s); } };
c.render = function () { c.renders++; };
vm.createContext(c);
try { vm.runInContext(src, c); } catch (e) { console.log('FAIL load ' + e.message); process.exit(0); }
['fpMs', 'fpIso', 'fpPad', 'fpWhen', 'fpDate', 'fpDur', 'fpShort', 'fpKey', 'frameBannerKind', 'frameKeepTouching', 'frameBanner', 'frameFix', 'frameReleaseHint', 'frameReleaseLine', 'frameEstimateLine', 'fpNthSunday', 'fpLastSunday', 'frameDst'].forEach(function (f) {
  if (typeof c[f] !== 'function') bad.push('missing ' + f);
});
if (bad.length) { console.log('FAIL ' + bad.join('; ')); process.exit(0); }
function run(code) { return vm.runInContext(code, c); }
function setF(a, b, r1, r2) { run("W = { frame: { incidentStart: '" + a + "', incidentEnd: '" + b + "', referenceStart: '" + r1 + "', referenceEnd: '" + r2 + "', releases: [] } };"); }
function eq(label, got, want) { if (got !== want) bad.push(label + ' -> ' + JSON.stringify(got) + ' wanted ' + JSON.stringify(want)); }
function has(label, s, part) { if (String(s).indexOf(part) < 0) bad.push(label + ' lacks ' + JSON.stringify(part) + ' in ' + String(s).slice(0, 200)); }
eq('fpDur 3d', c.fpDur(259200000), '3 days');
eq('fpDur 5m', c.fpDur(300000), '5 minutes');
eq('fpDur 1h30', c.fpDur(5400000), '1 h 30 min');
eq('fpDur 1d', c.fpDur(86400000), '1 day');
eq('fpDur 1h', c.fpDur(3600000), '1 hour');
eq('fpShort 10d', c.fpShort(864000000), '10d');
eq('fpShort 45m', c.fpShort(2700000), '45m');
eq('fpShort 1h30', c.fpShort(5400000), '1h 30m');
eq('fpWhen', c.fpWhen(Date.UTC(2026, 9, 26, 9, 20)), '26 Oct 09:20 UTC');
eq('fpDate', c.fpDate(Date.UTC(2026, 10, 1)), '1 Nov 2026');
eq('fpMs empty', isNaN(c.fpMs('')), true);
eq('fpIso', c.fpIso(Date.UTC(2026, 9, 26, 9, 5)), '2026-10-26T09:05');
eq('nth sunday US end 2026', c.fpNthSunday(2026, 10, 1), Date.UTC(2026, 10, 1));
eq('nth sunday US start 2026', c.fpNthSunday(2026, 2, 2), Date.UTC(2026, 2, 8));
eq('last sunday EU start 2026', c.fpLastSunday(2026, 2), Date.UTC(2026, 2, 29));
eq('last sunday EU end 2026', c.fpLastSunday(2026, 9), Date.UTC(2026, 9, 25));
// default frame is touching but starts dismissed
setF('2026-10-26T09:00', '2026-10-26T10:00', '2026-10-26T08:00', '2026-10-26T09:00');
eq('kind touching', c.frameBannerKind(), 'touching');
eq('default touching dismissed', c.frameBanner(), '');
// a later touching frame is not dismissed
setF('2026-10-26T11:00', '2026-10-26T12:00', '2026-10-26T10:00', '2026-10-26T11:00');
run("W.frameKeep = 'x';");
var t = c.frameBanner();
has('touching text', t, 'The reference window ends the instant the incident starts, with no gap between them.');
has('touching button', t, "frameFix('gap')"); has('touching label', t, 'Add a 5-minute gap'); has('keep', t, "frameFix('keep')"); has('keep label', t, 'Keep anyway'); has('role', t, 'role="status"'); has('notice class', t, 'class="notice"');
c.frameFix('gap');
eq('gap start', run('W.frame.referenceStart'), '2026-10-26T09:55'); eq('gap end', run('W.frame.referenceEnd'), '2026-10-26T10:55');
eq('gap clears banner', c.frameBannerKind(), '');
// overlap
setF('2026-10-26T09:00', '2026-10-26T10:00', '2026-10-26T08:30', '2026-10-26T09:30'); run("W.frameKeep = 'x';");
eq('kind overlap', c.frameBannerKind(), 'overlap');
t = c.frameBanner();
has('overlap text', t, 'The reference window overlaps the incident window by 30 minutes. Comparing against a period that includes the problem is not a fair baseline.');
has('overlap fix', t, "frameFix('move')"); has('overlap label', t, 'Move reference before the incident');
var before = c.renders;
c.frameFix('move');
eq('move end', run('W.frame.referenceEnd'), '2026-10-26T08:55'); eq('move start', run('W.frame.referenceStart'), '2026-10-26T07:55');
eq('move rendered', c.renders, before + 1);
eq('move clears', c.frameBannerKind(), '');
// keep anyway
setF('2026-10-26T09:00', '2026-10-26T10:00', '2026-10-26T08:30', '2026-10-26T09:30'); run("W.frameKeep = 'x';");
c.frameFix('keep');
eq('keep hides', c.frameBanner(), '');
run("W.frame.referenceStart = '2026-10-26T08:20';");
has('keep resets on edit', c.frameBanner(), 'overlaps the incident window by 30 minutes');
// ratio, reference longer
setF('2026-10-26T00:00', '2026-10-27T00:00', '2026-10-15T00:00', '2026-10-25T00:00'); run("W.frameKeep = 'x';");
eq('kind ratio', c.frameBannerKind(), 'ratio');
t = c.frameBanner();
has('ratio text', t, 'The reference window is 10&times; the length of the incident window (10d vs 1d). Rates are compared per minute, but very different lengths make rare events harder to compare fairly.');
has('ratio fix', t, "frameFix('match')"); has('ratio label', t, 'Match the incident window length');
c.frameFix('match');
eq('match start', run('W.frame.referenceStart'), '2026-10-24T00:00'); eq('match end', run('W.frame.referenceEnd'), '2026-10-25T00:00');
// ratio, incident longer
setF('2026-10-20T00:00', '2026-10-23T12:00', '2026-10-18T00:00', '2026-10-19T00:00'); run("W.frameKeep = 'x';");
has('ratio incident longer', c.frameBanner(), 'The incident window is 3.5&times; the length of the reference window (84h vs 1d)');
// priority and ok
setF('2026-10-26T09:00', '2026-10-26T10:00', '2026-10-01T00:00', '2026-10-26T09:30');
eq('overlap beats ratio', c.frameBannerKind(), 'overlap');
setF('2026-10-26T09:00', '2026-10-26T10:00', '2026-10-26T07:00', '2026-10-26T08:00');
eq('fair windows', c.frameBannerKind(), ''); eq('fair banner', c.frameBanner(), '');
setF('', '2026-10-26T10:00', '2026-10-26T07:00', '2026-10-26T08:00');
eq('invalid kind', c.frameBannerKind(), ''); eq('invalid estimate', c.frameEstimateLine(), '');
// release lines
setF('2026-10-26T09:00', '2026-10-26T10:00', '2026-10-26T07:00', '2026-10-26T08:00');
has('hint', c.frameReleaseHint(), 'If you know of a deploy, flag flip or config change near the start of the slowdown, add it here. Unravis does not assume one happened.');
run("W.frame.releases = [{at:'2026-10-26T09:20',label:'x'}];");
eq('hint hidden', c.frameReleaseHint(), '');
has('release inside', c.frameReleaseLine({ at: '2026-10-26T09:20' }), 'This change is inside the incident window (26 Oct 09:20 UTC). It may be the cause rather than just a marker &mdash; Step 5 will line it up against the onset.');
eq('release outside', c.frameReleaseLine({ at: '2026-10-26T08:40' }), '');
eq('release empty', c.frameReleaseLine({ at: '' }), '');
// estimate
setF('2026-10-26T09:00', '2026-10-26T10:00', '2026-10-26T07:00', '2026-10-26T08:00');
t = c.frameEstimateLine();
has('est 1h', t, 'This reads &asymp; 4,800 calls from 2 sources and takes about 3 s.'); if (t.indexOf('hourly') >= 0) bad.push('est 1h must not mention hourly');
setF('2026-10-26T00:00', '2026-10-27T00:00', '2026-10-24T00:00', '2026-10-25T00:00');
has('est 1d', c.frameEstimateLine(), 'This reads &asymp; 115,000 calls from 2 sources and takes about 20 s.');
setF('2026-09-01T00:00', '2026-10-01T00:00', '2026-07-01T00:00', '2026-07-31T00:00');
t = c.frameEstimateLine();
has('est 30d rows', t, '3.4M calls from 2 sources and takes about 4 min.'); has('est 30d hourly', t, ' Shown as hourly averages &mdash; see below.');
run('W.srcCount = 1;'); has('est 1 source', c.frameEstimateLine(), 'from 1 source and');
// dst
setF('2026-11-01T00:00', '2026-11-02T00:00', '2026-10-30T00:00', '2026-10-31T00:00');
has('dst nov', c.frameDst(), 'This window crosses a daylight-saving change on 1 Nov 2026. All times above stay in UTC; the same UTC time may map to a different local clock time before and after this date.');
setF('2026-10-26T09:00', '2026-10-26T10:00', '2026-10-26T07:00', '2026-10-26T08:00');
eq('no dst default', c.frameDst(), '');
setF('2026-03-08T09:00', '2026-03-08T10:00', '2026-03-07T09:00', '2026-03-07T10:00');
has('dst march', c.frameDst(), 'on 8 Mar 2026');
console.log(bad.length ? 'FAIL ' + bad.join('; ') : 'PASS');
