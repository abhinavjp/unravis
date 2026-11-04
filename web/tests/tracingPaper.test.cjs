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

test('case home offers the timeline entry and keeps the Analysis block and the guided mount', () => {
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
  for (const needle of ['home-analysis', 'GuidedAnalysis']) {
    assert.ok(src.includes(needle), 'CaseHome.svelte must still contain ' + needle);
  }
});

test('entry is enabled for a closed case, which opens the timeline as a viewer (UI-R2-03 option A)', () => {
  const src = home();
  const line = src.split('\n').find((row) => row.includes('id="home-tracing-open"'));
  assert.ok(line, 'the entry button line must exist');
  assert.doesNotMatch(line, /disabled=/);
  assert.doesNotMatch(src, /home-tracing-closed/);
  assert.doesNotMatch(src, /closedReason/);
  assert.match(src, /<TracingPaper \{caseId\} title=\{info\?\.title \|\| caseCode\(caseId\)\} closed=\{isClosed\} readOnly=\{readOnly && !isClosed\}/);
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
  for (const needle of ['new AbortController', 'nextRequestSeq', 'TRACING_TEXT.cancel', 'TRACING_TEXT.retry']) {
    assert.ok(src.includes(needle), 'TracingPaper.svelte must contain ' + needle);
  }
  // One stale-answer guard: the aborted signal (the second, sequence-based check was redundant).
  assert.doesNotMatch(src, /isStale|latestSeq/);
  // A superseded read can fail as an ordinary error (the body read is a CaseApiError), so the signal is checked first (C2).
  assert.match(src, /catch \(error\) \{\s*if \(mine\.signal\.aborted\) return;/);
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

test('Cancel on a re-run keeps the previous chart or says what happened, never an empty region', () => {
  const src = paper();
  const cancel = src.slice(src.indexOf('function cancel()'), src.indexOf('function onBarSelect'));
  assert.match(cancel, /status = result \? 'ready' : 'idle'/);
  assert.match(cancel, /cancelled = true/);
  assert.match(src, /WINDOW_TEXT\.cancelledKept/);
  assert.match(src, /WINDOW_TEXT\.cancelledNone/);
  const copy = read('src/lib/tracing/windows.ts');
  assert.match(copy, /cancelledKept: 'Cancelled\. The chart still shows the previous result\.'/);
});

test('a re-run keeps the old chart under a veil of the same height instead of swapping in a short block', () => {
  const src = paper();
  const chart = src.slice(src.indexOf('data-region="chart"'), src.indexOf('data-region="notes"'));
  assert.match(chart, /status === 'loading' && !result/, 'the short block is for a first load only');
  assert.match(chart, /class="chart-veil"/);
  assert.match(chart, /inert=\{status === 'loading'\}/);
  assert.match(src, /\.chart-hold \{ position: relative; \}/);
});

test('Try again repeats the request that failed, not the boxes as they are now', () => {
  const src = paper();
  assert.match(src, /run\(lastBody \?\? undefined\)/);
  assert.match(src, /lastBody = body/);
});

test('an empty window shows one hint, and typing in a box clears the refusal', () => {
  const src = paper();
  assert.match(src, /windowIsEmpty\(result\.lanes\)/);
  assert.match(src, /WINDOW_TEXT\.noEvents/);
  assert.equal((src.match(/onchange=\{onFieldInput\}/g) || []).length, 2, 'both pickers report an edit');
  assert.match(src, /function onFieldInput\(\) \{\s*edited = true;\s*clearProblems\(\);/);
});

test('the read-only no-op effect is gone and readOnly still disables the inputs (a closed case does not)', () => {
  const src = paper();
  assert.doesNotMatch(src, /void readOnly/);
  assert.ok((src.match(/disabled=\{readOnly\}/g) || []).length >= 3, 'both pickers, the overlap button and Start tracing');
  assert.doesNotMatch(src, /readOnly \|\| closed/);
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

test('Try again and Cancel give keyboard focus to the chart region instead of dropping it (UI-R2-02)', () => {
  const src = paper();
  assert.match(src, /<div data-region="chart" class="chart-region" role="region" aria-label=\{TRACING_TEXT\.chartLabel\} tabindex="-1" bind:this=\{chartRegion\}>/);
  const fn = src.slice(src.indexOf('async function keepFocusInChart'), src.indexOf('function cancel()'));
  assert.match(fn, /chartRegion\.contains\(document\.activeElement\)/);
  assert.match(fn, /await tick\(\)/);
  assert.match(fn, /active === document\.body\) chartRegion\?\.focus\(\)/);
  assert.equal((src.match(/keepFocusInChart\(/g) || []).length, 4, 'defined once, used by Try again and by both Cancel buttons');
  assert.match(src, /keepFocusInChart\(\(\) => void run\(lastBody \?\? undefined\)\)/);
  assert.equal((src.match(/keepFocusInChart\(cancel\)/g) || []).length, 2);
  assert.doesNotMatch(src, /onclick=\{cancel\}/);
});

test('the failed read is a styled error panel with the same sentence and Try again (UI-R2-09)', () => {
  const src = paper();
  const chart = src.slice(src.indexOf('data-region="chart"'), src.indexOf('data-region="notes"'));
  const panel = chart.slice(chart.indexOf('class="error-panel"'), chart.indexOf("{:else if status === 'loading'"));
  assert.match(panel, /class="error-icon" aria-hidden="true"/);
  assert.match(panel, /<p role="status">\{errorText\}<\/p>/);
  assert.match(panel, /TRACING_TEXT\.retry/);
  assert.match(src, /\.error-panel \{[^}]*border: var\(--hairline-width\) solid var\(--bad\)/);
});

test('Cancelled is said in the notes region below the chart, so the chart never moves (UI-R2-12)', () => {
  const src = paper();
  const chart = src.slice(src.indexOf('data-region="chart"'), src.indexOf('data-region="notes"'));
  assert.doesNotMatch(chart, /cancel-note|cancelledKept|cancelledNone/);
  const notes = src.slice(src.indexOf('data-region="notes"'), src.indexOf('</section>'));
  assert.match(notes, /class="cancel-note">\{result \? WINDOW_TEXT\.cancelledKept : WINDOW_TEXT\.cancelledNone\}/);
});

test('typed windows are restored on open and kept after the first edit only (UI-R2-11)', () => {
  const src = paper();
  assert.match(src, /loadWindows\(caseId\) \?\? defaultWindows\(windowNow\(app\.info, Date\.now\(\)\)\)/);
  assert.match(src, /if \(edited\) saveWindows\(caseId,/);
  assert.match(src, /edited = true;\s*clearProblems\(\);\s*\}\s*\n\s*\n/);
});

test('a closed case shows one plain line at the top of the timeline, announced as a status (UI-R2-03)', () => {
  const src = paper();
  assert.match(src, /\{#if closed\}<p class="closed-line" role="status" data-testid="tp-closed-line">\{TRACING_TEXT\.closedView\}<\/p>\{\/if\}/);
  assert.match(read('src/lib/copy/tracingCopy.ts'), /closedView: 'This case is closed\. You can look at the timeline but not change the case\.'/);
});

test('the chart region has a role and a name, and a case with no events says how to fix it (UI-R3-02, UI-R3-03)', () => {
  const src = read('src/components/TracingPaper.svelte');
  assert.match(src, /data-region="chart" class="chart-region" role="region" aria-label=\{TRACING_TEXT\.chartLabel\}/);
  assert.match(src, /windowHasNoData\(result\.lanes, result\.warnings\)/);
  assert.match(src, /\{:else if noData\}<p class="empty-hint">\{WINDOW_TEXT\.noData\}<\/p>/);
  assert.match(read('src/lib/components/WindowPicker.svelte'), /@media \(max-width: 1024px\), \(pointer: coarse\) \{/, 'the picker has bigger touch targets');
});

test('erasing a case drops its kept tracing choice and windows before the active-case guard (P5)', () => {
  const src = read('src/lib/appState.svelte.ts');
  assert.match(src, /import \{ forgetSources \} from '\.\/tracing\/sources';/);
  assert.match(src, /import \{ forgetWindows \} from '\.\/tracing\/windows';/);
  const body = src.slice(src.indexOf('export function caseErased'), src.indexOf('export function openCase('));
  assert.match(body, /forgetSources\(id\); forgetWindows\(id\);/);
  assert.ok(body.indexOf('forgetSources(id)') < body.indexOf('activeCaseId !== id'), 'the stored keys go before the active-case guard');
});
