const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

// Update check in Unravis: the wording of each answer, the preview hash, and which release links are ours.

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

const upd = loadTypeScript('src/lib/updateCheck.ts');
const UNREACHABLE = 'Could not check for updates. Check your connection and try again later.';

test('the latest answer names the current version', () => {
  assert.equal(upd.updateCheckNote({ status: 'latest', current: '1.2.0', latest: '1.2.0', url: '' }), 'You are on the latest version (v1.2.0).');
});

test('the available answer names both versions', () => {
  assert.equal(upd.updateCheckNote({ status: 'available', current: '1.2.0', latest: '1.3.0', url: 'https://github.com/abhinavjp/unravis/releases/tag/v1.3.0' }), 'Version 1.3.0 is available. You have v1.2.0.');
});

test('the unreachable answer says the check could not be made', () => {
  assert.equal(upd.updateCheckNote({ status: 'unreachable', current: '1.2.0', latest: '', url: '' }), UNREACHABLE);
});

test('the dev answer says there is nothing to compare', () => {
  assert.equal(upd.updateCheckNote({ status: 'dev', current: 'dev', latest: '', url: '' }), 'This is a development build, so there is nothing to compare.');
});

test('an unknown status gets the unreachable wording', () => {
  assert.equal(upd.updateCheckNote({ status: 'weird', current: '1.2.0', latest: '', url: '' }), UNREACHABLE);
});

test('the preview hash accepts only the three demo names', () => {
  assert.equal(upd.updatePreviewFrom('#update=available'), 'available');
  assert.equal(upd.updatePreviewFrom('#t=abc&update=offline'), 'offline');
  assert.equal(upd.updatePreviewFrom('#update=latest'), 'latest');
  assert.equal(upd.updatePreviewFrom('#update=bogus'), '');
  assert.equal(upd.updatePreviewFrom(''), '');
});

test('only links inside the project own repository count as release links', () => {
  assert.equal(upd.isOwnReleaseUrl('https://github.com/abhinavjp/unravis/releases/tag/v1.0.0'), true);
  assert.equal(upd.isOwnReleaseUrl('https://github.com/abhinavjp/unravis.evil.com/x'), false);
  assert.equal(upd.isOwnReleaseUrl('http://github.com/abhinavjp/unravis/'), false);
  assert.equal(upd.isOwnReleaseUrl(''), false);
});
