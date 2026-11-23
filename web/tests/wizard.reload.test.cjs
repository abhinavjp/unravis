const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const ts = require('typescript');

// BL-495h (sweep CASES-25): a reload in the create guide goes back to the guide and offers the kept draft, instead of landing on the analysis start screen.

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

test('a reload in the create guide with a kept draft goes back to the guide, also in the demo and over a remembered investigation', () => {
  assert.equal(flow.initialScreen(facts({ createView: true })), 'create');
  assert.equal(flow.initialScreen(facts({ createView: true, demo: true })), 'create');
  assert.equal(flow.initialScreen(facts({ createView: true, resume: true })), 'create');
  assert.equal(flow.initialScreen(facts({ createView: true, lock: 'unknown' })), 'create');
  assert.equal(flow.go('loading', 'create').ok, true, 'the move table allows the first screen to be the guide');
});

test('the other first-screen rules still win where they did', () => {
  assert.equal(flow.initialScreen(facts({ createView: true, deepLink: true })), 'running', 'a link that carries a view wins');
  assert.equal(flow.initialScreen(facts({ createView: true, lock: 'locked' })), 'unlock', 'a locked key comes first');
  assert.equal(flow.initialScreen(facts({ createView: true, caseView: true })), 'case', 'a remembered case page is not taken by the guide');
  assert.equal(flow.initialScreen(facts()), 'start');
  assert.equal(flow.initialScreen(facts({ demo: true })), 'running');
  assert.equal(flow.afterUnlock('create'), 'create', 'the unlock screen hands the guide back');
});

test('the app remembers the guide only while it shows, and only goes back with a kept draft', () => {
  const state = read('src/lib/appState.svelte.ts');
  assert.match(state, /const CREATE_VIEW_KEY = 'unravis\.createView'/);
  assert.match(state, /storedCreateView\(to === 'create'\)/, 'written when the guide shows, cleared when the work moves to another working screen');
  assert.match(state, /const createView = storedCreateView\(\) && draftHasContent\(readSavedDraft\(\)\);/, 'no kept draft, no return to the guide');
  assert.match(state, /const leavesGuide = screen === 'create' && \(mark === 'start' \|\| mark === 'running'\);/, 'Back in a guide a reload brought back leaves it');
  assert.match(state, /initialScreen\(\{ demo, deepLink, resume, lock, caseView, createView \}\)/);
  assert.match(state, /unlockOrigin = initialScreen\(\{ demo, deepLink, resume, lock: 'unlocked', caseView, createView \}\)/);
});

test('a kept draft is worth coming back to only with a name or environments (an own-key switch alone is not)', () => {
  const store = load('src/lib/wizardDraftStore.ts');
  assert.equal(store.draftHasContent(null), false);
  assert.equal(store.draftHasContent({ title: '', envText: '', keyOn: true }), false);
  assert.equal(store.draftHasContent({ title: '  ', envText: ' ', keyOn: false }), false);
  assert.equal(store.draftHasContent({ title: 'A', envText: '', keyOn: false }), true);
  assert.equal(store.draftHasContent({ title: '', envText: 'prod', keyOn: false }), true);
});
