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

const casePanel = read('src/lib/components/CasePanel.svelte');
const importPanel = read('src/lib/components/ImportPanel.svelte');
const caseTools = read('src/lib/components/CaseTools.svelte');
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

test('a case erased elsewhere makes the case list reload and stop offering it', () => {
  assert.match(casePanel, /export async function noteGone\(\)/);
  const body = casePanel.slice(casePanel.indexOf('export async function noteGone'), casePanel.indexOf('export async function refresh'));
  assert.match(body, /await refresh\(\)/, 'the list is reloaded');
  assert.match(body, /case-not-found/, 'the reason is shown');
  assert.match(casePanel, /if \(isCaseGone\(error\)\) \{ void noteGone\(\); return; \}/, 'the panel itself reacts to a gone case');
  assert.match(importPanel, /if \(isCaseGone\(error\)\) \{ void casePanel\?\.noteGone\(\); return; \}/, 'import and preview errors reach it too');
  assert.match(importPanel, /onGone=\{\(\) => casePanel\?\.noteGone\(\)\}/, 'so do timeline and export errors');
  assert.match(caseTools, /if \(isCaseGone\(error\)\) onGone\?\.\(\);/);
});

test('a case list that failed to load is not shown as "No cases yet"', () => {
  assert.match(casePanel, /catch \(error\) \{ loadFailed = true; fail\(error\); \}/);
  assert.match(casePanel, /\{#if !loadFailed\}<p class="note">\{IMPORT_TEXT\.noCases\}<\/p>\{\/if\}/);
  assert.ok(IMPORT_TEXT.noCases, 'the empty-list text still exists for a real empty list');
  const loaded = casePanel.slice(casePanel.indexOf('cases = await listCases'), casePanel.indexOf('} catch (error) { loadFailed'));
  assert.match(loaded, /loadFailed = false/, 'a later good load clears the flag');
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

test('the import panel moves focus into itself when it opens, so Tab stays inside the dialog', () => {
  assert.match(importPanel, /bind:element=\{drawer\}/, 'the panel holds its dialog element');
  const mount = importPanel.slice(importPanel.indexOf('onMount(() => {'));
  assert.ok(mount.slice(0, mount.indexOf('listHosts()')).includes('drawer?.focus();'), 'it focuses the dialog before anything else');
});
