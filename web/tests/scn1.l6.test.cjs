const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const { loadTypeScript, change, unknown, result } = require('./guided.fixture.cjs');

const f = loadTypeScript('src/lib/guided/frame.ts');
const read = (rel) => fs.readFileSync(path.join(__dirname, '..', rel), 'utf8');

// A copy of internal/audit/query.go epochBucketCount, working in milliseconds.
const buckets = (startMs, endMs, seconds) => {
  const unit = seconds * 1000;
  const first = Math.floor(startMs / unit);
  const last = Math.floor(endMs / unit);
  return last - first + (endMs % unit !== 0 ? 1 : 0);
};
const frameOf = (incidentStart, incidentEnd, referenceStart, referenceEnd) => ({ ...f.emptyFrame(), incidentStart, incidentEnd, referenceStart, referenceEnd });

test('B-01: the slowest-calls bucket size keeps both windows inside 120 buckets', () => {
  const cases = [
    ['2026-10-17T10:30', '2026-10-17T11:00', '2026-10-17T10:00', '2026-10-17T10:30', 60],
    ['2026-10-17T10:00', '2026-10-17T11:59', '2026-10-17T09:00', '2026-10-17T10:00', 60],
    ['2026-10-17T10:00', '2026-10-17T12:00', '2026-10-17T08:00', '2026-10-17T10:00', 120],
    ['2026-10-20T05:30', '2026-10-20T08:00', '2026-10-20T03:00', '2026-10-20T05:30', 120],
    ['2026-10-16T00:00', '2026-10-17T00:00', '2026-10-15T00:00', '2026-10-16T00:00', 780],
    ['2026-10-10T00:00', '2026-10-17T00:00', '2026-10-03T00:00', '2026-10-10T00:00', 5100]
  ];
  for (const [is, ie, rs, re, expected] of cases) {
    const frame = frameOf(is, ie, rs, re);
    assert.deepEqual(f.frameProblems(frame), [], is);
    const q = f.slowQuery(frame, ['s']);
    assert.equal(q.bucketSeconds, expected, is + ' to ' + ie);
    assert.ok(buckets(Date.parse(q.start), Date.parse(q.end), q.bucketSeconds) <= 120, 'incident ' + is);
    assert.ok(buckets(Date.parse(q.baselineStart), Date.parse(q.baselineEnd), q.bucketSeconds) <= 120, 'reference ' + is);
  }
});

test('B-01: the longer window decides, and an odd start time stays inside the cap', () => {
  // a short incident with a 5 hour normal period: 18000 s / 119 = 151 s, rounded up to 3 minutes
  const q = f.slowQuery(frameOf('2026-10-17T10:00', '2026-10-17T10:30', '2026-10-17T05:00', '2026-10-17T10:00'), ['s']);
  assert.equal(q.bucketSeconds, 180);
  const odd = f.slowQuery(frameOf('2026-10-20T05:37', '2026-10-20T08:07', '2026-10-20T03:07', '2026-10-20T05:37'), ['s']);
  assert.ok(buckets(Date.parse(odd.start), Date.parse(odd.end), odd.bucketSeconds) <= 120);
  assert.equal(f.slowBucketSeconds(f.emptyFrame()), 60);
});
