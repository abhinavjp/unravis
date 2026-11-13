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
const { DRILL_TEXT: T } = loadTypeScript('src/lib/drill/drillCopy.ts');

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
