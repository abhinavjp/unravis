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

const { createDrawerHistory, DRAWER_MARK } = loadTypeScript('src/lib/drawerHistory.ts');
const { classifyStartupFailure, comparisonLabel, startupLintEntries, STARTUP_TEXT, COMPARISON_NOT_RUN, COMPARISON_NOT_RUN_FILTER } = loadTypeScript('src/lib/copy/startupCopy.ts');
const { lintCopy, fleschKincaidGrade, MAX_GRADE } = loadTypeScript('src/lib/copy/copylint.ts');
const { deck } = loadTypeScript('src/lib/copy/deck.ts');

/* A history that behaves like the browser's: back() lands later, as a popstate the test delivers. */
function fakeBrowser(url = '/app#view=where', state = { uvIdx: 0 }) {
  const entries = [{ state, url }];
  let at = 0;
  const pending = [];
  const hist = {
    get state() { return entries[at].state; },
    pushState(next, _unused, to) { entries.length = at + 1; entries.push({ state: next, url: to ?? entries[at].url }); at++; },
    replaceState(next, _unused, to) { entries[at] = { state: next, url: to ?? entries[at].url }; },
    back() { pending.push('back'); }
  };
  return {
    hist, entries,
    get at() { return at; },
    get url() { return entries[at].url; },
    /** Delivers one pending or user Back as a popstate to `onPop`. */
    popBack(onPop) { if (pending.length) pending.shift(); if (at > 0) at--; return onPop(); },
    forward(onPop) { if (at < entries.length - 1) at++; return onPop(); },
    get pendingBacks() { return pending.length; }
  };
}
const marked = (state) => state && state[DRAWER_MARK] === true;

test('opening a drawer adds one entry at the same address and closing from the page steps back over it', () => {
  const b = fakeBrowser();
  const closed = [];
  const d = createDrawerHistory(b.hist, (id) => closed.push(id));
  assert.equal(b.entries.length, 1, 'nothing is added on load');
  d.sync(['side']);
  assert.equal(b.entries.length, 2);
  assert.equal(b.url, '/app#view=where');
  assert.equal(marked(b.hist.state), true);
  assert.equal(b.hist.state.uvIdx, 0, 'the view history index is kept');
  d.sync([]);
  assert.equal(b.pendingBacks, 1);
  assert.equal(b.popBack(() => d.pop()), true, 'its own Back is consumed');
  assert.deepEqual(closed, [], 'no double close');
  assert.equal(b.at, 0);
  assert.equal(marked(b.hist.state), false);
});

test('the browser Back closes the drawer and leaves the page where it was', () => {
  const b = fakeBrowser();
  const closed = [];
  const d = createDrawerHistory(b.hist, (id) => closed.push(id));
  d.sync(['connections']);
  assert.equal(b.popBack(() => d.pop()), true);
  assert.deepEqual(closed, ['connections']);
  assert.equal(b.at, 0);
  d.sync([]); // the page now reports it closed: nothing more to do
  assert.equal(b.pendingBacks, 0);
  assert.equal(b.entries.length, 2);
});

test('opening a second drawer over the first reuses the entry', () => {
  const b = fakeBrowser();
  const d = createDrawerHistory(b.hist, () => {});
  d.sync(['side']);
  d.sync(['side', 'glossary']);
  assert.equal(b.entries.length, 2);
  d.sync(['glossary']); // the first one closed from the page: the entry stays for the other
  assert.equal(b.pendingBacks, 0);
  assert.equal(b.entries.length, 2);
  d.sync(['glossary', 'side']);
  assert.equal(b.entries.length, 2);
});

test('Back closes the drawer opened last, then the next one', () => {
  const b = fakeBrowser();
  const closed = [];
  const d = createDrawerHistory(b.hist, (id) => closed.push(id));
  d.sync(['side']);
  d.sync(['side', 'glossary']);
  assert.equal(b.popBack(() => d.pop()), true);
  assert.deepEqual(closed, ['glossary']);
  assert.equal(marked(b.hist.state), true, 'the entry is back for the drawer still open');
  d.sync(['side']);
  assert.equal(b.pendingBacks, 0);
  assert.equal(b.popBack(() => d.pop()), true);
  assert.deepEqual(closed, ['glossary', 'side']);
  assert.equal(marked(b.hist.state), false);
  d.sync([]);
  assert.equal(b.popBack(() => d.pop()), false, 'with no drawer open Back belongs to the page');
});

test('a reload on a drawer entry clears the marker and opens nothing', () => {
  const b = fakeBrowser('/app#t=x', { uvIdx: 2, [DRAWER_MARK]: true });
  const closed = [];
  const d = createDrawerHistory(b.hist, (id) => closed.push(id));
  assert.equal(marked(b.hist.state), false);
  assert.equal(b.hist.state.uvIdx, 2);
  assert.equal(b.entries.length, 1);
  assert.deepEqual(d.openIds, []);
  assert.equal(d.pop(), false);
  assert.deepEqual(closed, []);
});

test('Forward onto an entry no drawer owns steps over it', () => {
  const b = fakeBrowser();
  const d = createDrawerHistory(b.hist, () => {});
  d.sync(['side']);
  b.popBack(() => d.pop());
  d.sync([]);
  assert.equal(b.forward(() => d.pop()), true);
  assert.equal(b.pendingBacks, 1);
  assert.equal(b.popBack(() => d.pop()), true);
  assert.equal(b.at, 0);
});

test('a drawer opened while its own Back is still landing waits for it', () => {
  const b = fakeBrowser();
  const d = createDrawerHistory(b.hist, () => {});
  d.sync(['side']);
  d.sync([]);
  d.sync(['glossary']); // before the Back lands
  assert.equal(b.entries.length, 2, 'no push while the Back is pending');
  assert.equal(b.popBack(() => d.pop()), true);
  assert.equal(b.entries.length, 2);
  assert.equal(b.at, 1);
  assert.equal(marked(b.hist.state), true);
  assert.deepEqual(d.openIds, ['glossary']);
});

test('a view change waits for the drawer entry to clear', () => {
  const b = fakeBrowser();
  const d = createDrawerHistory(b.hist, () => {});
  const order = [];
  d.whenClear(() => order.push('now'));
  assert.deepEqual(order, ['now']);
  d.sync(['side']);
  d.sync([]);
  d.whenClear(() => order.push('after'));
  assert.deepEqual(order, ['now']);
  b.popBack(() => d.pop());
  assert.deepEqual(order, ['now', 'after']);
});

test('the address and its fragment are never rewritten', () => {
  const b = fakeBrowser('/app?x=1#t=token-123');
  const d = createDrawerHistory(b.hist, () => {});
  d.sync(['coverage']);
  d.sync(['coverage', 'side']);
  assert.equal(b.url, '/app?x=1#t=token-123');
  b.popBack(() => d.pop());
  assert.equal(b.url, '/app?x=1#t=token-123');
});

test('every drawer in the page state reaches the history through the one module', () => {
  const state = read('src/lib/appState.svelte.ts');
  assert.match(state, /createDrawerHistory\(history, closeDrawerById\)/);
  for (const id of ['connections', 'glossary', 'coverage', 'side']) assert.match(state, new RegExp(`'${id}'`));
  assert.match(state, /if \(drawerHistory\?\.pop\(\)\) return;/);
});

/* ---------------------------------------------------------------- startup failures */

const apiError = (status, code) => Object.assign(new Error('Open Unravis from the link it printed when it started, or restart it.'), { name: 'ApiError', status, code });

test('no token in this tab is its own message and offers no retry', () => {
  const result = classifyStartupFailure({ hadToken: false, cause: apiError(401, 'no_session') });
  assert.equal(result.kind, 'noSession');
  assert.equal(result.message, 'This tab has no session. Open Unravis again from the launch link the app printed, or restart the app to get a new one.');
  assert.equal(result.canRetry, false);
});

test('a token the server refuses (401 or 403) is its own message and offers no retry', () => {
  for (const status of [401, 403]) {
    const result = classifyStartupFailure({ hadToken: true, cause: apiError(status, status === 401 ? 'no_session' : undefined) });
    assert.equal(result.kind, 'sessionRefused');
    assert.equal(result.message, 'This session is no longer valid. The app may have been restarted. Open the newest launch link.');
    assert.equal(result.canRetry, false);
  }
});

test('an unreachable server keeps Retry', () => {
  for (const cause of [new TypeError('Failed to fetch'), new TypeError('NetworkError when attempting to fetch resource.'), new TypeError('Load failed'), new Error('network error')]) {
    const result = classifyStartupFailure({ hadToken: true, cause });
    assert.equal(result.kind, 'unreachable');
    assert.equal(result.message, 'Can’t reach the Unravis app. Check it is still running, then retry.');
    assert.equal(result.canRetry, true);
  }
});

test('anything else keeps the message it had and keeps Retry', () => {
  const server = Object.assign(new Error('Request failed (500)'), { name: 'ApiError', status: 500 });
  assert.deepEqual(classifyStartupFailure({ hadToken: true, cause: server }), { kind: 'other', message: 'Request failed (500)', canRetry: true });
  assert.equal(classifyStartupFailure({ hadToken: true, cause: new SyntaxError('Unexpected token <') }).kind, 'other');
  assert.equal(classifyStartupFailure({ hadToken: true, cause: 'odd' }).message, STARTUP_TEXT.fallback);
  assert.equal(classifyStartupFailure({ hadToken: true, cause: Object.assign(new Error('x'), { name: 'AbortError' }) }).kind, 'other');
  // An error that carries an HTTP status is never taken for a lost connection, even when its words look like one.
  assert.equal(classifyStartupFailure({ hadToken: true, cause: Object.assign(new Error('Failed to fetch'), { status: 502 }) }).kind, 'other');
  // A missing token with a network failure is still a missing session only when the error says so.
  assert.equal(classifyStartupFailure({ hadToken: false, cause: new TypeError('Failed to fetch') }).kind, 'unreachable');
});

test('the startup sentences pass the copy lint and read at or below grade 8.5', () => {
  const entries = startupLintEntries();
  assert.ok(entries.length >= 5);
  assert.deepEqual(lintCopy(entries, deck.terms), []);
  for (const entry of entries) assert.ok(fleschKincaidGrade(entry.text) <= MAX_GRADE, entry.id + ' ' + fleschKincaidGrade(entry.text));
  for (const entry of entries) assert.doesNotMatch(entry.text, /\b(because|caused?|due to|led to|waiting)\b/i);
});

test('the failed-comparison label names the filter problem and keeps the plain label otherwise', () => {
  const filter = 'The server could not use these filters or this time window. Check them and try again.';
  assert.equal(comparisonLabel(filter, [filter, null]), COMPARISON_NOT_RUN_FILTER);
  assert.equal(comparisonLabel('Select at least one source before running a comparison.', [filter, 'Select at least one source before running a comparison.']), COMPARISON_NOT_RUN_FILTER);
  assert.equal(comparisonLabel('Request failed (500)', [filter, null]), COMPARISON_NOT_RUN);
  assert.equal(comparisonLabel('', [null, '']), COMPARISON_NOT_RUN);
  assert.equal(COMPARISON_NOT_RUN_FILTER, 'Comparison not run: fix the filter first');
});

test('the shell shows Retry only when retrying can help', () => {
  const app = read('src/App.svelte');
  assert.match(app, /\{#if app\.initCanRetry\}<button .*>Retry<\/button>\{\/if\}/);
  assert.doesNotMatch(app, /<strong>Explorer unavailable<\/strong>/);
  assert.match(app, /comparisonLabel\(/);
});

// ---- Screen routing (TSK-15): one reducer, legal moves only ----
const { SCREENS: SCREEN_LIST, canGo, go: moveScreen, initialScreen, afterUnlock, createRoute, leavesCaseWork, WIZARD_READY } = loadTypeScript('src/lib/screenFlow.ts');

const LEGAL_MOVES = {
  loading: ['unlock', 'start', 'running'],
  unlock: ['start', 'running', 'create'],
  setup: ['start', 'unlock'],
  start: ['setup', 'create', 'running', 'unlock'],
  create: ['start', 'running', 'unlock'],
  running: ['start', 'unlock']
};

test('every legal screen move is allowed and returns the target screen', () => {
  for (const [from, targets] of Object.entries(LEGAL_MOVES)) {
    for (const to of targets) {
      assert.equal(canGo(from, to), true, from + ' to ' + to);
      assert.deepEqual(moveScreen(from, to), { ok: true, screen: to }, from + ' to ' + to);
    }
  }
});

test('every move outside the table is refused, keeps the current screen, and says why', () => {
  for (const from of SCREEN_LIST) {
    for (const to of SCREEN_LIST) {
      if (from === to || LEGAL_MOVES[from].includes(to)) continue;
      const move = moveScreen(from, to);
      assert.equal(move.ok, false, from + ' to ' + to);
      assert.equal(move.screen, from);
      assert.match(move.reason, new RegExp(from + '.*' + to));
    }
  }
  // The named illegal ones: nothing goes back to loading, and set-up cannot jump into the analysis.
  assert.equal(moveScreen('running', 'loading').ok, false);
  assert.equal(moveScreen('setup', 'running').ok, false);
  assert.equal(moveScreen('unlock', 'setup').ok, false);
});

test('staying on a screen is allowed and does nothing', () => {
  for (const screen of SCREEN_LIST) assert.deepEqual(moveScreen(screen, screen), { ok: true, screen });
});

test('the first screen: demo runs, a locked key unlocks, a link or remembered tab resumes, else start', () => {
  const facts = (over) => ({ demo: false, deepLink: false, resume: false, lock: 'unlocked', ...over });
  assert.equal(initialScreen(facts({ demo: true, lock: 'locked' })), 'running', 'the demo flow is untouched');
  assert.equal(initialScreen(facts({ lock: 'locked' })), 'unlock');
  assert.equal(initialScreen(facts({ lock: 'locked', deepLink: true })), 'unlock', 'a locked key comes before a deep link');
  assert.equal(initialScreen(facts({ deepLink: true })), 'running');
  assert.equal(initialScreen(facts({ resume: true })), 'running');
  assert.equal(initialScreen(facts({ lock: 'none' })), 'start');
  assert.equal(initialScreen(facts({ lock: 'unknown' })), 'start');
  assert.equal(initialScreen(facts()), 'start');
});

test('after unlock the person returns to the work that was interrupted, else the start screen', () => {
  assert.equal(afterUnlock('running'), 'running');
  assert.equal(afterUnlock('start'), 'start');
  assert.equal(afterUnlock('create'), 'create', 'the create guide comes back as it was (TSK-19)');
  assert.equal(afterUnlock(null), 'start');
});

test('Create a new one opens the guided flow; with it off it falls back to the import panel', () => {
  assert.equal(WIZARD_READY, true);
  assert.deepEqual(createRoute(), { screen: 'create', panel: null });
  assert.deepEqual(createRoute(false), { screen: 'running', panel: 'import' });
  assert.deepEqual(createRoute(true), { screen: 'create', panel: null });
  assert.equal(canGo('start', createRoute().screen), true);
});

test('only the working screen keeps a case drawer open', () => {
  assert.equal(leavesCaseWork('running'), false);
  for (const screen of ['unlock', 'setup', 'start', 'create']) assert.equal(leavesCaseWork(screen), true, screen);
});

test('appState routes through the reducer, logs refused moves in dev only, and the header Home returns to start without closing', () => {
  const state = read('src/lib/appState.svelte.ts');
  assert.match(state, /const move = moveScreen\(screen, to\);/);
  assert.match(state, /if \(!move\.ok\) \{ devLog\(move\.reason\); return false; \}/);
  assert.match(state, /import\.meta as unknown as \{ env\?: \{ DEV\?: boolean \} \}\)\.env\?\.DEV/);
  assert.match(state, /export function goHome\(\) \{ go\('start'\); \}/);
  assert.doesNotMatch(state.slice(state.indexOf('export function goHome')), /^[^\n]*closeCase/);
  const header = read('src/components/AppHeader.svelte');
  assert.match(header, /goHome/);
  const mount = state.slice(state.indexOf('const stopLockListener'), state.indexOf('const stopLockListener') + 200);
  assert.match(mount, /onCasesLocked\(noteLocked\)/);
});

test('the autorun waits for the working screen, so the start screen never starts a query', () => {
  assert.match(read('src/lib/appState.svelte.ts'), /autoRan \|\| screen !== 'running'/);
});

test('the close-case action returns to the start screen', () => {
  assert.match(read('src/lib/components/ImportPanel.svelte'), /<CasePanel [^>]*onClosed=\{goHome\}/);
  assert.match(read('src/lib/components/CasePanel.svelte'), /onClosed\?\.\(\)/);
});
