const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

// Partial endpoint-daily and step-change notices, the tracing lane reason codes (timeout, broad-scan), and the 409/413 import reasons.

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

const PARTIAL_SENTENCE = (shown, planned) => `Showing ${shown} of ${planned} days; the read ran out of time. Shorten the window or Try again.`;
const BROAD_SCAN = 'The live audit source would not read a window this long, so no API requests were read. This is not the same as no traffic. Choose a window of 24 hours or less.';
const TIMEOUT = 'Showing part of this lane: the read hit its time limit. Try a shorter window.';

const lane = (extra = {}) => ({
  key: 'audit', label: 'API requests', state: 'incomplete', reason: '', origin: 'live', asOf: '', consistency: 'live-non-snapshot',
  complete: false, truncated: false, scanned: 0, hidden: 0, sources: [], buckets: [], ...extra
});

/* ------------------------------------------------------------------ partial days sentence */

test('partialDaysText names the days shown out of the days planned, and falls back when the plan is unknown', () => {
  const { partialDaysText, WAIT_TEXT } = loadTypeScript('src/lib/copy/waitCopy.ts');
  assert.equal(partialDaysText(12, 30), PARTIAL_SENTENCE(12, 30));
  assert.equal(partialDaysText(0, 7), PARTIAL_SENTENCE(0, 7));
  for (const planned of [undefined, 0, -3, NaN]) assert.equal(partialDaysText(5, planned), WAIT_TEXT.daysPartialUnknown);
  assert.equal(partialDaysText(9, 7), WAIT_TEXT.daysPartialUnknown, 'more shown than planned is not a count worth printing');
  assert.match(WAIT_TEXT.daysPartialUnknown, /Shorten the window or Try again\.$/);
});

/* ------------------------------------------------------------------ endpoint daily */

test('the daily notice shows only for a partial read and counts the days drawn', () => {
  const { dailyPartialNotice, dailyWarnings } = loadTypeScript('src/lib/patterns.ts');
  const days = Array.from({ length: 12 }, (_, i) => ({ date: '2026-10-' + String(i + 1).padStart(2, '0'), start: '', end: '', count: 40, p50Ms: 100, p95Ms: 900 }));
  assert.equal(dailyPartialNotice({ days, warnings: [] }), '');
  assert.equal(dailyPartialNotice({ partial: false, days, warnings: [] }), '');
  assert.equal(dailyPartialNotice({ partial: true, days, daysPlanned: 30, warnings: [] }), PARTIAL_SENTENCE(12, 30));
  assert.match(dailyPartialNotice({ partial: true, days, warnings: [] }), /Shorten the window or Try again\.$/);

  const cut = 'The read ran out of time after 12 of 30 days, so only the oldest 12 are shown. Choose a shorter span to see them all.';
  const other = 'Some days had few requests.';
  assert.deepEqual(dailyWarnings({ partial: true, warnings: [cut, other] }), [other], 'the cut warning is replaced by the dictionary notice');
  assert.deepEqual(dailyWarnings({ warnings: [cut, other] }), [cut, other], 'a finished read keeps every warning');
  assert.deepEqual(dailyWarnings({ warnings: undefined }), []);
});

test('EndpointDaily keeps the finished days drawn, shows the notice and offers Try again', () => {
  const src = read('src/components/EndpointDaily.svelte');
  assert.match(src, /data-testid="daily-partial"/);
  assert.match(src, /role="status"/);
  assert.match(src, /dailyPartialNotice\(phase\.daily\)/);
  assert.match(src, /dailyWarnings\(phase\.daily\)/);
  assert.match(src, /title=\{WAIT_TEXT\.retryTitle\}[\s\S]*?\{WAIT_TEXT\.retry\}<\/button>/);
  assert.match(src, /void attempt;/, 'the effect reads the retry counter, so Try again reads the same span again');
  assert.match(src, /attempt \+= 1/);
  // The notice sits above the table and the chart, which are drawn from phase.daily in either case.
  assert.ok(src.indexOf('data-testid="daily-partial"') < src.indexOf('<table'));
  assert.ok(!/phase\.kind === 'done' && !phase\.daily\.partial/.test(src), 'a partial read is not hidden');
});

test('the EndpointDaily type carries partial and daysPlanned, both optional', () => {
  const types = read('src/lib/types.ts');
  assert.match(types, /export interface EndpointDaily \{[^}]*partial\?: boolean;[^}]*daysPlanned\?: number/);
});

/* ------------------------------------------------------------------ step change */

test('the step-change notice tells a cut read from days left out in part', () => {
  const { stepPartialNotice, stepSection } = loadTypeScript('src/lib/patterns.ts');
  assert.equal(stepPartialNotice({ partial: false, daysRead: 4, daysLeftOut: 0 }, 7), '');
  assert.equal(stepPartialNotice({ partial: true, daysRead: 4, daysLeftOut: 0 }, 7), PARTIAL_SENTENCE(4, 7));
  assert.equal(stepPartialNotice({ partial: true, daysRead: 4, daysLeftOut: 2 }, 7), PARTIAL_SENTENCE(4, 7), 'fewer days covered than asked is a cut read');
  assert.equal(stepPartialNotice({ partial: true, daysRead: 5, daysLeftOut: 2 }, 7), '', 'days left out in part alone are not a cut read');
  assert.match(stepPartialNotice({ partial: true, daysRead: 4, daysLeftOut: 0 }), /Shorten the window or Try again\.$/, 'unknown plan still shows a notice');
  assert.equal(stepPartialNotice({ partial: true, daysRead: 5, daysLeftOut: 1 }), '');

  const step = { zone: 'UTC', start: 's', end: 'e', daysRead: 4, daysLeftOut: 0, operations: [], partial: true };
  const section = stepSection({ stepChange: step }, true, 7);
  assert.equal(section.kind, 'rows');
  assert.equal(section.partial, PARTIAL_SENTENCE(4, 7));
  assert.equal(stepSection({ stepChange: { ...step, partial: false } }, true, 7).partial, '');
});

test('PatternsPanel shows the step notice with Try again, from the days asked for when the report was run', () => {
  const src = read('src/components/PatternsPanel.svelte');
  assert.match(src, /stepSection\(report, phase\.stepAsked, phase\.stepPlanned\)/);
  assert.match(src, /data-testid="step-partial"/);
  assert.match(src, /role="status"[^>]*data-testid="step-partial"/);
  assert.match(src, /onclick=\{\(\) => void run\(\)\}>\{WAIT_TEXT\.retry\}/);
  assert.match(src, /const stepPlanned = stepAsked \? Number\(stepDays\) : 0;/, 'the plan is captured when the run starts, not after the answer');
});

/* ------------------------------------------------------------------ tracing lane reasons */

test('a timeout lane reads as a partial lane with the actionable sentence, in note, hint and tooltip', () => {
  const lanes = loadTypeScript('src/lib/tracing/lanes.ts');
  const tooltip = loadTypeScript('src/lib/tracing/tooltip.ts');
  const timeoutLane = lane({ incompleteReason: 'timeout', truncated: true, reason: 'server words that must not win' });
  assert.equal(lanes.laneNote(timeoutLane), TIMEOUT);
  assert.equal(lanes.laneNoteTone(timeoutLane), 'warn');
  assert.equal(lanes.laneCutShort(timeoutLane), true);
  assert.equal(lanes.laneStatusLine(timeoutLane), TIMEOUT);
  assert.ok(tooltip.laneHeadHint(timeoutLane).endsWith(TIMEOUT));
  assert.ok(tooltip.laneHeadHint(timeoutLane).startsWith(tooltip.laneHint('audit')));
  assert.match(TIMEOUT, /Try a shorter window\.$/);
});

test('a timeout lane that kept the state "events" (sql, iis) still says it was cut short', () => {
  const lanes = loadTypeScript('src/lib/tracing/lanes.ts');
  const kept = lane({ key: 'sql', state: 'events', incompleteReason: 'timeout', truncated: true, buckets: [{ start: '2026-10-01T00:00:00Z', count: 3, errors: 0 }] });
  assert.equal(lanes.laneNote(kept), TIMEOUT);
  assert.equal(lanes.laneCutShort(kept), true);
  // The same lane without the reason is quiet, as before.
  assert.equal(lanes.laneNote({ ...kept, incompleteReason: undefined }), '');
  assert.equal(lanes.laneNote(lane({ state: 'events' })), '');
});

test('a broad-scan lane says the window is too long for the live source and how to fix it', () => {
  const lanes = loadTypeScript('src/lib/tracing/lanes.ts');
  const tooltip = loadTypeScript('src/lib/tracing/tooltip.ts');
  const broad = lane({ incompleteReason: 'broad-scan', reason: 'server words that must not win' });
  assert.equal(lanes.laneNote(broad), BROAD_SCAN);
  assert.equal(lanes.laneNoteTone(broad), 'warn');
  assert.equal(lanes.laneCutShort(broad), true);
  assert.equal(lanes.INCOMPLETE_TEXT['broad-scan'], BROAD_SCAN);
  assert.ok(tooltip.laneHeadHint(broad).endsWith(BROAD_SCAN));
  assert.match(BROAD_SCAN, /Choose a window of 24 hours or less.$/);
  assert.doesNotMatch(BROAD_SCAN, /allow-broad-scans/, 'no command-line advice on this screen');
  assert.match(BROAD_SCAN, /not the same as no traffic/);
  // A lane that saw nothing but was never read does not claim "no events".
  assert.equal(lanes.laneStatusLine(lane({ state: 'none-seen', incompleteReason: 'broad-scan' })), BROAD_SCAN);
  assert.equal(lanes.laneStatusLine(lane({ state: 'none-seen' })), 'No events in this period');
});

test('rowcap keeps the partial style and error keeps the error style; neither counts as cut short', () => {
  const lanes = loadTypeScript('src/lib/tracing/lanes.ts');
  assert.equal(lanes.laneNoteTone(lane({ incompleteReason: 'rowcap' })), 'warn');
  assert.equal(lanes.laneNoteTone(lane({ incompleteReason: 'error' })), 'bad');
  assert.equal(lanes.laneNoteTone(lane()), 'bad');
  assert.equal(lanes.laneCutShort(lane({ incompleteReason: 'rowcap' })), false);
  assert.equal(lanes.laneCutShort(lane({ incompleteReason: 'error' })), false);
  assert.equal(lanes.laneNote(lane({ incompleteReason: 'error' })), 'The audit read failed; nothing was shortened.');
});

test('a disabled lane is never reported as cut short', () => {
  const lanes = loadTypeScript('src/lib/tracing/lanes.ts');
  const disabled = lane({ incompleteReason: 'timeout', disabled: 'tz-rule', reason: 'Time zone not checked' });
  assert.equal(lanes.laneCutShort(disabled), false);
  assert.equal(lanes.laneNote(disabled), 'Time zone not checked');
});

test('the status line shows when any lane was cut short and not otherwise', () => {
  const lanes = loadTypeScript('src/lib/tracing/lanes.ts');
  const { WAIT_TEXT } = loadTypeScript('src/lib/copy/waitCopy.ts');
  assert.equal(lanes.anyLaneCutShort([lane(), lane({ key: 'sql', incompleteReason: 'timeout' })]), true);
  assert.equal(lanes.anyLaneCutShort([lane({ incompleteReason: 'broad-scan' })]), true);
  // A capped or failed lane was not read in full either: the status block names it (before, only timeout and broad-scan did).
  assert.equal(lanes.anyLaneCutShort([lane({ incompleteReason: 'rowcap' }), lane({ state: 'events' })]), true);
  assert.equal(lanes.anyLaneCutShort([lane({ incompleteReason: 'error' })]), true);
  assert.equal(lanes.anyLaneCutShort([lane({ state: 'events' }), lane({ key: 'sql', state: 'no-data' })]), false);
  assert.equal(lanes.anyLaneCutShort([]), false);
  assert.equal(WAIT_TEXT.lanesCutShort, 'Some lanes were not read in full. What was read is drawn; the rest is marked not read.');
  const chart = read('src/components/TracingChart.svelte');
  assert.match(chart, /anyLaneCutShort\(response\.lanes\)/);
  assert.match(chart, /role="status" class="notice cut-short" data-testid="lanes-cut-short"/);
  assert.match(chart, /<strong>\{WAIT_TEXT\.lanesCutShort\}<\/strong>/);
  assert.match(chart, /readSummary\(response\.lanes, at\)/);
  assert.match(chart, /class="lane-note \{laneNoteTone\(lane\)\}"/);
  assert.match(chart, /\.lane-note\.warn/);
});

test('parseTracing keeps the new reason codes and drops unknown ones', () => {
  const model = loadTypeScript('src/lib/tracing/model.ts');
  const response = (code) => ({
    window: { start: '2026-10-01T00:00:00Z', end: '2026-10-02T00:00:00Z', axisStart: '2026-10-01T00:00:00Z', axisEnd: '2026-10-02T00:00:00Z', bucketSeconds: 3600 },
    lanes: [{ key: 'audit', label: 'API', state: 'incomplete', reasonCode: code, complete: false, truncated: code === 'timeout', buckets: [] }]
  });
  assert.equal(model.parseTracing(response('timeout')).lanes[0].incompleteReason, 'timeout');
  assert.equal(model.parseTracing(response('broad-scan')).lanes[0].incompleteReason, 'broad-scan');
  assert.equal(model.parseTracing(response('rowcap')).lanes[0].incompleteReason, 'rowcap');
  assert.equal(model.parseTracing(response('something-new')).lanes[0].incompleteReason, undefined);
});

/* ------------------------------------------------------------------ import reasons (413 and 409) */

test('the import reasons read as the agreed sentences', () => {
  const copy = loadTypeScript('src/lib/copy/importCopy.ts');
  assert.equal(copy.reasonText('file-cap'), 'This file is larger than one import allows. Split it or import a shorter period.');
  assert.equal(copy.reasonText('case-cap'), 'This case is full. Free space by removing older imports or archive it.');
  assert.equal(copy.reasonText('case-import-running'), 'An import is running on this case. Try again when it finishes.');
  assert.equal(copy.errorText({ status: 413, reason: 'file-cap', message: 'raw server words' }), copy.reasonText('file-cap'));
  assert.equal(copy.errorText({ status: 413, reason: 'case-cap', message: 'raw server words' }), copy.reasonText('case-cap'));
  assert.equal(copy.errorText({ status: 409, reason: 'case-import-running', message: 'raw server words' }), copy.reasonText('case-import-running'));
  assert.equal(copy.reasonText('limit-bytes'), copy.REASON_TEXT.limit, 'the older limit reasons are unchanged');
});

test('the new reasons pass the copy lint', () => {
  const { lintScreenStrings } = loadTypeScript('src/lib/copy/copylint.ts');
  const { deck } = loadTypeScript('src/lib/copy/deck.ts');
  const { waitCopyEntries } = loadTypeScript('src/lib/copy/waitCopy.ts');
  const { importCopyEntries } = loadTypeScript('src/lib/copy/importCopy.ts');
  const { INCOMPLETE_TEXT } = loadTypeScript('src/lib/tracing/lanes.ts');
  const entries = [
    ...waitCopyEntries(), ...importCopyEntries(),
    { id: 'lanes.incomplete.broad-scan', text: INCOMPLETE_TEXT['broad-scan'] },
    { id: 'lanes.incomplete.timeout', text: INCOMPLETE_TEXT.timeout }
  ];
  assert.ok(entries.some((entry) => entry.id === 'wait.partialDays'));
  assert.deepEqual(lintScreenStrings(entries, deck.terms), []);
});

test('ApiError keeps the reason a case route sends beside its message', async () => {
  globalThis.sessionStorage = { getItem: () => 'session-token-1', setItem() {}, removeItem() {} };
  const api = loadTypeScript('src/lib/api.ts');
  const original = globalThis.fetch;
  try {
    globalThis.fetch = async () => new Response(JSON.stringify({ error: 'busy', reason: 'case-import-running' }), { status: 409, headers: { 'content-type': 'application/json' } });
    await assert.rejects(api.api.saveCaseWorkspace('abcd2345abcd2345abcd234567', {}), (error) => error.name === 'ApiError' && error.status === 409 && error.reason === 'case-import-running');
    globalThis.fetch = async () => new Response(JSON.stringify({ error: 'nope' }), { status: 409, headers: { 'content-type': 'application/json' } });
    await assert.rejects(api.api.saveCaseWorkspace('abcd2345abcd2345abcd234567', {}), (error) => error.status === 409 && error.reason === undefined);
  } finally { globalThis.fetch = original; }
});

test('the workspace saver reports a failed save, and the note is the import-running sentence', async () => {
  const ws = loadTypeScript('src/lib/caseWorkspace.ts');
  const copy = loadTypeScript('src/lib/copy/importCopy.ts');
  const failures = [];
  const sent = [];
  let reject = true;
  const timers = [];
  const saver = ws.createWorkspaceSaver({
    send: async (caseId) => { if (reject) { const error = new Error('busy'); error.reason = 'case-import-running'; throw error; } },
    delayMs: 1,
    setTimer: (run) => { timers.push(run); return timers.length; },
    clearTimer: () => {},
    sent: (caseId) => sent.push(caseId),
    failed: (caseId, cause) => failures.push([caseId, ws.workspaceFailureNote(cause)])
  });
  saver.schedule('c1', { version: 1 });
  timers.pop()();
  await new Promise((resolve) => setImmediate(resolve));
  assert.deepEqual(failures, [['c1', copy.reasonText('case-import-running')]]);
  assert.deepEqual(sent, []);
  // A failed save is not remembered as sent, so the same workspace goes again, and then clears the note.
  reject = false;
  saver.schedule('c1', { version: 1 });
  timers.pop()();
  await new Promise((resolve) => setImmediate(resolve));
  assert.deepEqual(sent, ['c1']);
  assert.equal(failures.length, 1);
});

test('workspaceFailureNote is quiet for every other failure, and a throwing listener never breaks the saver', async () => {
  const ws = loadTypeScript('src/lib/caseWorkspace.ts');
  for (const cause of [undefined, null, 'x', new Error('boom'), { reason: 'other' }, { reason: 7 }]) assert.equal(ws.workspaceFailureNote(cause), '');
  const timers = [];
  const saver = ws.createWorkspaceSaver({
    send: async () => { throw new Error('boom'); },
    setTimer: (run) => { timers.push(run); return 1; }, clearTimer: () => {},
    failed: () => { throw new Error('listener'); }
  });
  saver.schedule('c1', { version: 1 });
  timers.pop()();
  await new Promise((resolve) => setImmediate(resolve));
});

test('the app shows the import-running note inline and clears it on the next good save', () => {
  const state = read('src/lib/appState.svelte.ts');
  assert.match(state, /failed: \(caseId, cause\) => \{ if \(caseId === activeCaseId\) \{ const note = workspaceFailureNote\(cause\); if \(note\) workspaceNote = note; \} \}/);
  assert.match(state, /workspaceNote = '';/);
  assert.match(state, /get workspaceNote\(\)/);
  const appSrc = read('src/App.svelte');
  assert.match(appSrc, /\{#if app\.workspaceNote\}<div class="notice" role="status" data-testid="workspace-note">/);
});

test('health runs and the run problem line show the import-running sentence on a 409', () => {
  // runProblem tests its error with instanceof, and each loaded module has its own classes, so the wiring is read from the source here.
  const source = read('src/lib/lanes.ts');
  assert.match(source, /status === 409 && \(error instanceof CaseApiError \|\| error instanceof ApiError\) && error\.reason === 'case-import-running'\) return \{ text: reasonText\('case-import-running'\)/);
  assert.ok(source.indexOf("error.reason === 'case-import-running'") < source.indexOf('/already/i.test(message)'), 'the reason is checked before the generic busy line');
  const panel = read('src/lib/components/HealthPanel.svelte');
  assert.match(panel, /error\.reason === 'case-import-running'\) return reasonText\('case-import-running'\)/);
});
