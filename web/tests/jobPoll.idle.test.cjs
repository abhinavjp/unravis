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
