const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

const root = path.resolve(__dirname, '..');
function loadTypeScript(relativePath) {
  const filename = path.join(root, relativePath);
  const source = fs.readFileSync(filename, 'utf8');
  const output = ts.transpileModule(source, {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022, esModuleInterop: true }
  }).outputText;
  const module = { exports: {} };
  const nodeRequire = createRequire(filename);
  // A relative import of another TypeScript file in src/lib is transpiled the same way.
  const load = (request) => request.startsWith('./') && fs.existsSync(path.join(path.dirname(filename), request + '.ts'))
    ? loadTypeScript(path.relative(root, path.join(path.dirname(filename), request + '.ts')))
    : nodeRequire(request);
  new Function('require', 'module', 'exports', output)(load, module, module.exports);
  return module.exports;
}

const charts = loadTypeScript('src/lib/charts.ts');
const time = loadTypeScript('src/lib/time.ts');
const presentation = loadTypeScript('src/lib/presentation.ts');
const investigation = loadTypeScript('src/lib/investigation.ts');

function stats({ count = 0, p50 = null, p95 = null, histogram = [] } = {}) {
  return { count, meanMs: null, minMs: null, maxMs: null, p50, p95, histogram };
}
function comparison(key, controller, outcome = 'success', currentEvents = 0) {
  return {
    key, sourceId: 'source-1', controller, action: 'run', kind: 'synchronous', outcome,
    state: 'compared', current: stats(), reference: stats({ p50: { lowerMs: 90, upperMs: 110, valueMs: 100 } }),
    medianRatio: null, p95Ratio: null, medianDeltaMs: null, p95DeltaMs: null,
    currentEvents, referenceEvents: 0
  };
}
function point(at, ratio) {
  return {
    at, count: ratio == null ? null : 1,
    p50: ratio == null ? null : { lowerMs: 1, upperMs: 2, valueMs: 1.5 },
    p95: ratio == null ? null : { lowerMs: 2, upperMs: 3, valueMs: 2.5 },
    p95Ratio: ratio
  };
}
function analysis({ start, end, bucketSeconds, apis = [], timeline = [] }) {
  return {
    query: {
      sourceIds: ['source-1'], start, end, baselineStart: start, baselineEnd: end,
      kind: 'all', outcome: 'all', metric: 'total', bucketSeconds, controller: '', action: ''
    },
    apis, timeline, sources: [], quality: { incomplete: 0, invalid: 0, unknownOutcome: 0 },
    approximate: true, warnings: []
  };
}

test('epoch bucket count includes every intersected bucket for unaligned half-open windows', () => {
  const start = Date.parse('2026-10-22T12:00:05.000Z');
  const end = Date.parse('2026-10-22T12:20:05.000Z');
  assert.equal(charts.countEpochBuckets(start, end, 10), 121);
  assert.equal(charts.countEpochBuckets(Date.parse('2026-10-22T12:00:00Z'), Date.parse('2026-10-22T12:20:00Z'), 10), 120);
  assert.equal(charts.countEpochBuckets(start, end, 11), 110);
});

test('automatic bucket width fits both unaligned windows inside 120 epoch buckets', () => {
  const start = '2026-10-22T12:00:05.000Z';
  const end = '2026-10-22T12:20:05.000Z';
  const baselineStart = '2026-10-22T11:40:05.000Z';
  const baselineEnd = start;
  const width = time.bucketForWindows(start, end, baselineStart, baselineEnd);
  assert.ok(width >= 10);
  assert.ok(charts.countEpochBuckets(Date.parse(start), Date.parse(end), width) <= 120);
  assert.ok(charts.countEpochBuckets(Date.parse(baselineStart), Date.parse(baselineEnd), width) <= 120);
});

test('aligned chart points retain the final intersected bucket and exact cohort lookup never falls back', () => {
  const keyB = 'eyJCIiwiYWN0aW9uIn0';
  const start = '2026-10-22T12:00:05.000Z';
  const end = '2026-10-22T12:20:05.000Z';
  const rowB = comparison(keyB, 'B');
  const result = analysis({
    start, end, bucketSeconds: 10, apis: [comparison('missing-A', 'A'), rowB],
    timeline: [{ key: keyB, points: [point('2026-10-22T12:00:00.000Z', 1.2), point('2026-10-22T12:20:00.000Z', 2)] }]
  });
  const missing = charts.alignedPoints(result, 'missing-A');
  const matching = charts.alignedPoints(result, keyB);
  assert.equal(missing.length, 121);
  assert.equal(missing[120].at, '2026-10-22T12:20:00.000Z');
  assert.equal(missing[0].count, null);
  assert.equal(missing[0].p95, null);
  assert.equal(matching[0].p95Ratio, 1.2);
  assert.equal(matching[120].p95Ratio, 2);
  const selectedMissing = result.apis[0];
  const line = charts.timelineOption(result, selectedMissing, 'UTC', null);
  assert.ok(line.series[0].data.every(value => value === null));
  assert.ok(line.series[1].data.every(value => value === null));
  assert.ok(line.series[2].data.every(value => value === 100));
  const normalized = charts.ratioOption(result, selectedMissing, 'UTC');
  assert.ok(normalized.series[0].data.every(value => value === null));
});

test('histogram labels derive zero, finite, and overflow intervals from shared logarithmic bounds', () => {
  assert.deepEqual(charts.histogramBinBounds(0), { lower: 0, upper: 1 });
  assert.deepEqual(charts.histogramBinBounds(1), { lower: 1, upper: 1.1 });
  assert.equal(charts.histogramBinBounds(192).upper, Math.pow(1.1, 192));
  assert.equal(charts.histogramBinBounds(193).lower, Math.pow(1.1, 192));
  assert.equal(charts.histogramBinBounds(193).upper, null);
  assert.match(charts.histogramBinLabel(0), /^\[0, 1\) ms$/);
  assert.match(charts.histogramBinLabel(1), /^\[1, 1\.1\) ms$/);
  assert.match(charts.histogramBinLabel(192), / ms$/);
  assert.match(charts.histogramBinLabel(193), /^≥ 88,592,213 ms \(overflow\)$/);
});

test('UTC previews preserve the actual normalized query instant and datetime-local edits retain milliseconds', () => {
  const serverTime = '2026-10-22T12:00:37.123456Z';
  const query = time.defaultQuery(new Date(serverTime), ['source-1']);
  assert.equal(query.end, '2026-10-22T12:00:37.123Z');
  assert.equal(time.utcPreview(query.end), query.end);
  const localValue = time.toLocalInput(new Date(query.end));
  assert.equal(Date.parse(time.fromLocalInput(localValue)), Date.parse(query.end));
});

test('separate local date/time edits preserve the untouched part, milliseconds, and reject DST gaps', () => {
  const previousTZ = process.env.TZ;
  process.env.TZ = 'America/New_York';
  try {
    const beforeSpringGap = time.fromLocalParts('2026-03-08', '01:59:59.456');
    const local = new Date(beforeSpringGap);
    assert.equal(time.toLocalDatePart(local), '2026-03-08');
    assert.equal(time.toLocalTimePart(local), '01:59:59.456');
    assert.equal(time.toLocalTimePart(new Date(time.fromLocalParts('2026-03-09', time.toLocalTimePart(local)))), '01:59:59.456', 'date edit keeps the selected time');
    assert.equal(time.toLocalDatePart(new Date(time.fromLocalParts(time.toLocalDatePart(local), '01:30:00.789'))), '2026-03-08', 'time edit keeps the selected date');
    assert.equal(new Date(time.fromLocalParts('2026-03-08', '01:30:00.789')).getMilliseconds(), 789);
    assert.throws(() => time.fromLocalParts('2026-03-08', '02:30:00.123'), /clocks change/);
    assert.throws(() => time.fromLocalParts('2026-02-30', '12:00'), /not valid on this date/);
    assert.throws(() => time.fromLocalParts('', '12:00'), /valid date and time/);
  } finally {
    if (previousTZ === undefined) delete process.env.TZ;
    else process.env.TZ = previousTZ;
  }
});

test('outcome percentages use all known events, including events without valid duration samples', () => {
  const success = comparison('success', 'Orders', 'success', 20);
  const failure = comparison('failure', 'Orders', 'failure', 2);
  failure.current.count = 0; // known failures have no valid latency samples
  const label = presentation.outcomeRateText([success, failure], success, 'all');
  assert.match(label, /20 \/ 22 known events \(90\.9%\)/);
  assert.match(presentation.outcomeRateText([success, failure], success, 'success'), /success · 20 events/);
  assert.doesNotMatch(presentation.outcomeRateText([success, failure], success, 'success'), /%/);
});

test('statusRates splits 4xx and 5xx', () => {
  const rollup = { controller: 'Orders', action: 'Create', requests: 200, failures: 30, errorRate: 0.15, clientErrorRate: 0.1, serverErrorRate: 0.05, perMinute: 2 };
  const rates = presentation.statusRates(rollup, 'all', 'all');
  assert.equal(rates.client, '10.0%');
  assert.equal(rates.server, '5.0%');
  assert.equal(rates.merged, '15.0%');
  assert.match(rates.tip, /Combined error rate 15\.0%/);
  assert.match(rates.tip, /30 failed of 200/);
  // Under an outcome filter the share is unknowable: never show 0%.
  const filtered = presentation.statusRates(rollup, 'client_error', 'all');
  assert.deepEqual([filtered.client, filtered.server, filtered.merged], ['—', '—', '—']);
  // A rollup without the split, or without any known result, shows no rate rather than 0%.
  assert.equal(presentation.statusRates({ ...rollup, clientErrorRate: undefined, serverErrorRate: null }, 'all').client, '—');
  assert.equal(presentation.statusRates({ ...rollup, serverErrorRate: null }, 'all').server, '—');
  const unsplit = presentation.statusRates({ ...rollup, clientErrorRate: null, serverErrorRate: null }, 'all', 'all');
  assert.deepEqual([unsplit.client, unsplit.server, unsplit.merged], ['—', '—', '15.0%']);
  assert.match(unsplit.tip, /split is unavailable/);
  assert.equal(presentation.statusRates(undefined, 'all').client, '—');
  assert.match(presentation.statusRates(undefined, 'all').tip, /No requests with a known/);
  assert.match(presentation.statusRates(rollup, 'all', 'hangfire').tip, /for the hangfire requests of this API/);
  assert.match(presentation.statusRates({ ...rollup, kind: 'asynchronous' }, 'all', 'all').tip, /sources merged, for the asynchronous requests/);
  assert.match(presentation.statusRates(rollup, 'all', 'all').tip, /rollup without a kind/);
});

test('kind labels and quality by kind keep unknown HTTP rows distinct', () => {
  assert.equal(presentation.kindLabel('unknown'), 'HTTP (polling type unknown)');
  assert.equal(presentation.kindLabel('synchronous'), 'synchronous');
  assert.equal(presentation.kindLabel(undefined), '');
  assert.match(presentation.unknownKindTip, /cannot be attributed to Synchronous or Asynchronous/);
  assert.equal(presentation.qualityByKindText(undefined), '');
  assert.equal(presentation.qualityByKindText({ incomplete: 0, invalid: 0, unknownOutcome: 0 }), '');
  const text = presentation.qualityByKindText({ incomplete: 5, invalid: 1, unknownOutcome: 0, byKind: {
    unknown: { incomplete: 4, invalid: 0, unknownOutcome: 0 }, hangfire: { incomplete: 1, invalid: 1, unknownOutcome: 0 }, synchronous: { incomplete: 0, invalid: 0, unknownOutcome: 0 } } });
  assert.equal(text, 'By request type: hangfire: 1 missing timing, 1 unusable timing · HTTP (polling type unknown): 4 missing timing');
});

test('API-by-time heatmap preserves exact cohort rows and missing timeline cells', () => {
  const start = '1970-01-01T00:00:00.000Z';
  const end = '1970-01-01T00:00:30.000Z';
  const rowA = comparison('key-A-exact', 'A', 'success', 3);
  const rowB = comparison('key-B-exact', 'B', 'failure', 2);
  const result = analysis({
    start, end, bucketSeconds: 10, apis: [rowB, rowA],
    timeline: [
      { key: rowA.key, points: [point(start, 2)] },
      { key: rowB.key, points: [point('1970-01-01T00:00:10.000Z', 3)] }
    ]
  });
  const option = charts.heatmapOption(result, new Map([['source-1', 'Source one']]), 'UTC');
  const values = option.series[0].data.map(cell => cell.value);
  assert.equal(option.xAxis.data.length, 3);
  assert.equal(option.yAxis.data.length, 2);
  assert.equal(charts.heatmapCohortKeyAt(result, 0), rowA.key);
  assert.equal(charts.heatmapCohortKeyAt(result, 1), rowB.key);
  assert.equal(option.series[0].data[0].name, rowA.key);
  assert.equal(option.series[0].data[4].name, rowB.key);
  assert.deepEqual(values, [[0, 0, 2], [1, 0, null], [2, 0, null], [0, 1, null], [1, 1, 3], [2, 1, null]]);
  assert.equal(option.series[0].data[1].itemStyle.color, '#edf0f4');
});

test('registered ECharts legends and markLine render without missing-component warnings', () => {
  const echarts = charts.echarts;
  const { SVGRenderer } = require('echarts/renderers');
  echarts.use([SVGRenderer]);
  const row = comparison('render-key', 'Render', 'success', 1);
  const result = analysis({
    start: '1970-01-01T00:00:00.000Z', end: '1970-01-01T00:00:10.000Z', bucketSeconds: 10,
    apis: [row], timeline: [{ key: row.key, points: [point('1970-01-01T00:00:00.000Z', 1.5)] }]
  });
  const options = [
    charts.timelineOption(result, row, 'UTC', 50),
    charts.ratioOption(result, row, 'UTC'),
    charts.histogramOption({ ...row, current: stats({ histogram: [{ bin: 0, count: 1 }] }) }),
    charts.heatmapOption(result, new Map(), 'UTC')
  ];
  const warnings = [];
  const originalWarn = console.warn;
  try {
    console.warn = (...args) => warnings.push(args.join(' '));
    const chart = echarts.init(null, undefined, { renderer: 'svg', ssr: true, width: 720, height: 440 });
    for (const option of options) {
      chart.clear();
      chart.setOption(option);
      assert.match(chart.renderToSVGString(), /<svg/);
    }
    chart.dispose();
  } finally { console.warn = originalWarn; }
  assert.deepEqual(warnings, []);
});

test('chart time zones always offer UTC alongside the browser-supported zones', () => {
  assert.ok(time.zones.includes('UTC'));
  assert.equal(time.zones.filter(zone => zone === 'UTC').length, 1);
});

test('investigation snapshot round-trips every filter, display setting, and selected-source descriptor', () => {
  const query = { sourceIds: ['old-1'], start: '2026-10-22T12:00:00.000Z', end: '2026-10-22T12:15:00.000Z', baselineStart: '2026-10-22T11:45:00.000Z', baselineEnd: '2026-10-22T12:00:00.000Z', kind: 'hangfire', outcome: 'failure', metric: 'api', bucketSeconds: 37, controller: 'Orders', action: 'Run' };
  const snapshot = investigation.createInvestigationSnapshot(query, [{ id: 'old-1', label: 'secret label', database: 'ledger', collection: 'audit', selectionKey: 'stable-key' }], 'Asia/Dubai', 123.5);
  const restored = investigation.restoreInvestigation(snapshot, [{ id: 'new-1', label: 'other', database: 'ledger', collection: 'audit', selectionKey: 'stable-key' }]);
  assert.deepEqual(restored, { ...query, sourceIds: ['new-1'], zone: 'Asia/Dubai', targetMs: 123.5 });
});

test('stable keys remap session IDs and keep same database from another connection unselected', () => {
  const snapshot = investigation.createInvestigationSnapshot({ sourceIds: ['old'], start: '2026-10-22T12:00:00.000Z', end: '2026-10-22T12:15:00.000Z', baselineStart: '2026-10-22T11:45:00.000Z', baselineEnd: '2026-10-22T12:00:00.000Z', kind: 'all', outcome: 'all', metric: 'total', bucketSeconds: 10, controller: '', action: '' }, [{ id: 'old', label: 'db A', database: 'same', collection: 'audit', selectionKey: 'host-a-key' }], 'UTC', null);
  assert.deepEqual(investigation.restoreInvestigation(snapshot, [{ id: 'new', label: 'db B', database: 'same', collection: 'audit', selectionKey: 'host-b-key' }]).sourceIds, []);
  assert.deepEqual(investigation.restoreInvestigation(snapshot, [{ id: 'new', label: 'db A', database: 'different', collection: 'other', selectionKey: 'host-a-key' }]).sourceIds, ['new']);
});

test('intentional zero source selection is preserved', () => {
  const query = { sourceIds: [], start: '2026-10-22T12:00:00.000Z', end: '2026-10-22T12:15:00.000Z', baselineStart: '2026-10-22T11:45:00.000Z', baselineEnd: '2026-10-22T12:00:00.000Z', kind: 'all', outcome: 'all', metric: 'total', bucketSeconds: 10, controller: '', action: '' };
  const snapshot = investigation.createInvestigationSnapshot(query, [], 'UTC', null);
  assert.deepEqual(investigation.restoreInvestigation(snapshot, [{ id: 'new', label: 'x', database: 'x', collection: 'y' }]).sourceIds, []);
});

test('snapshot parser ignores malformed, unsupported-version, and invalid-schema data', () => {
  assert.equal(investigation.parseInvestigationSnapshot('{'), null);
  assert.equal(investigation.parseInvestigationSnapshot(JSON.stringify({ version: 2 })), null);
  assert.equal(investigation.parseInvestigationSnapshot(JSON.stringify({ version: 1, query: {}, selectedSources: [], zone: 'UTC', targetMs: null })), null);
});

test('saved investigations accept the http kinds and reject unknown kinds', () => {
  const query = { start: '2026-10-22T12:00:00.000Z', end: '2026-10-22T12:15:00.000Z', baselineStart: '2026-10-22T11:45:00.000Z', baselineEnd: '2026-10-22T12:00:00.000Z', outcome: 'all', metric: 'total', bucketSeconds: 10, controller: '', action: '' };
  const snap = kind => ({ version: 1, query: { ...query, kind }, selectedSources: [], zone: 'UTC', targetMs: null });
  for (const kind of ['http', 'http_unknown']) assert.equal(investigation.validateInvestigationSnapshot(snap(kind)), true, kind);
  assert.equal(investigation.validateInvestigationSnapshot(snap('unknown')), false);
});

test('snapshot parsing and restore project query fields and source descriptors onto the allowlist', () => {
  const query = { start: '2026-10-22T12:00:00.000Z', end: '2026-10-22T12:15:00.000Z', baselineStart: '2026-10-22T11:45:00.000Z', baselineEnd: '2026-10-22T12:00:00.000Z', kind: 'all', outcome: 'all', metric: 'total', bucketSeconds: 10, controller: '', action: '', uri: 'mongodb://attacker', password: 'secret', savedConnectionId: 'secret' };
  const parsed = investigation.parseInvestigationSnapshot(JSON.stringify({ version: 1, query, selectedSources: [{ id: 'source', selectionKey: 'key', uri: 'mongodb://attacker' }], zone: 'UTC', targetMs: null, payload: 'private' }));
  assert.ok(parsed);
  assert.equal(JSON.stringify(parsed).includes('mongodb'), false);
  assert.equal(JSON.stringify(investigation.restoreInvestigation(parsed, [{ id: 'source', label: '', database: '', collection: '' }])).includes('mongodb'), false);
});

test('snapshot schema checks enum value types and backend length and source bounds', () => {
  const base = { version: 1, query: { start: '2026-10-22T12:00:00.000Z', end: '2026-10-22T12:15:00.000Z', baselineStart: '2026-10-22T11:45:00.000Z', baselineEnd: '2026-10-22T12:00:00.000Z', kind: 'all', outcome: 'all', metric: 'total', bucketSeconds: 10, controller: '', action: '' }, selectedSources: [], zone: 'UTC', targetMs: null };
  assert.equal(investigation.validateInvestigationSnapshot({ ...base, query: { ...base.query, kind: ['all'] } }), false);
  assert.equal(investigation.validateInvestigationSnapshot({ ...base, query: { ...base.query, controller: 'x'.repeat(201) } }), false);
  assert.equal(investigation.validateInvestigationSnapshot({ ...base, selectedSources: Array.from({ length: 9 }, (_, i) => ({ id: String(i) })) }), false);
  assert.equal(investigation.validateInvestigationSnapshot({ ...base, selectedSources: [{ id: 'x'.repeat(257) }] }), false);
  assert.equal(investigation.validateInvestigationSnapshot({ ...base, selectedSources: [{ id: 'x', selectionKey: 'k'.repeat(129) }] }), false);
});

test('snapshot serialization allowlists fields and excludes credentials and result data', () => {
  const query = { sourceIds: ['s'], start: '2026-10-22T12:00:00.000Z', end: '2026-10-22T12:15:00.000Z', baselineStart: '2026-10-22T11:45:00.000Z', baselineEnd: '2026-10-22T12:00:00.000Z', kind: 'all', outcome: 'all', metric: 'total', bucketSeconds: 10, controller: '', action: '', uri: 'mongodb://secret', savedConnectionId: 'secret' };
  const snapshot = investigation.createInvestigationSnapshot(query, [{ id: 's', label: 'private', database: 'db', collection: 'audit', selectionKey: 'key', uri: 'mongodb://secret', credentials: 'secret' }], 'UTC', null);
  const serialized = JSON.stringify(snapshot);
  for (const forbidden of ['mongodb', 'secret', 'private', 'credentials', 'uri', 'savedConnectionId', 'results', 'payload']) assert.equal(serialized.includes(forbidden), false);
});

test('blocked storage reads, writes, and clears are nonfatal', () => {
  const blocked = { getItem() { throw new Error('blocked'); }, setItem() { throw new Error('quota'); }, removeItem() { throw new Error('blocked'); } };
  assert.equal(investigation.readInvestigationSnapshot(blocked), null);
  const snapshot = investigation.createInvestigationSnapshot({ sourceIds: [], start: '2026-10-22T12:00:00.000Z', end: '2026-10-22T12:15:00.000Z', baselineStart: '2026-10-22T11:45:00.000Z', baselineEnd: '2026-10-22T12:00:00.000Z', kind: 'all', outcome: 'all', metric: 'total', bucketSeconds: 10, controller: '', action: '' }, [], 'UTC', null);
  assert.equal(investigation.writeInvestigationSnapshot(blocked, snapshot), false);
  assert.doesNotThrow(() => investigation.clearInvestigationSnapshot(blocked));
});

test('live and demo investigations use separate keys and invalid edits do not overwrite the last valid snapshot', () => {
  const entries = new Map();
  const storage = { getItem(key) { return entries.get(key) ?? null; }, setItem(key, value) { entries.set(key, value); }, removeItem(key) { entries.delete(key); } };
  const query = { sourceIds: [], start: '2026-10-22T12:00:00.000Z', end: '2026-10-22T12:15:00.000Z', baselineStart: '2026-10-22T11:45:00.000Z', baselineEnd: '2026-10-22T12:00:00.000Z', kind: 'all', outcome: 'all', metric: 'total', bucketSeconds: 10, controller: '', action: '' };
  const valid = investigation.createInvestigationSnapshot(query, [], 'UTC', null);
  assert.equal(investigation.writeInvestigationSnapshot(storage, valid, 'live'), true);
  assert.equal(investigation.readInvestigationSnapshot(storage, 'demo'), null);
  assert.equal(investigation.writeInvestigationSnapshot(storage, { ...valid, query: { ...valid.query, bucketSeconds: 1 } }, 'live'), false);
  assert.deepEqual(investigation.readInvestigationSnapshot(storage, 'live'), valid);
});

test('unavailable sources retain selected and unchecked intent through edits and ordered re-addition', () => {
  const A = { id: 'old-a', label: 'A', database: 'db-a', collection: 'audit', selectionKey: 'key-a' };
  const B = { id: 'old-b', label: 'B', database: 'db-b', collection: 'audit', selectionKey: 'key-b' };
  const query = { sourceIds: [A.id], start: '2026-10-22T12:00:00.000Z', end: '2026-10-22T12:15:00.000Z', baselineStart: '2026-10-22T11:45:00.000Z', baselineEnd: '2026-10-22T12:00:00.000Z', kind: 'all', outcome: 'all', metric: 'total', bucketSeconds: 10, controller: '', action: '' };
  const original = investigation.createInvestigationSnapshot(query, [A, B], 'UTC', null);
  const restoredEmpty = investigation.restoreInvestigation(original, []);
  const edited = investigation.createInvestigationSnapshot({ ...restoredEmpty, controller: 'Orders' }, [], 'UTC', null, original);
  assert.deepEqual(edited.knownSources.map(x => x.selectionKey), ['key-a', 'key-b']);
  assert.deepEqual(edited.selectedSources.map(x => x.selectionKey), ['key-a']);
  const Bnew = { ...B, id: 'new-b' };
  assert.equal(investigation.shouldSelectAddedSource(Bnew, edited), false);
  assert.equal(investigation.hasUnresolvedKnownSources(edited, [Bnew]), true);
  const Anew = { ...A, id: 'new-a' };
  assert.equal(investigation.shouldSelectAddedSource(Anew, edited), true);
  assert.equal(investigation.shouldSelectAddedSource({ id: 'new-c', label: 'C', database: 'db-c', collection: 'audit', selectionKey: 'key-c' }, edited), true);
});

test('zero-selection intent survives source re-addition and explicit deletion removes stale intent', () => {
  const A = { id: 'a', label: 'A', database: 'a', collection: 'audit', selectionKey: 'key-a' };
  const B = { id: 'b', label: 'B', database: 'b', collection: 'audit', selectionKey: 'key-b' };
  const query = { sourceIds: [], start: '2026-10-22T12:00:00.000Z', end: '2026-10-22T12:15:00.000Z', baselineStart: '2026-10-22T11:45:00.000Z', baselineEnd: '2026-10-22T12:00:00.000Z', kind: 'all', outcome: 'all', metric: 'total', bucketSeconds: 10, controller: '', action: '' };
  const snapshot = investigation.createInvestigationSnapshot(query, [A, B], 'UTC', null);
  const Bnew = { ...B, id: 'new-b' };
  const Anew = { ...A, id: 'new-a' };
  assert.equal(investigation.shouldSelectAddedSource(Bnew, snapshot), false);
  assert.equal(investigation.shouldSelectAddedSource(Anew, snapshot), false);
  const afterExplicitDelete = investigation.forgetInvestigationSource(snapshot, Anew);
  assert.equal(investigation.hasUnresolvedKnownSources(afterExplicitDelete, [Bnew]), false);
  assert.equal(investigation.shouldSelectAddedSource(Anew, afterExplicitDelete), true);
});

test('production-sized results rank real slowdowns first and keep unavailable ratios last in either direction', () => {
 const rows = Array.from({length:214}, (_,i) => ({...comparison('g-'+i,'API'+i), state:i===0?'missing_current':'compared', current:stats({count:i===0?0:30}), reference:stats({count:30}), p95Ratio:i===0?null:i/10}));
 assert.equal(presentation.initialComparison(rows).key,'g-213');
 assert.equal(presentation.rankComparisons(rows)[0].key,'g-213');
 assert.equal(presentation.rankComparisons(rows,'ratio',1).at(-1).key,'g-0');
 assert.equal(presentation.rankComparisons(rows,'ratio',-1).at(-1).key,'g-0');
 const notes=presentation.resultNotes(rows);
 assert.equal(notes.length,2);
 assert.ok(notes.some(note=>note.includes('1 API groups have no usable timings')));
 assert.ok(!notes.join(' ').includes('g-0'));
});

test('impact sort puts the largest total added time first and keeps rows without impact last', () => {
 const row=(key,impactMs,count)=>({...comparison(key,'API'+key),impactMs,current:stats({count}),p95Ratio:1});
 const rows=[row('small',500*20,20),row('none',undefined,500),row('big',200*1000,1000),row('negative',-3000,100),row('null',null,50)];
 assert.deepEqual(presentation.rankComparisons(rows,'impact').map(r=>r.key),['big','small','negative','none','null']);
 assert.deepEqual(presentation.rankComparisons(rows,'impact',1).map(r=>r.key),['negative','small','big','none','null']);
 assert.equal(presentation.initialComparison(rows).key,'big');
});

test('stale-run detection ignores JSON property order and equivalent UTC timestamps but catches real edits', () => {
 const q=analysis({start:'2026-10-22T10:00:00.000Z',end:'2026-10-22T12:00:00.000Z',bucketSeconds:600}).query;
 const reordered=Object.fromEntries(Object.entries(q).reverse());
 reordered.start='2026-10-22T10:00:00Z';
 assert.equal(presentation.queryFingerprint(q),presentation.queryFingerprint(reordered));
 assert.notEqual(presentation.queryFingerprint(q),presentation.queryFingerprint({...q,controller:'Payroll'}));
 assert.notEqual(presentation.queryFingerprint(q),presentation.queryFingerprint({...q,sourceIds:[]}));
});

test('multi-API timeline uses each own baseline and preserves absent observations', () => {
 const quantile=v=>({valueMs:v,lowerMs:v-1,upperMs:v+1});
 const a={...comparison('a','Payroll'),reference:stats({count:30,p95:quantile(100)})};
 const b={...comparison('b','Jobs'),reference:stats({count:30,p95:quantile(200)})};
 const p={at:'2026-10-22T12:00:00.000Z',count:30,p50:quantile(100),p95:quantile(150),p95Ratio:null};
 assert.equal(charts.baselineChange(a,p,'milliseconds'),50);
 assert.equal(charts.baselineChange(b,p,'milliseconds'),-50);
 assert.equal(charts.baselineChange(a,p,'percent'),50);
 assert.equal(charts.baselineChange(b,p,'percent'),-25);
 assert.equal(charts.baselineChange(a,undefined,'percent'),null);
 assert.equal(charts.baselineChange({...a,reference:stats({p95:quantile(0)})},p,'percent'),null);
});

test('request volume preserves all starts, empty slices, Other APIs, and exact subwindows', () => {
 const volume=loadTypeScript('src/lib/volume.ts');
 const q={start:'2026-10-22T12:00:01Z',end:'2026-10-22T12:00:29Z',bucketSeconds:10};
 const rows=[{controller:'A',action:'Run',at:'2026-10-22T12:00:00Z',count:5},{controller:'B',action:'Run',at:'2026-10-22T12:00:20Z',count:3},{controller:'<img>',action:'Run',at:'2026-10-22T12:00:20Z',count:1}];
 const result=volume.volumeData(q,rows,0,2,1);
 assert.equal(result.total,9);assert.deepEqual(result.series.map(s=>s.counts),[[5,0,0],[0,0,4]]);
 assert.equal(result.slots[0].start,Date.parse(q.start));assert.equal(result.slots[2].end,Date.parse(q.end));
 const sub=volume.volumeData(q,rows,2,2,1);assert.equal(sub.total,4);assert.equal(sub.apis[0].controller,'B');
 assert.equal(volume.volumeData(q,rows,0,2,8,JSON.stringify(['A','Run'])).total,5);
 assert.equal(volume.volumeData(q,[],0,2).total,0);
});

test('traffic scatter flags any slow request, uses measured complete-window denominators, and does not average percentiles',()=>{
 const {trafficData}=loadTypeScript('src/lib/traffic.ts');
 const q={start:'2026-10-22T12:00:01Z',end:'2026-10-22T12:00:40Z',bucketSeconds:10};
 const timing=(max,count=20)=>({count,maxMs:max,p95:{valueMs:10,lowerMs:9,upperMs:11},p50:{valueMs:5,lowerMs:4,upperMs:6}});
 const rows=[{at:'2026-10-22T12:00:00Z',count:4,timing:timing(2000)},{at:'2026-10-22T12:00:10Z',count:4,timing:timing(2000)},{at:'2026-10-22T12:00:20Z',count:5,timing:timing(20)},{at:'2026-10-22T12:00:30Z',count:5,timing:timing(null,0)}];
 const d=trafficData(q,rows,1000,'p95');assert.equal(d.windows[0].partial,true);assert.equal(d.points[0].slow,true);assert.equal(d.points[0].latency,10);assert.equal(d.buckets.length,1);assert.equal(d.buckets[0].measured,2);assert.equal(d.buckets[0].slow,1);assert.equal(d.buckets[0].percent,50);assert.equal(d.unmeasured,1);
});

test('error rate shows as a percent, and the load badge needs |rho| >= 0.6 over at least 8 paired points', () => {
  const { errorRateText, loadBadge, rollupFor } = loadTypeScript('src/lib/traffic.ts');
  assert.equal(errorRateText(null), '—');
  assert.equal(errorRateText(undefined), '—');
  assert.equal(errorRateText(0), '0.0%');
  assert.equal(errorRateText(0.05), '5.0%');
  assert.equal(errorRateText(0.0123), '1.2%');
  assert.equal(loadBadge(0.8), 'slows under load (ρ=0.8)');
  assert.equal(loadBadge(0.6), 'slows under load (ρ=0.6)');
  assert.equal(loadBadge(0.59), null);
  assert.equal(loadBadge(-0.9), 'faster under load (ρ=-0.9)', 'a strong negative correlation must not read as slowing');
  assert.equal(loadBadge(-0.5), null);
  assert.equal(loadBadge(0.8, 7), null);
  assert.equal(loadBadge(0.8, 8), 'slows under load (ρ=0.8)');
  assert.equal(loadBadge(null), null);
  assert.equal(loadBadge(undefined), null);
  assert.equal(loadBadge(Number.NaN), null);
  assert.equal(errorRateText(0.05, 'all'), '5.0%');
  assert.equal(errorRateText(0.05, 'success'), '—', 'an outcome filter makes the share meaningless');
  assert.equal(errorRateText(0, 'failure'), '—');
  const { errorRateTip, loadTip } = loadTypeScript('src/lib/traffic.ts');
  const r = { controller: 'A', action: 'Run', requests: 200, failures: 10, errorRate: 0.05, perMinute: 3.33 };
  assert.match(errorRateTip(r, 'all', 'all'), /10 failed of 200/);
  assert.match(errorRateTip(r, 'all', 'all'), /all sources merged/);
  assert.match(errorRateTip({ ...r, kind: 'synchronous' }, 'all', 'all'), /for the synchronous requests/);
  assert.match(errorRateTip(r, 'all', 'hangfire'), /hangfire/);
  assert.match(errorRateTip(r, 'success', 'all'), /outcome filter set to All/);
  assert.match(errorRateTip(undefined, 'all', 'all'), /known/);
  assert.match(loadTip(0.8), /other traffic/);
  assert.match(loadTip(-0.9), /latency falls as load rises/);
  assert.doesNotMatch(loadTip(-0.9), /rises and falls/);
  const rollups = [{ controller: 'A', action: 'Run', requests: 10, failures: 1, errorRate: 0.1, perMinute: 2 }];
  assert.equal(rollupFor(rollups, { controller: 'A', action: 'Run', kind: 'synchronous' }).errorRate, 0.1);
  assert.equal(rollupFor(rollups, { controller: 'A', action: 'Other', kind: 'synchronous' }), undefined);
  assert.equal(rollupFor(undefined, { controller: 'A', action: 'Run', kind: 'synchronous' }), undefined);
  const byKind = [
    { controller: 'A', action: 'Run', kind: 'synchronous', requests: 10, failures: 1, errorRate: 0.1, perMinute: 2 },
    { controller: 'A', action: 'Run', kind: 'asynchronous', requests: 10, failures: 5, errorRate: 0.5, perMinute: 2 }];
  assert.equal(rollupFor(byKind, { controller: 'A', action: 'Run', kind: 'asynchronous' }).errorRate, 0.5, 'rollups are never merged across kinds');
  assert.equal(rollupFor(byKind, { controller: 'A', action: 'Run', kind: 'unknown' }), undefined);
});

test('phase bar segments split audit and api shares, tolerate gaps, and describe the additive model', () => {
  const { phaseSegments, phaseMs, phaseTip } = loadTypeScript('src/lib/phases.ts');
  const s = phaseSegments({ auditMs: 20, apiMs: 80, payloadMs: 5 });
  assert.equal(s.total, 100);
  assert.equal(s.auditPct, 20);
  assert.equal(s.apiPct, 80);
  const apiOnly = phaseSegments({ apiMs: 50 });
  assert.equal(apiOnly.audit, null);
  assert.equal(apiOnly.apiPct, 100);
  assert.equal(phaseSegments(null), null);
  assert.equal(phaseSegments(undefined), null);
  assert.equal(phaseSegments({ payloadMs: 5 }), null, 'payload alone is not latency');
  assert.equal(phaseSegments({ auditMs: 0, apiMs: 0 }), null);
  assert.equal(phaseMs(null), 'N/A');
  assert.equal(phaseMs(12.34), '12.3 ms');
  assert.equal(phaseMs(1500), '1.50 s');
  const tip = phaseTip({ auditMs: 0, apiMs: 200 });
  assert.match(tip, /after the response/);
  assert.match(tip, /api \+200 ms/);
  assert.match(tip, /audit 0\.0 ms/);
  assert.doesNotMatch(phaseTip(null), /Change versus reference/);
});

test('the total metric is labelled as observed API time and never as a total request', () => {
  assert.equal(presentation.metricLabels.total, 'Observed API time (AuditCreateTime + ApiTime)');
  assert.equal(presentation.metricLabels.api, 'API processing');
  for (const file of ['src/components/QueryControls.svelte', 'src/App.svelte', 'src/components/ComparisonTable.svelte']) {
    assert.doesNotMatch(fs.readFileSync(path.join(root, file), 'utf8'), /Total request/);
  }
});

test('the partial-sample tooltip appears only when legacy samples lack AuditCreateTime', () => {
  assert.equal(presentation.observedPartialTip(undefined), undefined);
  assert.equal(presentation.observedPartialTip(0), undefined);
  assert.equal(presentation.observedPartialTip(1234), `${(1234).toLocaleString()} samples lack AuditCreateTime (legacy)`);
});

test('the quality strip shows pending async only when it is above zero', () => {
  const app = fs.readFileSync(path.join(root, 'src/App.svelte'), 'utf8');
  assert.match(app, /\{#if analysis\.quality\.pendingAsync\}[^\n]*pending async/);
});

const breakdown = loadTypeScript('src/lib/breakdown.ts');
const breakdownBase = () => analysis({ start: '2026-10-22T12:00:00Z', end: '2026-10-22T13:00:00Z', bucketSeconds: 60 }).query;

test('a breakdown row filter merges into the query without dropping other filters', () => {
  const base = breakdownBase();
  // The server builds a row's filter as a fragment of the shared record filter fields.
  const query = { ...base, controller: 'Pay', uiVersions: ['2.0'], userAgentContains: 'old', urlContains: '/pay', completion: 'incomplete' };
  const row = { value: 'UA-A', count: 3, filter: { userAgentContains: 'UA-A' } };
  const merged = breakdown.mergeRecordFilter(query, breakdown.breakdownRowFilter(row));
  assert.deepEqual(breakdown.activeRecordFilter(merged), { uiVersions: ['2.0'], userAgentContains: 'UA-A', urlContains: '/pay', completion: 'incomplete' });
  assert.equal(merged.controller, 'Pay');
  assert.deepEqual(merged.sourceIds, base.sourceIds);
  assert.equal(query.userAgentContains, 'old', 'the input query is not mutated');
  // A user id value replaces the earlier user ids: one dimension.
  const swapped = breakdown.mergeRecordFilter({ ...base, userIds: [3, 4] }, { userIds: [7] });
  assert.deepEqual(breakdown.activeRecordFilter(swapped), { userIds: [7] });
  assert.deepEqual(breakdown.activeRecordFilter(breakdown.mergeRecordFilter(base, { statusCodes: ['OK'] })), { statusCodes: ['OK'] });
  // "Other" and every row without a filter (missing value, polling type, capped) cannot be listed.
  assert.equal(breakdown.breakdownRowFilter({ value: 'Other', count: 9 }), null);
  assert.equal(breakdown.breakdownRowFilter({ value: '(not yet written)', missing: true, count: 9 }), null);
  assert.equal(breakdown.breakdownRowFilter({ value: 'Synchronous', count: 9, filter: {} }), null);
  // A fragment the shared serialization would drop selects nothing and is not clickable.
  assert.equal(breakdown.breakdownRowFilter({ value: '0', count: 1, filter: { userIds: [0] } }), null);
  assert.equal(breakdown.mergeRecordFilter(query, null), query);
});

test('a breakdown request leaves its own dimension out of the active filter', () => {
  const base = breakdownBase();
  const full = { ...base, userAgentContains: 'a', statusCodes: ['OK'], userIds: [7], operation: 'Pay.Run' };
  assert.deepEqual(breakdown.activeRecordFilter(breakdown.withoutDimension(full, 'userAgent')), { userIds: [7], statusCodes: ['OK'], operation: 'Pay.Run' });
  assert.deepEqual(breakdown.activeRecordFilter(breakdown.withoutDimension(full, 'userId')), { userAgentContains: 'a', statusCodes: ['OK'], operation: 'Pay.Run' });
  assert.deepEqual(breakdown.activeRecordFilter(breakdown.withoutDimension(full, 'pollingType')), breakdown.activeRecordFilter(full));
  assert.deepEqual(breakdown.withoutDimension(full, 'userAgent').sourceIds, base.sourceIds);
  assert.deepEqual(breakdown.activeRecordFilter(breakdown.clearRecordFilter(full)), {});
  assert.deepEqual(breakdown.clearRecordFilter(full).sourceIds, base.sourceIds);
  assert.equal(breakdown.hasRecordFilter({}), false);
  assert.equal(breakdown.hasRecordFilter({ userIps: [''] }), false);
  assert.equal(breakdown.hasRecordFilter({ userIps: ['10.0.0.1'] }), true);
});

test('a drill-down replaces the record filters of the base query and nothing else', () => {
  const base = { ...breakdownBase(), urlContains: '/pay', userIds: [3] };
  assert.equal(breakdown.applyDrill(base, {}), base);
  const drill = breakdown.activeRecordFilter(breakdown.mergeRecordFilter(base, { userIds: [7] }));
  const effective = breakdown.applyDrill(base, drill);
  assert.deepEqual(breakdown.activeRecordFilter(effective), { userIds: [7], urlContains: '/pay' });
  assert.equal(effective.controller, base.controller);
  assert.deepEqual(breakdown.activeRecordFilter(base), { userIds: [3], urlContains: '/pay' });
  assert.deepEqual(breakdown.recordFilterChips(effective), [{ dim: 'userId', text: 'User: 7' }]);
  assert.deepEqual(breakdown.recordFilterChips({ statusCodes: ['OK'], userAgentContains: 'Fire', uiVersions: ['2.1'], userIps: ['10.0.0.1'] }).map(chip => chip.dim), ['statusCode', 'uiVersion', 'userAgent', 'userIp']);
});

test('breakdown helpers cover every record filter key', () => {
  // clearRecordFilter must drop every key serializeRecordFilters can emit, or a stale filter would leak into a drill-down.
  const every = { userIds: [1], uiVersions: ['1'], userAgentContains: 'a', userIps: ['1.1.1.1'], urlContains: 'u', auditId: 'a'.repeat(24), statusCodes: ['OK'], completion: 'pending', operation: 'A.b' };
  assert.deepEqual(Object.keys(investigation.serializeRecordFilters(every)).sort(), Object.keys(every).sort());
  assert.deepEqual(breakdown.activeRecordFilter(breakdown.clearRecordFilter({ ...breakdownBase(), ...every })), {});
});
test('a pooled-kinds breakdown says why it has no percentiles and shows an em dash', () => {
  assert.equal(breakdown.pooledKindsNote, 'Choose Synchronous, Asynchronous or Hangfire to see percentiles; types are never pooled.');
  assert.ok(breakdown.breakdownNotes('userId', true).includes(breakdown.pooledKindsNote));
  assert.ok(!breakdown.breakdownNotes('userId', false).includes(breakdown.pooledKindsNote));
  assert.ok(!breakdown.breakdownNotes('userId', undefined).includes(breakdown.pooledKindsNote));
  const quantile = { lowerMs: 90, upperMs: 110, valueMs: 100 };
  assert.equal(breakdown.percentileText(quantile, true), '—');
  assert.equal(breakdown.percentileText(null, true), '—');
  assert.equal(breakdown.percentileText(quantile, false), '≈ 100.0 ms');
  assert.equal(breakdown.percentileText(null, false), 'N/A');
  const panel = fs.readFileSync(path.join(root, 'src/components/BreakdownPanel.svelte'), 'utf8');
  assert.match(panel, /result\?\.pooledKinds === true/);
  assert.match(panel, /percentileText\(row\.stats\.p50, pooled\)/);
  assert.match(panel, /percentileText\(result\.other\.stats\.p95, pooled\)/);
  assert.match(panel, /breakdownNotes\(dim, pooled\)/);
  assert.match(fs.readFileSync(path.join(root, 'src/lib/types.ts'), 'utf8'), /pooledKinds\?: boolean/);
});

test('breakdown copy promises matching requests and explains text matching, non-atomic reads and Complete', () => {
  const panel = fs.readFileSync(path.join(root, 'src/components/BreakdownPanel.svelte'), 'utf8');
  assert.match(panel, /Choose a row to list matching requests/);
  assert.doesNotMatch(panel, /exactly those requests/);
  assert.match(breakdown.matchingNote('userAgent'), /ignoring case.*more requests than the row count/);
  assert.match(breakdown.matchingNote('statusCode'), /ignoring case.*more requests than the row count/);
  for (const dim of ['userId', 'uiVersion', 'userIp', 'pollingType']) assert.equal(breakdown.matchingNote(dim), null);
  assert.ok(breakdown.breakdownNotes('userAgent', false).includes(breakdown.matchingNote('userAgent')));
  assert.ok(breakdown.breakdownNotes('userId', false).every(note => note === breakdown.nonAtomicNote));
  assert.match(breakdown.nonAtomicNote, /separate reads.*drift/);
  assert.doesNotMatch(breakdown.nonAtomicNote, /two passes/);
  // A status code is matched whole (trimmed, ignoring case); only a user agent is matched as contained text.
  assert.doesNotMatch(breakdown.matchingNote('statusCode'), /contain/);
  assert.match(breakdown.matchingNote('userAgent'), /contained/);
  assert.match(panel, /class:breakdown-note-pooled=\{note === pooledKindsNote\}/);
  assert.doesNotMatch(panel, /startsWith\('Choose'\)/);
  assert.match(breakdown.completeTitle, /Requests minus Missing timing.*invalid.*valid timings/);
  assert.match(panel, /title=\{completeTitle\}>Complete</);
  // The empty-state rows span every column: nine headers.
  assert.equal((panel.match(/<th[ >]/g) ?? []).length, 9);
  assert.equal((panel.match(/colspan="9"/g) ?? []).length, 2);
});

test('a breakdown row that cannot be listed says why in one sentence and never invents a filter', () => {
  const other = breakdown.breakdownPickTitle('userAgent', null);
  const missing = breakdown.breakdownPickTitle('userAgent', { value: '(not yet written)', missing: true, count: 4 });
  const polling = breakdown.breakdownPickTitle('pollingType', { value: 'Synchronous', count: 4 });
  const long = breakdown.breakdownPickTitle('userAgent', { value: 'x'.repeat(300), count: 4 });
  const multibyte = breakdown.breakdownPickTitle('userAgent', { value: '\u00e9'.repeat(101), count: 4 });
  const pickUserId = value => breakdown.breakdownPickTitle('userId', { value, count: 4 });
  const userId = ['0', '-3', 'abc', '2147483648'].map(pickUserId);
  const [zero, negative, word, huge] = userId;
  const blankUserId = pickUserId('  ');
  const longWithReplacement = breakdown.breakdownPickTitle('userAgent', { value: '\uFFFD' + 'x'.repeat(250), count: 4 });
  const longUserId = pickUserId('1'.repeat(250));
  const blank = breakdown.breakdownPickTitle('userAgent', { value: '   ', count: 4 });
  const nul = breakdown.breakdownPickTitle('userAgent', { value: 'Mozilla\u0000', count: 4 });
  const invalid = breakdown.breakdownPickTitle('userAgent', { value: 'Mozilla\uFFFD', count: 4 });
  const neutral = breakdown.breakdownPickTitle('userAgent', { value: 'Mozilla/5.0', count: 4 });
  const listable = breakdown.breakdownPickTitle('userId', { value: '7', count: 4, filter: { userIds: [7] } });
  assert.match(other, /remaining values/);
  assert.match(missing, /missing value/);
  assert.match(polling, /Polling type is not a request filter/);
  assert.match(long, /too long/);
  assert.match(multibyte, /too long/, 'the limit is 200 bytes, not characters');
  // Each user id value gets its own reason, and none gets another one's.
  assert.match(zero, /user id below 1/);
  assert.match(negative, /user id below 1/);
  assert.match(word, /must be a whole number/);
  assert.match(huge, /above 2147483647/);
  for (const [title, wrong] of [[zero, /whole number|above 2147483647/], [negative, /whole number|above 2147483647/], [word, /below 1|above 2147483647/], [huge, /below 1|whole number/]]) assert.doesNotMatch(title, wrong);
  // A valid id in range is not rejected by the user id rules; it gets the neutral reason.
  assert.match(pickUserId('7'), /cannot be used as a filter/);
  assert.doesNotMatch(pickUserId('7'), /below 1|above 2147483647|whole number/);
  assert.match(blankUserId, /blank value/);
  assert.doesNotMatch(blankUserId, /user id|whole number/);
  // The length is judged before the characters: a long value that happens to hold U+FFFD is long, not unreadable.
  assert.match(longWithReplacement, /too long/);
  assert.doesNotMatch(longWithReplacement, /unreadable/);
  assert.match(longUserId, /too long/);
  assert.match(blank, /blank value/);
  assert.match(nul, /unreadable characters/);
  assert.match(invalid, /unreadable characters/);
  assert.match(neutral, /cannot be used as a filter/);
  // Only the genuinely long value is called long; no branch claims a cut-off for a value that is not.
  for (const title of [...userId, blankUserId, blank, nul, invalid, neutral]) assert.doesNotMatch(title, /too long|cut off/);
  assert.equal(listable, 'Show matching requests');
  assert.equal(new Set([other, missing, polling, long, zero, word, huge, blank, nul, neutral, listable]).size, 11);
  for (const title of [other, missing, polling, long, multibyte, longWithReplacement, ...userId, blankUserId, blank, nul, invalid, neutral]) assert.equal(title.split('. ').length, 1, 'one sentence: ' + title);
  // A row without a filter yields no filter, whatever its dimension.
  assert.equal(breakdown.breakdownRowFilter({ value: '(not yet written)', missing: true, count: 4 }), null);
  assert.equal(breakdown.breakdownRowFilter({ value: 'Synchronous', count: 4 }), null);
});

test('percentileThreshold uses cohort p95/p99 and returns null below sample floor', () => {
  const { percentileThreshold, percentileLabel } = loadTypeScript('src/lib/slowness.ts');
  const quantile = (valueMs) => ({ lowerMs: valueMs * 0.95, upperMs: valueMs * 1.05, valueMs });
  const cohort = (count, p95, p99) => ({ ...stats({ count, p95: p95 == null ? null : quantile(p95) }), p99: p99 == null ? null : quantile(p99) });
  assert.equal(percentileThreshold(cohort(400, 820, 1900), 'p95'), 820);
  assert.equal(percentileThreshold(cohort(400, 820, 1900), 'p99'), 1900);
  assert.equal(percentileThreshold(cohort(20, 820, null), 'p95'), 820);
  assert.equal(percentileThreshold(cohort(19, 820, null), 'p95'), null);
  assert.equal(percentileThreshold(cohort(99, 820, 1900), 'p99'), null);
  assert.equal(percentileThreshold(cohort(100, 820, 1900), 'p99'), 1900);
  assert.equal(percentileThreshold(cohort(400, 820, null), 'p99'), null);
  assert.equal(percentileThreshold(cohort(400, 0, null), 'p95'), null);
  assert.equal(percentileThreshold(cohort(400, null, null), 'p95'), null);
  assert.equal(percentileThreshold(null, 'p95'), null);
  assert.equal(percentileThreshold(undefined, 'p99'), null);
  assert.match(percentileLabel('p95', 820), /≥ P95/);
  assert.match(percentileLabel('p99', 1900), /histogram estimate/);
});

test('quantileText shows the sample-floor note for a missing P99 only', () => {
  const { quantileText } = loadTypeScript('src/lib/slowness.ts');
  assert.equal(quantileText({ lowerMs: 1, upperMs: 2, valueMs: 12.34 }), '≈ 12.3 ms');
  assert.equal(quantileText(null), 'N/A');
  assert.equal(quantileText(undefined), 'N/A');
  assert.equal(quantileText(null, 'needs ≥ 100 samples'), 'needs ≥ 100 samples');
});

test('slowest requests send the minimum and cursor, and the UI offers load more', () => {
  const api = fs.readFileSync(path.join(root, 'src/lib/api.ts'), 'utf8');
  assert.match(api, /slowest:[^\n]*minMs/);
  assert.match(api, /slowest:[^\n]*cursor/);
  const component = fs.readFileSync(path.join(root, 'src/components/TrafficSlowness.svelte'), 'utf8');
  assert.match(component, /Load more/);
  assert.match(component, /nextCursor/);
  assert.match(component, /histogram estimate/);
  const table = fs.readFileSync(path.join(root, 'src/components/ComparisonTable.svelte'), 'utf8');
  for (const heading of ['>P50<', '>P90<', '>P99<']) assert.ok(table.includes(heading), heading);
  assert.match(table, /quantileText\(row\.current\.p99, p99NeedsSamples\)/);
  assert.equal(loadTypeScript('src/lib/slowness.ts').p99NeedsSamples, 'needs ≥ 100 samples');
});
test('every quality count is a button that drills into the requests behind it', () => {
  const app = fs.readFileSync(path.join(root, 'src/App.svelte'), 'utf8');
  for (const state of ['incomplete', 'invalid', 'unclassified', 'pending']) {
    assert.match(app, new RegExp(`drillQuality\\('${state}'\\)`));
  }
  assert.match(app, /requestController\.signal, requestState, drillOverview\?\.order \?\? ''\)/);
  const table = fs.readFileSync(path.join(root, 'src/components/RequestTable.svelte'), 'utf8');
  assert.match(table, /row\.id/);
  assert.match(table, /stateLabel\(row\.state\)/);
});

test('stateLabel names every row state and drill-down filter', () => {
  assert.equal(presentation.stateLabel('incomplete'), 'Missing timing');
  assert.equal(presentation.stateLabel('invalid'), 'Unusable timing');
  assert.equal(presentation.stateLabel('pending'), 'Pending async');
  assert.equal(presentation.stateLabel('unclassified'), 'Unclassified result');
  assert.equal(presentation.stateLabel('valid'), 'Complete');
  assert.equal(presentation.stateLabel(undefined), '');
});

const recordFilterBase = { sourceIds: ['s'], start: '2026-10-22T12:00:00.000Z', end: '2026-10-22T12:15:00.000Z', baselineStart: '2026-10-22T11:45:00.000Z', baselineEnd: '2026-10-22T12:00:00.000Z', kind: 'all', outcome: 'all', metric: 'total', bucketSeconds: 10, controller: '', action: '' };

test('serializeRecordFilters omits empties', () => {
  assert.deepEqual(investigation.serializeRecordFilters({}), {});
  assert.deepEqual(investigation.serializeRecordFilters(null), {});
  assert.deepEqual(investigation.serializeRecordFilters({
    userIds: [], uiVersions: [' ', ''], userAgentContains: '   ', userIps: [], urlContains: '', auditId: '', statusCodes: [], completion: 'all', operation: ' '
  }), {});
  assert.deepEqual(investigation.serializeRecordFilters({
    userIds: [7, 7, 9], uiVersions: [' 2.1 ', '2.1'], userAgentContains: ' Firefox ', userIps: ['203.0.113.7'], urlContains: '/pay', auditId: '0123456789abcdef01234567',
    statusCodes: ['404', ''], completion: 'incomplete', operation: 'Payroll.Run'
  }), {
    userIds: [7, 9], uiVersions: ['2.1'], userAgentContains: 'Firefox', userIps: ['203.0.113.7'], urlContains: '/pay', auditId: '0123456789abcdef01234567',
    statusCodes: ['404'], completion: 'incomplete', operation: 'Payroll.Run'
  });
  assert.equal('userIds' in investigation.serializeRecordFilters({ userIds: [] }), false);
  // Wrong types are dropped rather than trusted.
  assert.deepEqual(investigation.serializeRecordFilters({ userIds: ['7', 1.5, NaN], uiVersions: 'x', completion: 'everything', operation: 5 }), {});
  assert.equal(investigation.serializeRecordFilters({ userIds: Array.from({ length: 80 }, (_, i) => i) }).userIds.length, 50);
  assert.equal(investigation.serializeRecordFilters({ statusCodes: Array.from({ length: 30 }, (_, i) => String(400 + i)) }).statusCodes.length, 20);
});

test('operation and user id validity match the server rules', () => {
  const { isValidOperation, isValidUserId, serializeRecordFilters } = investigation;
  for (const good of ['Payroll.Run', 'Payroll.Run.Now', ' Payroll.Run ']) assert.equal(isValidOperation(good), true, good);
  for (const bad of ['', 'PayrollRun', 'Payroll.', '.Run', '.', '  ']) assert.equal(isValidOperation(bad), false, bad);
  assert.equal(isValidUserId(1), true);
  assert.equal(isValidUserId(2147483647), true);
  for (const bad of [0, -1, 2147483648, 1.5, NaN]) assert.equal(isValidUserId(bad), false, String(bad));
  // An operation the server would reject never reaches a request.
  assert.deepEqual(serializeRecordFilters({ operation: 'PayrollRun', userIds: [0, -3, 2147483648, 5] }), { userIds: [5] });
});

test('filter drafts resync from a restored query without clobbering partial typing', () => {
  const { syncListDraft, syncOperationDraft, isValidUserId } = investigation;
  // A restored query fills a blank draft.
  assert.equal(syncOperationDraft('', 'Payroll.Run'), 'Payroll.Run');
  assert.equal(syncListDraft('', ['2.1', '2.2']), '2.1, 2.2');
  assert.equal(syncListDraft('', [7, 9]), '7, 9');
  // A cleared query empties a draft that was applied, and a stale one is replaced.
  assert.equal(syncOperationDraft('Payroll.Run', undefined), '');
  assert.equal(syncListDraft('7, 9', undefined), '');
  assert.equal(syncListDraft('7', [8]), '8');
  assert.equal(syncOperationDraft('Old.Op', 'Payroll.Run'), 'Payroll.Run');
  // What the user is typing stays as typed while it parses to the applied value.
  assert.equal(syncOperationDraft(' Payroll.Run ', 'Payroll.Run'), ' Payroll.Run ');
  assert.equal(syncOperationDraft('Payroll', undefined), 'Payroll');
  assert.equal(syncOperationDraft('Payroll.', undefined), 'Payroll.');
  assert.equal(syncListDraft('2.1, ', ['2.1']), '2.1, ');
  assert.equal(syncListDraft('2.1,2.1', ['2.1']), '2.1,2.1');
  assert.equal(syncListDraft('', undefined), '');
  const ok = (item) => /^\d{1,10}$/.test(item) && isValidUserId(Number(item));
  assert.equal(syncListDraft('007, abc, ', [7], ok), '007, abc, ');
  assert.equal(syncListDraft('7, 07', [7], ok), '7, 07');
});

test('QueryControls resyncs its drafts from the query', () => {
  const source = fs.readFileSync(path.join(root, 'src/components/QueryControls.svelte'), 'utf8');
  assert.match(source, /syncOperationDraft\(/);
  assert.equal((source.match(/syncListDraft\(/g) ?? []).length >= 4, true);
  assert.match(source, /\$effect\(/);
});

test('record filters survive a saved investigation and an old snapshot without them still loads', () => {
  const filters = { userIds: [7], completion: 'pending', operation: 'Payroll.Run', urlContains: '/pay' };
  const snapshot = investigation.createInvestigationSnapshot({ ...recordFilterBase, ...filters, uiVersions: [], auditId: '' }, [], 'UTC', null);
  assert.deepEqual(Object.keys(snapshot.query).filter(key => !(key in recordFilterBase)), ['userIds', 'urlContains', 'completion', 'operation']);
  const parsed = investigation.parseInvestigationSnapshot(JSON.stringify(snapshot));
  assert.deepEqual(parsed.query.userIds, [7]);
  assert.deepEqual(investigation.restoreInvestigation(parsed, []).operation, 'Payroll.Run');
  const { sourceIds, ...legacyQuery } = recordFilterBase;
  const legacy = investigation.parseInvestigationSnapshot(JSON.stringify({ version: 1, query: legacyQuery, selectedSources: [], zone: 'UTC', targetMs: null }));
  assert.ok(legacy);
  assert.deepEqual(Object.keys(investigation.restoreInvestigation(legacy, [])).sort(), [...Object.keys(recordFilterBase), 'targetMs', 'zone'].sort());
  const tampered = investigation.parseInvestigationSnapshot(JSON.stringify({ version: 1, query: { ...legacyQuery, userIds: 'oops', completion: 'x' }, selectedSources: [], zone: 'UTC', targetMs: null }));
  assert.ok(tampered);
  assert.equal('userIds' in tampered.query || 'completion' in tampered.query, false);
});

test('query fingerprint covers record filters and treats empty as absent', () => {
  const plain = presentation.queryFingerprint(recordFilterBase);
  assert.equal(presentation.queryFingerprint({ ...recordFilterBase, userIds: [], uiVersions: [], userAgentContains: '', userIps: [], urlContains: ' ', auditId: '', statusCodes: [], completion: 'all', operation: '' }), plain);
  for (const filter of [{ userIds: [1] }, { uiVersions: ['2'] }, { userAgentContains: 'a' }, { userIps: ['1.1.1.1'] }, { urlContains: 'u' }, { auditId: 'a'.repeat(24) }, { statusCodes: ['500'] }, { completion: 'invalid' }, { operation: 'A.b' }]) {
    assert.notEqual(presentation.queryFingerprint({ ...recordFilterBase, ...filter }), plain, JSON.stringify(filter));
  }
  assert.equal(presentation.queryFingerprint({ ...recordFilterBase, userIds: [2, 1] }), presentation.queryFingerprint({ ...recordFilterBase, userIds: [1, 2] }));
});
test('quickRange 24h yields previous 24h baseline and respects 7d cap', () => {
  const now = new Date('2026-10-22T12:00:37.123Z');
  const day = time.quickRange('24h', now);
  assert.equal(day.end, '2026-10-22T12:00:37.123Z');
  assert.equal(day.start, '2026-10-21T12:00:37.123Z');
  assert.equal(day.baselineEnd, day.start, 'the baseline ends where the investigated window starts');
  assert.equal(day.baselineStart, '2026-10-20T12:00:37.123Z');
  assert.equal(Date.parse(day.end) - Date.parse(day.start), Date.parse(day.baselineEnd) - Date.parse(day.baselineStart), 'equal-length windows');
  const week = time.quickRange('7d', now);
  assert.equal(Date.parse(week.end) - Date.parse(week.start), 7 * 86_400_000, 'a 7 day window is the longest allowed');
  assert.equal(Date.parse(week.baselineEnd) - Date.parse(week.baselineStart), 7 * 86_400_000);
  assert.deepEqual(time.quickRanges.map(range => range.id), ['15m', '1h', '4h', '24h', '7d']);
  for (const { id } of time.quickRanges) {
    const range = time.quickRange(id, now);
    const span = Date.parse(range.end) - Date.parse(range.start);
    assert.ok(span <= 7 * 86_400_000, `${id} stays inside the 7 day cap`);
    assert.ok(range.bucketSeconds >= 10 && range.bucketSeconds <= 86_400);
    assert.ok(charts.countEpochBuckets(range.start, range.end, range.bucketSeconds) <= 120, `${id} current window fits 120 buckets`);
    assert.ok(charts.countEpochBuckets(range.baselineStart, range.baselineEnd, range.bucketSeconds) <= 120, `${id} baseline window fits 120 buckets`);
  }
  assert.throws(() => time.quickRange('30d', now), /Unknown quick range/);
});

test('overview delta text shows relative traffic, percentage points and milliseconds, and a dash for nil', () => {
  const overview = loadTypeScript('src/lib/overview.ts');
  assert.equal(overview.changeText(0.25), '+25.0%');
  assert.equal(overview.changeText(-0.5), '-50.0%');
  assert.equal(overview.changeText(0), '0.0%');
  assert.equal(overview.changeText(null), '—');
  assert.equal(overview.changeText(undefined), '—');
  assert.equal(overview.changeText(Number.NaN), '—');
  assert.equal(overview.pointsText(0.012), '+1.2 pp');
  assert.equal(overview.pointsText(-0.034), '-3.4 pp');
  assert.equal(overview.pointsText(null), '—');
  assert.equal(overview.msChangeText(120.4), '+120 ms');
  assert.equal(overview.msChangeText(-3.46), '-3.5 ms');
  assert.equal(overview.msChangeText(1500), '+1.5 s');
  assert.equal(overview.msChangeText(undefined), '—');
  assert.equal(overview.rateText(0.0734), '7.3%');
  assert.equal(overview.rateText(null), '—');
});

test('overview tiles drill into the existing state, kind and outcome filters; unknown maps to http_unknown', () => {
  const { overviewDrill } = loadTypeScript('src/lib/overview.ts');
  assert.deepEqual({ ...overviewDrill('synchronous', 'events') }, { kind: 'synchronous', state: 'all', outcome: '', label: 'synchronous · every event', note: '' });
  assert.deepEqual({ ...overviewDrill('hangfire', 'completed') }, { kind: 'hangfire', state: 'complete', outcome: '', label: 'hangfire · Complete', note: '' });
  assert.deepEqual({ ...overviewDrill('asynchronous', 'clientErrors') }, { kind: 'asynchronous', state: 'all', outcome: 'client_error', label: 'asynchronous · client errors (4xx)', note: '' });
  assert.deepEqual({ ...overviewDrill('asynchronous', 'serverErrors') }, { kind: 'asynchronous', state: 'all', outcome: 'server_error', label: 'asynchronous · server errors (5xx)', note: '' });
  assert.equal(overviewDrill('unknown', 'events').kind, 'http_unknown');
  assert.match(overviewDrill('unknown', 'events').label, /polling type unknown/);
});

test('overview drills say how the opened list differs from the tile', () => {
  const { overviewDrill, drillHeading } = loadTypeScript('src/lib/overview.ts');
  const notes = (kind, tile, context) => overviewDrill(kind, tile, context).note;
  // Events: Synchronous and Asynchronous lists also hold the unknown type's incomplete and invalid rows, exactly.
  assert.equal(notes('synchronous', 'events', { unknownKindRows: 3, unknownResults: 2 }), 'also lists 3 incomplete or unusable rows of unknown request type');
  assert.equal(notes('asynchronous', 'events', { unknownKindRows: 1 }), 'also lists 1 incomplete or unusable row of unknown request type');
  assert.equal(notes('synchronous', 'events', { unknownKindRows: 0 }), '');
  assert.equal(notes('synchronous', 'events'), '');
  assert.equal(notes('hangfire', 'events', { unknownKindRows: 3, unknownResults: 2 }), '');
  assert.equal(notes('unknown', 'events', { unknownKindRows: 3, unknownResults: 2 }), '');
  assert.equal(notes('synchronous', 'completed', { unknownKindRows: 3, unknownResults: 2 }), '');
  // 4xx and 5xx lists also keep every row of unknown result, so the bound is "up to".
  assert.equal(notes('synchronous', 'clientErrors', { unknownKindRows: 3, unknownResults: 2 }), 'also lists up to 5 rows with an unknown result or request type');
  assert.equal(notes('asynchronous', 'serverErrors', { unknownKindRows: 3 }), 'also lists up to 3 incomplete or unusable rows of unknown request type');
  assert.equal(notes('hangfire', 'serverErrors', { unknownKindRows: 3, unknownResults: 2 }), 'also lists up to 2 rows whose result is unknown');
  assert.equal(notes('unknown', 'clientErrors', { unknownKindRows: 3, unknownResults: 1 }), 'also lists up to 1 row whose result is unknown');
  assert.equal(notes('hangfire', 'clientErrors', {}), '');
  // The tile drill replaces a narrower outcome filter and says so; Events and Completed keep the user's filter.
  assert.equal(notes('hangfire', 'serverErrors', { outcomeFilter: 'failure' }), 'replaces the failure outcome filter, so counts may differ');
  assert.equal(notes('hangfire', 'clientErrors', { outcomeFilter: 'server_error' }), 'replaces the server error outcome filter, so counts may differ');
  assert.equal(notes('hangfire', 'serverErrors', { outcomeFilter: 'server_error' }), '');
  assert.equal(notes('hangfire', 'serverErrors', { outcomeFilter: 'all' }), '');
  assert.equal(notes('hangfire', 'events', { outcomeFilter: 'success' }), '');
  assert.equal(notes('synchronous', 'clientErrors', { unknownKindRows: 3, outcomeFilter: 'failure' }), 'also lists up to 3 incomplete or unusable rows of unknown request type; replaces the failure outcome filter, so counts may differ');
  // The heading shows the label and the note together.
  assert.equal(drillHeading({ label: 'synchronous · every event', note: '' }), 'synchronous · every event');
  assert.equal(drillHeading({ label: 'synchronous · every event', note: 'also lists 3 incomplete or unusable rows of unknown request type' }), 'synchronous · every event (also lists 3 incomplete or unusable rows of unknown request type)');
});

test('operation rows drill into their controller, action and request type', () => {
  const { operationDrill } = loadTypeScript('src/lib/overview.ts');
  assert.deepEqual({ ...operationDrill({ controller: 'Payroll', action: 'Run', kind: 'synchronous' }) }, {
    kind: 'synchronous', state: 'all', outcome: '', label: 'Payroll.Run · synchronous · every event', note: 'also lists any incomplete or unusable rows of unknown request type', controller: 'Payroll', action: 'Run'
  });
  assert.equal(operationDrill({ controller: 'Payroll', action: 'Run', kind: 'asynchronous' }).note, 'also lists any incomplete or unusable rows of unknown request type');
  const hangfire = operationDrill({ controller: 'Jobs', action: 'Nightly', kind: 'hangfire' });
  assert.deepEqual({ kind: hangfire.kind, note: hangfire.note, controller: hangfire.controller, action: hangfire.action }, { kind: 'hangfire', note: '', controller: 'Jobs', action: 'Nightly' });
  const unknown = operationDrill({ controller: 'Payroll', action: 'Run', kind: 'unknown' });
  assert.equal(unknown.kind, 'http_unknown');
  assert.match(unknown.label, /polling type unknown/);
  assert.equal(operationDrill({ controller: 'Payroll', action: 'Run' }).kind, 'all');
});

test('unknown-type tiles and operations say what a Synchronous or Asynchronous query leaves out', () => {
  const { overviewDrill, operationDrill, drillHeading } = loadTypeScript('src/lib/overview.ts');
  const gapNote = (kind) => 'also lists any rows of unknown request type that the ' + kind + ' filter leaves out of this count';
  // The unknown row's counts leave out valid unknown-type rows under these queries, but its http_unknown list includes them: no number, every tile.
  for (const queryKind of ['synchronous', 'asynchronous']) {
    for (const tile of ['events', 'completed', 'clientErrors', 'serverErrors']) {
      const drill = overviewDrill('unknown', tile, { queryKind, unknownKindRows: 3, unknownResults: 2 });
      assert.equal(drill.kind, 'http_unknown');
      assert.equal(drill.note, gapNote(queryKind), queryKind + ' ' + tile);
    }
  }
  assert.equal(overviewDrill('unknown', 'serverErrors', { queryKind: 'synchronous', outcomeFilter: 'success' }).note, gapNote('synchronous') + '; replaces the success outcome filter, so counts may differ');
  assert.equal(drillHeading(overviewDrill('unknown', 'events', { queryKind: 'asynchronous' })), 'HTTP (polling type unknown) · every event (' + gapNote('asynchronous') + ')');
  // Any other query keeps the unknown row exact, and no other row takes this note.
  for (const queryKind of [undefined, 'all', 'http', 'http_unknown', 'hangfire']) {
    assert.equal(overviewDrill('unknown', 'events', { queryKind }).note, '');
    assert.equal(overviewDrill('unknown', 'completed', { queryKind }).note, '');
  }
  assert.equal(overviewDrill('unknown', 'clientErrors', { queryKind: 'all', unknownResults: 2 }).note, 'also lists up to 2 rows whose result is unknown');
  for (const kind of ['synchronous', 'asynchronous', 'hangfire']) assert.doesNotMatch(overviewDrill(kind, 'completed', { queryKind: 'synchronous' }).note, /leaves out/);
  assert.equal(overviewDrill('hangfire', 'events', { queryKind: 'synchronous' }).note, '');
  // Operation rows of unknown type say the same, and only under those queries.
  assert.equal(operationDrill({ controller: 'Payroll', action: 'Run', kind: 'unknown' }, 'synchronous').note, gapNote('synchronous'));
  assert.equal(operationDrill({ controller: 'Payroll', action: 'Run', kind: 'unknown' }, 'asynchronous').note, gapNote('asynchronous'));
  for (const queryKind of [undefined, 'all', 'http', 'http_unknown', 'hangfire']) assert.equal(operationDrill({ controller: 'Payroll', action: 'Run', kind: 'unknown' }, queryKind).note, '');
  assert.equal(operationDrill({ controller: 'Jobs', action: 'Nightly', kind: 'hangfire' }, 'synchronous').note, '');
});

test('a zero-count tile still opens when its list can hold rows the count leaves out', () => {
  const { tileCanOpen, overviewDrill } = loadTypeScript('src/lib/overview.ts');
  // The unknown row under a Synchronous or Asynchronous query counts no valid unknown-type rows, so its Completed (and any zero) tile is 0
  // although the list it opens is not empty: the tile stays enabled and its note is shown.
  for (const queryKind of ['synchronous', 'asynchronous']) {
    for (const tile of ['events', 'completed', 'clientErrors', 'serverErrors']) {
      const context = { queryKind };
      assert.equal(tileCanOpen('unknown', tile, 0, context), true, queryKind + ' ' + tile);
      assert.notEqual(overviewDrill('unknown', tile, context).note, '');
    }
  }
  // With no gap a zero count stays disabled: other queries, other rows, and a note that only concerns the outcome filter.
  for (const queryKind of [undefined, 'all', 'http', 'http_unknown', 'hangfire']) {
    for (const tile of ['events', 'completed', 'clientErrors', 'serverErrors']) assert.equal(tileCanOpen('unknown', tile, 0, { queryKind }), false, String(queryKind) + ' ' + tile);
  }
  for (const kind of ['synchronous', 'asynchronous', 'hangfire']) {
    for (const tile of ['events', 'completed', 'clientErrors', 'serverErrors']) {
      assert.equal(tileCanOpen(kind, tile, 0, { queryKind: 'synchronous' }), false, kind + ' ' + tile);
      assert.equal(tileCanOpen(kind, tile, 0), false, kind + ' ' + tile);
    }
  }
  assert.equal(tileCanOpen('hangfire', 'serverErrors', 0, { outcomeFilter: 'success' }), false);
  // A count above zero always opens; so does a zero count whose list still holds unusable unknown-type rows or unknown results.
  assert.equal(tileCanOpen('hangfire', 'events', 4, {}), true);
  assert.equal(tileCanOpen('synchronous', 'events', 0, { unknownKindRows: 3 }), true);
  assert.equal(tileCanOpen('synchronous', 'completed', 0, { unknownKindRows: 3 }), false);
  assert.equal(tileCanOpen('hangfire', 'clientErrors', 0, { unknownResults: 2 }), true);
});

test('operation rows sort by a delta with missing deltas always last', () => {
  const { sortOperations } = loadTypeScript('src/lib/overview.ts');
  const rollup = (action, trafficDelta, p95DeltaMs, events = 10) => ({ controller: 'C', action, kind: 'synchronous', requests: events, failures: 0, perMinute: 1, events, trafficDelta, p95DeltaMs });
  const rows = [rollup('a', 0.1, undefined), rollup('b', -0.9, 30), rollup('c', undefined, 5), rollup('d', 0.5, -4, 99)];
  assert.deepEqual(sortOperations(rows, 'trafficDelta', -1).map(r => r.action), ['d', 'a', 'b', 'c']);
  assert.deepEqual(sortOperations(rows, 'trafficDelta', 1).map(r => r.action), ['b', 'a', 'd', 'c']);
  assert.deepEqual(sortOperations(rows, 'p95DeltaMs', -1).map(r => r.action), ['b', 'c', 'd', 'a']);
  assert.deepEqual(sortOperations(rows, 'events', -1).map(r => r.action)[0], 'd');
  assert.deepEqual(rows.map(r => r.action), ['a', 'b', 'c', 'd']);
});

test('the overview strip, quick range presets and operation view are wired into the UI', () => {
  const read = file => fs.readFileSync(path.join(root, file), 'utf8');
  const strip = read('src/components/OverviewStrip.svelte');
  assert.match(strip, /overviewDrill/);
  assert.match(strip, /unknownKindRows/);
  assert.match(strip, /queryKind: kindFilter/);
  assert.match(strip, /disabled=\{!canOpen\(row, tile, count\)\}/);
  assert.match(strip, /tileCanOpen\(/);
  assert.match(strip, /outcomeFilter/);
  assert.match(strip, /\{@render quality\(\)\}/);
  const app = read('src/App.svelte');
  assert.match(app, /<OverviewStrip/);
  assert.match(app, /drillOverview/);
  assert.match(app, /drillHeading\(drillOverview\)/);
  assert.match(app, /<OverviewStrip[^\n]*kindFilter=\{analysis\.query\.kind\}/);
  assert.match(app, /copy\.controller = drillOverview\.controller/);
  assert.match(app, /copy\.action = drillOverview\.action/);
  assert.match(app, /<ComparisonTable[\s\S]*?onDrill=\{drillFromOverview\}/);
  assert.match(app, /drillQuality\('incomplete'\)/);
  assert.match(read('src/components/QueryControls.svelte'), /quickRanges/);
  const table = read('src/components/ComparisonTable.svelte');
  assert.match(table, /<OperationTable[^>]*\{onDrill\}[^>]*\{kindFilter\}/);
  const operations = read('src/components/OperationTable.svelte');
  assert.match(operations, /operationDrill\(row, kindFilter\)/);
  assert.match(operations, /<button[^>]*class="api-select"[^>]*onclick=\{\(\) => onDrill\?\.\(operationDrill\(row, kindFilter\)\)\}/);
  assert.doesNotMatch(operations, /Every stored event/);
  assert.match(table, />Operation</);
  assert.match(read('src/components/OperationTable.svelte'), /Δ traffic[\s\S]*Δ P95[\s\S]*Δ 4xx[\s\S]*Δ 5xx[\s\S]*Δ completion/);
});

test('range filters serialize only usable bounds in a fixed order', () => {
  const { serializeRecordFilters } = investigation;
  assert.deepEqual(serializeRecordFilters({ observedMs: {}, apiTimeMs: { min: undefined }, requestKb: null, responseKb: 'x', incompleteAgeMs: [] }), {});
  assert.deepEqual(serializeRecordFilters({ responseKb: { max: 9 }, observedMs: { max: 500, min: 100 }, incompleteAgeMs: { min: 0 } }), { observedMs: { min: 100, max: 500 }, responseKb: { max: 9 }, incompleteAgeMs: { min: 0 } });
  assert.deepEqual(Object.keys(serializeRecordFilters({ incompleteAgeMs: { min: 1 }, requestKb: { min: 1 }, auditCreateTimeMs: { min: 1 }, apiTimeMs: { min: 1 }, observedMs: { min: 1 }, responseKb: { min: 1 } })), ['observedMs', 'apiTimeMs', 'auditCreateTimeMs', 'requestKb', 'responseKb', 'incompleteAgeMs']);
  // A range the server would reject, or a value of the wrong type, never reaches a request.
  assert.deepEqual(serializeRecordFilters({ observedMs: { min: 9, max: 1 }, apiTimeMs: { min: -1 }, requestKb: { min: NaN, max: Infinity }, responseKb: { min: '5' }, auditCreateTimeMs: { max: 1_000_000_001 } }), {});
  assert.deepEqual(serializeRecordFilters({ apiTimeMs: { min: -1, max: 4 } }), { apiTimeMs: { max: 4 } });
  assert.deepEqual(serializeRecordFilters({ requestKb: { max: 10_000_000 } }), { requestKb: { max: 10_000_000 } });
  assert.deepEqual(serializeRecordFilters({ requestKb: { max: 10_000_001 } }), {});
});

test('range drafts build, explain what is wrong and resync from a restored query', () => {
  const { buildRange, rangeBoundProblem, syncRangeDraft, MAX_RANGE_MS } = investigation;
  assert.deepEqual(buildRange('', '', MAX_RANGE_MS), { range: undefined, problems: [] });
  assert.deepEqual(buildRange(' 100 ', '2.5', MAX_RANGE_MS), { range: undefined, problems: ['The minimum is above the maximum, so neither is applied'] });
  assert.deepEqual(buildRange('1.5', '', MAX_RANGE_MS), { range: { min: 1.5 }, problems: [] });
  assert.deepEqual(buildRange('', '0', MAX_RANGE_MS), { range: { max: 0 }, problems: [] });
  assert.deepEqual(buildRange('5', '5', MAX_RANGE_MS).range, { min: 5, max: 5 });
  // A bad bound is not applied but the other one still is.
  const negative = buildRange('-3', '8', MAX_RANGE_MS);
  assert.deepEqual(negative.range, { max: 8 });
  assert.match(negative.problems[0], /negative/);
  assert.match(buildRange('abc', '', MAX_RANGE_MS).problems[0], /not a number/);
  assert.match(buildRange('0x10', '', MAX_RANGE_MS).problems[0], /not a number/);
  assert.match(buildRange('1e3', '', MAX_RANGE_MS).problems[0], /not a number/);
  assert.match(buildRange('', '2000000000', MAX_RANGE_MS).problems[0], /above the limit/);
  assert.equal(buildRange('', '2000000000', MAX_RANGE_MS).range, undefined);
  assert.equal(rangeBoundProblem('1000000000', MAX_RANGE_MS), '');
  assert.equal(rangeBoundProblem('  ', MAX_RANGE_MS), '');
  assert.equal(rangeBoundProblem('.5', MAX_RANGE_MS), '');
  // The drafts follow the applied range, and partial typing survives.
  const same = { min: '5.', max: '' };
  assert.equal(syncRangeDraft(same, { min: 5 }, MAX_RANGE_MS), same);
  assert.deepEqual(syncRangeDraft({ min: '', max: '' }, { min: 100, max: 500 }, MAX_RANGE_MS), { min: '100', max: '500' });
  assert.deepEqual(syncRangeDraft({ min: '9', max: '1' }, undefined, MAX_RANGE_MS), { min: '9', max: '1' });
  assert.deepEqual(syncRangeDraft({ min: '100', max: '500' }, undefined, MAX_RANGE_MS), { min: '', max: '' });
  assert.deepEqual(syncRangeDraft({ min: '100', max: '' }, { max: 7 }, MAX_RANGE_MS), { min: '', max: '7' });
});

test('range conflicts name the completion or range the server would refuse', () => {
  const { rangeConflict } = investigation;
  assert.equal(rangeConflict({}), '');
  assert.equal(rangeConflict({ observedMs: { min: 1 }, completion: 'complete' }), '');
  assert.equal(rangeConflict({ observedMs: { min: 1 }, completion: 'all' }), '');
  assert.equal(rangeConflict({ requestKb: { min: 1 }, completion: 'pending' }), '');
  assert.equal(rangeConflict({ incompleteAgeMs: { min: 1 }, completion: 'pending' }), '');
  assert.equal(rangeConflict({ incompleteAgeMs: { min: 1 }, completion: 'incomplete' }), '');
  for (const key of ['observedMs', 'apiTimeMs', 'auditCreateTimeMs']) {
    for (const completion of ['incomplete', 'pending', 'invalid']) assert.match(rangeConflict({ [key]: { max: 5 }, completion }), /only describes complete/, `${key} ${completion}`);
  }
  for (const completion of ['complete', 'invalid']) assert.match(rangeConflict({ incompleteAgeMs: { max: 5 }, completion }), /only describes incomplete/, completion);
  assert.match(rangeConflict({ observedMs: { min: 1 }, incompleteAgeMs: { min: 1 } }), /cannot be combined/);
  // An empty range does not count as in use.
  assert.equal(rangeConflict({ observedMs: {}, incompleteAgeMs: { min: 1 } }), '');
});

test('ranges survive a saved investigation and an old snapshot without them still loads', () => {
  const ranges = { observedMs: { min: 250 }, responseKb: { min: 1, max: 64 }, incompleteAgeMs: undefined, requestKb: { min: 5, max: 1 } };
  const snapshot = investigation.createInvestigationSnapshot({ ...recordFilterBase, ...ranges }, [], 'UTC', null);
  assert.deepEqual(Object.keys(snapshot.query).filter(key => !(key in recordFilterBase)), ['observedMs', 'responseKb']);
  const parsed = investigation.parseInvestigationSnapshot(JSON.stringify(snapshot));
  assert.deepEqual(parsed.query.observedMs, { min: 250 });
  assert.deepEqual(investigation.restoreInvestigation(parsed, []).responseKb, { min: 1, max: 64 });
  assert.equal('requestKb' in investigation.restoreInvestigation(parsed, []), false);
  const { sourceIds, ...legacyQuery } = recordFilterBase;
  const tampered = investigation.parseInvestigationSnapshot(JSON.stringify({ version: 1, query: { ...legacyQuery, observedMs: 'fast', apiTimeMs: { min: -4 } }, selectedSources: [], zone: 'UTC', targetMs: null }));
  assert.ok(tampered);
  assert.equal('observedMs' in tampered.query || 'apiTimeMs' in tampered.query, false);
});

test('query fingerprint covers every range and treats an empty range as absent', () => {
  const plain = presentation.queryFingerprint(recordFilterBase);
  assert.equal(presentation.queryFingerprint({ ...recordFilterBase, observedMs: {}, apiTimeMs: undefined, requestKb: { min: undefined } }), plain);
  const keys = ['observedMs', 'apiTimeMs', 'auditCreateTimeMs', 'requestKb', 'responseKb', 'incompleteAgeMs'];
  const seen = new Set([plain]);
  for (const key of keys) {
    for (const range of [{ min: 1 }, { max: 1 }, { min: 1, max: 2 }]) seen.add(presentation.queryFingerprint({ ...recordFilterBase, [key]: range }));
  }
  assert.equal(seen.size, 1 + keys.length * 3);
});

test('QueryControls offers the ranges inside the record filters panel only', () => {
  const source = fs.readFileSync(path.join(root, 'src/components/QueryControls.svelte'), 'utf8');
  const panelStart = source.indexOf('record-filters"');
  const panel = source.slice(panelStart, source.indexOf('</details>', panelStart));
  assert.match(panel, /RANGE_FIELDS/);
  assert.match(panel, /rangeNote/);
  const earlier = source.slice(source.indexOf('advanced-filters">'), panelStart);
  assert.equal(/RANGE_FIELDS|rf-observedMs/.test(earlier), false);
  assert.match(source, /syncRangeDraft\(/);
  assert.equal(/#[0-9a-fA-F]{3,8}\b/.test(source.slice(source.indexOf('<style>'))), false);
});

test('breakdown drill-down keeps and clears range filters like any other record filter', () => {
  const base = { observedMs: { min: 500 }, incompleteAgeMs: { max: 60000 } };
  assert.deepEqual(breakdown.activeRecordFilter(breakdown.mergeRecordFilter(base, { userIds: [7] })), { userIds: [7], observedMs: { min: 500 }, incompleteAgeMs: { max: 60000 } });
  assert.deepEqual(breakdown.activeRecordFilter(breakdown.clearRecordFilter(base)), {});
  assert.deepEqual(breakdown.activeRecordFilter(breakdown.applyDrill({ observedMs: { min: 1 } }, { apiTimeMs: { min: 9 } })), { apiTimeMs: { min: 9 } });
});

// Task 10: failure investigation. A slot with no point is null, never a zero:
// a gap means no observation, while a real zero means the slot had none.
function failurePoint(at, extra = {}) {
  return { at, count: 5, p50: null, p95: null, p95Ratio: null, ...extra };
}
test('failureSeries returns null for missing slots, never zero', () => {
  const start = '2026-10-22T12:00:00.000Z';
  const end = '2026-10-22T12:30:00.000Z';
  const result = analysis({
    start, end, bucketSeconds: 600,
    timeline: [
      { key: 'a', points: [failurePoint('2026-10-22T12:00:00.000Z', { count5xx: 3, count4xx: 1 }), failurePoint('2026-10-22T12:20:00.000Z')] },
      { key: 'b', points: [failurePoint('2026-10-22T12:00:00.000Z', { count5xx: 2 })] }
    ]
  });
  // One cohort: the middle slot has no point (null), the last has a point with no failures (0).
  assert.deepEqual(charts.failureSeries(result, 'a', 'count5xx'), [3, null, 0]);
  assert.deepEqual(charts.failureSeries(result, 'a', 'count4xx'), [1, null, 0]);
  // All cohorts: slots sum across the cohorts that have a point and stay null where none does.
  assert.deepEqual(charts.failureSeries(result, null, 'count5xx'), [5, null, 0]);
  // A cohort with no timeline series at all is entirely null.
  assert.deepEqual(charts.failureSeries(result, 'missing', 'count5xx'), [null, null, null]);
  // A point carrying only unfinished rows (count 0) is a real observation, so its failures are 0.
  const incompleteOnly = analysis({ start, end, bucketSeconds: 600, timeline: [{ key: 'a', points: [failurePoint('2026-10-22T12:10:00.000Z', { count: 0, incomplete: 2 })] }] });
  assert.deepEqual(charts.failureSeries(incompleteOnly, 'a', 'count5xx'), [null, 0, null]);
  assert.deepEqual(charts.failureSeries(incompleteOnly, 'a', 'incomplete'), [null, 2, null]);
  const trend = charts.failureTrendOption(result, 'UTC');
  assert.deepEqual(trend.series.map(item => item.name), ['4xx client errors', '5xx server errors and failed jobs']);
  assert.deepEqual(trend.series[1].data, [5, null, 0]);
  assert.equal(trend.series.every(item => item.connectNulls === false), true);
});

test('multi-timeline series toggle: percentiles change from their own reference, failure counts are absolute and null in gaps', () => {
  const start = '2026-10-22T12:00:00.000Z';
  const end = '2026-10-22T12:20:00.000Z';
  const q = (valueMs) => ({ lowerMs: valueMs - 1, upperMs: valueMs + 1, valueMs });
  const row = { ...comparison('a', 'A'), reference: { ...stats({ p50: q(100), p95: q(200) }), p90: q(150), p99: null } };
  const sample = failurePoint('2026-10-22T12:00:00.000Z', { p50: q(110), p90: q(180), p95: q(300), p99: q(400), count5xx: 4 });
  assert.equal(charts.baselineChange(row, sample, 'milliseconds'), 100);
  assert.equal(charts.baselineChange(row, sample, 'milliseconds', 'p50'), 10);
  assert.equal(charts.baselineChange(row, sample, 'milliseconds', 'p90'), 30);
  assert.equal(charts.baselineChange(row, sample, 'milliseconds', 'p99'), null);
  const result = analysis({ start, end, bucketSeconds: 600, apis: [row], timeline: [{ key: 'a', points: [sample] }] });
  const p90 = charts.multiTimelineOption(result, ['a'], new Map(), 'UTC', 'milliseconds', 'p90');
  assert.deepEqual(p90.series[0].data.map(item => item.value), [30, null]);
  const fives = charts.multiTimelineOption(result, ['a'], new Map(), 'UTC', 'milliseconds', 'count5xx');
  assert.deepEqual(fives.series[0].data.map(item => item.value), [4, null]);
  assert.equal(charts.multiTimelineOption(result, ['a'], new Map(), 'UTC', 'milliseconds').series[0].data[0].value, 100);
});

test('FailurePanel reuses BreakdownPanel on status codes and the colours stay out of the markup', () => {
  const panel = fs.readFileSync(path.join(root, 'src/components/FailurePanel.svelte'), 'utf8');
  assert.match(panel, /<BreakdownPanel[^>]*lockedDim="statusCode"/);
  assert.match(panel, /failureTrendOption/);
  assert.equal(/#[0-9a-fA-F]{3,8}\b/.test(panel), false);
  const app = fs.readFileSync(path.join(root, 'src/App.svelte'), 'utf8');
  assert.match(app, /<FailurePanel[^>]*onDrill=\{drillInto\}/);
});

const incomplete = loadTypeScript('src/lib/incomplete.ts');

test('completionRate is null when events is 0', () => {
  assert.equal(incomplete.completionRate({ events: 0, complete: 0, incomplete: 0, pending: 0 }), null);
  assert.equal(incomplete.completionRate({}), null);
  // Nothing but unusable rows: invalid is never in the denominator, so there is no rate to show (not 0%).
  assert.equal(incomplete.completionRate({ events: 4, complete: 0, incomplete: 0, pending: 0 }), null);
  // complete / (complete + incomplete + pending); the 4 invalid rows are only part of events.
  assert.equal(incomplete.completionRate({ events: 13, complete: 5, incomplete: 1, pending: 3 }), 5 / 9);
  assert.equal(incomplete.completionRate({ events: 3, complete: 3 }), 1);
  assert.equal(incomplete.completionRate({ events: 2, complete: 0, incomplete: 2 }), 0);
});

test('operations are ranked by completion rate, worst first, and a missing rate is left out', () => {
  const rollup = (action, completed, incompleteRows, pending, invalid, share) => ({ controller: 'Api', action, kind: 'synchronous', requests: 0, failures: 0, perMinute: 0, events: completed + incompleteRows + pending + invalid, completed, incomplete: incompleteRows, pending, invalid, ...(share === undefined ? {} : { completionShare: share }) });
  const ranked = incomplete.rankByCompletion([
    rollup('Healthy', 10, 0, 0, 0),
    rollup('Stuck', 1, 8, 1, 0, 0.1),
    rollup('Slow', 5, 5, 0, 0),
    rollup('AllBad', 0, 0, 0, 7),
    rollup('Tie', 5, 5, 0, 0)
  ]);
  assert.deepEqual(ranked.map(row => row.rollup.action), ['Stuck', 'Slow', 'Tie', 'Healthy']);
  assert.deepEqual(ranked.map(row => row.rate), [0.1, 0.5, 0.5, 1]);
  assert.deepEqual(ranked.map(row => row.unfinished), [9, 5, 5, 0]);
  // The server's share wins over a rate worked out from the counts.
  assert.equal(incomplete.rankByCompletion([rollup('Share', 5, 5, 0, 0, 0.25)])[0].rate, 0.25);
  assert.deepEqual(incomplete.rankByCompletion([]), []);
});

test('the stale threshold is a positive number of minutes with a code-derived default', () => {
  assert.equal(incomplete.DEFAULT_STALE_MINUTES, 10);
  assert.equal(incomplete.parseStaleMinutes('10'), 10);
  assert.equal(incomplete.parseStaleMinutes(' 2.5 '), 2.5);
  for (const bad of ['', '  ', '0', '-3', 'abc', '1e3', '5 min', 'NaN', 'Infinity', '99999999']) assert.equal(incomplete.parseStaleMinutes(bad), null, bad);
  assert.equal(incomplete.staleThresholdMs(10), 600000);
  assert.equal(incomplete.staleThresholdMs(0.5), 30000);
  const source = fs.readFileSync(path.join(root, 'src/lib/incomplete.ts'), 'utf8');
  assert.match(source, /code-derived default/);
  assert.match(source, /not measured/);
});

test('the stale list opens the shared request list, oldest first, with the age threshold', () => {
  const drill = incomplete.staleDrill({ controller: 'Payroll', action: 'Run', kind: 'synchronous' }, 'incomplete', 10);
  assert.equal(drill.completion, 'incomplete');
  assert.equal(drill.order, 'oldest');
  assert.deepEqual(drill.incompleteAgeMs, { min: 600000 });
  assert.equal(drill.controller, 'Payroll');
  assert.equal(drill.action, 'Run');
  assert.equal(drill.kind, 'synchronous');
  assert.equal(drill.state, 'all');
  assert.equal(drill.outcome, '');
  assert.match(drill.label, /Payroll\.Run/);
  assert.match(drill.label, /10 min/);
  assert.match(drill.label, /oldest first/);
  const pending = incomplete.staleDrill(null, 'pending', 2.5);
  assert.equal(pending.completion, 'pending');
  assert.deepEqual(pending.incompleteAgeMs, { min: 150000 });
  assert.equal(pending.kind, 'all');
  assert.equal('controller' in pending || 'action' in pending, false);
  assert.match(pending.label, /2\.5 min/);
});

test('the incomplete panel and the Completion % column use existing tokens and show a dash for no rate', () => {
  const panel = fs.readFileSync(path.join(root, 'src/components/IncompletePanel.svelte'), 'utf8');
  assert.equal(/#[0-9a-fA-F]{3,8}\b/.test(panel.slice(panel.indexOf('<style>'))), false);
  assert.match(panel, /rankByCompletion/);
  assert.match(panel, /staleDrill/);
  assert.match(panel, /parseStaleMinutes/);
  const table = fs.readFileSync(path.join(root, 'src/components/ComparisonTable.svelte'), 'utf8');
  assert.match(table, /Completion %/);
  assert.match(table, /colspan="15"/);
  assert.equal(overviewRate(null), '—');
  assert.equal(overviewRate(undefined), '—');
  assert.equal(overviewRate(0), '0.0%');
});

function overviewRate(value) { return loadTypeScript('src/lib/overview.ts').rateText(value); }

test('the stale drill drops every latency range, because the server refuses one beside an age range', () => {
  const drill = incomplete.staleDrill(null, 'incomplete', 10);
  const query = { kind: 'all', outcome: 'all', observedMs: { min: 5 }, apiTimeMs: { max: 900 }, auditCreateTimeMs: { min: 1, max: 2 }, requestKb: { min: 1 }, responseKb: { max: 4 } };
  const applied = incomplete.applyStaleFilters(query, drill);
  assert.equal(applied, query);
  assert.equal(applied.completion, 'incomplete');
  assert.deepEqual(applied.incompleteAgeMs, { min: 600000 });
  for (const key of ['observedMs', 'apiTimeMs', 'auditCreateTimeMs']) assert.equal(key in applied, false, key);
  // Size ranges can sit beside an age range and stay.
  assert.deepEqual(applied.requestKb, { min: 1 });
  assert.deepEqual(applied.responseKb, { max: 4 });
  // A drill with no age range leaves latency ranges alone.
  const untouched = incomplete.applyStaleFilters({ apiTimeMs: { max: 900 } }, { completion: undefined, incompleteAgeMs: undefined });
  assert.deepEqual(untouched, { apiTimeMs: { max: 900 } });
  const source = fs.readFileSync(path.join(root, 'src/App.svelte'), 'utf8');
  assert.match(source, /applyStaleFilters\(copy, drillOverview\)/);
});

test('the stale list buttons say what the list returns and that the counts cover every age', () => {
  const counts = { incomplete: 4, pending: 3 };
  const unfinished = incomplete.staleButtonText('incomplete', counts, 10);
  assert.match(unfinished.text, /oldest first/);
  assert.match(unfinished.text, /7 in total/);
  assert.match(unfinished.text, /includes pending/);
  assert.doesNotMatch(unfinished.text, /stale incomplete \(4\)/);
  assert.match(unfinished.tip, /every age/);
  assert.match(unfinished.tip, /at least 10 min/);
  assert.match(unfinished.tip, /pending/);
  const pending = incomplete.staleButtonText('pending', counts, 2.5);
  assert.match(pending.text, /pending async/);
  assert.match(pending.text, /3 in total/);
  assert.match(pending.tip, /every age/);
  assert.match(pending.tip, /2\.5 min/);
  assert.match(incomplete.staleButtonText('incomplete', counts, null).tip, /stale threshold/);
  assert.match(incomplete.staleDrill(null, 'incomplete', 10).label, /includes pending/);
  const strip = fs.readFileSync(path.join(root, 'src/components/OverviewStrip.svelte'), 'utf8');
  assert.match(strip, /Completed . all events/);
});

test('an export from the stale list carries the same oldest-first order, and a plain export carries none', async () => {
  const bodies = [];
  const saved = { document: global.document, fetch: global.fetch };
  global.document = { querySelector: () => null };
  global.fetch = async (url, init) => {
    bodies.push(JSON.parse(init.body));
    return { ok: true, status: 200, blob: async () => 'csv', headers: { get: () => null } };
  };
  try {
    const { api } = loadTypeScript('src/lib/api.ts');
    await api.export({ kind: 'all' }, '', undefined, 'all', 'oldest');
    await api.export({ kind: 'all' }, '', undefined, 'all');
    await api.export({ kind: 'all' }, '', undefined, 'all', '');
  } finally {
    global.document = saved.document;
    global.fetch = saved.fetch;
    if (saved.document === undefined) delete global.document;
    if (saved.fetch === undefined) delete global.fetch;
  }
  assert.equal(bodies[0].order, 'oldest');
  assert.equal(bodies[0].state, 'all');
  assert.equal('order' in bodies[1], false);
  assert.equal('order' in bodies[2], false);
  const source = fs.readFileSync(path.join(root, 'src/App.svelte'), 'utf8');
  assert.match(source, /api\.export\([^\n]*requestState, drillOverview\?\.order \?\? ''\)/);
});

const depth = loadTypeScript('src/lib/depth.ts');
const navAt = (whereOpen, hasContext, recordOpen) => ({ whereOpen, hasContext, recordOpen });

test('depth follows the deepest open level: pulse, where, evidence, record', () => {
  assert.equal(depth.depthOf(navAt(false, false, false)), 0);
  assert.equal(depth.depthOf(navAt(true, false, false)), 1);
  assert.equal(depth.depthOf(navAt(true, true, false)), 2);
  assert.equal(depth.depthOf(navAt(false, true, false)), 2);
  assert.equal(depth.depthOf(navAt(true, true, true)), 3);
});

test('breadcrumb is empty at the pulse and names every level reached beyond it', () => {
  assert.deepEqual(depth.breadcrumb(navAt(false, false, false)), []);
  assert.deepEqual(depth.breadcrumb(navAt(true, false, false)).map(c => c.id), ['pulse', 'where']);
  const deep = depth.breadcrumb(navAt(true, true, true), { evidence: 'Orders.Get', record: 'abc123' });
  assert.deepEqual(deep.map(c => c.label), ['Pulse', 'Where is it?', 'Orders.Get', 'abc123']);
  assert.deepEqual(deep.map(c => c.current), [false, false, false, true]);
  assert.deepEqual(depth.breadcrumb(navAt(false, true, false)).map(c => c.label), ['Pulse', 'Operation']);
  assert.deepEqual(depth.breadcrumb(navAt(false, true, true)).map(c => c.id), ['pulse', 'evidence', 'record']);
});

test('Esc steps up exactly one level and keeps the levels above open', () => {
  const full = navAt(true, true, true);
  assert.equal(depth.stepUpTarget(full), 'record');
  const afterRecord = depth.stepUp(full);
  assert.deepEqual(afterRecord, navAt(true, true, false));
  assert.equal(depth.stepUpTarget(afterRecord), 'evidence');
  const afterOperation = depth.stepUp(afterRecord);
  assert.deepEqual(afterOperation, navAt(true, false, false));
  assert.equal(depth.stepUpTarget(afterOperation), 'where');
  assert.deepEqual(depth.stepUp(afterOperation), navAt(false, false, false));
  assert.equal(depth.stepUpTarget(navAt(false, false, false)), null);
  assert.deepEqual(depth.stepUp(navAt(false, false, false)), navAt(false, false, false));
  assert.deepEqual(depth.stepUpTo(full, 1), navAt(true, false, false));
  assert.deepEqual(depth.stepUpTo(full, 0), navAt(false, false, false));
});

test('the active quick range is the one whose window and equal reference window match the query', () => {
  const ranges = time.quickRanges;
  const q = time.quickRange('1h', new Date('2026-10-22T12:00:00Z'));
  assert.equal(depth.activeRangeId(q, ranges), '1h');
  assert.equal(depth.activeRangeId({ ...q, baselineEnd: '2026-10-22T10:00:00Z' }, ranges), null);
  assert.equal(depth.activeRangeId({ ...q, start: 'nonsense' }, ranges), null);
  assert.equal(depth.activeRangeId({ ...q, end: '2026-10-22T11:30:00Z' }, ranges), null);
});

test('filter chips list exactly the active More filters and each patch removes only its own', () => {
  const query = time.defaultQuery(new Date('2026-10-22T12:00:00Z'), ['source-1']);
  assert.deepEqual(depth.filterChips(query), []);
  assert.equal(depth.moreFiltersCount(query), 0);
  Object.assign(query, {
    kind: 'http_unknown', outcome: 'server_error', metric: 'api', controller: 'Orders', action: ' Get ',
    completion: 'pending', userIds: [7, 9], statusCodes: ['500'], urlContains: '/api/x', observedMs: { min: 100 }, requestKb: { min: 1, max: 5 }
  });
  const chips = depth.filterChips(query);
  assert.deepEqual(chips.map(c => c.id), ['kind', 'outcome', 'metric', 'controller', 'action', 'completion', 'userIds', 'statusCodes', 'urlContains', 'observedMs', 'requestKb']);
  assert.equal(depth.moreFiltersCount(query), chips.length);
  assert.equal(chips.find(c => c.id === 'action').value, 'Get');
  assert.equal(chips.find(c => c.id === 'kind').value, 'HTTP (polling type unknown)');
  assert.equal(chips.find(c => c.id === 'userIds').value, '7, 9');
  assert.equal(chips.find(c => c.id === 'observedMs').value, 'at least 100 ms');
  assert.equal(chips.find(c => c.id === 'requestKb').value, '1–5 KB');
  for (const chip of chips) {
    const before = depth.filterChips(query).map(c => c.id);
    depth.applyChipPatch(query, chip);
    const after = depth.filterChips(query).map(c => c.id);
    assert.deepEqual(after, before.filter(id => id !== chip.id), 'removing ' + chip.id);
  }
  assert.equal(query.kind, 'all');
  assert.equal(query.outcome, 'all');
  assert.equal(query.metric, 'total');
  assert.equal(query.controller, '');
  assert.equal(query.completion, undefined);
});

test('the sync/async/hangfire/http kind and the quick ranges are not chips, they have their own controls', () => {
  const query = time.defaultQuery(new Date('2026-10-22T12:00:00Z'), ['source-1']);
  for (const kind of ['all', 'synchronous', 'asynchronous', 'hangfire', 'http']) {
    query.kind = kind;
    assert.deepEqual(depth.filterChips(query), [], kind);
  }
});

const pulse = loadTypeScript('src/lib/pulse.ts');
const overviewRow = (kind, events, completed, clientErrors, serverErrors, p95Ms) => ({ kind, events, completed, clientErrors, serverErrors, p95: p95Ms == null ? null : { valueMs: p95Ms } });
const rollupRow = (controller, action, extra = {}) => ({ controller, action, kind: 'synchronous', requests: 10, failures: 0, perMinute: 1, ...extra });

test('the pulse figures sum the request types in scope and say so when latency cannot be stated', () => {
  const rows = [overviewRow('synchronous', 80, 60, 4, 1, 250), overviewRow('asynchronous', 20, 20, 0, 3, 900)];
  const all = pulse.pulseFigures(rows, 'all', 'total', 'Observed API time (AuditCreateTime + ApiTime)');
  assert.deepEqual(all.map(f => f.id), ['requests', 'completion', 'p95', 'client', 'server']);
  assert.equal(all[0].value, (100).toLocaleString());
  assert.equal(all[1].value, '80.0%');
  assert.equal(all[2].value, '—');
  assert.match(all[2].hint, /one request type/);
  assert.match(all[2].tip, /not pooled/);
  assert.equal(all[3].value, '4');
  assert.equal(all[4].value, '4');
  const sync = pulse.pulseFigures(rows, 'synchronous', 'total', 'Observed API time (AuditCreateTime + ApiTime)');
  assert.equal(sync[2].label, 'Observed P95');
  assert.equal(sync[2].value, '250 ms');
  assert.equal(pulse.pulseFigures(rows, 'synchronous', 'api', 'API processing')[2].label, 'API P95');
  assert.match(sync[1].tip, /Completed . all events/);
});

test('an empty period gives honest figures, never a fake zero percent or a made-up P95', () => {
  const figures = pulse.pulseFigures([], 'synchronous', 'total', 'x');
  assert.equal(figures[0].value, '0');
  assert.equal(figures[1].value, '—');
  assert.equal(figures[2].value, '—');
  assert.equal(figures[2].hint, 'No timed requests');
  assert.equal(pulse.heroHasData([]), false);
  assert.equal(pulse.heroHasData([{ count: 0 }, { count: 0 }]), false);
  assert.equal(pulse.heroHasData([{ count: 0 }, { count: 2 }]), true);
});

test('only the four request types with their own histogram count as a single kind', () => {
  for (const kind of ['synchronous', 'asynchronous', 'hangfire', 'http_unknown']) assert.equal(pulse.singleKind(kind), true, kind);
  for (const kind of ['all', 'unknown']) assert.equal(pulse.singleKind(kind), false, kind);
  assert.equal(pulse.durationText(null), '—');
  assert.equal(pulse.durationText(Number.NaN), '—');
  assert.equal(pulse.durationText(4.26), '4.3 ms');
  assert.equal(pulse.durationText(250.4), '250 ms');
  assert.equal(pulse.durationText(1500), '1.50 s');
  assert.equal(pulse.durationText(12_400), '12.4 s');
});

test('operations rank worst first: lowest completion, otherwise highest value, and rows without a value go last', () => {
  const rows = [
    rollupRow('A', 'one', { requests: 5, events: 5, completionRate: 0.9, p95Ms: 100, serverErrorRate: 0.01 }),
    rollupRow('B', 'two', { requests: 50, events: 50, completionRate: 0.4, p95Ms: 900, serverErrorRate: 0.2 }),
    rollupRow('C', 'three', { requests: 20, events: 20 }),
    rollupRow('D', 'four', { requests: 20, events: 20, completionRate: 0.7, p95Ms: 300, serverErrorRate: 0 })
  ];
  const names = (lens) => pulse.rankOperations(rows, lens).map(row => row.controller).join('');
  assert.equal(names('completion'), 'BDAC');
  assert.equal(names('p95'), 'BDAC');
  assert.equal(names('server'), 'BADC');
  assert.equal(names('requests'), 'BCDA');
  assert.deepEqual(rows.map(row => row.controller), ['A', 'B', 'C', 'D']);
});

test('the lens read states the one number that matters with its honest label and delta', () => {
  const row = rollupRow('A', 'one', { requests: 5, events: 8, completionRate: 0.5, p95Ms: 1200, p95DeltaMs: 200, clientErrorRate: 0.25, serverErrorRate: null });
  assert.equal(pulse.lensRead(row, 'requests').label, 'requests');
  assert.equal(pulse.lensRead(row, 'requests').sort, 8);
  assert.equal(pulse.lensRead(row, 'completion').label, 'completed');
  assert.equal(pulse.lensRead(row, 'p95').value, '1.20 s');
  assert.equal(pulse.lensRead(row, 'p95').label, 'P95');
  assert.equal(pulse.lensRead(row, 'client').label, '4xx');
  assert.equal(pulse.lensRead(row, 'server').sort, null);
  assert.equal(pulse.lensRead(row, 'server').value, '—');
  assert.equal(pulse.lensRead(row, 'completion').delta, '—');
});

test('a sparkline comes from the busiest comparison group of the operation and is empty without a series', () => {
  const point = (count, extra = {}) => ({ count, ...extra });
  const analysisLike = {
    apis: [
      { key: 'small', controller: 'A', action: 'one', kind: 'synchronous', current: { count: 3 } },
      { key: 'big', controller: 'A', action: 'one', kind: 'synchronous', current: { count: 30 } },
      { key: 'other-kind', controller: 'A', action: 'one', kind: 'asynchronous', current: { count: 99 } }
    ],
    timeline: [
      { key: 'small', points: [point(1)] },
      { key: 'big', points: [point(4, { incomplete: 1, count4xx: 2, count5xx: 3, p95: { valueMs: 120 } }), point(null)] }
    ]
  };
  const target = { controller: 'A', action: 'one', kind: 'synchronous' };
  assert.deepEqual(pulse.sparkSeries(analysisLike, target, 'requests'), [5, null]);
  assert.deepEqual(pulse.sparkSeries(analysisLike, target, 'completion'), [1, null]);
  assert.deepEqual(pulse.sparkSeries(analysisLike, target, 'client'), [2, null]);
  assert.deepEqual(pulse.sparkSeries(analysisLike, target, 'server'), [3, null]);
  assert.deepEqual(pulse.sparkSeries(analysisLike, target, 'p95'), [120, null]);
  assert.deepEqual(pulse.sparkSeries(analysisLike, { controller: 'Z', action: 'none', kind: 'synchronous' }, 'p95'), []);
  assert.deepEqual(pulse.sparkSeries(analysisLike, { controller: 'A', action: 'one', kind: 'asynchronous' }, 'p95'), []);
});

const autorun = loadTypeScript('src/lib/autorun.ts');
const scopeAt = (extra = {}) => ({ ...time.defaultQuery(new Date('2026-10-22T12:00:00Z'), ['source-1']), ...extra });

test('an automatic run is blocked by the same problems, in the same order, as a manual run', () => {
  assert.equal(autorun.scopeProblem(scopeAt(), true), null);
  assert.match(autorun.scopeProblem(scopeAt(), false), /valid local value/);
  assert.match(autorun.scopeProblem(scopeAt({ sourceIds: [] }), true), /Select at least one source/);
  assert.match(autorun.scopeProblem(scopeAt({ start: scopeAt().end, end: scopeAt().start }), true), /earlier than its end/);
  assert.match(autorun.scopeProblem(scopeAt({ baselineEnd: scopeAt().end }), true), /reference window must end/);
  assert.match(autorun.scopeProblem(scopeAt({ bucketSeconds: 5 }), true), /Bucket size/);
  assert.match(autorun.scopeProblem(scopeAt({ bucketSeconds: 10, start: '2026-10-22T00:00:00Z', end: '2026-10-22T12:00:00Z', baselineStart: '2026-10-21T12:00:00Z', baselineEnd: '2026-10-22T00:00:00Z' }), true), /more than 120 time points/);
  assert.match(autorun.scopeProblem(scopeAt({ start: '2026-10-01T00:00:00Z', end: '2026-10-22T12:00:00Z', baselineStart: '2026-09-01T00:00:00Z', baselineEnd: '2026-10-01T00:00:00Z' }), true), /at most 7 days/);
});

test('the run hint names what is missing instead of raising an error', () => {
  assert.equal(autorun.scopeHint(scopeAt(), true), '');
  assert.equal(autorun.scopeHint(scopeAt({ sourceIds: [] }), true), 'Choose a source to run the analysis.');
  assert.equal(autorun.scopeHint(scopeAt(), false), 'Complete the dates to run the analysis.');
});

test('only a landing window of at most 24 hours runs by itself', () => {
  const hours = (count) => ({ start: '2026-10-22T00:00:00Z', end: new Date(Date.parse('2026-10-22T00:00:00Z') + count * 3_600_000).toISOString() });
  assert.equal(autorun.landingMayAutoRun(hours(1)), true);
  assert.equal(autorun.landingMayAutoRun(hours(24)), true);
  assert.equal(autorun.landingMayAutoRun(hours(25)), false);
  assert.equal(autorun.landingMayAutoRun(hours(168)), false);
  assert.equal(autorun.landingMayAutoRun({ start: 'nonsense', end: hours(1).end }), false);
  assert.equal(autorun.landingMayAutoRun(hours(0)), false);
});

test('a scope change runs only when the scope is valid and is not what is shown or already in flight', () => {
  const decide = (state) => autorun.autoRunDecision({ problem: null, signature: 'B', shown: 'A', inflight: null, ...state });
  assert.equal(decide({}), 'run');
  assert.equal(decide({ shown: 'B' }), 'skip');
  assert.equal(decide({ shown: null }), 'run');
  assert.equal(decide({ problem: 'Choose a source' }), 'skip');
  assert.equal(decide({ inflight: 'B' }), 'skip');
  assert.equal(decide({ inflight: 'C' }), 'run');
  assert.equal(decide({ inflight: 'C', shown: 'B' }), 'run');
});

test('a burst of scope changes becomes one run after the quiet period and the last change wins', () => {
  let now = 0; let nextId = 1; const timers = new Map();
  const fake = { set: (fn, ms) => { const id = nextId++; timers.set(id, { fn, at: now + ms }); return id; }, clear: (id) => { timers.delete(id); } };
  const advance = (ms) => { now += ms; for (const [id, timer] of [...timers]) if (timer.at <= now) { timers.delete(id); timer.fn(); } };
  let runs = 0;
  const coalescer = autorun.createCoalescer(() => { runs++; }, autorun.AUTO_RUN_DELAY_MS, fake);
  assert.equal(autorun.AUTO_RUN_DELAY_MS, 300);
  coalescer.schedule(); advance(200);
  coalescer.schedule(); advance(200);
  coalescer.schedule(); advance(299);
  assert.equal(runs, 0);
  assert.equal(coalescer.pending, true);
  advance(1);
  assert.equal(runs, 1);
  assert.equal(coalescer.pending, false);
  advance(1000);
  assert.equal(runs, 1);
  coalescer.schedule(); coalescer.cancel(); advance(1000);
  assert.equal(runs, 1);
  assert.equal(coalescer.pending, false);
});

test('the evidence frame always keeps Slowest, and comparison-wide contexts keep Failures and Breakdown', () => {
  const ids = (context) => depth.evidenceTabs({ operation: false, group: false, scoped: true, phases: false, failed: 0, unfinished: 0, ...context }).map(tab => tab.id);
  assert.deepEqual(ids({}), ['failures', 'slowest', 'breakdown']);
  assert.deepEqual(ids({ scoped: false }), ['slowest']);
  assert.deepEqual(ids({ operation: true, group: true, phases: true, failed: 3, unfinished: 2 }), ['timeline', 'phases', 'failures', 'incomplete', 'slowest', 'breakdown']);
  assert.deepEqual(ids({ operation: true, group: true }), ['timeline', 'slowest', 'breakdown']);
  assert.deepEqual(ids({ group: true, failed: 1 }), ['timeline', 'failures', 'slowest', 'breakdown']);
  assert.ok(ids({ operation: true }).includes('slowest'));
});

test('the all-operations drill is comparison-wide: no controller or action, every state, and the comparison request type', () => {
  const { allOperationsDrill } = loadTypeScript('src/lib/overview.ts');
  const all = allOperationsDrill();
  assert.equal(all.kind, 'all');
  assert.equal(all.state, 'all');
  assert.equal(all.controller, undefined);
  assert.equal(all.action, undefined);
  assert.equal(all.label, 'All operations');
  assert.equal(allOperationsDrill('asynchronous').kind, 'asynchronous');
});

test('the redesign keeps its contrast, status and navigation fixes in the sources', () => {
  const css = fs.readFileSync(path.join(root, 'src/app.css'), 'utf8');
  const phaseBar = fs.readFileSync(path.join(root, 'src/components/PhaseBar.svelte'), 'utf8');
  const app = fs.readFileSync(path.join(root, 'src/App.svelte'), 'utf8');
  assert.equal((css.match(/--control-line:\s*#[0-9a-f]{6}/gi) ?? []).length, 3, 'one control boundary per theme block');
  assert.match(css, /input, select[^{]*\{[^}]*border[^;]*var\(--control-line\)/);
  assert.match(css, /::placeholder\s*\{[^}]*opacity:\s*1/);
  assert.doesNotMatch(phaseBar, /--pending/);
  assert.doesNotMatch(css, /max-width:\s*14ch/);
  assert.match(css, /\.crumbs li:last-child\s*\{[^}]*flex:\s*1 1 auto/);
  assert.doesNotMatch(app, /onclick=\{runAnalysis\}/);
  assert.match(app, /scopeRun\.schedule\(\)/);
});
