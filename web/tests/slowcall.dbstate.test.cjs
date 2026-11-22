const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

// Slow call screen: the database lane state model (which of nine states, and the words for it).

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
const { DRILL_TEXT: T, drillCopyEntries } = loadTypeScript('src/lib/drill/drillCopy.ts');
const { lintScreenStrings } = loadTypeScript('src/lib/copy/copylint.ts');
const { deck } = loadTypeScript('src/lib/copy/deck.ts');

const fmt = { date: (iso) => 'D(' + iso.slice(0, 10) + ')', time: (iso) => 'T(' + iso.slice(11, 16) + ')' };
const WS = Date.parse('2026-11-11T10:00:00Z');
const WE = Date.parse('2026-11-11T11:00:00Z');
const base = {
  laneReasonCode: '', laneHasShape: false, laneAsOf: null, blockSeconds: null,
  queryStore: null, hasEvents: false, windowStart: WS, windowEnd: WE
};
const run = (over) => dbState({ ...base, ...over }, fmt);
const qsOn = { state: 'on', firstData: '2026-11-01T00:00:00Z' };

test('the nine keys are exported in order', () => {
  assert.deepEqual([...DB_STATE_KEYS], ['qs', 'qsfile', 'api', 'off', 'ro', 'late', 'denied', 'events', 'notread']);
});

test('qs: live Query Store with the block size from the lane', () => {
  const v = run({ laneReasonCode: 'held-sql', laneHasShape: true, blockSeconds: 1800, queryStore: qsOn });
  assert.equal(v.key, 'qs');
  assert.equal(v.source, T.dbSrcLive);
  assert.equal(v.message, T.dbMsgLive(30));
  assert.equal(v.short, T.laneBlocks(30));
  assert.equal(v.shape, 'blocks');
});

test('qs: layered-sql counts the same, and a missing block size reads as 15 minutes', () => {
  const v = run({ laneReasonCode: 'layered-sql', laneHasShape: true, queryStore: qsOn });
  assert.equal(v.key, 'qs');
  assert.equal(v.short, T.laneBlocks(15));
});

test('qsfile: an imported file shows the import date', () => {
  const v = run({ laneReasonCode: 'qs-file', laneHasShape: true, laneAsOf: '2026-11-10T08:00:00Z', blockSeconds: 900 });
  assert.equal(v.key, 'qsfile');
  assert.equal(v.source, T.dbSrcFile('D(2026-11-10)'));
  assert.equal(v.message, T.dbMsgFile('D(2026-11-10)'));
  assert.equal(v.short, T.laneBlocks(15));
  assert.equal(v.shape, 'blocks');
});

test('api: seen by the API only', () => {
  const v = run({ laneReasonCode: 'api-seen', laneHasShape: true });
  assert.equal(v.key, 'api');
  assert.equal(v.source, T.dbSrcApi);
  assert.equal(v.message, T.dbMsgApi);
  assert.equal(v.short, T.laneApiOnly);
  assert.equal(v.shape, 'apiseen');
});

test('off: Query Store is switched off', () => {
  const v = run({ queryStore: { state: 'off' } });
  assert.equal(v.key, 'off');
  assert.equal(v.source, T.dbSrcNone);
  assert.equal(v.message, T.dbMsgOff);
  assert.equal(v.short, T.dbShortOff);
  assert.equal(v.shape, 'none');
});

test('off with imported events gives events', () => {
  const v = run({ queryStore: { state: 'off' }, hasEvents: true });
  assert.equal(v.key, 'events');
  assert.equal(v.source, T.dbSrcEvents);
  assert.equal(v.message, T.dbMsgEvents);
  assert.equal(v.short, T.dbShortEvents);
  assert.equal(v.shape, 'markers');
});

test('ro: read-only Query Store', () => {
  const v = run({ laneReasonCode: 'held-sql', laneHasShape: true, blockSeconds: 900, queryStore: { state: 'read-only', firstData: '2026-11-01T00:00:00Z' } });
  assert.equal(v.key, 'ro');
  assert.equal(v.source, T.dbSrcReadOnly);
  assert.equal(v.message, T.dbMsgReadOnly);
  assert.equal(v.short, T.laneBlocks(15));
  assert.equal(v.shape, 'blocks');
});

test('late: only when firstData is after the window start', () => {
  const late = run({ laneReasonCode: 'held-sql', laneHasShape: true, queryStore: { state: 'on', firstData: '2026-11-11T10:30:00Z' } });
  assert.equal(late.key, 'late');
  assert.equal(late.source, T.dbSrcLate('T(10:30)'));
  assert.equal(late.message, T.dbMsgLate('T(10:30)'));
  assert.equal(late.short, T.dbShortLate);
  assert.equal(late.shape, 'blocks');
  const exact = run({ laneReasonCode: 'held-sql', laneHasShape: true, queryStore: { state: 'on', firstData: '2026-11-11T10:00:00Z' } });
  assert.equal(exact.key, 'qs');
  const early = run({ laneReasonCode: 'held-sql', laneHasShape: true, queryStore: { state: 'on', firstData: '2026-11-11T09:00:00Z' } });
  assert.equal(early.key, 'qs');
  const none = run({ laneReasonCode: 'held-sql', laneHasShape: true, queryStore: { state: 'on', firstData: null } });
  assert.equal(none.key, 'qs');
  const bad = run({ laneReasonCode: 'held-sql', laneHasShape: true, queryStore: { state: 'on', firstData: 'not a date' } });
  assert.equal(bad.key, 'qs');
});

test('late wins over read-only when both apply', () => {
  const v = run({ laneReasonCode: 'held-sql', laneHasShape: true, queryStore: { state: 'read-only', firstData: '2026-11-11T10:30:00Z' } });
  assert.equal(v.key, 'late');
});

test('denied: the saved login may not read Query Store, and wins over events and shape', () => {
  const v = run({ queryStore: { state: 'denied' }, hasEvents: true, laneReasonCode: 'held-sql', laneHasShape: true });
  assert.equal(v.key, 'denied');
  assert.equal(v.source, T.dbSrcNone);
  assert.equal(v.message, T.dbMsgDenied);
  assert.equal(v.short, T.dbShortDenied);
  assert.equal(v.shape, 'none');
});

test('events: markers only when nothing else matches', () => {
  const v = run({ hasEvents: true });
  assert.equal(v.key, 'events');
  assert.equal(v.shape, 'markers');
});

test('notread: nothing saved for this time', () => {
  const v = run({});
  assert.equal(v.key, 'notread');
  assert.equal(v.source, T.dbSrcNone);
  assert.equal(v.message, T.dbMsgNotRead);
  assert.equal(v.short, T.notRecorded);
  assert.equal(v.shape, 'none');
});

test('a lane code without a drawn shape does not claim a Query Store state', () => {
  assert.equal(run({ laneReasonCode: 'held-sql', laneHasShape: false, queryStore: qsOn }).key, 'notread');
  assert.equal(run({ laneReasonCode: 'api-seen', laneHasShape: false }).key, 'notread');
  assert.equal(run({ laneReasonCode: 'qs-file', laneHasShape: false }).key, 'notread');
});

test('unknown state (row missing or an error) is not its own state: it follows the data, ending at notread', () => {
  assert.equal(run({ queryStore: { state: 'unknown' } }).key, 'notread');
  assert.equal(run({ queryStore: { state: 'unknown' }, hasEvents: true }).key, 'events');
  assert.equal(run({ queryStore: { state: 'unknown' }, laneReasonCode: 'held-sql', laneHasShape: true }).key, 'qs');
});

test('pretend wins over a real qs', () => {
  const real = { laneReasonCode: 'held-sql', laneHasShape: true, blockSeconds: 900, queryStore: qsOn };
  assert.equal(run(real).key, 'qs');
  assert.equal(run({ ...real, pretend: 'off' }).key, 'off');
  assert.equal(run({ ...real, pretend: null }).key, 'qs');
});

test('every pretended state has words and uses sample values when there is no real data', () => {
  for (const key of DB_STATE_KEYS) {
    const v = run({ pretend: key });
    assert.equal(v.key, key);
    assert.ok(v.source && v.message && v.short, key);
  }
  assert.equal(run({ pretend: 'qsfile' }).source, T.dbSrcFile('11 Nov 2026'));
  assert.equal(run({ pretend: 'late' }).source, T.dbSrcLate('10:30'));
  assert.equal(run({ pretend: 'qs' }).short, T.laneBlocks(15));
});

test('shape is none for off, denied and notread in every route', () => {
  for (const key of ['off', 'denied', 'notread']) assert.equal(run({ pretend: key }).shape, 'none', key);
});

// BL-016: Query Store is off but the lane still draws blocks it kept.
const FIRST = '2026-11-10T00:00:00Z';
const LAST = '2026-11-11T10:45:00Z';
const held = { laneReasonCode: 'held-sql', laneHasShape: true };
const stampOf = (iso) => fmt.date(iso) + ', ' + fmt.time(iso);

test('offkept: off with a drawn held series', () => {
  const v = run({ ...held, blockSeconds: 900, queryStore: { state: 'off', firstData: FIRST, lastData: LAST } });
  assert.equal(v.key, 'offkept');
  assert.equal(v.shape, 'blocks');
  assert.equal(v.source, T.dbSrcOffKept);
  // The range ends at the START of the newest block: that block may be partial, so no block length is added.
  assert.equal(v.message, T.dbMsgOffKept(stampOf(FIRST), stampOf(LAST)));
  assert.equal(v.short, T.dbShortOffKept);
});

test('offkept without a block length gives the same range', () => {
  const v = run({ ...held, queryStore: { state: 'off', firstData: FIRST, lastData: LAST } });
  assert.equal(v.key, 'offkept');
  assert.equal(v.message, T.dbMsgOffKept(stampOf(FIRST), stampOf(LAST)));
});

test('offkept without times says saved earlier', () => {
  const v = run({ ...held, blockSeconds: 900, queryStore: { state: 'off' } });
  assert.equal(v.key, 'offkept');
  assert.equal(v.message, T.dbMsgOffKept(null, null));
  assert.match(v.message, /saved earlier/);
});

test('offkept: layered-sql counts as a drawn series', () => {
  assert.equal(run({ laneReasonCode: 'layered-sql', laneHasShape: true, queryStore: { state: 'off' } }).key, 'offkept');
});

test('off with no series is unchanged', () => {
  const v = run({ queryStore: { state: 'off' } });
  assert.equal(v.key, 'off');
  assert.equal(v.message, T.dbMsgOff);
  assert.equal(run({ ...held, laneHasShape: false, queryStore: { state: 'off' } }).key, 'off');
});

test('off with events and a held series is offkept', () => {
  assert.equal(run({ ...held, hasEvents: true, queryStore: { state: 'off' } }).key, 'offkept');
});

// The window of the call is 10:00 to 11:00 on 11 Nov. The kept range ends with a block that starts at 10:45 and lasts 15 minutes.
const kept = (firstData, lastData, over = {}) => run({ blockSeconds: 900, queryStore: { state: 'off', firstData, lastData }, ...over });
const outside = (from, to) => T.dbMsgOffOutside(stampOf(from), stampOf(to));
const notRead = (from, to) => T.dbMsgOffNotRead(stampOf(from), stampOf(to));

test('off, no shape, window inside the kept range: not read for this time, never "none of it is for this time"', () => {
  const v = kept('2026-11-11T09:00:00Z', LAST);
  assert.equal(v.key, 'off');
  assert.equal(v.message, notRead('2026-11-11T09:00:00Z', LAST));
  assert.notEqual(v.message, outside('2026-11-11T09:00:00Z', LAST));
  assert.equal(v.short, T.dbShortOff);
  assert.equal(v.shape, 'none');
  assert.doesNotMatch(v.message, /none of it is for this time/);
});

test('off, no shape, window that overlaps either end of the kept range: not read', () => {
  assert.equal(kept('2026-11-11T10:30:00Z', '2026-11-11T12:00:00Z').message, notRead('2026-11-11T10:30:00Z', '2026-11-11T12:00:00Z'));
  assert.equal(kept('2026-11-10T00:00:00Z', '2026-11-11T10:15:00Z').message, notRead('2026-11-10T00:00:00Z', '2026-11-11T10:15:00Z'));
});

test('off, no shape, window after the kept range: none of it is for this time', () => {
  const last = '2026-11-11T09:30:00Z'; // the newest block ends at 09:45, before the window starts at 10:00
  assert.equal(kept(FIRST, last).message, outside(FIRST, last));
  // A block that ends exactly where the window starts is outside, a block still running into the window is not.
  assert.equal(kept(FIRST, '2026-11-11T09:45:00Z').message, outside(FIRST, '2026-11-11T09:45:00Z'));
  assert.equal(kept(FIRST, '2026-11-11T09:46:00Z').message, notRead(FIRST, '2026-11-11T09:46:00Z'));
});

test('off, no shape, window before the kept range: none of it is for this time', () => {
  const first = '2026-11-11T11:00:00Z'; // the window ends at 11:00, as the oldest block starts
  assert.equal(kept(first, '2026-11-11T12:00:00Z').message, outside(first, '2026-11-11T12:00:00Z'));
  assert.equal(kept('2026-11-11T10:59:00Z', '2026-11-11T12:00:00Z').message, notRead('2026-11-11T10:59:00Z', '2026-11-11T12:00:00Z'));
});

test('off, no shape, block length unknown: the end of the range cannot be placed, so a later window is not read, not outside', () => {
  const v = run({ queryStore: { state: 'off', firstData: FIRST, lastData: '2026-11-11T09:30:00Z' } });
  assert.equal(v.message, notRead(FIRST, '2026-11-11T09:30:00Z'));
  // The saved block length from the day is used when the lane sent none.
  const w = run({ queryStore: { state: 'off', firstData: FIRST, lastData: '2026-11-11T09:30:00Z', intervalMinutes: 15 } });
  assert.equal(w.message, outside(FIRST, '2026-11-11T09:30:00Z'));
  // A window before the range is outside whatever the block length is.
  assert.equal(run({ queryStore: { state: 'off', firstData: '2026-11-11T11:00:00Z', lastData: '2026-11-11T12:00:00Z' } }).message, outside('2026-11-11T11:00:00Z', '2026-11-11T12:00:00Z'));
});

test('off with only a first time keeps the plain off message', () => {
  assert.equal(run({ queryStore: { state: 'off', firstData: FIRST } }).message, T.dbMsgOff);
});

test('denied still wins over a drawn off series', () => {
  assert.equal(run({ ...held, queryStore: { state: 'denied' } }).key, 'denied');
});

test('offkept is real-data only: it is not one of the demo keys', () => {
  assert.equal(DB_STATE_KEYS.includes('offkept'), false);
});

test('every off-with-history sentence passes the copy lint, including the one without times', () => {
  const extra = [
    { id: 'drill.dbMsgOffKept.notimes', text: T.dbMsgOffKept(null, null) },
    { id: 'drill.dbMsgOffNotRead.sample', text: T.dbMsgOffNotRead('11 Nov 2026, 10:00', '11 Nov 2026, 10:45') }
  ];
  assert.deepEqual(lintScreenStrings([...drillCopyEntries(), ...extra], deck.terms), []);
});
