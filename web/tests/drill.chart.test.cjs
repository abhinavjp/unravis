const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const { compile } = require('svelte/compiler');
const { render } = require('svelte/server');
const ts = require('typescript');

function between(text, from, to) {
  const a = text.indexOf(from);
  assert.ok(a >= 0, `missing: ${from}`);
  const b = to == null ? text.length : text.indexOf(to, a + from.length);
  assert.ok(b >= 0, `missing: ${to}`);
  return text.slice(a, b);
}

const root = path.resolve(__dirname, '..');
// W-chart: a red diamond click and Shift+Enter open the drill-in. The Svelte file is read as text.
const src = fs.readFileSync(path.join(root, 'src/components/TracingChart.svelte'), 'utf8');
const chartCache = new Map();
function loadModule(relativePath) {
  const filename = path.join(root, relativePath);
  if (chartCache.has(filename)) return chartCache.get(filename).exports;
  let source = fs.readFileSync(filename, 'utf8');
  if (filename.endsWith('.svelte')) {
    source = compile(source, { filename: path.basename(filename), generate: 'server' }).js.code;
  }
  const output = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022, esModuleInterop: true } }).outputText;
  const module = { exports: {} };
  chartCache.set(filename, module);
  const nodeRequire = createRequire(filename);
  const load = (request) => {
    if (/^\.\.?\//.test(request)) {
      for (const candidate of [request, request + '.ts', request + '.svelte.ts']) {
        const full = path.join(path.dirname(filename), candidate);
        if (fs.existsSync(full) && fs.statSync(full).isFile()) return loadModule(path.relative(root, full));
      }
    }
    return nodeRequire(request);
  };
  new Function('require', 'module', 'exports', '$state', output)(load, module, module.exports, (v) => v);
  return module.exports;
}
function chartBody(extra = {}) {
  const Chart = loadModule('src/components/TracingChart.svelte').default;
  const start = '2026-10-01T10:00:00Z';
  const bucket = { start: '2026-10-01T10:01:00Z', count: 4, errors: 0, p50Ms: 20, p95Ms: 80, maxMs: 9000, timedCount: 4 };
  const lane = (key) => ({ key, label: key, state: 'events', reason: '', origin: 'case', asOf: start, consistency: 'stored-as-of', complete: true, truncated: false, scanned: 0, hidden: 0, sources: [], buckets: [bucket], events: [] });
  const response = { window: { start, end: '2026-10-01T10:10:00Z', axisStart: start, axisEnd: '2026-10-01T10:10:00Z', bucketSeconds: 60 }, lanes: [lane('audit'), lane('sql'), lane('iis')], sources: [], allLanesStored: true, mixedConsistency: false, truncated: false, warnings: [], auditSources: [], savedNotConnected: [] };
  return render(Chart, { props: { response, zone: 'UTC', onSelect() {}, onDiamond() {}, ...extra } }).body;
}

test('the onDiamond prop exists and is optional', () => {
  assert.match(src, /onDiamond\?: \(slot: number\) => void;/);
});

test('the flagged mark carries its slot', () => {
  assert.match(src, /class:flagged=\{mark\.flagged\} data-slot=\{mark\.index\}/);
  const page = chartBody();
  assert.ok(page.indexOf('data-slot=') >= 0, 'rendered marks carry data-slot');
  assert.ok(page.indexOf('class="mark') >= 0);
});

test('the click hit-tests the diamonds and IIS dots before selecting', () => {
  assert.match(src, /import \{ hitFlagged/);
  assert.match(src, /\.panel\[data-lane="audit"\] \.mark, /);
  assert.match(src, /getBoundingClientRect\(\)/);
  assert.match(src, /hitFlagged\(rects, event\.clientX, event\.clientY, 6\)/);
  const click = between(src, 'function onOverlayClick', 'function showTouchTip');
  assert.ok(click.indexOf('if (onDiamond') >= 0);
  assert.ok(click.indexOf('selectAt(slot)') >= 0);
  assert.ok(click.indexOf('if (onDiamond') < click.indexOf('selectAt(slot)'));
  assert.ok(click.indexOf('if (onDiamond') < click.indexOf('showTouchTip(slot, event)'));
});

test('diamonds in the IIS and SQL lanes and IIS dots also open the Slow call screen', () => {
  assert.ok(src.includes('.panel[data-lane="iis"] .mark,'), 'IIS marks, flagged or not, are click targets');
  assert.ok(src.includes('.panel[data-lane="iis"] .dot'));
  assert.ok(src.includes('.panel[data-lane="sql"] .mark.flagged'));
  assert.ok(src.includes('<span class="dot" data-slot={dot.index}'));
  const fn = between(src, 'function slotOpensCall', 'function diamondAt');
  assert.ok(fn.includes('anyMark(hero)'), 'every API-lane mark opens Call detail');
  assert.ok(!fn.includes('anyMark(webHero)'), 'IIS-lane mark does not open Call detail independently');
  assert.ok(!fn.includes('webHero?.dots.some'), 'IIS dots do not open Call detail independently');
  assert.ok(src.includes('.panel[data-lane="audit"] .mark,'), 'API marks, flagged or not, are click targets');
  assert.doesNotMatch(src, /auditMarkFlagged/);
});

test('Shift+Enter opens a flagged bucket; plain Enter still selects', () => {
  assert.match(src, /event\.shiftKey && onDiamond && slotOpensCall\(current\)\) onDiamond\(current\); else onSelect\?\.\(current\)/);
  assert.match(src, /onSelect\?\.\(selectedSlot\)/);
});
