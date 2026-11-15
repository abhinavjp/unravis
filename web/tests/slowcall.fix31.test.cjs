const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

function between(text, from, to) {
  const a = text.indexOf(from);
  assert.ok(a >= 0, `missing: ${from}`);
  const b = to == null ? text.length : text.indexOf(to, a + from.length);
  assert.ok(b >= 0, `missing: ${to}`);
  return text.slice(a, b);
}

// FIX-31 / FIX-33 (UI-04, UI-08, UI-10): lane gap reasons, quiet readout, keyboard and row-cap behaviour.

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

const { DRILL_TEXT, drillCopyEntries } = loadTypeScript('src/lib/drill/drillCopy.ts');
const { laneGap, laneGapText, buildLane, capRows, sameReadout, LIST_ROW_CAP } = loadTypeScript('src/lib/drill/callLanes.ts');
const read = (p) => fs.readFileSync(path.join(root, p), 'utf8').replace(/\r\n/g, '\n');

const T0 = Date.parse('2026-03-01T10:00:00Z');
const win = { axisStart: '2026-03-01T10:00:00Z', bucketSeconds: 60 };

test('UI-04: a lane with no numbers says why, from its own state', () => {
  assert.equal(laneGap({ state: 'no-data' }), 'no-file');
  assert.equal(laneGap({ state: 'incomplete' }), 'read-failed');
  for (const state of ['events', 'none-seen', 'clock-unchecked', 'out-of-range']) assert.equal(laneGap({ state }), 'not-recorded', state);
  assert.equal(laneGap(undefined), 'not-recorded');
});

test('UI-04: buildLane carries the gap for the API and web-server lanes too', () => {
  const lane = (key, state) => buildLane({ key, state, buckets: [], events: [] }, key, win, T0, T0 + 120000);
  assert.equal(lane('audit', 'no-data').gap, 'no-file');
  assert.equal(lane('iis', 'no-data').gap, 'no-file');
  assert.equal(lane('audit', 'incomplete').gap, 'read-failed');
  assert.equal(lane('iis', 'incomplete').gap, 'read-failed');
  assert.equal(lane('iis', 'events').gap, 'not-recorded');
  // A lane the answer does not hold yet (still loading) is not claimed to be unselected.
  assert.equal(buildLane(undefined, 'audit', win, T0, T0 + 120000).gap, 'not-recorded');
});

test('UI-04: the words are plain and never a number', () => {
  assert.equal(laneGapText('not-recorded'), DRILL_TEXT.notRecorded);
  assert.equal(laneGapText('no-file'), DRILL_TEXT.laneNoFile);
  assert.equal(laneGapText('read-failed'), DRILL_TEXT.laneReadFailed);
  assert.equal(laneGapText(undefined), DRILL_TEXT.notRecorded);
  const all = ['not-recorded', 'no-file', 'read-failed'].map(laneGapText);
  assert.equal(new Set(all).size, 3);
  for (const t of all) assert.doesNotMatch(t, /\d/);
  assert.ok(drillCopyEntries().some((e) => e.id === 'drill.rowsCapped'));
});

test('UI-04: the lane head uses the reason when there is no peak', () => {
  const src = read('src/components/CallLanes.svelte');
  const branch = 'lane.peak ? DRILL_TEXT.laneShape(formatAtOnce(lane.peak.atOnce)) : laneGapText(lane.gap)';
  assert.ok(src.indexOf(branch) >= 0, branch);
  assert.match(src, /lane\.peak \? DRILL_TEXT\.laneShape\(formatAtOnce\(lane\.peak\.atOnce\)\) : laneGapText\(lane\.gap\)/);
  assert.doesNotMatch(src, /: DRILL_TEXT\.notRecorded;\s*\n\s*\};/);
  const copy = read('src/lib/drill/drillCopy.ts');
  assert.ok(copy.indexOf('laneNoFile') >= 0);
  assert.ok(copy.indexOf('laneReadFailed') >= 0);
});

test('UI-10: capRows keeps the first rows and the full count', () => {
  const rows = Array.from({ length: 450 }, (_, i) => i);
  const r = capRows(rows);
  assert.equal(LIST_ROW_CAP, 200);
  assert.equal(r.rows.length, 200);
  assert.equal(r.rows[0], 0);
  assert.equal(r.rows[199], 199);
  assert.equal(r.total, 450);
  assert.equal(r.capped, true);
  const exact = capRows(rows.slice(0, 200));
  assert.equal(exact.capped, false);
  assert.equal(exact.rows.length, 200);
  const few = capRows([1, 2, 3]);
  assert.deepEqual(few.rows, [1, 2, 3]);
  assert.equal(few.capped, false);
  assert.equal(capRows([]).total, 0);
  assert.equal(capRows(rows, 5).rows.length, 5);
  assert.equal(capRows(rows, 0).rows.length, 200, 'a bad cap falls back to the default');
  assert.equal(capRows(rows, NaN).rows.length, 200);
});

test('UI-10: the cap note is plain words', () => {
  assert.equal(DRILL_TEXT.rowsCapped(200, 450), 'Showing the first 200 of 450');
  const copy = read('src/lib/drill/drillCopy.ts');
  assert.ok(copy.indexOf('Showing the first') >= 0);
});

test('UI-10: both tables cap their rows and say so', () => {
  const all = read('src/components/CallAllSources.svelte');
  assert.match(all, /capRows\(items\)/);
  assert.match(all, /\{#each capped\.rows as item \(item\.key\)\}/);
  assert.match(all, /DRILL_TEXT\.rowsCapped\(capped\.rows\.length, capped\.total\)/);
  assert.match(all, /DRILL_TEXT\.allHeading\(items\.length\)/, 'the heading still counts every item');
  const near = read('src/components/CallNearby.svelte');
  assert.match(near, /capRows\(rows\)/);
  assert.match(near, /\{#each capped\.rows as row /);
  assert.match(near, /DRILL_TEXT\.rowsCapped\(capped\.rows\.length, capped\.total\)/);
});

test('UI-10: all-sources rows take Space as well as Enter, and mark the selected row', () => {
  const all = read('src/components/CallAllSources.svelte');
  assert.match(all, /event\.key !== 'Enter' && event\.key !== ' '/);
  assert.match(all, /aria-selected=\{selectedKey === item\.key\}/);
  assert.match(all, /class:selected=\{selectedKey === item\.key\}/);
});

test('UI-08: the lanes area is a labelled group, not an application', () => {
  const src = read('src/components/CallLanes.svelte');
  assert.doesNotMatch(src, /role="application"/);
  assert.match(src, /role="group"\s+aria-label=\{DRILL_TEXT\.timelineTitle\}/);
  assert.ok(read('src/lib/drill/drillCopy.ts').indexOf('timelineTitle') >= 0);
});

test('UI-08: the hover readout is not live; only a pinned point is announced', () => {
  const src = read('src/components/CallLanes.svelte');
  assert.match(src, /data-testid="scall-readout" aria-live="off"/);
  assert.equal((src.match(/aria-live="polite"/g) ?? []).length, 1, 'one polite region');
  assert.match(src, /data-testid="scall-pinned-live"/);
  assert.match(src, /pinnedMs !== null && pinnedReadout/);
});

test('UI-08: the parent is told only when the readout changes', () => {
  const src = read('src/components/CallLanes.svelte');
  assert.match(src, /sameReadout\(sent\.value, value\)/);
  const calls = src.match(/onReadout\(/g) ?? [];
  assert.equal(calls.length, 1, 'one call, inside the guarded emit');
  assert.match(src, /emit\(readoutFor\(hoverMs\), false\)/);
  assert.match(src, /emit\(readoutFor\(pinnedMs\), true\)/);
  assert.match(src, /emit\(readoutFor\(next\), true\)/);
});

test('UI-08: sameReadout compares what would be drawn', () => {
  const a = { stepStart: 1, isThisCall: false, api: { atOnce: 1, p95Ms: 2, refused: null }, iis: null, db: null };
  assert.equal(sameReadout(a, { ...a, api: { ...a.api } }), true);
  assert.equal(sameReadout(a, { ...a, stepStart: 2 }), false);
  assert.equal(sameReadout(a, { ...a, api: { ...a.api, atOnce: 2 } }), false);
  assert.equal(sameReadout(null, null), true);
  assert.equal(sameReadout(a, null), false);
  assert.equal(sameReadout(null, a), false);
});
