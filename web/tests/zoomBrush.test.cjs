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

test('brushHit on a selection narrower than two handles still favours the edges, but keeps a middle third for moving', () => {
  const sel = { left: 0.500, right: 0.510 };
  assert.equal(b.brushHit(0.5, sel, 0.01), 'start');
  assert.equal(b.brushHit(0.51, sel, 0.01), 'end');
  assert.equal(b.brushHit(0.502, sel, 0.01), 'start');
  assert.equal(b.brushHit(0.508, sel, 0.01), 'end');
  // Changed: the whole inside used to be edges; now the middle third is the body.
  assert.equal(b.brushHit(0.505, sel, 0.01), 'move');
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
  // Crossed handles: the start dragged beyond the end stops one floor (a step) short of it, inside the original window.
  const crossed = b.brushDrag('start', week, view, view.start, view.end + 2 * HOUR, STEP);
  assert.deepEqual(crossed, { start: view.end - STEP, end: view.end });
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
  // A 20 second new drag lands both ends on one grid point: nothing changes (it used to grow to a one-minute window).
  const tiny = b.brushDrag('new', week, view, week.start + 3 * HOUR + MIN, week.start + 3 * HOUR + MIN + 20_000, STEP);
  assert.deepEqual(tiny, view);
  // A new range outside the root on both sides covers the root, never more.
  const wide = b.brushDrag('new', week, view, week.start - DAY, week.end + DAY, STEP);
  assert.deepEqual(wide, week);
  // Unreadable input returns the start view.
  assert.deepEqual(b.brushDrag('move', week, view, NaN, 1, STEP), view);
});

test('brushDrag snaps edges to the step on the epoch grid', () => {
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

// ---- Review fixes: tiny drags, epoch grid, narrow selections ----
const H2 = 2 * HOUR;
const root7 = { start: at('2001-10-22T00:00:00Z'), end: at('2001-10-29T00:00:00Z') };

test('a short new-range drag that snaps both ends to one grid point changes nothing, instead of growing to one minute', () => {
  // 7 day root, 2 hour step: a 40 minute drag lands both ends on the same grid point.
  const from = root7.start + 3 * H2 + 10 * MIN;
  const win = b.brushDrag('new', root7, view, from, from + 40 * MIN, H2);
  assert.deepEqual(win, view, 'no-op, never a 1 minute window');
  // A drag that crosses into the next grid cell spans one whole step.
  const straddle = b.brushDrag('new', root7, view, root7.start + 3 * H2 - 50 * MIN, root7.start + 3 * H2 + 70 * MIN, H2);
  assert.equal(straddle.end - straddle.start, H2);
});

test('a new-range drag never returns a window narrower than one step unless it is a no-op', () => {
  let seed = 99;
  const next = () => { seed = (seed * 1103515245 + 12345) % 2147483648; return seed / 2147483648; };
  for (let i = 0; i < 300; i += 1) {
    const from = root7.start + next() * 7 * DAY, to = from + (next() - 0.5) * 3 * H2;
    const win = b.brushDrag('new', root7, view, from, to, H2);
    const same = win.start === view.start && win.end === view.end;
    assert.ok(same || win.end - win.start >= H2, JSON.stringify(win));
  }
});

test('dragging a handle past the other stops one floor short of it instead of crossing', () => {
  const v = { start: root7.start + 2 * DAY, end: root7.start + 3 * DAY };
  const floor = H2; // max(1 minute, the step)
  const start = b.brushDrag('start', root7, v, v.start, v.end + 5 * DAY, H2);
  assert.equal(start.end, v.end, 'the other edge stays');
  assert.equal(start.start, v.end - floor);
  const end = b.brushDrag('end', root7, v, v.end, v.start - 5 * DAY, H2);
  assert.equal(end.start, v.start);
  assert.equal(end.end, v.start + floor);
  for (const win of [start, end]) assert.ok(win.start >= v.start && win.end <= v.end, 'inside the original window');
});

test('resizing never makes the view narrower than a step', () => {
  const v = { start: root7.start + DAY, end: root7.start + DAY + 6 * HOUR };
  const win = b.brushDrag('end', root7, v, v.end, v.start + 10 * MIN, H2);
  assert.ok(win.end - win.start >= H2);
});

test('edges snap to the epoch grid, so a root that ends at an odd "now" still has edges on bucket boundaries', () => {
  const nowRoot = { start: at('2001-10-22T14:37:12.345Z'), end: at('2001-10-29T14:37:12.345Z') };
  const step = 15 * MIN;
  const v = { start: nowRoot.start + DAY, end: nowRoot.start + 2 * DAY };
  const inner = b.brushDrag('start', nowRoot, v, v.start, v.start + 3 * HOUR + 4 * MIN, step);
  assert.equal(inner.start % step, 0, 'start on the epoch grid');
  const fresh = b.brushDrag('new', nowRoot, v, nowRoot.start + 2 * DAY + 5 * MIN, nowRoot.start + 3 * DAY + 5 * MIN, step);
  assert.equal(fresh.start % step, 0);
  assert.equal(fresh.end % step, 0);
  const moved = b.brushDrag('move', nowRoot, v, v.start, v.start + 3 * HOUR, step);
  assert.equal(moved.start % step, 0);
  assert.equal(moved.end - moved.start, DAY);
  const centred = b.brushCentre(nowRoot, v, nowRoot.start + 4 * DAY, step);
  assert.equal(centred.start % step, 0);
  // Past either end the edge is clamped to the root, which may itself be off the grid.
  const all = b.brushDrag('new', nowRoot, v, nowRoot.start - DAY, nowRoot.end + DAY, step);
  assert.deepEqual(all, nowRoot);
  const last = b.brushDrag('end', nowRoot, v, v.end, nowRoot.end + 9 * DAY, step);
  assert.equal(last.end, nowRoot.end);
});

test('a selection narrower than two handles can still be moved by its middle', () => {
  const sel = { left: 0.5, right: 0.515 };
  const handle = 0.01; // 0.015 wide, a third is 0.005
  assert.equal(b.brushHit(0.5075, sel, handle), 'move');
  assert.equal(b.brushHit(0.5, sel, handle), 'start');
  assert.equal(b.brushHit(0.515, sel, handle), 'end');
  assert.equal(b.brushHit(0.502, sel, handle), 'start');
  assert.equal(b.brushHit(0.513, sel, handle), 'end');
  // Outside the selection the full handle still grabs the edge.
  assert.equal(b.brushHit(0.49, sel, handle), 'start');
  assert.equal(b.brushHit(0.525, sel, handle), 'end');
  // A very narrow one still has a middle.
  assert.equal(b.brushHit(0.5025, { left: 0.5, right: 0.505 }, handle), 'move');
});

test('brushFloor is the keyboard floor: one minute, or the whole root when it is narrower', () => {
  assert.equal(b.brushFloor(week), z.MIN_VIEW_MS);
  assert.equal(b.brushFloor({ start: 0, end: 30_000 }), 30_000);
});

