const assert = require('node:assert/strict');
const test = require('node:test');
const { loadTypeScript, change, unknown, result } = require('./guided.fixture.cjs');

const f = loadTypeScript('src/lib/guided/frame.ts');
const fix = loadTypeScript('src/lib/guided/afterFix.ts');
const t = loadTypeScript('src/lib/guided/tools.ts');

const NOW = Date.parse('2026-10-20T09:00:30Z');
const good = () => ({ ...f.emptyFrame(), incidentStart: '2026-10-17T10:30', incidentEnd: '2026-10-17T11:00', referenceStart: '2026-10-17T09:30', referenceEnd: '2026-10-17T10:30' });

/* ------------------------------------------------------------------ Step 0 */

test('times are read as UTC and a date that does not exist is refused', () => {
  assert.equal(f.parseLocalUtc('2026-10-17T10:30'), Date.parse('2026-10-17T10:30:00Z'));
  assert.ok(Number.isNaN(f.parseLocalUtc('2026-02-31T10:30')));
  assert.ok(Number.isNaN(f.parseLocalUtc('2026-10-17 10:30')));
  assert.ok(Number.isNaN(f.parseLocalUtc('')));
  assert.ok(Number.isNaN(f.parseLocalUtc('2026-10-17T25:00')));
  assert.equal(f.toLocalUtc(Date.parse('2026-10-17T10:30:59Z')), '2026-10-17T10:30');
});

test('quick picks set an incident window and a normal period of the same length before it', () => {
  const hour = f.quickPick('last-hour', NOW);
  assert.deepEqual(hour, { incidentStart: '2026-10-20T08:00', incidentEnd: '2026-10-20T09:00', referenceStart: '2026-10-20T07:00', referenceEnd: '2026-10-20T08:00' });
  const today = f.quickPick('today', NOW);
  assert.equal(today.incidentStart, '2026-10-20T00:00');
  assert.equal(today.incidentEnd, '2026-10-20T09:00');
  assert.equal(today.referenceEnd, today.incidentStart);
  const yesterday = f.quickPick('yesterday', NOW);
  assert.equal(yesterday.incidentStart, '2026-10-19T00:00');
  assert.equal(yesterday.incidentEnd, '2026-10-20T00:00');
  assert.equal(yesterday.referenceStart, '2026-10-18T00:00');
  // just after midnight, Today is still a window of at least five minutes
  const early = f.quickPick('today', Date.parse('2026-10-20T00:01:00Z'));
  assert.equal(early.incidentEnd, '2026-10-20T00:05');
  for (const pick of ['last-hour', 'today', 'yesterday']) {
    assert.deepEqual(f.frameProblems({ ...f.emptyFrame(), ...f.quickPick(pick, NOW) }), []);
  }
});

test('the windows are checked in plain words', () => {
  assert.deepEqual(f.frameProblems(good()), []);
  assert.equal(f.frameProblems(f.emptyFrame()).length, 2);
  assert.match(f.frameProblems({ ...good(), incidentEnd: '2026-10-17T10:00' })[0], /ends before it starts/);
  assert.match(f.frameProblems({ ...good(), incidentStart: '2026-10-01T00:00' }).join(' '), /longer than 7 days/);
  assert.match(f.frameProblems({ ...good(), referenceEnd: '2026-10-17T10:45' }).join(' '), /overlap/);
  // windows that touch end to start do not overlap
  assert.deepEqual(f.frameProblems(good()).filter((p) => /overlap/.test(p)), []);
  assert.match(f.frameProblems({ ...good(), poolLimit: '0' }).join(' '), /pool limit/);
  assert.match(f.frameProblems({ ...good(), poolLimit: '1.5' }).join(' '), /pool limit/);
  assert.match(f.frameProblems({ ...good(), poolLimit: '100001' }).join(' '), /pool limit/);
  assert.match(f.frameProblems({ ...good(), poolLimit: 'abc' }).join(' '), /pool limit/);
  assert.deepEqual(f.frameProblems({ ...good(), poolLimit: '100' }), []);
  assert.deepEqual(f.frameProblems({ ...good(), poolLimit: '  ' }), []);
  const many = Array.from({ length: 51 }, () => ({ at: '2026-10-17T10:00', label: 'r' }));
  assert.match(f.frameProblems({ ...good(), releases: many }).join(' '), /at most 50/);
  assert.match(f.frameProblems({ ...good(), releases: [{ at: 'soon', label: 'r' }] }).join(' '), /needs a time/);
});

test('the request has ISO times, trims text, and is null when it cannot be run', () => {
  assert.equal(f.toRequest(good(), []), null);
  assert.equal(f.toRequest(f.emptyFrame(), ['s']), null);
  const r = f.toRequest({ ...good(), route: ' A.One ', poolLimit: '50', releasesEntered: true, releases: [{ at: '2026-10-17T10:00', label: ' v2 '.padEnd(300, 'x') }] }, ['s1', 's2'], false);
  assert.equal(r.incident.start, '2026-10-17T10:30:00.000Z');
  assert.equal(r.route, 'A.One');
  assert.equal(r.poolLimit, 50);
  assert.equal(r.releases[0].label.length, 200);
  assert.equal(r.includeSeries, false);
  assert.equal('poolLimit' in f.toRequest(good(), ['s']), false);
});

test('the request key ignores source order and series, and changes with anything that changes the answer', () => {
  const a = f.toRequest(good(), ['b', 'a']);
  const b = f.toRequest(good(), ['a', 'b'], false);
  assert.equal(f.requestKey(a), f.requestKey(b));
  assert.notEqual(f.requestKey(a), f.requestKey(f.toRequest({ ...good(), route: 'X.Y' }, ['a', 'b'])));
  assert.notEqual(f.requestKey(a), f.requestKey(f.toRequest({ ...good(), poolLimit: '9' }, ['a', 'b'])));
  assert.notEqual(f.requestKey(a), f.requestKey(f.toRequest(good(), ['a'])));
  assert.notEqual(f.requestKey(a), f.requestKey(f.toRequest({ ...good(), releasesEntered: true }, ['a', 'b'])));
});

test('widening the incident window moves the start earlier and stays inside 7 days', () => {
  assert.equal(f.widenIncident(good(), 30).incidentStart, '2026-10-17T10:00');
  const wide = { ...good(), incidentStart: '2026-10-10T11:30', incidentEnd: '2026-10-17T11:00' };
  assert.equal(f.widenIncident(wide, 600).incidentStart, '2026-10-10T11:00');
  assert.equal(f.widenIncident({ ...good(), incidentStart: 'junk' }, 30).incidentStart, 'junk');
  assert.equal(f.windowMinutes('2026-10-17T10:30', '2026-10-17T11:00'), 30);
  assert.equal(f.windowMinutes('2026-10-17T11:00', '2026-10-17T10:30'), 0);
  assert.equal(f.windowMinutes('', ''), 0);
});

/* ------------------------------------------------------------------ After the fix */

const ctx = { normalP95: 500, windowMinutes: 10, laterLabel: '27 Oct 10:30 to 11:00' };
const day = (p95, over = {}) => {
  const base = result();
  const facts = { ...base.facts, change: { ...base.facts.change, p95 }, ...over };
  return result({ facts });
};

test('a later day with a slow end back inside the normal range is Fixed', () => {
  const v = fix.classifyDay(day(change(3000, 520)), ctx);
  assert.equal(v.outcome, 'fixed');
  assert.equal(v.word, 'Fixed');
  assert.equal(v.canRecord, true);
  assert.ok(v.recordLine.length > 0 && v.recordLine.length <= 300);
  assert.match(v.recordLine, /Fixed/);
});

test('Fixed needs a known normal range: without one the best answer is Better, not fixed', () => {
  const v = fix.classifyDay(day(change(3000, 520)), { ...ctx, normalP95: undefined });
  assert.equal(v.outcome, 'better');
  assert.ok(v.warnings.length > 0);
});

test('a fall that is still above normal is Better, not fixed', () => {
  const v = fix.classifyDay(day(change(3000, 1200)), ctx);
  assert.equal(v.outcome, 'better');
  assert.match(v.sentence, /still above/);
});

test('a rise is Worse, and no movement is Unchanged', () => {
  assert.equal(fix.classifyDay(day(change(500, 2000)), ctx).outcome, 'worse');
  assert.equal(fix.classifyDay(day(change(3000, 2950)), ctx).outcome, 'unchanged');
});

test('a day with no calls, or no earlier day, or no slow end is No data and cannot be recorded', () => {
  const base = result();
  const none = result({ facts: { ...base.facts, counts: { ...base.facts.counts, incident: 0 } } });
  const v = fix.classifyDay(none, ctx);
  assert.equal(v.outcome, 'no-data');
  assert.equal(v.canRecord, false);
  assert.equal(v.recordLine, '');
  assert.equal(fix.classifyDay(result({ facts: { ...base.facts, counts: { ...base.facts.counts, reference: 0 } } }), ctx).outcome, 'no-data');
  assert.equal(fix.classifyDay(day(unknown()), ctx).outcome, 'no-data');
  // never a zero comparison: every row of a no-data day reads No data or the figures the engine gave
  assert.ok(!v.rows.some((r) => /^0 ms$/.test(r.later) && r.later === r.earlier));
});

test('a day with missing minutes is Partly there, and the missing minutes are not counted as zero', () => {
  const base = result();
  const v = fix.classifyDay(day(change(3000, 520), { incident: { ...base.facts.incident, testableMinutes: 4 } }), ctx);
  assert.equal(v.outcome, 'partly');
  assert.match(v.warnings[0], /Only 4 of 10 minutes/);
  assert.match(v.warnings[0], /not counted as zero/);
  // partly beats fixed: a day with gaps is never called fixed
  assert.notEqual(v.outcome, 'fixed');
});

test('days that are not alike get a warning, and are never called fixed', () => {
  const base = result();
  const busier = day(change(3000, 520), { incident: { ...base.facts.incident, perSec: 5 } });
  const v = fix.classifyDay(busier, ctx);
  assert.equal(v.outcome, 'unlike');
  assert.match(v.warnings[0], /Calls per second rose/);
  assert.match(v.recordLine, /Warning/);
  const mixed = day(change(3000, 520), { routes: [
    { route: 'A.One', incident: { n: 10 }, reference: { n: 300 }, p95Change: unknown() },
    { route: 'B.Two', incident: { n: 390 }, reference: { n: 0 }, p95Change: unknown() }
  ] });
  assert.equal(fix.classifyDay(mixed, ctx).outcome, 'unlike');
  assert.ok(fix.mixShift(mixed) >= fix.MIX_SHIFT);
  assert.equal(fix.mixShift(result({ facts: { ...base.facts, routes: [] } })), 0);
});

test('the table has the four measures, and the load change is No data when a day has none', () => {
  const v = fix.classifyDay(day(change(3000, 520)), ctx);
  assert.deepEqual(v.rows.map((r) => r.measure.split(' ')[0]), ['P95', 'P50', 'D', 'L']);
  assert.equal(v.rows[0].mark.tone, 'ok');
  const base = result();
  const noLoad = fix.classifyDay(day(change(3000, 520), { load: { ...base.facts.load, refL: undefined } }), ctx);
  assert.equal(noLoad.rows[3].change, 'No data');
});

test('the record line is cut to 300 characters', () => {
  const v = fix.classifyDay(day(change(3000, 520)), { ...ctx, laterLabel: 'x'.repeat(400) });
  assert.equal(v.recordLine.length, 300);
});

test('aboveNormal needs both marks, and dayLabel never invents a date', () => {
  const th = { changeMinPercent: 30, changeMinMs: 100 };
  assert.equal(fix.aboveNormal(500, 640, th), false);
  assert.equal(fix.aboveNormal(500, 700, th), true);
  assert.equal(fix.aboveNormal(0, 700, th), false);
  assert.equal(fix.aboveNormal(100, 180, th), false);
  assert.equal(fix.dayLabel('2026-10-27T10:30:00Z', '2026-10-27T11:00:00Z'), '27 Oct 10:30 to 11:00');
  assert.equal(fix.dayLabel('2026-10-27T23:30:00Z', '2026-10-28T00:30:00Z'), '27 Oct 23:30 to 28 Oct 00:30');
  assert.equal(fix.dayLabel('', 'x'), 'the chosen hours');
});

/* ------------------------------------------------------------------ Manual tools */

test('there are thirteen tools with unique ids', () => {
  assert.equal(t.TOOLS.length, 13);
  assert.equal(new Set(t.TOOLS.map((x) => x.id)).size, 13);
  assert.equal(t.toolById('slowest-apis').title, 'Slowest APIs');
  assert.equal(t.toolById('nope'), undefined);
});

test('a tool missing its source is Cannot test yet and names the source', () => {
  const none = { have: new Set(), result: null };
  for (const tool of t.TOOLS) {
    const s = t.toolState(tool, none);
    assert.equal(s.word, 'Cannot test yet', tool.id);
    assert.ok(s.missing.length > 0);
    assert.match(s.why, /needs/);
  }
  const audit = { have: new Set(['audit']), result: null };
  assert.match(t.toolState(t.toolById('sql-waits'), audit).why, /SQL Server source/);
  assert.match(t.toolState(t.toolById('mongo-health'), audit).why, /MongoDB source/);
  assert.match(t.toolState(t.toolById('host-resources'), audit).why, /Blackbox source/);
  assert.equal(t.toolState(t.toolById('slowest-apis'), audit).word, 'Ready');
});

test('the time split runs on audit alone and says what IIS would add', () => {
  const audit = { have: new Set(['audit']), result: null };
  const s = t.toolState(t.toolById('time-split'), audit);
  assert.equal(s.word, 'Partly ready');
  assert.match(s.why, /IIS/);
  assert.equal(t.toolState(t.toolById('time-split'), { have: new Set(['audit', 'iis']), result: null }).word, 'Ready');
});

test('a tool that shows a step takes the engine word for that step', () => {
  const { step } = require('./guided.fixture.cjs');
  const r = result({ steps: [step(3, 'Partly ready', { reasons: ['No IIS source.'] }), step(8, 'Failed')] });
  const have = new Set(['audit', 'iis', 'mongo']);
  assert.equal(t.toolState(t.toolById('time-split'), { have, result: r }).word, 'Partly ready');
  assert.equal(t.toolState(t.toolById('time-split'), { have, result: r }).why, 'No IIS source.');
  assert.equal(t.toolState(t.toolById('mongo-health'), { have, result: r }).word, 'Failed');
});

test('a view name is plain text of at most 60 characters', () => {
  assert.equal(t.cleanViewName('  Checkout\n\tslow  '), 'Checkout slow');
  assert.equal(t.cleanViewName('x'.repeat(100)).length, 60);
  assert.equal(t.cleanViewName('\u0000\u0007 '), '');
  assert.equal(t.cleanViewName(''), '');
});

test('a view saves windows and filters only: no result, no URL, no secret-looking keys', () => {
  const s = t.viewSettings({ ...good(), route: 'A.One', poolLimit: '99', releases: [{ at: 'x', label: 'secret release' }] }, { minCalls: 20, match: 'Pay' });
  assert.deepEqual(Object.keys(s).sort(), ['incidentEnd', 'incidentStart', 'match', 'minCalls', 'referenceEnd', 'referenceStart', 'route']);
  assert.ok(!JSON.stringify(s).includes('secret'));
  assert.equal(t.viewSettings(good(), { match: 'http://evil/x?token=1' }).match, undefined);
  assert.equal(t.viewSettings(good(), { minCalls: NaN }).minCalls, undefined);
  assert.equal(t.viewSettings(good(), { minCalls: -5 }).minCalls, 0);
  assert.equal(t.viewSettings(good(), { minCalls: 9e12 }).minCalls, 1000000);
  assert.equal(t.viewSettings(good(), { match: 'x'.repeat(500) }).match.length, 100);
  assert.ok(JSON.stringify(t.viewSettings(good())).length < 1024);
});

test('a damaged saved view opens with whatever is good in it', () => {
  assert.deepEqual(t.readViewSettings(undefined), {});
  assert.deepEqual(t.readViewSettings(null), {});
  const hostile = { incidentStart: '2026-10-17T10:30', incidentEnd: 12, referenceStart: 'tomorrow', route: 'http://x', match: 'ok', minCalls: '20', password: 'p', __proto__: { x: 1 } };
  assert.deepEqual(t.readViewSettings(hostile), { incidentStart: '2026-10-17T10:30', match: 'ok' });
  assert.deepEqual(t.readViewSettings({ minCalls: -1 }), {});
  assert.deepEqual(t.readViewSettings({ minCalls: 7.9 }), { minCalls: 7 });
  assert.deepEqual(t.readViewSettings({ route: 'x'.repeat(300) }), {});
});

test('applying a view changes only the windows and route it holds', () => {
  const frame = { ...good(), poolLimit: '12', releasesEntered: true };
  const out = t.applyViewToFrame(frame, { incidentStart: '2026-10-18T10:30', route: 'Z.Z' });
  assert.equal(out.incidentStart, '2026-10-18T10:30');
  assert.equal(out.incidentEnd, frame.incidentEnd);
  assert.equal(out.route, 'Z.Z');
  assert.equal(out.poolLimit, '12');
  assert.deepEqual(t.applyViewToFrame(frame, {}), frame);
});

test('the 200-view limit is detected at the limit, not after it, and window text is short', () => {
  const views = Array.from({ length: 199 }, (_, i) => ({ id: String(i) }));
  assert.equal(t.viewsFull(views), false);
  assert.equal(t.viewsFull([...views, { id: 'x' }]), true);
  assert.equal(t.viewsFull(Array.from({ length: 100 }, () => ({}))), false);
  assert.equal(t.viewWindows({ incidentStart: '2026-10-17T10:30', incidentEnd: '2026-10-17T11:00' }), '2026-10-17 10:30 to 2026-10-17 11:00');
  assert.equal(t.viewWindows({}), 'No window saved');
});

test('the slowest-calls query reads the incident window, all kinds, and splits the API name', () => {
  assert.equal(f.slowQuery(good(), []), null);
  assert.equal(f.slowQuery(f.emptyFrame(), ['s']), null);
  const q = f.slowQuery({ ...good(), route: 'Payroll.Run.All' }, ['s']);
  assert.equal(q.start, '2026-10-17T10:30:00.000Z');
  assert.equal(q.baselineEnd, '2026-10-17T10:30:00.000Z');
  assert.equal(q.kind, 'all');
  assert.equal(q.controller, 'Payroll');
  assert.equal(q.action, 'Run.All');
  const all = f.slowQuery(good(), ['s']);
  assert.equal(all.controller, '');
  assert.equal(all.action, '');
});
