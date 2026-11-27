const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

// RV-DAT-1 (W-13): the page offers "Set aside and start a new list" only for the server's own "saved-file-damaged" code.
const root = path.resolve(__dirname, '..');
const read = (relative) => fs.readFileSync(path.join(root, relative), 'utf8').replace(/\r\n/g, '\n');
function loadTypeScript(relativePath) {
  const filename = path.join(root, relativePath);
  const output = ts.transpileModule(fs.readFileSync(filename, 'utf8'), {
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

const savedFile = loadTypeScript('src/lib/savedFile.ts');
const copy = loadTypeScript('src/lib/copy/savedFileCopy.ts');
const failure = (code, message, status) => Object.assign(new Error(message), { code, status });

test('only the server\'s saved-file-damaged code counts as a damaged file', () => {
  assert.equal(savedFile.isSavedFileDamaged(failure('saved-file-damaged', 'x', 503)), true);
  assert.equal(savedFile.isSavedFileDamaged(failure('unavailable', 'x', 503)), false);
  assert.equal(savedFile.isSavedFileDamaged(new Error('The saved connections file is damaged')), false);
  assert.equal(savedFile.isSavedFileDamaged(null), false);
  assert.equal(savedFile.isSavedFileDamaged('saved-file-damaged'), false);
});

test('a set-aside that fails says what happened, and a file that reads fine just reloads', () => {
  assert.deepEqual(savedFile.setAsideFailure(failure('saved-file-not-damaged', 'x', 409)), { healthy: true, text: copy.SAVED_FILE_TEXT.readsFine });
  const full = savedFile.setAsideFailure(failure('set-aside-full', 'Three damaged files are already set aside, so this one cannot be set aside too.', 409));
  assert.equal(full.healthy, false);
  assert.match(full.text, /^Three damaged files are already set aside/);
  assert.deepEqual(savedFile.setAsideFailure(new Error('boom')), { healthy: false, text: copy.SAVED_FILE_TEXT.failed });
});

test('the words are the plan\'s, and the kept note names the file', () => {
  assert.equal(copy.SAVED_FILE_TEXT.damaged, 'The saved connections file is damaged and cannot be read. You can set it aside and start a new list. The old file is kept.');
  assert.equal(copy.SAVED_FILE_TEXT.button, 'Set aside and start a new list');
  assert.match(copy.keptNote('connections.dat.unreadable-20261127-143012'), /connections\.dat\.unreadable-20261127-143012 in the Unravis data folder/);
  assert.equal(copy.keptNote(''), copy.SAVED_FILE_TEXT.readsFine);
});

test('the drawer and the Connections page offer the way out only on that code, and never in place of the demo message', () => {
  const state = read('src/lib/appState.svelte.ts');
  assert.match(state, /recentFileDamaged = isSavedFileDamaged\(cause\)/);
  assert.match(state, /export async function finishRecentSetAside/);
  const drawer = read('src/components/ConnectionsDrawer.svelte');
  assert.match(drawer, /\{#if conn\.recentFileDamaged\}<SavedFileDamaged onDone=\{finishRecentSetAside\} \/>/);
  const page = read('src/components/sources/ConnectionsPage.svelte');
  assert.match(page, /mongoDamaged = b\.status === 'rejected' && isSavedFileDamaged\(b\.reason\)/);
  assert.match(page, /\{#if mongoDamaged\}<SavedFileDamaged page /);
  assert.match(page, /status === 503 && !isSavedFileDamaged\(r\.reason\)/);
  const api = read('src/lib/api.ts');
  assert.match(api, /setAsideConnections: \(\) => request<\{ name: string \}>\('\/connections\/recent\/set-aside', \{ method: 'POST' \}\)/);
});

test('the button is disabled while it works, so a double press cannot set aside twice', () => {
  const part = read('src/components/sources/SavedFileDamaged.svelte');
  assert.match(part, /if \(busy\) return;/);
  assert.match(part, /disabled=\{busy\}/);
  assert.match(part, /role="alert"/);
});
