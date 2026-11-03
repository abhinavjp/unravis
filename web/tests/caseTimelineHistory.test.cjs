const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const ts = require('typescript');

// Opening the Investigation Timeline from the case page: a history step, remembered for a reload, and the export scrub setting read per case.

const root = path.resolve(__dirname, '..');
const read = (relative) => fs.readFileSync(path.join(root, relative), 'utf8');
function load(relativePath) {
  const output = ts.transpileModule(read(relativePath), {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022, esModuleInterop: true }
  }).outputText;
  const module = { exports: {} };
  new Function('require', 'module', 'exports', output)(require, module, module.exports);
  return module.exports;
}

const flow = load('src/lib/screenFlow.ts');
const facts = (over = {}) => ({ demo: false, deepLink: false, resume: false, lock: 'unlocked', ...over });

test('a reload on a case page goes back to the case page, also in the demo and over a remembered investigation', () => {
  assert.equal(flow.initialScreen(facts({ caseView: true })), 'case');
  assert.equal(flow.initialScreen(facts({ caseView: true, resume: true })), 'case');
  assert.equal(flow.initialScreen(facts({ caseView: true, demo: true })), 'case');
  assert.equal(flow.initialScreen(facts({ caseView: true, lock: 'unknown' })), 'case');
});

test('the other first-screen rules are unchanged by the case page', () => {
  assert.equal(flow.initialScreen(facts()), 'start');
  assert.equal(flow.initialScreen(facts({ resume: true })), 'running');
  assert.equal(flow.initialScreen(facts({ demo: true })), 'running');
  assert.equal(flow.initialScreen(facts({ caseView: true, deepLink: true })), 'running', 'a link that carries a view wins');
  assert.equal(flow.initialScreen(facts({ caseView: true, lock: 'locked' })), 'unlock', 'a locked key comes first');
  assert.equal(flow.initialScreen(facts({ caseView: true, lock: 'damaged', demo: true })), 'running');
});

test('history marks name the case page with or without its timeline', () => {
  assert.deepEqual(flow.parseScreenMark('case'), { screen: 'case', tracing: false });
  assert.deepEqual(flow.parseScreenMark(flow.CASE_TRACING_MARK), { screen: 'case', tracing: true });
  assert.deepEqual(flow.parseScreenMark(''), { screen: '', tracing: false });
  assert.equal(flow.isHistoryScreen(flow.CASE_TRACING_MARK), false, 'the mark is read through parseScreenMark');
});

test('the app holds the timeline state, remembers it in the tab and wires the history', () => {
  const state = read('src/lib/appState.svelte.ts');
  assert.match(state, /const CASE_VIEW_KEY = 'unravis\.caseView'/);
  assert.match(state, /export function openTracing\(/);
  assert.match(state, /export function closeTracing\(/);
  assert.match(state, /marker\.markScreen\(CASE_TRACING_MARK\)/);
  assert.match(state, /marker\.stepBack\('case'\)/);
  // The remembered word sits in a try/catch like the case id; it is never the case id itself.
  const fn = state.slice(state.indexOf('function storedCaseView'), state.indexOf('let tracingCase'));
  assert.match(fn, /try \{[\s\S]*\} catch/);
  const home = read('src/lib/components/CaseHome.svelte');
  assert.match(home, /app\.tracingCase === caseId/);
  assert.match(home, /onclick=\{openTracing\}/);
  assert.doesNotMatch(home, /tracingFor/);
});

test('the demo no longer forgets the case on a reload', () => {
  const state = read('src/lib/appState.svelte.ts');
  assert.doesNotMatch(state, /info\.demo\) setActiveCase\(''\)/);
});

test('the timeline reads the export scrub setting for its own case only', () => {
  assert.ok(read('src/components/TracingPaper.svelte').includes('exportScrub.forCase(caseId)'));
  assert.ok(!read('src/components/TracingPaper.svelte').includes('exportScrub.value'));
});

test('exportScrub.forCase hides a setting that another case produced', () => {
  const source = read('src/lib/exportScrub.svelte.ts').replace(/\$state<boolean \| null>\(null\)/, 'null');
  const output = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText;
  const module = { exports: {} };
  new Function('require', 'module', 'exports', output)(require, module, module.exports);
  const { exportScrub } = module.exports;
  assert.equal(exportScrub.forCase('A'), null);
  exportScrub.set(true, 'A');
  assert.equal(exportScrub.forCase('A'), true);
  assert.equal(exportScrub.forCase('B'), null, 'case B must not see the setting from case A');
  exportScrub.keepOnlyFor('B');
  assert.equal(exportScrub.forCase('A'), null);
});
