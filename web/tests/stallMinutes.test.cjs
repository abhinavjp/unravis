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

const sm = loadTypeScript('src/lib/stallMinutes.ts');
const { deck } = loadTypeScript('src/lib/copy/deck.ts');
const { lintCopy } = loadTypeScript('src/lib/copy/copylint.ts');

const M = 60_000;
const t0 = Date.parse('2026-10-23T10:00:00Z');
const iso = (ms) => new Date(ms).toISOString();
const row = (id, startMin, endMin) => ({ id, sourceId: 's1', initiatedAt: iso(t0 + startMin * M), completedAt: endMin === null ? null : iso(t0 + endMin * M) });

test('minuteWindow pads 15 minutes either side and rounds to whole minutes', () => {
  const w = sm.minuteWindow({ start: iso(t0 + 20 * M + 30_000), end: iso(t0 + 25 * M + 10_000) });
  assert.equal(w.startMs, t0 + 5 * M);
  assert.equal(w.endMs, t0 + 41 * M);
  assert.equal(w.minutes, 36);
  assert.equal(w.clamped, false);
});

test('minuteWindow cuts at 24 hours and flags it; bad input gives null', () => {
  const w = sm.minuteWindow({ start: iso(t0), end: iso(t0 + 30 * 3_600_000) });
  assert.equal(w.minutes, 24 * 60);
  assert.equal(w.clamped, true);
  assert.equal(sm.minuteWindow({ start: 'nope', end: iso(t0) }), null);
  assert.equal(sm.minuteWindow({ start: iso(t0 + M), end: iso(t0) }), null);
});

test('bucketMinutes counts starts, finishes, running and slow running requests', () => {
  const window = { startMs: t0, endMs: t0 + 10 * M, minutes: 10 };
  // Runs minute 1 to minute 6 (5 minutes). With a 2 minute limit it is slow from the end of minute 2 onward.
  const counts = sm.bucketMinutes([row('a', 1, 6), row('b', 3, 3.5)], window, 2 * M);
  const b = counts.buckets;
  assert.equal(b[1].started, 1);
  assert.equal(b[6].completed, 1);
  assert.equal(b[3].completed, 1);
  // Request a finishes exactly on the start of minute 6, so it is not running in minute 6.
  assert.deepEqual(b.map((x) => x.inFlight), [0, 1, 1, 2, 1, 1, 0, 0, 0, 0]);
  // Request a has run 1 minute at the end of minute 1 (not slow), 2 minutes at the end of minute 2 (slow).
  assert.deepEqual(b.map((x) => x.stallsInFlight), [0, 0, 1, 1, 1, 1, 0, 0, 0, 0]);
  assert.equal(counts.rows, 2);
  assert.equal(counts.noFinish, 0);
});

test('bucketMinutes counts a request that began before the window and ignores one wholly outside', () => {
  const window = { startMs: t0, endMs: t0 + 5 * M, minutes: 5 };
  const counts = sm.bucketMinutes([row('early', -10, 2), row('before', -10, -5), row('after', 8, 9)], window, M);
  assert.equal(counts.rows, 1);
  assert.equal(counts.buckets[0].started, 0);
  assert.equal(counts.buckets[0].inFlight, 1);
  assert.equal(counts.buckets[2].completed, 1);
  assert.equal(counts.buckets[0].stallsInFlight, 1);
});

test('bucketMinutes counts a row with no finish as running to the end and reports it', () => {
  const window = { startMs: t0, endMs: t0 + 5 * M, minutes: 5 };
  const counts = sm.bucketMinutes([row('open', 1, null), { id: 'x', sourceId: 's1', initiatedAt: null, completedAt: null }], window, 2 * M);
  assert.equal(counts.noFinish, 1);
  assert.equal(counts.noStart, 1);
  assert.deepEqual(counts.buckets.map((x) => x.inFlight), [0, 1, 1, 1, 1]);
  assert.deepEqual(counts.buckets.map((x) => x.completed), [0, 0, 0, 0, 0]);
});

test('bucketMinutes counts a repeated request once and keeps sources apart', () => {
  const window = { startMs: t0, endMs: t0 + 5 * M, minutes: 5 };
  const other = { ...row('a', 1, 2), sourceId: 's2' };
  const counts = sm.bucketMinutes([row('a', 1, 2), row('a', 1, 2), other], window, M);
  assert.equal(counts.rows, 2);
  assert.equal(counts.buckets[1].started, 2);
});

test('peakStallMinute picks the highest count, earliest on a tie, and null when none', () => {
  const mk = (n, i) => ({ startMs: t0 + i * M, started: 0, completed: 0, inFlight: n, stallsInFlight: n });
  assert.equal(sm.peakStallMinute([mk(0, 0), mk(0, 1)]), null);
  assert.equal(sm.peakStallMinute([mk(1, 0), mk(3, 1), mk(3, 2)]).startMs, t0 + M);
});

test('loadMinuteRows pages until the cursor ends and passes the window and limit', async () => {
  const calls = [];
  const pages = [
    { rows: [row('a', 1, 2)], nextCursor: 'c1', sources: [{ sourceId: 's1', ok: true }], truncated: false },
    { rows: [row('b', 2, 3)], nextCursor: '', sources: [{ sourceId: 's1', ok: true }], truncated: false }
  ];
  const fetchPage = async (input) => { calls.push(input); return pages[calls.length - 1]; };
  const load = await sm.loadMinuteRows(fetchPage, ['s1'], { startMs: t0, endMs: t0 + 10 * M, clamped: false });
  assert.equal(load.rows.length, 2);
  assert.equal(load.partial, false);
  assert.equal(calls.length, 2);
  assert.equal(calls[0].cursor, '');
  assert.equal(calls[1].cursor, 'c1');
  assert.equal(calls[0].limit, 100);
  assert.equal(calls[0].start, iso(t0));
  assert.equal(calls[0].end, iso(t0 + 10 * M));
});

test('loadMinuteRows marks partial on a truncated page, a page cap or a clamped window, and lists failed sources', async () => {
  const truncated = async () => ({ rows: [], nextCursor: '', sources: [{ sourceId: 's1', ok: false, error: 'down' }], truncated: true });
  const a = await sm.loadMinuteRows(truncated, ['s1'], { startMs: t0, endMs: t0 + M, clamped: false });
  assert.equal(a.partial, true);
  assert.equal(a.failed.length, 1);
  const endless = async () => ({ rows: [], nextCursor: 'more', sources: [], truncated: false });
  const b = await sm.loadMinuteRows(endless, ['s1'], { startMs: t0, endMs: t0 + M, clamped: false });
  assert.equal(b.partial, true);
  const clean = async () => ({ rows: [], nextCursor: '', sources: [], truncated: false });
  const c = await sm.loadMinuteRows(clean, ['s1'], { startMs: t0, endMs: t0 + M, clamped: true });
  assert.equal(c.partial, true);
});

test('loadMinuteRows stops when aborted', async () => {
  const controller = new AbortController();
  controller.abort();
  await assert.rejects(sm.loadMinuteRows(async () => ({ rows: [], nextCursor: '' }), ['s1'], { startMs: t0, endMs: t0 + M, clamped: false }, controller.signal));
});

test('the text is plain, avoids causal words and passes copy lint', () => {
  const buckets = [{ startMs: t0, started: 0, completed: 0, inFlight: 4, stallsInFlight: 4 }];
  const strings = [
    ...Object.values(sm.STALL_MINUTES_TEXT),
    sm.peakLine(buckets, () => '10:00'),
    sm.peakLine([], () => '10:00'),
    sm.unplacedLine({ noFinish: 2, noStart: 1 }),
    sm.emptyWindowLine()
  ];
  assert.deepEqual(lintCopy(strings.map((text, i) => ({ id: 'm' + i, text })), deck.terms), []);
  for (const text of strings) assert.doesNotMatch(text, /\b(caused|because|due to|waiting on|blocked by|retry|proven)\b/i, text);
  assert.equal(sm.unplacedLine({ noFinish: 0, noStart: 0 }), '');
});
