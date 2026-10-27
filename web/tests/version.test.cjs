const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const ts = require('typescript');

const root = path.resolve(__dirname, '..');
const read = (relativePath) => fs.readFileSync(path.join(root, relativePath), 'utf8');
function loadTypeScript(relativePath) {
  const output = ts.transpileModule(read(relativePath), { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText;
  const module = { exports: {} };
  new Function('module', 'exports', output)(module, module.exports);
  return module.exports;
}
const { versionLine } = loadTypeScript('src/lib/versionLine.ts');

test('the version line names the build and drops what was not stamped', () => {
  assert.equal(versionLine({ version: '1.2.3', commit: '0123456789abcdef', date: '2026-10-27' }), 'Unravis v1.2.3 (build 0123456, 2026-10-27)');
  assert.equal(versionLine({ version: 'v1.2.3-4-gabc', commit: '', date: '' }), 'Unravis v1.2.3-4-gabc');
  assert.equal(versionLine({ version: 'dev', commit: '', date: '2026-10-27' }), 'Unravis dev (2026-10-27)');
  assert.equal(versionLine({ version: 'ci-abc1234', commit: 'abc1234', date: '' }), 'Unravis ci-abc1234 (build abc1234)');
});

test('a missing, empty or malformed answer gives no line', () => {
  assert.equal(versionLine(null), '');
  assert.equal(versionLine(undefined), '');
  assert.equal(versionLine({ version: '  ', commit: 'abc', date: 'x' }), '');
  assert.equal(versionLine({}), '');
});

test('the help panel asks the server for the version and shows it quietly at the foot', () => {
  const api = read('src/lib/api.ts');
  assert.match(api, /version: \(\) => request<VersionInfo>\('\/version'\)/);
  const glossary = read('src/components/Glossary.svelte');
  assert.match(glossary, /api\.version\(\)/);
  assert.match(glossary, /\.catch\(/, 'a failed call leaves the panel working');
  assert.ok(glossary.indexOf('</dl>') < glossary.indexOf('build-line'), 'the line sits after the glossary list');
});
