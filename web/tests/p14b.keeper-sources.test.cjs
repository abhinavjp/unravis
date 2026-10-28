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
  assert.match(state, /if \(keeperSourceChanged\(cause\)\) void refreshKeeperSources\(\);/);
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
