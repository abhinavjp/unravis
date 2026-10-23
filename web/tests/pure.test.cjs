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
  new Function('require', 'module', 'exports', output)(createRequire(filename), module, module.exports);
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
  assert.equal(presentation.statusRates(undefined, 'all').client, '—');
  assert.match(presentation.statusRates(undefined, 'all').tip, /No requests with a known/);
  assert.match(presentation.statusRates(rollup, 'all', 'hangfire').tip, /Covers only the hangfire requests/);
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
  assert.match(errorRateTip(r, 'all', 'all'), /every source and kind/);
  assert.doesNotMatch(errorRateTip(r, 'all', 'hangfire'), /every source and kind/);
  assert.match(errorRateTip(r, 'all', 'hangfire'), /hangfire/);
  assert.match(errorRateTip(r, 'success', 'all'), /outcome filter set to All/);
  assert.match(errorRateTip(undefined, 'all', 'all'), /known/);
  assert.match(loadTip(0.8), /other traffic/);
  assert.match(loadTip(-0.9), /latency falls as load rises/);
  assert.doesNotMatch(loadTip(-0.9), /rises and falls/);
  const rollups = [{ controller: 'A', action: 'Run', requests: 10, failures: 1, errorRate: 0.1, perMinute: 2 }];
  assert.equal(rollupFor(rollups, { controller: 'A', action: 'Run' }).errorRate, 0.1);
  assert.equal(rollupFor(rollups, { controller: 'A', action: 'Other' }), undefined);
  assert.equal(rollupFor(undefined, { controller: 'A', action: 'Run' }), undefined);
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
