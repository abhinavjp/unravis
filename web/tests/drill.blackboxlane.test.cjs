const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

// BBX-5-17 / BL-103, after the wave-13 review (W13-01..W13-04) and UX-9: the Blackbox SQL lane source.
// The lane reads HOST PDH batch counters (blackbox.Sample.SQLHost...BatchRequestsPerSec), NOT the
// SQL-service DMV stream. The server sends estimated batch COUNTS with no busyMs, names the source
// "blackbox" and sends the moment its recording began (`since`). The web says, to someone who has
// never seen this screen: "Blackbox machine-wide batches per second[, read every N s] since <date>".
// The cadence is the recorder's own (config permits 5/15/30/60 s) and is only stated when the source
// sent it; otherwise the caption says the read interval is unknown. No date is ever invented.

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

const { dbState, DB_STATE_KEYS } = loadTypeScript('src/lib/drill/dbState.ts');
const { DRILL_TEXT: T, DRILL_HELP, drillCopyEntries } = loadTypeScript('src/lib/drill/drillCopy.ts');
const { lintScreenStrings } = loadTypeScript('src/lib/copy/copylint.ts');
const { deck } = loadTypeScript('src/lib/copy/deck.ts');
const model = loadTypeScript('src/lib/tracing/model.ts');
const { buildLane, cellValue, measureOf, formatAtOnce } = loadTypeScript('src/lib/drill/callLanes.ts');
const { mapRow } = loadTypeScript('src/lib/drill/callMap.ts');
const { readoutSpan } = loadTypeScript('src/lib/drill/callReadout.ts');

const fmt = { date: (iso) => 'D(' + iso.slice(0, 10) + ')', time: (iso) => 'T(' + iso.slice(11, 16) + ')' };
const WS = Date.parse('2026-11-11T10:00:00Z');
const WE = Date.parse('2026-11-11T11:00:00Z');
const SINCE = '2026-10-20T08:30:00Z';
const base = {
  laneReasonCode: '', laneHasShape: false, laneAsOf: null, laneSince: null, blockSeconds: null,
  queryStore: null, hasEvents: false, windowStart: WS, windowEnd: WE
};
const run = (over) => dbState({ ...base, ...over }, fmt);
const blackbox = (over = {}) => run({ laneReasonCode: 'blackbox', laneHasShape: true, laneSince: SINCE, ...over });

// ---- the state itself ----

test('blackbox: the server source key picks its own state', () => {
  const v = blackbox();
  assert.equal(v.key, 'blackbox');
  assert.equal(v.source, T.dbSrcBlackbox('D(2026-10-20)'));
  assert.equal(v.measure, 'batch-rate', 'the blackbox reason code is the host batch-throughput contract');
});

test('blackbox: the label names the machine-wide batch measure and the date, with no invented cadence', () => {
  const v = blackbox();
  assert.match(v.source, /^Blackbox machine-wide batches per second since /);
  assert.ok(v.source.includes('D(2026-10-20)'), 'the label carries the date the recording began: ' + v.source);
  assert.doesNotMatch(v.source, /15 to 60|every 15 s/, 'no cadence is claimed when the source sent none: ' + v.source);
  assert.equal(T.dbSrcBlackbox('20 Oct 2026'), 'Blackbox machine-wide batches per second since 20 Oct 2026');
});

test('blackbox: it says what the source is and stays calm, with the usual short label and shape', () => {
  const v = blackbox();
  assert.equal(v.message, T.dbMsgBlackbox(null));
  assert.equal(typeof v.message, 'string');
  // UX-9 raised the cap: the message defines batch, host counter and instance; it renders as a wrapping <p>.
  assert.ok(v.message.length > 0 && v.message.length < 520, 'message length ' + v.message.length);
  assert.equal(v.short, T.dbShortBlackbox(null));
  assert.ok(v.short.length > 0 && v.short.length < 60, 'short length ' + v.short.length);
  assert.equal(v.shape, 'blocks');
  assert.equal(DB_STATE_KEYS.includes('blackbox'), false, 'blackbox is a real server state, not a demo pretend switch');
});

test('blackbox: the message separates throughput from concurrency and keeps gaps unknown', () => {
  const msg = T.dbMsgBlackbox(null);
  assert.ok(msg.includes('batches per second'), msg);
  assert.ok(msg.includes('not how many ran at once or how long it took'), msg);
  assert.ok(msg.includes('unknown, not zero'), msg);
  assert.ok(msg.includes('The read interval is not recorded.'), 'an absent cadence is stated as unknown: ' + msg);
  assert.doesNotMatch(msg, /15 to 60|every 15 seconds|every 60/, 'no unconsumed DMV cadence: ' + msg);
});

// ---- UX-9: the message a first-time investigator reads defines every term it uses ----

test('UX-9: the state message defines batch, host counter and instance, and says the count covers every app', () => {
  for (const cadence of [15, null]) {
    const msg = blackbox({ laneCadenceSeconds: cadence }).message;
    assert.match(msg, /A batch is one request; it may hold several queries\./, msg);
    assert.match(msg, /host counter: SQL Server's own count of batches per second/, msg);
    assert.match(msg, /added up across every SQL Server instance on the machine/, msg);
    assert.match(msg, /an instance is one running copy of SQL Server/, msg);
    assert.match(msg, /every app, not only this one/, msg);
    assert.match(msg, /work that arrived, not how many ran at once or how long it took/, msg);
    assert.match(msg, /Gaps stay unknown, not zero\./, msg);
    assert.doesNotMatch(msg, /added up over the instances there/, 'the old undefined phrasing is gone: ' + msg);
  }
});

test('UX-9: the at-once lane copy is unchanged beside the reworded rate copy', () => {
  assert.equal(T.unitAtOnce, 'running at once');
  assert.equal(T.laneShape('4.2'), 'Height = running at once, 0 to 4.2');
  assert.equal(T.readDb('4.2'), 'Database running at once (average) 4.2');
  assert.equal(T.readDbPeak('4.2'), 'Database running at once (average), highest while it ran 4.2');
  assert.equal(T.mapAxisLine, 'Across is time. A taller column means more running at once, and each row has its own scale.');
  assert.equal(
    T.mapCaptionMore,
    'Drag the box or use Earlier and Later to look at another time. Taller means more running at once. Each row is scaled to its own busiest column, so compare a row with itself, not with other rows. Hatched means no data was recorded, not zero.'
  );
});

test('blackbox: a sent cadence of 5, 15, 30 or 60 s is the one stated, in label, message and short', () => {
  for (const cadence of [5, 15, 30, 60]) {
    const v = blackbox({ laneCadenceSeconds: cadence });
    assert.ok(v.source.includes(`read every ${cadence} s`), v.source);
    assert.ok(v.message.includes(`It reads every ${cadence} s.`), v.message);
    assert.equal(v.short, `batches/s every ${cadence} s`);
    assert.ok(v.message.length < 520, 'message length ' + v.message.length);
    assert.ok(v.short.length < 60, 'short length ' + v.short.length);
  }
});

test('blackbox: a missing or unusable cadence reads as unknown, never as a guess', () => {
  for (const cadence of [null, undefined, 0, -5, NaN, '15']) {
    const v = blackbox({ laneCadenceSeconds: cadence });
    assert.doesNotMatch(v.source, /every \d/, v.source);
    assert.ok(v.message.includes('The read interval is not recorded.'), v.message);
    assert.equal(v.short, 'batches/s, read interval unknown');
  }
});

test('blackbox: no since from the server says the recording start is unknown, never a sample date', () => {
  const v = run({ laneReasonCode: 'blackbox', laneHasShape: true });
  assert.equal(v.key, 'blackbox');
  assert.equal(v.source, T.dbSrcBlackbox(null));
  assert.ok(v.source.includes('recording start unknown'), v.source);
  assert.doesNotMatch(v.source, /11 Nov 2026|since D\(/, 'no fabricated production date: ' + v.source);
});

test('blackbox: source availability rests on recorded observations, not on drawable concurrency', () => {
  // Positive batch counts with no busyMs: concurrency cannot be drawn, yet the source is real.
  const observed = run({ laneReasonCode: 'blackbox', laneHasShape: false, laneHasObservations: true, laneSince: SINCE });
  assert.equal(observed.key, 'blackbox');
  assert.equal(observed.shape, 'blocks');
  // No valid observation at all: still never a claim, and never a zero lane.
  const empty = run({ laneReasonCode: 'blackbox', laneHasShape: true, laneHasObservations: false, laneSince: SINCE });
  assert.equal(empty.key, 'notread');
});

test('blackbox: the lane source wins over a Query Store state that says otherwise', () => {
  // The drawing shows the Blackbox blocks, so the words must not claim the lane records nothing.
  for (const queryStore of [{ state: 'off' }, { state: 'denied' }, { state: 'read-only', firstData: '2026-11-01T00:00:00Z' }]) {
    assert.equal(blackbox({ queryStore }).key, 'blackbox', JSON.stringify(queryStore));
  }
  assert.equal(blackbox({ blockSeconds: 0 }).key, 'blackbox');
});

test('blackbox: nothing drawn on the lane is not a claim, and never a zero lane', () => {
  const empty = run({ laneReasonCode: 'blackbox', laneHasShape: false, laneSince: SINCE });
  assert.notEqual(empty.key, 'blackbox');
  assert.equal(empty.key, 'notread');
  assert.equal(empty.source, T.dbSrcNone);
  assert.equal(empty.short, T.notRecorded);
  assert.equal(empty.shape, 'none');
});

test('an absent source still falls through to "Not recorded", exactly as before', () => {
  const v = run({});
  assert.equal(v.key, 'notread');
  assert.equal(v.source, 'Not recorded');
  assert.equal(v.message, T.dbMsgNotRead);
  assert.equal(v.short, T.notRecorded);
  assert.equal(v.shape, 'none');
  assert.doesNotMatch(v.source, /Blackbox/i);
  // and an unreadable Query Store row still follows the data, not the new key
  assert.equal(run({ queryStore: { state: 'unknown' } }).key, 'notread');
  assert.equal(run({ laneReasonCode: 'blackbox', laneHasShape: true, queryStore: null }).key, 'blackbox');
});

test('the other database source labels are untouched', () => {
  assert.equal(T.dbSrcLive, 'Query Store (live)');
  assert.equal(T.dbSrcFile('11 Nov 2026'), 'Query Store file imported 11 Nov 2026');
  assert.equal(T.dbSrcApi, 'Seen by the API (time inside the database)');
  assert.equal(T.dbSrcLate('10:30'), 'Query Store (live), nothing before 10:30');
  assert.equal(T.dbSrcReadOnly, 'Query Store (live), read-only');
  assert.equal(T.dbSrcEvents, 'SQL Server event files, markers only');
  assert.equal(T.dbSrcNone, 'Not recorded');
  // the states they belong to still read the same
  assert.equal(run({ laneReasonCode: 'held-sql', laneHasShape: true, blockSeconds: 1800, queryStore: { state: 'on', firstData: '2026-11-01T00:00:00Z' } }).source, T.dbSrcLive);
  assert.equal(run({ laneReasonCode: 'api-seen', laneHasShape: true }).source, T.dbSrcApi);
});

test('a Query Store file with no readable import date says so, outside the demo path', () => {
  const v = run({ laneReasonCode: 'qs-file', laneHasShape: true });
  assert.equal(v.key, 'qsfile');
  assert.equal(v.source, 'Query Store file, import date not recorded');
  assert.doesNotMatch(v.source, /11 Nov 2026/);
  assert.doesNotMatch(v.message, /11 Nov 2026/);
  // the demonstration date stays behind the explicit demo switch
  assert.equal(run({ pretend: 'qsfile' }).source, T.dbSrcFile('11 Nov 2026'));
});

// ---- copy registration: a function key is not auto-counted, so the SAMPLE entry and the fns row must exist ----

test('dbSrcBlackbox is registered in the lint entries, and the count guard holds', () => {
  const entries = drillCopyEntries();
  const ids = entries.map((e) => e.id);
  assert.ok(ids.includes('drill.dbSrcBlackbox'), 'drill.dbSrcBlackbox is linted');
  assert.ok(ids.includes('drill.dbMsgBlackbox'), 'drill.dbMsgBlackbox is linted');
  assert.ok(ids.includes('drill.dbShortBlackbox'), 'drill.dbShortBlackbox is linted');
  assert.equal(ids.filter((id) => id === 'drill.dbSrcBlackbox').length, 1, 'one entry per key');
  assert.equal(new Set(ids).size, ids.length, 'entry ids are unique');
  assert.equal(entries.length, Object.keys(T).length + Object.keys(DRILL_HELP).length);
  const hit = entries.find((e) => e.id === 'drill.dbSrcBlackbox');
  assert.match(hit.text, /^Blackbox machine-wide batches per second, read every 15 s since \S+ \S+ \d{4}$/);
});

test('no help key was added: the help deck keeps its fixed 26 keys', () => {
  assert.equal(Object.keys(DRILL_HELP).length, 26);
  assert.equal(Object.keys(DRILL_HELP).filter((k) => /blackbox/i.test(k)).length, 0);
});

test('the new copy passes the copy lint with the rest of the drill deck', () => {
  assert.deepEqual(lintScreenStrings(drillCopyEntries(), deck.terms), []);
});

test('the new copy uses no banned wording and names no cause', () => {
  const banned = /because|due to|caus|waiting on|blocked by|led to|results? in|responsible for|leak|starv|saturat|proven/i;
  const texts = [T.dbSrcBlackbox('20 Oct 2026', 15), T.dbSrcBlackbox(null), T.dbMsgBlackbox(15), T.dbMsgBlackbox(null), T.dbShortBlackbox(15), T.dbShortBlackbox(null)];
  for (const text of texts) {
    assert.equal(typeof text, 'string');
    assert.doesNotMatch(text, banned, text);
  }
});

// ---- the wire: the server's `since` reaches the screen ----

test('parseTracing keeps the server since on the lane, and drops one it cannot read', () => {
  const WINDOW = {
    start: '2026-11-11T10:00:00Z', end: '2026-11-11T11:00:00Z',
    axisStart: '2026-11-11T10:00:00Z', axisEnd: '2026-11-11T11:00:00Z', bucketSeconds: 60
  };
  const lane = (over) => ({
    key: 'sql', label: 'SQL Server', state: 'events', origin: 'case', asOf: '2026-11-11T11:05:00Z',
    complete: true, truncated: false, reason: '', scanned: 10, hidden: 0, sources: [],
    buckets: [{ start: '2026-11-11T10:00:00Z', count: 3 }], ...over
  });
  const withSince = model.parseTracing({ window: WINDOW, lanes: [lane({ reasonCode: 'blackbox', since: SINCE })], allLanesStored: true });
  assert.equal(withSince.lanes[0].since, SINCE);
  assert.equal(withSince.lanes[0].reasonCode, 'blackbox');
  const noSince = model.parseTracing({ window: WINDOW, lanes: [lane()], allLanesStored: true });
  assert.equal('since' in noSince.lanes[0], false);
  const badSince = model.parseTracing({ window: WINDOW, lanes: [lane({ since: 'not a date' })], allLanesStored: true });
  assert.equal('since' in badSince.lanes[0], false);
});

test('the screen hands the server since and cadence to the database state, and adds no new polling', () => {
  const source = fs.readFileSync(path.join(root, 'src', 'components', 'SlowCallScreen.svelte'), 'utf8');
  assert.match(source, /laneSince:/);
  assert.match(source, /sqlLane\?\.since/);
  assert.match(source, /laneCadenceSeconds: sqlLane\?\.cadenceSeconds \?\? null/);
  assert.match(source, /laneHasObservations:/);
  assert.doesNotMatch(source, /setInterval|EventSource|new WebSocket/);
  assert.equal((source.match(/dbState\(/g) ?? []).length, 1, 'one database state call');
});

// ---- UX-1: a Blackbox lane is read in its own measure everywhere the call is described ----
// The server sends batch COUNTS with no busy time (internal/server/tracing_blackbox.go builds its buckets
// with tracingBuckets(axis, perCellInt, nil)), so every cell has a rate and atOnce null. The overview strip,
// the "while this call ran" readout and the inspector must all read that rate: a lane that recorded counts
// never says "no data" or "not recorded", and a rate is never printed as a number running at once.

const T0 = Date.parse('2026-11-11T10:00:00Z');
const MIN = 60000;
const SPAN = 15 * MIN;
const iso = (ms) => new Date(ms).toISOString();
const WINDOW = { start: iso(T0), end: iso(T0 + SPAN), axisStart: iso(T0), axisEnd: iso(T0 + SPAN), bucketSeconds: 60 };
const OVER = { start: T0, end: T0 + SPAN };

// The current server's SQL lane: counts only, no busyMs, no blockSeconds, no events.
function bbWire(buckets, over = {}) {
  return {
    window: WINDOW,
    allLanesStored: true,
    lanes: [{
      key: 'sql', label: 'SQL Server', state: 'events', reason: '', origin: 'case', asOf: iso(T0 + SPAN),
      complete: true, truncated: false, scanned: 90000, hidden: 0,
      sources: [{ sourceId: 'blackbox', label: 'Blackbox sampling every 15 s since 20 Oct 2026' }],
      reasonCode: 'blackbox', since: SINCE, buckets, events: [], ...over
    }]
  };
}
const counts = (list) => list.map((c, i) => ({ start: iso(T0 + i * MIN), count: c }));
const bbParsed = (buckets, over) => model.parseTracing(bbWire(buckets, over)).lanes[0];
// The lane the screen builds from that answer (toLaneIn for a Blackbox SQL lane).
function bbLane(buckets, over) {
  const lane = bbParsed(buckets, over);
  return buildLane({
    key: 'sql', state: lane.state, reasonCode: lane.reasonCode,
    measure: lane.reasonCode === 'blackbox' ? 'batch-rate' : undefined,
    blockSeconds: lane.blockSeconds, buckets: lane.buckets, events: lane.events ?? [], hidden: lane.hidden
  }, 'sql', WINDOW, T0, T0 + SPAN);
}

// ---- the real component code, cut out and run (a .svelte component cannot be mounted under node) ----
const readLf = (rel) => fs.readFileSync(path.join(root, rel), 'utf8').replace(/\r\n/g, '\n');
const screenSrc = readLf('src/components/SlowCallScreen.svelte');
const inspectorSrc = readLf('src/components/CallInspector.svelte');
const minimapSrc = readLf('src/components/CallMinimap.svelte');
const toJs = (src) => ts.transpileModule(src, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText;

// The argument list of the call whose opening bracket sits at `open`; strings and comments are skipped.
function callArgs(src, open) {
  assert.equal(src[open], '(', 'the bracket is where the helper expects it');
  let depth = 0;
  for (let i = open; i < src.length; i += 1) {
    const ch = src[i];
    if (ch === '/' && src[i + 1] === '/') { i = src.indexOf('\n', i); if (i < 0) break; continue; }
    if (ch === '/' && src[i + 1] === '*') { i = src.indexOf('*/', i + 2) + 1; continue; }
    if (ch === '"' || ch === "'" || ch === '`') {
      const quote = ch;
      i += 1;
      while (i < src.length && src[i] !== quote) { if (src[i] === '\\') i += 1; i += 1; }
      continue;
    }
    if (ch === '(') depth += 1;
    else if (ch === ')') { depth -= 1; if (depth === 0) return src.slice(open + 1, i); }
  }
  throw new Error('unbalanced call at ' + open);
}

// The screen's own toLaneIn and mapRows derived, run against the real lane and strip helpers.
function screenMapRows(resp, savedDay = null) {
  const from = screenSrc.indexOf('function toLaneIn(');
  const to = screenSrc.indexOf('const SQL_EVENT_SOURCES');
  assert.ok(from >= 0 && to > from, 'toLaneIn is in the screen');
  const marker = 'const mapRows = $derived.by';
  const at = screenSrc.indexOf(marker);
  assert.ok(at >= 0, 'mapRows is a derived in the screen');
  const body = callArgs(screenSrc, at + marker.length);
  const js = toJs(`function make(savedDay, overviewResp, over, buildLane, mapRow, cellValue) {
${screenSrc.slice(from, to)}
  const rows = ${body};
  return rows();
}`);
  return new Function(`${js}\nreturn make;`)()(savedDay, resp, OVER, buildLane, mapRow, cellValue);
}

// The inspector's own database sentence, run with the real copy and number format.
function inspectorDbLine(whileRan, db) {
  const at = inspectorSrc.indexOf('const dbLine = $derived');
  assert.ok(at >= 0, 'dbLine is a derived in the inspector');
  const arg = callArgs(inspectorSrc, inspectorSrc.indexOf('(', at));
  const js = toJs(`function make(DRILL_TEXT, formatAtOnce, whileRan, db) {
  const value = ${arg};
  return typeof value === 'function' ? value() : value;
}`);
  return new Function(`${js}\nreturn make;`)()(T, formatAtOnce, whileRan, db);
}

// The minimap's own peak and hover text, run with the real copy. The clock is fixed UTC so no test
// depends on the machine's time zone; only the words and the number are under test.
function minimapText(sqlMeasure) {
  const after = minimapSrc.indexOf('function onStripClick(');
  assert.ok(after >= 0, 'onStripClick is in the minimap');
  const start = minimapSrc.indexOf('\n  const ', after);
  const end = minimapSrc.indexOf('</script>', start);
  assert.ok(start > after && end > start, 'the minimap text helpers sit at the end of the script');
  const js = toJs(`function make(DRILL_TEXT, formatAtOnce, clock, LANE_NAME, sqlMeasure) {
${minimapSrc.slice(start, end)}
  const valueOf = typeof atOnceOf === 'function' ? atOnceOf : valueAt;
  return { peakText, cellTitle, valueOf };
}`);
  const clock = (ms) => new Date(ms).toISOString().slice(11, 16);
  const LANE_NAME = { audit: T.laneApi, iis: T.laneIis, sql: T.laneDb };
  return new Function(`${js}\nreturn make;`)()(T, formatAtOnce, clock, LANE_NAME, sqlMeasure);
}

const ran = (db) => ({ stepStart: T0, api: null, iis: null, db, isThisCall: true });
const readCell = (rate, atOnce, extra = {}) => ({ start: T0, end: T0 + MIN, atOnce, rate, p95Ms: null, refused: null, ...extra });
const BB_STATE = { key: 'blackbox', measure: 'batch-rate', shape: 'blocks' };
const QS_STATE = { key: 'qs', measure: 'at-once', shape: 'blocks' };

test('UX-1: a count-only Blackbox lane draws its own measure in the overview strip', () => {
  const buckets = counts([6000, 0, 3000, 0, 120]);
  const lane = bbLane(buckets);
  assert.equal(lane.measure, 'batch-rate');
  assert.equal(measureOf({ reasonCode: 'blackbox' }), 'batch-rate');
  for (const cell of lane.cells) assert.equal(cell.atOnce, null, 'the server sent no busy time');
  // The mapping the strip needs: one number per cell in the lane's own measure.
  const drawn = mapRow('sql', lane.cells.map((c) => ({ start: c.start, end: c.end, atOnce: cellValue(c, lane.measure) })));
  assert.notEqual(drawn.peak, null, 'a lane that recorded counts has a busiest column');
  assert.deepEqual(drawn.peak, { atOnce: 100, start: T0 });
  // And the screen's real mapRows must do exactly that, not hand the raw cells to the strip.
  const rows = screenMapRows(model.parseTracing(bbWire(buckets)));
  const sql = rows.find((r) => r.key === 'sql');
  assert.notEqual(sql.peak, null, 'the strip says "no data" for a lane that recorded counts');
  assert.deepEqual(sql.peak, drawn.peak);
  assert.deepEqual(sql.columns, drawn.columns);
  assert.equal(sql.columns[0].height, 1);
  assert.equal(sql.columns[1].height, 0, 'a genuine zero is a real column, not a gap');
  assert.equal(sql.columns[5].height, null, 'a minute with no bucket stays hatched');
  assert.equal(rows.find((r) => r.key === 'audit').peak, null, 'a lane with no answer is still empty');
});

test('UX-1: the strip model itself stays measure-agnostic', () => {
  const lane = bbLane(counts([6000]));
  const raw = mapRow('sql', lane.cells); // the model draws the number it is given, and no other
  assert.equal(raw.peak, null);
  for (const col of raw.columns) assert.equal(col.height, null);
});

test('UX-1: the strip words for the Database row name batches per second, and only for that row', () => {
  const row = { key: 'sql', columns: [], peak: { atOnce: 100, start: T0 } };
  const rate = minimapText('batch-rate');
  assert.equal(rate.peakText(row), T.mapPeakRate('100', '10:00'));
  assert.match(rate.peakText(row), /batches/);
  assert.equal(rate.cellTitle(row, T0, 1, 100), T.mapCellRate('Database', '100', '10:00'));
  assert.match(rate.cellTitle(row, T0, 1, 100), /batches per second around/);
  assert.equal(rate.cellTitle(row, T0, null, null), T.mapCellNone('Database', '10:00'), 'a gap is still a gap');
  assert.equal(rate.peakText({ key: 'sql', columns: [], peak: null }), T.mapNoData);
  assert.equal(rate.valueOf(row, 0.5), 50, 'the height is turned back into the lane number');
  assert.equal(rate.valueOf(row, null), null);
  // A concurrency row never takes the throughput words, even beside a throughput Database row.
  const audit = { key: 'audit', columns: [], peak: { atOnce: 4, start: T0 } };
  assert.equal(rate.peakText(audit), T.mapPeak('4.0', '10:00'));
  assert.equal(rate.cellTitle(audit, T0, 1, 4), T.mapCell('API calls', '4.0', '10:00'));
  // And an at-once Database lane reads exactly as it did before.
  const once = minimapText('at-once');
  assert.equal(once.peakText(row), T.mapPeak('100', '10:00'));
  assert.equal(once.cellTitle(row, T0, 1, 100), T.mapCell('Database', '100', '10:00'));
});

test('UX-1: the inspector prints the highest batches per second while the call ran', () => {
  assert.equal(inspectorDbLine(ran(readCell(100, null)), BB_STATE), T.readDbRatePeak('100'));
  assert.equal(inspectorDbLine(ran(readCell(0, null)), BB_STATE), T.readDbRatePeak('0'), 'a genuine zero is a number');
  assert.equal(inspectorDbLine(ran(readCell(null, null)), BB_STATE), T.readDbNone);
  assert.equal(inspectorDbLine(ran(null), BB_STATE), T.readDbNone);
  assert.equal(inspectorDbLine(null, BB_STATE), T.readDbNone);
  // A concurrency lane keeps its own words, and a rate is never printed as one.
  assert.equal(inspectorDbLine(ran(readCell(null, 4.2)), QS_STATE), T.readDbPeak('4.2'));
  assert.equal(inspectorDbLine(ran(readCell(null, null)), QS_STATE), T.readDbNone);
  assert.equal(inspectorDbLine(ran(readCell(100, null)), QS_STATE), T.readDbNone, 'a rate is not a concurrency number');
  // Nothing drawn on the lane is never a number.
  assert.equal(inspectorDbLine(ran(readCell(100, null)), { ...BB_STATE, shape: 'none' }), T.readDbNone);
});

test('UX-1: the span readout takes the highest rate, not the first step the call touched', () => {
  // Minutes 0..2 were not recorded; minute 3 saw 50 batches/s and minute 4 saw 200.
  const lane = bbLane([{ start: iso(T0 + 3 * MIN), count: 3000 }, { start: iso(T0 + 4 * MIN), count: 12000 }]);
  const r = readoutSpan({ audit: [], iis: [], sql: lane.cells }, { startMs: T0 + 2 * MIN, endMs: T0 + 4 * MIN + 5000 });
  assert.notEqual(r.db, null);
  assert.notEqual(r.db.rate, null, 'counts exist in the span, so this is not "not recorded"');
  assert.equal(r.db.rate, 200, 'the highest rate in any step the call touched');
  assert.equal(r.db.start, T0 + 4 * MIN);
  assert.equal(r.db.atOnce, null, 'a rate never becomes a concurrency number');
  assert.equal(inspectorDbLine(r, BB_STATE), T.readDbRatePeak('200'));
  // The first touched step on its own is unknown, and stays unknown.
  const firstOnly = readoutSpan({ audit: [], iis: [], sql: lane.cells }, { startMs: T0 + 2 * MIN, endMs: T0 + 2 * MIN });
  assert.equal(firstOnly.db.rate, null);
  assert.equal(inspectorDbLine(firstOnly, BB_STATE), T.readDbNone);
  // A call with no end is read at its start.
  const atStart = readoutSpan({ audit: [], iis: [], sql: lane.cells }, { startMs: T0 + 3 * MIN, endMs: T0 + 3 * MIN });
  assert.equal(atStart.db.rate, 50);
  // Nothing under the call is still null, never a zero lane.
  assert.equal(readoutSpan({ audit: [], iis: [], sql: lane.cells }, { startMs: T0 + 9 * MIN, endMs: T0 + 10 * MIN }).db.rate, null);
});

test('UX-1: a rate is never compared with a number running at once', () => {
  // One step measured busy time as well as counts (1000 at once, 10 batches/s); the next only counts (200 batches/s).
  const lane = bbLane([
    { start: iso(T0 + 2 * MIN), count: 600, busyMs: 60000000, p95Ms: 900 },
    { start: iso(T0 + 3 * MIN), count: 12000, p95Ms: 300 }
  ]);
  const r = readoutSpan({ audit: [], iis: [], sql: lane.cells }, { startMs: T0 + 2 * MIN, endMs: T0 + 3 * MIN + 1000 });
  assert.equal(r.db.rate, 200, 'the peak is the highest rate, not the biggest concurrency number');
  assert.equal(r.db.start, T0 + 3 * MIN);
  assert.equal(r.db.p95Ms, 900, 'the slowest 5% is still the highest in the span');
  assert.equal(inspectorDbLine(r, BB_STATE), T.readDbRatePeak('200'));
  // When only the step that measured both is touched, both of its own numbers are reported.
  const one = readoutSpan({ audit: [], iis: [], sql: lane.cells }, { startMs: T0 + 2 * MIN, endMs: T0 + 2 * MIN + 1000 });
  assert.equal(one.db.rate, 10);
  assert.equal(one.db.atOnce, 1000);
});

test('UX-1: a concurrency lane keeps the readout and the sentence it had before', () => {
  const busy = (list) => list.map((b, i) => ({ start: iso(T0 + i * MIN), count: 0, busyMs: b }));
  const lane = buildLane({ key: 'sql', state: 'events', reasonCode: 'held-sql', buckets: busy([60000, 300000, 120000]), events: [] }, 'sql', WINDOW, T0, T0 + SPAN);
  assert.equal(lane.measure, 'at-once');
  const r = readoutSpan({ audit: [], iis: [], sql: lane.cells }, { startMs: T0, endMs: T0 + 2 * MIN });
  assert.equal(r.db.atOnce, 5, 'busy 300000 ms over a 60 s step is 5 running at once');
  assert.equal(r.db.rate, null);
  assert.equal(inspectorDbLine(r, QS_STATE), T.readDbPeak('5.0'));
  const rows = screenMapRows({ window: WINDOW, lanes: [{ key: 'sql', state: 'events', reasonCode: 'held-sql', buckets: busy([60000, 300000, 120000]), events: [] }] });
  assert.deepEqual(rows.find((x) => x.key === 'sql').peak, { atOnce: 5, start: T0 + MIN });
});

test('UX-1: the new rate copy is registered, linted and in plain words', () => {
  const ids = drillCopyEntries().map((e) => e.id);
  for (const key of ['readDbRatePeak', 'mapPeakRate', 'mapCellRate', 'mapAxisLineRate', 'mapCaptionMoreRate']) {
    assert.ok(ids.includes('drill.' + key), key + ' is linted');
    assert.equal(ids.filter((id) => id === 'drill.' + key).length, 1, 'one entry per key');
  }
  assert.equal(T.readDbRatePeak('100'), 'Database batches per second, highest while it ran 100');
  assert.equal(T.mapCellRate('Database', '100', '10:14'), 'Database: 100 batches per second around 10:14');
  assert.match(T.mapPeakRate('100', '10:14'), /batches/);
  assert.match(T.mapAxisLineRate, /batches per second/);
  assert.match(T.mapCaptionMoreRate, /batches per second/);
  // Word-boundary anchored: "scaled to" must not read as the causal phrase "led to".
  const banned = /\b(?:because|due to|caus\w*|waiting on|blocked by|led to|results? in|responsible for|leak\w*|starv\w*|saturat\w*|proven)\b/i;
  const texts = [T.readDbRatePeak('100'), T.mapPeakRate('100', '10:14'), T.mapCellRate('Database', '100', '10:14'), T.mapAxisLineRate, T.mapCaptionMoreRate];
  for (const text of texts) assert.doesNotMatch(text, banned, text);
  assert.deepEqual(lintScreenStrings(drillCopyEntries(), deck.terms), []);
});

test('UX-1: the screen and the panels are wired to the measure, with no new read', () => {
  assert.match(screenSrc, /cellValue\(c, lane\.measure\)/, 'the strip cells carry the lane measure');
  assert.match(screenSrc, /<CallMinimap[\s\S]*?sqlMeasure=\{/, 'the minimap is told the SQL measure');
  assert.match(minimapSrc, /sqlMeasure: LaneMeasure/, 'the minimap takes the measure as a prop');
  assert.match(inspectorSrc, /db\.measure === 'batch-rate'/);
  assert.match(inspectorSrc, /DRILL_TEXT\.readDbRatePeak\(/);
  assert.doesNotMatch(screenSrc, /setInterval|EventSource|new WebSocket/);
});

// ---- PERF-3: a clipped edge bucket divides its count by the seconds it covers ----
// The server cuts the first and last bucket to the asked window (tracingAxis.Bucket) and sends
// `end` + `clipped` on every bucket. 3,000 batches over the 30 s a clipped 60 s bucket covers is
// 100 batches/s, not 50. A missing or unreadable end keeps the full step as the divisor, so old
// fixtures (no end, no clipped) read exactly as before — never NaN or Infinity.

test('PERF-3: a clipped 60 s bucket covering 30 s reads 100 batches/s, not 50', () => {
  const buckets = [
    { start: iso(T0), end: iso(T0 + 30000), clipped: true, count: 3000 },
    { start: iso(T0 + MIN), end: iso(T0 + 2 * MIN), clipped: false, count: 3000 },
    { start: iso(T0 + 2 * MIN), count: 3000 } // no end, no clipped: the old fixture shape
  ];
  const lane = buildLane({ key: 'sql', state: 'events', reasonCode: 'blackbox', buckets, events: [] }, 'sql', WINDOW, T0, T0 + SPAN);
  assert.equal(lane.cells[0].rate, 100, '3000 batches over the covered 30 s is 100/s');
  assert.equal(lane.cells[1].rate, 50, 'an unclipped full bucket keeps the step divisor');
  assert.equal(lane.cells[2].rate, 50, 'a bucket with no clipped flag is unchanged');
  assert.deepEqual(lane.peak, { atOnce: 100, start: T0 }, 'the peak reads the corrected rate');
});

test('PERF-3: a clipped trailing edge and a clipped full-width bucket both read their true rate', () => {
  const buckets = [
    { start: iso(T0), end: iso(T0 + MIN), clipped: true, count: 6000 }, // flagged clipped but covers the whole step
    { start: iso(T0 + 14 * MIN), end: iso(T0 + 14 * MIN + 15000), clipped: true, count: 300 } // only the last 15 s
  ];
  const lane = buildLane({ key: 'sql', state: 'events', reasonCode: 'blackbox', buckets, events: [] }, 'sql', WINDOW, T0, T0 + SPAN);
  assert.equal(lane.cells[0].rate, 100);
  assert.equal(lane.cells[14].rate, 20, '300 batches over the covered 15 s is 20/s');
});

test('PERF-3: a missing, unreadable or empty covered span falls back to the step, never NaN or Infinity', () => {
  const buckets = [
    { start: iso(T0), clipped: true, count: 3000 }, // clipped but no end sent
    { start: iso(T0 + MIN), end: 'not a date', clipped: true, count: 3000 },
    { start: iso(T0 + 2 * MIN), end: iso(T0 + 2 * MIN), clipped: true, count: 3000 }, // zero coverage
    { start: iso(T0 + 3 * MIN), end: iso(T0 + 3 * MIN - 1000), clipped: true, count: 3000 } // end before start
  ];
  const lane = buildLane({ key: 'sql', state: 'events', reasonCode: 'blackbox', buckets, events: [] }, 'sql', WINDOW, T0, T0 + SPAN);
  for (let i = 0; i < 4; i += 1) {
    assert.equal(lane.cells[i].rate, 50, 'bucket ' + i + ' keeps the full-step divisor');
    assert.ok(Number.isFinite(lane.cells[i].rate), 'bucket ' + i + ' is a finite number');
  }
});

test('PERF-3: the at-once busy-time path keeps the full step divisor (deliberate scope limit)', () => {
  // PERF-3 corrects COUNT rates only. The busyMs concurrency path has the same clipped-edge
  // pattern but is deliberately left as it was; this pins that limit so the change is visible.
  const buckets = [{ start: iso(T0), end: iso(T0 + 30000), clipped: true, count: 3000, busyMs: 30000 }];
  const lane = buildLane({ key: 'sql', state: 'events', reasonCode: 'blackbox', buckets, events: [] }, 'sql', WINDOW, T0, T0 + SPAN);
  assert.equal(lane.cells[0].rate, 100, 'the count rate uses the covered seconds');
  assert.equal(lane.cells[0].atOnce, 0.5, 'busyMs still divides by the full step — unchanged in PERF-3');
});

// The screen's real toLaneIn, cut out and run against a parsed lane (same extraction as screenMapRows).
function screenToLaneIn(lane, savedDay = null) {
  const from = screenSrc.indexOf('function toLaneIn(');
  const to = screenSrc.indexOf('const SQL_EVENT_SOURCES');
  assert.ok(from >= 0 && to > from, 'toLaneIn is in the screen');
  const js = toJs(`function make(savedDay, lane) {
${screenSrc.slice(from, to)}
  return toLaneIn(lane);
}`);
  return new Function(`${js}\nreturn make;`)()(savedDay, lane);
}

test('PERF-3 end to end: a server-shaped clipped edge bucket reads 100 batches/s through parseTracing, toLaneIn and laneCells', () => {
  // The real chain the app runs: server payload -> parseTracing (parseBucket) -> the screen's own
  // toLaneIn -> buildLane/laneCells. The first bucket is cut to the asked window: 30 s of a 60 s step.
  const buckets = [
    { start: iso(T0), end: iso(T0 + 30000), clipped: true, count: 3000 },
    { start: iso(T0 + MIN), end: iso(T0 + 2 * MIN), clipped: false, count: 3000 },
    { start: iso(T0 + 2 * MIN), end: iso(T0 + 3 * MIN), clipped: false, count: 0 }
  ];
  const parsed = model.parseTracing(bbWire(buckets)).lanes[0];
  assert.equal(parsed.buckets[0].end, iso(T0 + 30000), 'the parser keeps the bucket end the server sent');
  assert.equal(parsed.buckets[0].clipped, true, 'the parser keeps the clipped flag the server sent');
  const lane = buildLane(screenToLaneIn(parsed), 'sql', WINDOW, T0, T0 + SPAN);
  assert.equal(lane.cells[0].rate, 100, '3000 batches over the covered 30 s is 100/s, end to end');
  assert.equal(lane.cells[1].rate, 50, 'an unclipped bucket is unchanged');
  assert.equal(lane.cells[2].rate, 0, 'a genuine zero stays zero');
  assert.deepEqual(lane.peak, { atOnce: 100, start: T0 });
});

test('PERF-3 end to end: an unreadable end or clipped flag is dropped by the parser and the rate falls back to the step', () => {
  const buckets = [
    { start: iso(T0), end: 'not a date', clipped: true, count: 3000 },
    { start: iso(T0 + MIN), clipped: true, count: 3000 }, // no end sent
    { start: iso(T0 + 2 * MIN), end: iso(T0 + 3 * MIN), count: 3000 }, // no clipped flag: old shape
    { start: iso(T0 + 3 * MIN), end: 123, clipped: 'yes', count: 3000 } // wrong types
  ];
  const parsed = model.parseTracing(bbWire(buckets)).lanes[0];
  assert.equal('end' in parsed.buckets[0], false, 'an unparseable end is dropped');
  assert.equal(parsed.buckets[0].clipped, true);
  assert.equal('end' in parsed.buckets[1], false);
  assert.equal('clipped' in parsed.buckets[2], false, 'a missing clipped flag stays off');
  assert.equal('end' in parsed.buckets[3], false, 'a non-string end is dropped');
  assert.equal('clipped' in parsed.buckets[3], false, 'a non-boolean clipped is dropped');
  const lane = buildLane(screenToLaneIn(parsed), 'sql', WINDOW, T0, T0 + SPAN);
  for (let i = 0; i < 4; i += 1) {
    assert.equal(lane.cells[i].rate, 50, 'bucket ' + i + ' keeps the full-step divisor');
    assert.ok(Number.isFinite(lane.cells[i].rate), 'bucket ' + i + ' is finite, never NaN or Infinity');
  }
});

test('UX-1: end to end — counts on the wire become batches/s in the strip, the readout and the inspector', () => {
  const buckets = counts([6000, 0, 3000, 0, 120]);
  const lane = bbLane(buckets);
  // The real database state for that lane, not a stub: it decides the measure the inspector reads.
  const view = dbState({
    ...base, laneReasonCode: 'blackbox', laneHasShape: lane.hasShape, laneHasObservations: lane.hasShape,
    laneSince: SINCE, blockSeconds: lane.blockSeconds, windowStart: T0, windowEnd: T0 + SPAN
  }, fmt);
  assert.equal(view.key, 'blackbox');
  assert.equal(view.measure, 'batch-rate');
  const r = readoutSpan({ audit: [], iis: [], sql: lane.cells }, { startMs: T0, endMs: T0 + 4 * MIN + 5000 });
  assert.equal(inspectorDbLine(r, view), T.readDbRatePeak('100'));
  // The strip the screen draws, read with the measure the same state reports.
  const sql = screenMapRows(model.parseTracing(bbWire(buckets))).find((x) => x.key === 'sql');
  const mm = minimapText(view.measure);
  assert.equal(mm.peakText(sql), T.mapPeakRate('100', '10:00'));
  const col = sql.columns[0];
  assert.equal(mm.cellTitle(sql, col.start, col.height, mm.valueOf(sql, col.height)), T.mapCellRate('Database', '100', '10:00'));
  const gap = sql.columns[5];
  assert.equal(gap.height, null);
  assert.equal(mm.cellTitle(sql, gap.start, null, null), T.mapCellNone('Database', '10:05'));
});
