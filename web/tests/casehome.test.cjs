const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

// The case home page: the story built from what a case saved, its copy, and how the page is wired in.

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
const read = (relative) => fs.readFileSync(path.join(root, relative), 'utf8');

const { buildStory, shortText } = loadTypeScript('src/lib/story.ts');
const copy = loadTypeScript('src/lib/copy/caseHomeCopy.ts');
const { lintScreenStrings } = loadTypeScript('src/lib/copy/copylint.ts');
const { deck } = loadTypeScript('src/lib/copy/deck.ts');

const info = { id: 'a'.repeat(26), title: 'Slow checkout', created: '2026-10-24T09:00:00Z', bytes: 0, imports: 1 };
const file = (id, imported, extra = {}) => ({ id, importer: 'iis', fileName: id + '.log', sha256: '', bytes: 10, rows: 5, rejected: 0, hostLabel: '', tzRule: '', quality: null, knownGaps: [], imported, status: 'ok', supplied: {}, ...extra });
const run = (id, at, extra = {}) => ({ id, at, kind: 'sql', label: 'db1', status: 'ok', report: null, ...extra });
const theory = (id, createdAt, extra = {}) => ({ id, text: 'The pool ran dry', state: 'open', evidenceIds: [], notes: '', createdAt, updatedAt: createdAt, history: [], ...extra });

test('the story lists what happened, oldest first, whatever order it was read in', () => {
  const story = buildStory({
    info,
    imports: [file('f2', '2026-10-24T12:00:00Z'), file('f1', '2026-10-24T10:00:00Z')],
    health: [run(3, '2026-10-24T11:00:00Z')],
    theories: [theory('t1', '2026-10-24T13:00:00Z')]
  });
  assert.deepEqual(story.map((event) => event.id), ['case:' + info.id, 'file:f1', 'health:3', 'file:f2', 'theory:t1']);
});

test('equal times keep a steady order, and a time that cannot be read goes last', () => {
  const same = '2026-10-24T10:00:00Z';
  const story = buildStory({ info: { ...info, created: same }, imports: [file('f', same)], health: [run(1, same)], theories: [theory('t', same), theory('late', 'not a date')] });
  assert.deepEqual(story.map((event) => event.kind), ['case', 'file', 'health', 'theory', 'theory']);
  assert.equal(story[story.length - 1].id, 'theory:late');
});

test('a file that was read but not kept is a warning that carries the reason', () => {
  const [, kept] = buildStory({ info, imports: [file('x', '2026-10-24T10:00:00Z', { status: 'failed', reason: 'The file was empty.' })], health: [], theories: [] });
  assert.equal(kept.tone, 'warn');
  assert.equal(kept.detail, 'The file was empty.');
});

test('a health check shows its kind, and its tone follows the result', () => {
  const [, sql, mongo] = buildStory({ info, imports: [], health: [run(1, '2026-10-24T10:00:00Z', { status: 'bad' }), run(2, '2026-10-24T11:00:00Z', { kind: 'mongo', status: 'ok' })], theories: [] });
  assert.equal(sql.title, copy.CASE_HOME_TEXT.eventHealthSql);
  assert.equal(sql.tone, 'bad');
  assert.equal(mongo.title, copy.CASE_HOME_TEXT.eventHealthMongo);
  assert.equal(mongo.tone, 'ok');
});

test('a theory edited after it was written gets a second line; an untouched one does not', () => {
  const plain = buildStory({ info: null, imports: [], health: [], theories: [theory('a', '2026-10-24T10:00:00Z')] });
  assert.equal(plain.length, 1);
  const edited = buildStory({ info: null, imports: [], health: [], theories: [theory('a', '2026-10-24T10:00:00Z', { updatedAt: '2026-10-24T12:00:00Z', state: 'supported', history: [{ state: 'open', at: '2026-10-24T12:00:00Z', note: '' }] })] });
  assert.deepEqual(edited.map((event) => event.title), [copy.CASE_HOME_TEXT.eventTheoryAdded, copy.CASE_HOME_TEXT.eventTheoryChanged]);
});

test('an empty case has an empty story, and a missing case record adds no start line', () => {
  assert.deepEqual(buildStory({ info: null, imports: [], health: [], theories: [] }), []);
});

test('a long theory is cut at a word edge and a short one is untouched', () => {
  assert.equal(shortText('  one   two '), 'one two');
  const cut = shortText('word '.repeat(60), 40);
  assert.ok(cut.length <= 41 && cut.endsWith('…') && !cut.includes('  '));
});

test('the case home copy passes the screen-text rules', () => {
  assert.deepEqual(lintScreenStrings(copy.caseHomeCopyEntries(), deck.terms), []);
});

test('the case page is wired in: the screen, the header button and the health callback', () => {
  assert.match(read('src/App.svelte'), /app\.screen === 'case'[\s\S]{0,40}<CaseHome/);
  assert.match(read('src/components/AppHeader.svelte'), /openCaseHome/);
  assert.match(read('src/lib/components/HealthPanel.svelte'), /onRan\?\.\(\)/);
  const page = read('src/lib/components/CaseHome.svelte');
  assert.match(page, /buildStory\(/);
  assert.match(page, /openSidePanel\('ledger'\)/);
});

test('the case page survives a reload, an erase, a lock and a closed case', () => {
  const state = read('src/lib/appState.svelte.ts');
  assert.match(state, /unravis\.activeCase/);
  assert.match(state, /export function caseErased/);
  const cases = read('src/lib/components/CasePanel.svelte');
  assert.match(cases, /caseErased\(id\)/);
  const api = read('src/lib/api.ts');
  assert.match(api, /LOCKED_STATUS\) signalCasesLocked\(\)/);
  const page = read('src/lib/components/CaseHome.svelte');
  assert.match(page, /panels\.sidePanel/);
  assert.match(page, /status === 409/);
  assert.match(page, /readOnly/);
  const ledger = read('src/components/LedgerPanel.svelte');
  assert.match(ledger, /const cid = ledger\.caseId/);
});

test('the back button name contains its visible text', () => {
  assert.ok(copy.CASE_HOME_TEXT.backLabel.includes(copy.CASE_HOME_TEXT.back));
});
