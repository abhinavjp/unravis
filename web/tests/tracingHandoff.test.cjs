const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

// WP-61: a clicked chart bar becomes the analysis window.

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

const { slotToBucket, bucketToWindow, handOffBucket, handoffCopyEntries } = loadTypeScript('src/lib/tracing/handoff.ts');
const { lintScreenStrings } = loadTypeScript('src/lib/copy/copylint.ts');
const { deck } = loadTypeScript('src/lib/copy/deck.ts');

const lane = (key, buckets, extra = {}) => ({ key, state: 'events', buckets, ...extra });
const response = (lanes, windowStart = '2026-10-20T09:00:00Z') => ({
  window: { start: windowStart, end: '2026-10-20T10:00:00Z', axisStart: '2026-10-20T09:00:00Z', axisEnd: '2026-10-20T10:00:00Z', bucketSeconds: 300 },
  lanes
});
const bucket = { start: '2026-10-20T09:05:00Z', count: 4, errors: 0 };

test('slotToBucket gives the slot start and end from the server axis', () => {
  assert.deepEqual(slotToBucket(response([lane('audit', [bucket])]), {}, 1), { start: '2026-10-20T09:05:00.000Z', end: '2026-10-20T09:10:00.000Z' });
});

test('an empty slot, an out-of-range slot or a slot outside the window gives null', () => {
  const r = response([lane('audit', [bucket])]);
  for (const i of [2, -1, 12, 1.5]) assert.equal(slotToBucket(r, {}, i), null, String(i));
  const clipped = response([lane('audit', [{ start: '2026-10-20T09:00:00Z', count: 3, errors: 0 }])], '2026-10-20T09:07:00Z');
  assert.equal(slotToBucket(clipped, {}, 0), null);
});

test("a hidden lane's bars do not count", () => {
  const r = response([lane('sql', [bucket])]);
  assert.equal(slotToBucket(r, { sql: false }, 1), null);
  assert.deepEqual(slotToBucket(r, {}, 1), { start: '2026-10-20T09:05:00.000Z', end: '2026-10-20T09:10:00.000Z' });
});

test('a disabled lane or one that does not draw bars cannot hand off', () => {
  const disabled = response([lane('sql', [bucket], { disabled: 'tz-rule' })]);
  assert.equal(slotToBucket(disabled, {}, 1), null);
  const noData = response([lane('iis', [bucket], { state: 'no-data' })]);
  assert.equal(slotToBucket(noData, {}, 1), null);
  const partial = response([lane('iis', [bucket], { state: 'incomplete' })]);
  assert.notEqual(slotToBucket(partial, {}, 1), null, 'an incomplete lane still draws the bars it has');
  const both = response([lane('sql', [bucket], { disabled: 'tz-rule' }), lane('audit', [bucket])]);
  assert.notEqual(slotToBucket(both, {}, 1), null, 'another lane that does draw it still counts');
});

test('laneDrawsBars and isLaneVisible follow the chart rule', () => {
  const { laneDrawsBars, isLaneVisible } = loadTypeScript('src/lib/tracing/handoff.ts');
  assert.equal(laneDrawsBars({ state: 'events' }, []), true);
  assert.equal(laneDrawsBars({ state: 'events', disabled: 'tz-rule' }, [{}]), false);
  assert.equal(laneDrawsBars({ state: 'incomplete' }, []), false);
  assert.equal(laneDrawsBars({ state: 'incomplete' }, [{}]), true);
  assert.equal(laneDrawsBars({ state: 'no-data' }, [{}]), false);
  assert.equal(isLaneVisible({}, 'sql'), true);
  assert.equal(isLaneVisible({ sql: false }, 'sql'), false);
  assert.equal(isLaneVisible({ sql: false }, 'iis'), true);
});

test('bucketToWindow returns the start and end of a valid bucket', () => {
  const b = { start: '2026-10-20T09:00:00Z', end: '2026-10-20T09:05:00Z' };
  assert.deepEqual(bucketToWindow(b), b);
});

test('bucketToWindow refuses a missing, reversed, equal or unparsable bucket', () => {
  assert.equal(bucketToWindow(null), null);
  assert.equal(bucketToWindow(undefined), null);
  assert.equal(bucketToWindow({ start: '2026-10-20T09:05:00Z', end: '2026-10-20T09:00:00Z' }), null);
  assert.equal(bucketToWindow({ start: '2026-10-20T09:00:00Z', end: '2026-10-20T09:00:00Z' }), null);
  assert.equal(bucketToWindow({ start: 'x', end: 'y' }), null);
});

test('handOffBucket sets the window first and opens the analysis second, once', () => {
  const calls = [];
  const b = { start: '2026-10-20T09:00:00Z', end: '2026-10-20T09:05:00Z' };
  const ok = handOffBucket(b, { setWindow: (w) => calls.push(['set', w]), open: () => calls.push(['open']) });
  assert.equal(ok, true);
  assert.deepEqual(calls.map((c) => c[0]), ['set', 'open']);
  assert.deepEqual(calls[0][1], b);
});

test('handOffBucket does nothing for a refused bucket', () => {
  const calls = [];
  const deps = { setWindow: () => calls.push('set'), open: () => calls.push('open') };
  assert.equal(handOffBucket({ start: '2026-10-20T09:05:00Z', end: '2026-10-20T09:00:00Z' }, deps), false);
  assert.equal(handOffBucket(null, deps), false);
  assert.deepEqual(calls, []);
});

test("the paper wires the chart's onSelect to the analysis", () => {
  const paper = read('src/components/TracingPaper.svelte');
  for (const re of [/onSelect=/, /slotToBucket/, /handOffBucket/, /setWindowFromHour/, /openAnalysis/]) assert.match(paper, re);
  const chart = read('src/components/TracingChart.svelte');
  assert.match(chart, /onSelect/);
  assert.doesNotMatch(chart, /onBucketClick|echarts/);
});

test('the hand-off imports nothing from the guided folders', () => {
  for (const file of ['src/lib/tracing/handoff.ts', 'src/components/TracingChart.svelte', 'src/components/TracingPaper.svelte']) {
    const imports = read(file).split(/\r?\n/).filter((line) => /^\s*import\b|\bfrom\s+['"]/.test(line));
    for (const line of imports) assert.doesNotMatch(line, /\/guided\//, file);
  }
});

test('hand-off copy passes the screen-string lint', () => {
  assert.deepEqual(lintScreenStrings(handoffCopyEntries(), deck.terms), []);
});
