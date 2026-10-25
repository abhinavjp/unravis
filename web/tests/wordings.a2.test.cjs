// A2 rule wordings (wave-2 plan 4.3): the TypeScript twins of the Go wordings in
// internal/evidence/wording.go. internal/evidence/wording_test.go pins the same
// sentences, so a change on one side fails on the other.
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

const { lintCopy } = loadTypeScript('src/lib/copy/copylint.ts');
const w = loadTypeScript('src/lib/copy/wordings.ts');

const term = (name) => ({ id: name.toLowerCase(), term: name, hint: 'h', definition: 'd', notMeaning: 'n', aliases: [] });
const TERMS = [term('Episode'), term('Correlation'), term('Sampling')];
const rules = (text) => lintCopy([{ id: 'x', text }], TERMS).map((f) => f.rule);

const CASES = [
  [w.markerBefore('2 min 31 s', 'recycle event of the application pool', 'web-a'),
    'This interval starts 2 min 31 s after a recycle event of the application pool on web-a (Correlation).'],
  [w.markerBefore('12 s', 'IIS restart', 'web-a'), 'This interval starts 12 s after an IIS restart on web-a (Correlation).'],
  [w.markerAfter('1 min', 'recycle event of the application pool', 'web-a'),
    'This interval ends 1 min before a recycle event of the application pool on web-a (Correlation).'],
  [w.markerAfter('40 s', 'ASP.NET restart', w.hostName('')), 'This interval ends 40 s before an ASP.NET restart on an unnamed host (Correlation).'],
  [w.lowestInWindow('available memory', '420 MiB'), 'Lowest available memory in this window: 420 MiB.'],
  [w.countInWindow('Redis timeout entries', 1), 'Redis timeout entries in this window: 1.'],
  [w.overlapBuffered('Redis SLOWLOG', 'slow command', 1.012),
    'The Redis SLOWLOG buffer held a slow command of 1.012 s overlapping this interval (Correlation).'],
  [w.overlapBuffered('Redis latency monitor', 'latency spike', 2),
    'The Redis latency monitor buffer held a latency spike of 2 s overlapping this interval (Correlation).'],
  [w.coverageGap(13, 'host.cpu', 'web-a'), 'Not observed (gap or sampling: 13 % of samples present for host.cpu on web-a).'],
  [w.noProcessCountersGap('web-a'), 'Not observed (gap or sampling: no per-process counters for web-a).'],
  [w.noRuntimeCountersGap('web-a'), 'Not observed (gap or sampling: no runtime counters for this process on web-a).'],
  [w.noThreadPoolCounterGap('web-a'),
    'Not observed (gap or sampling: no thread-pool counter exists on web-a; ASP.NET Requests Queued shown instead).'],
  [w.noAppLogGap('web-a'), 'Not observed (gap or sampling: no app log for web-a).'],
  [w.eventCapGap(5000),
    'Not observed (gap or sampling: the rules read only the first 5000 matching events, so overlap and log results are partial).'],
  [w.shortSegmentGap('web-a w3wp:1240', 'private bytes', '2001-10-25T00:00:00Z', '2001-10-25T00:30:00Z', 7, '30 min', 12, '30 min', '80'),
    'Not observed (gap or sampling: web-a w3wp:1240 private bytes: the segment from 2001-10-25T00:00:00Z to 2001-10-25T00:30:00Z has 7 samples over 30 min; a rise is tested on 12 or more samples over 30 min or more, with 80 % of the samples expected).'],
  [w.noSlowlogImportGap('redis-a'), 'Not observed (gap or sampling: no Redis SLOWLOG import for redis-a).'],
  [w.slowlogThresholdGap('redis-a'), 'Not observed (gap or sampling: slowlog-log-slower-than not stated for redis-a).'],
  [w.slowlogWindowUnknownGap('redis-a'), 'Not observed (gap or sampling: the Redis slow-log window on redis-a is unknown).'],
  [w.slowlogWindowStartsGap('redis-a', '2001-10-25T01:02:20Z'),
    'Not observed (gap or sampling: the Redis slow-log window on redis-a starts at 2001-10-25T01:02:20Z, after this interval starts).'],
  [w.slowlogWindowEndsGap('redis-a', '2001-10-25T01:02:10Z'),
    'Not observed (gap or sampling: the Redis slow-log window on redis-a ends at 2001-10-25T01:02:10Z, before this interval ends).'],
  [w.slowlogIdGap('redis-a'),
    'Not observed (gap or sampling: the Redis slow-log buffer on redis-a has a gap in its entry ids, so entries may be missing).'],
  [w.recycleGapEnabledTriggers(),
    'Application pool recycle events are logged only for the triggers enabled in logEventOnRecycle; other recycle triggers may have left no event.'],
  [w.recycleGapLegacyDefaults(),
    'Application pool recycle events are logged by default only for the Time, Memory and PrivateMemory triggers before IIS 8.5, and logEventOnRecycle may have been changed; other recycle triggers may have left no event.']
];

test('the A2 wordings read exactly as the Go twins do', () => {
  for (const [got, want] of CASES) assert.equal(got, want);
  assert.equal(w.onHost('Host CPU at or above 90 %', 'web-a'), 'Host CPU at or above 90 % on web-a');
  assert.equal(w.listEntry('w3wp:1240', '82 %'), 'w3wp:1240 (82 %)');
  assert.equal(w.otherProcesses('5 %'), 'all other processes (5 %)');
  assert.equal(w.listedProcessesOnly(), '(sum of listed processes only)');
  assert.equal(w.hostName(''), 'an unnamed host');
  assert.equal(w.hostName('web-a'), 'web-a');
});

test('the A2 wordings pass the copy lint (no causal word, exact wording)', () => {
  for (const [s] of CASES) assert.deepEqual(rules(s), [], s);
});

// The copy lint holds prose around a wording to grade 8.5 and leaves the fixed wordings out of that count (they
// carry host names, timestamps and product names). The two recycle sentences must be fixed wordings, not prose.
test('the recycle gap sentences are fixed wordings, so they stay out of the reading grade', () => {
  for (const s of [w.recycleGapEnabledTriggers(), w.recycleGapLegacyDefaults()]) {
    assert.deepEqual(rules(s), [], s);
    // Reworded, the same sentence is prose and fails.
    assert.ok(rules(s.replace('may have left no event', 'might have left no event')).includes('wording'), s);
  }
});

test('paraphrased A2 wordings fail the copy lint', () => {
  for (const s of [
    'This interval ends soon before a restart.',
    'This interval ends 1 min before the recycle event on web-a (Correlation).',
    'The Redis SLOWLOG held a slow command of 1.012 s overlapping this interval (Correlation).',
    'Not observed (the Redis slow-log started late).',
    'Minimum available memory in this window: 420 MiB.'
  ]) {
    assert.ok(rules(s).includes('wording'), s);
  }
});
