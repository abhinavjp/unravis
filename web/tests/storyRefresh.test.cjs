// BL-162 / BL-164 leftover: "Story so far" on Case home reads the case again after any successful add (files through the Add files
// pop-up, and the Add data run for Mongo, SQL, Redis, Blackbox and what was found on this PC), once per success, and never for a failure.
// These drive the real popup state and the real run with fake server calls, and count the signal the page watches (`caseAdded.seq`).
// The page's own wiring (one quiet re-read per signal, no loading screen, no focus move) is pinned against the Svelte source.
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
// CRLF -> LF at the read point: a Windows checkout has CRLF, and the patterns below use \n.
const read = (relative) => fs.readFileSync(path.join(root, relative), 'utf8').replace(/\r\n/g, '\n');

const { caseAdded, noteCaseAdded, landedCount } = load('src/lib/caseAdded.svelte.ts');
const { createPopupState } = load('src/lib/components/importPopup/popupState.svelte.ts');
const { CaseApiError } = load('src/lib/cases.ts');
const W = load('src/lib/wizard.svelte.ts');
const C = load('src/lib/wizardCreate.svelte.ts');
const src = load('src/lib/wizardSources.ts');

const flush = async () => { for (let i = 0; i < 8; i++) await new Promise((resolve) => setImmediate(resolve)); };

test('the signal counts each add once and ignores a missing case', () => {
  const before = caseAdded.seq;
  noteCaseAdded('');
  assert.equal(caseAdded.seq, before);
  noteCaseAdded('c1');
  assert.equal(caseAdded.seq, before + 1);
  assert.equal(caseAdded.caseId, 'c1');
});

test('landedCount counts only steps that went through', () => {
  const state = { tags: { a: { status: 'done' }, b: { status: 'failed' } }, audit: { c: { status: 'done' } }, imports: { d: { status: 'running' }, e: { status: 'done' } } };
  assert.equal(landedCount(state), 3);
});

// ---- the Add files pop-up
const record = (status) => ({ id: 1, importer: 'iis-w3c', fileName: 'u_ex1.log', sha256: '', bytes: 9, rows: 5, rejected: 0, hostLabel: '', tzRule: '', quality: {}, knownGaps: [], imported: '', status, supplied: {} });
function popupApi(answer) {
  return {
    async check(caseId, files) {
      return { checkedAt: '', files: files.map((f) => ({ key: f.key, name: f.file.name, size: f.file.size, verdict: 'ready', importer: 'iis-w3c', tied: [], serverName: '', needsZone: false, reason: '', rows: 0, emitted: 0, rejected: 0 })) };
    },
    runner() { return async () => { if (answer instanceof Error) throw answer; return answer; }; },
    async addComputer() { return { ok: true }; }
  };
}
async function importOne(answer) {
  const popup = createPopupState(popupApi(answer));
  popup.caseId = 'case-1';
  await popup.addFiles([new File(['synthetic'], 'u_ex1.log')]);
  popup.continueToCheck();
  await flush();
  popup.ack = true;
  popup.startImport();
  await flush();
  return popup;
}

test('a file the case kept makes one story re-read, and only one', async () => {
  const before = caseAdded.seq;
  const popup = await importOne({ importer: 'iis-w3c', score: 1, tied: [], report: null, record: record('ok') });
  assert.equal(popup.importDone, true);
  assert.equal(caseAdded.seq, before + 1);
  assert.equal(caseAdded.caseId, 'case-1');
  popup.dispose();
});

test('a file the case did not keep makes no re-read', async () => {
  const before = caseAdded.seq;
  const refused = await importOne({ importer: 'iis-w3c', score: 1, tied: [], report: null, record: record('failed') });
  assert.equal(refused.importDone, true);
  refused.dispose();
  const failed = await importOne(new CaseApiError('bad', 422, 'import-failed'));
  failed.dispose();
  assert.equal(caseAdded.seq, before);
});

// ---- the Add data run (Mongo, SQL, Redis, Blackbox, found on this PC)
function runApi(over = {}) {
  const none = async () => { throw new Error('unexpected call'); };
  return { createCase: none, putEnvironments: async () => [], postSourceTag: async () => ({}), testConnection: none, addAuditSource: async () => ({}), saveRetries: async () => ({}), startImports: none, watchJob: none, importFile: none, ...over };
}
function addingDraft(...sources) {
  W.resetWizard();
  W.setSources(sources.map((s) => src.makeSource(s.kind, s.ref, s.label)));
  C.createView.appendOnly = true;
  C.createView.submit.caseId = 'case-2';
  C.setAck(true);
}

test('a source that was added to an existing case makes one story re-read', async () => {
  addingDraft({ kind: 'redis', ref: '10.0.0.1:6379', label: 'Redis A' });
  const before = caseAdded.seq;
  await C.createNow(runApi());
  assert.equal(caseAdded.seq, before + 1);
  assert.equal(caseAdded.caseId, 'case-2');
  // The same run pressed again changes nothing, so nothing is read again.
  await C.createNow(runApi());
  assert.equal(caseAdded.seq, before + 1);
});

test('a source that could not be added makes no story re-read', async () => {
  addingDraft({ kind: 'redis', ref: '10.0.0.1:6379', label: 'Redis A' });
  const before = caseAdded.seq;
  const result = await C.createNow(runApi({ postSourceTag: async () => { throw new CaseApiError('no', 500, 'failed'); } }));
  assert.ok(result.failures.length > 0);
  assert.equal(caseAdded.seq, before);
});

test('the run of a new case (not an existing one) signals nothing', async () => {
  W.resetWizard();
  W.setSources([src.makeSource('redis', '10.0.0.1:6379', 'Redis A')]);
  C.createView.appendOnly = false;
  C.createView.submit.caseId = 'case-3';
  const before = caseAdded.seq;
  await C.createNow(runApi());
  assert.equal(caseAdded.seq, before);
});

// ---- the page
test('Case home reads the case again quietly for each signal, with no loading screen and no focus move', () => {
  const home = read('src/lib/components/CaseHome.svelte');
  const reread = home.match(/async function rereadFiles\(everything = false\) \{[\s\S]*?\n  \}\n/)[0];
  assert.doesNotMatch(reread, /phase\s*=|focus\(|load\(/, 'the quiet read never shows the loading text or moves focus');
  assert.match(reread, /mine !== rereadRun/, 'an older read never overwrites a newer one');
  const loadPage = home.match(/async function loadPage\(id: string, quiet: boolean\) \{[\s\S]*?controller\?\.abort\(\);/)[0];
  assert.match(loadPage, /rereadRun \+= 1;/, 'a load that starts after a re-read outranks it');
  // The other way round: an add that lands while a read of the page is on its way waits for it, then reads once, so the read that started last is the one that shows.
  const loadWrap = home.match(/async function load\(id: string, quiet = false\) \{[\s\S]*?\n  \}\n/)[0];
  assert.match(loadWrap, /finally \{[\s\S]*loadsOpen === 0 && rereadWanted[\s\S]*void rereadFiles\(\)/, 'the re-read waits for the open read, then runs once');
  assert.match(loadWrap, /!destroyed && wanted === caseId/, 'a held re-read is not run for a page that is gone');
  assert.match(home, /Promise\.race\(\[refreshLedger\(\), aborted\]\)/, 'an aborted load stops waiting for the theories read');
  assert.match(home, /if \(loadsOpen > 0\) rereadWanted = id; else void untrack\(\(\) => rereadFiles\(\)\);/, 'a signal during a read is kept, not dropped, and not run beside it');
  assert.match(home, /\$effect\(\(\) => \{\s*const seq = caseAdded\.seq;\s*if \(seq === seenAdd\) return;\s*seenAdd = seq;/, 'one read for each signal');
  assert.doesNotMatch(home, /drawerWas/, 'the read no longer waits for the pop-up to close');
});

test('the add paths signal once, after the case kept something', () => {
  assert.match(read('src/lib/components/importPopup/popupState.svelte.ts'), /outcome\.verdict !== 'rejected'\)\) noteCaseAdded\(s\.caseId\)/);
  assert.match(read('src/lib/wizardCreate.svelte.ts'), /createView\.appendOnly && result\.caseId && landedCount\(createView\.submit\) > landedBefore\) noteCaseAdded/);
});
