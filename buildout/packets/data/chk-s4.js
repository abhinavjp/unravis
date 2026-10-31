// usage (from buildout/wireframes) after apply: node ../packets/data/chk-s4.js   prints PASS or FAIL <reason>
var fs = require('fs'), vm = require('vm'), bad = [];
function rd(p) { return fs.readFileSync(p, 'utf8'); }
var src = rd('parts/10-data3-near.js');
if (/=>|`|\blet\b|\bconst\b/.test(src)) bad.push('not ES5');
if (/Math\.random|Date\.now|new Date\(/.test(src)) bad.push('fixture not deterministic');
var vars = (src.match(/^var\s+[A-Za-z_$][\w$]*/gm) || []).join(',');
if (vars !== 'var NEAR_ROWS') bad.push('top-level vars: ' + vars);
var fns = (src.match(/^function\s+[A-Za-z_$][\w$]*/gm) || []).join('|');
if (fns !== 'function nearSet|function nearView') bad.push('top-level functions: ' + fns);
var c = { renders: 0 }; c.render = function () { c.renders++; }; vm.createContext(c);
try {
  vm.runInContext(rd('parts/11-charts-c.js'), c); vm.runInContext(src, c);
} catch (e) { console.log('FAIL load ' + e.message); process.exit(0); }
if (typeof c.nearSet !== 'function' || typeof c.nearView !== 'function') bad.push('missing function');
if (!c.NEAR_ROWS || c.NEAR_ROWS.length < 12 || c.NEAR_ROWS.length > 16) bad.push('NEAR_ROWS count ' + (c.NEAR_ROWS && c.NEAR_ROWS.length));
if (bad.length) { console.log('FAIL ' + bad.join('; ')); process.exit(0); }
function run(s) { return vm.runInContext(s, c); }
function eq(l, g, w) { if (g !== w) bad.push(l + ' -> ' + JSON.stringify(g) + ' wanted ' + JSON.stringify(w)); }
function cnt(s, t) { return String(s).split(t).length - 1; }
function has(s, t) { if (String(s).indexOf(t) < 0) bad.push('lacks ' + JSON.stringify(t)); }
run('W = {};');
var v = c.nearView();
eq('lazy state', run('W.near.min'), 5);
if (v.indexOf('<section class="nearby near" aria-label="Nearby activity">') !== 0) bad.push('section open wrong');
has(v, '<div class="sthead"><h4>Nearby activity around the onset (09:14 UTC)</h4></div>');
has(v, '<p class="note">Every source we have, on one time line: the shaded band is the slice the buttons choose, and the table lists what falls inside it. MongoDB, Host, IIS and Windows events have no source in this case, so those lanes stay empty.</p>');
has(v, 'role="radiogroup" aria-label="Minutes around the onset"');
eq('radios', cnt(v, 'name="near-min"'), 3);
eq('checked 5', cnt(v, 'value="5" checked'), 1);
eq('onchange wired', cnt(v, 'onchange="nearSet('), 3);
has(v, 'class="axis-label">API</text>');
has(v, 'class="axis-label">SQL</text>');
has(v, 'class="axis-label">Mongo</text>');
has(v, 'class="axis-label">Host</text>');
has(v, 'minute +60 to +119');
has(v, 'Shaded band is the selected time slice');
eq('one svg', cnt(v, '<svg'), 1);
eq('events drawn', cnt(v, '<circle class="ev') + cnt(v, '<rect class="ev'), 14);
eq('bar events', cnt(v, '<rect class="ev'), 1);
// default band: 5 min around minute 74 -> 09:12 to 09:17 UTC, 7 of 14
has(v, 'Events inside the shaded band, 09:12 to 09:17 UTC (7 of 14).');
eq('in-brush 5', cnt(v, 'class="ev in-brush'), 7);
eq('in-brush bar', cnt(v, '<rect class="ev in-brush'), 1);
eq('table rows 5', cnt(v, '<tr><td>'), 7);
has(v, '<th>Time (UTC)</th><th>Source</th><th>What happened</th>');
eq('view does not render', c.renders, 0);
var r0 = c.renders;
c.nearSet(2);
eq('set 2', run('W.near.min'), 2);
eq('render on set', c.renders, r0 + 1);
var v2 = c.nearView();
has(v2, 'Events inside the shaded band, 09:13 to 09:15 UTC (4 of 14).');
eq('in-brush 2', cnt(v2, 'class="ev in-brush'), 4);
eq('table rows 2', cnt(v2, '<tr><td>'), 4);
eq('checked 2', cnt(v2, 'value="2" checked'), 1);
if (v2.indexOf('09:07:10') >= 0) bad.push('2-min band must not list 09:07:10');
c.nearSet(15);
var v15 = c.nearView();
has(v15, 'Events inside the shaded band, 09:07 to 09:22 UTC (12 of 14).');
eq('in-brush 15', cnt(v15, 'class="ev in-brush'), 12);
eq('table rows 15', cnt(v15, '<tr><td>'), 12);
eq('checked 15', cnt(v15, 'value="15" checked'), 1);
has(v15, '<tr><td>09:07:10</td><td>API</td><td>Orders.List call, 450 ms</td></tr>');
if (v15.indexOf('09:23:00') >= 0) bad.push('15-min band must not list 09:23:00');
// guards
run('W.near = { min: 99 };');
eq('odd min falls back', cnt(c.nearView(), 'value="5" checked'), 1);
run('W.near = null;');
c.nearSet(5);
eq('re-init after null', run('W.near && W.near.min'), 5);
// empty fixture edge (mutates NEAR_ROWS; keep last)
run('NEAR_ROWS.length = 0;');
var e = c.nearView();
has(e, 'No activity to show.');
has(e, 'Nothing was recorded inside the band.');
eq('empty rows', cnt(e, '<tr><td>'), 0);
// wiring
var s = rd('parts/30-steps-0-4.js');
eq('wire near', cnt(s, "  + (typeof nearView === 'function' ? nearView() : '')"), 1);
eq('chart line kept', cnt(s, "  + minuteLine('inFlight', 'Calls in flight by minute', 'calls')"), 1);
try { new Function(s); } catch (x) { bad.push('syntax ' + x.message); }
['.near{margin-top:12px}', '.near .picks label.wf{display:flex;align-items:center;gap:6px;color:var(--text)}', 'figure.ml rect.brush{stroke:var(--accent);stroke-width:1}'].forEach(function (r) {
  if (rd('parts/02-css-near.html').indexOf(r) < 0) bad.push('css lacks ' + r);
});
console.log(bad.length ? 'FAIL ' + bad.join('; ') : 'PASS');
