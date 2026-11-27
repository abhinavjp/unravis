const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { loadTypeScript } = require('./helpers/loadTs.cjs');

const L = loadTypeScript('src/lib/tracing/bbLanes.ts');
const { BBL_TEXT, BBL_LANE_TEXT, bbLanesCopyEntries } = loadTypeScript('src/lib/copy/bbLanesCopy.ts');
const { lintScreenStrings } = loadTypeScript('src/lib/copy/copylint.ts');
const { deck } = loadTypeScript('src/lib/copy/deck.ts');

const goSource = () => {
  const dir = path.join(__dirname, '..', '..', 'internal', 'importer', 'bbsource');
  return fs.readdirSync(dir).filter((f) => f.endsWith('.go') && !f.endsWith('_test.go')).map((f) => fs.readFileSync(path.join(dir, f), 'utf8')).join('\n');
};
const view = (def, points) => L.laneView(def, { sel: def.sel, unit: '', unitMixed: false, count: points.length, points });
const pt = (t, v, count = 1) => ({ t, min: v, mean: v, max: v, count });

test('every lane has a unique id, a known group, a name and a meaning, and exactly 8 start on', () => {
  const ids = L.LANES.map((l) => l.id);
  assert.equal(new Set(ids).size, ids.length);
  const groups = new Set(L.GROUPS.map((g) => g.id));
  for (const l of L.LANES) {
    assert.ok(groups.has(l.group), l.id);
    assert.ok(BBL_LANE_TEXT[l.id] && BBL_LANE_TEXT[l.id].name && BBL_LANE_TEXT[l.id].what, l.id);
  }
  assert.equal(Object.keys(BBL_LANE_TEXT).length, L.LANES.length, 'no copy for a lane that does not exist');
  assert.equal(L.defaultLaneIds().length, 8);
});

test('a lane selector is one the series route accepts: a kind, the Blackbox source, at most 4 attributes, no stray separator', () => {
  for (const l of L.LANES.filter((x) => x.mode === 'value')) {
    const terms = l.sel.split(';');
    assert.match(terms[0], /^kind=(counter|metric)$/, l.id);
    assert.ok(terms.includes('source=blackbox-source'), l.id);
    assert.ok(terms.filter((t) => t.startsWith('attr.')).length <= 4, l.id);
    for (const t of terms) assert.match(t, /^[a-z.]+=[^=;]+$/, l.id + ' ' + t);
  }
  for (const l of L.LANES.filter((x) => x.mode === 'count')) assert.equal(l.sel, '', l.id + ' is counted from events');
});

test('the counter names of the lanes are the ones the Blackbox importer writes (kept in step with internal/importer/bbsource)', () => {
  const src = goSource();
  for (const l of L.LANES.filter((x) => x.mode === 'value')) {
    for (const m of l.sel.matchAll(/attr\.(object|counter|metric|instance)=([^;]+)/g)) {
      const value = m[2];
      // The Go source writes these as quoted literals (the Windows counters) or as the Redis key names.
      assert.ok(src.includes('"' + value + '"') || fs.readFileSync(path.join(__dirname, '..', '..', 'internal', 'bbredis', 'parse.go'), 'utf8').includes('"' + value + '"'), l.id + ': ' + value + ' is not in the importer');
    }
  }
  for (const l of L.LANES.filter((x) => x.eventKind)) assert.ok(src.includes('"' + l.eventKind + '"'), l.id + ' event kind');
});

test('the event kinds the timeline marks are the kinds the importer writes', () => {
  const src = goSource() + fs.readFileSync(path.join(__dirname, '..', '..', 'internal', 'importer', 'sqlserver', 'xe.go'), 'utf8') + fs.readFileSync(path.join(__dirname, '..', '..', 'internal', 'importer', 'sqlserver', 'errorlog.go'), 'utf8');
  for (const k of L.EVENT_KINDS) assert.ok(src.includes('"' + k + '"'), k);
  assert.equal(L.eventText('recorder-start') !== '', true);
  assert.equal(L.eventText('nothing'), '');
});

test('a high-is-bad lane shows the highest value of each bucket, a low-is-bad lane the lowest, in the lane unit', () => {
  const cpu = L.laneById('cpu');
  const v = view(cpu, [{ t: 0, min: 10, mean: 20, max: 80, count: 4 }, { t: 60000, min: 5, mean: 6, max: 7, count: 2 }]);
  assert.deepEqual(v.points.map((p) => p.v), [80, 7]);
  const mem = L.laneById('mem');
  const GB = 1024 * 1024 * 1024;
  const m = view(mem, [{ t: 0, min: 2 * GB, mean: 4 * GB, max: 6 * GB, count: 3 }]);
  assert.equal(m.points[0].v, 2);
  assert.equal(L.formatLaneValue('gb', 2), '2.00 GB');
});

test('a bucket with no reading is absent, never zero; an empty answer has no points', () => {
  const v = view(L.laneById('cpu'), [pt(0, 5), { t: 60000, min: 0, mean: 0, max: 0, count: 0 }, pt(120000, 7)]);
  assert.deepEqual(v.points.map((p) => p.t), [0, 120000]);
  assert.equal(L.laneView(L.laneById('cpu'), undefined).points.length, 0);
  assert.equal(L.formatLaneValue('pct', null), BBL_TEXT.notRecorded);
  assert.equal(L.scaleLine(L.laneView(L.laneById('cpu'), undefined)), BBL_TEXT.noData);
});

test('a count lane counts events per bucket and leaves out buckets inside a gap', () => {
  const evt = L.laneById('evt');
  const v = L.laneView(evt, undefined, { times: [1000, 1500, 61000], fromMs: 0, toMs: 180000, bucketMs: 60000, gaps: [{ from: 120000, to: 180000 }] });
  assert.deepEqual(v.points, [{ t: 0, v: 2 }, { t: 60000, v: 1 }]);
});

test('scale lines: high-is-bad starts at zero; low-is-bad says its lowest value too', () => {
  const hi = view(L.laneById('cpu'), [pt(0, 10), pt(60000, 83)]);
  assert.equal(L.scaleLine(hi), BBL_TEXT.scale('83 %'));
  const lo = view(L.laneById('free'), [pt(0, 55), pt(60000, 49)]);
  assert.equal(L.scaleLine(lo), BBL_TEXT.scaleLow('49 %', '55 %'));
});

test('gap spans are clipped to the window and merged', () => {
  const out = L.gapSpans([{ start: 50, end: 120 }, { start: 100, end: 200 }, { start: 500, end: 600 }, { start: 900, end: 950 }], 80, 700);
  assert.deepEqual(out, [{ from: 80, to: 200 }, { from: 500, to: 600 }]);
});

test('which lane moved first: judged against the first third, earliest first, and a lane with no normal is not judged', () => {
  const flat = (n, v) => Array.from({ length: n }, (_, i) => pt(i * 60000, v + (i % 2)));
  const a = view(L.laneById('cpu'), [...flat(40, 30), ...Array.from({ length: 80 }, (_, i) => pt((40 + i) * 60000, i >= 30 ? 90 : 31))]);
  const b = view(L.laneById('blk'), [...flat(40, 0), ...Array.from({ length: 80 }, (_, i) => pt((40 + i) * 60000, i >= 10 ? 9 : 0))]);
  const sparse = view(L.laneById('dl'), [pt(0, 0), pt(100 * 60000, 5)]);
  const out = L.firstToChange([a, b, sparse], 0, 120 * 60000);
  assert.deepEqual(out.map((m) => m.id), ['blk', 'cpu']);
  assert.ok(out[0].t < out[1].t);
});

test('the lane choice keeps at most 10, only known ids, no repeat; a bad saved value gives the starting lanes', () => {
  assert.deepEqual(L.readLaneChoice(null), L.defaultLaneIds());
  assert.deepEqual(L.readLaneChoice(['cpu', 'cpu', 'nope', 7, 'mem']), ['cpu', 'mem']);
  assert.equal(L.readLaneChoice(L.LANES.map((l) => l.id)).length, 10);
  let ids = L.defaultLaneIds();
  ids = L.addLane(ids, 'dq');
  ids = L.addLane(ids, 'free');
  assert.equal(ids.length, 10);
  assert.equal(L.addLane(ids, 'pgs').length, 10, 'refused at 10');
  assert.equal(L.addLane(ids, 'nope').length, 10);
  assert.equal(L.removeLane(ids, 'cpu').includes('cpu'), false);
});

test('the picker search matches the name, the meaning and the counter', () => {
  const dl = L.laneById('dl');
  assert.equal(L.laneMatches(dl, 'deadlock'), true);
  assert.equal(L.laneMatches(dl, '  DEADLOCKS/sec '), true);
  assert.equal(L.laneMatches(dl, 'redis'), false);
  assert.equal(L.laneMatches(dl, ''), true);
});

test('the lane copy passes the screen-string lint and has no em dash', () => {
  const entries = bbLanesCopyEntries();
  assert.ok(entries.length > 80);
  for (const e of entries) assert.equal(e.text.includes('—'), false, e.id);
  assert.deepEqual(lintScreenStrings(entries, deck.terms), []);
});
