const assert = require('node:assert/strict');
const test = require('node:test');
const { loadTypeScript } = require('./helpers/loadTs.cjs');

// BL-429: the wait on a scan the person started (discover.ts waitForScan) asks every 500 ms while visible, every 5 s while hidden,
// and asks at once when the page is shown again. The scan itself runs at the server either way.

const flush = () => new Promise((resolve) => setImmediate(resolve));

test('waitForScan: 500 ms visible, 5 s hidden, at once on show', async (t) => {
  const handlers = new Set();
  const doc = {
    visibilityState: 'visible',
    addEventListener: (type, fn) => handlers.add(fn),
    removeEventListener: (type, fn) => handlers.delete(fn),
    set(state) { doc.visibilityState = state; [...handlers].forEach((fn) => fn()); }
  };
  globalThis.document = doc;
  t.mock.timers.enable({ apis: ['setTimeout', 'Date'], now: 1_000_000 });
  try {
    const { waitForScan } = loadTypeScript('src/lib/discover.ts');
    let asked = 0;
    let state = 'running';
    const client = { getScan: async () => { asked++; return { id: 's', state }; } };
    const waiting = waitForScan(client, 's');
    const tick = async (ms) => { let left = ms; do { const step = Math.min(left, 100); t.mock.timers.tick(step); await flush(); left -= step; } while (left > 0); };
    await flush();
    assert.equal(asked, 1);
    await tick(500); assert.equal(asked, 2, 'visible: every 500 ms');
    doc.set('hidden');
    await tick(500); assert.equal(asked, 3, 'the wait already running ends on time');
    await tick(4900); assert.equal(asked, 3, 'hidden: slowed');
    await tick(100); assert.equal(asked, 4, 'hidden: every 5 s');
    await tick(2000);
    doc.set('visible');
    await flush();
    assert.equal(asked, 5, 'shown: asks at once');
    state = 'done';
    await tick(500);
    const scan = await waiting;
    assert.equal(scan.state, 'done');
    assert.equal(handlers.size, 0, 'nothing is left listening');
  } finally { t.mock.timers.reset(); delete globalThis.document; }
});

// ---- the real import and check job waits (cases.ts) and the drawer wait pattern, across hide/show cycles ----

const CASE = 'abcdefghijklmnopqrstuvwxy2';
const JOB = '0123456789abcdef0123456789abcdef';

async function withPage(t, fn) {
  const handlers = new Set();
  const doc = {
    visibilityState: 'visible',
    addEventListener: (type, fn2) => { if (type === 'visibilitychange') handlers.add(fn2); },
    removeEventListener: (type, fn2) => { if (type === 'visibilitychange') handlers.delete(fn2); },
    set(state) { doc.visibilityState = state; [...handlers].forEach((h) => h()); },
    handlers
  };
  const names = ['document', 'location', 'history', 'sessionStorage', 'fetch'];
  const saved = Object.fromEntries(names.map((name) => [name, Object.getOwnPropertyDescriptor(globalThis, name)]));
  const answers = { seen: 0, doneAfter: Infinity, kind: 'import' };
  const values = {
    document: doc,
    location: { hash: '', search: '', pathname: '/' },
    history: { state: null, replaceState: () => {} },
    sessionStorage: { getItem: (k) => (k === 'unravis.session' ? 'token-0123456789abcdef' : null), setItem() {}, removeItem() {} },
    fetch: async () => {
      answers.seen++;
      const done = answers.seen >= answers.doneAfter;
      const body = { id: JOB, kind: answers.kind, state: done ? 'done' : 'running', bytesRead: 1, bytesTotal: 2 };
      if (done) body.result = answers.kind === 'check' ? { files: [], checkedAt: 'now' } : {};
      return { ok: true, status: 200, json: async () => body, headers: { get: () => null } };
    }
  };
  for (const name of names) Object.defineProperty(globalThis, name, { configurable: true, writable: true, value: values[name] });
  t.mock.timers.enable({ apis: ['setTimeout', 'Date'], now: 1_000_000 });
  const tick = async (ms) => { let left = ms; do { const step = Math.min(left, 100); t.mock.timers.tick(step); await flush(); left -= step; } while (left > 0); };
  try { return await fn({ doc, answers, tick, cases: loadTypeScript('src/lib/cases.ts') }); } finally {
    t.mock.timers.reset();
    for (const name of names) { if (saved[name]) Object.defineProperty(globalThis, name, saved[name]); else delete globalThis[name]; }
  }
}

for (const kind of ['import', 'check']) {
  test('wait for a ' + kind + ' job: slow while hidden, over several hide/show cycles, the result shows as soon as the page is shown, no double timers', async (t) => {
    await withPage(t, async ({ doc, answers, tick, cases }) => {
      answers.kind = kind;
      const first = { id: JOB, state: 'running', kind, bytesRead: 0, bytesTotal: 0, answer: null, checkReport: null, failure: null };
      const wait = kind === 'check' ? cases.waitForCheckJob : cases.waitForImportJob;
      let finished = false;
      const waiting = wait(CASE, first).then(() => { finished = true; });
      await tick(0);
      assert.equal(answers.seen, 0, 'the first ask comes after one wait');
      await tick(1000); assert.equal(answers.seen, 1, 'visible: one ask a second');
      // Three hide/show cycles with different lengths. Hidden: at most one ask per 5 s; shown: an ask at once, never two.
      for (const hiddenMs of [2000, 12_000, 600]) {
        doc.set('hidden');
        const before = answers.seen;
        await tick(hiddenMs);
        // The wait that was already running ends on time (one ask when it is under 1 s from done); after that one ask every 5 s.
        assert.equal(answers.seen - before, { 2000: 1, 12_000: 3, 600: 0 }[hiddenMs], 'asks while hidden for ' + hiddenMs + ' ms');
        const atShow = answers.seen;
        doc.set('visible');
        await tick(0);
        assert.equal(answers.seen, atShow + 1, 'one ask at once on show, not two');
        await tick(1000);
        assert.equal(answers.seen, atShow + 2, 'then back to one a second');
        assert.equal(doc.handlers.size, 1, 'exactly one waiter listening');
      }
      // Done while hidden: the page sees it as soon as it is shown.
      doc.set('hidden');
      answers.doneAfter = answers.seen + 1;
      await tick(5000);
      assert.equal(answers.seen, answers.doneAfter, 'the hidden wait asked once more and saw it done');
      await tick(0);
      await waiting;
      assert.equal(finished, true);
      assert.equal(doc.handlers.size, 0, 'no listener left behind');
    });
  });
}

test('wait for an import job: the result that arrives while hidden is read at once when the page is shown', async (t) => {
  await withPage(t, async ({ doc, answers, tick, cases }) => {
    const first = { id: JOB, state: 'running', kind: 'import', bytesRead: 0, bytesTotal: 0, answer: null, checkReport: null, failure: null };
    let finished = false;
    doc.set('hidden'); // the page is hidden when the wait begins
    const waiting = cases.waitForImportJob(CASE, first).then(() => { finished = true; });
    await tick(0);
    answers.doneAfter = 1; // done at the server now
    await tick(1000);
    assert.equal(finished, false, 'hidden: the wait is slowed, so it has not looked yet');
    doc.set('visible');
    await tick(0);
    await waiting;
    assert.equal(finished, true, 'shown: it looked at once and the result is in');
    assert.equal(doc.handlers.size, 0);
  });
});

// The three screens that wait on a job of their own (ArchiveDrawer, FreeDrawer, the archive follow in CaseHome) all do the same thing:
// cancel the pending timer, then arm a new pacedTimeout whose callback asks and re-arms while the job runs. This is that loop with the
// real pacedTimeout, driven through hide/show cycles; the components only call it.
test('the drawer wait pattern (cancel, re-arm with pacedTimeout): one timer at a time through hide/show cycles, and a clean end', async (t) => {
  await withPage(t, async ({ doc, tick }) => {
    const { pacedTimeout } = loadTypeScript('src/lib/visibility.ts');
    let asked = 0, running = true, alive = true, cancelTimer;
    function schedule() { cancelTimer?.(); if (alive) cancelTimer = pacedTimeout(() => void poll(), 1000); }
    async function poll() { if (!alive) return; asked++; await Promise.resolve(); if (running) schedule(); }
    schedule();
    await tick(1000); assert.equal(asked, 1);
    for (let cycle = 0; cycle < 4; cycle++) {
      doc.set('hidden');
      await tick(1500);
      doc.set('hidden'); // a second event changes nothing
      doc.set('visible');
      await tick(0);
      assert.equal(doc.handlers.size, 1, 'one timer waiting after cycle ' + cycle);
      await tick(1000);
    }
    const before = asked;
    await tick(10_000);
    assert.ok(asked - before >= 9 && asked - before <= 10, 'back to one ask a second: ' + (asked - before));
    // The screen closes in the middle of a wait.
    alive = false; cancelTimer?.();
    assert.equal(doc.handlers.size, 0);
    const stopped = asked;
    await tick(20_000);
    assert.equal(asked, stopped);
    // The job ends while the page is hidden: one more ask happens, then nothing is armed.
    alive = true; running = true; schedule();
    doc.set('hidden');
    await tick(5000);
    running = false;
    const hiddenAsks = asked;
    doc.set('visible'); await tick(0);
    assert.equal(asked, hiddenAsks + 1, 'shown: one ask at once, and it finds the job over');
    await tick(10_000);
    assert.equal(asked, hiddenAsks + 1, 'and no timer is armed after that');
    assert.equal(doc.handlers.size, 0, 'nothing listening once the job is over');
  });
});
