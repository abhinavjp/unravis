const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

// Guards found by clicking the wrong thing: a case erased elsewhere, a failed list load, a double submit.
// Everything here is synthetic.

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

const cases = loadTypeScript('src/lib/cases.ts');
const { IMPORT_TEXT, errorText } = loadTypeScript('src/lib/copy/importCopy.ts');

const appState = read('src/lib/appState.svelte.ts');

test('only a not-found answer counts as a case that is gone', () => {
  const gone = new cases.CaseApiError('x', 404, 'case-not-found');
  assert.equal(cases.isCaseGone(gone), true);
  assert.equal(cases.isCaseGone(new cases.CaseApiError('x', 404, 'import-not-found')), false);
  assert.equal(cases.isCaseGone(new cases.CaseApiError('x', 401, 'unauthorized')), false);
  assert.equal(cases.isCaseGone(new cases.CaseApiError('x', 0, 'unreachable')), false);
  assert.equal(cases.isCaseGone(new Error('case-not-found')), false);
  assert.equal(cases.isCaseGone(null), false);
});

test('a case that vanished is explained in plain words', () => {
  assert.match(errorText({ status: 404, reason: 'case-not-found', message: '' }), /not found.*erased/i);
});

test('adding a connection twice at once is refused before any work starts', () => {
  const add = appState.slice(appState.indexOf('export async function addSource'));
  const guard = add.indexOf('if (sourceBusy) return;');
  assert.ok(guard > 0, 'addSource checks sourceBusy');
  assert.ok(guard < add.indexOf('sourceBusy = true'), 'the check comes before the flag is set');
  assert.ok(guard < add.indexOf('connectionInput()'), 'and before anything is read or sent');
});

test('the file checks refuse an empty file and a negative size before upload', () => {
  assert.deepEqual(cases.fileSizeProblem(0), { kind: 'empty' });
  assert.deepEqual(cases.fileSizeProblem(-1), { kind: 'empty' });
  assert.equal(cases.fileSizeProblem(1), null);
});

test('an export of an empty case cannot be saved', () => {
  assert.equal(cases.canSaveCaseExport(null), false);
});

test('an unlisted computer is asked for in plain words, never as an unknown host', () => {
  const text = errorText({ status: 422, reason: 'unknown-host', message: '' });
  assert.match(text, /needs a computer/i, 'the file is said to need a computer');
  assert.doesNotMatch(text, /unknown host/i);
  assert.match(IMPORT_TEXT.blocked['unknown-host'], /needs a computer/i);
});

test('a pasted list longer than the server allows is cut to its first values and says so', () => {
  const inv = loadTypeScript('src/lib/investigation.ts');
  assert.equal(inv.MAX_FILTER_VALUES, 20);
  assert.equal(inv.MAX_USER_IDS, 50);
  const text = Array.from({ length: 5000 }, (_, i) => `192.0.2.${i}`).join(', ');
  assert.equal(inv.itemsOverLimit(text, 20), 4980);
  assert.equal(inv.itemsOverLimit('a, b, a', 20), 0, 'duplicates do not count twice');
  assert.equal(inv.itemsOverLimit(Array.from({ length: 20 }, (_, i) => 'v' + i).join(','), 20), 0, 'exactly the limit is fine');
  const first20 = inv.splitList(text).slice(0, 20);
  assert.equal(inv.syncListDraft(text, first20, () => true, 20), text, 'the typed text is kept while the applied list is its first 20');
  const controls = ['src/components/QueryControls.svelte', ...fs.readdirSync(path.join(root, 'src/components/filters')).filter(name => name.endsWith('.svelte')).map(name => 'src/components/filters/' + name)].map(read).join('\n');
  assert.match(controls, /splitList\(text\)\.slice\(0, MAX_FILTER_VALUES\)/, 'setList applies at most 20 values');
  assert.match(controls, /\.slice\(0, MAX_USER_IDS\)/, 'user ids stop at 50');
  for (const id of ['over-user-ids', 'over-ui-versions', 'over-user-ips', 'over-status-codes']) assert.match(controls, new RegExp(`data-testid="${id}"`), `${id} notice exists`);
});
