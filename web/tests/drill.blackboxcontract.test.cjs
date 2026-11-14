const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

// Wave-13 lane fix (W13-01..W13-04): the Blackbox measure/unit contract, run end to end through the
// real exported helpers against the CURRENT server shape (internal/server/tracing_blackbox.go):
// the SQL lane carries reasonCode 'blackbox', estimated host batch COUNTS per bucket, no busyMs,
// an omitted blockSeconds (the server's 0 is dropped), empty events and a `since` timestamp.
// Chain: parseTracing -> toLaneIn mapping -> buildLane/laneCells -> regroupBlocks -> mapRow/readoutAt -> dbState.
// The toLaneIn mapping below mirrors SlowCallScreen.svelte and is kept honest by source-text assertions.

const root = path.resolve(__dirname, '..');
function loadTypeScript(relativePath) {
  const filename = path.join(root, relativePath);
  const source = fs.readFileSync(filename, 'utf8');
  const output = ts.transpileModule(source, {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022, esModuleInterop: true }
  }).outputText;
  const module = { exports: {} };
  const nodeRequire = createRequire(filename);
  const load = (request) => /^\.\.?\//.test(request) && fs.existsSync(path.join(path.dirname(filename), request + '.ts'))
    ? loadTypeScript(path.relative(root, path.join(path.dirname(filename), request + '.ts')))
    : nodeRequire(request);
  new Function('require', 'module', 'exports', output)(load, module, module.exports);
  return module.exports;
}

const { parseTracing } = loadTypeScript('src/lib/tracing/model.ts');
const { buildLane, laneCells, regroupBlocks, measureOf, cellValue, formatAtOnce } = loadTypeScript('src/lib/drill/callLanes.ts');
const { dbState } = loadTypeScript('src/lib/drill/dbState.ts');
const { DRILL_TEXT: T } = loadTypeScript('src/lib/drill/drillCopy.ts');
const { mapRow } = loadTypeScript('src/lib/drill/callMap.ts');
const { readoutAt } = loadTypeScript('src/lib/drill/callReadout.ts');

const T0 = Date.parse('2026-11-11T10:00:00Z');
const MIN = 60000;
const SPAN = 15 * MIN;
const iso = (ms) => new Date(ms).toISOString();
const WINDOW = { start: iso(T0), end: iso(T0 + SPAN), axisStart: iso(T0), axisEnd: iso(T0 + SPAN), bucketSeconds: 60 };
const SINCE = '2026-10-20T08:30:00Z';
const fmt = { date: (s) => 'D(' + s.slice(0, 10) + ')', time: (s) => 'T(' + s.slice(11, 16) + ')' };
const call = { startMs: T0 + 2 * MIN, endMs: T0 + 2 * MIN + 5000 };

// The current server's SQL lane: counts only, no busyMs, no blockSeconds, no events.
function sqlWire(buckets, over = {}) {
  return {
    key: 'sql', label: 'SQL Server', state: 'events', reason: '', origin: 'case', asOf: iso(T0 + SPAN),
    complete: true, truncated: false, scanned: 90000, hidden: 0,
    sources: [{ sourceId: 'blackbox', label: 'Blackbox sampling every 15 s since 20 Oct 2026' }],
    reasonCode: 'blackbox', since: SINCE, buckets, events: [], ...over
  };
}
const wire = (buckets, over) => ({ window: WINDOW, lanes: [sqlWire(buckets, over)], allLanesStored: true });
const counts = (list) => list.map((c, i) => ({ start: iso(T0 + i * MIN), count: c }));
const parsedSql = (buckets, over) => parseTracing(wire(buckets, over)).lanes[0];

// Mirrors SlowCallScreen.toLaneIn: Query Store block semantics only for Query Store provenance.
function toLaneIn(lane, savedDay) {
  const reasonCode = lane.reasonCode ?? lane.incompleteReason;
  const qsProvenance = reasonCode === 'held-sql' || reasonCode === 'layered-sql' || reasonCode === 'qs-file';
  const minutes = savedDay?.lane?.queryStore?.intervalMinutes;
  const fallback = qsProvenance && typeof minutes === 'number' && Number.isFinite(minutes) && minutes > 0 ? minutes * 60 : undefined;
  return {
    key: lane.key,
    state: lane.state,
    reasonCode,
    measure: reasonCode === 'blackbox' ? 'batch-rate' : undefined,
    blockSeconds: lane.blockSeconds ?? fallback,
    buckets: lane.buckets,
    events: lane.events ?? [],
    hidden: lane.hidden
  };
}
const build = (lane, savedDay) => buildLane(toLaneIn(lane, savedDay), 'sql', WINDOW, T0, T0 + SPAN);
const dbView = (lane, over = {}) => dbState({
  // The screen passes the wire lane's reason code; a built batch-rate lane is the 'blackbox' contract.
  laneReasonCode: lane.measure === 'batch-rate' ? 'blackbox' : '',
  laneHasShape: lane.hasShape,
  laneHasObservations: lane.hasShape,
  laneAsOf: null,
  laneSince: null,
  laneCadenceSeconds: null,
  blockSeconds: lane.blockSeconds,
  queryStore: null,
  hasEvents: false,
  windowStart: T0,
  windowEnd: T0 + SPAN,
  ...over
}, fmt);
const savedQsDay = { lane: { queryStore: { state: 'on', firstData: '2026-11-01T00:00:00Z', intervalMinutes: 15 } } };

// ---- W13-01: positive counts draw a unit-labelled lane; duration is unavailable, never "nothing slow" ----

test('positive-only: 100 batches/s throughout draws a nonempty Blackbox lane with throughput units', () => {
  const sql = parsedSql(counts(Array(15).fill(6000)));
  for (const bucket of sql.buckets) assert.equal('busyMs' in bucket, false, 'the server sent no busy time');
  assert.equal('blockSeconds' in sql, false, 'the server omitted the zero block length');
  const lane = build(sql);
  assert.equal(lane.measure, 'batch-rate');
  assert.equal(lane.cells.length, 15);
  for (const cell of lane.cells) {
    assert.equal(cell.rate, 100, 'count 6000 over a 60 s step is 100 batches/s');
    assert.equal(cell.atOnce, null, 'no duration is inferred from a batch rate');
  }
  assert.equal(lane.hasShape, true);
  assert.deepEqual(lane.peak, { atOnce: 100, start: T0 });
  const db = dbView(lane, { laneSince: sql.since });
  assert.equal(db.key, 'blackbox');
  assert.equal(db.shape, 'blocks');
  assert.equal(db.measure, 'batch-rate');
  assert.ok(db.source.includes('batches per second'), db.source);
  assert.notEqual(db.source, T.dbSrcNone);
  // the readout carries the throughput unit and states concurrency as unavailable
  const read = readoutAt(T0 + 30000, { audit: [], iis: [], sql: lane.cells }, call);
  assert.equal(read.db.rate, 100);
  assert.equal(read.db.atOnce, null);
  assert.equal(T.readDbRate(formatAtOnce(read.db.rate)), 'Database batches per second 100');
  assert.equal(T.readDbNoAtOnce, 'running at once not recorded');
});

test('genuine-zero: a read that saw no batches is a real zero, still a Blackbox source', () => {
  const lane = build(parsedSql(counts(Array(15).fill(0))));
  assert.equal(lane.hasShape, true);
  for (const cell of lane.cells) {
    assert.equal(cell.rate, 0);
    assert.equal(cell.atOnce, null);
  }
  const db = dbView(lane);
  assert.equal(db.key, 'blackbox');
  const read = readoutAt(T0 + 30000, { audit: [], iis: [], sql: lane.cells }, call);
  assert.equal(read.db.rate, 0, 'a genuine zero is drawn as zero');
});

test('mixed: positive, zero, missing and incomplete minutes keep their own values', () => {
  const buckets = counts([6000, 0, 3000, 0, 120]);
  buckets.push({ start: iso(T0 + 5 * MIN), count: 0, complete: false }); // explicitly not read to the end
  // minutes 6..14 have no bucket at all
  const lane = build(parsedSql(buckets));
  assert.deepEqual(lane.cells.map((c) => c.rate), [100, 0, 50, 0, 2, null, null, null, null, null, null, null, null, null, null]);
  for (const cell of lane.cells) assert.equal(cell.atOnce, null);
  assert.equal(lane.hasShape, true);
  assert.equal(dbView(lane).key, 'blackbox');
  // the incomplete minute reads as unknown, never as its count of 0
  const read = readoutAt(T0 + 5 * MIN + 30000, { audit: [], iis: [], sql: lane.cells }, call);
  assert.equal(read.db.rate, null);
});

test('unavailable: no buckets, or only incomplete ones, is not a source and never a zero lane', () => {
  for (const buckets of [[], Array.from({ length: 15 }, (_, i) => ({ start: iso(T0 + i * MIN), count: 0, complete: false }))]) {
    const lane = build(parsedSql(buckets));
    assert.equal(lane.hasShape, false);
    assert.equal(lane.peak, null);
    for (const cell of lane.cells) assert.equal(cell.rate, null);
    const db = dbView(lane);
    assert.equal(db.key, 'notread');
    assert.equal(db.source, T.dbSrcNone);
    assert.equal(db.shape, 'none');
  }
});

// ---- W13-02: an unrelated saved Query Store day never touches Blackbox cells ----

test('a saved Query Store day does not change Blackbox cells or borrow its block length', () => {
  const sql = parsedSql(counts([6000, 0, 3000, 0, 120]));
  const without = build(sql, null);
  const withDay = build(sql, savedQsDay);
  assert.equal(toLaneIn(sql, savedQsDay).blockSeconds, undefined, 'the 15-minute Query Store interval is not borrowed');
  assert.deepEqual(withDay.cells, without.cells);
  assert.equal(withDay.blockSeconds, null);
  // API-seen data is equally protected
  const apiSeen = parsedSql(counts([6000]), { reasonCode: 'api-seen' });
  assert.equal(toLaneIn(apiSeen, savedQsDay).blockSeconds, undefined);
  // Query Store provenance still gets its own source interval
  const held = parsedSql(counts([6000]), { reasonCode: 'held-sql' });
  assert.equal(toLaneIn(held, savedQsDay).blockSeconds, 900);
  const qsFile = parsedSql(counts([6000]), { reasonCode: 'qs-file' });
  assert.equal(toLaneIn(qsFile, savedQsDay).blockSeconds, 900);
});

test('mixed cells within one block stay unknown after regroup, minimap and readout transforms', () => {
  // One 15-minute block: a complete count-zero minute, positive-count minutes with no duration,
  // a missing minute and an explicitly incomplete final minute (the W13-02 exploit shape).
  const buckets = counts([0, 6000, 6000]);
  buckets.push({ start: iso(T0 + 14 * MIN), count: 0, complete: false });
  const lane = build(parsedSql(buckets));
  const forced = regroupBlocks(lane.cells, 900); // even if a block length were ever supplied
  assert.equal(forced[0].rate, 200 / 3, 'average over the known minutes only: (0 + 100 + 100) / 3');
  assert.equal(forced[1].rate, forced[0].rate);
  assert.equal(forced[2].rate, forced[0].rate);
  for (let i = 3; i < 15; i += 1) assert.equal(forced[i].rate, null, 'minute ' + i + ' stays unknown');
  assert.equal(forced[14].rate, null, 'the explicit complete:false minute stays unknown');
  for (const cell of forced) assert.equal(cell.atOnce, null);
  // the minimap keeps every unknown column unknown (its unit is concurrency, which was not recorded)
  const map = mapRow('sql', forced);
  for (const col of map.columns) assert.equal(col.height, null);
  // the readout at an unknown minute is unknown, not zero
  const read = readoutAt(T0 + 10 * MIN + 30000, { audit: [], iis: [], sql: forced }, call);
  assert.equal(read.db.rate, null);
  assert.equal(read.db.atOnce, null);
  // and a block with one known zero plus unknowns never becomes all zeros
  const zeroBlock = regroupBlocks(laneCells({ key: 'sql', state: 'events', reasonCode: 'blackbox', buckets: counts([0]), events: [] }, WINDOW, T0, T0 + SPAN), 900);
  assert.equal(zeroBlock[0].rate, 0);
  for (let i = 1; i < 15; i += 1) assert.equal(zeroBlock[i].rate, null);
});

test('the minimap never turns a missing measurement into a zero column', () => {
  const lane = build(parsedSql(counts([6000, 0])));
  const map = mapRow('sql', lane.cells);
  assert.equal(map.columns[0].height, null, 'concurrency was not recorded, so the concurrency strip stays hatched');
  assert.equal(map.columns[2].height, null);
  assert.equal(map.peak, null);
});

// ---- W13-03: captions describe the host-counter source and its real cadence ----

test('captions name the host batch source and state 5/15/30/60 s only when the source sent it', () => {
  const lane = build(parsedSql(counts([6000])));
  for (const cadence of [5, 15, 30, 60]) {
    const sql = parsedSql(counts([6000]), { cadenceSeconds: cadence });
    assert.equal(sql.cadenceSeconds, cadence, 'optional source metadata is kept when sent');
    const db = dbView(lane, { laneSince: sql.since, laneCadenceSeconds: sql.cadenceSeconds });
    assert.ok(db.source.includes(`read every ${cadence} s`), db.source);
    assert.ok(db.message.includes('host counter'), db.message);
    assert.ok(db.short.includes(`every ${cadence} s`), db.short);
  }
  const unknown = dbView(lane, { laneSince: SINCE });
  assert.doesNotMatch(unknown.source, /every \d/, unknown.source);
  assert.ok(unknown.message.includes('The read interval is not recorded.'), unknown.message);
  assert.equal(unknown.short, 'batches/s, read interval unknown');
  // no caption borrows the SQL-service DMV cadences or the server label's fixed 15 s claim
  for (const text of [unknown.source, unknown.message, unknown.short]) {
    assert.doesNotMatch(text, /15 to 60|sampling every 15 s/, text);
  }
  assert.ok(unknown.message.includes('not how many ran at once or how long it took'), 'throughput vs concurrency is explicit');
});

// ---- W13-04: missing, invalid and valid dates after parseTracing ----

test('a valid since reaches the caption; a missing or invalid one reads as unknown, never a sample date', () => {
  const lane = build(parsedSql(counts([6000])));
  const valid = dbView(lane, { laneSince: parsedSql(counts([6000])).since });
  assert.ok(valid.source.includes('since D(2026-10-20)'), valid.source);
  for (const over of [{ since: undefined }, { since: 'not a date' }, { since: '' }, { since: 123 }]) {
    const sql = parsedSql(counts([6000]), over);
    assert.equal('since' in sql, false, 'the parser drops an unreadable since');
    const db = dbView(lane, { laneSince: sql.since ?? null });
    assert.ok(db.source.includes('recording start unknown'), db.source);
    assert.doesNotMatch(db.source, /11 Nov 2026/, 'no fabricated production date');
  }
});

test('optional cadence metadata is validated; unknown additions never break the parse', () => {
  for (const bad of [0, -5, '15', null, NaN]) {
    const sql = parsedSql(counts([6000]), { cadenceSeconds: bad });
    assert.equal('cadenceSeconds' in sql, false, 'cadence ' + JSON.stringify(bad) + ' is not kept');
  }
  const sql = parsedSql(counts([6000]), { someFutureField: { a: 1 }, measure: 'whatever' });
  assert.equal(sql.reasonCode, 'blackbox');
  assert.equal(measureOf({ reasonCode: sql.reasonCode }), 'batch-rate', 'the measure is derived from the contract, not an unknown field');
});

// ---- the wiring: the screen and the chart keep the contract ----

test('the screen gates the Query Store fallback by provenance and passes the measure and cadence', () => {
  const source = fs.readFileSync(path.join(root, 'src', 'components', 'SlowCallScreen.svelte'), 'utf8');
  const fn = source.slice(source.indexOf('function toLaneIn'), source.indexOf('const SQL_EVENT_SOURCES'));
  assert.ok(fn.includes("reasonCode !== 'held-sql' && reasonCode !== 'layered-sql' && reasonCode !== 'qs-file'"), 'only Query Store provenance borrows the saved interval');
  assert.ok(fn.includes('lane.blockSeconds ?? qsBlockSeconds(lane.key)'), 'the lane keeps its own block length first');
  assert.ok(fn.includes("measure: reasonCode === 'blackbox' ? 'batch-rate' : undefined"), 'the measure passes through');
  assert.ok(source.includes('laneCadenceSeconds: sqlLane?.cadenceSeconds ?? null'));
  assert.ok(source.includes('laneHasObservations: lanes.sql.hasShape'));
  assert.doesNotMatch(source, /\{@html/);
});

test('the chart labels the throughput measure and never claims absent slow work', () => {
  const source = fs.readFileSync(path.join(root, 'src', 'components', 'CallLanes.svelte'), 'utf8');
  assert.ok(source.includes("lanes.sql.measure === 'batch-rate'"));
  assert.ok(source.includes('DRILL_TEXT.laneRateShape('));
  assert.ok(source.includes('DRILL_TEXT.readDbRate('));
  assert.ok(source.includes('DRILL_TEXT.readDbNoAtOnce'));
  assert.ok(source.includes('DRILL_TEXT.legendRate'));
  assert.ok(source.includes("name === 'sql' && sqlRate ? DRILL_TEXT.laneNoCallTimes"));
  assert.doesNotMatch(source, /\{@html/);
});

test('the state model keeps demonstration dates behind the demo switch only', () => {
  const source = fs.readFileSync(path.join(root, 'src', 'lib', 'drill', 'dbState.ts'), 'utf8');
  assert.ok(source.includes('input.pretend ? SAMPLE_DATE : null'), 'the sample date is demo-only');
  assert.ok(source.includes("const since = input.laneSince ? fmt.date(input.laneSince) : null"), 'no date fallback for the recording start');
  const copy = fs.readFileSync(path.join(root, 'src', 'lib', 'drill', 'drillCopy.ts'), 'utf8');
  assert.doesNotMatch(copy, /15 to 60/, 'the unsupported DMV cadence range is gone from the copy');
});