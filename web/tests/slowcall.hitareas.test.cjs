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

test('two bars drawn over each other keep a pressable part each: the shared part is split in the middle', () => {
  // short items close in time: each is drawn 3 px wide, so the drawings overlap although the times do not
  const a = bar('A', 100, 103, 0);
  const b = bar('B', 101, 104, 0);
  const m = barPressAreas([a, b], 24, 24, 1000, 100);
  assert.ok(!overlaps(m.get('A'), m.get('B')), 'areas do not overlap');
  const inArea = (k, x) => m.get(k).x.a <= x && x < m.get(k).x.b;
  assert.ok(inArea('A', 100.5), 'the part only A shows is A');
  assert.ok(!inArea('B', 100.5), 'and B does not take it');
  assert.ok(inArea('B', 103.5), 'the part only B shows is B');
  assert.ok(!inArea('A', 103.5), 'and A does not take it');
  assert.equal(m.get('A').x.b, 102);
  assert.equal(m.get('B').x.a, 102);
  // a bar inside a longer bar of the same row: both areas still exist and do not overlap
  const n = barPressAreas([bar('long', 100, 200, 0), bar('short', 140, 143, 0)], 24, 24, 1000, 100);
  assert.ok(!overlaps(n.get('long'), n.get('short')));
});

test('two short bars at the right edge of the drawing never give an area with a negative or empty size', () => {
  // review of PR 177: bars 998.5 to 1002.25 and 999.5 to 1003.25 used to give the later area x 1000.875 to 1000
  const m = barPressAreas([bar('A', 998.5, 1002.25, 0), bar('B', 999.5, 1003.25, 0)], 30, 33, 1000, 100);
  for (const a of m.values()) {
    assert.ok(a.x.b > a.x.a && a.y.b > a.y.a, 'not empty');
    assert.ok(a.x.a >= 0 && a.x.b <= 1000);
  }
  assert.ok(m.has('A'));
  assert.ok(!overlaps(m.get('A'), m.get('B') ?? { x: { a: 0, b: 0 }, y: { a: 0, b: 0 } }));
});

test('whatever the bars, no area overlaps another, each keeps a pressable part of its own bar, none takes what only another bar shows, all stay in the drawing', () => {
  let seed = 7;
  const rnd = () => { seed = (seed * 1664525 + 1013904223) % 4294967296; return seed / 4294967296; };
  for (let run = 0; run < 300; run += 1) {
    const n = 1 + Math.floor(rnd() * 14);
    const bars = [];
    // Half of the runs are crowded: short items close in time. As on the lanes, the true times of two items of one row never overlap, but a bar
    // is drawn at least 3 units wide, so the drawings of two close items can overlap (never one inside the other).
    const crowded = run % 2 === 1;
    // every fifth run puts the crowd at the left or right edge of the drawing; bars may reach past the right edge (they are not clamped when drawn)
    const edge = run % 5 === 2 ? (run % 10 === 2 ? 'left' : 'right') : null;
    const spans = [];
    for (let i = 0; i < n; i += 1) {
      const row = Math.floor(rnd() * 3);
      const x1 = edge === 'left' ? Math.floor(rnd() * 8) : edge === 'right' ? 992 + Math.floor(rnd() * 8) : crowded ? 400 + Math.floor(rnd() * 40) : Math.floor(rnd() * 960);
      const tw = crowded || edge ? Math.floor(rnd() * 9) : 3 + Math.floor(rnd() * 80);
      const t = { row, a: x1, b: edge ? x1 + tw : Math.min(1000, x1 + tw) };
      if (spans.some((o) => o.row === row && o.a < t.b + 1e-9 && t.a < o.b + 1e-9 && !(o.b <= t.a || t.b <= o.a))) continue;
      if (spans.some((o) => o.row === row && o.a === t.a)) continue;
      spans.push(t);
      bars.push(bar('k' + i, t.a, edge ? t.a + Math.max(tw, 3) : Math.min(1000, t.a + Math.max(tw, 3)), row));
    }
    const needX = 10 + rnd() * 30;
    const needY = 10 + rnd() * 30;
    const m = barPressAreas(bars, needX, needY, 1000, 100);
    for (const b of bars) {
      const a = m.get(b.key);
      // an area is missing only for a bar that another bar is drawn over (two short bars at the edge)
      if (!a) { assert.ok(bars.some((o) => o !== b && overlaps(b, o)), 'a bar with no area is one that is drawn over another'); continue; }
      assert.ok(a.x.a >= 0 && a.x.b <= 1000 && a.y.a >= 0 && a.y.b <= 100, 'inside the drawing');
      assert.ok(a.x.b > a.x.a && a.y.b > a.y.a, 'not empty');
      // own bar: something of it is inside the area (all of it when no other bar is drawn over it)
      assert.ok(Math.min(a.x.b, b.x.b) > Math.max(a.x.a, b.x.a) && Math.min(a.y.b, b.y.b) > Math.max(a.y.a, b.y.a), 'a pressable part of its own bar');
      const touched = bars.some((o) => o !== b && overlaps(b, o));
      if (!touched) assert.ok(contains(a, { x: { a: b.x.a, b: Math.min(b.x.b, 1000) }, y: b.y }), 'keeps the whole drawn bar when no bar is drawn over it');
      for (const o of bars) {
        if (o === b) continue;
        const ao = m.get(o.key);
        if (ao) assert.ok(!overlaps(a, ao), 'areas do not overlap');
        if (!overlaps(b, o)) {
          assert.ok(!overlaps(a, o), 'does not cover another bar');
        } else {
          // drawn over each other: no point that only the other bar shows (inside o, outside b) lies in this area
          for (let k = 0; k <= 20; k += 1) {
            const px = o.x.a + ((o.x.b - o.x.a) * k) / 20;
            const outsideB = px < b.x.a || px > b.x.b;
            const inside = px > a.x.a && px < a.x.b && o.y.a + 1 > a.y.a && o.y.a + 1 < a.y.b;
            assert.ok(!(outsideB && inside), 'an area takes the part only another bar shows');
          }
        }
      }
    }
  }
});

test('no bars gives no areas', () => {
  assert.equal(barPressAreas([], 24, 24, 1000, 100).size, 0);
});
