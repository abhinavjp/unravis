const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const ts = require('typescript');
const { compile } = require('svelte/compiler');
const { render } = require('svelte/server');
const { loadTypeScript } = require('./helpers/loadTs.cjs');

// W-16: removing a saved connection asks first, and says so when it fails. Same question in the Connections drawer and the SQL Server form.

const root = path.join(__dirname, '..');
const read = (relative) => fs.readFileSync(path.join(root, relative), 'utf8');
const { runRemoval } = loadTypeScript('src/lib/removeFlow.ts');
const { REMOVE_TEXT } = loadTypeScript('src/lib/copy/removeConnectionCopy.ts');
const { lintCopy } = loadTypeScript('src/lib/copy/copylint.ts');
const { deck } = loadTypeScript('src/lib/copy/deck.ts');

function loadComponent() {
  const compiled = compile(read('src/components/RemoveConfirm.svelte'), { filename: 'RemoveConfirm.svelte', generate: 'server' });
  const js = ts.transpileModule(compiled.js.code, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022, esModuleInterop: true } }).outputText;
  const mods = { '../lib/copy/removeConnectionCopy': { REMOVE_TEXT }, '../lib/removeFlow': { runRemoval } };
  const held = { exports: {} };
  new Function('require', 'module', 'exports', js)((request) => mods[request] ?? require(request), held, held.exports);
  return held.exports.default;
}

test('runRemoval: only a true answer counts as removed; false and a throw are failures that never escape', async () => {
  assert.equal(await runRemoval(async () => true), 'removed');
  assert.equal(await runRemoval(async () => false), 'failed');
  assert.equal(await runRemoval(async () => { throw new Error('503 from the server'); }), 'failed');
  assert.equal(await runRemoval(async () => undefined), 'failed');
});

test('a row that was not asked about shows only its Remove button: no question, no error, nothing removed', () => {
  const Remove = loadComponent();
  let calls = 0;
  const { body } = render(Remove, { props: { label: 'UK production', remove: async () => { calls++; return true; } } });
  assert.match(body, /<button[^>]*aria-label="Remove: UK production"[^>]*>Remove<\/button>/);
  assert.ok(!body.includes(REMOVE_TEXT.ask), 'the question is not shown until the button is pressed');
  assert.ok(!body.includes(REMOVE_TEXT.failed));
  assert.equal(calls, 0, 'rendering removes nothing');
  const locked = render(Remove, { props: { label: 'UK production', locked: true, remove: async () => true } }).body;
  assert.match(locked, /<button[^>]*disabled[^>]*>Remove<\/button>/, 'a busy drawer locks the button');
});

test('the first press only asks; only the answer Remove calls the removal; a failure keeps the row and says why', () => {
  const src = read('src/components/RemoveConfirm.svelte');
  assert.match(src, /async function ask\(\) \{ phase = 'ask';/, 'the Remove button only opens the question');
  assert.doesNotMatch(src.slice(src.indexOf('async function ask()'), src.indexOf('async function keep()')), /runRemoval|remove\(/);
  assert.match(src, /async function confirm\(\) \{\s*if \(phase !== 'ask'\) return;\s*phase = 'busy';\s*const result = await runRemoval\(remove\);/);
  assert.match(src, /if \(result === 'removed'\) return;[^\n]*\n\s*phase = 'failed';/);
  assert.match(src, /\{#if phase === 'failed'\}<div class="inline-error remove-error" role="alert">\{REMOVE_TEXT\.failed\}<\/div>\{\/if\}/);
  assert.match(src, /onclick=\{\(\) => void keep\(\)\}>\{REMOVE_TEXT\.keep\}/);
  assert.match(src, /event\.key !== 'Escape'/, 'Esc withdraws the question');
  assert.doesNotMatch(src, /window\.confirm|[^.\w]confirm\(['"`]/, 'no browser pop-up');
});

test('both places use the same question: the drawer and the SQL Server form', () => {
  const drawer = read('src/components/ConnectionsDrawer.svelte');
  assert.match(drawer, /<RemoveConfirm label=\{recent\.label\} locked=\{[^}]*\} remove=\{\(\) => forgetRecent\(recent\)\} \/>/);
  assert.doesNotMatch(drawer, /'Forget'|Forget</, 'the drawer button is Remove now');
  const form = read('src/components/wizard/SqlServerForm.svelte');
  assert.match(form, /<RemoveConfirm label=\{item\.label\} locked=\{busy \|\| testing\} remove=\{\(\) => forget\(item\.id\)\} \/>/);
  assert.doesNotMatch(form, /void forget\(/, 'the form no longer removes on the first press');
  const forget = form.slice(form.indexOf('async function forget('), form.indexOf('function use('));
  assert.match(forget, /try \{ await api\.forgetSqlConnection\(id\); \} catch \{ return false; \}/, 'a failure is reported, not swallowed');
  assert.match(forget, /await loadSaved\(\);\s*return true;/);
  assert.doesNotMatch(read('src/lib/copy/wizardSourcesCopy.ts'), /sqlForget/);
});

test('forgetRecent reports success or failure and leaves the list alone when the call fails', () => {
  const state = read('src/lib/appState.svelte.ts');
  const body = state.slice(state.indexOf('export async function forgetRecent'), state.indexOf('async function rememberConnection'));
  assert.match(body, /Promise<boolean> \{\s*if \(connectionTesting \|\| sourceBusy \|\| historyMutationBusy\) return false;/);
  const after = body.slice(body.indexOf('await api.forgetConnection(recent.id);'));
  const filter = after.indexOf('recentConnections = recentConnections.filter');
  assert.ok(filter > 0 && after.indexOf('return true;') > filter, 'the row leaves the list only after the call worked');
  assert.match(body, /\} catch \{\s*return false;\s*\} finally/);
});

test('the words pass the copy lint and the drawer layout lets the question take the full row', () => {
  assert.deepEqual(lintCopy(Object.values(REMOVE_TEXT).map((text, i) => ({ id: 'r' + i, text })), deck.terms), []);
  for (const text of Object.values(REMOVE_TEXT)) assert.doesNotMatch(text, /[‒-―−]| - |--|\p{Extended_Pictographic}/u, text);
  assert.equal(REMOVE_TEXT.ask, 'Remove this saved connection? Its saved password is removed too.');
  assert.equal(REMOVE_TEXT.failed, 'The saved connection could not be removed. It is still on the list. Try again.');
  assert.match(read('src/app.css'), /\.recent-card-actions \{ display: flex; flex-wrap: wrap;/);
});
