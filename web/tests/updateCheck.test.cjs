const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

// Update check in Unravis: the wording of each answer, the preview hash, and which release links are ours.

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

const upd = loadTypeScript('src/lib/updateCheck.ts');
const UNREACHABLE = 'Could not check for updates. Check your connection and try again later.';

test('the latest answer names the current version', () => {
  assert.equal(upd.updateCheckNote({ status: 'latest', current: '1.2.0', latest: '1.2.0', url: '' }), 'You are on the latest version (v1.2.0).');
});

test('the available answer names both versions', () => {
  assert.equal(upd.updateCheckNote({ status: 'available', current: '1.2.0', latest: '1.3.0', url: 'https://github.com/abhinavjp/unravis/releases/tag/v1.3.0' }), 'Version 1.3.0 is available. You have version 1.2.0.');
});

test('the unreachable answer says the check could not be made', () => {
  assert.equal(upd.updateCheckNote({ status: 'unreachable', current: '1.2.0', latest: '', url: '' }), UNREACHABLE);
});

test('the dev answer says there is nothing to compare', () => {
  assert.equal(upd.updateCheckNote({ status: 'dev', current: 'dev', latest: '', url: '' }), 'This is a development build, so there is nothing to compare.');
});

test('an unknown status gets the unreachable wording', () => {
  assert.equal(upd.updateCheckNote({ status: 'weird', current: '1.2.0', latest: '', url: '' }), UNREACHABLE);
});

test('the preview hash accepts only the three demo names', () => {
  assert.equal(upd.updatePreviewFrom('#update=available'), 'available');
  assert.equal(upd.updatePreviewFrom('#t=abc&update=offline'), 'offline');
  assert.equal(upd.updatePreviewFrom('#update=latest'), 'latest');
  assert.equal(upd.updatePreviewFrom('#update=bogus'), '');
  assert.equal(upd.updatePreviewFrom(''), '');
});

test('only links inside the project own repository count as release links', () => {
  assert.equal(upd.isOwnReleaseUrl('https://github.com/abhinavjp/unravis/releases/tag/v1.0.0'), true);
  assert.equal(upd.isOwnReleaseUrl('https://github.com/abhinavjp/unravis.evil.com/x'), false);
  assert.equal(upd.isOwnReleaseUrl('http://github.com/abhinavjp/unravis/'), false);
  assert.equal(upd.isOwnReleaseUrl(''), false);
});

test('the result state is always one of the four known names', () => {
  for (const name of ['latest', 'available', 'dev']) assert.equal(upd.updateCheckState({ status: name }), name);
  assert.equal(upd.updateCheckState({ status: 'weird' }), 'unreachable');
  assert.equal(upd.updateCheckState({ status: 'unreachable' }), 'unreachable');
});

test('without a window there is no remembered preview', () => {
  assert.equal(upd.updatePreviewNow(), '');
});

test('the unverified answer says it was ignored', () => {
  assert.equal(upd.updateCheckNote({ status: 'unverified', current: '1.2.0', latest: '', url: '' }), 'An update answer came back, but it did not pass the safety check, so it was ignored.');
  assert.equal(upd.updateCheckState({ status: 'unverified' }), 'unverified');
  assert.equal(upd.updateCheckState({ status: 'none' }), 'none');
  assert.equal(upd.updateCheckState({ status: 'surprise' }), 'unreachable');
});

test('the how-often labels', () => {
  assert.equal(upd.intervalLabel(30), 'Every 30 minutes');
  assert.equal(upd.intervalLabel(60), 'Every hour');
  assert.equal(upd.intervalLabel(360), 'Every 6 hours');
  assert.equal(upd.intervalLabel(1440), 'Every day');
  assert.equal(upd.intervalLabel(45), 'Every 45 minutes');
  assert.equal(upd.intervalLabel(120), 'Every 2 hours');
  assert.equal(upd.intervalLabel(4320), 'Every 3 days');
});

test('the last-checked line', () => {
  const now = new Date('2026-11-20T12:00:00Z');
  assert.equal(upd.checkedNote('', now), '');
  assert.equal(upd.checkedNote('nonsense', now), '');
  assert.equal(upd.checkedNote('2026-11-20T11:59:50Z', now), 'Last checked just now.');
  assert.equal(upd.checkedNote('2026-11-20T11:59:00Z', now), 'Last checked 1 minute ago.');
  assert.equal(upd.checkedNote('2026-11-20T11:30:00Z', now), 'Last checked 30 minutes ago.');
  assert.equal(upd.checkedNote('2026-11-20T10:00:00Z', now), 'Last checked 2 hours ago.');
  assert.equal(upd.checkedNote('2026-11-15T12:00:00Z', now), 'Last checked 5 days ago.');
});

test('the bar shows only for a newer version that was not dismissed', () => {
  const st = loadTypeScript('src/lib/updateState.svelte.ts');
  const avail = { status: 'available', current: '1.2.0', latest: '1.3.0', url: '' };
  assert.equal(st.updateBarVisible(avail, ''), true);
  assert.equal(st.updateBarVisible(avail, '1.3.0'), false);
  assert.equal(st.updateBarVisible({ ...avail, latest: '1.4.0' }, '1.3.0'), true, 'a newer version shows the bar again');
  assert.equal(st.updateBarVisible({ ...avail, status: 'latest' }, ''), false);
  assert.equal(st.updateBarVisible({ ...avail, status: 'unverified' }, ''), false);
  assert.equal(st.updateBarVisible({ ...avail, latest: '' }, ''), false);
  assert.equal(st.updateBarVisible(null, ''), false);
});

test('download and install: percent, sentences and the preview names', () => {
  assert.equal(upd.downloadPercent(0, 100), 0);
  assert.equal(upd.downloadPercent(42, 100), 42);
  assert.equal(upd.downloadPercent(999, 100), 100, 'never above 100');
  assert.equal(upd.downloadPercent(5, 0), 0, 'unknown total');
  assert.equal(upd.downloadPercent(-5, 100), 0);
  const base = { status: 'available', current: '1.2.0', latest: '1.3.0', url: '' };
  const say = (install) => upd.installNote({ ...base, install });
  assert.equal(say({ state: 'downloading', version: '1.3.0', done: 42, total: 100 }), 'Downloading version 1.3.0... 42%');
  assert.match(say({ state: 'ready', version: '1.3.0', done: 1, total: 1 }), /is ready/);
  assert.match(say({ state: 'launched', version: '1.3.0', done: 1, total: 1 }), /being applied.*Apply now again/);
  assert.equal(say({ state: 'failed', version: '1.3.0', done: 0, total: 0, error: 'The download was cut off.' }), 'Update failed. The download was cut off.');
  assert.equal(say(undefined), '');
  for (const name of ['downloading', 'ready', 'failed', 'launched']) assert.equal(upd.updatePreviewFrom('#update=' + name), name);
});

test('the bar stays open for a running download; other states, including a ready update, can be hidden with Later', () => {
  const st = loadTypeScript('src/lib/updateState.svelte.ts');
  const avail = { status: 'available', current: '1.2.0', latest: '1.3.0', url: '', canInstall: true };
  const at = (state) => ({ ...avail, install: { state, version: '1.3.0', done: 0, total: 1 } });
  for (const state of ['downloading']) assert.equal(st.updateBarVisible(at(state), '1.3.0'), true, state + ' stays');
  for (const state of ['idle', 'failed', 'launched', 'ready']) assert.equal(st.updateBarVisible(at(state), '1.3.0'), false, state + ' hides after Not now or Later');
  for (const state of ['idle', 'failed', 'launched', 'downloading', 'ready']) assert.equal(st.updateBarVisible(at(state), ''), true, state + ' shows');
  assert.equal(st.updateBarVisible({ ...at('ready'), status: 'latest' }, ''), false);
  assert.equal(st.updateBarVisible({ ...at('ready'), latest: '' }, ''), false);
});

test('the screen reader line has no percentage', () => {
  const base = { status: 'available', current: '1.2.0', latest: '1.3.0', url: '' };
  assert.equal(upd.installAnnouncement({ ...base, install: { state: 'downloading', version: '1.3.0', done: 40, total: 100 } }), 'Downloading version 1.3.0.');
  assert.equal(upd.installAnnouncement({ ...base, install: { state: 'ready', version: '1.3.0', done: 1, total: 1 } }), 'Version 1.3.0 is ready to install.');
  assert.equal(upd.installAnnouncement(base), '');
});
