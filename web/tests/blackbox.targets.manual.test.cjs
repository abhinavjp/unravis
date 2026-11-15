const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

// W3-06: successful-but-incomplete discovery must still let the owner add a known valid name,
// and a selected target's state must be honest. The endpoint offers running services only and
// caps each list at 256, so a name absent from a list is unknown, never guessed stopped. The
// manual add row sits alongside the detected lists, not only in their else branch. Pack/remote
// ('manual') mode is unchanged: it never fetches and its chips carry no state note.

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
const { BB_TEXT, blackboxCopyEntries } = loadTypeScript('src/lib/copy/blackboxCopy.ts');
const { lintScreenStrings } = loadTypeScript('src/lib/copy/copylint.ts');
const { deck } = loadTypeScript('src/lib/copy/deck.ts');
const { pickedTargetState, targetRunState, toggleTarget } = bb;

// A successful answer that detected both sources but does not list the target the owner wants.
const incomplete = {
  appPools: [{ name: 'DefaultAppPool', state: 'unknown' }],
  services: [{ name: 'W3SVC', running: true, state: 'running' }],
  detected: true,
  appPoolsState: 'detected',
  servicesState: 'detected'
};

/* ------------------------------------------------------------------ the honest state helper */

test('pickedTargetState reads a listed target through targetRunState (running, stopped, unknown, legacy boolean)', () => {
  assert.equal(pickedTargetState('service', 'W3SVC', incomplete), 'running');
  assert.equal(pickedTargetState('appPool', 'DefaultAppPool', incomplete), 'unknown');
  const stopped = { appPools: [], services: [{ name: 'Spooler', state: 'stopped' }], detected: true, servicesState: 'detected' };
  assert.equal(pickedTargetState('service', 'Spooler', stopped), 'stopped');
  // An older server that sends only a boolean still reads correctly.
  const legacy = { appPools: [], services: [{ name: 'Spooler', running: false }], detected: true };
  assert.equal(pickedTargetState('service', 'Spooler', legacy), 'stopped');
  assert.equal(targetRunState({ name: 'Spooler', running: false }), 'stopped');
});

test('a name absent from a successfully detected list is not-in-list, never guessed stopped', () => {
  // The desired stopped service is not offered (the endpoint lists running services only).
  assert.equal(pickedTargetState('service', 'MyStoppedSvc', incomplete), 'not-in-list');
  assert.notEqual(pickedTargetState('service', 'MyStoppedSvc', incomplete), 'stopped');
  // An app pool absent from a detected (but possibly capped) config list is also unknown.
  assert.equal(pickedTargetState('appPool', 'MissingPool', incomplete), 'not-in-list');
});

test('a capped-away name is not-in-list and can still be added as kind+name only', () => {
  // 256 running services fill the cap; the wanted name sorts beyond it and is absent.
  const full = Array.from({ length: 256 }, (_, i) => ({ name: 'Svc' + String(i).padStart(3, '0'), running: true, state: 'running' }));
  const capped = { appPools: [], services: full, detected: true, servicesState: 'detected' };
  assert.equal(capped.services.length, 256);
  assert.equal(pickedTargetState('service', 'ZuluSvc', capped), 'not-in-list');
  // The owner can still add it; only kind+name is stored, and its state stays honestly unknown.
  const added = toggleTarget([], 'service', 'ZuluSvc');
  assert.deepEqual(added, [{ kind: 'service', name: 'ZuluSvc' }]);
  assert.deepEqual(Object.keys(added[0]).sort(), ['kind', 'name']);
  assert.equal(pickedTargetState('service', 'ZuluSvc', capped), 'not-in-list');
});

test('the per-source state tells unreadable and not-found apart from a detected-but-absent name', () => {
  const unreadable = { appPools: [{ name: 'DefaultAppPool', state: 'unknown' }], services: [], detected: true, appPoolsState: 'detected', servicesState: 'unavailable' };
  // Pools were read: an absent pool is not-in-list. Services could not be read: an absent service is list-unreadable.
  assert.equal(pickedTargetState('appPool', 'OtherPool', unreadable), 'not-in-list');
  assert.equal(pickedTargetState('service', 'AnySvc', unreadable), 'list-unreadable');
  const noIis = { appPools: [], services: [{ name: 'W3SVC', state: 'running' }], detected: true, appPoolsState: 'not-found', servicesState: 'detected' };
  assert.equal(pickedTargetState('appPool', 'AnyPool', noIis), 'source-not-found');
  assert.equal(pickedTargetState('service', 'OtherSvc', noIis), 'not-in-list');
  // Nothing read at all (fetch failed, or neither source answered): unknown, never stopped.
  assert.equal(pickedTargetState('service', 'AnySvc', null), 'list-unreadable');
  const none = { appPools: [], services: [], detected: false, appPoolsState: 'unavailable', servicesState: 'unavailable' };
  assert.equal(pickedTargetState('service', 'AnySvc', none), 'list-unreadable');
});

test('an older server with no per-source state still labels an absent name unknown, not stopped', () => {
  const legacyDetected = { appPools: [{ name: 'DefaultAppPool' }], services: [{ name: 'W3SVC', running: true }], detected: true };
  assert.equal(pickedTargetState('service', 'MyStoppedSvc', legacyDetected), 'not-in-list');
  const legacyNone = { appPools: [], services: [], detected: false };
  assert.equal(pickedTargetState('service', 'MyStoppedSvc', legacyNone), 'list-unreadable');
});

/* ------------------------------------------------------------------ manual add alongside the lists */

test('the manual add row renders alongside the detected lists, not only in their else branch', () => {
  const comp = read('src/components/BlackboxSettings.svelte');
  // The else branch now holds only the manual note; the add row follows the closing {/if}, so it
  // renders whether or not detection succeeded. This is the W3-06 fix: the old else-only row is gone.
  assert.match(comp, /\{:else\}\s*<p class="bb-note">\{T\.targetsManualNote\}<\/p>\s*\{\/if\}\s*<div class="bb-target-add">/, 'the add row is outside the showLists conditional');
  assert.match(comp, /\{#if showLists\}/, 'the detected lists are still offered when detection succeeded');
  assert.match(comp, /\{@render targetChips\(extraPicked\)\}/, 'selected names absent from the lists render as chips');
  assert.match(comp, /\{#if !showLists\}\s*\{@render targetChips\(picked\)\}/, 'with no lists, every picked name renders as a chip');
  // The add row keeps the same kind+name helper and the same server-side validation/cap (nothing weakened here).
  assert.match(comp, /function addTarget\(\)/);
  assert.match(comp, /settings\.targets = toggleTarget\(picked, manualKind, name\)/);
  assert.match(comp, /disabled=\{!manualName\.trim\(\)\} onclick=\{addTarget\}/);
});

test('successful-but-incomplete discovery still allows adding the wanted stopped service, stored as kind+name', () => {
  // The owner wants MyStoppedSvc, which the running-only list does not offer. They type it and add it.
  let targets = [];
  targets = toggleTarget(targets, 'service', 'MyStoppedSvc');
  assert.deepEqual(targets, [{ kind: 'service', name: 'MyStoppedSvc' }]);
  // It is honestly unknown (not-in-list), never stopped, and survives a settings round-trip.
  assert.equal(pickedTargetState('service', 'MyStoppedSvc', incomplete), 'not-in-list');
  const settings = bb.defaultSettings('web-a');
  settings.targets = targets;
  const back = JSON.parse(JSON.stringify(settings));
  assert.deepEqual(back.targets, [{ kind: 'service', name: 'MyStoppedSvc' }]);
  assert.equal(bb.settingsProblem(back), null);
  for (const t of back.targets) assert.deepEqual(Object.keys(t).sort(), ['kind', 'name'], 'no path, command line or key is stored');
});

/* ------------------------------------------------------------------ honest chip labels */

test('a selected stopped service is labelled stopped; an absent name is labelled unknown, never stopped', () => {
  const comp = read('src/components/BlackboxSettings.svelte');
  // The chip note maps each honest state to its copy; absent-name states never map to "not running".
  assert.match(comp, /if \(s === 'stopped'\) return T\.targetsNotRunning;/);
  assert.match(comp, /if \(s === 'unknown'\) return T\.targetsStateUnknown;/);
  assert.match(comp, /if \(s === 'not-in-list'\) return T\.targetsNotInList;/);
  assert.match(comp, /if \(s === 'source-not-found'\) return T\.targetsSourceNotFound;/);
  assert.match(comp, /if \(s === 'list-unreadable'\) return T\.targetsListUnreadable;/);
  assert.match(comp, /pickedTargetState\(t\.kind, t\.name, \{ appPools, services, detected, appPoolsState, servicesState \}\)/);
  // The listed checkbox keeps its stopped label (W2-07). UX-6: no per-row targetsStateUnknown
  // assertion may live here — when every row is unknown, one group note under the legend says it,
  // and UX-8 deletes that copy key; a leftover assertion would break it.
  assert.match(comp, /\{#if targetRunState\(t\) === 'stopped'\}\{' '\}<span class="bb-note">\{T\.targetsNotRunning\}<\/span>/);
  // The absent-name copy says unknown, and never claims the target is stopped or not running.
  for (const key of ['targetsNotInList', 'targetsListUnreadable', 'targetsSourceNotFound']) {
    assert.match(BB_TEXT[key], /unknown/, key + ' says the running state is unknown');
    assert.doesNotMatch(BB_TEXT[key], /not running|stopped/i, key + ' never guesses stopped');
  }
  assert.equal(BB_TEXT.targetsNotRunning, 'not running');
  assert.equal(BB_TEXT.targetsStateUnknown, 'running state unknown');
});

test('chips carry a state note only on this PC after the single read settles; the note is rendered', () => {
  const comp = read('src/components/BlackboxSettings.svelte');
  assert.match(comp, /const chipsLabelled = \$derived\(targetMode === 'pc' && targetsRead\)/, 'pack/remote chips stay unlabelled');
  assert.match(comp, /\{#if chipsLabelled && chipNote\(t\)\} <span class="bb-note">\{chipNote\(t\)\}<\/span>\{\/if\}/, 'the chip renders its honest note');
});

/* ------------------------------------------------------------------ read once, per-source state kept */

test('the picker keeps the per-source states from the one read and never polls', () => {
  const comp = read('src/components/BlackboxSettings.svelte');
  assert.match(comp, /onMount\(\(\) => \{\s*if \(targetMode !== 'pc'\) return;/, 'pack/remote mode never fetches');
  assert.match(comp, /blackboxApi\.targets\(\)/);
  assert.match(comp, /appPoolsState = answer\.appPoolsState;/);
  assert.match(comp, /servicesState = answer\.servicesState;/);
  assert.match(comp, /\.catch\(\(\) => \{ targetsRead = true;/, 'a failed read still settles so chips can say the list could not be read');
  assert.doesNotMatch(comp, /setInterval|setTimeout/, 'no polling');
  assert.equal((comp.match(/targetsRead = true;/g) ?? []).length, 2, 'read settles on success and on failure');
});

/* ------------------------------------------------------------------ pack/remote mode unchanged */

test('pack/remote (manual) mode is unchanged: no fetch, typed names, unlabelled chips', () => {
  const comp = read('src/components/BlackboxSettings.svelte');
  assert.match(comp, /targetMode\?: 'pc' \| 'manual'/);
  assert.match(comp, /\{T\.targetsManualNote\}/, 'the manual note is still shown when there are no lists');
  assert.match(comp, /const chipsLabelled = \$derived\(targetMode === 'pc' && targetsRead\)/, 'manual mode never labels chips');
  const wizard = read('src/components/BlackboxWizard.svelte');
  assert.match(wizard, /targetMode=\{where === 'pc' \? 'pc' : 'manual'\}/, 'the wizard passes the mode it is in');
  // In manual mode nothing is detected, so every picked name is a chip and the add row is the only way in.
  assert.match(comp, /\{#if !showLists\}\s*\{@render targetChips\(picked\)\}/);
});

/* ------------------------------------------------------------------ only kind+name is stored */

test('the picker stores only kind+name and never a path, command line or key', () => {
  const comp = read('src/components/BlackboxSettings.svelte');
  assert.match(comp, /toggleTarget\(picked, kind, name\)/, 'checking a name uses the pure helper');
  assert.match(comp, /function removeTarget\(kind: TargetKind, name: string\)/);
  assert.doesNotMatch(comp, /commandLine|imagePath|\.path\b|readKey|password|secret/, 'no path, command line or key is stored');
  const both = toggleTarget(toggleTarget([], 'appPool', 'DefaultAppPool'), 'service', 'W3SVC');
  for (const t of both) assert.deepEqual(Object.keys(t).sort(), ['kind', 'name']);
});

/* ------------------------------------------------------------------ copy */

test('the new target copy passes the screen-string lint and the whole copy still lints clean', () => {
  const entries = blackboxCopyEntries();
  assert.deepEqual(lintScreenStrings(entries, deck.terms), []);
  for (const key of ['targetsNotInList', 'targetsListUnreadable', 'targetsSourceNotFound', 'targetsListNote']) {
    assert.equal(typeof BB_TEXT[key], 'string', key + ' is a plain string');
    assert.ok(entries.some((e) => e.id === 'blackbox.' + key), key + ' is linted');
  }
  assert.equal(BB_TEXT.targetsListNote, 'The list may be incomplete. Type a name below to add a target that is not listed.');
});