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

const copy = loadTypeScript('src/lib/copy/guidedCopy.ts');

test('B-07: a stale sticker reads Out of date, rerun, in a neutral tone, and keeps the readiness word for screen readers', () => {
  const src = read('src/lib/components/guided/Sticker.svelte');
  assert.equal(copy.GUIDED_TEXT.outOfDate, 'Out of date, rerun');
  assert.ok(/import \{ GUIDED_TEXT as T \} from '\.\.\/\.\.\/copy\/guidedCopy'/.test(src), 'the sticker uses the shared copy');
  assert.ok(/\{T\.outOfDate\}/.test(src), 'the visible stale text is T.outOfDate');
  assert.ok(!src.includes('· Out of date'), 'the old dotted text is gone');
  assert.ok(/'sticker tone-' \+ \(outOfDate \? 'stale' : tone\)/.test(src), 'a stale sticker takes the neutral tone, not the readiness colour');
  assert.ok(/class="visually-hidden">\{word\}\. </.test(src), 'the old word is kept for screen readers');
  assert.ok(/\.tone-stale \{[^}]*var\(--surface-2\)/.test(src), 'the neutral fill exists');
  assert.ok(/\.stale \{[^}]*margin-inline-start/.test(src), 'the rule pinned by guided.ui.test.cjs stays');
});

test('B-21: a change with a blank name is a frame problem, a named one is not', () => {
  const base = { ...f.emptyFrame(), incidentStart: '2026-10-17T10:30', incidentEnd: '2026-10-17T11:00', referenceStart: '2026-10-17T09:30', referenceEnd: '2026-10-17T10:30' };
  assert.deepEqual(f.frameProblems({ ...base, releases: [{ at: '2026-10-17T10:00', label: '' }] }), ['Each change needs a name.']);
  assert.deepEqual(f.frameProblems({ ...base, releases: [{ at: '2026-10-17T10:00', label: '   ' }] }), ['Each change needs a name.']);
  assert.deepEqual(f.frameProblems({ ...base, releases: [{ at: '2026-10-17T10:00', label: 'v2' }] }), []);
  assert.deepEqual(f.frameProblems({ ...base, releases: [{ at: 'soon', label: '' }] }), ['Each change needs a time, in UTC.', 'Each change needs a name.']);
  assert.equal(f.toRequest({ ...base, releases: [{ at: '2026-10-17T10:00', label: '' }] }, ['s']), null);
});

test('B-21 and B-22: Step 0 explains an unticked box and a full change list', () => {
  const src = read('src/lib/components/guided/Step0Frame.svelte');
  assert.ok(/\{#if store\.frame\.releases\.length >= MAX_RELEASES\}<p class="note" role="status">\{T\.changesFull\}<\/p>\{\/if\}/.test(src));
  assert.ok(/\{#if store\.frame\.releases\.length > 0 && !store\.frame\.releasesEntered\}<p class="note" role="status">\{T\.changesUnticked\}<\/p>\{\/if\}/.test(src));
  assert.equal(copy.GUIDED_TEXT.changesUnticked, 'These changes are used only when the box beside Add a change is ticked.');
  // the full-list note is the same sentence the frame check uses
  const many = Array.from({ length: 51 }, () => ({ at: '2026-10-17T10:00', label: 'r' }));
  const base = { ...f.emptyFrame(), incidentStart: '2026-10-17T10:30', incidentEnd: '2026-10-17T11:00', referenceStart: '2026-10-17T09:30', referenceEnd: '2026-10-17T10:30' };
  assert.deepEqual(f.frameProblems({ ...base, releases: many }), [copy.GUIDED_TEXT.changesFull]);
});

const m = loadTypeScript('src/lib/guided/stepModel.ts');

test('B-03: the focus hint names Steps 3 and 4 and the table says it lists every API', () => {
  assert.equal(copy.GUIDED_TEXT.focusNote, 'An API is the URL path of one endpoint. Steps 3 and 4 show only that API. The rest still read every API.');
  assert.ok(!/Steps 2 to 4/.test(copy.GUIDED_TEXT.focusNote));
  assert.equal(copy.GUIDED_TEXT.byApiFocusNote, 'This table lists every API, so you can see whether the API you chose is the only one that moved.');
  const src = read('src/lib/components/guided/StepFacts.svelte');
  assert.ok(/const focusRoute = \$derived\(answeredRoute\(store\.answeredKey\)\)/.test(src));
  assert.ok(/\{#if focusRoute\}<p class="note">\{T\.byApiFocusNote\}<\/p>\{\/if\}/.test(src));
});

test('B-03: answeredRoute reads the route out of a request key and never throws', () => {
  const frame = { ...f.emptyFrame(), incidentStart: '2026-10-17T10:30', incidentEnd: '2026-10-17T11:00', referenceStart: '2026-10-17T09:30', referenceEnd: '2026-10-17T10:30' };
  const keyOf = (route) => f.requestKey(f.toRequest({ ...frame, route }, ['b', 'a']));
  assert.equal(m.answeredRoute(keyOf(' Reports.Generate ')), 'Reports.Generate');
  assert.equal(m.answeredRoute(keyOf('')), '');
  assert.equal(m.answeredRoute(''), '');
  assert.equal(m.answeredRoute('not json'), '');
  assert.equal(m.answeredRoute('{"a":1}'), '');
  assert.equal(m.answeredRoute('[1,2,3,4]'), '');
});

test('B-26: the minute chart caption says what it is, and the P95 caption is unchanged', () => {
  assert.equal(m.peakCaption('p95', 2345.6, 'ms'), 'Highest 2,346 ms.');
  assert.equal(m.peakCaption('inFlight', 1.4, 'calls'), 'Highest minute average: 1.4 calls.');
  assert.equal(m.peakCaption('inFlight', 1, 'calls'), 'Highest minute average: 1 call.');
  assert.equal(m.peakCaption('inFlight', 0.04, 'calls'), 'Highest minute average: 0.04 calls.');
  assert.equal(m.peakCaption('inFlight', 0, 'calls'), 'Highest minute average: 0 calls.');
  const src = read('src/lib/components/guided/MinuteLine.svelte');
  assert.ok(/import \{ fmtWhen, peakCaption \} from/.test(src));
  assert.ok(/\{peakCaption\(field, peak, unit\)\}/.test(src));
  assert.ok(!/Math\.round\(top\)/.test(src), 'the clamped scale is no longer printed as the highest value');
  assert.ok(/const top = \$derived\(Math\.max\(1, peak\)\)/.test(src), 'the scale keeps its floor of 1');
});

test('B-28: Calls in flight (average) shows enough decimals and never calls equal figures higher', () => {
  const r = result();
  const withLoad = (load) => result({ facts: { ...r.facts, load: { ...r.facts.load, ...load } } });
  const row = (load) => m.loadRows(withLoad(load))[2];
  // 0.101 and 0.104 both show as 0.1, so the direction is About the same even though the engine said up
  assert.deepEqual(row({ refL: 0.101, incL: 0.104, lDirection: 'up' }), { measure: 'Calls in flight (average)', normal: '0.1', incident: '0.1', direction: 'About the same' });
  // under 1 uses two decimals, so a visible difference keeps the engine direction
  assert.deepEqual(row({ refL: 0.12, incL: 0.18, lDirection: 'up' }), { measure: 'Calls in flight (average)', normal: '0.12', incident: '0.18', direction: 'Higher' });
  // over 1 keeps one decimal
  assert.deepEqual(row({ refL: 2.04, incL: 2.31, lDirection: 'up' }), { measure: 'Calls in flight (average)', normal: '2', incident: '2.3', direction: 'Higher' });
  // missing figures still read No data and Not sure
  const gone = row({ refL: undefined, incL: undefined, lDirection: 'unknown' });
  assert.equal(gone.incident, 'No data');
  assert.equal(gone.direction, 'Not sure');
  assert.equal(m.loadRows(result())[2].direction, 'Higher');
});

test('B-23: Step 5 says Change the list, and a list entered on purpose is not asked for again', () => {
  const src = read('src/lib/components/guided/StepFacts.svelte');
  assert.equal(copy.GUIDED_TEXT.changeListButton, 'Change the list');
  assert.equal(copy.GUIDED_TEXT.changesNoneEntered, 'You said there was no change to list.');
  assert.equal(copy.GUIDED_TEXT.changeWindow, 'Change the window');
  assert.ok(/\{result\.signals\.changes\.entered \? T\.changesNoneEntered : T\.changesNone\}/.test(src));
  assert.ok(/onclick=\{\(\) => store\.goTo\(0\)\}>\{T\.changeListButton\}<\/button>/.test(src));
  // Step 1 keeps its own button
  assert.ok(/\{T\.changeWindow\}/.test(src));
});

test('B-20: the checklist subtitle names the four lists exactly as the headings do', () => {
  assert.deepEqual(m.LISTS.map((l) => l.label), ['Need to do', 'Working on', 'What we learned', 'Ruled out']);
  for (const l of m.LISTS) assert.ok(copy.GUIDED_TEXT.checklistLead.includes(l.label), l.label);
  assert.ok(!/what is under way/i.test(copy.GUIDED_TEXT.checklistLead));
});

test('B-30: Step 1 names what is left out of the scope', () => {
  const base = result();
  const withCounts = (counts) => result({ facts: { ...base.facts, counts: { ...base.facts.counts, ...counts } } });
  assert.equal(m.readGap(base, ''), null, 'read equals used: nothing to explain');
  const stale = m.readGap(withCounts({ read: 1514, used: 1500, incomplete: 14, incident: 764, reference: 750 }), '');
  assert.deepEqual(stale, { total: '1,514', used: '1,500', parts: ['14 not completed'] });
  assert.equal(copy.GUIDED_TEXT.readGap(stale.total, stale.used, stale.parts), 'Scope counts 1,500 of the 1,514 calls read. Left out: 14 not completed.');
  const many = m.readGap(withCounts({ read: 1000, used: 940, incomplete: 20, noStart: 3, anomalous: 2, outside: 35 }), '');
  assert.deepEqual(many.parts, ['20 not completed', '3 with no start time', '2 with a clock anomaly', '35 outside both windows']);
  // an API focus leaves other APIs out by design
  const focus = m.readGap(withCounts({ read: 1000, used: 400 }), 'A.One');
  assert.deepEqual(focus.parts, ['600 to other APIs']);
  // no focus and no named reason: say nothing rather than guess
  assert.equal(m.readGap(withCounts({ read: 1000, used: 400 }), ''), null);
  const src = read('src/lib/components/guided/StepFacts.svelte');
  assert.ok(/\{#if gap\}<p class="note">\{T\.readGap\(gap\.total, gap\.used, gap\.parts\)\}<\/p>\{\/if\}/.test(src));
  assert.ok(/const gap = \$derived\(result \? readGap\(result, focusRoute\) : null\)/.test(src));
});

test('B-54: Step 3 says what IIS would add and the button names the IIS source', () => {
  assert.equal(copy.GUIDED_TEXT.addIisLogs, 'Add the IIS logs');
  assert.equal(copy.GUIDED_TEXT.iisAdds, 'IIS logs show the time before a call reaches the app (outside time, O).');
  assert.equal(copy.GUIDED_TEXT.addAuditLogs, undefined, 'the old audit logs name is gone');
  const src = read('src/lib/components/guided/StepFacts.svelte');
  assert.ok(/<p class="note">\{T\.iisAdds\}<\/p>\s*<div class="row"><button[^>]*onclick=\{onAddData\}>\{T\.addIisLogs\}<\/button><\/div>/.test(src));
  assert.ok(!/addAuditLogs/.test(src));
});

const fix = loadTypeScript('src/lib/guided/afterFix.ts');
const fixCtx = { normalP95: 500, windowMinutes: 10, laterLabel: '27 Oct 10:30 to 11:00' };
const fixDay = (p95, over = {}) => {
  const base = result();
  return result({ facts: { ...base.facts, change: { ...base.facts.change, p95 }, ...over } });
};

test('B-32: too few calls to read the slow end is its own outcome and cannot be recorded', () => {
  const v = fix.classifyDay(fixDay(unknown()), fixCtx);
  assert.equal(v.outcome, 'too-few');
  assert.equal(v.word, 'Too few calls');
  assert.equal(fix.OUTCOME_WORD['too-few'], 'Too few calls');
  assert.equal(v.sentence, 'Too few calls on one of the days to read the slow end. Pick a wider window or another day.');
  assert.equal(v.canRecord, false);
  assert.equal(v.recordLine, '');
  // no calls at all is still No data
  const base = result();
  const none = result({ facts: { ...base.facts, counts: { ...base.facts.counts, incident: 0 } } });
  assert.equal(fix.classifyDay(none, fixCtx).outcome, 'no-data');
  assert.equal(fix.classifyDay(none, fixCtx).word, 'No data for that day');
});

test('B-32: the earlier column falls back to the case figures, and never invents a percent', () => {
  const p95 = { ...change(3000, 520), refMs: undefined, deltaPct: undefined };
  const without = fix.classifyDay(fixDay(p95), fixCtx);
  assert.equal(without.rows[0].earlier, 'No data');
  const withEarlier = fix.classifyDay(fixDay(p95), { ...fixCtx, earlier: { p95: 3000, p50: 800, mean: 1200, l: 1.8 } });
  assert.equal(withEarlier.rows[0].earlier, '3,000 ms');
  assert.equal(withEarlier.rows[0].change, 'No data');
  // the run's own figure always wins over the fallback
  const own = fix.classifyDay(fixDay(change(3000, 520)), { ...fixCtx, earlier: { p95: 1 } });
  assert.equal(own.rows[0].earlier, '3,000 ms');
  // L: the engine has 0.6 for the earlier day, so a missing one is the only case that falls back
  const base = result();
  const noRef = fixDay(change(3000, 520), { load: { ...base.facts.load, refL: undefined } });
  assert.equal(fix.classifyDay(noRef, fixCtx).rows[3].earlier, 'No data');
  assert.equal(fix.classifyDay(noRef, { ...fixCtx, earlier: { l: 1.8 } }).rows[3].earlier, '1.8');
  assert.notEqual(fix.classifyDay(noRef, { ...fixCtx, earlier: { l: 1.8 } }).rows[3].change, 'No data');
});

test('B-32: a later day must come after the incident day', () => {
  const frame = { ...f.emptyFrame(), incidentStart: '2026-10-17T10:30', incidentEnd: '2026-10-17T11:00', referenceStart: '2026-10-17T09:30', referenceEnd: '2026-10-17T10:30' };
  assert.equal(fix.isLaterDay(frame, '2026-10-18'), true);
  assert.equal(fix.isLaterDay(frame, '2026-10-17'), false, 'the incident day itself');
  assert.equal(fix.isLaterDay(frame, '2026-10-16'), false, 'an earlier day');
  assert.equal(fix.isLaterDay(frame, ''), false);
  assert.equal(fix.isLaterDay(frame, 'tomorrow'), false);
  assert.equal(fix.isLaterDay({ ...frame, incidentStart: '' }, '2026-10-18'), false);
  assert.equal(copy.GUIDED_TEXT.fixDayAfter, 'The later day must come after the incident day. Pick a later date.');
  const src = read('src/lib/components/guided/AfterFix.svelte');
  assert.ok(/if \(!isLaterDay\(store\.frame, day\)\)/.test(src));
  assert.ok(/T\.fixDayAfter/.test(src));
  assert.ok(/frame\.route\.trim\(\) === answeredRoute\(store\.answeredKey\)/.test(src));
  assert.ok(/laterLabel: dayLabel\(request\.incident\.start, request\.incident\.end\),\s*earlier\s*\}\)/.test(src), 'earlier is passed to classifyDay');
});
