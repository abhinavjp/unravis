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
  const load = (request) => request.startsWith('./') && fs.existsSync(path.join(path.dirname(filename), request + '.ts'))
    ? loadTypeScript(path.relative(root, path.join(path.dirname(filename), request + '.ts')))
    : nodeRequire(request);
  new Function('require', 'module', 'exports', output)(load, module, module.exports);
  return module.exports;
}

const time = loadTypeScript('src/lib/time.ts');
const format = loadTypeScript('src/lib/format.ts');
const read = file => fs.readFileSync(path.join(root, file), 'utf8');

const at = '2026-10-24T15:19:37.123Z';

test('formatTime prints one pattern with the zone suffix', () => {
  assert.equal(time.formatTime(at), '24 Oct 15:19:37 UTC');
  assert.equal(time.formatTime(at, { zone: 'Asia/Dubai' }), '24 Oct 19:19:37 GMT+4');
  assert.equal(time.formatTime(at, { precision: 'minute' }), '24 Oct 15:19 UTC');
  assert.equal(time.formatTime(at, { precision: 'ms' }), '24 Oct 15:19:37.123 UTC');
  assert.equal(time.formatTime(at, { suffix: false }), '24 Oct 15:19:37');
  assert.equal(time.formatTime(at, { date: false, suffix: false }), '15:19:37');
  assert.equal(time.formatTime(at, { year: true }), '24 Oct 2026 15:19:37 UTC');
  assert.equal(time.formatTime('2026-09-05T00:00:00Z'), '5 Sep 00:00:00 UTC');
});

test('formatTime accepts numbers and dates, and falls back for a missing or unreadable value', () => {
  assert.equal(time.formatTime(Date.parse(at)), '24 Oct 15:19:37 UTC');
  assert.equal(time.formatTime(new Date(at)), '24 Oct 15:19:37 UTC');
  assert.equal(time.formatTime(null), 'N/A');
  assert.equal(time.formatTime(''), 'N/A');
  assert.equal(time.formatTime('not a time', { fallback: 'unknown' }), 'unknown');
  assert.equal(time.formatTime(at, { zone: 'Not/AZone' }), '24 Oct 15:19:37 UTC');
});

test('formatTimeRange prints the zone once and a single instant once', () => {
  assert.equal(time.formatTimeRange('2026-10-24T15:04:37Z', '2026-10-24T15:19:37Z'), '24 Oct 15:04:37–15:19:37 UTC');
  assert.equal(time.formatTimeRange('2026-10-24T23:50:00Z', '2026-10-25T00:05:00Z'), '24 Oct 23:50:00 – 25 Oct 00:05:00 UTC');
  assert.equal(time.formatTimeRange('2026-10-24T15:05:37Z', '2026-10-24T15:05:37Z'), '24 Oct 15:05:37 UTC');
  assert.equal(time.formatTimeRange('2026-10-24T15:04:37Z', 'x'), 'N/A');
  assert.equal(time.formatTimeRange('2026-10-24T20:04:37Z', '2026-10-24T20:19:37Z', { zone: 'Asia/Dubai' }), '25 Oct 00:04:37–00:19:37 GMT+4');
});

test('zoneAbbreviation names the zone once for a table heading', () => {
  assert.equal(time.zoneAbbreviation('UTC'), 'UTC');
  assert.equal(time.zoneAbbreviation('Asia/Dubai', at), 'GMT+4');
});

test('precisionFor asks for milliseconds only when two moments share a second', () => {
  assert.equal(time.precisionFor(['2026-10-24T18:19:37.000Z', '2026-10-24T18:19:37.300Z', '2026-10-24T18:19:37.700Z']), 'ms');
  assert.equal(time.precisionFor(['2026-10-24T18:19:37.000Z', '2026-10-24T18:19:38.000Z', null]), 'second');
  assert.equal(time.precisionFor([]), 'second');
});

test('formatDuration uses one rule: ms under a second, seconds under a minute, then minutes', () => {
  const duration = format.formatDuration;
  assert.equal(duration(700), '700 ms');
  assert.equal(duration(8700), '8.7 s');
  assert.equal(duration(4400), '4.4 s');
  assert.equal(duration(64_100), '1 min 4 s');
  assert.equal(duration(68_000), '1 min 8 s');
  assert.equal(duration(120_000), '2 min');
  assert.equal(duration(4.26), '4.3 ms');
  assert.equal(duration(0.4), '<1 ms');
  assert.equal(duration(0), '0 ms');
  assert.equal(duration(999.6), '1.0 s');
  assert.equal(duration(59_960), '1 min');
  assert.equal(duration(3_720_000), '1 h 2 min');
  assert.equal(duration(-1500), '−1.5 s');
  assert.equal(duration(null), '—');
  assert.equal(duration(Number.NaN), '—');
});

test('count helpers read the same everywhere', () => {
  assert.equal(format.formatCount(1234), (1234).toLocaleString());
  assert.equal(format.formatCount(null), '—');
  assert.equal(format.countNoun(1, 'event'), '1 event');
  assert.equal(format.countNoun(20, 'event'), '20 events');
  assert.equal(format.countNoun(0, 'event'), '0 events');
  assert.equal(format.formatCountOf(506, 508, 'request'), '506 of 508 requests');
  assert.equal(format.formatCountOf(1, 1, 'request'), '1 of 1 request');
});

test('the time zone store is wired to the app scope and the toggle component exists', () => {
  const store = read('src/lib/timeZone.svelte.ts');
  assert.match(store, /localStorage/);
  assert.match(store, /try \{/);
  const state = read('src/lib/appState.svelte.ts');
  assert.match(state, /timeZone\.zone/);
  assert.match(read('src/components/TimeZoneToggle.svelte'), /timeZone\.set\(/);
});

test('timestamps in the screens go through formatTime, not the browser default format', () => {
  for (const file of ['RequestTable', 'RecordDetail', 'ActivityContext', 'MultiTimeline', 'RequestVolume', 'TrafficSlowness', 'PatternsPanel', 'ExportPanel']) {
    const source = read('src/components/' + file + '.svelte');
    assert.match(source, /formatTime/, file);
    assert.doesNotMatch(source, /\.toLocaleString\(\)\s*:\s*'N\/A'|new Intl\.DateTimeFormat|formatLocal/, file);
  }
});

test('count tiles and tables format counts through formatCount, and call unusable timings unusable', () => {
  for (const file of ['OverviewStrip', 'ComparisonTable', 'OperationTable', 'IncompletePanel', 'FailurePanel']) {
    const source = read('src/components/' + file + '.svelte');
    assert.match(source, /formatCount/, file);
    assert.doesNotMatch(source, /\.toLocaleString\(\)/, file);
    assert.doesNotMatch(source, /invalid (rows?|timings?|samples?)|valid (response-time )?(timings?|samples?)/i, file);
  }
});

test('overview tiles are hairline-separated columns with no coloured side stripe', () => {
  const style = read('src/components/OverviewStrip.svelte').split('<style>')[1];
  assert.ok(style, 'OverviewStrip has its own style block');
  assert.match(style, /border-inline-start: var\(--hairline-width\) solid var\(--hairline\)/);
  assert.doesNotMatch(style, /border-(left|inline-start)\s*:\s*[^;]*var\(--(accent|bad|warn|good)/);
  assert.doesNotMatch(style, /border-radius:\s*var/);
});
