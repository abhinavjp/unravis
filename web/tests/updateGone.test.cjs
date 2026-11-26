const assert = require('node:assert/strict');
const test = require('node:test');
const { loadTypeScript } = require('./helpers/loadTs.cjs');

// An update restarts Unravis on a new address with a new session. The page that pressed Apply now cannot reach the new run, so the bar
// must say so (and stop offering a button that can only fail with "Failed to fetch"). It must not say so for one lost read, or when the
// app is only refusing a request.

const flush = () => new Promise((resolve) => setImmediate(resolve));
const netError = () => new TypeError('Failed to fetch');
const apiError = (message, code, status) => Object.assign(new Error(message), { code, status });

function setup(t, api) {
  const doc = { visibilityState: 'visible', hasFocus: () => true, addEventListener() {}, removeEventListener() {} };
  globalThis.document = doc;
  globalThis.window = { addEventListener() {}, removeEventListener() {} };
  globalThis.localStorage = { getItem: () => null, setItem() {}, removeItem() {} };
  t.mock.timers.enable({ apis: ['setTimeout', 'Date'], now: 5_000_000 });
  const mod = loadTypeScript('src/lib/updateState.svelte.ts', { './api': { api }, './updateCheck': { updatePreviewNow: () => '' } });
  const tick = async (ms) => { let left = ms; do { const step = Math.min(left, 250); t.mock.timers.tick(step); await flush(); left -= step; } while (left > 0); };
  return { mod, tick, done() { t.mock.timers.reset(); delete globalThis.document; delete globalThis.window; delete globalThis.localStorage; } };
}

const launched = { status: 'available', latest: '1.3.0', current: '1.2.0', url: '', install: { state: 'launched', version: '1.3.0' } };

test('lostServer: a failed fetch or an unknown session is a lost app; a refusal from a live app is not', () => {
  const { lostServer } = loadTypeScript('src/lib/updateLink.ts');
  assert.equal(lostServer(netError()), true);
  assert.equal(lostServer(apiError('no session', 'no_session', 401)), true);
  assert.equal(lostServer(apiError('Update is on hold', undefined, 409)), false);
  assert.equal(lostServer(Object.assign(new Error('x'), { name: 'AbortError' })), false);
  assert.equal(lostServer(new Error('plain')), false);
});

test('launched, then the app stops answering: the bar says the app is gone after two misses, not one', async (t) => {
  let alive = true;
  const h = setup(t, { updateStatus: async () => { if (!alive) throw netError(); return launched; } });
  try {
    await h.mod.refreshUpdateStatus();
    assert.equal(h.mod.appLink.gone, false);
    alive = false;
    await h.tick(3000);
    assert.equal(h.mod.appLink.gone, false, 'one lost read is a hiccup');
    await h.tick(3000);
    assert.equal(h.mod.appLink.gone, true);
    await h.tick(30_000); // the poll stopped; nothing more is sent
  } finally { h.done(); }
});

test('Check again: the note clears when the app answers', async (t) => {
  let alive = false;
  const h = setup(t, { updateStatus: async () => { if (!alive) throw netError(); return launched; } });
  try {
    h.mod.updates.reply = launched;
    await h.mod.refreshUpdateStatus();
    await h.mod.refreshUpdateStatus();
    assert.equal(h.mod.appLink.gone, true);
    await h.mod.checkAppAgain();
    assert.equal(h.mod.appLink.gone, true, 'still gone while it does not answer');
    alive = true;
    await h.mod.checkAppAgain();
    assert.equal(h.mod.appLink.gone, false);
  } finally { h.done(); }
});

test('Apply now on a dead page: no raw "Failed to fetch"; the bar says the app is gone', async (t) => {
  const h = setup(t, { updateInstall: async () => { throw netError(); }, updateStatus: async () => { throw netError(); } });
  try {
    await h.mod.startUpdateInstall();
    assert.equal(h.mod.appLink.gone, true);
    assert.equal(h.mod.installing.error, '');
    assert.equal(h.mod.installing.busy, false);
  } finally { h.done(); }
});

test('Apply now refused by a live app keeps its own sentence and does not say the app is gone', async (t) => {
  const h = setup(t, { updateInstall: async () => { throw apiError('The update is not downloaded yet.', undefined, 409); }, updateStatus: async () => ({ ...launched, install: { state: 'idle' } }) });
  try {
    await h.mod.startUpdateInstall();
    assert.equal(h.mod.appLink.gone, false);
    assert.equal(h.mod.installing.error, 'The update is not downloaded yet.');
  } finally { h.done(); }
});

test('the app answers on the same page after all (the setup was closed): Apply now works the second time', async (t) => {
  let installs = 0;
  const h = setup(t, { updateInstall: async () => { installs++; return launched; }, updateStatus: async () => launched });
  try {
    await h.mod.startUpdateInstall();
    await h.mod.startUpdateInstall();
    assert.equal(installs, 2);
    assert.equal(h.mod.appLink.gone, false);
    assert.equal(h.mod.installing.error, '');
  } finally { h.done(); }
});

test('Apply now succeeds, then the app vanishes: the page notices within a few seconds without a reload', async (t) => {
  let alive = true;
  const h = setup(t, {
    updateInstall: async () => launched,
    updateStatus: async () => { if (!alive) throw netError(); return launched; },
  });
  try {
    await h.mod.startUpdateInstall();
    alive = false;
    await h.tick(3000);
    assert.equal(h.mod.appLink.gone, false, 'one lost read is a hiccup');
    await h.tick(3000);
    assert.equal(h.mod.appLink.gone, true);
  } finally { h.done(); }
});

test('one lost request on a live app is a hiccup: a plain retry note, not "gone"', async (t) => {
  const h = setup(t, { updateInstall: async () => { throw netError(); }, updateStatus: async () => ({ ...launched, install: { state: 'ready', version: '1.3.0' } }) });
  try {
    await h.mod.startUpdateInstall();
    assert.equal(h.mod.appLink.gone, false);
    assert.equal(h.mod.installing.error, 'Could not reach Unravis just now. Try again.');
  } finally { h.done(); }
});

test('Apply now lost its reply but the update did start: no "could not reach" error, and the page keeps watching for the restart', async (t) => {
  let alive = true;
  const h = setup(t, {
    updateInstall: async () => { throw netError(); },
    updateStatus: async () => { if (!alive) throw netError(); return launched; },
  });
  try {
    await h.mod.startUpdateInstall();
    assert.equal(h.mod.installing.error, '', 'the status read says launched: that is not a failure to tell the person about');
    assert.equal(h.mod.appLink.gone, false);
    alive = false;
    await h.tick(3000);
    assert.equal(h.mod.appLink.gone, false, 'one lost read is a hiccup');
    await h.tick(3000);
    assert.equal(h.mod.appLink.gone, true, 'the launched poll was started, so the restart is noticed');
  } finally { h.done(); }
});

test('Check again with no answer says so; an answer clears it', async (t) => {
  let alive = false;
  const h = setup(t, { updateInstall: async () => { throw netError(); }, updateStatus: async () => { if (!alive) throw netError(); return launched; } });
  try {
    await h.mod.startUpdateInstall();
    assert.equal(h.mod.appLink.gone, true);
    assert.equal(h.mod.appLink.recheckFailed, false);
    await h.mod.checkAppAgain();
    assert.equal(h.mod.appLink.recheckFailed, true);
    alive = true;
    await h.mod.checkAppAgain();
    assert.equal(h.mod.appLink.recheckFailed, false);
    assert.equal(h.mod.appLink.gone, false);
  } finally { h.done(); }
});

test('a successful press clears an earlier lost read, so one later miss is not enough', async (t) => {
  let fail = true;
  const h = setup(t, { updateInstall: async () => launched, updateStatus: async () => { if (fail) throw netError(); return launched; } });
  try {
    h.mod.updates.reply = launched;
    await h.mod.refreshUpdateStatus(); // one miss
    await h.mod.startUpdateInstall(); // answers
    await h.mod.refreshUpdateStatus(); // another miss
    assert.equal(h.mod.appLink.gone, false);
  } finally { h.done(); }
});
