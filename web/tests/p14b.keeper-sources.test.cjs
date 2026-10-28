const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

// Audit sources the key keeper opened from saved links (P14b): the sticker says so, a stopped keeper says they cannot be read, a read
// the keeper refused reads the source list again, and the page never handles a password for them.
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
      if (fs.existsSync(plain)) return loadTypeScript(path.relative(root, plain));
    }
    return nodeRequire(request);
  };
  new Function('require', 'module', 'exports', output)(load, module, module.exports);
  return module.exports;
}
const read = (relative) => fs.readFileSync(path.join(root, relative), 'utf8');
const keeper = loadTypeScript('src/lib/keeper.ts');
const copy = loadTypeScript('src/lib/copy/sourcesCopy.ts');
const { ApiError } = loadTypeScript('src/lib/api.ts');
const { lintScreenStrings } = loadTypeScript('src/lib/copy/copylint.ts');

test('heldSourceText: the existing keeper sticker words, nothing for a source opened here', () => {
  assert.equal(keeper.heldSourceText({ keeper: 'held' }), copy.KEEPER_TEXT.held);
  assert.equal(keeper.heldSourceText({ keeper: 'stopped' }), copy.KEEPER_TEXT.heldDown);
  assert.equal(keeper.heldSourceText({}), '');
  assert.equal(keeper.heldSourceText({ keeper: 'other' }), '');
});

test('keeperSourceChanged: a stopped keeper or a closed source, never another refusal', () => {
  assert.equal(keeper.keeperSourceChanged(new ApiError('x', 'keeper-stopped', undefined, 503)), true);
  assert.equal(keeper.keeperSourceChanged(new ApiError('x', 'keeper-impostor', undefined, 503)), true);
  assert.equal(keeper.keeperSourceChanged(new ApiError('x', 'keeper-source-gone', undefined, 404)), true);
  assert.equal(keeper.keeperSourceChanged(new ApiError('x', 'keeper-mixed', undefined, 400)), false, 'a mixed choice changes no source');
  assert.equal(keeper.keeperSourceChanged(new ApiError('x', 'keeper-full', undefined, 503)), false);
  assert.equal(keeper.keeperSourceChanged(new ApiError('x', undefined, undefined, 503)), false, 'the demo answers a plain 503');
  assert.equal(keeper.keeperSourceChanged(null), false);
});

test('the stopped note passes the copy lint and is not a status word', () => {
  const entries = copy.sourcesCopyEntries().filter((e) => e.id === 'sources.keeper.sourcesStopped');
  assert.equal(entries.length, 1);
  assert.deepEqual(lintScreenStrings(entries, []), []);
  assert.match(copy.KEEPER_TEXT.sourcesStopped, /typed still work/);
  assert.ok(copy.KEEPER_TEXT.sourcesStopped.length < 110, 'short enough for a narrow menu');
});

test('the source menu: the sticker is information (no button), the note shows only when the keeper is stopped', () => {
  const pop = read('src/components/SourcePopover.svelte');
  assert.match(pop, /\{#if heldSourceText\(source\)\}<span class="src-sticker held" class:down=\{source\.keeper === 'stopped'\} data-keeper-source=\{source\.keeper\}>/);
  assert.match(pop, /\{#if keeperStopped\}<p class="keeper-note" role="note" data-keeper-sources-stopped>\{KEEPER_TEXT\.sourcesStopped\}<\/p>\{\/if\}/);
  assert.equal((pop.match(/btn-primary/g) || []).length, 0, 'the menu adds no main button');
  assert.doesNotMatch(pop, /type="password"/, 'no password is asked for a keeper-held source');
});

test('a read the keeper refused reads the list again; the previews ask the demo', () => {
  const state = read('src/lib/appState.svelte.ts');
  assert.match(state, /onKeeperRefusal\(\(\) => void refreshKeeperSources\(\)\);/, 'every refused read, not only an analysis');
  assert.doesNotMatch(state, /keeperSourceChanged\(cause\)/, 'one place decides');
  assert.match(state, /for \(const source of sources\) if \(!now\.has\(source\.id\)\) forgetSource\(source\);/, 'a closed source leaves the page as a removal would');
  assert.match(state, /await api\.deleteSource\(source\.id\);\s*forgetSource\(source\);/, 'removal and a closed source share one path');
  assert.equal((state.match(/api\.sources\(keeperPreview\(\)\)/g) || []).length, 1, 'the list read again');
  assert.match(state, /const asked = keeperPreview\(\);\s*const loaded = \(await api\.sources\(asked\)\) \?\? \[\];/, 'the first list');
  assert.match(state, /if \(info\.demo && keeperPreview\(\) !== asked\) void refreshKeeperSources\(\);/, 'a preview named while the first list loaded');
  assert.match(state, /if \(info\.demo && !keeperPreviewWatched\) \{ keeperPreviewWatched = true; onKeeperPreview\(\(\) => void refreshKeeperSources\(\)\); \}/, 'only the demo follows a preview named later');
  const api = read('src/lib/api.ts');
  assert.match(api, /sources: \(preview = ''\) => request<Source\[\]>\('\/sources' \+ \(preview \? '\?preview=' \+ encodeURIComponent\(preview\) : ''\)\)/);
  const types = read('src/lib/types.ts');
  assert.match(types, /keeper\?: 'held' \| 'stopped' \}/);
});

test('the fixed status words are unchanged', () => {
  assert.equal(copy.KEEPER_TEXT.held, 'Password held by the key keeper');
  assert.equal(copy.KEEPER_TEXT.heldDown, 'Password held by the key keeper, which cannot be used until it runs again');
});

test('refresh after refusal: any server read the keeper refuses calls the listeners once, other failures do not', () => {
  let calls = 0;
  const off = keeper.onKeeperRefusal(() => { calls++; });
  keeper.noticeKeeperRefusal(new ApiError('x', 'keeper-source-gone', undefined, 404)); // for example the coverage read
  keeper.noticeKeeperRefusal(new ApiError('x', 'keeper-stopped', undefined, 503)); // for example a record or a grid read
  keeper.noticeKeeperRefusal(new ApiError('x', 'keeper-audit-busy', undefined, 503)); // busy: nothing about the sources changed
  keeper.noticeKeeperRefusal(new ApiError('x', 'keeper-full', undefined, 503));
  keeper.noticeKeeperRefusal(new ApiError('x', undefined, undefined, 500));
  assert.equal(calls, 2);
  off();
  keeper.noticeKeeperRefusal(new ApiError('x', 'keeper-stopped', undefined, 503));
  assert.equal(calls, 2, 'a removed listener is not called');
});

test('refresh after refusal: the one place that reads the server tells the listeners, for every read', async () => {
  const api = read('src/lib/api.ts');
  assert.match(api, /noticeKeeperRefusal\(failure\);[^\r\n]*\r?\n\s*throw failure;/, 'every failed answer goes through the notice');
  assert.equal((api.match(/noticeKeeperRefusal\(/g) || []).length, 1, 'only in request()');
  // Run the real request(): a coverage read answered with the keeper's refusal reaches the listener.
  const calls = [];
  const off = keeper.onKeeperRefusal(() => calls.push('refreshed'));
  const stored = { getItem: () => 'tok', setItem() {}, removeItem() {} };
  global.sessionStorage = stored;
  cache.delete(path.join(root, 'src/lib/api.ts')); // a copy that finds a session token
  const realFetch = global.fetch;
  global.fetch = async () => new Response(JSON.stringify({ error: 'The key keeper closed this source.', code: 'keeper-source-gone' }), { status: 404 });
  try {
    const apiModule = loadTypeScript('src/lib/api.ts');
    await assert.rejects(apiModule.api.coverage('s1', '2026-01-01', '2026-01-02'), /closed this source/);
    assert.deepEqual(calls, ['refreshed']);
    global.fetch = async () => new Response(JSON.stringify({ error: 'Busy.', code: 'keeper-audit-busy' }), { status: 503 });
    await assert.rejects(apiModule.api.record('s1', 'r1'));
    assert.deepEqual(calls, ['refreshed'], 'a busy keeper changes no source');
  } finally {
    off();
    global.fetch = realFetch;
    delete global.sessionStorage;
  }
});

test('the stopped keeper: held sources show unticked and cannot be ticked, and are not left chosen', () => {
  assert.equal(keeper.keeperUnreadable({ keeper: 'stopped' }), true);
  assert.equal(keeper.keeperUnreadable({ keeper: 'held' }), false);
  assert.equal(keeper.keeperUnreadable({}), false);
  const pop = read('src/components/SourcePopover.svelte');
  assert.match(pop, /checked=\{shownIds\.includes\(source\.id\)\} disabled=\{unreadableIds\.has\(source\.id\)\}/);
  assert.match(pop, /shownIds\.length === 0 \? 'Choose sources'/, 'the header counts only what a run can read');
  const state = read('src/lib/appState.svelte.ts');
  assert.match(state, /function unselectUnreadableSources\(\)/);
  assert.equal((state.match(/unselectUnreadableSources\(\);/g) || []).length, 2, 'the first list and every re-read');
  assert.match(state, /if \(keeperRefreshing\) return;/, 'a burst of refusals makes one re-read');
});
