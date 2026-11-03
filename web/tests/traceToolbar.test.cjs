const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

// The trace toolbar (WP-10): lane visibility state, the scrub pill sentences, and the component wiring.

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
const read = (relative) => fs.readFileSync(path.join(root, relative), 'utf8');

const toolbar = loadTypeScript('src/lib/tracing/toolbar.ts');
const { lintScreenStrings } = loadTypeScript('src/lib/copy/copylint.ts');
const { deck } = loadTypeScript('src/lib/copy/deck.ts');

test('lanes start on and toggle one at a time', () => {
  const all = toolbar.allLanesVisible();
  assert.deepEqual(all, { audit: true, sql: true, iis: true });
  const off = toolbar.toggleLane(all, 'sql');
  assert.deepEqual(off, { audit: true, sql: false, iis: true });
  assert.notEqual(off, all);
  assert.deepEqual(all, { audit: true, sql: true, iis: true });
  assert.deepEqual(toolbar.toggleLane(off, 'sql'), { audit: true, sql: true, iis: true });
  assert.deepEqual(toolbar.visibleKeys({ audit: true, sql: false, iis: true }), ['audit', 'iis']);
  assert.deepEqual(toolbar.visibleKeys(all), ['audit', 'sql', 'iis']);
  assert.deepEqual(toolbar.visibleKeys(toolbar.toggleLane(toolbar.toggleLane(all, 'audit'), 'iis')), ['sql']);
});

test('scrub pill states the real setting', () => {
  assert.equal(toolbar.scrubPillText(null), 'Default: export hides IP, referer and cookie hash');
  assert.equal(toolbar.scrubPillText(undefined), 'Default: export hides IP, referer and cookie hash');
  assert.equal(toolbar.scrubPillText(true), 'Export hides IP, referer and cookie hash');
  assert.equal(toolbar.scrubPillText(false), 'Export includes IP, referer and cookie hash (scrub is off)');
  for (const value of [null, true, false]) assert.ok(!toolbar.scrubPillText(value).includes('masked'));
});

test('toolbar component has real buttons and live announcements', () => {
  const source = read('src/components/TraceToolbar.svelte');
  assert.match(source, /role="toolbar"/);
  assert.match(source, /aria-pressed/);
  assert.match(source, /aria-expanded/);
  assert.match(source, /aria-live="polite"/);
  assert.match(source, /scrubPillText\(/);
  // Two button templates: one per lane (looped over the lane order) and the pill.
  assert.equal((source.match(/<button/g) || []).length, 2);
  assert.ok(!/callTrace/.test(source));
});

test('swatches read the shared lane variables', () => {
  const source = read('src/components/TraceToolbar.svelte');
  assert.ok(source.includes('var(--lane-audit'));
  assert.ok(source.includes('var(--lane-sql'));
  assert.ok(source.includes('var(--lane-iis'));
  assert.ok(!source.includes('--lane-audit:'));
  assert.ok(!source.includes('--lane-sql:'));
  assert.ok(!source.includes('--lane-iis:'));
  assert.match(source, /import type \{ TracingLaneKey \} from '\.\.\/lib\/tracing\/model'/);
});

test('lane toggles come from the single lane order and stay 32 px high on a phone', () => {
  const source = read('src/components/TraceToolbar.svelte');
  assert.match(source, /\{#each LANE_ORDER as key/);
  assert.ok(!/ as TracingLaneKey\)/.test(source));
  assert.deepEqual(toolbar.LANE_ORDER, ['audit', 'sql', 'iis']);
  assert.equal(toolbar.LANE_ORDER, loadTypeScript('src/lib/tracing/lanes.ts').LANE_ORDER);
  for (const key of toolbar.LANE_ORDER) assert.equal(typeof toolbar.TOOLBAR_TEXT[key], 'string');
  const phone = source.replace(/\r\n/g, '\n').match(/@media \(max-width: 480px\) \{([\s\S]*?)\n \}/);
  assert.ok(phone, 'phone rule present');
  assert.match(phone[1], /\.tt-lane/);
  assert.match(phone[1], /\.pill/);
  assert.match(phone[1], /min-height: var\(--space-8\)/);
  // Spacing and radius come from tokens, not literal px (the swatch dot size and the visually hidden rule are the fixed sizes).
  const css = source.slice(source.indexOf('<style>'), source.indexOf('.sr-only {'));
  assert.ok(!/\b(?:padding|gap|margin|border-radius|min-height)\s*:[^;]*\d+px/.test(css));
});

test('export scrub setting belongs to the case that previewed it', () => {
  const { exportScrub } = loadTypeScript('src/lib/exportScrub.svelte.ts');
  assert.equal(exportScrub.forCase('a'), null);
  exportScrub.set(false, 'a');
  assert.equal(exportScrub.value, false);
  assert.equal(exportScrub.forCase('a'), false);
  assert.equal(exportScrub.forCase('b'), null);
  // Opening the same case again keeps it; opening another case drops it.
  exportScrub.keepOnlyFor('a');
  assert.equal(exportScrub.value, false);
  exportScrub.keepOnlyFor('b');
  assert.equal(exportScrub.value, null);
  assert.equal(exportScrub.forCase('a'), null);
  // A value stored with no case is left alone (nothing to compare it with).
  exportScrub.set(true);
  exportScrub.keepOnlyFor('c');
  assert.equal(exportScrub.value, true);
  exportScrub.set(null);
});

test('toolbar copy passes the screen-string lint', () => {
  assert.deepEqual(lintScreenStrings(toolbar.toolbarCopyEntries(), deck.terms), []);
});

test('toolbar is not mounted by the per-call tray', () => {
  const tray = read('src/components/CallTrace.svelte');
  assert.ok(!tray.includes('TraceToolbar'));
  assert.ok(!tray.includes('tracing'));
});
