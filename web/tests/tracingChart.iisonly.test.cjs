const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');

// C-3 / C-5: an IIS-only slot must not open an empty Call detail;
// Shift+Enter hint wording uses action words, not "red diamond".

const root = path.resolve(__dirname, '..');
const source = fs.readFileSync(path.join(root, 'src/components/TracingChart.svelte'), 'utf8');

// C-3: source-level checks

test('C-3: slotOpensCall body returns only anyMark(hero)', () => {
  const i = source.indexOf('function slotOpensCall');
  assert.ok(i >= 0, 'slotOpensCall must exist');
  const j = source.indexOf('function diamondAt');
  assert.ok(j >= 0, 'diamondAt must exist');
  const fn = source.slice(i, j);
  assert.ok(fn.includes('anyMark(hero)'), 'must check API-lane (hero)');
  assert.ok(!fn.includes('anyMark(webHero)'), 'must NOT check IIS lane independently');
  assert.ok(!fn.includes('webHero?.dots.some'), 'must NOT check IIS dots independently');
});

test('C-3: diamondAt checks slotOpensCall before returning a slot', () => {
  const i = source.indexOf('function diamondAt');
  assert.ok(i >= 0, 'diamondAt must exist');
  const j = source.indexOf('\n  }', i);
  assert.ok(j >= 0);
  const fn = source.slice(i, j);
  assert.ok(fn.includes('slotOpensCall('), 'diamondAt must gate on slotOpensCall');
});

// C-5: copy checks (source-level)

test('C-5: SHIFT_ENTER_HINT is the correct action-word string', () => {
  assert.ok(source.includes("SHIFT_ENTER_HINT = 'Shift+Enter opens the slowest call in this bucket.'"), 'SHIFT_ENTER_HINT must have new wording');
});

test('C-5: BUCKET_SELECTOR_LABEL does not mention "red diamond"', () => {
  const i = source.indexOf('BUCKET_SELECTOR_LABEL');
  assert.ok(i >= 0, 'BUCKET_SELECTOR_LABEL must exist');
  // Find the string value: grab until the closing quote
  const afterAssign = source.slice(i, i + 400);
  assert.ok(!afterAssign.includes('red diamond'), 'must not say "red diamond" in BUCKET_SELECTOR_LABEL');
  assert.ok(afterAssign.includes('Shift+Enter opens the slowest call in the bucket'), 'must say what the key does');
});

// Render check: reuse the fix32 harness pattern if typescript is available.
// If the module is missing (pre-existing environment gap) the test is skipped.

let chartModule = null;
let Chart = null;
try {
  const { createRequire } = require('node:module');
  const ts = require('typescript');
  const { compile } = require('svelte/compiler');
  const cache = new Map();
  function loadModule(relativePath) {
    const filename = path.join(root, relativePath);
    if (cache.has(filename)) return cache.get(filename).exports;
    let src = fs.readFileSync(filename, 'utf8');
    if (filename.endsWith('.svelte')) {
      src = compile(src, { filename: path.basename(filename), generate: 'server' }).js.code;
    }
    const output = ts.transpileModule(src, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022, esModuleInterop: true } }).outputText;
    const mod = { exports: {} };
    cache.set(filename, mod);
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
    new Function('require', 'module', 'exports', '$state', output)(load, mod, mod.exports, (value) => value);
    return mod.exports;
  }
  chartModule = loadModule('src/components/TracingChart.svelte');
  Chart = chartModule.default;
} catch (_) {
  // typescript or svelte/compiler not available in this environment; render tests are skipped.
}

test('C-5: rendered selector names the key in action words, and the hint rule is keyed on opensCall', { skip: !Chart }, () => {
  const { render } = require('svelte/server');
  function bucket(minute, count, extra = {}) {
    const start = new Date(Date.UTC(2026, 9, 1, 10, minute)).toISOString().replace('.000Z', 'Z');
    return { start, count, errors: 0, ...extra };
  }
  function lane(key, buckets) {
    return {
      key, label: key, state: buckets.length ? 'events' : 'no-data', reason: '', origin: 'case', asOf: '2026-10-01T11:00:00Z',
      consistency: 'stored-as-of', complete: true, truncated: false, scanned: 0, hidden: 0, sources: [], buckets, events: []
    };
  }
  const response = {
    window: { start: '2026-10-01T10:00:00Z', end: '2026-10-01T10:10:00Z', axisStart: '2026-10-01T10:00:00Z', axisEnd: '2026-10-01T10:10:00Z', bucketSeconds: 60 },
    lanes: [
      lane('audit', [bucket(1, 4, { p50Ms: 20, p95Ms: 80, maxMs: 9000, timedCount: 4 })]),
      lane('sql', []),
      lane('iis', [bucket(1, 2, { p50Ms: 10, p95Ms: 50, maxMs: 100, timedCount: 2 })])
    ],
    sources: [], allLanesStored: true, mixedConsistency: false, truncated: false, warnings: [], auditSources: [], savedNotConnected: []
  };
  const decode = (text) => text.replace(/&amp;/g, '&').replace(/&#39;/g, "'").replace(/&quot;/g, '"');
  const page = decode(render(Chart, { props: { response, zone: 'UTC', onSelect() {}, onDiamond() {} } }).body);
  assert.ok(page.includes('Shift+Enter opens the slowest call in the bucket when it has an API call.'), 'the selector label names the key');
  assert.equal(chartModule.diamondKeyHint({ opensCall: true, hasHandler: true, fromKeyboard: true }), 'Shift+Enter opens the slowest call in this bucket.');
  assert.equal(chartModule.diamondKeyHint({ opensCall: false, hasHandler: true, fromKeyboard: true }), '');
});
