const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

// Clumsy-user tests for the drill-down chart: hostile brush drags, wild keyboard use, zooming past the floor, promoting at the root,
// and a long trail of "Investigate this window" followed by Back. Everything runs the pure modules; every window is synthetic
// (a week of October 2001, UTC) and the random keys come from a fixed seed, so the tests are deterministic.

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

const z = loadTypeScript('src/lib/zoom.ts');
const b = loadTypeScript('src/lib/zoomBrush.ts');
const p = loadTypeScript('src/lib/promote.ts');
const { countEpochBuckets, bucketForWindows } = loadTypeScript('src/lib/time.ts');
const { scopeProblem } = loadTypeScript('src/lib/autorun.ts');
const { zoomCopy, allZoomStrings } = loadTypeScript('src/lib/copy/zoomCopy.ts');
const { lintScreenStrings } = loadTypeScript('src/lib/copy/copylint.ts');
const { deck } = loadTypeScript('src/lib/copy/deck.ts');

const SEC = 1000, MIN = 60_000, HOUR = 3_600_000, DAY = 86_400_000;
const at = (text) => Date.parse(text);
const iso = (ms) => new Date(ms).toISOString();
const week = { start: at('2001-10-22T00:00:00Z'), end: at('2001-10-29T00:00:00Z') };
const LADDER = [10, 15, 30, 60, 120, 180, 300, 600, 900, 1800, 3600, 7200, 10_800, 21_600, 43_200, 86_400];

// A small seeded generator (mulberry32): the same numbers on every run.
function seeded(seed) {
  let a = seed >>> 0;
  return () => {
    a = (a + 0x6D2B79F5) >>> 0;
    let t = a;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

// A brush result is valid when it is a real window inside the root and at least one floor wide.
function assertValidBrush(win, rootWin, label) {
  assert.ok(z.isWindow(win), label + ' is a window: ' + JSON.stringify(win));
  assert.ok(win.start >= rootWin.start && win.end <= rootWin.end, label + ' stays inside the root: ' + JSON.stringify(win));
  assert.ok(win.end - win.start >= Math.min(z.MIN_VIEW_MS, rootWin.end - rootWin.start), label + ' is at least the floor wide: ' + JSON.stringify(win));
}

const STEPS = [10 * SEC, 15 * MIN, HOUR];
const HOSTILE_POINTS = (rootWin) => [
  NaN, Infinity, -Infinity, 0, -1, 1e15, -1e15,
  rootWin.start - 10 * DAY, rootWin.start - 1, rootWin.start, rootWin.start + 1, rootWin.end - 1, rootWin.end, rootWin.end + 1, rootWin.end + 10 * DAY
];

// ---------------------------------------------------------------------------------------------------------------------
// The overview brush
// ---------------------------------------------------------------------------------------------------------------------
test('a brush drag in any mode, from or to any point (NaN, backwards, off either end of the track), stays valid and inside the root', () => {
  const roots = [week, { start: week.start, end: week.start + 30 * SEC }, { start: week.start, end: week.start + 2 * MIN }];
  for (const rootWin of roots) {
    const rootWidth = rootWin.end - rootWin.start;
    const startViews = [rootWin, { start: rootWin.start, end: rootWin.start + Math.min(MIN, rootWidth) }];
    // A selection under the floor is not a real view (move keeps its width), so the half-root view is only used when it is a minute or more.
    if (rootWidth / 2 >= MIN) startViews.push({ start: rootWin.end - rootWidth / 2, end: rootWin.end });
    const points = HOSTILE_POINTS(rootWin);
    for (const mode of ['start', 'end', 'move', 'new']) for (const stepMs of STEPS) for (const startView of startViews) for (const from of points) for (const to of points) {
      const out = b.brushDrag(mode, rootWin, startView, from, to, stepMs);
      const label = `${mode} ${stepMs} ${from}->${to}`;
      if (Number.isFinite(from) && Number.isFinite(to)) assertValidBrush(out, rootWin, label);
      else assert.deepEqual(out, startView, label + ': an unreadable pointer leaves the view alone');
    }
  }
});

test('a brush drag with a step that is zero, negative, NaN or infinite changes nothing, and mode none never moves', () => {
  const view = { start: week.start + DAY, end: week.start + 2 * DAY };
  for (const step of [0, -900_000, NaN, Infinity, -Infinity]) for (const mode of ['start', 'end', 'move', 'new']) {
    assert.deepEqual(b.brushDrag(mode, week, view, week.start, week.end, step), view, mode + ' ' + step);
    assert.deepEqual(b.brushCentre(week, view, week.start + 3 * DAY, step), view, 'centre ' + step);
    assert.equal(b.brushKey('start', 'ArrowRight', week, view, step), null, 'arrow with step ' + step);
  }
  assert.deepEqual(b.brushDrag('none', week, view, 0, 1e12, 900_000), view);
  // A root or view that is not a window is refused, not repaired.
  for (const badRoot of [{ start: 5, end: 5 }, { start: 9, end: 3 }, { start: NaN, end: 1 }, null]) {
    assert.deepEqual(b.brushDrag('new', badRoot, view, 0, 10, 900_000), view);
    assert.equal(b.brushKey('end', 'End', badRoot, view, 900_000), null);
  }
  assert.equal(b.brushKey('end', 'End', week, { start: 4, end: 4 }, 900_000), null);
});

test('dragging a new range backwards, or with no width, grows to the 1 minute floor inside the root', () => {
  const view = { start: week.start + DAY, end: week.start + 2 * DAY };
  const step = 10 * SEC;
  const mid = week.start + 3 * DAY;
  const backwards = b.brushDrag('new', week, view, mid + HOUR, mid, step);
  assert.deepEqual(backwards, { start: mid, end: mid + HOUR });
  const flat = b.brushDrag('new', week, view, mid, mid, step);
  assert.equal(flat.end - flat.start, MIN);
  assert.equal(flat.start, mid);
  // Zero width at the very end of the root grows backwards, not past it.
  const atEnd = b.brushDrag('new', week, view, week.end, week.end, step);
  assert.deepEqual(atEnd, { start: week.end - MIN, end: week.end });
  // A drag that ends outside the track on either side is held at that side.
  assert.deepEqual(b.brushDrag('new', week, view, mid, week.end + 40 * DAY, step), { start: mid, end: week.end });
  assert.deepEqual(b.brushDrag('new', week, view, mid, week.start - 40 * DAY, step), { start: week.start, end: mid });
});

test('dragging an edge across the other edge swaps them instead of inverting the view, and an edge cannot pass the floor', () => {
  const view = { start: week.start + DAY, end: week.start + 2 * DAY };
  const step = 15 * MIN;
  const crossed = b.brushDrag('start', week, view, view.start, view.end + 6 * HOUR, step);
  assert.deepEqual(crossed, { start: view.end, end: view.end + 6 * HOUR });
  const crossedEnd = b.brushDrag('end', week, view, view.end, view.start - 6 * HOUR, step);
  assert.deepEqual(crossedEnd, { start: view.start - 6 * HOUR, end: view.start });
  // Pressing the end edge and landing on the start edge leaves the floor, never zero.
  const squash = b.brushDrag('end', week, view, view.end, view.start, step);
  assert.equal(squash.end - squash.start, MIN);
  assert.ok(squash.start >= week.start && squash.end <= week.end);
});

test('moving the whole selection far off either end of the track keeps its width and sits on the edge', () => {
  const view = { start: week.start + DAY, end: week.start + 2 * DAY };
  const left = b.brushDrag('move', week, view, view.start + HOUR, week.start - 100 * DAY, 15 * MIN);
  assert.deepEqual(left, { start: week.start, end: week.start + DAY });
  const right = b.brushDrag('move', week, view, view.start + HOUR, week.end + 100 * DAY, 15 * MIN);
  assert.deepEqual(right, { start: week.end - DAY, end: week.end });
  // A click on the track far outside it centres on the nearest edge.
  assert.deepEqual(b.brushCentre(week, view, week.end + 9 * DAY, 15 * MIN), { start: week.end - DAY, end: week.end });
  assert.deepEqual(b.brushCentre(week, view, week.start - 9 * DAY, 15 * MIN), { start: week.start, end: week.start + DAY });
  assert.deepEqual(b.brushCentre(week, view, NaN, 15 * MIN), view);
});

test('a window under the 1 minute floor can never come out of any brush action', () => {
  const tiny = { start: week.start + DAY, end: week.start + DAY + 20 * SEC };
  // The view itself cannot be tiny in a real root, but a hostile caller can pass one: every drag still ends at the floor or more.
  for (const mode of ['start', 'end', 'move', 'new']) {
    const out = b.brushDrag(mode, week, tiny, tiny.start, tiny.start + 5 * SEC, 10 * SEC);
    if (mode === 'move') assert.equal(out.end - out.start, 20 * SEC, 'move keeps the width it was handed');
    else assert.ok(out.end - out.start >= MIN, mode);
  }
  // A root narrower than a minute: the floor is the root itself.
  const small = { start: week.start, end: week.start + 30 * SEC };
  assert.deepEqual(b.brushDrag('new', small, small, small.start + 5 * SEC, small.start + 6 * SEC, 10 * SEC), small);
  assert.deepEqual(b.brushKey('start', 'Home', small, small, 10 * SEC), small);
  assert.deepEqual(b.brushKey('end', 'PageUp', small, small, 10 * SEC), small);
});

test('hammering the brush keys from a fixed seed never leaves the root or drops under the floor', () => {
  const rand = seeded(20011022);
  const keys = ['ArrowLeft', 'ArrowRight', 'ArrowUp', 'ArrowDown', 'PageUp', 'PageDown', 'Home', 'End', 'Tab', 'Enter', 'a', ''];
  for (const rootWin of [week, { start: week.start, end: week.start + 90 * SEC }, { start: week.start + 17 * SEC, end: week.start + 2 * HOUR + 3 * SEC }]) {
    for (const stepMs of [10 * SEC, 900_000, 7_200_000]) {
      let view = { start: rootWin.start, end: Math.min(rootWin.end, rootWin.start + 5 * MIN) };
      for (let n = 0; n < 200; n++) {
        const key = keys[Math.floor(rand() * keys.length)];
        const handle = rand() < 0.5 ? 'start' : 'end';
        const next = b.brushKey(handle, key, rootWin, view, stepMs);
        if (['Tab', 'Enter', 'a', ''].includes(key)) { assert.equal(next, null, 'key ' + JSON.stringify(key) + ' is not ours'); continue; }
        assert.ok(next, key + ' returns a window');
        assertValidBrush(next, rootWin, `${n} ${handle} ${key}`);
        view = next;
      }
    }
  }
});

test('PageUp and PageDown pressed a hundred times in a row stay on the track, keep the width and stop at the edges', () => {
  const width = 6 * HOUR;
  let view = { start: week.start + 3 * DAY, end: week.start + 3 * DAY + width };
  for (let n = 0; n < 100; n++) {
    view = b.brushKey('end', 'PageUp', week, view, 900_000);
    assertValidBrush(view, week, 'up ' + n);
    assert.equal(view.end - view.start, width);
  }
  assert.deepEqual(view, { start: week.end - width, end: week.end });
  for (let n = 0; n < 100; n++) {
    view = b.brushKey('start', 'PageDown', week, view, 900_000);
    assertValidBrush(view, week, 'down ' + n);
    assert.equal(view.end - view.start, width);
  }
  assert.deepEqual(view, { start: week.start, end: week.start + width });
  // Both handles do the same thing for a page key, and a whole-root view cannot go anywhere.
  assert.deepEqual(b.brushKey('start', 'PageUp', week, week, 900_000), week);
  assert.deepEqual(b.brushKey('end', 'PageDown', week, week, 900_000), week);
});

test('arrow keys stop one floor apart: the start handle cannot reach the end handle and Home and End send handles to their limits', () => {
  let view = { start: week.start, end: week.start + 5 * MIN };
  for (let n = 0; n < 50; n++) view = b.brushKey('start', 'ArrowRight', week, view, 15 * MIN);
  assert.equal(view.end - view.start, MIN, 'the start handle stops one floor before the end handle');
  assert.equal(view.end, week.start + 5 * MIN);
  for (let n = 0; n < 50; n++) view = b.brushKey('end', 'ArrowLeft', week, view, 15 * MIN);
  assert.equal(view.end - view.start, MIN);
  assert.deepEqual(b.brushKey('start', 'Home', week, view, 15 * MIN), { start: week.start, end: view.end });
  assert.deepEqual(b.brushKey('end', 'End', week, view, 15 * MIN), { start: view.start, end: week.end });
  assert.deepEqual(b.brushKey('end', 'Home', week, view, 15 * MIN), { start: view.start, end: view.start + MIN });
  assert.deepEqual(b.brushKey('start', 'End', week, view, 15 * MIN), { start: view.end - MIN, end: view.end });
  // The edges at the very ends of the root do not wrap round.
  const full = week;
  assert.deepEqual(b.brushKey('start', 'ArrowLeft', week, full, 15 * MIN), full);
  assert.deepEqual(b.brushKey('end', 'ArrowRight', week, full, 15 * MIN), full);
});

test('brushHit on a track that was pressed outside it, or on a collapsed selection, still gives one answer', () => {
  const sel = { left: 0.5, right: 0.5 };
  for (const x of [-5, 0, 0.5, 1, 7]) assert.ok(['start', 'end', 'move', 'new'].includes(b.brushHit(x, sel, 0.01)), String(x));
  assert.equal(b.brushHit(-5, { left: 0.3, right: 0.7 }, 0.01), 'new');
  assert.equal(b.brushHit(9, { left: 0.3, right: 0.7 }, 0.01), 'new');
  // With nothing finite to compare, nothing is grabbed as an edge or the body.
  assert.equal(b.brushHit(NaN, { left: 0.3, right: 0.7 }, 0.01), 'new');
  // A big handle on a thin selection prefers the nearer edge.
  assert.equal(b.brushHit(0.31, { left: 0.3, right: 0.32 }, 0.05), 'start');
  assert.equal(b.brushHit(0.319, { left: 0.3, right: 0.32 }, 0.05), 'end');
});

// ---------------------------------------------------------------------------------------------------------------------
// Interval choice and the 120 point cap
// ---------------------------------------------------------------------------------------------------------------------
function assertCapped(view, bucket, label) {
  const ref = z.referenceFor(view);
  assert.ok(countEpochBuckets(view.start, view.end, bucket.seconds) <= 120, label + ' view points');
  assert.ok(countEpochBuckets(ref.start, ref.end, bucket.seconds) <= 120, label + ' reference points');
  assert.ok(z.slotsOf(view, bucket.seconds).length <= 120, label + ' slots');
  assert.ok(bucket.seconds >= z.MIN_BUCKET_SECONDS && bucket.seconds <= z.MAX_BUCKET_SECONDS, label + ' bounds');
}

test('every ladder rung keeps both the view and its reference at 120 points, aligned or off by a millisecond', () => {
  for (const rung of LADDER) {
    const width = 120 * rung * SEC;
    const alignedStart = Math.ceil(week.start / (rung * SEC)) * rung * SEC;
    const aligned = { start: alignedStart, end: alignedStart + width };
    const exact = z.bucketForView(aligned);
    assert.ok(exact, 'aligned ' + rung);
    assert.equal(exact.seconds, rung, 'an exact 120 slot window lands on its own rung ' + rung);
    assertCapped(aligned, exact, 'aligned ' + rung);
    // One millisecond later makes it 121 slots: the next rung (or none, past one day).
    const nudged = { start: alignedStart + 1, end: alignedStart + 1 + width };
    const next = z.bucketForView(nudged);
    if (rung === 86_400) assert.equal(next, null, 'nothing is wider than a day');
    else { assert.ok(next && next.seconds > rung, 'nudged ' + rung); assertCapped(nudged, next, 'nudged ' + rung); }
  }
});

test('a random view of any width from 1 minute to 7 days, at any offset, is drawn with at most 120 points and a typed interval never breaks that', () => {
  const rand = seeded(7);
  const manuals = [null, undefined, NaN, Infinity, -Infinity, 0, -30, 1, 9, 10, 11, 45.5, 3600, 86_400, 1e9];
  for (let n = 0; n < 400; n++) {
    const width = MIN + Math.floor(rand() * (7 * DAY - MIN));
    const start = week.start + Math.floor(rand() * 1e9);
    const view = { start, end: start + width };
    for (const manualSeconds of manuals) {
      const bucket = z.bucketForView(view, { manualSeconds });
      assert.ok(bucket, `${width} ${manualSeconds}`);
      assertCapped(view, bucket, `${width} ${manualSeconds}`);
      const typed = typeof manualSeconds === 'number' && Number.isFinite(manualSeconds) && manualSeconds > 0;
      assert.equal(bucket.source, typed ? 'manual' : 'automatic');
      if (typed && bucket.raised) assert.ok(bucket.seconds > Math.min(86_400, Math.max(10, Math.round(manualSeconds))), 'raised means longer than asked');
    }
  }
});

test('a view that cannot be read has no interval and no slots', () => {
  for (const bad of [{ start: NaN, end: 5 }, { start: 5, end: 5 }, { start: 9, end: 3 }, null, undefined, { start: 0, end: Infinity }]) {
    assert.equal(z.bucketForView(bad), null);
    assert.deepEqual(z.slotsOf(bad, 60), []);
  }
  assert.deepEqual(z.slotsOf(week, 1e-9).length <= 120, true);
  assert.equal(z.slotsOf(week, 1e-9).length, 120, 'a tiny interval is capped, not run away');
});

// ---------------------------------------------------------------------------------------------------------------------
// The stack: drags, scale and keys
// ---------------------------------------------------------------------------------------------------------------------
test('drill from the whole week by dragging a fifth of the view over and over never passes 120 bars or goes under the floor', () => {
  let stack = z.createStack(week);
  const widths = [z.widthOf(week)];
  for (let n = 0; n < 40; n++) {
    const view = z.currentOf(stack);
    const bucket = z.bucketForView(view);
    assert.ok(bucket, 'level ' + n);
    assertCapped(view, bucket, 'level ' + n);
    assert.ok(z.widthOf(view) >= z.MIN_VIEW_MS, 'floor at level ' + n);
    assert.ok(view.start >= week.start && view.end <= week.end, 'inside the root at level ' + n);
    const from = view.start + z.widthOf(view) * 0.4, to = view.start + z.widthOf(view) * 0.6;
    const next = z.zoomToRange(stack, from, to, bucket.seconds);
    if (next === stack) break;
    assert.ok(z.widthOf(z.currentOf(next)) < z.widthOf(view), 'each drag zooms in');
    stack = next;
    widths.push(z.widthOf(view));
  }
  assert.ok(widths.length >= 5, 'it takes several drags to get to the bottom: ' + widths.length);
  assert.ok(z.widthOf(z.currentOf(stack)) >= z.MIN_VIEW_MS);
  // Another drag at the bottom does nothing, whatever the pointer does.
  const bottom = z.currentOf(stack);
  for (const [f, t] of [[bottom.start, bottom.end], [NaN, 5], [bottom.end, bottom.start], [-1e15, 1e15]]) assert.equal(z.zoomToRange(stack, f, t, 10), stack, f + ' ' + t);
});

test('zoom in until the floor, keep hammering, then zoom out until the whole week: every step is valid and the end is the root', () => {
  let stack = z.createStack(week);
  let steps = 0;
  while (z.canScale(stack, 'in') && steps < 60) {
    const next = z.scale(stack, 'in');
    assert.notEqual(next, stack);
    stack = next; steps++;
    const view = z.currentOf(stack);
    assert.ok(z.widthOf(view) >= z.MIN_VIEW_MS, 'floor at step ' + steps);
    assert.ok(view.start >= week.start && view.end <= week.end);
    assertCapped(view, z.bucketForView(view), 'in ' + steps);
  }
  assert.ok(steps > 8 && steps < 20, 'a week halves a dozen times before it meets a minute: ' + steps);
  for (let n = 0; n < 50; n++) assert.equal(z.scale(stack, 'in'), stack, 'extra zoom in at the floor changes nothing');
  assert.equal(z.zoomBlockedNotice(stack, null, 'UTC'), zoomCopy.minuteFloor);
  let outs = 0;
  while (!z.isRoot(stack) && outs < 80) {
    stack = z.scale(stack, 'out'); outs++;
    const view = z.currentOf(stack);
    assert.ok(view.start >= week.start && view.end <= week.end, 'out inside the root at step ' + outs);
    assertCapped(view, z.bucketForView(view), 'out ' + outs);
  }
  assert.ok(z.isRoot(stack), 'zooming out reaches the whole period');
  assert.ok(outs <= steps + 1, 'out takes no more steps than in (plus the last jump to the root): ' + outs + ' vs ' + steps);
  for (let n = 0; n < 50; n++) assert.equal(z.scale(stack, 'out'), stack, 'extra zoom out at the root changes nothing');
  assert.deepEqual(z.currentOf(stack), week);
});

test('the stack ignores unreadable, empty, floor-breaking and outside windows, and a bad jump or an empty Back', () => {
  const top = z.createStack(week);
  const mid = week.start + 2 * DAY;
  const refused = [
    null, undefined, {}, { start: NaN, end: NaN }, { start: mid, end: mid }, { start: mid + HOUR, end: mid },
    { start: mid, end: mid + 59_999 }, { start: week.end + DAY, end: week.end + 2 * DAY }, { start: week.start - 2 * DAY, end: week.start - DAY }
  ];
  for (const win of refused) { assert.equal(z.push(top, win), top, 'push ' + JSON.stringify(win)); assert.equal(z.setTop(top, win), top, 'setTop ' + JSON.stringify(win)); }
  // A window that pokes out of the root: push cuts it to the root, setTop refuses it whole.
  const poke = { start: week.end - HOUR, end: week.end + 5 * DAY };
  assert.deepEqual(z.currentOf(z.push(top, poke)), { start: week.end - HOUR, end: week.end });
  assert.equal(z.setTop(top, poke), top);
  const deep = z.push(z.push(top, { start: mid, end: mid + 6 * HOUR }), { start: mid + HOUR, end: mid + 2 * HOUR });
  assert.equal(deep.frames.length, 2);
  for (const bad of [-1, 3, 99, NaN, 1.5, Infinity, -Infinity]) assert.equal(z.jumpTo(deep, bad), deep, 'jumpTo ' + bad);
  assert.equal(z.jumpTo(deep, 2), deep);
  assert.equal(z.jumpTo(deep, 1).frames.length, 1);
  assert.equal(z.jumpTo(deep, 0).frames.length, 0);
  assert.equal(z.back(top), top, 'Back at the root is the same stack');
  assert.equal(z.reset(top), top);
  assert.equal(z.createStack(null), null);
  assert.equal(z.createStack({ start: 5, end: 5 }), null);
  // The same window as now, or one step above, never grows the trail.
  assert.equal(z.push(deep, z.currentOf(deep)), deep);
  assert.equal(z.setTop(deep, z.currentOf(deep)), deep);
  // Pushing the whole root from deep inside goes home.
  assert.equal(z.push(deep, week).frames.length, 0);
});

test('shift hammered both ways stays inside the root, moves by whole widths and does nothing at the root', () => {
  const top = z.createStack(week);
  assert.equal(z.shift(top, 1, 'UTC'), top);
  assert.equal(z.shift(top, -1, 'UTC'), top);
  const mid = week.start + 3 * DAY;
  let stack = z.push(top, { start: mid + 7 * MIN, end: mid + 7 * MIN + 2 * HOUR });
  for (let n = 0; n < 200; n++) {
    stack = z.shift(stack, 1, 'UTC');
    const view = z.currentOf(stack);
    assert.ok(view.end <= week.end && view.start >= week.start);
    assert.ok(z.widthOf(view) >= z.MIN_VIEW_MS);
  }
  assert.equal(z.currentOf(stack).end, week.end);
  assert.equal(z.shift(stack, 1, 'UTC'), stack, 'nothing later than the root');
  assert.ok(!z.canShift(stack, 1, 'UTC'));
  for (let n = 0; n < 200; n++) stack = z.shift(stack, -1, 'UTC');
  assert.equal(z.currentOf(stack).start, week.start);
  assert.equal(z.shift(stack, -1, 'UTC'), stack);
  assert.ok(stack.frames.length <= 2, 'shifting replaces the top step and never piles up the trail: ' + stack.frames.length);
  // A zone nobody has heard of reads as UTC rather than throwing.
  assert.doesNotThrow(() => z.shift(z.push(top, { start: mid, end: mid + DAY }), 1, 'Not/AZone'));
});

test('chart keys: modified keys are left to the page, unknown keys are ignored, and the cursor is clamped for any index or count', () => {
  for (const key of ['ArrowLeft', 'Enter', '+', 'Escape', 'Backspace']) {
    for (const modifiers of [{ ctrl: true }, { meta: true }, { alt: true }, { ctrl: true, alt: true }]) assert.equal(z.chartKeyAction(key, modifiers), null, key);
  }
  for (const key of ['', 'a', 'Tab', 'PageUp', ' ', 'arrowleft', 'F5']) assert.equal(z.chartKeyAction(key), null, JSON.stringify(key));
  const right = z.chartKeyAction('ArrowRight'), left = z.chartKeyAction('ArrowLeft');
  for (const count of [0, -3, NaN, 1.5, Infinity]) for (const index of [-1, 0, 5]) assert.equal(z.moveCursor(index, right, count), -1, `count ${count}`);
  for (const index of [-5, -1, 0, 1, 5, 99, NaN, 1.5, Infinity]) {
    for (const action of [left, right, z.chartKeyAction('Home'), z.chartKeyAction('End')]) {
      const out = z.moveCursor(index, action, 6);
      assert.ok(Number.isInteger(out) && out >= 0 && out < 6, `${index} ${JSON.stringify(action)} -> ${out}`);
    }
  }
  // Hammer right and left and the cursor sits on the first and last slot.
  let cursor = -1;
  for (let n = 0; n < 50; n++) cursor = z.moveCursor(cursor, right, 6);
  assert.equal(cursor, 5);
  for (let n = 0; n < 50; n++) cursor = z.moveCursor(cursor, left, 6);
  assert.equal(cursor, 0);
  // Enter on a cursor that points at no slot, or on a slot with nothing deeper, changes nothing.
  const top = z.createStack(week);
  const slots = z.slotsOf(week, 3600);
  const enter = z.chartKeyAction('Enter');
  for (const cursorAt of [-1, 9999, NaN]) assert.equal(z.applyChartKey(top, enter, { slots, cursor: cursorAt, zone: 'UTC' }), top);
  assert.equal(z.applyChartKey(top, z.chartKeyAction('Backspace'), { slots, cursor: 0, zone: 'UTC' }), top, 'Back at the root');
  assert.equal(z.applyChartKey(top, z.chartKeyAction('-'), { slots, cursor: 0, zone: 'UTC' }), top, 'zoom out at the root');
  assert.equal(z.applyChartKey(top, left, { slots, cursor: 0, zone: 'UTC' }), top, 'a cursor key never edits the stack');
});

test('Enter, pressed repeatedly on the middle slot, drills week to day to hour to minute and then explains the floor', () => {
  let stack = z.createStack(week);
  const seen = [];
  for (let n = 0; n < 8; n++) {
    const view = z.currentOf(stack);
    const bucket = z.bucketForView(view);
    const slots = z.slotsOf(view, bucket.seconds);
    const cursor = Math.floor(slots.length / 2);
    const next = z.applyChartKey(stack, z.chartKeyAction('Enter'), { slots, cursor, zone: 'UTC' });
    if (next === stack) {
      assert.equal(z.zoomBlockedNotice(stack, slots[cursor], 'UTC') !== '', true, 'a refused Enter says why');
      break;
    }
    stack = next;
    seen.push(z.levelOf(stack));
    assertCapped(z.currentOf(stack), z.bucketForView(z.currentOf(stack)), 'level ' + n);
  }
  assert.deepEqual(seen.slice(0, 3), ['day', 'hour', 'minute']);
  assert.equal(z.levelOf(stack), 'minute');
  assert.equal(z.currentOf(stack).end - z.currentOf(stack).start, MIN);
});

test('the drag readout never throws for a pointer outside the slots, NaN or backwards, and says the range is empty when it is', () => {
  const view = week;
  const slots = z.slotsOf(view, 7200);
  for (const [a, c] of [[-5, 1e6], [NaN, 3], [3, NaN], [10, 2], [Infinity, -Infinity], [0, 0]]) {
    const out = z.dragReadout(slots, a, c, 7200, view, 'UTC');
    assert.equal(typeof out.text, 'string');
    assert.equal(typeof out.ok, 'boolean');
  }
  assert.deepEqual(z.dragReadout([], 0, 1, 7200, view, 'UTC'), { text: '', ok: false });
  assert.deepEqual(z.dragReadout(slots, 0, slots.length - 1, 7200, view, 'UTC'), { text: zoomCopy.readoutWhole, ok: false });
});

// ---------------------------------------------------------------------------------------------------------------------
// Investigate this window: promotion, the trail and Back
// ---------------------------------------------------------------------------------------------------------------------
const fieldsOf = (startMs, endMs, bucketSeconds) => {
  const width = endMs - startMs;
  return { start: iso(startMs), end: iso(endMs), baselineStart: iso(startMs - width), baselineEnd: iso(startMs), bucketSeconds: bucketSeconds ?? bucketForWindows(iso(startMs), iso(endMs), iso(startMs - width), iso(startMs)) };
};
const weekFields = () => fieldsOf(week.start, week.end);
const sources = ['source-1'];
const asQuery = (patch) => ({ sourceIds: sources, ...patch });

test('Investigate at the root, with no view at all, is refused with the root reason and changes nothing', () => {
  const current = weekFields();
  const out = p.planInvestigation({ current, view: null, runLoading: false, stale: false, trail: [] });
  assert.deepEqual(out, { refused: zoomCopy.investigateTop });
  assert.equal(p.promotedScope(current, null), null);
  assert.equal(p.promotedScope(current, undefined), null);
  assert.equal(p.promotedScope(current, {}), null);
  // The root reason wins over a run in progress and over unapplied changes.
  for (const [runLoading, stale] of [[true, false], [false, true], [true, true]]) assert.deepEqual(p.planInvestigation({ current, view: null, runLoading, stale, trail: [] }), { refused: zoomCopy.investigateTop });
  assert.deepEqual(p.investigateState({ isRoot: true, runLoading: true, stale: true }), { enabled: false, reason: zoomCopy.investigateTop });
  assert.deepEqual(p.investigateState({ isRoot: false, runLoading: true, stale: true }), { enabled: false, reason: zoomCopy.investigateBusy });
  assert.deepEqual(p.investigateState({ isRoot: false, runLoading: false, stale: true }), { enabled: false, reason: zoomCopy.investigateStale });
  assert.deepEqual(p.investigateState({ isRoot: false, runLoading: false, stale: false }), { enabled: true, reason: '' });
});

test('Investigate while loading or stale is refused with a reason and a second click leaves the trail alone', () => {
  const current = weekFields();
  const lens = { start: week.start + 2 * DAY, end: week.start + 2 * DAY + 6 * HOUR };
  const trail = [];
  const first = p.planInvestigation({ current, view: lens, runLoading: false, stale: false, trail });
  assert.ok(first.patch && first.trail.length === 1);
  // The double click: the run has started, so the second click is refused before it can add a second entry.
  const second = p.planInvestigation({ current, view: lens, runLoading: true, stale: false, trail: first.trail });
  assert.deepEqual(second, { refused: zoomCopy.investigateBusy });
  assert.deepEqual(p.planInvestigation({ current, view: lens, runLoading: false, stale: true, trail: first.trail }), { refused: zoomCopy.investigateStale });
  assert.equal(first.trail.length, 1);
  assert.deepEqual(trail, [], 'the input trail is never edited in place');
});

test('Investigate on the window already on screen, on a window that covers it, a 30 second window and a window outside it is refused', () => {
  const current = weekFields();
  const inside = week.start + 2 * DAY;
  const cases = {
    'the same window': { start: week.start, end: week.end },
    'a wider window': { start: week.start - DAY, end: week.end + DAY },
    'thirty seconds': { start: inside, end: inside + 30 * SEC },
    'one second under a minute': { start: inside, end: inside + 59 * SEC },
    'outside, before': { start: week.start - 3 * DAY, end: week.start - DAY },
    'outside, after': { start: week.end + DAY, end: week.end + 3 * DAY },
    'touching the start only': { start: week.start - HOUR, end: week.start + 20 * SEC },
    'NaN edge': { start: NaN, end: inside },
    'backwards': { start: inside + HOUR, end: inside },
    'empty': { start: inside, end: inside }
  };
  for (const [name, view] of Object.entries(cases)) {
    assert.equal(p.promotedScope(current, view), null, name);
    const plan = p.planInvestigation({ current, view, runLoading: false, stale: false, trail: [] });
    assert.ok('refused' in plan, name);
    if (name !== 'NaN edge' && name !== 'backwards' && name !== 'empty') assert.equal(plan.refused, zoomCopy.investigateCannot, name);
  }
  // Exactly one minute is allowed.
  const minute = p.promotedScope(current, { start: inside, end: inside + MIN });
  assert.ok(minute);
  assert.equal(minute.end, iso(inside + MIN));
  // A current window that cannot be read cannot be promoted from.
  for (const bad of [{ ...current, start: 'not a date' }, { ...current, end: '' }, { ...current, start: current.end, end: current.start }]) {
    assert.equal(p.promotedScope(bad, { start: inside, end: inside + HOUR }), null);
  }
});

test('a lens wider than 7 days is refused even from a current window that is wider', () => {
  const wide = fieldsOf(week.start, week.start + 9 * DAY, 3600);
  assert.equal(p.promotedScope(wide, { start: week.start, end: week.start + 8 * DAY }), null);
  assert.ok(p.promotedScope(wide, { start: week.start, end: week.start + 7 * DAY }));
});

test('a promoted window keeps every edge a whole second and passes scopeProblem, for 500 random lenses with milliseconds', () => {
  const rand = seeded(1001);
  let promoted = 0;
  for (let n = 0; n < 500; n++) {
    const current = rand() < 0.5 ? weekFields() : fieldsOf(week.start + 3 * HOUR, week.start + 3 * HOUR + 2 * DAY + 1 * SEC);
    const now = p.rootOf(current);
    const width = MIN + Math.floor(rand() * (z.widthOf(now) - MIN));
    const start = now.start + Math.floor(rand() * (z.widthOf(now) - width)) + (rand() < 0.5 ? 0.5 : 0) + Math.floor(rand() * 1000) * (rand() < 0.5 ? 1 : 0);
    const lens = { start, end: start + width + Math.floor(rand() * 1000) };
    const patch = p.promotedScope(current, lens);
    if (!patch) continue;
    promoted++;
    for (const key of ['start', 'end', 'baselineStart', 'baselineEnd']) {
      assert.match(patch[key], /^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d\.000Z$/, key + ' is a whole-second ISO');
      assert.equal(Date.parse(patch[key]) % SEC, 0);
    }
    const s = Date.parse(patch.start), e = Date.parse(patch.end);
    assert.ok(s >= Math.ceil(now.start / SEC) * SEC && e <= Math.floor(now.end / SEC) * SEC, 'held inside the current window');
    assert.ok(s <= Math.ceil(lens.start / SEC) * SEC && e >= Math.min(Math.ceil(lens.end / SEC) * SEC, Math.floor(now.end / SEC) * SEC), 'rounded outward, never clipped inward');
    assert.ok(e - s >= MIN);
    assert.equal(patch.baselineEnd, patch.start, 'the reference ends where the window starts');
    assert.equal(Date.parse(patch.baselineEnd) - Date.parse(patch.baselineStart), e - s, 'and has the same length');
    assert.equal(scopeProblem(asQuery(patch), true), null, JSON.stringify(patch));
  }
  assert.ok(promoted > 400, 'most random lenses are promotable: ' + promoted);
});

test('a typed interval that no longer fits, or is not a whole number, is replaced by the app rule on promotion; one that fits is kept', () => {
  const lens = { start: week.start + 2 * DAY, end: week.start + 2 * DAY + 6 * HOUR };
  const typed = (seconds) => ({ ...weekFields(), bucketSeconds: seconds });
  // 7200 is the app's own pick for the week, so it is not "typed"; use a different value the user chose.
  const kept = p.promotedScope(typed(600), lens);
  assert.equal(kept.bucketSeconds, 600, 'a typed 10 minutes still fits 6 hours (36 points)');
  for (const bad of [NaN, 1.5, Infinity, -3, 0]) {
    const out = p.promotedScope(typed(bad), lens);
    assert.equal(out.bucketSeconds, bucketForWindows(out.start, out.end, out.baselineStart, out.baselineEnd), 'app rule for ' + bad);
    assert.equal(scopeProblem(asQuery(out), true), null);
  }
  // A typed interval as long as the new window or longer would give one bar: not kept.
  const long = p.promotedScope(typed(86_400), { start: week.start + DAY, end: week.start + DAY + 2 * HOUR });
  assert.notEqual(long.bucketSeconds, 86_400);
  assert.equal(scopeProblem(asQuery(long), true), null);
});

test('promote, Back, promote again keeps the trail consistent and backTarget matches each step', () => {
  const lensOf = (win) => ({ start: win.start + HOUR, end: win.start + HOUR + Math.floor((win.end - win.start) / 2) });
  let current = weekFields();
  let trail = [];
  const originals = [];
  for (let step = 0; step < 4; step++) {
    const lens = lensOf(p.rootOf(current));
    const plan = p.planInvestigation({ current, view: lens, runLoading: false, stale: false, trail });
    assert.ok(plan.patch, 'step ' + step);
    originals.push(current);
    current = plan.patch;
    trail = plan.trail;
    assert.equal(trail.length, step + 1);
    const target = p.backTarget(trail, current);
    assert.equal(target, trail[trail.length - 1], 'Back right after promoting points at the last entry');
    assert.deepEqual(target.from, originals[step]);
    assert.deepEqual(target.lens, lens);
  }
  // Back is only offered while the window is still the promoted one.
  assert.equal(p.backTarget(trail, { start: current.start, end: iso(Date.parse(current.end) + SEC) }), null, 'the user moved the end');
  assert.equal(p.backTarget(trail, { start: iso(Date.parse(current.start) - SEC), end: current.end }), null, 'the user moved the start');
  assert.equal(p.backTarget([], current), null);
  assert.equal(p.backTarget(trail, { start: '', end: '' }), null);
  // Walk all the way Back, checking the label and the restored window at each step.
  for (let step = 3; step >= 0; step--) {
    const target = p.backTarget(trail, current);
    assert.ok(target, 'back ' + step);
    const seconds = (Date.parse(originals[step].end) - Date.parse(originals[step].start));
    assert.equal(p.backLabel(target), 'Back to ' + z.durationLabel(seconds), 'label ' + step);
    const popped = p.popTrail(trail);
    assert.equal(popped.entry, target);
    trail = popped.trail;
    current = popped.entry.from;
    assert.deepEqual(current, originals[step], 'Back restores exactly what was there, interval included');
  }
  assert.deepEqual(trail, []);
  // Promote again from the restored week: a fresh trail of one.
  const again = p.planInvestigation({ current, view: lensOf(p.rootOf(current)), runLoading: false, stale: false, trail });
  assert.equal(again.trail.length, 1);
  assert.equal(p.backTarget(again.trail, again.patch), again.trail[0]);
});

test('Back label names the length of the window it returns to, in the plain words of the strip', () => {
  const entry = (startMs, endMs) => ({ from: fieldsOf(startMs, endMs), to: { start: iso(startMs), end: iso(startMs + MIN) }, lens: { start: startMs, end: startMs + MIN } });
  assert.equal(p.backLabel(entry(week.start, week.end)), 'Back to 7 days');
  assert.equal(p.backLabel(entry(week.start, week.start + DAY)), 'Back to 24 hours');
  assert.equal(p.backLabel(entry(week.start, week.start + 15 * MIN)), 'Back to 15 minutes');
  assert.equal(p.backLabel(entry(week.start, week.start + 2 * MIN)), 'Back to 2 minutes');
  assert.equal(p.backLabel(entry(week.start, week.start + MIN)), 'Back to 60 seconds', 'under 90 seconds the strip counts in seconds');
});

test('the trail keeps the newest 10 when it overflows, never edits its input, and popTrail on an empty trail is safe', () => {
  const entry = (n) => ({ from: fieldsOf(week.start, week.end), to: { start: iso(week.start + n * MIN), end: iso(week.start + (n + 1) * MIN) }, lens: { start: week.start + n * MIN, end: week.start + (n + 1) * MIN } });
  assert.equal(p.TRAIL_LIMIT, 10);
  let trail = [];
  for (let n = 0; n < 25; n++) {
    const before = trail;
    const snapshot = JSON.stringify(before);
    trail = p.pushTrail(trail, entry(n));
    assert.equal(JSON.stringify(before), snapshot, 'input untouched at ' + n);
    assert.ok(trail.length <= 10);
    assert.equal(trail[trail.length - 1].lens.start, week.start + n * MIN);
  }
  assert.equal(trail.length, 10);
  assert.deepEqual(trail.map((item) => (item.lens.start - week.start) / MIN), [15, 16, 17, 18, 19, 20, 21, 22, 23, 24]);
  for (let n = 0; n < 10; n++) {
    const popped = p.popTrail(trail);
    assert.ok(popped.entry);
    assert.equal(popped.trail.length, trail.length - 1);
    assert.equal(trail.length, 10 - n, 'popTrail does not edit its input');
    trail = popped.trail;
  }
  for (let n = 0; n < 5; n++) {
    const popped = p.popTrail(trail);
    assert.equal(popped.entry, null);
    assert.deepEqual(popped.trail, []);
    trail = popped.trail;
  }
  assert.equal(p.backTarget(trail, { start: iso(week.start), end: iso(week.end) }), null);
});

test('twelve promotions in a row, each into the first half of the last, overflow the trail yet Back keeps working to the oldest kept window', () => {
  let current = weekFields();
  let trail = [];
  const windows = [current];
  for (let n = 0; n < 12; n++) {
    const now = p.rootOf(current);
    const half = Math.floor((now.end - now.start) / 2 / SEC) * SEC;
    const plan = p.planInvestigation({ current, view: { start: now.start, end: now.start + half }, runLoading: false, stale: false, trail });
    assert.ok(plan.patch, 'promotion ' + n + ' of ' + (now.end - now.start) / MIN + ' minutes');
    assert.equal(scopeProblem(asQuery(plan.patch), true), null, 'promotion ' + n);
    current = plan.patch; trail = plan.trail; windows.push(current);
    assert.ok(trail.length <= 10);
  }
  assert.equal(trail.length, 10);
  for (let n = 0; n < 10; n++) {
    const target = p.backTarget(trail, current);
    assert.ok(target, 'Back ' + n);
    assert.deepEqual(target.from, windows[windows.length - 2 - n], 'Back goes to the window before, ' + n);
    ({ entry: { from: current }, trail } = p.popTrail(trail));
  }
  assert.deepEqual(current, windows[2], 'the two oldest steps fell off the trail, so Back stops at the third window');
  assert.equal(p.backTarget(trail, current), null, 'then there is nothing to go back to');
});

test('every new zoom string passes the copy lint', () => {
  const entries = allZoomStrings().map((text, index) => ({ id: 'zoom' + index, text }));
  assert.ok(entries.length >= 30);
  assert.deepEqual(lintScreenStrings(entries, deck.terms), []);
});
