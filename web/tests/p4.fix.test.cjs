const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

// P4 fix pass: a wrong case key does not lock the app (real Response objects), and the other P4 review fixes.

const root = path.resolve(__dirname, '..');
function loadTypeScript(relativePath, runes = false, cache = new Map()) {
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
      if (fs.existsSync(plain)) return loadTypeScript(path.relative(root, plain), runes, cache);
      if (fs.existsSync(store)) return loadTypeScript(path.relative(root, store), runes, cache);
    }
    return nodeRequire(request);
  };
  new Function('require', 'module', 'exports', '$state', output)(load, module, module.exports, runes ? (value) => value : undefined);
  return module.exports;
}
const read = (relative) => fs.readFileSync(path.join(root, relative), 'utf8');

const TOKEN = 'link-session-token-0123456789abcdef';
const ID = 'k7m2qx4a' + 'abcdefghij' + 'klmnopqr';
const PASS = 'unravis demo case key';

// Runs `fn` with stand-ins for the browser globals api.ts reads at load time, and a fetch that answers from `responses`.
async function withApi(responses, fn) {
  const names = ['location', 'history', 'sessionStorage', 'fetch'];
  const saved = Object.fromEntries(names.map((name) => [name, Object.getOwnPropertyDescriptor(globalThis, name)]));
  const calls = [];
  const items = new Map([['unravis.session', TOKEN]]);
  const values = {
    location: { hash: '', search: '', pathname: '/' },
    history: { state: null, replaceState: () => {} },
    sessionStorage: { getItem: (k) => (items.has(k) ? items.get(k) : null), setItem: (k, v) => { items.set(k, String(v)); }, removeItem: (k) => { items.delete(k); } },
    fetch: async (url, init = {}) => {
      calls.push({ url: String(url), method: init.method ?? 'GET', headers: new Headers(init.headers), body: init.body });
      const next = responses.shift() ?? { status: 200, body: {} };
      if (next.network) throw new TypeError('Failed to fetch');
      const make = () => ({ ok: next.status >= 200 && next.status < 300, status: next.status, json: async () => next.body, text: async () => JSON.stringify(next.body ?? {}), headers: { get: () => null } });
      return new Response(JSON.stringify(next.body ?? {}), { status: next.status, headers: { 'Content-Type': 'application/json' } });
    }
  };
  for (const name of names) Object.defineProperty(globalThis, name, { configurable: true, writable: true, value: values[name] });
  try {
    return await fn({ calls, items });
  } finally {
    for (const name of names) {
      if (saved[name]) Object.defineProperty(globalThis, name, saved[name]);
      else delete globalThis[name];
    }
  }
}


const shared = new Map();
const lockSignal = loadTypeScript('src/lib/lockSignal.ts', false, shared);

test('a wrong case key (423 wrong-case-key) does not tell the app the master key is locked', async () => {
  await withApi([{ status: 423, body: { reason: 'wrong-case-key' } }, { status: 423, body: { reason: 'case-locked' } }, { status: 423, body: { reason: 'locked' } }], async () => {
    let signals = 0;
    lockSignal.onCasesLocked(() => { signals++; });
    const api = loadTypeScript('src/lib/caseKeyApi.ts', false, shared);
    await assert.rejects(() => api.unlockCase(ID, PASS), (error) => error.reason === 'wrong-case-key');
    assert.equal(signals, 0, 'a wrong case key stays on the case card');
    await assert.rejects(() => api.getCaseKeyStatus(ID), (error) => error.reason === 'case-locked');
    assert.equal(signals, 0);
    await assert.rejects(() => api.getCaseKeyStatus(ID), (error) => error.reason === 'locked');
    assert.equal(signals, 1, 'a real master lock still reaches the unlock screen');
  });
});

test('signalIfAppLocked leaves a body that was already read alone', async () => {
  let signals = 0;
  lockSignal.onCasesLocked(() => { signals++; });
  const response = new Response(JSON.stringify({ reason: 'wrong-case-key' }), { status: 423 });
  await response.json();
  await lockSignal.signalIfAppLocked(response);
  assert.equal(signals, 0);
  await lockSignal.signalIfAppLocked(new Response(JSON.stringify({ reason: 'locked' }), { status: 423 }));
  assert.equal(signals, 1);
});

// ---- the archive and restore of a keyed case

test('an archive file made under a case key is parsed as such, and its path is the folder and the name', () => {
  const model = loadTypeScript('src/lib/archiveModel.ts');
  const file = model.parseArchiveFile({ name: 'a.unravis-case', folder: 'D:\\Unravis archive\\', bytes: 5, keyKind: 'case', keyEnding: '082C', counts: {} });
  assert.equal(file.keyKind, 'case');
  assert.equal(file.keyEnding, '082c');
  assert.equal(model.archiveFilePath(file), 'D:\\Unravis archive\\a.unravis-case');
  assert.equal(model.archiveFilePath(null), '');
  assert.equal(model.archiveFilePath({ name: 'a.unravis-case', folder: '' }), 'a.unravis-case');
  assert.equal(model.parseArchiveFile({ name: 'b', keyKind: 'weird' }).keyKind, 'master');
});

test('the archive dialog tells a keyed case it is sealed with its case key and asks for no passphrase', () => {
  const src = read('src/lib/components/ArchiveDrawer.svelte');
  assert.match(src, /const ownKey = \$derived\(info\.ownKey === true\)/);
  assert.match(src, /const typed = \$derived\(!ownKey && /, 'a keyed case never shows the typed-passphrase field');
  assert.match(src, /\{#if ownKey\}\s*<p class="hint">\{ARCHIVE_TEXT\.sealedCaseHint\}<\/p>\s*\{:else if typed\}/);
  const { ARCHIVE_TEXT } = loadTypeScript('src/lib/copy/archiveCopy.ts');
  assert.match(ARCHIVE_TEXT.sealedCase, /case key/i);
  assert.match(ARCHIVE_TEXT.sealedCaseHint, /any computer/);
});

test('the restore dialog asks a keyed file for its case key and says the new case keeps it', () => {
  const src = read('src/lib/components/RestoreDrawer.svelte');
  assert.match(src, /const caseKeyed = \$derived\(mode === 'as-new' && file\?\.keyKind === 'case'\)/);
  assert.match(src, /caseKeyed \? ARCHIVE_TEXT\.restoreCaseKeyLabel/);
  const { ARCHIVE_TEXT, archiveReasonText } = loadTypeScript('src/lib/copy/archiveCopy.ts');
  assert.match(ARCHIVE_TEXT.restoreCaseKeyHint, /keeps this key/);
  assert.equal(archiveReasonText('case-key-needed'), ARCHIVE_TEXT.caseKeyNeeded);
});

test('Erase names the case file when the page knows it, and CaseHome passes the path', () => {
  const copy = loadTypeScript('src/lib/copy/eraseCopy.ts');
  assert.match(copy.eraseBody('D:\\a\\b.unravis-case'), /The case file D:\\a\\b\.unravis-case is not touched\./);
  assert.match(copy.eraseBody(), /A case file you saved is not touched\./);
  const home = read('src/lib/components/CaseHome.svelte');
  assert.match(home, /<EraseCase \{caseId\} archivePath=\{archiveFilePath\(currentFile\) \|\| undefined\}/);
});

test('the erase code accepts the short code with spaces or dashes in any case, and the whole id', () => {
  const cases = loadTypeScript('src/lib/cases.ts');
  assert.equal(cases.eraseConfirmed('k7m2qx4a', ID), true);
  assert.equal(cases.eraseConfirmed('K7M2 QX4A', ID), true);
  assert.equal(cases.eraseConfirmed('k7m2-qx4a', ID), true);
  assert.equal(cases.eraseConfirmed(ID, ID), true);
  assert.equal(cases.eraseConfirmed('k7m2qx4', ID), false);
  assert.equal(cases.eraseConfirmed('', ID), false);
});

test('a code for a legacy case resolves, and its start-list row has an Open button beside the label', async () => {
  const code = loadTypeScript('src/lib/caseCode.ts');
  const outcome = await code.resolveCode(ID, async () => { throw Object.assign(new Error('x'), { reason: 'untrusted-case' }); });
  assert.equal(outcome.ok, true);
  const start = read('src/components/StartScreen.svelte');
  assert.match(start, /\{#if row\.untrusted\}<span class="case-closed" role="note">\{row\.untrustedLabel\}<\/span>\{\/if\}/);
  assert.ok(!/\{:else if row\.untrusted\}/.test(start), 'the label no longer replaces the Open button');
});

// ---- the key screens

test('a refused key change during an archive says the archive must be cancelled first', () => {
  const key = loadTypeScript('src/lib/caseKey.ts');
  const copy = loadTypeScript('src/lib/copy/caseKeyCopy.ts');
  const failure = key.caseKeyFailureOf({ status: 409, reason: 'cancel-archive-first' });
  assert.equal(failure.kind, 'archiving');
  assert.match(copy.caseKeyErrorText(failure, 'current'), /archive of this case is running.*Cancel it first/);
});

test('the key page title is optional only when there is no key, and Remove has its own button label', () => {
  const copy = loadTypeScript('src/lib/copy/caseKeyCopy.ts');
  assert.equal(copy.CASE_KEY_TEXT.pageTitle, 'Case key (optional)');
  assert.equal(copy.CASE_KEY_TEXT.pageTitleHas, 'Case key');
  assert.notEqual(copy.CASE_KEY_TEXT.removeConfirm, copy.CASE_KEY_TEXT.saveChange);
  assert.match(copy.CASE_KEY_TEXT.oldFileNote, /old key/);
  const page = read('src/components/CaseKeyPage.svelte');
  assert.match(page, /const title = \$derived\(has \? CASE_KEY_TEXT\.pageTitleHas : CASE_KEY_TEXT\.pageTitle\)/);
  assert.match(page, /mode === 'remove' \? CASE_KEY_TEXT\.removeConfirm/);
  assert.match(page, /\{#if has\}<p class="note">\{CASE_KEY_TEXT\.oldFileNote\}<\/p>\{\/if\}/, 'the old-file note shows for Change and for Remove');
});

test('Escape leaves the Erase page, the key page and the locked card, unless a call is running', () => {
  for (const file of ['src/components/EraseCase.svelte', 'src/components/CaseKeyPage.svelte', 'src/components/CaseKeyCard.svelte']) {
    const src = read(file);
    assert.match(src, /<svelte:window onkeydown=\{escape\} \/>/, file);
    assert.match(src, /event\.key !== 'Escape' \|\| busy/, file + ' does nothing while busy');
    assert.match(src, /sectionEl\?\.contains\(document\.activeElement\)/, file + ' answers only when focus is inside');
    assert.match(src, /onCancel\(\)/, file);
  }
  assert.match(read('src/components/EraseCase.svelte'), /busy \|\| finished/, 'after the erase Escape does nothing');
});

test('the Erase card keeps one width in every state', () => {
  assert.match(read('src/components/EraseCase.svelte'), /\.erase \{[^}]*width: 100%; max-width: 560px/);
});

test('the locked card does not talk about locking before the case is open, and focus goes to the case name after the unlock', () => {
  const copy = loadTypeScript('src/lib/copy/caseKeyCopy.ts');
  assert.ok(!/Locking/.test(copy.CASE_KEY_TEXT.unlockIntro));
  assert.match(read('src/lib/components/CaseHome.svelte'), /onOpened=\{\(\) => void load\(caseId\)\.then\(\(\) => tick\(\)\)\.then\(\(\) => headingEl\?\.focus\(\)\)\}/);
});

test('the create wizard review step says whether the case has a key of its own', () => {
  const copy = loadTypeScript('src/lib/copy/wizardCreateCopy.ts');
  assert.match(copy.CREATE_TEXT.keyOn, /own key/);
  assert.match(copy.CREATE_TEXT.keyOff, /no key of its own/);
  const step = read('src/components/wizard/ReviewStep.svelte');
  assert.match(step, /wizard\.caseKey\.on \? CREATE_TEXT\.keyOn : CREATE_TEXT\.keyOff/);
  assert.ok(!/wizard\.caseKey\.key/.test(step), 'the key itself is never shown on the review step');
});

test('the damaged-key screen says "Its working copy" for one archived case', () => {
  const copy = loadTypeScript('src/lib/copy/masterKeyCopy.ts');
  assert.equal(copy.freedArchivesWarning(1), '1 archived case can only be opened with the old key. Its working copy was freed.');
  assert.equal(copy.freedArchivesWarning(3), '3 archived cases can only be opened with the old key. Their working copies were freed.');
});
