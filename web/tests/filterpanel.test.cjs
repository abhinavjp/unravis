const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

// The More filters panel: its pure helpers and the shared FilterGroup shell. Everything here is synthetic.

const root = path.resolve(__dirname, '..');
function loadTypeScript(relativePath) {
  const filename = path.join(root, relativePath);
  const source = fs.readFileSync(filename, 'utf8');
  const output = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022, esModuleInterop: true } }).outputText;
  const module = { exports: {} };
  const nodeRequire = createRequire(filename);
  const load = (request) => /^\.\.?\//.test(request) && fs.existsSync(path.join(path.dirname(filename), request + '.ts'))
    ? loadTypeScript(path.relative(root, path.join(path.dirname(filename), request + '.ts')))
    : nodeRequire(request);
  new Function('require', 'module', 'exports', output)(load, module, module.exports);
  return module.exports;
}
const read = (relative) => fs.readFileSync(path.join(root, relative), 'utf8');

const depth = loadTypeScript('src/lib/depth.ts');
const time = loadTypeScript('src/lib/time.ts');

test('panelGroupCounts sorts every More filters chip into exactly one group', () => {
  const query = time.defaultQuery(new Date('2026-10-22T12:00:00Z'), ['source-1']);
  assert.deepEqual(depth.panelGroupCounts(depth.filterChips(query)), { what: 0, details: 0, ranges: 0, chart: 0 });
  Object.assign(query, { kind: 'http_unknown', outcome: 'server_error', metric: 'api', controller: 'Orders', action: ' Get ',
    completion: 'pending', userIds: [7, 9], statusCodes: ['500'], urlContains: '/api/x', observedMs: { min: 100 }, requestKb: { min: 1, max: 5 } });
  const counts = depth.panelGroupCounts(depth.filterChips(query));
  assert.deepEqual(counts, { what: 5, details: 3, ranges: 2, chart: 1 });
  assert.equal(Object.values(counts).reduce((a, b) => a + b, 0), depth.moreFiltersCount(query));
  Object.assign(query, { operation: 'Orders.Get', auditId: 'a'.repeat(24), incompleteAgeMs: { min: 1000 }, observedMs: undefined });
  const more = depth.panelGroupCounts(depth.filterChips(query));
  assert.equal(more.what, 6); assert.equal(more.details, 4); assert.equal(more.ranges, 2);
  assert.throws(() => depth.panelGroupCounts([{ id: 'mystery', group: 'record', label: '', value: '', patch: {} }]));
});

test('filtersInUseText counts in words', () => {
  assert.equal(depth.filtersInUseText(0), 'No filters in use');
  assert.equal(depth.filtersInUseText(1), '1 filter in use');
  assert.equal(depth.filtersInUseText(11), '11 filters in use');
});

test('unknownKindToggle maps the checkbox to a kind', () => {
  assert.equal(depth.unknownKindToggle(true), 'http_unknown');
  assert.equal(depth.unknownKindToggle(false), 'http');
  const query = time.defaultQuery(new Date('2026-10-22T12:00:00Z'), ['source-1']);
  query.kind = depth.unknownKindToggle(true);
  assert.deepEqual(depth.filterChips(query).map(c => c.id), ['kind']);
  query.kind = depth.unknownKindToggle(false);
  assert.deepEqual(depth.filterChips(query), []);
});

test('FilterGroup is a labelled ruled section with a counted title and no box', () => {
  const src = read('src/components/filters/FilterGroup.svelte');
  assert.match(src, /<section class="filter-group"[^>]*aria-labelledby=\{'fg-' \+ id \+ '-title'\}/);
  assert.match(src, /<h2 class="fg-title" id=\{'fg-' \+ id \+ '-title'\}>/);
  assert.match(src, /<span class="visually-hidden"> in use<\/span>/);
  const css = read('src/app.css');
  const block = css.slice(css.indexOf('/* More filters panel */'));
  assert.match(block, /\.filter-group\s*\{[^}]*border-top:\s*1px solid var\(--line\)/);
  assert.doesNotMatch(block.slice(0, block.indexOf('/* ----------')), /#[0-9a-fA-F]{3,8}\b|rgba?\(|hsla?\(/);
  assert.match(css, /\.visually-hidden\s*\{[^}]*clip:/);
  assert.match(block, /\.fg-count\s*\{[^}]*tabular-nums/);
});
