const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

// UI loop round 5: focus and title on open, round axis labels for multi-day windows, closed-case restore, lane swatch beside the name.

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
const read = (relative) => fs.readFileSync(path.join(root, relative), 'utf8').replace(/\r\n/g, '\n');
const { formatTime } = loadTypeScript('src/lib/time.ts');
const { axisTicks, tickOffsets, tickStep } = loadTypeScript('src/lib/tracing/canvas.ts');
const { dayTicks, roundTicks } = loadTypeScript('src/lib/chartTime.ts');

const HOUR = 3_600_000, DAY = 24 * HOUR;
const clock = (ms, zone) => formatTime(ms, { zone, precision: 'minute', date: false, suffix: false });
const dayOf = (ms, zone) => formatTime(ms, { zone, precision: 'minute', suffix: false });

// UI-R5-02. Bucket widths follow the server ladder (1 h to 24 h, at most 120 buckets).
const WINDOWS = [
  // [label, start, bucket width, bucket count]
  ['7 days aligned', '2026-10-13T00:00:00Z', 2 * HOUR, 84],
  ['7 days from 09:00', '2026-10-13T09:00:00Z', 2 * HOUR, 85],
  ['10 days aligned', '2026-10-10T00:00:00Z', 2 * HOUR, 120],
  ['10 days from 09:00', '2026-10-10T09:00:00Z', 4 * HOUR, 61],
  ['14 days aligned', '2026-10-06T00:00:00Z', 4 * HOUR, 84],
  ['14 days from 09:00', '2026-10-06T09:00:00Z', 4 * HOUR, 85],
  ['30 days aligned', '2026-09-20T00:00:00Z', 6 * HOUR, 120],
  ['30 days from 09:00', '2026-09-20T09:00:00Z', 8 * HOUR, 91]
];
const ZONES = ['UTC', 'Asia/Dubai', 'Asia/Kolkata', 'Europe/London', 'America/New_York'];

test('multi-day axis labels sit on local midnights, at most six, never crowded', () => {
  for (const [label, iso, widthMs, shown] of WINDOWS) {
    for (const zone of ZONES) {
      const startMs = Date.parse(iso);
      for (const chars of [16, 21]) {
        for (const widthPx of [0, 280, 700, 1000, 1190]) {
          const why = `${label} ${zone} chars ${chars} px ${widthPx}`;
          const ticks = axisTicks(shown, widthPx, chars, { startMs, widthMs, zone });
          assert.ok(ticks.length >= 1 && ticks.length <= 6, why + ': ' + ticks.length);
          const step = tickStep(shown, widthPx, chars);
          for (let i = 0; i < ticks.length; i++) {
            const t = ticks[i];
            assert.ok(t.ms >= startMs && t.ms < startMs + shown * widthMs, why);
            assert.equal(t.offset, (t.ms - startMs) / widthMs, why);
            assert.equal(clock(t.ms, zone), '00:00', why + ' ' + dayOf(t.ms, zone));
            // One hour of slack: a clock change makes a calendar day 23 or 25 hours long.
            if (i > 0) assert.ok(t.ms - ticks[i - 1].ms >= step * widthMs - HOUR, why);
          }
        }
      }
    }
  }
});

test('7, 10, 14 and 30 day windows in UTC give the expected dates', () => {
  const days = (iso, widthMs, shown, widthPx = 1000) => {
    const startMs = Date.parse(iso);
    return axisTicks(shown, widthPx, 16, { startMs, widthMs, zone: 'UTC' }).map((t) => dayOf(t.ms, 'UTC'));
  };
  // 7 days: a 28 hour gap rounds up to every second midnight.
  assert.deepEqual(days('2026-10-13T00:00:00Z', 2 * HOUR, 84), ['13 Oct 00:00', '15 Oct 00:00', '17 Oct 00:00', '19 Oct 00:00']);
  // The same week from 09:00: the first label is the first midnight inside the window.
  assert.deepEqual(days('2026-10-13T09:00:00Z', 2 * HOUR, 84), ['14 Oct 00:00', '16 Oct 00:00', '18 Oct 00:00', '20 Oct 00:00']);
  assert.deepEqual(days('2026-10-10T00:00:00Z', 2 * HOUR, 120), ['10 Oct 00:00', '12 Oct 00:00', '14 Oct 00:00', '16 Oct 00:00', '18 Oct 00:00']);
  assert.deepEqual(days('2026-10-06T00:00:00Z', 4 * HOUR, 84), ['6 Oct 00:00', '9 Oct 00:00', '12 Oct 00:00', '15 Oct 00:00', '18 Oct 00:00']);
  // 30 days: weekly midnights.
  assert.deepEqual(days('2026-09-20T00:00:00Z', 6 * HOUR, 120), ['20 Sep 00:00', '27 Sep 00:00', '4 Oct 00:00', '11 Oct 00:00', '18 Oct 00:00']);
  // A phone-width plot asks for more room per label, so the step grows.
  const narrow = days('2026-10-13T00:00:00Z', 2 * HOUR, 84, 280);
  assert.deepEqual(narrow, ['13 Oct 00:00', '17 Oct 00:00']);
});

test('windows of one to three days use 3, 6 or 12 hour marks in the zone', () => {
  const marks = (iso, widthMs, shown, zone = 'UTC', widthPx = 1000) => {
    const startMs = Date.parse(iso);
    return axisTicks(shown, widthPx, 16, { startMs, widthMs, zone }).map((t) => clock(t.ms, zone));
  };
  assert.deepEqual(marks('2026-10-19T00:00:00Z', HOUR, 48), ['00:00', '12:00', '00:00', '12:00']);
  // Not tied to bar edges: a start at 07:00 (bars on odd hours) still labels 12:00 and 00:00 exactly.
  assert.deepEqual(marks('2026-10-19T07:00:00Z', 2 * HOUR, 24), ['12:00', '00:00', '12:00', '00:00']);
  // Kolkata is UTC+5:30: its midnight is 18:30 UTC.
  assert.deepEqual(marks('2026-10-19T00:00:00Z', HOUR, 48, 'Asia/Kolkata'), ['12:00', '00:00', '12:00', '00:00']);
});

test('a clock change inside the window keeps labels on local midnight', () => {
  // London falls back on 25 Oct 2026 (01:00 UTC): midnights before it are 23:00 UTC, after it 00:00 UTC.
  const startMs = Date.parse('2026-10-22T00:00:00Z');
  const ticks = axisTicks(84, 1000, 16, { startMs, widthMs: 2 * HOUR, zone: 'Europe/London' });
  assert.deepEqual(ticks.map((t) => dayOf(t.ms, 'Europe/London')), ['23 Oct 00:00', '25 Oct 00:00', '27 Oct 00:00']);
  assert.equal(ticks[1].ms - ticks[0].ms, 2 * DAY);
  assert.equal(ticks[2].ms - ticks[1].ms, 2 * DAY + HOUR);
  // New York falls back on 1 Nov 2026 (06:00 UTC).
  const ny = axisTicks(84, 1000, 16, { startMs: Date.parse('2026-10-29T00:00:00Z'), widthMs: 2 * HOUR, zone: 'America/New_York' });
  assert.ok(ny.length >= 3);
  assert.ok(ny.every((t) => clock(t.ms, 'America/New_York') === '00:00'));
  // Spring forward: London on 29 Mar 2026.
  const spring = dayTicks(Date.parse('2026-03-26T00:00:00Z'), Date.parse('2026-04-02T00:00:00Z'), 1, 'Europe/London');
  assert.equal(spring.length, 8);
  assert.ok(spring.every((ms) => clock(ms, 'Europe/London') === '00:00'));
  assert.equal(spring[4] - spring[3], 23 * HOUR);
});

test('dayTicks counts calendar days from the first midnight and roundTicks stays on the clock', () => {
  const startMs = Date.parse('2026-10-13T09:17:00Z'), endMs = Date.parse('2026-10-27T00:00:00Z');
  assert.deepEqual(dayTicks(startMs, endMs, 3, 'UTC').map((ms) => dayOf(ms, 'UTC')), ['14 Oct 00:00', '17 Oct 00:00', '20 Oct 00:00', '23 Oct 00:00', '26 Oct 00:00']);
  assert.deepEqual(dayTicks(startMs, endMs, 0, 'UTC'), []);
  assert.deepEqual(roundTicks(Date.parse('2026-10-13T09:17:00Z'), Date.parse('2026-10-13T20:00:00Z'), 6 * HOUR, 'UTC').map((ms) => clock(ms, 'UTC')), ['12:00', '18:00']);
});

test('tickOffsets still answers in whole slots for the same windows', () => {
  const startMs = Date.parse('2026-10-13T09:00:00Z');
  const offsets = tickOffsets(84, 1000, 16, { startMs, widthMs: 2 * HOUR, zone: 'UTC' });
  assert.ok(offsets.length >= 3 && offsets.length <= 6);
  assert.ok(offsets.every((k) => Number.isInteger(k) && k >= 0 && k < 84));
  assert.deepEqual(offsets, [...offsets].sort((a, b) => a - b));
  // Under a day nothing changed: slot-edge labels.
  assert.deepEqual(tickOffsets(48, 800, 16, { startMs: Date.parse('2026-10-20T08:00:00Z'), widthMs: 5 * 60_000, zone: 'UTC' }).map((k) => k % 12), [0, 0, 0, 0]);
});

test('the chart draws labels from axisTicks at their own position', () => {
  const chart = read('src/components/TracingChart.svelte');
  assert.match(chart, /\{#each ticks as tick \(tick\.ms\)\}/);
  assert.match(chart, /left:\{tick\.offset \* widthPct\}%">\{at\(tick\.ms\)\}/);
});

// UI-R5-01
test('the timeline takes focus on its heading once, when it opens', () => {
  const paper = read('src/components/TracingPaper.svelte');
  assert.match(paper, /<h2 id="tp-title" tabindex="-1" bind:this=\{headingEl\}>/);
  assert.match(paper, /if \(!active \|\| active === document\.body\) headingEl\?\.focus\(\);\n\s+void run\(\);/);
  // Only onMount focuses the heading: Try again, Cancel and re-runs never do.
  assert.equal((paper.match(/headingEl\?\.focus\(\)/g) || []).length, 1);
});
test('the tab title names the screen and goes back to the app name', () => {
  const home = read('src/lib/components/CaseHome.svelte');
  assert.match(home, /\$effect\(\(\) => \{ if \(phase === 'ready'\) return setDocTitle\(timelineShown \? TRACING_TEXT\.heading : info\?\.title \|\| caseCode\(caseId\)\); \}\);/);
  const doc = { title: 'Unravis' };
  global.document = doc;
  try {
    const { setDocTitle } = loadTypeScript('src/lib/docTitle.ts');
    const restore = setDocTitle('Investigation Timeline');
    assert.equal(doc.title, 'Investigation Timeline - Unravis');
    const next = setDocTitle('  Orders API stall ');
    assert.equal(doc.title, 'Orders API stall - Unravis');
    next();
    assert.equal(doc.title, 'Unravis');
    restore();
    assert.equal(doc.title, 'Unravis');
    setDocTitle('   ');
    assert.equal(doc.title, 'Unravis');
  } finally {
    delete global.document;
  }
});

// UI-R5-04
test('a closed case restored into the timeline falls back to the case page like an archived one', () => {
  const home = read('src/lib/components/CaseHome.svelte');
  assert.match(home, /const isClosed = \$derived\(!!info && believedState\(info\) === 'closed'\);/);
  assert.match(home, /\(isArchived \|\| isClosed\)\) closeTracing\(\)/);
  assert.match(home, /\{:else if timelineShown\}/);
  // The case page button stays disabled for a closed case (UI-R2-03 is still open).
  assert.match(home, /id="home-tracing-open"[^>]*disabled=\{!!info\?\.closed\}/);
});

// UI-R5-05
test('the lane swatch sits inline in front of the lane name', () => {
  const chart = read('src/components/TracingChart.svelte');
  assert.match(chart, /<span class="lane-title"><span class="swatch" style=\{SWATCH_STYLE\[lane\.key\]\} aria-hidden="true"><\/span><strong>\{LANE_NAME\[lane\.key\]\}<\/strong><\/span>/);
  assert.match(chart, /\.lane-label \.lane-title \{ display: flex; align-items: center;/);
  assert.match(chart, /\.lane-label \.swatch \{ flex: none; width: 10px; height: 10px;/);
});
