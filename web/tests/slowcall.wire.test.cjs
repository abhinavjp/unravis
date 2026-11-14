const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');



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

// The tracing answer parser keeps the fields the Slow call screen reads: bucket busyMs, lane blockSeconds, raw reasonCode and lane events.

const { parseTracing } = loadTypeScript('src/lib/tracing/model.ts');

const win = { start: '2026-11-01T00:00:00Z', end: '2026-11-01T01:00:00Z', axisStart: '2026-11-01T00:00:00Z', axisEnd: '2026-11-01T01:00:00Z', bucketSeconds: 60 };
const bucket = (extra = {}) => ({ start: '2026-11-01T00:00:00Z', count: 1, ...extra });
const answer = (lane) => parseTracing({ window: win, lanes: [{ key: 'sql', label: 'SQL', buckets: [], ...lane }] }).lanes[0];
const event = (extra = {}) => ({ id: 'e1', label: 'Slow query', start: '2026-11-01T00:00:10Z', end: '2026-11-01T00:00:12Z', durationMs: 2000, ...extra });

test('busyMs is kept only as a finite number from zero up', () => {
  const lane = answer({ buckets: [bucket({ busyMs: 1500 }), bucket({ busyMs: 0 }), bucket({ busyMs: -1 }), bucket({ busyMs: '5' }), bucket({ busyMs: null }), bucket()] });
  assert.deepEqual(lane.buckets.map((b) => b.busyMs), [1500, 0, undefined, undefined, undefined, undefined]);
  assert.equal(lane.buckets.some((b, i) => i > 1 && 'busyMs' in b), false);
});

test('blockSeconds is kept only above zero', () => {
  assert.equal(answer({ blockSeconds: 3600 }).blockSeconds, 3600);
  for (const bad of [0, -60, NaN, '60', null]) assert.equal('blockSeconds' in answer({ blockSeconds: bad }), false);
  assert.equal('blockSeconds' in answer({}), false);
});

test('reasonCode is kept as text and the narrowed fields are unchanged', () => {
  const timeout = answer({ reasonCode: 'timeout' });
  assert.equal(timeout.reasonCode, 'timeout');
  assert.equal(timeout.incompleteReason, 'timeout');
  const empty = answer({ reasonCode: 'not-imported' });
  assert.equal(empty.reasonCode, 'not-imported');
  assert.equal(empty.emptyBecause, 'not-imported');
  assert.equal(empty.incompleteReason, undefined);
  assert.equal('reasonCode' in answer({ reasonCode: '' }), false);
  assert.equal('reasonCode' in answer({ reasonCode: 7 }), false);
  assert.equal('reasonCode' in answer({}), false);
});

test('events keep the readable items and drop the bad ones', () => {
  const lane = answer({
    events: [event(), event({ id: 'e2', durationMs: null, kind: 'http-error' }), event({ id: '' , label: 5 }), event({ start: 'nope' }), event({ end: 3 }),
      event({ durationMs: -1 }), event({ durationMs: 'x' }), null, 'text', 4]
  });
  assert.deepEqual(lane.events, [
    { id: 'e1', label: 'Slow query', start: '2026-11-01T00:00:10Z', end: '2026-11-01T00:00:12Z', durationMs: 2000 },
    { id: 'e2', label: 'Slow query', start: '2026-11-01T00:00:10Z', end: '2026-11-01T00:00:12Z', durationMs: null, kind: 'http-error' }
  ]);
});

test('no events, an empty list or only bad items leaves the field off the lane', () => {
  for (const events of [undefined, [], [null, { id: 1 }], 'x', {}]) assert.equal('events' in answer({ events }), false);
});
