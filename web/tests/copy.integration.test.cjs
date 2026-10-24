const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

const root = path.resolve(__dirname, '..');
function loadTypeScript(relativePath) {
  const filename = path.join(root, relativePath);
  const source = fs.readFileSync(filename, 'utf8');
  const output = ts.transpileModule(source, {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022, esModuleInterop: true }
  }).outputText;
  const module = { exports: {} };
  const nodeRequire = createRequire(filename);
  // A relative import of another TypeScript file in src/lib is transpiled the same way.
  const load = (request) => request.startsWith('./') && fs.existsSync(path.join(path.dirname(filename), request + '.ts'))
    ? loadTypeScript(path.relative(root, path.join(path.dirname(filename), request + '.ts')))
    : nodeRequire(request);
  new Function('require', 'module', 'exports', output)(load, module, module.exports);
  return module.exports;
}

const { deck } = loadTypeScript('src/lib/copy/deck.ts');
const { lintCopy } = loadTypeScript('src/lib/copy/copylint.ts');
const plain = loadTypeScript('src/lib/copy/plainSummary.ts');
const wordings = loadTypeScript('src/lib/copy/wordings.ts');

test('the real deck passes the copy lint', () => {
  const entries = deck.terms.flatMap((t) => [
    { id: `${t.id}.hint`, text: t.hint },
    { id: `${t.id}.definition`, text: t.definition },
    { id: `${t.id}.notMeaning`, text: t.notMeaning }
  ]);
  assert.deepEqual(lintCopy(entries, deck.terms), []);
});

test('wordings output passes the copy lint', () => {
  const entries = [
    wordings.notObservedThreshold('SQL lock events', '30 seconds'),
    wordings.notObservedGap('the source was not captured in this window'),
    wordings.overlapUnique('IIS log', 'a restart', 28),
    wordings.auditCreate(28),
    wordings.plainAuditCreate(28)
  ].map((text, i) => ({ id: `w${i}`, text }));
  assert.deepEqual(lintCopy(entries, deck.terms), []);
});

test('exports used by the UI exist', () => {
  for (const name of ['plainAuditCreateSummary', 'plainTypicalVsSlowest', 'plainLowSample', 'plainIncomplete']) {
    assert.equal(typeof plain[name], 'function', name);
  }
});
