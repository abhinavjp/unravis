const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

// BBX-1-12: the install-wizard target picker. Targets are owner-selected app pools and services,
// stored as kind+name only. This-PC mode reads the detected names once on mount; pack/remote mode
// is manual entry. A not-running target says "not running", never 0.

const root = path.resolve(__dirname, '..');
const cache = new Map();
function loadTypeScript(relativePath) {
  const filename = path.join(root, relativePath);
  if (cache.has(filename)) return cache.get(filename).exports;
  const source = fs.readFileSync(filename, 'utf8');
  const output = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022, esModuleInterop: true } }).outputText;
  const module = { exports: {} };
  cache.set(filename, module);
  const nodeRequire = createRequire(filename);
  const load = (request) => {
    if (/^\.\.?\//.test(request)) {
      const plain = path.join(path.dirname(filename), request + '.ts');
      const store = path.join(path.dirname(filename), request + '.svelte.ts');
      if (fs.existsSync(plain)) return loadTypeScript(path.relative(root, plain));
      if (fs.existsSync(store)) return loadTypeScript(path.relative(root, store));
    }
    return nodeRequire(request);
  };
  new Function('require', 'module', 'exports', '$state', output)(load, module, module.exports, (value) => value);
  return module.exports;
}
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
  assert.equal(BB_TEXT.targetsHeading, 'Targets (app pools and services)');
  assert.equal(BB_TEXT.targetsHelp, 'Pick the app pools and services to watch in detail. Other counters stay machine-wide.');
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

test('a stopped target says "not running"; an unreadable state says unknown, never stopped', () => {
  const comp = read('src/components/BlackboxSettings.svelte');
  // W2-07: configuration presence is not a running claim, and unknown is not stopped.
  assert.match(comp, /\{#if targetRunState\(t\) === 'stopped'\} <span class="bb-note">\{T\.targetsNotRunning\}<\/span>\{:else if targetRunState\(t\) === 'unknown'\} <span class="bb-note">\{T\.targetsStateUnknown\}<\/span>\{\/if\}/);
  assert.equal(BB_TEXT.targetsNotRunning, 'not running');
  assert.equal(BB_TEXT.targetsStateUnknown, 'running state unknown');
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
    stepScript = js + '\nmodule.exports.step = { addTarget, pick, removeTarget,'
      + ' get manualName() { return manualName; }, set manualName(v) { manualName = v; },'
      + ' get manualKind() { return manualKind; }, set manualKind(v) { manualKind = v; },'
      + ' get targetRefusal() { return typeof targetRefusal === \'undefined\' ? null : targetRefusal; },'
      + ' targetError: typeof targetError === \'undefined\' ? null : targetError };';
  }
  const module = { exports: {} };
  const need = (request) => {
    if (request === 'svelte') return { onMount: () => {} };
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