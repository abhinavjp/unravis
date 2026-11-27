const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const ts = require('typescript');
const { compile } = require('svelte/compiler');
const { render } = require('svelte/server');
const { loadTypeScript } = require('./helpers/loadTs.cjs');

// W-02: the page says when the app has stopped (three lost requests in a row, or one failed 20-second check), says when this page belongs to
// an earlier run (the app answers again but refuses the session), and the update bar steps aside while either banner shows.
// Only a lost connection (code `unreachable`) counts; a read that ran out of time (code `timeout`) never does.

const root = path.join(__dirname, '..');
const flush = () => new Promise((resolve) => setImmediate(resolve));
const lost = () => Object.assign(new Error('Unravis could not be reached.'), { code: 'unreachable', status: 0 });
const timedOut = () => Object.assign(new Error('That took too long.'), { code: 'timeout', status: 0 });
const refused = () => Object.assign(new Error('Open Unravis from the link it printed when it started, or restart it.'), { code: 'no_session', status: 401 });

function setup(t, { hidden = false } = {}) {
  const doc = { visibilityState: hidden ? 'hidden' : 'visible', listeners: [], addEventListener(n, f) { this.listeners.push(f); }, removeEventListener(n, f) { this.listeners = this.listeners.filter((x) => x !== f); } };
  globalThis.document = doc;
  t.mock.timers.enable({ apis: ['setTimeout', 'Date'], now: 7_000_000 });
  // A fresh store for each test: overrides skip the loader cache.
  // The rules (livenessCore.ts) are one shared module, so each test starts them from a page that has heard nothing.
  const mod = loadTypeScript('src/lib/liveness.svelte.ts', { './visibility': loadTypeScript('src/lib/visibility.ts') });
  mod.resetLiveness();
  const tick = async (ms) => { let left = ms; do { const step = Math.min(left, 250); t.mock.timers.tick(step); await flush(); left -= step; } while (left > 0); };
  const show = (state) => { doc.visibilityState = state; for (const f of [...doc.listeners]) f(); };
  return { mod, tick, show, done() { t.mock.timers.reset(); delete globalThis.document; } };
}

test('two lost requests in a row are a hiccup; the third says the app has stopped', (t) => {
  const h = setup(t);
  try {
    h.mod.noteUnreachable();
    h.mod.noteUnreachable();
    assert.equal(h.mod.liveness.stopped, false);
    h.mod.noteUnreachable();
    assert.equal(h.mod.liveness.stopped, true);
  } finally { h.done(); }
});

test('an answer in between restarts the count, and an answer after a stop clears it', (t) => {
  const h = setup(t);
  try {
    h.mod.noteUnreachable(); h.mod.noteUnreachable();
    h.mod.noteAnswered();
    h.mod.noteUnreachable(); h.mod.noteUnreachable();
    assert.equal(h.mod.liveness.stopped, false, 'the two before the answer do not count');
    h.mod.noteUnreachable();
    assert.equal(h.mod.liveness.stopped, true);
    h.mod.noteAnswered();
    assert.equal(h.mod.liveness.stopped, false);
    assert.equal(h.mod.liveness.lastHeardAt, 7_000_000);
  } finally { h.done(); }
});

test('one failed light check says stopped at once; the next good check clears it', async (t) => {
  const h = setup(t);
  try {
    let alive = false;
    const probe = async () => { if (!alive) throw lost(); return { version: 'x' }; };
    const stop = h.mod.startLivenessWatch(probe);
    await h.tick(20_000);
    assert.equal(h.mod.liveness.stopped, true);
    alive = true;
    await h.tick(20_000);
    assert.equal(h.mod.liveness.stopped, false);
    stop();
  } finally { h.done(); }
});

test('a read that ran out of time never says stopped, and does not clear a stop either', async (t) => {
  const h = setup(t);
  try {
    const stop = h.mod.startLivenessWatch(async () => { throw timedOut(); });
    await h.tick(60_000);
    assert.equal(h.mod.liveness.stopped, false, 'a slow app is not a stopped app');
    h.mod.noteUnreachable(); h.mod.noteUnreachable(); h.mod.noteUnreachable();
    assert.equal(h.mod.liveness.stopped, true);
    await h.tick(60_000);
    assert.equal(h.mod.liveness.stopped, true, 'a timeout is no sign the app is back');
    stop();
  } finally { h.done(); }
});

test('a hidden tab makes no light check; it checks again once shown and a full interval has passed', async (t) => {
  const h = setup(t, { hidden: true });
  try {
    let calls = 0;
    const stop = h.mod.startLivenessWatch(async () => { calls += 1; });
    await h.tick(100_000);
    assert.equal(calls, 0, 'nothing is sent while the page is hidden');
    h.show('visible');
    await h.tick(1);
    assert.equal(calls, 1);
    stop();
  } finally { h.done(); }
});

test('a refused session after a stop means an earlier run, the light check then ends, and a later answer does not undo it', async (t) => {
  const h = setup(t);
  try {
    let reply = 'lost';
    let calls = 0;
    const probe = async () => { calls += 1; if (reply === 'lost') throw lost(); throw refused(); };
    h.mod.startLivenessWatch(probe);
    await h.tick(20_000);
    assert.equal(h.mod.liveness.stopped, true);
    reply = 'refused';
    await h.tick(20_000);
    assert.equal(h.mod.liveness.earlier, true);
    assert.equal(h.mod.liveness.stopped, false, 'one banner at a time');
    const seen = calls;
    await h.tick(100_000);
    assert.equal(calls, seen, 'the check has ended');
  } finally { h.done(); }
});

test('a refused session before the light check runs (the start screen) does not add the earlier-run banner', (t) => {
  const h = setup(t);
  try {
    h.mod.noteRefused();
    assert.equal(h.mod.liveness.earlier, false);
    assert.equal(h.mod.liveness.stopped, false);
  } finally { h.done(); }
});

test('an error answer from the app (a 500) means the app is there, and clears a stop', async (t) => {
  const h = setup(t);
  try {
    h.mod.noteUnreachable(); h.mod.noteUnreachable(); h.mod.noteUnreachable();
    assert.equal(h.mod.liveness.stopped, true);
    const stop = h.mod.startLivenessWatch(async () => { throw Object.assign(new Error('The disk is full.'), { status: 500 }); });
    await h.tick(20_000);
    assert.equal(h.mod.liveness.stopped, false);
    stop();
  } finally { h.done(); }
});

// The request layer reports into the store. Loads a fresh api.ts and liveness store with a fetch that behaves as `behave` says.
async function withApi(behave, fn) {
  const names = ['location', 'history', 'sessionStorage', 'fetch'];
  const saved = Object.fromEntries(names.map((name) => [name, Object.getOwnPropertyDescriptor(globalThis, name)]));
  const store = new Map([['unravis.session', 'link-session-token-0123456789abcdef+/=']]);
  const values = {
    location: { hash: '', search: '', pathname: '/' },
    history: { state: null, replaceState: () => {} },
    sessionStorage: { getItem: (k) => (store.has(k) ? store.get(k) : null), setItem: (k, v) => { store.set(k, String(v)); }, removeItem: (k) => { store.delete(k); } },
    fetch: behave
  };
  for (const name of names) Object.defineProperty(globalThis, name, { configurable: true, writable: true, value: values[name] });
  try {
    const liveness = loadTypeScript('src/lib/liveness.svelte.ts');
    liveness.resetLiveness();
    // api.ts reports into livenessCore.ts, the same shared module the store mirrors.
    return await fn(loadTypeScript('src/lib/api.ts', {}), liveness);
  } finally {
    for (const name of names) {
      if (saved[name]) Object.defineProperty(globalThis, name, saved[name]);
      else delete globalThis[name];
    }
  }
}

test('three real requests that cannot reach the app in a row make the page say stopped; an answer clears it', async () => {
  let up = false;
  await withApi(async () => { if (!up) throw new TypeError('Failed to fetch'); return { ok: true, status: 200, headers: { get: () => null }, json: async () => ({}) }; }, async (module, liveness) => {
    for (let i = 0; i < 2; i += 1) await assert.rejects(module.api.info(), (e) => e.code === 'unreachable');
    assert.equal(liveness.liveness.stopped, false);
    await assert.rejects(module.api.info(), (e) => e.code === 'unreachable');
    assert.equal(liveness.liveness.stopped, true);
    up = true;
    await module.api.info();
    assert.equal(liveness.liveness.stopped, false);
    assert.ok(liveness.liveness.lastHeardAt > 0);
  });
});

test('a real request that ran out of time (code timeout) is never counted as a lost app', async (t) => {
  // The browser's own AbortSignal.timeout cannot be moved by a mock clock, so the page's timer fallback is the one under test.
  const nativeTimeout = AbortSignal.timeout;
  AbortSignal.timeout = undefined;
  t.mock.timers.enable({ apis: ['setTimeout'] });
  try {
    await withApi((url, init) => new Promise((resolve, reject) => {
      init.signal.addEventListener('abort', () => reject(new DOMException('The operation was aborted.', 'AbortError')), { once: true });
    }), async (module, liveness) => {
      const { READ_TIMEOUT_MS } = loadTypeScript('src/lib/requestTimeout.ts');
      for (let i = 0; i < 4; i += 1) {
        const pending = module.api.info();
        const outcome = assert.rejects(pending, (e) => e.code === 'timeout');
        await flush();
        t.mock.timers.tick(READ_TIMEOUT_MS + 1);
        await outcome;
      }
      assert.equal(liveness.liveness.stopped, false, 'four timeouts in a row are a slow app, not a stopped one');
    });
  } finally { t.mock.timers.reset(); AbortSignal.timeout = nativeTimeout; }
});

test('a 401 on a live app, while the page watches, says earlier run; a 500 does not', async () => {
  let status = 500;
  await withApi(async () => ({ ok: false, status, headers: { get: () => null }, json: async () => (status === 500 ? { error: 'The disk is full.' } : { error: 'Invalid or missing session token.' }) }), async (module, liveness) => {
    const stop = liveness.startLivenessWatch(async () => {});
    await assert.rejects(module.api.info(), (e) => e.status === 500);
    assert.equal(liveness.liveness.earlier, false);
    status = 401;
    await assert.rejects(module.api.info(), (e) => e.code === 'no_session');
    assert.equal(liveness.liveness.earlier, true);
    stop();
  });
});

// The banner and the update bar, rendered by Svelte itself (server render, no DOM).
function loadComponent(file, mods) {
  const compiled = compile(fs.readFileSync(path.join(root, file), 'utf8'), { filename: path.basename(file), generate: 'server' });
  const js = ts.transpileModule(compiled.js.code, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022, esModuleInterop: true } }).outputText;
  const held = { exports: {} };
  const load = (request) => mods[request] ?? require(request);
  new Function('require', 'module', 'exports', js)(load, held, held.exports);
  return held.exports.default;
}

const copy = loadTypeScript('src/lib/copy/livenessCopy.ts');

test('the stopped banner renders amber text with the start step, the saved-files line and a Reload button', () => {
  const liveness = loadTypeScript('src/lib/liveness.svelte.ts');
  liveness.resetLiveness();
  liveness.liveness.stopped = true;
  liveness.liveness.lastHeardAt = Date.UTC(2026, 10, 27, 14, 32);
  const Banner = loadComponent('src/components/LivenessBanner.svelte', { '../lib/liveness.svelte': liveness, '../lib/copy/livenessCopy': copy });
  const { body } = render(Banner, { props: {} });
  assert.match(body, /data-testid="stopped-banner"/);
  assert.match(body, /role="alert"/);
  assert.match(body, /Unravis has stopped\./);
  assert.match(body, /Start Unravis again<\/b> from the Start menu or its tray icon, then press Reload\./);
  assert.match(body, /Cases and files you saved stay on this PC\./);
  assert.match(body, /updated\. Last heard from it at \d\d:\d\d\. Cases and files/, 'sentences are separated by a space');
  assert.match(body, /data-testid="stopped-reload"[^>]*>Reload this page<\/button>/);
  assert.doesNotMatch(body, /earlier-banner/);
});

test('the stopped banner leaves out "Last heard" when the page never heard from the app', () => {
  const liveness = loadTypeScript('src/lib/liveness.svelte.ts');
  liveness.resetLiveness();
  liveness.liveness.stopped = true;
  const Banner = loadComponent('src/components/LivenessBanner.svelte', { '../lib/liveness.svelte': liveness, '../lib/copy/livenessCopy': copy });
  const { body } = render(Banner, { props: {} });
  assert.doesNotMatch(body, /Last heard/);
});

test('the earlier-run banner says Unravis was restarted, points to the tray and has no Reload button', () => {
  const liveness = loadTypeScript('src/lib/liveness.svelte.ts');
  liveness.resetLiveness();
  liveness.liveness.earlier = true;
  const Banner = loadComponent('src/components/LivenessBanner.svelte', { '../lib/liveness.svelte': liveness, '../lib/copy/livenessCopy': copy });
  const { body } = render(Banner, { props: {} });
  assert.match(body, /data-testid="earlier-banner"/);
  assert.match(body, /Unravis was restarted\./);
  assert.match(body, /Open Unravis again from the tray icon<\/b> and use the new page\. You can close this one\./);
  assert.doesNotMatch(body, /<button/);
  assert.doesNotMatch(body, /Reload/);
});

test('no banner renders while the app answers', () => {
  const liveness = loadTypeScript('src/lib/liveness.svelte.ts');
  liveness.resetLiveness();
  const Banner = loadComponent('src/components/LivenessBanner.svelte', { '../lib/liveness.svelte': liveness, '../lib/copy/livenessCopy': copy });
  assert.doesNotMatch(render(Banner, { props: {} }).body, /banner|live-bar/);
});

test('the update bar shows its "being applied" text normally and is hidden while the app is stopped or restarted', () => {
  const liveness = loadTypeScript('src/lib/liveness.svelte.ts');
  const updateCheck = loadTypeScript('src/lib/updateCheck.ts');
  const reply = { status: 'available', latest: '1.3.0', current: '1.2.0', url: '', canInstall: true, install: { state: 'launched', version: '1.3.0' } };
  const state = {
    appLink: { gone: false, recheckFailed: false }, installing: { busy: false, error: '' }, updates: { reply, dismissed: '' },
    updateBarVisible: (r, d) => !!r && r.status === 'available' && r.latest !== d,
    checkAppAgain: async () => {}, dismissUpdate: () => {}, dismissUpdateResult: async () => {}, startUpdateDownload: async () => {}, startUpdateInstall: async () => {}
  };
  const mods = { '../lib/liveness.svelte': liveness, '../lib/updateState.svelte': state, '../lib/updateLink': loadTypeScript('src/lib/updateLink.ts'), '../lib/updateCheck': updateCheck, svelte: require('svelte') };
  const Bar = loadComponent('src/components/UpdateBanner.svelte', mods);
  liveness.resetLiveness();
  assert.match(render(Bar, { props: {} }).body, /data-testid="update-bar"/);
  liveness.liveness.stopped = true;
  assert.doesNotMatch(render(Bar, { props: {} }).body, /update-bar/);
  liveness.resetLiveness();
  liveness.liveness.earlier = true;
  assert.doesNotMatch(render(Bar, { props: {} }).body, /update-bar/);
});

test('the banner words pass the copy lint and read at or below grade 8.5', () => {
  const { lintCopy, fleschKincaidGrade, MAX_GRADE, MIN_WORDS_FOR_GRADE } = loadTypeScript('src/lib/copy/copylint.ts');
  const { deck } = loadTypeScript('src/lib/copy/deck.ts');
  const entries = copy.livenessLintEntries();
  assert.deepEqual(lintCopy(entries, deck.terms), []);
  // The grade is only meaningful for a sentence of some length (the lint's own rule); a four-word title has none.
  for (const entry of entries) if (entry.text.split(/\s+/).length >= MIN_WORDS_FOR_GRADE) assert.ok(fleschKincaidGrade(entry.text) <= MAX_GRADE, entry.id + ' ' + fleschKincaidGrade(entry.text));
});

test('the app shell wires it: the banner sits above the update bar, and the light check reads the version', () => {
  const app = fs.readFileSync(path.join(root, 'src/App.svelte'), 'utf8');
  assert.match(app, /<LivenessBanner \/>\s*<UpdateBanner \/>/);
  assert.match(app, /startLivenessWatch\(\(\) => api\.version\(\)\)/);
});
