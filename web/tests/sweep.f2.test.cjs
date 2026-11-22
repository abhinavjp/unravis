const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');
const { compile } = require('svelte/compiler');
const { render } = require('svelte/server');
const { loadTypeScript } = require('./helpers/loadTs.cjs');

// UI sweep, package F2 (analysis screens and time zones). Logic tests for the zone-aware date boxes, server-side renders of the components whose output
// can be read in node, and source pins for the rest. Every name carries its finding id. Everything here is synthetic.
const root = path.resolve(__dirname, '..');
const repo = path.resolve(root, '..');
const read = (relative) => fs.readFileSync(path.join(root, relative), 'utf8');
const zi = loadTypeScript('src/lib/zonedInput.ts');
const time = loadTypeScript('src/lib/time.ts');

function loadComponent(file, resolve) {
  const full = path.join(root, file);
  const compiled = compile(fs.readFileSync(full, 'utf8'), { filename: path.basename(file), generate: 'server' });
  const js = ts.transpileModule(compiled.js.code, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText;
  const module = { exports: {} };
  const nodeRequire = createRequire(full);
  new Function('require', 'module', 'exports', js)((request) => resolve(request) ?? nodeRequire(request), module, module.exports);
  return module.exports.default;
}

// ---------------------------------------------------------------------------------------------------------------------
// SWEEP-F2-01: the When date and time boxes follow the header zone ("Times in").
// ---------------------------------------------------------------------------------------------------------------------
test('SWEEP-F2-01: a box shows the wall clock of the chosen zone, with the offset of that very day', () => {
  const summer = '2026-10-20T09:00:00Z', winter = '2026-11-22T09:00:00.123Z';
  assert.equal(zi.toZonedInput(summer, 'UTC'), '2026-10-20T09:00:00.000');
  assert.equal(zi.toZonedInput(summer, 'Europe/London'), '2026-10-20T10:00:00.000', 'London is on summer time on 20 Oct');
  assert.equal(zi.toZonedInput(winter, 'Europe/London'), '2026-11-22T09:00:00.123', 'and on winter time on 22 Nov, with the milliseconds kept');
  assert.equal(zi.toZonedInput(summer, 'Asia/Kolkata'), '2026-10-20T14:30:00.000');
  assert.equal(zi.toZonedInput(summer, 'Australia/Sydney'), '2026-10-20T20:00:00.000');
  assert.equal(zi.toZonedInput('2026-10-20T20:30:00Z', 'Asia/Kolkata'), '2026-10-21T02:00:00.000', 'the date moves with the zone');
  assert.deepEqual(zi.toZonedParts(summer, 'Europe/London'), { date: '2026-10-20', time: '10:00:00.000' });
  assert.equal(zi.toZonedInput('not a time', 'Europe/London'), '');
  assert.equal(zi.toZonedInput(summer, 'Not/AZone'), '2026-10-20T09:00:00.000', 'an unknown zone reads as UTC');
});

test('SWEEP-F2-01: a London user who types 10:00 gets 10:00 London time, not 10:00 UTC', () => {
  assert.equal(zi.fromZonedParts('2026-10-20', '10:00', 'Europe/London'), '2026-10-20T09:00:00.000Z', 'summer time: one hour ahead of UTC');
  assert.equal(zi.fromZonedParts('2026-11-22', '10:00', 'Europe/London'), '2026-11-22T10:00:00.000Z', 'winter time: the same as UTC');
  assert.equal(zi.fromZonedParts('2026-10-20', '10:00', 'Asia/Kolkata'), '2026-10-20T04:30:00.000Z');
  assert.equal(zi.fromZonedParts('2026-10-20', '10:00', 'Australia/Sydney'), '2026-10-19T23:00:00.000Z', 'Sydney is on summer time (UTC+11) from 4 Oct');
  assert.equal(zi.fromZonedParts('2026-10-20', '10:00', 'UTC'), '2026-10-20T10:00:00.000Z');
  assert.equal(zi.fromZonedParts('2026-10-20', '10:00:30.5', 'UTC'), '2026-10-20T10:00:30.500Z', 'a short fraction is padded');
  assert.equal(zi.fromZonedParts('2026-10-20', '10:00', 'Not/AZone'), '2026-10-20T10:00:00.000Z');
});

test('SWEEP-F2-01: clock changes. A skipped time is refused, a repeated time takes its first occurrence', () => {
  // London: clocks go forward at 01:00 UTC on 29 Mar 2026 (01:00 to 02:00 does not exist) and back at 01:00 UTC on 25 Oct 2026 (01:00 to 02:00 happens twice).
  assert.throws(() => zi.fromZonedParts('2026-03-29', '01:30', 'Europe/London'), /clocks change/);
  assert.equal(zi.fromZonedParts('2026-03-29', '00:59:59.999', 'Europe/London'), '2026-03-29T00:59:59.999Z');
  assert.equal(zi.fromZonedParts('2026-03-29', '02:00', 'Europe/London'), '2026-03-29T01:00:00.000Z', 'the first real minute after the gap');
  assert.equal(zi.fromZonedParts('2026-10-25', '01:30', 'Europe/London'), '2026-10-25T00:30:00.000Z', 'the first 01:30 (still summer time)');
  assert.equal(zi.fromZonedParts('2026-10-25', '00:30', 'Europe/London'), '2026-10-24T23:30:00.000Z');
  assert.equal(zi.fromZonedParts('2026-10-25', '02:00', 'Europe/London'), '2026-10-25T02:00:00.000Z');
  // New York: forward on 8 Mar 2026 at 02:00, back on 1 Nov 2026 at 02:00.
  assert.throws(() => zi.fromZonedParts('2026-03-08', '02:30', 'America/New_York'), /clocks change/);
  assert.equal(zi.fromZonedParts('2026-11-01', '01:30', 'America/New_York'), '2026-11-01T05:30:00.000Z');
  // Sydney (southern summer): forward on 4 Oct 2026 at 02:00, back on 5 Apr 2026 at 03:00.
  assert.throws(() => zi.fromZonedParts('2026-10-04', '02:30', 'Australia/Sydney'), /clocks change/);
  assert.equal(zi.fromZonedParts('2026-04-05', '02:30', 'Australia/Sydney'), '2026-04-04T15:30:00.000Z');
  // The box value of a repeated hour reads back to the first occurrence, and any other moment round-trips exactly.
  assert.equal(zi.toZonedInput('2026-10-25T01:30:00Z', 'Europe/London'), '2026-10-25T01:30:00.000', 'the second 01:30 is shown as 01:30');
});

test('SWEEP-F2-01: nonsense is refused with words, never turned into a different date', () => {
  assert.throws(() => zi.fromZonedParts('2026-02-30', '10:00', 'UTC'), /not a valid time/);
  assert.throws(() => zi.fromZonedParts('2026-10-20', '24:00', 'UTC'), /not a valid time/);
  assert.throws(() => zi.fromZonedParts('2026-10-20', '10:60', 'UTC'), /not a valid time/);
  assert.throws(() => zi.fromZonedParts('', '10:00', 'UTC'), /valid date and time/);
  assert.throws(() => zi.fromZonedParts('2026-10-20', '', 'UTC'), /valid date and time/);
  assert.throws(() => zi.fromZonedParts('20-10-2026', '10:00', 'UTC'), /valid date and time/);
});

test('SWEEP-F2-01: every moment across clock changes survives a trip through the box and back, in every header zone', () => {
  const zones = ['UTC', 'Europe/London', 'Europe/Dublin', 'Asia/Kolkata', 'Australia/Sydney', 'America/New_York'];
  const starts = ['2026-03-07T00:00:00Z', '2026-03-28T00:00:00Z', '2026-04-03T00:00:00Z', '2026-10-03T00:00:00Z', '2026-10-24T00:00:00Z', '2026-10-31T00:00:00Z'];
  let checked = 0, repeated = 0;
  for (const zone of zones) {
    for (const start of starts) {
      for (let step = 0; step < 4 * 24 * 3; step++) {
        const at = Date.parse(start) + step * 15 * 60_000 + 123;
        const box = zi.toZonedInput(at, zone);
        const back = Date.parse(zi.fromZonedParts(box.slice(0, 10), box.slice(11), zone));
        if (back === at) { checked++; continue; }
        // The only way back differs is the hour that happens twice: the second pass reads as the first, one clock change earlier.
        assert.ok(back < at && (at - back) % (15 * 60_000) === 0 && at - back <= 2 * 3_600_000, zone + ' ' + new Date(at).toISOString() + ' came back as ' + new Date(back).toISOString());
        repeated++;
      }
    }
  }
  assert.ok(checked > 3000, 'most moments round-trip exactly (' + checked + ')');
  assert.ok(repeated > 0, 'the sweep did reach a repeated hour');
});

test('SWEEP-F2-01: the zone name beside a box is the name on that day, so summer and winter differ', () => {
  assert.equal(time.zoneAbbreviation('Europe/London', '2026-10-20T08:00:00Z'), 'GMT+1');
  assert.equal(time.zoneAbbreviation('Europe/London', '2026-11-22T08:00:00Z'), 'GMT+0', 'winter London reads GMT+0 in this app');
  assert.equal(time.zoneAbbreviation('Asia/Kolkata', '2026-10-20T08:00:00Z'), 'GMT+5:30');
});

test('SWEEP-F2-01: the When group reads and writes in the header zone and says so', () => {
  const when = read('src/components/filters/WhenGroup.svelte');
  assert.match(when, /import \{ fromZonedParts, toZonedInput \} from '\.\.\/\.\.\/lib\/zonedInput'/);
  assert.match(when, /import \{ TIME_ZONE_OPTIONS, timeZone \} from '\.\.\/\.\.\/lib\/timeZone\.svelte'/);
  assert.match(when, /query\[field\] = fromZonedParts\(datePart, timePart, zone, query\[field\]\)/);
  assert.match(when, /const localValue = \(value: string\) => toZonedInput\(value, zone\)/);
  assert.match(when, /Times are in <strong>\{zoneLabel\}<\/strong>/);
  assert.doesNotMatch(when, /browser time zone|resolvedOptions|fromLocalParts|toLocalDatePart|toLocalTimePart/, 'nothing reads the browser zone any more');
  // Each of the four boxes names its own zone at its own date.
  for (const field of ['start', 'end', 'baselineStart', 'baselineEnd']) assert.match(when, new RegExp('zone=\\{zoneAt\\(query\\.' + field + '\\)\\}'));
  const field = read('src/components/DateTimeField.svelte');
  assert.match(field, /Time\{#if zone\}\{' '\}\(\{zone\}\)\{\/if\}/, 'the zone follows the word Time after a space (BL-495: the 24-hour box)');
  // The query itself stays in UTC and the raw UTC echo stays under each pair.
  assert.equal((when.match(/class="utc-echo"/g) ?? []).length, 2);
});

// ---------------------------------------------------------------------------------------------------------------------
// SWEEP-F2-02: the All operations table names the zone at the rows' own date.
// ---------------------------------------------------------------------------------------------------------------------
const zoneState = { zone: 'Europe/London' };
const stateLabel = (value) => value;
const RequestTable = loadComponent('src/components/RequestTable.svelte', (request) => ({
  '../lib/presentation': { stateLabel },
  '../lib/time': time,
  '../lib/timeZone.svelte': { timeZone: zoneState },
  '../lib/format': { formatCount: (value) => String(value) }
})[request]);
const row = (id, initiatedAt) => ({ sourceId: 's1', id, controller: 'Payroll', action: 'Run', url: '/payroll/run', status: 200, outcome: 'success', state: 'valid', durationMs: 52, apiTimeMs: 30, auditCreateTimeMs: 5, userId: 7, uiVersion: '2.0', initiatedAt });
const tableHtml = (rows) => render(RequestTable, { props: { rows, sourceLabels: new Map([['s1', 'Demo East']]), loading: false, pageNumber: 1, nextCursor: '', truncated: false, recordId: '', onFirst() {}, onNext() {}, onInspect() {}, onContext() {} } }).body;

test('SWEEP-F2-02: the heading says GMT+1 over London summer rows, whatever day the clock says it is', () => {
  const html = tableHtml([row('a', '2026-10-20T08:59:52Z'), row('b', '2026-10-20T08:55:00Z')]);
  assert.match(html, /Initiated \(GMT\+1\)/, 'summer rows are one hour ahead');
  assert.doesNotMatch(html, /Initiated \(GMT\+0\)/);
  assert.match(html, /20 Oct 09:59:52/, 'the cell shows the London clock without a suffix');
  const winter = tableHtml([row('a', '2026-12-01T10:00:00Z')]);
  assert.match(winter, /Initiated \(GMT\+0\)/);
});

test('SWEEP-F2-02: rows that straddle a clock change name their zone in each cell and not in the heading', () => {
  const html = tableHtml([row('a', '2026-10-24T23:30:00Z'), row('b', '2026-10-25T02:30:00Z')]);
  assert.doesNotMatch(html, /Initiated \(/);
  assert.match(html, /25 Oct 00:30:00 GMT\+1/);
  assert.match(html, /25 Oct 02:30:00 GMT\+0/);
});

test('SWEEP-F2-10: the two row buttons are one size, never wrap, and an empty Failures tab says so', () => {
  const html = tableHtml([row('a', '2026-10-20T08:59:52Z')]);
  assert.match(html, /class="row-actions[^"]*"><button class="secondary-button"[^>]*>Nearby activity<\/button><button class="secondary-button payload-button"/);
  assert.doesNotMatch(html, /quiet-button payload-button/);
  const style = read('src/components/RequestTable.svelte');
  assert.match(style, /\.row-actions :global\(button\) \{ white-space: nowrap;/);
  const evidence = read('src/components/EvidenceView.svelte');
  assert.match(evidence, /const noFailures = \$derived\(activeTab === 'failures' && failedTotal === 0 && !drillActive\)/);
  assert.match(evidence, /const showHistory = \$derived\(\(activeTab === 'failures' && !noFailures\)/);
  assert.match(evidence, /\{#if noFailures\}<p class="failure-none" role="status"><strong>No failed requests in this period\.<\/strong>/);
});

// ---------------------------------------------------------------------------------------------------------------------
// SWEEP-F2-04: a stale run says so once, in space the bar always has.
// ---------------------------------------------------------------------------------------------------------------------
test('SWEEP-F2-04: one stale message (in the bar), and the bar reserves its place', () => {
  const app = read('src/App.svelte');
  assert.doesNotMatch(app, /The figures below are from the last run/, 'the second message is gone');
  assert.match(read('src/lib/appState.svelte.ts'), /FILTERS_CHANGED_HINT = 'Filters changed\. Click Run analysis to use the new window\.'/);
  const bar = read('src/components/SubBar.svelte');
  assert.match(bar, /<span class="run-hint" class:stale=\{results\.displayedRunIsStale && !loading\} class:reserved=\{reservedHint\} id="run-hint" role="status">\{hintText\}<\/span>/);
  const style = bar.slice(bar.indexOf('<style>'));
  assert.match(style, /@media \(min-width: 721px\)/);
  assert.match(style, /\.scope-main \.run-actions \{[^}]*position: relative[^}]*padding-bottom: 20px; margin-bottom: -14px/);
  assert.match(style, /\.scope-main \.run-hint\.reserved \{ position: absolute; right: 0; bottom: 0;/, 'the short line takes no room in the row, so it cannot wrap the bar');
  // SWEEP-F2-S3: only the short line is reserved. A validation sentence is never cut: no ellipsis and no hidden overflow anywhere in the bar.
  assert.doesNotMatch(style, /text-overflow|overflow: hidden/);
  assert.match(style, /\.scope-main \.run-hint:not\(\.reserved\) \{[^}]*overflow-wrap: anywhere/);
  assert.match(bar, /const reservedHint = \$derived\(hintText === '' \|\| hintText === FILTERS_CHANGED_HINT\)/);
  assert.doesNotMatch(read('src/lib/appState.svelte.ts'), /autoRunDecision|createCoalescer/, 'the manual run stays: nothing wires the auto-run helpers into the page');
});

// ---------------------------------------------------------------------------------------------------------------------
// SWEEP-F2-07/08/09: tables that fit their boxes.
// ---------------------------------------------------------------------------------------------------------------------
test('SWEEP-F2-07: the group table folds the reference count into Current requests and keeps one line per fact', () => {
  const src = read('src/components/ComparisonTable.svelte');
  assert.doesNotMatch(src, /<th title="Reference-period requests/);
  assert.match(src, /\{formatCount\(row\.reference\.count\)\} in reference<\/small>/);
  assert.match(src, /<th aria-sort=\{sort === 'count'[^\n]*Current requests[^\n]*<Hint termId="reference-period" \/><\/th>/);
  assert.match(src, /<td colspan="14" class="table-empty">/);
  assert.equal((src.match(/<th[ >]/g) ?? []).length - (src.match(/<thead>/g) ?? []).length >= 0, true);
  assert.match(src, /<PhaseBar phases=\{row\.current\.phases\} delta=\{row\.phaseDeltaMs\} compact \/>/);
  assert.match(src, /class="table-scroll group-table-scroll scroll-cue"/);
  const style = src.slice(src.indexOf('<style>'));
  assert.match(style, /@media \(min-width: 721px\)/, 'the phone cards keep their own rules');
  assert.match(style, /td\.group-kind small\) \{ max-width: 210px; overflow: visible; white-space: normal; \}/);
  const css = read('src/app.css');
  assert.match(css, /\.table-scroll\.scroll-cue \{[\s\S]*?no-repeat local,[\s\S]*?no-repeat scroll;\s*\}/);
});

test('SWEEP-F2-07: the compact phase line keeps the three figures on one short line', () => {
  const PhaseBar = loadComponent('src/components/PhaseBar.svelte', (request) => ({ '../lib/phases': loadTypeScript('src/lib/phases.ts') })[request]);
  const phases = { auditMs: 51, apiMs: 204, payloadMs: 25.5 };
  const clean = (html) => html.replace(/<!--.*?-->/g, '');
  const full = clean(render(PhaseBar, { props: { phases } }).body);
  const compact = clean(render(PhaseBar, { props: { phases, compact: true } }).body);
  assert.match(full, /Audit write 51\.0 ms · API work 204 ms/);
  assert.match(full, /Payload write 25\.5 ms, after the user got the response/);
  assert.match(compact, /Audit write 51\.0 ms · API work 204 ms · Payload write 25\.5 ms<\/small>/);
  assert.equal((compact.match(/<small/g) ?? []).length, 1, 'one line');
  assert.match(compact, /title="Mean time per request\./, 'the tooltip still says the payload is written after the response');
});

test('SWEEP-F2-08: the episodes table drops the milliseconds and the zone suffix, names the zone once, and sits in a wider drawer', () => {
  const src = read('src/components/PatternsPanel.svelte');
  assert.match(src, /const episodeAt = \(value: string\) => formatTime\(value, \{ zone, precision: 'second', suffix: episodeZones\.size > 1 \}\)/);
  assert.match(src, /<th scope="col">Start\{episodeZones\.size > 1 \? '' : ' \(' \+ episodeZone \+ '\)'\}<\/th>/);
  assert.match(src, /onclick=\{\(\) => toggle\(episode\.index\)\}>\{episodeAt\(episode\.start\)\}<\/button>/);
  assert.match(src, /<td class="when">\{episodeAt\(episode\.end\)\}<\/td>/);
  assert.match(src, /\.patterns-table \.when, \.patterns-table tbody th \{ white-space: nowrap; \}/);
  assert.match(src, /width: min\(680px, calc\(100vw - 24px\)\)/);
  assert.match(src, /class="table-scroll scroll-cue"/);
  const patterns = loadTypeScript('src/lib/patterns.ts');
  assert.deepEqual(patterns.PATTERN_RANGES.map((range) => range.label), ['Last 15 min', 'Last hour', 'Last 4 hours', 'Last 24 hours'], 'the drawer says what the page says');
  assert.doesNotMatch(patterns.PATTERNS_TEXT.intro, /looks at the slowest/i, 'the intro no longer repeats the note under it');
});

test('SWEEP-F2-09: guide tables wrap in the page text face, and the developer note is gone from the guide index', () => {
  const guide = read('src/components/HelpGuide.svelte');
  const style = guide.slice(guide.indexOf('<style>'));
  // Desktop windows wrap in the page text face; a narrow window keeps the scrolling table the guide script turns into a named region.
  const wideRules = style.slice(style.indexOf('@media (min-width: 721px)'));
  assert.match(wideRules, /table \{ font-family: inherit; white-space: normal; \}/);
  assert.match(wideRules, /th, td \{ font-family: inherit; white-space: normal; \}/);
  assert.doesNotMatch(style.slice(0, style.indexOf('@media (min-width: 721px)')), /table \{[^}]*white-space: normal/);
  const index = fs.readFileSync(path.join(repo, 'docs', 'user', 'index.md'), 'utf8');
  assert.doesNotMatch(index, /clean PC/i);
  assert.doesNotMatch(index, /Not yet (?:run|verified)/i);
  // No page of the user guide carries a note for the people who build the app.
  for (const file of fs.readdirSync(path.join(repo, 'docs', 'user')).filter((name) => name.endsWith('.md') && name !== 'screenshots.md')) {
    assert.doesNotMatch(fs.readFileSync(path.join(repo, 'docs', 'user', file), 'utf8'), /Not yet run on a clean PC/, file);
  }
});

// ---------------------------------------------------------------------------------------------------------------------
// SWEEP-F2-11/12/13/17/18/19/20: the smaller screens.
// ---------------------------------------------------------------------------------------------------------------------
test('SWEEP-F2-11: the zoom strip says how to zoom once, and the rest sits behind one "?"', () => {
  const src = read('src/components/ZoomStrip.svelte');
  assert.doesNotMatch(src, /<HelpNote id="drill-(?:back|investigate)"/);
  assert.match(src, /<p class="zoom-note hint">\{model\.hint\}/);
  assert.match(src, /aria-expanded=\{moreShown\} aria-controls=\{moreId\} aria-label=\{zoomCopy\.moreLabel\}/);
  // Scope, reference and as-of lines share one block, which stays in the page (hidden) so aria-controls always points at something.
  assert.match(src, /<div class="zoom-more" id=\{moreId\} hidden=\{!moreShown\}>/);
  assert.equal((src.match(/class="zoom-note"/g) ?? []).length, 5, 'five lines, all inside the "?" block');
  const { zoomCopy } = loadTypeScript('src/lib/copy/zoomCopy.ts');
  assert.equal(zoomCopy.moreLabel, 'More about zooming');
  assert.match(zoomCopy.asOfNote, /when the zoomed read finished/);
});

test('SWEEP-F2-12: the legend draws the hollow dot for the slowest request, and the diamond only when a diamond is drawn', () => {
  const src = read('src/components/Pulse.svelte');
  assert.match(src, /<i class="swatch-slowest"><\/i>Slowest request in the interval/);
  assert.match(src, /\{#if outliers\.flagged\.length\}<span><i class="swatch-max"><\/i>Far slower than usual<\/span>\{\/if\}/);
  assert.match(src, /\.hero-legend \.swatch-slowest \{[^}]*border-radius: 50%/);
});

test('SWEEP-F2-13: one name for each fact in the quality strip, Patterns and the stale-time note', () => {
  const patterns = loadTypeScript('src/lib/patterns.ts');
  assert.equal(patterns.qualityLine({ incomplete: 6, pending: 6, invalid: 2 }), '6 with missing timing, 6 pending, 2 with unusable timing');
  assert.match(read('src/components/PatternsPanel.svelte'), /\{qualityLine\(report\.quality\)\}<Hint termId="missing-timing" \/>/);
  const panel = read('src/components/IncompletePanel.svelte');
  assert.doesNotMatch(panel, /taken from the code|not measured from live data/);
  assert.match(panel, /'Default ' \+ DEFAULT_STALE_MINUTES \+ ' minutes\. Change it to suit your system\.'/);
  const css = read('src/app.css');
  assert.match(css, /\.quality-strip \{ align-items: flex-start; \}/);
  assert.match(css, /\.quality-strip > div\.quality-filter-note span \{ text-decoration: none; \}/);
});

test('SWEEP-F2-17: the record page is wide, and the saving-time callout is for slow or missing times only', () => {
  assert.match(read('src/app.css'), /\.record-view \{ max-width: 1240px; \}/);
  const src = read('src/components/RecordDetail.svelte');
  assert.match(src, /const AUDIT_NOTE_MIN_MS = 1000;/);
  assert.match(src, /\{#if plain && \(detail\.auditCreateTimeMs == null \|\| !Number\.isFinite\(detail\.auditCreateTimeMs\) \|\| detail\.auditCreateTimeMs >= AUDIT_NOTE_MIN_MS\)\}/);
});

test('SWEEP-F2-18: the heat-map legend has outlines and a value, and the day column is part of its row', () => {
  const heat = read('src/components/HourHeatmap.svelte');
  assert.match(heat, /\.hm-swatch \{[^}]*border: 1px solid var\(--control-line\)/);
  assert.match(heat, /\{GRID_TEXT\.higher\}\{#if maxText\} \(\{maxText\}\)\{\/if\}/);
  const daily = read('src/components/DailyTable.svelte');
  assert.match(daily, /\.daily-table tbody th \{[^}]*background: transparent; box-shadow: none; border-bottom: 1px solid var\(--line-soft\);/);
});

test('SWEEP-F2-19: Explain simply has words for the chart too', () => {
  const { help } = loadTypeScript('src/lib/copy/helpCopy.ts');
  assert.ok(help['pulse-chart'].plain && help['pulse-chart'].simple);
  assert.match(read('src/components/Pulse.svelte'), /<HelpNote id="pulse-chart" \/>\s*<div class="hero-stage">/);
});

test('SWEEP-F2-20: the one-liners from the small-items list', () => {
  // The KPI note says how its figure and the big KPI fit: 506 in the share, 508 in all.
  const plain = loadTypeScript('src/lib/copy/plainSummary.ts');
  const window = { start: '2026-10-20T08:45:00Z', end: '2026-10-20T09:00:00Z', zone: 'Europe/London' };
  const note = plain.plainIncomplete({ completed: 500, incomplete: 6, pending: 0, invalid: 2, window });
  assert.match(note.summary, /^6 of 506 requests \(1\.2%\) had no final result/);
  assert.match(note.summary, /Of all 508 requests, 2 had a timing that could not be used, so they are left out of the 506 above\.$/);
  // The evidence legend writes P95 as everywhere else.
  assert.doesNotMatch(read('src/lib/charts.ts'), /Current p95|Reference p95/);
  // The header's demo badge and reference time are separated.
  assert.match(read('src/app.css'), /\.mode-badge \+ \.updated-label\.reference::before \{ content: "\\00b7"/);
});

// ---------------------------------------------------------------------------------------------------------------------
// SWEEP-F2-C12 / SWEEP-F2-C13: Read SQL / Read Mongo times, list rows and day rows follow the header zone.
// ---------------------------------------------------------------------------------------------------------------------
test('SWEEP-F2-C13: a moment on a row is shown in the header zone first, with the UTC text second', () => {
  const lanes = loadTypeScript('src/lib/lanes.ts');
  assert.equal(lanes.zoneThenUtc('2026-10-18T13:00:00Z', 'Europe/London'), '18 Oct 2026 14:00 GMT+1 (2026-10-18 13:00 UTC)');
  assert.equal(lanes.zoneThenUtc('2026-12-18T13:00:00Z', 'Europe/London'), '18 Dec 2026 13:00 GMT+0 (2026-12-18 13:00 UTC)');
  assert.equal(lanes.zoneThenUtc('2026-10-18T13:00:00Z', 'UTC'), '2026-10-18 13:00 UTC');
  assert.equal(lanes.zoneThenUtc('2026-10-18T13:00:00Z', 'Asia/Kolkata'), '18 Oct 2026 18:30 GMT+5:30 (2026-10-18 13:00 UTC)');
  const step = read('src/components/sources/SourceStep.svelte');
  assert.match(step, /<span class="name">\{zoneThenUtc\(day\.incident\.start, timeZone\.zone\)\}<\/span>/);
  assert.match(read('src/components/sources/LaneResultView.svelte'), /\{zoneThenUtc\(lane\.onset, timeZone\.zone\)\}/);
});

test('SWEEP-F2-C13: the case list rows, erased lines and the start screen follow the header zone', () => {
  const start = loadTypeScript('src/lib/startScreen.ts');
  const c = { id: '3f9a27c1aaaaaaaaaaaaaaaaaa', title: 'Checkout slowdown', created: '2026-10-17T09:00:00Z', imports: 1 };
  assert.equal(start.caseRow(c).created, '17 Oct 2026 09:00 UTC', 'UTC when no zone is named');
  assert.equal(start.caseRow(c, 'Europe/London').created, '17 Oct 2026 10:00 GMT+1', 'the same start as the case page shows');
  const view = start.startView('none', [c], 1, [{ id: '9c01bd44bbbbbbbbbbbbbbbbbb', at: '2026-10-20T09:12:00Z' }], 'Europe/London');
  assert.equal(view.rows[0].created, '17 Oct 2026 10:00 GMT+1');
  assert.match(view.erased[0].text, /erased 20 Oct 2026 10:12 GMT\+1/);
  assert.match(read('src/components/StartScreen.svelte'), /startView\(lock, cases, pages, erased, timeZone\.zone\)/);
  assert.match(read('src/lib/components/ArchivedList.svelte'), /caseRow\(c, timeZone\.zone\)/);
});

test('SWEEP-F2-C12: the four UTC boxes say what they hold, sit two by two, and echo the time in the header zone', () => {
  const lanes = loadTypeScript('src/lib/lanes.ts');
  assert.equal(lanes.utcTextToIso('2026-10-26 10:30'), '2026-10-26T10:30:00.000Z');
  assert.equal(lanes.utcTextToIso('2026-10-26T10:30'), '2026-10-26T10:30:00.000Z');
  assert.equal(lanes.utcTextToIso('2026-02-30 10:30'), null);
  assert.equal(lanes.utcTextToIso('soon'), null);
  const runner = read('src/components/sources/LaneRunner.svelte');
  assert.equal((runner.match(/<div class="pair pair-times">/g) ?? []).length, 2, 'two rows of two');
  assert.equal((runner.match(/class="echo"/g) ?? []).length, 4);
  assert.match(runner, /if \(timeZone\.zone === 'UTC'\) return '';/);
  assert.match(runner, /placeholder=\{RUN_TEXT\.timePlaceholder\}/);
  const { RUN_TEXT } = loadTypeScript('src/lib/copy/sourcesCopy.ts');
  assert.equal(RUN_TEXT.timePlaceholder, 'yyyy-mm-dd hh:mm', 'a format, not a date that looks filled in');
  assert.equal(RUN_TEXT.incidentStart, 'Incident starts (UTC)', 'the labels the other screens and specs use stay');
  assert.match(runner, /\.pair-times \{ grid-template-columns: repeat\(2, minmax\(0, 1fr\)\); \}/);
});

// ---------------------------------------------------------------------------------------------------------------------
// Round 2 (review S3, S6, S7, N2).
// ---------------------------------------------------------------------------------------------------------------------
test('SWEEP-F2-N2: the open More filters panel has one scroller, the panel body', () => {
  const css = read('src/app.css').replace(/\/\*[\s\S]*?\*\//g, '');
  assert.doesNotMatch(css, /\.scope:has\(\.more-filters\[open\]\)/);
  assert.match(css, /\.more-filters\[open\] > \.more-body \{ max-height: max\(200px, calc\(100dvh - var\(--header-h\) - 400px\)\); overflow-y: auto;/);
});

test('SWEEP-F2-S6: editing a box that holds the second pass of a repeated hour keeps the second pass', () => {
  // London, 25 Oct 2026: 01:00 UTC the clocks go back, so 01:00 to 02:00 happens twice (BST at 00:30Z, GMT at 01:30Z).
  const second = '2026-10-25T01:30:00.000Z';
  assert.equal(zi.toZonedInput(second, 'Europe/London'), '2026-10-25T01:30:00.000');
  // An edit that leaves the wall time as it was, or changes only the seconds or the minutes inside the repeated hour, keeps GMT.
  assert.equal(zi.fromZonedParts('2026-10-25', '01:30:00.000', 'Europe/London', second), second);
  assert.equal(zi.fromZonedParts('2026-10-25', '01:30:05', 'Europe/London', second), '2026-10-25T01:30:05.000Z');
  assert.equal(zi.fromZonedParts('2026-10-25', '01:45', 'Europe/London', second), '2026-10-25T01:45:00.000Z');
  // Without a previous value, or when the previous value was the first pass or another day, the first occurrence is still taken.
  assert.equal(zi.fromZonedParts('2026-10-25', '01:30', 'Europe/London'), '2026-10-25T00:30:00.000Z');
  assert.equal(zi.fromZonedParts('2026-10-25', '01:30', 'Europe/London', '2026-10-25T00:30:00.000Z'), '2026-10-25T00:30:00.000Z');
  assert.equal(zi.fromZonedParts('2026-10-25', '01:30', 'Europe/London', '2026-10-24T12:00:00.000Z'), '2026-10-25T00:30:00.000Z');
  // Moving out of the repeated hour reads normally, whatever the previous value was.
  assert.equal(zi.fromZonedParts('2026-10-25', '03:00', 'Europe/London', second), '2026-10-25T03:00:00.000Z');
  assert.equal(zi.fromZonedParts('2026-10-25', '00:15', 'Europe/London', second), '2026-10-24T23:15:00.000Z');
  // A date change to another day with an ordinary 01:30 is not affected.
  assert.equal(zi.fromZonedParts('2026-10-26', '01:30', 'Europe/London', second), '2026-10-26T01:30:00.000Z');
  // The same rule in New York (1 Nov 2026, 01:00-02:00 twice) and Sydney (5 Apr 2026, 02:00-03:00 twice).
  assert.equal(zi.fromZonedParts('2026-11-01', '01:30', 'America/New_York', '2026-11-01T06:30:00.000Z'), '2026-11-01T06:30:00.000Z');
  assert.equal(zi.fromZonedParts('2026-04-05', '02:30', 'Australia/Sydney', '2026-04-04T16:30:00.000Z'), '2026-04-04T16:30:00.000Z');
  // The window start/end that a person never touched cannot drift: every instant in the repeated hour survives an edit of its own box.
  for (const at of [Date.parse('2026-10-25T00:00:00Z'), Date.parse('2026-10-25T00:30:00Z'), Date.parse('2026-10-25T01:00:00Z'), Date.parse('2026-10-25T01:59:00Z'), Date.parse('2026-10-25T02:00:00Z')]) {
    const iso = new Date(at).toISOString();
    const box = zi.toZonedInput(iso, 'Europe/London');
    assert.equal(zi.fromZonedParts(box.slice(0, 10), box.slice(11), 'Europe/London', iso), iso, iso);
  }
  assert.match(read('src/components/filters/WhenGroup.svelte'), /fromZonedParts\(datePart, timePart, zone, query\[field\]\)/);
});
