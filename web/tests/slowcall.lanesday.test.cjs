const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

// A saved SQL day carries the Query Store state of its lane through toDay/toLane.

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

const { toDay, toLane } = loadTypeScript('src/lib/lanes.ts');

const dayWith = (queryStore, withKey = true) => toDay({ id: 1, kind: 'sql', lane: withKey ? { checks: [], queryStore } : { checks: [] } });

test('each known Query Store state passes through', () => {
  for (const state of ['on', 'off', 'read-only', 'denied', 'unknown']) {
    const qs = dayWith({ state, firstData: '2026-10-01T00:00:00Z', intervalMinutes: 60 }).lane.queryStore;
    assert.deepEqual(qs, { state, firstData: '2026-10-01T00:00:00Z', intervalMinutes: 60 });
  }
});

test('a lane without the key stays without it', () => {
  const lane = dayWith(undefined, false).lane;
  assert.equal(lane.queryStore, undefined);
  assert.equal('queryStore' in lane, false);
});

test('an unknown state or a non-object is dropped, never made up', () => {
  for (const bad of [{ state: 'hacked' }, { state: 'ON' }, { state: 5 }, {}, 'on', 7, null, ['on']]) {
    const lane = dayWith(bad).lane;
    assert.equal(lane.queryStore, undefined);
    assert.equal('queryStore' in lane, false);
  }
  assert.equal(toLane({ checks: [], queryStore: { state: '__proto__' } }).queryStore, undefined);
});

test('a bad firstData or an interval that is not above zero becomes null', () => {
  for (const firstData of ['not a date', '', 12, {}, null]) {
    assert.equal(dayWith({ state: 'on', firstData }).lane.queryStore.firstData, null);
  }
  for (const intervalMinutes of [0, -5, NaN, Infinity, '60', null, {}]) {
    assert.equal(dayWith({ state: 'on', intervalMinutes }).lane.queryStore.intervalMinutes, null);
  }
  const bare = dayWith({ state: 'denied' }).lane.queryStore;
  assert.deepEqual(bare, { state: 'denied', firstData: null, intervalMinutes: null });
});
