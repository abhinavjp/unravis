// FIX-35 / TEST-04 (d): the import popup's awkward paths, driven through the real createPopupState with a fake PopupApi:
//   - Cancel, then close, then the file settles, then close again (with one file left over);
//   - a failed read (the check fails, then works on a retry; a failure the person already moved on from is dropped);
//   - a failed import of one file (the runner fails: the Result step says so, nothing is lost);
//   - empty input (nothing chosen, a zero-byte file, a server answer that leaves a file out).
// No browser, no network, no timers: the harness transpiles the .svelte.ts with a $state identity shim, like
// importPopup.requestClose.test.cjs. Synthetic file names and bytes only.
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
const { CaseApiError } = load('src/lib/cases.ts');
const { POPUP_TEXT } = load('src/lib/copy/importPopupCopy.ts');

const flush = async () => { for (let i = 0; i < 8; i++) await new Promise((resolve) => setImmediate(resolve)); };
const IMPORTER = 'iis-w3c';

const usedAnswer = (fileName) => ({
  importer: IMPORTER, score: 1, tied: [], report: null,
  record: { id: 1, importer: IMPORTER, fileName, sha256: '', bytes: 9, rows: 5, rejected: 0, hostLabel: '', tzRule: '', quality: {}, knownGaps: [], imported: '', status: 'ok', supplied: {} }
});
const verdictFor = (f) => ({ key: f.key, name: f.file.name, size: f.file.size, verdict: 'ready', importer: IMPORTER, tied: [], serverName: '', needsZone: false, reason: '', rows: 0, emitted: 0, rejected: 0 });

// A fake api. `check` answers every file ready unless `checkImpl` is given; `runner` hands each sent file a deferred the test settles.
function fakeApi(options = {}) {
  const sent = []; // { name, settle, fail }
  const checks = []; // the key lists each check was asked about
  const api = {
    async check(caseId, files, signal) {
      checks.push(files.map((f) => f.file.name));
      if (options.checkImpl) return options.checkImpl(files, signal, checks.length);
      return { checkedAt: '', files: files.map(verdictFor) };
    },
    runner() {
      return (item) => new Promise((resolve, reject) => {
        sent.push({ name: item.file.name, settle: (answer) => resolve(answer), fail: (error) => reject(error) });
      });
    },
    async addComputer() { return { ok: true }; }
  };
  return { api, sent, checks };
}

const file = (name, text = 'synthetic') => new File([text], name);

async function checkedPopup(api, names) {
  const popup = createPopupState(api);
  await popup.addFiles(names.map((n) => file(n)));
  popup.continueToCheck();
  await flush();
  return popup;
}
async function startedPopup(api, names) {
  const popup = await checkedPopup(api, names);
  popup.ack = true;
  assert.equal(popup.canImport(), true, 'ready to import');
  popup.startImport();
  await flush();
  return popup;
}

// ---- Cancel, then close ---------------------------------------------------------------------------------------------------

test('Cancel with two files: the second is never sent; closing waits with a plain note, then asks before dropping the leftover file', async () => {
  const { api, sent } = fakeApi();
  const popup = await startedPopup(api, ['u_ex261019.log', 'u_ex261020.log']);
  assert.equal(popup.importing, true);
  assert.equal(sent.length, 1, 'files go one at a time: only the first is in flight');

  popup.cancelImport();
  assert.equal(popup.stopping, true);
  assert.equal(popup.step, 2, 'back on Choose');

  popup.requestClose();
  assert.equal(popup.closed, false, 'x while stopping does not close');
  assert.equal(popup.discard, false, 'and does not offer to discard the file still being sent');
  assert.equal(popup.closeNote, POPUP_TEXT.closeWhileStopping, 'the person is told why, in the popup copy');

  sent[0].settle(usedAnswer(sent[0].name));
  await flush();
  assert.equal(sent.length, 1, 'the second file was never started');
  assert.equal(popup.stopping, false);
  assert.equal(popup.closeNote, '', 'the note goes away once the file has settled');
  assert.equal(popup.importProgress, 1, 'the first file outcome was kept');
  assert.equal(popup.listedFiles(), 1, 'the unsent second file is still listed');

  // The leftover file is still the person's: closing asks first, Keep returns, Discard closes and clears.
  popup.requestClose();
  assert.equal(popup.closed, false);
  assert.equal(popup.discard, true, 'asks before dropping the unsent file');
  popup.keepFiles();
  assert.equal(popup.discard, false);
  assert.equal(popup.closed, false);
  popup.requestClose();
  popup.discardNow();
  assert.equal(popup.closed, true);
  assert.equal(popup.listedFiles(), 0, 'discard clears the list');
  popup.dispose();
});

test('Cancel with a single file: after it settles the list is empty and the next close needs no question', async () => {
  const { api, sent } = fakeApi();
  const popup = await startedPopup(api, ['u_ex261019.log']);
  popup.cancelImport();
  popup.requestClose();
  assert.equal(popup.closed, false);
  sent[0].settle(usedAnswer(sent[0].name));
  await flush();
  assert.equal(popup.step, 1, 'nothing left to choose, so it goes back to the first step');
  popup.requestClose();
  assert.equal(popup.discard, false, 'nothing to lose, so no question');
  assert.equal(popup.closed, true);
  popup.dispose();
});

test('a file that settles after the session was cleared (Discard) is not counted on the cleared popup', async () => {
  const { api, sent } = fakeApi();
  const popup = await startedPopup(api, ['u_ex261019.log', 'u_ex261020.log']);
  popup.cancelImport();
  popup.discardNow(); // defensive: the shell only offers Discard once nothing is in flight, but the state must hold if it is called anyway
  assert.equal(popup.closed, true);
  sent[0].settle(usedAnswer(sent[0].name));
  await flush();
  assert.equal(popup.importProgress, 0, 'the late outcome is not counted');
  assert.equal(popup.result, null, 'no Result step is built for a cleared session');
  assert.equal(popup.step, 1);
  popup.dispose();
});

// ---- A failed read ------------------------------------------------------------------------------------------------------

test('a failed check says why in plain words, offers no import, and a second try works', async () => {
  let failFirst = true;
  const { api, checks } = fakeApi({
    checkImpl: async (files) => {
      if (failFirst) { failFirst = false; throw new CaseApiError('boom', 503, 'unreachable'); }
      return { checkedAt: '', files: files.map(verdictFor) };
    }
  });
  const popup = await checkedPopup(api, ['u_ex261019.log']);
  assert.equal(popup.checking, false, 'the spinner stops');
  assert.equal(popup.report, null, 'no report from a failed read');
  assert.notEqual(popup.checkError, '', 'a reason is shown');
  assert.doesNotMatch(popup.checkError, /boom|503|CaseApiError/, 'the reason is plain words, not the raw error');
  popup.ack = true;
  assert.equal(popup.canImport(), false, 'cannot import what was not read');
  popup.startImport();
  await flush();
  assert.equal(popup.importing, false);

  // Try again: back to Choose, then Continue reads the same file again.
  popup.goTo(1);
  popup.continueToCheck();
  await flush();
  assert.equal(checks.length, 2, 'the file was read a second time');
  assert.equal(popup.checkError, '', 'the old reason is cleared');
  assert.equal(popup.report.files.length, 1);
  popup.dispose();
});

test('a read that fails with something that is not an API error still gives a plain reason', async () => {
  const { api } = fakeApi({ checkImpl: async () => { throw new TypeError('network down'); } });
  const popup = await checkedPopup(api, ['u_ex261019.log']);
  assert.notEqual(popup.checkError, '');
  assert.doesNotMatch(popup.checkError, /network down|TypeError/);
  popup.dispose();
});

test('a check that fails after the person went back is dropped: no stale error on the first step', async () => {
  let rejectCheck;
  const { api } = fakeApi({ checkImpl: () => new Promise((_, reject) => { rejectCheck = reject; }) });
  const popup = createPopupState(api);
  await popup.addFiles([file('u_ex261019.log')]);
  popup.continueToCheck();
  await flush();
  assert.equal(popup.checking, true);
  popup.goTo(1);
  rejectCheck(new CaseApiError('late', 503, 'unreachable'));
  await flush();
  assert.equal(popup.checkError, '', 'the person had moved on, so the late failure says nothing');
  assert.equal(popup.step, 1);
  popup.dispose();
});

test('one file fails while sending: the Result step names it with a reason and the other file still counts', async () => {
  const { api, sent } = fakeApi();
  const popup = await startedPopup(api, ['u_ex261019.log', 'u_ex261020.log']);
  sent[0].fail(new CaseApiError('bad', 422, 'no-rows'));
  await flush();
  assert.equal(sent.length, 2, 'the failure of the first does not stop the second');
  sent[1].settle(usedAnswer(sent[1].name));
  await flush();
  assert.equal(popup.step, 3);
  assert.equal(popup.importDone, true);
  assert.equal(popup.result.notImported.length, 1);
  assert.equal(popup.result.notImported[0].name, 'u_ex261019.log');
  assert.notEqual(popup.result.notImported[0].reason, '');
  assert.equal(popup.result.imported.reduce((n, g) => n + g.count, 0), 1, 'the good file is counted');
  popup.dispose();
});

// ---- Empty input --------------------------------------------------------------------------------------------------------

test('Continue with nothing chosen does nothing: no check, still on the first step', async () => {
  const { api, checks } = fakeApi();
  const popup = createPopupState(api);
  popup.continueToCheck();
  await flush();
  assert.equal(popup.step, 1);
  assert.equal(popup.checking, false);
  assert.equal(checks.length, 0);
  assert.equal(popup.canImport(), false);
  popup.startImport();
  assert.equal(popup.importing, false);
  popup.dispose();
});

test('a zero-byte file is never sent to the server: it is listed as not supported and nothing can be imported', async () => {
  const { api, sent, checks } = fakeApi();
  const popup = createPopupState(api);
  await popup.addFiles([new File([], 'u_ex261019.log')]);
  assert.equal(popup.listedFiles(), 1);
  popup.continueToCheck();
  await flush();
  assert.equal(checks.length, 0, 'an empty file is not sent to the check');
  assert.equal(popup.checking, false);
  assert.equal(popup.report.files.length, 1);
  assert.equal(popup.report.files[0].verdict, 'not-supported');
  assert.equal(popup.report.files[0].reason, 'no-rows');
  popup.ack = true;
  assert.equal(popup.canImport(), false, 'nothing importable, even with the notice ticked');
  popup.startImport();
  await flush();
  assert.equal(sent.length, 0);
  assert.equal(popup.importing, false);
  popup.dispose();
});

test('a file the server leaves out of its answer is listed as not supported, and the other file can still be imported', async () => {
  const { api, sent } = fakeApi({ checkImpl: async (files) => ({ checkedAt: '', files: files.slice(0, 1).map(verdictFor) }) });
  const popup = await checkedPopup(api, ['u_ex261019.log', 'u_ex261020.log']);
  assert.equal(popup.report.files.length, 2, 'one verdict per listed file');
  assert.deepEqual(popup.report.files.map((v) => v.verdict), ['ready', 'not-supported']);
  popup.ack = true;
  assert.equal(popup.targetCount(), 1);
  popup.startImport();
  await flush();
  assert.equal(sent.length, 1);
  assert.equal(sent[0].name, 'u_ex261019.log');
  sent[0].settle(usedAnswer(sent[0].name));
  await flush();
  assert.equal(popup.result.notImported.some((n) => n.name === 'u_ex261020.log'), true, 'the left-out file is named on the Result step');
  popup.dispose();
});

test('the same file chosen twice is listed once', async () => {
  const { api, checks } = fakeApi();
  const popup = createPopupState(api);
  const f = file('u_ex261019.log');
  await popup.addFiles([f]);
  await popup.addFiles([f]);
  assert.equal(popup.listedFiles(), 1);
  popup.continueToCheck();
  await flush();
  assert.deepEqual(checks[0], ['u_ex261019.log']);
  popup.dispose();
});
