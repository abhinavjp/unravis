const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

// Audit database gaps on Add data > MongoDB (existing case): the readiness chip, the collection-name check, the database list, and the fixes
// the hub review asked of AddAuditDatabase.svelte (C3, C4, C7, C8, C11, P7) and appState.addDatabaseSource (P2).

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
      if (fs.existsSync(plain)) return loadTypeScript(path.relative(root, plain));
    }
    return nodeRequire(request);
  };
  new Function('require', 'module', 'exports', output)(load, module, module.exports);
  return module.exports;
}
const read = (relative) => fs.readFileSync(path.join(root, relative), 'utf8');

const gap = loadTypeScript('src/lib/auditGap.ts');
const copy = loadTypeScript('src/lib/copy/auditGapCopy.ts');
const { lintScreenStrings } = loadTypeScript('src/lib/copy/copylint.ts');
const { deck } = loadTypeScript('src/lib/copy/deck.ts');

const base = { testing: false, tested: true, testProblem: false, database: 'BrainAudit', chosen: 'audit', collectionProblem: '', connected: false, tagged: false, addProblem: false };
const word = (over) => gap.auditReady({ ...base, ...over }).word;

test('collection check: shape of the name only', () => {
  assert.equal(gap.collectionProblem('ApiAuditLog'), '');
  assert.equal(gap.collectionProblem('  ApiAuditLog  '), '');
  assert.equal(gap.collectionProblem(''), 'empty');
  assert.equal(gap.collectionProblem('   '), 'empty');
  assert.equal(gap.collectionProblem('a'.repeat(121)), 'too-long');
  assert.equal(gap.collectionProblem('Api*'), 'pattern');
  assert.equal(gap.collectionProblem('Api?Log'), 'pattern');
  for (const bad of ['a b', 'a$b', 'a/b', 'a\\b', 'a"b', 'a\u0000b']) assert.equal(gap.collectionProblem(bad), 'bad-char', JSON.stringify(bad));
  assert.equal(gap.collectionProblem('system.profile'), 'system');
  assert.equal(gap.collectionProblem('System.users'), 'system');
  assert.equal(gap.collectionProblem('audit.2026'), '', 'a dot is allowed');
});

test('ready chip: uses only the four readiness words', () => {
  const words = new Set();
  for (const over of [
    {}, { testing: true }, { tested: false }, { database: '' }, { chosen: 'unused' }, { collectionProblem: 'empty' }, { testProblem: true, tested: false },
    { addProblem: true }, { connected: true }, { tagged: true }, { chosen: 'other' }, { tagged: true, chosen: 'other' }
  ]) words.add(word(over));
  assert.deepEqual([...words].sort(), ['Cannot test yet', 'Failed', 'Partly ready', 'Ready']);
});

test('ready chip: each state', () => {
  assert.equal(word({}), 'Ready');
  assert.equal(word({ tested: false }), 'Cannot test yet');
  assert.equal(word({ testing: true }), 'Cannot test yet');
  assert.equal(word({ database: '' }), 'Cannot test yet');
  assert.equal(word({ chosen: 'unused' }), 'Cannot test yet');
  assert.equal(word({ collectionProblem: 'pattern' }), 'Cannot test yet');
  assert.equal(word({ collectionProblem: 'pattern', chosen: 'other' }), 'Partly ready', 'application data does not read the collection');
  assert.equal(word({ tested: false, testProblem: true }), 'Failed');
  assert.equal(word({ addProblem: true }), 'Failed');
  assert.equal(word({ connected: true, addProblem: true }), 'Partly ready', 'connected but not tagged is partly ready, whatever the retry said');
  assert.equal(word({ tagged: true, connected: true }), 'Ready');
  assert.equal(word({ tagged: true, chosen: 'other' }), 'Partly ready');
  assert.equal(gap.auditReady({ ...base, tested: false }).cls, 'cannot');
});

test('database list: text names only, cut at 200 and said so', () => {
  assert.deepEqual(gap.cleanDatabases(['a', '', 5, null, 'b']), { list: ['a', 'b'], cut: false });
  assert.deepEqual(gap.cleanDatabases(undefined), { list: [], cut: false });
  const many = Array.from({ length: 205 }, (_, i) => 'db' + i);
  const out = gap.cleanDatabases(many);
  assert.equal(out.list.length, 200);
  assert.equal(out.cut, true);
  assert.equal(gap.cleanDatabases(many.slice(0, 200)).cut, false);
});

test('password for a test: typed wins; empty reuses the held one for the same saved connection only (C7)', () => {
  const held = { savedConnectionId: 'a', password: 'secret' };
  assert.equal(gap.passwordForTest('typed', held, 'a'), 'typed');
  assert.equal(gap.passwordForTest('', held, 'a'), 'secret');
  assert.equal(gap.passwordForTest('', held, 'b'), '');
  assert.equal(gap.passwordForTest('', null, 'a'), '');
});

test('copy: registered, plain, and lint-clean', () => {
  const entries = copy.auditGapCopyEntries();
  const ids = new Set(entries.map((e) => e.id));
  for (const key of Object.keys(copy.AUDIT_GAP_TEXT)) assert.ok(ids.has('auditGap.' + key), key);
  for (const key of Object.keys(copy.COLLECTION_PROBLEM_TEXT)) assert.ok(ids.has('auditGap.collection.' + key), key);
  assert.deepEqual(lintScreenStrings(entries, deck.terms), []);
  const why = ['testing', 'not-tested', 'no-database', 'no-use', 'collection', 'test-failed', 'add-failed', 'connected-not-tagged', 'app-only', 'added-audit', 'added-app', 'ready'];
  for (const w of why) assert.ok(copy.readyHintText({ word: 'Ready', cls: 'ready', why: w }).length > 0, w);
});

test('component: C3 the saved connection cannot change during a test, and a late answer is ignored', () => {
  const c = read('src/components/sources/AddAuditDatabase.svelte');
  assert.match(c, /id="\{uid\}-saved" value=\{savedId\} disabled=\{readOnly \|\| busy \|\| testing\}/);
  assert.match(c, /const gen = \+\+testGen;/);
  assert.match(c, /if \(gen !== testGen \|\| savedId !== id\) return;/);
  assert.match(c, /testGen\+\+; testing = false;/, 'a reset invalidates a test in flight');
  assert.match(c, /finally \{ if \(gen === testGen\) testing = false; \}/);
});

test('component: C4 Add waits for a test and works on the connection held when it was pressed', () => {
  const c = read('src/components/sources/AddAuditDatabase.svelte');
  assert.match(c, /const canAdd = \$derived\(!readOnly && !busy && !testing && tested/);
  assert.match(c, /const h = held;\s+if \(!canAdd \|\| !h \|\| !recent\) return;/);
  assert.doesNotMatch(c.slice(c.indexOf('async function add()'), c.indexOf('function another()')), /\bheld\b(?!;)/, 'add() uses h, not held, after the first read');
});

test('component: C8 and P7 a finished add locks its ticks and collection; a connected database cannot be swapped', () => {
  const c = read('src/components/sources/AddAuditDatabase.svelte');
  assert.match(c, /<fieldset class="uses" disabled=\{readOnly \|\| busy \|\| connected \|\| tagged\}>/);
  assert.match(c, /id="\{uid\}-col"[^>]*disabled=\{readOnly \|\| busy \|\| connected \|\| tagged\}/);
  assert.match(c, /id="\{uid\}-db" value=\{database\} disabled=\{readOnly \|\| busy \|\| connected\}/);
  assert.match(c, /function pick\(name: string\) \{[^}]*if \(connected\) return;/s);
});

test('component: C11 the server warning and a cut list are shown', () => {
  const c = read('src/components/sources/AddAuditDatabase.svelte');
  assert.match(c, /data-test-warning/);
  assert.match(c, /data-list-cut/);
  assert.match(c, /safeServerLine\(out\.warning, secretsOf\(input\)\)/, 'the warning is cleaned of the address and credentials');
});

test('component: gaps are on screen, and Attach to a server is not offered', () => {
  const c = read('src/components/sources/AddAuditDatabase.svelte');
  assert.match(c, /data-testid="audit-ready-chip"/);
  assert.match(c, /data-collection-check=/);
  assert.match(c, /data-testid="audit-check-permission"/);
  assert.doesNotMatch(c, /Attach to a server/);
});

test('appState P2: addDatabaseSource leaves the open case alone when it changed during the add', () => {
  const s = read('src/lib/appState.svelte.ts');
  const body = s.slice(s.indexOf('export async function addDatabaseSource'), s.indexOf('export async function deleteSource'));
  assert.match(body, /const origin = activeCaseId;/);
  assert.match(body, /const sameCase = activeCaseId === origin;/);
  assert.match(body, /if \(sameCase && shouldSelectAddedSource/);
  assert.match(body, /if \(sameCase && activeCaseId === origin\) \{ completePendingRestore\(\); queueWorkspaceSave\(\); \}/);
});
