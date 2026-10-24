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

// B9 (dating a step), the audit-create series, the fixed-duration check and the daily view: the pure helpers behind them.
const patterns = loadTypeScript('src/lib/patterns.ts');
const { deck } = loadTypeScript('src/lib/copy/deck.ts');
const { lintCopy } = loadTypeScript('src/lib/copy/copylint.ts');

const DAY = 86_400_000;
const lintOk = (strings) => {
  assert.deepEqual(lintCopy(strings.map((text, i) => ({ id: 'b9-' + i, text })), deck.terms), []);
  for (const text of strings) assert.doesNotMatch(text, /\b(caused|because|due to|waiting on|blocked by|retry|complete|proven)\b/i, text);
};
const query = { sourceIds: ['demo-west'], start: '2026-10-17T00:00:00Z', end: '2026-10-24T00:00:00Z' };

const step = (over = {}) => ({
  zone: 'UTC', start: '2026-07-26T00:00:00Z', end: '2026-10-24T00:00:00Z', daysRead: 88, daysLeftOut: 0, partial: false,
  date: '2026-10-05', sharing: ['Invoices.Search', 'Orders.Submit', 'Catalog.Browse'],
  split: { date: '2026-10-05', zone: 'UTC', baselineStart: '2026-09-28T00:00:00Z', baselineEnd: '2026-10-05T00:00:00Z', start: '2026-10-05T00:00:00Z', end: '2026-10-12T00:00:00Z' },
  operations: [
    { operation: 'Health.Ping', volume: 4000, daysUsed: 88, found: false, reason: 'The p95 changed by less than 2 times.' },
    { operation: 'Invoices.Search', volume: 900, daysUsed: 88, found: true, date: '2026-10-05', beforeMs: 300, afterMs: 1500, ratio: 5, trafficRatio: 1 },
    { operation: 'Orders.Submit', volume: 1200, daysUsed: 88, found: true, date: '2026-10-05', beforeMs: 800, afterMs: 2400, ratio: 3, trafficRatio: 1 },
    { operation: 'Catalog.Browse', volume: 700, daysUsed: 88, found: true, date: '2026-10-05', beforeMs: 200, afterMs: 600, ratio: 3, trafficRatio: 1 },
    { operation: 'Accounts.Summary', volume: 500, daysUsed: 88, found: true, date: '2026-10-06', beforeMs: 400, afterMs: 100, ratio: 4, trafficRatio: 1 }
  ],
  ...over
});

// ------------------------------------------------------------------ the request

test('stepWindow reads the last N whole days up to the end of the range, and refuses bad spans', () => {
  const window = patterns.stepWindow(query, 90);
  assert.equal(Date.parse(window.end) - Date.parse(window.start), 90 * DAY);
  assert.equal(window.end, '2026-10-24T00:00:00.000Z');
  for (const bad of [0, -1, 121, 1.5, NaN]) assert.equal(patterns.stepWindow(query, bad), null, String(bad));
  assert.equal(patterns.stepWindow({ end: 'not a date' }, 30), null);
  assert.equal(patterns.stepWindow(query, 120) !== null, true);
});

test('the request is unchanged unless step dating or the series is asked for', () => {
  const plain = patterns.windowInputSeconds(query, '', '', false);
  assert.deepEqual(plain.input, { sourceIds: ['demo-west'], start: query.start, end: query.end });
  const withEmptyExtras = patterns.windowInputSeconds(query, '', '', false, { stepDays: 0, auditSeries: false, zone: 'Asia/Kolkata' });
  assert.deepEqual(withEmptyExtras.input, plain.input, 'a zone alone adds nothing');
});

test('step dating and the series add their own fields and the display zone', () => {
  const built = patterns.windowInputSeconds(query, '', '', false, { stepDays: 60, auditSeries: true, zone: 'Asia/Kolkata' });
  assert.equal(built.input.auditSeries, true);
  assert.equal(built.input.timeZone, 'Asia/Kolkata');
  assert.equal(built.input.stepChange.end, query.end.replace('Z', '.000Z'));
  assert.equal(Date.parse(built.input.stepChange.end) - Date.parse(built.input.stepChange.start), 60 * DAY);
  const seriesOnly = patterns.windowInputSeconds(query, '', '', false, { auditSeries: true, zone: 'UTC' });
  assert.equal(seriesOnly.input.stepChange, undefined);
  assert.equal(seriesOnly.input.timeZone, 'UTC');
  assert.ok('error' in patterns.windowInputSeconds(query, '', '', false, { stepDays: 500, zone: 'UTC' }));
  assert.ok('error' in patterns.windowInputSeconds(query, '', '', false, { stepDays: 2.5, zone: 'UTC' }));
});

// ------------------------------------------------------------------ B9 section

test('step rows: operations with a step first, busiest first, the shared date marked', () => {
  const rows = patterns.stepRows(step());
  assert.deepEqual(rows.map((r) => r.operation), ['Orders.Submit', 'Invoices.Search', 'Catalog.Browse', 'Accounts.Summary', 'Health.Ping']);
  const orders = rows[0];
  assert.equal(orders.before, '0.8 s');
  assert.equal(orders.after, '2.4 s');
  assert.equal(orders.change, '3 times longer');
  assert.equal(orders.result, patterns.STEP_TEXT.stepShared);
  const accounts = rows[3];
  assert.equal(accounts.result, patterns.STEP_TEXT.stepOwn, 'a step on another date does not share the date');
  assert.equal(accounts.change, '4 times shorter');
  const flat = rows[4];
  assert.equal(flat.found, false);
  assert.equal(flat.result, patterns.STEP_TEXT.stepNone);
  assert.equal(flat.reason, 'The p95 changed by less than 2 times.');
  assert.equal(flat.before, '–');
});

test('step section: hidden unless asked, a plain absence line when asked and missing, rows when present', () => {
  assert.deepEqual(patterns.stepSection({}, false), { kind: 'hidden' });
  const missing = patterns.stepSection({}, true);
  assert.equal(missing.kind, 'note');
  assert.match(missing.text, /^Not observed \(gap or sampling: /);
  const section = patterns.stepSection({ stepChange: step() }, true);
  assert.equal(section.kind, 'rows');
  assert.equal(section.date, '2026-10-05');
  assert.equal(section.daysLine, '88 whole days were read.');
  assert.ok(section.split && section.scope);
});

test('days line: one day, and days read only in part', () => {
  assert.equal(patterns.stepDaysLine({ daysRead: 1, daysLeftOut: 0 }), '1 whole day was read.');
  assert.equal(patterns.stepDaysLine({ daysRead: 80, daysLeftOut: 2 }), '80 whole days were read. 2 read only in part and left out.');
});

test('no date shared: no split is offered', () => {
  const section = patterns.stepSection({ stepChange: step({ date: '', sharing: [], split: undefined }) }, true);
  assert.equal(section.kind, 'rows');
  assert.equal(section.split, null);
  assert.equal(section.scope, null);
});

test('splitScope: valid split gives the page fields, with a bucket the page accepts', () => {
  const patch = patterns.splitScope(step().split);
  assert.equal(patch.baselineStart, '2026-09-28T00:00:00.000Z');
  assert.equal(patch.baselineEnd, '2026-10-05T00:00:00.000Z');
  assert.equal(patch.start, '2026-10-05T00:00:00.000Z');
  assert.equal(patch.end, '2026-10-12T00:00:00.000Z');
  assert.ok(Number.isInteger(patch.bucketSeconds) && patch.bucketSeconds > 0);
  assert.ok((Date.parse(patch.end) - Date.parse(patch.start)) / (patch.bucketSeconds * 1000) <= 120, 'at most 120 buckets');
});

test('splitScope: refuses what the page would refuse', () => {
  const good = step().split;
  assert.equal(patterns.splitScope(undefined), null);
  assert.equal(patterns.splitScope(null), null);
  assert.equal(patterns.splitScope({ ...good, start: 'nope' }), null);
  assert.equal(patterns.splitScope({ ...good, baselineEnd: '2026-10-06T00:00:00Z' }), null, 'the earlier range must end by the later start');
  assert.equal(patterns.splitScope({ ...good, end: '2026-10-20T00:00:00Z' }), null, 'a range over 7 days');
  assert.equal(patterns.splitScope({ ...good, baselineStart: '2026-09-01T00:00:00Z' }), null, 'a range over 7 days');
  assert.equal(patterns.splitScope({ ...good, end: good.start }), null, 'an empty range');
});

// ------------------------------------------------------------------ audit-create series

const series = (over = {}) => ({
  zone: 'UTC', excludedHangfire: 4, withoutAudit: 2, partial: false,
  days: [
    { date: '2026-10-17', n: 410, p50Ms: 120, p95Ms: 800, insertShare: 0.083 },
    { date: '2026-10-18', n: 12, p50Ms: null, p95Ms: null, insertShare: null },
    { date: '2026-10-19', n: 300, p50Ms: 150, p95Ms: 29500, insertShare: 0.4, partDay: true }
  ],
  band: { fromMs: 26200, toMs: 28900, count: 6, days: ['2026-10-17', '2026-10-18'], ofLong: 9 },
  ...over
});

test('audit day rows: a thin day shows its count and dashes, a part day is flagged', () => {
  const rows = patterns.auditDayRows(series().days);
  assert.equal(rows[0].median, '0.1 s');
  assert.equal(rows[0].slowest, '0.8 s');
  assert.equal(rows[0].share, '8.3%');
  assert.deepEqual([rows[1].n, rows[1].median, rows[1].slowest, rows[1].share], [12, '–', '–', '–']);
  assert.equal(rows[2].partDay, true);
  assert.deepEqual(patterns.auditDayRows(undefined), []);
});

test('band text: the band, the count and the days', () => {
  assert.equal(patterns.bandText(series().band), '6 delays between 26.2 s and 28.9 s, on 2 days (2026-10-17, 2026-10-18).');
  assert.equal(patterns.bandText({ fromMs: 20000, toMs: 20500, count: 1, days: ['2026-10-17'], ofLong: 1 }), '1 delay between 20 s and 20.5 s, on 1 day (2026-10-17).');
});

test('audit series section: hidden, absent, empty, and full', () => {
  assert.deepEqual(patterns.auditSeriesSection({}, false), { kind: 'hidden' });
  assert.equal(patterns.auditSeriesSection({}, true).kind, 'note');
  assert.equal(patterns.auditSeriesSection({ auditSeries: series({ days: [] }) }, true).kind, 'note');
  const full = patterns.auditSeriesSection({ auditSeries: series() }, true);
  assert.equal(full.kind, 'rows');
  assert.equal(full.days.length, 3);
  assert.match(full.band, /^6 delays between/);
  assert.equal(full.excluded, patterns.excludedLine(4));
  assert.equal(full.without, '2 requests have no audit create time and were left out.');
  assert.equal(full.partial, '');
  const none = patterns.auditSeriesSection({ auditSeries: series({ band: null, excludedHangfire: 0, withoutAudit: 0 }) }, true);
  assert.equal(none.band, patterns.STEP_TEXT.bandNone);
  assert.equal(none.excluded, '');
  assert.equal(none.without, '');
  const partial = patterns.auditSeriesSection({ auditSeries: series({ partial: true, partialReason: 'The row cap was reached' }) }, true);
  assert.equal(partial.partial, 'Partial read. These counts are lower bounds. The row cap was reached.');
});

test('without-audit line speaks of one request or many', () => {
  assert.equal(patterns.withoutAuditLine(0), '');
  assert.equal(patterns.withoutAuditLine(1), '1 request has no audit create time and was left out.');
});

// ------------------------------------------------------------------ the daily view

test('daily rows: a thin day or a part day has no time, and says why', () => {
  const rows = patterns.dailyRows([
    { date: '2026-10-17', start: '', end: '', count: 40, p50Ms: 100, p95Ms: 900 },
    { date: '2026-10-18', start: '', end: '', count: 3, p50Ms: null, p95Ms: null, low: true },
    { date: '2026-10-19', start: '', end: '', count: 70, p50Ms: null, p95Ms: null, partial: true }
  ]);
  assert.deepEqual(rows.map((r) => [r.slowest, r.note]), [['0.9 s', ''], ['–', patterns.STEP_TEXT.dailyFew], ['–', patterns.STEP_TEXT.dailyPart]]);
  assert.deepEqual(patterns.dailyRows(undefined), []);
});

test('chart runs break at days with no figure and never invent a point', () => {
  const runs = patterns.dailyChartRuns([{ slowestMs: 100 }, { slowestMs: 200 }, { slowestMs: null }, { slowestMs: 400 }], 300, 56);
  assert.equal(runs.length, 2);
  assert.deepEqual(runs[0], ['0,42', '100,28']);
  assert.deepEqual(runs[1], ['300,0']);
  assert.deepEqual(patterns.dailyChartRuns([{ slowestMs: null }]), []);
  assert.deepEqual(patterns.dailyChartRuns([]), []);
  assert.deepEqual(patterns.dailyChartRuns([{ slowestMs: 50 }]), [['150,0']]);
});

test('daily window is the same rule as the step window', () => {
  assert.deepEqual(patterns.dailyWindow(query, 14), patterns.stepWindow(query, 14));
  assert.equal(patterns.dailyWindow(query, 0), null);
  assert.ok(patterns.DAILY_DAY_CHOICES.every((n) => n >= 1 && n <= patterns.MAX_STEP_DAYS));
  assert.equal(patterns.DEFAULT_DAILY_DAYS, 14, 'the default fits what a live source allows');
});

// ------------------------------------------------------------------ wording

test('every new sentence passes the copy lint', () => {
  const text = Object.values(patterns.STEP_TEXT);
  const built = [
    ...patterns.stepRows(step()).flatMap((r) => [r.result, r.reason, r.change]),
    patterns.stepDaysLine({ daysRead: 80, daysLeftOut: 2 }),
    patterns.stepSection({}, true).text,
    patterns.auditSeriesSection({}, true).text,
    patterns.bandText(series().band),
    patterns.excludedLine(4),
    patterns.withoutAuditLine(2),
    patterns.auditSeriesSection({ auditSeries: series({ partial: true, partialReason: 'The row cap was reached' }) }, true).partial
  ].filter(Boolean);
  lintOk([...text, ...built]);
});

test('the server wording of the two findings passes the copy lint', () => {
  lintOk([
    'Observed: daily p95 for Invoices.Search moved from 0.3 s to 1.5 s around 2026-10-05. 3 of the top 5 operations show a step of at least 2 times within a day of that date. This dates a change in the numbers; it does not explain the change. Days with fewer than 20 timed requests are left out, and each p95 is accurate to about 5%.'
  ]);
});
