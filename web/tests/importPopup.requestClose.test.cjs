// UI-06: the "Import files" popup must not close (and drop an in-flight file's outcome) while a file is still
// importing OR stopping. cancelImport sets stopping=true and importing=false (the file being sent is still
// finishing), so requestClose has to wait for both, not just importing.
//
// These are BEHAVIOUR tests, not source-text checks: they drive the real createPopupState through
// addFiles -> continueToCheck -> startImport -> cancelImport -> requestClose with a fake PopupApi whose runner
// stays in flight until released, then assert on the popup's observable state. The harness mirrors
// tests/p2.cases.test.cjs: transpile the .svelte.ts with a $state identity shim so it runs under node --test.
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

// Let the popup's async check/import promise chains settle. No real timers are involved: the fake runner
// resolves on demand and a clean answer never hits the queue's busy-retry sleep.
const flush = async () => { for (let i = 0; i < 8; i++) await new Promise((resolve) => setImmediate(resolve)); };

const FILE_NAME = 'u_ex261019.log';
const IMPORTER = 'iis-w3c';

// A successful server answer for one file: classifyAnswer reads it as 'used' (status ok, rows > 0, none rejected).
const usedAnswer = () => ({
  importer: IMPORTER, score: 1, tied: [], report: null,
  record: { id: 1, importer: IMPORTER, fileName: FILE_NAME, sha256: '', bytes: 9, rows: 5, rejected: 0, hostLabel: '', tzRule: '', quality: {}, knownGaps: [], imported: '', status: 'ok', supplied: {} }
});

// A fake PopupApi: check answers one 'ready' verdict per file; the runner stays in flight until finish() is
// called, so the import is genuinely mid-send when Cancel is pressed. No network, no timers, no real data.
function fakeApi() {
  let release;
  const inFlight = new Promise((resolve) => { release = resolve; });
  const api = {
    async check(caseId, files) {
      return { checkedAt: '', files: files.map((f) => ({ key: f.key, name: f.file.name, size: f.file.size, verdict: 'ready', importer: IMPORTER, tied: [], serverName: '', needsZone: false, reason: '', rows: 0, emitted: 0, rejected: 0 })) };
    },
    runner() { return () => inFlight; },
    async addComputer() { return { ok: true }; }
  };
  return { api, finish: (answer) => release(answer) };
}

// Drive the real popup to an in-flight import: stage a file, check it, acknowledge, start. After this,
// importing is true and one file is being sent (the runner promise is still pending).
async function inflightImport() {
  const { api, finish } = fakeApi();
  const popup = createPopupState(api);
  await popup.addFiles([new File(['synthetic'], FILE_NAME)]);
  popup.continueToCheck();
  await flush();
  assert.equal(popup.checking, false, 'the check settled');
  assert.equal(popup.report.files.length, 1, 'one ready file');
  popup.ack = true;
  assert.equal(popup.canImport(), true, 'ready to import');
  popup.startImport();
  await flush();
  assert.equal(popup.importing, true, 'the import is running');
  assert.equal(popup.step, 3, 'on the Importing step');
  assert.equal(popup.listedFiles(), 1, 'the file is still being sent');
  return { popup, finish };
}

test('UI-06: requestClose is a no-op while stopping — Cancel then x keeps the in-flight file outcome', async () => {
  const { popup, finish } = await inflightImport();

  // Cancel: the file being sent is still finishing, so stopping is true and importing is false.
  popup.cancelImport();
  assert.equal(popup.stopping, true, 'Cancel with a file in flight sets stopping');
  assert.equal(popup.importing, false, 'Cancel clears importing');
  assert.equal(popup.step, 2, 'Cancel returns to Choose');
  assert.equal(popup.listedFiles(), 1, 'the in-flight file is still listed');

  // x (requestClose) while stopping must NOT close or open the discard confirmation: the outcome is preserved.
  // Without the stopping guard this sets discard=true (and a later Discard would drop the file's outcome).
  popup.requestClose();
  assert.equal(popup.closed, false, 'the popup did not close while stopping');
  assert.equal(popup.discard, false, 'no discard confirmation: the in-flight file outcome is not dropped');
  assert.equal(popup.stopping, true, 'still stopping');
  assert.equal(popup.listedFiles(), 1, 'the file is still there');

  // Let the in-flight file finish: because the popup stayed open, its outcome is recorded, not lost.
  finish(usedAnswer());
  await flush();
  assert.equal(popup.importProgress, 1, 'the file outcome was counted');
  assert.equal(popup.importGroups[0].done, 1, 'the group shows the file finished');
  assert.equal(popup.listedFiles(), 0, 'the settled file left the list');
  assert.equal(popup.stopping, false, 'stopping cleared once the file settled');
  assert.equal(popup.closed, false, 'the popup never closed on its own');
  popup.dispose();
});

test('UI-06: requestClose is a no-op while importing (the existing guard is kept)', async () => {
  const { popup, finish } = await inflightImport();
  assert.equal(popup.importing, true);
  assert.equal(popup.stopping, false);

  popup.requestClose();
  assert.equal(popup.closed, false, 'the popup did not close while importing');
  assert.equal(popup.discard, false, 'no discard confirmation while importing');

  // Release and dispose so nothing is left pending.
  finish(usedAnswer());
  await flush();
  popup.dispose();
});

test('UI-06: requestClose closes when neither importing nor stopping (control)', async () => {
  const { api } = fakeApi();
  const popup = createPopupState(api);
  assert.equal(popup.importing, false);
  assert.equal(popup.stopping, false);
  assert.equal(popup.listedFiles(), 0);

  popup.requestClose();
  assert.equal(popup.closed, true, 'with nothing in flight the popup closes');
  assert.equal(popup.discard, false);
  popup.dispose();
});
