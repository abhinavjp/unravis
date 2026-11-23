// BL-162 / BL-164 leftover: Story so far also reads again when the person comes back to the page, because a change made somewhere the page
// cannot see (another browser tab, the API) has no signal in this tab. Pure rules (`storyReturn.ts`) are driven directly; the page's own wiring
// is pinned against the Svelte source.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const ts = require('typescript');

const root = path.resolve(__dirname, '..');
const source = fs.readFileSync(path.join(root, 'src/lib/storyReturn.ts'), 'utf8');
const module_ = { exports: {} };
new Function('module', 'exports', ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText)(module_, module_.exports);
const { RETURN_GAP_MS, readOnReturn, sameRead, watchReturn } = module_.exports;
const page = fs.readFileSync(path.join(root, 'src/lib/components/CaseHome.svelte'), 'utf8');

const base = { ready: true, hidden: false, busy: false, changedElsewhere: false, lastReadAt: 1000, now: 1000 + RETURN_GAP_MS };

test('coming back after the gap reads the case again', () => {
  assert.equal(readOnReturn(base), true);
});

test('no request storm: a return inside the gap, or while a read is on its way, reads nothing', () => {
  assert.equal(readOnReturn({ ...base, now: base.lastReadAt + RETURN_GAP_MS - 1 }), false);
  assert.equal(readOnReturn({ ...base, busy: true }), false);
});

test('nothing is read for a page that is hidden, not showing a case, or waiting for a reload after another tab moved the case', () => {
  assert.equal(readOnReturn({ ...base, hidden: true }), false);
  assert.equal(readOnReturn({ ...base, ready: false }), false);
  assert.equal(readOnReturn({ ...base, changedElsewhere: true }), false);
});

test('watchReturn listens to focus and visibilitychange, and stops', () => {
  const log = [];
  const target = (name) => ({ addEventListener: (type, fn) => log.push(`${name}+${type}`) && fn, removeEventListener: (type) => log.push(`${name}-${type}`) });
  const stop = watchReturn(() => {}, target('win'), target('doc'));
  assert.deepEqual(log, ['win+focus', 'doc+visibilitychange']);
  stop();
  assert.deepEqual(log.slice(2), ['win-focus', 'doc-visibilitychange']);
  assert.equal(typeof watchReturn(() => {}, undefined, undefined), 'function');
});

test('an answer that says what the page shows is the same read; a new file is not', () => {
  const files = [{ id: 1, fileName: 'a.log' }];
  assert.equal(sameRead(files, [{ id: 1, fileName: 'a.log' }]), true);
  assert.equal(sameRead(files, [{ id: 1, fileName: 'a.log' }, { id: 2, fileName: 'b.log' }]), false);
  assert.equal(sameRead(null, null), true);
  assert.equal(sameRead(null, files), false);
});

test('Case home reads on return, keeps what it shows when the read fails, and reads each part once', () => {
  assert.match(page, /watchReturn\(backOnPage\)/);
  assert.match(page, /function backOnPage\(\)[\s\S]*readOnReturn\(\{[\s\S]*isHidden\(\)[\s\S]*changedElsewhere: changed[\s\S]*void rereadFiles\(true\)/);
  // The case, its files and its health checks are read together; the theories only on return.
  const reread = page.slice(page.indexOf('async function rereadFiles'), page.indexOf('function backOnPage'));
  assert.match(reread, /getCaseView\(id\)/);
  assert.match(reread, /listImports\(id\)/);
  assert.match(reread, /fetchHealthRuns\(id\)\.catch\(\(\) => null\)/);
  assert.match(reread, /everything && reads \? refreshLedger\(\)/);
  // The answers are put in only when they all came and say something new; a failure goes to the catch and puts in nothing.
  assert.ok(reread.indexOf('const fresh = await getCaseView') < reread.indexOf('info = fresh'));
  assert.match(reread, /if \(!sameRead\(info, fresh\)\) info = fresh/);
  assert.match(reread, /catch \(error\) \{[\s\S]*lockedUnder\(error\)/);
  // The last read starts the gap; the listeners are taken off when the page goes.
  assert.match(reread, /lastReadAt = Date\.now\(\)/);
  assert.match(page, /stopWatching\(\); controller\?\.abort\(\)/);
});

// ---- the newest read wins (health checks), and a theory saved here is not lost to an older read
const { newestWins } = module_.exports;

test('newestWins: an answer is put in only while no read that started later has put its answer in', () => {
  const reads = newestWins();
  const slow = reads.begin();
  const finishedCheck = reads.begin();
  assert.equal(reads.accept(finishedCheck), true);
  assert.equal(reads.accept(slow), false, 'an older list that is slow never replaces the newer one');
  const next = reads.begin();
  assert.equal(reads.accept(next), true);
  assert.equal(reads.accept(next), false, 'one answer is put in once');
});

test('newestWins: a read that fails or is thrown away holds nothing back', () => {
  const reads = newestWins();
  const pageRead = reads.begin();
  reads.begin(); // a later read that never answers (it failed): it never asks to put anything in
  assert.equal(reads.accept(pageRead), true, 'the page read is still put in');
});

test('Case home gives every read of the health checks a number, and a case erased elsewhere shows as gone', () => {
  assert.match(page, /const healthReads = newestWins\(\)/);
  const reread = page.slice(page.indexOf('async function rereadFiles'), page.indexOf('function backOnPage'));
  assert.match(reread, /healthReads\.begin\(\)[\s\S]*fetchHealthRuns\(id\)/);
  assert.match(reread, /runs && healthReads\.accept\(healthToken\)/);
  const finished = page.slice(page.indexOf('async function rereadHealth'), page.indexOf('// The case was locked'));
  assert.match(finished, /healthReads\.begin\(\)[\s\S]*healthReads\.accept\(token\)\) health = runs/);
  assert.match(page, /healthReads\.begin\(\);\n\s*const \[files, runs, theory\][\s\S]*healthReads\.accept\(healthToken\)\) health = /, 'the page read takes a number too');
  assert.match(reread, /getCaseView\(id\)\.catch\(\(error: unknown\) => \{ if \(isCaseGone\(error\)\) return null; throw error; \}\);\s*if \(!fresh\) \{ if \(id === caseId && mine === rereadRun\) gone\(\); return; \}/);
});

function ledgerStore(listCalls) {
  const file = path.join(root, 'src/lib/ledgerStore.svelte.ts');
  const out = ts.transpileModule(fs.readFileSync(file, 'utf8'), { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText;
  const mod = { exports: {} };
  const req = (request) => {
    if (request === './api') return { api: { ledgerList: listCalls } };
    if (request === './ledger') return { addCitation: (list) => list, editorFromHypothesis: (h) => ({ text: h.text }) };
    if (request === './storyReturn') return module_.exports;
    throw new Error('unexpected ' + request);
  };
  new Function('require', 'module', 'exports', '$state', out)(req, mod, mod.exports, (value) => value);
  return mod.exports;
}
const entry = (id, text) => ({ id, text });

test('a theory saved while a list read is on its way is not lost: the read is made once more', async () => {
  const first = Promise.withResolvers();
  const answers = [first.promise, Promise.resolve([entry('a', 'old'), entry('b', 'new')])];
  let calls = 0;
  const store = ledgerStore(() => answers[calls++]);
  store.setLedgerCase('c1', false);
  store.ledger.items = [entry('a', 'old')];
  const reading = store.refreshLedger();
  // The save finishes here (the entry joins the list) before the older answer comes back.
  store.ledger.items.unshift(entry('b', 'new'));
  store.noteLedgerWrite();
  first.resolve([entry('a', 'old')]);
  await reading;
  assert.equal(calls, 2, 'read once more, not in a loop');
  assert.deepEqual(store.ledger.items.map((h) => h.id).sort(), ['a', 'b']);
});

test('a write during the second read too: the list on screen stands and no third read is made', async () => {
  const gates = [Promise.withResolvers(), Promise.withResolvers()];
  let calls = 0;
  const store = ledgerStore(() => gates[calls++].promise);
  store.setLedgerCase('c1', false);
  store.ledger.items = [entry('a', 'x')];
  const reading = store.refreshLedger();
  store.noteLedgerWrite();
  gates[0].resolve([entry('a', 'x')]);
  await new Promise((resolve) => setImmediate(resolve));
  store.ledger.items.unshift(entry('b', 'y'));
  store.noteLedgerWrite();
  gates[1].resolve([entry('a', 'x')]);
  await reading;
  assert.equal(calls, 2);
  assert.deepEqual(store.ledger.items.map((h) => h.id), ['b', 'a']);
  assert.equal(store.ledger.loaded, true);
});

test('an unchanged theory list is not put in again; a changed one is', async () => {
  const store = ledgerStore(() => Promise.resolve([entry('a', 'x')]));
  store.setLedgerCase('c1', false);
  await store.refreshLedger();
  const shown = store.ledger.items;
  await store.refreshLedger();
  assert.equal(store.ledger.items, shown, 'same answer, same list object');
  const changed = ledgerStore(() => Promise.resolve([entry('a', 'x'), entry('b', 'y')]));
  changed.setLedgerCase('c1', false);
  changed.ledger.items = [entry('a', 'x')];
  await changed.refreshLedger();
  assert.equal(changed.ledger.items.length, 2);
});

test('the ledger panel tells the store about each local write', () => {
  const panel = fs.readFileSync(path.join(root, 'src/components/LedgerPanel.svelte'), 'utf8');
  assert.match(panel, /function replace\(h: Hypothesis\) \{\n\s*noteLedgerWrite\(\);/);
  assert.match(panel, /noteLedgerWrite\(\);\n\s*ledger\.items = ledger\.items\.filter/);
});

test('a thrown-away re-read takes no health number, and another case starts with none of the last one\'s checks', () => {
  const reread = page.slice(page.indexOf('async function rereadFiles'), page.indexOf('function backOnPage'));
  // The case is read first; only then, while the read is still wanted (same case, newest re-read), does it take its number.
  const guard = reread.indexOf('if (id !== caseId || mine !== rereadRun) return;');
  const take = reread.indexOf('healthReads.begin()');
  assert.ok(guard > reread.indexOf('await getCaseView') && guard < take, 'the guard comes before begin()');
  // Opening another case on the same page (restore as new, then Open the new case) lets go of the last case's health checks and files at once.
  assert.match(page, /if \(loadedFor !== id\) \{ health = \[\]; imports = \[\]; \}\n\s*loadedFor = id;/);
});

test('the page read: a failed health read puts nothing in and takes no turn; an older answer does not overwrite a newer re-read', () => {
  const load = page.slice(page.indexOf('async function loadPage'), page.indexOf('function refusedWith'));
  assert.match(load, /if \(runs\.status === 'fulfilled' && healthReads\.accept\(healthToken\)\) health = runs\.value;/);
  assert.doesNotMatch(load, /health = [^;]*: \[\]/, 'a failed read no longer blanks the checks');
  assert.match(load, /const run = rereadRun;/);
  assert.match(load, /const overtaken = phase === 'ready' && run !== rereadRun;\s*if \(!overtaken\) \{\s*info = found;\s*imports = /);
});

test('a finished check puts its list in only for the case the page still shows', () => {
  const finished = page.slice(page.indexOf('async function rereadHealth'), page.indexOf('// The case was locked'));
  assert.match(finished, /if \(id === caseId && healthReads\.accept\(token\)\) health = runs;/);
});
