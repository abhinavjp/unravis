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
  // A relative import ('../lib/drawerBusy') resolves to the TypeScript file beside the component; the rest is a normal require.
  const load = (request) => {
    if (/^\.\.?\//.test(request)) return loadTypeScript(path.relative(root, path.join(root, path.dirname(relative), request + '.ts')));
    return require(request);
  };
  new Function('require', 'module', 'exports', js)(load, held, held.exports);
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
  assert.match(busy, /role="status"[^>]*>\s*<span class="spinner[^"]*" aria-hidden="true"><\/span>\s*<span>Adding the databases\. Please wait\.<\/span>/);
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
  assert.match(source, /const close = \(\) => \{ closeUnlessBusy\(busy, onClose\); \};/);
  // onClose is called nowhere but inside that guard.
  assert.equal((source.match(/\bonClose\b/g) ?? []).length, 3); // destructured, typed, and the one guarded use
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
  assert.match(read('src/lib/appState.svelte.ts'), /if \(connectionsOpen\) \{ closeUnlessBusy\(sourceBusy, \(\) => void closeConnections\(\)\); return; \}/);
});

test('The close rule: a busy drawer stays open and says so; an idle one closes once', () => {
  const { closeUnlessBusy } = loadTypeScript('src/lib/drawerBusy.ts');
  let closed = 0;
  assert.equal(closeUnlessBusy(true, () => { closed += 1; }), false);
  assert.equal(closed, 0);
  assert.equal(closeUnlessBusy(false, () => { closed += 1; }), true);
  assert.equal(closed, 1);
});

test('Drawer rendered busy imports the real close rule (the component runs it, not a copy)', () => {
  // loadSvelte resolves '../lib/drawerBusy' to the real file; a missing or renamed export would fail to render.
  const Drawer = loadSvelte('src/components/Drawer.svelte');
  assert.ok(render(Drawer, { props: drawerProps({ busy: true, busyText: 'x' }) }).body.length > 0);
});

// ---- RV-UI-11: Apply reasons ----

const bb = loadTypeScript('src/lib/blackbox.ts');
const { BB_TEXT } = loadTypeScript('src/lib/copy/blackboxCopy.ts');

test('Apply reason: another change in progress wins, then settings loading, then settings unreadable, else none', () => {
  assert.equal(bb.applyWait({ working: true, hasSettings: true, readingSettings: false, installed: true }), 'busy');
  assert.equal(bb.applyWait({ working: true, hasSettings: false, readingSettings: true, installed: true }), 'busy');
  assert.equal(bb.applyWait({ working: false, hasSettings: false, readingSettings: true, installed: true }), 'loading');
  assert.equal(bb.applyWait({ working: false, hasSettings: false, readingSettings: false, installed: true }), 'unreadable');
  assert.equal(bb.applyWait({ working: false, hasSettings: true, readingSettings: true, installed: true }), null);
});

test('Apply reason: a PC with no service says so in plain words, and does not claim its settings could not be read', () => {
  assert.equal(bb.applyWait({ working: false, hasSettings: false, readingSettings: false, installed: false }), 'unavailable');
  // 'unavailable' is a blocked state, so the buttons stay off; the reason beside them is its own sentence, not the unreadable one.
  assert.equal(BB_TEXT.applyWaitNotInstalled, 'No Blackbox service is installed on this PC.');
  assert.notEqual(BB_TEXT.applyWaitNotInstalled, BB_TEXT.applyUnreadable);
  const panel = read('src/components/BlackboxPanel.svelte');
  assert.match(panel, /applyBlocked === 'unreadable' \? T\.applyUnreadable : applyBlocked === 'unavailable' \? T\.applyWaitNotInstalled : ''\)/);
  assert.match(panel, /applyWait\(\{[^}]*installed: thisPc\.installed \}\)/);
});

test('Apply reasons are whole sentences with a full stop', () => {
  for (const key of ['applyWaitLoading', 'applyWaitBusy', 'applyWaitNotInstalled', 'applyUnreadable']) assert.match(BB_TEXT[key], /\.$/, key);
});

test('After an Apply the status screen shows a failed look at the list too, with Try again', () => {
  const panel = read('src/components/BlackboxPanel.svelte');
  assert.match(panel, /\{#if loadError\}<div class="inline-error" role="alert" data-status-load-error>\{T\.loadFailed\} \{loadError\}<\/div>[\s\S]*?\{T\.retry\}/);
  // It sits in the status view (after its action messages), not only in the list view.
  assert.ok(panel.indexOf('data-status-load-error') > panel.indexOf("view.kind === 'status' && current"));
});

test('Browser Back closing the Connections drawer is left unguarded on purpose, and says why', () => {
  assert.match(read('src/lib/appState.svelte.ts'), /Left unguarded on purpose \(W-20\): this is Browser Back/);
});

test('Apply reason: the words are the approved ones and the panel shows them beside both Apply buttons', () => {
  assert.equal(BB_TEXT.applyWaitLoading, "Loading this PC's settings.");
  assert.equal(BB_TEXT.applyWaitBusy, 'Another change is being applied.');
  const panel = read('src/components/BlackboxPanel.svelte');
  assert.match(panel, /disabled=\{applyBlocked !== null\} aria-describedby=\{applyWhy \? 'bb-apply-why' : undefined\}/);
  assert.match(panel, /disabled=\{applyBlocked !== null\} aria-describedby=\{applyWhy \? 'bb-status-apply-why' : undefined\}/);
  assert.match(panel, /id="bb-apply-why">\{applyWhy\}/);
  assert.match(panel, /id="bb-status-apply-why">\{applyWhy\}/);
  assert.match(panel, /applyBlocked === 'busy' \? T\.applyWaitBusy : applyBlocked === 'loading' \? T\.applyWaitLoading/);
  // The reason line is drawn only when there are words (a not-installed PC shows none).
  assert.match(panel, /\{#if applyWhy\}<p class="bb-note bb-reason" id="bb-status-apply-why">/);
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
  assert.match(source, /const empty = unlockProblemBeforeSend\(attempt\);[\s\S]*?if \(empty\) \{ problem = ''; await tick\(\); problem = empty; field\?\.focus\(\); return; \}/);
  assert.doesNotMatch(source, /if \(!attempt\) \{ problem = unlockMessage/);
});

// ---- RV-UI-8: failed reads that left the screen looking wrong ----

test('Add another day: a failed read says so, keeps the days shown and offers Try again', () => {
  const source = read('src/components/sources/AddAnotherDay.svelte');
  assert.match(source, /catch \{ loadFailed = true; \}/);
  assert.match(source, /\{#if shown\.view === 'failed'\}[\s\S]*data-load-failed[\s\S]*data-load-retry[\s\S]*\{:else if shown\.view === 'empty'\}/);
  assert.match(source, /\{#if shown\.stale\}<p class="say bad" role="alert" data-load-failed>\{DAY_TEXT\.refreshFailed\}/);
  assert.match(source, /\{#if onCancel && shown\.cancel\}/);
  assert.doesNotMatch(source, /catch \{ \/\* [^*]*\*\/ \}\s*\n\s*loaded = true/);
  const { DAY_TEXT } = loadTypeScript('src/lib/copy/sourcesCopy.ts');
  assert.match(DAY_TEXT.loadFailed, /could not be read/);
  assert.equal(DAY_TEXT.tryAgain, 'Try again');
});

test('Add another day: what shows for each combination of loaded, failed and held', () => {
  const { dayListView } = loadTypeScript('src/lib/lanes.ts');
  const base = { loaded: true, loadFailed: false, daysHeld: 2, byDayCount: 1, canReadSql: true };
  // A failed first read with nothing held is a failure, never "the case holds nothing".
  assert.deepEqual(dayListView({ ...base, loadFailed: true, daysHeld: 0, byDayCount: 0 }), { view: 'failed', stale: false, cancel: true });
  // A failed refresh keeps the days already shown and adds the stale line.
  assert.deepEqual(dayListView({ ...base, loadFailed: true }), { view: 'list', stale: true, cancel: true });
  // A good read of nothing is the empty page; with a way to read SQL, its own button replaces Cancel.
  assert.deepEqual(dayListView({ ...base, daysHeld: 0, byDayCount: 0 }), { view: 'empty', stale: false, cancel: false });
  assert.deepEqual(dayListView({ ...base, daysHeld: 0, byDayCount: 0, canReadSql: false }), { view: 'empty', stale: false, cancel: true });
  // Holding only a by-the-hour-less source (MongoDB) is also "nothing to add a day to".
  assert.equal(dayListView({ ...base, byDayCount: 0 }).view, 'empty');
  // Before the first read finishes, nothing says "empty" or "failed".
  assert.deepEqual(dayListView({ ...base, loaded: false, daysHeld: 0, byDayCount: 0 }), { view: 'list', stale: false, cancel: true });
  assert.deepEqual(dayListView(base), { view: 'list', stale: false, cancel: true });
});

test('Opening a case: a failed read of its saved workspace, or of the sources of a case just made, tells the person', () => {
  const state = read('src/lib/appState.svelte.ts');
  assert.match(state, /workspaceNote = workspaceNoteAfter\('workspace-unreadable', !!info\?\.demo, workspaceNote\);/);
  assert.match(state, /workspaceNote = workspaceNoteAfter\('sources-unreadable', !!info\?\.demo, workspaceNote\);/);
  const { CASE_RESTORE_TEXT } = loadTypeScript('src/lib/copy/caseRestoreCopy.ts');
  assert.match(CASE_RESTORE_TEXT.workspaceUnreadable, /not saved with the case/);
  assert.match(CASE_RESTORE_TEXT.sourcesUnreadable, /Open the case again/);
});

test('The unreadable note does not outlive its case: a restore start or a case change clears it, and the demo stays quiet', () => {
  const { workspaceNoteAfter } = loadTypeScript('src/lib/caseWorkspace.ts');
  const { CASE_RESTORE_TEXT } = loadTypeScript('src/lib/copy/caseRestoreCopy.ts');
  const stale = CASE_RESTORE_TEXT.workspaceUnreadable;
  assert.equal(workspaceNoteAfter('workspace-unreadable', false, ''), stale);
  assert.equal(workspaceNoteAfter('sources-unreadable', false, ''), CASE_RESTORE_TEXT.sourcesUnreadable);
  // A new restore, or a switch to another case, wipes the old note (this is the bug the review found).
  assert.equal(workspaceNoteAfter('restore-start', false, stale), '');
  assert.equal(workspaceNoteAfter('case-changed', false, stale), '');
  assert.equal(workspaceNoteAfter('case-changed', false, CASE_RESTORE_TEXT.sourcesUnreadable), '');
  // The demo has no saved workspace (the read answers 503): that is normal, so no note and an existing one is left as it is.
  assert.equal(workspaceNoteAfter('workspace-unreadable', true, ''), '');
  assert.equal(workspaceNoteAfter('workspace-unreadable', true, 'kept'), 'kept');
  // The same read failing twice in a row ends with the note shown once, not blank.
  const first = workspaceNoteAfter('workspace-unreadable', false, workspaceNoteAfter('restore-start', false, stale));
  assert.equal(first, stale);
  // The app calls it at each of those points.
  const state = read('src/lib/appState.svelte.ts');
  assert.match(state, /workspaceNote = workspaceNoteAfter\('restore-start'/);
  assert.equal((state.match(/workspaceNoteAfter\('case-changed'/g) ?? []).length, 2);
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
