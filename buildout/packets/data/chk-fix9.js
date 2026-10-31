// usage (from buildout/wireframes) after apply: node ../packets/data/chk-fix9.js   prints PASS or FAIL <reason>
var fs = require('fs'), vm = require('vm'), bad = [];
function rd(p) { return fs.readFileSync(p, 'utf8'); }
function cnt(s, t) { return String(s).split(t).length - 1; }
function has(l, s, t) { if (String(s).indexOf(t) < 0) bad.push(l + ' lacks ' + JSON.stringify(t)); }
function not(l, s, t) { if (String(s).indexOf(t) >= 0) bad.push(l + ' must not contain ' + JSON.stringify(t)); }
function eq(l, g, w) { if (g !== w) bad.push(l + ' -> ' + JSON.stringify(g) + ' wanted ' + JSON.stringify(w)); }
function rowApis(html) {
  var out = [], m, re = /<tr class="[^"]*"[^>]*><td>([^<]*)<\/td>/g;
  while ((m = re.exec(html))) out.push(m[1]);
  return out;
}

// --- ES5 scan, syntax, and no HTML entity inside any inert('...') literal of the edited files ---
var EDITED = ['parts/20-shell.js', 'parts/30-steps-0-4.js', 'parts/41-tools-a.js'];
var SRC = {};
EDITED.forEach(function (p) {
  SRC[p] = rd(p);
  if (/=>|`|\blet\b|\bconst\b/.test(SRC[p])) bad.push(p + ' not ES5');
  try { new Function(SRC[p]); } catch (e) { bad.push(p + ' syntax ' + e.message); }
  SRC[p].split('\n').forEach(function (line) {
    var m, re = /inert\('((?:[^'\\]|\\.)*)'\)/g;
    while ((m = re.exec(line))) {
      if (/&[a-zA-Z]+;/.test(m[1])) bad.push(p + ' entity inside inert literal: ' + m[1]);
    }
  });
});
var s20 = SRC['parts/20-shell.js'], s30 = SRC['parts/30-steps-0-4.js'], s41 = SRC['parts/41-tools-a.js'];

// --- wiring greps ---
eq('shell seeds callsApi empty', cnt(s20, "callsApi:''"), 1);
eq('slowCallsTable takes names', cnt(s30, 'function slowCallsTable(withNearby, names){'), 1);
eq('old slowCallsTable signature gone', cnt(s30, 'function slowCallsTable(withNearby){'), 0);
eq('names branch keeps picker fallback', cnt(s30, 'var apiNames = (names instanceof Array) ? names : selRoutes().map(function(r){ return r[0]; });'), 1);
eq('step3 call unchanged', cnt(s30, 'slowCallsTable(true)'), 1);
eq('step3 copy button', cnt(s30, '>Use this filter in the Slowest calls tool too</button>'), 1);
eq('step3 copy onclick', cnt(s30, "W.callsApi=(W.apiMode||\\'all\\')===\\'one\\'?(W.apiOne||ROUTES[0][0]):\\'\\';W.slowPg=0;render()"), 1);
eq('step3 copy note', cnt(s30, 'Each place keeps its own filter. This button copies this one to the Slowest calls tool.'), 1);
eq('openCallsFor writes callsApi', cnt(s41.replace(/\r/g, ''), 'function openCallsFor(api){\n  W.callsApi = api;'), 1);
eq('openCallsFor drops apiMode', cnt(s41, "W.apiMode = 'one'"), 0);
eq('openCallsFor drops apiOne', cnt(s41, 'W.apiOne = api'), 0);
eq('tool names list', cnt(s41, 'var callNames = W.callsApi ? [W.callsApi] : ROUTES.map(function(r){ return r[0]; });'), 1);
eq('tool passes names', cnt(s41, 'slowCallsTable(true, callNames)'), 1);
eq('tool old call gone', cnt(s41, 'slowCallsTable(false) +'), 0);
eq('tool filter select', cnt(s41, 'onchange="W.callsApi=this.value;W.slowPg=0;render()"'), 1);
eq('tool option per route via esc', cnt(s41, "'<option value=\"' + esc(r[0]) + '\"'"), 1);
eq('tool copy button', cnt(s41, '>Use this filter in Step 3 too</button>'), 1);
eq('tool copy onclick', cnt(s41, "if(W.callsApi){W.apiMode=\\'one\\';W.apiOne=W.callsApi;}else{W.apiMode=\\'all\\';}render()"), 1);
eq('tool copy note', cnt(s41, 'Each place keeps its own filter. This button copies this one to Step 3.'), 1);
if (bad.length) { console.log('FAIL ' + bad.join('; ')); process.exit(0); }

// --- vm: load the real chain with a stubbed DOM, same approach chk-fix7.js uses ---
var c = { renders: 0 };
c.render = function () { c.renders++; };
c.openTool = function (id) { c.W.tool = id; };
c.TA_callBars = function () { return ''; };
function fakeEl() { return { innerHTML: '', scrollTop: 0, onclick: null, dataset: {}, classList: { toggle: function () {} } }; }
c.document = { getElementById: function () { return fakeEl(); }, documentElement: { dataset: {}, classList: { toggle: function () {} } }, querySelectorAll: function () { return []; } };
c.addEventListener = function () {};
c.history = { replaceState: function () {} };
c.window = c;
vm.createContext(c);
var PARTS = ['parts/10-data.js', 'parts/10-data2-calls.js', 'parts/30-pager.js', 'parts/20-shell.js', 'parts/30-steps-0-4.js', 'parts/41-tools-a.js'];
try { PARTS.forEach(function (p) { vm.runInContext(rd(p), c); }); } catch (e) { console.log('FAIL load ' + e.message); process.exit(0); }
function run(s) { return vm.runInContext(s, c); }
// the real render() draws the whole app; the copy handlers only need a no-op
run('render = function(){ };');
['openCallsFor', 'slowCallsTable', 'selRoutes', 'apiPicker', 'step3'].forEach(function (f) { if (typeof c[f] !== 'function') bad.push('missing ' + f); });
if (typeof c.TOOL_BODIES === 'undefined' || typeof c.TOOL_BODIES['slowest-calls'] !== 'function') bad.push('missing TOOL_BODIES[slowest-calls]');
if (bad.length) { console.log('FAIL ' + bad.join('; ')); process.exit(0); }

var RE_TOOL = /onclick="([^"]*)">Use this filter in Step 3 too<\/button>/;
var RE_STEP3 = /onclick="([^"]*)">Use this filter in the Slowest calls tool too<\/button>/;

// --- 1. initial state: the tool filter starts empty and the picker state is unset ---
eq('initial callsApi', run('W.callsApi'), '');
eq('initial apiMode unset', run('typeof W.apiMode'), 'undefined');
eq('initial apiOne unset', run('typeof W.apiOne'), 'undefined');

// --- 2. openCallsFor writes only the tool filter ---
c.openCallsFor('Orders.Create');
eq('openCallsFor callsApi', run('W.callsApi'), 'Orders.Create');
eq('openCallsFor leaves apiMode', run('typeof W.apiMode'), 'undefined');
eq('openCallsFor leaves apiOne', run('typeof W.apiOne'), 'undefined');
eq('openCallsFor resets pager', run('W.slowPg'), 0);
eq('openCallsFor shows table', run('W.slowShown'), true);
eq('openCallsFor opens the tool', run('W.tool'), 'slowest-calls');

// --- 3. the tool body lists only the picked API and draws the filter row ---
var body = c.TOOL_BODIES['slowest-calls']();
var apis = rowApis(body);
eq('filtered row count', apis.length, run("Math.min(SLOW_PAGE, CALLS.filter(function(x){ return x[0] === 'Orders.Create'; }).length)"));
eq('filtered rows all Orders.Create', apis.length > 0 && apis.every(function (a) { return a === 'Orders.Create'; }), true);
has('filter row select', body, '<select class="wf" onchange="W.callsApi=this.value;W.slowPg=0;render()">');
has('all-apis option', body, '<option value="">All APIs</option>');
has('picked option selected', body, '<option value="Orders.Create" selected>Orders.Create</option>');
has('tool copy note text', body, 'Each place keeps its own filter. This button copies this one to Step 3.');

// --- 4. the tool copy button copies tool -> step 3, both ways ---
var m1 = RE_TOOL.exec(body);
if (!m1) bad.push('tool copy button onclick not found');
else {
  run(m1[1]);
  eq('copy tool->step3 mode', run('W.apiMode'), 'one');
  eq('copy tool->step3 one', run('W.apiOne'), 'Orders.Create');
}
run("W.callsApi = ''; W.apiMode = 'one'; W.apiOne = 'Basket.Get'; W.slowPg = 0;");
var bodyAll = c.TOOL_BODIES['slowest-calls']();
var apisAll = rowApis(bodyAll);
eq('unfiltered page is a full page', apisAll.length, run('Math.min(SLOW_PAGE, CALLS.length)'));
has('unfiltered pager total', bodyAll, ' of ' + run('CALLS.length') + '.');
eq('unfiltered includes other apis', apisAll.indexOf('Orders.List') >= 0 && apisAll.indexOf('Payments.Authorize') >= 0, true);
has('all-apis selected when empty', bodyAll, '<option value="" selected>All APIs</option>');
var m2 = RE_TOOL.exec(bodyAll);
if (!m2) bad.push('tool copy button onclick missing when filter empty');
else {
  run(m2[1]);
  eq('copy empty -> all', run('W.apiMode'), 'all');
  eq('copy empty keeps apiOne', run('W.apiOne'), 'Basket.Get');
}

// --- 5. slowCallsTable: the names argument wins; no argument keeps the Step 3 picker ---
run("W.apiMode = 'all'; W.apiOne = undefined; W.callsApi = ''; W.slowPg = 0;");
var onlyList = rowApis(run("slowCallsTable(false, ['Orders.List'])"));
eq('names arg rows all Orders.List', onlyList.length > 0 && onlyList.every(function (a) { return a === 'Orders.List'; }), true);
eq('names arg count', onlyList.length, run("Math.min(SLOW_PAGE, CALLS.filter(function(x){ return x[0] === 'Orders.List'; }).length)"));
run("W.apiMode = 'one'; W.apiOne = 'Orders.List'; W.callsApi = 'Orders.Create'; W.slowPg = 0;");
var stepTbl = rowApis(c.slowCallsTable(true));
eq('no names still follows picker', stepTbl.length > 0 && stepTbl.every(function (a) { return a === 'Orders.List'; }), true);
var bodyInd = c.TOOL_BODIES['slowest-calls']();
eq('tool follows callsApi not picker', rowApis(bodyInd).every(function (a) { return a === 'Orders.Create'; }), true);

// --- 6. Step 3 output is unchanged when only callsApi changes ---
run("W.apiMode = 'all'; W.apiOne = undefined; W.callsApi = ''; W.slowPg = 0;");
var s3a = c.step3('Ready');
run("W.callsApi = 'Orders.Create';");
eq('step3 ignores callsApi', c.step3('Ready'), s3a);
has('step3 copy note text', s3a, 'Each place keeps its own filter. This button copies this one to the Slowest calls tool.');

// --- 7. the Step 3 copy button copies step 3 -> tool, in every mode ---
var m3 = RE_STEP3.exec(s3a);
if (!m3) bad.push('step3 copy button onclick not found');
else {
  var oc = m3[1];
  run(oc);
  eq('all copies as empty', run('W.callsApi'), '');
  run("W.apiMode = 'one'; W.apiOne = 'Basket.Get'; W.slowPg = 3;");
  run(oc);
  eq('one copies the pick', run('W.callsApi'), 'Basket.Get');
  eq('one copy resets pager', run('W.slowPg'), 0);
  run("W.apiMode = 'several'; W.apiSet = ['Orders.Create'];");
  has('copy button in several mode', c.step3('Ready'), 'Use this filter in the Slowest calls tool too');
  run(oc);
  eq('several copies as empty', run('W.callsApi'), '');
  run("W.apiMode = 'one'; W.apiOne = undefined;");
  run(oc);
  eq('one without pick copies first route', run('W.callsApi'), run('ROUTES[0][0]'));
}

console.log(bad.length ? 'FAIL ' + bad.join('; ') : 'PASS');
