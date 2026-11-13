// Source-text checks for the Slow call screen state in appState.svelte.ts.
const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');

const src = fs.readFileSync(path.join(__dirname, '..', 'src', 'lib', 'appState.svelte.ts'), 'utf8');

function body(startText) {
  const start = src.indexOf(startText);
  assert.ok(start >= 0, `${startText} missing`);
  return src.slice(start, src.indexOf('\n}\n', start));
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

test('closeSlowCall({ handoff: true }) replaces the entry and does not step back', () => {
  const close = body('export function closeSlowCall');
  assert.ok(close.includes('closeSlowCall(options?: { handoff?: boolean })'), 'signature');
  const handoffAt = close.indexOf('if (handoff) { marker.markScreen(CASE_TRACING_MARK, true); return; }');
  const stepAt = close.indexOf('marker.stepBack(CASE_TRACING_MARK)');
  assert.ok(handoffAt >= 0 && stepAt > handoffAt, 'handoff replace must come before the default step back');
});

test('Esc skips an open menu inside a hidden part of the page', () => {
  const keys = body('export function onWindowKey(');
  assert.ok(keys.includes(".find((d) => !d.closest('[hidden]'))"), 'hidden menus must be skipped');
});
