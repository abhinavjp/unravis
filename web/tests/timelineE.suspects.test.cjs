const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

// Investigation Timeline gap 4: the Suspects rail. The engine (lib/tracing/suspects.ts) reads only the timeline's own counts, so it is tested with
// made-up responses (dates in 2001, no real data), for the live API-audit lane and the case-stored SQL and IIS lanes alike.

const root = path.resolve(__dirname, '..');
const loaded = new Map();
function loadTypeScript(relativePath) {
  const filename = path.join(root, relativePath);
  if (loaded.has(filename)) return loaded.get(filename).exports;
  const source = fs.readFileSync(filename, 'utf8');
  const output = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022, esModuleInterop: true } }).outputText;
  const module = { exports: {} };
  loaded.set(filename, module);
  const nodeRequire = createRequire(filename);
  const load = (request) => /^\.\.?\//.test(request) && fs.existsSync(path.join(path.dirname(filename), request + '.ts'))
    ? loadTypeScript(path.relative(root, path.join(path.dirname(filename), request + '.ts')))
    : nodeRequire(request);
  new Function('require', 'module', 'exports', output)(load, module, module.exports);
  return module.exports;
}
const read = (relative) => fs.readFileSync(path.join(root, relative), 'utf8');

const engine = loadTypeScript('src/lib/tracing/suspects.ts');
const copy = loadTypeScript('src/lib/copy/suspectsCopy.ts');
const { deck } = loadTypeScript('src/lib/copy/deck.ts');
const { lintScreenStrings } = loadTypeScript('src/lib/copy/copylint.ts');
const { evaluateSuspects, suspectsView, anyLaneRead, momentStartMs, SUSPECT_CATALOGUE, SUSPECT_RULES, CATALOGUE_TOTAL } = engine;

const START = Date.parse('2001-03-04T10:00:00Z');
const BAR = 300; // seconds: 12 bars across one hour
const iso = (ms) => new Date(ms).toISOString();
const windowOf = (bars = 12) => ({ start: iso(START), end: iso(START + bars * BAR * 1000), axisStart: iso(START), axisEnd: iso(START + bars * BAR * 1000), bucketSeconds: BAR });
const lane = (key, counts, extra = {}) => ({
  key, label: key, state: 'events', reason: '', origin: key === 'audit' ? 'live' : 'case', asOf: iso(START), consistency: 'point-in-time', complete: true, truncated: false,
  scanned: 0, hidden: 0, sources: [], buckets: counts.map((count, i) => ({ start: iso(START + i * BAR * 1000), count, errors: 0 })), ...extra
});
const empty = (key, state) => lane(key, [], { state });
const response = (lanes, bars = 12) => ({ window: windowOf(bars), lanes, sources: [], allLanesStored: false, mixedConsistency: false, truncated: false, warnings: [], auditSources: [] });
const flat = (n, value) => Array.from({ length: n }, () => value);
const step = (n, before, after) => Array.from({ length: n }, (_, i) => (i < n / 2 ? before : after));
const byId = (list, id) => list.find((s) => s.id === id);

test('the catalogue lists the 26 suspects of the wireframe, each with its sentences, and the full catalogue is 44', () => {
  assert.equal(SUSPECT_CATALOGUE.length, 26);
  assert.equal(CATALOGUE_TOTAL, 44);
  assert.equal(new Set(SUSPECT_CATALOGUE.map((entry) => entry.id)).size, 26);
  for (const entry of SUSPECT_CATALOGUE) {
    const text = copy.SUSPECT_ENTRY_TEXT[entry.id];
    assert.ok(text && text.title && text.check && text.overturn && text.needs, entry.id + ' needs all four sentences');
    for (const name of [...entry.must, ...(entry.expect ?? [])]) assert.ok(copy.SIGNAL_TEXT[name], entry.id + ' names a signal with no label: ' + name);
  }
  assert.deepEqual([SUSPECT_RULES.riseRatio, SUSPECT_RULES.minEvents], [1.5, 30]);
});

test('every sentence of the rail passes the copy lint (no causes, grade 8.5 or lower, jargon only with a glossary term)', () => {
  const entries = copy.suspectsCopyEntries();
  assert.ok(entries.length > 150);
  assert.deepEqual(lintScreenStrings(entries, deck.terms).map((f) => f.id + ': ' + f.message), []);
});

test('live API-audit lane: a rise in the second half is Consistent (orange) with a moment on the chart; nothing is ever red', () => {
  const list = evaluateSuspects(response([lane('audit', step(12, 5, 20)), empty('sql', 'no-data'), empty('iis', 'no-data')]));
  assert.equal(list.length, 26);
  const l1 = byId(list, 'L1');
  assert.equal(l1.colour, 'orange');
  assert.equal(l1.result, 'Consistent');
  assert.equal(l1.momentSlot, 6);
  assert.equal(l1.momentLane, 'audit');
  assert.ok(l1.why.includes('5.0') && l1.why.includes('20.0'), 'the sentence carries the figures it read');
  assert.equal(list.filter((s) => s.colour === 'red').length, 0);
  // The audit rise alone does not test the suspects that need other readings: they are grey with their real Needs text.
  const l2 = byId(list, 'L2');
  assert.equal(l2.colour, 'grey');
  assert.equal(l2.result, 'Cannot test yet');
  assert.equal(l2.needs, copy.SUSPECT_ENTRY_TEXT.L2.needs);
  assert.equal(l2.momentSlot, null, 'no reading of its own was dated');
});

test('a flat lane rules the rise-based suspects out (green); a lane with no rise is "No", not a guess', () => {
  const list = evaluateSuspects(response([lane('audit', flat(12, 10)), lane('sql', flat(12, 10)), empty('iis', 'no-data')]));
  const l1 = byId(list, 'L1');
  assert.equal(l1.colour, 'green');
  assert.equal(l1.result, 'Contradicted');
  assert.equal(byId(list, 'D1').colour, 'green', 'D1 needs slow database calls up');
  assert.equal(l1.momentSlot, null);
});

test('case-stored SQL lane: a rise in slow database calls is read the same way, and the live lane is not needed', () => {
  const list = evaluateSuspects(response([empty('audit', 'no-data'), lane('sql', step(12, 4, 14), { origin: 'case' }), empty('iis', 'no-data')]));
  const d1 = byId(list, 'D1');
  assert.equal(d1.colour, 'grey', 'blocking reports are not in the timeline counts');
  const sqlRow = d1.evidence.find((row) => row.role === 'must' && row.value === 'yes');
  assert.ok(sqlRow, 'the SQL rise reads Yes');
  assert.equal(d1.momentLane, 'sql');
  assert.equal(d1.momentSlot, 6);
  const l1 = byId(list, 'L1');
  assert.equal(l1.colour, 'grey', 'the API audit has no file, so the surge cannot be tested');
  assert.ok(l1.why.includes('no file'));
});

test('IIS lane: audit calls per web request falling by 30% or more reads Yes for the audit-gap check, steady reads No', () => {
  const falling = evaluateSuspects(response([lane('audit', step(12, 10, 5)), empty('sql', 'no-data'), lane('iis', flat(12, 10), { origin: 'case' })]));
  assert.equal(byId(falling, 'Q2').colour, 'orange');
  const steady = evaluateSuspects(response([lane('audit', flat(12, 10)), empty('sql', 'no-data'), lane('iis', flat(12, 10), { origin: 'case' })]));
  assert.equal(byId(steady, 'Q2').colour, 'green');
  const noIis = evaluateSuspects(response([lane('audit', flat(12, 10)), empty('sql', 'no-data'), empty('iis', 'no-data')]));
  const q2 = byId(noIis, 'Q2');
  assert.equal(q2.colour, 'grey');
  assert.ok(q2.why.includes('no file'));
});

test('a lane switched off for its time zone makes the clock check orange and every dependent reading unknown', () => {
  const off = lane('sql', [], { state: 'clock-unchecked', disabled: 'tz-rule' });
  const list = evaluateSuspects(response([lane('audit', step(12, 5, 20)), off, empty('iis', 'no-data')]));
  assert.equal(byId(list, 'Q1').colour, 'orange');
  assert.ok(byId(list, 'Q1').why.includes('time zone'));
  assert.equal(byId(list, 'D1').colour, 'grey');
  assert.ok(byId(list, 'D1').why.includes('time zone'));
  const fine = evaluateSuspects(response([lane('audit', step(12, 5, 20)), empty('sql', 'no-data'), empty('iis', 'no-data')]));
  assert.equal(byId(fine, 'Q1').colour, 'green');
});

test('honest limits: too few events, a lane not read in full, and a window too short are all unknown, never a rise or a quiet lane', () => {
  const few = evaluateSuspects(response([lane('audit', step(12, 1, 2)), empty('sql', 'no-data'), empty('iis', 'no-data')]));
  assert.equal(byId(few, 'L1').colour, 'grey');
  assert.ok(byId(few, 'L1').why.includes('only 18 events'));
  const cut = evaluateSuspects(response([lane('audit', step(12, 5, 20), { chunksTotal: 3, chunksRead: 1, readThrough: iso(START + 20 * 60 * 1000) }), empty('sql', 'no-data'), empty('iis', 'no-data')]));
  assert.equal(byId(cut, 'L1').colour, 'grey', 'a read that stopped early cannot rule a surge in or out');
  assert.ok(byId(cut, 'L1').why.includes('not read in full'));
  const incomplete = evaluateSuspects(response([lane('audit', step(12, 5, 20), { state: 'incomplete', incompleteReason: 'time-limit' }), empty('sql', 'no-data'), empty('iis', 'no-data')]));
  assert.equal(byId(incomplete, 'L1').colour, 'grey');
  const short = evaluateSuspects(response([lane('audit', step(2, 20, 40)), empty('sql', 'no-data'), empty('iis', 'no-data')], 2));
  assert.equal(byId(short, 'L1').colour, 'grey');
});

test('a rise from nothing counts once the window passes the event gate', () => {
  const list = evaluateSuspects(response([lane('audit', step(12, 0, 10)), empty('sql', 'no-data'), empty('iis', 'no-data')]));
  assert.equal(byId(list, 'L1').colour, 'orange');
  assert.equal(byId(list, 'L1').momentSlot, 6);
});

test('the moment is the onset bar even when the rise starts before the midpoint', () => {
  const counts = [10, 10, 10, 40, 40, 40, 40, 40, 40, 40, 40, 40];
  const list = evaluateSuspects(response([lane('audit', counts), empty('sql', 'no-data'), empty('iis', 'no-data')]));
  const l1 = byId(list, 'L1');
  assert.equal(l1.colour, 'orange');
  assert.equal(l1.momentSlot, 3, 'the rise is dated at its onset (index 3), not at the midpoint (index 6)');
  assert.equal(l1.momentLane, 'audit');
});

test('every grey card names what it needs, every card names its checks and what would overturn it', () => {
  const list = evaluateSuspects(response([lane('audit', step(12, 5, 20)), empty('sql', 'no-data'), empty('iis', 'no-data')]));
  for (const s of list) {
    assert.ok(s.check && s.overturn, s.id);
    assert.ok(s.evidence.length >= 1, s.id);
    if (s.colour === 'grey') assert.ok(s.needs && s.why.length > 0, s.id);
  }
  assert.ok(list.filter((s) => s.colour === 'grey').length >= 20, 'most of the catalogue cannot be tested from counts, and says so');
});

test('the view pins the data checks while they are not green, shows five, and gives the rest on See all', () => {
  const list = evaluateSuspects(response([lane('audit', step(12, 5, 20)), lane('sql', step(12, 4, 14)), lane('iis', flat(12, 10), { origin: 'case' })]));
  const less = suspectsView(list, false);
  assert.equal(less.total, 26);
  assert.equal(less.shown.length, SUSPECT_RULES.top);
  assert.equal(less.green.length, 0, 'ruled-out suspects fold away until See all');
  assert.ok(less.more);
  assert.equal(less.counts.red + less.counts.orange + less.counts.grey + less.counts.green, 26);
  const pinnedIds = less.pinned.map((s) => s.id);
  for (const id of pinnedIds) assert.ok(['Q1', 'Q2'].includes(id));
  assert.ok(!less.shown.some((s) => ['Q1', 'Q2'].includes(s.id)), 'a pinned card is not listed twice');
  // Orange comes before grey.
  const order = ['red', 'orange', 'grey'];
  for (let i = 1; i < less.shown.length; i++) assert.ok(order.indexOf(less.shown[i - 1].colour) <= order.indexOf(less.shown[i].colour));
  const more = suspectsView(list, true);
  assert.equal(more.shown.length + more.pinned.length + more.green.length, 26);
  assert.ok(more.shown.length > less.shown.length);
});

test('empty states: no lane with events means nothing to read; a quiet window says no suspect is ranked', () => {
  const none = response([empty('audit', 'none-seen'), empty('sql', 'no-data'), empty('iis', 'no-data')]);
  assert.equal(anyLaneRead(none), false);
  const quiet = response([lane('audit', flat(12, 10)), empty('sql', 'no-data'), empty('iis', 'no-data')]);
  assert.equal(anyLaneRead(quiet), true);
  const view = suspectsView(evaluateSuspects(quiet), false);
  assert.equal(view.anyRanked, false, 'a flat window ranks nothing');
  const rising = suspectsView(evaluateSuspects(response([lane('audit', step(12, 5, 20)), empty('sql', 'no-data'), empty('iis', 'no-data')])), false);
  assert.equal(rising.anyRanked, true);
});

test('momentStartMs gives the start of the bar, and null for a bar outside the grid', () => {
  const r = response([lane('audit', step(12, 5, 20))]);
  assert.equal(momentStartMs(r, 6), START + 6 * BAR * 1000);
  assert.equal(momentStartMs(r, 99), null);
  assert.equal(momentStartMs(r, -1), null);
});

test('the rail component is keyboard reachable, labelled, and never opens Run analysis; the chart takes a focus request', () => {
  const rail = read('src/components/SuspectsRail.svelte');
  assert.ok(rail.includes('<aside class="sus"') && rail.includes('aria-label={SUSPECT_TEXT.label}'));
  assert.ok(rail.includes('aria-expanded={all}'), 'See all is a disclosure button');
  assert.ok(rail.includes('aria-label={SUSPECT_TEXT.showOnChartLabel(suspect.id)}'));
  assert.ok(rail.includes('<details'), 'Evidence and checks are native disclosures');
  assert.ok(rail.includes('role="status"'));
  assert.ok(!rail.includes('openAnalysis'), 'Show on chart never leaves the timeline');
  assert.ok(!rail.includes('fetch'), 'no second request: the rail reads the answer the timeline already holds');
  const paper = read('src/components/TracingPaper.svelte');
  assert.ok(paper.includes('class="trgrid" class:hasrail={showRail}'));
  assert.ok(paper.includes('focus={chartFocus}') && paper.includes('onShow={showOnChart}'));
  const chart = read('src/components/TracingChart.svelte');
  assert.ok(chart.includes('focus?: { slot: number; token: number } | null;'));
  assert.ok(/function revealSlot\(slot: number\)[\s\S]*selectedSlot = clampIndex\(slot\);[\s\S]*showKeyboardTip\(\);[\s\S]*overlayEl\?\.focus/.test(chart));
  assert.ok(paper.includes('min-width: 1180px') && paper.includes('380px'));
});
