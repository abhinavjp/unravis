const assert = require('node:assert/strict');
const test = require('node:test');
const { loadTypeScript, read } = require('./scn1.l4.helpers.cjs');
const investigation = loadTypeScript('src/lib/investigation.ts');
const charts = loadTypeScript('src/lib/charts.ts');
const { lintCopy } = loadTypeScript('src/lib/copy/copylint.ts');
const { deck } = loadTypeScript('src/lib/copy/deck.ts');

function stats({ count = 0, p50 = null, p95 = null, histogram = [] } = {}) {
  return { count, meanMs: null, minMs: null, maxMs: null, p50, p95, histogram };
}
function comparison(key, controller, outcome = 'success', currentEvents = 0) {
  return {
    key, sourceId: 'source-1', controller, action: 'run', kind: 'synchronous', outcome,
    state: 'compared', current: stats(), reference: stats({ p50: { lowerMs: 90, upperMs: 110, valueMs: 100 } }),
    medianRatio: null, p95Ratio: null, medianDeltaMs: null, p95DeltaMs: null,
    currentEvents, referenceEvents: 0
  };
}
function point(at, ratio) {
  return {
    at, count: ratio == null ? null : 1,
    p50: ratio == null ? null : { lowerMs: 1, upperMs: 2, valueMs: 1.5 },
    p95: ratio == null ? null : { lowerMs: 2, upperMs: 3, valueMs: 2.5 },
    p95Ratio: ratio
  };
}
function analysis({ start, end, bucketSeconds, apis = [], timeline = [] }) {
  return {
    query: {
      sourceIds: ['source-1'], start, end, baselineStart: start, baselineEnd: end,
      kind: 'all', outcome: 'all', metric: 'total', bucketSeconds, controller: '', action: ''
    },
    apis, timeline, sources: [], quality: { incomplete: 0, invalid: 0, unknownOutcome: 0 },
    approximate: true, warnings: []
  };
}

test('D-A-46: Charts no longer compares the target with epoch time', () => {
  const src = read('src/components/Charts.svelte');
  assert.doesNotMatch(src, /Date\.parse\(analysis\.query\.start\)/);
  assert.match(src, /const targetMs = \$derived\(appliedTarget\(wholeTargetMs\)\);/);
  assert.match(src, /import \{ appliedTarget \} from '\.\.\/lib\/investigation';/);
});

test('D-A-46: appliedTarget keeps a usable duration and drops the rest', () => {
  assert.equal(investigation.appliedTarget(100), 100);
  assert.equal(investigation.appliedTarget(0.5), 0.5);
  assert.equal(investigation.appliedTarget(3600000), 3600000);
  for (const value of [0, -5, NaN, Infinity, 1e9, 3600001, null, undefined]) assert.equal(investigation.appliedTarget(value), null, String(value));
});

test('D-A-46: a target of 100 gives a series named Response-time target', () => {
  const start = '1970-01-01T00:00:00.000Z', end = '1970-01-01T00:00:10.000Z';
  const row = comparison('target-key', 'Target', 'success', 1);
  const result = analysis({ start, end, bucketSeconds: 10, apis: [row], timeline: [{ key: row.key, points: [point(start, 1.5)] }] });
  const withTarget = charts.timelineOption(result, row, 'UTC', 100);
  assert.ok(withTarget.series.some(item => item.name === 'Response-time target' && item.data.every(value => value === 100)));
  assert.ok(!charts.timelineOption(result, row, 'UTC', null).series.some(item => item.name === 'Response-time target'));
});

test('D-A-47: targetProblem says Not applied for a target that is not used', () => {
  for (const value of [0, -5, NaN, 1e9]) assert.equal(investigation.targetProblem(value), 'Not applied. Enter a response time above 0 and up to 3,600,000 ms.', String(value));
  for (const value of [null, undefined, 100, 0.5, 3600000]) assert.equal(investigation.targetProblem(value), '', String(value));
});

test('D-A-47: intervalRangeProblem flags a number outside 10 to 86,400', () => {
  for (const value of [5, 9, 86401, 10.5, NaN, null, undefined]) assert.equal(investigation.intervalRangeProblem(value), 'Use a whole number from 10 to 86,400 seconds. The run waits until you do.', String(value));
  for (const value of [10, 60, 86400]) assert.equal(investigation.intervalRangeProblem(value), '', String(value));
});

test('D-A-47: the new sentences pass the copy lint', () => {
  assert.deepEqual(lintCopy([{ id: 'target-problem', text: investigation.targetProblem(0) }, { id: 'interval-problem', text: investigation.intervalRangeProblem(5) }], deck.terms), []);
});

test('D-A-47: ChartGroup shows both lines next to their own field', () => {
  const group = read('src/components/filters/ChartGroup.svelte');
  assert.match(group, /import \{ intervalRangeProblem, targetProblem \} from '\.\.\/\.\.\/lib\/investigation';/);
  assert.match(group, /id="bucket-error" role="alert"/);
  assert.match(group, /id="latency-target-error" role="alert"/);
  assert.match(group, /aria-describedby=\{targetText \? 'latency-target-error' : undefined\}/);
  assert.match(group, /aria-describedby=\{intervalRangeText \? 'bucket-error' : undefined\}/);
  assert.match(group, /max="3600000"/);
  assert.match(group, /Adds a chart line compared with P95\./);
  assert.match(group, /10–86,400 seconds; no more than 120 points\./);
});

test('D-A-47: a bad target does not set the problem dot', () => {
  const group = read('src/components/filters/ChartGroup.svelte');
  assert.match(group, /\$effect\(\(\) => \{ hasError = chartIntervalProblem\(query\); \}\);/);
});
