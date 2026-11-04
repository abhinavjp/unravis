const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

// Add data > MongoDB on an existing case (P7): the copy is registered and lint-clean, the route opens the new page and keeps the profiler apart,
// and the page connects with the Connections drawer's steps and then tags the database to the case.

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

const copy = loadTypeScript('src/lib/copy/sourcesCopy.ts');
const model = loadTypeScript('src/lib/wizardSources.ts');
const { lintScreenStrings } = loadTypeScript('src/lib/copy/copylint.ts');
const { deck } = loadTypeScript('src/lib/copy/deck.ts');

test('audit-add: every sentence of the page is registered for the copy lint, and none breaks a rule', () => {
  const entries = copy.sourcesCopyEntries();
  const ids = new Set(entries.map((e) => e.id));
  for (const key of Object.keys(copy.AUDIT_ADD_TEXT)) assert.ok(ids.has('sources.auditAdd.' + key) || typeof copy.AUDIT_ADD_TEXT[key] === 'function', 'not registered: ' + key);
  for (const built of ['testedLine', 'addedAudit', 'addedApp', 'connectedNotTagged']) assert.ok(ids.has('sources.auditAdd.' + built), 'no sample of ' + built);
  assert.deepEqual(lintScreenStrings(entries, deck.terms), []);
});

test('audit-add: the copy names API audit logs, tells the profiler apart, and no longer says the card reads the server', () => {
  assert.match(copy.AUDIT_ADD_TEXT.tickAudit, /API audit logs \(added to the analysis\)/);
  assert.match(copy.AUDIT_ADD_TEXT.profilerBody, /separate/i);
  assert.doesNotMatch(copy.ADD_TEXT.mongoBody, /clusterMonitor/);
  assert.match(copy.ADD_TEXT.mongoBody, /audit logs/);
  assert.match(copy.AUDIT_ADD_TEXT.addedAudit('ApiAudit'), /Sources/);
  assert.match(copy.AUDIT_ADD_TEXT.connectedNotTagged('ApiAudit'), /connected/);
});

test('audit-add: the default tick follows the same name rule as the create wizard', () => {
  assert.equal(model.looksLikeAuditDatabase('Sandbox_ApiAuditLogs'), true);
  assert.equal(model.looksLikeAuditDatabase('orders'), false);
  assert.equal(model.classFromTicks(true, true), 'audit');
  assert.equal(model.classFromTicks(false, true), 'other');
  assert.equal(model.classFromTicks(false, false), 'unused');
});

test('audit-add: the hub opens the new page from Add data > MongoDB and keeps Step 8 as its own tab', () => {
  const hub = read('src/components/sources/SourcesHub.svelte');
  assert.match(hub, /import AddAuditDatabase from '\.\/AddAuditDatabase\.svelte'/);
  assert.match(hub, /what === 'mongo'\) addingAudit = true/);
  assert.doesNotMatch(hub, /what === 'mongo'\) tab = 'mongo'/);
  assert.match(hub, /id: 'mongo', label: HUB_TEXT\.mongo/, 'the Step 8 tab is still listed');
  assert.match(hub, /<MongoStepPanel /);
  assert.match(hub, /onProfiler=\{\(\) => \{ addingAudit = false; tab = 'mongo'; \}\}/);
});

test('audit-add: the page connects like the Connections drawer, then tags the database to the case', () => {
  const page = read('src/components/sources/AddAuditDatabase.svelte');
  assert.match(page, /addDatabaseSource\(/);
  assert.match(page, /postSourceTag\(id,/);
  assert.match(page, /kind: 'mongo-database'/);
  assert.match(page, /classification: cls/);
  assert.match(page, /api\.testConnection\(/);
  // The password is never written to storage, the URL or a log.
  assert.doesNotMatch(page, /localStorage|sessionStorage|console\.|location\./);
  // A failed tag keeps the connected state so the retry only tags.
  assert.match(page, /connectedNotTagged/);
  const state = read('src/lib/appState.svelte.ts');
  const body = state.slice(state.indexOf('export async function addDatabaseSource'), state.indexOf('export async function deleteSource'));
  for (const step of ['api.addSource(', 'sources = [...sources, added]', 'noteSavedConnection([added]', 'queueWorkspaceSave()', 'completePendingRestore()']) assert.ok(body.includes(step), 'missing step: ' + step);
});

test('audit-add: a closed or read-only case switches every control off', () => {
  const page = read('src/components/sources/AddAuditDatabase.svelte');
  assert.match(page, /const canAdd = \$derived\(!readOnly/);
  assert.ok((page.match(/readOnly \|\| busy/g) ?? []).length >= 4);
});
