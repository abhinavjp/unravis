const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

// Scenario 1, lane L3: sources and connections.
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
const wizardCopy = loadTypeScript('src/lib/copy/wizardSourcesCopy.ts');
const { lintScreenStrings } = loadTypeScript('src/lib/copy/copylint.ts');
const { deck } = loadTypeScript('src/lib/copy/deck.ts');

test('L3-01 the connections-page SQL form names its real buttons and refuses a long server name', () => {
  const form = read('src/components/sources/SqlConnectionForm.svelte');
  assert.match(form, /<p class="lead">\{CONN_TEXT\.sqlFormIntro\}<\/p>/);
  assert.doesNotMatch(form, /SOURCES_TEXT\.sqlIntro/);
  assert.match(form, /line = SOURCES_TEXT\.sqlTestNeedPassword/);
  assert.doesNotMatch(form, /CONN_TEXT\.testNeedsPassword/);
  assert.doesNotMatch(form, /line = CONN_TEXT\.saved/);
  assert.match(form, /form\.server\.trim\(\)\.length > 200[\s\S]*?SOURCES_TEXT\.sqlServerTooLong/);
  assert.match(form, /maxlength="400"/);
  assert.doesNotMatch(form, /maxlength="260"/);
  assert.ok(copy.CONN_TEXT.sqlFormIntro.includes('Press Test '));
  assert.ok(copy.CONN_TEXT.sqlFormIntro.includes('Save connection'));
  assert.ok(!copy.CONN_TEXT.sqlFormIntro.includes('list databases'));
  assert.ok(wizardCopy.SOURCES_TEXT.sqlIntro.includes('Test and list databases'));
  assert.ok(wizardCopy.SOURCES_TEXT.sqlServerTooLong.includes('200'));
});

test('L3 copy passes the screen lint', () => {
  assert.deepEqual(lintScreenStrings([...copy.sourcesCopyEntries(), ...wizardCopy.wizardSourcesCopyEntries()], deck.terms), []);
});
