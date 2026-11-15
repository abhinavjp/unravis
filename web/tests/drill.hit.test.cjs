const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

// Drill: which flagged diamond a click landed on.

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

const { hitFlagged } = loadTypeScript('src/lib/drill/hit.ts');

const box = (slot, left, top, right, bottom) => ({ slot, left, top, right, bottom });

test('a point inside a mark returns its slot', () => {
  const marks = [box(3, 100, 100, 120, 120)];
  assert.equal(hitFlagged(marks, 110, 110), 3);
});

test('a point outside the rect but within pad still hits', () => {
  const marks = [box(5, 100, 100, 120, 120)];
  assert.equal(hitFlagged(marks, 104, 110), 5);
  assert.equal(hitFlagged(marks, 110, 126), 5);
  assert.equal(hitFlagged(marks, 110, 130), null);
});

test('a point outside the rect and pad returns null', () => {
  const marks = [box(1, 100, 100, 120, 120)];
  assert.equal(hitFlagged(marks, 50, 50), null);
  assert.equal(hitFlagged(marks, 100 - 7, 110), null);
});

test('overlapping marks: the one with the nearest centre wins', () => {
  const marks = [box(1, 100, 100, 140, 140), box(2, 120, 100, 160, 140)];
  // centres: slot 1 at (120,120), slot 2 at (140,120)
  assert.equal(hitFlagged(marks, 125, 120), 1);
  assert.equal(hitFlagged(marks, 137, 120), 2);
});

test('an empty list returns null', () => {
  assert.equal(hitFlagged([], 10, 10), null);
});
