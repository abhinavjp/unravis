const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

// The call trace inside Nearby activity: its words, its geometry, the tray of chosen calls, the request, and how it is wired in.

const root = path.resolve(__dirname, '..');
const cache = new Map();
function loadTypeScript(relativePath) {
  const filename = path.join(root, relativePath);
  if (cache.has(filename)) return cache.get(filename).exports;
  const source = fs.readFileSync(filename, 'utf8');
  const output = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022, esModuleInterop: true } }).outputText;
  const module = { exports: {} };
  cache.set(filename, module);
  const nodeRequire = createRequire(filename);
  const load = (request) => {
    if (/^\.\.?\//.test(request)) {
      const plain = path.join(path.dirname(filename), request + '.ts');
      const store = path.join(path.dirname(filename), request + '.svelte.ts');
      if (fs.existsSync(plain)) return loadTypeScript(path.relative(root, plain));
      if (fs.existsSync(store)) return loadTypeScript(path.relative(root, store));
    }
    return nodeRequire(request);
  };
  new Function('require', 'module', 'exports', '$state', output)(load, module, module.exports, (value) => value);
  return module.exports;
}
const read = (relative) => fs.readFileSync(path.join(root, relative), 'utf8');

const trace = loadTypeScript('src/lib/callTrace.ts');
const tray = loadTypeScript('src/lib/callTraceTray.svelte.ts');
const copy = loadTypeScript('src/lib/copy/callTraceCopy.ts');
const { lintScreenStrings, fleschKincaidGrade, MAX_GRADE, MIN_WORDS_FOR_GRADE } = loadTypeScript('src/lib/copy/copylint.ts');
const { deck } = loadTypeScript('src/lib/copy/deck.ts');

const T0 = Date.parse('2026-10-24T10:00:00.000Z');
const iso = (offsetMs) => new Date(T0 + offsetMs).toISOString();
const call = (id, extra = {}) => ({
  id, sourceId: 'src-a', controller: 'Orders', action: 'Save', outcome: 'ok', start: iso(0), end: iso(2000), durationMs: 2000, clockAnomaly: false,
  parts: [{ key: 'audit', label: 'Audit write', ms: 200 }, { key: 'api', label: 'API work', ms: 1500 }, { key: 'other', label: 'Other time', ms: 300 }, { key: 'payload', label: 'Payload record', ms: 40 }],
  ...extra
});
const event = (id, from, to, extra = {}) => ({ id, source: 'iis', label: 'slow request', start: iso(from), end: iso(to), durationMs: to - from, ...extra });
const lane = (key, state, events = []) => ({ key, label: key.toUpperCase(), state, sources: [], events, hidden: 0 });
const oneTrace = (id, offset, extra = {}) => ({
  call: call(id, { start: iso(offset), end: iso(offset + 2000) }),
  window: { from: iso(offset - 120000), to: iso(offset + 122000) },
  lanes: [lane('sql', 'events', [event('e1', offset + 500, offset + 900)])],
  checks: [],
  ...extra
});

/* ------------------------------------------------------------------ copy */

test('the call trace words pass the copy lint and the reading grade', () => {
  const entries = copy.callTraceCopyEntries();
  assert.ok(entries.length > 40);
  assert.deepEqual(lintScreenStrings(entries, deck.terms).map((f) => f.id + ': ' + f.message), []);
  for (const entry of entries.filter((e) => e.text.split(' ').length >= MIN_WORDS_FOR_GRADE)) {
    assert.ok(fleschKincaidGrade(entry.text) <= MAX_GRADE, entry.id + ' is grade ' + fleschKincaidGrade(entry.text).toFixed(1) + ': ' + entry.text);
  }
});

test('no word says something caused something else', () => {
  const banned = /\b(?:caus\w*|because|due to|blocked by|waiting on|led to|leads to|result(?:s|ed)? (?:in|from)|responsible|culprit|root)\b/i;
  for (const entry of copy.callTraceCopyEntries()) assert.doesNotMatch(entry.text, banned, entry.id);
  assert.match(copy.CALL_TRACE_TEXT.overlapNote, /does not say why/);
  assert.match(copy.CALL_TRACE_TEXT.splitNote, /no timings for each filter/);
});

test('the four status labels and the lane state texts are exact', () => {
  assert.equal(copy.statusText('consistent'), 'Consistent with this idea');
  assert.equal(copy.statusText('contradicted'), 'The data does not fit this idea');
  assert.equal(copy.statusText('not-enough-data'), 'Not enough data');
  assert.equal(copy.statusText('not-testable'), 'Not testable here');
  assert.equal(copy.LANE_STATE_TEXT['no-data'], 'This case has no file for this source');
  assert.equal(copy.LANE_STATE_TEXT['none-seen'], 'Files cover this time and nothing was seen');
  assert.equal(copy.LANE_STATE_TEXT['clock-unchecked'], 'Time zone not checked, so nothing is drawn');
  assert.equal(copy.LANE_STATE_TEXT['no-case'], 'Open a case to see this source');
  // A status is never told by colour alone: each has its own mark.
  assert.equal(new Set(['consistent', 'contradicted', 'not-enough-data', 'not-testable'].map(copy.statusIcon)).size, 4);
});

test('the case home hint is plain and passes the lint', () => {
  const home = loadTypeScript('src/lib/copy/caseHomeCopy.ts');
  assert.match(home.CASE_HOME_TEXT.analysisHint, /Nearby activity/);
  assert.deepEqual(lintScreenStrings(home.caseHomeCopyEntries(), deck.terms), []);
  assert.match(read('src/lib/components/CaseHome.svelte'), /CASE_HOME_TEXT\.analysisHint/);
});

/* ------------------------------------------------------------------ geometry */

test('position and width follow the axis, and a time outside it is held at the edge', () => {
  const axis = { startMs: 0, endMs: 1000 };
  assert.equal(trace.pctOf(axis, 250), 25);
  assert.equal(trace.pctOf(axis, -5), 0);
  assert.equal(trace.pctOf(axis, 5000), 100);
  assert.equal(trace.pctOf({ startMs: 5, endMs: 5 }, 5), 0, 'an empty axis does not divide by zero');
  const box = trace.spanBox(axis, 100, 300);
  assert.deepEqual([box.left, box.width], [10, 20]);
});

test('a tiny event keeps a minimum visible width and stays inside the axis', () => {
  const axis = { startMs: 0, endMs: 600000 };
  const tiny = trace.spanBox(axis, 1000, 1001);
  assert.equal(tiny.width, trace.MIN_EVENT_PCT);
  const atEnd = trace.spanBox(axis, 600000, 600000);
  assert.ok(atEnd.left + atEnd.width <= 100 + 1e-9);
  assert.equal(trace.spanBox(axis, 700000, 800000), null, 'an event after the axis is not drawn');
  assert.equal(trace.spanBox(axis, -9000, -8000), null, 'an event before the axis is not drawn');
  assert.ok(trace.spanBox(axis, -9000, 5000), 'an event that crosses the edge is drawn');
});

test('overlapping events are stacked in rows, the last row takes the overflow', () => {
  const boxes = [{ left: 0, width: 50 }, { left: 10, width: 10 }, { left: 60, width: 5 }, null, { left: 12, width: 10 }, { left: 14, width: 10 }, { left: 15, width: 10 }];
  const rows = trace.packRows(boxes, 3);
  assert.equal(rows[0], 0);
  assert.equal(rows[1], 1);
  assert.equal(rows[2], 0, 'a later event reuses the first free row');
  assert.ok(Math.max(...rows) <= 2);
});

test('the call is split into audit, API and other; payload is a marker outside the total', () => {
  const parts = call('c1').parts;
  const segments = trace.partSegments(parts);
  assert.deepEqual(segments.map((s) => s.key), ['audit', 'api', 'other']);
  assert.ok(Math.abs(segments.reduce((sum, s) => sum + s.widthPct, 0) - 100) < 1e-9);
  assert.equal(trace.payloadPart(parts).ms, 40);
  assert.deepEqual(trace.partSegments([{ key: 'payload', label: 'p', ms: 5 }]), []);
  assert.equal(trace.payloadPart([]), null);
});

test('aligned mode measures each call from its own start; real clock uses the shared window', () => {
  const result = { traces: [oneTrace('a', 0), oneTrace('b', 600000)], compare: [], sharedWindow: { from: iso(-120000), to: iso(722000) }, truncated: false };
  const aligned = trace.buildAxis('alignedToStart', result);
  assert.deepEqual([aligned.startMs, aligned.endMs], [-120000, 122000]);
  const real = trace.buildAxis('realClock', result);
  assert.deepEqual([real.startMs, real.endMs], [T0 - 120000, T0 + 722000]);
  // The same event sits at the same place for both calls in aligned mode, and far apart in real clock mode.
  const [a, b] = result.traces;
  const ea = trace.eventBox(aligned, 'alignedToStart', a.call, a.lanes[0].events[0]);
  const eb = trace.eventBox(aligned, 'alignedToStart', b.call, b.lanes[0].events[0]);
  assert.equal(ea.left, eb.left);
  const ra = trace.eventBox(real, 'realClock', a.call, a.lanes[0].events[0]);
  const rb = trace.eventBox(real, 'realClock', b.call, b.lanes[0].events[0]);
  assert.ok(rb.left > ra.left + 50);
});

test('real clock is offered with a shared window, or for one call, and not for far apart calls', () => {
  const far = { traces: [oneTrace('a', 0), oneTrace('b', 9 * 3600000)], compare: [], truncated: false };
  assert.equal(trace.realClockOffered(far), false);
  assert.equal(trace.buildAxis('realClock', far), null);
  assert.equal(trace.realClockOffered({ traces: [oneTrace('a', 0)], compare: [], truncated: false }), true);
  assert.equal(trace.realClockOffered({ ...far, sharedWindow: { from: iso(0), to: iso(1) } }), true);
  assert.notEqual(trace.buildAxis('alignedToStart', far), null);
  assert.equal(trace.buildAxis('alignedToStart', { traces: [], compare: [], truncated: false }), null);
});

test('tick labels: offsets when aligned, times of day when real, spread across the axis', () => {
  const axis = { startMs: -120000, endMs: 120000 };
  const aligned = trace.tickMarks(axis, 'alignedToStart', 'UTC');
  assert.equal(aligned.length, 5);
  assert.deepEqual(aligned.map((t) => t.pct), [0, 25, 50, 75, 100]);
  assert.equal(aligned[0].label, '-2 min');
  assert.equal(aligned[2].label, '0 min', 'one unit for the whole axis, zero included');
  assert.equal(aligned[4].label, '+2 min');
  const real = trace.tickMarks({ startMs: T0, endMs: T0 + 60000 }, 'realClock', 'UTC');
  assert.match(real[0].label, /10:00:00/);
  assert.match(real[4].label, /10:01:00/);
});

test('a call with no usable end is a thin start mark, and callBox and startPct place it', () => {
  const axis = { startMs: -1000, endMs: 3000 };
  const done = trace.callBox(axis, 'alignedToStart', call('c'));
  assert.deepEqual([done.left, done.width], [25, 50]);
  const open = trace.callBox(axis, 'alignedToStart', call('c', { end: null, durationMs: null }));
  assert.equal(open.width, trace.MIN_EVENT_PCT);
  const odd = trace.callBox(axis, 'alignedToStart', call('c', { clockAnomaly: true, end: iso(-5) }));
  assert.equal(odd.width, trace.MIN_EVENT_PCT);
  assert.equal(trace.startPct(axis, 'alignedToStart', call('c')), 25);
});

test('a check names its calls by number', () => {
  const traces = [oneTrace('a', 0), oneTrace('b', 1000), oneTrace('c', 2000)];
  assert.deepEqual(trace.callNumbers({ callIds: ['c', 'a'] }, traces), [1, 3]);
  assert.equal(copy.callsAre([1, 3]), 'Calls 1 and 3');
  assert.equal(copy.callsAre([2]), 'Call 2');
});

/* ------------------------------------------------------------------ the tray */

const entry = (id, extra = {}) => ({ sourceId: 'src-a', id, label: 'Orders.Save ' + id, ...extra });

test('the tray holds at most three calls, refuses a fourth and says why', () => {
  tray.clearTray();
  for (const id of ['1', '2', '3']) assert.equal(tray.addToTray(entry(id)), true);
  assert.equal(tray.trayIsFull(), true);
  assert.equal(tray.addToTray(entry('4')), false);
  assert.deepEqual(tray.tray.calls.map((c) => c.id), ['1', '2', '3']);
  assert.equal(tray.tray.notice, copy.CALL_TRACE_TEXT.trayFull);
  tray.removeFromTray(entry('2'));
  assert.equal(tray.tray.notice, '');
  assert.equal(tray.addToTray(entry('4')), true);
  tray.clearTray();
  assert.deepEqual(tray.tray.calls, []);
});

test('the tray keeps one copy of a call, and one call id in two sources are two calls', () => {
  tray.clearTray();
  assert.equal(tray.addToTray(entry('1')), true);
  assert.equal(tray.addToTray(entry('1', { label: 'again' })), false);
  assert.equal(tray.tray.notice, copy.CALL_TRACE_TEXT.trayDuplicate);
  assert.equal(tray.addToTray(entry('1', { sourceId: 'src-b' })), true);
  assert.equal(tray.tray.calls.length, 2);
  assert.equal(tray.inTray({ sourceId: 'src-b', id: '1' }), true);
  tray.clearTray();
});

test('a row with no id or source cannot be added', () => {
  tray.clearTray();
  assert.equal(tray.addToTray(entry('')), false);
  assert.equal(tray.addToTray(entry('9', { sourceId: '' })), false);
  assert.equal(tray.tray.notice, copy.CALL_TRACE_TEXT.trayNoId);
  assert.equal(tray.tray.calls.length, 0);
});

test('the tray is emptied when another case opens, not when the same case is named again', () => {
  tray.clearTray();
  tray.syncTrayCase('case-1');
  tray.addToTray(entry('1'));
  tray.syncTrayCase('case-1');
  assert.equal(tray.tray.calls.length, 1, 'the same case keeps the calls');
  tray.syncTrayCase('case-2');
  assert.equal(tray.tray.calls.length, 0, 'another case empties the tray');
  tray.addToTray(entry('1'));
  tray.syncTrayCase('');
  assert.equal(tray.tray.calls.length, 0, 'leaving for the demo empties it too');
});

/* ------------------------------------------------------------------ the request */

function mockServer(handler) {
  const calls = [];
  globalThis.sessionStorage = { getItem: () => 'session-token-1', setItem() {}, removeItem() {} };
  globalThis.fetch = async (url, init = {}) => {
    calls.push({ url, init, headers: new Headers(init.headers) });
    const out = handler(url, init);
    return { ok: (out.status ?? 200) < 400, status: out.status ?? 200, json: async () => out.body };
  };
  return calls;
}

// api.ts reads the session token when it loads, so it is loaded after the mock storage exists.
function freshTrace() {
  cache.clear();
  globalThis.sessionStorage = { getItem: () => 'session-token-1', setItem() {}, removeItem() {} };
  return loadTypeScript('src/lib/callTrace.ts');
}

test('fetchCallTrace posts to the case route when a case is open and to the plain route when not', async () => {
  const api = freshTrace();
  const answer = { traces: [{ call: call('a'), window: { from: iso(0), to: iso(1) }, lanes: null, checks: null }], compare: null, truncated: false };
  let calls = mockServer(() => ({ body: answer }));
  const refs = [{ sourceId: 'src-a', id: 'a', label: 'ignored' }];
  const out = await api.fetchCallTrace(refs, 2, 2, '');
  assert.equal(calls[0].url, '/api/call-trace');
  assert.equal(calls[0].init.method, 'POST');
  assert.equal(calls[0].headers.get('X-Audit-Token'), 'session-token-1');
  assert.deepEqual(JSON.parse(calls[0].init.body), { calls: [{ sourceId: 'src-a', id: 'a' }], beforeMin: 2, afterMin: 2 });
  assert.deepEqual(out.traces[0].lanes, [], 'a null list becomes an empty one');
  assert.deepEqual(out.compare, []);
  assert.deepEqual(out.traces[0].checks, []);
  calls = mockServer(() => ({ body: answer }));
  await api.fetchCallTrace(refs, 5, 99, 'abc 1');
  assert.equal(calls[0].url, '/api/cases/abc%201/call-trace');
  assert.deepEqual([JSON.parse(calls[0].init.body).beforeMin, JSON.parse(calls[0].init.body).afterMin], [5, 30], 'the margin is held to 30 minutes');
});

test('fetchCallTrace refuses no calls and four calls before any request, and an unreadable answer', async () => {
  const api = freshTrace();
  const calls = mockServer(() => ({ body: { traces: [] } }));
  await assert.rejects(api.fetchCallTrace([], 2, 2, ''));
  await assert.rejects(api.fetchCallTrace([1, 2, 3, 4].map((n) => ({ sourceId: 's', id: String(n) })), 2, 2, ''));
  assert.equal(calls.length, 0);
  mockServer(() => ({ body: { nope: true } }));
  await assert.rejects(api.fetchCallTrace([{ sourceId: 's', id: '1' }], 2, 2, ''), /unreadable/);
});

test('fetchCallTrace keeps the server message on an error and passes the abort signal on', async () => {
  const api = freshTrace();
  mockServer(() => ({ status: 404, body: { error: 'That call was not found.' } }));
  await assert.rejects(api.fetchCallTrace([{ sourceId: 's', id: '1' }], 2, 2, ''), (error) => error.message === 'That call was not found.' && error.status === 404);
  const controller = new AbortController();
  globalThis.fetch = async (_url, init = {}) => {
    assert.equal(init.signal, controller.signal);
    throw new DOMException('Aborted', 'AbortError');
  };
  await assert.rejects(api.fetchCallTrace([{ sourceId: 's', id: '1' }], 2, 2, '', controller.signal), (error) => error.name === 'AbortError');
});

/* ------------------------------------------------------------------ wiring */

test('Nearby activity mounts the call trace and leaves the drawer alone', () => {
  const activity = read('src/components/ActivityContext.svelte');
  assert.match(activity, /import CallTrace from '\.\/CallTrace\.svelte'/);
  assert.match(activity, /<CallTrace \{zone\} onclose=\{closeTrace\} \/>/);
  assert.match(activity, /id="nearby-activity"/);
  assert.doesNotMatch(activity, /CaseTools|openSidePanel|panels\.sidePanel/);
  assert.doesNotMatch(read('src/components/CallTrace.svelte'), /CaseTools|openSidePanel|panels\.sidePanel/);
  const drawer = fs.readdirSync(path.join(root, 'src')).length ? read('src/components/ActivityContext.svelte') : '';
  assert.ok(drawer.length > 0);
});

test('the chosen calls are not reset by the anchor effect', () => {
  const activity = read('src/components/ActivityContext.svelte');
  const anchorEffect = activity.match(/\$effect\(\(\)=>\{ const current=anchor;[^\n]*/)[0];
  assert.doesNotMatch(anchorEffect, /tray|clearTray|syncTrayCase/);
  assert.match(activity, /syncTrayCase\(id\)/);
  assert.match(activity, /clearTray/);
});

test('each nearby row can be added to compare, and a refused add shows its reason', () => {
  const activity = read('src/components/ActivityContext.svelte');
  assert.match(activity, /TRACE_TEXT\.addCompare/);
  assert.match(activity, /compareReason\(row\)/);
  assert.match(activity, /trayNoId/);
  assert.match(activity, /trayFull/);
  assert.match(activity, /trayMessage\(\)/);
});

test('the call trace guards against late answers and stops when the panel goes', () => {
  const source = read('src/components/CallTrace.svelte');
  assert.match(source, /sequence/);
  assert.match(source, /AbortController/);
  assert.match(source, /onMount\(\(\) => \(\) => \{[^}]*abort\(\)/);
  assert.match(source, /prefers-reduced-motion/);
  assert.match(source, /formatTime/);
  assert.match(source, /<details class="ct-list">/);
  assert.match(source, /T\.modeReal/);
  assert.match(source, /T\.modeAligned/);
});
