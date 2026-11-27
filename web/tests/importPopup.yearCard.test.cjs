// A log whose lines carry no year (Redis writes day and month only): the popup asks for the year on its own card.
//  (a) the check answer's `needsYear` is parsed; (b) a year card sits beside a computer card; (c) it blocks Import until answered;
//  (d) answering does NOT check again; (e) the year goes out with the file as supplied.year; (f) leave out and undo; (g) the year rules.
// BEHAVIOUR tests: they drive the real createPopupState with a fake PopupApi (same harness as importPopup.fix16.test.cjs).
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

const { createPopupState, yearProblem, yearCardOf, YEAR_CARD } = load('src/lib/components/importPopup/popupState.svelte.ts');
const { toCheckFile } = (() => { try { return load('src/lib/cases.ts'); } catch { return {}; } })();
const { POPUP_TEXT } = load('src/lib/copy/importPopupCopy.ts');

const flush = async () => { for (let i = 0; i < 8; i++) await new Promise((resolve) => setImmediate(resolve)); };

const base = (f) => ({ key: f.key, name: f.file.name, size: f.file.size, importer: 'redis-log', tied: [], serverName: '', needsZone: false, needsYear: false, reason: '', rows: 0, emitted: 0, rejected: 0 });
// The server's answer for a Redis log: the year is missing, and the file may also lack a computer (it then sits on both cards).
const yearless = (f, withComputer) => ({ ...base(f), verdict: withComputer ? 'needs-computer' : 'needs-year', serverName: withComputer ? 'redis-box' : '', needsYear: true, reason: withComputer ? '' : 'needs-year' });

function api(answer) {
  const checks = [];
  const sent = [];
  return {
    checks, sent,
    api: {
      async check(caseId, files) { checks.push(files.length); return { checkedAt: '', files: files.map(answer) }; },
      runner() { return (item) => { sent.push(item); return Promise.resolve({ ok: true }); }; },
      async addComputer(label) { return { ok: true, label }; }
    }
  };
}

async function staged(harness) {
  const popup = createPopupState(harness.api);
  await popup.addFiles([new File(['x'], 'server_log.txt')]);
  popup.continueToCheck();
  await flush();
  return popup;
}

test('year rules: four digits, 2000 to 2100', () => {
  for (const good of ['2000', '2026', '2100']) assert.equal(yearProblem(good), false, good);
  for (const bad of ['', '26', '20266', '1999', '2101', 'abcd', '20 6']) assert.equal(yearProblem(bad), true, JSON.stringify(bad));
});

test('a verdict that needs a year names its card; a ready one has none', () => {
  assert.equal(yearCardOf({ importer: 'redis-log', verdict: 'needs-year', needsYear: false }), YEAR_CARD + 'redis-log');
  assert.equal(yearCardOf({ importer: 'redis-log', verdict: 'needs-computer', needsYear: true }), YEAR_CARD + 'redis-log');
  assert.equal(yearCardOf({ importer: 'redis-log', verdict: 'ready', needsYear: false }), null);
});

test('a log that needs only a year opens one year card, and the check is not asked again when it is answered', async () => {
  const h = api((f) => yearless(f, false));
  const popup = await staged(h);
  assert.deepEqual(popup.cardIds(), [YEAR_CARD + 'redis-log']);
  assert.equal(popup.fixesNeeded(), 1);
  popup.ack = true;
  assert.equal(popup.canImport(), false, 'an open year card blocks the import');
  const checksBefore = h.checks.length;
  popup.resolveCard(YEAR_CARD + 'redis-log', '2026');
  await flush();
  assert.equal(h.checks.length, checksBefore, 'answering the year does not check the files again');
  assert.equal(popup.cards[YEAR_CARD + 'redis-log'], 'resolved');
  assert.equal(popup.years[YEAR_CARD + 'redis-log'], '2026');
  assert.equal(popup.fixesNeeded(), 0);
  assert.equal(popup.canImport(), true);
  popup.dispose();
});

test('the year goes out with the file as supplied.year', async () => {
  const h = api((f) => yearless(f, false));
  const popup = await staged(h);
  popup.resolveCard(YEAR_CARD + 'redis-log', '2025');
  popup.ack = true;
  popup.startImport();
  await flush();
  assert.equal(h.sent.length, 1);
  assert.deepEqual(h.sent[0].supplied, { year: '2025' });
  popup.dispose();
});

test('a log with no computer and no year has both cards, and each is answered on its own', async () => {
  const h = api((f) => yearless(f, true));
  const popup = await staged(h);
  assert.deepEqual([...popup.cardIds()].sort(), ['redis-box', YEAR_CARD + 'redis-log'].sort());
  assert.equal(popup.fixesNeeded(), 2);
  popup.resolveCard(YEAR_CARD + 'redis-log', '2026');
  assert.equal(popup.fixesNeeded(), 1, 'the computer card is still open');
  popup.dispose();
});

test('leaving the year card out drops its files; undo opens it again', async () => {
  const h = api((f) => yearless(f, false));
  const popup = await staged(h);
  popup.leaveOut(YEAR_CARD + 'redis-log');
  assert.equal(popup.fixesNeeded(), 0);
  assert.equal(popup.targetCount(), 0, 'a left-out year card sends nothing');
  popup.undoCard(YEAR_CARD + 'redis-log');
  assert.equal(popup.fixesNeeded(), 1);
  assert.equal(popup.focus, 'card-' + YEAR_CARD + 'redis-log');
  popup.dispose();
});

test('the year answer is forgotten when the session is cleared', async () => {
  const h = api((f) => yearless(f, false));
  const popup = await staged(h);
  popup.resolveCard(YEAR_CARD + 'redis-log', '2026');
  popup.importMore();
  assert.deepEqual(popup.years, {});
  popup.dispose();
});

test('the copy reads plainly for a first-time investigator', () => {
  assert.match(POPUP_TEXT.yearTitle(1, 'Redis'), /does not say which year/);
  assert.match(POPUP_TEXT.yearTitle(3, 'Redis'), /^3 Redis logs do not say which year/);
  assert.match(POPUP_TEXT.yearAccept('2026', 1), /Use 2026 and accept 1 file$/);
  assert.match(POPUP_TEXT.yearAccept('2026', 2), /accept 2 files$/);
  assert.match(POPUP_TEXT.yearResolved('2026', 1), /Year 2026 added, 1 file accepted/);
});

test('toCheckFile reads needsYear', () => {
  if (!toCheckFile) return;
  const raw = { key: 'k', name: 'n', size: 1, verdict: 'needs-year', importer: 'redis-log', needsYear: true };
  assert.equal(toCheckFile(raw).needsYear, true);
  assert.equal(toCheckFile({ ...raw, needsYear: 'yes' }).needsYear, false, 'only a real true counts');
  assert.equal(toCheckFile({ ...raw, needsYear: undefined }).needsYear, false);
});
