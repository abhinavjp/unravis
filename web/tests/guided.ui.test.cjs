const assert = require('node:assert/strict');
const test = require('node:test');
const { loadTypeScript, change, step, result } = require('./guided.fixture.cjs');

const m = loadTypeScript('src/lib/guided/stepModel.ts');
const f = loadTypeScript('src/lib/guided/frame.ts');
const fix = loadTypeScript('src/lib/guided/afterFix.ts');
const t = loadTypeScript('src/lib/guided/tools.ts');
const n = loadTypeScript('src/lib/guided/nearby.ts');
const r = loadTypeScript('src/lib/guided/report.ts');

const WORDS = ['Ready', 'Partly ready', 'Cannot test yet', 'Failed'];
const TONES = { Ready: 'ready', 'Partly ready': 'partly', 'Cannot test yet': 'cannot', Failed: 'failed' };

/* ---------------------------------------------------------- every step, every state */

test('every step shows each of the four status words with its own tone, and out of date keeps the word', () => {
  for (let number = 0; number <= 10; number += 1) {
    for (const word of WORDS) {
      const answer = result({ steps: Array.from({ length: 11 }, (_, i) => step(i, i === number ? word : 'Ready')) });
      const s = m.stickerFor(answer, number, 'done');
      assert.equal(s.word, word, `step ${number}`);
      assert.equal(s.tone, TONES[word]);
      assert.equal(s.outOfDate, false);
      // Step 0 never goes out of date: it is the question.
      const stale = m.railItems(answer, 'done', number, true)[number].sticker;
      assert.equal(stale.word, word);
      assert.equal(stale.outOfDate, number > 0);
    }
  }
});

test('waiting and running are not pass words, and a run that failed before any answer says Failed', () => {
  for (let number = 0; number <= 10; number += 1) {
    assert.equal(m.stickerFor(null, number, 'idle').tone, 'waiting');
    assert.equal(m.stickerFor(result(), number, 'running').tone, 'running');
    assert.equal(m.stickerFor(null, number, 'failed').word, 'Failed');
  }
});

test('a failed rerun keeps the earlier answer on screen with its own words', () => {
  assert.equal(m.stickerFor(result(), 3, 'failed').word, 'Ready');
});

/* ---------------------------------------------------------- after the fix, later frame */

const frame = () => ({ ...f.emptyFrame(), incidentStart: '2026-10-17T10:30', incidentEnd: '2026-10-17T11:00', referenceStart: '2026-10-17T09:30', referenceEnd: '2026-10-17T10:30', route: 'A.One' });

test('the later frame moves the incident hours to the later day and the incident becomes the reference', () => {
  const later = fix.laterFrame(frame(), '2026-10-20');
  assert.equal(later.incidentStart, '2026-10-20T10:30');
  assert.equal(later.incidentEnd, '2026-10-20T11:00');
  assert.equal(later.referenceStart, '2026-10-17T10:30');
  assert.equal(later.referenceEnd, '2026-10-17T11:00');
  assert.equal(later.route, 'A.One');
});

test('the later frame is null for an unreadable day or an unusable incident, and a same-day pick is flagged as overlapping', () => {
  assert.equal(fix.laterFrame(frame(), ''), null);
  assert.equal(fix.laterFrame(frame(), '2026-02-31'), null);
  assert.equal(fix.laterFrame(frame(), 'tomorrow'), null);
  assert.equal(fix.laterFrame({ ...frame(), incidentStart: '' }, '2026-10-20'), null);
  assert.equal(fix.laterFrame({ ...frame(), incidentEnd: '2026-10-17T10:00' }, '2026-10-20'), null);
  const same = fix.laterFrame(frame(), '2026-10-17');
  assert.ok(f.frameProblems(same).length > 0);
});

/* ---------------------------------------------------------- slowest APIs */

const routes = () => ([
  { route: 'A.One', incident: { n: 300, p95: 3000 }, reference: { n: 300, p95: 500 }, p95Change: change(500, 3000) },
  { route: 'B.Two', incident: { n: 100, p95: 410 }, reference: { n: 300, p95: 400 }, p95Change: change(400, 410) },
  { route: 'C.Few', incident: { n: 3 }, reference: { n: 300, p95: 400 }, p95Change: { known: false, meaningful: false, direction: 'flat', markPercent: 30, markMs: 100 } }
]);
const withRoutes = (list) => { const base = result(); return { ...base, facts: { ...base.facts, routes: list } }; };

test('slowest APIs rank by the slow end, put an unknown one last, and honour the call minimum and the name filter', () => {
  const answer = withRoutes(routes());
  assert.deepEqual(t.slowestApiRows(answer, 0, '').map((x) => x.route), ['A.One', 'B.Two', 'C.Few']);
  assert.deepEqual(t.slowestApiRows(answer, 20, '').map((x) => x.route), ['A.One', 'B.Two']);
  assert.deepEqual(t.slowestApiRows(answer, 0, ' two ').map((x) => x.route), ['B.Two']);
  assert.equal(t.slowestApiRows(answer, Number.NaN, '').length, 3);
  assert.deepEqual(t.slowestApiRows(null, 0, ''), []);
  const few = t.slowestApiRows(answer, 0, 'few')[0];
  assert.equal(few.p95, 'No data');
  assert.equal(few.mark.tone, 'grey');
});

/* ---------------------------------------------------------- nearby activity */

test('duration bands: under 1 s fast, 1 to 2 s slow, over 2 s very slow, no time unknown and never fast', () => {
  assert.equal(n.durationBand(999), 'fast');
  assert.equal(n.durationBand(1000), 'slow');
  assert.equal(n.durationBand(2000), 'slow');
  assert.equal(n.durationBand(2001), 'very-slow');
  for (const bad of [undefined, null, Number.NaN, -1, Infinity]) assert.equal(n.durationBand(bad), 'unknown');
});

test('the nearby window is built from a UTC moment and whole-minute spans of 0 to 120', () => {
  const w = n.nearbyWindow('2026-10-17T10:42', 5, 10);
  assert.equal(w.start, '2026-10-17T10:37:00.000Z');
  assert.equal(w.end, '2026-10-17T10:52:00.000Z');
  assert.equal(n.nearbyWindow('', 5, 5), null);
  assert.equal(n.nearbyWindow('2026-10-17T10:42', 0, 0), null);
  assert.equal(n.nearbyWindow('2026-10-17T10:42', 121, 0), null);
  assert.equal(n.nearbyWindow('2026-10-17T10:42', -1, 5), null);
  assert.equal(n.nearbyWindow('2026-10-17T10:42', 1.5, 5), null);
  assert.equal(n.nearbyWindow('2026-10-17T10:42', 120, 120).start, '2026-10-17T08:42:00.000Z');
});

test('the nearby moment starts at the found onset, else the incident start, else nothing', () => {
  assert.equal(n.defaultAround('2026-10-17T10:42:00Z', '2026-10-17T10:30'), '2026-10-17T10:42');
  assert.equal(n.defaultAround('not a date', '2026-10-17T10:30'), '2026-10-17T10:30');
  assert.equal(n.defaultAround(undefined, ''), '');
});

/* ---------------------------------------------------------- report */

test('report warnings list out of date, the read limit and the server warnings once each', () => {
  assert.deepEqual(r.reportWarnings(undefined, false, 'cut', 'old'), []);
  assert.deepEqual(r.reportWarnings({ requests: 1, iisRows: 0, truncated: false, warnings: [] }, false, 'cut', 'old'), []);
  assert.deepEqual(r.reportWarnings({ requests: 1, iisRows: 0, truncated: true, warnings: ['w', 'w', ''] }, true, 'cut', 'old'), ['old', 'cut', 'w']);
});

test('report file names have no folders or control characters, with a fallback per format', () => {
  assert.equal(r.reportFileName({ filename: 'a/b/report.html', format: 'html' }), 'report.html');
  assert.equal(r.reportFileName({ filename: '', format: 'csv' }), 'unravis-data.csv');
  assert.equal(r.reportFileName({ filename: '..', format: 'html' }), 'unravis-report.html');
  assert.equal(r.reportFileName({ filename: 'a<b>:c.html', format: 'html' }), 'abc.html');
});

test('byte labels are short and never negative', () => {
  assert.equal(r.byteLabel(0), '0 bytes');
  assert.equal(r.byteLabel(512), '512 bytes');
  assert.equal(r.byteLabel(2048), '2 KB');
  assert.equal(r.byteLabel(3 * 1024 * 1024), '3.0 MB');
  assert.equal(r.byteLabel(-5), '0 bytes');
});
