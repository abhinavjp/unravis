const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

const root = path.resolve(__dirname, '..');
function loadTypeScript(relativePath) {
  const filename = path.join(root, relativePath);
  const source = fs.readFileSync(filename, 'utf8');
  const output = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022, esModuleInterop: true } }).outputText;
  const module = { exports: {} };
  const nodeRequire = createRequire(filename);
  const load = (request) => /^\.\.?\//.test(request) && fs.existsSync(path.join(path.dirname(filename), request + '.ts'))
    ? loadTypeScript(path.relative(root, path.join(path.dirname(filename), request + '.ts')))
    : nodeRequire(request);
  new Function('require', 'module', 'exports', output)(load, module, module.exports);
  return module.exports;
}
const read = (relative) => fs.readFileSync(path.join(root, relative), 'utf8');

const { startView, truncateTitle, caseCounts, caseRow, START_CARDS, CASE_PAGE_SIZE, TITLE_MAX, visibleCount } = loadTypeScript('src/lib/startScreen.ts');
const { START_TEXT, startLintEntries } = loadTypeScript('src/lib/copy/startCopy.ts');
const { lintScreenStrings } = loadTypeScript('src/lib/copy/copylint.ts');
const { deck } = loadTypeScript('src/lib/copy/deck.ts');

const makeCase = (n, extra = {}) => ({ id: 'case-' + n, title: 'Case ' + n, created: '2026-10-24T15:19:37Z', closed: false, bytes: 1024, imports: n % 4, ...extra });
const makeCases = (n) => Array.from({ length: n }, (_, i) => makeCase(i + 1));

test('three cards are always present, in order, when the cases are open', () => {
  for (const lock of [null, 'none', 'unlocked']) {
    assert.deepEqual(startView(lock, []).cards.map((c) => c.id), ['existing', 'enter', 'create'], String(lock));
  }
  assert.equal(START_CARDS.length, 3);
  for (const card of START_CARDS) { assert.ok(card.title.trim()); assert.ok(card.text.trim()); }
});

test('zero cases shows the empty line and no rows', () => {
  const view = startView('unlocked', []);
  assert.equal(view.rows.length, 0);
  assert.equal(view.total, 0);
  assert.equal(view.empty, START_TEXT.existingEmpty);
  assert.equal(view.hidden, 0);
  assert.equal(view.shown, '');
});

test('one case shows title, created date and import count', () => {
  const view = startView('unlocked', [makeCase(3, { title: 'Payroll slowdown', imports: 1 })]);
  assert.equal(view.rows.length, 1);
  assert.equal(view.empty, '');
  const row = view.rows[0];
  assert.equal(row.id, 'case-3');
  assert.equal(row.title, 'Payroll slowdown');
  assert.match(row.created, /24 Oct 2026/);
  assert.equal(row.counts, '1 import');
  assert.equal(row.closed, false);
});

test('a source count is shown only when the server sent one', () => {
  assert.equal(caseCounts({ imports: 3 }), '3 imports');
  assert.equal(caseCounts({ imports: 3, sources: 2 }), '3 imports, 2 sources');
  assert.equal(caseCounts({ imports: 0, sources: 1 }), '0 imports, 1 source');
  assert.equal(caseCounts({ imports: Number.NaN }), '0 imports');
});

test('500 cases are capped at one page, with a show more count', () => {
  const cases = makeCases(500);
  const first = startView('unlocked', cases, 1);
  assert.equal(first.rows.length, CASE_PAGE_SIZE);
  assert.equal(first.hidden, 500 - CASE_PAGE_SIZE);
  assert.equal(first.total, 500);
  assert.equal(first.shown, CASE_PAGE_SIZE + ' of 500 cases shown.');
  const second = startView('unlocked', cases, 2);
  assert.equal(second.rows.length, 2 * CASE_PAGE_SIZE);
  const all = startView('unlocked', cases, 10);
  assert.equal(all.rows.length, 500);
  assert.equal(all.hidden, 0);
  assert.equal(all.shown, '');
  // A strange page count never shows fewer than one page or breaks.
  assert.equal(visibleCount(0), CASE_PAGE_SIZE);
  assert.equal(visibleCount(Number.NaN), CASE_PAGE_SIZE);
});

test('a list of exactly one page has no show more', () => {
  const view = startView('unlocked', makeCases(CASE_PAGE_SIZE), 1);
  assert.equal(view.hidden, 0);
  assert.equal(view.shown, '');
});

test('long titles are cut to one line and keep the full text for the tooltip', () => {
  const long = 'A very long case title that keeps going well past what one row can hold '.repeat(3);
  const row = caseRow(makeCase(1, { title: long }));
  assert.ok([...row.title].length <= TITLE_MAX);
  assert.ok(row.title.endsWith('…'));
  assert.equal(row.fullTitle, long.trim());
  assert.equal(truncateTitle('Short'), 'Short');
  assert.equal(truncateTitle('x'.repeat(TITLE_MAX)), 'x'.repeat(TITLE_MAX));
  // Cutting never splits an emoji in two.
  const emoji = truncateTitle('\u{1F600}'.repeat(100));
  assert.equal([...emoji].length, TITLE_MAX);
  assert.doesNotMatch(emoji, /[\ud800-\udbff](?![\udc00-\udfff])/);
});

test('a blank title reads as Untitled case', () => {
  assert.equal(truncateTitle('   '), START_TEXT.untitled);
  assert.equal(caseRow(makeCase(1, { title: '' })).fullTitle, START_TEXT.untitled);
});

test('a closed case is labelled, an open one is not', () => {
  const [open, closed] = startView('unlocked', [makeCase(1), makeCase(2, { closed: true })]).rows;
  assert.equal(open.closed, false);
  assert.equal(closed.closed, true);
  assert.equal(closed.closedLabel, START_TEXT.closedLabel);
  assert.match(START_TEXT.closedLabel, /closed/i);
});

test('a locked key hides every title, count and card, even when a list is passed in', () => {
  const view = startView('locked', makeCases(500), 3);
  assert.equal(view.locked, true);
  assert.deepEqual(view.rows, []);
  assert.deepEqual(view.cards, []);
  assert.equal(view.total, 0);
  assert.equal(view.hidden, 0);
  assert.equal(view.shown, '');
  assert.equal(view.empty, '');
  assert.equal(view.offerSetup, false);
  assert.doesNotMatch(JSON.stringify(view), /Case \d|imports?\b/);
});

test('the master key link is offered only while no key is set', () => {
  assert.equal(startView('none', []).offerSetup, true);
  assert.equal(startView('unlocked', []).offerSetup, false);
  assert.equal(startView(null, []).offerSetup, false);
});

test('the start screen reads the key status first and asks for no cases while locked', () => {
  const src = read('src/components/StartScreen.svelte');
  const status = src.indexOf('getMasterKeyStatus(');
  const list = src.indexOf('listCasesAndErased(');
  assert.ok(status > 0 && list > status, 'status is read before the case list');
  assert.match(src, /if \(closesCases\(lock\)\) \{ noteLocked\(\); return; \}/);
  assert.match(src, /\{#if !view\.locked\}/);
  assert.match(src, /onclick=\{onCreate\}/);
  assert.match(src, /<HelpNote id="start" \/>/);
  assert.doesNotMatch(src, /\bfetch\(/);
});

test('the start screen is a new Landing mode and leaves loading, connect and running alone', () => {
  const landing = read('src/components/Landing.svelte');
  assert.match(landing, /'loading'/);
  assert.match(landing, /'cases'/);
  assert.match(landing, /<StartScreen /);
});

test('start screen copy passes the copy lint', () => {
  assert.deepEqual(lintScreenStrings(startLintEntries(), deck.terms).map((f) => f.id + ': ' + f.message), []);
});

test('a case the server refuses as untrusted is marked, a normal one is not', () => {
  assert.equal(caseRow(makeCase(1, { untrusted: true })).untrusted, true);
  assert.equal(caseRow(makeCase(2, {})).untrusted, false);
  assert.equal(caseRow(makeCase(3, { untrusted: 'yes' })).untrusted, false);
});
