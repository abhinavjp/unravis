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
  const quiet = console.error; console.error = () => {};
  try { assert.equal(depth.panelGroupCounts([{ id: 'mystery', group: 'record', label: '', value: '', patch: {} }]).details, 1, 'an unknown id is counted, not thrown'); } finally { console.error = quiet; }
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
  const css = read('src/brand/tokens.css') + '\n' + read('src/app.css');
  const block = css.slice(css.indexOf('/* More filters panel */'));
  assert.match(block, /\.filter-group\s*\{[^}]*border-top:\s*1px solid var\(--line\)/);
  assert.doesNotMatch(block.slice(0, block.indexOf('/* ----------')), /#[0-9a-fA-F]{3,8}\b|rgba?\(|hsla?\(/);
  assert.match(css, /\.visually-hidden\s*\{[^}]*clip:/);
  assert.match(block, /\.fg-count\s*\{[^}]*tabular-nums/);
});

// Task 5: integration. The panel is a group rail with the count and chips at the top.
const filterRail = loadTypeScript('src/lib/filterRail.ts');
const FILTER_FILES = fs.readdirSync(path.join(root, 'src/components/filters')).filter(name => name.endsWith('.svelte')).map(name => 'src/components/filters/' + name);

test('FilterChips hands focus on after a removal so it never drops to the page', () => {
  const src = read('src/components/FilterChips.svelte');
  assert.match(src, /await tick\(\)/);
  assert.match(src, /onEmpty\?\.\(\)/);
  assert.match(src, /querySelectorAll(?:<[^>]*>)?\('\.filter-chip button'\)/);
  assert.match(src, /'Remove ' \+ chip\.label \+ ' filter: ' \+ chip\.value/);
  assert.match(src, />Clear all</);
});

test('SubBar shows the count, stale hint and chips at the top of the open panel and hides the outer chips', () => {
  const src = read('src/components/SubBar.svelte');
  assert.match(src, /<details class="more-filters" bind:open=\{moreOpen\}>/);
  assert.match(src, /bind:this=\{summaryEl\}/);
  const bodyStart = src.indexOf('<div class="more-body">');
  const body = src.slice(bodyStart, src.indexOf('{#key', bodyStart));
  assert.match(body, /filtersInUseText\(moreCount\)/);
  assert.match(body, /results\.displayedRunIsStale && !loading/);
  assert.match(body, /Changes here apply when you press Run analysis\./);
  assert.match(body, /<FilterChips /);
  assert.match(src, /\{#if !moreOpen\}<FilterChips/);
  assert.match(src, /count-badge/);
  assert.match(src, /bind:activeGroup=\{panelGroup\}/);
  assert.match(src, /firstActiveGroup\(/);
});

test('Esc still closes the panel: the state handler targets it and no filter group swallows keys', () => {
  const state = read('src/lib/appState.svelte.ts');
  assert.match(state, /details\.menu\[open\], details\.more-filters\[open\]/);
  assert.match(state, /removeChip\(chip: FilterChip\) \{ applyChipPatch\(query, chip\); onScopeChange\(\); \}/);
  for (const file of FILTER_FILES) assert.doesNotMatch(read(file), /onkeydown/, file);
  assert.equal(filterRail.railTarget('Escape', 1, 5), null);
  assert.equal(filterRail.railTarget('Tab', 1, 5), null);
});

test('QueryControls keeps its quick range guard and no longer owns the date validation', () => {
  const src = read('src/components/QueryControls.svelte');
  assert.match(src, /if \(activeRange === id && dateInputsValid\) return;/);
  assert.doesNotMatch(src, /invalidWindows/);
  assert.match(src, /quickRanges/);
  assert.doesNotMatch(src, /id="kind"/);
  assert.match(src, /kindSegments/);
  for (const file of ['src/components/QueryControls.svelte', ...FILTER_FILES]) assert.doesNotMatch(read(file), /http_unknown">HTTP \(polling type unknown\)/, file);
});

test('the stylesheet keeps the panel scroll cap and drops the old flat grid', () => {
  const css = read('src/brand/tokens.css') + '\n' + read('src/app.css');
  assert.match(css, /@media \(min-width: 721px\)[^@]*\.more-filters\[open\] > \.more-body\s*\{[^}]*max-height:[^}]*overflow-y:\s*auto/);
  assert.doesNotMatch(css, /\.query-ranges|\.advanced-filters|\.exact-times|\.record-chips/);
  assert.match(css, /\.auth-options summary/);
  assert.doesNotMatch(css, /grid-template-areas:\s*"when what"/);
  assert.match(css, /\.rail-layout\s*\{[^}]*grid-template-columns/);
  assert.match(css, /\.rail-pane\[hidden\]\s*\{\s*display:\s*none/);
  assert.match(css, /\.rail-item\.on\s*\{/);
  assert.doesNotMatch(css.slice(css.indexOf('/* More filters: group rail.')), /#[0-9a-fA-F]{3,8}\b|rgba?\(|hsla?\(/);
});

test('RAIL_GROUPS lists the five groups in order', () => {
  assert.deepEqual(filterRail.RAIL_GROUPS.map(group => group.id), ['when', 'what', 'details', 'ranges', 'chart']);
  assert.deepEqual(filterRail.RAIL_GROUPS.map(group => group.label), ['When', 'What', 'Request details', 'Ranges', 'Chart']);
});

test('firstActiveGroup opens on the first group with a filter, else When', () => {
  const none = { what: 0, details: 0, ranges: 0, chart: 0 };
  assert.equal(filterRail.firstActiveGroup(none), 'when');
  assert.equal(filterRail.firstActiveGroup({ ...none, what: 2 }), 'what');
  assert.equal(filterRail.firstActiveGroup({ ...none, details: 1, ranges: 3 }), 'details');
  assert.equal(filterRail.firstActiveGroup({ ...none, ranges: 1, chart: 1 }), 'ranges');
  assert.equal(filterRail.firstActiveGroup({ ...none, chart: 1 }), 'chart');
});

test('railTarget wraps with the arrows and jumps with Home and End', () => {
  assert.equal(filterRail.railTarget('ArrowDown', 0, 5), 1);
  assert.equal(filterRail.railTarget('ArrowRight', 4, 5), 0);
  assert.equal(filterRail.railTarget('ArrowUp', 0, 5), 4);
  assert.equal(filterRail.railTarget('ArrowLeft', 3, 5), 2);
  assert.equal(filterRail.railTarget('Home', 3, 5), 0);
  assert.equal(filterRail.railTarget('End', 1, 5), 4);
  assert.equal(filterRail.railTarget('Enter', 1, 5), null);
  assert.equal(filterRail.railTarget('ArrowDown', 0, 0), null);
});

test('QueryControls renders the rail as an ARIA tab set with every pane mounted', () => {
  const src = read('src/components/QueryControls.svelte');
  assert.match(src, /role="tablist"/);
  assert.match(src, /aria-orientation=\{vertical \? 'vertical' : 'horizontal'\}/);
  assert.match(src, /role="tab"/);
  assert.match(src, /aria-selected=\{current === group\.id\}/);
  assert.match(src, /aria-controls=\{'rail-pane-' \+ group\.id\}/);
  assert.match(src, /tabindex=\{current === group\.id \? 0 : -1\}/);
  for (const id of ['when', 'what', 'details', 'ranges', 'chart']) {
    assert.match(src, new RegExp(`role="tabpanel" id="rail-pane-${id}" aria-labelledby="rail-tab-${id}" hidden=\\{current !== '${id}'\\}`));
  }
  for (const name of ['WhenGroup', 'WhatGroup', 'DetailsGroup', 'RangesGroup', 'ChartGroup']) assert.match(src, new RegExp(`<${name} `));
  assert.match(src, /railTarget\(event\.key/);
  assert.match(src, /event\.preventDefault\(\);\s*select\(/);
  assert.doesNotMatch(src, /\{#if current === /, 'hidden panes stay mounted, so no pane is conditionally rendered');
});

test('a problem in a group shows an error dot with text and a jump list in the panel', () => {
  const src = read('src/components/QueryControls.svelte');
  assert.match(src, /class="rail-dot" aria-hidden="true"/);
  assert.match(src, /RAIL_TEXT\.problem/);
  assert.match(src, /class="panel-problems"/);
  assert.match(src, /class="problem-link"/);
  assert.match(src, /id === 'when' \? !dateInputsValid : errors\[id\]/, 'When reports through dateInputsValid');
  for (const id of ['what', 'details', 'ranges']) assert.match(src, new RegExp(`bind:hasError=\\{errors\\.${id}\\}`));
  for (const name of ['WhatGroup', 'DetailsGroup', 'RangesGroup']) {
    assert.match(read(`src/components/filters/${name}.svelte`), /hasError = \$bindable\(false\)/, name);
    assert.match(read(`src/components/filters/${name}.svelte`), /\$effect\(\(\) => \{ hasError = /, name);
  }
  assert.equal(filterRail.RAIL_TEXT.problem, 'has a problem');
});

test('the rail copy passes the copy lint', () => {
  const banned = /\b(?:cause|caused|causes|because|due to|waiting on|wait for|blocked by|led to|result in|result from|responsible for|proof|prove|proven)\b/i;
  for (const text of Object.values(filterRail.RAIL_TEXT)) assert.doesNotMatch(text, banned, text);
  for (const group of filterRail.RAIL_GROUPS) assert.doesNotMatch(group.label, banned, group.label);
});
