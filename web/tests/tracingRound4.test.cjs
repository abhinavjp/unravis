const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

// UI loop round 4: archived restore, years on the axis, case name, phone date boxes, round axis labels, ledger 409, wide column and note banner.

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
const { needsYear } = loadTypeScript('src/lib/chartTime.ts');
const { formatTime } = loadTypeScript('src/lib/time.ts');
const { tickOffsets } = loadTypeScript('src/lib/tracing/canvas.ts');
const { barTipLines } = loadTypeScript('src/lib/tracing/tooltip.ts');

const MIN = 60_000, HOUR = 60 * MIN;
const NOW = Date.parse('2026-11-04T09:00:00Z');

// UI-R4-02
test('the year is printed when the window is not in the current year or spans two years', () => {
  const t = (iso) => Date.parse(iso);
  assert.equal(needsYear(t('2026-10-20T08:00:00Z'), t('2026-10-20T11:59:00Z'), 'UTC', NOW), false);
  assert.equal(needsYear(t('2026-12-31T22:00:00Z'), t('2027-01-01T01:59:00Z'), 'UTC', NOW), true);
  assert.equal(needsYear(t('2001-03-01T00:00:00Z'), t('2001-03-01T04:00:00Z'), 'UTC', NOW), true);
  assert.equal(needsYear(t('2099-03-01T00:00:00Z'), t('2099-03-01T04:00:00Z'), 'UTC', NOW), true);
  // The zone decides which year a moment is in: 23:30 UTC on 31 Dec is already 2027 in Dubai.
  assert.equal(needsYear(t('2026-12-31T23:30:00Z'), t('2026-12-31T23:45:00Z'), 'Asia/Dubai', t('2027-01-01T00:00:00Z')), false);
  assert.equal(needsYear(t('2026-12-31T23:30:00Z'), t('2026-12-31T23:45:00Z'), 'UTC', t('2027-01-01T00:00:00Z')), true);
  assert.equal(needsYear(Number.NaN, 1, 'UTC', NOW), false);
});

test('labels and tooltip ranges carry the year only when asked', () => {
  const a = Date.parse('2026-12-31T23:00:00Z'), b = Date.parse('2027-01-01T00:00:00Z');
  assert.equal(formatTime(a, { zone: 'UTC', precision: 'minute', year: true }), '31 Dec 2026 23:00 UTC');
  assert.equal(formatTime(b, { zone: 'UTC', precision: 'minute', year: true }), '1 Jan 2027 00:00 UTC');
  assert.equal(formatTime(a, { zone: 'UTC', precision: 'minute' }), '31 Dec 23:00 UTC');
  const lines = barTipLines({ key: 'iis', startText: '31 Dec 2026 23:00 UTC', endText: '1 Jan 2027 00:00 UTC', zoneName: 'UTC', count: 0, errors: 0, origin: 'case', asOfText: '', note: '' });
  assert.equal(lines[0], '31 Dec 2026 23:00 to 1 Jan 2027 00:00 UTC');
  const chart = read('src/components/TracingChart.svelte');
  assert.match(chart, /needsYear\(grid\.firstMs/);
  assert.match(chart, /year: showYear/);
});

// UI-R4-05
const hhmm = (ms, zone = 'UTC') => formatTime(ms, { zone, precision: 'minute', date: false, suffix: false });
const labelsFor = (startIso, widthMs, shown, widthPx, zone = 'UTC') => {
  const startMs = Date.parse(startIso);
  const chars = 'XX XXX 00:00 UTC'.length;
  return tickOffsets(shown, widthPx, chars, { startMs, widthMs, zone }).map((k) => hhmm(startMs + k * widthMs, zone));
};
test('axis labels land on round hours for a four hour window', () => {
  assert.deepEqual(labelsFor('2026-10-20T08:00:00Z', 5 * MIN, 48, 800), ['08:00', '09:00', '10:00', '11:00']);
});
test('axis labels skip a first slot that is not round', () => {
  assert.deepEqual(labelsFor('2026-10-20T07:50:00Z', 5 * MIN, 48, 800), ['08:00', '09:00', '10:00', '11:00']);
});
test('axis labels use half hours when the step is shorter', () => {
  // 24 five-minute slots (two hours): the step is four slots (20 minutes), so the half hour is the smallest round step that fits.
  assert.deepEqual(labelsFor('2026-10-20T08:00:00Z', 5 * MIN, 24, 1200), ['08:00', '08:30', '09:00', '09:30']);
});
test('axis labels use six hour and day boundaries for long windows', () => {
  assert.deepEqual(labelsFor('2026-10-19T00:00:00Z', 6 * HOUR, 8, 900), ['00:00', '12:00', '00:00', '12:00']);
  const days = labelsFor('2026-10-05T00:00:00Z', 6 * HOUR, 120, 900);
  assert.ok(days.length >= 2 && days.every((label) => label === '00:00'), days.join(','));
});
test('axis labels use the local clock in the display zone', () => {
  // Asia/Kolkata is UTC+5:30: a slot that starts on a round local hour starts at :30 UTC.
  assert.deepEqual(labelsFor('2026-10-20T02:30:00Z', 5 * MIN, 48, 800, 'Asia/Kolkata'), ['08:00', '09:00', '10:00', '11:00']);
});
test('axis labels fall back to every Nth slot when no round time fits', () => {
  const startMs = Date.parse('2026-10-20T08:00:00Z');
  // A 7 minute slot is not a whole part of any round step.
  const odd = tickOffsets(30, 900, 16, { startMs, widthMs: 7 * MIN, zone: 'UTC' });
  assert.deepEqual(odd, tickOffsets(30, 900, 16));
  assert.equal(odd[0], 0);
  // Without alignment the old rule is unchanged: the first slot, then every Nth.
  assert.deepEqual(tickOffsets(48, 800, 16), [0, 8, 16, 24, 32, 40]);
});
test('the right edge rule still drops a label that would hang over', () => {
  const offsets = tickOffsets(48, 800, 16, { startMs: Date.parse('2026-10-20T08:00:00Z'), widthMs: 5 * MIN, zone: 'UTC' });
  assert.ok(offsets.every((k) => k === 0 || (k / 48) * 800 + 56 <= 808));
});
test('a clock change inside the range keeps labels on local round hours', () => {
  // London falls back on 2026-10-25 at 01:00 UTC: slots after it have a different offset.
  const startMs = Date.parse('2026-10-24T22:00:00Z');
  const labels = tickOffsets(48, 800, 16, { startMs, widthMs: 15 * MIN, zone: 'Europe/London' }).map((k) => hhmm(startMs + k * 15 * MIN, 'Europe/London'));
  assert.ok(labels.length >= 2 && labels.every((label) => label.endsWith(':00')), labels.join(','));
});

// UI-R4-01
test('an archived case restored into the timeline returns to the case page', () => {
  const home = read('src/lib/components/CaseHome.svelte');
  assert.match(home, /\$effect\(\(\) => \{ if \(phase === 'ready' && tracingOpen && \(isArchived \|\| isClosed\)\) closeTracing\(\); \}\);/);
  assert.match(home, /const timelineShown = \$derived\(tracingOpen && !isArchived && !isClosed\);/);
  assert.match(home, /\{:else if timelineShown\}/);
});
test('a closed case keeps its read-only timeline and has no Try again', () => {
  const paper = read('src/components/TracingPaper.svelte');
  assert.match(paper, /\{#if !closed\}<button.*>\{TRACING_TEXT\.retry\}<\/button>\{\/if\}/);
  assert.match(paper, /if \(closed\) \{\s*status = 'error';/);
});

// UI-R4-08
test('the timeline screen names the case as text', () => {
  const home = read('src/lib/components/CaseHome.svelte');
  assert.match(home, /<TracingPaper \{caseId\} title=\{info\?\.title \|\| caseCode\(caseId\)\}/);
  const paper = read('src/components/TracingPaper.svelte');
  assert.match(paper, /\{#if title\}<p class="case-name"[^>]*>\{title\}<\/p>\{\/if\}/);
  assert.match(paper, /\.case-name \{[^}]*overflow-wrap: anywhere/);
  assert.ok(!paper.includes('{@html'), 'the title is text, never HTML');
});

// UI-R4-07
test('on a phone the date boxes fill the fieldset', () => {
  const paper = read('src/components/TracingPaper.svelte');
  const start = paper.indexOf('@media (max-width: 720px)');
  assert.ok(start > 0);
  const block = paper.slice(start, paper.indexOf('.chart-region:focus-visible'));
  assert.match(block, /\.window-set \{ flex: 1 1 100%; \}/);
  assert.match(block, /\.window-field input \{ width: 100%; box-sizing: border-box; \}/);
});

// UI-R4-06
test('the header count and the reload do not ask for theories before the case page knows the case', () => {
  const count = read('src/components/LedgerCount.svelte');
  assert.match(count, /!ledger\.loaded && app\.screen !== 'case'/);
  const state = read('src/lib/appState.svelte.ts');
  assert.match(state, /setLedgerCase\(activeCaseId, storedCaseView\(\) === ''\)/);
  const store = read('src/lib/ledgerStore.svelte.ts');
  assert.match(store, /setLedgerCase\(caseId: string, read = true\)/);
  assert.match(store, /if \(caseId && read\) void refreshLedger/);
});

// UI-R4-04
test('only the timeline gets the wide column and the note banner has the amber edge', () => {
  const home = read('src/lib/components/CaseHome.svelte');
  assert.match(home, /<div class="case-home" class:wide=\{timelineShown\}>/);
  assert.match(home, /\.case-home \{[^}]*max-width: 880px/);
  assert.match(home, /\.case-home\.wide \{ max-width: 1232px; \}/);
  const chart = read('src/components/TracingChart.svelte');
  const notice = chart.split('\n').find((row) => row.trim().startsWith('.notice {'));
  assert.ok(notice);
  assert.match(notice, /border-left: var\(--space-1\) solid var\(--warn\)/);
  assert.match(notice, /color-mix\(in srgb, var\(--warn\) 9%, var\(--surface\)\)/);
});

// Round ticks keep the spacing the every-Nth rule guaranteed: never closer than tickStep slots, never more than six, the start label stays.
const { tickStep } = loadTypeScript('src/lib/tracing/canvas.ts');
test('round ticks never sit closer than the every-Nth spacing and never exceed six labels', () => {
  const windows = [
    ['2026-10-19T15:00:00Z', 5 * MIN, 36, 'UTC'],
    ['2026-10-19T15:00:00Z', 5 * MIN, 36, 'Asia/Kolkata'],
    ['2026-10-20T07:50:00Z', 5 * MIN, 48, 'UTC'],
    ['2026-10-19T00:00:00Z', 6 * HOUR, 120, 'UTC'],
    ['2026-10-24T22:00:00Z', 15 * MIN, 48, 'Europe/London'],
    ['2026-03-28T20:00:00Z', 15 * MIN, 60, 'Europe/London']
  ];
  for (const [iso, widthMs, shown, zone] of windows) {
    const startMs = Date.parse(iso);
    for (const chars of [16, 21]) {
      for (let widthPx = 0; widthPx <= 1400; widthPx += 50) {
        const step = tickStep(shown, widthPx, chars);
        const ticks = tickOffsets(shown, widthPx, chars, { startMs, widthMs, zone });
        const why = `${iso} ${widthMs / MIN}m x${shown} ${zone} chars ${chars} px ${widthPx}: ${ticks.join(',')}`;
        assert.ok(ticks.length >= 1 && ticks.length <= 6, why);
        // A clock change can shift local round times by up to an hour; every other gap is at least one step.
        for (let i = 1; i < ticks.length; i++) assert.ok(ticks[i] - ticks[i - 1] >= step || zone === 'Europe/London', why);
      }
    }
  }
});
test('the first label stays on the window start when it is a round time', () => {
  const startMs = Date.parse('2026-10-19T15:00:00Z');
  for (const widthPx of [0, 400, 612, 800, 1000, 1190]) {
    assert.equal(tickOffsets(36, widthPx, 16, { startMs, widthMs: 5 * MIN, zone: 'UTC' })[0], 0, String(widthPx));
    assert.equal(tickOffsets(36, widthPx, 21, { startMs, widthMs: 5 * MIN, zone: 'UTC' })[0], 0, String(widthPx));
  }
  // Six labels at the e2e shape (three hours, five minute bars): 15:00, 15:30 ... 17:30.
  assert.deepEqual(labelsFor('2026-10-19T15:00:00Z', 5 * MIN, 36, 1000), ['15:00', '15:30', '16:00', '16:30', '17:00', '17:30']);
});
test('a start on a whole hour keeps its label when the step is a larger round step', () => {
  const startMs = Date.parse('2026-10-19T15:00:00Z');
  // 400 px with the year form: a step of 15 slots, so 2 hours; 15:00 is not a 2 hour mark but it is the window start.
  const offsets = tickOffsets(36, 400, 21, { startMs, widthMs: 5 * MIN, zone: 'UTC' });
  assert.deepEqual(offsets, [0, 24]);
  assert.deepEqual(offsets.map((k) => hhmm(startMs + k * 5 * MIN)), ['15:00', '17:00']);
});
