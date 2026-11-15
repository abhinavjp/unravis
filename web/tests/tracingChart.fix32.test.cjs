const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');
const { compile } = require('svelte/compiler');
const { render } = require('svelte/server');

// FIX-32 (UI-05, UI-11): the chart names Shift+Enter in plain words, the lane hint is a real button that
// Esc can close, and the bucket selector is a slider (it has a value and arrow keys), not a plain group.
// The real component is rendered on the server side of Svelte, in memory; synthetic data only.

const root = path.resolve(__dirname, '..');
const cache = new Map();
function loadModule(relativePath) {
  const filename = path.join(root, relativePath);
  if (cache.has(filename)) return cache.get(filename).exports;
  let source = fs.readFileSync(filename, 'utf8');
  if (filename.endsWith('.svelte')) {
    source = compile(source, { filename: path.basename(filename), generate: 'server' }).js.code;
  }
  const output = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022, esModuleInterop: true } }).outputText;
  const module = { exports: {} };
  cache.set(filename, module);
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
  new Function('require', 'module', 'exports', '$state', output)(load, module, module.exports, (value) => value);
  return module.exports;
}
const read = (relative) => fs.readFileSync(path.join(root, relative), 'utf8');

const chartModule = loadModule('src/components/TracingChart.svelte');
const Chart = chartModule.default;
const source = read('src/components/TracingChart.svelte');

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
function response() {
  return {
    window: { start: '2026-10-01T10:00:00Z', end: '2026-10-01T10:10:00Z', axisStart: '2026-10-01T10:00:00Z', axisEnd: '2026-10-01T10:10:00Z', bucketSeconds: 60 },
    lanes: [lane('audit', [bucket(1, 4, { p50Ms: 20, p95Ms: 80, maxMs: 90, timedCount: 4 }), bucket(2, 3, { p50Ms: 25, p95Ms: 90, maxMs: 9000, timedCount: 3 })]), lane('sql', []), lane('iis', [])],
    sources: [], allLanesStored: true, mixedConsistency: false, truncated: false, warnings: [], auditSources: [], savedNotConnected: []
  };
}
const html = (extra = {}) => render(Chart, { props: { response: response(), zone: 'UTC', onSelect() {}, onDiamond() {}, ...extra } }).body;
const decode = (text) => text.replace(/&amp;/g, '&').replace(/&#39;/g, "'").replace(/&quot;/g, '"');

test('UI-05: the bucket selector names Shift+Enter in plain words', () => {
  const page = decode(html());
  const label = /aria-label="(Time buckets\.[^"]*)"/.exec(page);
  assert.ok(label, 'the bucket selector keeps its Time buckets label');
  assert.match(label[1], /Enter opens Run analysis for the selected bucket\./);
  assert.match(label[1], /Shift\+Enter opens the slowest call/);
  assert.doesNotMatch(label[1], /red diamond/);
});

test('UI-05: the keyboard tooltip names the key only on a bucket with a red diamond', () => {
  assert.equal(typeof chartModule.diamondKeyHint, 'function', 'the hint rule is exported for tests');
  const hint = chartModule.diamondKeyHint;
  assert.match(hint({ opensCall: true, hasHandler: true, fromKeyboard: true }), /Shift\+Enter opens the slowest call/);
  assert.equal(hint({ opensCall: false, hasHandler: true, fromKeyboard: true }), '');
  assert.equal(hint({ opensCall: true, hasHandler: false, fromKeyboard: true }), '');
  assert.equal(hint({ opensCall: true, hasHandler: true, fromKeyboard: false }), '', 'a pointer user clicks the diamond, so the key is not mentioned');
});

test('UI-05: the diamonds themselves stay hidden from the tab order', () => {
  const page = html();
  assert.doesNotMatch(page, /<span class="mark[^"]*"[^>]*tabindex/);
  assert.match(page, /<span class="mark [^"]*flagged"[^>]*aria-hidden="true"/);
});

test('UI-11: each lane hint is a focusable button that describes itself by the tooltip', () => {
  const page = html();
  for (const key of ['audit', 'sql', 'iis']) {
    const hintAt = page.indexOf(`id="tc-hint-${key}"`);
    const head = page.slice(page.lastIndexOf('<div class="lane-label', hintAt), hintAt);
    const button = /<button[^>]*class="hint-btn[^"]*"[^>]*>/.exec(head);
    assert.ok(button, 'lane ' + key + ' has a hint button in its head');
    assert.match(button[0], /type="button"/);
    assert.match(button[0], new RegExp(`aria-describedby="tc-hint-${key}"`));
    assert.match(button[0], /aria-label="[^"]+"/);
    assert.doesNotMatch(button[0], /tabindex="-1"/);
    assert.doesNotMatch(head.slice(0, head.indexOf('>')), /aria-describedby/, 'the group no longer carries the description twice');
  }
  assert.equal((page.match(/role="tooltip" class="hint[ "]/g) ?? []).length, 3);
  // The lane head itself still has no action and is not a tab stop.
  assert.doesNotMatch(page, /<div class="lane-label[^>]*tabindex/);
});

test('UI-11: Esc closes an open hint without reaching anything else, once', () => {
  const { openHints, escapeHints, forgetHint } = chartModule;
  assert.equal(typeof openHints, 'function');
  // Nothing is showing: Esc is not ours.
  assert.deepEqual(escapeHints({ hover: null, focus: null, closed: [] }), { handled: false, closed: [] });
  // A hint shown by focus is closed and Esc is consumed.
  let result = escapeHints({ hover: null, focus: 'sql', closed: [] });
  assert.equal(result.handled, true);
  assert.deepEqual(result.closed, ['sql']);
  assert.deepEqual(openHints({ hover: null, focus: 'sql', closed: result.closed }), []);
  // A second Esc is not consumed, so it can still close the screen behind (the hint is already gone).
  assert.equal(escapeHints({ hover: null, focus: 'sql', closed: result.closed }).handled, false);
  // A hint shown by hover on another lane is closed too.
  result = escapeHints({ hover: 'iis', focus: 'sql', closed: [] });
  assert.deepEqual([...result.closed].sort(), ['iis', 'sql']);
  // Leaving the lane (pointer and focus both gone) lets the hint come back next time.
  assert.deepEqual(forgetHint({ hover: null, focus: null, closed: ['sql', 'iis'] }, 'sql'), ['iis']);
  assert.deepEqual(forgetHint({ hover: null, focus: 'sql', closed: ['sql'] }, 'sql'), ['sql'], 'still focused, stays closed');
});

test('UI-11: the hint stays hoverable and persists until closed (WCAG 1.4.13)', () => {
  assert.match(source, /\.lane-label:hover \.hint, \.lane-label:focus-within \.hint \{ display: block; \}/);
  assert.match(source, /\.lane-label\[data-hint-closed\] \.hint \{ display: none; \}/);
  assert.match(source, /onkeydowncapture=\{onWindowKeyCapture\}/);
  const capture = source.slice(source.indexOf('function onWindowKeyCapture'));
  const body = capture.slice(0, capture.indexOf('\n  }'));
  assert.match(body, /stopPropagation\(\)/);
  assert.match(body, /preventDefault\(\)/);
});

test('UI-11: the bucket selector is a slider with a value, not a plain group', () => {
  const page = html();
  const tag = /<div[^>]*class="overlay[^"]*"[^>]*>/.exec(decode(page));
  assert.ok(tag, 'the overlay renders');
  assert.match(tag[0], /role="slider"/);
  assert.match(tag[0], /aria-roledescription="bucket selector"/);
  assert.match(tag[0], /tabindex="0"/);
  assert.match(tag[0], /aria-orientation="horizontal"/);
  assert.match(tag[0], /aria-valuemin="0"/);
  assert.match(tag[0], /aria-valuemax="9"/);
  assert.match(tag[0], /aria-valuenow="0"/);
  const valuetext = /aria-valuetext="([^"]*)"/.exec(tag[0]);
  assert.ok(valuetext, 'a slider says its value in words');
  assert.match(valuetext[1], /No bucket chosen yet/);
  assert.doesNotMatch(tag[0], /role="group"/);
});

test('UI-11: the arrow-key behaviour is unchanged', () => {
  for (const needle of ["event.key === 'ArrowLeft'", "event.key === 'ArrowRight'", "event.key === 'Home'", "event.key === 'End'", "event.key === 'Enter'"]) {
    assert.ok(source.includes(needle), needle);
  }
});
