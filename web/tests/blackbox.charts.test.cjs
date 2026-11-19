const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const { loadTypeScript } = require('./helpers/loadTs.cjs');

// The Blackbox charts view (Perfmon-style charts on POST /api/blackbox/{id}/series): shape check, names, units,
// value formats, gap breaks, the log-scale rule, figures, windows, and the wiring into the panel.

const s = loadTypeScript('src/lib/blackboxSeries.ts');
const { BBC_TEXT } = loadTypeScript('src/lib/copy/blackboxChartsCopy.ts');
const { lintCopy } = loadTypeScript('src/lib/copy/copylint.ts');
const read = (p) => fs.readFileSync(path.join(__dirname, '..', p), 'utf8');

const T0 = Date.parse('2026-11-18T06:00:00Z');
const answer = (extra = {}) => ({
  start: '2026-11-18T06:00:00Z', end: '2026-11-18T07:00:00Z', bucketSeconds: 15, intervalSeconds: 15, samples: 3,
  lines: [{ key: 'cpu', group: 'host', label: 'cpu', unit: 'pct', points: [[T0, 10, 5, 20]] }],
  groups: [{ group: 'clr', read: 0, missing: 0, slow: 3, busy: 0 }], processes: [], eventIds: [], dumps: [], gaps: [], markers: [],
  read: { files: 1, samples: 3, torn: 0, dropped: [], missing: [] }, state: {}, ...extra
});

test('readSeriesAnswer keeps a documented answer and refuses one with a bad point, so the view never draws a guess', () => {
  assert.equal(s.readSeriesAnswer(answer()).lines[0].key, 'cpu');
  assert.equal(s.readSeriesAnswer(answer({ lines: [{ key: 'cpu', unit: 'pct', points: [[T0, 'x', 1, 2]] }] })), null);
  assert.equal(s.readSeriesAnswer(answer({ lines: [{ key: 'cpu', unit: 'pct', points: [[T0, 1, 2]] }] })), null);
  assert.equal(s.readSeriesAnswer({ ...answer(), bucketSeconds: undefined }), null);
  assert.equal(s.readSeriesAnswer(null), null);
  // a server that sends null for an empty list (Go nil slice) still reads
  assert.deepEqual(s.readSeriesAnswer(answer({ processes: null, markers: null })).processes, []);
});

test('names follow the key shape the server writes: host fields, named things, the CPU split, TCP states and event levels', () => {
  assert.equal(s.seriesTitle('cpu'), 'Processor busy (% Processor Time)');
  assert.equal(s.seriesTitle('sqlHost.SQLEXPRESS.batchRequestsPerSec'), 'Batches (Batch Requests/sec): SQLEXPRESS');
  assert.equal(s.seriesTitle('diskCpu.C:.readMs'), 'Read time (Avg. Disk sec/Read): C:');
  assert.equal(s.seriesTitle('diskCpu.cpu.dpcPct'), 'Driver work (% DPC Time)');
  assert.equal(s.seriesTitle('tcp.states.time-wait'), 'Connections in state time-wait');
  assert.equal(s.seriesTitle('events.level2'), 'Error events');
  assert.equal(s.seriesTitle('iis.my.pool.activeRequests'), 'Requests running (Active Requests): my.pool');
  // an unknown field shows its own name, never nothing
  assert.equal(s.seriesTitle('newGroup.x.someNewCounter'), 'someNewCounter: x');
});

test('every field the recorder writes as a number has a plain name', () => {
  const src = ['groups_types.go', 'sample.go'].map((f) => fs.readFileSync(path.join(__dirname, '..', '..', 'internal', 'blackbox', f), 'utf8')).join('\n');
  const numeric = [...src.matchAll(/^\s*\w+\s+\*?(?:float64|uint64|uint32|int)\s+`json:"(\w+)/gm)].map((m) => m[1]);
  const skip = new Set(['v', 'seq', 'pid', 'startTicks', 'start', 'channel', 'eventId', 'level', 'count', 'state', 'size', 'seconds', 'cpuSec', 'ws', 'priv', 'handles', 'netIn', 'netOut', 'unreadable']);
  const missing = [...new Set(numeric)].filter((f) => !skip.has(f)).filter((f) => s.seriesName('g.x.' + f).name === f);
  assert.deepEqual(missing, []);
});

test('units: the server guess is kept, and the fields it cannot guess are fixed', () => {
  assert.equal(s.seriesUnit('memAvail', 'bytes'), 'bytes');
  assert.equal(s.seriesUnit('clr.w3wp.pctTimeInGC', 'count'), 'pct');
  assert.equal(s.seriesUnit('http.api.maxQueueItemAge', 'count'), 'ms');
  assert.equal(s.seriesUnit('sqlHost.X.pageLifeExpectancy', 'count'), 'sec');
  assert.equal(s.seriesUnit('x', 'furlongs'), 'count');
});

test('formatValue gives each unit in words a person reads, and a missing value as "not read", never 0', () => {
  assert.equal(s.formatValue('pct', 12.345), '12.3 %');
  assert.equal(s.formatValue('bytes', 3 * 1024 ** 3), '3 GB');
  assert.equal(s.formatValue('bytes', 1536 * 1024), '1.5 MB');
  assert.equal(s.formatValue('bytesPerSec', 2048), '2 KB/s');
  assert.equal(s.formatValue('ms', 4.2), '4.2 ms');
  assert.equal(s.formatValue('ms', 2500), '2.5 s');
  assert.equal(s.formatValue('sec', 900), '15 min');
  assert.equal(s.formatValue('perSec', 1234), '1,234 /s');
  assert.equal(s.formatValue('count', null), 'not read');
  assert.equal(s.formatValue('pct', NaN), 'not read');
});

test('chartData breaks the line at a hole longer than one and a half buckets, and the log scale hides 0 and counts it', () => {
  const pts = [[T0, 1, 1, 1], [T0 + 15000, 2, 2, 2], [T0 + 60000, 0, 0, 0], [T0 + 75000, 4, 4, 4]];
  const lin = s.chartData(pts, 15, false);
  assert.deepEqual(lin.data, [[T0, 1], [T0 + 15000, 2], [T0 + 30000, null], [T0 + 60000, 0], [T0 + 75000, 4]]);
  assert.equal(lin.hidden, 0);
  const log = s.chartData(pts, 15, true);
  assert.deepEqual(log.data[3], [T0 + 60000, null]);
  assert.equal(log.hidden, 1);
});

test('lineFigures: last, average, lowest of the lows, highest of the highs, inside the zoom only', () => {
  const pts = [[T0, 10, 5, 20], [T0 + 15000, 30, 25, 40], [T0 + 30000, 50, 1, 90]];
  assert.deepEqual(s.lineFigures(pts), { last: 50, avg: 30, min: 1, max: 90, n: 3 });
  assert.deepEqual(s.lineFigures(pts, T0, T0 + 15000), { last: 30, avg: 20, min: 5, max: 40, n: 2 });
  assert.equal(s.lineFigures([]).avg, null);
});

test('the default set is Perfmon\'s slow-server start (CPU, memory, disk time, disk queue) when recorded, else the first line', () => {
  assert.deepEqual(s.defaultSelection([{ key: 'diskQueue' }, { key: 'cpu' }, { key: 'x' }]), ['cpu', 'diskQueue']);
  assert.deepEqual(s.defaultSelection([{ key: 'tcp.retransmitsPerSec' }]), ['tcp.retransmitsPerSec']);
  assert.deepEqual(s.defaultSelection([]), []);
});

test('one strip per unit, in first-picked order, so percent and bytes never share an axis', () => {
  const lines = [{ key: 'cpu', unit: 'pct' }, { key: 'memAvail', unit: 'bytes' }, { key: 'vm.stealPct', unit: 'pct' }];
  assert.deepEqual(s.unitStrips(['memAvail', 'cpu', 'vm.stealPct', 'gone'], lines), [{ unit: 'bytes', keys: ['memAvail'] }, { unit: 'pct', keys: ['cpu', 'vm.stealPct'] }]);
});

test('picker groups follow the fixed order and an unknown group still shows', () => {
  const g = s.pickerGroups([{ key: 'sqlHost.A.userConnections', group: 'sqlHost' }, { key: 'cpu', group: 'host' }, { key: 'zzz.a', group: 'zzz' }]);
  assert.deepEqual(g.map((x) => x.group), ['host', 'sqlHost', 'zzz']);
  assert.equal(g[1].title, 'SQL Server');
});

test('a group with no readings says why; a read group or one never asked for says nothing', () => {
  assert.match(s.coverNote({ group: 'clr', read: 0, missing: 0, slow: 2, busy: 0 }), /too long/);
  assert.match(s.coverNote({ group: 'clr', read: 0, missing: 4, slow: 0, busy: 0 }), /could not be read/);
  assert.equal(s.coverNote({ group: 'clr', read: 1, missing: 4, slow: 0, busy: 0 }), null);
  assert.equal(s.coverNote({ group: 'clr', read: 0, missing: 0, slow: 0, busy: 0 }), null);
});

test('windows end at the last sample, not now, never start before the first, and the first view holds a short recording whole', () => {
  const first = '2026-11-18T06:00:00Z', last = '2026-11-18T06:40:00Z';
  const w = s.presetWindow('15m', first, last);
  assert.equal(w.endMs, Date.parse(last) + 1000);
  assert.equal(w.endMs - w.startMs, 15 * 60000);
  assert.equal(s.presetWindow('24h', first, last).startMs, Date.parse(first));
  assert.equal(s.presetWindow('1h', undefined, undefined), null);
  assert.equal(s.firstPreset(first, last), '1h');
  assert.equal(s.firstPreset('2026-11-10T00:00:00Z', last), '7d');
  assert.ok(s.MAX_SPAN_MS <= 7 * 24 * 3600 * 1000, 'the server refuses more than 7 days');
});

test('the charts copy passes the copy lint', () => {
  const items = Object.entries(BBC_TEXT).map(([id, v]) => ({ id, text: typeof v === 'function' ? v('A', 'B') : v }));
  const found = lintCopy(items, []).filter((f) => f.severity === 'error');
  assert.deepEqual(found, []);
});

test('the panel opens the charts for a Blackbox with facts, and the client posts to the series route', () => {
  const panel = read('src/components/BlackboxPanel.svelte');
  assert.match(panel, /<BlackboxCharts item=\{chartsFor\}/);
  assert.match(panel, /chartsFor = item/);
  assert.doesNotMatch(panel, /PROTOTYPE \(throwaway/);
  assert.match(read('src/lib/blackbox.ts'), /series: \(id: string[^\n]*\n\s*call<unknown>\('POST', '\/' \+ encodeURIComponent\(id\) \+ '\/series'/);
  const view = read('src/components/BlackboxCharts.svelte');
  assert.match(view, /readSeriesAnswer\(raw\)/, 'every answer goes through the shape check');
  assert.match(view, /type: log \? 'log' : 'value'/);
});

test('alignedRequest makes each point a whole number of recorder readings and never asks for more than 600 points', () => {
  const end = Date.parse('2026-11-19T02:52:10Z');
  const r = s.alignedRequest(end - 6 * 3600_000, end, 15);
  assert.equal(r.bucketSeconds % 15, 0);
  assert.equal(r.bucketSeconds, 45);
  assert.ok(r.points <= 600);
  assert.equal((r.endMs - r.startMs) / 1000, r.points * r.bucketSeconds);
  assert.ok(r.startMs <= end - 6 * 3600_000 && r.endMs >= end);
  const short = s.alignedRequest(end - 15 * 60_000, end, 15);
  assert.equal(short.bucketSeconds, 15);
  assert.equal(s.alignedRequest(end - 7 * 24 * 3600_000, end, 15).points <= 600, true);
  // the server refuses more than 7 days; stretching to whole buckets must stay under it
  const week = s.alignedRequest(end - 7 * 24 * 3600_000 + 60_000, end, 15);
  assert.ok(week.endMs - week.startMs <= 7 * 24 * 3600_000, 'stays inside 7 days');
});
