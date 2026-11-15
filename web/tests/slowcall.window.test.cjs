const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

// Slow call window model: presets, custom values, Earlier/Later, Back to this call, wide rule.

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

const W = loadTypeScript('src/lib/drill/callWindow.ts');
const MIN = 60000;
const CALL = Date.UTC(2026, 10, 12, 10, 0, 0);
const win = (m, shiftMs = 0) => ({ before: m, after: m, shiftMs });

test('constants and default', () => {
  assert.deepEqual([...W.WINDOW_PRESETS], [5, 10, 15, 30, 60]);
  assert.equal(W.WINDOW_MIN, 1);
  assert.equal(W.WINDOW_MAX, 120);
  assert.equal(W.OVERVIEW_MINUTES, 60);
  assert.equal(W.WIDE_SECONDS, 901);
  assert.deepEqual(W.DEFAULT_WINDOW, { before: 15, after: 15, shiftMs: 0 });
});

test('each preset gives the right bounds and is its own preset', () => {
  for (const m of W.WINDOW_PRESETS) {
    const b = W.windowBounds(CALL, win(m));
    assert.equal(b.start, CALL - m * MIN);
    assert.equal(b.end, CALL + m * MIN);
    assert.equal(W.presetOf(win(m)), m);
  }
  const moved = W.windowBounds(CALL, { before: 10, after: 20, shiftMs: 5 * MIN });
  assert.equal(moved.start, CALL - 5 * MIN);
  assert.equal(moved.end, CALL + 25 * MIN);
});

test('overview is the call start plus or minus 60 minutes', () => {
  assert.deepEqual(W.overviewBounds(CALL), { start: CALL - 60 * MIN, end: CALL + 60 * MIN });
});

test('clampMinutes keeps whole minutes 1 to 120 only', () => {
  assert.equal(W.clampMinutes(0), null);
  assert.equal(W.clampMinutes(1), 1);
  assert.equal(W.clampMinutes(120), 120);
  assert.equal(W.clampMinutes(121), null);
  assert.equal(W.clampMinutes(1.5), null);
  assert.equal(W.clampMinutes('15'), null);
  assert.equal(W.clampMinutes(NaN), null);
  assert.equal(W.clampMinutes(null), null);
});

test('presetOf is null when moved or not a preset', () => {
  assert.equal(W.presetOf(win(15, MIN)), null);
  assert.equal(W.presetOf({ before: 10, after: 20, shiftMs: 0 }), null);
  assert.equal(W.presetOf(win(7)), null);
  assert.equal(W.presetOf(W.DEFAULT_WINDOW), 15);
});

test('isWide: false for plus or minus 15 unmoved, true for 30 and for moved', () => {
  assert.equal(W.isWide(win(15)), false);
  assert.equal(W.isWide(win(5)), false);
  assert.equal(W.isWide(win(30)), true);
  assert.equal(W.isWide({ before: 5, after: 16, shiftMs: 0 }), true);
  assert.equal(W.isWide(win(15, MIN)), true);
});

test('coversOverview only when the window holds the whole strip', () => {
  assert.equal(W.coversOverview(CALL, win(15)), false);
  assert.equal(W.coversOverview(CALL, win(60)), true);
  assert.equal(W.coversOverview(CALL, win(120)), true);
  assert.equal(W.coversOverview(CALL, win(60, MIN)), false);
});

test('stepWindow moves half a window and canStep says so', () => {
  const w = win(10);
  const later = W.stepWindow(CALL, w, 1);
  assert.equal(later.shiftMs, 10 * MIN);
  assert.equal(W.stepWindow(CALL, later, -1).shiftMs, 0);
  assert.equal(W.canStep(CALL, w, 1), true);
  assert.equal(W.canStep(CALL, w, -1), true);
});

test('step right at the right end is refused, and the window stays inside the strip', () => {
  const w = win(15);
  const a = W.stepWindow(CALL, w, 1); // +15
  const b = W.stepWindow(CALL, a, 1); // +30
  const c = W.stepWindow(CALL, b, 1); // +45 is the end
  assert.equal(c.shiftMs, 45 * MIN);
  assert.equal(W.windowBounds(CALL, c).end, W.overviewBounds(CALL).end);
  assert.equal(W.canStep(CALL, c, 1), false);
  assert.equal(W.stepWindow(CALL, c, 1).shiftMs, c.shiftMs);
  assert.equal(W.canStep(CALL, c, -1), true);
  // a step that would overshoot is cut at the end
  const d = W.stepWindow(CALL, { before: 30, after: 30, shiftMs: 20 * MIN }, 1);
  assert.equal(d.shiftMs, 30 * MIN);
});

test('left end is refused too', () => {
  const left = W.stepWindow(CALL, win(60), -1);
  assert.equal(left.shiftMs, 0);
  const w = { before: 15, after: 15, shiftMs: -45 * MIN };
  assert.equal(W.canStep(CALL, w, -1), false);
  assert.equal(W.canStep(CALL, w, 1), true);
});

test('a shift left out of range by a preset change: Later is refused, Earlier works, clampWindow fixes it', () => {
  const w = win(30, 50 * MIN); // allowed shift is -30..+30
  assert.equal(W.canStep(CALL, w, 1), false);
  assert.equal(W.stepWindow(CALL, w, 1), w);
  assert.equal(W.canStep(CALL, w, -1), true);
  assert.equal(W.stepWindow(CALL, w, -1).shiftMs, 20 * MIN);
  assert.equal(W.clampWindow(w).shiftMs, 30 * MIN);
  assert.equal(W.clampWindow(win(30, -50 * MIN)).shiftMs, -30 * MIN);
  const ok = win(15, 10 * MIN);
  assert.equal(W.clampWindow(ok), ok);
  assert.equal(W.clampWindow(win(90, 5 * MIN)).shiftMs, 5 * MIN); // cannot fit: left alone
});

test('a window as long as or longer than the strip cannot step either way', () => {
  for (const m of [60, 90, 120]) {
    assert.equal(W.canStep(CALL, win(m), 1), false);
    assert.equal(W.canStep(CALL, win(m), -1), false);
  }
});

test('centreOn clamps at both ends', () => {
  const w = win(15);
  assert.equal(W.centreOn(CALL, w, CALL + 10 * MIN).shiftMs, 10 * MIN);
  assert.equal(W.centreOn(CALL, w, CALL + 500 * MIN).shiftMs, 45 * MIN);
  assert.equal(W.centreOn(CALL, w, CALL - 500 * MIN).shiftMs, -45 * MIN);
  assert.equal(W.centreOn(CALL, win(60), CALL + 10 * MIN).shiftMs, 0);
});

test('centreOn allows for an uneven window', () => {
  const w = { before: 10, after: 20, shiftMs: 0 }; // middle sits 5 min after the call
  assert.equal(W.centreOn(CALL, w, CALL + 5 * MIN).shiftMs, 0);
  assert.equal(W.centreOn(CALL, w, CALL + 15 * MIN).shiftMs, 10 * MIN);
});

test('backToCall resets the shift and keeps the size', () => {
  const back = W.backToCall({ before: 10, after: 20, shiftMs: 7 * MIN });
  assert.deepEqual(back, { before: 10, after: 20, shiftMs: 0 });
});

test('callInside is false after two steps of a plus or minus 5 window', () => {
  const end = CALL + 3000;
  let w = win(5);
  assert.equal(W.callInside(CALL, end, w), true);
  w = W.stepWindow(CALL, w, 1);
  assert.equal(W.callInside(CALL, end, w), true); // call start sits on the left edge
  w = W.stepWindow(CALL, w, 1);
  assert.equal(W.callInside(CALL, end, w), false);
  assert.equal(W.callInside(CALL, end, W.backToCall(w)), true);
});
