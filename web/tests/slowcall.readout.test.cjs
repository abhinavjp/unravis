const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

// Slow call screen: hover readout numbers and the all-sources time-ordered list.

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

const { readoutAt, allSources, sourceOfEvent, eventLabel } = loadTypeScript('src/lib/drill/callReadout.ts');

const cell = (start, end, atOnce, p95Ms, refused) => ({ start, end, atOnce, p95Ms, refused });
const call = { startMs: 1000, endMs: 3000 };

test('readoutAt picks the step that holds the hover time', () => {
  const lanes = {
    audit: [cell(0, 1000, 1, 10, 0), cell(1000, 2000, 5, 50, 2), cell(2000, 3000, 7, 70, 0)],
    iis: [cell(1000, 2000, 3, 30, 1)],
    sql: [cell(1000, 2000, 2, 20, null)]
  };
  const r = readoutAt(1500, lanes, call);
  assert.equal(r.stepStart, 1000);
  assert.equal(r.api.p95Ms, 50);
  assert.equal(r.iis.refused, 1);
  assert.equal(r.db.atOnce, 2);
  assert.equal(r.isThisCall, true);
  assert.equal(readoutAt(2500, lanes, call).api.p95Ms, 70);
});

test('a lane with a gap is null, not 0', () => {
  const lanes = { audit: [cell(0, 1000, 1, 10, 0)], iis: [], sql: [cell(5000, 6000, 1, 1, 0)] };
  const r = readoutAt(500, lanes, call);
  assert.equal(r.iis, null);
  assert.equal(r.db, null);
  assert.notEqual(r.api, null);
  assert.equal(r.isThisCall, false);
  const missing = readoutAt(500, { audit: [cell(0, 1000, null, null, null)], iis: [], sql: [] }, call);
  assert.equal(missing.api.p95Ms, null);
});

test('readoutAt is null outside every lane', () => {
  const lanes = { audit: [cell(0, 1000, 1, 10, 0)], iis: [], sql: [] };
  assert.equal(readoutAt(9999, lanes, call), null);
  assert.equal(readoutAt(NaN, lanes, call), null);
  assert.equal(readoutAt(1, { audit: [], iis: [], sql: [] }, call), null);
});

test('sourceOfEvent maps known sources and drops the rest', () => {
  for (const s of ['sql-xe', 'sql-errorlog', 'sql-pdh', 'sql-querystore']) assert.equal(sourceOfEvent(s), 'db');
  for (const s of ['iis-httperr', 'iis-w3c', 'iis-freb']) assert.equal(sourceOfEvent(s), 'iis');
  assert.equal(sourceOfEvent('audit'), null);
  assert.equal(sourceOfEvent('toString'), null);
});

test('eventLabel uses only the kind', () => {
  assert.equal(eventLabel({ evidenceId: 'e', source: 'sql-xe', kind: 'blocked-process', start: '' }), 'Blocked process');
  assert.equal(eventLabel({ evidenceId: 'e', source: 'sql-xe', kind: 'deadlock', start: '' }), 'Deadlock');
});

const T0 = Date.parse('2026-01-01T10:00:00Z');
const iso = (ms) => new Date(T0 + ms).toISOString();
const shownAll = { api: true, iis: true, db: true };
function base(extra) {
  return {
    apiRows: [], iisBars: [], events: [], callId: 'c1', start: T0, end: T0 + 10000, shown: shownAll, ...extra
  };
}

test('allSources sorts by time then api, iis, db and marks the call row', () => {
  const out = allSources(base({
    apiRows: [
      { id: 'c1', controller: 'Orders', action: 'Get', initiatedAt: iso(2000), durationMs: 500 },
      { id: 'c2', controller: 'Orders', action: 'List', initiatedAt: iso(1000), durationMs: 100 }
    ],
    iisBars: [{ id: 'i1', label: 'GET /orders', start: T0 + 2000, end: T0 + 2600 }],
    events: [
      { evidenceId: 'x1', source: 'sql-xe', kind: 'blocked-process', start: iso(2000), durationMs: 300 },
      { evidenceId: 'x2', source: 'sql-errorlog', kind: 'login-failed', start: iso(500) }
    ]
  }));
  assert.deepEqual(out.map((i) => i.source), ['db', 'api', 'api', 'iis', 'db']);
  assert.deepEqual(out.map((i) => i.startMs - T0), [500, 1000, 2000, 2000, 2000]);
  assert.equal(out.find((i) => i.key === 'api:c1').thisCall, true);
  assert.equal(out.find((i) => i.key === 'api:c2').thisCall, false);
});

test('events with no duration are markers with no took time', () => {
  const out = allSources(base({ events: [{ evidenceId: 'x2', source: 'sql-errorlog', kind: 'login-failed', start: iso(500) }] }));
  assert.equal(out.length, 1);
  assert.equal(out[0].marker, true);
  assert.equal(out[0].tookMs, null);
  assert.equal(out[0].endMs, null);
  assert.equal(out[0].what, 'Login failed');
  const timed = allSources(base({ events: [{ evidenceId: 'x3', source: 'iis-httperr', kind: 'timer-minbytes', start: iso(500), durationMs: 250 }] }));
  assert.equal(timed[0].marker, false);
  assert.equal(timed[0].tookMs, 250);
});

test('a switched-off source is gone', () => {
  const input = base({
    apiRows: [{ id: 'c1', controller: 'A', action: 'B', initiatedAt: iso(100), durationMs: 5 }],
    iisBars: [{ id: 'i1', label: 'GET /a', start: T0 + 100, end: T0 + 200 }],
    events: [
      { evidenceId: 'x1', source: 'sql-xe', kind: 'deadlock', start: iso(100), durationMs: 10 },
      { evidenceId: 'x2', source: 'iis-w3c', kind: 'slow-request', start: iso(100), durationMs: 10 }
    ]
  });
  assert.equal(allSources({ ...input, shown: { api: false, iis: true, db: true } }).some((i) => i.source === 'api'), false);
  const noIis = allSources({ ...input, shown: { api: true, iis: false, db: true } });
  assert.equal(noIis.some((i) => i.source === 'iis'), false);
  assert.equal(noIis.some((i) => i.source === 'db'), true);
  assert.equal(allSources({ ...input, shown: { api: false, iis: false, db: false } }).length, 0);
});

test('duplicate ids collapse', () => {
  const row = { id: 'c1', controller: 'A', action: 'B', initiatedAt: iso(100), durationMs: 5 };
  const ev = { evidenceId: 'x1', source: 'sql-xe', kind: 'deadlock', start: iso(100), durationMs: 10 };
  const out = allSources(base({ apiRows: [row, row], events: [ev, ev] }));
  assert.equal(out.length, 2);
});

test('an unknown event source is dropped', () => {
  const out = allSources(base({ events: [{ evidenceId: 'x1', source: 'mystery', kind: 'deadlock', start: iso(100), durationMs: 10 }] }));
  assert.equal(out.length, 0);
});

test('only items that overlap the window are kept', () => {
  const out = allSources(base({
    apiRows: [
      { id: 'before', controller: 'A', action: 'B', initiatedAt: iso(-5000), durationMs: 100 },
      { id: 'straddle', controller: 'A', action: 'B', initiatedAt: iso(-100), durationMs: 500 },
      { id: 'after', controller: 'A', action: 'B', initiatedAt: iso(20000), durationMs: 100 },
      { id: 'nodur', controller: 'A', action: 'B', initiatedAt: iso(300), durationMs: null }
    ]
  }));
  assert.deepEqual(out.map((i) => i.key), ['api:straddle', 'api:nodur']);
  assert.equal(out[1].tookMs, null);
});

test('same record id from two sources stays two rows and only the call source is this call', () => {
  const rows = [
    { id: 'c1', sourceId: 'srcA', controller: 'A', action: 'B', initiatedAt: iso(100), durationMs: 5 },
    { id: 'c1', sourceId: 'srcB', controller: 'A', action: 'B', initiatedAt: iso(100), durationMs: 5 }
  ];
  const out = allSources(base({ apiRows: rows, callSourceId: 'srcA' }));
  assert.deepEqual(out.map((i) => i.key).sort(), ['api:srcA:c1', 'api:srcB:c1']);
  assert.deepEqual(out.map((i) => [i.key, i.thisCall]).sort(), [['api:srcA:c1', true], ['api:srcB:c1', false]]);
});

test('one source or no source id keeps the plain api:<id> key', () => {
  const one = allSources(base({ apiRows: [{ id: 'c1', sourceId: 'srcA', controller: 'A', action: 'B', initiatedAt: iso(100), durationMs: 5 }], callSourceId: 'srcA' }));
  assert.equal(one[0].key, 'api:c1');
  assert.equal(one[0].thisCall, true);
  const none = allSources(base({ apiRows: [{ id: 'c1', controller: 'A', action: 'B', initiatedAt: iso(100), durationMs: 5 }] }));
  assert.equal(none[0].key, 'api:c1');
  const wrong = allSources(base({ apiRows: [{ id: 'c1', sourceId: 'srcA', controller: 'A', action: 'B', initiatedAt: iso(100), durationMs: 5 }], callSourceId: 'srcB' }));
  assert.equal(wrong[0].thisCall, false);
});

test('events with an empty evidence id do not collapse into one', () => {
  const out = allSources(base({ events: [
    { evidenceId: '', source: 'sql-xe', kind: 'deadlock', start: iso(100), durationMs: 10 },
    { evidenceId: '', source: 'sql-xe', kind: 'deadlock', start: iso(900), durationMs: 10 },
    { evidenceId: '', source: 'sql-xe', kind: 'blocked-process', start: iso(100), durationMs: 10 }
  ] }));
  assert.equal(out.length, 3);
  assert.equal(new Set(out.map((i) => i.key)).size, 3);
});
