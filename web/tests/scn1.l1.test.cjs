const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

// Scenario 1 fixes for the app shell and case screens (lane L1). Each packet appends its own tests below.

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

test('D-A-15: set-up opens from a case page and returns there', () => {
  const flow = loadTypeScript('src/lib/screenFlow.ts');
  assert.equal(flow.canGo('case', 'setup'), true);
  assert.equal(flow.canGo('setup', 'case'), true);
  assert.equal(flow.canGo('running', 'setup'), false);
  assert.equal(flow.canGo('setup', 'running'), false);
  assert.equal(flow.canGo('unlock', 'setup'), false);
  assert.equal(flow.setupReturn('case'), 'case');
  assert.equal(flow.setupReturn('start'), 'start');
  assert.equal(flow.setupReturn(null), 'start');
  assert.equal(flow.setupReturn('running'), 'start');
});

test('D-A-15: go() sends set-up back to its origin and openKeySetup records it', () => {
  const src = read('src/lib/appState.svelte.ts');
  const go = src.slice(src.indexOf('export function go(to: Screen)'), src.indexOf('// Reads the key state once the app info is in'));
  assert.match(go, /if \(screen === 'setup' && to === 'start' && setupOrigin\) to = setupReturn\(setupOrigin\);\s*const move = moveScreen\(screen, to\);/);
  assert.match(go, /screen = move\.screen;\s*if \(move\.screen !== 'setup'\) setupOrigin = null;/);
  assert.match(src, /export function openKeySetup\(\) \{\s*const from = screen;\s*if \(go\('setup'\)\) setupOrigin = from === 'case' \? 'case' : null;\s*\}/);
  assert.match(src, /unlockOrigin = null;\s*setupOrigin = null;\s*lockError = '';/);
  assert.match(src, /go\('start'\);\s*go\('setup'\);/);
});

test('D-A-05: a case made by the create guide opens on Case home, import drawer only when there are no sources', () => {
  const state = read('src/lib/appState.svelte.ts');
  const body = state.slice(state.indexOf('export async function openCreatedCase'));
  const fn = body.slice(0, body.search(/\r?\n\}\r?\n/));
  assert.match(fn, /if \(go\('case'\) && !hasSources\) openSidePanel\('import'\);/);
  assert.doesNotMatch(fn, /go\('running'\)/, 'no longer jumps to the analysis');
  assert.match(fn, /setActiveCase\(id, \{ restore: false \}\)/);
  assert.match(fn, /autoRan = false/);
  const flow = loadTypeScript('src/lib/screenFlow.ts');
  assert.equal(flow.canGo('create', 'case'), true, 'the move the new tail asks for is legal');
  const landing = read('src/components/Landing.svelte');
  assert.match(landing, /function openCreated\(id: string\) \{ resetWizard\(\); void openCreatedCase\(id\); \}/);
  const copy = read('src/lib/copy/wizardCreateCopy.ts');
  assert.match(copy, /openCase: 'Open the case'/);
});

function fakeHistory(hash = '') {
  const entries = [{ hash, state: null }];
  let at = 0;
  const loc = { pathname: '/', search: '', get hash() { return entries[at].hash; } };
  const hashOf = (url, keep) => (typeof url === 'string' ? (url.includes('#') ? url.slice(url.indexOf('#')) : '') : keep);
  const hist = {
    get state() { return entries[at].state; },
    pushState(state, _unused, url) { entries.length = at + 1; entries.push({ hash: hashOf(url, entries[at].hash), state }); at++; },
    replaceState(state, _unused, url) { entries[at] = { hash: hashOf(url, entries[at].hash), state }; },
    back() {}
  };
  return { loc, hist, entries, get at() { return at; }, goTo(to) { at = to; } };
}
const whereView = { type: 'where', lens: 'requests' };
const opView = { type: 'op', lens: 'requests', controller: 'C', action: 'A' };

test('D-A-07: historyStep decides none, replace or push and never marks a key screen', () => {
  const flow = loadTypeScript('src/lib/screenFlow.ts');
  assert.deepEqual([...flow.HISTORY_SCREENS], ['start', 'case', 'create', 'running']);
  for (const key of ['loading', 'unlock', 'setup']) assert.equal(flow.isHistoryScreen(key), false, key);
  assert.equal(flow.historyStep('loading', 'start', ''), 'replace');
  assert.equal(flow.historyStep('unlock', 'start', ''), 'replace');
  assert.equal(flow.historyStep('setup', 'start', ''), 'replace');
  assert.equal(flow.historyStep('start', 'running', 'start'), 'push');
  assert.equal(flow.historyStep('start', 'case', 'start'), 'push');
  assert.equal(flow.historyStep('start', 'create', 'start'), 'push');
  assert.equal(flow.historyStep('case', 'start', 'case'), 'push');
  assert.equal(flow.historyStep('start', 'case', ''), 'push', 'an unmarked entry after a normal move still adds an entry');
  assert.equal(flow.historyStep('create', 'case', 'create'), 'replace', 'Create ends on Case home in place');
  assert.equal(flow.historyStep('start', 'unlock', 'start'), 'none');
  assert.equal(flow.historyStep('start', 'setup', 'start'), 'none');
  assert.equal(flow.historyStep('unlock', 'case', 'case'), 'none', 'the entry already says case');
});

test('D-A-07: markScreen marks in place or adds an entry, and Back and Forward report the entry screen', () => {
  const { createViewHistory } = loadTypeScript('src/lib/wayfinding.ts');
  const b = fakeHistory();
  const h = createViewHistory(b.loc, b.hist);
  assert.equal(h.screen, '');
  h.markScreen('start', true);
  assert.equal(b.entries.length, 1);
  assert.equal(h.screen, 'start');
  h.markScreen('start');
  assert.equal(b.entries.length, 1, 'the same screen adds nothing');
  h.markScreen('case');
  h.markScreen('running');
  assert.equal(b.entries.length, 3);
  assert.equal(b.at, 2);
  b.goTo(1);
  assert.equal(h.pop(), null);
  assert.equal(h.screen, 'case');
  b.goTo(0);
  h.pop();
  assert.equal(h.screen, 'start');
  b.goTo(2);
  h.pop();
  assert.equal(h.screen, 'running');
  h.markScreen('start');
  assert.equal(b.entries.length, 4, 'a new move from a middle entry drops the entries after it');
  b.goTo(1);
  h.pop();
  h.markScreen('create');
  assert.equal(b.entries.length, 3);
});

test('D-A-07: a reload ignores marks from the page before it', () => {
  const { createViewHistory } = loadTypeScript('src/lib/wayfinding.ts');
  const b = fakeHistory();
  const first = createViewHistory(b.loc, b.hist);
  first.markScreen('case', true);
  assert.equal(first.screen, 'case');
  const second = createViewHistory(b.loc, b.hist);
  assert.equal(second.screen, '', 'the old mark belongs to the old page life');
  second.markScreen('start', true);
  assert.equal(second.screen, 'start');
});

test('D-A-07: view changes keep the screen mark, and stepping up never goes Back across a screen', () => {
  const { createViewHistory, SCREEN_MARK } = loadTypeScript('src/lib/wayfinding.ts');
  const b = fakeHistory();
  const h = createViewHistory(b.loc, b.hist);
  h.markScreen('running', true);
  assert.equal(h.sync(whereView), 'push');
  assert.equal(b.entries[1].state[SCREEN_MARK], 'running');
  h.markScreen('case');
  assert.equal(h.sync(opView), 'push');
  assert.equal(b.entries[3].state[SCREEN_MARK], 'case');
  h.markScreen('running', true);
  assert.equal(h.sync(whereView), 'push', 'the entry before was made on the case page, so this is not a Back');
  const plain = fakeHistory();
  const hp = createViewHistory(plain.loc, plain.hist);
  assert.equal(hp.sync(whereView), 'push');
  assert.deepEqual(plain.entries[1].state, { uvIdx: 1 }, 'with no screen mark the state is exactly what it was before');
});

test('D-A-07: appState marks screens through the history, ignores key screens and refuses safely', () => {
  const state = read('src/lib/appState.svelte.ts');
  assert.match(state, /let poppingScreen = false;/);
  const go = state.slice(state.indexOf('export function go(to: Screen)'), state.indexOf('// Reads the key state once the app info is in'));
  assert.match(go, /^export function go\(to: Screen\): boolean \{\s*const from = screen;/);
  assert.match(go, /if \(!poppingScreen\) noteScreenHistory\(from, move\.screen\);/);
  assert.match(go, /function noteScreenHistory\(from: Screen, to: Screen\) \{[\s\S]*drawers\.whenClear\([\s\S]*historyStep\(from, to, marker\.screen\)[\s\S]*marker\.markScreen\(to, step === 'replace'\)/);
  assert.match(go, /function stepScreenFromHistory\(wanted: string\) \{[\s\S]*wanted === 'case' && !activeCaseId[\s\S]*marker\.markScreen\(screen, true\)/);
  assert.match(go, /try \{ if \(!go\(wanted\)\) marker\.markScreen\(screen, true\); \} finally \{ poppingScreen = false; \}/);
  assert.match(state, /if \(drawerHistory\?\.pop\(\)\) return;\s*const target = viewHistory\?\.pop\(\);\s*if \(target === undefined\) return;/);
});

test('D-A-03/04/16: start list order, Erase on a missing row, empty code field', () => {
  const view = loadTypeScript('src/lib/startScreen.ts');
  const ids = view.startView('unlocked', [{ id: 'a', title: 'A', created: '', closed: null, imports: 0, untrusted: true }, { id: 'b', title: 'B', created: '2026-10-24T15:19:37Z', closed: null, imports: 0 }]).rows.map((r) => r.id);
  assert.deepEqual(ids, ['b', 'a']);
  const start = read('src/components/StartScreen.svelte');
  assert.match(start, /\{:else if row\.missing\}[\s\S]*?askErase\(row\.id\)/);
  assert.match(start, /\{#if row\.untrusted\}<span class="case-closed" role="note">\{row\.untrustedLabel\}<\/span>\{\/if\}/, 'untrusted rows keep their Open button');
  assert.equal(loadTypeScript('src/lib/caseCode.ts').codeIsEmpty(''), true);
});

test('D-A-23: the wait line counts down from a fixed end time, then frees the button', () => {
  const copy = loadTypeScript('src/lib/copy/caseKeyCopy.ts');
  const master = loadTypeScript('src/lib/copy/masterKeyCopy.ts');
  assert.equal(copy.secondsLeft(10000, 0), 10);
  assert.equal(copy.secondsLeft(10000, 1), 10);
  assert.equal(copy.secondsLeft(10000, 1000), 9);
  assert.equal(copy.secondsLeft(10000, 9001), 1);
  assert.equal(copy.secondsLeft(10000, 10000), 0);
  assert.equal(copy.secondsLeft(10000, 12000), 0, 'never below zero');
  assert.equal(master.waitText(copy.secondsLeft(60000, 0)), 'Try again in 1 minute.');
  assert.equal(master.waitText(copy.secondsLeft(60000, 1000)), 'Try again in 59 seconds.');
  assert.equal(master.waitText(copy.secondsLeft(8000, 7000)), 'Try again in 1 second.');
  const card = read('src/components/CaseKeyCard.svelte');
  assert.match(card, /waitTimer = setInterval\(\(\) => \{\s*remaining = secondsLeft\(waitEnds, Date\.now\(\)\);\s*if \(remaining > 0\) return;\s*\/\/[^\n]*\s*stopWait\(\);\s*held = false;\s*problem = '';\s*\}, 500\);/);
  assert.match(card, /problem = CASE_KEY_TEXT\.throttled;\s*startWait\(Math\.max\(1, failure\.retryAfterSeconds \?\? 1\)\);/);
  assert.match(card, /onDestroy\(\(\) => \{ pass = ''; controller\.abort\(\); stopWait\(\); \}\);/);
  assert.match(card, /<p id="case-key-unlock-wait" class="inline-error" hidden=\{!held\}>\{waitText\(remaining\)\}<\/p>/);
  assert.match(card, /aria-describedby="case-key-unlock-help case-key-unlock-problem case-key-unlock-wait"/);
  assert.doesNotMatch(card, /role="alert"[^>]*case-key-unlock-wait|case-key-unlock-wait"[^>]*role=/, 'the countdown is not a live region');
});

test('D-A-22/23: the key card still keeps the key out of storage and keeps one main button', () => {
  const card = read('src/components/CaseKeyCard.svelte');
  assert.match(card, /const attempt = pass;\s*pass = '';/);
  assert.doesNotMatch(card, /localStorage|sessionStorage|console\.|location\.|history\./);
  assert.match(card, /disabled=\{busy \|\| held\}/);
  assert.equal((card.match(/class="primary-button"/g) ?? []).length, 1);
  const lint = loadTypeScript('src/lib/copy/caseKeyCopy.ts').caseKeyLintEntries();
  assert.ok(lint.some((entry) => entry.id === 'casekey.unlockEmpty'), 'the new sentence is in the copy lint');
});
