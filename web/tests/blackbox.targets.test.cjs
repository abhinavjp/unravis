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
const { BB_TEXT, blackboxCopyEntries } = loadTypeScript('src/lib/copy/blackboxCopy.ts');
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

test('a not-running target shows the words "not running", never 0', () => {
  const comp = read('src/components/BlackboxSettings.svelte');
  assert.match(comp, /\{#if !t\.running\} <span class="bb-note">\{T\.targetsNotRunning\}<\/span>\{\/if\}/);
  assert.equal(BB_TEXT.targetsNotRunning, 'not running');
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