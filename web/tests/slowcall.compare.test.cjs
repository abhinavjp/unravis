const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const ts = require('typescript');

// Call detail: the "this call vs normal" marker and the whole-span readout. Pure functions, so they run on transpiled source.

const root = path.resolve(__dirname, '..');
function load(relativePath) {
  const source = fs.readFileSync(path.join(root, relativePath), 'utf8');
  const output = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText;
  const module = { exports: {} };
  new Function('module', 'exports', output)(module, module.exports);
  return module.exports;
}
const { compareToNormal } = load('src/lib/drill/callCompare.ts');
const { readoutSpan } = load('src/lib/drill/callReadout.ts');

const steps = (...p95s) => p95s.map((p95Ms) => ({ p95Ms }));

test('above the highest step mark', () => {
  const r = compareToNormal(3000, steps(500, 800, 1200));
  assert.deepEqual({ verdict: r.verdict, typicalMs: r.typicalMs, highestMs: r.highestMs, steps: r.steps }, { verdict: 'above', typicalMs: 800, highestMs: 1200, steps: 3 });
});

test('between the typical and the highest mark is near', () => {
  assert.equal(compareToNormal(1000, steps(500, 800, 1200)).verdict, 'near');
  assert.equal(compareToNormal(800, steps(500, 800, 1200)).verdict, 'near');
});

test('below the typical mark', () => {
  assert.equal(compareToNormal(200, steps(500, 800, 1200)).verdict, 'below');
});

test('typical is the middle of an even count', () => {
  assert.equal(compareToNormal(1, steps(400, 600, 800, 1000)).typicalMs, 700);
});

test('no usable mark or no duration gives null, never a guess', () => {
  assert.equal(compareToNormal(1000, steps(null, null)), null);
  assert.equal(compareToNormal(1000, []), null);
  assert.equal(compareToNormal(null, steps(500)), null);
  assert.equal(compareToNormal(NaN, steps(500)), null);
  assert.equal(compareToNormal(1000, steps(NaN, null)), null);
});

const cell = (start, end, atOnce, p95Ms = null) => ({ start, end, atOnce, p95Ms, refused: null });

test('span readout takes the highest value over every step the call touched', () => {
  const lanes = { audit: [cell(0, 100, 2, 300), cell(100, 200, 7, 900), cell(200, 300, 3, 100)], iis: [cell(0, 100, 1), cell(100, 200, 4)], sql: [] };
  const r = readoutSpan(lanes, { startMs: 50, endMs: 150 });
  assert.equal(r.api.atOnce, 7);
  assert.equal(r.api.p95Ms, 900);
  assert.equal(r.iis.atOnce, 4);
  assert.equal(r.db, null);
  assert.equal(r.isThisCall, true);
});

test('a step the call does not touch is left out', () => {
  const lanes = { audit: [cell(0, 100, 2), cell(100, 200, 7)], iis: [], sql: [] };
  assert.equal(readoutSpan(lanes, { startMs: 10, endMs: 90 }).api.atOnce, 2);
  assert.equal(readoutSpan(lanes, { startMs: 100, endMs: 150 }).api.atOnce, 7);
});

test('a call with no end is read at its start; a lane with only empty cells reads not recorded', () => {
  const lanes = { audit: [cell(0, 100, 2), cell(100, 200, 7)], iis: [cell(0, 100, null)], sql: [] };
  const r = readoutSpan(lanes, { startMs: 120, endMs: 120 });
  assert.equal(r.api.atOnce, 7);
  assert.equal(r.iis, null);
  assert.equal(readoutSpan(lanes, { startMs: 5, endMs: 50 }).iis.atOnce, null);
});

test('nothing under the call, or a bad start, gives null', () => {
  assert.equal(readoutSpan({ audit: [cell(0, 100, 2)], iis: [], sql: [] }, { startMs: 500, endMs: 600 }), null);
  assert.equal(readoutSpan({ audit: [], iis: [], sql: [] }, { startMs: NaN, endMs: 1 }), null);
});
