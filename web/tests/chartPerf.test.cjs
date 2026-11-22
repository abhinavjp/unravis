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
  assert.deepEqual(cp.lineSeriesPerf(Array.from({ length: 39 }, (_, i) => i), 6), { showSymbol: true, symbolSize: 6 });
});

test('lineSeriesPerf: from 40 points the line builds no dot shapes, and keeps its size for the hover dot', () => {
  assert.deepEqual(cp.lineSeriesPerf(Array.from({ length: 40 }, (_, i) => i), 6), { showSymbol: false, symbolSize: 6 });
});

test('lonePointSeries: nothing for a short line or a line with no lone point', () => {
  const short = Array.from({ length: 39 }, (_, i) => (i % 2 ? null : i));
  assert.deepEqual(cp.lonePointSeries(short, { color: '#f00', size: 5 }), []);
  assert.equal(cp.lonePointSeries(short, { color: '#f00', size: 5, always: true }).length, 1, 'always: also for a short line');
  assert.deepEqual(cp.lonePointSeries(Array.from({ length: 60 }, (_, i) => i), { color: '#f00', size: 5 }), []);
});

test('lonePointSeries: one silent, nameless scatter holding only the lone points, with the line\'s colour and axes', () => {
  const data = Array.from({ length: 60 }, (_, i) => (i < 10 || i > 12 ? i : null));
  data[30] = 5; data[29] = null; data[31] = null; // alone
  data[45] = 7; data[44] = null; data[46] = null; // alone
  const [scatter, ...rest] = cp.lonePointSeries(data, { color: '#abc', size: 5, yAxisIndex: 1, z: 3 });
  assert.equal(rest.length, 0);
  assert.equal(scatter.type, 'scatter');
  assert.equal(scatter.name, '', 'no legend entry');
  assert.equal(scatter.silent, true, 'no pointer events');
  assert.deepEqual(scatter.tooltip, { show: false }, 'not in the axis tooltip');
  assert.deepEqual(scatter.data, [[30, 5], [45, 7]]);
  assert.equal(scatter.symbolSize, 5);
  assert.deepEqual(scatter.itemStyle, { color: '#abc' });
  assert.equal(scatter.yAxisIndex, 1);
  assert.equal('xAxisIndex' in scatter, false);
});

test('lonePointSeries keeps [t, value] pairs as they are', () => {
  const pairs = Array.from({ length: 50 }, (_, i) => [1000 + i * 60, i === 20 ? 9 : null]);
  assert.deepEqual(cp.lonePointSeries(pairs, { color: '#000', size: 5 })[0].data, [[1000 + 20 * 60, 9]]);
});

test('what ECharts is asked to build: no shape per point on a long gapped line, and the lone points and the hover dot still draw', () => {
  const echarts = require('echarts/core');
  const { LineChart, ScatterChart } = require('echarts/charts');
  const { GridComponent, TooltipComponent } = require('echarts/components');
  const { SVGRenderer } = require('echarts/renderers');
  echarts.use([LineChart, ScatterChart, GridComponent, TooltipComponent, SVGRenderer]);
  const n = 400;
  const data = Array.from({ length: n }, (_, i) => (i % 4 === 3 ? null : i % 50));
  data[100] = 20; data[99] = null; data[101] = null; // the one lone point
  const shapes = (svg) => (svg.match(/<path/g) || []).length;
  const render = (series) => {
    const chart = echarts.init(null, null, { renderer: 'svg', ssr: true, width: 800, height: 200 });
    chart.setOption({ animation: false, xAxis: { type: 'category', data: data.map((_, i) => 'p' + i) }, yAxis: { type: 'value' }, series });
    const svg = chart.renderToSVGString();
    chart.dispose();
    return svg;
  };
  const line = (extra) => ({ type: 'line', connectNulls: false, data, ...extra });
  // Before this change: a symbol per point, sized 0 except the lone one.
  const lone = cp.lonePointFlags(data);
  const before = render([line({ showSymbol: true, symbolSize: (_v, p) => (lone[p.dataIndex] ? 5 : 0) })]);
  const after = render([line({ ...cp.lineSeriesPerf(data, 5) }), ...cp.lonePointSeries(data, { color: '#c00', size: 5 })]);
  assert.ok(shapes(before) > 200, 'the old option made a shape per point: ' + shapes(before));
  assert.ok(shapes(after) < 40, 'the new one makes a few paths for the line, axes and the one lone point: ' + shapes(after));
  assert.ok(shapes(after) < shapes(before) / 5);
  // The hover dot: highlighting a point of a showSymbol:false line (what the axis pointer does) adds a temporary symbol at symbolSize.
  const chart = echarts.init(null, null, { renderer: 'svg', ssr: true, width: 800, height: 200 });
  chart.setOption({ animation: false, xAxis: { type: 'category', data: data.map((_, i) => 'p' + i) }, yAxis: { type: 'value' }, series: [line({ ...cp.lineSeriesPerf(data, 5) })] });
  const quiet = shapes(chart.renderToSVGString());
  chart.dispatchAction({ type: 'highlight', seriesIndex: 0, dataIndex: 10 });
  assert.equal(shapes(chart.renderToSVGString()), quiet + 1, 'the point under the pointer gets one dot');
  chart.dispose();
});

test('hiding a line in the legend hides its lone dots too (the scatter carries the line\'s name), and the legend still shows one entry', () => {
  const echarts = require('echarts/core');
  const { LineChart, ScatterChart } = require('echarts/charts');
  const { GridComponent, TooltipComponent, LegendComponent } = require('echarts/components');
  const { SVGRenderer } = require('echarts/renderers');
  echarts.use([LineChart, ScatterChart, GridComponent, TooltipComponent, LegendComponent, SVGRenderer]);
  const LINE = '#0a0a0a', DOT = '#c00123';
  const data = Array.from({ length: 60 }, (_, i) => (i % 4 === 3 ? null : i % 9));
  data[30] = 7; data[29] = null; data[31] = null; // the lone point
  const named = (name) => [
    { type: 'line', name: 'Current P95', connectNulls: false, data, lineStyle: { color: LINE }, itemStyle: { color: LINE }, ...cp.lineSeriesPerf(data, 6) },
    ...cp.lonePointSeries(data, { color: DOT, size: 6, name })
  ];
  const make = (series) => {
    const chart = echarts.init(null, null, { renderer: 'svg', ssr: true, width: 800, height: 240 });
    chart.setOption({ animation: false, legend: { type: 'scroll', top: 0 }, xAxis: { type: 'category', data: data.map((_, i) => 'p' + i) }, yAxis: { type: 'value' }, series });
    return chart;
  };
  const dots = (svg) => (svg.match(new RegExp(DOT.slice(1), 'gi')) || []).length;
  const withName = make(named('Current P95'));
  assert.ok(dots(withName.renderToSVGString()) > 0, 'the lone dot is drawn');
  const legendEntries = withName.getModel().getComponent('legend').getData().length;
  assert.equal(legendEntries, 1, 'one legend entry, not two');
  withName.dispatchAction({ type: 'legendUnSelect', name: 'Current P95' });
  assert.equal(dots(withName.renderToSVGString()), 0, 'hidden with its line');
  withName.dispatchAction({ type: 'legendSelect', name: 'Current P95' });
  assert.ok(dots(withName.renderToSVGString()) > 0, 'and back with it');
  withName.dispose();
  // Without the name the dot would stay behind: this is what the name is for.
  const nameless = make(named(undefined));
  nameless.dispatchAction({ type: 'legendUnSelect', name: 'Current P95' });
  assert.ok(dots(nameless.renderToSVGString()) > 0, 'a nameless scatter is not hidden by the legend');
  nameless.dispose();
});

test('every lone-point scatter in charts.ts carries the name of its line', () => {
  const calls = [...read('lib/charts.ts').matchAll(/lonePointSeries\([^)]*\)/g)].map((m) => m[0]);
  assert.equal(calls.length, 5);
  for (const call of calls) assert.match(call, /name: '/, call);
});

test('stripsOption: a long resource line has no dot shapes and its lone points come from the scatter', () => {
  const { stripsOption } = loadTypeScript('src/lib/resource.ts');
  const points = Array.from({ length: 80 }, (_, i) => [i * 60000, i === 40 ? 9 : (i % 5 === 4 ? null : i % 7)]);
  points[39][1] = null; points[41][1] = null;
  const pal = { text: '#000', text2: '#555', axisLine: '#999', grid: '#eee', warn: '#fa0', bad: '#f00', accent: '#00f', series: ['#0a0', '#a0a'], surface: '#fff' };
  const option = stripsOption({ strips: [{ id: 'host-cpu', title: 'CPU', unit: '%', max: 100, lines: [{ name: 'CPU', points }] }], view: { fromMs: 0, toMs: 80 * 60000 }, episodes: [], markers: [], zone: 'UTC', pal });
  const lines = option.series.filter((x) => x.type === 'line' && x.name === 'CPU');
  assert.equal(lines.length, 1);
  assert.equal(lines[0].showSymbol, false);
  const scatters = option.series.filter((x) => x.type === 'scatter');
  assert.equal(scatters.length, 1);
  assert.deepEqual(scatters[0].data, [[40 * 60000, 9]]);
  assert.equal(scatters[0].xAxisIndex, 0);
  assert.equal(scatters[0].yAxisIndex, 0);
  assert.equal(scatters[0].silent, true);
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
  assert.match(read('lib/resource.ts'), /lonePointSeries\(line\.points/);
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
