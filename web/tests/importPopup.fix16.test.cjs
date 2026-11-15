// FIX-16 / UI-12: the "Import files" popup state.
//  (a) "Add all servers" must stop adding computers once the popup is disposed or its session is cleared;
//  (b) an add that fails (other than "taken") is told to the person in plain words, not skipped silently;
//  (c) Esc / x / scrim while an import runs show a short plain reason instead of doing nothing.
// BEHAVIOUR tests: they drive the real createPopupState with a fake PopupApi (same harness as importPopup.requestClose.test.cjs).
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

const { createPopupState } = load('src/lib/components/importPopup/popupState.svelte.ts');
const { POPUP_TEXT } = load('src/lib/copy/importPopupCopy.ts');

const flush = async () => { for (let i = 0; i < 8; i++) await new Promise((resolve) => setImmediate(resolve)); };

// Every file names its own computer (file "web-a.log" names computer "web-a"), so each is a server card that "Add all" can add.
const needsComputer = (f) => ({ key: f.key, name: f.file.name, size: f.file.size, verdict: 'needs-computer', importer: 'iis-w3c', tied: [], serverName: f.file.name.replace(/\.log$/, ''), needsZone: false, reason: '', rows: 0, emitted: 0, rejected: 0 });
const readyVerdict = (f) => ({ ...needsComputer(f), verdict: 'ready', serverName: '' });

// A fake PopupApi. `adds` records every addComputer call; each call waits for the matching gate so a test can
// dispose or clear the popup while an add is still in flight, then release it.
function serverApi(answers) {
  const adds = [];
  const gates = [];
  const api = {
    async check(caseId, files) { return { checkedAt: '', files: files.map(needsComputer) }; },
    runner() { return () => new Promise(() => {}); },
    addComputer(label) {
      adds.push(label);
      return new Promise((resolve) => gates.push(() => resolve(answers(label))));
    }
  };
  return { api, adds, release: (n = 0) => gates[n]?.() };
}

async function twoServerCards(api) {
  const popup = createPopupState(api);
  await popup.addFiles([new File(['x'], 'web-a.log'), new File(['x'], 'web-b.log')]);
  popup.continueToCheck();
  await flush();
  assert.equal(popup.report.files.length, 2, 'two files checked');
  assert.deepEqual(popup.cardIds(), ['web-a', 'web-b'], 'two server cards are open');
  return popup;
}

test('UI-12a: addAllServers adds no more computers once the popup is disposed', async () => {
  const { api, adds, release } = serverApi((label) => ({ ok: true, label }));
  const popup = await twoServerCards(api);

  popup.addAllServers();
  await flush();
  assert.deepEqual(adds, ['web-a'], 'the first add is in flight');

  popup.dispose();
  release(0);
  await flush();
  assert.deepEqual(adds, ['web-a'], 'no second computer is added after dispose');
  assert.equal(popup.cards['web-b'], undefined, 'the second card was not touched');
});

test('UI-12a: addAllServers adds no more computers once the session is cleared (Import more / Discard)', async () => {
  const { api, adds, release } = serverApi((label) => ({ ok: true, label }));
  const popup = await twoServerCards(api);

  popup.addAllServers();
  await flush();
  assert.deepEqual(adds, ['web-a']);

  popup.importMore(); // clearSession
  release(0);
  await flush();
  assert.deepEqual(adds, ['web-a'], 'no second computer is added after the session was cleared');
  assert.deepEqual(popup.cards, {}, 'the cleared session got no resolved card back');
  assert.equal(popup.focus, null, 'no focus request is left from the abandoned add');
  popup.dispose();
});

test('UI-12a: control - an untouched popup adds every server card', async () => {
  const { api, adds, release } = serverApi((label) => ({ ok: true, label }));
  const popup = await twoServerCards(api);

  popup.addAllServers();
  await flush(); release(0);
  await flush(); release(1);
  await flush();
  assert.deepEqual(adds, ['web-a', 'web-b']);
  assert.equal(popup.cards['web-a'], 'resolved');
  assert.equal(popup.cards['web-b'], 'resolved');
  assert.deepEqual(popup.addNotes, [], 'nothing failed, so nothing is noted');
  popup.dispose();
});

test('UI-12b: a failed add is noted in plain words, the other servers still go in, and a taken name is not a failure', async () => {
  const outcomes = { 'web-a': { ok: false, reason: 'failed' }, 'web-b': { ok: false, reason: 'taken' }, 'web-c': { ok: true, label: 'web-c' } };
  const { api, adds, release } = serverApi((label) => outcomes[label]);
  const popup = createPopupState(api);
  await popup.addFiles([new File(['x'], 'web-a.log'), new File(['x'], 'web-b.log'), new File(['x'], 'web-c.log')]);
  popup.continueToCheck();
  await flush();

  popup.addAllServers();
  for (let i = 0; i < 3; i++) { await flush(); release(i); }
  await flush();

  assert.deepEqual(adds, ['web-a', 'web-b', 'web-c']);
  assert.equal(popup.cards['web-a'], undefined, 'the failed computer stays an open card');
  assert.equal(popup.cards['web-b'], 'resolved', 'a name already in the list is the goal reached');
  assert.equal(popup.cards['web-c'], 'resolved');
  assert.equal(popup.addNotes.length, 1, 'exactly the failed add is noted');
  assert.equal(popup.addNotes[0], POPUP_TEXT.addFailed('web-a', 'failed'));
  assert.match(popup.addNotes[0], /web-a/, 'the note names the computer');
  assert.doesNotMatch(popup.addNotes[0], /undefined|\[object|failed'|reason/, 'plain words only');

  // A fresh Add all starts from a clean slate: the old notes do not pile up.
  popup.addAllServers();
  await flush(); release(3);
  await flush();
  assert.equal(popup.addNotes.length, 1, 'notes are replaced on each run, not appended');
  popup.dispose();
});

test('UI-12b: every failure reason has plain words', () => {
  for (const reason of ['label', 'zone', 'locked', 'failed']) {
    const text = POPUP_TEXT.addFailed('web-a', reason);
    assert.match(text, /web-a/);
    assert.doesNotMatch(text, /undefined|\[object/, 'plain words only');
    assert.ok(text.length > 12 && text.length < 200, reason + ' length ' + text.length);
  }
});

async function inflightImport() {
  let release;
  const inFlight = new Promise((resolve) => { release = resolve; });
  const api = {
    async check(caseId, files) { return { checkedAt: '', files: files.map(readyVerdict) }; },
    runner() { return () => inFlight; },
    async addComputer() { return { ok: true, label: 'x' }; }
  };
  const popup = createPopupState(api);
  await popup.addFiles([new File(['x'], 'web-a.log')]);
  popup.continueToCheck();
  await flush();
  popup.ack = true;
  popup.startImport();
  await flush();
  assert.equal(popup.importing, true);
  return { popup, finish: (answer) => release(answer) };
}

test('UI-12c: Esc / x / scrim while importing show a plain reason and leave the popup open', async () => {
  const { popup, finish } = await inflightImport();
  assert.equal(popup.closeNote, '', 'no note before anyone tries to close');

  popup.requestClose();
  assert.equal(popup.closed, false, 'the popup stays open');
  assert.equal(popup.closeNote, POPUP_TEXT.closeWhileImporting);
  assert.equal(popup.closeNote, 'An import is running. Press Cancel first.', 'names the visible Cancel button');

  finish({ importer: 'iis-w3c', score: 1, tied: [], report: null, record: { id: 1, importer: 'iis-w3c', fileName: 'web-a.log', sha256: '', bytes: 1, rows: 5, rejected: 0, hostLabel: '', tzRule: '', quality: {}, knownGaps: [], imported: '', status: 'ok', supplied: {} } });
  await flush();
  assert.equal(popup.closeNote, '', 'the note goes away once the import has ended');
  popup.dispose();
});

test('UI-12c: while stopping the reason is shown too, and Cancel clears a stale note', async () => {
  const { popup, finish } = await inflightImport();
  popup.requestClose();
  assert.equal(popup.closeNote, POPUP_TEXT.closeWhileImporting);

  popup.cancelImport(); // the file being sent is still finishing: stopping
  assert.equal(popup.stopping, true);
  popup.requestClose();
  assert.equal(popup.closed, false);
  assert.notEqual(popup.closeNote, '', 'a reason is shown while stopping');
  assert.doesNotMatch(popup.closeNote, /Press Cancel/, 'it no longer tells the person to press Cancel: they already did');

  finish(undefined);
  await flush();
  assert.equal(popup.closeNote, '');
  popup.dispose();
});

test('UI-12c: control - with nothing running, closing needs no reason', () => {
  const popup = createPopupState({ check: async () => ({ files: [], checkedAt: '' }), runner: () => async () => ({}), addComputer: async () => ({ ok: true, label: 'x' }) });
  popup.requestClose();
  assert.equal(popup.closed, true);
  assert.equal(popup.closeNote, '');
  popup.dispose();
});
