// ---------- dummy data: one incident story ----------
// API Orders.Create slowed from ~420 ms to ~2.1 s P95 from 09:14 UTC on 26 Oct 2026.
// Incident window 09:00-10:00 UTC, reference 08:00-09:00 UTC. 12 APIs. Sources: audit + IIS + SQL present, MongoDB and Host missing.
var STEPS = [
  { n:0,  label:'Frame',        title:'Frame the question',   grp:'Investigate', st:'Ready' },
  { n:1,  label:'Trust data',   title:'Can I trust the data?',grp:'Investigate', st:'Ready' },
  { n:2,  label:'Shape',        title:'Shape',                grp:'Investigate', st:'Ready' },
  { n:3,  label:'Time split',   title:'Time split',           grp:'Investigate', st:'Partly ready' },
  { n:4,  label:'Load',         title:'Load',                 grp:'Investigate', st:'Ready' },
  { n:5,  label:'Changes',      title:'Changes',              grp:'Investigate', st:'Ready' },
  { n:6,  label:'App tier',     title:'App tier',             grp:'Investigate', st:'Ready' },
  { n:7,  label:'SQL',          title:'SQL Server',           grp:'Sources',     st:'Ready' },
  { n:8,  label:'Mongo',        title:'MongoDB',              grp:'Sources',     st:'Cannot test yet' },
  { n:9,  label:'Host',         title:'Host',                 grp:'Sources',     st:'Cannot test yet' },
  { n:10, label:'Origin',       title:'Origin and suspects',  grp:'Conclude',    st:'Ready' },
  { n:11, label:'After the fix',title:'After the fix',        grp:'Follow up',   st:'Waiting' },
  { n:12, label:'Checklist',    title:'Checklist',            grp:'Follow up',   st:'Ready' },
  { n:13, label:'Export',       title:'Export',               grp:'Follow up',   st:'Waiting' }
];
var GROUPS = ['Investigate','Sources','Conclude','Follow up'];

// The 12 APIs: route, incident calls, incident p95 ms, reference p95 ms, delta pct, delta ms, mark tone (bad/ok/grey), mark word
var ROUTES = [
  ['Orders.Create',    8412, 2100,  420, 400, 1680, 'bad',  'Slower'],
  ['Orders.List',      3150,  980,  450, 118,  530, 'bad',  'Slower'],
  ['Payments.Authorize',2980, 1240,  610, 103,  630, 'bad',  'Slower'],
  ['Orders.Get',       4202,  390,  380,   3,   10, 'grey', 'No change'],
  ['Basket.Add',       6104,  540,  512,   5,   28, 'grey', 'No change'],
  ['Basket.Get',       5890,  205,  198,   4,    7, 'grey', 'No change'],
  ['Catalog.Search',   4410,  312,  305,   2,    7, 'grey', 'No change'],
  ['Catalog.Get',      3760,  188,  190,  -1,   -2, 'grey', 'No change'],
  ['Customers.Login',  1520,  402,  398,   1,    4, 'grey', 'No change'],
  ['Stock.Reserve',    1090,  266,  240,  11,   26, 'grey', 'No change'],
  ['Ping.Health',       720,   12,   12,   0,    0, 'grey', 'No change'],
  ['Reports.Daily',      12, 4820, 4650,   4,  170, 'grey', 'No change']
];

// Time split parts: key, label, normal ms, incident ms, share of extra time (fraction), tested
var PARTS = [
  { k:'A', label:'Audit set-up (A)',    normal:18,  inc:22,   share:0.002, tested:true },
  { k:'C', label:'Action time (C)',     normal:240, inc:480,  share:0.143, tested:true },
  { k:'P', label:'Payload record (P)',  normal:12,  inc:14,   share:0.001, tested:true },
  { k:'O', label:'Outside time (O)',    normal:150, inc:1584, share:0.854, tested:true }
];

// Load table (step 4): measure, normal, incident, direction
var LOADROWS = [
  ['Calls arriving per second', '10.2', '10.4', 'About the same'],
  ['Time a call spends in the system', '420 ms', '2,100 ms', 'Higher'],
  ['Calls in flight (average)', '4.3', '21.4', 'Higher'],
  ['Calls in flight (highest minute)', '9', '34', 'Higher']
];

// Slowest calls (step 3 / slowest-calls tool): api, started, duration ms
var CALLS = [
  ['Orders.Create',      '09:24:11', 4812],
  ['Orders.Create',      '09:22:09', 4350],
  ['Orders.List',        '09:31:40', 3904],
  ['Orders.Create',      '09:18:58', 3611],
  ['Payments.Authorize', '09:27:02', 2990],
  ['Orders.Create',      '09:36:44', 2877],
  ['Orders.Create',      '09:15:03', 2640],
  ['Orders.List',        '09:41:19', 2412],
  ['Payments.Authorize', '09:29:55', 2388],
  ['Orders.Create',      '09:44:30', 2301]
];

// Changes the user listed (step 0 / step 5): at, label, relation to onset
var CHANGES0 = [
  { at:'2026-10-26T09:05', label:'Release 2026.10.26.2 of Orders.Api deployed', rel:'9 minutes before the onset' },
  { at:'2026-10-26T08:40', label:'Index maintenance on the Orders table finished', rel:'34 minutes before the onset' }
];

// Suspects: id, group, title, result, colour, why, needs, nextCheck, overturn, evidence[], tier, observed
var SUSPECTS = [
  { id:'D1', grp:'D', title:'Blocking on the Orders tables in SQL Server', result:'Consistent', colour:'red', tier:'SQL Server', obs:'Observed',
    why:'Waits on the Orders tables rose at 09:14, the same minute the slow end moved. Blocking chains of up to 9 s were recorded.',
    evidence:['Lock waits on Orders and OrderLines rose from 0.4 s to 31 s per minute at 09:14.','The slowest calls of Orders.Create overlap the blocking chains.'],
    nextCheck:'Read the blocking chain around 09:14 in the SQL source and name the head blocker.',
    overturn:'A blocking chain that starts after 09:14, or waits that rose before the slow end moved.' },
  { id:'C1', grp:'C', title:'Release 2026.10.26.2 of Orders.Api', result:'Partly consistent', colour:'orange', tier:'App', obs:'Inferred',
    why:'Deployed 9 minutes before the onset, the closest listed change. The release itself is not in the data; only the timing fits.',
    evidence:['Change listed for 09:05; the onset is 09:14.','No change after 09:05 is listed.'],
    nextCheck:'Diff release 2026.10.26.2 for changes to the Orders write path.',
    overturn:'A run of the previous release showing the same slow end at the same hour.' },
  { id:'S1', grp:'S', title:'Queueing before the app (outside time)', result:'Partly consistent', colour:'orange', tier:'App', obs:'Observed',
    why:'Outside time (O) grew from 150 ms to 1,584 ms and holds 85 % of the extra time. Where the wait happens is not in the data.',
    evidence:['O is the dominant part of the extra time.','Calls in flight rose while the arrival rate stayed flat.'],
    nextCheck:'Check the IIS queue and the proxy in front of the app for waits after 09:05.',
    overturn:'O time that begins before 09:05, before the release.' },
  { id:'M1', grp:'M', title:'MongoDB slow operations', result:'Cannot test yet', colour:'grey', obs:'Inferred',
    why:'This case has no MongoDB source, so nothing about MongoDB could be read.', needs:'a MongoDB source',
    nextCheck:'Add the MongoDB source and run the steps again.', overturn:'' },
  { id:'H1', grp:'H', title:'Host CPU, disk or network saturation', result:'Cannot test yet', colour:'grey', obs:'Inferred',
    why:'This case has no Blackbox on a host, so processor, disk and network could not be read.', needs:'a Blackbox source',
    nextCheck:'Place a Blackbox on the app host and run the steps again.', overturn:'' },
  { id:'L1', grp:'L', title:'Higher load: more calls arriving', result:'Contradicted', colour:'green', obs:'Observed',
    why:'Calls arriving per second were about the same (10.2 against 10.4). The system filled because calls took longer.',
    evidence:['Arrival rate flat; time per call rose.'], nextCheck:'', overturn:'' },
  { id:'Q1', grp:'Q', title:'Clock difference between sources', result:'Contradicted', colour:'green', obs:'Observed',
    why:'7,204 calls had both a start and an end; the clocks agree within 2 s.', evidence:[], nextCheck:'', overturn:'' }
];
var COLOUR_WORD = { red:'Most likely suspect', orange:'Likely suspect', green:'Ruled out by the data we have', grey:'Not sure' };

// Step 10 origin rows: tier, when, what, role
var ORIGINROWS = [
  ['SQL Server', '26 Oct 09:14', 'Waits on the Orders tables rose', 'Moved first'],
  ['App',        '26 Oct 09:14', 'Outside time (O) rose in the same minute', 'Followed within the same minute'],
  ['MongoDB',    'Not dated',    'No MongoDB source in the case', 'Cannot test yet'],
  ['Host',       'Not dated',    'No Blackbox on a host in the case', 'Cannot test yet']
];
var SURE = {
  word:'Medium',
  why:'The sources that could test the origin agree. MongoDB and Host are missing, so their suspects could not be tested.',
  used:'Audit, IIS, SQL Server', missing:'MongoDB, Host',
  notTested:'M1: MongoDB slow operations, H1: Host CPU, disk or network saturation',
  overturn:'D1: Read the blocking chain around 09:14 in the SQL source and name the head blocker.'
};

// Checklist seed: list (todo/doing/learned/ruled_out), text, suspect id or ''
var CHECKLIST0 = [
  ['todo',      'D1: Read the blocking chain around 09:14 in the SQL source and name the head blocker.', 'D1'],
  ['todo',      'C1: Diff release 2026.10.26.2 for changes to the Orders write path.', 'C1'],
  ['todo',      'Ask the SQL owner for the index maintenance window of 08:40.', ''],
  ['todo',      'S1: Check the IIS queue and the proxy in front of the app for waits after 09:05.', 'S1'],
  ['doing',     'Compare release 2026.10.26.2 against the onset with the Orders team.', ''],
  ['learned',   'The slow end moved first on SQL Server at 09:14 UTC.', ''],
  ['learned',   'Calls per second were flat; each call stayed longer.', ''],
  ['learned',   'Outside time (O) holds 85 % of the extra time.', ''],
  ['ruled_out', 'L1: Higher load: more calls arriving.', 'L1']
];
var LISTS = [ ['todo','Need to do'], ['doing','Working on'], ['learned','What we learned'], ['ruled_out','Ruled out'] ];

// Saved views seed: name, tool id, windows line, saved
var VIEWS0 = [
  { name:'Orders.Create, incident hour', tool:'response-time', win:'2026-10-26 09:00 to 2026-10-26 10:00', saved:'2026-10-26 15:02' },
  { name:'Slowest calls at the onset',   tool:'slowest-calls', tool2:'slowest-calls', win:'2026-10-26 09:10 to 2026-10-26 09:30', saved:'2026-10-26 15:20' }
];

// After the fix comparison rows: measure, earlier, later, change, mark tone/word or ''
var FIXROWS = [
  ['P95 (slow end)',          '2,100 ms', '790 ms', '-62 %', 'bad',  'Slower'],
  ['P50 (middle)',            '380 ms',   '210 ms', '-45 %', 'grey', 'No change'],
  ['D (whole call, average)', '2,150 ms', '820 ms', '-62 %', 'bad',  'Slower'],
  ['L (calls in flight)',     '21.4',     '8.1',    '-62 %', '',     '']
];

// ---------- the 13 manual tools, in the order of TOOLS in tools.ts ----------
var TOOLS = [
  { id:'response-time',  title:'Response time over time',        blurb:'How the slow end moved, minute by minute, and which APIs changed.', step:2,  needs:['audit'],          sources:'Audit' },
  { id:'slowest-apis',   title:'Slowest APIs',                   blurb:'APIs ranked by their slow end, against the normal period.',                    needs:['audit'],          sources: 'Audit' },
  { id:'slowest-calls',  title:'Slowest calls',                  blurb:'The individual calls that took longest in a window.',                          needs:['audit'],          sources: 'Audit' },
  { id:'time-split',     title:'Time split A C P O',             blurb:'Where the time of a call went: audit set-up, action, payload record, outside time.', step:3, needs:['audit','iis'], sources:'Audit, IIS' },
  { id:'calls-in-flight',title:'Calls in flight',                blurb:'How many calls were open at once, against the pool limit if you enter one.', step:4, needs:['audit'],   sources:'Audit' },
  { id:'nearby-activity',title:'Nearby activity',                blurb:'What every source recorded around one moment.',                                needs:['audit'],          sources:'Audit, IIS, SQL Server, MongoDB, Host' },
  { id:'changes',        title:'Changes timeline',               blurb:'Releases and changes you list, set beside the minute things moved.', step:5,   needs:['audit'],          sources:'Audit, your change list' },
  { id:'sql-waits',      title:'SQL blocking and waits',         blurb:'Blocking and waits recorded on SQL Server.',                         step:7,  needs:['sql'],            sources:'SQL Server' },
  { id:'mongo-health',   title:'MongoDB health',                 blurb:'Slow operations, queues and the state of the replica set.',          step:8,  needs:['mongo'],          sources:'MongoDB' },
  { id:'host-resources', title:'Host CPU, memory and disk',      blurb:'What the application host recorded.',                                step:9,  needs:['host'],           sources:'Blackbox' },
  { id:'sql-host',       title:'SQL host disk, memory and network', blurb:'What the SQL Server host recorded.',                              step:9,  needs:['host'],           sources:'Blackbox on the SQL host' },
  { id:'after-fix',      title:'After the fix',                  blurb:'The same hours on a later day, set beside the incident day.',                  needs:['audit'],          sources:'Audit' },
  { id:'suspect-summary',title:'Suspect summary',                blurb:'Every suspect with its colour and its cheapest check.',              step:10, needs:['audit'],          sources:'Audit and any other source in the case' }
];
var NEED_WORD = { audit:'an audit source', iis:'an IIS log source', sql:'a SQL Server source', mongo:'a MongoDB source', host:'a Blackbox source' };
var READS_ITSELF = { 'slowest-calls':1, 'nearby-activity':1 };

// ---------- formatting helpers (mirror stepModel.ts) ----------
function fmtCount(n){ return Math.round(n).toLocaleString('en-US'); }
function fmtMs(n){ return Math.round(n).toLocaleString('en-US') + ' ms'; }
function fmtPct(n){ var r = Math.round(n); return (r > 0 ? '+' : r < 0 ? '-' : '') + Math.abs(r).toLocaleString('en-US') + ' %'; }
function esc(s){ return String(s).replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/"/g,'&quot;'); }

// ---------- per-minute series for the charts (reference 08:00-08:59, incident 09:00-09:59) ----------
// One value per minute: p95 ms and average calls in flight. The long gap 09:31-09:44 breaks the incident line.
var SERIES = (function(){
  var out = [], inc = [];
  for (var m = 0; m < 60; m++) {
    var hm = function(h){ return '2026-10-26T' + (h < 10 ? '0'+h : h) + ':' + (m < 10 ? '0'+m : m) + ':00Z'; };
    var jitter = ((m * 37) % 23) - 11; // deterministic wobble, no randomness so the build is stable
    var refP = 420 + jitter * 4 + (m % 9 === 0 ? 30 : 0);
    out.push({ at:hm(8), win:'reference', p95:Math.round(refP), inFlight:4 + (m % 5 === 0 ? 1 : 0) + jitter / 22 });
    var incP, inf;
    if (m < 14) { incP = 430 + jitter; inf = 4 + jitter / 11; }
    else { var rise = Math.min(1, (m - 14) / 6); incP = 430 + rise * 1670 + jitter * 3; inf = 4 + rise * 17.5 + jitter / 11; }
    var gap = (m >= 31 && m <= 44) ? 'long' : '';
    inc.push({ at:hm(9), win:'incident', p95:Math.round(incP), inFlight:Math.round(inf * 10) / 10, gap:gap });
  }
  return out.concat(inc);
})();

// MinuteLine: the same SVG the app draws (runs of one window, a gap breaks the line), caption with range and peak, legend.
function minuteLine(field, label, unit) {
  var W = 640, H = 140, PAD = 4;
  var pts = SERIES.map(function(p){ return { at:p.at, win:p.win, v:p[field], gap:!!p.gap }; });
  var peak = 0; pts.forEach(function(p){ if (!p.gap && typeof p.v === 'number' && p.v > peak) peak = p.v; });
  var top = Math.max(1, peak);
  var x = function(i){ return PAD + (i / (pts.length - 1)) * (W - 2 * PAD); };
  var y = function(v){ return H - PAD - (v / top) * (H - 2 * PAD); };
  var runs = [], cur = null;
  pts.forEach(function(p, i){
    if (typeof p.v !== 'number' || p.gap) { if (cur) runs.push(cur); cur = null; return; }
    if (!cur || cur.win !== p.win) { if (cur) runs.push(cur); cur = { win:p.win, d:[] }; }
    cur.d.push((cur.d.length ? 'L' : 'M') + x(i).toFixed(1) + ' ' + y(p.v).toFixed(1));
  });
  if (cur) runs.push(cur);
  var cap = field === 'p95' ? 'Highest ' + Math.round(peak).toLocaleString('en-US') + ' ' + unit + '.'
    : 'Highest minute average: ' + (Math.round(peak * 10) / 10) + ' ' + (Math.round(peak) === 1 ? unit.replace(/s$/,'') : unit) + '.';
  var paths = runs.map(function(r){ return '<path d="' + r.d.join(' ') + '" class="line ' + (r.win === 'incident' ? 'incident' : 'normal') + '"/>'; }).join('');
  return '<figure class="ml"><figcaption>' + label + ' <span class="range">26 Oct 08:00 to 26 Oct 09:59 UTC. ' + cap + '</span></figcaption>'
    + '<svg viewBox="0 0 ' + W + ' ' + H + '" role="img" aria-label="' + label + '" preserveAspectRatio="none">'
    + '<line x1="' + PAD + '" y1="' + (H - PAD) + '" x2="' + (W - PAD) + '" y2="' + (H - PAD) + '" class="axis"/>' + paths + '</svg>'
    + '<p class="legend"><span class="key normal"></span> Normal <span class="key incident"></span> Incident. A break in a line is a minute with no data.</p></figure>';
}
