const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const { loadTypeScript } = require('./helpers/loadTs.cjs');

// The SQL service's lines in the Blackbox charts view: keys are "sqlDmv.<kind>[.<what>].<field>" (internal/server/blackbox_sqlseries.go).
// Names, meanings and the note that says why a SQL line is missing. Only numbers are charted: nothing here may name a query or a login.

const s = loadTypeScript('src/lib/blackboxSeries.ts');
const c = loadTypeScript('src/lib/blackboxCompare.ts');
const n = loadTypeScript('src/lib/blackboxSqlNames.ts');
const { lintCopy } = loadTypeScript('src/lib/copy/copylint.ts');
const read = (p) => fs.readFileSync(path.join(__dirname, '..', p), 'utf8');

const T0 = Date.parse('2026-11-23T06:00:00Z');
const base = (extra = {}) => ({
  start: '2026-11-23T06:00:00Z', end: '2026-11-23T07:00:00Z', bucketSeconds: 15, intervalSeconds: 15, samples: 3,
  lines: [{ key: 'cpu', group: 'host', label: 'cpu', unit: 'pct', points: [[T0, 10, 5, 20]] }],
  groups: [], processes: [], eventIds: [], dumps: [], gaps: [], markers: [],
  read: { files: 1, samples: 3, torn: 0, dropped: [], missing: [] }, state: {}, ...extra
});

// Every key the server can write, by kind. Keep in step with addSQLSeries.
const KEYS = [
  'sqlDmv.requests.tasks', 'sqlDmv.requests.blocked', 'sqlDmv.requests.headBlockers', 'sqlDmv.requests.maxWaitMs', 'sqlDmv.requests.maxBlockMs',
  'sqlDmv.requestsByWait.LCK_M_X.tasks', 'sqlDmv.requestsByWait.RUNNING.tasks',
  'sqlDmv.waits.PAGEIOLATCH_SH.deltaMs', 'sqlDmv.waits.PAGEIOLATCH_SH.deltaTasks',
  'sqlDmv.schedulers.currentTasks', 'sqlDmv.schedulers.runnableTasks',
  'sqlDmv.semaphores.pool2sem1.grantees', 'sqlDmv.semaphores.pool2sem1.waiters', 'sqlDmv.semaphores.pool2sem1.forced', 'sqlDmv.semaphores.pool2sem1.timeouts',
  'sqlDmv.tempdb.internalBytes', 'sqlDmv.tempdb.userBytes',
  'sqlDmv.fileStats.readLatencyMs', 'sqlDmv.fileStats.writeLatencyMs', 'sqlDmv.fileStats.reads', 'sqlDmv.fileStats.writes',
  'sqlDmv.fileStats.db5.readLatencyMs', 'sqlDmv.fileStats.db5.writeLatencyMs',
  'sqlDmv.ag.databases', 'sqlDmv.ag.notSynchronizing', 'sqlDmv.ag.synchronizing', 'sqlDmv.ag.synchronized', 'sqlDmv.ag.other'
];

test('every sqlDmv key the server writes has a plain name and a meaning of its own, never the bare field name', () => {
  for (const k of KEYS) {
    const l = n.sqlDmvLine(k);
    assert.ok(l, k);
    assert.ok(l.name.length > 5 && l.meaning.length > 20, k);
    assert.notEqual(l.name, k.split('.').pop(), k);
    assert.equal(s.seriesName(k).name, l.name, k);
    assert.equal(c.meaningOf(k, 'count'), l.meaning, k);
  }
});

test('a named thing is shown after the name: a wait type, a pool and semaphore, a database by number', () => {
  assert.equal(s.seriesTitle('sqlDmv.waits.PAGEIOLATCH_SH.deltaMs'), 'Wait time added (wait stats): PAGEIOLATCH_SH');
  assert.equal(s.seriesTitle('sqlDmv.requestsByWait.LCK_M_X.tasks'), 'Requests in wait type: LCK_M_X');
  assert.equal(s.seriesTitle('sqlDmv.semaphores.pool2sem1.waiters'), 'Queries queued for a memory grant: pool 2, semaphore 1');
  assert.equal(s.seriesTitle('sqlDmv.fileStats.db5.readLatencyMs'), 'Time per read (file stalls): database 5');
  assert.equal(s.seriesTitle('sqlDmv.fileStats.readLatencyMs'), 'Time per read, all database files (file stalls)');
});

test('a key that is not one of the known shapes falls back to its own words, and a lookalike never reads a name from the prototype', () => {
  assert.equal(n.sqlDmvLine('sqlDmv.future.thing'), null);
  assert.equal(n.sqlDmvLine('sqlDmv.requests.constructor'), null);
  assert.equal(n.sqlDmvLine('sqlDmv.semaphores.poolXsemY.waiters'), null);
  assert.equal(n.sqlDmvLine('sqlDmv.fileStats.dbX.readLatencyMs'), null);
  assert.equal(n.sqlDmvLine('sqlHost.X.batchRequestsPerSec'), null);
  assert.equal(s.seriesTitle('sqlDmv.future.thing'), 'thing: future');
  assert.equal(s.seriesTitle('sqlDmv.__proto__.x'), 'x: __proto__');
});

test('the group is listed after the SQL host counters, with its own title', () => {
  const lines = [
    { key: 'sqlDmv.tempdb.userBytes', group: 'sqlDmv', label: '', unit: 'bytes', points: [[T0, 1, 1, 1]] },
    { key: 'sqlHost.X.batchRequestsPerSec', group: 'sqlHost', label: '', unit: 'perSec', points: [[T0, 1, 1, 1]] }
  ];
  assert.deepEqual(s.pickerGroups(lines).map((g) => g.group), ['sqlHost', 'sqlDmv']);
  assert.equal(s.GROUP_TITLES.sqlDmv, 'SQL Server: deeper checks');
});

test('units come from the server and the SQL lines use the units the server sets', () => {
  assert.equal(s.seriesUnit('sqlDmv.tempdb.userBytes', 'bytes'), 'bytes');
  assert.equal(s.seriesUnit('sqlDmv.waits.X.deltaMs', 'ms'), 'ms');
  assert.equal(s.seriesUnit('sqlDmv.requests.tasks', 'count'), 'count');
});

test('readSeriesAnswer reads the sql block as numbers and flags, and an answer without it still reads', () => {
  assert.equal(s.readSeriesAnswer(base()).sql, undefined);
  const a = s.readSeriesAnswer(base({ sql: { files: 2, samples: 40, missing: 1, dropped: 0, corrupt: 0, torn: 1, truncated: 3, locked: true, evil: 'x' } }));
  assert.deepEqual(a.sql, { files: 2, samples: 40, missing: 1, dropped: 0, corrupt: 0, torn: 1, truncated: 3, locked: true, unsupported: false, limited: false, newer: false });
  const b = s.readSeriesAnswer(base({ sql: { files: 'x', samples: null } }));
  assert.equal(b.sql.files, 0);
  assert.equal(b.sql.samples, 0);
  assert.equal(s.readSeriesAnswer(base({ sql: 'x' })).sql, undefined);
});

test('sqlNote says why SQL lines are missing, and says nothing when the service simply has no files', () => {
  const ok = { files: 0, samples: 0, missing: 6, dropped: 0, corrupt: 0, torn: 0, truncated: 0 };
  assert.equal(n.sqlNote(undefined), null);
  assert.equal(n.sqlNote(ok), null); // SQL checks never switched on: not a problem to report
  assert.match(n.sqlNote({ ...ok, locked: true }), /locked to administrators/);
  assert.match(n.sqlNote({ ...ok, newer: true }), /newer version/);
  assert.match(n.sqlNote({ ...ok, unsupported: true }), /by its address/);
  assert.match(n.sqlNote({ ...ok, limited: true }), /shorter range/);
  assert.match(n.sqlNote({ ...ok, dropped: 2 }), /could not be read/);
  assert.match(n.sqlNote({ ...ok, files: 3, dropped: 1 }), /some points are missing/);
  assert.match(n.sqlNote({ ...ok, files: 3, corrupt: 2 }), /damaged/);
  assert.match(n.sqlNote({ ...ok, files: 3, truncated: 1 }), /one reading was cut/);
  assert.match(n.sqlNote({ ...ok, files: 3, truncated: 4 }), /4 readings were cut/);
  // one torn file is the hour being written right now: normal
  assert.equal(n.sqlNote({ ...ok, files: 3, torn: 1 }), null);
});

test('the SQL group never says "adapters": its partial count is a row-limit cut, which sqlNote words', () => {
  assert.equal(s.coverNote({ group: 'sqlDmv', read: 10, missing: 0, slow: 0, busy: 0, partial: 4 }), null);
  assert.match(s.coverNote({ group: 'tcp', read: 10, missing: 0, slow: 0, busy: 0, partial: 4 }), /adapters/);
});

test('names and notes pass the same copy checks as the rest of the charts view, and name no cause', () => {
  const texts = [];
  for (const k of KEYS) {
    const l = n.sqlDmvLine(k);
    texts.push({ id: k + ':name', text: l.name }, { id: k + ':meaning', text: l.meaning });
  }
  const findings = lintCopy(texts).filter((f) => f.rule === 'causal' || f.rule === 'wording');
  assert.deepEqual(findings, []);
});

test('the Charts view shows the note under the list of groups that were not fully read', () => {
  const src = read('src/components/BlackboxCharts.svelte');
  assert.match(src, /import \{ sqlNote \} from '\.\.\/lib\/blackboxSqlNames'/);
  assert.match(src, /sqlNote\(answer\.sql\)/);
  assert.match(src, /\{#if sqlNoteText\}<li>\{own\(GROUP_TITLES, 'sqlDmv'\)\}: \{sqlNoteText\}<\/li>\{\/if\}/);
});

test('the keys the Go builder writes are the ones this view names (drift check against blackbox_sqlseries.go)', () => {
  const go = fs.readFileSync(path.join(__dirname, '..', '..', 'internal', 'server', 'blackbox_sqlseries.go'), 'utf8');
  const written = [...go.matchAll(/add\("([A-Za-z.]+)(?:"|\.)/g)].map((m) => m[1]);
  assert.ok(written.length > 20, 'found the add(...) calls');
  const kinds = new Set(written.map((w) => w.split('.')[0]));
  for (const k of ['requests', 'requestsByWait', 'waits', 'schedulers', 'semaphores', 'tempdb', 'fileStats', 'ag']) assert.ok(kinds.has(k), k);
  for (const k of kinds) assert.ok(KEYS.some((x) => x.split('.')[1] === k), 'no name test for kind ' + k);
});
