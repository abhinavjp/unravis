const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

// FIX-35 / TEST-04 (b): overlapping window changes on the Slow call screen controller.
// Behaviour tests on the real createCallController with fake reads that the test answers by hand, in the order it wants.
// The point of every test: change the window while the reads of the earlier window are still out, answer the NEWER window
// first (or the older one late, or with an error), and check that the screen only ever holds the newest window's answers.

const root = path.resolve(__dirname, '..');
function loadTypeScript(relativePath) {
  const filename = path.join(root, relativePath);
  const source = fs.readFileSync(filename, 'utf8');
  const output = ts.transpileModule(source, {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022, esModuleInterop: true }
  }).outputText;
  const module = { exports: {} };
  const nodeRequire = createRequire(filename);
  const load = (request) => /^\.\.?\//.test(request) && fs.existsSync(path.join(path.dirname(filename), request + '.ts'))
    ? loadTypeScript(path.relative(root, path.join(path.dirname(filename), request + '.ts')))
    : nodeRequire(request);
  new Function('require', 'module', 'exports', output)(load, module, module.exports);
  return module.exports;
}

const { createCallController } = loadTypeScript('src/lib/drill/callController.ts');

const CALL_START = Date.parse('2026-11-12T10:00:00.000Z');
const tick = () => new Promise((r) => setImmediate(r));
const deferred = () => {
  let resolve, reject;
  const promise = new Promise((res, rej) => { resolve = res; reject = rej; });
  return { promise, resolve, reject };
};
const page = (ids, extra) => ({ rows: ids.map((id) => ({ id, sourceId: 's1' })), nextCursor: '', sources: [], truncated: false, ...(extra || {}) });
const answer = (tag) => ({ tag });
const eventsOf = (...ids) => ({ events: ids.map((evidenceId) => ({ evidenceId })), truncated: false });
const W = (minutes) => ({ before: minutes, after: minutes, shiftMs: 0 });

function makeRecord() {
  return {
    id: 'r1', sourceId: 's1', controller: 'Orders', action: 'Get', kind: 'http', outcome: 'ok', state: 'valid',
    status: '200', url: '/x', initiatedAt: new Date(CALL_START).toISOString(), completedAt: new Date(CALL_START + 5000).toISOString(),
    durationMs: 5000, apiTimeMs: 4000, auditCreateTimeMs: 100, payloadRecordTimeMs: 10, requestKB: 1, responseKB: 1,
    userId: 7, payload: { request: '', response: '', truncated: false }
  };
}

function makeDeps() {
  const calls = { slowest: [], record: [], tracing: [], activity: [], events: [] };
  const mk = (name) => (...args) => {
    const d = deferred();
    calls[name].push({ args, d, signal: args[args.length - 1] });
    return d.promise;
  };
  return { calls, deps: { slowest: mk('slowest'), record: mk('record'), tracing: mk('tracing'), activity: mk('activity'), events: mk('events') } };
}

// A ready controller on the default +-15 minute window with every read still out. Indexes after this:
// tracing[0] overview, tracing[1] window; slowest[0] find, slowest[1] window rows; activity[0]; events[0].
async function ready() {
  const { calls, deps } = makeDeps();
  const changes = [];
  const ctl = createCallController(
    { caseId: 'abcdefghijklmnopqrstuvwxyz', slot: 3, start: '2026-11-12T09:59:00.000Z', end: '2026-11-12T10:01:00.000Z', sourceIds: ['s1'] },
    deps,
    (s) => changes.push(s)
  );
  ctl.start();
  calls.slowest[0].d.resolve(page(['r1']));
  await tick();
  calls.record[0].d.resolve(makeRecord());
  await tick();
  return { calls, ctl, changes };
}

// Window 30 minutes, then window 45 minutes while the 30 minute reads are still out. Returns the indexes of each pair of reads.
function twoChanges(ctl) {
  ctl.commit(W(30)); // older: tracing[2], slowest[2], activity[1], events[1]
  ctl.commit(W(45)); // newer: tracing[3], slowest[3], activity[2], events[2]
}

test('the older window answers LATE, after the newer one: the newer answers stay on screen', async () => {
  const { calls, ctl } = await ready();
  twoChanges(ctl);
  // Older reads were aborted by the newer commit.
  for (const list of [calls.tracing.slice(1, 3), calls.slowest.slice(1, 3), calls.activity.slice(0, 2), calls.events.slice(0, 2)]) {
    for (const c of list) assert.equal(c.signal.aborted, true, 'earlier window read is aborted');
  }
  // Newer first.
  calls.tracing[3].d.resolve(answer('w45'));
  calls.slowest[3].d.resolve(page(['new-slow']));
  calls.activity[2].d.resolve(page(['new-near']));
  calls.events[2].d.resolve(eventsOf('new-ev'));
  await tick();
  // Then the older window's answers arrive late (a fake that ignores abort, like a real response already in flight).
  calls.tracing[2].d.resolve(answer('w30'));
  calls.slowest[2].d.resolve(page(['old-slow']));
  calls.activity[1].d.resolve(page(['old-near']));
  calls.events[1].d.resolve(eventsOf('old-ev'));
  await tick();
  const s = ctl.state();
  assert.deepEqual(s.window, W(45), 'the window on screen is the newest one');
  assert.equal(s.visible.tag, 'w45');
  assert.deepEqual(s.slowRows.map((r) => r.id), ['new-slow']);
  assert.deepEqual(s.nearby.rows.map((r) => r.id), ['new-near']);
  assert.deepEqual(s.events.map((e) => e.evidenceId), ['new-ev']);
  assert.equal(s.visibleLoading, false);
  assert.equal(s.slowRowsLoading, false);
  assert.equal(s.nearby.asking, false);
});

test('the older window answers BEFORE the newer one: it is dropped and the screen keeps loading for the newer one', async () => {
  const { calls, ctl } = await ready();
  twoChanges(ctl);
  calls.tracing[2].d.resolve(answer('w30'));
  calls.slowest[2].d.resolve(page(['old-slow']));
  calls.activity[1].d.resolve(page(['old-near']));
  calls.events[1].d.resolve(eventsOf('old-ev'));
  await tick();
  let s = ctl.state();
  assert.equal(s.visible, null, 'no answer for the old window is shown under the new axis');
  assert.equal(s.visibleLoading, true);
  assert.deepEqual(s.slowRows, []);
  assert.equal(s.slowRowsLoading, true);
  assert.deepEqual(s.nearby.rows, []);
  assert.equal(s.nearby.asking, true);
  assert.deepEqual(s.events, []);
  calls.tracing[3].d.resolve(answer('w45'));
  calls.slowest[3].d.resolve(page(['new-slow']));
  await tick();
  s = ctl.state();
  assert.equal(s.visible.tag, 'w45');
  assert.deepEqual(s.slowRows.map((r) => r.id), ['new-slow']);
  assert.equal(s.slowRowsLoading, false);
});

test('the older window FAILS late: it sets no failure flag and clears nothing the newer window already loaded', async () => {
  const { calls, ctl } = await ready();
  twoChanges(ctl);
  calls.tracing[3].d.resolve(answer('w45'));
  calls.slowest[3].d.resolve(page(['new-slow']));
  calls.activity[2].d.resolve(page(['new-near']));
  calls.events[2].d.resolve(eventsOf('new-ev'));
  await tick();
  calls.tracing[2].d.reject(new Error('old down'));
  calls.slowest[2].d.reject(new Error('old down'));
  calls.activity[1].d.reject(new Error('old down'));
  calls.events[1].d.reject(new Error('old down'));
  await tick();
  const s = ctl.state();
  assert.equal(s.visibleFailed, false);
  assert.equal(s.slowRowsFailed, false);
  assert.equal(s.nearby.failed, false);
  assert.equal(s.visible.tag, 'w45');
  assert.deepEqual(s.slowRows.map((r) => r.id), ['new-slow']);
  assert.deepEqual(s.nearby.rows.map((r) => r.id), ['new-near']);
  assert.deepEqual(s.events.map((e) => e.evidenceId), ['new-ev'], 'an old events failure does not wipe the new markers');
});

test('three changes in a row, answered 2nd, 3rd, 1st: only the 3rd shows', async () => {
  const { calls, ctl } = await ready();
  ctl.commit(W(20)); // tracing[2], slowest[2]
  ctl.commit(W(30)); // tracing[3], slowest[3]
  ctl.commit(W(40)); // tracing[4], slowest[4]
  calls.slowest[3].d.resolve(page(['second']));
  calls.slowest[4].d.resolve(page(['third']));
  calls.slowest[2].d.resolve(page(['first']));
  calls.tracing[3].d.resolve(answer('second'));
  calls.tracing[4].d.resolve(answer('third'));
  calls.tracing[2].d.resolve(answer('first'));
  await tick();
  const s = ctl.state();
  assert.deepEqual(s.window, W(40));
  assert.deepEqual(s.slowRows.map((r) => r.id), ['third']);
  assert.equal(s.visible.tag, 'third');
});

test('changing the window mid-load starts a read for the new window only once per commit (no duplicate reads)', async () => {
  const { calls, ctl } = await ready();
  const before = { t: calls.tracing.length, s: calls.slowest.length, a: calls.activity.length, e: calls.events.length };
  twoChanges(ctl);
  assert.equal(calls.tracing.length, before.t + 2);
  assert.equal(calls.slowest.length, before.s + 2);
  assert.equal(calls.activity.length, before.a + 2);
  assert.equal(calls.events.length, before.e + 2);
  // The read asks for the window that was chosen, not the one before it.
  const asked = (c) => c.args;
  const [, startIso, endIso] = asked(calls.tracing[3]);
  assert.equal(Date.parse(endIso) - Date.parse(startIso), 90 * 60000, 'the newest read spans 45 minutes each side');
});

test('preview mid-load moves the window only: no read starts and the in-flight answers still land', async () => {
  const { calls, ctl } = await ready();
  const n = calls.tracing.length + calls.slowest.length + calls.activity.length + calls.events.length;
  ctl.preview(W(40));
  ctl.preview(W(50));
  assert.equal(calls.tracing.length + calls.slowest.length + calls.activity.length + calls.events.length, n);
  assert.deepEqual(ctl.state().window, W(50));
  // The 15 minute reads are still current (no commit happened), so their answers are kept.
  calls.slowest[1].d.resolve(page(['kept']));
  calls.tracing[1].d.resolve(answer('w15'));
  await tick();
  assert.deepEqual(ctl.state().slowRows.map((r) => r.id), ['kept']);
  assert.equal(ctl.state().visible.tag, 'w15');
  // Letting go commits the final window.
  ctl.commit(W(50));
  assert.equal(calls.tracing.length, 3);
});

test('the window leaves +-60 while the strip is still loading: the late strip answer does not become the visible window', async () => {
  const { calls, ctl } = await ready();
  ctl.commit(W(60)); // covers the strip: waits for the overview answer
  assert.equal(ctl.state().visibleLoading, true);
  ctl.commit(W(30)); // own read; tracing[2]
  assert.equal(calls.tracing.length, 3);
  calls.tracing[0].d.resolve(answer('overview'));
  await tick();
  let s = ctl.state();
  assert.equal(s.overview.tag, 'overview', 'the strip itself is kept');
  assert.equal(s.visible, null, 'but the 30 minute window is not drawn from the 120 minute strip');
  assert.equal(s.visibleLoading, true);
  calls.tracing[2].d.resolve(answer('w30'));
  await tick();
  s = ctl.state();
  assert.equal(s.visible.tag, 'w30');
  assert.equal(s.visibleLoading, false);
});

test('the window goes to +-60 while a 15 minute read is out: the late 15 minute answer does not replace the strip', async () => {
  const { calls, ctl } = await ready();
  calls.tracing[0].d.resolve(answer('overview'));
  await tick();
  ctl.commit(W(60)); // takes the strip answer at once
  assert.equal(ctl.state().visible.tag, 'overview');
  calls.tracing[1].d.resolve(answer('w15-late'));
  await tick();
  assert.equal(ctl.state().visible.tag, 'overview');
  assert.equal(ctl.state().visibleLoading, false);
});

test('after dispose mid-load, no answer of any window reaches the screen', async () => {
  const { calls, ctl, changes } = await ready();
  twoChanges(ctl);
  ctl.dispose();
  const seen = changes.length;
  calls.tracing[3].d.resolve(answer('w45'));
  calls.slowest[3].d.resolve(page(['x']));
  calls.events[2].d.resolve(eventsOf('x'));
  await tick();
  assert.equal(changes.length, seen);
});
