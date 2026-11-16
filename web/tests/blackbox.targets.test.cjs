const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const { loadTypeScript } = require('./helpers/loadTs.cjs');
const ts = require('typescript');

// BBX-1-12: the install-wizard target picker. Targets are owner-selected app pools and services,
// stored as kind+name only. This-PC mode reads the detected names once on mount; pack/remote mode
// is manual entry. A not-running target says "not running", never 0.

const root = path.resolve(__dirname, '..');
const read = (relative) => fs.readFileSync(path.join(root, relative), 'utf8').replace(/\r\n/g, '\n');

const bb = loadTypeScript('src/lib/blackbox.ts');
const { BB_TEXT, BB_REFUSAL, blackboxCopyEntries } = loadTypeScript('src/lib/copy/blackboxCopy.ts');
const { lintScreenStrings } = loadTypeScript('src/lib/copy/copylint.ts');
const { deck } = loadTypeScript('src/lib/copy/deck.ts');

/* ------------------------------------------------------------------ the pure helper */

test('toggleTarget adds a kind+name pair, removes it on a second call, and never mutates the list given', () => {
  const start = [];
  const added = bb.toggleTarget(start, 'appPool', 'DefaultAppPool');
  assert.deepEqual(added, [{ kind: 'appPool', name: 'DefaultAppPool' }]);
  assert.deepEqual(start, [], 'the list given is not changed');
  assert.notEqual(added, start, 'a new list comes back');
  const both = bb.toggleTarget(added, 'service', 'W3SVC');
  assert.deepEqual(both, [{ kind: 'appPool', name: 'DefaultAppPool' }, { kind: 'service', name: 'W3SVC' }]);
  const removed = bb.toggleTarget(both, 'appPool', 'DefaultAppPool');
  assert.deepEqual(removed, [{ kind: 'service', name: 'W3SVC' }]);
  assert.deepEqual(both, [{ kind: 'appPool', name: 'DefaultAppPool' }, { kind: 'service', name: 'W3SVC' }], 'removal does not change the list given');
  assert.deepEqual(bb.toggleTarget(removed, 'service', 'W3SVC'), []);
});

test('toggleTarget matches kind and name together, and a stored target holds only kind and name', () => {
  const one = bb.toggleTarget([], 'appPool', 'DefaultAppPool');
  // The same name under the other kind is a different target: it is added, not removed.
  const two = bb.toggleTarget(one, 'service', 'DefaultAppPool');
  assert.deepEqual(two, [{ kind: 'appPool', name: 'DefaultAppPool' }, { kind: 'service', name: 'DefaultAppPool' }]);
  // Removing the app pool leaves the service of the same name.
  assert.deepEqual(bb.toggleTarget(two, 'appPool', 'DefaultAppPool'), [{ kind: 'service', name: 'DefaultAppPool' }]);
  for (const t of two) assert.deepEqual(Object.keys(t).sort(), ['kind', 'name'], 'no path, command line or key is stored');
});

/* ------------------------------------------------------------------ settings */

test('defaultSettings starts with no targets, and a settings round-trip keeps the targets picked', () => {
  assert.deepEqual(bb.defaultSettings().targets, []);
  assert.deepEqual(bb.defaultSettings('web-a').targets, []);
  const settings = bb.defaultSettings('web-a');
  settings.targets = bb.toggleTarget(settings.targets, 'appPool', 'DefaultAppPool');
  settings.targets = bb.toggleTarget(settings.targets, 'service', 'W3SVC');
  const back = JSON.parse(JSON.stringify(settings));
  assert.deepEqual(back.targets, [{ kind: 'appPool', name: 'DefaultAppPool' }, { kind: 'service', name: 'W3SVC' }]);
  assert.equal(bb.settingsProblem(back), null, 'picked targets do not make the settings wrong');
});

/* ------------------------------------------------------------------ the client */

const TOKEN = 'bbx-1-12-token';
const realFetch = globalThis.fetch;
test.afterEach(() => { globalThis.fetch = realFetch; delete globalThis.sessionStorage; });

test('the targets client reads GET /api/blackbox/this-pc/targets with the session token, names only', async () => {
  const answer = {
    appPools: [{ name: 'DefaultAppPool', running: true }, { name: 'Classic .NET AppPool', running: false }],
    services: [{ name: 'W3SVC', running: true }],
    detected: true
  };
  const calls = [];
  globalThis.sessionStorage = { getItem: () => TOKEN, setItem() {}, removeItem() {} };
  globalThis.fetch = async (url, init = {}) => {
    calls.push({ url: String(url), method: init.method ?? 'GET', headers: init.headers, credentials: init.credentials });
    return new Response(JSON.stringify(answer), { status: 200, headers: { 'Content-Type': 'application/json' } });
  };
  const got = await bb.blackboxApi.targets();
  assert.deepEqual(got, answer);
  const call = calls.find((c) => c.url.endsWith('/this-pc/targets'));
  assert.ok(call, 'the targets endpoint was called');
  assert.equal(call.url, '/api/blackbox/this-pc/targets');
  assert.equal(call.method, 'GET');
  assert.equal(call.headers['X-Audit-Token'], TOKEN, 'the same auth header as the other calls');
  assert.equal(call.credentials, 'same-origin');
});

/* ------------------------------------------------------------------ copy */

test('the target picker copy passes the screen-string lint and the count guard holds', () => {
  const entries = blackboxCopyEntries();
  assert.ok(entries.length > 80, 'the lint reads the whole copy');
  assert.deepEqual(lintScreenStrings(entries, deck.terms), []);
  for (const key of ['targetsHeading', 'targetsHelp', 'targetsNotRunning', 'targetsAdd', 'targetsKindAppPool', 'targetsKindService']) {
    assert.equal(typeof BB_TEXT[key], 'string', key + ' is a plain string');
  }
  // UX-8: the heading says what the list is in the words the person brings, and the help says what
  // the choice does to the recording, in one plain sentence with no acronym in it.
  assert.equal(BB_TEXT.targetsHeading, 'Websites and services to watch closely (optional)');
  assert.equal(BB_TEXT.targetsHelp, 'Each watched name gets its own processor and memory record too, and none means the whole machine only.');
  assert.doesNotMatch(BB_TEXT.targetsHelp, /CPU|IIS|app pool|counter|machine-wide/i, 'the sentence is plain words, not the field names');
  assert.equal(BB_TEXT.targetsNotRunning, 'not running');
  assert.equal(BB_TEXT.targetsAdd, 'Add target');
});

/* ------------------------------------------------------------------ the Settings step */

test('the Settings step reads targets once on mount, for this PC only, and never polls', () => {
  const comp = read('src/components/BlackboxSettings.svelte');
  assert.match(comp, /onMount\(\(\) => \{\s*if \(targetMode !== 'pc'\) return;/, 'pack/remote mode never fetches');
  assert.match(comp, /blackboxApi\.targets\(\)/, 'the detected names come from the endpoint');
  assert.doesNotMatch(comp, /setInterval|setTimeout/, 'no polling');
  const wizard = read('src/components/BlackboxWizard.svelte');
  assert.match(wizard, /targetMode=\{where === 'pc' \? 'pc' : 'manual'\}/, 'the wizard passes the mode it is in');
});

test('a stopped target says "not running"; an unreadable state is never labelled stopped', () => {
  const comp = read('src/components/BlackboxSettings.svelte');
  // W2-07: configuration presence is not a running claim, and unknown is not stopped.
  // UX-6: when every row of a group is unknown, one group note under the legend stands in for
  // the per-row notes (rendered tests below). No per-row targetsStateUnknown assertion may live
  // in this file: UX-8 deletes that copy key, and a leftover assertion here would break it.
  assert.match(comp, /\{#if targetRunState\(t\) === 'stopped'\}\{' '\}<span class="bb-note">\{T\.targetsNotRunning\}<\/span>/);
  assert.equal(BB_TEXT.targetsNotRunning, 'not running');
});

test('targetRunState reads the server state, an older boolean, or unknown', () => {
  const { targetRunState } = loadTypeScript('src/lib/blackbox.ts');
  assert.equal(targetRunState({ name: 'a', state: 'running' }), 'running');
  assert.equal(targetRunState({ name: 'a', state: 'stopped' }), 'stopped');
  assert.equal(targetRunState({ name: 'a', state: 'unknown' }), 'unknown');
  // An older server that always sends a boolean still reads correctly.
  assert.equal(targetRunState({ name: 'a', running: true }), 'running');
  assert.equal(targetRunState({ name: 'a', running: false }), 'stopped');
  // Nothing known: unknown, never a guessed stopped or running.
  assert.equal(targetRunState({ name: 'a' }), 'unknown');
  // An explicit state wins over a stale boolean.
  assert.equal(targetRunState({ name: 'a', state: 'unknown', running: true }), 'unknown');
});

test('the picker stores only kind+name, and the record.processes checkbox and note are unchanged', () => {
  const comp = read('src/components/BlackboxSettings.svelte');
  assert.match(comp, /toggleTarget\(picked, kind, name\)/, 'checking a name uses the pure helper');
  assert.match(comp, /bind:checked=\{settings\.record\.processes\}/);
  assert.match(comp, /\{#if !settings\.record\.processes\}<p class="bb-note">\{T\.processesNote\}<\/p>\{\/if\}/);
  assert.doesNotMatch(comp, /commandLine|imagePath|\.path\b|readKey|password|secret/, 'no path, command line or key is stored');
  // The manual row adds a name; the chips remove one. Both go through the same kind+name helper.
  assert.match(comp, /function addTarget\(\)/);
  assert.match(comp, /function removeTarget\(kind: TargetKind, name: string\)/);
});

/* ------------------------------------------------------------------ UX-4: the target rules, before the server is asked */

// The rules are the server's own (Validate in internal/blackbox/config.go): at most 32 targets, kind
// appPool or service, a name of 1 to 128 bytes with no \ / : * ? " < > | and no control character, and
// no duplicate kind+name. Names match without regard to case, as Windows matches them.
const t = (kind, name) => ({ kind, name });
const many = (n, kind = 'service') => Array.from({ length: n }, (_, i) => t(kind, 'svc-' + i));

test('targetsProblem accepts what the server accepts: an empty list, spaces, dots and 128 bytes', () => {
  assert.equal(bb.targetsProblem([]), null);
  // An older server sends no targets at all: that is machine-wide only, not a problem.
  assert.equal(bb.targetsProblem(undefined), null);
  assert.equal(bb.targetsProblem(null), null);
  for (const good of [t('appPool', 'Payroll API'), t('service', 'EmailSvc'), t('appPool', 'My.App-Pool (v2)'), t('appPool', 'x'.repeat(128)), t('service', ' ')]) {
    assert.equal(bb.targetsProblem([good]), null, JSON.stringify(good));
  }
  assert.equal(bb.targetsProblem(many(32)), null, '32 targets are allowed');
  // The same name under the other kind is a different target, not a duplicate.
  assert.equal(bb.targetsProblem([t('appPool', 'EmailSvc'), t('service', 'EmailSvc')]), null);
  // 128 bytes, not 128 characters: the server counts bytes, so 64 two-byte letters are the limit.
  assert.equal(bb.targetsProblem([t('appPool', '\u00e9'.repeat(64))]), null);
  // The server refuses only the characters it lists: a delete character is not one of them.
  assert.equal(bb.targetsProblem([t('service', 'a\u007fb')]), null);
});

test('targetsProblem names the first thing wrong, in the order the server checks it', () => {
  const cases = [
    [[t('appPool', '')], 'empty'],
    [[t('appPool', 'x'.repeat(129))], 'long'],
    [[t('appPool', '\u00e9'.repeat(65))], 'long'],
    [[t('process', 'EmailSvc')], 'kind'],
    [[t('AppPool', 'EmailSvc')], 'kind'],
    [[t('Service', 'EmailSvc')], 'kind'],
    [[t('', 'EmailSvc')], 'kind'],
    [[t('service', 'EmailSvc'), t('service', 'EmailSvc')], 'duplicate'],
    [[t('service', 'EmailSvc'), t('service', 'emailsvc')], 'duplicate'],
    [[t('appPool', 'Payroll API'), t('appPool', 'payroll api')], 'duplicate'],
    [many(33), 'many'],
    [many(32).concat([t('service', 'svc-0')]), 'many']
  ];
  for (const [targets, want] of cases) assert.equal(bb.targetsProblem(targets), want, JSON.stringify(targets));
  // Every character the server refuses, and the control characters it refuses too.
  for (const ch of ['\\', '/', ':', '*', '?', '"', '<', '>', '|', '\t', '\n', '\r', '\u0001', '\u0000', '\u001f']) {
    assert.equal(bb.targetsProblem([t('appPool', 'a' + ch + 'b')]), 'chars', JSON.stringify(ch));
  }
  // The count is checked first, as the server does, so a full list says so even when a name is wrong too.
  assert.equal(bb.targetsProblem(many(33).concat([t('service', 'a/b')])), 'many');
  // A wrong name later in the list is still found.
  assert.equal(bb.targetsProblem([t('service', 'W3SVC'), t('appPool', 'a|b')]), 'chars');
  // The list given is never changed.
  const start = [t('service', 'W3SVC')];
  bb.targetsProblem(start);
  assert.deepEqual(start, [t('service', 'W3SVC')]);
});

test('settingsProblem and fieldProblemNow cover the targets, so the wizard and Apply refuse before the round trip', () => {
  const good = bb.defaultSettings('web-a');
  assert.equal(bb.settingsProblem(good), null);
  assert.equal(bb.settingsProblem({ ...good, targets: [t('appPool', 'a/b')] }), 'targets');
  assert.equal(bb.settingsProblem({ ...good, targets: many(33) }), 'targets');
  assert.equal(bb.settingsProblem({ ...good, targets: [t('service', 'EmailSvc'), t('service', 'emailsvc')] }), 'targets');
  assert.equal(bb.settingsProblem({ ...good, targets: many(32) }), null);
  // The log folders are checked first, as the server checks them first.
  assert.equal(bb.settingsProblem({ ...good, logPaths: ['logs'], targets: [t('appPool', 'a/b')] }), 'logs');
  const bad = { ...good, targets: [t('appPool', 'a/b')] };
  assert.equal(bb.fieldProblemNow('targets', bad, 'C:\\logs'), 'targets');
  assert.equal(bb.fieldProblemNow('targets', { ...good, targets: [] }, 'C:\\logs'), null, 'the message goes once the list is fixed');
  assert.equal(typeof BB_TEXT.settingProblems.targets, 'string');
});

test('the target words and the invalid-config refusal are plain sentences, and the lint reads them', () => {
  const entries = blackboxCopyEntries();
  assert.deepEqual(lintScreenStrings(entries, deck.terms), []);
  for (const key of ['empty', 'long', 'chars', 'kind', 'duplicate', 'many']) {
    assert.equal(typeof BB_TEXT.targetProblems[key], 'string', key + ' has words');
    assert.ok(entries.some((e) => e.id === 'blackbox.targetProblems.' + key), key + ' is linted');
  }
  for (const id of ['blackbox.settingProblems.targets', 'blackbox.refusal.invalid-config']) {
    assert.ok(entries.some((e) => e.id === id), id + ' is linted');
  }
  // No file name, no field name, no reason code: a first-time investigator reads what to do.
  const words = BB_REFUSAL['invalid-config'] + ' ' + BB_TEXT.settingProblems.targets + ' ' + Object.values(BB_TEXT.targetProblems).join(' ');
  assert.doesNotMatch(words, /blackbox\.json|invalid-config|"name"|"folder"|not valid|Validate/i);
  assert.match(BB_REFUSAL['invalid-config'], /nothing was changed/i, 'the refusal says what happened');
});

test('a refused settings call reads the plain sentence, and the server own words still win', () => {
  const raw = new bb.BlackboxError({ status: 422, error: 'blackbox.json is not valid: duplicate target', reason: 'invalid-config' });
  assert.equal(bb.refusalText(raw, BB_REFUSAL), BB_REFUSAL['invalid-config']);
  const explained = new bb.BlackboxError({ status: 422, error: 'raw', reason: 'invalid-config', what: 'Choose a folder of its own.', next: 'Then try again.' });
  assert.equal(bb.refusalText(explained, BB_REFUSAL), 'Choose a folder of its own. Then try again.');
});

/* ------------------------------------------------------------------ UX-4: the Settings step refuses a typed name inline */

// The step's own script, run with plain values for the runes: no DOM, no network, no clock. The alert
// markup below is the template that renders the words this returns. Transpiled once, run per case.
// Each case is a fresh step: with plain values a derived list is read once, when the step is built.
let stepScript;
function freshStep(overrides = {}) {
  const settings = overrides.settings ?? bb.defaultSettings('web-a');
  const props = { settings, logPathsText: settings.logPaths.join('\n'), idPrefix: 'bb', problem: null, targetMode: 'manual', ...overrides };
  if (!stepScript) {
    const src = read('src/components/BlackboxSettings.svelte');
    const script = src.match(/<script lang="ts">([\s\S]*?)<\/script>/)[1];
    const js = ts.transpileModule(script, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022, esModuleInterop: true } }).outputText;
    // UX-7: the announcement, the focus slots and the two element refs, read and written live.
    // Typeof-guarded so this harness also runs against a step script that predates them.
    stepScript = js + '\nmodule.exports.step = { addTarget, pick, removeTarget,'
      + ' focusSlot: typeof focusSlot === \'undefined\' ? null : focusSlot,'
      + ' chipsShown: typeof chipsShown === \'undefined\' ? null : chipsShown,'
      + ' get announcement() { return typeof announcement === \'undefined\' ? null : announcement; },'
      + ' set announcement(v) { if (typeof announcement !== \'undefined\') { announcement = v; } },'
      + ' get chipsBox() { return typeof chipsBox === \'undefined\' ? null : chipsBox; },'
      + ' set chipsBox(v) { try { chipsBox = v; } catch { /* a step script with no such ref: nothing to focus */ } },'
      + ' get nameField() { return typeof nameField === \'undefined\' ? null : nameField; },'
      + ' set nameField(v) { try { nameField = v; } catch { /* a step script with no such ref: nothing to focus */ } },'
      + ' get manualName() { return manualName; }, set manualName(v) { manualName = v; },'
      + ' get manualKind() { return manualKind; }, set manualKind(v) { manualKind = v; },'
      + ' get targetRefusal() { return typeof targetRefusal === \'undefined\' ? null : targetRefusal; },'
      + ' targetError: typeof targetError === \'undefined\' ? null : targetError };';
  }
  const module = { exports: {} };
  const need = (request) => {
    // UX-7: removeTarget waits for the list to render before it moves focus, so the step needs a real tick.
    if (request === 'svelte') return { onMount: () => {}, tick: () => Promise.resolve() };
    if (request === '../lib/blackbox') return bb;
    if (request === '../lib/copy/blackboxCopy') return { BB_TEXT, BB_REFUSAL, blackboxCopyEntries };
    throw new Error('unexpected import: ' + request);
  };
  const identity = (value) => value;
  new Function('require', 'module', 'exports', '$state', '$derived', '$props', '$bindable', stepScript)(
    need, module, module.exports, identity, identity, () => props, () => undefined);
  return { step: module.exports.step, settings };
}

test('addTarget refuses a name the server would refuse, says why, and does not add it', () => {
  const cases = [
    ['a/b', 'chars'], ['back\\slash', 'chars'], ['C:pool', 'chars'], ['a*b', 'chars'], ['a?b', 'chars'],
    ['a"b', 'chars'], ['a<b', 'chars'], ['a>b', 'chars'], ['a|b', 'chars'], ['a\tb', 'chars'],
    ['x'.repeat(129), 'long'], ['x'.repeat(128) + 'y', 'long']
  ];
  for (const [name, why] of cases) {
    const { step, settings } = freshStep();
    step.manualName = name;
    step.addTarget();
    assert.deepEqual(settings.targets, [], JSON.stringify(name) + ' was not added');
    assert.equal(step.targetRefusal, BB_TEXT.targetProblems[why], JSON.stringify(name));
    assert.equal(step.targetError(), BB_TEXT.targetProblems[why], 'the alert renders these words');
    assert.equal(step.manualName, name, 'the typed name stays so it can be fixed');
  }
});

test('addTarget refuses a duplicate without regard to case, and the 33rd target', () => {
  const held = { ...bb.defaultSettings('web-a'), targets: [t('service', 'EmailSvc')] };
  const dup = freshStep({ settings: held });
  dup.step.manualKind = 'service';
  dup.step.manualName = 'emailsvc';
  dup.step.addTarget();
  assert.deepEqual(dup.settings.targets, [t('service', 'EmailSvc')], 'the case-folded duplicate was not added');
  assert.equal(dup.step.targetError(), BB_TEXT.targetProblems.duplicate);
  // The same name under the other kind is a different target: it is added.
  const other = freshStep({ settings: { ...bb.defaultSettings('web-a'), targets: [t('service', 'EmailSvc')] } });
  other.step.manualKind = 'appPool';
  other.step.manualName = 'EmailSvc';
  other.step.addTarget();
  assert.deepEqual(other.settings.targets, [t('service', 'EmailSvc'), t('appPool', 'EmailSvc')]);
  assert.equal(other.step.targetError(), '');
  // A full list refuses the next name instead of sending 33 to the server.
  const full = freshStep({ settings: { ...bb.defaultSettings('web-a'), targets: many(32) } });
  full.step.manualName = 'svc-32';
  full.step.addTarget();
  assert.equal(full.settings.targets.length, 32);
  assert.equal(full.step.targetError(), BB_TEXT.targetProblems.many);
});

test('addTarget adds a good name, clears the words and the box, and an empty name does nothing', () => {
  const { step, settings } = freshStep();
  step.manualName = '  Payroll API  ';
  step.addTarget();
  assert.deepEqual(settings.targets, [t('appPool', 'Payroll API')], 'the name is trimmed, as the server sees it');
  assert.equal(step.targetError(), '');
  assert.equal(step.manualName, '');
  step.manualName = '   ';
  step.addTarget();
  assert.deepEqual(settings.targets, [t('appPool', 'Payroll API')]);
  assert.equal(step.targetError(), '');
  // A refused name, then a good one: the words go away.
  const second = freshStep();
  second.step.manualName = 'a|b';
  second.step.addTarget();
  assert.equal(second.step.targetError(), BB_TEXT.targetProblems.chars);
  second.step.manualName = 'W3SVC';
  second.step.addTarget();
  assert.deepEqual(second.settings.targets, [t('appPool', 'W3SVC')]);
  assert.equal(second.step.targetError(), '');
});

test('the refusal does not outlive what it describes: removing a chip, picking a name or changing the kind clears it', () => {
  const { step, settings } = freshStep({ settings: { ...bb.defaultSettings('web-a'), targets: many(32) } });
  step.manualName = 'svc-32';
  step.addTarget();
  assert.equal(step.targetError(), BB_TEXT.targetProblems.many);
  // Removing one makes room, so the words go and the same name is then accepted.
  step.removeTarget('service', 'svc-31');
  assert.equal(step.targetError(), '');
  assert.equal(settings.targets.length, 31);
  const room = freshStep({ settings });
  room.step.manualName = 'svc-32';
  room.step.addTarget();
  assert.equal(room.settings.targets.length, 32);
  assert.equal(room.step.targetError(), '');
  // A duplicate refusal goes when a chip is removed or a name is picked.
  const dup = freshStep({ settings: { ...bb.defaultSettings('web-a'), targets: [t('service', 'EmailSvc')] } });
  dup.step.manualKind = 'service';
  dup.step.manualName = 'emailsvc';
  dup.step.addTarget();
  assert.equal(dup.step.targetError(), BB_TEXT.targetProblems.duplicate);
  dup.step.pick('service', 'EmailSvc');
  assert.equal(dup.step.targetError(), '');
  // The kind is part of the decision, so changing it clears the words too.
  const comp = read('src/components/BlackboxSettings.svelte');
  assert.match(comp, /<select bind:value=\{manualKind\} onchange=\{\(\) => \{ targetRefusal = ''; \}\}>/);
  assert.match(comp, /oninput=\{\(\) => \{ if \(targetRefusal\) targetRefusal = ''; \}\}/);
});

test('the refusal renders as an alert beside the target row, and the row is the field the wizard focuses', () => {
  const comp = read('src/components/BlackboxSettings.svelte');
  assert.match(comp, /\{#if targetError\(\)\}<p class="inline-error" id="\{idPrefix\}-targets-error" role="alert">\{targetError\(\)\}<\/p>\{\/if\}/);
  // The wizard and Apply focus '#bb-f-' + problem, so the target row carries that id.
  assert.match(comp, /id="\{idPrefix\}-f-targets"/);
  assert.match(comp, /aria-invalid=\{targetError\(\) \? 'true' : undefined\}/);
  assert.match(comp, /aria-describedby=\{targetError\(\) \? idPrefix \+ '-targets-error' : undefined\}/);
  // The words go once the person types again, and the targets problem found at the last attempt shows here too.
  assert.match(comp, /oninput=\{\(\) => \{ if \(targetRefusal\) targetRefusal = ''; \}\}/);
  assert.match(comp, /function targetError\(\): string \{ return targetRefusal \|\| \(problem === 'targets' \? T\.settingProblems\.targets : ''\); \}/);
  // A settings problem found at the last attempt reads the same alert.
  const { step } = freshStep({ problem: 'targets' });
  assert.equal(step.targetError(), BB_TEXT.settingProblems.targets);
});

/* ------------------------------------------------------------------ UX-4: the alert, rendered for real */

// The step rendered by Svelte itself (server render, no DOM, no network): the words above are the words
// the page shows. The inline Add refusal uses this same element, with targetError() proven above.
// Compiled once: the render is a pure function of the props.
let stepComponent;
function renderStep(overrides = {}) {
  const settings = overrides.settings ?? bb.defaultSettings('web-a');
  const props = { settings, logPathsText: settings.logPaths.join('\n'), idPrefix: 'bb', problem: null, targetMode: 'manual', ...overrides };
  if (!stepComponent) {
    const { compile } = require('svelte/compiler');
    const compiled = compile(read('src/components/BlackboxSettings.svelte'), { filename: 'BlackboxSettings.svelte', generate: 'server' });
    const js = ts.transpileModule(compiled.js.code, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022, esModuleInterop: true } }).outputText;
    const held = { exports: {} };
    const load = (request) => {
      if (request === 'svelte') return { onMount: () => {} };
      if (request === '../lib/blackbox') return bb;
      if (request === '../lib/copy/blackboxCopy') return { BB_TEXT, BB_REFUSAL, blackboxCopyEntries };
      return require(request);
    };
    new Function('require', 'module', 'exports', js)(load, held, held.exports);
    stepComponent = held.exports.default;
  }
  const html = require('svelte/server').render(stepComponent, { props }).html;
  // Svelte escapes the symbols in the copy; read them back as the person sees them.
  const plain = html.replace(/&quot;/g, '"').replace(/&#39;/g, "'").replace(/&lt;/g, '<').replace(/&gt;/g, '>').replace(/&amp;/g, '&');
  return { html, plain };
}

test('the rendered step shows the targets alert, marks the field and points at the message', () => {
  const { html, plain } = renderStep({ problem: 'targets' });
  assert.match(html, /<p class="inline-error[^"]*" id="bb-targets-error" role="alert">/);
  assert.ok(plain.includes(BB_TEXT.settingProblems.targets), 'the alert says the targets words');
  assert.match(html, /id="bb-f-targets"[^>]*aria-invalid="true"/);
  assert.match(html, /id="bb-f-targets"[^>]*aria-describedby="bb-targets-error"/);
  // Apply settings uses its own prefix, so its alert and field ids do not clash with the wizard's.
  const apply = renderStep({ problem: 'targets', idPrefix: 'bb-apply' });
  assert.match(apply.html, /id="bb-apply-targets-error" role="alert"/);
  assert.match(apply.html, /id="bb-apply-f-targets"/);
});

test('the rendered step shows no target alert when nothing is wrong', () => {
  const { html } = renderStep();
  assert.doesNotMatch(html, /bb-targets-error/, 'no alert and no dangling aria-describedby');
  assert.match(html, /id="bb-f-targets"/);
  assert.doesNotMatch(html, /id="bb-f-targets"[^>]*aria-invalid="true"/);
  // A good list of 32 targets renders as chips, with no alert.
  const full = renderStep({ settings: { ...bb.defaultSettings('web-a'), targets: many(32) } });
  assert.doesNotMatch(full.html, /bb-targets-error/);
  assert.equal((full.html.match(/class="bb-chip[ "]/g) ?? []).length, 32);
});

/* ------------------------------------------------------------------ UX-5: honest loading / failed / partial states */

// The one mount-time read can be in flight, refused, or settled with only part of the answer.
// Each detected list must say which it is; a failed or unreadable list is never shown as an
// empty healthy list, and an empty detected list is skipped entirely (no fieldset).

const poolsOk = [{ name: 'DefaultAppPool', state: 'unknown' }];
const servicesOk = [{ name: 'W3SVC', running: true, state: 'running' }];
const flush = () => new Promise((resolve) => setImmediate(resolve));
// Svelte scopes its styles with a class on the element, so a legend reads <legend class="svelte-...">.
const legendOf = (label) => new RegExp('<legend[^>]*>' + label.replace(/[.*+?^${}()|[\]\\]/g, '\\$&') + '</legend>');

test('targetListState says loading, failed, not-found, unavailable, empty or list, and is pure', () => {
  const f = bb.targetListState;
  assert.equal(typeof f, 'function', 'the pure helper exists');
  // In flight: nothing read yet, nothing failed yet.
  assert.equal(f([], undefined, false, false, false), 'loading');
  // The read was refused or never reached the server: failed, whatever the lists hold.
  assert.equal(f([], undefined, false, true, true), 'failed');
  assert.equal(f([], 'detected', true, true, true), 'failed');
  // The source is not on this PC (no IIS): not-found, even when the other source was read.
  assert.equal(f([], 'not-found', false, true, false), 'not-found');
  assert.equal(f([], 'not-found', true, true, false), 'not-found');
  // The source may be here but could not be read: unavailable, never an empty healthy list.
  assert.equal(f([], 'unavailable', true, true, false), 'unavailable');
  assert.equal(f([], 'unavailable', false, true, false), 'unavailable');
  // Read successfully and holds no names: empty (the fieldset is skipped).
  assert.equal(f([], 'detected', true, true, false), 'empty');
  // Names were read: list.
  assert.equal(f(poolsOk, 'detected', true, true, false), 'list');
  assert.equal(f(servicesOk, 'detected', true, true, false), 'list');
  // An older server sends no per-source state: detected means the lists were read.
  assert.equal(f([], undefined, true, true, false), 'empty');
  assert.equal(f(servicesOk, undefined, true, true, false), 'list');
  // Nothing detected and no per-source state: nothing could be read, never an empty healthy list.
  assert.equal(f([], undefined, false, true, false), 'unavailable');
  // A missing list is tolerated like an empty one; the list given is never changed.
  const frozen = Object.freeze([...poolsOk]);
  assert.equal(f(frozen, 'detected', true, true, false), 'list');
  assert.equal(f(null, 'detected', true, true, false), 'empty');
  assert.equal(f(undefined, undefined, false, false, false), 'loading');
  assert.deepEqual(frozen, poolsOk);
  // Pure: the same inputs give the same answer every time.
  assert.equal(f([], 'unavailable', true, true, false), f([], 'unavailable', true, true, false));
});

test('targetListState tells a partial answer apart: what was read is a list, what failed is not', () => {
  const f = bb.targetListState;
  // Pools read, services refused: the pools are offered, the services say they could not be read.
  assert.equal(f(poolsOk, 'detected', true, true, false), 'list');
  assert.equal(f([], 'unavailable', true, true, false), 'unavailable');
  // No IIS but services read: the pools say not-found, the services are offered.
  assert.equal(f([], 'not-found', true, true, false), 'not-found');
  assert.equal(f(servicesOk, 'detected', true, true, false), 'list');
});

// The step's own script with the mount callback captured, run against the REAL client: the only
// stub is fetch itself. Proves the failure path is genuinely reachable, not a state set by hand.
function mountStep(overrides = {}) {
  const settings = overrides.settings ?? bb.defaultSettings('web-a');
  const props = { settings, logPathsText: settings.logPaths.join('\n'), idPrefix: 'bb', problem: null, targetMode: 'pc', ...overrides };
  const src = read('src/components/BlackboxSettings.svelte');
  const script = src.match(/<script lang="ts">([\s\S]*?)<\/script>/)[1];
  const js = ts.transpileModule(script, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022, esModuleInterop: true } }).outputText;
  const exposed = js + '\nmodule.exports.step = { groupState,'
    + ' get targetsRead() { return targetsRead; }, get targetsFailed() { return targetsFailed; },'
    + ' get detected() { return detected; }, get appPools() { return appPools; }, get services() { return services; },'
    // UX-6: the filter and the group note, read live. Typeof-guarded so this harness also runs
    // against a step script that predates them.
    + ' shownTargets: typeof shownTargets === \'undefined\' ? null : shownTargets,'
    + ' allUnknown: typeof allUnknown === \'undefined\' ? null : allUnknown,'
    + ' filterNote: typeof filterNote === \'undefined\' ? null : filterNote,'
    + ' get poolFilter() { return typeof poolFilter === \'undefined\' ? \'\' : poolFilter; }, set poolFilter(v) { poolFilter = v; },'
    + ' get serviceFilter() { return typeof serviceFilter === \'undefined\' ? \'\' : serviceFilter; }, set serviceFilter(v) { serviceFilter = v; } };';
  let mounted = null;
  const need = (request) => {
    if (request === 'svelte') return { onMount: (fn) => { mounted = fn; } };
    if (request === '../lib/blackbox') return bb;
    if (request === '../lib/copy/blackboxCopy') return { BB_TEXT, BB_REFUSAL, blackboxCopyEntries };
    throw new Error('unexpected import: ' + request);
  };
  const module = { exports: {} };
  const identity = (value) => value;
  new Function('require', 'module', 'exports', '$state', '$derived', '$props', '$bindable', exposed)(
    need, module, module.exports, identity, identity, () => props, () => undefined);
  return { step: module.exports.step, mount: () => mounted(), settings };
}
const tokenSession = () => { globalThis.sessionStorage = { getItem: () => TOKEN, setItem() {}, removeItem() {} }; };

test('the mount read drives the states: loading in flight, failed after a rejected fetch', async () => {
  tokenSession();
  const calls = [];
  let rejectFetch;
  globalThis.fetch = (url) => { calls.push(String(url)); return new Promise((_, reject) => { rejectFetch = reject; }); };
  const { step, mount } = mountStep();
  assert.equal(step.groupState('appPool'), 'loading', 'before the read settles both lists are loading');
  assert.equal(step.groupState('service'), 'loading');
  mount();
  assert.deepEqual(calls, ['/api/blackbox/this-pc/targets'], 'the real client made the one read');
  assert.equal(step.targetsRead, false);
  assert.equal(step.groupState('appPool'), 'loading', 'still loading while the fetch is in flight');
  rejectFetch(new TypeError('network down'));
  await flush();
  assert.equal(step.targetsRead, true, 'the failed read still settles');
  assert.equal(step.targetsFailed, true, 'the catch marks the read failed');
  assert.equal(step.groupState('appPool'), 'failed');
  assert.equal(step.groupState('service'), 'failed');
  assert.deepEqual(step.appPools, [], 'nothing was read');
});

test('a refused read (503) also lands as failed, never an empty healthy list', async () => {
  tokenSession();
  globalThis.fetch = async () => new Response(JSON.stringify({ error: 'Blackbox is not available in this build.', reason: 'unavailable' }), { status: 503, headers: { 'Content-Type': 'application/json' } });
  const { step, mount } = mountStep();
  mount();
  await flush();
  assert.equal(step.targetsFailed, true);
  assert.equal(step.groupState('appPool'), 'failed');
  assert.equal(step.groupState('service'), 'failed');
  assert.notEqual(step.groupState('service'), 'empty', 'a refusal is never an empty healthy list');
});

test('a partial answer keeps what was read and marks the other list unavailable', async () => {
  tokenSession();
  const body = { appPools: poolsOk, services: [], detected: true, appPoolsState: 'detected', servicesState: 'unavailable' };
  globalThis.fetch = async () => new Response(JSON.stringify(body), { status: 200, headers: { 'Content-Type': 'application/json' } });
  const { step, mount } = mountStep();
  mount();
  await flush();
  assert.equal(step.targetsFailed, false);
  assert.equal(step.detected, true);
  assert.equal(step.groupState('appPool'), 'list');
  assert.equal(step.groupState('service'), 'unavailable');
});

test('empty detected lists, a missing source and nothing detected each read honestly after the mount read', async () => {
  tokenSession();
  const cases = [
    [{ appPools: [], services: [], detected: true, appPoolsState: 'detected', servicesState: 'detected' }, 'empty', 'empty'],
    [{ appPools: [], services: servicesOk, detected: true, appPoolsState: 'not-found', servicesState: 'detected' }, 'not-found', 'list'],
    [{ appPools: [], services: [], detected: false, appPoolsState: 'unavailable', servicesState: 'unavailable' }, 'unavailable', 'unavailable'],
    // An older server: no per-source state, nothing detected. Nothing could be read.
    [{ appPools: [], services: [], detected: false }, 'unavailable', 'unavailable']
  ];
  for (const [body, pools, services] of cases) {
    globalThis.fetch = async () => new Response(JSON.stringify(body), { status: 200, headers: { 'Content-Type': 'application/json' } });
    const { step, mount } = mountStep();
    mount();
    await flush();
    assert.equal(step.targetsFailed, false, JSON.stringify(body));
    assert.equal(step.groupState('appPool'), pools, JSON.stringify(body));
    assert.equal(step.groupState('service'), services, JSON.stringify(body));
  }
});

test('manual mode never fetches, and a read that settles after unmount changes nothing', async () => {
  tokenSession();
  const calls = [];
  globalThis.fetch = async (url) => { calls.push(String(url)); return new Response('{}', { status: 200 }); };
  const manual = mountStep({ targetMode: 'manual' });
  manual.mount();
  await flush();
  assert.deepEqual(calls, [], 'pack/remote mode never asks the endpoint');
  assert.equal(manual.step.targetsRead, false);
  // Unmounted before the answer arrives: the late answer is dropped.
  let resolveFetch;
  globalThis.fetch = () => new Promise((resolve) => { resolveFetch = resolve; });
  const { step, mount } = mountStep();
  const cleanup = mount();
  cleanup();
  resolveFetch(new Response(JSON.stringify({ appPools: poolsOk, services: [], detected: true }), { status: 200, headers: { 'Content-Type': 'application/json' } }));
  await flush();
  assert.equal(step.targetsRead, false, 'the late answer never lands');
});

/* ------------------------------------------------------------------ UX-5: the states, rendered for real */

// Svelte's server render is synchronous, so the mount read is bridged to it: the answer (or the
// refusal) is first produced by the REAL client with a stubbed fetch, then handed to the step's
// own then/catch synchronously. The async tests above prove the unbridged path end to end.
const pcBridge = { result: null, failed: false };
const pcThenable = {
  then(cb) { if (!pcBridge.failed && pcBridge.result) cb(pcBridge.result); return this; },
  catch(cb) { if (pcBridge.failed) cb(pcBridge.result); return this; }
};
let pcComponent;
function renderPcStep(overrides = {}) {
  const settings = overrides.settings ?? bb.defaultSettings('web-a');
  const props = { settings, logPathsText: settings.logPaths.join('\n'), idPrefix: 'bb', problem: null, targetMode: 'pc', ...overrides };
  if (!pcComponent) {
    const { compile } = require('svelte/compiler');
    const compiled = compile(read('src/components/BlackboxSettings.svelte'), { filename: 'BlackboxSettings.svelte', generate: 'server' });
    const js = ts.transpileModule(compiled.js.code, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022, esModuleInterop: true } }).outputText;
    const held = { exports: {} };
    const load = (request) => {
      if (request === 'svelte') return { onMount: (fn) => { fn(); } };
      if (request === '../lib/blackbox') return { ...bb, blackboxApi: { ...bb.blackboxApi, targets: () => pcThenable } };
      if (request === '../lib/copy/blackboxCopy') return { BB_TEXT, BB_REFUSAL, blackboxCopyEntries };
      return require(request);
    };
    new Function('require', 'module', 'exports', js)(load, held, held.exports);
    pcComponent = held.exports.default;
  }
  const html = require('svelte/server').render(pcComponent, { props }).html;
  const plain = html.replace(/&quot;/g, '"').replace(/&#39;/g, "'").replace(/&lt;/g, '<').replace(/&gt;/g, '>').replace(/&amp;/g, '&');
  return { html, plain, fieldsets: (html.match(/<fieldset/g) ?? []).length };
}
async function realAnswer(body) {
  tokenSession();
  globalThis.fetch = async () => new Response(JSON.stringify(body), { status: 200, headers: { 'Content-Type': 'application/json' } });
  return bb.blackboxApi.targets();
}
async function realRefusal() {
  tokenSession();
  globalThis.fetch = async () => { throw new TypeError('network down'); };
  try { await bb.blackboxApi.targets(); } catch (error) { return error; }
  throw new Error('the read should have failed');
}

test('the rendered step says it is looking while the read is in flight, politely (role=status)', () => {
  pcBridge.result = null; pcBridge.failed = false;
  const { html, plain } = renderPcStep();
  assert.match(html, /<p class="bb-note[^"]*" role="status">/);
  assert.ok(plain.includes(BB_TEXT.targetsLoading), 'the loading words are shown');
  assert.ok(!legendOf(BB_TEXT.targetsAppPools).test(html), 'no list is claimed yet');
  assert.ok(!plain.includes(BB_TEXT.targetsLoadFailed), 'nothing has failed');
});

test('the rendered step says the read failed, and never shows it as an empty healthy list', async () => {
  pcBridge.result = await realRefusal(); pcBridge.failed = true;
  const { html, plain, fieldsets } = renderPcStep();
  assert.ok(plain.includes(BB_TEXT.targetsLoadFailed), 'the failure words are shown');
  assert.match(html, /role="status"/);
  assert.equal(fieldsets, 1, 'only the What to record fieldset; no detected-list fieldsets');
  assert.ok(!plain.includes(BB_TEXT.targetsLoading), 'it is no longer loading');
  assert.match(html, /id="bb-f-targets"/, 'names can still be typed');
  assert.match(BB_TEXT.targetsLoadFailed, /could not be read/, 'the copy says what happened');
});

test('the rendered step offers what was read and says which list could not be read (partial)', async () => {
  pcBridge.result = await realAnswer({ appPools: poolsOk, services: [], detected: true, appPoolsState: 'detected', servicesState: 'unavailable' }); pcBridge.failed = false;
  const { html, plain, fieldsets } = renderPcStep();
  assert.ok(legendOf(BB_TEXT.targetsAppPools).test(html), 'the pools that were read are offered');
  assert.ok(!legendOf(BB_TEXT.targetsServices).test(html), 'the unreadable services list is not an empty fieldset');
  assert.ok(plain.includes(BB_TEXT.targetsServicesUnreadable), 'the services say they could not be read');
  assert.ok(!plain.includes(BB_TEXT.targetsPoolsUnreadable), 'the pools were read');
  assert.equal(fieldsets, 2);
  assert.ok(plain.includes(BB_TEXT.targetsListNote), 'the list note stays');
});

test('the rendered step says when a source is not on this PC, apart from unreadable', async () => {
  pcBridge.result = await realAnswer({ appPools: [], services: servicesOk, detected: true, appPoolsState: 'not-found', servicesState: 'detected' }); pcBridge.failed = false;
  const { html, plain, fieldsets } = renderPcStep();
  assert.ok(plain.includes(BB_TEXT.targetsPoolsNotFound), 'no IIS here is said plainly');
  assert.ok(!plain.includes(BB_TEXT.targetsPoolsUnreadable), 'not-found is not unreadable');
  assert.ok(legendOf(BB_TEXT.targetsServices).test(html));
  assert.ok(!legendOf(BB_TEXT.targetsAppPools).test(html));
  assert.equal(fieldsets, 2);
});

test('the rendered step skips an empty detected fieldset entirely, and says when both lists could not be read', async () => {
  // Both sources read, both hold no names: no fieldsets, no failure claimed.
  pcBridge.result = await realAnswer({ appPools: [], services: [], detected: true, appPoolsState: 'detected', servicesState: 'detected' }); pcBridge.failed = false;
  const empty = renderPcStep();
  assert.equal(empty.fieldsets, 1, 'an empty list is skipped, not shown as an empty fieldset');
  assert.ok(!empty.plain.includes(BB_TEXT.targetsLoadFailed));
  assert.ok(!empty.plain.includes(BB_TEXT.targetsPoolsUnreadable));
  assert.ok(!empty.plain.includes(BB_TEXT.targetsServicesUnreadable));
  // Neither source could be read: both say so. This was silent before UX-5.
  pcBridge.result = await realAnswer({ appPools: [], services: [], detected: false, appPoolsState: 'unavailable', servicesState: 'unavailable' });
  const unreadable = renderPcStep();
  assert.ok(unreadable.plain.includes(BB_TEXT.targetsPoolsUnreadable), 'the pools say they could not be read');
  assert.ok(unreadable.plain.includes(BB_TEXT.targetsServicesUnreadable), 'the services say they could not be read');
  assert.equal(unreadable.fieldsets, 1);
  // An older server that detected nothing: still never a silent empty healthy list.
  pcBridge.result = await realAnswer({ appPools: [], services: [], detected: false });
  const legacy = renderPcStep();
  assert.ok(legacy.plain.includes(BB_TEXT.targetsPoolsUnreadable));
  assert.ok(legacy.plain.includes(BB_TEXT.targetsServicesUnreadable));
});

test('manual mode renders none of the read states, and the new copy is linted and plain', () => {
  const { html, plain } = renderStep();
  assert.ok(!plain.includes(BB_TEXT.targetsLoading), 'pack/remote never loads');
  assert.ok(!plain.includes(BB_TEXT.targetsLoadFailed));
  // UX-7: the panel now always carries one status region (a live region has to be in the page before
  // the words change). In manual mode it is the only one, and it says nothing yet: the read-state
  // status lines are the ones that must not render here, proven by the copy assertions around this.
  assert.equal(countOf(html, 'role="status"'), 1, 'only the empty announcement region');
  assert.match(html, /<p class="bb-sr[^"]*" role="status"[^>]*><\/p>/, 'it is empty until a target is added or removed');
  assert.ok(!plain.includes(BB_TEXT.targetsPoolsUnreadable));
  const entries = blackboxCopyEntries();
  assert.deepEqual(lintScreenStrings(entries, deck.terms), []);
  for (const key of ['targetsLoading', 'targetsLoadFailed', 'targetsPoolsNotFound', 'targetsPoolsUnreadable', 'targetsServicesNotFound', 'targetsServicesUnreadable']) {
    assert.equal(typeof BB_TEXT[key], 'string', key + ' is a plain string');
    assert.ok(entries.some((e) => e.id === 'blackbox.' + key), key + ' is linted');
  }
  // A first-time investigator reads what happened and what to do; no jargon, no reason codes.
  const words = [BB_TEXT.targetsLoadFailed, BB_TEXT.targetsPoolsUnreadable, BB_TEXT.targetsServicesUnreadable].join(' ');
  assert.doesNotMatch(words, /unavailable|not-found|503|endpoint|fetch|detect/i, 'no operational codes leak into the words');
  assert.match(BB_TEXT.targetsLoadFailed, /Type the names below/, 'the consequence and the way forward are said');
});

/* ------------------------------------------------------------------ UX-6: bounded lists, one group note, a filter */

// A long detected list must stay usable: a bounded scroll region a keyboard can reach, a filter
// that narrows it without hiding that names exist, and ONE note under the legend of a group that
// holds an unknown running state (UX-8b: any row, not only a whole list of them) — never the same
// note on every row. No per-row targetsStateUnknown assertion lives in this file (UX-8 deletes that key).
const svc200 = Array.from({ length: 200 }, (_, i) => ({ name: 'Svc' + String(i).padStart(3, '0'), state: 'unknown' }));
const countOf = (text, needle) => text.split(needle).length - 1;

test('a 200-service list is bounded and scrollable, and says unknown once under the legend, not 200 times', async () => {
  pcBridge.result = await realAnswer({ appPools: [], services: svc200, detected: true, appPoolsState: 'not-found', servicesState: 'detected' }); pcBridge.failed = false;
  const { html, plain } = renderPcStep();
  assert.equal(countOf(plain, BB_TEXT.targetsAllUnknown), 1, 'exactly one group note');
  // The note sits under the Services legend (Svelte adds a scoping class to it), before the list.
  const legendAt = html.search(legendOf(BB_TEXT.targetsServices));
  assert.ok(legendAt >= 0, 'the services legend renders');
  const noteAt = plain.indexOf(BB_TEXT.targetsAllUnknown);
  assert.ok(html.indexOf(BB_TEXT.targetsAllUnknown) > legendAt, 'the note is under the legend');
  assert.ok(noteAt < plain.indexOf('Svc000'), 'the note comes before the first row');
  // Every name sits in one scroll region a keyboard can reach, with a meaningful label.
  const scroll = html.match(/<div class="bb-scroll[^"]*" tabindex="0" role="group" aria-label="Services">([\s\S]*?)<\/div>/);
  assert.ok(scroll, 'the list is wrapped in a labelled, focusable scroll region');
  assert.equal((scroll[1].match(/type="checkbox"/g) ?? []).length, 200, 'all 200 names are inside it');
  assert.ok(!scroll[1].includes('bb-note'), 'no per-row note on any of the 200 rows');
  // Bounded height: the style block caps the region and lets it scroll.
  const comp = read('src/components/BlackboxSettings.svelte');
  assert.match(comp, /\.bb-scroll \{[^}]*max-height: 240px;[^}]*overflow: auto;/);
  // A filter box is offered, with a visible label, and the list iterates the filtered names.
  assert.match(html, /id="bb-services-filter"/);
  assert.ok(plain.includes(BB_TEXT.targetsFilterLabel), 'the filter box is labelled');
  assert.match(comp, /\{#each shownTargets\('service'\) as t \(t\.name\)\}/);
  assert.match(comp, /\{#each shownTargets\('appPool'\) as t \(t\.name\)\}/);
});

test('the filter narrows a 200-name list, case-insensitively, and says how many are hidden', async () => {
  tokenSession();
  const body = { appPools: [], services: svc200, detected: true, appPoolsState: 'not-found', servicesState: 'detected' };
  globalThis.fetch = async () => new Response(JSON.stringify(body), { status: 200, headers: { 'Content-Type': 'application/json' } });
  const { step, mount } = mountStep();
  mount();
  await flush();
  assert.equal(step.shownTargets('service').length, 200, 'an empty filter shows everything');
  assert.equal(step.allUnknown('service'), true, 'every row is unknown');
  step.serviceFilter = 'svc19';
  const narrowed = step.shownTargets('service');
  assert.equal(narrowed.length, 10, 'Svc190 to Svc199 match');
  assert.ok(narrowed.every((t) => t.name.toLowerCase().includes('svc19')));
  assert.equal(step.filterNote('service'), BB_TEXT.targetsFilterCount(10, 200), 'the count says what is hidden');
  step.serviceFilter = 'SVC007';
  assert.deepEqual(step.shownTargets('service').map((t) => t.name), ['Svc007'], 'case does not matter');
  step.serviceFilter = 'zzz';
  assert.deepEqual(step.shownTargets('service'), []);
  assert.equal(step.filterNote('service'), BB_TEXT.targetsFilterNone(200), 'no match says how to see the names again');
  step.serviceFilter = '   ';
  assert.equal(step.shownTargets('service').length, 200, 'a blank filter shows everything');
  // Filtering never changes the read list itself.
  assert.equal(step.services.length, 200);
});

test('a mixed list keeps its honest per-row labels and says the unread states once, as the overall note', async () => {
  pcBridge.result = await realAnswer({
    appPools: [{ name: 'Payroll', state: 'running' }, { name: 'DefaultAppPool', state: 'unknown' }],
    services: [{ name: 'W3SVC', state: 'running' }, { name: 'Spooler', state: 'stopped' }],
    detected: true, appPoolsState: 'detected', servicesState: 'detected'
  }); pcBridge.failed = false;
  const { html, plain } = renderPcStep();
  // UX-8b (decision A): the old rule here was "no group note when some states are known", which left
  // an unknown row with no words at all once UX-8 took its label away. The note now fires on ANY
  // unknown row, so the pools group says it once. The services group holds no unknown row, so it
  // stays quiet: one note per group that needs one, never a note per group that does not.
  assert.equal(countOf(plain, BB_TEXT.targetsAllUnknown), 1, 'the group with an unknown row gets the overall note');
  const poolsLegend = html.search(legendOf(BB_TEXT.targetsAppPools));
  const servicesLegend = html.search(legendOf(BB_TEXT.targetsServices));
  assert.ok(poolsLegend >= 0 && servicesLegend > poolsLegend, 'both legends render, pools first');
  assert.ok(html.indexOf(BB_TEXT.targetsAllUnknown) > poolsLegend && html.indexOf(BB_TEXT.targetsAllUnknown) < servicesLegend, 'the note belongs to the pools group only');
  // SSR anchor comments may sit between the name and its note; the person reads "Spooler not running".
  assert.match(html, /Spooler(?:<!--[^>]*-->)* <span class="bb-note[^"]*">not running<\/span>/);
  // W2-07: the unknown row is never labelled stopped, and UX-8 keeps it free of any label of its own.
  assert.doesNotMatch(html, /DefaultAppPool(?:<!--[^>]*-->)* <span class="bb-note[^"]*">not running<\/span>/);
  assert.doesNotMatch(html, /DefaultAppPool(?:<!--[^>]*-->)* <span class="bb-note/, 'no per-row label on the unknown row');
});

test('UX-8: a mixed list renders one honest overall note and no per-row unknown label', async () => {
  // The pools group is mixed: one name known running, one whose state could not be read, one known
  // stopped. The services group holds nothing but unknown names. Since UX-8b each of these groups
  // owns one honest overall note (UX-6), because each holds at least one unread state — the count is
  // per group, never per row. Everything below is read off the rendered text, never off the source.
  pcBridge.result = await realAnswer({
    appPools: [{ name: 'Payroll', state: 'running' }, { name: 'Nightly', state: 'unknown' }, { name: 'Legacy', state: 'stopped' }],
    services: [{ name: 'BatchSvc', state: 'unknown' }, { name: 'QueueSvc', state: 'unknown' }],
    detected: true, appPoolsState: 'detected', servicesState: 'detected'
  }); pcBridge.failed = false;
  const { html, plain } = renderPcStep();
  // One note per group that holds an unknown row (UX-8b), and never one per row: three unknown names
  // across the two groups here, so the count is 2 - one per group - not 3.
  assert.equal(countOf(plain, BB_TEXT.targetsAllUnknown), 2, 'one honest overall note per group');
  // UX-8: no per-row unknown label anywhere. The words are pinned here as the person reads them,
  // so the assertion still says something once the per-row copy key is gone.
  assert.equal(countOf(plain, 'running state unknown'), 0, 'no per-row unknown label renders');
  // What is known is still said per row: a stopped row keeps its label, a running row claims nothing.
  assert.match(html, /Legacy(?:<!--[^>]*-->)* <span class="bb-note[^"]*">not running<\/span>/, 'the stopped row keeps its honest label');
  assert.doesNotMatch(html, /Payroll(?:<!--[^>]*-->)* <span class="bb-note/, 'a known running row says nothing');
  // The unknown row says nothing of its own either: silence is not a claim that it is running.
  assert.doesNotMatch(html, /Nightly(?:<!--[^>]*-->)* <span class="bb-note/, 'the unknown row carries no label of its own');
  // No name was dropped on the way: every one is still offered, with the same states as before.
  for (const name of ['Payroll', 'Nightly', 'Legacy', 'BatchSvc', 'QueueSvc']) {
    assert.ok(plain.includes(name), name + ' is still listed');
  }
});

test('UX-8b: a group with one unknown row gets the one overall note, and still no per-row label', async () => {
  // The pools group is mixed on purpose: one name known running, one whose state could not be read.
  // The services group holds only known states, so any overall note on the page must come from the
  // pools group. Read off the rendered text, never off the component source.
  pcBridge.result = await realAnswer({
    appPools: [{ name: 'Payroll', state: 'running' }, { name: 'Nightly', state: 'unknown' }],
    services: [{ name: 'W3SVC', state: 'running' }, { name: 'Spooler', state: 'stopped' }],
    detected: true, appPoolsState: 'detected', servicesState: 'detected'
  }); pcBridge.failed = false;
  const { html, plain } = renderPcStep();
  assert.equal(countOf(plain, BB_TEXT.targetsAllUnknown), 1, 'exactly one overall note for a group with one unknown row');
  assert.equal(countOf(plain, 'running state unknown'), 0, 'and still no per-row unknown label');
  // The note stands where the all-unknown note stands: under the App pools legend, before the rows.
  const legendAt = html.search(legendOf(BB_TEXT.targetsAppPools));
  assert.ok(legendAt >= 0, 'the pools legend renders');
  assert.ok(html.indexOf(BB_TEXT.targetsAllUnknown) > legendAt, 'the note is under the pools legend');
  assert.ok(plain.indexOf(BB_TEXT.targetsAllUnknown) < plain.indexOf('Payroll'), 'the note comes before the first row');
  // Known states are still said per row; the unknown row itself still says nothing of its own.
  assert.match(html, /Spooler(?:<!--[^>]*-->)* <span class="bb-note[^"]*">not running<\/span>/, 'a known stopped row keeps its label');
  assert.doesNotMatch(html, /Nightly(?:<!--[^>]*-->)* <span class="bb-note/, 'the unknown row carries no label of its own');
});

test('an all-unknown pools list gets its own single note, and the new copy is linted and plain', async () => {
  // 'B' carries no state at all (an older server): that is unknown too, never stopped.
  pcBridge.result = await realAnswer({ appPools: [{ name: 'A', state: 'unknown' }, { name: 'B' }], services: [], detected: true, appPoolsState: 'detected', servicesState: 'detected' }); pcBridge.failed = false;
  const { html, plain } = renderPcStep();
  assert.equal(countOf(plain, BB_TEXT.targetsAllUnknown), 1, 'one note for the pools group');
  const scroll = html.match(/<div class="bb-scroll[^"]*" tabindex="0" role="group" aria-label="App pools">([\s\S]*?)<\/div>/);
  assert.ok(scroll, 'the pools scroll region is labelled too');
  assert.ok(!scroll[1].includes('bb-note'), 'no per-row note on either pool row');
  assert.match(html, /id="bb-pools-filter"/);
  // The new copy is plain, linted, and states the consequence and the way back.
  const entries = blackboxCopyEntries();
  assert.deepEqual(lintScreenStrings(entries, deck.terms), []);
  for (const key of ['targetsAllUnknown', 'targetsFilterLabel']) {
    assert.equal(typeof BB_TEXT[key], 'string', key + ' is a plain string');
    assert.ok(entries.some((e) => e.id === 'blackbox.' + key), key + ' is linted');
  }
  for (const id of ['blackbox.targetsFilterCount.1', 'blackbox.targetsFilterCount.2', 'blackbox.targetsFilterNone']) {
    assert.ok(entries.some((e) => e.id === id), id + ' is linted');
  }
  assert.equal(BB_TEXT.targetsFilterCount(10, 200), 'Showing 10 of 200 names.');
  assert.match(BB_TEXT.targetsFilterNone(200), /all 200 names/, 'the no-match note says how many exist');
  assert.match(BB_TEXT.targetsAllUnknown, /records nothing/, 'the group note states the consequence');
});

/* ------------------------------------------------------------------ UX-7: the chip remove button: size, focus, an announcement */

// The × on a chip is the only way to take a name off the list once the detected lists are shown.
// Before UX-7 it was a 14px glyph, so a tap missed and the focus ring vanished with the node it was
// on, and the name left the page without a word. This suite has no DOM (no jsdom is installed), so
// it follows the style of the file: the region, its emptiness and the 24px box are read off the real
// rendered output and the shipped style block, and the focus move is proven by running the
// component's own removeTarget against stub elements that record which one was focused.

test('a removed or an added target is announced in the panel own plain words, and the copy is linted', () => {
  assert.equal(typeof BB_TEXT.targetsRemoved, 'function', 'the removed words exist');
  assert.equal(typeof BB_TEXT.targetsAdded, 'function', 'the added words exist');
  // The name first, then what happened to the list - and nothing the code does not do.
  assert.equal(BB_TEXT.targetsRemoved('W3SVC'), 'W3SVC removed from the targets.');
  assert.equal(BB_TEXT.targetsAdded('W3SVC'), 'W3SVC added to the targets.');
  const entries = blackboxCopyEntries();
  for (const key of ['targetsRemoved', 'targetsAdded']) {
    assert.ok(entries.some((e) => e.id === 'blackbox.' + key), key + ' is linted');
  }
  for (const text of [BB_TEXT.targetsRemoved('W3SVC'), BB_TEXT.targetsAdded('W3SVC')]) {
    assert.ok(text.startsWith('W3SVC'), 'the name that changed is said');
    assert.doesNotMatch(text, /stopped|running|recording|saved|written|applied|installed/i, 'it claims only that the list changed, never that anything was saved or is being recorded');
  }
  assert.deepEqual(lintScreenStrings(entries, deck.terms), [], 'the whole copy still lints clean');
});

test('the panel carries one visually-hidden status region that renders the announcement, and it is there before anything changes', () => {
  const comp = read('src/components/BlackboxSettings.svelte');
  // A live region inserted together with its text is missed by most screen readers, so the element
  // stands on its own: no {#if} around it, and the words are its only content.
  assert.match(comp, /<p class="bb-sr" role="status" aria-live="polite">\{announcement\}<\/p>/);
  assert.doesNotMatch(comp, /\{#if announcement\}/, 'the region is never conditional');
  assert.doesNotMatch(comp, /aria-hidden=\{?announcement|hidden=\{?announcement/, 'it is hidden by CSS only, never from assistive tech');
  // Visually hidden the usual way, so it takes no room but is still read.
  assert.match(comp, /\.bb-sr \{[^}]*position: absolute;[^}]*width: 1px;[^}]*height: 1px;[^}]*clip-path: inset\(50%\);/);
  // It renders in the page itself, empty, in both modes.
  pcBridge.result = null; pcBridge.failed = false;
  for (const { html } of [renderStep(), renderPcStep()]) {
    assert.match(html, /<p class="bb-sr[^"]*" role="status" aria-live="polite"><\/p>/);
  }
});

test('the chip remove control is a real touch and keyboard target', () => {
  const comp = read('src/components/BlackboxSettings.svelte');
  // 24px is the smallest size a thumb or a keyboard user can be trusted to hit (WCAG 2.5.8).
  assert.match(comp, /\.bb-chip-x \{[^}]*min-width: 24px;[^}]*min-height: 24px;/, 'the × has a 24px square');
  // It stays a real button whose name says which target it removes, so the focus ring reads properly.
  assert.match(comp, /<button type="button" class="bb-chip-x" aria-label="\{T\.targetsRemove\}: \{t\.name\}"/);
  assert.match(comp, /\.bb-chip-x:focus-visible/, 'and it shows a focus ring');
});

test('focusSlot names the chip that takes the removed one place, and says when no chip is left', () => {
  const { step } = freshStep();
  const f = step.focusSlot;
  assert.equal(typeof f, 'function', 'the helper exists');
  // A chip in the middle or at the start: the one that shifts into its place, at the same index.
  assert.equal(f(3, 0), 0);
  assert.equal(f(3, 1), 1);
  // The last chip went: the new last one takes focus, so focus never runs off the end.
  assert.equal(f(3, 2), 1);
  assert.equal(f(2, 1), 0);
  // Nothing left, or nothing found: no chip to focus, so the name box does.
  assert.equal(f(1, 0), null);
  assert.equal(f(0, 0), null);
  assert.equal(f(3, -1), null);
});

test('removing a chip moves focus to the next chip, the new last chip or the name box - never to the removed node', async () => {
  const cases = [
    [['Alpha', 'Bravo', 'Carol'], 'Bravo', 'chip:Carol'],
    [['Alpha', 'Bravo', 'Carol'], 'Alpha', 'chip:Bravo'],
    [['Alpha', 'Bravo', 'Carol'], 'Carol', 'chip:Bravo'],
    [['Alpha'], 'Alpha', 'name-box']
  ];
  for (const [names, gone, want] of cases) {
    const settings = { ...bb.defaultSettings('web-a'), targets: names.map((n) => t('service', n)) };
    const { step } = freshStep({ settings });
    const landed = [];
    // What the page holds once the removal has rendered: one × per remaining chip, and the name box.
    step.chipsBox = { querySelectorAll: () => (settings.targets ?? []).map((x) => ({ focus: () => landed.push('chip:' + x.name) })) };
    step.nameField = { focus: () => landed.push('name-box') };
    await step.removeTarget('service', gone);
    assert.deepEqual(landed, [want], names.join('/') + ' minus ' + gone);
    assert.deepEqual(settings.targets.map((x) => x.name), names.filter((n) => n !== gone), 'the name came off the list');
    assert.ok(!landed[0].includes(gone), 'focus never lands on the chip that was removed');
  }
  // Focus moves only once the list has rendered, and never by scrolling the page or grabbing the body.
  const comp = read('src/components/BlackboxSettings.svelte');
  assert.match(comp, /async function removeTarget\(kind: TargetKind, name: string\) \{[\s\S]*?await tick\(\);[\s\S]*?focus\(\);/, 'the update is awaited before focus moves');
  assert.doesNotMatch(comp, /document\.body\.focus|scrollIntoView|window\.focus/, 'nothing jumps to the top of the page');
});

test('the announcement follows what actually happened: an add, a refusal, a tick, an untick and a chip removal', async () => {
  // A typed name that is accepted.
  const add = freshStep();
  add.step.manualName = 'Payroll API';
  add.step.addTarget();
  assert.equal(add.step.announcement, BB_TEXT.targetsAdded('Payroll API'));
  // A name refused for its characters: nothing was added, so nothing may be announced.
  const refused = freshStep();
  refused.step.manualName = 'a|b';
  refused.step.addTarget();
  assert.equal(refused.step.announcement, '', 'a refusal announces nothing');
  // Ticking a detected name adds it; ticking a name that is on the list takes it off.
  const held = { ...bb.defaultSettings('web-a'), targets: [t('service', 'W3SVC')] };
  const pickedUp = freshStep({ settings: { ...bb.defaultSettings('web-a'), targets: [] } });
  pickedUp.step.pick('service', 'W3SVC');
  assert.equal(pickedUp.step.announcement, BB_TEXT.targetsAdded('W3SVC'), 'a picked name is announced');
  const putDown = freshStep({ settings: { ...bb.defaultSettings('web-a'), targets: [t('service', 'W3SVC')] } });
  putDown.step.pick('service', 'W3SVC');
  assert.equal(putDown.step.announcement, BB_TEXT.targetsRemoved('W3SVC'), 'an unticked name is announced too');
  // A chip removal announces the name it took off the list.
  const removed = freshStep({ settings: { ...bb.defaultSettings('web-a'), targets: [t('service', 'W3SVC')] } });
  await removed.step.removeTarget('service', 'W3SVC');
  assert.equal(removed.step.announcement, BB_TEXT.targetsRemoved('W3SVC'));
});