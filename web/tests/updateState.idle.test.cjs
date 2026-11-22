const assert = require('node:assert/strict');
const test = require('node:test');
const { loadTypeScript } = require('./helpers/loadTs.cjs');

// BL-429 and the silent updater (BL-168/171): what the page sends while visible and while hidden.
//   page presence   POST /api/ui/presence every 45 s while the page is visible and was touched in the last 5 minutes; nothing otherwise.
//                   The program counts the app as in use for 2 minutes after the last ping (internal/server/updater_auto.go).
//   update status   one read when the page opens, one every 10 minutes while visible; none while hidden; one at once on show when a full
//                   interval has passed.
//   download poll   a read a second while a download runs and the page is visible; none while hidden; one at once on show.

const MIN = 60_000;
const flush = () => new Promise((resolve) => setImmediate(resolve));

function setup(t) {
  const sent = { presence: 0, status: 0 };
  let installState = 'idle';
  const listeners = new Map();
  const doc = {
    visibilityState: 'visible',
    hasFocus: () => true,
    handlers: new Set(),
    addEventListener(type, fn) { if (type === 'visibilitychange') doc.handlers.add(fn); },
    removeEventListener(type, fn) { if (type === 'visibilitychange') doc.handlers.delete(fn); },
    set(state) { doc.visibilityState = state; for (const fn of [...doc.handlers]) fn(); }
  };
  globalThis.document = doc;
  globalThis.window = {
    addEventListener(type, fn) { (listeners.get(type) ?? listeners.set(type, new Set()).get(type)).add(fn); },
    removeEventListener(type, fn) { listeners.get(type)?.delete(fn); }
  };
  globalThis.localStorage = { getItem: () => null, setItem() {}, removeItem() {} };
  t.mock.timers.enable({ apis: ['setTimeout', 'Date'], now: 5_000_000 });
  const api = {
    uiPresence: async () => { sent.presence++; return { ok: true }; },
    updateStatus: async () => { sent.status++; return { status: 'available', latest: '1.3.0', current: '1.2.0', url: '', install: { state: installState } }; }
  };
  const mod = loadTypeScript('src/lib/updateState.svelte.ts', { './api': { api }, './updateCheck': { updatePreviewNow: () => '' } });
  const tick = async (ms) => { let left = ms; do { const step = Math.min(left, 250); t.mock.timers.tick(step); await flush(); left -= step; } while (left > 0); };
  const touch = () => { for (const fn of listeners.get('pointermove') ?? []) fn(); };
  return { sent, doc, mod, tick, touch, setInstall: (state) => { installState = state; }, listeners, done() { t.mock.timers.reset(); delete globalThis.document; delete globalThis.window; delete globalThis.localStorage; } };
}

test('visible and touched: presence pings at once and every 45 seconds, status is read at once and every 10 minutes', async (t) => {
  const h = setup(t);
  try {
    const stop = h.mod.startUpdateWatch();
    await h.tick(0);
    assert.equal(h.sent.presence, 1, 'a ping when the page opens');
    assert.equal(h.sent.status, 1, 'one status read when the page opens');
    for (let i = 0; i < 4; i++) { h.touch(); await h.tick(45_000); }
    assert.equal(h.sent.presence, 5);
    await h.tick(10 * MIN - 4 * 45_000);
    assert.equal(h.sent.status, 2, 'the next status read is ten minutes after the first');
    stop();
    const before = { ...h.sent };
    await h.tick(20 * MIN);
    assert.deepEqual(h.sent, before, 'stopped means stopped');
  } finally { h.done(); }
});

test('presence rule is unchanged: a page not touched for five minutes sends no ping', async (t) => {
  const h = setup(t);
  try {
    const stop = h.mod.startUpdateWatch();
    await h.tick(5 * MIN - 1000);
    const pings = h.sent.presence;
    assert.ok(pings >= 6, 'pinged while the page was fresh: ' + pings);
    await h.tick(3 * MIN);
    assert.equal(h.sent.presence, pings, 'no ping after five minutes without a touch');
    h.touch();
    await h.tick(45_000);
    assert.equal(h.sent.presence, pings + 1, 'a touch brings the pings back within one interval');
    stop();
  } finally { h.done(); }
});

test('hidden: no presence ping and no status read; on show the pings come back at once and the status is current', async (t) => {
  const h = setup(t);
  try {
    const stop = h.mod.startUpdateWatch();
    await h.tick(0);
    h.doc.set('hidden');
    const before = { ...h.sent };
    await h.tick(30 * MIN);
    assert.deepEqual(h.sent, before, 'nothing sent in 30 minutes hidden');
    h.touch(); // the person comes back: the touch is recent only if it happened within five minutes; this one is new
    h.doc.set('visible');
    await h.tick(0);
    assert.equal(h.sent.presence, before.presence + 1, 'a ping at once on show');
    assert.equal(h.sent.status, before.status + 1, 'one status read at once on show (a full interval has passed)');
    stop();
  } finally { h.done(); }
});

test('hidden for under a minute with a recent touch: the first ping after show is on time, no extra requests while away', async (t) => {
  const h = setup(t);
  try {
    const stop = h.mod.startUpdateWatch();
    await h.tick(0);
    assert.equal(h.sent.presence, 1);
    h.doc.set('hidden');
    await h.tick(20_000);
    h.doc.set('visible');
    await h.tick(0);
    assert.equal(h.sent.presence, 1, 'only 20 s since the last ping: it waits for the rest of the 45 s');
    assert.equal(h.sent.status, 1, 'and the status read is not repeated on every tab switch');
    await h.tick(25_000);
    assert.equal(h.sent.presence, 2);
    stop();
  } finally { h.done(); }
});

test('the app counts as in use at most 45 s plus the two-minute window after the last ping: hidden stops pings, visible keeps them inside that gap', async (t) => {
  const h = setup(t);
  try {
    const stop = h.mod.startUpdateWatch();
    const pingTimes = [];
    let last = h.sent.presence;
    const start = Date.now();
    for (let s = 0; s < 600; s++) {
      if (s === 300) h.doc.set('hidden');
      h.touch();
      await h.tick(1000);
      if (h.sent.presence > last) { pingTimes.push(Date.now() - start); last = h.sent.presence; }
    }
    const visibleGaps = pingTimes.filter((x) => x <= 300_000).map((x, i, a) => (i ? x - a[i - 1] : 0)).slice(1);
    assert.ok(visibleGaps.every((gap) => gap <= 46_000), 'visible gaps are 45 s: ' + visibleGaps.join(','));
    assert.ok(pingTimes.every((x) => x <= 300_000), 'no ping after the page was hidden at 300 s: ' + pingTimes.join(','));
    stop();
  } finally { h.done(); }
});

test('download poll: a read a second while visible, none while hidden, one at once on show, and it ends by itself', async (t) => {
  const h = setup(t);
  try {
    h.setInstall('downloading');
    await h.mod.refreshUpdateStatus();
    assert.equal(h.sent.status, 1);
    await h.tick(3000);
    assert.equal(h.sent.status, 4, 'once a second while visible');
    h.doc.set('hidden');
    await h.tick(10 * MIN);
    assert.equal(h.sent.status, 4, 'nothing while hidden');
    h.doc.set('visible');
    await h.tick(0);
    assert.equal(h.sent.status, 5, 'one read at once on show');
    h.setInstall('ready');
    await h.tick(1000);
    assert.equal(h.sent.status, 6);
    await h.tick(10_000);
    assert.equal(h.sent.status, 6, 'the poll ended when the download was done');
  } finally { h.done(); }
});
