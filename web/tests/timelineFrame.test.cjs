const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

// The y-axis ticks of the Investigation Timeline chart: whole numbers from 0 to the tallest bar.

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

const lanes = loadTypeScript('src/lib/tracing/lanes.ts');

test('a scale with no height has the single tick 0', () => {
  assert.deepEqual(lanes.yTicks(0), [0]);
  assert.deepEqual(lanes.yTicks(-5), [0]);
  assert.deepEqual(lanes.yTicks(NaN), [0]);
});

test('below 40 only exact whole quarters are kept', () => {
  assert.deepEqual(lanes.yTicks(1), [0, 1]);
  assert.deepEqual(lanes.yTicks(2), [0, 1, 2]);
  assert.deepEqual(lanes.yTicks(3), [0, 3]);
  assert.deepEqual(lanes.yTicks(4), [0, 1, 2, 3, 4]);
  assert.deepEqual(lanes.yTicks(8), [0, 2, 4, 6, 8]);
  assert.deepEqual(lanes.yTicks(10), [0, 10]);
  assert.deepEqual(lanes.yTicks(12), [0, 3, 6, 9, 12]);
});

test('from 40 up each quarter is rounded', () => {
  assert.deepEqual(lanes.yTicks(40), [0, 10, 20, 30, 40]);
  assert.deepEqual(lanes.yTicks(45), [0, 11, 23, 34, 45]);
  assert.deepEqual(lanes.yTicks(100), [0, 25, 50, 75, 100]);
  assert.deepEqual(lanes.yTicks(1250000), [0, 312500, 625000, 937500, 1250000]);
});

test('the ticks are strictly ascending from 0 to the rounded top', () => {
  for (const max of [4, 12, 40, 45, 100, 1250000]) {
    const ticks = lanes.yTicks(max);
    assert.equal(ticks[0], 0, `max ${max} starts at 0`);
    assert.equal(ticks[ticks.length - 1], Math.round(max), `max ${max} ends at the rounded top`);
    for (let i = 1; i < ticks.length; i++) assert.ok(ticks[i] > ticks[i - 1], `max ${max} is strictly ascending`);
  }
});
