const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

// Slow call screen: the lanes model (running at once, gaps, bars).

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

const { barPressAreas } = loadTypeScript('src/lib/drill/callLanes.ts');

// BL-492: the invisible press areas of the bars on the Slow call lanes.
const bar = (key, x1, x2, row) => ({ key, x: { a: x1, b: x2 }, y: { a: 3 + row * 13, b: 12 + row * 13 } });
const size = (a) => ({ w: a.x.b - a.x.a, h: a.y.b - a.y.a });
const contains = (a, b) => a.x.a <= b.x.a && a.x.b >= b.x.b && a.y.a <= b.y.a && a.y.b >= b.y.b;
const overlaps = (a, b) => Math.min(a.x.b, b.x.b) - Math.max(a.x.a, b.x.a) > 1e-9 && Math.min(a.y.b, b.y.b) - Math.max(a.y.a, b.y.a) > 1e-9;

test('a bar with nothing near it gets the full 24 x 24 area around it', () => {
  const a = barPressAreas([bar('a', 400, 500, 1)], 24, 24, 1000, 100).get('a');
  assert.deepEqual(size(a), { w: 100, h: 24 }); // already wider than 24
  assert.equal(a.y.a, 16 + 4.5 - 12);
  const narrow = barPressAreas([bar('n', 400, 403, 1)], 24, 24, 1000, 100).get('n');
  assert.deepEqual(size(narrow), { w: 24, h: 24 });
  assert.ok(narrow.x.a < 400 && narrow.x.b > 403);
});

test('at the edge of the drawing the area is moved inward, not cut short', () => {
  const top = barPressAreas([bar('t', 400, 500, 0)], 24, 24, 1000, 100).get('t');
  assert.equal(top.y.a, 0);
  assert.equal(size(top).h, 24);
  const left = barPressAreas([bar('l', 0, 3, 0)], 24, 24, 1000, 100).get('l');
  assert.equal(left.x.a, 0);
  assert.equal(size(left).w, 24);
  const bottom = barPressAreas([{ key: 'b', x: { a: 400, b: 500 }, y: { a: 90, b: 99 } }], 24, 24, 1000, 100).get('b');
  assert.equal(bottom.y.b, 100);
  assert.equal(size(bottom).h, 24);
});

test('three stacked bar rows: the free space is split at the middle of each gap, the lowest bar takes the room below it', () => {
  const rows = [bar('r0', 500, 540, 0), bar('r1', 500, 540, 1), bar('r2', 500, 540, 2)];
  const m = barPressAreas(rows, 24, 24, 1000, 100);
  // bar rows are 9 tall with 4 between them: the middle of a gap is 2 from each bar
  assert.equal(m.get('r0').y.b, 14);
  assert.equal(m.get('r1').y.a, 14);
  assert.equal(m.get('r1').y.b, 27);
  assert.equal(m.get('r2').y.a, 27);
  assert.equal(m.get('r0').y.a, 0);
  assert.equal(size(m.get('r2')).h, 24);
  assert.ok(size(m.get('r1')).h < 24 && size(m.get('r0')).h < 24);
  for (const k of ['r0', 'r1', 'r2']) assert.ok(contains(m.get(k), rows.find((r) => r.key === k)));
});

test('two bars side by side in one row share the gap between them; a bar far away keeps its full width', () => {
  const m = barPressAreas([bar('a', 100, 104, 0), bar('b', 112, 116, 0), bar('c', 600, 604, 0)], 24, 24, 1000, 100);
  assert.equal(m.get('a').x.b, 108);
  assert.equal(m.get('b').x.a, 108);
  assert.equal(size(m.get('c')).w, 24);
  assert.ok(!overlaps(m.get('a'), m.get('b')));
});

test('a bar at a corner of another bar (apart on both axes) is cut along one axis only', () => {
  const a = bar('a', 100, 130, 0);
  const b = bar('b', 134, 164, 1);
  const m = barPressAreas([a, b], 24, 24, 1000, 100);
  assert.ok(!overlaps(m.get('a'), m.get('b')));
  assert.ok(contains(m.get('a'), a) && contains(m.get('b'), b));
});

test('whatever the bars, no area overlaps another, none covers another bar, each keeps its own bar and stays in the drawing', () => {
  let seed = 7;
  const rnd = () => { seed = (seed * 1664525 + 1013904223) % 4294967296; return seed / 4294967296; };
  for (let run = 0; run < 300; run += 1) {
    const n = 1 + Math.floor(rnd() * 14);
    const bars = [];
    for (let i = 0; i < n; i += 1) {
      const row = Math.floor(rnd() * 3);
      const x1 = Math.floor(rnd() * 960);
      const w = 3 + Math.floor(rnd() * 80);
      const cand = bar('k' + i, x1, Math.min(1000, x1 + w), row);
      // the lanes never put two bars of one row on the same time
      if (bars.every((o) => o.y.a !== cand.y.a || o.x.b <= cand.x.a || cand.x.b <= o.x.a)) bars.push(cand);
    }
    const needX = 10 + rnd() * 30;
    const needY = 10 + rnd() * 30;
    const m = barPressAreas(bars, needX, needY, 1000, 100);
    assert.equal(m.size, bars.length);
    for (const b of bars) {
      const a = m.get(b.key);
      assert.ok(contains(a, b), 'keeps its bar');
      assert.ok(a.x.a >= 0 && a.x.b <= 1000 && a.y.a >= 0 && a.y.b <= 100, 'inside the drawing');
      for (const o of bars) {
        if (o === b) continue;
        assert.ok(!overlaps(a, m.get(o.key)), 'areas do not overlap');
        assert.ok(!overlaps(a, o), 'does not cover another bar');
      }
    }
  }
});

test('no bars gives no areas', () => {
  assert.equal(barPressAreas([], 24, 24, 1000, 100).size, 0);
});
