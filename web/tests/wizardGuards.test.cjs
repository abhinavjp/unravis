const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { loadTypeScript } = require('./helpers/loadTs.cjs');
const read = (p) => fs.readFileSync(path.join(__dirname, '..', p), 'utf8');

const g = loadTypeScript('src/lib/wizardGuards.ts');
const snap = (o) => ({ type: 'text', filled: false, files: 0, touched: false, ...o });

test('typed text counts as unsaved input only after the person typed it and while it is not blank', () => {
  assert.equal(g.fieldHoldsInput(snap({ filled: true, touched: true })), true);
  assert.equal(g.fieldHoldsInput(snap({ filled: true })), false, 'a prefilled address is not input');
  assert.equal(g.fieldHoldsInput(snap({ filled: false, touched: true })), false);
  assert.equal(g.fieldHoldsInput(snap({ touched: true })), false, 'typed and cleared again');
  assert.equal(g.fieldHoldsInput(snap({ type: 'password', filled: true, touched: true })), true);
  assert.equal(g.fieldHoldsInput(snap({ type: 'textarea', filled: true, touched: true })), true);
});

test('ticks and lists count once the person changed them; buttons and hidden fields never do', () => {
  assert.equal(g.fieldHoldsInput(snap({ type: 'checkbox', touched: true })), true);
  assert.equal(g.fieldHoldsInput(snap({ type: 'checkbox' })), false, 'a tick the form made itself');
  assert.equal(g.fieldHoldsInput(snap({ type: 'select', touched: true })), true);
  assert.equal(g.fieldHoldsInput(snap({ type: 'hidden', filled: true, touched: true })), false);
  assert.equal(g.fieldHoldsInput(snap({ type: 'submit', filled: true, touched: true })), false);
  assert.equal(g.fieldHoldsInput(snap({ type: 'file', files: 1 })), true);
  assert.equal(g.holdsUnsavedInput([snap(), snap({ type: 'checkbox' })]), false);
  assert.equal(g.holdsUnsavedInput([snap(), snap({ filled: true, touched: true })]), true);
});

test('the snapshot of a field never copies its text, only whether there is any', () => {
  const field = { tagName: 'INPUT', type: 'password', value: 'hunter2', files: null };
  const root = { querySelectorAll: () => [field] };
  const [one] = g.snapshotFields(root, new WeakSet([field]));
  assert.equal(one.filled, true);
  assert.equal(JSON.stringify(one).includes('hunter2'), false);
  assert.equal('value' in one, false);
  assert.equal(g.snapshotFields({ querySelectorAll: () => [{ ...field, value: '  ' }] }, new WeakSet())[0].filled, false);
});

test('names are compared without case, trailing dot or the default port', () => {
  assert.equal(g.nameKey('redis', 'Cache-1.:6379'), 'cache-1');
  assert.equal(g.nameKey('redis', 'cache-1'), 'cache-1');
  assert.equal(g.nameKey('redis', 'cache-1:6380'), 'cache-1:6380');
  assert.equal(g.nameKey('sql-server', 'DB1.:1433'), 'db1');
  assert.equal(g.nameKey('sql-server', '192.168.4.2,1655'), '192.168.4.2,1655');
  assert.equal(g.nameKey('blackbox', 'Server 1'), 'server 1');
  assert.equal(g.nameKey('blackbox', 'Rec.'), 'rec.', 'a recorder named with a dot is not the one without');
  assert.equal(g.nameKey('blackbox', 'D:\\Blackbox'), 'd:\\blackbox');
  const found = g.alreadyInCase([{ kind: 'redis', ref: 'a', label: 'cache-1:6379' }, { kind: 'redis', ref: 'b', label: 'cache-1:6380' }], [{ kind: 'redis', label: 'CACHE-1.' }]);
  assert.deepEqual(found.map((s) => s.ref), ['a']);
});

test('the tabs are off while the case is read again before a send, and a read that ends after the tab was left sends nothing', () => {
  const hub = read('src/components/sources/SourcesHub.svelte');
  assert.match(hub, /disabled=\{createView\.running \|\| addGuard\.preparing\}/);
  assert.match(hub, /if \(next === tab \|\| addGuard\.preparing\) return;/);
  const flow = read('src/components/sourcewiz/AddSourcesFlow.svelte');
  assert.match(flow, /if \(!\(await prepare\(\)\)\) return;\s+\/\/[^\n]*\n\s+if \(screen !== 'review'\) return;\s+const dropped = dropDuplicates\(\);/, 'a send happens only from the Review the person saw');
  assert.match(flow, /gone = true; if \(iSetPreparing\)/, 'only the flow that set the flag clears it');
  assert.match(flow, /disabled=\{addGuard\.preparing\} onclick=\{\(\) => void go\('add'\)\}/, 'Back is off while the case is read');
  assert.match(flow, /role="status" tabindex="-1" bind:this=\{statusEl\}/, 'a status says why, and focus rests on it');
  assert.match(flow, /const target = caseId;/);
});

test('leaving the Add data tab keeps an unfinished list and failures on screen, and nothing else', () => {
  const f = (o) => ({ sources: 0, started: false, failures: 0, running: false, ...o });
  assert.equal(g.keepOnLeave(f({ sources: 2 })), true);
  assert.equal(g.keepOnLeave(f({})), false);
  assert.equal(g.keepOnLeave(f({ sources: 2, started: true, failures: 1 })), true);
  assert.equal(g.keepOnLeave(f({ sources: 2, started: true })), false, 'a clean end is cleared');
  assert.equal(g.keepOnLeave(f({ sources: 2, running: true })), false, 'a run in flight is not this rule');
});

test('a source the case already holds is found by kind and name, for the kinds whose tag is the whole source', () => {
  const chosen = [
    { kind: 'redis', ref: 'a', label: 'Cache-1:6379' }, { kind: 'redis', ref: 'b', label: 'cache-2:6379' }, { kind: 'sql-server', ref: 'c', label: 'db1' },
    { kind: 'mongo-database', ref: 'd', label: 'audit' }, { kind: 'iis-w3c', ref: 'e', label: 'u_ex1.log' }, { kind: 'redis', ref: 'f', label: '' }
  ];
  const existing = [{ kind: 'redis', label: ' cache-1:6379 ' }, { kind: 'mongo-database', label: 'audit' }, { kind: 'iis-w3c', label: 'u_ex1.log' }, { kind: 'sql-server', label: 'other' }, { kind: 'redis', label: '' }];
  assert.deepEqual(g.alreadyInCase(chosen, existing).map((s) => s.ref), ['a']);
  assert.deepEqual(g.alreadyInCase(chosen, []), []);
});

test('a fresh read of the case says whether it can take data', () => {
  assert.equal(g.gateOfView({ state: 'open' }), 'open');
  assert.equal(g.gateOfView({}), 'open');
  assert.equal(g.gateOfView({ state: 'closed' }), 'closed');
  assert.equal(g.gateOfView({ state: 'archived' }), 'closed');
  assert.equal(g.gateOfView({ closed: '2026-10-20T00:00:00Z' }), 'closed');
  assert.equal(g.gateOfView({ state: 'open', untrusted: true }), 'closed');
  assert.equal(g.gateOfView(null), 'gone');
});

test('reset guard: the draft is not reset while a run is writing it, and is when it ends', () => {
  const w = loadTypeScript('src/lib/wizard.svelte.ts');
  w.wizard.title = 'Keep me';
  let running = true;
  w.onWizardResetBlocked(() => running);
  assert.equal(w.wizardResetBlocked(), true);
  assert.equal(w.resetWizard(), false);
  assert.equal(w.wizard.title, 'Keep me');
  running = false;
  assert.equal(w.resetWizard(), true);
  assert.equal(w.wizard.title, '');
});

test('the create state registers its run as a reset blocker, and the kept add list is dropped when the case page is left', () => {
  assert.match(read('src/lib/wizardCreate.svelte.ts'), /onWizardResetBlocked\(\(\) => createView\.running\)/);
  const gs = read('src/lib/wizardGuards.svelte.ts');
  assert.match(gs, /export function dropKeptAddDraft/);
  assert.match(gs, /addGuard\.keptFor = '';\s+resetWizard\(\)/);
});

test('the hub asks before a tab switch away from a card with typed input, through one function every switch uses', () => {
  const hub = read('src/components/sources/SourcesHub.svelte');
  assert.match(hub, /function goTab\(next: Tab\)/);
  assert.match(hub, /if \(tab === 'add' && addGuard\.entryDirty\) \{ askTab = next; return; \}/);
  assert.match(hub, /onclick=\{\(\) => goTab\(item\.id\)\}/);
  assert.match(hub, /return dropKeptAddDraft/);
  assert.doesNotMatch(hub, /onclick=\{\(\) => \{ tab = item\.id; \}\}/);
});

test('the Add sources flow reads the case before the draft question, again at Review, at the run and when the window gets focus back', () => {
  const flow = read('src/components/sourcewiz/AddSourcesFlow.svelte');
  assert.match(flow, /await Promise\.all\(\[checkCase\(signal\), loadCase\(signal\)\]\);[\s\S]*if \(foreign\) \{ checking = false; if \(gate === 'open' && !readOnly\) askDiscard = true; \}/);
  assert.match(flow, /window\.addEventListener\('focus', looksAgain\)/);
  assert.match(flow, /async function prepare\(\)[\s\S]*await Promise\.all\(\[checkCase\(\), loadCase\(\)\]\)/);
  assert.match(flow, /async function go\(to: Screen\) \{\s+if \(to === 'review'\) \{[\s\S]*await prepare\(\)/);
  assert.match(flow, /async function run\(\) \{[\s\S]*await prepare\(\)[\s\S]*const dropped = dropDuplicates\(\);[\s\S]*if \(closed\)/);
  assert.match(flow, /\{:else if foreign && !owned\}/, 'a draft left in the way is never reset when the case cannot take data');
  assert.match(flow, /if \(!resetWizard\(\)\) return;/, 'a refused reset changes nothing on screen');
});

test('the card host and the other reset actions ask first, and no typed value is stored by any guard', () => {
  const step = read('src/components/sourcewiz/SourceStep.svelte');
  assert.match(step, /function askBack\(\)/);
  assert.match(step, /onclick=\{askBack\}/);
  const find = read('src/components/sourcewiz/FindOnPc.svelte');
  assert.match(find, /sourcesView\.phase === 'done' && hasTicks\(\)\) \{ askAgain = true; return; \}/);
  assert.match(read('src/components/CreateWizard.svelte'), /onclick=\{\(\) => \{ askStartOver = true; \}\}/);
  for (const file of ['src/lib/wizardGuards.ts', 'src/lib/wizardGuards.svelte.ts', 'src/components/sourcewiz/ConfirmAsk.svelte', 'src/components/sourcewiz/SourceStep.svelte', 'src/components/sourcewiz/AddSourcesFlow.svelte', 'src/components/sources/SourcesHub.svelte']) {
    assert.doesNotMatch(read(file), /localStorage|sessionStorage|console\.(log|info|warn|error)/, file + ' keeps nothing and logs nothing');
  }
});
