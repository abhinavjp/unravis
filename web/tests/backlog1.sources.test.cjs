const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

// Backlog 1, P2: a source that was never imported carries noFile onto its chip, and the copy for that mark passes the lint.

const root = path.resolve(__dirname, '..');
function loadTypeScript(relativePath) {
  const filename = path.join(root, relativePath);
  const source = fs.readFileSync(filename, 'utf8');
  const output = ts.transpileModule(source, {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022, esModuleInterop: true }
  }).outputText;
  const module = { exports: {} };
  const nodeRequire = createRequire(filename);
  const load = (request) => /^\.\.?\//.test(request) && fs.existsSync(path.join(path.dirname(filename), request + '.ts'))
    ? loadTypeScript(path.relative(root, path.join(path.dirname(filename), request + '.ts')))
    : nodeRequire(request);
  new Function('require', 'module', 'exports', output)(load, module, module.exports);
  return module.exports;
}

const sources = loadTypeScript('src/lib/tracing/sources.ts');
const { lintScreenStrings } = loadTypeScript('src/lib/copy/copylint.ts');
const { deck } = loadTypeScript('src/lib/copy/deck.ts');

const response = (refs) => ({ auditSources: [], sources: refs, savedNotConnected: [], lanes: [] });

test('chipsOf marks a source that was never imported, and leaves imported ones unmarked', () => {
  const chips = sources.chipsOf(response([{ sourceId: 'x', label: 'u_ex.log', noFile: true }, { sourceId: 'y', label: 'a.log' }]));
  assert.equal(chips.find((c) => c.id === 'x').noFile, true);
  assert.equal('noFile' in chips.find((c) => c.id === 'y'), false);
});

test('the not-imported copy passes the copy lint', () => {
  const entries = sources.sourcesCopyEntries().filter((e) => e.id === 'tracingSources.notImported' || e.id === 'tracingSources.notImportedTitle');
  assert.equal(entries.length, 2);
  assert.deepEqual(lintScreenStrings(entries, deck.terms), []);
});

test('the whole picker copy still passes the copy lint', () => {
  assert.deepEqual(lintScreenStrings(sources.sourcesCopyEntries(), deck.terms), []);
});
