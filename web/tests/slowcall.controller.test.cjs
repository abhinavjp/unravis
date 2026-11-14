const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

// Slow call controller: every read in one place, tested with fake dependencies resolved by hand.

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
const { DEFAULT_WINDOW } = loadTypeScript('src/lib/drill/callWindow.ts');

const CASE = 'abcdefghijklmnopqrstuvwxyz'.slice(0, 26);
const CALL_START = Date.parse('2026-11-12T10:00:00.000Z');
const MIN = 60000;

function deferred() {
  let resolve, reject;
  const promise = new Promise((res, rej) => { resolve = res; reject = rej; });
  return { promise, resolve, reject };
}
const tick = () => new Promise((r) => setImmediate(r));

function makeTarget(extra) {
  return {
    caseId: CASE, slot: 3, start: '2026-11-12T09:59:00.000Z', end: '2026-11-12T10:01:00.000Z',
    sourceIds: ['s1'], ...(extra || {})
  };
}

function makeRecord(extra) {
  return {
    id: 'r1', sourceId: 's1', controller: 'Orders', action: 'Get', kind: 'http', outcome: 'ok', state: 'valid',
    status: '200', url: '/x', initiatedAt: new Date(CALL_START).toISOString(), completedAt: new Date(CALL_START + 5000).toISOString(),
    durationMs: 5000, apiTimeMs: 4000, auditCreateTimeMs: 100, payloadRecordTimeMs: 10, requestKB: 1, responseKB: 1,
    userId: 7, payload: { request: '', response: '', truncated: false }, ...(extra || {})
  };
}

const page = (rows, extra) => ({ rows: rows || [], nextCursor: '', sources: [], truncated: false, ...(extra || {}) });
const tracingAnswer = (tag) => ({ tag });

// Fake deps: each call is recorded and gets a deferred the test resolves by hand.
function makeDeps() {
  const calls = { slowest: [], record: [], tracing: [], activity: [], events: [] };
  const mk = (name) => (...args) => {
    const d = deferred();
    const signal = args[args.length - 1];
    calls[name].push({ args, d, signal });
    return d.promise;
  };
  return { calls, deps: { slowest: mk('slowest'), record: mk('record'), tracing: mk('tracing'), activity: mk('activity'), events: mk('events') } };
}

// A ready controller: the call found, window reads started and not yet answered.
async function ready(targetExtra, recordExtra) {
  const { calls, deps } = makeDeps();
  const changes = [];
  const found = [];
  const ctl = createCallController(makeTarget(targetExtra), deps, (s) => changes.push(s), (c) => found.push(c));
  ctl.start();
  if (!(targetExtra && targetExtra.call)) {
    calls.slowest[0].d.resolve(page([{ id: 'r1', sourceId: 's1' }]));
    await tick();
  }
  calls.record[0].d.resolve(makeRecord(recordExtra));
  await tick();
  return { calls, deps, ctl, changes, found };
}

test('a target with a call skips the slowest search and reads the record directly', async () => {
  const { calls, ctl, found } = await ready({ call: { sourceId: 's1', id: 'r1' } });
  assert.equal(calls.slowest.length, 1, 'only the window slow-rows read, no search');
  assert.deepEqual(calls.record[0].args.slice(0, 2), ['s1', 'r1']);
  assert.equal(ctl.state().phase, 'ready');
  assert.deepEqual(found, [{ sourceId: 's1', id: 'r1' }]);
});

test('without a call the slot is searched, then the record is read and onFound fires', async () => {
  const { calls, ctl, found } = await ready();
  assert.equal(calls.slowest[0].args[0].start, '2026-11-12T09:59:00.000Z');
  assert.deepEqual(calls.record[0].args.slice(0, 2), ['s1', 'r1']);
  assert.deepEqual(found, [{ sourceId: 's1', id: 'r1' }]);
  const s = ctl.state();
  assert.equal(s.phase, 'ready');
  assert.equal(s.callStartMs, CALL_START);
  assert.equal(s.callEndMs, CALL_START + 5000);
  assert.equal(s.record.perf, null);
});

test('the phase is finding until the record arrives', async () => {
  const { calls, deps } = makeDeps();
  const ctl = createCallController(makeTarget(), deps, () => {});
  ctl.start();
  assert.equal(ctl.state().phase, 'finding');
  calls.slowest[0].d.resolve(page([{ id: 'r1', sourceId: 's1' }]));
  await tick();
  assert.equal(ctl.state().phase, 'finding');
});

test('no hit in the slot gives not-found and reads nothing else', async () => {
  const { calls, deps } = makeDeps();
  const ctl = createCallController(makeTarget(), deps, () => {});
  ctl.start();
  calls.slowest[0].d.resolve(page([]));
  await tick();
  assert.equal(ctl.state().phase, 'not-found');
  assert.equal(calls.record.length, 0);
  assert.equal(calls.tracing.length, 0);
});

test('a failed record read gives failed, and retry tries again', async () => {
  const { calls, deps } = makeDeps();
  const ctl = createCallController(makeTarget({ call: { sourceId: 's1', id: 'r1' } }), deps, () => {});
  ctl.start();
  calls.record[0].d.reject(new Error('down'));
  await tick();
  assert.equal(ctl.state().phase, 'failed');
  ctl.retry();
  assert.equal(ctl.state().phase, 'finding');
  assert.equal(calls.record.length, 2);
  calls.record[1].d.resolve(makeRecord());
  await tick();
  assert.equal(ctl.state().phase, 'ready');
});

test('once the call is known the overview, window, slow rows, nearby and events reads all start', async () => {
  const { calls } = await ready();
  assert.equal(calls.tracing.length, 2, 'overview and the 15 minute window');
  assert.equal(calls.slowest.length, 2, 'search plus window slow rows');
  assert.equal(calls.activity.length, 1);
  assert.equal(calls.events.length, 1);
  const [ov, win] = calls.tracing.map((c) => c.args);
  assert.equal(ov[1], new Date(CALL_START - 60 * MIN).toISOString());
  assert.equal(ov[2], new Date(CALL_START + 60 * MIN).toISOString());
  assert.equal(win[1], new Date(CALL_START - 15 * MIN).toISOString());
  assert.equal(win[2], new Date(CALL_START + 15 * MIN).toISOString());
  assert.deepEqual(win[3], ['s1']);
});

test('preview makes zero dependency calls but moves the window and tells the screen', async () => {
  const { calls, ctl, changes } = await ready();
  const count = () => ({ t: calls.tracing.length, s: calls.slowest.length, a: calls.activity.length, e: calls.events.length });
  const before = count();
  const seen = changes.length;
  ctl.preview({ before: 30, after: 30, shiftMs: 0 });
  ctl.preview({ before: 30, after: 30, shiftMs: 5 * MIN });
  assert.deepEqual(count(), before);
  assert.equal(ctl.state().window.shiftMs, 5 * MIN);
  assert.equal(changes.length, seen + 2);
});

test('two quick commits: the first visible answer is dropped, the second is kept', async () => {
  const { calls, ctl } = await ready();
  ctl.commit({ before: 10, after: 10, shiftMs: 0 });
  ctl.commit({ before: 30, after: 30, shiftMs: 0 });
  const visible = calls.tracing.slice(2); // after the overview and the first window
  assert.equal(visible.length, 2);
  assert.equal(visible[0].signal.aborted, true);
  visible[0].d.resolve(tracingAnswer('first'));
  await tick();
  assert.equal(ctl.state().visible, null);
  visible[1].d.resolve(tracingAnswer('second'));
  await tick();
  assert.equal(ctl.state().visible.tag, 'second');
  assert.equal(ctl.state().visibleLoading, false);
});

test('a commit aborts the earlier slow rows, nearby and events reads and drops their answers', async () => {
  const { calls, ctl } = await ready();
  const oldSlow = calls.slowest[1];
  const oldNearby = calls.activity[0];
  const oldEvents = calls.events[0];
  ctl.commit({ before: 30, after: 30, shiftMs: 0 });
  assert.equal(oldSlow.signal.aborted, true);
  assert.equal(oldNearby.signal.aborted, true);
  assert.equal(oldEvents.signal.aborted, true);
  oldSlow.d.resolve(page([{ id: 'old' }]));
  oldNearby.d.resolve(page([{ id: 'old' }]));
  oldEvents.d.resolve({ events: [{ evidenceId: 'old' }], truncated: false });
  await tick();
  const s = ctl.state();
  assert.deepEqual(s.slowRows, []);
  assert.deepEqual(s.nearby.rows, []);
  assert.deepEqual(s.events, []);
});

test('plus or minus 60 minutes makes no second tracing call and reuses the overview answer', async () => {
  const { calls, ctl } = await ready();
  calls.tracing[0].d.resolve(tracingAnswer('overview'));
  calls.tracing[1].d.resolve(tracingAnswer('window15'));
  await tick();
  const before = calls.tracing.length;
  ctl.commit({ before: 60, after: 60, shiftMs: 0 });
  assert.equal(calls.tracing.length, before);
  assert.equal(ctl.state().visible.tag, 'overview');
  assert.equal(ctl.state().visibleLoading, false);
});

test('a 60 minute window chosen while the overview is still loading takes the overview answer when it lands', async () => {
  const { calls, ctl } = await ready();
  calls.tracing[1].d.resolve(tracingAnswer('window15'));
  await tick();
  ctl.commit({ before: 60, after: 60, shiftMs: 0 });
  assert.equal(ctl.state().visibleLoading, true);
  calls.tracing[0].d.resolve(tracingAnswer('overview'));
  await tick();
  assert.equal(ctl.state().visible.tag, 'overview');
  assert.equal(ctl.state().visibleLoading, false);
});

test('chip change with an untruncated wide everyone load makes no activity call, a truncated one does', async () => {
  const wide = { before: 30, after: 30, shiftMs: 0 };
  const a = await ready();
  a.ctl.commit(wide);
  a.calls.activity[1].d.resolve(page([{ id: 'x' }]));
  await tick();
  assert.equal(a.ctl.state().nearby.loaded.truncated, false);
  const n = a.calls.activity.length;
  a.ctl.setChip('user');
  a.ctl.setChip('api');
  assert.equal(a.calls.activity.length, n);

  const b = await ready();
  b.ctl.commit(wide);
  b.calls.activity[1].d.resolve(page([{ id: 'x' }], { truncated: true }));
  await tick();
  assert.equal(b.ctl.state().nearby.loaded.truncated, true);
  const m = b.calls.activity.length;
  b.ctl.setChip('user');
  assert.equal(b.calls.activity.length, m + 1);
  assert.equal(b.calls.activity[m].args[0].userId, 7);
  assert.equal(b.ctl.state().nearby.asking, true);
});

test('a chip change reads nothing but nearby calls', async () => {
  const { calls, ctl } = await ready();
  calls.activity[0].d.resolve(page([], { truncated: true }));
  await tick();
  const t = calls.tracing.length, s = calls.slowest.length, e = calls.events.length;
  ctl.setChip('api');
  assert.equal(calls.tracing.length, t);
  assert.equal(calls.slowest.length, s);
  assert.equal(calls.events.length, e);
  assert.equal(calls.activity.length, 2);
  assert.equal(calls.activity[1].args[0].operation, 'Orders.Get');
});

test('dispose then resolve: onChange is not called again and every read is aborted', async () => {
  const { calls, ctl, changes } = await ready();
  ctl.dispose();
  const seen = changes.length;
  for (const list of Object.values(calls)) for (const c of list) assert.equal(c.signal.aborted, true);
  calls.tracing[0].d.resolve(tracingAnswer('o'));
  calls.tracing[1].d.resolve(tracingAnswer('v'));
  calls.slowest[1].d.resolve(page([{ id: 'a' }]));
  calls.activity[0].d.resolve(page([]));
  calls.events[0].d.resolve({ events: [], truncated: false });
  await tick();
  ctl.preview({ before: 5, after: 5, shiftMs: 0 });
  ctl.commit({ before: 5, after: 5, shiftMs: 0 });
  assert.equal(changes.length, seen);
});

test('an events failure leaves the screen ready with the other reads kept', async () => {
  const { calls, ctl } = await ready();
  calls.events[0].d.reject(new Error('down'));
  calls.slowest[1].d.resolve(page([{ id: 'a', sourceId: 's1' }]));
  await tick();
  const s = ctl.state();
  assert.equal(s.phase, 'ready');
  assert.deepEqual(s.events, []);
  assert.equal(s.slowRows.length, 1);
});

test('an overview failure sets its flag, keeps the rest, and a covering window gets its own read', async () => {
  const { calls, ctl } = await ready();
  calls.tracing[0].d.reject(new Error('down'));
  calls.tracing[1].d.resolve(tracingAnswer('window15'));
  await tick();
  const s = ctl.state();
  assert.equal(s.phase, 'ready');
  assert.equal(s.overviewFailed, true);
  assert.equal(s.visible.tag, 'window15');
  const n = calls.tracing.length;
  ctl.commit({ before: 60, after: 60, shiftMs: 0 });
  assert.equal(calls.tracing.length, n + 1, 'the overview is not there to reuse');
});

test('a failed visible read sets its flag without failing the screen', async () => {
  const { calls, ctl } = await ready();
  calls.tracing[1].d.reject(new Error('down'));
  await tick();
  const s = ctl.state();
  assert.equal(s.phase, 'ready');
  assert.equal(s.visibleFailed, true);
  assert.equal(s.visibleLoading, false);
});

test('slow rows are capped at 50', async () => {
  const { calls, ctl } = await ready();
  calls.slowest[1].d.resolve(page(Array.from({ length: 80 }, (_, i) => ({ id: 'r' + i, sourceId: 's1' }))));
  await tick();
  assert.equal(ctl.state().slowRows.length, 50);
});

test('a failed slow-rows read sets slowRowsFailed; a later good read clears it; an aborted read does not set it', async () => {
  const { calls, ctl } = await ready();
  assert.equal(ctl.state().slowRowsFailed, false);
  calls.slowest[1].d.reject(new Error('down'));
  await tick();
  assert.equal(ctl.state().slowRowsFailed, true);
  assert.deepEqual(ctl.state().slowRows, []);
  ctl.commit({ before: 30, after: 30, shiftMs: 0 });
  assert.equal(ctl.state().slowRowsFailed, false, 'a new read starts clean');
  calls.slowest[2].d.resolve(page([{ id: 'a', sourceId: 's1' }]));
  await tick();
  assert.equal(ctl.state().slowRowsFailed, false);
  assert.equal(ctl.state().slowRows.length, 1);
  // An aborted read that then fails is not a failure of the current window.
  ctl.commit({ before: 10, after: 10, shiftMs: 0 });
  ctl.commit({ before: 5, after: 5, shiftMs: 0 });
  assert.equal(calls.slowest[3].signal.aborted, true);
  calls.slowest[3].d.reject(new Error('aborted'));
  await tick();
  assert.equal(ctl.state().slowRowsFailed, false);
});

test('the default window is 15 minutes each side and the chip starts on everyone', () => {
  const { deps } = makeDeps();
  const ctl = createCallController(makeTarget(), deps, () => {});
  assert.deepEqual(ctl.state().window, DEFAULT_WINDOW);
  assert.equal(ctl.state().chip, 'everyone');
});
