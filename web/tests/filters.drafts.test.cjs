const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

// Removing a filter chip must keep the draft text of every other field, flag a bad chart interval and never crash the counts.
// The groups run in Svelte, so this simulates what each group's sync effect does with the same pure helpers, and checks the source wiring.

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
const read = (file) => fs.readFileSync(path.join(root, file), 'utf8');

const depth = loadTypeScript('src/lib/depth.ts');
const inv = loadTypeScript('src/lib/investigation.ts');
const rail = loadTypeScript('src/lib/filterRail.ts');
const time = loadTypeScript('src/lib/time.ts');
const baseQuery = () => time.defaultQuery(new Date('2026-10-22T12:00:00Z'), ['source-1']);

// ---------------------------------------------------------------------------------------------------------------------
// 2. Removing a chip keeps drafts in the other groups
// ---------------------------------------------------------------------------------------------------------------------
test('removeChip no longer remounts the controls; clearChips still does as a true reset', () => {
  const state = read('src/lib/appState.svelte.ts');
  const remove = state.match(/export function removeChip\([^)]*\) \{[^\n]*\}/)[0];
  const clear = state.match(/export function clearChips\(\) \{[^\n]*\}/)[0];
  assert.doesNotMatch(remove, /controlsResetKey/);
  assert.match(remove, /applyChipPatch\(query, chip\)/);
  assert.match(remove, /onScopeChange\(\)/);
  assert.match(clear, /controlsResetKey\+\+/);
});

// What the group components do with the query, in the same order, using the same helpers.
const LIST_KEYS = ['userIds', 'uiVersions', 'userIps', 'statusCodes'];
const userIdOk = (item) => /^\d{1,10}$/.test(item) && inv.isValidUserId(Number(item));
function mountDrafts(query) {
  const join = (items) => (items ?? []).join(', ');
  return {
    lists: Object.fromEntries(LIST_KEYS.map(key => [key, join(query[key])])),
    operation: query.operation ?? '',
    ranges: Object.fromEntries(inv.RANGE_FIELDS.map(field => [field.key, { min: query[field.key]?.min != null ? String(query[field.key].min) : '', max: query[field.key]?.max != null ? String(query[field.key].max) : '' }]))
  };
}
function syncDrafts(drafts, query) {
  for (const key of LIST_KEYS) drafts.lists[key] = inv.syncListDraft(drafts.lists[key], query[key], key === 'userIds' ? userIdOk : undefined, key === 'userIds' ? inv.MAX_USER_IDS : inv.MAX_FILTER_VALUES);
  drafts.operation = inv.syncOperationDraft(drafts.operation, query.operation);
  for (const field of inv.RANGE_FIELDS) {
    const applied = query[field.key];
    drafts.ranges[field.key] = inv.syncRangeDraft(drafts.ranges[field.key], applied && (applied.min != null || applied.max != null) ? applied : undefined, field.limit);
  }
}

const FULL = () => Object.assign(baseQuery(), {
  kind: 'http_unknown', outcome: 'failure', metric: 'api', controller: 'Orders', action: 'Get', operation: 'Orders.Get', completion: 'complete',
  userIds: [7, 9], uiVersions: ['2.1'], userIps: ['10.0.0.1'], statusCodes: ['500'], userAgentContains: 'Firefox', urlContains: '/api', auditId: 'a'.repeat(24),
  observedMs: { min: 5 }, apiTimeMs: { max: 9 }, auditCreateTimeMs: { min: 1, max: 2 }, requestKb: { min: 3 }, responseKb: { max: 4 }, incompleteAgeMs: { min: 6 }
});

test('removing any chip clears that chip\'s own field and keeps an invalid draft in another group', () => {
  const chips = depth.filterChips(FULL());
  assert.equal(chips.length, 20, 'every chip type is covered');
  for (const chip of chips) {
    const query = FULL();
    // The user has half-typed junk in boxes that apply nothing (so they have no chip): a bad user id and a bad range bound.
    const drafts = mountDrafts(query);
    drafts.lists.userIds = '7, 9, abc';
    drafts.ranges.requestKb = { min: '3', max: 'oops' };
    syncDrafts(drafts, query);
    assert.equal(drafts.lists.userIds, '7, 9, abc', 'settled before the removal');
    depth.applyChipPatch(query, chip);
    syncDrafts(drafts, query);
    assert.ok(!depth.filterChips(query).some(other => other.id === chip.id), chip.id + ' chip is gone');
    // The removed chip's own box is empty.
    if (LIST_KEYS.includes(chip.id)) assert.equal(drafts.lists[chip.id], '', chip.id + ' box cleared');
    if (chip.id === 'operation') assert.equal(drafts.operation, '');
    if (inv.RANGE_FIELDS.some(field => field.key === chip.id)) assert.deepEqual(drafts.ranges[chip.id], { min: '', max: '' }, chip.id + ' boxes cleared');
    // The draft in the other groups survives, unless it is the one removed.
    if (chip.id !== 'userIds') assert.equal(drafts.lists.userIds, '7, 9, abc', 'user id draft kept after removing ' + chip.id);
    if (chip.id !== 'requestKb') assert.deepEqual(drafts.ranges.requestKb, { min: '3', max: 'oops' }, 'range draft kept after removing ' + chip.id);
  }
});

test('an invalid draft with no chip behind it survives removing a chip, and the typed text with rejected tokens is dropped only with its own chip', () => {
  const query = Object.assign(baseQuery(), { outcome: 'failure' });
  const drafts = mountDrafts(query);
  drafts.lists.userIds = 'abc';
  syncDrafts(drafts, query);
  assert.equal(drafts.lists.userIds, 'abc');
  depth.applyChipPatch(query, depth.filterChips(query)[0]);
  syncDrafts(drafts, query);
  assert.equal(query.outcome, 'all');
  assert.equal(drafts.lists.userIds, 'abc');
  // Removing the user chip while a rejected token sits beside a good id rewrites the box to the (now empty) applied list.
  Object.assign(query, { userIds: [5] });
  drafts.lists.userIds = '5, abc';
  syncDrafts(drafts, query);
  assert.equal(drafts.lists.userIds, '5, abc');
  depth.applyChipPatch(query, depth.filterChips(query).find(chip => chip.id === 'userIds'));
  syncDrafts(drafts, query);
  assert.equal(drafts.lists.userIds, '');
});

test('every group resyncs from the query instead of relying on a remount', () => {
  const details = read('src/components/filters/DetailsGroup.svelte');
  assert.match(details, /syncListDraft\(userIdsText, applied\.userIds/);
  for (const name of ['uiVersionsText', 'userIpsText', 'statusCodesText']) assert.match(details, new RegExp('syncListDraft\\(' + name));
  // The three text boxes without a draft read the query one way, so they follow it.
  for (const [id, key] of [['rf-agent', 'userAgentContains'], ['rf-url', 'urlContains'], ['rf-audit-id', 'auditId']]) {
    assert.match(details, new RegExp('id="' + id + '"[^>]*value=\\{query\\.' + key + ' \\?\\? \'\'\\}'), id);
  }
  const what = read('src/components/filters/WhatGroup.svelte');
  assert.match(what, /syncOperationDraft\(operationText, applied\)/);
  assert.match(what, /id="rf-completion"[^>]*value=\{query\.completion \?\? ''\}/);
  assert.match(what, /id="outcome" bind:value=\{query\.outcome\}/);
  assert.match(what, /id="controller" bind:value=\{query\.controller\}/);
  assert.match(what, /id="action" bind:value=\{query\.action\}/);
  assert.match(what, /id="kind-unknown" checked=\{query\.kind === 'http_unknown'\}/);
  assert.match(read('src/components/filters/RangesGroup.svelte'), /syncRangeDraft\(rangeText\[key\]/);
  assert.match(read('src/components/filters/ChartGroup.svelte'), /id="metric"[^>]*bind:value=\{query\.metric\}/);
  // The bar reads the query too, and no group or the bar keys on controlsResetKey themselves.
  assert.doesNotMatch(read('src/components/QueryControls.svelte') + read('src/components/filters/WhenGroup.svelte'), /controlsResetKey/);
});

// ---------------------------------------------------------------------------------------------------------------------
// 3. A bad chart interval is flagged
// ---------------------------------------------------------------------------------------------------------------------
test('chartIntervalProblem is true exactly when the interval would stop the run', () => {
  const query = baseQuery();
  const make = (bucketSeconds) => ({ ...query, bucketSeconds });
  for (const bad of [null, undefined, NaN, 0, 9, 10.5, -30, 86_401, '60', Infinity]) assert.equal(rail.chartIntervalProblem(make(bad)), true, String(bad));
  assert.equal(rail.chartIntervalProblem(make(query.bucketSeconds)), false, 'the default interval is fine');
  assert.equal(rail.chartIntervalProblem(make(86_400)), false);
  // A whole number that makes more than 120 points is a problem too: a 1-day window at 10 s is 8,640 points.
  const day = 86_400_000, end = Date.parse(query.end);
  const dayLong = { ...query, start: new Date(end - day).toISOString(), baselineEnd: new Date(end - day).toISOString(), baselineStart: new Date(end - 2 * day).toISOString(), bucketSeconds: 10 };
  assert.equal(rail.chartIntervalProblem(dayLong), true);
  assert.equal(rail.chartIntervalProblem({ ...dayLong, bucketSeconds: 900 }), false);
  const autorun = loadTypeScript('src/lib/autorun.ts');
  // Whatever scopeProblem rejects for the interval, the group flags (the windows are valid and the source is chosen).
  for (const value of [null, 9, 10.5, 86_401, 10, 60, 600, 3600, 86_400]) {
    const q = make(value);
    const blocked = autorun.scopeProblem(q, true) !== null;
    assert.equal(rail.chartIntervalProblem(q), blocked, 'interval ' + value);
  }
});

test('ChartGroup exposes a bindable hasError that QueryControls feeds to the Chart rail entry', () => {
  const chart = read('src/components/filters/ChartGroup.svelte');
  assert.match(chart, /hasError = \$bindable\(false\)/);
  assert.match(chart, /\$effect\(\(\) => \{ hasError = chartIntervalProblem\(query\); \}\)/);
  const controls = read('src/components/QueryControls.svelte');
  assert.match(controls, /<ChartGroup [^>]*bind:hasError=\{errors\.chart\}/);
});

// ---------------------------------------------------------------------------------------------------------------------
// 11. Unknown chip ids no longer crash the counts
// ---------------------------------------------------------------------------------------------------------------------
test('an unknown chip id is counted under details and logged once, instead of throwing', () => {
  const calls = [];
  const original = console.error;
  console.error = (...args) => calls.push(args.join(' '));
  try {
    const chip = (id) => ({ id, group: 'record', label: '', value: '', patch: {} });
    const counts = depth.panelGroupCounts([chip('kind'), chip('mystery-id-1'), chip('mystery-id-1'), chip('constructor')]);
    assert.deepEqual(counts, { what: 1, details: 3, ranges: 0, chart: 0 });
    assert.equal(calls.filter(text => text.includes('mystery-id-1')).length, 1, 'logged once per id');
    assert.equal(calls.filter(text => text.includes('constructor')).length, 1);
    depth.panelGroupCounts([chip('mystery-id-1')]);
    assert.equal(calls.filter(text => text.includes('mystery-id-1')).length, 1, 'still once on the next call');
    assert.equal(Object.values(counts).reduce((a, b) => a + b, 0), 4, 'the total still equals the number of chips');
  } finally { console.error = original; }
});

test('every chip filterChips can produce is mapped to a group (no fallback, no log)', () => {
  const calls = [];
  const original = console.error;
  console.error = (...args) => calls.push(args);
  try {
    const chips = depth.filterChips(FULL());
    assert.equal(chips.length, 20);
    const counts = depth.panelGroupCounts(chips);
    assert.deepEqual(counts, { what: 6, details: 7, ranges: 6, chart: 1 });
    assert.equal(calls.length, 0);
  } finally { console.error = original; }
});

// ---------------------------------------------------------------------------------------------------------------------
// (a) Plain decimals in the range boxes
// ---------------------------------------------------------------------------------------------------------------------
test('formatNumber writes plain decimals, never an exponent, and round-trips', () => {
  const cases = [[0, '0'], [5, '5'], [1.5, '1.5'], [100, '100'], [1e-7, '0.0000001'], [1.5e-7, '0.00000015'], [2.5e-10, '0.00000000025'], [1_000_000_000, '1000000000'], [1e21, '1000000000000000000000'], [1.25e21, '1250000000000000000000'], [123456.789, '123456.789']];
  for (const [value, text] of cases) assert.equal(inv.formatNumber(value), text, String(value));
  for (const value of [5e-324, 1e-7, 4.9e-8, 0.1, 0.000001, 1e-6, 123e-9, 1.7976931348623157e308, 1e9]) {
    const text = inv.formatNumber(value);
    assert.doesNotMatch(text, /e/i, String(value));
    assert.equal(Number(text), value, 'round-trips ' + value);
    assert.doesNotMatch(text, /\.\d*0$/, 'no trailing zero in ' + text);
  }
});

test('a saved tiny bound round-trips through the range boxes', () => {
  const applied = inv.serializeRecordFilters({ observedMs: { min: 1e-7 } }).observedMs;
  const draft = inv.syncRangeDraft({ min: '', max: '' }, applied, inv.MAX_RANGE_MS);
  assert.deepEqual(draft, { min: '0.0000001', max: '' });
  assert.deepEqual(inv.buildRange(draft.min, draft.max, inv.MAX_RANGE_MS).range, { min: 1e-7 });
  assert.deepEqual(inv.syncRangeDraft(draft, applied, inv.MAX_RANGE_MS), draft, 'settled');
  const query = Object.assign(baseQuery(), { observedMs: { min: 1e-7 } });
  assert.equal(depth.filterChips(query).find(chip => chip.id === 'observedMs').value, 'at least 0.0000001 ms');
  assert.match(read('src/components/filters/RangesGroup.svelte'), /formatNumber\(range\.min\)/);
});
