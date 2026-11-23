// BL-510: axis titles, one unit and one precision on the other time charts (response time over time, change versus reference, failures per interval,
// compare lines, daily overlay). Edge values on purpose: empty series, all null, zero, negative, thousands, a locale that swaps the marks.
const assert = require('node:assert/strict');
const test = require('node:test');
const { loadTypeScript } = require('./helpers/loadTs.cjs');

const axis = loadTypeScript('src/lib/chartAxis.ts');
const charts = loadTypeScript('src/lib/charts.ts');
const overlay = loadTypeScript('src/lib/rollupOverlay.ts');

const q = (valueMs) => ({ lowerMs: valueMs - 1, upperMs: valueMs + 1, valueMs });
const stats = (over = {}) => ({ count: 1, meanMs: null, minMs: null, maxMs: null, p50: null, p95: null, histogram: [], ...over });
const row = (key, ref = {}) => ({
  key, sourceId: 's', controller: 'C' + key, action: 'run', kind: 'synchronous', outcome: 'success', state: 'compared',
  current: stats(), reference: stats(ref), medianRatio: null, p95Ratio: null, medianDeltaMs: null, p95DeltaMs: null, currentEvents: 1, referenceEvents: 1
});
const at = (i) => new Date(Date.UTC(2026, 9, 22, 12, i * 10)).toISOString();
const analysisOf = (apis, points) => ({
  query: { sourceIds: ['s'], start: at(0), end: at(3), baselineStart: at(0), baselineEnd: at(3), kind: 'all', outcome: 'all', metric: 'total', bucketSeconds: 600, controller: '', action: '' },
  apis, timeline: apis.map(api => ({ key: api.key, points })), sources: [], quality: { incomplete: 0, invalid: 0, unknownOutcome: 0 }, approximate: true, warnings: []
});
const pt = (i, over = {}) => ({ at: at(i), count: 5, p50: q(100), p90: q(150), p95: q(200), p99: q(300), p95Ratio: 2, count4xx: 1, count5xx: 2, incomplete: 0, ...over });
const ticksOf = (ax, values) => values.map(value => ax.axisLabel.formatter(value));
const decimalsIn = (text) => (String(text).split(/[.,]/).length > 1 && /\.\d+$/.test(text) ? text.split('.').pop().length : 0);

test('valueAxis: zero is inside, every tick has the same decimals, the top covers the largest value', () => {
  const a = axis.valueAxis([0.4, 1.7, null, NaN, undefined], 4, 2);
  assert.equal(a.min, 0);
  assert.ok(a.max >= 1.7 && a.max / a.interval === Math.round(a.max / a.interval));
  const ticks = []; for (let v = a.min; v <= a.max + 1e-9; v += a.interval) ticks.push(a.format(v));
  assert.equal(new Set(ticks.map(decimalsIn)).size, 1, ticks.join(' | '));
  // Negative values extend the axis below zero by whole steps.
  const n = axis.valueAxis([-35, 10], 4, 10);
  assert.ok(n.min <= -35 && n.max >= 10 && n.min % n.interval === 0);
  // No values, or all zero, still give a drawable axis.
  for (const empty of [[], [null], [0, 0]]) { const e = axis.valueAxis(empty, 4, 1); assert.equal(e.min, 0); assert.ok(e.max > 0 && e.interval > 0); }
});

test('signedDurationAxis: milliseconds below a second at the farthest value, seconds from there, in milliseconds of data', () => {
  const ms = axis.signedDurationAxis([-40, 120, 300]);
  assert.equal(ms.unit, 'ms');
  assert.ok(ms.min <= -40 && ms.max >= 300 && Number.isInteger(ms.interval));
  const s = axis.signedDurationAxis([-2500, 9000]);
  assert.equal(s.unit, 's');
  assert.ok(s.min <= -2500 && s.max >= 9000, 'min and max stay in milliseconds, the unit of the data');
  assert.equal(s.format(s.max), s.format(9000 + (s.max - 9000)));
  assert.doesNotMatch(s.format(1000), /s$/, 'the unit is in the axis title, not in the ticks');
  // 999.6 ms is a second; the axis and durationText agree.
  assert.equal(axis.signedDurationAxis([999.6]).unit, 's');
  assert.equal(axis.signedDurationAxis([]).unit, 'ms');
});

test('response time over time: time title with the zone, unit title, one unit and one precision on the ticks, room for the title above the zoom strip', () => {
  const a = analysisOf([row('a', { p50: q(100), p95: q(1500) })], [pt(0, { p50: q(120), p95: q(2400) }), pt(1, { p50: null, p95: null }), pt(2, { p50: q(90), p95: q(61000) })]);
  const o = charts.timelineOption(a, a.apis[0], 'Asia/Dubai', 50);
  assert.equal(o.xAxis.name, 'Time (Asia/Dubai)');
  assert.equal(o.yAxis.name, 'Response time (s)', 'the largest value is 61 s');
  assert.ok(o.yAxis.max >= 61000);
  const ticks = ticksOf(o.yAxis, [0, o.yAxis.interval, o.yAxis.interval * 2, o.yAxis.max]);
  assert.equal(new Set(ticks.map(decimalsIn)).size, 1, ticks.join(' | '));
  for (const tick of ticks) assert.doesNotMatch(tick, /[a-z]/i, 'no unit in a tick: ' + tick);
  assert.ok(o.grid.bottom >= 70, 'the zoom strip starts 8 px from the bottom and is 14 px tall; the title is 30 px under the axis');
  // Small values stay in milliseconds, whole.
  const small = analysisOf([row('a')], [pt(0, { p50: q(8), p95: q(35) })]);
  const so = charts.timelineOption(small, small.apis[0], 'UTC', null);
  assert.equal(so.yAxis.name, 'Response time (ms)');
  assert.ok(Number.isInteger(so.yAxis.interval));
  // Nothing at all still builds.
  const none = analysisOf([row('a')], [pt(0, { p50: null, p95: null })]);
  assert.doesNotThrow(() => charts.timelineOption(none, none.apis[0], 'UTC', null));
});

test('change versus reference ratio: titled, the same decimals on every tick, the reference line (1) inside', () => {
  const a = analysisOf([row('a')], [pt(0, { p95Ratio: 0.5 }), pt(1, { p95Ratio: 3.25 }), pt(2, { p95Ratio: null })]);
  const o = charts.ratioOption(a, a.apis[0], 'UTC');
  assert.equal(o.xAxis.name, 'Time (UTC)');
  assert.equal(o.yAxis.name, 'Ratio (×)');
  assert.ok(o.yAxis.max >= 3.25 && o.yAxis.min === 0);
  const ticks = []; for (let v = 0; v <= o.yAxis.max + 1e-9; v += o.yAxis.interval) ticks.push(o.yAxis.axisLabel.formatter(v));
  assert.ok(ticks.every(tick => tick.endsWith('×')));
  assert.equal(new Set(ticks.map(decimalsIn)).size, 1, ticks.join(' | '));
  const flat = analysisOf([row('a')], [pt(0, { p95Ratio: null })]);
  assert.ok(charts.ratioOption(flat, flat.apis[0], 'UTC').yAxis.max >= 1, 'the reference line stays on the axis with no data');
});

test('failures per interval: whole numbers only, titled, thousands separators, a zero axis still draws', () => {
  const a = analysisOf([row('a')], [pt(0, { count4xx: 1200, count5xx: 3 }), pt(1, { count4xx: 0, count5xx: 0 })]);
  const o = charts.failureTrendOption(a, 'Europe/London');
  assert.equal(o.xAxis.name, 'Time (Europe/London)');
  assert.equal(o.yAxis.name, 'Responses per interval');
  assert.ok(Number.isInteger(o.yAxis.interval) && o.yAxis.max >= 1200);
  assert.equal(o.yAxis.axisLabel.formatter(1200), (1200).toLocaleString());
  assert.ok(o.grid.bottom >= 70);
  const zero = analysisOf([row('a')], [pt(0, { count4xx: 0, count5xx: 0 })]);
  const z = charts.failureTrendOption(zero, 'UTC');
  assert.ok(z.yAxis.max >= 1 && z.yAxis.interval >= 1);
});

test('compare lines: failure counts are whole, a percent change shares one precision, a millisecond change names one unit', () => {
  const ref = { p50: q(100), p90: q(150), p95: q(200), p99: q(300) };
  const a = analysisOf([row('a', ref)], [pt(0, { p95: q(250) }), pt(1, { p95: q(150) }), pt(2, { p95: null })]);
  const fail = charts.multiTimelineOption(a, ['a'], new Map(), 'UTC', 'milliseconds', 'count5xx');
  assert.equal(fail.yAxis.name, 'Responses per interval');
  assert.ok(Number.isInteger(fail.yAxis.interval));
  assert.equal(fail.xAxis.name, 'Time (UTC)');
  const pct = charts.multiTimelineOption(a, ['a'], new Map(), 'UTC', 'percent', 'p95');
  assert.equal(pct.yAxis.name, 'Change from own reference (%)');
  assert.ok(pct.yAxis.min < 0 && pct.yAxis.max > 0, 'a drop and a rise both fit, zero inside');
  const pctTicks = []; for (let v = pct.yAxis.min; v <= pct.yAxis.max + 1e-9; v += pct.yAxis.interval) pctTicks.push(pct.yAxis.axisLabel.formatter(v).replace(/^\+/, ''));
  assert.equal(new Set(pctTicks.map(decimalsIn)).size, 1, pctTicks.join(' | '));
  const ms = charts.multiTimelineOption(a, ['a'], new Map(), 'UTC', 'milliseconds', 'p95');
  assert.equal(ms.yAxis.name, 'Change from own reference (ms)');
  assert.match(ms.yAxis.axisLabel.formatter(50), /^\+50$/);
  assert.ok(ms.grid.bottom >= 70);
  // A big change reads in seconds, on every tick.
  const big = analysisOf([row('a', ref)], [pt(0, { p95: q(12200) })]);
  const bo = charts.multiTimelineOption(big, ['a'], new Map(), 'UTC', 'milliseconds', 'p95');
  assert.equal(bo.yAxis.name, 'Change from own reference (s)');
  // No rows at all still builds.
  assert.doesNotThrow(() => charts.multiTimelineOption(a, [], new Map(), 'UTC', 'percent', 'p95'));
});

test('daily overlay: both axes titled, count axis whole, P95 axis one unit', () => {
  const q2 = (ms) => ({ lowerMs: ms, upperMs: ms, valueMs: ms });
  const cell = (date, hour, over = {}) => ({ date, hour, start: date + 'T00:00:00Z', end: date + 'T00:59:59Z', count: 40, latencyCount: 30, p95: q2(100 + hour * 100), slowCount: 1, noise: false, ...over });
  const days = ['2001-03-01', '2001-03-02'].map(date => ({ date, hours: 24, partial: false }));
  const grid = { noiseBelow: 5, days, cells: days.flatMap(day => [...Array(24).keys()].map(hour => cell(day.date, hour))) };
  const a = overlay.overlayDay(grid, '2001-03-01'), b = overlay.overlayDay(grid, '2001-03-02');
  const palette = { text: '#000', text2: '#555', axisLine: '#ccc', grid: '#eee', surface: '#fff', series: ['#111', '#222'] };
  const p95 = overlay.overlayOption(a, b, 'p95', palette);
  assert.equal(p95.xAxis.name, 'Hour of the day');
  assert.equal(p95.yAxis.name, 'P95 response time (s)', 'the slowest hour is 2.4 s');
  const count = overlay.overlayOption(a, b, 'count', palette);
  assert.equal(count.yAxis.name, 'Requests per interval');
  assert.ok(Number.isInteger(count.yAxis.interval));
  // Empty days still build.
  const empty = { ...a, points: a.points.map(point => ({ ...point, p95: null, count: null, exists: false })) };
  assert.doesNotThrow(() => overlay.overlayOption(empty, empty, 'p95', palette));
});
