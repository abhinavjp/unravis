const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

// The chart canvas maths: zoom ranges over absolute bucket slots, latency percentiles and control copy.

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
      const store = path.join(path.dirname(filename), request + '.svelte.ts');
      if (fs.existsSync(plain)) return loadTypeScript(path.relative(root, plain));
      if (fs.existsSync(store)) return loadTypeScript(path.relative(root, store));
    }
    return nodeRequire(request);
  };
  new Function('require', 'module', 'exports', '$state', output)(load, module, module.exports, (value) => value);
  return module.exports;
}

const {
  MIN_ZOOM_SLOTS, fullRange, isZoomed, clampRange, zoomIn, zoomOut, rangeFromDrag,
  absoluteSlot, clampArrow, PERCENTILES, PERCENTILE_LABEL, percentileValue, hasLatency,
  CANVAS_TEXT, zoomedText, canvasCopyEntries
} = loadTypeScript('src/lib/tracing/canvas.ts');
const { lintScreenStrings } = loadTypeScript('src/lib/copy/copylint.ts');
const { deck } = loadTypeScript('src/lib/copy/deck.ts');

test('zoom ranges stay inside the grid', () => {
  let r = zoomIn(fullRange(120), 120);
  assert.equal(r.to - r.from, 60);
  let guard = 0;
  while (r.to - r.from > MIN_ZOOM_SLOTS && guard < 20) {
    r = zoomIn(r, 120);
    guard += 1;
    assert.ok(r.from >= 0 && r.to <= 120 && r.from < r.to);
  }
  assert.equal(r.to - r.from, 4);
  assert.deepEqual(zoomIn(r, 120), r); // already at the narrowest width
  let out = r;
  guard = 0;
  while (isZoomed(out, 120) && guard < 20) {
    out = zoomOut(out, 120);
    guard += 1;
    assert.ok(out.from >= 0 && out.to <= 120);
  }
  assert.deepEqual(out, fullRange(120));
  assert.deepEqual(zoomOut(out, 120), fullRange(120)); // never wider than the grid
  assert.deepEqual(zoomIn(fullRange(120), 120, 2), { from: 0, to: 60 }); // focus near the start is clamped
  assert.deepEqual(zoomIn(fullRange(120), 120, 119), { from: 60, to: 120 }); // focus near the end is clamped
  const tiny = zoomIn(fullRange(3), 3);
  assert.equal(tiny.to - tiny.from, 3); // n below the minimum width shows the whole grid
});

test('drag selects whole bars in either direction', () => {
  assert.deepEqual(rangeFromDrag(120, 30, 10), { from: 10, to: 31 });
  assert.deepEqual(rangeFromDrag(120, 10, 30), { from: 10, to: 31 });
  assert.deepEqual(rangeFromDrag(120, -5, 200), { from: 0, to: 120 }); // out-of-range indexes are clamped
  const oneBar = rangeFromDrag(120, 5, 5);
  assert.equal(oneBar.to - oneBar.from, MIN_ZOOM_SLOTS); // a one-bar drag widens to the minimum
  assert.deepEqual(oneBar, { from: 5, to: 9 });
});

test('reset returns the full window', () => {
  const zoomed = zoomIn(fullRange(120), 120);
  assert.ok(isZoomed(zoomed, 120));
  assert.ok(!isZoomed(fullRange(120), 120));
  assert.equal(zoomedText(60, 120), 'Showing 60 of 120 bars.');
});

test('slot indices stay absolute under zoom', () => {
  const r = zoomIn(fullRange(120), 120);
  assert.deepEqual(r, { from: 30, to: 90 });
  assert.equal(absoluteSlot(r, 0), 30);
  assert.equal(absoluteSlot(r, 0.999), 89);
  assert.equal(clampArrow(r, 30, -1), 30); // the left edge stops the arrow
  assert.equal(clampArrow(r, 89, 1), 89); // the right edge stops the arrow
  assert.equal(clampArrow(r, 50, 1), 51);
});

test('percentile values come only from the returned bucket', () => {
  const bucket = { start: '2026-01-01T10:00:00Z', count: 10, errors: 0, p95Ms: 840 };
  assert.equal(percentileValue(bucket, 'p95'), 840);
  assert.equal(percentileValue(bucket, 'p50'), null); // a bucket without p50Ms has no P50
  assert.equal(percentileValue({ ...bucket, p50Ms: -3 }, 'p50'), null);
  assert.equal(percentileValue({ ...bucket, p50Ms: Number.NaN }, 'p50'), null);
  assert.equal(hasLatency([], 'p95'), false);
  assert.equal(hasLatency([{ start: '2026-01-01T10:00:00Z', count: 4, errors: 0 }, bucket], 'p95'), true);
  assert.deepEqual(PERCENTILES, ['p50', 'p95']);
  assert.equal(PERCENTILE_LABEL.p50, 'Typical (P50)');
  assert.equal(PERCENTILE_LABEL.p95, 'Slow end (P95)');
});

test('canvas copy passes the screen-string lint', () => {
  assert.deepEqual(lintScreenStrings(canvasCopyEntries(), deck.terms), []);
});

const canvasSource = fs.readFileSync(path.join(root, 'src/lib/tracing/canvas.ts'), 'utf8');

test('the canvas maths stays out of the guided and chart code', () => {
  assert.doesNotMatch(canvasSource, /from '.*(guided|charts)/);
  assert.ok(CANVAS_TEXT.dragHint.length > 0);
  assert.deepEqual(clampRange({ from: -10, to: 500 }, 120), fullRange(120));
});
