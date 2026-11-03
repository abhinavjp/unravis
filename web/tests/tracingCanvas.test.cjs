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
  absoluteSlot, clampArrow, zoomFocus, stepSlot, edgeSlot, tickStep, tickOffsets, latencySegments, PERCENTILES, PERCENTILE_LABEL, percentileValue, hasLatency,
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

test('zoom focus: a selected bucket wins, else the data decides, never an empty stretch (UI-R1-03)', () => {
  const r = fullRange(36);
  assert.equal(zoomFocus(r, 10, [{ index: 30, count: 5 }]), 10); // the selection wins
  assert.equal(zoomFocus(r, 99, [{ index: 30, count: 5 }]), 30); // a selection outside the range is ignored
  assert.equal(zoomFocus(r, null, []), undefined); // no data: caller zooms on the middle
  assert.equal(zoomFocus(r, null, [{ index: 4, count: 0 }]), undefined); // empty buckets are not data
  // the data span (index 20 to 22) fits the next width, so its middle is the focus
  assert.equal(zoomFocus(r, null, [{ index: 20, count: 1 }, { index: 22, count: 9 }]), 21);
  // a span wider than the next width falls back to the tallest bucket
  assert.equal(zoomFocus(r, null, [{ index: 2, count: 3 }, { index: 30, count: 50 }]), 30);
  // data outside the current range does not count
  assert.equal(zoomFocus({ from: 0, to: 10 }, null, [{ index: 30, count: 5 }]), undefined);
});

test('repeated zoom in on one burst keeps the burst in view (UI-R1-03)', () => {
  let range = fullRange(60);
  const bars = [{ index: 12, count: 4 }, { index: 13, count: 9 }];
  for (let step = 0; step < 6; step += 1) {
    range = zoomIn(range, 60, zoomFocus(range, null, bars));
    assert.ok(range.from <= 13 && range.to > 12, 'the burst stays in the window at step ' + step + ': ' + JSON.stringify(range));
  }
});

test('keyboard stepping skips slots outside the window and jumps to the first and last (UI-R1-15)', () => {
  const r = { from: 0, to: 10 };
  const outside = (slot) => slot === 0 || slot === 9;
  assert.equal(stepSlot(r, 5, 1, outside), 6);
  assert.equal(stepSlot(r, 8, 1, outside), 8); // the only slot beyond is outside: stay
  assert.equal(stepSlot(r, 1, -1, outside), 1);
  assert.equal(stepSlot(r, 0, 1, outside), 1); // leaving an outside slot works
  assert.equal(edgeSlot(r, 'first', outside), 1);
  assert.equal(edgeSlot(r, 'last', outside), 8);
  assert.equal(edgeSlot({ from: 3, to: 6 }, 'first', outside), 3);
  assert.equal(edgeSlot({ from: 3, to: 6 }, 'last', outside), 5);
  assert.equal(edgeSlot({ from: 0, to: 1 }, 'last', () => true), 0); // all outside: the range edge
});

test('axis ticks are thinned to the plot width so labels never overlap', () => {
  assert.deepEqual(tickOffsets(0, 500, 6), []);
  assert.equal(tickOffsets(36, 0, 6)[0], 0); // unmeasured: the count rule only
  assert.ok(tickOffsets(36, 0, 6).length <= 6);
  assert.equal(tickOffsets(36, 1000, 6).length, 6); // a wide plot keeps the old six labels
  const labelChars = 12; // "19 Oct 10:20"
  for (const [shown, widthPx] of [[36, 259], [120, 225], [36, 560], [120, 900], [60, 300]]) {
    const offsets = tickOffsets(shown, widthPx, labelChars);
    assert.equal(offsets[0], 0);
    const slotPx = widthPx / shown;
    for (let i = 1; i < offsets.length; i += 1) {
      const gap = (offsets[i] - offsets[i - 1]) * slotPx;
      assert.ok(gap >= labelChars * 7, 'labels at ' + shown + ' slots in ' + widthPx + 'px are ' + gap + 'px apart');
    }
  }
  assert.ok(tickStep(36, 100, 12) > tickStep(36, 1000, 12));
  assert.equal(tickOffsets(1, 100, 12).length, 1);
});

test('latency segments split on gaps, scale to the tallest visible value and stay absolute (WEB-R1-07)', () => {
  const grid = { firstMs: Date.parse('2026-10-01T00:00:00Z'), widthMs: 60000 };
  const at = (minute, extra) => ({ start: new Date(grid.firstMs + minute * 60000).toISOString(), count: 1, errors: 0, ...extra });
  const buckets = [at(0, { p95Ms: 100 }), at(1, { p95Ms: 200 }), at(3, { p95Ms: 50 }), at(4), at(5, { p95Ms: 200 })];
  const out = latencySegments(buckets, grid, { from: 0, to: 10 }, 'p95');
  assert.equal(out.max, 200);
  assert.equal(out.segments.length, 3); // 0-1, 3, 5: gaps at 2 and 4
  assert.equal(out.segments[0], '5.00,50.00 15.00,0.00');
  // zoomed: only visible buckets count, and x is relative to the range
  const zoomed = latencySegments(buckets, grid, { from: 3, to: 7 }, 'p95');
  assert.equal(zoomed.max, 200);
  assert.equal(zoomed.segments[0].split(' ')[0].split(',')[0], '12.50');
  // nothing to draw
  assert.deepEqual(latencySegments(buckets, grid, { from: 0, to: 10 }, 'p50'), { segments: [], max: 0 });
  assert.deepEqual(latencySegments(buckets, grid, { from: 4, to: 4 }, 'p95'), { segments: [], max: 0 });
  // the larger value wins when two buckets share a slot
  const dup = latencySegments([at(2, { p95Ms: 10 }), at(2, { p95Ms: 40 })], grid, { from: 0, to: 4 }, 'p95');
  assert.equal(dup.max, 40);
});
