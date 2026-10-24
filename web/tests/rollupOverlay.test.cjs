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

const o = loadTypeScript('src/lib/rollupOverlay.ts');
const { deck } = loadTypeScript('src/lib/copy/deck.ts');
const { lintScreenStrings } = loadTypeScript('src/lib/copy/copylint.ts');

const q = (ms) => ({ lowerMs: ms, upperMs: ms, valueMs: ms });
const cell = (date, hour, over = {}) => ({
  date, hour, start: date + 'T' + String(hour).padStart(2, '0') + ':00:00Z', end: date + 'T' + String(hour).padStart(2, '0') + ':59:59Z',
  count: 40, latencyCount: 30, p95: q(100 + hour), slowCount: 1, noise: false, ...over
});
const fullDay = (date, over = {}, hours = [...Array(24).keys()]) => hours.map(hour => cell(date, hour, typeof over === 'function' ? over(hour) : over));
const gridOf = (days, cells) => ({ noiseBelow: 5, days, cells });
const dayRow = (date, hours = 24, partial = false) => ({ date, hours, partial });

test('the default pair is the latest two whole days, oldest first', () => {
  const choices = o.overlayChoices(gridOf(
    [dayRow('2001-03-01'), dayRow('2001-03-02'), dayRow('2001-03-03'), dayRow('2001-03-04', 24, true)],
    [...fullDay('2001-03-01'), ...fullDay('2001-03-02'), ...fullDay('2001-03-03'), ...fullDay('2001-03-04', {}, [0, 1, 2])]));
  assert.equal(choices.length, 4);
  assert.deepEqual(o.defaultOverlayDays(choices), ['2001-03-02', '2001-03-03']);
  assert.match(choices[3].label, /partial/);
});

test('the default pair falls back to partial days, and is null under two days', () => {
  const two = o.overlayChoices(gridOf([dayRow('2001-03-01', 24, true), dayRow('2001-03-02', 24, true)], [...fullDay('2001-03-01', {}, [3]), ...fullDay('2001-03-02', {}, [4])]));
  assert.deepEqual(o.defaultOverlayDays(two), ['2001-03-01', '2001-03-02']);
  const one = o.overlayChoices(gridOf([dayRow('2001-03-01')], fullDay('2001-03-01')));
  assert.equal(o.defaultOverlayDays(one), null);
  assert.equal(o.defaultOverlayDays([]), null);
  assert.deepEqual(o.overlayChoices(gridOf([], [])), []);
});

test('a day has 24 points on the 0 to 23 axis and the metrics read the right field', () => {
  const grid = gridOf([dayRow('2001-03-01')], fullDay('2001-03-01', hour => ({ count: 10 + hour })));
  const day = o.overlayDay(grid, '2001-03-01');
  assert.equal(day.points.length, 24);
  assert.equal(o.pointValue(day.points[5], 'p95'), 105);
  assert.equal(o.pointValue(day.points[5], 'count'), 15);
  assert.equal(o.overlayDay(grid, '2001-04-01'), null);
});

test('noise cells are dashed for counts and gaps for P95; masked P95 is a gap', () => {
  const cells = fullDay('2001-03-01', hour => hour === 4 ? { count: 2, noise: true, p95: null, latencyCount: 2 } : hour === 5 ? { count: 12, p95: null, latencyCount: 12 } : hour === 6 ? { count: 0, noise: true, p95: null, latencyCount: 0 } : {});
  const day = o.overlayDay(gridOf([dayRow('2001-03-01')], cells), '2001-03-01');
  const p95 = o.overlayLine(day, 'p95');
  assert.equal(p95.solid[4], null);
  assert.equal(p95.solid[5], null);
  assert.equal(p95.solid[6], null);
  assert.equal(p95.solid[3], 103);
  assert.deepEqual(p95.dashedHours, []);
  const count = o.overlayLine(day, 'count');
  assert.deepEqual(count.dashedHours, [4, 6]);
  assert.equal(count.solid[4], null);
  assert.equal(count.dashed[4], 2);
  assert.equal(count.dashed[6], 0, 'a count of zero is a real value');
  // The plain points beside a dashed point carry the dashed bridge.
  assert.equal(count.dashed[3], 40);
  assert.equal(count.dashed[5], 12);
  assert.equal(count.dashed[7], 40);
  assert.equal(count.dashed[10], null);
  assert.equal(count.solid[3], 40);
});

test('a 23-hour day leaves the skipped hour as a gap and a 25-hour day is still 24 points', () => {
  const spring = o.overlayDay(gridOf([dayRow('2001-03-11', 23)], fullDay('2001-03-11', {}, [...Array(24).keys()].filter(hour => hour !== 2))), '2001-03-11');
  assert.equal(spring.points.length, 24);
  assert.equal(spring.points[2].exists, false);
  assert.equal(o.pointValue(spring.points[2], 'count'), null);
  assert.equal(o.overlayLine(spring, 'count').solid[2], null);
  assert.match(spring.label, /23 h day/);
  // A repeated hour is one cell on the server; a second cell with the same hour must not crash or add a point.
  const fall = o.overlayDay(gridOf([dayRow('2001-11-04', 25)], [...fullDay('2001-11-04'), cell('2001-11-04', 1, { count: 90 })]), '2001-11-04');
  assert.equal(fall.points.length, 24);
  assert.match(fall.label, /25 h day/);
  assert.ok(o.overlayNotes([spring, fall], 'count').some(note => /23-hour/.test(note)));
  assert.ok(o.overlayNotes([spring, fall], 'count').some(note => /25-hour/.test(note)));
});

test('a cell with an out-of-range hour or no p95 object is tolerated', () => {
  const cells = [cell('2001-03-01', 99), cell('2001-03-01', -1), { ...cell('2001-03-01', 7), p95: undefined }, cell('2001-03-01', 8, { p95: { lowerMs: null, upperMs: null, valueMs: null } })];
  const day = o.overlayDay(gridOf([dayRow('2001-03-01')], cells), '2001-03-01');
  assert.equal(day.points[7].p95Ms, null);
  assert.equal(day.points[8].p95Ms, null);
  assert.equal(o.pointText(day.points[8], 'p95'), o.overlayCopy.fewTimings);
  assert.equal(o.pointText(day.points[0], 'p95'), o.overlayCopy.noHour);
});

test('the words of a point name the case: few requests, few timings, a real value', () => {
  const real = { hour: 9, exists: true, count: 40, p95Ms: 450, noise: false };
  assert.equal(o.pointText(real, 'p95'), '≈ 450 ms');
  assert.equal(o.pointText(real, 'count'), '40 requests');
  assert.equal(o.pointText({ ...real, count: 1, noise: true, p95Ms: null }, 'count'), '1 request (few)');
  assert.equal(o.pointText({ ...real, count: 3, noise: true, p95Ms: null }, 'p95'), o.overlayCopy.fewRequests);
  assert.equal(o.hourText(7), '07:00');
});

test('the caption names the zone and the summary names both days, the high and the low', () => {
  assert.match(o.overlayCaption('p95', 'Asia/Kolkata'), /Asia\/Kolkata time/);
  assert.match(o.overlayCaption('count', 'UTC'), /follow UTC/);
  const grid = gridOf([dayRow('2001-03-01'), dayRow('2001-03-02')], [...fullDay('2001-03-01'), ...fullDay('2001-03-02', hour => ({ count: hour === 3 ? 1 : 40, noise: hour === 3, p95: hour === 3 ? null : q(500) }))]);
  const a = o.overlayDay(grid, '2001-03-01'), b = o.overlayDay(grid, '2001-03-02');
  const text = o.overlaySummary(a, b, 'p95', 'UTC');
  assert.match(text, /1 Mar/);
  assert.match(text, /2 Mar/);
  assert.match(text, /highest ≈ 123 ms at 23:00/);
  assert.match(text, /lowest ≈ 100 ms at 00:00/);
  assert.match(text, /1 hour left blank/);
  const none = o.overlayDay(gridOf([dayRow('2001-03-05')], [cell('2001-03-05', 1, { noise: true, p95: null })]), '2001-03-05');
  assert.match(o.overlaySummary(none, none, 'p95', 'UTC'), /no values/);
});

test('the chart option has two solid series, two dashed ones, 24 hours and a tooltip that escapes', () => {
  const grid = gridOf([dayRow('2001-03-01'), dayRow('2001-03-02')], [...fullDay('2001-03-01'), ...fullDay('2001-03-02', hour => hour === 4 ? { count: 2, noise: true, p95: null } : {})]);
  const a = o.overlayDay(grid, '2001-03-01'), b = o.overlayDay(grid, '2001-03-02');
  const palette = { text: '#000', text2: '#555', axisLine: '#ccc', grid: '#eee', surface: '#fff', series: ['#111', '#222'] };
  const option = o.overlayOption(a, b, 'count', palette);
  assert.equal(option.series.length, 4);
  assert.equal(option.xAxis.data.length, 24);
  assert.equal(option.xAxis.data[0], '00:00');
  assert.deepEqual([option.series[0].name, option.series[2].name], [a.label, b.label]);
  assert.equal(option.legend, undefined, 'the HTML key is the legend');
  assert.equal(option.series[0].lineStyle.type, 'solid');
  assert.equal(option.series[1].lineStyle.type, 'dashed');
  assert.equal(option.series[3].data[4].itemStyle.borderColor, '#222');
  assert.equal(option.series[3].data[0].value, null);
  assert.equal(option.yAxis.minInterval, 1);
  const evil = o.overlayOption({ ...a, label: '<b>x</b>' }, b, 'count', palette);
  assert.doesNotMatch(evil.tooltip.formatter([{ dataIndex: 4 }]), /<b>x<\/b>/);
  assert.match(evil.tooltip.formatter([{ dataIndex: 4 }]), /04:00/);
  assert.equal(o.overlayOption(a, b, 'p95', palette).yAxis.minInterval, undefined);
  assert.equal(o.overlayOption(a, b, 'p95', palette).yAxis.axisLabel.formatter(1500), '1.5 s');
});

test('the table rows have one line per hour', () => {
  const grid = gridOf([dayRow('2001-03-01'), dayRow('2001-03-02', 23)], [...fullDay('2001-03-01'), ...fullDay('2001-03-02', {}, [...Array(24).keys()].filter(hour => hour !== 2))]);
  const rows = o.overlayRows(o.overlayDay(grid, '2001-03-01'), o.overlayDay(grid, '2001-03-02'), 'count');
  assert.equal(rows.length, 24);
  assert.equal(rows[2].second, o.overlayCopy.noHour);
  assert.equal(rows[0].hour, '00:00');
});

test('the words of the overlay pass the copy lint: plain, no causal claims, jargon explained', () => {
  const day = (over = {}) => ({ date: '2001-03-11', label: '11 Mar (23 h day)', hours: 23, partial: true, points: [], ...over });
  const strings = [
    ...Object.values(o.overlayCopy), ...o.overlayMetrics.map(item => item.label),
    o.overlayCaption('p95', 'Asia/Kolkata'), o.overlayCaption('count', 'UTC'),
    ...o.overlayNotes([day(), day({ hours: 25, partial: false })], 'p95'), ...o.overlayNotes([day()], 'count'),
    o.overlayDayLabel({ date: '2001-11-04', hours: 25, partial: false })
  ];
  const entries = strings.map((text, index) => ({ id: 'overlay' + index, text }));
  assert.ok(entries.length > 15);
  assert.deepEqual(lintScreenStrings(entries, deck.terms), []);
  for (const { text } of entries) assert.doesNotMatch(text, /\bcause|\bbecause\b|\bdue to\b|\bled to\b|\bresponsible\b/i, text);
});
