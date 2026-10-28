// P3 fix pass: the page side of the UI review findings (drawer focus, case home after a move, master key wording, folders).
// Each test pins one finding so it cannot come back.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

const root = path.resolve(__dirname, '..');
const loaded = new Map();
function load(relativePath) {
  const filename = path.join(root, relativePath);
  if (loaded.has(filename)) return loaded.get(filename).exports;
  const output = ts.transpileModule(fs.readFileSync(filename, 'utf8'), { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022, esModuleInterop: true } }).outputText;
  const module = { exports: {} };
  loaded.set(filename, module);
  const nodeRequire = createRequire(filename);
  const req = (request) => {
    if (/^\.\.?\//.test(request)) {
      const plain = path.join(path.dirname(filename), request + '.ts');
      const store = path.join(path.dirname(filename), request + '.svelte.ts');
      if (fs.existsSync(plain)) return load(path.relative(root, plain));
      if (fs.existsSync(store)) return load(path.relative(root, store));
    }
    return nodeRequire(request);
  };
  new Function('require', 'module', 'exports', '$state', output)(req, module, module.exports, (value) => value);
  return module.exports;
}
const read = (relative) => fs.readFileSync(path.join(root, relative), 'utf8').replace(/\r\n/g, '\n');

// UI major 1: the four archive drawers take focus when they open (so Escape and Tab work from the first key press)
// and give it back to the button that opened them.
test('the archive, free and restore drawers take focus on open and return it', () => {
  for (const name of ['ArchiveDrawer', 'FreeDrawer', 'RestoreDrawer']) {
    const text = read('src/lib/components/' + name + '.svelte');
    const tag = text.match(/<Drawer [^>]*>/)[0];
    assert.match(tag, /\bfocusOnOpen\b/, name + ' must take focus');
    assert.match(tag, /\breturnFocus\b/, name + ' must return focus');
  }
  const drawer = read('src/components/Drawer.svelte');
  assert.match(drawer, /event\.key === 'Escape'/, 'the shared drawer closes on Escape');
  assert.match(drawer, /focusOnOpen\) titleElement\?\.focus/, 'and puts focus inside on open');
});

test('closing a drawer lets focus go back to the trigger, and falls back to the case name only when the trigger is gone', () => {
  const home = read('src/lib/components/CaseHome.svelte');
  const fn = home.match(/function closeArchiveDrawer\(\) \{[\s\S]*?\n  \}/)[0];
  assert.match(fn, /document\.activeElement/);
  assert.match(fn, /document\.body/);
  assert.match(fn, /headingEl\?\.focus\(\)/);
});

const life = load('src/lib/caseLifecycle.ts');
const copy = load('src/lib/copy/archiveCopy.ts');
const { lintScreenStrings } = load('src/lib/copy/copylint.ts');

// UI major 2, minor 8 and 9: an archived case answers 409 to its files, checks and theories, so the case home must not ask for them
// (that read failing silently is what left the page stale after archiving), and it offers no analysis for it (spec section 9).
test('only an open or closed case reads its own data', () => {
  assert.equal(life.readsCaseData({ state: 'open', closed: null }), true);
  assert.equal(life.readsCaseData({ state: 'closed', closed: 'x' }), true);
  assert.equal(life.readsCaseData({ closed: 'x' }), true, 'an older server with only the closed stamp');
  assert.equal(life.readsCaseData({ state: 'archived', closed: 'x' }), false);
  assert.equal(life.readsCaseData({ state: 'archiving', closed: 'x' }), false);
});

test('an archived case has no Reopen button (it would never work); Restore is the way back', () => {
  assert.equal(life.reopenButton({ state: 'archived', closed: 'x' }, false), 'hidden');
  assert.equal(life.reopenButton({ state: 'archiving', closed: 'x' }, false), 'hidden');
});

test('the case home reads the case first and asks for files, checks and theories only when it may', () => {
  const home = read('src/lib/components/CaseHome.svelte');
  const load = home.match(/async function load\(id: string, quiet = false\) \{[\s\S]*?\n  \}\n/)[0];
  assert.match(load, /getCaseView\(id, mine\.signal\)\]\);[\s\S]*readsCaseData\(found\)/, 'the view is read before anything else');
  assert.match(load, /reads \? listImports\(/);
  assert.match(load, /reads \? fetchHealthRuns\(/);
  assert.match(load, /reads \? refreshLedger\(/);
  assert.doesNotMatch(load, /Promise\.allSettled\(\[getCaseView\(id, mine\.signal\), listImports/, 'no read of an archived case in parallel with the view');
  const reread = home.match(/async function rereadFiles\(\) \{[\s\S]*?\n  \}\n/)[0];
  assert.match(reread, /readsCaseData\(view\) \? await listImports/, 'a reread of an archived case does not list files');
});

test('after a move the whole page is read again, so an archived case shows as archived at once', () => {
  const home = read('src/lib/components/CaseHome.svelte');
  const moved = home.match(/function archiveMoved\([\s\S]*?\n  \}\n/)[0];
  assert.match(moved, /load\(caseId, true\)/);
  assert.match(moved, /restore-as-new/);
});

test('the archived home hides the analysis, the checks, the sources, the theories and the file list, and says why', () => {
  const home = read('src/lib/components/CaseHome.svelte');
  const at = (needle) => home.indexOf(needle);
  const open1 = at('{#if offersWork}');
  const story = at('aria-labelledby="home-story"');
  const open2 = home.indexOf('{#if offersWork}', open1 + 1);
  assert.ok(open1 > 0 && open2 > story, 'two guarded runs, one before the story and one after');
  for (const id of ['home-analysis', 'home-guided']) assert.ok(at(id) > open1 && at(id) < story, id);
  for (const id of ['home-theories', 'home-health', 'home-sources', 'home-files']) assert.ok(at(id) > open2, id);
  assert.match(home, /ARCHIVE_TEXT\.archivedWork/);
  assert.ok(copy.archiveLintEntries().some((entry) => entry.text === copy.ARCHIVE_TEXT.archivedWork), 'the new sentence is in the copy lint');
});

test('the archived list opens a case with its theories held back, and nothing else holds them', () => {
  assert.match(read('src/components/StartScreen.svelte'), /onOpen=\{\(id\) => onOpen\(id, \{ archived: true \}\)\}/);
  assert.match(read('src/lib/appState.svelte.ts'), /holdLedger\(id, options\.archived === true\)/);
  assert.match(read('src/lib/ledgerStore.svelte.ts'), /if \(held\.has\(caseId\)\) return Promise\.resolve\(\);/);
});

// F7 and UI major 3: with no master key set up there is nothing to unlock. Free and Restore as new say so before the check or the typing, and
// offer the one way forward. The failure text is "Set a master key first" (the server's own sentence), never "Unlock it first".
test('no master key: the wording says set one, not unlock', () => {
  const { ARCHIVE_TEXT, archiveReasonText } = copy;
  assert.match(ARCHIVE_TEXT.noMasterKey, /Set a master key first/);
  assert.doesNotMatch(ARCHIVE_TEXT.noMasterKey, /unlock/i);
  assert.equal(archiveReasonText('no-master-key', 409), ARCHIVE_TEXT.noMasterKey);
  for (const key of ['noMasterKeyFree', 'noMasterKeyRestore']) assert.match(ARCHIVE_TEXT[key], /Set a master key first/, key);
  const entries = copy.archiveLintEntries().map((entry) => entry.text);
  for (const key of ['noMasterKey', 'noMasterKeyFree', 'noMasterKeyRestore', 'noMasterKeySetup', 'noMasterKeyLater']) assert.ok(entries.includes(ARCHIVE_TEXT[key]), key + ' is in the copy lint');
});

test('Free and Restore as new look at the master key state on open and show the way to set one', () => {
  for (const [name, what] of [['FreeDrawer', 'free'], ['RestoreDrawer', 'restore']]) {
    const text = read('src/lib/components/' + name + '.svelte');
    assert.match(text, /getMasterKeyStatus\(\)\)\.state === 'none'\) noKey = true/, name + ' reads the key state');
    assert.match(text, new RegExp('<NoMasterKey what="' + what + '" onSetup=\{setUpKey\}'), name + ' shows the message');
    assert.match(text, /function setUpKey\(\) \{[^}]*onClose\(\); openKeySetup\(\);/, name + ' closes and goes to the set up screen');
    assert.match(text, /reason === 'no-master-key'\) noKey = true/, name + ' also falls back on the server answer');
  }
  const free = read('src/lib/components/FreeDrawer.svelte');
  assert.ok(free.indexOf('{#if noKey && stage !== 3}') < free.indexOf('{:else if stage === 1}'), 'the message replaces the check form');
  assert.match(read('src/lib/components/RestoreDrawer.svelte'), /mode === 'as-new' && noKey/, 'Restore in place is not blocked');
  const small = read('src/lib/components/NoMasterKey.svelte');
  assert.match(small, /ARCHIVE_TEXT\.noMasterKeySetup/);
  assert.match(small, /class="primary-button"/);
  assert.match(small, /class="secondary-button"/);
});
