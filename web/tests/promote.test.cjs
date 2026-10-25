const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

const root = path.resolve(__dirname, '..');

// Load a plain .ts module (and the .ts modules it imports).
function load(relativePath) {
  const filename = path.join(root, relativePath);
  const source = fs.readFileSync(filename, 'utf8');
  const js = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022, esModuleInterop: true } }).outputText;
  const module = { exports: {} };
  const nodeRequire = createRequire(filename);
  const need = (request) => {
    const base = path.join(path.dirname(filename), request);
    if (/^\.\.?\//.test(request) && fs.existsSync(base + '.ts')) return load(path.relative(root, base + '.ts'));
    return nodeRequire(request);
  };
  new Function('require', 'module', 'exports', js)(need, module, module.exports);
  return module.exports;
}

const p = load('src/lib/promote.ts');
const { bucketForWindows } = load('src/lib/time.ts');
const { zoomCopy, allZoomStrings } = load('src/lib/copy/zoomCopy.ts');
const { MIN_VIEW_MS } = load('src/lib/zoom.ts');

const SEC = 1000, MIN = 60_000, HOUR = 3_600_000, DAY = 86_400_000;
const at = (text) => Date.parse(text);
const iso = (ms) => new Date(ms).toISOString();
const t0 = at('2001-10-22T00:00:00Z');
// Synthetic week, 7200 s automatic interval.
const current = { start: '2001-10-22T00:00:00.000Z', end: '2001-10-29T00:00:00.000Z', baselineStart: '2001-10-15T00:00:00.000Z', baselineEnd: '2001-10-22T00:00:00.000Z', bucketSeconds: 7200 };
const view = (startMs, endMs) => ({ start: startMs, end: endMs });
const oct24 = (h, m = 0) => at('2001-10-24T00:00:00Z') + h * HOUR + m * MIN;
const hour14 = view(oct24(14), oct24(15));

test('promotedScope sets the window, the reference before it and the app interval', () => {
  const out = p.promotedScope(current, hour14);
  assert.equal(out.start, '2001-10-24T14:00:00.000Z');
  assert.equal(out.end, '2001-10-24T15:00:00.000Z');
  assert.equal(out.baselineStart, '2001-10-24T13:00:00.000Z');
  assert.equal(out.baselineEnd, '2001-10-24T14:00:00.000Z');
  assert.equal(out.bucketSeconds, bucketForWindows(out.start, out.end, out.baselineStart, out.baselineEnd));
  assert.equal(out.bucketSeconds, 30);
});

test('promotedScope rounds outward to whole seconds and stays inside the current window', () => {
  const t = t0 + 2 * DAY;
  const out = p.promotedScope(current, view(t + 400_600, t + 4_000_200));
  const s = Date.parse(out.start), e = Date.parse(out.end);
  assert.equal(s % SEC, 0); assert.equal(e % SEC, 0);
  assert.ok(s <= t + 400_600 && e >= t + 4_000_200);
  assert.equal(s, t + 400_000); assert.equal(e, t + 4_001_000);
  assert.ok(e - s >= MIN_VIEW_MS);
  // The reference is whole-second too.
  assert.equal(Date.parse(out.baselineStart) % SEC, 0);
  // A view hanging 5 s past the current end is clipped to it.
  const edge = at(current.end);
  const clipped = p.promotedScope(current, view(edge - HOUR, edge + 5 * SEC));
  assert.equal(clipped.end, current.end);
  assert.equal(clipped.start, iso(edge - HOUR));
  // A current window with millisecond edges still gives whole seconds inside it.
  const rough = { ...current, start: '2001-10-22T00:00:00.400Z', end: '2001-10-29T00:00:00.700Z' };
  const inner = p.promotedScope(rough, view(at(rough.start), at(rough.end) - HOUR));
  assert.equal(Date.parse(inner.start) % SEC, 0);
  assert.ok(Date.parse(inner.start) >= at(rough.start));
});

test('promotedScope rejects a window that is outside the current one instead of clipping it', () => {
  // No overlap at all, and an overlap that leaves under a minute, are refused, never shrunk to fit.
  const edge = at(current.end);
  assert.equal(p.promotedScope(current, view(edge + HOUR, edge + 2 * HOUR)), null);
  assert.equal(p.promotedScope(current, view(t0 - 2 * HOUR, t0 - HOUR)), null);
  assert.equal(p.promotedScope(current, view(edge - 20 * SEC, edge + 2 * HOUR)), null);
  // A window wider than 7 days is never produced.
  const wide = { ...current, start: iso(t0 - DAY), end: iso(t0 + 8 * DAY) };
  assert.equal(p.promotedScope(wide, view(t0 - DAY, t0 + 8 * DAY - HOUR)), null);
});

test('promotedScope refuses a window under a minute, an unreadable one, or the current one', () => {
  assert.equal(p.promotedScope(current, view(oct24(14), oct24(14) + 30 * SEC)), null);
  assert.equal(p.promotedScope(current, view(oct24(14), oct24(14) + MIN - SEC)), null);
  // Rounding outward may bring 59.9 seconds up to a whole minute; that is a window of a minute, which is allowed.
  assert.notEqual(p.promotedScope(current, view(oct24(14), oct24(14) + MIN - 1)), null);
  assert.notEqual(p.promotedScope(current, view(oct24(14), oct24(14) + MIN)), null);
  assert.equal(p.promotedScope(current, { start: NaN, end: oct24(15) }), null);
  assert.equal(p.promotedScope(current, { start: oct24(15), end: oct24(14) }), null);
  assert.equal(p.promotedScope(current, null), null);
  assert.equal(p.promotedScope(current, view(at(current.start), at(current.end))), null);
  assert.equal(p.promotedScope({ ...current, start: 'nope' }, hour14), null);
});

test('promotedScope drops a manual interval that no longer fits (Review Focus 3)', () => {
  const manual = (seconds) => ({ ...current, bucketSeconds: seconds });
  const half = view(oct24(14), oct24(14, 30));
  assert.equal(p.promotedScope(manual(3600), half).bucketSeconds, 15);
  assert.equal(p.promotedScope(manual(60), hour14).bucketSeconds, 60);
  // (30 minutes against a 30-minute reference: the app rule is 15 seconds.)
  // The automatic 7200 on a 1-hour view gives 30, not 7200.
  assert.equal(p.promotedScope(current, hour14).bucketSeconds, 30);
  // Manual and small, but it would give more than 120 points: back to the app rule.
  assert.equal(p.promotedScope(manual(10), view(oct24(0), oct24(6))).bucketSeconds, bucketForWindows(iso(oct24(0)), iso(oct24(6)), iso(oct24(0) - 6 * HOUR), iso(oct24(0))));
  // Manual and not smaller than the new width: app rule.
  assert.equal(p.promotedScope(manual(3600), hour14).bucketSeconds, 30);
});

test('investigateState gives the first reason in order', () => {
  assert.deepEqual(p.investigateState({ isRoot: true, runLoading: true, stale: true }), { enabled: false, reason: zoomCopy.investigateTop });
  assert.deepEqual(p.investigateState({ isRoot: false, runLoading: true, stale: true }), { enabled: false, reason: zoomCopy.investigateBusy });
  assert.deepEqual(p.investigateState({ isRoot: false, runLoading: false, stale: true }), { enabled: false, reason: zoomCopy.investigateStale });
  assert.deepEqual(p.investigateState({ isRoot: false, runLoading: false, stale: false }), { enabled: true, reason: '' });
});

test('planInvestigation refuses while a run is loading (Review Focus 1)', () => {
  const trail = [];
  const busy = p.planInvestigation({ current, view: hour14, runLoading: true, stale: false, trail });
  assert.deepEqual(busy, { refused: zoomCopy.investigateBusy });
  assert.equal(trail.length, 0);
  // A double click: the first plans, the second sees the run the first started.
  const first = p.planInvestigation({ current, view: hour14, runLoading: false, stale: false, trail });
  assert.ok(!('refused' in first));
  const second = p.planInvestigation({ current, view: hour14, runLoading: true, stale: false, trail: first.trail });
  assert.ok('refused' in second);
  assert.equal(first.trail.length, 1);
  assert.equal(trail.length, 0, 'the input trail is never mutated');
});

test('planInvestigation refuses at the root, while stale, and for a window that cannot be promoted', () => {
  const plan = (over) => p.planInvestigation({ current, view: hour14, runLoading: false, stale: false, trail: [], ...over });
  assert.deepEqual(plan({ view: null }), { refused: zoomCopy.investigateTop });
  assert.deepEqual(plan({ stale: true }), { refused: zoomCopy.investigateStale });
  assert.deepEqual(plan({ view: view(oct24(14), oct24(14) + 30 * SEC) }), { refused: zoomCopy.investigateCannot });
  assert.deepEqual(plan({ view: view(at(current.start), at(current.end)) }), { refused: zoomCopy.investigateCannot });
});

test('planInvestigation records the way back: the old window, the new one and the lens', () => {
  const plan = p.planInvestigation({ current, view: hour14, runLoading: false, stale: false, trail: [] });
  assert.equal(plan.trail.length, 1);
  const entry = plan.trail[0];
  assert.deepEqual(entry.from, current);
  assert.deepEqual(entry.to, { start: plan.patch.start, end: plan.patch.end });
  assert.deepEqual(entry.lens, hour14);
  assert.notEqual(entry.from, current, 'the entry owns a copy');
});

test('the trail caps at 10, pops in order, and backTarget hides after a manual edit', () => {
  let trail = [];
  const entries = [];
  for (let i = 0; i < 12; i++) {
    const entry = { from: { ...current, bucketSeconds: 7200 + i }, to: { start: iso(t0 + i * HOUR), end: iso(t0 + (i + 1) * HOUR) }, lens: view(t0, t0 + HOUR) };
    entries.push(entry);
    trail = p.pushTrail(trail, entry);
  }
  assert.equal(p.TRAIL_LIMIT, 10);
  assert.equal(trail.length, 10);
  assert.equal(trail[0], entries[2]);
  assert.equal(trail[9], entries[11]);
  const popped = p.popTrail(trail);
  assert.equal(popped.entry, entries[11]);
  assert.equal(popped.trail.length, 9);
  assert.equal(trail.length, 10, 'popTrail does not mutate');
  assert.deepEqual(p.popTrail([]), { entry: null, trail: [] });
  const last = entries[11];
  assert.equal(p.backTarget(trail, last.to), last);
  assert.equal(p.backTarget(trail, { start: last.to.start, end: iso(at(last.to.end) + MIN) }), null);
  assert.equal(p.backTarget(trail, { start: iso(at(last.to.start) - MIN), end: last.to.end }), null);
  assert.equal(p.backTarget([], last.to), null);
  const seven = { from: current, to: last.to, lens: hour14 };
  assert.equal(p.backLabel(seven), 'Back to 7 days');
});

test('promote then back then promote again keeps the trail consistent', () => {
  let trail = [];
  const a = p.planInvestigation({ current, view: view(oct24(10), oct24(22)), runLoading: false, stale: false, trail });
  trail = a.trail;
  assert.equal(p.backTarget(trail, a.patch).from.end, current.end);
  const b = p.planInvestigation({ current: a.patch, view: hour14, runLoading: false, stale: false, trail });
  trail = b.trail;
  assert.equal(trail.length, 2);
  assert.equal(p.backLabel(p.backTarget(trail, b.patch)), 'Back to 12 hours');
  // Back from b restores a's window and the lens that was investigated, and the label now names a's parent.
  const popped = p.popTrail(trail);
  assert.deepEqual(popped.entry.from, a.patch);
  assert.deepEqual(popped.entry.lens, hour14);
  trail = popped.trail;
  assert.equal(p.backLabel(p.backTarget(trail, a.patch)), 'Back to 7 days');
  assert.equal(p.backTarget(trail, b.patch), null);
});

test('back restores the previous query window and the lens', () => {
  const plan = p.planInvestigation({ current, view: hour14, runLoading: false, stale: false, trail: [] });
  const { entry } = p.popTrail(plan.trail);
  assert.deepEqual(entry.from, current);
  assert.deepEqual(p.rootOf(entry.from), { start: at(current.start), end: at(current.end) });
  assert.deepEqual(entry.lens, hour14);
});

test('a promoted window keeps every edge whole-second ISO and passes scopeProblem', () => {
  const { scopeProblem } = load('src/lib/autorun.ts');
  const t = t0 + 3 * DAY;
  const out = p.promotedScope(current, view(t + 12_345, t + 2 * HOUR + 987));
  for (const key of ['start', 'end', 'baselineStart', 'baselineEnd']) assert.match(out[key], /^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d\.000Z$/, key);
  assert.equal(scopeProblem({ ...out, sourceIds: ['s1'] }, true), null);
});

test('every new string is in the copy lint list', () => {
  assert.ok(allZoomStrings().includes(zoomCopy.investigateCannot));
});

const source = fs.readFileSync(path.join(root, 'src/lib/appState.svelte.ts'), 'utf8');
const body = (name) => {
  const from = source.search(new RegExp('export (async )?function ' + name + '\\b'));
  assert.ok(from >= 0, name + ' exists');
  const rest = source.slice(from + 10);
  const next = rest.search(/\nexport (async )?function |\n\/\/ /);
  return source.slice(from, from + 10 + (next < 0 ? rest.length : next));
};

test('appState: investigateWindow plans before it writes, then reruns once', () => {
  const text = body('investigateWindow');
  assert.ok(text.indexOf('planInvestigation(') >= 0);
  assert.ok(text.indexOf('planInvestigation(') < text.indexOf('Object.assign(query'));
  assert.ok(text.indexOf('windowTrail = ') > text.indexOf('planInvestigation('), 'the trail is written only after the plan is accepted');
  assert.ok(text.indexOf('Object.assign(query') < text.indexOf('controlsResetKey++'));
  assert.ok(text.indexOf('controlsResetKey++') < text.indexOf('runAnalysis('));
  assert.doesNotMatch(text, /onScopeChange\(/);
  assert.match(text, /analysis\.query/, 'the current window comes from the analysis on screen');
  assert.match(text, /zoomView\.view/);
  assert.match(text, /notify\(/, 'a refusal is shown in the strip');
});

test('appState: runAnalysis sets loading before its first await', () => {
  const text = body('runAnalysis');
  assert.ok(text.search(/loading = true/) >= 0);
  assert.ok(text.search(/loading = true/) < text.search(/await /));
});

test('appState: resetInvestigation clears the trail and Back reopens the lens', () => {
  assert.match(body('resetInvestigation'), /windowTrail = \[\]/);
  const back = body('backToPreviousWindow');
  assert.match(back, /zoomView\.expect\(/);
  assert.match(back, /popTrail\(/);
  assert.ok(back.indexOf('Object.assign(query') < back.indexOf('controlsResetKey++'));
  assert.doesNotMatch(back, /onScopeChange\(/);
  assert.match(source, /get windowBack\(\) \{ return backTarget\(windowTrail, query\)/);
});
