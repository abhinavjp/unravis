const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const { loadTypeScript } = require('./helpers/loadTs.cjs');

// Lane W: interval wording, reason-aware next steps, reload hint, Start/Stop logic, Stop confirm, no raw errors.

const root = path.resolve(__dirname, '..');
const read = (r) => fs.readFileSync(path.join(root, r), 'utf8').replace(/\r\n/g, '\n');
const bb = loadTypeScript('src/lib/blackbox.ts');
const copy = loadTypeScript('src/lib/copy/blackboxCopy.ts');
const { BB_TEXT: T, BB_REFUSAL } = copy;

test('UX-01: one helper words the interval, with the effective value when it differs', () => {
  assert.equal(copy.intervalWords(15), 'Recording every 15 seconds');
  assert.equal(copy.intervalWords(15, 15), 'Recording every 15 seconds');
  assert.equal(copy.intervalWords(15, undefined), 'Recording every 15 seconds');
  assert.equal(copy.intervalWords(15, 60), 'Recording every 60 seconds (set to 15; slowed to stay light)');
  assert.match(copy.intervalWords(30, 15), /Recording every 15 seconds \(set to 30;/);
  const panel = read('src/components/BlackboxPanel.svelte');
  assert.match(panel, /intervalWords\(f\.intervalSeconds, f\.effectiveIntervalSeconds\)/);
  assert.doesNotMatch(panel, /intervalSeconds\} \{T\.seconds\}/);
  assert.match(read('src/components/BlackboxAdd.svelte'), /intervalWords\(outcome\.answer\.facts\.intervalSeconds, outcome\.answer\.facts\.effectiveIntervalSeconds\)/);
});

test('UX-02: Unreachable advice follows the server reason', () => {
  const next = (reason) => copy.stateNextFor({ state: 'unreachable', reason }, true);
  assert.match(next('key'), /read key was refused/);
  assert.match(next('wait'), /Wait a few minutes/);
  assert.match(next('busy'), /busy/);
  assert.match(next('newer-format'), /Update Unravis/);
  assert.equal(next('timeout'), T.stateNext.unreachable);
  assert.equal(next(''), T.stateNext.unreachable);
  assert.equal(next(undefined), T.stateNext.unreachable);
  assert.equal(copy.stateNextFor({ state: 'running' }, true), T.stateNext.running);
});

test('UX-13/14: paused differs from low, and this PC is told Stop then Start', () => {
  assert.match(copy.stateNextFor({ state: 'disk-low', tone: 'bad' }, true), /resumes by itself/);
  assert.match(copy.stateNextFor({ state: 'disk-low', tone: 'warn' }, true), /before it pauses/);
  assert.match(copy.stateNextFor({ state: 'running-late' }, true), /Stop, then Start/);
  assert.match(copy.stateNextFor({ state: 'running-late' }, false), /Services/);
});

test('UX-05: offline and no_session say reload, and the helper tells them apart', () => {
  assert.match(BB_REFUSAL.offline, /reload this page/i);
  assert.match(BB_REFUSAL.no_session, /Reload this page/);
  const mk = (reason) => new bb.BlackboxError({ status: 0, error: 'x', reason });
  assert.equal(bb.needsReload(mk('offline')), true);
  assert.equal(bb.needsReload(mk('no_session')), true);
  assert.equal(bb.needsReload(mk('rate')), false);
  assert.equal(bb.needsReload(new Error('x')), false);
  const panel = read('src/components/BlackboxPanel.svelte');
  assert.match(panel, /onclick=\{reload\}>\{T\.reload\}/);
});

test('UX-06: an unknown reason gets a cause and a next step, never a bare line', () => {
  const err = new bb.BlackboxError({ status: 500, error: 'The Blackbox request failed (500).', reason: 'other' });
  assert.equal(bb.refusalText(err, BB_REFUSAL), BB_REFUSAL.other);
  assert.match(BB_REFUSAL.other, /Press the button again/);
  assert.match(T.caseUseFailed, /Try again/);
  assert.match(T.keyFailed, /Press Show again/);
  assert.doesNotMatch(read('src/components/BlackboxCaseUse.svelte'), /problem = error instanceof Error && error\.message \? error\.message : T\.caseUseFailed/);
  assert.match(read('src/components/BlackboxReadKey.svelte'), /refusalText\(error,/);
});

test('UX-03: Start and Stop follow the service, never both', () => {
  const pc = (installed, running) => ({ installed, running });
  assert.deepEqual(bb.pcControls('running', pc(true, true)), { start: false, stop: true });
  assert.deepEqual(bb.pcControls('stopped', pc(true, false)), { start: true, stop: false });
  assert.deepEqual(bb.pcControls('unreachable', pc(true, true)), { start: false, stop: true });
  assert.deepEqual(bb.pcControls('unreachable', pc(true, false)), { start: true, stop: false });
  assert.deepEqual(bb.pcControls('not-found', pc(false, false)), { start: false, stop: false });
  assert.deepEqual(bb.pcControls('running-late', pc(true, false)), { start: false, stop: true });
});

test('UX-04: Stop asks first, with Keep recording focused', () => {
  const panel = read('src/components/BlackboxPanel.svelte');
  assert.match(panel, /onclick=\{askStop\}/);
  assert.doesNotMatch(panel, /onclick=\{stop\}>\{T\.stop\}/);
  assert.match(panel, /keepBtn\?\.focus\(\)/);
  assert.match(panel, /\{T\.stopYes\}/);
  assert.match(T.stopTitle, /Stop recording\?/);
});

test('UX-09: a link without a saved key says so, and can be repaired', () => {
  assert.match(T.readKeyMissing, /No read key is saved/);
  const panel = read('src/components/BlackboxPanel.svelte');
  assert.match(panel, /T\.readKeyMissing/);
  assert.match(panel, /canChangeKey/);
});

test('UX-11: age in words, exact time kept', () => {
  const now = Date.parse('2026-11-17T09:07:00Z');
  assert.equal(copy.ageWords('2026-11-17T09:05:00Z', now), '2 minutes ago');
  assert.equal(copy.ageWords('2026-11-17T09:06:40Z', now), 'less than a minute ago');
  assert.equal(copy.ageWords('2026-11-17T07:07:00Z', now), '2 hours ago');
  assert.equal(copy.ageWords('nonsense', now), '');
  assert.equal(copy.ageWords(undefined, now), '');
  assert.equal(copy.ageWords('2027-01-01T00:00:00Z', now), '');
});

test('UX-19: failed add lists only the reasons that fit', () => {
  assert.equal(copy.addFailReasonsFor('url').length, 3);
  assert.equal(copy.addFailReasonsFor('folder').length, 3);
  assert.equal(copy.addFailReasonsFor(null).length, 5);
  assert.ok(copy.addFailReasonsFor('url').every((r) => !/share/i.test(r.split('.')[0]) ));
});

test('the new texts pass the copy lint list and hold no dash', () => {
  const entries = copy.blackboxCopyEntries();
  assert.ok(entries.some((e) => e.id === 'blackbox.interval.slow'));
  for (const e of entries) assert.doesNotMatch(e.text, /[–—]/, e.id);
});
