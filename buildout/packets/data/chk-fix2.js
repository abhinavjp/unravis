// usage (from buildout/wireframes) after apply: node ../packets/data/chk-fix2.js   prints PASS or FAIL <reason>
var fs = require('fs'), vm = require('vm'), bad = [];
function rd(p) { return fs.readFileSync(p, 'utf8'); }
function cnt(s, t) { return s.split(t).length - 1; }
var BLOCKED = '<p class="ev-blocked">No chart or table: the data this card needs was not collected.</p>';

var whySrc = rd('parts/13-why-b.js');
var depSrc = rd('parts/12-evidence-g.js');
if (/=>|`|\blet\b|\bconst\b/.test(whySrc)) bad.push('13-why-b.js not ES5');
if (/=>|`|\blet\b|\bconst\b/.test(depSrc)) bad.push('12-evidence-g.js not ES5');

var c = { renders: 0, W: { deps: {} } };
c.render = function () { c.renders++; };
c.setTimeout = function () {};
c.navigator = {};
c.document = {
  createElement: function () { return { style: {}, select: function () {}, setAttribute: function () {} }; },
  body: { appendChild: function () {}, removeChild: function () {} },
  execCommand: function () { return true; }
};
vm.createContext(c);
var files = [
  '11-charts-a.js', '11-charts-axis.js', '11-charts-b.js',
  '12-evidence-a.js', '12-evidence-b.js', '12-evidence-c.js', '12-evidence-d.js',
  '12-evidence-e.js', '12-evidence-f.js', '12-evidence-g.js',
  '13-why-a.js', '13-why-b.js', '14-deps-data.js'
];
try {
  files.forEach(function (f) { vm.runInContext(rd('parts/' + f), c); });
} catch (e) { console.log('FAIL load ' + e.message); process.exit(0); }

function run(s) { try { return vm.runInContext(s, c); } catch (e) { return 'ERR:' + e.message; } }
function eq(l, g, w) { if (g !== w) bad.push(l + ' -> ' + JSON.stringify(g) + ' wanted ' + JSON.stringify(w)); }
function has(l, h, t) { if (String(h).indexOf(t) < 0) bad.push(l + ' lacks ' + JSON.stringify(t)); }
function not(l, h, t) { if (String(h).indexOf(t) >= 0) bad.push(l + ' must not contain ' + JSON.stringify(t)); }

// ---- EVDEP_NAMES: every EVDEP_DEFS id has a short name ----
eq('names defined', run('typeof EVDEP_NAMES'), 'object');
eq('no extra names', run('Object.keys(EVDEP_NAMES).length'), run('Object.keys(EVDEP_DEFS).length'));
eq('every id named', run('Object.keys(EVDEP_DEFS).every(function(k){return typeof EVDEP_NAMES[k] === "string" && EVDEP_NAMES[k].length > 0;})'), true);
eq('name querystore', run('EVDEP_NAMES.querystore'), 'Query Store');
eq('name qswaits', run('EVDEP_NAMES.qswaits'), 'Query Store wait stats');
eq('name systemhealth', run('EVDEP_NAMES.systemhealth'), 'system_health capture');
eq('name viewstate', run('EVDEP_NAMES.viewstate'), 'VIEW SERVER STATE');
eq('name profiler', run('EVDEP_NAMES.profiler'), 'Profiler trace');
eq('name monitorrole', run('EVDEP_NAMES.monitorrole'), 'Monitor role');
eq('name oplogread', run('EVDEP_NAMES.oplogread'), 'oplog read');
eq('name blackbox', run('EVDEP_NAMES.blackbox'), 'Blackbox');
eq('name perfaccess', run('EVDEP_NAMES.perfaccess'), 'performance counter access');

// ---- pretend-off buttons: short labels in both states ----
run('W = { deps: {} };');
var p1 = run('evDepToggles("sql")');
has('unpressed querystore', p1, 'Pretend off: Query Store</button>');
has('unpressed qswaits', p1, 'Pretend off: Query Store wait stats</button>');
has('unpressed viewstate', p1, 'Pretend off: VIEW SERVER STATE</button>');
not('label must not be the long title', p1, 'Query Store is off for this database');
has('unpressed aria', p1, 'aria-pressed="false"');
has('unpressed handler', p1, 'onclick="evDepToggle(\'querystore\')"');
var ph = run('evDepToggles("host")');
has('host blackbox', ph, 'Pretend off: Blackbox</button>');
has('host perfaccess', ph, 'Pretend off: performance counter access</button>');
not('host has no sql', ph, 'VIEW SERVER STATE</button>');
run('evDepToggle("querystore");');
var p2 = run('evDepToggles("sql")');
has('pressed querystore', p2, 'Stop pretending: Query Store</button>');
has('pressed aria', p2, 'aria-pressed="true"');
has('pressed handler', p2, 'onclick="evDepToggle(\'querystore\')"');
not('pressed no pretend for that id', p2, 'Pretend off: Query Store</button>');
has('other button still pretend', p2, 'Pretend off: VIEW SERVER STATE</button>');

// ---- Why view: unblocked shows the chart, blocked shows the sentence and no chart ----
run('W = { deps: {} };');
run('whyCard = { id: "sql-topqueries-orders", label: "Top queries", rule: "Queries got slower.", verdict: "bad", chart: "bars", series: [{ name: "p95", win: "incident", pts: [{ t: 0, v: 5 }, { t: 1, v: 9 }] }], table: { cols: ["Metric"], rows: [["p95"]] } };');
var y0 = run('whyView(whyCard)');
has('open why has chart', y0, '<figure class="ml"');
has('open why chip bad', y0, 'ev-chip bad');
not('open why not blocked', y0, BLOCKED);

run('W = { deps: { querystore: true } };');
var y1 = run('whyView(whyCard)');
has('blocked why sentence', y1, BLOCKED);
not('blocked why no figure', y1, '<figure class="ml"');
not('blocked why no svg', y1, '<svg');
has('blocked why chip none', y1, 'ev-chip none');
has('blocked why banner kept', y1, 'Could not check');

// ---- wiring greps on the edited files ----
eq('why computes ev0', cnt(whySrc, "var ev0 = (typeof evDepEffVerdict === 'function') ? evDepEffVerdict(c) : c.verdict;"), 1);
eq('why uses ev0', cnt(whySrc, "var v = (ev0 === 'ok' || ev0 === 'watch' || ev0 === 'bad' || ev0 === 'none') ? ev0 : 'none';"), 1);
eq('why blocked guard', cnt(whySrc, "if (typeof evDepBlocked === 'function' && evDepBlocked(c)) {"), 1);
eq('why blocked sentence', cnt(whySrc, BLOCKED), 1);
eq('why old verdict line gone', cnt(whySrc, "var v = (c.verdict === 'ok'"), 0);
eq('dep names object', cnt(depSrc, 'var EVDEP_NAMES = {'), 1);
eq('dep label expression', cnt(depSrc, "+ (o[k] ? 'Stop pretending: ' : 'Pretend off: ') + (EVDEP_NAMES[k] || k) + '</button>'"), 1);
eq('dep long title gone from label', cnt(depSrc, "Pretend off: ' + EVDEP_DEFS[k].title"), 0);

try { new Function(whySrc); } catch (x) { bad.push('syntax 13-why-b.js ' + x.message); }
try { new Function(depSrc); } catch (x) { bad.push('syntax 12-evidence-g.js ' + x.message); }

console.log(bad.length ? 'FAIL ' + bad.join('; ') : 'PASS');
