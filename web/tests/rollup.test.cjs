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

const r = loadTypeScript('src/lib/rollup.ts');
const breakdown = loadTypeScript('src/lib/breakdown.ts');
const { deck } = loadTypeScript('src/lib/copy/deck.ts');
const { lintScreenStrings } = loadTypeScript('src/lib/copy/copylint.ts');

const day = (over = {}) => ({
  date: '2026-10-20', start: '2026-10-20T00:00:00Z', end: '2026-10-21T00:00:00Z', hours: 24, partial: false,
  total: 500, sync: 350, background: 100, other: 50, latencyCount: 300,
  p50: { lowerMs: 100, upperMs: 120, valueMs: 110 }, p95: { lowerMs: 600, upperMs: 700, valueMs: 650 },
  count4xx: 5, count5xx: 2, lowTraffic: false, vsPrev: 'up', vsPrevPct: 25, vsPrevDate: '2026-10-19', ...over
});
const cell = (over = {}) => ({
  date: '2026-10-20', hour: 9, start: '2026-10-20T09:00:00Z', end: '2026-10-20T10:00:00Z', count: 40, latencyCount: 30,
  p95: { lowerMs: 400, upperMs: 500, valueMs: 450 }, slowCount: 6, noise: false, ...over
});

// ---- CSV escaping ----

test('csvSafe puts an apostrophe before = + - @ tab and carriage return', () => {
  for (const text of ['=SUM(A1)', '+1', '-2+3', '@cmd', '\tx', '\rx']) assert.equal(r.csvSafe(text), "'" + text, JSON.stringify(text));
});

test('csvSafe sees through spaces, hidden characters and full-width forms', () => {
  assert.equal(r.csvSafe('  =1+1'), "'  =1+1");
  assert.equal(r.csvSafe('​=1+1'), "'​=1+1");
  assert.equal(r.csvSafe('﻿@x'), "'﻿@x");
  assert.equal(r.csvSafe('＝1+1'), "'＝1+1");
  assert.equal(r.csvSafe('＠x'), "'＠x");
});

test('csvSafe leaves plain text, numbers inside text, empty text and an existing apostrophe alone', () => {
  for (const text of ['', 'plain', 'a=b', '2026-10-20', "'=already", 'x - y', '1,5']) assert.equal(r.csvSafe(text), text, JSON.stringify(text));
});

test('csvCell quotes commas, quotes and line breaks, and still guards formulas', () => {
  assert.equal(r.csvCell('a,b'), '"a,b"');
  assert.equal(r.csvCell('say "hi"'), '"say ""hi"""');
  assert.equal(r.csvCell('line\nbreak'), '"line\nbreak"');
  assert.equal(r.csvCell('=HYPERLINK("x","y")'), '"\'=HYPERLINK(""x"",""y"")"');
  assert.equal(r.csvCell(null), '');
  assert.equal(r.csvCell(undefined), '');
  assert.equal(r.csvCell(0), '0');
  assert.equal(r.csvCell(false), 'false');
});

test('a finite number is written as a number; the guard is for text, and a number as text is guarded', () => {
  assert.equal(r.csvCell(-3), '-3');
  assert.equal(r.csvCell('-3'), "'-3");
  assert.equal(r.csvCell(Number.NaN), 'NaN');
});

test('dailyCsv has a header naming the zone, one line per day, CRLF ends, and flags', () => {
  const csv = r.dailyCsv([day(), day({ date: '2026-10-21', lowTraffic: true, total: 10, vsPrev: '', vsPrevPct: null, vsPrevDate: undefined, p50: null, p95: null })], 'Asia/Kolkata');
  const lines = csv.split('\r\n');
  assert.equal(lines.length, 4);
  assert.equal(lines[3], '');
  assert.ok(lines[0].startsWith('Date (Asia/Kolkata),'));
  assert.equal(lines[0].split(',').length, r.dailyCsvHeader.length);
  const first = lines[1].split(',');
  assert.equal(first[0], '2026-10-20');
  assert.equal(first[3], '500');
  assert.equal(first[9], '110');
  assert.equal(first[10], '650');
  const second = lines[2].split(',');
  assert.equal(second[11], 'yes');
  assert.equal(second[9], '');
  assert.equal(second[12], '', 'a day that was not compared has an empty comparison cell');
  assert.equal(first[12], '25% more requests than 19 Oct');
});

test('dailyCsv writes a 23 hour day with its real length', () => {
  const csv = r.dailyCsv([day({ hours: 23 })], 'UTC');
  assert.equal(csv.split('\r\n')[1].split(',')[1], '23');
});

test('a hostile zone name cannot start a formula in the header', () => {
  const header = r.dailyCsv([], '=cmd|calc').split('\r\n')[0];
  assert.ok(header.startsWith("Date (=cmd|calc)"), header); // Starts with the word Date, so it stays text
  assert.ok(!header.startsWith('='));
});

// ---- Trend ----

test('trendView gives an arrow only to a day that was compared', () => {
  assert.equal(r.trendView(day()).arrow, '▲');
  assert.match(r.trendView(day()).text, /25% more requests than 19 Oct/);
  assert.equal(r.trendView(day({ vsPrev: 'down', vsPrevPct: -30 })).arrow, '▼');
  assert.match(r.trendView(day({ vsPrev: 'down', vsPrevPct: -30 })).text, /30% fewer/);
  assert.equal(r.trendView(day({ vsPrev: 'flat', vsPrevPct: 3 })).arrow, '▬');
  assert.equal(r.trendView(day({ lowTraffic: true })).arrow, '');
  assert.match(r.trendView(day({ lowTraffic: true })).text, /fewer than 100/);
  assert.equal(r.trendView(day({ partial: true })).arrow, '');
  assert.equal(r.trendView(day({ vsPrev: '', vsPrevPct: null })).arrow, '');
});

test('dayLabel is a local date without zone maths', () => {
  assert.equal(r.dayLabel('2026-03-08'), '8 Mar');
  assert.equal(r.dayLabel('not a date'), 'not a date');
  assert.equal(r.dayLengthNote({ hours: 24 }), '');
  assert.equal(r.dayLengthNote({ hours: 23 }), '23 h day');
  assert.equal(r.dayLengthNote({ hours: 25 }), '25 h day');
});

// ---- Cells to windows ----

test('cellWindow returns the UTC start and end of a cell with requests', () => {
  assert.deepEqual(r.cellWindow(cell()), { start: '2026-10-20T09:00:00Z', end: '2026-10-20T10:00:00Z' });
});

test('cellWindow keeps a 25 hour day correct: the window is the cell, not the clock label', () => {
  // The repeated hour is a real 60-minute span; the cell carries its own UTC bounds.
  const repeated = cell({ date: '2026-11-01', hour: 1, start: '2026-11-01T06:00:00Z', end: '2026-11-01T07:00:00Z' });
  assert.deepEqual(r.cellWindow(repeated), { start: '2026-11-01T06:00:00Z', end: '2026-11-01T07:00:00Z' });
});

test('cellWindow gives nothing for an empty square, a missing one, or bad times', () => {
  assert.equal(r.cellWindow(cell({ count: 0 })), null);
  assert.equal(r.cellWindow(null), null);
  assert.equal(r.cellWindow(undefined), null);
  assert.equal(r.cellWindow(cell({ start: 'nope' })), null);
  assert.equal(r.cellWindow(cell({ start: '2026-10-20T10:00:00Z', end: '2026-10-20T09:00:00Z' })), null);
  assert.equal(r.cellWindow(cell({ end: '2026-10-20T09:00:00Z' })), null);
});

test('a noise cell can still be chosen: few requests are still requests', () => {
  assert.ok(r.cellWindow(cell({ count: 2, noise: true })));
});

test('hourWindowQuery sets the window, the hour before it as reference, and a valid bucket', () => {
  const q = r.hourWindowQuery({ start: '2026-10-20T09:00:00Z', end: '2026-10-20T10:00:00Z' });
  assert.equal(q.start, '2026-10-20T09:00:00.000Z');
  assert.equal(q.end, '2026-10-20T10:00:00.000Z');
  assert.equal(q.baselineStart, '2026-10-20T08:00:00.000Z');
  assert.equal(q.baselineEnd, '2026-10-20T09:00:00.000Z');
  assert.ok(q.bucketSeconds >= 10 && q.bucketSeconds <= 86400);
  assert.ok(3600 / q.bucketSeconds <= 120, 'at most 120 buckets');
});

test('hourWindowQuery refuses a window that is not a span', () => {
  assert.throws(() => r.hourWindowQuery({ start: 'x', end: 'y' }), RangeError);
  assert.throws(() => r.hourWindowQuery({ start: '2026-10-20T10:00:00Z', end: '2026-10-20T10:00:00Z' }), RangeError);
});

// ---- Grid model ----

test('gridRows lays a day out in 24 squares and leaves a 23 hour day with one empty square', () => {
  const cells = [];
  for (let h = 0; h < 24; h++) if (h !== 2) cells.push(cell({ date: '2026-03-08', hour: h }));
  const rows = r.gridRows({ days: [{ date: '2026-03-08', hours: 23, partial: false }], cells });
  assert.equal(rows.length, 1);
  assert.equal(rows[0].cells.length, 24);
  assert.equal(rows[0].cells[2], null);
  assert.equal(rows[0].hours, 23);
  assert.ok(rows[0].cells[3]);
});

test('gridRows sorts days, keeps a day with no cells, and ignores an hour out of range', () => {
  const rows = r.gridRows({
    days: [{ date: '2026-10-21', hours: 24, partial: true }, { date: '2026-10-20', hours: 24, partial: false }],
    cells: [cell({ hour: 99 }), cell({ hour: -1 }), cell({ date: '2026-10-22', hour: 1 })]
  });
  assert.deepEqual(rows.map(row => row.date), ['2026-10-20', '2026-10-21', '2026-10-22']);
  assert.ok(rows[0].cells.every(item => item === null));
  assert.ok(rows[2].cells[1]);
});

test('cellValue is nothing for a noise cell and an empty cell', () => {
  assert.equal(r.cellValue(cell({ noise: true, count: 3 }), 'p95'), null);
  assert.equal(r.cellValue(cell({ count: 0 }), 'slow'), null);
  assert.equal(r.cellValue(cell(), 'p95'), 450);
  assert.equal(r.cellValue(cell(), 'slow'), 0.2);
  assert.equal(r.cellValue(cell({ p95: null }), 'p95'), null);
  assert.equal(r.cellValue(cell({ latencyCount: 0 }), 'slow'), null);
});

test('shadeLevel runs 0 to 4 and never leaves that range', () => {
  assert.equal(r.shadeLevel(null, 100), 0);
  assert.equal(r.shadeLevel(0, 100), 0);
  assert.equal(r.shadeLevel(50, 0), 0);
  assert.equal(r.shadeLevel(1, 100), 1);
  assert.equal(r.shadeLevel(100, 100), 4);
  assert.equal(r.shadeLevel(500, 100), 4);
  assert.equal(r.shadeLevel(Number.NaN, 100), 0);
  assert.equal(r.shadeLevel(50, 100), 2);
});

test('gridMax ignores noise cells so one thin hour cannot wash out the rest', () => {
  const cells = [cell({ hour: 1 }), cell({ hour: 2, noise: true, count: 2, p95: { lowerMs: 1, upperMs: 2, valueMs: 99999 } })];
  assert.equal(r.gridMax(cells, 'p95'), 450);
  assert.equal(r.gridMax([], 'p95'), 0);
});

test('moveInGrid moves by arrow key over squares that exist and stays at the edges', () => {
  const full = (date) => { const cells = Array(24).fill(null).map((_, h) => cell({ date, hour: h })); return { date, hours: 24, partial: false, cells }; };
  const rows = [full('a'), full('b')];
  rows[1].cells[5] = null;
  assert.deepEqual(r.moveInGrid(rows, 0, 0, 'ArrowRight'), [0, 1]);
  assert.deepEqual(r.moveInGrid(rows, 0, 0, 'ArrowLeft'), [0, 0]);
  assert.deepEqual(r.moveInGrid(rows, 0, 23, 'ArrowRight'), [0, 23]);
  assert.deepEqual(r.moveInGrid(rows, 0, 3, 'ArrowDown'), [1, 3]);
  assert.deepEqual(r.moveInGrid(rows, 1, 3, 'ArrowDown'), [1, 3]);
  assert.deepEqual(r.moveInGrid(rows, 0, 5, 'ArrowDown'), [0, 5], 'a gap below stops the move');
  assert.deepEqual(r.moveInGrid(rows, 1, 4, 'ArrowRight'), [1, 6], 'a gap beside is stepped over');
  assert.deepEqual(r.moveInGrid(rows, 1, 6, 'ArrowLeft'), [1, 4]);
  assert.deepEqual(r.moveInGrid(rows, 0, 9, 'Home'), [0, 0]);
  assert.deepEqual(r.moveInGrid(rows, 0, 9, 'End'), [0, 23]);
  assert.deepEqual(r.moveInGrid(rows, 0, 9, 'x'), [0, 9]);
});

// ---- Request ----

test('rollupParams never asks for more than 120 days', () => {
  const now = new Date('2026-10-25T12:00:00Z');
  const p = r.rollupParams({ id: 'x', label: 'x', days: 400 }, now, 'UTC', ['a']);
  assert.equal((Date.parse(p.to) - Date.parse(p.from)) / 86400000, 120);
  assert.equal(r.rollupParams({ id: 'x', label: 'x', days: 0 }, now, 'UTC', []).from, '2026-10-24T12:00:00.000Z');
  assert.ok(r.rollupRanges.every(range => range.days >= 1 && range.days <= r.MAX_ROLLUP_DAYS));
});

test('rollupPath carries from, to, tz and sources, with the values escaped', () => {
  const path = r.rollupPath('daily', { from: '2026-10-01T00:00:00.000Z', to: '2026-10-08T00:00:00.000Z', tz: 'America/New_York', sourceIds: ['a', 'b&c'], slowMs: 800 });
  assert.ok(path.startsWith('/daily?'));
  const q = new URLSearchParams(path.slice(path.indexOf('?') + 1));
  assert.equal(q.get('tz'), 'America/New_York');
  assert.equal(q.get('sources'), 'a,b&c');
  assert.equal(q.get('slowMs'), '800');
  assert.equal(new URLSearchParams(r.rollupPath('hourgrid', { from: 'a', to: 'b', tz: 'UTC', sourceIds: [] }).split('?')[1]).has('sources'), false);
  assert.ok(r.rollupPath('hourgrid', { from: 'a', to: 'b', tz: 'UTC', sourceIds: [] }).startsWith('/hourgrid?'));
});

test('latencyText shows a dash when the server masked the time', () => {
  assert.equal(r.latencyText(null), '—');
  assert.equal(r.latencyText({ lowerMs: 1, upperMs: null, valueMs: null }), '—');
  assert.equal(r.latencyText({ lowerMs: 1, upperMs: 3, valueMs: 1234.4 }), '≈ 1,234 ms');
});

test('cellLabel names the hour, the count, and says when a shade is not reliable', () => {
  const text = r.cellLabel(cell(), 'UTC', 1000);
  assert.match(text, /40 requests/);
  assert.match(text, /P95/);
  assert.match(r.cellLabel(cell({ noise: true, count: 3 }), 'UTC', 1000), /too few/);
  assert.match(r.cellLabel(cell({ count: 0 }), 'UTC', 1000), /no requests/);
  assert.match(r.cellLabel(null, 'UTC', 1000), /No such hour/);
  assert.match(r.cellLabel(cell({ count: 1, noise: true }), 'UTC', 1000), /1 request,/);
});

// ---- Breakdown dimensions ----

test('the two new breakdown dimensions are listed, and own no record filter', () => {
  const dims = breakdown.breakdownDims.map(item => item.dim);
  assert.ok(dims.includes('hourOfDay'));
  assert.ok(dims.includes('requestSizeBucket'));
  const q = { start: 'a', end: 'b', userIds: [7] };
  assert.deepEqual(breakdown.withoutDimension(q, 'hourOfDay'), q);
  assert.deepEqual(breakdown.withoutDimension(q, 'requestSizeBucket'), q);
});

test('only hour of day sends a zone, and its note names the zone', () => {
  assert.equal(breakdown.breakdownZone('hourOfDay', 'Asia/Kolkata'), 'Asia/Kolkata');
  assert.equal(breakdown.breakdownZone('userId', 'Asia/Kolkata'), undefined);
  assert.equal(breakdown.breakdownZone('requestSizeBucket', 'UTC'), undefined);
  assert.match(breakdown.bucketNote('hourOfDay', 'Asia/Kolkata'), /Asia\/Kolkata time/);
  assert.match(breakdown.bucketNote('hourOfDay', 'UTC'), /in UTC/);
  assert.match(breakdown.bucketNote('requestSizeBucket'), /kilobytes/);
  assert.equal(breakdown.bucketNote('userId', 'UTC'), null);
  assert.match(breakdown.breakdownPickTitle('hourOfDay', { value: '09:00', missing: false }), /not a request filter/);
});

// ---- Words ----

test('the words of the view pass the copy lint: plain, no causal claims, jargon explained', () => {
  const strings = [
    ...Object.values(r.rollupCopy), r.zoneNote('Asia/Kolkata'), r.zoneNote('UTC'), r.slowNote(1000),
    ...r.gridMetrics.map(item => item.label), ...r.rollupRanges.map(item => item.label),
    r.trendView(day()).text, r.trendView(day({ vsPrev: 'down', vsPrevPct: -30 })).text, r.trendView(day({ vsPrev: 'flat', vsPrevPct: 2 })).text,
    r.trendView(day({ lowTraffic: true })).text, r.trendView(day({ partial: true })).text, r.trendView(day({ vsPrev: '', vsPrevPct: null })).text,
    r.cellLabel(cell(), 'UTC', 1000), r.cellLabel(cell({ noise: true, count: 2 }), 'UTC', 1000), r.cellLabel(cell({ count: 0 }), 'UTC', 1000),
    breakdown.bucketNote('hourOfDay', 'Asia/Kolkata'), breakdown.bucketNote('hourOfDay', 'UTC'), breakdown.bucketNote('requestSizeBucket')
  ];
  const entries = strings.map((text, index) => ({ id: 'rollup' + index, text }));
  assert.ok(entries.length > 25);
  assert.deepEqual(lintScreenStrings(entries, deck.terms), []);
  for (const { text } of entries) assert.doesNotMatch(text, /\bcause|\bbecause\b|\bdue to\b|\bled to\b|\bresponsible\b/i, text);
});

test('the view says which zone it uses', () => {
  assert.match(r.zoneNote('Europe/Paris'), /Europe\/Paris time/);
  assert.equal(r.zoneNote('UTC'), 'Days and hours follow UTC.');
});
