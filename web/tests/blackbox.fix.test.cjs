const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

// P9 review fixes: each test pins one finding from the UI review or the re-review so it cannot come back.

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
const read = (relative) => fs.readFileSync(path.join(root, relative), 'utf8').replace(/\r\n/g, '\n');

const bb = loadTypeScript('src/lib/blackbox.ts');
const { BB_TEXT: T } = loadTypeScript('src/lib/copy/blackboxCopy.ts');
const { makeRevealGuard } = loadTypeScript('src/lib/blackboxKey.ts');

test('review 1: a Listens on address that is not an address or a name is refused', () => {
  const good = bb.defaultSettings('web-a');
  assert.equal(bb.settingsProblem(good), null);
  for (const bind of ['127.0.0.1', '0.0.0.0', '::1', 'web-a', 'web-a.corp.local', 'fe80::1']) {
    assert.equal(bb.settingsProblem({ ...good, bind }), null, bind);
  }
  for (const bind of ['not an address !!', '', '   ', '999.1.1.1', '1.2.3', 'a_b', '-web', 'web-', 'http://x', 'a b']) {
    assert.equal(bb.settingsProblem({ ...good, bind }), 'bind', JSON.stringify(bind));
  }
  assert.match(T.settingProblems.bind, /address such as 127\.0\.0\.1, or a server name/);
});

test('review 2: a system folder or a drive root is refused with plain words and no file or field names', () => {
  const good = bb.defaultSettings('web-a');
  for (const folder of ['C:\\Windows\\System32', 'c:/windows', 'C:\\Program Files\\x', 'C:\\Program Files (x86)', 'C:\\Users', 'C:\\ProgramData\\', 'D:\\', 'D:']) {
    assert.equal(bb.settingsProblem({ ...good, folder }), 'folder', folder);
  }
  for (const folder of ['D:\\UnravisBlackbox', 'C:\\Users\\bob\\bb', 'E:\\Recordings\\web-a']) {
    assert.equal(bb.settingsProblem({ ...good, folder }), null, folder);
  }
  assert.match(T.settingProblems.folder, /Windows system folder/);
  assert.doesNotMatch(T.settingProblems.folder + T.settingProblems.bind, /blackbox\.json|"folder"|"bind"|not valid/);
});

test('review 3 and 4: the drawer takes focus when it opens and every screen scrolls to the top and focuses its heading', () => {
  const drawer = read('src/components/Drawer.svelte');
  assert.match(drawer, /focusOnOpen = false/);
  assert.match(drawer, /onMount\(\(\) => \{ if \(focusOnOpen\) titleElement\?\.focus\(\{ preventScroll: true \}\); \}\)/);
  assert.match(drawer, /tabindex=\{focusTitle \|\| focusOnOpen \? -1 : undefined\}/);
  const panel = read('src/components/BlackboxPanel.svelte');
  assert.match(panel, /<Drawer[^>]* focusOnOpen /);
  assert.match(panel, /async function show\(next: View\)[\s\S]*await tick\(\);\s*drawerEl\?\.scrollTo\?\.\(\{ top: 0 \}\)/);
  assert.match(panel, /h3\[tabindex="-1"\][\s\S]*focus\(\{ preventScroll: true \}\)/);
  for (const file of ['BlackboxPanel', 'BlackboxAdd', 'BlackboxRemove']) {
    assert.match(read('src/components/' + file + '.svelte'), /<h3 tabindex="-1">/, file);
  }
});

test('review 5: the wizard name error clears as soon as the name is valid', () => {
  const wizard = read('src/components/BlackboxWizard.svelte');
  assert.match(wizard, /oninput=\{\(\) => \{ if \(problem && !nameProblem\(name\)\) problem = ''; \}\}/);
});

test('review 6 and 7: the facts say files and samples in words, and unread files count as gaps', () => {
  assert.equal(T.filesAndSamples(4, 960), '4 files and 960 samples');
  assert.equal(T.filesAndSamples(1, 1), '1 file and 1 sample');
  assert.equal(T.gapsText(0, 0), 'none');
  assert.equal(T.gapsText(2, 0), '2 gaps');
  assert.equal(T.gapsText(1, 0), '1 gap');
  assert.equal(T.gapsText(0, 1), '1 recording file was not read to the end');
  assert.equal(T.gapsText(1, 3), '1 gap. 3 recording files were not read to the end');
  const panel = read('src/components/BlackboxPanel.svelte');
  assert.match(panel, /T\.filesAndSamples\(item\.facts\.files, item\.facts\.samples\)/);
  assert.match(panel, /T\.gapsText\(item\.facts\.gaps, item\.facts\.unread \?\? 0\)/);
  assert.doesNotMatch(panel, /\{item\.facts\.files\}, \{item\.facts\.samples\}/);
});

test('review 8: the case Add data card says whether the case reads a Blackbox', () => {
  const card = read('src/components/sources/AddDataPanel.svelte');
  assert.match(card, /caseUseApi\.listTags\(caseId\)/);
  assert.match(card, /t\.kind === BLACKBOX_TAG_KIND/);
  assert.match(card, /<p class="hint">\{ADD_TEXT\.held\(blackboxHeld\)\}<\/p>/);
});

test('review 9: the host card has its own nutshell and never a lazy plural', () => {
  const card = read('src/components/BlackboxHostCard.svelte');
  assert.match(card, /\{T\.hostNutshell\}/);
  assert.doesNotMatch(card, /answer\.step\.nutshell/);
  assert.doesNotMatch(T.hostNutshell, /Cannot test yet|Until that data/);
  assert.doesNotMatch(T.hostNutshell, /\(s\)/);
});

test('review 10: Use in this case is off for Not placed, Unreachable and Not found entries', () => {
  const use = read('src/components/BlackboxCaseUse.svelte');
  assert.match(use, /const readable = \$derived\(placed && item\.state\.state !== 'unreachable' && item\.state\.state !== 'not-found'\)/);
  assert.match(use, /disabled=\{!!busy \|\| !caseId \|\| !readable \|\| use\?\.state === 'other'\}/);
  assert.match(use, /\{T\.caseUseNotReadable\}/);
});

test('review 12: Remove and Forget are outlined and Cancel is the one primary button', () => {
  const remove = read('src/components/BlackboxRemove.svelte');
  assert.match(remove, /class="secondary-button" disabled=\{busy \|\| !ready\} onclick=\{removeHere\}/);
  assert.equal((remove.match(/class="secondary-button"[^>]*onclick=\{forget\}/g) ?? []).length, 2);
  assert.doesNotMatch(remove, /class="primary-button"[^>]*onclick=\{(removeHere|forget)\}/);
  assert.ok((remove.match(/class="primary-button"[^>]*onclick=\{onCancel\}/g) ?? []).length >= 3);
});

test('review 13: a stale tab refreshes the list on open and Forget on a missing entry leaves only Back to the list', () => {
  const panel = read('src/components/BlackboxPanel.svelte');
  assert.match(panel, /if \(fromList && next\.kind === 'status'\) void refreshOpened\(next\.id\)/);
  assert.match(panel, /!items\.some\(\(i\) => i\.id === id\)[\s\S]*scanNote = T\.goneFromList/);
  const remove = read('src/components/BlackboxRemove.svelte');
  assert.match(remove, /error\.refusal\.status === 404/);
  assert.equal((remove.match(/phase\.kind === 'failed' && phase\.missing/g) ?? []).length, 2);
  assert.match(remove, /class="primary-button" onclick=\{onChanged\}>\{T\.back\}/);
});

test('review 14: a typed key is described as typed, not as shown on the server at install', () => {
  assert.match(T.readKeyNote, /^You typed the read key/);
  assert.doesNotMatch(T.readKeyNote, /installed|shown once/);
});

test('re-review N1(a): the revealed key is not in a live region, a short status says it is shown', () => {
  const key = read('src/components/BlackboxReadKey.svelte');
  assert.doesNotMatch(key, /bb-key-value"[^>]*aria-live/);
  assert.match(key, /<p class="bb-key-value">/);
  assert.match(key, /\{#if key\}<p class="bb-sr" role="status">\{T\.keyShownAnnounce\}<\/p>\{\/if\}/);
  assert.doesNotMatch(T.keyShownAnnounce, /\w{16,}/);
});

test('re-review N1(b): a late Show answer cannot set the key after Hide, the timer or closing', () => {
  const guard = makeRevealGuard();
  const first = guard.next();
  assert.equal(guard.valid(first), true);
  guard.cancel();
  assert.equal(guard.valid(first), false, 'Hide or close cancels a Show in flight');
  const second = guard.next();
  const third = guard.next();
  assert.equal(guard.valid(second), false, 'a newer Show replaces an older one');
  assert.equal(guard.valid(third), true);
  const key = read('src/components/BlackboxReadKey.svelte');
  assert.match(key, /function letGo\(\) \{\s*guard\.cancel\(\);/);
  assert.match(key, /const got = await blackboxApi\.revealKey\(item\.id\);\s*if \(!guard\.valid\(mine\)\) return;\s*key = got;/);
  assert.match(key, /catch \(error\) \{\s*if \(!guard\.valid\(mine\)\) return;/);
});
