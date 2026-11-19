const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const { loadTypeScript } = require('./helpers/loadTs.cjs');

// P9 review fixes: each test pins one finding from the UI review or the re-review so it cannot come back.

const root = path.resolve(__dirname, '..');

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
  assert.match(drawer, /onMount\(\(\)\s*=>\s*\{\s*if\s*\(focusOnOpen\)\s*titleElement\?\.focus\(\{\s*preventScroll:\s*true\s*\}\);\s*\}\)/);
  assert.match(drawer, /tabindex=\{focusTitle\s*\|\|\s*focusOnOpen\s*\?\s*-1\s*:\s*undefined\}/);
  const panel = read('src/components/BlackboxPanel.svelte');
  assert.match(panel, /<Drawer[^>]* focusOnOpen /);
  assert.match(panel, /async function show\(next:\s*View\)[\s\S]*await tick\(\);[\s\S]*if \(!popView && !wasPop\) drawerEl\?\.scrollTo\?\.\(\{\s*top:\s*0\s*\}\)/);
  // Footer Close in the pop-up asks first, like Esc and the corner X (review of BL-131).
  assert.match(panel, /onclick=\{\(\) => pop\?\.requestLeave\(\)\}>\{T\.popClose\}/);
  assert.match(read('src/components/blackboxWizard/WizardDialog.svelte'), /export function requestLeave\(\): void \{ handleLeaveRequest\(\); \}/);
  assert.match(panel, /h3\[tabindex="-1"\][\s\S]*focus\(\{ preventScroll: true \}\)/);
  for (const file of ['BlackboxPanel', 'BlackboxAdd', 'BlackboxRemove']) {
    assert.match(read('src/components/' + file + '.svelte'), /<h3 tabindex="-1">/, file);
  }
});

test('review 5: the wizard name error clears as soon as the name is valid', () => {
  // The pop-up's name step keeps no problem string of its own: the line is derived from the one state on every keystroke,
  // so it is gone the moment the typed name is valid (this replaces the drawer wizard's oninput that cleared its string).
  const step = read('src/components/blackboxWizard/StepName.svelte');
  assert.match(step, /const problem = \$derived\(stepProblem\(wizard, 'name'\)\);/);
  assert.match(step, /const show = \$derived\(problem !== null && \(touched \|\| showProblem\)\);/);
  assert.match(step, /oninput=\{type\}/);
  assert.match(step, /onChange\(\{ \.\.\.wizard, settings: \{ \.\.\.wizard\.settings, name: event\.currentTarget\.value \} \}\)/);
  assert.match(step, /\{#if show && words\}/);
  const w = loadTypeScript('src/lib/blackboxWizard.ts');
  const typed = (name) => ({ ...w.initialState(), settings: { ...w.initialState().settings, name } });
  assert.equal(w.stepProblem(typed('web a'), 'name'), 'name-chars');
  assert.equal(w.stepProblem(typed('web-a'), 'name'), null, 'a valid name leaves no problem to show');
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
  assert.match(panel, /T\.filesAndSamples\(item\.facts\.files,\s*item\.facts\.samples\)/);
  assert.match(panel, /T\.gapsText\(item\.facts\.gaps,\s*item\.facts\.unread\s*\?\?\s*0,\s*item\.facts\.longestGapSeconds\s*\?\?\s*0\)/);
  assert.doesNotMatch(panel, /\{item\.facts\.files\}, \{item\.facts\.samples\}/);
});

test('review 8: the case Add data card says whether the case reads a Blackbox', () => {
  const card = read('src/components/sources/AddDataPanel.svelte');
  assert.match(card, /caseUseApi\.listTags\(caseId\)/);
  assert.match(card, /t\.kind === BLACKBOX_TAG_KIND/);
  assert.match(card, /<span\s+class="pill"\s+data-tone="warn">\{ADD_TEXT\.held\(blackboxHeld\)\}<\/span>/);
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
  assert.match(use, /const readable\s*=\s*\$derived\(placed\s*&&\s*item\.state\.state\s*!==\s*'unreachable'\s*&&\s*item\.state\.state\s*!==\s*'not-found'\)/);
  assert.match(use, /disabled=\{!!busy\s*\|\|\s*!caseId\s*\|\|\s*!readable\s*\|\|\s*closed\s*\|\|\s*use\?\.state\s*===\s*'other'\}/);
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
  assert.match(panel, /if\s*\(fromList\s*&&\s*next\.kind\s*===\s*'status'\)\s*void\s*refreshOpened\(next\.id\)/);
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
  assert.match(key, /\{#if\s+key\}\s*<p\s+class="bb-sr"\s+role="status">\{T\.keyShownAnnounce\}<\/p>\s*\{\/if\}/);
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
  assert.match(key, /function letGo\(\)\s*\{\s*guard\.cancel\(\);/);
  assert.match(key, /const got\s*=\s*await\s*blackboxApi\.revealKey\(item\.id\);\s*if\s*\(!guard\.valid\(mine\)\)\s*return;\s*key\s*=\s*got;/);
  assert.match(key, /catch\s*\(error\)\s*\{\s*if\s*\(!guard\.valid\(mine\)\)\s*return;/);
});
