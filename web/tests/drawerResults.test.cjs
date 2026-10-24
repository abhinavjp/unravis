const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

const root = path.resolve(__dirname, '..');
function loadTypeScript(relativePath) {
  const filename = path.join(root, relativePath);
  const source = fs.readFileSync(filename, 'utf8');
  const output = ts.transpileModule(source, {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022, esModuleInterop: true }
  }).outputText;
  const module = { exports: {} };
  const nodeRequire = createRequire(filename);
  const load = (request) => /^\.\.?\//.test(request) && fs.existsSync(path.join(path.dirname(filename), request + '.ts'))
    ? loadTypeScript(path.relative(root, path.join(path.dirname(filename), request + '.ts')))
    : nodeRequire(request);
  new Function('require', 'module', 'exports', output)(load, module, module.exports);
  return module.exports;
}

const read = (relative) => fs.readFileSync(path.join(root, relative), 'utf8').replace(/\r\n/g, '\n');
const results = loadTypeScript('src/lib/drawerResults.svelte.ts');
const { deck } = loadTypeScript('src/lib/copy/deck.ts');
const { lintCopy } = loadTypeScript('src/lib/copy/copylint.ts');

const M = 60_000;
const T0 = Date.parse('2026-10-24T14:00:00Z');
const known = ['a', 'b', 'c'];
const patternsEntry = (n, extra = {}) => ({
  key: 'a|s|e|45|600|' + n, windowKey: results.patternsWindowKey(['a'], 's' + n, 'e'), sourceIds: ['a'],
  stallSec: '45', gapSec: '600', idleAsked: false, report: { n }, savedAt: T0, ...extra
});
const coverageEntry = (n, sourceId = 'a') => ({ key: results.coverageKey(sourceId, 's' + n, 'e'), sourceId, report: { n }, savedAt: T0 });
test.beforeEach(() => results.clearDrawerResults());

test('a saved patterns report comes back for the same window, with the limits it was run with', () => {
  results.savePatterns(patternsEntry(1, { stallSec: '30', gapSec: '300', idleAsked: true }));
  const found = results.readPatterns(results.patternsWindowKey(['a'], 's1', 'e'), known);
  assert.deepEqual(found.report, { n: 1 });
  assert.equal(found.stallSec, '30');
  assert.equal(found.gapSec, '300');
  assert.equal(found.idleAsked, true);
  assert.equal(found.savedAt, T0);
  assert.equal(results.readPatterns(results.patternsWindowKey(['a'], 'other', 'e'), known), null);
});

test('saving the same ask again replaces the entry; Run again is the only way to refresh it', () => {
  results.savePatterns(patternsEntry(1, { report: { run: 1 } }));
  results.savePatterns(patternsEntry(1, { report: { run: 2 }, savedAt: T0 + 5 * M }));
  assert.equal(results.drawerResultCounts().patterns, 1);
  const found = results.readPatterns(results.patternsWindowKey(['a'], 's1', 'e'), known);
  assert.deepEqual(found.report, { run: 2 });
  assert.equal(found.savedAt, T0 + 5 * M);
});

test('of several reports for one window, the newest one is shown', () => {
  const window = results.patternsWindowKey(['a'], 's1', 'e');
  results.savePatterns(patternsEntry(1, { key: 'k1', report: { which: 'first' } }));
  results.savePatterns(patternsEntry(1, { key: 'k2', report: { which: 'second' }, stallSec: '10' }));
  assert.deepEqual(results.readPatterns(window, known).report, { which: 'second' });
});

test('patterns keeps three reports and drops the oldest', () => {
  for (let n = 1; n <= 4; n++) results.savePatterns(patternsEntry(n));
  assert.equal(results.PATTERNS_MAX, 3);
  assert.equal(results.drawerResultCounts().patterns, 3);
  assert.equal(results.readPatterns(results.patternsWindowKey(['a'], 's1', 'e'), known), null);
  for (const n of [2, 3, 4]) assert.ok(results.readPatterns(results.patternsWindowKey(['a'], 's' + n, 'e'), known), 'window ' + n);
});

test('coverage keeps eight reports, one per source and period, and drops the oldest', () => {
  for (let n = 1; n <= 9; n++) results.saveCoverage(coverageEntry(n));
  assert.equal(results.COVERAGE_MAX, 8);
  assert.equal(results.drawerResultCounts().coverage, 8);
  assert.equal(results.readCoverage(results.coverageKey('a', 's1', 'e'), known), null);
  assert.ok(results.readCoverage(results.coverageKey('a', 's9', 'e'), known));
  results.saveCoverage({ ...coverageEntry(9), report: { n: 'again' } });
  assert.equal(results.drawerResultCounts().coverage, 8);
  assert.deepEqual(results.readCoverage(results.coverageKey('a', 's9', 'e'), known).report, { n: 'again' });
  assert.equal(results.readCoverage(results.coverageKey('b', 's9', 'e'), known), null, 'another source has its own entry');
});

test('a patterns report that names a removed source is hidden and dropped for good', () => {
  results.savePatterns(patternsEntry(1, { sourceIds: ['a', 'b'], windowKey: results.patternsWindowKey(['a', 'b'], 's', 'e') }));
  results.savePatterns(patternsEntry(2));
  const window = results.patternsWindowKey(['a', 'b'], 's', 'e');
  assert.ok(results.readPatterns(window, ['a', 'b']));
  assert.equal(results.readPatterns(window, ['a']), null);
  assert.equal(results.readPatterns(window, ['a', 'b']), null, 'the entry is gone, not just hidden');
  assert.ok(results.readPatterns(results.patternsWindowKey(['a'], 's2', 'e'), ['a']), 'other reports stay');
});

test('a coverage report for a removed source is hidden and dropped for good', () => {
  results.saveCoverage(coverageEntry(1, 'b'));
  results.saveCoverage(coverageEntry(2, 'a'));
  assert.equal(results.readCoverage(results.coverageKey('b', 's1', 'e'), ['a']), null);
  assert.equal(results.readCoverage(results.coverageKey('b', 's1', 'e'), ['a', 'b']), null);
  assert.ok(results.readCoverage(results.coverageKey('a', 's2', 'e'), ['a']));
});

test('clearDrawerResults forgets both stores', () => {
  results.savePatterns(patternsEntry(1));
  results.saveCoverage(coverageEntry(1));
  results.clearDrawerResults();
  assert.deepEqual(results.drawerResultCounts(), { patterns: 0, coverage: 0 });
  assert.equal(results.readPatterns(results.patternsWindowKey(['a'], 's1', 'e'), known), null);
  assert.equal(results.readCoverage(results.coverageKey('a', 's1', 'e'), known), null);
});

test('a stored entry is not changed by the caller afterwards', () => {
  const ids = ['a'];
  results.savePatterns(patternsEntry(1, { sourceIds: ids }));
  ids.push('zzz');
  assert.deepEqual(results.readPatterns(results.patternsWindowKey(['a'], 's1', 'e'), known).sourceIds, ['a']);
});

test('ageText counts whole minutes, then hours, then days', () => {
  assert.equal(results.ageText(T0, T0), 'less than 1 min ago');
  assert.equal(results.ageText(T0, T0 + 59_999), 'less than 1 min ago');
  assert.equal(results.ageText(T0, T0 + M), '1 min ago');
  assert.equal(results.ageText(T0, T0 + 59 * M), '59 min ago');
  assert.equal(results.ageText(T0, T0 + 60 * M), '1 h ago');
  assert.equal(results.ageText(T0, T0 + 25 * 60 * M), '1 d ago');
  assert.equal(results.ageText(T0, T0 - 5 * M), 'less than 1 min ago', 'a clock that went back is not a negative age');
});

test('foundAtText shows the clock time and the age, and adds the day once it is a day old', () => {
  assert.equal(results.foundAtText(T0, T0 + 5 * M, 'UTC'), 'Found at 14:00 UTC (5 min ago)');
  assert.equal(results.foundAtText(T0, T0 + 26 * 60 * M, 'UTC'), 'Found at 24 Oct 14:00 UTC (1 d ago)');
  assert.equal(results.foundAtText(T0, T0 + M, 'Asia/Dubai'), 'Found at 18:00 GMT+4 (1 min ago)');
});

test('the drawer copy passes the copy lint and names no cause', () => {
  const texts = [
    ...Object.values(results.DRAWER_RESULTS_TEXT),
    results.foundAtText(T0, T0 + 5 * M, 'UTC') + '. ' + results.DRAWER_RESULTS_TEXT.savedNote,
    results.foundAtText(T0, T0 + 30 * 60 * M, 'UTC')
  ];
  assert.deepEqual(lintCopy(texts.map((text, i) => ({ id: 'd' + i, text })), deck.terms), []);
  for (const text of texts) assert.doesNotMatch(text, /\b(caused|because|due to|waiting on|blocked by)\b/i, text);
});

test('the module keeps results in memory only', () => {
  const source = read('src/lib/drawerResults.svelte.ts');
  assert.doesNotMatch(source, /sessionStorage|localStorage|indexedDB|document\.cookie|fetch\(/);
  assert.doesNotMatch(source, /\$state|\$derived|\$effect/, 'plain module state, so it loads outside Svelte');
});

// ---- the two panels use the memory the way the decision says ----
const patternsPanel = read('src/components/PatternsPanel.svelte');
const coveragePanel = read('src/components/CoveragePanel.svelte');

test('Patterns restores on mount, never runs by itself, and saves only finished reports', () => {
  const mount = /onMount\(\(\) => \{[\s\S]*?\n  \}\);/.exec(patternsPanel)[0];
  assert.match(mount, /readPatterns\(patternsWindowKey\(query\.sourceIds, query\.start, query\.end\), app\.sources\.map/);
  assert.match(mount, /phase = \{ kind: 'done', report: saved\.report/);
  assert.match(mount, /savedAt = saved\.savedAt/);
  assert.doesNotMatch(mount, /run\(\)|api\.patterns/, 'reopening must not call the server');
  assert.equal((patternsPanel.match(/api\.patterns\(/g) ?? []).length, 1, 'one call site, inside run()');
  assert.equal((patternsPanel.match(/savePatterns\(/g) ?? []).length, 1);
  assert.match(patternsPanel, /if \(mine\.signal\.aborted\) return;\n      savePatterns\(\{ \.\.\.saveAs, report \}\);/);
  assert.doesNotMatch(/catch \(cause\) \{[\s\S]*?\n    \}\n  \}/.exec(patternsPanel)[0], /savePatterns/, 'an error is not saved');
});

test('Patterns shows the as-of text with a Run again button that runs the search', () => {
  assert.match(patternsPanel, /\{#if savedAt !== null\}/);
  assert.match(patternsPanel, /foundAtText\(savedAt, now, zone\)/);
  assert.match(patternsPanel, /onclick=\{run\}>\{DRAWER_RESULTS_TEXT\.runAgain\}</);
  assert.match(patternsPanel, /savedAt = null;\n    phase = \{ kind: 'running' \};/, 'a fresh run clears the saved marker');
});

test('Patterns clears the memory when the session is forgotten', () => {
  assert.match(patternsPanel, /if \(!readSessionToken\(\)\) \{ clearDrawerResults\(\); return; \}/);
  assert.match(patternsPanel, /cause instanceof ApiError && cause\.status === 401\) clearDrawerResults\(\)/);
});

test('Coverage restores on mount, never runs by itself, and never saves a refusal or an error', () => {
  const mount = /onMount\(\(\) => \{[\s\S]*?\n  \}\);/.exec(coveragePanel)[0];
  assert.match(mount, /readCoverage\(coverageKey\(sourceId, range\.start, range\.end\), app\.sources\.map/);
  assert.match(mount, /phase = \{ kind: 'done', report: saved\.report \}/);
  assert.doesNotMatch(mount, /run\(\)|api\.coverage/, 'reopening must not call the server');
  assert.equal((coveragePanel.match(/api\.coverage\(/g) ?? []).length, 1);
  assert.equal((coveragePanel.match(/saveCoverage\(/g) ?? []).length, 1);
  const refusal = coveragePanel.indexOf("if ('refused' in result)");
  const save = coveragePanel.indexOf('saveCoverage(');
  assert.ok(refusal > 0 && save > refusal, 'the refusal branch returns before anything is saved');
  assert.match(coveragePanel, /if \('refused' in result\) \{ phase = \{ kind: 'refused', text: coverageRefusalText\(result\) \}; return; \}/);
  assert.doesNotMatch(/catch \(cause\) \{[\s\S]*?\n    \}\n  \}/.exec(coveragePanel)[0], /saveCoverage/);
});

test('Coverage shows the as-of text with a Run again button and clears with the session', () => {
  assert.match(coveragePanel, /foundAtText\(savedAt, now, scope\.zone\)/);
  assert.match(coveragePanel, /onclick=\{run\}>\{DRAWER_RESULTS_TEXT\.runAgain\}</);
  assert.match(coveragePanel, /savedAt = null;\n    phase = \{ kind: 'running' \};/);
  assert.match(coveragePanel, /if \(!readSessionToken\(\)\) \{ clearDrawerResults\(\); return; \}/);
  assert.match(coveragePanel, /cause instanceof ApiError && cause\.status === 401\) clearDrawerResults\(\)/);
});

test('the panels only read the sources list; appState is not edited and nothing touches storage', () => {
  for (const source of [patternsPanel, coveragePanel]) {
    assert.doesNotMatch(source, /app\.sources\s*=[^=]/);
    assert.doesNotMatch(source, /sessionStorage|localStorage|indexedDB/);
  }
});
