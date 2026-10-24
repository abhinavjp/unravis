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
const read = (relativePath) => fs.readFileSync(path.join(root, relativePath), 'utf8');

const z = loadTypeScript('src/lib/zoom.ts');
const { zoomCopy } = loadTypeScript('src/lib/copy/zoomCopy.ts');
const { lintScreenStrings } = loadTypeScript('src/lib/copy/copylint.ts');
const auto = loadTypeScript('src/lib/autorun.ts');
const { deck } = loadTypeScript('src/lib/copy/deck.ts');
const { focusWouldBeLost } = loadTypeScript('src/lib/focusLoss.ts');

const MIN = 60_000, HOUR = 3_600_000, DAY = 86_400_000;
const at = (text) => Date.parse(text);
// Synthetic windows only: a week from Monday 22 Oct to Monday 29 Oct 2001 (UTC).
const week = { start: at('2001-10-22T00:00:00Z'), end: at('2001-10-29T00:00:00Z') };
const ZONE = 'UTC';

test('D1: a window with requests but nothing to compare is not called empty', () => {
  const base = { isRoot: false, answered: true, loading: false, error: '', hasRequests: true };
  assert.equal(z.emptyWindowMessage(base), '', 'has data, no reference: no "No requests were found"');
  assert.equal(z.emptyWindowMessage({ ...base, hasRequests: false }), zoomCopy.noData);
  assert.equal(z.emptyWindowMessage({ ...base, hasRequests: false, error: 'x' }), zoomCopy.cannotRead);
  assert.equal(z.emptyWindowMessage({ ...base, hasRequests: false, loading: true }), '');
  assert.equal(z.emptyWindowMessage({ ...base, hasRequests: false, answered: false }), '');
  assert.equal(z.emptyWindowMessage({ ...base, hasRequests: false, isRoot: true }), '');
});

test('D1: the charts take the empty message from the store, not from "zoomed and not loading"', () => {
  const multi = read('src/components/MultiTimeline.svelte');
  assert.match(multi, /zoomView\.emptyMessage/);
  assert.doesNotMatch(multi, /zoomed\s*&&\s*!\s*zoomView\.loading/);
  assert.ok(!multi.includes('zoomCopy.noData'), 'MultiTimeline no longer decides the wording itself');
  for (const file of ['Charts', 'Pulse', 'FailurePanel']) assert.match(read('src/components/' + file + '.svelte'), /zoomView\.emptyMessage/, file);
  assert.match(multi, /No comparable time points|comparable/i, 'the no-comparable wording is still there');
});

test('D2: the interval is a human duration at every level', () => {
  assert.equal(z.intervalLabel(10), '10 seconds');
  assert.equal(z.intervalLabel(1), '1 second');
  assert.equal(z.intervalLabel(60), '1 minute');
  assert.equal(z.intervalLabel(1800), '30 minutes');
  assert.equal(z.intervalLabel(3600), '1 hour');
  assert.equal(z.intervalLabel(7200), '2 hours');
  assert.equal(z.intervalLabel(86_400), '1 day');
  assert.equal(z.intervalLabel(5066), 'about 84 minutes');
  assert.equal(z.intervalLabel(5064), 'about 84 minutes');
  assert.doesNotMatch(z.intervalLabel(5066), /5066|seconds/);
  assert.equal(z.intervalLabel(75), '75 seconds');
  assert.equal(z.intervalLabel(100_000), 'about 28 hours');
  const stack = z.createStack(week);
  const model = z.stripModel({ stack, zone: ZONE, bucket: { seconds: 5066, source: 'automatic', raised: false, asked: null }, loading: false, error: '', hasData: null, asOf: null, overviewCounts: [] });
  assert.ok(model.intervalLine.includes('about 84 minutes'), model.intervalLine);
  assert.ok(!model.intervalLine.includes('5066'), model.intervalLine);
});

test('D3: only the whole period is "Whole period"; other windows get an accurate name', () => {
  const stack = z.createStack(week);
  assert.equal(z.levelNameOf(stack, ZONE), zoomCopy.levelNames.window);
  const half = z.scale(stack, 'in');
  assert.notEqual(half, stack);
  assert.equal(z.levelNameOf(half, ZONE), zoomCopy.levelNames.custom, '3.5 days is not the whole period');
  assert.notEqual(zoomCopy.levelNames.custom, zoomCopy.levelNames.window);
  assert.equal(z.stripModel({ stack: half, zone: ZONE, bucket: null, loading: false, error: '', hasData: true, asOf: null, overviewCounts: [] }).levelName, zoomCopy.levelNames.custom);
  // Earlier / Later on a window that is not a day keep the neutral name.
  const moved = z.shift(half, 1, ZONE);
  assert.equal(z.levelNameOf(moved, ZONE), zoomCopy.levelNames.custom);
  // An exact day, hour and minute keep their names.
  const day = z.dayWindow(at('2001-10-24T12:00:00Z'), ZONE);
  const inDay = z.push(stack, day);
  assert.equal(z.levelNameOf(inDay, ZONE), zoomCopy.levelNames.day);
  const hour = z.hourWindow(at('2001-10-24T12:30:00Z'), ZONE);
  const inHour = z.push(inDay, hour);
  assert.equal(z.levelNameOf(inHour, ZONE), zoomCopy.levelNames.hour);
  const minute = z.minuteWindow(at('2001-10-24T12:30:00Z'));
  assert.equal(z.levelNameOf(z.push(inHour, minute), ZONE), zoomCopy.levelNames.minute);
  const labels = Object.values(zoomCopy.levelNames);
  assert.deepEqual(lintScreenStrings(labels.map((text, i) => ({ id: 'level' + i, text })), deck.terms), []);
});

test('D4: Zoom in, Zoom in, Earlier, Later, Zoom out never repeats a step in the trail', () => {
  let stack = z.createStack(week);
  const steps = [s => z.scale(s, 'in'), s => z.scale(s, 'in'), s => z.shift(s, -1, ZONE), s => z.shift(s, 1, ZONE), s => z.scale(s, 'out')];
  for (const step of steps) stack = step(stack);
  for (let i = 1; i < stack.frames.length; i++) assert.ok(!z.sameWindow(stack.frames[i], stack.frames[i - 1]), 'frame ' + i + ' repeats frame ' + (i - 1));
  const crumbs = z.stripModel({ stack, zone: ZONE, bucket: null, loading: false, error: '', hasData: true, asOf: null, overviewCounts: [] }).crumbs;
  for (let i = 1; i < crumbs.length; i++) assert.ok(!(crumbs[i].label === crumbs[i - 1].label && crumbs[i].label !== crumbs[0].label) || !z.sameWindow(stack.frames[i - 1], stack.frames[i - 2] ?? stack.root), 'crumb repeated');
});

test('D4: zooming out onto the step above steps back to it', () => {
  let stack = z.createStack(week);
  stack = z.scale(stack, 'in'); // 3.5 days
  const outer = stack.frames[0];
  stack = z.scale(stack, 'in'); // 1.75 days
  assert.equal(stack.frames.length, 2);
  stack = z.scale(stack, 'out');
  assert.equal(stack.frames.length, 1, 'no second copy of the 3.5 day step');
  assert.ok(z.sameWindow(stack.frames[0], outer));
});

test('D4: no run of zoom, shift and scale steps ever leaves two equal steps side by side', () => {
  let seed = 12345;
  const next = () => (seed = (seed * 1103515245 + 12345) & 0x7fffffff) / 0x7fffffff;
  for (let run = 0; run < 300; run++) {
    let stack = z.createStack(week);
    for (let n = 0; n < 14; n++) {
      const pick = Math.floor(next() * 5);
      if (pick === 0) stack = z.scale(stack, 'in');
      else if (pick === 1) stack = z.scale(stack, 'out');
      else if (pick === 2) stack = z.shift(stack, -1, ZONE);
      else if (pick === 3) stack = z.shift(stack, 1, ZONE);
      else stack = z.back(stack);
      for (let i = 1; i < stack.frames.length; i++) assert.ok(!z.sameWindow(stack.frames[i], stack.frames[i - 1]), 'run ' + run + ' step ' + n);
    }
  }
});

test('D5: a click where Zoom in is disabled says "This is the smallest window."', () => {
  assert.equal(zoomCopy.minuteFloor, 'This is the smallest window.');
  let stack = z.createStack(week);
  stack = z.push(stack, z.dayWindow(at('2001-10-24T12:00:00Z'), ZONE));
  stack = z.push(stack, z.hourWindow(at('2001-10-24T12:30:00Z'), ZONE));
  stack = z.push(stack, z.minuteWindow(at('2001-10-24T12:30:00Z')));
  assert.equal(z.canScale(stack, 'in'), false, 'a minute cannot zoom in');
  const slot = { start: at('2001-10-24T12:30:00Z'), end: at('2001-10-24T12:30:10Z') };
  assert.equal(z.zoomBlockedNotice(stack, slot, ZONE), zoomCopy.minuteFloor);
  assert.equal(z.zoomBlockedNotice(stack, null, ZONE), zoomCopy.minuteFloor, 'the + key too');
  // A slot that can zoom has nothing to say.
  const root = z.createStack(week);
  const daySlot = { start: at('2001-10-24T00:00:00Z'), end: at('2001-10-25T00:00:00Z') };
  assert.equal(z.zoomBlockedNotice(root, daySlot, ZONE), '');
  // The strip carries the notice and defaults to none.
  const base = { stack, zone: ZONE, bucket: null, loading: false, error: '', hasData: true, asOf: null, overviewCounts: [] };
  assert.equal(z.stripModel(base).notice, '');
  assert.equal(z.stripModel({ ...base, notice: zoomCopy.minuteFloor }).notice, zoomCopy.minuteFloor);
});

test('D6: the chart blur handler does not change state during a DOM update', () => {
  const source = read('src/lib/chartZoom.ts');
  const blur = source.slice(source.indexOf('onBlur'));
  assert.match(blur, /queueMicrotask\(\s*\(\)\s*=>\s*zoomView\.clearAnnouncement\(\)\s*\)/);
  assert.doesNotMatch(source, /^\s*zoomView\.clearAnnouncement\(\);/m, 'no direct call left');
});

test('D7: the empty message takes focus that the hidden chart held', () => {
  const stage = { contains: (node) => node === chart || node === message };
  const chart = { name: 'chart' }, message = { name: 'message' }, body = { name: 'body' };
  assert.equal(focusWouldBeLost(stage, message, chart, body), true);
  assert.equal(focusWouldBeLost(stage, message, message, body), false, 'already there');
  assert.equal(focusWouldBeLost(stage, message, body, body), false, 'focus was never in the chart');
  assert.equal(focusWouldBeLost(stage, message, { name: 'elsewhere' }, body), false, 'focus is outside the stage');
  assert.equal(focusWouldBeLost(null, message, chart, body), false);
  assert.equal(focusWouldBeLost(stage, message, null, body), false);
  for (const file of ['MultiTimeline', 'Charts', 'Pulse', 'FailurePanel']) assert.match(read('src/components/' + file + '.svelte'), /use:focusEmpty/, file);
});

test('minor: an interval that no longer fits the period is explained, not silently replaced', () => {
  const query = { start: '2001-10-22T00:00:00Z', end: '2001-10-29T00:00:00Z', baselineStart: '2001-10-15T00:00:00Z', baselineEnd: '2001-10-22T00:00:00Z', bucketSeconds: 3600 };
  assert.equal(auto.intervalFits(query, 3600), false, '168 hourly points is over 120');
  assert.equal(auto.intervalFits(query, 7200), true);
  const note = auto.intervalFitNote(query);
  assert.match(note, /3600 seconds/);
  assert.match(note, /120 points/);
  assert.equal(auto.intervalFitNote({ ...query, bucketSeconds: 7200 }), '');
  assert.equal(auto.intervalFitNote({ ...query, bucketSeconds: NaN }), '', 'not a number: no note');
  assert.equal(auto.intervalFitNote({ ...query, bucketSeconds: 5 }), '', 'out of range is the run hint\'s job');
  const replaced = auto.intervalReplacedNote(3600, { ...query, bucketSeconds: 5064 });
  assert.match(replaced, /3600/);
  assert.match(replaced, /5064/);
  assert.equal(auto.intervalReplacedNote(5064, { ...query, bucketSeconds: 5064 }), '');
  assert.equal(auto.intervalReplacedNote(7200, { ...query, bucketSeconds: 7200 }), '');
  assert.equal(auto.intervalReplacedNote(7200, { ...query, bucketSeconds: 5064 }), '', 'the old number still fit: no note');
  assert.deepEqual(lintScreenStrings([{ id: 'fit', text: note }, { id: 'replaced', text: replaced }], deck.terms), []);
});
