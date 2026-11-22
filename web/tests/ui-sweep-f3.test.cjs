const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const { loadTypeScript } = require('./helpers/loadTs.cjs');

// UI sweep, package F3 (cases, create wizard, keys, erase, update bar, names). Each test is named with the finding it covers.
// Logic goes through loadTypeScript; layout and focus are covered by tools/e2e/specs/ui-sweep-f3.spec.ts.

const root = path.resolve(__dirname, '..');
const read = (r) => fs.readFileSync(path.join(root, r), 'utf8').replace(/\r\n/g, '\n');
const create = loadTypeScript('src/lib/copy/wizardCreateCopy.ts');
const importCopy = loadTypeScript('src/lib/copy/importCopy.ts');
const lifecycle = loadTypeScript('src/lib/caseLifecycle.ts');
const update = loadTypeScript('src/lib/updateCheck.ts');
const sources = loadTypeScript('src/lib/copy/sourcesCopy.ts');
const zone = loadTypeScript('src/lib/copy/zoneChoiceCopy.ts');
const home = loadTypeScript('src/lib/copy/caseHomeCopy.ts');
const draftStore = loadTypeScript('src/lib/wizardDraftStore.ts');

test('SWEEP-F3-01 (CASES-05): a blocked Create case button says why, in the order the person can fix it', () => {
  assert.equal(create.createBlockedLine([]), '');
  assert.equal(create.createBlockedLine(['no-ack']), create.CREATE_TEXT.blockedAck);
  assert.equal(create.createBlockedLine(['files', 'no-ack']), create.CREATE_TEXT.blockedAck, 'the notice comes before the file list');
  assert.equal(create.createBlockedLine(['scan-busy', 'no-ack']), create.CREATE_TEXT.blockedScan, 'a running scan comes first');
  assert.equal(create.createBlockedLine(['zone']), create.CREATE_TEXT.blockedZone);
  for (const line of Object.values(create.CREATE_TEXT).filter((t) => typeof t === 'string')) assert.doesNotMatch(line, /\bWait for\b/);
});

test('SWEEP-F3-02 (CASES-10): the confirm line says "file" for one file and "files" for several', () => {
  assert.equal(importCopy.ackLabelFor(1), importCopy.ACK_LABEL);
  assert.equal(importCopy.ackLabelFor(0), importCopy.ACK_LABEL);
  assert.equal(importCopy.ackLabelFor(3), importCopy.ACK_LABEL_MANY);
  assert.match(importCopy.ACK_LABEL, /this file meets/);
  assert.match(importCopy.ACK_LABEL_MANY, /these files meet/);
});

test('SWEEP-F3-03 (CASES-14): a closed case is erased 30 days after it was closed, from the close time', () => {
  assert.equal(lifecycle.CLOSED_RETENTION_DAYS, 30);
  assert.equal(lifecycle.closedEraseAt('2026-11-01T10:00:00Z'), '2026-12-01T10:00:00.000Z');
  assert.equal(lifecycle.closedEraseAt(null), null);
  assert.equal(lifecycle.closedEraseAt('not a time'), null);
  assert.match(home.closedEraseText('1 Dec 2026 10:00'), /^It will be erased on 1 Dec 2026 10:00 unless you reopen it\./);
});

test('SWEEP-F3-04 (CASES-20): a failed update says what went wrong and that nothing was changed; any other reason is shown as it came', () => {
  assert.equal(update.failedReason(undefined), '');
  assert.equal(update.failedReason('disk full'), 'disk full');
  const hash = update.failedReason('downloaded file does not match the signed release');
  assert.match(hash, /did not match the signed release/);
  assert.match(hash, /Nothing was changed\.$/);
  const note = update.installNote({ install: { state: 'failed', version: '1.3.0', error: 'disk full' } });
  assert.equal(note, 'Update failed. disk full');
});

test('SWEEP-F3-05 (CASES-17): the update bar says "You have version X", not "v"', () => {
  const src = read('src/lib/updateCheck.ts');
  assert.match(src, /You have version /);
  assert.doesNotMatch(src, /You have v\$\{/);
});

test('SWEEP-F3-06 (CASES-29): connection counts read as one sentence and the MongoDB list shows no limit', () => {
  assert.equal(sources.connectionCountText(1, 20, 1), '1 of 20 SQL Server connections saved. 1 MongoDB connection saved.');
  assert.equal(sources.connectionCountText(0, 20, 3), '0 of 20 SQL Server connections saved. 3 MongoDB connections saved.');
  assert.equal(sources.SOURCES_TEXT?.wordsNote, undefined);
});

test('SWEEP-F3-07 (CASES-29e): the stored computer name this-pc is shown as This PC', () => {
  assert.equal(zone.hostShown('this-pc'), 'This PC');
  assert.equal(zone.hostShown('web-01'), 'web-01');
  assert.equal(zone.hostOptionText({ label: 'this-pc', tz: 'Europe/London' }), 'This PC (Europe/London)');
  assert.equal(zone.hostOptionText({ label: 'this-pc', tz: '' }), 'This PC');
  assert.match(zone.hostSummaryText('this-pc', ''), /^Computer: This PC/);
  assert.equal(zone.hostSummaryText('', ''), '');
  assert.equal(create.hostSummaryText([{ label: 'WEB01', count: 3 }, { label: 'this-pc', count: 1 }]), '3 files under WEB01, 1 file under This PC.');
});

test('SWEEP-F3-08 (CASES-25): the draft keeps only the name, the environments and the own-key switch', () => {
  const store = new Map();
  global.sessionStorage = { getItem: (k) => (store.has(k) ? store.get(k) : null), setItem: (k, v) => store.set(k, String(v)), removeItem: (k) => store.delete(k) };
  try {
    assert.equal(draftStore.readSavedDraft(), null);
    draftStore.saveDraft({ title: 'Checkout slowdown', envText: 'prod, test', keyOn: true });
    const raw = [...store.values()][0];
    assert.deepEqual(Object.keys(JSON.parse(raw)).sort(), ['envText', 'keyOn', 'title']);
    assert.deepEqual({ ...draftStore.readSavedDraft() }, { title: 'Checkout slowdown', envText: 'prod, test', keyOn: true });
    store.set([...store.keys()][0], '{broken');
    assert.equal(draftStore.readSavedDraft(), null, 'unreadable storage starts an empty draft');
    draftStore.saveDraft({ title: 'x', envText: '', keyOn: false });
    draftStore.clearSavedDraft();
    assert.equal(store.size, 0);
  } finally { delete global.sessionStorage; }
  assert.doesNotThrow(() => draftStore.saveDraft({ title: 'x', envText: '', keyOn: false }), 'no storage is not an error');
  const wizard = read('src/lib/wizard.svelte.ts');
  assert.doesNotMatch(wizard, /sessionStorage|localStorage/, 'the wizard state itself never touches a browser store');
  assert.match(wizard, /persistDraft/);
  assert.match(read('src/components/CreateWizard.svelte'), /persistDraft\(caseMade\(\)\)/);
  assert.doesNotMatch(read('src/lib/wizardDraftStore.ts').replace(/\/\/.*$/gm, ''), /password|passphrase|connection/i, 'the stored shape has no secret field');
});

test('SWEEP-F3-09 (CASES-03, CASES-04): one name, "Add files" for the action and "theory" for the entry', () => {
  const header = read('src/components/AppHeader.svelte');
  assert.match(header, /Add files/);
  const ledger = loadTypeScript('src/lib/ledger.ts');
  assert.equal(ledger.ledgerCopy.addLabel, 'New theory');
  assert.match(ledger.ledgerCopy.empty, /No theories yet/);
  assert.doesNotMatch(JSON.stringify(ledger.ledgerCopy), /hypothes/i, 'what the person reads speaks of theories');
  // The hub tab, its drawer and the guided button keep "Add data": they hold SQL Server, MongoDB, Blackbox, Files and Another day (review B3).
  assert.equal(sources.HUB_TEXT.add, 'Add data');
  assert.equal(sources.ADD_TEXT.heading, 'Add data');
  assert.equal(sources.ADD_TEXT.drawerTitle('k7m2qx4a'), 'Add data, case k7m2qx4a');
  assert.equal(sources.ADD_TEXT.addFiles, 'Add files', 'the Files card button is the file import');
  assert.equal(loadTypeScript('src/lib/copy/guidedCopy.ts').GUIDED_TEXT.addData, 'Add data');
  assert.match(read('src/lib/copy/caseRestoreCopy.ts'), /collection_missing: '[^']*Open Add data, pick the right collection/);
  assert.doesNotMatch(read('src/lib/copy/wizardCreateCopy.ts'), /Open Add files|from Add files/);
});

test('SWEEP-F3-10 (CASES-16): the file cards in the wizard have the same height and a select wide enough for its words', () => {
  const css = read('src/components/wizard/ImportFilesPanel.svelte');
  assert.match(css, /min-width:\s*27ch/);
  assert.match(css, /min-height:\s*24px/);
});

test('SWEEP-F3-11 (CASES-18): the create progress marks the running step and ticks the done ones', () => {
  const src = read('src/components/wizard/CreateProgress.svelte');
  assert.match(src, /\[data-status='running'\]/);
  assert.match(src, /content: '\\2713/);
});

test('SWEEP-F3-12 (ANALYSIS-15): the Slow call screen marks the demo block as demo only inside a closed details', () => {
  const inspector = read('src/components/CallInspector.svelte');
  assert.match(inspector, /<details class="scall-pretend" data-testid="scall-pretend">/);
  assert.match(read('src/lib/drill/drillCopy.ts'), /pretendSummary: 'Demo only'/);
  assert.match(read('src/components/CallNearby.svelte'), /\{#if own\}<strong class="mark">\{DRILL_TEXT\.thisCallMark\}/);
});

test('SWEEP-F3-13 (ANALYSIS-16): the Investigation timeline has no UTC note line, no duplicate All cases button and a roomier label column', () => {
  assert.doesNotMatch(read('src/lib/tracing/windows.ts'), /utcNote/);
  assert.doesNotMatch(read('src/components/TracingPaper.svelte'), /utcNote/);
  assert.match(read('src/components/TracingChart.svelte'), /minmax\(8rem, 11\.25rem\)/);
  assert.match(read('src/lib/components/CaseHome.svelte'), /\{#if !timelineShown\}/);
  const lanes = loadTypeScript('src/lib/tracing/lanes.ts');
  assert.ok(lanes.COVER_TEXT.noRange.length < 90, 'the no-range line fits one row of the lane');
});
