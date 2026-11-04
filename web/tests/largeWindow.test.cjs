const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

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
const read = (relative) => fs.readFileSync(path.join(root, relative), 'utf8');

const CASE_ID = 'abcd2345abcd2345abcd234567';
const TIMEOUT_SENTENCE = 'This is taking longer than expected. Try a shorter window.';

const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

/** A fetch that never answers; it rejects with the signal's reason when the signal aborts, like the browser's fetch. */
function hungFetch(seen) {
  return (url, init = {}) => new Promise((_resolve, reject) => {
    seen.push({ url, init });
    const signal = init.signal;
    if (!signal) return;
    const fail = () => reject(signal.reason ?? new DOMException('Aborted', 'AbortError'));
    if (signal.aborted) fail(); else signal.addEventListener('abort', fail, { once: true });
  });
}

/** Replaces AbortSignal.timeout with a short one for the test and records the limit the code asked for. */
function shortTimeout(ms = 20) {
  const original = AbortSignal.timeout;
  const asked = [];
  AbortSignal.timeout = (limit) => {
    asked.push(limit);
    const controller = new AbortController();
    setTimeout(() => controller.abort(new DOMException('The operation timed out.', 'TimeoutError')), ms);
    return controller.signal;
  };
  return { asked, restore: () => { AbortSignal.timeout = original; } };
}

function session() {
  globalThis.sessionStorage = { getItem: () => 'session-token-1', setItem() {}, removeItem() {} };
}

/* ------------------------------------------------------------------ the guard */

test('guardSignal: a read with no limit gets the caller signal back untouched', () => {
  const { guardSignal } = loadTypeScript('src/lib/requestTimeout.ts');
  const controller = new AbortController();
  for (const limit of [null, undefined, 0, -5]) {
    const guard = guardSignal(controller.signal, limit);
    assert.equal(guard.signal, controller.signal);
    assert.equal(guard.timedOut(), false);
    guard.done();
  }
  assert.equal(guardSignal(undefined, null).signal, undefined);
});

test('guardSignal: the limit ends a read and reads as a timeout, not a cancel', async () => {
  const { guardSignal } = loadTypeScript('src/lib/requestTimeout.ts');
  const guard = guardSignal(undefined, 15);
  assert.equal(guard.signal.aborted, false);
  await sleep(60);
  assert.equal(guard.signal.aborted, true);
  assert.equal(guard.timedOut(), true);
  guard.done();
});

test('guardSignal: a cancel by the person is never reported as a timeout', async () => {
  const { guardSignal } = loadTypeScript('src/lib/requestTimeout.ts');
  const controller = new AbortController();
  const guard = guardSignal(controller.signal, 15);
  controller.abort();
  assert.equal(guard.signal.aborted, true);
  await sleep(60);
  assert.equal(guard.timedOut(), false, 'the person cancelled first');
  guard.done();
});

test('guardSignal: without AbortSignal.any a timer and a listener do the same job', async () => {
  const { guardSignal } = loadTypeScript('src/lib/requestTimeout.ts');
  const any = AbortSignal.any;
  AbortSignal.any = undefined;
  try {
    const slow = guardSignal(undefined, 15);
    await sleep(60);
    assert.equal(slow.signal.aborted, true);
    assert.equal(slow.timedOut(), true);
    slow.done();

    const controller = new AbortController();
    const cancelled = guardSignal(controller.signal, 15);
    controller.abort();
    assert.equal(cancelled.signal.aborted, true, 'a cancel passes through');
    await sleep(60);
    assert.equal(cancelled.timedOut(), false);
    cancelled.done();

    const quick = guardSignal(undefined, 15);
    quick.done();
    await sleep(60);
    assert.equal(quick.signal.aborted, false, 'done() releases the timer');
  } finally { AbortSignal.any = any; }
});

test('the timeout sentence and the read limit', () => {
  const rt = loadTypeScript('src/lib/requestTimeout.ts');
  assert.equal(rt.READ_TIMEOUT_MS, 150000);
  assert.equal(rt.timeoutMessage(), TIMEOUT_SENTENCE);
  const { ApiError } = loadTypeScript('src/lib/api.ts');
  assert.equal(rt.isTimeoutError(new ApiError('x', 'timeout')), true);
  assert.equal(rt.isTimeoutError(new ApiError('x', 'other')), false);
  assert.equal(rt.isTimeoutError(new DOMException('Aborted', 'AbortError')), false);
  assert.equal(rt.isTimeoutError('timeout'), false);
});

/* ------------------------------------------------------------------ api.ts */

test('api reads give up after the read limit with the plain sentence, and a cancel stays a cancel', async () => {
  session();
  const api = loadTypeScript('src/lib/api.ts');
  const seen = [];
  globalThis.fetch = hungFetch(seen);
  const stub = shortTimeout();
  try {
    const params = { from: '2026-01-01T00:00:00Z', to: '2026-04-01T00:00:00Z', tz: 'UTC', sourceIds: ['a'] };
    await assert.rejects(api.api.daily(params), (error) => error.name === 'ApiError' && error.code === 'timeout' && error.message === TIMEOUT_SENTENCE);
    assert.deepEqual(stub.asked, [150000]);

    const controller = new AbortController();
    const pending = api.api.hourGrid(params, controller.signal);
    controller.abort();
    await assert.rejects(pending, (error) => error.name === 'AbortError' && error.code !== 'timeout');
  } finally { stub.restore(); }
});

test('the export download and the export confirm have no time limit; the preview has one and follows the caller signal', async () => {
  session();
  const api = loadTypeScript('src/lib/api.ts');
  const seen = [];
  globalThis.fetch = hungFetch(seen);
  const stub = shortTimeout(5);
  try {
    const controller = new AbortController();
    const preview = api.api.exportPreview({ sourceIds: ['a'], start: 's', end: 'e', includePseudonyms: false, includeLedger: false }, controller.signal);
    controller.abort();
    await assert.rejects(preview, (error) => error.name === 'AbortError');
    assert.equal(seen[0].init.signal.aborted, true, 'cancelling the preview aborts the request itself');
    assert.deepEqual(stub.asked, [150000]);

    stub.asked.length = 0;
    const confirm = api.api.exportConfirm('p1', new AbortController().signal);
    await sleep(40);
    assert.deepEqual(stub.asked, [], 'a confirm that writes the file is not given a limit');
    assert.equal(seen[1].init.signal.aborted, false);
    void confirm.catch(() => {});
  } finally { stub.restore(); }
});

/* ------------------------------------------------------------------ cases.ts */

test('send: a GET read has the limit, a job poll and a plain POST have none, a slow POST read can ask for one', async () => {
  session();
  const cases = loadTypeScript('src/lib/cases.ts');
  const { READ_TIMEOUT_MS } = loadTypeScript('src/lib/requestTimeout.ts');
  const seen = [];
  globalThis.fetch = hungFetch(seen);
  const stub = shortTimeout();
  try {
    await assert.rejects(cases.send('/cases'), (error) => error.name === 'CaseApiError' && error.reason === 'timeout' && error.message === TIMEOUT_SENTENCE);
    assert.deepEqual(stub.asked, [READ_TIMEOUT_MS]);

    stub.asked.length = 0;
    const poll = cases.fetchImportJob(CASE_ID, '0123456789abcdef0123456789abcdef', undefined);
    const upload = cases.send('/cases', { method: 'POST', body: '{}' });
    await sleep(60);
    assert.deepEqual(stub.asked, [], 'a poll and a POST wait as long as the server needs');
    void poll.catch(() => {});
    void upload.catch(() => {});

    await assert.rejects(cases.send('/x', { method: 'POST', body: '{}' }, undefined, { timeoutMs: READ_TIMEOUT_MS }), (error) => error.reason === 'timeout');
    assert.deepEqual(stub.asked, [READ_TIMEOUT_MS]);

    const controller = new AbortController();
    const pending = cases.send('/cases', {}, controller.signal);
    controller.abort();
    await assert.rejects(pending, (error) => error.name === 'AbortError');
  } finally { stub.restore(); }
});

test('an answer body cut off by the time limit reads as a timeout, not as a damaged answer', async () => {
  session();
  const cases = loadTypeScript('src/lib/cases.ts');
  const response = { status: 200, ok: true, json: async () => { throw new DOMException('The operation timed out.', 'TimeoutError'); } };
  await assert.rejects(cases.json(response), (error) => error.reason === 'timeout' && error.message === TIMEOUT_SENTENCE);
  const damaged = { status: 200, ok: true, json: async () => { throw new SyntaxError('bad'); } };
  await assert.rejects(cases.json(damaged), (error) => error.reason === 'unreadable');
});

test('the timeline screen shows the timeout sentence for a timed-out read', () => {
  const model = loadTypeScript('src/lib/tracing/model.ts');
  const { CaseApiError } = loadTypeScript('src/lib/cases.ts');
  assert.equal(model.tracingErrorText(new CaseApiError(TIMEOUT_SENTENCE, 0, 'timeout')), TIMEOUT_SENTENCE);
});

/* ------------------------------------------------------------------ words */

test('the new words are exactly as agreed', () => {
  const { WAIT_TEXT } = loadTypeScript('src/lib/copy/waitCopy.ts');
  assert.equal(WAIT_TEXT.timeout, TIMEOUT_SENTENCE);
  assert.equal(WAIT_TEXT.stillReading, 'Still reading. A large window can take a minute. You can cancel.');
  assert.equal(WAIT_TEXT.laneTimeout, 'Showing part of this lane: the read hit its time limit. Try a shorter window.');
  assert.equal(WAIT_TEXT.longerPeriods, 'Use the Timeline or the Daily view for longer periods.');
  const { INCOMPLETE_TEXT } = loadTypeScript('src/lib/tracing/lanes.ts');
  assert.equal(INCOMPLETE_TEXT.timeout, WAIT_TEXT.laneTimeout);
});

test('the window cap is 186 days and the refusal says so', () => {
  const windows = loadTypeScript('src/lib/tracing/windows.ts');
  assert.equal(windows.MAX_TRACE_SPAN_MS, 186 * 24 * 3600 * 1000);
  assert.equal(windows.tooLongText('start or end'), 'The longest window is 186 days. Shorten the start or end.');
});

test('the 7-day Analysis refusal points to the longer views', () => {
  const autorun = loadTypeScript('src/lib/autorun.ts');
  assert.equal(autorun.SEVEN_DAY_TEXT, 'Each window can be at most 7 days. Use the Timeline or the Daily view for longer periods.');
});

test('every new string passes the copy lint', () => {
  const { lintScreenStrings } = loadTypeScript('src/lib/copy/copylint.ts');
  const { deck } = loadTypeScript('src/lib/copy/deck.ts');
  const { waitCopyEntries } = loadTypeScript('src/lib/copy/waitCopy.ts');
  const { rollupCopy } = loadTypeScript('src/lib/rollup.ts');
  const { SEVEN_DAY_TEXT } = loadTypeScript('src/lib/autorun.ts');
  const { INCOMPLETE_TEXT } = loadTypeScript('src/lib/tracing/lanes.ts');
  const { windowCopyEntries } = loadTypeScript('src/lib/tracing/windows.ts');
  const entries = [
    ...waitCopyEntries(),
    ...windowCopyEntries(),
    { id: 'autorun.sevenDays', text: SEVEN_DAY_TEXT },
    { id: 'lanes.incomplete.timeout', text: INCOMPLETE_TEXT.timeout },
    ...Object.entries(rollupCopy).map(([id, text]) => ({ id: 'rollup.' + id, text }))
  ];
  assert.ok(entries.length > 30);
  assert.deepEqual(lintScreenStrings(entries, deck.terms), []);
});

/* ------------------------------------------------------------------ the slow-read line */

test('slowRead: the plain line first, the long-wait line after six seconds', () => {
  const slow = loadTypeScript('src/lib/slowRead.ts');
  assert.equal(slow.SLOW_READ_MS, 6000);
  assert.equal(slow.readingLine('Reading the window…', false, 'Still reading.'), 'Reading the window…');
  assert.equal(slow.readingLine('Reading the window…', true, 'Still reading.'), 'Still reading.');
});

test('the timeline, the daily view and the Analysis bar all switch to the long-wait line and keep Cancel', () => {
  const paper = read('src/components/TracingPaper.svelte');
  assert.equal((paper.match(/waiting\.slow \? WAIT_TEXT\.stillReading : TRACING_TEXT\.readingWindow/g) ?? []).length, 2, 'the first read and the re-read under the veil');
  assert.equal((paper.match(/title=\{WAIT_TEXT\.cancelTitle\}/g) ?? []).length, 2);
  const rollup = read('src/components/RollupView.svelte');
  assert.match(rollup, /waiting\.slow \? WAIT_TEXT\.stillReading : rollupCopy\.reading/);
  assert.match(rollup, /onclick=\{cancel\}>\{WAIT_TEXT\.cancel\}/);
  const bar = read('src/components/SubBar.svelte');
  assert.match(bar, /loading && waiting\.slow \? WAIT_TEXT\.stillReading : runHint/);
  assert.match(bar, /\{#if loading\}<button class="quiet-button" title=\{RUN_TEXT\.cancelRunTitle\} onclick=\{cancelAnalysis\}>\{RUN_TEXT\.cancelRun\}<\/button>\{\/if\}/);
});

/* ------------------------------------------------------------------ the daily view */

test('the daily view reads both answers together, can be cancelled, and has Try again in its error', () => {
  const rollup = read('src/components/RollupView.svelte');
  assert.match(rollup, /await Promise\.all\(\[api\.daily\(params, controller\.signal\), api\.hourGrid\(params, controller\.signal\)\]\)/);
  assert.doesNotMatch(rollup, /const hours = await api\.hourGrid/);
  assert.match(rollup, /function cancel\(\) \{\s*sequence\+\+; controller\?\.abort\(\); loading = false; cancelled = true;/);
  assert.match(rollup, /\{#if error\}[\s\S]*?\{WAIT_TEXT\.retry\}[\s\S]*?\{\/if\}/);
  assert.match(rollup, /title=\{WAIT_TEXT\.retryTitle\} onclick=\{\(\) => void load\(\)\}/);
  assert.doesNotMatch(rollup, /Reading the sources…/, 'the words live in the copy file');
});

test('the old hard-coded labels moved into the copy file', () => {
  const rollup = loadTypeScript('src/lib/rollup.ts');
  assert.deepEqual(rollup.rollupRanges.map((range) => range.label), ['Last 24 hours', 'Last 7 days', 'Last 30 days', 'Last 60 days', 'Last 90 days', 'Last 120 days']);
  assert.equal(rollup.rollupCopy.spanLabel, 'Span');
  assert.equal(rollup.rollupCopy.reading, 'Reading the sources…');
  const heat = read('src/components/HourHeatmap.svelte');
  for (const old of ['aria-label="Shade squares by"', 'aria-label="No such hour on this day"', '<span>Lower</span>', '<span>Higher</span>', '<span>Few requests</span>']) assert.ok(!heat.includes(old), old);
  const bar = read('src/components/SubBar.svelte');
  assert.ok(!bar.includes("'Comparing…'") && !bar.includes('>Cancel run<'));
});

test('the Analysis error banner offers Try again for a timeout', () => {
  const app = read('src/App.svelte');
  assert.match(app, /results\.error === WAIT_TEXT\.timeout && !loading\}<button type="button" class="secondary-button" title=\{WAIT_TEXT\.retryTitle\} onclick=\{\(\) => runAnalysis\(\)\}>\{WAIT_TEXT\.retry\}<\/button>/);
});

test('job polls and the long guided reads ask for the right limits', () => {
  assert.match(read('src/lib/archiveApi.ts'), /'\/archive'\), \{\}, signal, NO_LIMIT/);
  assert.match(read('src/lib/archiveApi.ts'), /'\/verify'\), \{\}, signal, NO_LIMIT/);
  assert.match(read('src/lib/discover.ts'), /scanPath\(scanId\), \{\}, signal, \{ timeoutMs: null \}/);
  assert.equal((read('src/lib/guided/api.ts').match(/timeoutMs: READ_TIMEOUT_MS/g) ?? []).length, 2);
  assert.match(read('src/components/ExportPanel.svelte'), /api\.exportPreview\(exportInput\([^)]*\), mine\.signal\)/);
});
