const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const { loadTypeScript } = require('./helpers/loadTs.cjs');

// BL-429: the settings that keep charts cheap, and a scan of the sources that keeps them that way.

const cp = loadTypeScript('src/lib/chartPerf.ts');
const src = path.resolve(__dirname, '..', 'src');
const read = (file) => fs.readFileSync(path.join(src, file), 'utf8');
function walk(dir, out = []) {
  for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
    const full = path.join(dir, entry.name);
    if (entry.isDirectory()) walk(full, out);
    else if (/\.(ts|svelte)$/.test(entry.name)) out.push(full);
  }
  return out;
}
const files = walk(src).map((f) => ({ file: path.relative(src, f).replace(/\\/g, '/'), text: fs.readFileSync(f, 'utf8') }));

test('lineSeriesPerf: a short line keeps a dot on every point', () => {
  const o = cp.lineSeriesPerf(Array.from({ length: 39 }, (_, i) => i), 6);
  assert.deepEqual(o, { showSymbol: true, symbolSize: 6 });
});

test('lineSeriesPerf: from 40 points the dots go, and an unbroken line shows none', () => {
  const o = cp.lineSeriesPerf(Array.from({ length: 40 }, (_, i) => i), 6);
  assert.equal(o.showSymbol, false);
});

test('lineSeriesPerf: a point alone between gaps keeps its dot, the rest are size 0', () => {
  const data = Array.from({ length: 60 }, (_, i) => (i < 10 || i > 12 ? i : null));
  data[30] = 5; // alone
  data[31] = null; data[29] = null;
  const o = cp.lineSeriesPerf(data, 5);
  assert.equal(o.showSymbol, true);
  assert.equal(o.symbolSize(5, { dataIndex: 30 }), 5);
  assert.equal(o.symbolSize(5, { dataIndex: 3 }), 0);
});

test('lonePointFlags reads plain numbers, [t, v] pairs and { value } items', () => {
  assert.deepEqual(cp.lonePointFlags([null, 1, null, 2, 3]), [false, true, false, false, false]);
  assert.deepEqual(cp.lonePointFlags([[0, null], [1, 4], [2, null], [3, 5], [4, 6]]), [false, true, false, false, false]);
  assert.deepEqual(cp.lonePointFlags([{ value: null }, { value: 1 }, { value: null }, { value: 2 }, { value: 3 }]), [false, true, false, false, false]);
  assert.deepEqual(cp.lonePointFlags([1, 2]), [true, true], 'under three points every value is its own');
});

test('bigSeriesPerf: nothing below 2000 points; large and chunked from 2000', () => {
  assert.deepEqual(cp.bigSeriesPerf(1999), {});
  assert.deepEqual(cp.bigSeriesPerf(2000), { large: true, largeThreshold: 2000, progressive: 400, progressiveThreshold: 3000 });
});

test('watchSize does nothing while hidden and resizes once on show', () => {
  const calls = [];
  let observerCallback;
  const handlers = new Set();
  globalThis.document = {
    visibilityState: 'visible',
    addEventListener: (type, fn) => handlers.add(fn),
    removeEventListener: (type, fn) => handlers.delete(fn)
  };
  globalThis.ResizeObserver = class { constructor(fn) { observerCallback = fn; } observe() {} disconnect() { calls.push('disconnect'); } };
  try {
    const stop = cp.watchSize({}, () => calls.push('resize'));
    observerCallback();
    assert.deepEqual(calls, ['resize']);
    document.visibilityState = 'hidden'; handlers.forEach((fn) => fn());
    observerCallback(); observerCallback(); observerCallback();
    assert.deepEqual(calls, ['resize'], 'no resize while hidden');
    document.visibilityState = 'visible'; handlers.forEach((fn) => fn());
    assert.deepEqual(calls, ['resize', 'resize'], 'one resize on show for all the sizes seen while hidden');
    handlers.forEach((fn) => fn());
    assert.deepEqual(calls, ['resize', 'resize'], 'and none when nothing changed');
    stop();
    assert.equal(handlers.size, 0);
    assert.equal(calls.at(-1), 'disconnect');
  } finally { delete globalThis.document; delete globalThis.ResizeObserver; }
});

// ---- scans of the sources ----

test('only the Canvas renderer is registered, with no SVG renderer anywhere', () => {
  for (const f of files) assert.ok(!/SVGRenderer/.test(f.text), f.file + ' imports the SVG renderer');
  assert.match(read('lib/charts.ts'), /import \{ CanvasRenderer \} from 'echarts\/renderers'/);
});

test('appendData is never used (it has known bugs in ECharts)', () => {
  for (const f of files) assert.ok(!/\.appendData\(/.test(f.text), f.file + ' calls appendData');
});

test('every option builder turns animation off', () => {
  // The builders that make the options for a screen each say animation: false.
  const builders = [...read('lib/charts.ts').matchAll(/export const (\w+Option) = /g)].map((m) => m[1]);
  const body = read('lib/charts.ts');
  for (const name of builders) {
    const start = body.indexOf('export const ' + name + ' = ');
    const next = body.indexOf('\nexport ', start + 10);
    const slice = body.slice(start, next < 0 ? body.length : next);
    assert.match(slice, /animation: false/, name + ' must set animation: false');
  }
  assert.match(read('lib/resource.ts'), /animation: false,\n\s+grid: grids/);
  assert.match(read('lib/rollupOverlay.ts'), /animation: false/);
  assert.match(read('components/BlackboxCharts.svelte'), /animation: false/);
  assert.match(read('components/TrafficSlowness.svelte'), /animation:false/);
  assert.match(read('components/RequestVolume.svelte'), /animation:false/);
});

test('every chart that is made is disposed, and every size watcher goes through watchSize', () => {
  for (const f of files) {
    const made = (f.text.match(/echarts\.init\(/g) ?? []).length;
    if (!made || f.file === 'lib/chartZoom.ts') continue;
    assert.ok(/\.dispose\(\)/.test(f.text), f.file + ' makes a chart and never disposes it');
    assert.ok(!/new ResizeObserver/.test(f.text), f.file + ' watches size by itself (use watchSize: it waits while hidden)');
    assert.ok(/watchSize\(/.test(f.text), f.file + ' does not watch its size through watchSize');
  }
});

test('the big charts use the cheap options', () => {
  assert.match(read('components/TrafficSlowness.svelte'), /\.\.\.bigSeriesPerf\(rows\.length\)/);
  const charts = read('lib/charts.ts');
  for (const name of ['client', 'server', 'latency', 'current50', 'current95']) assert.match(charts, new RegExp('lineSeriesPerf\\(' + name + ', '));
  assert.match(read('lib/resource.ts'), /showSymbol: lone\.some\(Boolean\)/);
  assert.match(read('components/BlackboxCharts.svelte'), /showSymbol: cd\.data\.length < 40/);
});

test('page timers go through visibility.ts: no bare setInterval is left in the page code', () => {
  // CaseKeyCard: a lockout countdown of a few seconds that the person is watching (500 ms, ends by itself).
  const allowed = new Set(['lib/visibility.ts', 'components/CaseKeyCard.svelte']);
  for (const f of files) {
    if (allowed.has(f.file)) continue;
    const code = f.text.replace(/\/\/.*$/gm, '').replace(/\/\*[\s\S]*?\*\//g, '');
    assert.ok(!/\bsetInterval\(/.test(code), f.file + ' starts a setInterval (use visiblePoll)');
  }
});
