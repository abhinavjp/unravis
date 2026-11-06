const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

// The audit lane's hero layer: the per-slot percentile line, the dashed bridge, the slowest-request marks
// and the sentence about the flagged diamonds. Pure module, run through the same TypeScript loader as timelineFrame.test.cjs.

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
      // The slim test snapshot has no chartTime.ts; canvas.ts only needs it for the axis ticks, which the hero layer never calls.
      if (path.basename(request) === 'chartTime') return { dayTicks: () => [], roundTicks: () => [], zoneOffsetMs: () => 0 };
    }
    return nodeRequire(request);
  };
  new Function('require', 'module', 'exports', output)(load, module, module.exports);
  return module.exports;
}

const { HERO_TEXT, heroLayout, heroSentence } = loadTypeScript('src/lib/tracing/auditHero.ts');
const { lintScreenStrings } = loadTypeScript('src/lib/copy/copylint.ts');
const { deck } = loadTypeScript('src/lib/copy/deck.ts');

const T0 = Date.parse('2026-10-01T00:00:00Z');
const grid = { firstMs: T0, widthMs: 60000 };
const bucketAt = (slot, extra = {}) => ({ start: new Date(T0 + slot * 60000).toISOString(), count: 5, errors: 0, ...extra });

const isNiceCeiling = (value) => {
  const mantissa = value / Math.pow(10, Math.floor(Math.log10(value)));
  return [1, 2, 2.5, 5].some((m) => Math.abs(mantissa - m) < 1e-9);
};

test('a gap in the percentile values splits the line and is crossed by one dashed bridge', () => {
  // Six slots; P95 on slots 0, 1, 3, 4, 5 and nothing on slot 2.
  const buckets = [0, 1, 3, 4, 5].map((i) => bucketAt(i, { p95Ms: 200 + i * 50, timedCount: 4 }));
  const layout = heroLayout(buckets, grid, { from: 0, to: 6 }, 'p95');
  assert.equal(layout.line.length, 2, 'two runs: slots 0-1 and slots 3-5');
  assert.equal(layout.line[0].split(' ').length, 2);
  assert.equal(layout.line[1].split(' ').length, 3);
  assert.equal(layout.bridge.length, 1, 'one dashed bridge');
  const bridgePoints = layout.bridge[0].split(' ');
  assert.equal(bridgePoints.length, 3, 'the bridge covers slots 1 to 3, end points included');
  assert.ok(bridgePoints[0].startsWith('25.00,'), 'the bridge starts at the centre of slot 1');
  assert.equal(layout.dots.length, 5, 'one dot per slot with a value');
  assert.ok(layout.maxMs >= 450, 'the axis top covers the largest value');
  assert.ok(isNiceCeiling(layout.maxMs), 'the axis top is 1, 2, 2.5 or 5 times a power of ten');
  assert.deepEqual(layout.ticks.map((t) => t.bottomPct), [0, 50, 100]);
  assert.equal(layout.ticks[0].ms, 0);
  assert.equal(layout.ticks[2].ms, layout.maxMs);
  assert.equal(layout.flaggedCount, 0);
  assert.equal(heroSentence(layout), '');
});

test('a slowest request far above the usual P95 is a flagged, labelled diamond with its value printed', () => {
  const buckets = [0, 1, 3, 4, 5].map((i) => bucketAt(i, { p95Ms: 300, timedCount: 4, maxMs: i === 4 ? 60000 : 400 }));
  const layout = heroLayout(buckets, grid, { from: 0, to: 6 }, 'p95');
  const mark = layout.marks.find((m) => m.index === 4);
  assert.equal(mark.flagged, true);
  assert.equal(mark.labelled, true);
  assert.equal(mark.text, '60 s');
  assert.equal(layout.flaggedCount, 1);
  assert.equal(layout.topFlaggedMs, 60000);
  assert.equal(layout.typical, 300);
  const sentence = heroSentence(layout);
  assert.match(sentence, /red diamond/);
  assert.match(sentence, /60 s/);
  assert.match(sentence, /^One interval holds a request far slower than usual, up to 60 s\./);
  // Every other slot keeps an unflagged, unlabelled mark.
  assert.equal(layout.marks.length, 5);
  assert.equal(layout.marks.filter((m) => !m.flagged).length, 4);

  const calm = heroLayout([0, 1, 3, 4, 5].map((i) => bucketAt(i, { p95Ms: 300, timedCount: 4, maxMs: 800 })), grid, { from: 0, to: 6 }, 'p95');
  assert.equal(calm.flaggedCount, 0);
  assert.equal(calm.topFlaggedMs, null);
  assert.equal(heroSentence(calm), '');
});

test('several flagged intervals use the plural sentence and name the slowest of them', () => {
  const buckets = [0, 1, 3, 4, 5].map((i) => bucketAt(i, { p95Ms: 300, timedCount: 4, maxMs: i === 1 || i === 4 ? 60000 : 400 }));
  const layout = heroLayout(buckets, grid, { from: 0, to: 6 }, 'p95');
  assert.equal(layout.flaggedCount, 2);
  assert.match(heroSentence(layout), /^2 intervals hold a request far slower than usual/);
});

test('a view with no timings draws nothing', () => {
  const layout = heroLayout([bucketAt(0), bucketAt(2)], grid, { from: 0, to: 6 }, 'p95');
  assert.equal(layout.maxMs, 0);
  assert.deepEqual(layout.ticks, []);
  assert.deepEqual(layout.line, []);
  assert.deepEqual(layout.dots, []);
  assert.deepEqual(layout.bridge, []);
  assert.deepEqual(layout.marks, []);
  assert.equal(layout.flaggedCount, 0);
  assert.equal(layout.topFlaggedMs, null);
  assert.equal(heroSentence(layout), '');
});

test('a zoomed range positions the first visible slot half a slot in from the left', () => {
  const buckets = [2, 3, 4].map((i) => bucketAt(i, { p95Ms: 100, timedCount: 2, maxMs: 150 }));
  const layout = heroLayout(buckets, grid, { from: 2, to: 5 }, 'p95');
  assert.equal(layout.dots.length, 3);
  assert.equal(layout.dots[0].index, 2);
  assert.ok(Math.abs(layout.dots[0].leftPct - 100 / 6) < 1e-9, 'half a slot of a three-slot view');
  assert.equal(layout.line.length, 1);
});

test('buckets that share a slot keep the larger value and the larger slowest request', () => {
  const buckets = [bucketAt(0, { p95Ms: 100, maxMs: 200 }), bucketAt(0, { p95Ms: 250, maxMs: 180, count: 3 })];
  const layout = heroLayout(buckets, grid, { from: 0, to: 6 }, 'p95');
  assert.equal(layout.dots.length, 1);
  assert.equal(layout.dots[0].ms, 250);
  assert.equal(layout.marks[0].ms, 200);
});

test('every fixed string the hero layer can print passes the copy lint', () => {
  const entries = [
    { id: 'hero.legendRequests', text: HERO_TEXT.legendRequests },
    { id: 'hero.legendBridge', text: HERO_TEXT.legendBridge },
    { id: 'hero.legendSlowest', text: HERO_TEXT.legendSlowest },
    { id: 'hero.tipNoTiming', text: HERO_TEXT.tipNoTiming },
    { id: 'hero.tipSlowest', text: HERO_TEXT.tipSlowest },
    { id: 'hero.tipOutlier', text: HERO_TEXT.tipOutlier },
    { id: 'hero.empty', text: HERO_TEXT.empty },
    { id: 'hero.legendWebRequests', text: HERO_TEXT.legendWebRequests },
    { id: 'hero.legendRefused', text: HERO_TEXT.legendRefused },
    { id: 'hero.legendFailed', text: HERO_TEXT.legendFailed },
    { id: 'hero.webRefused', text: HERO_TEXT.webRefused(12, 340) },
    { id: 'hero.outlierOne', text: HERO_TEXT.outlierOne('60 s') },
    { id: 'hero.outlierMany', text: HERO_TEXT.outlierMany(3, '60 s') }
  ];
  assert.deepEqual(lintScreenStrings(entries, deck.terms), []);
});
