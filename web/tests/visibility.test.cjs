const assert = require('node:assert/strict');
const test = require('node:test');
const { loadTypeScript } = require('./helpers/loadTs.cjs');

// BL-429: the page timers stop (visiblePoll) or slow down (pacedTimeout, pacedSleep) while the page is hidden, and start again at once on show.
// The page is a fake document with a visibilityState and visibilitychange listeners; time is the node test clock.

const v = loadTypeScript('src/lib/visibility.ts');

function fakeDocument() {
  const listeners = new Set();
  const attrs = new Set();
  const doc = {
    visibilityState: 'visible',
    documentElement: { toggleAttribute: (name, on) => { if (on) attrs.add(name); else attrs.delete(name); } },
    addEventListener: (type, fn) => { if (type === 'visibilitychange') listeners.add(fn); },
    removeEventListener: (type, fn) => { if (type === 'visibilitychange') listeners.delete(fn); },
    listenerCount: () => listeners.size,
    attrs,
    set(state) { doc.visibilityState = state; for (const fn of [...listeners]) fn(); }
  };
  return doc;
}

const flush = () => new Promise((resolve) => setImmediate(resolve));

function withClock(t, body) {
  return async () => {
    const doc = fakeDocument();
    globalThis.document = doc;
    t.mock.timers.enable({ apis: ['setTimeout', 'Date'], now: 1_000_000 });
    // Time moves in small steps with a pause for promises between them, because a poll arms its next wait after its run has answered.
    const tick = async (ms) => { let left = ms; do { const step = Math.min(left, 250); t.mock.timers.tick(step); await flush(); left -= step; } while (left > 0); };
    try { await body(doc, tick); } finally { t.mock.timers.reset(); delete globalThis.document; }
  };
}

test('isHidden: anything but "visible" is hidden, and no document counts as visible', () => {
  delete globalThis.document;
  assert.equal(v.isHidden(), false);
  globalThis.document = { visibilityState: 'visible' };
  assert.equal(v.isHidden(), false);
  globalThis.document = { visibilityState: 'hidden' };
  assert.equal(v.isHidden(), true);
  globalThis.document = { visibilityState: 'prerender' };
  assert.equal(v.isHidden(), true);
  delete globalThis.document;
});

test('visiblePoll runs every interval while visible', (t) => withClock(t, async (doc, tick) => {
  let runs = 0;
  const stop = v.visiblePoll(() => { runs++; }, 1000);
  await tick(999); assert.equal(runs, 0);
  await tick(1); assert.equal(runs, 1);
  await tick(3000); assert.equal(runs, 4);
  stop();
  await tick(5000); assert.equal(runs, 4, 'stopped');
  assert.equal(doc.listenerCount(), 0, 'no listener is left behind');
})());

test('visiblePoll: immediate runs once at once, then every interval', (t) => withClock(t, async (doc, tick) => {
  let runs = 0;
  const stop = v.visiblePoll(() => { runs++; }, 45_000, { immediate: true });
  await tick(0); assert.equal(runs, 1);
  await tick(45_000); assert.equal(runs, 2);
  stop();
})());

test('visiblePoll: a hidden page runs nothing, and on show a due run happens at once', (t) => withClock(t, async (doc, tick) => {
  let runs = 0;
  const stop = v.visiblePoll(() => { runs++; }, 1000);
  await tick(1000); assert.equal(runs, 1);
  doc.set('hidden');
  await tick(60_000); assert.equal(runs, 1, 'nothing while hidden');
  doc.set('visible');
  await tick(0); assert.equal(runs, 2, 'one run at once on show');
  await tick(1000); assert.equal(runs, 3, 'then the normal rhythm');
  stop();
})());

test('visiblePoll: hidden for less than an interval, the wait goes on for what was left', (t) => withClock(t, async (doc, tick) => {
  let runs = 0;
  const stop = v.visiblePoll(() => { runs++; }, 10_000);
  await tick(4000);
  doc.set('hidden');
  await tick(1000);
  doc.set('visible');
  await tick(0); assert.equal(runs, 0, 'not due yet');
  await tick(4999); assert.equal(runs, 0);
  await tick(1); assert.equal(runs, 1, 'due 10 s after the start');
  stop();
})());

test('visiblePoll: starts hidden, runs nothing until shown', (t) => withClock(t, async (doc, tick) => {
  doc.visibilityState = 'hidden';
  let runs = 0;
  const stop = v.visiblePoll(() => { runs++; }, 1000, { immediate: true });
  await tick(10_000); assert.equal(runs, 0);
  doc.set('visible');
  await tick(0); assert.equal(runs, 1);
  stop();
})());

test('visiblePoll: a run that answers false ends the poll, and a slow run is never overlapped', (t) => withClock(t, async (doc, tick) => {
  let runs = 0;
  v.visiblePoll(() => { runs++; return runs < 3; }, 1000);
  await tick(10_000);
  assert.equal(runs, 3);
  assert.equal(doc.listenerCount(), 0, 'it let go of the page by itself');

  let started = 0;
  let release;
  const stop = v.visiblePoll(() => { started++; return new Promise((resolve) => { release = resolve; }); }, 1000);
  await tick(1000); assert.equal(started, 1);
  await tick(5000); assert.equal(started, 1, 'the next run waits for the answer');
  release(); await flush();
  await tick(1000); assert.equal(started, 2);
  stop();
})());

test('visiblePoll: a run that throws does not end the poll', (t) => withClock(t, async (doc, tick) => {
  let runs = 0;
  const stop = v.visiblePoll(() => { runs++; throw new Error('once'); }, 1000);
  await tick(3000); assert.equal(runs, 3);
  stop();
})());

test('onVisible calls start now when visible, stop on hide, start again on show, and stops on unsubscribe', (t) => withClock(t, async (doc) => {
  const log = [];
  const off = v.onVisible(() => log.push('start'), () => log.push('stop'));
  assert.deepEqual(log, ['start']);
  doc.set('visible'); assert.deepEqual(log, ['start'], 'no second start');
  doc.set('hidden'); doc.set('hidden');
  assert.deepEqual(log, ['start', 'stop']);
  doc.set('visible');
  assert.deepEqual(log, ['start', 'stop', 'start']);
  off();
  assert.deepEqual(log, ['start', 'stop', 'start', 'stop']);
  assert.equal(doc.listenerCount(), 0);
})());

test('pacedTimeout: visible waits ms; hidden waits at least the slow time; show wakes it at once', (t) => withClock(t, async (doc, tick) => {
  let fired = 0;
  v.pacedTimeout(() => { fired++; }, 1000, 5000);
  await tick(1000); assert.equal(fired, 1, 'visible: the normal wait');

  doc.set('hidden');
  v.pacedTimeout(() => { fired++; }, 1000, 5000);
  await tick(4999); assert.equal(fired, 1, 'hidden: slowed');
  await tick(1); assert.equal(fired, 2);

  v.pacedTimeout(() => { fired++; }, 1000, 5000);
  await tick(2000);
  doc.set('visible');
  assert.equal(fired, 3, 'shown: runs at once');
  await tick(10_000); assert.equal(fired, 3, 'and only once');
  assert.equal(doc.listenerCount(), 0);
})());

test('pacedTimeout: hidden after it was set, it still fires on time and a later show does nothing extra', (t) => withClock(t, async (doc, tick) => {
  let fired = 0;
  v.pacedTimeout(() => { fired++; }, 1000, 5000);
  doc.set('hidden');
  await tick(1000); assert.equal(fired, 1);
  doc.set('visible'); assert.equal(fired, 1);
})());

test('pacedTimeout: cancel stops it and lets go of the page', (t) => withClock(t, async (doc, tick) => {
  let fired = 0;
  const cancel = v.pacedTimeout(() => { fired++; }, 1000, 5000);
  cancel();
  await tick(10_000); assert.equal(fired, 0);
  assert.equal(doc.listenerCount(), 0);
})());

test('pacedSleep: resolves slowly while hidden, at once on show, and rejects on abort', (t) => withClock(t, async (doc, tick) => {
  doc.visibilityState = 'hidden';
  let done = false;
  const sleeping = v.pacedSleep(1000).then(() => { done = true; });
  await tick(4000); assert.equal(done, false);
  doc.set('visible'); await sleeping; assert.equal(done, true);

  const ctl = new AbortController();
  const aborted = v.pacedSleep(1000, ctl.signal);
  ctl.abort();
  await assert.rejects(aborted, (error) => error.name === 'AbortError');
  await assert.rejects(v.pacedSleep(1000, ctl.signal), (error) => error.name === 'AbortError');
  assert.equal(doc.listenerCount(), 0);
})());

test('markPageVisibility keeps data-page-hidden on the root in step with the page', (t) => withClock(t, async (doc) => {
  v.markPageVisibility();
  assert.equal(doc.attrs.has('data-page-hidden'), false);
  doc.set('hidden');
  assert.equal(doc.attrs.has('data-page-hidden'), true);
  doc.set('visible');
  assert.equal(doc.attrs.has('data-page-hidden'), false);
})());
