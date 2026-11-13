const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');

// Overview tab UI: reads the .svelte source as text, as tests/drill.popup.test.cjs does.
const src = fs.readFileSync(path.resolve(__dirname, '..', 'src/components/DrillOverview.svelte'), 'utf8');

test('no-timings branch with its test id and hint, and no blame on old rows', () => {
  assert.match(src, /\{#if perf === null\}/);
  assert.ok(src.includes('data-testid="drill-no-timings"'));
  assert.ok(src.includes('DRILL_TEXT.noTimings'));
  assert.ok(src.includes('DRILL_HELP.noTimings'));
  assert.doesNotMatch(src, /oldRow|drill-old-row/);
});

test('the facts list comes first, outside the perf branch, with a title and an always-visible hint', () => {
  const factsAt = src.indexOf('data-testid="drill-facts"');
  const branchAt = src.indexOf('{#if perf === null}');
  assert.ok(factsAt >= 0 && branchAt > factsAt, 'facts must render before and outside the perf branch');
  assert.ok(src.includes('DRILL_TEXT.factsTitle'));
  assert.match(src, /<p class="hint">\{DRILL_HELP\.factsTitle\}<\/p>/);
  assert.match(src, /callFacts\(record, timeZone\.zone\)/);
  for (const key of ['endpoint', 'status', 'started', 'duration']) assert.ok(src.includes('drill-fact-{fact.key}') && src.includes(key + ':'), key);
});

test('a part that was not recorded says so, and Other is never invented from partial parts', () => {
  assert.ok(src.includes("part.ms === null && part.key === 'other'"));
  assert.ok(src.includes('DRILL_TEXT.otherUnknown'));
  assert.ok(src.includes('DRILL_HELP.otherUnknown'));
  assert.ok(src.includes('DRILL_TEXT.notRecorded'));
});

test('missing values show notRecorded', () => {
  assert.ok(src.includes('DRILL_TEXT.notRecorded'));
});

test('SQL, Redis and HTTP rows are hidden: they are not recorded per call yet', () => {
  assert.doesNotMatch(src, /slowSql|slowHttp|partSql|partRedis|partHttp/);
});

test('one stacked bar shows the breakdown', () => {
  assert.ok(src.includes('data-testid="drill-bar"'));
  assert.ok(src.includes('DRILL_TEXT.barLabel'));
});

test('no raw HTML rendering', () => {
  assert.doesNotMatch(src, /\{@html/);
});

test('imports DRILL_HELP from drillCopy', () => {
  assert.match(src, /import \{[^}]*DRILL_HELP[^}]*\} from '\.\.\/lib\/drill\/drillCopy'/);
});

test('always-visible hint paragraphs', () => {
  assert.ok(src.includes('class="hint"'));
});

test('no guided or callTrace import', () => {
  assert.doesNotMatch(src, /from\s+['"][^'"]*(guided|callTrace)/i);
});

test('bar width is clamped between 0 and 100', () => {
  assert.match(src, /Math\.min\(100,\s*Math\.max\(0,\s*part\.share\)\)/);
});

test('empty sections are hidden by guards', () => {
  assert.match(src, /\{#if pressure\.length > 0\}/);
});

test('the no-timings branch comes before and excludes the timing section blocks', () => {
  const noneAt = src.indexOf('{#if perf === null}');
  const elseAt = src.indexOf('{:else if perf && where}');
  const whereSection = src.indexOf('aria-labelledby="ov-where"');
  assert.ok(noneAt >= 0 && elseAt > noneAt && whereSection > elseAt);
});

test('formatting comes from the shared formatMs rule', () => {
  assert.match(src, /import \{[^}]*formatMs[^}]*\} from '\.\.\/lib\/drill\/overview'/);
});
