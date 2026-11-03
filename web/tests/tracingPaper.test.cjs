const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');

// WP-16b: the case-home entry for the Investigation Timeline, and the TracingPaper shell it opens.
// These tests read the .svelte files as text, as tests/casehome.test.cjs does.

const root = path.resolve(__dirname, '..');
const read = (relative) => fs.readFileSync(path.join(root, relative), 'utf8');

const home = () => read('src/lib/components/CaseHome.svelte');
const paper = () => read('src/components/TracingPaper.svelte');

test('case home offers the timeline entry and keeps the Guided and Analysis blocks', () => {
  const src = home();
  for (const needle of [
    'home-tracing',
    'home-tracing-open',
    'TRACING_TEXT.entryButton',
    'lazyComponent(',
    "import('../../components/TracingPaper.svelte')"
  ]) {
    assert.ok(src.includes(needle), 'CaseHome.svelte must contain ' + needle);
  }
  for (const needle of ['home-guided', 'home-analysis', 'GuidedAnalysis']) {
    assert.ok(src.includes(needle), 'CaseHome.svelte must still contain ' + needle);
  }
});

test('entry is disabled for a closed case', () => {
  const src = home();
  const line = src.split('\n').find((row) => row.includes('id="home-tracing-open"'));
  assert.ok(line, 'the entry button line must exist');
  assert.match(line, /disabled=\{!!info\?\.closed\}/);
});

test('entry button is outlined, not a second primary', () => {
  const src = home();
  const start = src.indexOf('id="home-tracing"');
  assert.ok(start > 0, 'home-tracing section must exist');
  const end = src.indexOf('</section>', start);
  const block = src.slice(start, end);
  assert.match(block, /class="secondary-button"/);
  assert.doesNotMatch(block, /primary-button/);
});

test('lane colours are defined once in tokens.css, not in TracingPaper', () => {
  const tokens = fs.readFileSync(path.join(__dirname, '..', 'src', 'brand', 'tokens.css'), 'utf8');
  for (const name of ['--lane-audit', '--lane-sql', '--lane-iis']) {
    assert.equal(tokens.split(name + ':').length - 1, 3, name + ' is set for dark, light-by-media and light-forced');
    assert.ok(!paper().includes(name + ':'), 'TracingPaper.svelte must not redefine ' + name);
  }
});

test('TracingPaper has the four regions and the request guards', () => {
  const src = paper();
  for (const needle of ['data-region="windows"', '"toolbar"', '"chart"', '"notes"']) {
    assert.ok(src.includes(needle), 'TracingPaper.svelte must contain ' + needle);
  }
  for (const needle of ['new AbortController', 'nextRequestSeq', 'isStale', 'TRACING_TEXT.cancel', 'TRACING_TEXT.retry']) {
    assert.ok(src.includes(needle), 'TracingPaper.svelte must contain ' + needle);
  }
  assert.doesNotMatch(src, /from '.*(guided|callTrace)/);
});

test('empty case shows the no-sources sentence', () => {
  const src = paper();
  const chartStart = src.indexOf('data-region="chart"');
  const chartEnd = src.indexOf('data-region="notes"', chartStart);
  const chart = src.slice(chartStart, chartEnd);
  assert.match(chart, /TRACING_TEXT\.emptyNoSources/);
  assert.match(chart, /sources\.length === 0/);
  const copy = read('src/lib/copy/tracingCopy.ts');
  assert.match(copy, /No files have been added to this case yet\. Add a source first\./);
});

test('CallTrace stays untouched', () => {
  const src = read('src/components/CallTrace.svelte');
  assert.doesNotMatch(src, /TracingPaper/);
  assert.doesNotMatch(src, /tracing/i);
});

test('TracingPaper is only reached through lazyComponent', () => {
  const dir = path.join(root, 'src');
  const files = [];
  (function walk(d) {
    for (const e of fs.readdirSync(d, { withFileTypes: true })) {
      const p = path.join(d, e.name);
      if (e.isDirectory()) walk(p);
      else if (p.endsWith('.svelte') || p.endsWith('.ts')) files.push(p);
    }
  })(dir);
  for (const file of files) {
    const src = fs.readFileSync(file, 'utf8');
    assert.doesNotMatch(src, /^\s*import\s+TracingPaper\b/m, path.relative(root, file) + ' imports TracingPaper statically');
  }
});
