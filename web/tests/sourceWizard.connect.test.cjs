const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const ts = require('typescript');

const root = path.resolve(__dirname, '..');
const read = (r) => fs.readFileSync(path.join(root, r), 'utf8');
const load = (r) => { const m = { exports: {} }; new Function('module', 'exports', 'require', ts.transpileModule(read(r), { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText)(m, m.exports, require); return m.exports; };

test('Connect… on a found Mongo/SQL row opens the form with the address filled in', () => {
  const { connectPrefill } = load('src/lib/sourceWizard.ts');
  assert.equal(connectPrefill('mongo'), 'mongodb://localhost:27017');
  assert.equal(connectPrefill('sql'), 'localhost');
  const find = read('src/components/sourcewiz/FindOnPc.svelte');
  assert.match(find, /onCard\(db, f\.id\)/, 'the row passes its finding id');
  const step = read('src/components/sourcewiz/SourceStep.svelte');
  assert.match(step, /<MongoClassify prefill=\{from !== '' \? connectPrefill\('mongo'\)/);
  assert.match(step, /<SqlServerForm prefill=\{from !== '' \? connectPrefill\('sql'\)/);
  assert.match(read('src/components/wizard/MongoClassify.svelte'), /let uri = \$state\(prefill\)/);
  assert.match(read('src/components/wizard/SqlServerForm.svelte'), /server: prefill/);
});

test('after a successful connect the row shows Added and the wizard returns to the Find list', () => {
  const step = read('src/components/sourcewiz/SourceStep.svelte');
  assert.match(step, /function connectedDone\(\) \{ if \(from !== '' && !connected\.includes\(from\)\) connected = \[\.\.\.connected, from\]; from = ''; card = 'pc'; \}/);
  const find = read('src/components/sourcewiz/FindOnPc.svelte');
  assert.match(find, /connected\.includes\(e\.finding\.id\) \? WIZ\.find\.added/);
  assert.match(find, /connected\.includes\(f\.id\) \? WIZ\.find\.added : WIZ\.find\.connect/);
});

test('add mode never replaces the retry list the case keeps, and asks before discarding a create draft', () => {
  const submit = read('src/lib/wizardSubmit.ts');
  assert.match(submit, /api\.saveRetries && !input\.appendOnly/);
  const flow = read('src/components/sourcewiz/AddSourcesFlow.svelte');
  assert.match(flow, /createView\.appendOnly = true/);
  assert.match(flow, /blockers\(\)\.length > 0\) return/, 'blockers are read fresh at the click');
  assert.match(flow, /askDiscard/);
  assert.match(read('src/components/sources/SourcesHub.svelte'), /disabled=\{createView\.running\}/);
});
