const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

// SQL Server and MongoDB lanes (P7): the pure model, the copy lint, and the shape of the components.

const root = path.resolve(__dirname, '..');
const cache = new Map();
function loadTypeScript(relativePath) {
  const filename = path.join(root, relativePath);
  if (cache.has(filename)) return cache.get(filename).exports;
  const source = fs.readFileSync(filename, 'utf8');
  const output = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022, esModuleInterop: true } }).outputText;
  const module = { exports: {} };
  cache.set(filename, module);
  const nodeRequire = createRequire(filename);
  const load = (request) => {
    if (/^\.\.?\//.test(request)) {
      const plain = path.join(path.dirname(filename), request + '.ts');
      if (fs.existsSync(plain)) return loadTypeScript(path.relative(root, plain));
    }
    return nodeRequire(request);
  };
  new Function('require', 'module', 'exports', output)(load, module, module.exports);
  return module.exports;
}
const read = (relative) => fs.readFileSync(path.join(root, relative), 'utf8');

const lanes = loadTypeScript('src/lib/lanes.ts');
const copy = loadTypeScript('src/lib/copy/sourcesCopy.ts');
const { lintScreenStrings } = loadTypeScript('src/lib/copy/copylint.ts');
const { deck } = loadTypeScript('src/lib/copy/deck.ts');

const win = (a, b) => ({ start: a, end: b });
const day = (over = {}) => ({
  id: 1, at: '2026-10-26T12:00:00Z', kind: 'sql', label: 'SQL-PROD-01', database: 'BrainPayroll', state: 'Ready',
  incident: win('2026-10-26T10:30:00Z', '2026-10-26T11:00:00Z'), reference: win('2026-10-26T09:30:00Z', '2026-10-26T10:30:00Z'), lane: null, ...over
});

test('the copy of the P7 screens passes the copy lint', () => {
  assert.deepEqual(lintScreenStrings(copy.sourcesCopyEntries(), deck.terms), []);
});

test('the fixed words are the only readiness words', () => {
  assert.equal(lanes.toState('Ready'), 'Ready');
  assert.equal(lanes.toState('Partly ready'), 'Partly ready');
  assert.equal(lanes.toState('Cannot test yet'), 'Cannot test yet');
  assert.equal(lanes.toState('Failed'), 'Failed');
  // An unknown word is never read as a pass.
  assert.equal(lanes.toState('ok'), 'Cannot test yet');
  assert.equal(lanes.toState(undefined), 'Cannot test yet');
});

test('toLane reads a server lane and drops what is not text', () => {
  const lane = lanes.toLane({
    incident: win('a', 'b'), reference: win('c', 'd'), era: '2019', onset: '2026-10-26T10:41:00Z', sources: ['x', 3],
    checks: [
      { id: 'server', title: 'Server', state: 'Ready', summary: 's', feeds: ['D1'], fix: null },
      { id: 'errorlog', title: 'Error log', state: 'Cannot test yet', summary: 't', fix: { permission: 'VIEW SERVER STATE', script: 'GRANT x' } }
    ],
    answers: { D1: 'yes', D2: 7 }
  });
  assert.equal(lane.era, '2019');
  assert.deepEqual(lane.sources, ['x']);
  assert.deepEqual(lane.answers, { D1: 'yes' });
  assert.equal(lane.checks[1].fix.permission, 'VIEW SERVER STATE');
  assert.equal(lane.checks[0].fix, null);
  assert.equal(lane.state, 'Partly ready');
  assert.equal(lanes.toLane(null), null);
  assert.equal(lanes.toLane('x'), null);
});

test('the word for a whole lane follows its checks', () => {
  const c = (state) => ({ id: state, title: '', state, summary: '', feeds: [], fix: null });
  assert.equal(lanes.overallState([]), 'Cannot test yet');
  assert.equal(lanes.overallState([c('Ready'), c('Ready')]), 'Ready');
  assert.equal(lanes.overallState([c('Ready'), c('Cannot test yet')]), 'Partly ready');
  assert.equal(lanes.overallState([c('Cannot test yet'), c('Cannot test yet')]), 'Cannot test yet');
  assert.equal(lanes.overallState([c('Failed'), c('Failed')]), 'Failed');
  assert.equal(lanes.overallState([c('Failed'), c('Cannot test yet')]), 'Failed');
  assert.equal(lanes.overallState([c('Ready'), c('Failed')]), 'Partly ready');
});

test('a saved day keeps one shape', () => {
  const run = lanes.toDay({ id: 4, kind: 'mongo', label: 'MongoDB', database: 'BrainAudit', state: 'Partly ready', incident: win('a', 'b'), reference: win('c', 'd'), lane: { checks: [] } });
  assert.equal(run.kind, 'mongo');
  assert.equal(run.state, 'Partly ready');
  assert.deepEqual(run.lane.checks, []);
  assert.equal(lanes.toDay({}).kind, 'sql');
  assert.equal(lanes.toDay({}).state, 'Cannot test yet');
});

test('connection rows say Not tested until a test ran, and never hold a password', () => {
  const sql = { id: 'a1', label: '', server: 'sql-prod-01', host: 'sql-prod-01', port: 1433, auth: 'sql', username: 'sa-ro', database: '', encrypt: 'mandatory', trustServerCertificate: false, hasPassword: true, savedAt: '' };
  const row = lanes.sqlRow(sql);
  assert.equal(row.word, 'Not tested');
  assert.equal(row.label, 'sql-prod-01');
  assert.equal(row.password, 'saved');
  assert.deepEqual(Object.keys(row).sort(), ['detail', 'id', 'key', 'kind', 'label', 'password', 'problem', 'testedAt', 'word']);
  assert.equal(lanes.sqlRow({ ...sql, auth: 'windows', username: '', hasPassword: false }).password, 'not-needed');
  assert.equal(lanes.sqlRow({ ...sql, hasPassword: false }).password, 'none');
  assert.equal(lanes.sqlRow(sql, { word: 'Works', at: 't', problem: '' }).word, 'Works');
  assert.equal(lanes.mongoRow({ id: 'm', label: 'prod-mongo', uri: 'x', username: '', hasCredentials: false }).password, 'not-needed');
  assert.equal(lanes.mongoRow({ id: 'm', label: 'prod-mongo', uri: 'x', username: 'u', hasCredentials: true }).password, 'saved');
});

test('the 20th connection can be saved and the 21st cannot', () => {
  assert.equal(lanes.CONNECTION_LIMIT, 20);
  assert.equal(lanes.canSaveAnother(19), true);
  assert.equal(lanes.canSaveAnother(20), false);
  assert.equal(lanes.canSaveAnother(25), false);
});

test('UTC windows are read strictly', () => {
  assert.deepEqual(lanes.utcWindow('2026-10-26T10:30', '2026-10-26 11:00'), win('2026-10-26T10:30:00.000Z', '2026-10-26T11:00:00.000Z'));
  assert.equal(lanes.utcWindow('', '2026-10-26T11:00'), null);
  assert.equal(lanes.utcWindow('2026-10-26T11:00', '2026-10-26T11:00'), null, 'end must be after start');
  assert.equal(lanes.utcWindow('2026-10-26T12:00', '2026-10-26T11:00'), null);
  assert.equal(lanes.utcWindow('2026-02-31T10:00', '2026-03-01T10:00'), null, '31 February is not a day');
  assert.equal(lanes.utcWindow('26/10/2026 10:30', '26/10/2026 11:00'), null);
  assert.equal(lanes.utcInput('2026-10-26T10:30:00Z'), '2026-10-26T10:30');
  assert.equal(lanes.utcInput('nope'), '');
});

test('two days that overlap are caught here as the server catches them', () => {
  const held = [day()];
  const same = win('2026-10-26T10:30:00Z', '2026-10-26T11:00:00Z');
  assert.equal(lanes.overlapProblem(held, 'sql', 'SQL-PROD-01', 'BrainPayroll', same), null, 'the same window is a check again');
  const clash = win('2026-10-26T10:45:00Z', '2026-10-26T11:30:00Z');
  assert.equal(lanes.overlapProblem(held, 'sql', 'SQL-PROD-01', 'BrainPayroll', clash).id, 1);
  const touching = win('2026-10-26T11:00:00Z', '2026-10-26T12:00:00Z');
  assert.equal(lanes.overlapProblem(held, 'sql', 'SQL-PROD-01', 'BrainPayroll', touching), null, 'a window that only touches is fine');
  assert.equal(lanes.overlapProblem(held, 'sql', 'SQL-PROD-01', 'Other', clash), null, 'another database is another source');
  assert.equal(lanes.overlapProblem(held, 'mongo', 'SQL-PROD-01', 'BrainPayroll', clash), null);
  const next = lanes.shiftDays(day().incident, 1);
  assert.equal(next.start, '2026-10-27T10:30:00.000Z');
  assert.equal(lanes.overlapProblem(held, 'sql', 'SQL-PROD-01', 'BrainPayroll', next), null);
});

test('days group by source, newest day first', () => {
  const a = day({ id: 1 });
  const b = day({ id: 2, incident: win('2026-10-27T10:30:00Z', '2026-10-27T11:00:00Z') });
  const c = day({ id: 3, kind: 'mongo', label: 'MongoDB', database: 'BrainAudit' });
  const groups = lanes.groupSources([a, b, c]);
  assert.equal(groups.length, 2);
  const sql = groups.find((g) => g.kind === 'sql');
  assert.deepEqual(sql.days.map((d) => d.id), [2, 1]);
  assert.equal(lanes.readableByDay('sql'), true);
  assert.equal(lanes.readableByDay('mongo'), false, 'the profiler keeps only the latest entries');
});
