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

const { chartTime, chartRange, pickTickStep, roundTicks, axisLabels } = loadTypeScript('src/lib/chartTime.ts');
const { axisDuration, rangeLabel, niceHistogram } = loadTypeScript('src/lib/chartScale.ts');
const { gapBridge, heroOutliers } = loadTypeScript('src/lib/chartHero.ts');
const { timelineLineStyle, timelineDashArray } = loadTypeScript('src/lib/charts.ts');

const T = Date.UTC(2026, 9, 24, 15, 19, 37);
const MIN = 60_000, HOUR = 3_600_000;

test('chartTime prints day, month, clock and the zone once', () => {
  assert.equal(chartTime(T, 'UTC'), '24 Oct 15:19:37 UTC');
  assert.equal(chartTime(T, 'UTC', { zone: false }), '24 Oct 15:19:37');
  assert.equal(chartTime(T, 'UTC', { date: false, seconds: false, zone: false }), '15:19');
  assert.equal(chartTime(T, 'UTC', { time: false, zone: false }), '24 Oct');
  assert.equal(chartTime('2026-10-24T15:19:37Z', 'UTC'), '24 Oct 15:19:37 UTC');
  assert.equal(chartTime(T, 'Asia/Kolkata', { zone: false }), '24 Oct 20:49:37');
  assert.equal(chartTime('nonsense', 'UTC'), 'Unknown time');
});

test('chartRange names the zone once, repeats the date only across midnight, and reads an instant as "at"', () => {
  assert.equal(chartRange(T, T + 15 * MIN, 'UTC'), '24 Oct 15:19:37 to 15:34:37 UTC');
  assert.equal(chartRange(T, T + 12 * HOUR, 'UTC'), '24 Oct 15:19:37 to 25 Oct 03:19:37 UTC');
  assert.equal(chartRange(T, T, 'UTC'), 'at 24 Oct 15:19:37 UTC');
  assert.equal(chartRange(T, T + MIN, 'UTC', { seconds: false }), '24 Oct 15:19 to 15:20 UTC');
  assert.equal(chartRange(T, 'x', 'UTC'), 'Unknown time range');
});

test('pickTickStep chooses the smallest round step that fits the label budget', () => {
  assert.equal(pickTickStep(3 * HOUR), 30 * MIN);
  assert.equal(pickTickStep(4 * HOUR), HOUR);
  assert.equal(pickTickStep(4 * HOUR, 20), 15 * MIN);
  assert.equal(pickTickStep(60_000), 10_000);
  assert.equal(pickTickStep(365 * 24 * HOUR), 7 * 24 * HOUR);
});

test('roundTicks lands on round clock times in the display zone', () => {
  const start = Date.UTC(2026, 9, 24, 15, 19, 37);
  const utc = roundTicks(start, start + 2 * HOUR, 30 * MIN, 'UTC');
  assert.deepEqual(utc.map(at => chartTime(at, 'UTC', { date: false, seconds: false, zone: false })), ['15:30', '16:00', '16:30', '17:00']);
  const india = roundTicks(start, start + 2 * HOUR, 30 * MIN, 'Asia/Kolkata');
  assert.deepEqual(india.map(at => chartTime(at, 'Asia/Kolkata', { date: false, seconds: false, zone: false })), ['21:00', '21:30', '22:00', '22:30']);
  // a zone with a 45 minute offset still gets whole local hours
  const nepal = roundTicks(start, start + 2 * HOUR, HOUR, 'Asia/Kathmandu');
  assert.deepEqual(nepal.map(at => chartTime(at, 'Asia/Kathmandu', { date: false, seconds: false, zone: false })), ['22:00', '23:00']);
});

test('axisLabels is empty except on round times, with the date on the first label and after midnight', () => {
  const start = Date.UTC(2026, 9, 24, 15, 19, 0);
  const slots = Array.from({ length: 600 }, (_, i) => start + i * MIN);
  const labels = axisLabels(slots, 'UTC');
  const shown = labels.filter(Boolean);
  assert.ok(shown.length >= 2 && shown.length <= 8, 'a handful of labels, not one per slot: ' + shown.length);
  assert.match(shown[0], /^24 Oct \d\d:\d\d$/);
  for (const label of shown) assert.match(label, /(:00|:30)$/, 'labels sit on round times: ' + label);
  assert.ok(shown.slice(1).some(label => /^25 Oct /.test(label)), 'the first label after midnight carries the date');
  assert.ok(shown.filter(label => /^\d\d:\d\d$/.test(label)).length > 0, 'later labels in the same day are time only');
  assert.equal(labels.length, slots.length);
});

test('axisLabels never puts a label on a tick at the exclusive window end', () => {
  // ten one-minute slots from 15:00 to 15:09: the 15:10 tick is the end of the window, not a bar inside it
  const start = Date.UTC(2026, 9, 24, 15, 0, 0);
  const labels = axisLabels(Array.from({ length: 10 }, (_, i) => start + i * MIN), 'UTC');
  assert.ok(!labels.includes('15:10'), 'no 15:10 label: ' + JSON.stringify(labels));
  assert.equal(labels[labels.length - 1], '', 'the last bar (15:09) is not labelled 15:10');
});

test('axisLabels handles empty, single and day-long spans', () => {
  assert.deepEqual(axisLabels([], 'UTC'), []);
  assert.deepEqual(axisLabels([T], 'UTC'), ['15:19'].map(() => chartTime(T, 'UTC', { seconds: false, zone: false })));
  const days = Array.from({ length: 30 }, (_, i) => Date.UTC(2026, 9, 1 + i));
  const shown = axisLabels(days, 'UTC').filter(Boolean);
  assert.ok(shown.length > 1);
  for (const label of shown) assert.match(label, /^\d{1,2} [A-Z][a-z]{2}$/, 'whole-day steps print the date only: ' + label);
});

test('axisDuration never prints 0.0 ms and switches to seconds at one second', () => {
  assert.equal(axisDuration(0), '0 ms');
  assert.equal(axisDuration(0.04), '0 ms');
  assert.equal(axisDuration(250), '250 ms');
  assert.equal(axisDuration(1000), '1 s');
  assert.equal(axisDuration(1500), '1.5 s');
  assert.equal(axisDuration(12_340), '12.3 s');
  assert.equal(axisDuration(61_000), '61 s');
  assert.equal(axisDuration(Number.NaN), '');
  for (const value of [0, 0.01, 1, 99.9, 999.6]) assert.doesNotMatch(axisDuration(value), /\.\d ms/);
});

test('rangeLabel prints the unit once when both ends share it', () => {
  assert.equal(rangeLabel(50, 75), '50–75 ms');
  assert.equal(rangeLabel(1000, 2000), '1–2 s');
  assert.equal(rangeLabel(500, 1000), '500 ms–1 s');
  assert.equal(rangeLabel(5000, null), '≥ 5 s');
});

// The server's response-time ranges are each 10 % wider than the last.
const rawBins = (lows, count = 1) => lows.map(lower => ({ lower, upper: lower * 1.1, current: count, reference: 0 }));

test('niceHistogram returns nothing for an empty histogram', () => {
  assert.deepEqual(niceHistogram([]), []);
  assert.deepEqual(niceHistogram([{ lower: 100, upper: 110, current: 0, reference: 0 }]), []);
});

test('niceHistogram keeps every request exactly once', () => {
  const bins = rawBins([100, 110, 121, 133.1, 146.41, 161.05, 177.16, 194.87], 3);
  const out = niceHistogram(bins);
  assert.equal(out.reduce((sum, bin) => sum + bin.current, 0), 24);
  assert.equal(out.reduce((sum, bin) => sum + bin.reference, 0), 0);
});

test('niceHistogram uses linear round steps for a narrow band', () => {
  const out = niceHistogram(rawBins([100, 110, 121, 133.1, 146.41, 161.05, 177.16, 194.87]));
  assert.ok(out.length >= 3 && out.length <= 12, 'bin count ' + out.length);
  for (const bin of out) {
    assert.ok(Number.isInteger(bin.lower) && Number.isInteger(bin.upper), 'whole-number edges: ' + bin.label);
    assert.doesNotMatch(bin.label, /\./, 'no decimals in a narrow band: ' + bin.label);
  }
  const widths = new Set(out.map(bin => bin.upper - bin.lower));
  assert.equal(widths.size, 1, 'equal widths');
});

test('niceHistogram uses 1-2-5 edges across orders of magnitude and keeps the overflow range apart', () => {
  const bins = [...rawBins([5, 50, 500, 5000]), { lower: 60_000, upper: null, current: 2, reference: 1 }];
  const out = niceHistogram(bins);
  const finite = out.filter(bin => bin.upper != null);
  for (const bin of finite) assert.match(String(bin.lower).replace(/0+$/, '') || '0', /^[125]?$/, 'edge ' + bin.lower);
  const last = out[out.length - 1];
  assert.equal(last.upper, null);
  assert.equal(last.current, 2);
  assert.equal(last.reference, 1);
  assert.match(last.label, /^≥ 1 min|^≥ 60 s/);
  assert.equal(out.filter(bin => bin.upper == null).length, 1);
});

test('gapBridge draws a straight line across empty intervals only between two measured ones', () => {
  assert.deepEqual(gapBridge([null, 10, null, null, 40, null]), [null, 10, 20, 30, 40, null]);
  assert.deepEqual(gapBridge([10, 20, 30]), [null, null, null]);
  assert.deepEqual(gapBridge([null, null]), [null, null]);
  assert.deepEqual(gapBridge([5, null, 5, null, 9]), [5, 5, 5, 7, 9]);
  assert.deepEqual(gapBridge([]), []);
});

test('heroOutliers flags a slowest request far above the usual P95 and labels the worst few', () => {
  const normal = Array.from({ length: 10 }, (_, i) => ({ at: i, count: 20, latency: 200, max: 400 }));
  assert.deepEqual(heroOutliers(normal), { typical: 200, flagged: [], labelled: [] });
  const slots = normal.map((slot, i) => i === 2 ? { ...slot, max: 60_000 } : i === 5 ? { ...slot, max: 30_000 } : i === 7 ? { ...slot, max: 5000 } : i === 8 ? { ...slot, max: 2500 } : slot);
  const out = heroOutliers(slots);
  assert.equal(out.typical, 200);
  assert.deepEqual(out.flagged, [2, 5, 7, 8]);
  assert.deepEqual(out.labelled, [2, 5, 7], 'the three slowest, in time order');
  assert.deepEqual(heroOutliers(slots, 1).labelled, [2]);
});

test('heroOutliers never flags below one second and ignores intervals without a maximum', () => {
  const quick = Array.from({ length: 6 }, (_, i) => ({ at: i, count: 5, latency: 10, max: i === 3 ? 900 : 20 }));
  assert.deepEqual(heroOutliers(quick).flagged, []);
  const missing = [{ at: 0, count: 0, latency: null, max: null }, { at: 1, count: 0, latency: null }];
  assert.deepEqual(heroOutliers(missing), { typical: null, flagged: [], labelled: [] });
});

test('timelineLineStyle varies dash and width so overlapping lines are not told apart by colour alone', () => {
  const styles = Array.from({ length: 8 }, (_, i) => timelineLineStyle(i));
  const keys = new Set(styles.map(style => style.type + style.width));
  assert.ok(keys.size >= 3, 'at least three distinct line looks');
  assert.equal(styles[0].type, 'solid');
  assert.equal(timelineDashArray(0), '');
  assert.ok(styles.some(style => style.type !== 'solid'));
  for (const style of styles) assert.ok(style.width >= 1.5 && style.width <= 4);
  assert.deepEqual(styles.slice(0, 3).map(style => style.type), ['solid', 'dashed', 'dotted']);
  assert.ok(styles[0].width > styles[1].width, 'the first group is the heaviest line');
});
