const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const ts = require('typescript');
const { compile } = require('svelte/compiler');
const { render } = require('svelte/server');
const { loadTypeScript } = require('./helpers/loadTs.cjs');

// W-20: silent catches. One test group per behaviour that changed (RV-UI-8, -10, -11, -12, -13).
// There is no DOM here: pure functions run as they are, Drawer is rendered by Svelte's server renderer, and the rest is read as source.

const root = path.join(__dirname, '..');
const read = (relative) => fs.readFileSync(path.join(root, relative), 'utf8').replace(/\r\n/g, '\n');

function loadSvelte(relative) {
  const compiled = compile(read(relative), { filename: path.basename(relative), generate: 'server' });
  const js = ts.transpileModule(compiled.js.code, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022, esModuleInterop: true } }).outputText;
  const held = { exports: {} };
  new Function('require', 'module', 'exports', js)(require, held, held.exports);
  return held.exports.default;
}

// ---- RV-UI-13: Drawer busy ----

const noBody = () => {};
const drawerProps = (extra) => ({ title: 'Add databases', titleId: 't', closeLabel: 'Close', onClose() {}, children: noBody, ...extra });

test('Drawer busy: the close button looks inactive and a busy line and bar show', () => {
  const Drawer = loadSvelte('src/components/Drawer.svelte');
  const busy = render(Drawer, { props: drawerProps({ busy: true, busyText: 'Adding the databases. Please wait.' }) }).body;
  assert.match(busy, /aria-disabled="true"/);
  assert.match(busy, /class="[^"]*drawer-close[^"]*busy/);
  assert.match(busy, /role="status"[^>]*>\s*<span>Adding the databases\. Please wait\.<\/span>/);
  assert.match(busy, /drawer-busy-bar/);
});

test('Drawer not busy: the close button is as before and there is no busy line or bar', () => {
  const Drawer = loadSvelte('src/components/Drawer.svelte');
  const idle = render(Drawer, { props: drawerProps({ busyText: 'Adding the databases. Please wait.' }) }).body;
  assert.doesNotMatch(idle, /aria-disabled/);
  assert.doesNotMatch(idle, /drawer-busy/);
  assert.doesNotMatch(idle, /Please wait/);
});

test('Drawer busy: Esc, the dimmed page and the close button all go through one guard', () => {
  const source = read('src/components/Drawer.svelte');
  assert.match(source, /const close = \(\) => \{ if \(!busy\) onClose\(\); \};/);
  // onClose is called nowhere but inside that guard.
  assert.equal((source.match(/\bonClose\(\)/g) ?? []).length, 1);
  assert.match(source, /event\.key === 'Escape'\) \{[^}]*close\(\)/);
  // Esc is still taken while busy, so the page behind does not step up either.
  assert.match(source, /event\.key === 'Escape'\) \{ event\.preventDefault\(\);/);
  assert.match(source, /class="drawer-scrim"[^>]*onclick=\{close\}/);
  // The dimmed page and the close button are the only two onclick handlers that close it.
  assert.equal((source.match(/onclick={close}/g) ?? []).length, 2);
});

test('the Connections and Restore drawers pass busy and say why, and the page-level Esc leaves a busy Connections drawer open', () => {
  assert.match(read('src/components/ConnectionsDrawer.svelte'), /busy=\{conn\.sourceBusy\} busyText="Adding the databases\. Please wait\."/);
  assert.match(read('src/lib/components/RestoreDrawer.svelte'), /\{busy\} busyText="Restoring\. Please wait\."/);
  assert.match(read('src/lib/appState.svelte.ts'), /if \(connectionsOpen\) \{ if \(!sourceBusy\) void closeConnections\(\); return; \}/);
});

// ---- RV-UI-11: Apply reasons ----

const bb = loadTypeScript('src/lib/blackbox.ts');
const { BB_TEXT } = loadTypeScript('src/lib/copy/blackboxCopy.ts');

test('Apply reason: another change in progress wins, then settings loading, then settings unreadable, else none', () => {
  assert.equal(bb.applyWait({ working: true, hasSettings: true, readingSettings: false }), 'busy');
  assert.equal(bb.applyWait({ working: true, hasSettings: false, readingSettings: true }), 'busy');
  assert.equal(bb.applyWait({ working: false, hasSettings: false, readingSettings: true }), 'loading');
  assert.equal(bb.applyWait({ working: false, hasSettings: false, readingSettings: false }), 'unreadable');
  assert.equal(bb.applyWait({ working: false, hasSettings: true, readingSettings: true }), null);
});

test('Apply reason: the words are the approved ones and the panel shows them beside both Apply buttons', () => {
  assert.equal(BB_TEXT.applyWaitLoading, "Loading this PC's settings");
  assert.equal(BB_TEXT.applyWaitBusy, 'Another change is being applied');
  const panel = read('src/components/BlackboxPanel.svelte');
  assert.match(panel, /disabled=\{applyBlocked !== null\} aria-describedby=\{applyBlocked \? 'bb-apply-why' : undefined\}/);
  assert.match(panel, /disabled=\{applyBlocked !== null\} aria-describedby=\{applyBlocked \? 'bb-status-apply-why' : undefined\}/);
  assert.match(panel, /id="bb-apply-why">\{applyWhy\}/);
  assert.match(panel, /id="bb-status-apply-why">\{applyWhy\}/);
  assert.match(panel, /applyBlocked === 'busy' \? T\.applyWaitBusy : applyBlocked === 'loading' \? T\.applyWaitLoading/);
});

// ---- RV-UI-12: Blackbox list keeps the last good list ----

test('Blackbox list: a failed load shows the error above the list, which stays, with Try again', () => {
  const panel = read('src/components/BlackboxPanel.svelte');
  const errorAt = panel.indexOf('{T.loadFailed} {loadError}');
  const listAt = panel.indexOf('{:else if items.length === 0}');
  assert.ok(errorAt > 0 && listAt > errorAt, 'the error is above the list');
  // The error is its own block, so the list is not an else-branch of it.
  assert.match(panel, /\{#if loadError\}\s*<!--[^>]*-->\s*<div class="inline-error" role="alert">\{T\.loadFailed\} \{loadError\}\{#if list\} \{T\.loadStale\}\{\/if\}<\/div>/);
  assert.doesNotMatch(panel, /\{:else if loadError\}/);
  assert.match(panel, /\{T\.retry\}/);
  assert.equal(BB_TEXT.loadStale, 'The list below is the last one that loaded, and it may be out of date.');
});

// ---- RV-UI-10: empty key box ----

test('Unlock: an empty box says "Type the key first." and is not reported as a wrong key', () => {
  const { unlockProblemBeforeSend } = loadTypeScript('src/lib/masterKey.ts');
  const { KEY_TEXT } = loadTypeScript('src/lib/copy/masterKeyCopy.ts');
  assert.equal(unlockProblemBeforeSend(''), 'Type the key first.');
  assert.equal(KEY_TEXT.unlockEmpty, 'Type the key first.');
  assert.equal(unlockProblemBeforeSend('x'), '');
  assert.equal(unlockProblemBeforeSend(' '), '');
  const source = read('src/components/MasterKeyUnlock.svelte');
  assert.match(source, /const empty = unlockProblemBeforeSend\(attempt\);\s*if \(empty\) \{ problem = empty;/);
  assert.doesNotMatch(source, /if \(!attempt\) \{ problem = unlockMessage/);
});

// ---- RV-UI-8: failed reads that left the screen looking wrong ----

test('Add another day: a failed read says so, keeps the days shown and offers Try again', () => {
  const source = read('src/components/sources/AddAnotherDay.svelte');
  assert.match(source, /catch \{ loadFailed = true; \}/);
  assert.match(source, /\{#if loadFailed && days\.length === 0\}[\s\S]*data-load-failed[\s\S]*data-load-retry[\s\S]*\{:else if loaded && byDay\.length === 0\}/);
  assert.match(source, /\{#if loadFailed\}<p class="say bad" role="alert" data-load-failed>\{DAY_TEXT\.refreshFailed\}/);
  assert.doesNotMatch(source, /catch \{ \/\* [^*]*\*\/ \}\s*\n\s*loaded = true/);
  const { DAY_TEXT } = loadTypeScript('src/lib/copy/sourcesCopy.ts');
  assert.match(DAY_TEXT.loadFailed, /could not be read/);
  assert.equal(DAY_TEXT.tryAgain, 'Try again');
});

test('Opening a case: a failed read of its saved workspace, or of the sources of a case just made, tells the person', () => {
  const state = read('src/lib/appState.svelte.ts');
  assert.match(state, /workspace = parseWorkspace\(await api\.caseWorkspace\(id\)\); \} catch \{[\s\S]*?if \(!info\?\.demo\) workspaceNote = CASE_RESTORE_TEXT\.workspaceUnreadable;/);
  assert.match(state, /\} catch \{[^}]*workspaceNote = CASE_RESTORE_TEXT\.sourcesUnreadable;/);
  const { CASE_RESTORE_TEXT } = loadTypeScript('src/lib/copy/caseRestoreCopy.ts');
  assert.match(CASE_RESTORE_TEXT.workspaceUnreadable, /not saved with the case/);
  assert.match(CASE_RESTORE_TEXT.sourcesUnreadable, /Open the case again/);
});

test('The catches left silent each carry a comment that says why', () => {
  for (const [file, text] of [
    ['src/components/sourcewiz/AddSourcesFlow.svelte', 'silent on purpose: this read only fills'],
    ['src/components/sources/KeeperCard.svelte', 'silent on purpose: the refusal message above'],
    ['src/components/sources/KeeperCard.svelte', 'silent on purpose: a preview-only refresh'],
    ['src/lib/components/CaseHome.svelte', 'silent on purpose: the one-time offer'],
    ['src/lib/appState.svelte.ts', 'silent on purpose: this only refreshes the recent-connections list']
  ]) assert.ok(read(file).includes(text), `${file}: ${text}`);
});
