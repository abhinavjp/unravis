const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

// Slow call screen: the three-lane chart component, checked as source text.

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

const { DRILL_TEXT } = loadTypeScript('src/lib/drill/drillCopy.ts');
const source = fs.readFileSync(path.join(root, 'src/components/CallLanes.svelte'), 'utf8');

test('the three lanes are drawn with their own tokens', () => {
  for (const lane of ['audit', 'iis', 'sql']) {
    assert.ok(source.includes(`data-lane={name}`) || source.includes(`data-lane="${lane}"`), 'data-lane attribute');
    assert.ok(source.includes(`var(--lane-${lane})`), `uses --lane-${lane}`);
    assert.ok(new RegExp(`${lane}:\\s*'var\\(--lane-${lane}\\)'`).test(source), `colour map has ${lane}`);
  }
  assert.match(source, /name: 'audit' as LaneName/);
  assert.match(source, /name: 'iis' as LaneName/);
  assert.match(source, /name: 'sql' as LaneName/);
  assert.match(source, /data-testid="scall-lane-\{name\}"/);
  assert.match(source, /data-testid="scall-lanes"/);
  assert.match(source, /aria-busy=\{loading\}/);
});

test('every part of the chart is present', () => {
  for (const needle of [
    'class="gap"', 'class="thiscall"', 'class="bar"', 'class="shape"', 'scall-readout', 'scall-legend', 'scall-db-source',
    'preserveAspectRatio="none"', '<pattern', 'role="button"', 'tabindex="0"', 'aria-live="off"', 'data-key'
  ]) {
    assert.ok(source.includes(needle), `has ${needle}`);
  }
  assert.match(source, /Math\.max\(.*, 2 \* pxX\)/, 'the call column is at least 2 px wide');
  assert.match(source, /patternUnits="userSpaceOnUse"/);
  assert.equal((source.match(/<pattern /g) ?? []).length, 1, 'one shared pattern');
  assert.match(source, /instanceCounter/, 'pattern id is unique per instance');
});

test('hover, pin and keyboard are wired', () => {
  assert.match(source, /import \{[^}]*readoutAt[^}]*\} from '\.\.\/lib\/drill\/callReadout'/);
  assert.match(source, /onpointermove=\{onMove\}/);
  assert.match(source, /onpointerleave=\{onLeave\}/);
  assert.match(source, /onclick=\{onClick\}/);
  assert.match(source, /emit\(readoutFor\(hoverMs\), false\)/);
  assert.match(source, /emit\(readoutFor\(pinnedMs\), true\)/);
  assert.match(source, /onReadout\(value, pinned\)/);
  assert.match(source, /'ArrowLeft'/);
  assert.match(source, /'ArrowRight'/);
  assert.match(source, /e\.key === 'Enter' \|\| e\.key === ' '/);
  assert.match(source, /onSelect\(key\)/);
});

test('the database lane never draws a flat zero line when it has no shape', () => {
  assert.match(source, /db\.shape === 'none'/);
  assert.match(source, /db\.shape === 'markers'/);
  assert.match(source, /<p class="scall-note">\{db\.short\}<\/p>/);
  assert.match(source, /hatchAll\(name\)/);
});

test('fixed heights: no scrolling inside the lanes', () => {
  assert.doesNotMatch(source, /overflow(-y)?:\s*(auto|scroll)/);
  assert.doesNotMatch(source, /max-height/);
  assert.match(source, /\.scall-lane \{[^}]*height: var\(--scall-lane-h, 96px\);[^}]*overflow: hidden;/);
  assert.match(source, /\.scall-lanes \{[^}]*overflow: visible;/);
});

test('a missing number reads "not recorded", and the old chart is not used', () => {
  assert.match(source, /DRILL_TEXT\.notRecorded/);
  assert.doesNotMatch(source, /TracingChart/);
  assert.doesNotMatch(source, /\?\? 0\b[^;]*DRILL_TEXT\.read/);
});

test('every copy key the component uses exists', () => {
  const used = new Set([...source.matchAll(/DRILL_TEXT\.(\w+)/g)].map((m) => m[1]));
  assert.ok(used.size >= 20, `uses ${used.size} keys`);
  for (const key of used) assert.ok(key in DRILL_TEXT, `DRILL_TEXT.${key} exists`);
  for (const key of [
    'laneApi', 'laneIis', 'laneDb', 'laneSource', 'laneSlow', 'laneNothingSlow', 'laneShape', 'laneMoreBars',
    'readTitle', 'readApi', 'readP95', 'readIis', 'readRefused', 'readDb', 'readDbNone', 'readHover', 'readPinned',
    'legendWidth', 'legendShape', 'legendHatched', 'legendCall'
  ]) assert.ok(used.has(key), `component uses ${key}`);
});

test('the component holds no sentence of its own', () => {
  const markup = source.replace(/<script[\s\S]*?<\/script>/g, '').replace(/<style[\s\S]*?<\/style>/g, '');
  let flat = markup;
  for (let i = 0; i < 4; i += 1) flat = flat.replace(/\{[^{}]*\}/g, ' ');
  const text = flat.replace(/<[^>]*>/g, ' ').replace(/\s+/g, ' ').trim();
  assert.equal(text.replace(/[·]/g, '').trim(), '', `leftover text: ${text}`);
});

// ---- the throughput measure is labelled everywhere it is drawn (W13-01, W13-03) ----

test('the database lane draws and reads its own measure, not a renamed concurrency', () => {
  assert.match(source, /import \{[^}]*cellValue[^}]*\} from '\.\.\/lib\/drill\/callLanes'/);
  assert.match(source, /lanes\.sql\.measure === 'batch-rate'/);
  // shape, gaps and readout all go through the measure-aware value
  assert.match(source, /const valueOf = \(name: LaneName, cell: LaneCell\)/);
  assert.match(source, /if \(valueOf\(name, cell\) === null\)/);
  // unit-labelled header, readout and legend
  assert.match(source, /DRILL_TEXT\.laneRateShape\(/);
  assert.match(source, /DRILL_TEXT\.readDbRate\(/);
  assert.match(source, /DRILL_TEXT\.readDbRateNone/);
  assert.match(source, /DRILL_TEXT\.readDbNoAtOnce/);
  assert.match(source, /DRILL_TEXT\.legendRate/);
  assert.match(source, /DRILL_TEXT\.unitBatches/);
});

test('a throughput lane never claims "nothing slow here": call times are marked unavailable', () => {
  assert.match(source, /name === 'sql' && sqlRate \? DRILL_TEXT\.laneNoCallTimes/);
  assert.match(source, /DRILL_TEXT\.laneNoCallTimes : slowCount\(name\) > 0/);
});

test('no raw HTML is ever interpolated', () => {
  assert.doesNotMatch(source, /\{@html/);
});

// ---- UX-2: the chart help follows the lane's real measure (server-rendered, real data path) ----
// The lanes are built by buildLane from a shaped tracing answer and the db view by dbState,
// exactly as the screen builds them; the component's own sqlRate derivation drives the help.

const { compile } = require('svelte/compiler');
const { render } = require('svelte/server');

const ssrCache = new Map();
function loadModule(relativePath) {
  const filename = path.join(root, relativePath);
  if (ssrCache.has(filename)) return ssrCache.get(filename).exports;
  let src = fs.readFileSync(filename, 'utf8');
  if (filename.endsWith('.svelte')) {
    src = compile(src, { filename: path.basename(filename), generate: 'server' }).js.code;
  }
  const output = ts.transpileModule(src, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022, esModuleInterop: true } }).outputText;
  const module = { exports: {} };
  ssrCache.set(filename, module);
  const nodeRequire = createRequire(filename);
  const load = (request) => {
    if (/^\.\.?\//.test(request)) {
      for (const candidate of [request, request + '.ts', request + '.svelte.ts']) {
        const full = path.join(path.dirname(filename), candidate);
        if (fs.existsSync(full) && fs.statSync(full).isFile()) return loadModule(path.relative(root, full));
      }
    }
    return nodeRequire(request);
  };
  new Function('require', 'module', 'exports', '$state', output)(load, module, module.exports, (v) => v);
  return module.exports;
}

const { buildLane } = loadModule('src/lib/drill/callLanes.ts');
const { dbState } = loadModule('src/lib/drill/dbState.ts');
const fmt = { date: (iso) => iso.slice(0, 10), time: (iso) => iso.slice(11, 16) };
const WS = Date.parse('2026-11-11T10:00:00Z');
const WE = Date.parse('2026-11-11T11:00:00Z');
const win = { axisStart: '2026-11-11T10:00:00Z', bucketSeconds: 60 };
const bucketAt = (i, extra) => ({ start: new Date(WS + i * 60000).toISOString(), count: 4, ...extra });
// A Blackbox host-counter SQL lane: recorded batch COUNTS, no busy time (the real server shape).
const blackboxSqlIn = { key: 'sql', state: 'events', reasonCode: 'blackbox', buckets: [bucketAt(0, { count: 6000 }), bucketAt(1, { count: 3000 })], events: [] };
// A Query Store SQL lane: measured busy time, the concurrency measure.
const qsSqlIn = { key: 'sql', state: 'events', reasonCode: 'held-sql', buckets: [bucketAt(0, { busyMs: 12000 }), bucketAt(1, { busyMs: 6000 })], events: [] };
const auditIn = { key: 'audit', state: 'events', buckets: [bucketAt(0, { busyMs: 24000, p95Ms: 900 })], events: [] };
const iisIn = { key: 'iis', state: 'events', buckets: [bucketAt(0, { busyMs: 18000 })], events: [] };

function renderLanes(sqlIn, dbOver, shownOver) {
  const lanes = {
    audit: buildLane(auditIn, 'audit', win, WS, WE),
    iis: buildLane(iisIn, 'iis', win, WS, WE),
    sql: buildLane(sqlIn, 'sql', win, WS, WE)
  };
  const db = dbState({
    laneReasonCode: sqlIn.reasonCode ?? '', laneHasShape: lanes.sql.hasShape, laneAsOf: null,
    laneSince: '2026-10-20T08:30:00Z', laneCadenceSeconds: 15, blockSeconds: null,
    queryStore: { state: 'on' }, hasEvents: false, windowStart: WS, windowEnd: WE, ...dbOver
  }, fmt);
  const Lanes = loadModule('src/components/CallLanes.svelte').default;
  const { body } = render(Lanes, {
    props: {
      lanes, start: WS, end: WE, stepSeconds: 60,
      call: { startMs: WS + 600000, endMs: WS + 602400 },
      db, markers: [], shown: { api: true, iis: true, db: true, ...shownOver },
      zone: 'UTC', loading: false, selectedKey: null, onSelect() {}, onReadout() {}
    }
  });
  return body;
}

// Svelte's SSR adds comment markers and scoping classes; read the text, not the markup.
const plain = (html) => html.replace(/<!--[\s\S]*?-->/g, '').replace(/<[^>]*>/g, '').replace(/\s+/g, ' ').trim();
const readLine = (html) => plain(/<p class="scall-readline[^"]*"[^>]*>([\s\S]*?)<\/p>/.exec(html)?.[1] ?? '');
const legendItems = (html) => {
  const ul = /<ul class="scall-legend[^"]*"[^>]*>([\s\S]*?)<\/ul>/.exec(html)?.[1] ?? '';
  return [...ul.matchAll(/<li[^>]*>([\s\S]*?)<\/li>/g)].map((m) => plain(m[1]));
};

test('UX-2: a shown batch-rate database lane gets rate-aware chart help (real render)', () => {
  const html = renderLanes(blackboxSqlIn);
  assert.equal(readLine(html), DRILL_TEXT.chartReadLineRate, 'the read line names the rate measure');
  const items = legendItems(html);
  assert.ok(items.includes(`${DRILL_TEXT.helpUp} ${DRILL_TEXT.helpUpDbRate}`), 'the up help carries the database-lane sentence: ' + JSON.stringify(items));
  assert.ok(items.includes(DRILL_TEXT.legendRate), 'the rate legend line stays');
});

test('UX-2: an at-once database lane keeps the exact old chart help (real render)', () => {
  const html = renderLanes(qsSqlIn);
  assert.equal(readLine(html), DRILL_TEXT.chartReadLine);
  const items = legendItems(html);
  assert.ok(items.includes(DRILL_TEXT.helpUp), 'the up help is unchanged');
  assert.equal(items.some((t) => t.includes(DRILL_TEXT.helpUpDbRate ?? '§none§')), false, 'no rate sentence');
  assert.equal(items.includes(DRILL_TEXT.legendRate), false, 'no rate legend line');
});

test('UX-2: a rate lane hidden by the source switches keeps the at-once help (real render)', () => {
  const html = renderLanes(blackboxSqlIn, {}, { db: false });
  assert.equal(readLine(html), DRILL_TEXT.chartReadLine);
  const items = legendItems(html);
  assert.ok(items.includes(DRILL_TEXT.helpUp));
  assert.equal(items.some((t) => t.includes(DRILL_TEXT.helpUpDbRate ?? '§none§')), false);
});
