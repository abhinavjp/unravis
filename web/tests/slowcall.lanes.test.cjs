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

const { MAX_BAR_ROWS, atOnce, laneCells, regroupBlocks, packBars, buildLane, formatAtOnce } = loadTypeScript('src/lib/drill/callLanes.ts');

const T0 = Date.parse('2026-03-01T10:00:00Z');
const MIN = 60000;
const win = { axisStart: '2026-03-01T10:00:00Z', bucketSeconds: 60 };
const iso = (ms) => new Date(ms).toISOString();
const eventsLane = (over) => ({ key: 'audit', state: 'events', buckets: [], events: [], ...over });

test('atOnce is busy time divided by the step length', () => {
  assert.equal(atOnce(120000, 60), 2);
  assert.equal(atOnce(0, 60), 0);
  assert.equal(atOnce(30000, 60), 0.5);
});

test('not recorded is never zero', () => {
  assert.equal(atOnce(null, 60), null);
  assert.equal(atOnce(undefined, 60), null);
  assert.equal(atOnce(1000, 0), null);
  const lane = eventsLane({ buckets: [{ start: iso(T0), count: 5, busyMs: null }] });
  const cells = laneCells(lane, win, T0, T0 + 2 * MIN);
  assert.equal(cells.length, 2);
  assert.equal(cells[0].atOnce, null);
  assert.equal(cells[1].atOnce, null);
  const noData = laneCells({ key: 'sql', state: 'no-data', buckets: [{ start: iso(T0), count: 0, busyMs: 60000 }], events: [] }, win, T0, T0 + MIN);
  assert.equal(noData[0].atOnce, null);
});

test('a bucket that was read and saw nothing is zero, a missing one is not recorded', () => {
  const lane = eventsLane({ buckets: [{ start: iso(T0), count: 0, complete: true }, { start: iso(T0 + 2 * MIN), count: 0, complete: false }] });
  const cells = laneCells(lane, win, T0, T0 + 3 * MIN);
  assert.equal(cells[0].atOnce, 0);
  assert.equal(cells[1].atOnce, null);
  assert.equal(cells[2].atOnce, null);
});

test('laneCells gives one ordered cell per step and counts refused calls', () => {
  const lane = eventsLane({
    buckets: [
      { start: iso(T0 + MIN), count: 3, busyMs: 30000, p95Ms: 400, errors: 1, clientErrors: 2 },
      { start: iso(T0 + 2 * MIN), count: 1, busyMs: 6000 }
    ]
  });
  const cells = laneCells(lane, win, T0, T0 + 4 * MIN);
  assert.equal(cells.length, 4);
  assert.deepEqual(cells.map((c) => c.start), [T0, T0 + MIN, T0 + 2 * MIN, T0 + 3 * MIN]);
  assert.equal(cells[1].atOnce, 0.5);
  assert.equal(cells[1].p95Ms, 400);
  assert.equal(cells[1].refused, 3);
  assert.equal(cells[2].refused, null);
  assert.equal(cells[0].refused, null);
});

test('regroup spreads one busy cell over the whole block', () => {
  const cells = [];
  for (let i = 0; i < 15; i += 1) {
    cells.push({ start: T0 + i * MIN, end: T0 + (i + 1) * MIN, atOnce: i === 4 ? 15 : 0, p95Ms: null, refused: null });
  }
  const out = regroupBlocks(cells, 900);
  assert.equal(out.length, 15);
  for (const cell of out) assert.equal(cell.atOnce, 1);
});

test('regroup keeps a block of nulls as null', () => {
  const cells = [];
  for (let i = 0; i < 30; i += 1) {
    cells.push({ start: T0 + i * MIN, end: T0 + (i + 1) * MIN, atOnce: i < 15 ? null : 2, p95Ms: null, refused: null });
  }
  const out = regroupBlocks(cells, 900);
  assert.equal(out[0].atOnce, null);
  assert.equal(out[14].atOnce, null);
  assert.equal(out[15].atOnce, 2);
});

test('packing five overlapping bars gives three rows and two more', () => {
  const items = [0, 1, 2, 3, 4].map((i) => ({ id: 'e' + i, label: 'item ' + i, start: T0 + i * 1000, end: T0 + 60000 + i * 1000 }));
  const { bars, more } = packBars(items);
  assert.equal(MAX_BAR_ROWS, 3);
  assert.equal(bars.length, 3);
  assert.equal(more, 2);
  assert.deepEqual(bars.map((b) => b.row).sort(), [0, 1, 2]);
});

test('bars that do not overlap share a row and longest items win a place', () => {
  const side = packBars([
    { id: 'a', label: 'a', start: 0, end: 1000 },
    { id: 'b', label: 'b', start: 2000, end: 3000 }
  ]);
  assert.deepEqual(side.bars.map((b) => b.row), [0, 0]);
  assert.equal(side.more, 0);
  const crowded = packBars([
    { id: 'short', label: 's', start: 0, end: 10 },
    { id: 'long1', label: 'l1', start: 0, end: 5000 },
    { id: 'long2', label: 'l2', start: 0, end: 6000 },
    { id: 'long3', label: 'l3', start: 0, end: 7000 }
  ]);
  assert.equal(crowded.more, 1);
  assert.deepEqual(crowded.bars.map((b) => b.id).sort(), ['long1', 'long2', 'long3']);
});

test('IIS bars end at the log time, other lanes start at the event start', () => {
  const event = { id: 'r1', label: 'GET /x', start: iso(T0 + 10 * MIN), end: iso(T0 + 10 * MIN), durationMs: 5000 };
  const iis = buildLane({ key: 'iis', state: 'events', buckets: [], events: [event] }, 'iis', win, T0, T0 + 20 * MIN);
  assert.equal(iis.bars[0].end, T0 + 10 * MIN);
  assert.equal(iis.bars[0].start, T0 + 10 * MIN - 5000);
  assert.equal(iis.bars[0].kind, 'bar');
  const audit = buildLane(
    { key: 'audit', state: 'events', buckets: [], events: [{ id: 'a1', label: 'call', start: iso(T0 + MIN), end: iso(T0 + MIN + 8000), durationMs: 8000 }] },
    'audit', win, T0, T0 + 20 * MIN
  );
  assert.equal(audit.bars[0].start, T0 + MIN);
  assert.equal(audit.bars[0].end, T0 + MIN + 8000);
});

test('an event with no duration is a marker at its start', () => {
  const lane = buildLane(
    { key: 'audit', state: 'events', buckets: [], events: [{ id: 'm', label: 'm', start: iso(T0 + MIN), end: iso(T0 + 2 * MIN), durationMs: null }] },
    'audit', win, T0, T0 + 5 * MIN
  );
  assert.equal(lane.bars[0].kind, 'marker');
  assert.equal(lane.bars[0].start, T0 + MIN);
  assert.equal(lane.bars[0].end, T0 + MIN);
});

test('the database lane moves in blocks and the peak ignores gaps', () => {
  const buckets = [];
  for (let i = 0; i < 15; i += 1) buckets.push({ start: iso(T0 + i * MIN), count: 1, busyMs: i === 3 ? 900000 : 0 });
  const lane = buildLane({ key: 'sql', state: 'events', blockSeconds: 900, buckets, events: [] }, 'sql', win, T0, T0 + 30 * MIN);
  assert.equal(lane.cells.length, 30);
  assert.equal(lane.cells[0].atOnce, 1);
  assert.equal(lane.cells[14].atOnce, 1);
  assert.equal(lane.cells[15].atOnce, null);
  assert.deepEqual(lane.peak, { atOnce: 1, start: T0 });
  assert.equal(lane.blockSeconds, 900);
  assert.equal(lane.hasShape, true);
});

test('formatAtOnce shows one decimal under 10 and a whole number from 10', () => {
  assert.equal(formatAtOnce(4.24), '4.2');
  assert.equal(formatAtOnce(12.6), '13');
  assert.equal(formatAtOnce(0), '0');
  assert.equal(formatAtOnce(9.96), '10');
});

test('a lane with no data has no shape and no peak', () => {
  const lane = buildLane({ key: 'sql', state: 'no-data', buckets: [], events: [] }, 'sql', win, T0, T0 + 5 * MIN);
  assert.equal(lane.hasShape, false);
  assert.equal(lane.peak, null);
  assert.equal(lane.cells.length, 5);
  const missing = buildLane(undefined, 'iis', win, T0, T0 + 5 * MIN);
  assert.equal(missing.hasShape, false);
  assert.equal(missing.key, 'iis');
  assert.equal(missing.bars.length, 0);
});
