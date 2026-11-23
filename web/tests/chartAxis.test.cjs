// BL-495: axis titles, units and one precision on the Requests-over-time chart. Edge values on purpose: zero, tiny, huge, NaN, empty series,
// thousands separators and a locale that swaps the marks.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

const root = path.resolve(__dirname, '..');
const cache = new Map();
function loadTypeScript(relativePath) {
  const filename = path.join(root, relativePath);
  if (cache.has(filename)) return cache.get(filename).exports;
  const source = fs.readFileSync(filename, 'utf8');
  const output = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022, esModuleInterop: true } }).outputText;
  const module = { exports: {} };
  cache.set(filename, module);
  const nodeRequire = createRequire(filename);
  const load = (request) => /^\.\.?\//.test(request) && fs.existsSync(path.join(path.dirname(filename), request + '.ts'))
    ? loadTypeScript(path.relative(root, path.join(path.dirname(filename), request + '.ts')))
    : nodeRequire(request);
  new Function('require', 'module', 'exports', output)(load, module, module.exports);
  return module.exports;
}

const axis = loadTypeScript('src/lib/chartAxis.ts');
const { heroOption } = loadTypeScript('src/lib/charts.ts');

// Tests read en-GB-style text: the decimal mark is a dot, the thousands mark a comma.
const decimalsOf = (label) => (/\.(\d+)/.exec(label) || [])[1]?.length ?? 0;
const ticksOf = (a, count) => Array.from({ length: Math.round(a.maxMs / a.intervalMs) + 1 }, (_, i) => i * a.intervalMs);

test('niceStep: 1, 2 or 5 times a power of ten, never below the floor, and a bad number gives the floor', () => {
  assert.equal(niceStepOf(0.3), 0.5);
  assert.equal(niceStepOf(1), 1);
  assert.equal(niceStepOf(1.1), 2);
  assert.equal(niceStepOf(3), 5);
  assert.equal(niceStepOf(7), 10);
  assert.equal(niceStepOf(0.07), 0.1);
  assert.equal(axis.niceStep(0.1, 1), 1);
  for (const bad of [NaN, -5, 0, Infinity, undefined, null]) assert.equal(axis.niceStep(bad, 1), 1, String(bad));
  assert.equal(axis.niceStep(NaN), 1);
  // The float tolerance: noise above a step stays on the step, a real excess does not.
  assert.equal(axis.niceStep(5 + 5e-13), 5);
  assert.equal(axis.niceStep(0.1 * 3 + 0.2 * 1e-12), 0.5);
  assert.equal(axis.niceStep(2 * (1 + 1e-10)), 2);
  assert.equal(axis.niceStep(5.0001), 10);
  assert.equal(axis.niceStep(2.001), 5);
  function niceStepOf(v) { return axis.niceStep(v); }
});

test('decimalsFor and fixedNumber: the same decimals on every figure, no NaN, no -0', () => {
  assert.equal(axis.decimalsFor(5), 0);
  assert.equal(axis.decimalsFor(0.5), 1);
  assert.equal(axis.decimalsFor(0.25), 2);
  assert.equal(axis.decimalsFor(NaN), 0);
  assert.equal(axis.fixedNumber(3, 1, 'en-GB'), '3.0');
  assert.equal(axis.fixedNumber(3.04, 0, 'en-GB'), '3');
  assert.equal(axis.fixedNumber(1234567.891, 2, 'en-GB'), '1,234,567.89');
  assert.equal(axis.fixedNumber(NaN, 1), '—');
  assert.equal(axis.fixedNumber(undefined, 1), '—');
  assert.equal(axis.fixedNumber(Infinity, 1), '—');
  assert.equal(axis.fixedNumber(-0.0001, 1, 'en-GB'), '0.0');
  assert.equal(axis.fixedNumber(-0.0001, 1, 'en-GB').includes('-'), false);
});

test('fixedNumber: the locale changes the marks, never the number of decimals', () => {
  assert.equal(axis.fixedNumber(1234.5, 1, 'de-DE'), '1.234,5');
  assert.equal(axis.fixedNumber(1234, 1, 'de-DE'), '1.234,0');
  assert.equal(axis.fixedNumber(1234, 0, 'de-DE'), '1.234');
  assert.equal(axis.fixedNumber(1234.5, 1, 'en-US'), '1,234.5');
});

test('countAxis: whole numbers only, from 0, a top that is a whole multiple of the step', () => {
  const small = axis.countAxis(3);
  assert.deepEqual([small.interval, small.max], [1, 3]);
  assert.deepEqual([...Array(small.max / small.interval + 1)].map((_, i) => small.format(i * small.interval)), ['0', '1', '2', '3']);
  const big = axis.countAxis(1234, 4, 'en-GB');
  assert.deepEqual([big.interval, big.max], [500, 1500]);
  assert.equal(big.format(1500), '1,500');
  const huge = axis.countAxis(98_765_432, 4, 'en-GB');
  assert.equal(huge.max % huge.interval, 0);
  assert.ok(huge.max >= 98_765_432);
  assert.equal(huge.format(huge.max).includes('.'), false);
  assert.equal(axis.countAxis(98_765_432, 4, 'de-DE').format(40_000_000), '40.000.000');
  // fractions of a request cannot exist
  assert.equal(axis.countAxis(0.2).max, 1);
  assert.equal(axis.countAxis(1).max, 1);
});

test('countAxis: zero, empty, negative and not-a-number series still give a usable axis', () => {
  for (const bad of [0, NaN, -4, Infinity, undefined, null, Math.max(...[])]) {
    const a = axis.countAxis(bad);
    assert.ok(Number.isFinite(a.max) && a.max >= 1, String(bad));
    assert.ok(Number.isInteger(a.interval) && a.interval >= 1, String(bad));
    assert.equal(a.format(0), '0');
  }
});

test('durationAxis: ms below one second, seconds from one second, one precision on every tick', () => {
  const ms = axis.durationAxis(840);
  assert.equal(ms.unit, 'ms');
  assert.deepEqual(ticksOf(ms).map(ms.format), ['0', '500', '1,000']);
  assert.ok(ms.maxMs >= 840);
  const sec = axis.durationAxis(1800, 4, 'en-GB');
  assert.equal(sec.unit, 's');
  const labels = ticksOf(sec).map(sec.format);
  assert.deepEqual(labels, ['0.0', '0.5', '1.0', '1.5', '2.0']);
  assert.equal(new Set(labels.map(decimalsOf)).size, 1, 'one number of decimals');
  const long = axis.durationAxis(68_000, 4, 'en-GB');
  assert.deepEqual(ticksOf(long).map(long.format), ['0', '20', '40', '60', '80']);
});

test('durationAxis: tiny, huge, zero and bad values', () => {
  const tiny = axis.durationAxis(0.4);
  assert.equal(tiny.unit, 'ms');
  assert.ok(tiny.intervalMs >= 1, 'a fraction of a millisecond is never a step');
  assert.ok(tiny.maxMs >= 1);
  const huge = axis.durationAxis(5_400_000, 4, 'en-GB'); // 90 minutes
  assert.equal(huge.unit, 's');
  assert.ok(huge.maxMs >= 5_400_000);
  assert.equal(huge.format(huge.maxMs).includes('.'), false);
  assert.match(huge.format(huge.maxMs), /^\d{1,3}(,\d{3})*$/);
  for (const bad of [0, NaN, -1, Infinity, undefined, null]) {
    const a = axis.durationAxis(bad);
    assert.equal(a.unit, 'ms', String(bad));
    assert.ok(Number.isFinite(a.maxMs) && a.maxMs >= 1, String(bad));
    assert.equal(a.format(0), '0');
    assert.equal(a.format(NaN), '—');
  }
});

test('durationAxis: floating point noise never prints a long tail', () => {
  for (const top of [100, 300, 700, 1100, 2300, 2900, 9999, 10_001, 123_456, 7_654_321]) {
    const a = axis.durationAxis(top);
    const labels = ticksOf(a).map(a.format);
    assert.ok(labels.every(label => !/\d{5,}\d*\.\d{4,}/.test(label)), top + ': ' + labels);
    assert.equal(new Set(labels.map(decimalsOf)).size, 1, top + ': ' + labels.join(' '));
    assert.ok(a.maxMs >= top);
  }
});

test('durationText: one rule for one value, whoever shows it: <1 ms, whole ms under a second, seconds with one decimal from a second', () => {
  const t = (v) => axis.durationText(v, 'en-GB');
  assert.equal(t(0), '0 ms');
  assert.equal(t(0.4), '<1 ms');
  assert.equal(t(1), '1 ms');
  assert.equal(t(43.4), '43 ms');
  assert.equal(t(999.4), '999 ms');
  assert.equal(t(999.6), '1.0 s', 'a value that rounds to 1000 ms is a second, as 1000 is');
  assert.equal(t(1000), '1.0 s');
  assert.equal(t(1049), '1.0 s');
  assert.equal(t(1050), '1.1 s');
  assert.equal(t(65_700), '65.7 s');
  assert.equal(t(68_000), '68.0 s');
  assert.equal(t(60_000), '60.0 s', 'no false second decimal on a minute');
  assert.equal(t(999_900), '999.9 s');
  assert.equal(t(999_960), '1,000 s', 'a decimal that rounds to 1000 s drops the decimal');
  assert.equal(t(5_400_000), '5,400 s', 'from 1,000 s on whole seconds');
  assert.equal(t(86_400_000), '86,400 s');
  assert.equal(axis.durationText(68_000, 'de-DE'), '68,0 s');
  for (const bad of [null, undefined, NaN, -1, Infinity, '12']) assert.equal(axis.durationText(bad), null, String(bad));
});

test('durationSet: the P95 and the slowest request of one interval are written by the same rule (65.7 s next to 68.0 s)', () => {
  assert.deepEqual(axis.durationSet([65_700, 68_000], 'en-GB'), ['65.7 s', '68.0 s']);
  assert.deepEqual(axis.durationSet([2500, 68_000], 'en-GB'), ['2.5 s', '68.0 s']);
  assert.deepEqual(axis.durationSet([120, 420], 'en-GB'), ['120 ms', '420 ms']);
});

test('durationSet: a short P95 beside a long outlier is never "68,000 ms" and the outlier reads the same as everywhere else', () => {
  assert.deepEqual(axis.durationSet([43, 68_000], 'en-GB'), ['43 ms', '68.0 s']);
  assert.deepEqual(axis.durationSet([340, 60_000], 'en-GB'), ['340 ms', '60.0 s']);
  assert.deepEqual(axis.durationSet([3, 68_000], 'en-GB'), ['3 ms', '68.0 s']);
  assert.deepEqual(axis.durationSet([0.4, 2], 'en-GB'), ['<1 ms', '2 ms']);
  assert.deepEqual(axis.durationSet([0, 68_000], 'en-GB'), ['0 ms', '68.0 s']);
  for (const out of [68_000, 60_000, 1500, 999.6]) assert.equal(axis.durationSet([43, out], 'en-GB')[1], axis.durationText(out, 'en-GB'));
});

test('durationSet: missing, NaN, negative and infinite values come back null, the rest still print', () => {
  assert.deepEqual(axis.durationSet([null, 1500], 'en-GB'), [null, '1.5 s']);
  assert.deepEqual(axis.durationSet([NaN, undefined, -3, Infinity]), [null, null, null, null]);
  assert.deepEqual(axis.durationSet([]), []);
  assert.deepEqual(axis.durationSet([null, null]), [null, null]);
  assert.deepEqual(axis.durationSet([12_345], 'de-DE'), ['12,3 s']);
});

test('tickLabels: the 0, half, top ticks of a lane axis share a unit and decimals', () => {
  assert.deepEqual(axis.tickLabels([0, 1250, 2500], 'en-GB'), ['0.00 s', '1.25 s', '2.50 s']);
  assert.deepEqual(axis.tickLabels([0, 0.5, 1], 'en-GB'), ['0.0 ms', '0.5 ms', '1.0 ms']);
  assert.deepEqual(axis.tickLabels([0, 150, 300], 'en-GB'), ['0 ms', '150 ms', '300 ms']);
  assert.deepEqual(axis.tickLabels([0, 30_000, 60_000], 'en-GB'), ['0 s', '30 s', '60 s']);
  assert.deepEqual(axis.tickLabels([0, NaN, 10], 'en-GB'), ['0 ms', '—', '10 ms']);
  assert.deepEqual(axis.tickLabels([]), []);
});

test('titles: the time title names the zone; an empty zone falls back to UTC; the latency title carries the unit', () => {
  assert.equal(axis.requestsAxisTitle, 'Requests per interval');
  assert.equal(axis.timeAxisTitle('Europe/London'), 'Time (Europe/London)');
  assert.equal(axis.timeAxisTitle('  '), 'Time (UTC)');
  assert.equal(axis.latencyAxisTitle('s'), 'Response time (s)');
  assert.equal(axis.latencyAxisTitle('ms'), 'Response time (ms)');
});

const MIN = 60_000;
const slot = (i, count, latency, max, extra = {}) => ({ at: Date.UTC(2026, 9, 24, 10, 0) + i * 5 * MIN, count, latency, max, ...extra });

test('heroOption: both value axes and the time axis have a title with the unit', () => {
  const windows = [slot(0, 12, 800, 1400), slot(1, 0, null, null), slot(2, 30, 65_700, 68_000)];
  const o = heroOption(windows, 'UTC', true);
  assert.equal(o.xAxis.name, 'Time (UTC)');
  assert.equal(o.yAxis[0].name, 'Requests per interval');
  assert.equal(o.yAxis[1].name, 'Response time (s)', 'the axis holds the P95 and the slowest request, so it is not titled for the P95 alone');
  assert.equal(o.yAxis[1].position, 'right');
});

test('heroOption: every tick of an axis has the same decimals, and the axes end on a whole step', () => {
  const windows = [slot(0, 3, 800, 1400), slot(1, 7, 1500, 1800), slot(2, 1234, 65_700, 68_000)];
  const o = heroOption(windows, 'UTC', true);
  for (const y of o.yAxis) {
    assert.equal(y.min, 0);
    assert.equal(Math.round(y.max / y.interval) * y.interval, y.max, 'the top is a whole number of steps');
    const labels = Array.from({ length: Math.round(y.max / y.interval) + 1 }, (_, i) => y.axisLabel.formatter(i * y.interval));
    assert.equal(new Set(labels.map(decimalsOf)).size, 1, labels.join(' | '));
  }
  assert.ok(o.yAxis[0].axisLabel.formatter(o.yAxis[0].max).length > 0);
});

test('heroOption: without latency there is one value axis and no right-hand margin for a title', () => {
  const o = heroOption([slot(0, 5, 200, 400)], 'UTC', false);
  assert.equal(o.yAxis.length, 1);
  assert.equal(o.grid.right, 14);
  const withLatency = heroOption([slot(0, 5, 200, 400)], 'UTC', true);
  assert.ok(withLatency.grid.right > 40);
});

test('heroOption: the margins grow with the label width (a count in the millions needs more room than 3)', () => {
  const few = heroOption([slot(0, 3, null, null)], 'UTC', false);
  const many = heroOption([slot(0, 4_200_000, null, null)], 'UTC', false);
  assert.ok(many.grid.left > few.grid.left);
  assert.ok(many.yAxis[0].nameGap > few.yAxis[0].nameGap);
  // the title never sits on the tick labels
  assert.ok(many.grid.left >= many.yAxis[0].nameGap + 14 + 3, 'the whole title and 3 px of air beyond the gap');
});

test('heroOption: tooltip names the interval start and end and writes P95 and the slowest request alike', () => {
  const start = Date.UTC(2026, 9, 24, 10, 0);
  const windows = [{ ...slot(0, 1234, 65_700, 68_000), start, end: start + 5 * MIN }];
  const text = heroOption(windows, 'UTC', true).tooltip.formatter([{ dataIndex: 0 }]);
  assert.ok(text.includes('24 Oct 10:00:00 to 10:05:00 UTC'), text);
  assert.ok(text.includes('P95: ≈ 65.7 s'), text);
  assert.ok(text.includes('Slowest request: 68.0 s'), text);
  assert.match(text, /Requests: 1.?234/);
});

test('heroOption: a tooltip without an end falls back to the start time; the end of a one-instant bucket is not repeated', () => {
  const noEnd = heroOption([slot(0, 5, 200, 400)], 'UTC', true).tooltip.formatter([{ dataIndex: 0 }]);
  assert.ok(noEnd.startsWith('24 Oct 10:00:00 UTC'), noEnd);
  const at = Date.UTC(2026, 9, 24, 10, 0);
  const same = heroOption([{ ...slot(0, 5, 200, 400), start: at, end: at }], 'UTC', true).tooltip.formatter([{ dataIndex: 0 }]);
  assert.ok(same.startsWith('24 Oct 10:00:00 UTC'), same);
});

test('heroOption: empty, all-zero and NaN series build an option with finite axes and no NaN in any text', () => {
  const cases = [
    [],
    [slot(0, 0, null, null)],
    [slot(0, NaN, NaN, NaN)],
    [slot(0, 0, 0, 0), slot(1, 0, 0, 0)],
    [slot(0, 5, 0.3, 0.4)],
    [slot(0, 5, null, 80_000), slot(1, 1, 3, 68_000)]
  ];
  for (const windows of cases) {
    const o = heroOption(windows, 'UTC', true);
    for (const y of o.yAxis) {
      assert.ok(Number.isFinite(y.max) && y.max > 0 && Number.isFinite(y.interval) && y.interval > 0, JSON.stringify(windows));
      assert.ok(!/NaN|undefined|Infinity/.test(y.axisLabel.formatter(y.max)), JSON.stringify(windows));
    }
    if (windows.length) {
      const text = o.tooltip.formatter([{ dataIndex: 0 }]);
      assert.ok(!/NaN|undefined|Infinity/.test(text), text);
    }
  }
  assert.equal(heroOption([], 'UTC', true).tooltip.formatter([{ dataIndex: 0 }]), '');
});

test('heroOption: the diamond label uses the one duration rule, and a slot with no timing says so', () => {
  const windows = [slot(0, 10, 500, 500), slot(1, 10, 600, 68_000), slot(2, 4, null, null)];
  const o = heroOption(windows, 'UTC', true);
  const scatter = o.series.find(s => s.name === 'Slowest request');
  const diamond = scatter.data[1];
  assert.equal(diamond.symbol, 'diamond');
  assert.equal(diamond.label.formatter(), '68.0 s');
  assert.ok(o.tooltip.formatter([{ dataIndex: 2 }]).includes('P95: no timed requests'));
});

test('heroOption: German-style locales are not hard-wired: the tick labels come from the one formatter', () => {
  const o = heroOption([slot(0, 4_200_000, null, null)], 'UTC', false);
  assert.match(o.yAxis[0].axisLabel.formatter(2_000_000), /^2.000.000$|^2,000,000$|^2\s000\s000$/);
});

test('one slow request reads the same on the diamond, in the tooltip and in the note (P95 43 ms, slowest 68 s)', () => {
  const windows = [slot(0, 10, 43, 60), slot(1, 10, 43, 68_000), slot(2, 10, 45, 70)];
  const o = heroOption(windows, 'UTC', true);
  const diamond = o.series.find(s => s.name === 'Slowest request').data[1].label.formatter();
  const tip = o.tooltip.formatter([{ dataIndex: 1 }]);
  const note = axis.durationText(68_000);
  assert.equal(diamond, '68.0 s');
  assert.equal(note, '68.0 s');
  assert.ok(tip.includes('P95: ≈ 43 ms'), tip);
  assert.ok(tip.includes('Slowest request: ' + diamond), tip);
  assert.ok(!/68,000/.test(tip + diamond + note));
});

test('heroOption: the right margin and the left margin follow the data (a count in the millions takes about 96 px, a count under 100 takes 46)', () => {
  const small = heroOption([slot(0, 10, 200, 300)], 'UTC', true);
  const millions = heroOption([slot(0, 2_450_000, 200, 300)], 'UTC', true);
  assert.equal(small.grid.left, 46);
  assert.ok(millions.grid.left >= 92 && millions.grid.left <= 102, 'left margin ' + millions.grid.left);
});

test('durationAxis: the unit switches where durationText switches (a top of 999.6 ms is a second on the axis and on the diamond)', () => {
  const a = axis.durationAxis(999.6, 4, 'en-GB');
  assert.equal(a.unit, 's');
  assert.equal(axis.durationText(999.6, 'en-GB'), '1.0 s');
  assert.equal(axis.durationAxis(999.4).unit, 'ms');
  assert.equal(axis.durationText(999.4, 'en-GB'), '999 ms');
});

test('screen-reader and visible duration text come from one rule: no chart file formats a duration with the old formatters', () => {
  for (const file of ['src/components/TracingChart.svelte', 'src/components/Pulse.svelte', 'src/lib/tracing/auditHero.ts', 'src/lib/charts.ts']) {
    const source = fs.readFileSync(path.join(root, file), 'utf8');
    // charts.ts keeps axisDuration only for the other charts (response time, change versus reference); the Requests-over-time code must not use it.
    if (file === 'src/lib/charts.ts') {
      const hero = source.slice(source.indexOf('export const heroOption'), source.indexOf('export const histogramBinBounds'));
      assert.ok(!/axisDuration|formatDuration/.test(hero), 'heroOption');
    } else {
      assert.ok(!/formatDuration|axisDuration/.test(source), file);
    }
  }
  const lane = fs.readFileSync(path.join(root, 'src/components/TracingChart.svelte'), 'utf8');
  assert.match(lane, /class="latency-max">\{lineName\(lane\.key\)\}: up to \{durationText\(/);
  // The screen-reader text is the same string the tooltip and the diamond print for the same value.
  assert.equal(axis.durationText(5.5, 'en-GB'), '6 ms');
  assert.equal(axis.durationText(75_000, 'en-GB'), '75.0 s');
});
