const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

// Slow call overview strip (minimap) model: row scaling, box geometry, pointer maths.

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

const { mapRow, boxOf, fractionOf, centreFromFraction, columnMinutes, KEY_STEP_FRACTION } = loadTypeScript('src/lib/drill/callMap.ts');

const cell = (start, atOnce) => ({ start, end: start + 60000, atOnce });
const ov = { start: 0, end: 1000 };

test('mapRow scales each column to the row own peak', () => {
  const row = mapRow('audit', [cell(0, 2), cell(60000, 8), cell(120000, 4)]);
  assert.equal(row.key, 'audit');
  assert.deepEqual(row.columns.map((c) => c.height), [0.25, 1, 0.5]);
  assert.deepEqual(row.peak, { atOnce: 8, start: 60000 });
  assert.equal(row.columns[1].end, 120000);
});

test('mapRow keeps gaps as null and does not count them', () => {
  const row = mapRow('iis', [cell(0, null), cell(60000, 5), cell(120000, null)]);
  assert.deepEqual(row.columns.map((c) => c.height), [null, 1, null]);
  assert.deepEqual(row.peak, { atOnce: 5, start: 60000 });
});

test('mapRow gives height 0 for an all-zero row, with a peak of 0', () => {
  const row = mapRow('sql', [cell(0, 0), cell(60000, null), cell(120000, 0)]);
  assert.deepEqual(row.columns.map((c) => c.height), [0, null, 0]);
  assert.deepEqual(row.peak, { atOnce: 0, start: 0 });
});

test('mapRow of an all-null or empty row has no peak', () => {
  const gaps = mapRow('sql', [cell(0, null), cell(60000, null)]);
  assert.equal(gaps.peak, null);
  assert.deepEqual(gaps.columns.map((c) => c.height), [null, null]);
  const empty = mapRow('audit', []);
  assert.equal(empty.peak, null);
  assert.equal(empty.columns.length, 0);
});

test('boxOf: window equal to the overview fills the strip', () => {
  assert.deepEqual(boxOf(ov, { start: 0, end: 1000 }), { left: 0, width: 1 });
});

test('boxOf: an inside window gives plain fractions', () => {
  assert.deepEqual(boxOf(ov, { start: 250, end: 500 }), { left: 0.25, width: 0.25 });
});

test('boxOf: a window hanging off the right end is clamped inside the strip', () => {
  const box = boxOf(ov, { start: 900, end: 1200 });
  assert.equal(box.width, 0.3);
  assert.ok(Math.abs(box.left - 0.7) < 1e-9);
  assert.ok(box.left + box.width <= 1 + 1e-9);
});

test('boxOf: a window before the start and a window wider than the overview are clamped', () => {
  assert.equal(boxOf(ov, { start: -200, end: 100 }).left, 0);
  assert.deepEqual(boxOf(ov, { start: -500, end: 2000 }), { left: 0, width: 1 });
  assert.ok(boxOf(ov, { start: 500, end: 500 }).width > 0);
});

test('fractionOf is null outside the overview and a fraction inside', () => {
  assert.equal(fractionOf(ov, -1), null);
  assert.equal(fractionOf(ov, 1001), null);
  assert.equal(fractionOf(ov, 250), 0.25);
  assert.equal(fractionOf(ov, 0), 0);
  assert.equal(fractionOf(ov, 1000), 1);
});

test('centreFromFraction maps a pointer fraction to ms and clamps', () => {
  assert.equal(centreFromFraction(ov, 0.5), 500);
  assert.equal(centreFromFraction(ov, -3), 0);
  assert.equal(centreFromFraction(ov, 7), 1000);
});

test('columnMinutes rounds up with a minimum of 1', () => {
  assert.equal(columnMinutes(60), 1);
  assert.equal(columnMinutes(300), 5);
  assert.equal(columnMinutes(90), 2);
  assert.equal(columnMinutes(10), 1);
  assert.equal(columnMinutes(0), 1);
});

test('arrow key step is 5% of the strip', () => {
  assert.equal(KEY_STEP_FRACTION, 0.05);
});
