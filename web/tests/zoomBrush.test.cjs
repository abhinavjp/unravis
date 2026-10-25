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
  const output = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022, esModuleInterop: true } }).outputText;
  const module = { exports: {} };
  const nodeRequire = createRequire(filename);
  const load = (request) => /^\.\.?\//.test(request) && fs.existsSync(path.join(path.dirname(filename), request + '.ts'))
    ? loadTypeScript(path.relative(root, path.join(path.dirname(filename), request + '.ts')))
    : nodeRequire(request);
  new Function('require', 'module', 'exports', output)(load, module, module.exports);
  return module.exports;
}

const b = loadTypeScript('src/lib/zoomBrush.ts');
const z = loadTypeScript('src/lib/zoom.ts');

const MIN = 60_000, HOUR = 3_600_000, DAY = 86_400_000;
const STEP = 15 * MIN;
const at = (text) => Date.parse(text);
// Synthetic windows only: a week from Monday 22 Oct 2001 to Monday 29 Oct 2001 (UTC).
const week = { start: at('2001-10-22T00:00:00Z'), end: at('2001-10-29T00:00:00Z') };
const view = { start: week.start + 2 * DAY, end: week.start + 3 * DAY };

test('the module imports only from ./zoom', () => {
  const source = fs.readFileSync(path.join(root, 'src/lib/zoomBrush.ts'), 'utf8');
  const imports = [...new Set([...source.matchAll(/^import[^;]*from\s+'([^']+)'/gm)].map((match) => match[1]))];
  assert.deepEqual(imports, ['./zoom']);
});

test('brushStepMs is the bucket in milliseconds, at least 10 seconds', () => {
  assert.equal(b.brushStepMs(900), 900_000);
  assert.equal(b.brushStepMs(10), 10_000);
  assert.equal(b.brushStepMs(1), 10_000);
  assert.equal(b.brushStepMs(NaN), 10_000);
});

test('brushHit picks the edge, the body or a new range', () => {
  const sel = { left: 0.3, right: 0.7 };
  assert.equal(b.brushHit(0.30, sel, 0.01), 'start');
  assert.equal(b.brushHit(0.50, sel, 0.01), 'move');
  assert.equal(b.brushHit(0.70, sel, 0.01), 'end');
  assert.equal(b.brushHit(0.9, sel, 0.01), 'new');
  assert.equal(b.brushHit(0.1, sel, 0.01), 'new');
  assert.equal(b.brushHit(0.305, sel, 0.01), 'start', 'within the handle width counts');
  assert.equal(b.brushHit(0.695, sel, 0.01), 'end');
  assert.equal(b.brushHit(0.295, sel, 0.01), 'start', 'just outside the selection but inside the handle');
});

test('brushHit favours the edges on a selection narrower than two handles', () => {
  const sel = { left: 0.500, right: 0.510 };
  assert.equal(b.brushHit(0.5, sel, 0.01), 'start');
  assert.equal(b.brushHit(0.51, sel, 0.01), 'end');
  // Every point inside such a selection is within a handle of one edge, so it is never the body.
  for (const x of [0.5, 0.502, 0.505, 0.508, 0.51]) assert.notEqual(b.brushHit(x, sel, 0.01), 'move', String(x));
  assert.equal(b.brushHit(0.502, sel, 0.01), 'start');
  assert.equal(b.brushHit(0.508, sel, 0.01), 'end');
});

test('brushDrag clamps, orders and keeps the 1-minute floor', () => {
  // Dragging the start handle far past the end of the root.
  const past = b.brushDrag('start', week, view, view.start, week.end + 30 * DAY, STEP);
  assert.ok(past.end - past.start >= z.MIN_VIEW_MS, 'floor');
  assert.ok(past.end <= week.end, 'inside the root');
  assert.ok(past.start >= week.start);
  assert.ok(past.start < past.end, 'ordered');
  // The same with the view already at the end of the root.
  const tail = { start: week.end - HOUR, end: week.end };
  const tailDrag = b.brushDrag('start', week, tail, tail.start, week.end + DAY, STEP);
  assert.ok(tailDrag.end === week.end && tailDrag.end - tailDrag.start >= z.MIN_VIEW_MS && tailDrag.start < tailDrag.end);
  // Dragging the end handle before the start of the root.
  const before = b.brushDrag('end', week, view, view.end, week.start - 30 * DAY, STEP);
  assert.ok(before.start >= week.start && before.end - before.start >= z.MIN_VIEW_MS && before.start < before.end);
  // Crossed handles: the start dragged beyond the end still gives an ordered window.
  const crossed = b.brushDrag('start', week, view, view.start, view.end + 2 * HOUR, STEP);
  assert.ok(crossed.start < crossed.end && crossed.start >= view.end - 1 && crossed.end - crossed.start >= z.MIN_VIEW_MS);
  // Moving the body past the start of the root stops at the start and keeps the width.
  const moved = b.brushDrag('move', week, view, view.start, view.start - 30 * DAY, STEP);
  assert.equal(moved.start, week.start);
  assert.equal(moved.end - moved.start, view.end - view.start);
  // Moving the body past the end stops at the end and keeps the width.
  const movedLate = b.brushDrag('move', week, view, view.start, view.start + 30 * DAY, STEP);
  assert.equal(movedLate.end, week.end);
  assert.equal(movedLate.end - movedLate.start, view.end - view.start);
  // A new range dragged from right to left is ordered.
  const from = week.start + 5 * DAY, to = week.start + 3 * DAY;
  const fresh = b.brushDrag('new', week, view, from, to, STEP);
  assert.deepEqual(fresh, { start: to, end: from });
  // A 20 second new drag grows to exactly the floor.
  const tiny = b.brushDrag('new', week, view, week.start + 3 * HOUR + MIN, week.start + 3 * HOUR + MIN + 20_000, STEP);
  assert.equal(tiny.end - tiny.start, z.MIN_VIEW_MS);
  assert.ok(tiny.start >= week.start && tiny.end <= week.end);
  // A new range outside the root on both sides covers the root, never more.
  const wide = b.brushDrag('new', week, view, week.start - DAY, week.end + DAY, STEP);
  assert.deepEqual(wide, week);
  // Unreadable input returns the start view.
  assert.deepEqual(b.brushDrag('move', week, view, NaN, 1, STEP), view);
});

test('brushDrag snaps edges to the step from the root start', () => {
  const odd = (n) => n * STEP + 4 * MIN + 17_000;
  const cases = [
    b.brushDrag('start', week, view, view.start, view.start + odd(3), STEP),
    b.brushDrag('end', week, view, view.end, view.end - odd(2), STEP),
    b.brushDrag('new', week, view, week.start + odd(5), week.start + odd(30), STEP),
  ];
  for (const win of cases) {
    assert.equal((win.start - week.start) % STEP, 0, 'start on the grid');
    assert.equal((win.end - week.start) % STEP, 0, 'end on the grid');
  }
  const moved = b.brushDrag('move', week, view, view.start, view.start + odd(7), STEP);
  assert.equal((moved.start - week.start) % STEP, 0);
  assert.equal(moved.end - moved.start, view.end - view.start);
});

test("brushDrag with mode 'none' returns the start view", () => {
  assert.deepEqual(b.brushDrag('none', week, view, view.start, view.start + 9 * DAY, STEP), view);
  assert.deepEqual(b.brushDrag('none', week, view, 0, 0, STEP), { start: view.start, end: view.end });
});

test('brushDrag never leaves the root, whatever the drag', () => {
  let seed = 7;
  const next = () => { seed = (seed * 1103515245 + 12345) % 2147483648; return seed / 2147483648; };
  const modes = ['start', 'end', 'move', 'new'];
  for (let i = 0; i < 400; i += 1) {
    const mode = modes[i % 4];
    const from = week.start + (next() * 3 - 1) * 7 * DAY, to = week.start + (next() * 3 - 1) * 7 * DAY;
    const win = b.brushDrag(mode, week, view, from, to, STEP);
    assert.ok(win.start >= week.start && win.end <= week.end && win.start < win.end, mode + ' stays valid ' + JSON.stringify(win));
    assert.ok(win.end - win.start >= z.MIN_VIEW_MS, mode + ' keeps the floor');
  }
});

test('brushCentre keeps the width and clamps at the ends', () => {
  const width = view.end - view.start;
  const mid = week.start + 3 * DAY;
  const centred = b.brushCentre(week, view, mid, STEP);
  assert.equal(centred.end - centred.start, width);
  assert.equal(centred.start + width / 2, mid);
  const last = b.brushCentre(week, view, week.end, STEP);
  assert.equal(last.end, week.end);
  assert.equal(last.end - last.start, width);
  const first = b.brushCentre(week, view, week.start - DAY, STEP);
  assert.equal(first.start, week.start);
  assert.equal(first.end - first.start, width);
  assert.equal((centred.start - week.start) % STEP, 0, 'on the grid');
});

test('brushKey moves one edge or the whole window', () => {
  const width = view.end - view.start;
  assert.deepEqual(b.brushKey('end', 'ArrowRight', week, view, STEP), { start: view.start, end: view.end + STEP });
  assert.deepEqual(b.brushKey('end', 'ArrowLeft', week, view, STEP), { start: view.start, end: view.end - STEP });
  assert.deepEqual(b.brushKey('start', 'ArrowRight', week, view, STEP), { start: view.start + STEP, end: view.end });
  assert.deepEqual(b.brushKey('start', 'ArrowLeft', week, view, STEP), { start: view.start - STEP, end: view.end });
  assert.deepEqual(b.brushKey('end', 'ArrowUp', week, view, STEP), { start: view.start, end: view.end + STEP });
  assert.deepEqual(b.brushKey('end', 'ArrowDown', week, view, STEP), { start: view.start, end: view.end - STEP });
  // PageDown shifts the whole window one width earlier, from either handle. PageUp shifts it later.
  for (const handle of ['start', 'end']) {
    assert.deepEqual(b.brushKey(handle, 'PageDown', week, view, STEP), { start: view.start - width, end: view.end - width }, handle);
    assert.deepEqual(b.brushKey(handle, 'PageUp', week, view, STEP), { start: view.start + width, end: view.end + width }, handle);
  }
  // Clamped at the root, keeping the width.
  const early = { start: week.start + HOUR, end: week.start + 3 * HOUR };
  assert.deepEqual(b.brushKey('start', 'PageDown', week, early, STEP), { start: week.start, end: week.start + 2 * HOUR });
  // Home and End go to the root's edges.
  assert.deepEqual(b.brushKey('start', 'Home', week, view, STEP), { start: week.start, end: view.end });
  assert.deepEqual(b.brushKey('end', 'End', week, view, STEP), { start: view.start, end: week.end });
  // Anything else is not handled.
  assert.equal(b.brushKey('start', 'a', week, view, STEP), null);
  assert.equal(b.brushKey('end', 'Enter', week, view, STEP), null);
  assert.equal(b.brushKey('end', 'Escape', week, view, STEP), null);
});

test('brushKey keeps the handles apart by the 1-minute floor and stays in the root', () => {
  const narrow = { start: week.start + DAY, end: week.start + DAY + 5 * MIN };
  const shrunk = b.brushKey('end', 'ArrowLeft', week, narrow, STEP);
  assert.equal(shrunk.end - shrunk.start, z.MIN_VIEW_MS, 'the end stops one minute after the start');
  const squeezed = b.brushKey('start', 'ArrowRight', week, narrow, STEP);
  assert.equal(squeezed.end - squeezed.start, z.MIN_VIEW_MS);
  assert.equal(squeezed.end, narrow.end);
  // The start cannot pass the end with End, and the end cannot pass the start with Home.
  const startEnd = b.brushKey('start', 'End', week, view, STEP);
  assert.equal(startEnd.end - startEnd.start, z.MIN_VIEW_MS);
  assert.equal(startEnd.end, view.end);
  const endHome = b.brushKey('end', 'Home', week, view, STEP);
  assert.equal(endHome.end - endHome.start, z.MIN_VIEW_MS);
  assert.equal(endHome.start, view.start);
  // At the root's edges the keys leave the window as it is.
  assert.deepEqual(b.brushKey('start', 'ArrowLeft', week, week, STEP), week);
  assert.deepEqual(b.brushKey('end', 'ArrowRight', week, week, STEP), week);
  assert.deepEqual(b.brushKey('end', 'PageUp', week, week, STEP), week);
});

test('hammering keys with a fixed seed never leaves the root or the floor', () => {
  let seed = 12345;
  const next = () => { seed = (seed * 1103515245 + 12345) % 2147483648; return seed / 2147483648; };
  const keys = ['ArrowLeft', 'ArrowRight', 'ArrowUp', 'ArrowDown', 'PageUp', 'PageDown', 'Home', 'End'];
  let current = view;
  for (let i = 0; i < 300; i += 1) {
    const handle = next() < 0.5 ? 'start' : 'end';
    const win = b.brushKey(handle, keys[Math.floor(next() * keys.length)], week, current, STEP);
    assert.ok(win, 'handled');
    assert.ok(win.start >= week.start && win.end <= week.end && win.start < win.end, JSON.stringify(win));
    assert.ok(win.end - win.start >= z.MIN_VIEW_MS);
    current = win;
  }
});
