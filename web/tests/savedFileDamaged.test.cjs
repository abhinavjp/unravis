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

test('a set-aside that fails says what happened, a file that reads fine just reloads, and any other failure asks for a re-check', () => {
  assert.deepEqual(savedFile.setAsideFailure(failure('saved-file-not-damaged', 'x', 409)), { healthy: true, text: copy.SAVED_FILE_TEXT.readsFine, recheck: false });
  const full = savedFile.setAsideFailure(failure('set-aside-full', 'Three unreadable files are already set aside, so this one cannot be set aside too.', 409));
  assert.equal(full.healthy, false);
  assert.equal(full.recheck, false);
  assert.match(full.text, /^Three unreadable files are already set aside/);
  // A 503 (the file was busy, the keeper was stopped) shows the server's own sentence and asks the page to read the list again.
  const busy = savedFile.setAsideFailure(failure('', 'The saved connections file could not be set aside. It was left as it was.', 503));
  assert.deepEqual(busy, { healthy: false, text: 'The saved connections file could not be set aside. It was left as it was.', recheck: true });
  assert.deepEqual(savedFile.setAsideFailure(new Error('boom')), { healthy: false, text: copy.SAVED_FILE_TEXT.failed, recheck: true });
});

test('the damaged sentence and the shared flag come from the server\'s answer', () => {
  const shared = Object.assign(failure('saved-file-damaged', 'It is kept in C:\\keeper.', 503), { reason: 'shared' });
  assert.deepEqual(savedFile.damagedInfo(shared), { detail: 'It is kept in C:\\keeper.', shared: true });
  assert.deepEqual(savedFile.damagedInfo(failure('saved-file-damaged', 'Own file.', 503)), { detail: 'Own file.', shared: false });
  assert.deepEqual(savedFile.damagedInfo(failure('unavailable', 'x', 503)), { detail: '', shared: false });
  assert.equal(savedFile.damagedInfo(failure('saved-file-damaged', '', 503)).detail, copy.SAVED_FILE_TEXT.damaged);
});

test('the words say what moves, that the old file is kept, and who is affected', () => {
  assert.match(copy.SAVED_FILE_TEXT.damaged, /cannot be read on this account or by this version of Unravis/);
  assert.doesNotMatch(copy.SAVED_FILE_TEXT.damaged, /damaged and cannot/);
  assert.equal(copy.SAVED_FILE_TEXT.button, 'Set aside and start a new list');
  assert.match(copy.askText(false), /saved list, and the passwords saved with it/);
  assert.match(copy.askText(false), /kept in the same folder and is not deleted/);
  assert.match(copy.askText(false), /will not read it again/);
  assert.doesNotMatch(copy.askText(false), /everyone/);
  assert.match(copy.askText(true), /everyone who uses Unravis on this PC/);
  assert.match(copy.keptNote('connections.dat.unreadable-20261127-143012', 'C:\\Data'), /connections\.dat\.unreadable-20261127-143012 in C:\\Data\./);
  assert.match(copy.keptNote('connections.dat.unreadable-20261127-143012'), /in the same folder as the saved connections file/);
  assert.doesNotMatch(copy.keptNote('connections.dat.unreadable-20261127-143012'), /Unravis data folder/);
  assert.equal(copy.keptNote(''), copy.SAVED_FILE_TEXT.readsFine);
});

test('the drawer and the Connections page offer the way out only on that code, and never in place of the demo message', () => {
  const state = read('src/lib/appState.svelte.ts');
  assert.match(state, /recentFileDamaged = isSavedFileDamaged\(cause\)/);
  assert.match(state, /export async function finishRecentSetAside/);
  const drawer = read('src/components/ConnectionsDrawer.svelte');
  assert.match(drawer, /\{#if conn\.recentFileDamaged\}<SavedFileDamaged detail=\{conn\.recentDamagedDetail\} shared=\{conn\.recentDamagedShared\} onRecheck=\{recheckRecents\} onDone=\{async \(name, folder\) => \{ await finishRecentSetAside\(name, folder\); announceConnectionsChanged\(\); \}\} \/>/);
  const page = read('src/components/sources/ConnectionsPage.svelte');
  assert.match(page, /mongoDamaged = b\.status === 'rejected' && isSavedFileDamaged\(b\.reason\)/);
  assert.match(page, /\{#if mongoDamaged\}<SavedFileDamaged page /);
  assert.match(page, /status === 503 && !isSavedFileDamaged\(r\.reason\)/);
  const api = read('src/lib/api.ts');
  assert.match(api, /setAsideConnections: \(\) => request<\{ name: string; folder\?: string \}>\('\/connections\/recent\/set-aside', \{ method: 'POST' \}\)/);
});

test('the generic Try again never sits beside the set-aside panel', () => {
  const page = read('src/components/sources/ConnectionsPage.svelte');
  assert.match(page, /loadFailed = a\.status === 'rejected' && b\.status === 'rejected' && !demo && !keeperDown && !mongoDamaged;/);
});

test('after a failed set-aside the page and the drawer read the list again, so the way out is offered only while the file is unreadable', () => {
  const part = read('src/components/sources/SavedFileDamaged.svelte');
  assert.match(part, /if \(failure\.recheck\) await onRecheck\?\.\(\);/);
  assert.match(part, /if \(failure\.healthy\) await onDone\('', ''\);/);
  const state = read('src/lib/appState.svelte.ts');
  const recheck = state.slice(state.indexOf('export async function recheckRecents'));
  assert.match(recheck, /recentFileDamaged = false; recentHistoryError = '';/);
  assert.match(recheck, /catch \(cause\) \{ noteRecentsFailure\(cause\); \}/);
  const page = read('src/components/sources/ConnectionsPage.svelte');
  assert.match(page, /async function recheckDamaged\(\) \{\s+await load\(false\);/);
  assert.match(page, /onRecheck=\{recheckDamaged\}/);
});

test('one press only asks: nothing is sent until the person says yes, and No or Esc keeps the list as it is', () => {
  const part = read('src/components/sources/SavedFileDamaged.svelte');
  // The first button opens the question; only the confirm button calls the server.
  assert.match(part, /data-set-aside-ask onclick=\{\(\) => void ask\(\)\}/);
  assert.match(part, /data-set-aside onclick=\{\(\) => void setAside\(\)\}/);
  assert.equal((part.match(/api\.setAsideConnections\(\)/g) ?? []).length, 1);
  assert.match(part, /async function ask\(\) \{[^}]*phase = 'ask'/);
  assert.match(part, /if \(busy \|\| phase !== 'ask'\) return;/);
  assert.match(part, /event\.key !== 'Escape'/);
  assert.match(part, /\{askText\(shared\)\}/);
  assert.match(part, /bind:this=\{keepButton\}/);
  assert.match(part, /disabled=\{busy\}/);
  assert.match(part, /role="alert"/);
});
