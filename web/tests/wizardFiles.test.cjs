const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

// The glue of the "Import files" panel: rows become sources, files are held for the last step, and Remove lets the file go.

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

const kinds = loadTypeScript('src/lib/fileKinds.ts');
const files = loadTypeScript('src/lib/wizardFiles.svelte.ts');
const create = loadTypeScript('src/lib/wizardCreate.svelte.ts');
const draft = loadTypeScript('src/lib/wizard.svelte.ts');
const sources = loadTypeScript('src/lib/wizardSources.ts');

const fake = (name, size = 100) => ({ name, size, webkitRelativePath: '' });
const fresh = () => { draft.setSources([]); create.resetCreate(); };

test('rows become file sources, the files are held, and a file of the draft is not asked for again', () => {
  fresh();
  const rows = kinds.stageFiles([fake('u_ex261020.log'), fake('httperr1.log'), fake('mystery.dat')]);
  const result = files.addStagedFiles(rows);
  assert.equal(result.added, 2);
  assert.equal(result.full, 0);
  assert.deepEqual(draft.wizard.sources.map((s) => s.kind), ['iis-w3c', 'iis-httperr']);
  for (const source of draft.wizard.sources) assert.ok(files.hasHeldFile(sources.sourceKey(source)), source.kind);
  assert.deepEqual(result.keys, draft.wizard.sources.map(sources.sourceKey));
});

test('a suggestion is not sent as an importer, a kind chosen by hand is', () => {
  assert.equal(files.importerHint({ choice: 'iis-w3c', changed: false }), '');
  assert.equal(files.importerHint({ choice: 'iis-httperr', changed: true }), 'iis-httperr');
  assert.equal(files.importerHint({ choice: 'app-log', changed: true }), '');
  assert.equal(files.importerHint({ choice: '', changed: true }), '');
});

test('a refused row and a row left on "Do not use" are not added', () => {
  fresh();
  const rows = kinds.stageFiles([fake('logs.zip', 10), fake('u_ex1.log', 0), fake('u_ex2.log', 50)]);
  rows[2].choice = '';
  const result = files.addStagedFiles(rows);
  assert.equal(result.added, 0);
  assert.equal(draft.wizard.sources.length, 0);
});

test('Remove lets go of the held file', () => {
  fresh();
  files.addStagedFiles(kinds.stageFiles([fake('u_ex261020.log'), fake('u_ex261021.log')]));
  const [first, second] = draft.wizard.sources;
  draft.setSources([second]);
  files.releaseOrphanFiles();
  assert.equal(files.hasHeldFile(sources.sourceKey(first)), false);
  assert.equal(files.hasHeldFile(sources.sourceKey(second)), true);
});

test('two files of one kind get different sources', () => {
  fresh();
  files.addStagedFiles(kinds.stageFiles([fake('u_ex261020.log'), fake('u_ex261021.log')]));
  const keys = draft.wizard.sources.map(sources.sourceKey);
  assert.equal(new Set(keys).size, 2);
});
