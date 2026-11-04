const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

// WP-17b: the stacked three-lane tracing chart. `lanes.ts` is the pure chart math and wording;
// `TracingChart.svelte` is read as text, as tests/casehome.test.cjs does for other components.

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

const lanes = loadTypeScript('src/lib/tracing/lanes.ts');
const { lintScreenStrings } = loadTypeScript('src/lib/copy/copylint.ts');
const { deck } = loadTypeScript('src/lib/copy/deck.ts');

const {
  LANE_NAME, LANE_ASK, PANEL_TITLE, LANE_STATE_TEXT, INCOMPLETE_TEXT, MIXED_NOTE, TRUNCATED_NOTE,
  slotGrid, slotOutside, slotStartMs,
  barsFor, barMax, widthLabel, axisCaption, laneNote,
  truncatedNote, barHeightPct, laneDrawsBars, isLaneVisible, showsProvenance, laneStatusLine, NO_EVENTS_PERIOD, hasAnyData, outsideSlots
} = lanes;

function window(overrides = {}) {
  return { start: '2026-10-01T00:00:00Z', end: '2026-10-01T01:00:00Z', axisStart: '2026-10-01T00:00:00Z', axisEnd: '2026-10-01T01:00:00Z', bucketSeconds: 60, ...overrides };
}

function lane(overrides = {}) {
  return {
    key: 'sql', label: 'SQL Server', state: 'events', reason: '', origin: 'case', asOf: '2026-10-01T01:05:00Z',
    consistency: 'stored-as-of', complete: true, truncated: false, scanned: 0, hidden: 0, sources: [], buckets: [], events: [],
    ...overrides
  };
}

test('slot grid comes from the server axis', () => {
  const sixHour = slotGrid(window({ axisStart: '2026-10-01T00:00:00Z', axisEnd: '2026-10-31T00:00:00Z', bucketSeconds: 21600 }));
  assert.equal(sixHour.n, 120);
  assert.equal(sixHour.firstMs % 21600000, 0);

  const eightHour = slotGrid(window({ axisStart: '2026-10-01T00:00:00Z', axisEnd: '2026-10-31T08:00:00Z', bucketSeconds: 28800 }));
  assert.equal(eightHour.n, 91);

  const oneHour = slotGrid(window({ axisStart: '2026-10-01T00:00:00Z', axisEnd: '2026-10-01T01:00:00Z', bucketSeconds: 60 }));
  assert.equal(oneHour.n, 60);

  const g = slotGrid(window({
    start: '2026-10-01T10:07:00Z', end: '2026-10-01T11:07:00Z',
    axisStart: '2026-10-01T10:00:00Z', axisEnd: '2026-10-01T12:00:00Z', bucketSeconds: 60
  }));
  assert.equal(g.n, 120);
  assert.equal(slotOutside(g, 0), true);
  assert.equal(slotOutside(g, 30), false);

  assert.equal(slotGrid(window({ axisStart: '2026-10-01T02:00:00Z', axisEnd: '2026-10-01T00:00:00Z' })), null);
  assert.equal(slotGrid(window({ bucketSeconds: 0 })), null);
  assert.equal(slotGrid(window({ axisStart: 'not a date' })), null);
  assert.equal(slotGrid(window({ axisStart: '2026-01-01T00:00:00Z', axisEnd: '2026-10-31T00:00:00Z', bucketSeconds: 60 })), null);
});

test('bars are exactly the returned buckets', () => {
  const g = slotGrid(window({ axisStart: '2026-10-01T10:00:00Z', axisEnd: '2026-10-01T12:00:00Z', bucketSeconds: 60 }));
  const buckets = [
    { start: '2026-10-01T10:07:30Z', count: 4, errors: 1 },
    { start: '2026-10-01T10:08:00Z', count: 2, errors: 0 },
    { start: '2026-10-01T09:00:00Z', count: 9, errors: 0 },
    { start: 'x', count: 5, errors: 0 }
  ];
  const bars = barsFor(g, buckets);
  assert.deepEqual(bars.map((b) => b.index), [7, 8]);
  assert.deepEqual(bars[0], { index: 7, count: 4, errors: 1 });
  assert.deepEqual(bars[1], { index: 8, count: 2, errors: 0 });

  // An empty slot (index 9, say) gets no bar.
  assert.equal(bars.some((b) => b.index === 9), false);

  // Two buckets in the same cell add up.
  const combined = barsFor(g, [
    { start: '2026-10-01T10:07:00Z', count: 3, errors: 1 },
    { start: '2026-10-01T10:07:40Z', count: 2, errors: 1 }
  ]);
  assert.deepEqual(combined, [{ index: 7, count: 5, errors: 2 }]);

  assert.equal(barMax([]), 0);
  assert.equal(barMax(bars), 4);
});

test('lane wording is verbatim', () => {
  assert.deepEqual(LANE_STATE_TEXT, {
    'no-data': 'This case has no file for this source',
    'none-seen': 'Files cover this time and nothing was seen',
    'clock-unchecked': 'Time zone not checked, so nothing is drawn',
    incomplete: 'Read hit its limit; events may be missing',
    'out-of-range': 'Files do not cover this time'
  });
  assert.deepEqual(INCOMPLETE_TEXT, {
    timeout: 'The audit read ran out of time; nothing was shortened.',
    rowcap: 'The audit read reached its row limit; nothing was shortened.',
    error: 'The audit read failed; nothing was shortened.'
  });
  assert.deepEqual(MIXED_NOTE, {
    title: 'Some data was collected at different times.',
    body: 'API data was read just now, while SQL and web-server data came from saved logs. Compare the timing and trends, but do not expect totals from the different sources to match exactly.',
    fixed: 'This note cannot be dismissed.'
  });
  assert.equal(TRUNCATED_NOTE, 'The list of events was cut at its limit. The counts in the bars are not affected.');
  assert.deepEqual(LANE_NAME, {
    audit: 'API requests (audit log)',
    sql: 'Database (SQL Server)',
    iis: 'Web server (IIS)'
  });

  assert.equal(laneNote(lane({ state: 'incomplete', reason: '', incompleteReason: 'rowcap' })), INCOMPLETE_TEXT.rowcap);
  assert.equal(laneNote(lane({ state: 'out-of-range', reason: 'Files for this host start after this window.' })), 'Files for this host start after this window.');
  assert.equal(laneNote(lane({ state: 'events' })), '');
});

test('axis caption names the real width', () => {
  const hour = axisCaption('UTC', 3600, false);
  assert.match(hour, /Each bar covers 1 hour\b/);

  const sixHours = axisCaption('UTC', 21600, false);
  assert.match(sixHours, /6 hours/);
  assert.match(sixHours, /Longer periods group more time into each bar so the chart stays readable\./);

  const oneMinute = axisCaption('UTC', 60, false);
  assert.doesNotMatch(oneMinute, /Longer periods/);

  assert.match(axisCaption('UTC', 3600, true), /Grey dashes/);
  assert.equal(widthLabel(60), '1 minute');
  assert.equal(widthLabel(3600), '1 hour');
  assert.equal(widthLabel(7200), '2 hours');
});

test('disabled and truncated lanes', () => {
  assert.equal(
    laneNote(lane({ state: 'clock-unchecked', disabled: 'tz-rule', reason: '' })),
    LANE_STATE_TEXT['clock-unchecked']
  );
  const src = read('src/components/TracingChart.svelte');
  assert.ok(src.includes('lane.disabled'), 'TracingChart.svelte must read lane.disabled');
  assert.ok(src.includes('data-truncated'), 'TracingChart.svelte must carry data-truncated');
});

test('chart component is plain, read-only and keyboard operable', () => {
  const src = read('src/components/TracingChart.svelte');
  for (const needle of ['aria-roledescription="bucket selector"', 'ArrowLeft', 'ArrowRight', 'Enter', 'role="note"', 'MIXED_NOTE']) {
    assert.ok(src.includes(needle), 'TracingChart.svelte must contain ' + needle);
  }
  assert.doesNotMatch(src, /from '\.\.\/lib\/charts'/);
  assert.doesNotMatch(src, /echarts/i);
  assert.doesNotMatch(src, /guided/i);
  assert.doesNotMatch(src, /callTrace/i);
  assert.doesNotMatch(src, /dismiss/i);
  assert.ok(!src.includes('response.bucketSeconds'), 'the axis is response.window.bucketSeconds, not response.bucketSeconds');
  assert.ok(!src.includes('--lane-audit:'), 'TracingChart.svelte must not redefine --lane-audit');

  const lanesSrc = read('src/lib/tracing/lanes.ts');
  assert.doesNotMatch(lanesSrc, /from '\.\.\/charts'/);
  assert.doesNotMatch(lanesSrc, /echarts/i);
});

test('tracing lint', () => {
  const entries = [
    ...Object.entries(LANE_NAME).map(([id, text]) => ({ id: 'lanes.name.' + id, text })),
    ...Object.entries(LANE_ASK).map(([id, text]) => ({ id: 'lanes.ask.' + id, text })),
    ...Object.entries(PANEL_TITLE).map(([id, text]) => ({ id: 'lanes.panel.' + id, text })),
    { id: 'lanes.mixed.title', text: MIXED_NOTE.title },
    { id: 'lanes.mixed.body', text: MIXED_NOTE.body },
    { id: 'lanes.mixed.fixed', text: MIXED_NOTE.fixed },
    ...Object.entries(INCOMPLETE_TEXT).map(([id, text]) => ({ id: 'lanes.incomplete.' + id, text }))
  ];
  assert.deepEqual(lintScreenStrings(entries, deck.terms), []);
});

test('chart zoom, brush and row visibility stay absolute-indexed (WP-23b)', () => {
  const src = read('src/components/TracingChart.svelte');
  for (const needle of [
    'role="toolbar"', 'CANVAS_TEXT.zoomIn', 'CANVAS_TEXT.zoomOut', 'CANVAS_TEXT.resetZoom', 'aria-live="polite"',
    'absoluteSlot(', 'stepSlot(', 'edgeSlot(', 'rangeFromDrag(', 'isLaneVisible(visible', 'CANVAS_TEXT.allHidden',
    'onpointerdown={onOverlayDown}', 'onpointermove={onOverlayMove}', 'onpointerleave={onOverlayLeave}'
  ]) {
    assert.ok(src.includes(needle), 'TracingChart.svelte must contain ' + needle);
  }
  for (const key of ["'+'", "'-'", "'0'"]) assert.ok(src.includes('event.key === ' + key), 'keyboard ' + key);
  assert.doesNotMatch(src, /slotLeftPct|slotWidthPct/, 'positions come from the zoomed range, not the full grid');
  assert.doesNotMatch(src, /echarts|from '\.\.\/lib\/charts'|guided|callTrace/i);
});

test('latency line is plain svg with a P50/P95 selector (WP-23c)', () => {
  const src = read('src/components/TracingChart.svelte');
  for (const needle of ['<polyline', 'aria-pressed={percentile === option}', 'PERCENTILE_LABEL[option]', 'CANVAS_TEXT.noLatency', 'formatDuration(', 'hasLatency(', 'latencySegments(']) {
    assert.ok(src.includes(needle), 'TracingChart.svelte must contain ' + needle);
  }
  assert.doesNotMatch(src, /echarts|from '\.\.\/lib\/charts'/i);
});

const laneOf = (extra = {}) => ({ key: 'audit', state: 'events', origin: 'live', asOf: '2026-10-01T00:05:00Z', buckets: [], truncated: false, ...extra });

test('bar heights: zero only for an empty bucket, capped at 100 (UI-R1-02)', () => {
  assert.equal(barHeightPct(0, 10), 0);
  assert.equal(barHeightPct(5, 10), 50);
  assert.equal(barHeightPct(1, 60000) > 0, true);
  assert.equal(barHeightPct(20, 10), 100);
  assert.equal(barHeightPct(3, 0), 0);
  const src = read('src/components/TracingChart.svelte');
  assert.match(src, /\.bar\.nonzero \{ min-height: 2px/, 'any non-empty bucket keeps a visible bar');
});

test('lane rules shared by chart and tooltip', () => {
  assert.equal(laneDrawsBars(laneOf()), true);
  assert.equal(laneDrawsBars(laneOf({ disabled: true })), false);
  assert.equal(laneDrawsBars(laneOf({ state: 'none-seen' })), false);
  assert.equal(laneDrawsBars(laneOf({ state: 'incomplete' }), 0), false);
  assert.equal(laneDrawsBars(laneOf({ state: 'incomplete' }), 2), true);
  assert.equal(isLaneVisible({}, 'sql'), true);
  assert.equal(isLaneVisible({ sql: false }, 'sql'), false);
  assert.equal(isLaneVisible({ sql: false }, 'iis'), true);
  assert.equal(laneStatusLine(laneOf({ state: 'none-seen' })), NO_EVENTS_PERIOD);
  assert.equal(laneStatusLine(laneOf({ state: 'no-data' })), LANE_STATE_TEXT['no-data']);
  assert.equal(laneStatusLine(laneOf()), '');
});

test('no provenance line for a lane with no file (UI-R1-08)', () => {
  assert.equal(showsProvenance(laneOf({ state: 'no-data' })), false);
  for (const state of ['events', 'none-seen', 'incomplete', 'out-of-range', 'clock-unchecked']) assert.equal(showsProvenance(laneOf({ state })), true);
  const src = read('src/components/TracingChart.svelte');
  assert.ok(src.includes('{#if showsProvenance(lane)}'));
});

test('chart controls need data (UI-R1-18)', () => {
  const g = slotGrid(window());
  const lane = laneOf({ buckets: [{ start: '2026-10-01T00:10:00Z', count: 3, errors: 0 }] });
  assert.equal(hasAnyData(g, [lane]), true);
  assert.equal(hasAnyData(g, [laneOf()]), false);
  assert.equal(hasAnyData(g, [laneOf({ buckets: [{ start: '2026-10-01T00:10:00Z', count: 0, errors: 0 }] })]), false);
  assert.equal(hasAnyData(g, [{ ...lane, disabled: true }]), false);
  assert.equal(hasAnyData(null, [lane]), false);
  const src = read('src/components/TracingChart.svelte');
  assert.ok(src.includes('{#if grid && hasData}'), 'the toolbar is hidden without data');
});

test('outside slots are only the axis edges', () => {
  const wide = slotGrid(window({ start: '2026-10-01T00:00:30Z', end: '2026-10-01T00:59:30Z' }));
  assert.deepEqual(outsideSlots(wide, 0, wide.n), [0, wide.n - 1]);
  assert.deepEqual(outsideSlots(wide, 1, wide.n - 1), []);
  assert.deepEqual(outsideSlots(wide, 0, 1), [0]);
  const exact = slotGrid(window());
  assert.deepEqual(outsideSlots(exact, 0, exact.n), []);
});

test('the truncation note names the lane that was cut, in lane order (UI-R2-06)', () => {
  const sql = lane({ key: 'sql', truncated: true });
  const iis = lane({ key: 'iis', truncated: true });
  const audit = lane({ key: 'audit', truncated: false });
  assert.equal(truncatedNote([audit, sql, lane({ key: 'iis' })]), 'The list of events for Database (SQL Server) was cut at its limit. The counts in the bars are not affected.');
  assert.equal(truncatedNote([iis, sql]), 'The list of events for Database (SQL Server) and Web server (IIS) was cut at its limit. The counts in the bars are not affected.');
  assert.equal(truncatedNote([audit]), TRUNCATED_NOTE, 'no lane flagged: the plain note');
  assert.equal(truncatedNote([]), TRUNCATED_NOTE);
  assert.match(read('src/components/TracingChart.svelte'), /\{truncatedNote\(response\.lanes\)\}/);
});

test('Escape during a brush drag drops the selection and swallows the click that follows (UI-R2-05)', () => {
  const src = read('src/components/TracingChart.svelte');
  const start = src.indexOf('function cancelDrag()');
  assert.ok(start > 0, 'cancelDrag exists');
  const body = src.slice(start, src.indexOf('function onOverlayClick'));
  assert.match(body, /dragFrom = null;\s*dragTo = null;\s*suppressClick = true;/);
  assert.match(src, /event\.key === 'Escape' && dragFrom != null\) \{ event\.preventDefault\(\); event\.stopPropagation\(\); cancelDrag\(\)/);
  assert.match(src, /<svelte:window onkeydown=\{onWindowKey\} \/>/);
  // The drag branch comes before the tooltip branch, so one Escape does one thing.
  assert.ok(src.indexOf("event.key === 'Escape' && dragFrom != null") < src.indexOf("event.key === 'Escape' && tipSlot != null"));
});

function luminance(hex) {
  const channel = (value) => { const c = value / 255; return c <= 0.03928 ? c / 12.92 : Math.pow((c + 0.055) / 1.055, 2.4); };
  const n = parseInt(hex.slice(1), 16);
  return 0.2126 * channel((n >> 16) & 255) + 0.7152 * channel((n >> 8) & 255) + 0.0722 * channel(n & 255);
}
const contrast = (a, b) => { const [hi, lo] = [luminance(a), luminance(b)].sort((x, y) => y - x); return (hi + 0.05) / (lo + 0.05); };

test('the error overlay differs from the bar by pattern and by at least 3:1 lightness, in both themes (UI-R2-04)', () => {
  const css = read('src/brand/tokens.css');
  const values = (name) => [...css.matchAll(new RegExp('--' + name + ': *(#[0-9a-fA-F]{6})', 'g'))].map((match) => match[1]);
  const bg = values('bg');
  assert.equal(bg.length, 3, 'dark, light by media, light forced');
  for (const lane of ['lane-audit', 'lane-sql', 'lane-iis']) {
    const colours = values(lane);
    assert.equal(colours.length, 3);
    colours.forEach((colour, theme) => {
      assert.ok(contrast(colour, bg[theme]) >= 3, lane + ' against the stripe colour, theme ' + theme + ': ' + contrast(colour, bg[theme]).toFixed(2));
    });
  }
  const src = read('src/components/TracingChart.svelte');
  const rule = src.slice(src.indexOf('.bar-err {'), src.indexOf('}', src.indexOf('.bar-err {')));
  assert.match(rule, /repeating-linear-gradient\([^)]*var\(--bad\)[^)]*var\(--bg\)/);
  assert.equal(PANEL_TITLE.iis, 'Web-server requests (errors shown striped)');
});

test('the lane heads are not tab stops and keep a name (UI-R2-07)', () => {
  const src = read('src/components/TracingChart.svelte');
  assert.match(src, /<div class="lane-label" role="group" aria-label=\{LANE_NAME\[lane\.key\]\} aria-describedby="tc-hint-\{lane\.key\}"/);
  assert.doesNotMatch(src, /lane-label[^\n]*tabindex/);
});

test('the chart keeps the axis baseline, tab stops and ticks honest (UI-R1-07/10/15/17, WEB-R1)', () => {
  const src = read('src/components/TracingChart.svelte');
  const noteAt = src.indexOf('class="lane-note bad"');
  const barsAt = src.indexOf('<div class="bars">');
  assert.ok(noteAt > 0 && barsAt > noteAt, 'the incomplete note sits above the bars, so it cannot move the baseline');
  assert.ok(src.includes('class="scale"'), 'the y scale is its own bar-height box');
  assert.ok(src.includes('{formatCount(max)}'), 'axis ticks use the tooltip number format');
  assert.ok(src.includes('role="group" aria-label={LANE_NAME[lane.key]}'), 'lane labels are named groups');
  assert.ok(src.includes("event.key === 'Home'") && src.includes("event.key === 'End'"));
  assert.ok(src.includes('axisTicks(shown, plotW'), 'time labels are thinned to the measured plot');
  assert.ok(src.includes('bind:clientWidth={plotW}'));
  assert.match(src, /@media \(max-width: 768px\)/);
  assert.ok(!src.includes('onclick={() => select'), 'no per-column click closures');
});

test('a tap shows the tooltip and keeps it, a second tap on the same bar opens it; a mouse click still opens at once (UI-R3-06)', () => {
  const src = read('src/components/TracingChart.svelte');
  const down = src.slice(src.indexOf('function onOverlayDown'), src.indexOf('function onOverlayClick'));
  // A press from a finger or pen keeps the tooltip; only a mouse press hides it first, as before.
  assert.match(down, /downTouch = isTouchPointer\(event\.pointerType, coarsePointer\(\)\)/);
  assert.match(down, /if \(!downTouch\) hideTip\(\);/);
  const click = src.slice(src.indexOf('function onOverlayClick'), src.indexOf('function showTouchTip'));
  assert.match(click, /if \(touch && !\(tipFromTouch && tipSlot === slot\)\) \{ showTouchTip\(slot, event\); return; \}\s*selectAt\(slot\);/);
  assert.ok(click.indexOf('showTouchTip(') < click.indexOf('selectAt(slot)'), 'the touch branch comes before the hand-off');
  // The touch flag only comes from isTouchPointer, so a mouse press is never touch.
  assert.equal((src.match(/downTouch = /g) || []).length, 3, 'set on press, cleared on click, declared once');
  // Moving the pointer is hover or drag: it clears the touch flag. Leaving by finger keeps the tip; leaving by mouse hides it.
  assert.match(src, /tipFromTouch = false;\s*placeTip\(point\.x, point\.y\);/);
  assert.match(src, /if \(tipFromTouch && event\.pointerType !== 'mouse'\) return;/);
  assert.match(src, /function hideTip\(\) \{[^}]*tipFromTouch = false;/);
  // The hint is shown only for a touch tooltip, and the sentence is in the lint list.
  assert.match(src, /\{#if tipOpenable\}<span class="tip-action">\{TOOLTIP_TEXT\.tapAgain\}<\/span>\{\/if\}/);
});

test('the chart toolbar and hint are hidden while there is nothing to show (UI-R3-02)', () => {
  const src = read('src/components/TracingChart.svelte');
  assert.match(src, /\{#if grid && hasData\}/);
});

test('the SQL panel title claims only slow calls, because SQL data carries no errors (UI-R3-01)', () => {
  assert.equal(PANEL_TITLE.sql, 'Slow database calls (over 1 s)');
  assert.doesNotMatch(PANEL_TITLE.sql, /failed/i);
});
