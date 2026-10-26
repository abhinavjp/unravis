const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

// Clumsy-user review of the call trace: odd numbers, very long calls, missing ends, and wording.

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

const trace = loadTypeScript('src/lib/callTrace.ts');
const copy = loadTypeScript('src/lib/copy/callTraceCopy.ts');

const T0 = Date.parse('2026-10-24T10:00:00.000Z');
const iso = (offsetMs) => new Date(T0 + offsetMs).toISOString();
const call = (id, extra = {}) => ({
  id, sourceId: 'src-a', controller: 'Orders', action: 'Save', outcome: 'ok', start: iso(0), end: iso(2000), durationMs: 2000, clockAnomaly: false,
  parts: [{ key: 'audit', label: 'Audit write', ms: 200 }, { key: 'api', label: 'API work', ms: 1500 }, { key: 'other', label: 'Other time', ms: 300 }],
  ...extra
});
const oneTrace = (c, win) => ({ call: c, window: win ?? { from: iso(-120000), to: iso(122000) }, lanes: [], checks: [] });
const inside = (box) => box && box.left >= 0 && box.width > 0 && box.left + box.width <= 100 + 1e-9;

/* ---------------------------------------------------------------- wording */

test('callsAre: one and two calls read plainly', () => {
  assert.equal(copy.callsAre([1]), 'Call 1');
  assert.equal(copy.callsAre([1, 2]), 'Calls 1 and 2');
});

test('callsAre: three calls read as a list, not "1 and 2 and 3"', () => {
  assert.equal(copy.callsAre([1, 2, 3]), 'Calls 1, 2 and 3');
  assert.equal(copy.callsAre([]), '');
});

test('the error text does not say "Try again" twice', () => {
  assert.doesNotMatch(copy.CALL_TRACE_TEXT.failed, /try again/i);
  assert.equal(copy.CALL_TRACE_TEXT.retry, 'Try again');
});

/* --------------------------------------------------------------- geometry */

test('a call with no end time draws a thin start mark that stays inside the axis', () => {
  const t = oneTrace(call('a', { end: null, durationMs: null }));
  const axis = trace.buildAxis('aligned', { traces: [t], sharedWindow: null });
  const box = trace.callBox(axis, 'aligned', t.call);
  assert.ok(inside(box), JSON.stringify(box));
  assert.ok(box.width <= 1.5, 'a missing end must not look like a long call');
});

test('a call flagged with a clock problem ignores its end', () => {
  const t = oneTrace(call('a', { end: iso(-5000), clockAnomaly: true }));
  const axis = trace.buildAxis('aligned', { traces: [t], sharedWindow: null });
  assert.ok(inside(trace.callBox(axis, 'aligned', t.call)));
});

test('a call that lasts a day is clipped to the axis, not drawn past it', () => {
  const day = 86_400_000;
  const t = oneTrace(call('a', { end: iso(day), durationMs: day }));
  const axis = trace.buildAxis('aligned', { traces: [t], sharedWindow: null });
  const box = trace.callBox(axis, 'aligned', t.call);
  assert.ok(inside(box), JSON.stringify(box));
});

test('spanBox survives NaN, Infinity and reversed times', () => {
  const axis = { startMs: -1000, endMs: 1000 };
  assert.equal(trace.spanBox(axis, NaN, 5), null);
  assert.equal(trace.spanBox(axis, Infinity, Infinity), null);
  assert.ok(inside(trace.spanBox(axis, 500, 100)), 'reversed span falls back to a mark');
  assert.ok(inside(trace.spanBox(axis, 0, NaN)));
  assert.equal(trace.spanBox(axis, 5000, 6000), null);
});

test('buildAxis refuses unparseable dates instead of drawing NaN', () => {
  const t = oneTrace(call('a'), { from: 'not a date', to: 'also not' });
  assert.equal(trace.buildAxis('aligned', { traces: [t], sharedWindow: null }), null);
  assert.equal(trace.buildAxis('realClock', { traces: [t], sharedWindow: null }), null);
});

test('a zero-length window still gets a usable axis', () => {
  const t = oneTrace(call('a'), { from: iso(0), to: iso(0) });
  const axis = trace.buildAxis('realClock', { traces: [t], sharedWindow: null });
  assert.ok(axis && axis.endMs > axis.startMs);
});

test('partSegments ignores zero, negative and NaN parts and always sums to 100', () => {
  const segments = trace.partSegments([
    { key: 'audit', label: 'a', ms: 0 }, { key: 'api', label: 'b', ms: -4 }, { key: 'other', label: 'c', ms: NaN },
    { key: 'payload', label: 'p', ms: 50 }
  ]);
  assert.deepEqual(segments, []);
  const real = trace.partSegments([{ key: 'audit', label: 'a', ms: 1 }, { key: 'api', label: 'b', ms: 2_000_000_000 }]);
  assert.ok(Math.abs(real.reduce((sum, s) => sum + s.widthPct, 0) - 100) < 1e-9);
});

test('offsetLabel reads huge and tiny offsets without scientific notation or "NaN"', () => {
  for (const value of [0, 1, -1, 59_900, -59_900, 86_400_000, 1e12, 0.4]) {
    const label = trace.offsetLabel(value);
    assert.ok(!/NaN|e\+|Infinity/.test(label), value + ' -> ' + label);
  }
});

test('tick labels are evenly spaced percentages even on a tiny axis', () => {
  const ticks = trace.tickMarks({ startMs: -500, endMs: 500 }, 'aligned', 'UTC');
  assert.deepEqual(ticks.map((t) => t.pct), [0, 25, 50, 75, 100]);
  assert.equal(new Set(ticks.map((t) => t.label)).size, ticks.length, 'no two ticks share a label');
});

test('tick labels on a one-minute axis use one unit', () => {
  const ticks = trace.tickMarks({ startMs: -59_900, endMs: 60_130 }, 'aligned', 'UTC');
  const units = new Set(ticks.map((t) => (t.label.match(/(ms|s|min|h)$/) || [])[1]));
  assert.equal(units.size, 1, ticks.map((t) => t.label).join(' | '));
});

test('every axis size gets one unit, short numbers and no repeated labels', () => {
  for (const [from, to] of [[-500, 500], [-1500, 2500], [-2000, 3000], [-120_000, 122_000], [-1_800_000, 1_900_000], [-7_200_000, 8_000_000], [-100, 12_000_000]]) {
    const labels = trace.tickMarks({ startMs: from, endMs: to }, 'aligned', 'UTC').map((t) => t.label);
    assert.equal(new Set(labels.map((l) => l.split(' ')[1])).size, 1, labels.join(' | '));
    for (const label of labels) assert.match(label, /^[-+]?\d+(\.\d{1,2})? (ms|s|min|h)$/, label);
    assert.equal(new Set(labels).size, labels.length, labels.join(' | '));
  }
});

test('packRows never returns a row beyond the limit, however many events overlap', () => {
  const boxes = Array.from({ length: 200 }, () => ({ left: 10, width: 5 }));
  const rows = trace.packRows(boxes, 3);
  assert.ok(Math.max(...rows) <= 2);
});

test('callNumbers ignores ids that are not in the traces', () => {
  const traces = [oneTrace(call('a')), oneTrace(call('b'))];
  assert.deepEqual(trace.callNumbers({ callIds: ['zzz'] }, traces), []);
  assert.deepEqual(trace.callNumbers({ callIds: ['b', 'a'] }, traces), [1, 2]);
});

/* ----------------------------------------------- lane states, tray notices, focus and layout (fix batch) */

const read = (relative) => fs.readFileSync(path.join(root, relative), 'utf8');
const tray = loadTypeScript('src/lib/callTraceTray.svelte.ts');
const { lintScreenStrings } = loadTypeScript('src/lib/copy/copylint.ts');
const { deck } = loadTypeScript('src/lib/copy/deck.ts');
const entry = (id) => ({ sourceId: 'src-a', id, label: 'Orders.Save ' + id });

test('every lane state the server can send has words, and the new two read as asked', () => {
  const types = fs.readFileSync(path.resolve(root, '..', 'internal', 'calltrace', 'types.go'), 'utf8');
  const states = [...types.matchAll(/State\w+\s*=\s*"([^"]+)"/g)].map((m) => m[1]);
  assert.ok(states.includes('incomplete') && states.includes('out-of-range'));
  const source = read('src/lib/callTrace.ts');
  for (const state of states.filter((s) => s !== 'events')) {
    assert.ok(copy.LANE_STATE_TEXT[state], 'no words for lane state ' + state);
    assert.ok(source.includes("'" + state + "'"), 'LaneState type lacks ' + state);
  }
  assert.ok(copy.LANE_STATE_TEXT.incomplete.length > 0);
  assert.ok(copy.LANE_STATE_TEXT['out-of-range'].length > 0);
  assert.deepEqual(lintScreenStrings(copy.callTraceCopyEntries(), deck.terms).map((f) => f.id + ': ' + f.message), []);
});

test('a lane in a new state draws no events and shows its words, like an unchecked lane', () => {
  const source = read('src/components/CallTrace.svelte');
  assert.match(source, /\{#if lane\.state !== 'events'\}<span class="ct-state">\{LANE_STATE_TEXT\[lane\.state\]/);
  assert.match(source, /\{#if lane\.state === 'events'\}\s*<div class="ct-track ct-events"/);
  assert.match(source, /<caption>\{lane\.label\}\{lane\.state !== 'events' \? ': ' \+ \(LANE_STATE_TEXT\[lane\.state\]/);
});

test('the chart scroller has a name and a role, and the legend and note sit outside it', () => {
  const source = read('src/components/CallTrace.svelte');
  assert.match(source, /<div class="ct-scroll" role="region" aria-label=\{`[^`]*T\.chartLabel/);
  assert.ok(copy.CALL_TRACE_TEXT.chartLabel.length > 0);
  const scroll = source.slice(source.indexOf('<div class="ct-scroll"'), source.indexOf('<p class="ct-readout"'));
  const keyAt = scroll.indexOf('<div class="ct-key">');
  assert.ok(keyAt > 0, 'the key block follows the scroller');
  assert.doesNotMatch(scroll.slice(0, keyAt), /ct-legend|T\.payloadNote/, 'legend and note must not sit inside the scroller');
  assert.match(scroll.slice(keyAt), /ct-legend[\s\S]*T\.payloadNote/);
});

test('the trace panel can be closed, and the page hides it when the anchor changes but keeps the chosen calls', () => {
  assert.equal(copy.CALL_TRACE_TEXT.close, 'Close trace');
  assert.match(read('src/components/CallTrace.svelte'), /onclick=\{onclose\}>\{T\.close\}/);
  const activity = read('src/components/ActivityContext.svelte');
  assert.match(activity, /\{#if traceOpen&&tray\.calls\.length\}<CallTrace \{zone\} onclose=\{closeTrace\} \/>\{\/if\}/);
  const anchorHide = activity.match(/\$effect\(\(\)=>\{ const key=anchor\.sourceId[^\n]*/)[0];
  assert.match(anchorHide, /traceOpen=false/);
  assert.doesNotMatch(anchorHide, /clearTray|syncTrayCase|removeFromTray/, 'the chosen calls stay');
  assert.match(activity, /\$effect\(\(\)=>\{ if\(!tray\.calls\.length\) traceOpen=false; \}\);/);
  assert.match(read('src/lib/callTraceTray.svelte.ts'), /export const tray = \$state/, 'the tray stays a module store');
  // The module store keeps the calls whatever the page does.
  tray.clearTray();
  tray.addToTray(entry('1'));
  assert.equal(tray.tray.calls.length, 1);
  tray.clearTray();
});

test('focus lands on a control that still exists after Clear, Close and removing the last chip', () => {
  const activity = read('src/components/ActivityContext.svelte');
  assert.match(activity, /id="call-trace-this"/);
  assert.match(activity, /id="call-trace-compare"/);
  assert.match(activity, /async function closeTrace\(\)\{traceOpen=false;await tick\(\);\(document\.getElementById\('call-trace-compare'\)\?\?document\.getElementById\('call-trace-this'\)\)\?\.focus\(\);\}/);
  assert.match(activity, /async function clearCalls\(\)\{clearTray\(\);await closeTrace\(\);\}/);
  assert.match(activity, /onclick=\{clearCalls\}/);
  const panel = read('src/components/CallTrace.svelte');
  assert.match(panel, /removeFromTray\(call\);\s*if \(!tray\.calls\.length\) \{ onclose\(\); return; \}\s*await tick\(\);\s*document\.getElementById\('call-trace-heading'\)\?\.focus\(\)/);
  assert.match(panel, /void removeCall\(call\)/);
});

test('a full tray says why once, near the tray', () => {
  tray.clearTray();
  assert.equal(tray.trayMessage(), '');
  for (const id of ['1', '2']) tray.addToTray(entry(id));
  assert.equal(tray.trayMessage(), '', 'no hint while there is room');
  tray.addToTray(entry('3'));
  assert.equal(tray.trayMessage(), copy.CALL_TRACE_TEXT.trayFull, 'a full tray explains itself');
  tray.removeFromTray(entry('3'));
  assert.equal(tray.trayMessage(), '');
  const activity = read('src/components/ActivityContext.svelte');
  assert.equal((activity.match(/\{#if trayMessage\(\)\}/g) || []).length, 1, 'one line, drawn once');
  tray.clearTray();
});

test('a refusal notice goes with the next tray action or the next trace request', () => {
  tray.clearTray();
  tray.addToTray(entry('1'));
  assert.equal(tray.addToTray(entry('1')), false);
  assert.equal(tray.tray.notice, copy.CALL_TRACE_TEXT.trayDuplicate);
  assert.equal(tray.trayMessage(), copy.CALL_TRACE_TEXT.trayDuplicate);
  tray.addToTray(entry('2'));
  assert.equal(tray.tray.notice, '', 'a good add clears it');
  tray.addToTray(entry('2'));
  assert.notEqual(tray.tray.notice, '');
  tray.clearNotice();
  assert.equal(tray.tray.notice, '', 'a new trace request clears it');
  tray.addToTray(entry('2'));
  tray.removeFromTray(entry('2'));
  assert.equal(tray.tray.notice, '', 'remove clears it');
  tray.addToTray(entry('1'));
  tray.clearTray();
  assert.equal(tray.tray.notice, '', 'clear clears it');
  assert.match(read('src/components/CallTrace.svelte'), /clearNotice\(\);[^\n]*\n\s*loading = true/, 'load clears the notice before the request');
  assert.match(read('src/components/ActivityContext.svelte'), /traceOpen=false;clearNotice\(\);/, 'a new anchor clears it too');
});
