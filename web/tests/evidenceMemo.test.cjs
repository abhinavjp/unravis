const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

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

const memo = loadTypeScript('src/lib/evidenceMemo.ts');
const { lintCopy } = loadTypeScript('src/lib/copy/copylint.ts');
const { deck } = loadTypeScript('src/lib/copy/deck.ts');

const ok = (extra = {}) => ({ rows: [{ value: 'a', count: 1 }], sources: [{ sourceId: 's1', ok: true, error: '' }], provenance: [{ sourceId: 's1', completeness: 'ran_to_end' }], ...extra });
// loader builds a stand-in for an api call and counts how often it ran.
function loader(value = ok()) {
  const fn = (signal) => { fn.calls++; fn.signals.push(signal); return Promise.resolve(typeof value === 'function' ? value() : value); };
  fn.calls = 0; fn.signals = [];
  return fn;
}
// held is a call that stays open until the test finishes it (or its signal aborts).
function held() {
  const fn = (signal) => new Promise((resolve, reject) => {
    fn.calls++; fn.signals.push(signal);
    fn.resolve = resolve;
    signal.addEventListener('abort', () => reject(new DOMException('aborted', 'AbortError')), { once: true });
  });
  fn.calls = 0; fn.signals = [];
  return fn;
}
const fresh = () => memo.bumpEpoch();
const body = (n) => ({ query: { start: 'a', end: 'b' }, dim: 'userId', top: n });

test('a repeat is served from memory and says when it was fetched', async () => {
  fresh();
  const load = loader();
  const first = await memo.evidenceFetch('breakdown', body(20), load, undefined, () => 1000);
  const second = await memo.evidenceFetch('breakdown', body(20), load, undefined, () => 9000);
  assert.equal(load.calls, 1);
  assert.equal(first.fromMemo, false);
  assert.equal(second.fromMemo, true);
  assert.equal(second.fetchedAt, 1000, 'the time of the original read, not of the repeat');
  assert.equal(second.value, first.value);
  await memo.evidenceFetch('breakdown', body(21), load);
  assert.equal(load.calls, 2, 'a different request body is a different key');
});

test('the as-of footer reads as HH:MM:SS, names the zone, and passes the copy lint', () => {
  const text = memo.asOfText(Date.UTC(2026, 9, 24, 14, 3, 22), 'UTC');
  assert.match(text, /as of 14:03:22 UTC/);
  assert.deepEqual(lintCopy([{ id: 'memo-asof', text }], deck.terms), []);
});

test('an epoch bump empties the memo', async () => {
  fresh();
  const load = loader();
  await memo.evidenceFetch('slowest', body(1), load);
  await memo.evidenceFetch('activity', body(2), load);
  assert.equal(memo.memoSize().entries, 2);
  const before = memo.currentEpoch();
  assert.equal(memo.bumpEpoch(), before + 1);
  assert.deepEqual(memo.memoSize(), { entries: 0, bytes: 0 });
  const again = await memo.evidenceFetch('slowest', body(1), load);
  assert.equal(again.fromMemo, false);
  assert.equal(load.calls, 3);
});

test('a call that finishes after an epoch bump is not stored', async () => {
  fresh();
  const slow = held();
  const running = memo.evidenceFetch('breakdown', body(1), slow);
  memo.bumpEpoch();
  slow.resolve(ok());
  const result = await running;
  assert.equal(result.fromMemo, false, 'its own caller still gets the answer');
  assert.equal(memo.memoSize().entries, 0);
});

test('a response with a failed source is not stored', async () => {
  fresh();
  const failed = loader(ok({ sources: [{ sourceId: 's1', ok: true }, { sourceId: 's2', ok: false, error: 'timed out' }] }));
  await memo.evidenceFetch('breakdown', body(1), failed);
  await memo.evidenceFetch('breakdown', body(1), failed);
  assert.equal(failed.calls, 2, 'a retry goes to the server');
  assert.equal(memo.memoSize().entries, 0);
});

test('partial and hint reads are not stored, whole reads are', async () => {
  fresh();
  for (const completeness of ['partial', 'hint', '']) {
    const load = loader(ok({ provenance: [{ sourceId: 's1', completeness: 'complete' }, { sourceId: 's2', completeness }] }));
    await memo.evidenceFetch('slowest', body(completeness || 'blank'), load);
  }
  assert.equal(memo.memoSize().entries, 0);
  for (const completeness of ['complete', 'ran_to_end']) await memo.evidenceFetch('slowest', body(completeness), loader(ok({ provenance: [{ sourceId: 's1', completeness }] })));
  await memo.evidenceFetch('slowest', body('older server'), loader({ rows: [], sources: [], truncated: false }));
  assert.equal(memo.memoSize().entries, 3);
});

test('an error is never stored and the next call retries', async () => {
  fresh();
  let calls = 0;
  const flaky = () => { calls++; return calls === 1 ? Promise.reject(new Error('boom')) : Promise.resolve(ok()); };
  await assert.rejects(memo.evidenceFetch('activity', body(1), flaky), /boom/);
  const retry = await memo.evidenceFetch('activity', body(1), flaky);
  assert.equal(retry.fromMemo, false);
  assert.equal(calls, 2);
});

test('a 401 forgets everything already stored', async () => {
  fresh();
  const load = loader();
  await memo.evidenceFetch('breakdown', body(1), load);
  const gone = Object.assign(new Error('Open Unravis from the link'), { status: 401, code: 'no_session' });
  await assert.rejects(memo.evidenceFetch('slowest', body(2), () => Promise.reject(gone)), /Open Unravis/);
  assert.equal(memo.memoSize().entries, 0);
});

test('callers that arrive while the same call runs share one call', async () => {
  fresh();
  const slow = held();
  const a = memo.evidenceFetch('breakdown', body(1), slow);
  const b = memo.evidenceFetch('breakdown', body(1), slow);
  assert.equal(slow.calls, 1);
  slow.resolve(ok());
  const [x, y] = await Promise.all([a, b]);
  assert.equal(x.value, y.value);
  assert.equal(slow.calls, 1);
  assert.equal((await memo.evidenceFetch('breakdown', body(1), slow)).fromMemo, true);
});

test('one caller cancelling does not cancel the call the other still waits on', async () => {
  fresh();
  const slow = held();
  const first = new AbortController();
  const a = memo.evidenceFetch('breakdown', body(1), slow, first.signal);
  const b = memo.evidenceFetch('breakdown', body(1), slow);
  first.abort();
  await assert.rejects(a, (cause) => cause.name === 'AbortError');
  assert.equal(slow.signals[0].aborted, false);
  slow.resolve(ok());
  assert.equal((await b).fromMemo, false);
  assert.equal(memo.memoSize().entries, 1);
});

test('an aborted pending entry is not served', async () => {
  fresh();
  const slow = held();
  const controller = new AbortController();
  const first = memo.evidenceFetch('slowest', body(1), slow, controller.signal);
  controller.abort();
  await assert.rejects(first, (cause) => cause.name === 'AbortError');
  assert.equal(slow.signals[0].aborted, true, 'the shared call is cancelled once nobody waits for it');
  // The entry was removed before the rejection, so this caller starts a new call instead of joining the dead one.
  const next = memo.evidenceFetch('slowest', body(1), slow);
  assert.equal(slow.calls, 2);
  slow.resolve(ok());
  assert.equal((await next).fromMemo, false);
  assert.equal(memo.memoSize().entries, 1);
});

test('an abort that the loader ignores is still not stored', async () => {
  fresh();
  let release;
  const stubborn = () => new Promise((resolve) => { release = () => resolve(ok()); });
  const controller = new AbortController();
  const first = memo.evidenceFetch('activity', body(1), stubborn, controller.signal);
  controller.abort();
  await assert.rejects(first, (cause) => cause.name === 'AbortError');
  release();
  await new Promise((resolve) => setImmediate(resolve));
  assert.equal(memo.memoSize().entries, 0);
});

test('a signal that is already aborted never calls the server', async () => {
  fresh();
  const load = loader();
  const controller = new AbortController();
  controller.abort();
  await assert.rejects(memo.evidenceFetch('breakdown', body(1), load, controller.signal), (cause) => cause.name === 'AbortError');
  assert.equal(load.calls, 0);
});

test('the 65th entry evicts the least recently used one', async () => {
  fresh();
  const load = loader();
  for (let i = 0; i < memo.MEMO_MAX_ENTRIES; i++) await memo.evidenceFetch('breakdown', body(i), load);
  assert.equal(memo.memoSize().entries, 64);
  await memo.evidenceFetch('breakdown', body(0), load); // entry 0 is now the most recently used
  assert.equal(load.calls, 64);
  await memo.evidenceFetch('breakdown', body(64), load); // the 65th
  assert.equal(memo.memoSize().entries, 64);
  assert.equal((await memo.evidenceFetch('breakdown', body(0), load)).fromMemo, true, 'recently used entry survived');
  assert.equal((await memo.evidenceFetch('breakdown', body(1), load)).fromMemo, false, 'entry 1 was the least recently used and went');
});

test('the byte cap evicts too, and one oversized answer is not kept', async () => {
  fresh();
  const big = (n) => loader(ok({ pad: 'x'.repeat(n) }));
  const third = Math.floor(memo.MEMO_MAX_BYTES / 4);
  await memo.evidenceFetch('slowest', body(1), big(third));
  await memo.evidenceFetch('slowest', body(2), big(third));
  await memo.evidenceFetch('slowest', body(3), big(third));
  assert.equal(memo.memoSize().entries, 3);
  assert.ok(memo.memoSize().bytes <= memo.MEMO_MAX_BYTES);
  await memo.evidenceFetch('slowest', body(4), big(third));
  assert.equal(memo.memoSize().entries, 3, 'the oldest went to make room');
  assert.ok(memo.memoSize().bytes <= memo.MEMO_MAX_BYTES);
  assert.equal((await memo.evidenceFetch('slowest', body(1), big(third))).fromMemo, false);
  fresh();
  await memo.evidenceFetch('slowest', body(9), big(memo.MEMO_MAX_BYTES + 1));
  assert.equal(memo.memoSize().entries, 0);
});

test('the allowed endpoints are exactly breakdown, slowest and activity', async () => {
  fresh();
  assert.deepEqual([...memo.EVIDENCE_ENDPOINTS], ['breakdown', 'slowest', 'activity']);
  for (const endpoint of ['record', 'payload', 'export', 'requests', 'analyze', 'coverage', 'patterns', '']) {
    const load = loader();
    await assert.rejects(memo.evidenceFetch(endpoint, body(1), load), TypeError);
    assert.equal(load.calls, 0);
  }
  assert.equal(memo.memoSize().entries, 0);
});

test('the memo never touches sessionStorage, localStorage or indexedDB', async () => {
  const names = ['sessionStorage', 'localStorage', 'indexedDB'];
  const saved = Object.fromEntries(names.map((name) => [name, Object.getOwnPropertyDescriptor(globalThis, name)]));
  const touched = [];
  for (const name of names) {
    Object.defineProperty(globalThis, name, { configurable: true, get() { touched.push(name); throw new Error(name + ' was used'); } });
  }
  try {
    fresh();
    const load = loader();
    await memo.evidenceFetch('breakdown', body(1), load);
    await memo.evidenceFetch('breakdown', body(1), load);
    await assert.rejects(memo.evidenceFetch('slowest', body(2), () => Promise.reject(new Error('x'))), /x/);
    memo.bumpEpoch();
  } finally {
    for (const name of names) {
      if (saved[name]) Object.defineProperty(globalThis, name, saved[name]); else delete globalThis[name];
    }
  }
  assert.deepEqual(touched, []);
  assert.doesNotMatch(fs.readFileSync(path.join(root, 'src/lib/evidenceMemo.ts'), 'utf8'), /sessionStorage|localStorage|indexedDB/);
});
