const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

// Slow call screen: the nearby calls model (which rows show, when to ask the server, what to ask, gap and light cells).

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

const m = loadTypeScript('src/lib/drill/nearbyCalls.ts');

const T0 = Date.parse('2026-11-12T10:00:00.000Z');
const call = { id: 'c1', sourceId: 's1', controller: 'Orders', action: 'Get', userId: 7, startMs: T0, endMs: T0 + 10000 };
const row = (id, offsetMs, extra = {}) => ({
  id, sourceId: 's1', controller: 'Orders', action: 'Get',
  initiatedAt: new Date(T0 + offsetMs).toISOString(), durationMs: 100, userId: 7, ...extra
});
const text = { thisCall: 'this call', during: 'during this call' };
const MIN = '−';

test('NEARBY_LIMIT matches the server page size', () => {
  assert.equal(m.NEARBY_LIMIT, 100);
});

test('chipAvailable: user needs a userId, api needs controller and action', () => {
  assert.equal(m.chipAvailable('everyone', call), true);
  assert.equal(m.chipAvailable('user', { ...call, userId: undefined }), false);
  assert.equal(m.chipAvailable('user', { ...call, userId: null }), false);
  assert.equal(m.chipAvailable('user', call), true);
  assert.equal(m.chipAvailable('api', { ...call, action: '' }), false);
  assert.equal(m.chipAvailable('api', { ...call, controller: '' }), false);
  assert.equal(m.chipAvailable('api', call), true);
});

test('userId 0 is a real user, not missing', () => {
  const zero = { ...call, userId: 0 };
  assert.equal(m.chipAvailable('user', zero), true);
  const rows = [row('a', 1000, { userId: 0 }), row('b', 2000, { userId: 7 }), row('c', 3000, { userId: null })];
  assert.deepEqual(m.filterRows(rows, 'user', zero, T0 - 5000, T0 + 5000).map((r) => r.id), ['a']);
  assert.equal(m.activityInput(['s1'], T0, T0 + 1000, 'user', zero).userId, 0);
});

test('filterRows keeps rows inside the window, inclusive, sorted by time', () => {
  const rows = [row('late', 5000), row('out-after', 9000), row('edge', 6000), row('early', -4000), row('out-before', -9000), row('bad', 0, { initiatedAt: 'nope' })];
  const out = m.filterRows(rows, 'everyone', call, T0 - 5000, T0 + 6000);
  assert.deepEqual(out.map((r) => r.id), ['early', 'late', 'edge']);
});

test('filterRows user chip matches the call user only', () => {
  const rows = [row('mine', 100), row('other', 200, { userId: 8 }), row('none', 300, { userId: null }), row('gone', 400, { userId: undefined })];
  assert.deepEqual(m.filterRows(rows, 'user', call, T0 - 1000, T0 + 1000).map((r) => r.id), ['mine']);
  assert.deepEqual(m.filterRows(rows, 'user', { ...call, userId: null }, T0 - 1000, T0 + 1000), []);
});

test('filterRows api chip compares controller and action exactly', () => {
  const rows = [
    row('same', 100),
    row('case', 200, { controller: 'orders' }),
    row('action', 300, { action: 'Post' }),
    row('space', 400, { action: 'Get ' })
  ];
  assert.deepEqual(m.filterRows(rows, 'api', call, T0 - 1000, T0 + 1000).map((r) => r.id), ['same']);
});

test('needsServer: nothing loaded asks the server', () => {
  assert.equal(m.needsServer(null, 'everyone', T0, T0 + 1000), true);
});

test('needsServer: chip change after a non-everyone read asks the server', () => {
  const loaded = { start: T0 - 1000, end: T0 + 1000, chip: 'user', truncated: false };
  assert.equal(m.needsServer(loaded, 'api', T0, T0 + 500), true);
  assert.equal(m.needsServer(loaded, 'user', T0, T0 + 500), false);
});

test('needsServer: a narrowed chip on a truncated read asks the server', () => {
  const loaded = { start: T0 - 1000, end: T0 + 1000, chip: 'everyone', truncated: true };
  assert.equal(m.needsServer(loaded, 'user', T0, T0 + 500), true);
  assert.equal(m.needsServer(loaded, 'everyone', T0, T0 + 500), false);
});

test('needsServer: a window outside what was loaded asks the server', () => {
  const loaded = { start: T0 - 5000, end: T0 + 5000, chip: 'everyone', truncated: false };
  assert.equal(m.needsServer(loaded, 'everyone', T0 - 6000, T0), true);
  assert.equal(m.needsServer(loaded, 'everyone', T0, T0 + 6000), true);
});

test('needsServer: narrowing from 15 to 5 minutes after an untruncated everyone read stays in the browser', () => {
  const MIN15 = 15 * 60000;
  const loaded = { start: T0 - MIN15, end: T0 + MIN15, chip: 'everyone', truncated: false };
  assert.equal(m.needsServer(loaded, 'everyone', T0 - 5 * 60000, T0 + 5 * 60000), false);
  assert.equal(m.needsServer(loaded, 'user', T0 - 5 * 60000, T0 + 5 * 60000), false);
  assert.equal(m.needsServer(loaded, 'api', T0 - 5 * 60000, T0 + 5 * 60000), false);
});

test('activityInput everyone: ISO strings, empty cursor, limit, no userId or operation key', () => {
  const input = m.activityInput(['s1', 's2'], T0, T0 + 60000, 'everyone', call);
  assert.deepEqual(input.sourceIds, ['s1', 's2']);
  assert.equal(input.start, '2026-11-12T10:00:00.000Z');
  assert.equal(input.end, '2026-11-12T10:01:00.000Z');
  assert.equal(input.cursor, '');
  assert.equal(input.limit, 100);
  assert.equal('userId' in input, false);
  assert.equal('operation' in input, false);
});

test('activityInput user and api add only their own field', () => {
  const u = m.activityInput(['s1'], T0, T0 + 1000, 'user', call);
  assert.equal(u.userId, 7);
  assert.equal('operation' in u, false);
  const a = m.activityInput(['s1'], T0, T0 + 1000, 'api', call);
  assert.equal(a.operation, 'Orders.Get');
  assert.equal('userId' in a, false);
  for (const input of [u, a, m.activityInput(['s1'], T0, T0 + 1000, 'user', { ...call, userId: null })]) {
    assert.equal(Object.values(input).includes(undefined), false);
  }
  assert.equal('userId' in m.activityInput(['s1'], T0, T0 + 1000, 'user', { ...call, userId: null }), false);
});

test('gapLabel: own row, during, seconds and minutes with a true minus sign', () => {
  assert.equal(m.gapLabel(T0, call, text), 'this call');
  assert.equal(m.gapLabel(T0 + 4000, call, text), 'during this call');
  assert.equal(m.gapLabel(T0 + 10000, call, text), 'during this call');
  assert.equal(m.gapLabel(T0 + 10000 + 12000, call, text), '+12 s');
  assert.equal(m.gapLabel(T0 - 8000, call, text), MIN + '8 s');
  assert.equal(m.gapLabel(T0 + 10000 + 3 * 60000, call, text), '+3 min');
  assert.equal(m.gapLabel(T0 - 3 * 60000, call, text), MIN + '3 min');
  assert.equal(m.gapLabel(T0 - 3000, call, text).charCodeAt(0), 0x2212);
});

test('lightCells: null when not recorded, null numbers stay null', () => {
  assert.equal(m.lightCells(row('a', 0)), null);
  assert.equal(m.lightCells(row('a', 0, { light: null })), null);
  assert.deepEqual(m.lightCells(row('a', 0, { light: { sqlMs: 5, redisMs: null, httpMs: 0 } })), { sqlMs: 5, redisMs: null, httpMs: 0 });
  assert.deepEqual(m.lightCells(row('a', 0, { light: { sqlMs: null, redisMs: null, httpMs: null } })), { sqlMs: null, redisMs: null, httpMs: null });
});

test('isThisCall matches id and source', () => {
  assert.equal(m.isThisCall(row('c1', 0), call), true);
  assert.equal(m.isThisCall(row('c2', 0), call), false);
  assert.equal(m.isThisCall(row('c1', 0, { sourceId: 's2' }), call), false);
});
