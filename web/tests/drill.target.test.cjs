const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

// Drill target: slot to query, source fallback, nearby bounds.

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

const target = loadTypeScript('src/lib/drill/target.ts');

const window = {
  start: '2026-01-01T10:00:00.000Z',
  end: '2026-01-01T11:00:00.000Z',
  axisStart: '2026-01-01T10:00:00.000Z',
  axisEnd: '2026-01-01T11:00:00.000Z',
  bucketSeconds: 60
};

test('slotWindow gives slot 0 at the axis start and the last slot at its end', () => {
  const first = target.slotWindow(window, 0);
  assert.equal(first.start, '2026-01-01T10:00:00.000Z');
  assert.equal(first.end, '2026-01-01T10:01:00.000Z');
  const last = target.slotWindow(window, 59);
  assert.equal(last.start, '2026-01-01T10:59:00.000Z');
  assert.equal(last.end, '2026-01-01T11:00:00.000Z');
});

test('drillSourceIds falls back to listed sources without noFile when sourcesRead is empty', () => {
  const lane = { sourcesRead: [] };
  const sources = [
    { sourceId: 'a', label: 'A' },
    { sourceId: 'b', label: 'B', noFile: true },
    { sourceId: 'a', label: 'A again' }
  ];
  assert.deepEqual(target.drillSourceIds(lane, sources), ['a']);
  assert.deepEqual(target.drillSourceIds({ sourcesRead: [{ sourceId: 'x', label: 'X' }] }, sources), ['x']);
});

test('drillSourceIds caps the list at 8, the server limit', () => {
  const sources = Array.from({ length: 80 }, (_, i) => ({ sourceId: 's' + i, label: 'L' }));
  assert.equal(target.drillSourceIds({ sourcesRead: sources }, []).length, 8);
});

test('drillSourceIds can be empty when every listed source has no file', () => {
  assert.deepEqual(target.drillSourceIds({ sourcesRead: [] }, [{ sourceId: 'b', label: 'B', noFile: true }]), []);
});

test('slotWindow returns null for an unusable axis start, slot or bucket', () => {
  assert.equal(target.slotWindow({ ...window, axisStart: 'nope' }, 0), null);
  assert.equal(target.slotWindow(window, NaN), null);
  assert.equal(target.slotWindow(window, -1), null);
  assert.equal(target.slotWindow({ ...window, bucketSeconds: 0 }, 0), null);
});

test('drillQuery bucket follows the width: 60 s gives 60, 1 s gives the 10 s floor', () => {
  const mk = (s) => target.drillQuery({ start: '2026-01-01T10:00:00.000Z', end: new Date(Date.parse('2026-01-01T10:00:00.000Z') + s * 1000).toISOString() }, ['a']);
  assert.equal(mk(60).bucketSeconds, 60);
  assert.equal(mk(1).bucketSeconds, 10);
});

test('drillQuery baseline length equals the width and the baseline ends at start', () => {
  const q = target.drillQuery({ start: '2026-01-01T10:00:00.000Z', end: '2026-01-01T10:05:00.000Z' }, ['a']);
  const width = Date.parse(q.end) - Date.parse(q.start);
  assert.equal(Date.parse(q.baselineEnd) - Date.parse(q.baselineStart), width);
  assert.equal(q.baselineEnd, q.start);
  assert.equal(q.kind, 'all');
  assert.equal(q.outcome, 'all');
  assert.equal(q.metric, 'total');
  assert.deepEqual(q.sourceIds, ['a']);
});

test('drillQuery bucketSeconds is at least 10 and at most 86400', () => {
  const tiny = target.drillQuery({ start: '2026-01-01T10:00:00.000Z', end: '2026-01-01T10:00:01.000Z' }, []);
  assert.ok(tiny.bucketSeconds >= 10);
  const huge = target.drillQuery({ start: '2026-01-01T00:00:00.000Z', end: '2026-03-01T00:00:00.000Z' }, []);
  assert.equal(huge.bucketSeconds, 86400);
});

test('pickCall returns null for an empty or missing page and the first complete row otherwise', () => {
  assert.equal(target.pickCall(null), null);
  assert.equal(target.pickCall({ rows: [] }), null);
  const page = { rows: [{ sourceId: '', id: 'x' }, { sourceId: 's', id: 'r1' }, { sourceId: 's', id: 'r2' }] };
  assert.deepEqual(target.pickCall(page), { sourceId: 's', id: 'r1' });
});

test('nearbyBounds gives null when both times are null and plus or minus 15 minutes by default', () => {
  assert.equal(target.nearbyBounds({ initiatedAt: null, completedAt: null }), null);
  assert.equal(target.nearbyBounds({ initiatedAt: 'not a date', completedAt: null }), null);
  const bounds = target.nearbyBounds({ initiatedAt: '2026-01-01T10:00:00.000Z', completedAt: null });
  assert.equal(bounds.start, '2026-01-01T09:45:00.000Z');
  assert.equal(bounds.end, '2026-01-01T10:15:00.000Z');
  const fallback = target.nearbyBounds({ initiatedAt: null, completedAt: '2026-01-01T10:00:00.000Z' }, 5);
  assert.equal(fallback.start, '2026-01-01T09:55:00.000Z');
});
