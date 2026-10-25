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

const z = loadTypeScript('src/lib/zoom.ts');
const { zoomCopy, allZoomStrings } = loadTypeScript('src/lib/copy/zoomCopy.ts');
const { lintScreenStrings } = loadTypeScript('src/lib/copy/copylint.ts');
const { deck } = loadTypeScript('src/lib/copy/deck.ts');

const MIN = 60_000, HOUR = 3_600_000, DAY = 86_400_000;
const at = (text) => Date.parse(text);
// Synthetic windows only: October 2001, a week from Monday 22 Oct to Monday 29 Oct (UTC).
const week = { start: at('2001-10-22T00:00:00Z'), end: at('2001-10-29T00:00:00Z') };

test('level comes from the window width', () => {
  assert.equal(z.levelForWidth(7 * DAY), 'window');
  assert.equal(z.levelForWidth(36 * HOUR + 1), 'window');
  assert.equal(z.levelForWidth(36 * HOUR), 'day');
  assert.equal(z.levelForWidth(25 * HOUR), 'day');
  assert.equal(z.levelForWidth(90 * MIN + 1), 'day');
  assert.equal(z.levelForWidth(90 * MIN), 'hour');
  assert.equal(z.levelForWidth(HOUR), 'hour');
  assert.equal(z.levelForWidth(5 * MIN + 1), 'hour');
  assert.equal(z.levelForWidth(5 * MIN), 'minute');
  assert.equal(z.levelForWidth(MIN), 'minute');
  for (const bad of [0, -1, NaN, Infinity, -Infinity]) assert.equal(z.levelForWidth(bad), null, String(bad));
});

test('a window of zero, negative or unreadable length is refused everywhere', () => {
  const bad = [{ start: 5, end: 5 }, { start: 9, end: 3 }, { start: NaN, end: 4 }, { start: 1, end: NaN }, { start: 1, end: Infinity }, null, undefined, {}, { start: '1', end: '2' }];
  for (const win of bad) {
    assert.equal(z.isWindow(win), false, JSON.stringify(win));
    assert.equal(z.createStack(win), null);
    assert.equal(z.bucketForView(win), null);
    assert.deepEqual(z.slotsOf(win, 60), []);
  }
  const stack = z.createStack(week);
  for (const win of bad) assert.equal(z.push(stack, win), stack, 'push ignores ' + JSON.stringify(win));
  assert.deepEqual(z.slotsOf(week, 0), []);
  assert.deepEqual(z.slotsOf(week, NaN), []);
  assert.equal(z.snapRange(NaN, 5, 60, week), null);
  assert.equal(z.snapRange(0, 5, 0, week), null);
});

test('a local day is 24 hours, and 23 or 25 across a clock change', () => {
  const ny = (text) => z.dayWindow(at(text), 'America/New_York');
  // New York 2001: clocks go forward on 1 Apr (23 hour day) and back on 28 Oct (25 hour day).
  const spring = ny('2001-04-01T12:00:00Z');
  assert.equal(spring.end - spring.start, 23 * HOUR);
  assert.equal(new Date(spring.start).toISOString(), '2001-04-01T05:00:00.000Z');
  const fall = ny('2001-10-28T12:00:00Z');
  assert.equal(fall.end - fall.start, 25 * HOUR);
  assert.equal(new Date(fall.start).toISOString(), '2001-10-28T04:00:00.000Z');
  const plain = ny('2001-10-22T12:00:00Z');
  assert.equal(plain.end - plain.start, 24 * HOUR);
  const london = z.dayWindow(at('2001-10-28T12:00:00Z'), 'Europe/London');
  assert.equal(london.end - london.start, 25 * HOUR);
  const utc = z.dayWindow(at('2001-10-28T12:00:00Z'), 'UTC');
  assert.deepEqual(utc, { start: at('2001-10-28T00:00:00Z'), end: at('2001-10-29T00:00:00Z') });
  // An instant inside the repeated hour still lands in the 25 hour day.
  const repeated = ny('2001-10-28T05:30:00Z');
  assert.deepEqual(repeated, fall);
});

test('days around every clock change in several zones are contiguous and 23 to 25 hours long', () => {
  for (const zone of ['America/New_York', 'Europe/London', 'Australia/Lord_Howe', 'America/Sao_Paulo', 'Asia/Kolkata', 'Pacific/Auckland', 'UTC']) {
    let cursor = at('2001-03-20T00:00:00Z');
    let previous = null;
    const last = at('2001-11-10T00:00:00Z');
    let guard = 0;
    while (cursor < last && guard++ < 400) {
      const day = z.dayWindow(cursor, zone);
      assert.ok(day.start <= cursor && cursor < day.end, zone + ' contains ' + new Date(cursor).toISOString());
      const width = day.end - day.start;
      assert.ok(width >= 23 * HOUR - 30 * MIN && width <= 25 * HOUR, zone + ' width ' + width / HOUR);
      if (previous) assert.equal(day.start, previous.end, zone + ' days touch');
      previous = day;
      cursor = day.end;
    }
    assert.ok(guard > 200, zone);
  }
});

test('an hour is the local clock hour, also in the repeated hour and half-hour zones', () => {
  const second = z.hourWindow(at('2001-10-28T06:30:00Z'), 'America/New_York'); // 01:30 the second time, EST
  assert.equal(second.end - second.start, HOUR);
  assert.equal(new Date(second.start).toISOString(), '2001-10-28T06:00:00.000Z');
  const kolkata = z.hourWindow(at('2001-10-22T10:10:00Z'), 'Asia/Kolkata'); // 15:40 local: the hour is 15:00 to 16:00 local = 09:30Z to 10:30Z
  assert.equal(new Date(kolkata.start).toISOString(), '2001-10-22T09:30:00.000Z');
  assert.equal(z.hourWindow(NaN, 'UTC'), null);
  assert.deepEqual(z.minuteWindow(at('2001-10-22T10:10:42Z')), { start: at('2001-10-22T10:10:00Z'), end: at('2001-10-22T10:11:00Z') });
});

test('the window of a clicked bucket is its day, then its hour, then its minute, and stops there', () => {
  const stack0 = z.createStack(week);
  const daySlots = z.slotsOf(week, 86_400);
  assert.equal(daySlots.length, 7);
  const day = z.childWindow(week, daySlots[2], 'UTC', week);
  assert.deepEqual(day, { start: at('2001-10-24T00:00:00Z'), end: at('2001-10-25T00:00:00Z') });
  const stack1 = z.zoomIntoSlot(stack0, daySlots[2], 'UTC');
  assert.equal(z.levelOf(stack1), 'day');
  const hourSlots = z.slotsOf(z.currentOf(stack1), 3600);
  assert.equal(hourSlots.length, 24);
  const stack2 = z.zoomIntoSlot(stack1, hourSlots[14], 'UTC');
  assert.deepEqual(z.currentOf(stack2), { start: at('2001-10-24T14:00:00Z'), end: at('2001-10-24T15:00:00Z') });
  assert.equal(z.levelOf(stack2), 'hour');
  const minuteSlots = z.slotsOf(z.currentOf(stack2), 60);
  const stack3 = z.zoomIntoSlot(stack2, minuteSlots[7], 'UTC');
  assert.deepEqual(z.currentOf(stack3), { start: at('2001-10-24T14:07:00Z'), end: at('2001-10-24T14:08:00Z') });
  assert.equal(z.levelOf(stack3), 'minute');
  assert.equal(stack3.frames.length, 3);
  // Nothing deeper than a minute.
  assert.equal(z.zoomIntoSlot(stack3, z.slotsOf(z.currentOf(stack3), 10)[2], 'UTC'), stack3);
  assert.equal(z.childWindow(z.currentOf(stack3), { start: at('2001-10-24T14:07:00Z'), end: at('2001-10-24T14:07:10Z') }, 'UTC'), null);
});

test('a clicked day in a zone with a clock change is the 23 or 25 hour local day, cut to the queried window', () => {
  const zone = 'America/New_York';
  const range = { start: at('2001-10-26T00:00:00Z'), end: at('2001-11-02T00:00:00Z') };
  const slots = z.slotsOf(range, 86_400);
  const sunday = slots.find((slot) => slot.at === at('2001-10-28T00:00:00Z'));
  const child = z.childWindow(range, sunday, zone, range);
  assert.deepEqual(child, { start: at('2001-10-28T04:00:00Z'), end: at('2001-10-29T05:00:00Z') });
  assert.equal(child.end - child.start, 25 * HOUR);
  // A 25 hour day is still the day level, and it shows in 15 minute bars with every slot inside 120 points.
  const stack = z.zoomIntoSlot(z.createStack(range), sunday, zone);
  assert.equal(z.levelOf(stack), 'day');
  const bucket = z.bucketForView(child);
  assert.equal(bucket.seconds, 900);
  assert.equal(z.slotsOf(child, 900).length, 100);
  // The first day of a window that starts mid-day is cut to the window.
  const midday = { start: at('2001-10-26T15:00:00Z'), end: at('2001-11-02T15:00:00Z') };
  const first = z.childWindow(midday, { start: at('2001-10-26T15:00:00Z'), end: at('2001-10-27T00:00:00Z') }, 'UTC', midday);
  assert.deepEqual(first, { start: at('2001-10-26T15:00:00Z'), end: at('2001-10-27T00:00:00Z') });
});

test('a dragged range snaps outward to slot edges and stays inside the view', () => {
  const view = { start: at('2001-10-24T00:00:00Z'), end: at('2001-10-25T00:00:00Z') };
  const range = z.snapRange(at('2001-10-24T03:20:00Z'), at('2001-10-24T06:10:00Z'), 3600, view);
  assert.deepEqual(range, { start: at('2001-10-24T03:00:00Z'), end: at('2001-10-24T07:00:00Z') });
  // Dragged right to left gives the same window.
  assert.deepEqual(z.snapRange(at('2001-10-24T06:10:00Z'), at('2001-10-24T03:20:00Z'), 3600, view), range);
  // Past either edge it is cut to the view.
  assert.deepEqual(z.snapRange(at('2001-10-23T20:00:00Z'), at('2001-10-24T02:30:00Z'), 3600, view), { start: view.start, end: at('2001-10-24T03:00:00Z') });
  assert.deepEqual(z.snapRange(at('2001-10-24T22:30:00Z'), at('2001-10-25T09:00:00Z'), 3600, view), { start: at('2001-10-24T22:00:00Z'), end: view.end });
  // A drag inside one slot still gives that whole slot.
  assert.deepEqual(z.snapRange(at('2001-10-24T05:10:00Z'), at('2001-10-24T05:20:00Z'), 3600, view), { start: at('2001-10-24T05:00:00Z'), end: at('2001-10-24T06:00:00Z') });
  // The whole view, a range outside it, and a range under a minute give nothing.
  assert.equal(z.snapRange(view.start, view.end, 3600, view), null);
  assert.equal(z.snapRange(at('2001-10-20T00:00:00Z'), at('2001-10-21T00:00:00Z'), 3600, view), null);
  assert.equal(z.snapRange(at('2001-10-24T05:00:10Z'), at('2001-10-24T05:00:20Z'), 10, { start: view.start, end: at('2001-10-24T06:00:00Z') }), null);
  // The drag lands on a new stack frame.
  const stack = z.zoomToRange(z.createStack(view), at('2001-10-24T03:20:00Z'), at('2001-10-24T06:10:00Z'), 3600);
  assert.deepEqual(z.currentOf(stack), range);
});

test('the stack pushes, pops, jumps and resets; Back at the top does nothing', () => {
  const top = z.createStack(week);
  assert.equal(z.canBack(top), false);
  assert.equal(z.back(top), top, 'Back at the top is the same stack');
  assert.equal(z.reset(top), top);
  const day = { start: at('2001-10-24T00:00:00Z'), end: at('2001-10-25T00:00:00Z') };
  const hour = { start: at('2001-10-24T05:00:00Z'), end: at('2001-10-24T06:00:00Z') };
  const one = z.push(top, day);
  const two = z.push(one, hour);
  assert.deepEqual(top.frames, [], 'the old stack is not edited');
  assert.equal(two.frames.length, 2);
  assert.equal(z.canBack(two), true);
  assert.deepEqual(z.currentOf(z.back(two)), day);
  assert.deepEqual(z.currentOf(z.back(z.back(two))), week);
  assert.equal(z.reset(two).frames.length, 0);
  assert.equal(z.jumpTo(two, 1).frames.length, 1);
  assert.equal(z.jumpTo(two, 0).frames.length, 0);
  assert.equal(z.jumpTo(two, 2), two);
  assert.equal(z.jumpTo(two, 9), two);
  assert.equal(z.jumpTo(two, -1), two);
  assert.equal(z.jumpTo(two, 0.5), two);
  // Pushing what is already shown, or the whole period, adds nothing.
  assert.equal(z.push(one, day), one);
  assert.equal(z.push(one, week).frames.length, 0);
});

test('a zoom never leaves the queried window', () => {
  const top = z.createStack(week);
  const partlyOut = z.push(top, { start: at('2001-10-20T00:00:00Z'), end: at('2001-10-23T00:00:00Z') });
  assert.deepEqual(z.currentOf(partlyOut), { start: week.start, end: at('2001-10-23T00:00:00Z') });
  assert.equal(z.push(top, { start: at('2001-11-05T00:00:00Z'), end: at('2001-11-06T00:00:00Z') }), top, 'wholly outside');
  assert.equal(z.push(top, { start: at('2001-10-20T00:00:00Z'), end: at('2001-10-22T00:00:00Z') }), top, 'ends where the window starts');
  assert.equal(z.push(top, { start: at('2001-10-22T00:00:00Z'), end: at('2001-10-22T00:00:30Z') }), top, 'under a minute');
  assert.equal(z.push(top, { start: at('2001-10-20T00:00:00Z'), end: at('2001-11-20T00:00:00Z') }), top, 'wider than the window is the window');
  // Every shift and every scale stays inside, wherever it starts.
  const inside = (stack) => z.currentOf(stack).start >= week.start && z.currentOf(stack).end <= week.end && z.currentOf(stack).end > z.currentOf(stack).start;
  let stack = z.push(top, { start: at('2001-10-22T20:00:00Z'), end: at('2001-10-23T10:00:00Z') });
  for (let i = 0; i < 30; i++) { stack = z.shift(stack, i % 3 === 0 ? 1 : -1, 'UTC'); assert.ok(inside(stack)); }
  for (let i = 0; i < 30; i++) { stack = z.scale(stack, 'out'); assert.ok(inside(stack)); }
  for (let i = 0; i < 30; i++) { stack = z.scale(stack, 'in'); assert.ok(inside(stack)); }
  const edge = z.push(top, { start: at('2001-10-28T20:00:00Z'), end: week.end });
  assert.equal(z.shift(edge, 1, 'UTC'), edge, 'cannot go later than the end');
  assert.ok(inside(z.scale(edge, 'out')));
  // A child window is also cut to the window when one is given.
  const slot = { start: at('2001-10-21T00:00:00Z'), end: at('2001-10-22T00:00:00Z') };
  assert.equal(z.childWindow(week, slot, 'UTC', week), null);
});

test('earlier and later move by one view width; a whole day moves a whole local day', () => {
  const top = z.createStack(week);
  assert.equal(z.shift(top, 1, 'UTC'), top, 'the whole period has nowhere to go');
  assert.equal(z.canShift(top, -1, 'UTC'), false);
  const hours = z.push(top, { start: at('2001-10-24T05:00:00Z'), end: at('2001-10-24T08:00:00Z') });
  const later = z.shift(hours, 1, 'UTC');
  assert.deepEqual(z.currentOf(later), { start: at('2001-10-24T08:00:00Z'), end: at('2001-10-24T11:00:00Z') });
  assert.equal(later.frames.length, 1, 'it replaces the view; Back still goes to the whole period');
  assert.deepEqual(z.currentOf(z.shift(later, -1, 'UTC')), z.currentOf(hours));
  // New York, Saturday to Sunday to Monday around the 25 hour day.
  const zone = 'America/New_York';
  const range = { start: at('2001-10-20T04:00:00Z'), end: at('2001-11-03T04:00:00Z') };
  const saturday = z.dayWindow(at('2001-10-27T12:00:00Z'), zone);
  let stack = z.push(z.createStack(range), saturday);
  stack = z.shift(stack, 1, zone);
  const sunday = z.currentOf(stack);
  assert.equal(sunday.end - sunday.start, 25 * HOUR);
  stack = z.shift(stack, 1, zone);
  const monday = z.currentOf(stack);
  assert.equal(monday.start, sunday.end);
  assert.equal(monday.end - monday.start, 24 * HOUR);
  assert.deepEqual(z.currentOf(z.shift(z.shift(stack, -1, zone), -1, zone)), saturday);
  // At the first day of the window, earlier is off.
  const firstDay = z.push(z.createStack(range), z.dayWindow(range.start, zone));
  assert.equal(z.canShift(firstDay, -1, zone), false);
});

test('zoom in halves and zoom out doubles the view, and out of the widest view returns to the whole period', () => {
  const top = z.createStack(week);
  assert.equal(z.canScale(top, 'out'), false);
  const inOnce = z.scale(top, 'in');
  assert.equal(z.widthOf(z.currentOf(inOnce)), 3.5 * DAY);
  assert.equal(inOnce.frames.length, 1);
  const outAgain = z.scale(inOnce, 'out');
  assert.equal(outAgain.frames.length, 0, 'twice the width is the whole period');
  // Down to the one minute floor and no further.
  let stack = top;
  let guard = 0;
  while (z.canScale(stack, 'in') && guard++ < 40) stack = z.scale(stack, 'in');
  assert.ok(z.widthOf(z.currentOf(stack)) >= MIN);
  assert.ok(z.widthOf(z.currentOf(stack)) < 2 * MIN);
  assert.equal(z.scale(stack, 'in'), stack);
});

test('the interval keeps every view at 120 points or fewer, with the 10 and 86,400 second limits', () => {
  const widths = [MIN, 5 * MIN, 17 * MIN, HOUR, 90 * MIN, 3 * HOUR, 23 * HOUR, 25 * HOUR, 36 * HOUR, 50 * HOUR, 3 * DAY, 7 * DAY];
  for (const width of widths) {
    for (const offset of [0, 7 * MIN + 13_000, 31 * MIN]) {
      const view = { start: at('2001-10-22T00:00:00Z') + offset, end: at('2001-10-22T00:00:00Z') + offset + width };
      const choice = z.bucketForView(view);
      assert.ok(choice, 'a choice for ' + width / MIN + ' min');
      assert.ok(choice.seconds >= 10 && choice.seconds <= 86_400, 'limits ' + choice.seconds);
      assert.ok(z.slotsOf(view, choice.seconds).length <= 120, 'view points for ' + width / MIN + ' min at ' + choice.seconds);
      const reference = z.referenceFor(view);
      assert.ok(z.slotsOf(reference, choice.seconds).length <= 120, 'reference points');
      assert.equal(choice.source, 'automatic');
      assert.equal(choice.raised, false);
    }
  }
  // The automatic interval is the smallest step on the ladder, at least 10 seconds, that keeps the view and its reference at 120 points.
  const pick = (width) => z.bucketForView({ start: week.start, end: week.start + width }).seconds;
  const cases = [[MIN, 10], [5 * MIN, 10], [17 * MIN, 10], [HOUR, 30], [90 * MIN, 60], [3 * HOUR, 120], [5 * HOUR, 180],
    [23 * HOUR, 900], [DAY, 900], [36 * HOUR, 1800], [2 * DAY, 1800], [3 * DAY, 3600], [7 * DAY, 7200]];
  for (const [width, seconds] of cases) assert.equal(pick(width), seconds, width / MIN + ' min');
  assert.ok(pick(7 * DAY) >= 5040, 'a long window follows the app rule');
  assert.equal(z.slotsOf(week, 86_400).length, 7);
  const long = z.slotsOf({ start: 0, end: 400 * DAY }, 10);
  assert.equal(long.length, 120, 'slots are capped');
});

test('the automatic interval draws 60 to 120 bars for any view of 20 minutes or more', () => {
  const base = at('2001-10-22T00:00:00Z');
  const widths = [];
  for (let width = 20 * MIN; width < 7 * DAY; width += 7 * MIN) widths.push(width);
  widths.push(20 * MIN, 7 * DAY);
  for (const width of widths) {
    for (const offset of [0, 7 * MIN, 31 * MIN]) {
      const view = { start: base + offset, end: base + offset + width };
      const choice = z.bucketForView(view);
      assert.ok(choice, 'a choice for ' + width / MIN + ' min at +' + offset / MIN + ' min');
      const bars = z.slotsOf(view, choice.seconds).length;
      assert.ok(bars >= 60 && bars <= 120, bars + ' bars for ' + width / MIN + ' min at +' + offset / MIN + ' min (' + choice.seconds + ' s)');
    }
  }
});

test('the interval the user set still wins, and is raised only when it would pass 120 points', () => {
  const day = { start: at('2001-10-24T00:00:00Z'), end: at('2001-10-25T00:00:00Z') };
  const kept = z.bucketForView(day, { manualSeconds: 900 });
  assert.deepEqual({ seconds: kept.seconds, source: kept.source, raised: kept.raised }, { seconds: 900, source: 'manual', raised: false });
  assert.equal(z.bucketForView(day, { manualSeconds: 720 }).seconds, 720, 'a number off the round list is kept');
  const raised = z.bucketForView(day, { manualSeconds: 10 });
  assert.equal(raised.source, 'manual');
  assert.equal(raised.raised, true);
  assert.equal(raised.asked, 10);
  assert.ok(raised.seconds > 10 && z.slotsOf(day, raised.seconds).length <= 120);
  assert.equal(z.bucketForView(day, { manualSeconds: 5 }).asked, 5);
  assert.ok(z.bucketForView(day, { manualSeconds: 5 }).seconds >= 10);
  assert.equal(z.bucketForView(day, { manualSeconds: 1e9 }).seconds, 86_400);
  for (const nothing of [0, -5, NaN, null, undefined]) assert.equal(z.bucketForView(day, { manualSeconds: nothing }).source, 'automatic', String(nothing));
  // The app's own interval counts as manual only when it differs from the one the app would pick.
  const query = { start: '2001-10-22T00:00:00Z', end: '2001-10-29T00:00:00Z', baselineStart: '2001-10-15T00:00:00Z', baselineEnd: '2001-10-22T00:00:00Z', bucketSeconds: 0 };
  query.bucketSeconds = loadTypeScript('src/lib/time.ts').bucketForWindows(query.start, query.end, query.baselineStart, query.baselineEnd);
  assert.equal(z.isManualBucket(query), false);
  query.bucketSeconds = 3600;
  assert.equal(z.isManualBucket(query), true);
});

test('a zoomed read keeps the filters, narrows the window, and uses the equal length before it as the reference', () => {
  const base = { sourceIds: ['sql-a'], start: '2001-10-22T00:00:00.000Z', end: '2001-10-29T00:00:00.000Z', baselineStart: '2001-10-15T00:00:00.000Z', baselineEnd: '2001-10-22T00:00:00.000Z', kind: 'synchronous', outcome: 'all', metric: 'total', bucketSeconds: 5040, controller: 'web-a', action: 'job-alpha' };
  const view = { start: at('2001-10-24T00:00:00Z'), end: at('2001-10-25T00:00:00Z') };
  const query = z.zoomQuery(base, view, 3600);
  assert.equal(query.start, '2001-10-24T00:00:00.000Z');
  assert.equal(query.end, '2001-10-25T00:00:00.000Z');
  assert.equal(query.baselineStart, '2001-10-23T00:00:00.000Z');
  assert.equal(query.baselineEnd, '2001-10-24T00:00:00.000Z');
  assert.equal(query.bucketSeconds, 3600);
  assert.equal(query.controller, 'web-a');
  assert.deepEqual(query.sourceIds, ['sql-a']);
  assert.equal(base.start, '2001-10-22T00:00:00.000Z', 'the original query is untouched');
  assert.equal(Date.parse(query.baselineEnd) <= Date.parse(query.start), true);
});

test('keys on a focused chart', () => {
  assert.deepEqual(z.chartKeyAction('ArrowLeft'), { type: 'cursor', delta: -1 });
  assert.deepEqual(z.chartKeyAction('ArrowRight'), { type: 'cursor', delta: 1 });
  assert.deepEqual(z.chartKeyAction('Home'), { type: 'cursor-edge', edge: 'first' });
  assert.deepEqual(z.chartKeyAction('End'), { type: 'cursor-edge', edge: 'last' });
  assert.deepEqual(z.chartKeyAction('Enter'), { type: 'zoom' });
  assert.deepEqual(z.chartKeyAction('Backspace'), { type: 'back' });
  assert.deepEqual(z.chartKeyAction('Escape'), { type: 'back' });
  assert.deepEqual(z.chartKeyAction('+'), { type: 'zoom-in' });
  assert.deepEqual(z.chartKeyAction('-'), { type: 'zoom-out' });
  assert.equal(z.chartKeyAction('a'), null);
  assert.equal(z.chartKeyAction('Tab'), null);
  assert.equal(z.chartKeyAction('ArrowLeft', { alt: true }), null, 'Alt+Left is the browser Back');
  assert.equal(z.chartKeyAction('Backspace', { ctrl: true }), null);
  assert.equal(z.chartKeyAction('Enter', { meta: true }), null);
  const left = z.chartKeyAction('ArrowLeft'), right = z.chartKeyAction('ArrowRight');
  assert.equal(z.moveCursor(-1, right, 24), 0, 'the first arrow lands on an end');
  assert.equal(z.moveCursor(-1, left, 24), 23);
  assert.equal(z.moveCursor(5, right, 24), 6);
  assert.equal(z.moveCursor(0, left, 24), 0, 'it stops at the first slot');
  assert.equal(z.moveCursor(23, right, 24), 23, 'it stops at the last slot');
  assert.equal(z.moveCursor(5, z.chartKeyAction('Home'), 24), 0);
  assert.equal(z.moveCursor(5, z.chartKeyAction('End'), 24), 23);
  assert.equal(z.moveCursor(5, z.chartKeyAction('Enter'), 24), 5);
  assert.equal(z.moveCursor(3, right, 0), -1);
  assert.equal(z.moveCursor(NaN, right, 24), 0);
  // Enter zooms the slot under the cursor; Backspace and Escape step out; Enter with no cursor does nothing.
  const slots = z.slotsOf(week, 86_400);
  const top = z.createStack(week);
  const zoomed = z.applyChartKey(top, z.chartKeyAction('Enter'), { slots, cursor: 3, zone: 'UTC' });
  assert.deepEqual(z.currentOf(zoomed), { start: at('2001-10-25T00:00:00Z'), end: at('2001-10-26T00:00:00Z') });
  assert.equal(z.applyChartKey(top, z.chartKeyAction('Enter'), { slots, cursor: -1, zone: 'UTC' }), top);
  assert.equal(z.applyChartKey(top, z.chartKeyAction('Enter'), { slots, cursor: 99, zone: 'UTC' }), top);
  assert.equal(z.applyChartKey(zoomed, z.chartKeyAction('Escape'), { slots, cursor: 0, zone: 'UTC' }).frames.length, 0);
  assert.equal(z.applyChartKey(zoomed, z.chartKeyAction('Backspace'), { slots, cursor: 0, zone: 'UTC' }).frames.length, 0);
  assert.equal(z.applyChartKey(top, z.chartKeyAction('Backspace'), { slots, cursor: 0, zone: 'UTC' }), top, 'Backspace at the top is a no-op');
  assert.equal(z.applyChartKey(top, z.chartKeyAction('ArrowLeft'), { slots, cursor: 0, zone: 'UTC' }), top);
  assert.equal(z.applyChartKey(top, z.chartKeyAction('+'), { slots, cursor: 0, zone: 'UTC' }).frames.length, 1);
});

test('the strip model shows the trail, the level, and which buttons are off', () => {
  const zone = 'UTC';
  const top = z.createStack(week);
  const base = { zone, bucket: z.bucketForView(week), loading: false, error: '', hasData: true, asOf: null, overviewCounts: [] };
  const atTop = z.stripModel({ ...base, stack: top });
  assert.equal(atTop.summary, 'Zoom: 7 days');
  assert.equal(atTop.level, 'window');
  assert.equal(atTop.levelName, 'Whole period');
  assert.deepEqual(atTop.crumbs, [{ index: 0, label: '7 days', current: true }]);
  assert.deepEqual([atTop.canBack, atTop.canEarlier, atTop.canLater, atTop.canOut, atTop.canReset], [false, false, false, false, false]);
  assert.equal(atTop.canIn, true);
  assert.equal(atTop.scopeLine, '');
  assert.equal(atTop.asOfLine, '');
  const daySlot = z.slotsOf(week, 86_400)[2];
  const dayStack = z.zoomIntoSlot(top, daySlot, zone);
  const hourStack = z.zoomIntoSlot(dayStack, z.slotsOf(z.currentOf(dayStack), 3600)[14], zone);
  const model = z.stripModel({ ...base, stack: hourStack, bucket: z.bucketForView(z.currentOf(hourStack)), asOf: at('2001-10-24T14:03:22Z') });
  assert.deepEqual(model.crumbs.map((crumb) => crumb.label), ['7 days', '24 Oct', '24 Oct 14:00']);
  assert.deepEqual(model.crumbs.map((crumb) => crumb.current), [false, false, true]);
  assert.equal(model.summary, 'Zoom: 24 Oct 14:00');
  assert.equal(model.level, 'hour');
  assert.equal(model.canBack, true);
  assert.equal(model.canReset, true);
  assert.equal(model.canOut, true);
  assert.equal(model.canEarlier, true);
  assert.equal(model.canLater, true);
  assert.match(model.rangeLine, /^Showing 24 Oct 14:00 to 15:00 UTC\.$/);
  assert.equal(model.intervalLine, 'Interval: 30 seconds (automatic).');
  assert.match(model.asOfLine, /^As of 14:03:22 UTC\.$/);
  assert.match(model.scopeLine, /still cover the whole period: 22 Oct 00:00 to 29 Oct 00:00 UTC\./);
  assert.ok(model.referenceLine.length > 0);
  assert.ok(Math.abs(model.overview.left - (z.currentOf(hourStack).start - week.start) / (7 * DAY)) < 1e-12);
  assert.ok(Math.abs(model.overview.width - HOUR / (7 * DAY)) < 1e-12);
  // The smallest window cannot be zoomed into or shifted past the edge of the queried window.
  const minute = z.zoomIntoSlot(hourStack, z.slotsOf(z.currentOf(hourStack), 60)[0], zone);
  assert.equal(z.stripModel({ ...base, stack: minute }).canIn, false);
  assert.equal(z.stripModel({ ...base, stack: minute }).level, 'minute');
});

test('the strip says in plain words when a window has no data, is loading, or failed, and shows a manual interval', () => {
  const zone = 'UTC';
  const stack = z.zoomIntoSlot(z.createStack(week), z.slotsOf(week, 86_400)[1], zone);
  const base = { stack, zone, bucket: z.bucketForView(z.currentOf(stack)), loading: false, error: '', hasData: true, asOf: at('2001-10-23T10:00:00Z'), overviewCounts: [3, 0, 9, 1] };
  assert.equal(z.stripModel(base).message, '');
  assert.equal(z.stripModel({ ...base, hasData: false }).message, zoomCopy.noData);
  assert.equal(z.stripModel({ ...base, hasData: false }).messageTone, 'warn');
  assert.equal(z.stripModel({ ...base, hasData: null }).message, '', 'unknown is not empty');
  assert.equal(z.stripModel({ ...base, loading: true }).message, zoomCopy.loading);
  assert.equal(z.stripModel({ ...base, loading: true }).asOfLine, '', 'no "as of" for a read still running');
  const failed = z.stripModel({ ...base, error: 'The server did not answer.' });
  assert.equal(failed.messageTone, 'error');
  assert.match(failed.message, /could not be read\. The server did not answer\./);
  assert.equal(failed.asOfLine, '');
  const top = z.stripModel({ ...base, stack: z.createStack(week), hasData: false });
  assert.equal(top.message, '', 'the whole period keeps its own empty notes');
  const manual = z.stripModel({ ...base, bucket: z.bucketForView(z.currentOf(stack), { manualSeconds: 900 }) });
  assert.equal(manual.intervalLine, 'Interval: 15 minutes (your setting).');
  const raised = z.stripModel({ ...base, bucket: z.bucketForView(z.currentOf(stack), { manualSeconds: 10 }) });
  assert.match(raised.intervalLine, /^Interval: .+ Your setting of 10 seconds would give more than 120 points here\.$/);
  assert.deepEqual(z.stripModel(base).overview.bars, [1 / 3, 0, 1, 1 / 9]);
  assert.equal(z.stripModel({ ...base, overviewCounts: [] }).overview.hasData, false);
  assert.equal(z.stripModel({ ...base, overviewCounts: [0, NaN, -2] }).overview.hasData, false);
});

test('durations and labels read in plain numbers', () => {
  assert.equal(z.durationLabel(7 * DAY), '7 days');
  assert.equal(z.durationLabel(DAY), '24 hours');
  assert.equal(z.durationLabel(23 * HOUR), '23 hours');
  assert.equal(z.durationLabel(25 * HOUR), '25 hours');
  assert.equal(z.durationLabel(15 * MIN), '15 minutes');
  assert.equal(z.durationLabel(MIN), '60 seconds');
  assert.equal(z.durationLabel(10_000), '10 seconds');
  assert.equal(z.durationLabel(HOUR), '60 minutes');
  assert.equal(z.durationLabel(3 * HOUR), '3 hours');
  assert.equal(z.durationLabel(1.5 * DAY), '36 hours');
  assert.equal(z.durationLabel(0), 'no time');
  assert.equal(z.durationLabel(NaN), 'no time');
  assert.equal(z.windowLabel({ start: at('2001-10-28T04:00:00Z'), end: at('2001-10-29T05:00:00Z') }, 'America/New_York'), '28 Oct');
  assert.equal(z.windowLabel({ start: at('2001-10-24T05:20:00Z'), end: at('2001-10-24T05:50:00Z') }, 'UTC'), '24 Oct 05:20 to 05:50 UTC');
  assert.equal(z.windowLabel({ start: at('2001-10-24T05:20:00Z'), end: at('2001-10-24T05:21:00Z') }, 'UTC'), '05:20');
  assert.match(z.slotDescription({ at: 0, start: at('2001-10-24T05:00:00Z'), end: at('2001-10-24T06:00:00Z') }, 4, 24, 'UTC'), /^Part 5 of 24: 24 Oct 05:00 to 06:00 UTC\.$/);
});

test('every zoom string passes the copy lint and names no cause', () => {
  const entries = allZoomStrings().map((text, index) => ({ id: 'zoom' + index, text }));
  assert.ok(entries.length >= 30);
  assert.deepEqual(lintScreenStrings(entries, deck.terms), []);
  for (const { text } of entries) assert.doesNotMatch(text, /\bproven\b|\bcause|\bbecause\b|\bdue to\b|\bresult of\b/i, text);
});
