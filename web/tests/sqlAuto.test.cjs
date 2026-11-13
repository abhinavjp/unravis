const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

// The Timeline's own reading of the database (P5): which saved connection a case uses for SQL, and when a window needs a read.

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
const { pickSqlSource, decideSqlRead, windowsOfBody, readKey } = loadTypeScript('src/lib/tracing/sqlAuto.ts');

const conn = (over = {}) => ({ id: 'c1', label: 'Sandbox', server: 'host', host: 'host', port: 1433, auth: 'windows', username: '', database: 'Brain', encrypt: true, trustServerCertificate: true, hasPassword: false, savedAt: '', ...over });
const win = (start, end) => ({ start, end });
const day = (over = {}) => ({ id: 1, at: '2026-10-23T10:00:00Z', kind: 'sql', label: 'Sandbox', database: 'Brain', state: 'Pass', incident: win('2026-10-23T19:00:00Z', '2026-10-24T19:00:00Z'), reference: win('2026-10-22T19:00:00Z', '2026-10-23T19:00:00Z'), lane: null, ...over });
const input = (over = {}) => ({
  source: { connectionId: 'c1', label: 'Sandbox', database: 'Brain', needsPassword: false, remembered: true },
  days: [], incident: win('2026-10-25T00:00:00Z', '2026-10-25T06:00:00Z'), reference: win('2026-10-24T18:00:00Z', '2026-10-25T00:00:00Z'),
  demo: false, closed: false, enabled: true, ...over
});

test('the newest saved SQL day names the connection and database', () => {
  const s = pickSqlSource([day({ id: 1, database: 'Old' }), day({ id: 2, database: 'Brain' })], [conn()]);
  assert.deepEqual(s, { connectionId: 'c1', label: 'Sandbox', database: 'Brain', needsPassword: false, remembered: true });
});

test('a case with no saved day uses the only saved connection, marked as not remembered', () => {
  const s = pickSqlSource([], [conn()]);
  assert.equal(s.remembered, false);
  assert.equal(s.connectionId, 'c1');
});

test('no source: two saved connections and no day, or a day whose connection was forgotten', () => {
  assert.equal(pickSqlSource([], [conn(), conn({ id: 'c2', label: 'Other' })]), null);
  assert.equal(pickSqlSource([], []), null);
  assert.equal(pickSqlSource([day({ label: 'Gone' })], [conn()]), null);
});

test('a typed-password SQL login cannot be read automatically', () => {
  const s = pickSqlSource([], [conn({ auth: 'sql', hasPassword: false })]);
  assert.equal(s.needsPassword, true);
  assert.deepEqual(decideSqlRead(input({ source: s })), { kind: 'skip', why: 'password' });
  assert.equal(pickSqlSource([], [conn({ auth: 'sql', hasPassword: true })]).needsPassword, false);
});

test('a new window is read; the same window is not read again', () => {
  const v = decideSqlRead(input());
  assert.equal(v.kind, 'read');
  const covered = decideSqlRead(input({ days: [day({ incident: win('2026-10-25T00:00:00Z', '2026-10-25T12:00:00Z') })] }));
  assert.deepEqual(covered, { kind: 'covered' });
});

test('a window that crosses a saved day without being the same is not read', () => {
  const v = decideSqlRead(input({ days: [day({ incident: win('2026-10-25T03:00:00Z', '2026-10-25T09:00:00Z') })] }));
  assert.equal(v.kind, 'overlap');
});

test('nothing is read in the demo, on a closed case, with the toggle off, or without a window or database', () => {
  assert.deepEqual(decideSqlRead(input({ demo: true })), { kind: 'skip', why: 'demo' });
  assert.deepEqual(decideSqlRead(input({ closed: true })), { kind: 'skip', why: 'closed' });
  assert.deepEqual(decideSqlRead(input({ enabled: false })), { kind: 'skip', why: 'off' });
  assert.deepEqual(decideSqlRead(input({ source: null })), { kind: 'skip', why: 'no-source' });
  assert.deepEqual(decideSqlRead(input({ incident: null })), { kind: 'skip', why: 'no-window' });
  const noDb = { connectionId: 'c1', label: 'Sandbox', database: ' ', needsPassword: false, remembered: false };
  assert.deepEqual(decideSqlRead(input({ source: noDb })), { kind: 'skip', why: 'no-database' });
});

test('windowsOfBody refuses a window that does not end after it starts', () => {
  assert.equal(windowsOfBody(null), null);
  assert.equal(windowsOfBody({ incident: win('2026-10-25T06:00:00Z', '2026-10-25T00:00:00Z'), reference: win('a', 'b') }), null);
  const ok = windowsOfBody({ incident: win('2026-10-25T00:00:00Z', '2026-10-25T06:00:00Z'), reference: win('2026-10-24T18:00:00Z', '2026-10-25T00:00:00Z') });
  assert.equal(ok.incident.start, '2026-10-25T00:00:00Z');
});

test('the read key is the same for the same source and window, different otherwise', () => {
  const s = input().source;
  const a = readKey(s, win('2026-10-25T00:00:00Z', '2026-10-25T06:00:00Z'));
  assert.equal(a, readKey(s, win('2026-10-25T00:00:00.000Z', '2026-10-25T06:00:00.000Z')));
  assert.notEqual(a, readKey({ ...s, database: 'Other' }, win('2026-10-25T00:00:00Z', '2026-10-25T06:00:00Z')));
});
