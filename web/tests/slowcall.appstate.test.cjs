// Source-text checks for the Slow call screen state in appState.svelte.ts.
const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');

const src = fs.readFileSync(path.join(__dirname, '..', 'src', 'lib', 'appState.svelte.ts'), 'utf8').replace(/\r\n/g, '\n');

function body(startText, text = src) {
  const norm = text.replace(/\r\n/g, '\n');
  const start = norm.indexOf(startText);
  assert.ok(start >= 0, `${startText} missing`);
  return norm.slice(start, norm.indexOf('\n}\n', start));
}

test('the call target key and the three functions exist', () => {
  assert.ok(src.includes("CALL_TARGET_KEY = 'unravis.callTarget'"), 'CALL_TARGET_KEY missing');
  assert.ok(src.includes('function openSlowCall'), 'openSlowCall missing');
  assert.ok(src.includes('function closeSlowCall'), 'closeSlowCall missing');
  assert.ok(src.includes('function noteSlowCall'), 'noteSlowCall missing');
});

test('opening marks the call entry, and only for the open case', () => {
  const open = body('function openSlowCall');
  assert.ok(open.includes('marker.markScreen(CASE_CALL_MARK)'), 'call mark missing in openSlowCall');
  assert.ok(open.includes('target.caseId !== activeCaseId'), 'case guard missing in openSlowCall');
});

test('closing steps back to the timeline entry', () => {
  const close = body('function closeSlowCall');
  assert.ok(close.includes('marker.stepBack(CASE_TRACING_MARK)'), 'stepBack to the timeline mark missing in closeSlowCall');
});

test('Esc closes the call screen after the drill guard and before stepUpOnce', () => {
  const keys = body('export function onWindowKey(');
  const guard = keys.indexOf('if (drillOpen) return;');
  const call = keys.indexOf('if (slowCall) { closeSlowCall(); return; }');
  const step = keys.indexOf('stepUpOnce();');
  assert.ok(guard >= 0 && call >= 0 && step >= 0, 'a key line is missing');
  assert.ok(guard < call && call < step, 'order must be drill guard, then call close, then stepUpOnce');
});

test('the remembered call target reads and writes inside try and catch', () => {
  const stored = body('function storedCallTarget');
  assert.ok(stored.includes('try') && stored.includes('catch'), 'try/catch missing in storedCallTarget');
});

test('the call screen adds no address', () => {
  assert.ok(!src.includes('#/call'), 'no #/call address');
});

test('a case switch clears the open slow call and its stored target', () => {
  const set = body('export function setActiveCase(');
  assert.ok(set.includes("if (slowCall && slowCall.caseId !== id) { slowCall = null; storedCallTarget(null); }"), 'setActiveCase must clear the call of the old case');
});

test('Back and Esc keep the last call so Forward can reopen it', () => {
  const follow = body('function followHistoryMark');
  assert.ok(src.includes('let lastSlowCall: SlowCallTarget | null = null;'), 'lastSlowCall module variable missing');
  assert.ok(follow.includes('slowCall = slowCall ?? lastSlowCall ?? storedCallTarget();'), 'Forward must read lastSlowCall');
  assert.ok(!body('export function closeSlowCall').includes('lastSlowCall = null;') || body('export function closeSlowCall').includes('if (handoff) lastSlowCall = null;'), 'default close must not clear lastSlowCall');
  assert.ok(body('export function openSlowCall').includes('lastSlowCall = target;'), 'a new call must replace lastSlowCall');
  assert.ok(body('function setTracing').includes('lastSlowCall = null;'), 'closing the timeline must clear lastSlowCall');
  assert.ok(body('export function setActiveCase(').includes('lastSlowCall = null;'), 'a case switch must clear lastSlowCall');
  assert.ok(follow.includes('storedCallTarget(slowCall);'), 'Forward must store the reopened target again');
});

test('closeSlowCall({ handoff: true }) leaves the replace to the next screen and does not step back (R2-01)', () => {
  const close = body('export function closeSlowCall');
  assert.ok(close.includes('closeSlowCall(options?: { handoff?: boolean })'), 'signature');
  const handoffAt = close.indexOf('if (handoff) { handoffReplacePending = true; return; }');
  const stepAt = close.indexOf('marker.stepBack(CASE_TRACING_MARK)');
  assert.ok(handoffAt >= 0 && stepAt > handoffAt, 'the hand-off flag must be set before the default step back');
  assert.ok(!close.includes('marker.markScreen(CASE_TRACING_MARK, true)'), 'the hand-off must not mark a second timeline entry: the one below it is already there');
  assert.ok(src.includes('let handoffReplacePending = false;'), 'the one-shot replace flag missing');
  const note = body('function noteScreenHistory');
  const takeAt = note.indexOf('const replaceCallEntry = handoffReplacePending;');
  const clearAt = note.indexOf('handoffReplacePending = false;');
  const guardAt = note.indexOf('if (!marker || !drawers || !isHistoryScreen(to)) return;');
  assert.ok(takeAt >= 0 && clearAt > takeAt && clearAt < guardAt, 'the flag is taken and cleared before any early return, so a refused move cannot leave it set');
  assert.ok(note.includes('if (replaceCallEntry && marker.screen === CASE_CALL_MARK) marker.markScreen(to, true);'), 'the next screen must replace the call entry in place');
  assert.ok(note.indexOf('marker.markScreen(to, true)') < note.indexOf("marker.markScreen(to, step === 'replace')"), 'the hand-off replace comes before the usual push');
});

test('Back and Esc keep the stored call target, so Forward can reopen the call (R2-02)', () => {
  const tracing = body('function setTracing');
  const closed = tracing.slice(tracing.indexOf('if (!id)'));
  assert.ok(closed.includes('slowCall = null; lastSlowCall = null;'), 'closing the timeline still clears the open and the last call');
  assert.ok(!closed.includes('storedCallTarget'), 'closing the timeline must not erase the stored target: the call entry is still in the history');
  const close = body('export function closeSlowCall');
  assert.match(close, /if \(handoff\) storedCallTarget\(null\);/, 'only the hand-off forgets the call');
  assert.doesNotMatch(close, /^\s*storedCallTarget\(null\);/m, 'a plain close (Esc, Back, crumb) must keep the stored target');
  const follow = body('function followHistoryMark');
  assert.ok(follow.includes('if (slowCall) storedCallTarget(slowCall);'), 'stepping off the call entry must not erase the stored target');
  assert.ok(follow.includes('slowCall = slowCall ?? lastSlowCall ?? storedCallTarget();'), 'Forward still reads the stored target');
  // Leaving the case work for another screen, and a case switch, still forget the call.
  assert.ok(body('function noteCaseView').includes('storedCallTarget(null);'), 'leaving for another screen still forgets the call');
  assert.ok(body('export function setActiveCase(').includes('storedCallTarget(null);'), 'a case switch still forgets the call');
});

test('a kept stored target never reopens the call on a reload', () => {
  const first = body('async function chooseFirstScreen');
  assert.ok(first.includes("if (remembered === 'call' && !deepLink)"), 'only a remembered call screen restores the call');
  assert.ok(first.includes('const saved = storedCallTarget();'), 'the stored target is read only for that screen');
  assert.ok(body('function setTracing').includes("storedCaseView(id ? 'tracing' : 'case');"), 'closing the timeline remembers the timeline, not the call');
  assert.ok(body('export function closeSlowCall').includes("storedCaseView('tracing');"), 'closing the call remembers the timeline, not the call');
});

test('the timeline hand-off runs the window it set when the analysis opens (P46-01)', () => {
  assert.ok(src.includes('let handoffRunPending = false;'), 'the one-shot run flag missing');
  assert.ok(body('export function setTracingHandoff').includes('handoffRunPending = true;'), 'the hand-off must ask for a run on its own window');
  const open = body('export function openAnalysis');
  const takeAt = open.indexOf('const runHandoff = handoffRunPending;');
  const clearAt = open.indexOf('handoffRunPending = false;');
  assert.ok(takeAt >= 0 && clearAt > takeAt, 'openAnalysis takes the one-shot run flag and clears it');
  assert.ok(open.includes("if (!go('running') || !runHandoff) return;"), 'the run follows the move to the analysis, and only for a hand-off');
  assert.ok(open.includes('void runAnalysis(true);'), 'the handed-over window is run, not left as a draft under the last run');
  // The landing run stays one-shot: the hand-off run is a run of its own, not a second landing run.
  assert.match(src, /autoRan \|\| screen !== 'running'/);
});

test('Esc skips an open menu inside a hidden part of the page', () => {
  const keys = body('export function onWindowKey(');
  assert.ok(keys.includes(".find((d) => !d.closest('[hidden]'))"), 'hidden menus must be skipped');
});

test('CI-01: body() isolates the closing branch on a CRLF checkout', () => {
  const raw = fs.readFileSync(path.join(__dirname, '..', 'src', 'lib', 'appState.svelte.ts'), 'utf8');
  const crlf = raw.replace(/\r\n/g, '\n').replace(/\n/g, '\r\n');
  assert.equal(crlf.indexOf('\n}\n'), -1, 'sanity: raw CRLF has no LF-only close marker (the original CI bug)');
  const tracing = body('function setTracing', crlf);
  const closed = tracing.slice(tracing.indexOf('if (!id)'));
  assert.ok(closed.includes('slowCall = null; lastSlowCall = null;'), 'the closing branch is isolated on CRLF');
  assert.ok(!closed.includes('storedCallTarget'), 'CRLF must not let the closing branch swallow later storedCallTarget text');
});
