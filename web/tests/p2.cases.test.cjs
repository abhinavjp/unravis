// Phase P2 (case lifecycle core): the page side of the believed state (409 M2), Reopen, the "needs another try" list and the create request id.
// The server side is tested in Go (internal/casestore, internal/server). These tests pin what the page sends and shows.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

const root = path.resolve(__dirname, '..');
const loaded = new Map();
function load(relativePath) {
  const filename = path.join(root, relativePath);
  if (loaded.has(filename)) return loaded.get(filename).exports;
  const output = ts.transpileModule(fs.readFileSync(filename, 'utf8'), { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022, esModuleInterop: true } }).outputText;
  const module = { exports: {} };
  loaded.set(filename, module);
  const nodeRequire = createRequire(filename);
  const req = (request) => {
    if (/^\.\.?\//.test(request)) {
      const plain = path.join(path.dirname(filename), request + '.ts');
      const store = path.join(path.dirname(filename), request + '.svelte.ts');
      if (fs.existsSync(plain)) return load(path.relative(root, plain));
      if (fs.existsSync(store)) return load(path.relative(root, store));
    }
    return nodeRequire(request);
  };
  new Function('require', 'module', 'exports', '$state', output)(req, module, module.exports, (value) => value);
  return module.exports;
}
const read = (relative) => fs.readFileSync(path.join(root, relative), 'utf8');

const cases = load('src/lib/cases.ts');
const life = load('src/lib/caseLifecycle.ts');
const copy = load('src/lib/copy/caseHomeCopy.ts');
const importCopy = load('src/lib/copy/importCopy.ts');
const story = load('src/lib/story.ts');
const create = load('src/lib/createRequest.ts');
const sub = load('src/lib/wizardSubmit.ts');
const src = load('src/lib/wizardSources.ts');
const { lintScreenStrings } = load('src/lib/copy/copylint.ts');
const { deck } = load('src/lib/copy/deck.ts');

const TOKEN = 'link-session-token-0123456789abcdef';
const ID = 'abcdefghijklmnopqrstuvwxyz'.slice(0, 26).replace(/[^a-z2-7]/g, 'a');
const CASE_ID = 'a'.repeat(26);

/** A fetch that records what the page sent and answers from a queue, with sessionStorage standing in for the browser's. */
async function withServer(responses, fn) {
  const names = ['sessionStorage', 'fetch'];
  const saved = Object.fromEntries(names.map((name) => [name, Object.getOwnPropertyDescriptor(globalThis, name)]));
  const calls = [];
  const items = new Map([['unravis.session', TOKEN]]);
  const values = {
    sessionStorage: { getItem: (k) => (items.has(k) ? items.get(k) : null), setItem: (k, v) => { items.set(k, String(v)); }, removeItem: (k) => { items.delete(k); } },
    fetch: async (url, init = {}) => {
      calls.push({ url, method: init.method || 'GET', headers: new Headers(init.headers), body: init.body });
      const next = responses.shift() ?? { status: 200, body: {} };
      if (next.network) throw new TypeError('Failed to fetch');
      return { ok: next.status >= 200 && next.status < 300, status: next.status, json: async () => next.body, headers: { get: () => null } };
    }
  };
  for (const name of names) Object.defineProperty(globalThis, name, { configurable: true, writable: true, value: values[name] });
  try { return await fn({ calls, items }); } finally {
    for (const name of names) {
      if (saved[name]) Object.defineProperty(globalThis, name, saved[name]);
      else delete globalThis[name];
    }
  }
}

/* ------------------------------------------------------------------ the 409 (M2) */

test('p2: a 409 case-changed is the M2 sentence and is told apart from the other 409 answers', () => {
  const changed = new cases.CaseApiError('x', 409, 'case-changed');
  assert.equal(cases.isCaseChanged(changed), true);
  assert.equal(importCopy.errorText(changed), 'This case changed in another tab. Reload.');
  assert.equal(copy.CASE_HOME_TEXT.changedElsewhere, 'This case changed in another tab. Reload.');
  assert.equal(cases.isCaseChanged(new cases.CaseApiError('x', 409, 'case-closed')), false, 'a closed-case refusal is not M2');
  assert.equal(cases.isCaseChanged(new cases.CaseApiError('x', 423, 'locked')), false, 'the key-locked answer keeps its meaning');
  assert.equal(cases.isCaseChanged(new Error('plain')), false);
});

test('p2: the other new refusals have their own sentences (M3, M5, M15, M18, M19)', () => {
  const text = (reason) => importCopy.errorText(new cases.CaseApiError('x', 409, reason));
  assert.equal(text('case-already-made'), 'This case was already made. Find it on the start screen.');
  assert.equal(text('case-freeing'), 'This case is being freed. Try again in a moment.');
  assert.equal(text('case-erasing'), 'This case is being erased.');
  assert.equal(text('cancel-archive-first'), 'An archive is running. Cancel it first.');
  assert.equal(text('archive-running'), 'An archive is already running.');
});

/* ------------------------------------------------------------------ what the page sends */

test('p2: Close and Reopen send the state the page believes; no belief sends no header', async () => {
  await withServer([{ status: 200, body: { id: CASE_ID, closed: '2001-01-01T00:00:00Z' } }, { status: 200, body: { id: CASE_ID } }, { status: 200, body: { id: CASE_ID } }], async ({ calls }) => {
    await cases.closeCase(CASE_ID, 'open');
    await cases.reopenCase(CASE_ID, 'closed');
    await cases.closeCase(CASE_ID);
    assert.match(calls[0].url, /\/close$/);
    assert.equal(calls[0].headers.get('X-Expect-State'), 'open');
    assert.match(calls[1].url, /\/reopen$/);
    assert.equal(calls[1].headers.get('X-Expect-State'), 'closed');
    assert.equal(calls[2].headers.get('X-Expect-State'), null);
    assert.ok(calls.every((call) => call.method === 'POST'));
  });
});

test('p2: a 409 from the server arrives as the same refusal the screens test for', async () => {
  await withServer([{ status: 409, body: { error: 'This case changed in another tab. Reload.', reason: 'case-changed' } }], async () => {
    await assert.rejects(() => cases.closeCase(CASE_ID, 'open'), (error) => cases.isCaseChanged(error));
  });
});

test('p2: Create sends the request id in the body, trimmed title, and nothing else secret', async () => {
  await withServer([{ status: 201, body: { id: CASE_ID, title: 'T' } }], async ({ calls }) => {
    await cases.createCase('  T  ', 'abcd1234abcd1234');
    assert.deepEqual(JSON.parse(calls[0].body), { title: 'T', requestId: 'abcd1234abcd1234' });
  });
});

test('p2: the retry list is read with the case, put as a replacement, removed one at a time and tried one at a time', async () => {
  const view = { id: CASE_ID, title: 'T', state: 'open', history: [{ at: '2001-01-01T00:00:00Z', kind: 'closed' }, { kind: 5 }], retries: [{ id: 3, kind: 'import', key: 'k', label: 'a.log', reason: 'bad-file', created: 'now', password: 'secret' }, { id: 'x' }, null] };
  await withServer([{ status: 200, body: view }, { status: 200, body: { retries: [{ id: 4, kind: 'tag', key: 'k', label: 'L', reason: 'r', created: 'c' }] } }, { status: 204, body: {} }, { status: 204, body: {} }], async ({ calls }) => {
    const got = await cases.getCaseView(CASE_ID);
    assert.deepEqual(got.retries.map((r) => [r.id, r.kind]), [[3, 'import']], 'a bad item is dropped');
    assert.ok(!('password' in got.retries[0]), 'only the known fields are kept');
    assert.deepEqual(got.history, [{ at: '2001-01-01T00:00:00Z', kind: 'closed' }]);
    assert.equal(got.state, 'open');
    const put = await cases.putRetries(CASE_ID, [{ kind: 'tag', key: 'k', label: 'L', reason: 'r' }]);
    assert.equal(put[0].id, 4);
    assert.equal(calls[1].method, 'PUT');
    assert.match(calls[1].url, /\/retries$/);
    await cases.deleteRetry(CASE_ID, 4);
    await cases.runRetry(CASE_ID, 4);
    assert.equal(calls[2].method, 'DELETE');
    assert.match(calls[2].url, /\/retries\/4$/);
    assert.match(calls[3].url, /\/retries\/4\/retry$/);
  });
});

test('p2: a retry that only the wizard can do is told in plain words, and the two import failures stay apart (C19)', () => {
  assert.equal(cases.retryInPage(new cases.CaseApiError('x', 422, 'retry-in-page')), true);
  assert.equal(cases.retryInPage(new cases.CaseApiError('x', 422, 'other')), false);
  assert.notEqual(copy.retryKindLabel('audit-source'), copy.retryKindLabel('import'));
  assert.equal(copy.retryKindLabel('audit-source'), 'Not added to analysis');
  assert.equal(copy.retryKindLabel('import'), 'Import failed');
  assert.equal(copy.retryKindLabel('something-new'), copy.CASE_HOME_TEXT.retryOther);
});

/* ------------------------------------------------------------------ button states */

test('p2: the Reopen button is shown for a Closed case only, and is off while it runs', () => {
  const open = { state: 'open', closed: null };
  const closed = { state: 'closed', closed: 'x' };
  assert.equal(life.reopenButton(open, false), 'hidden');
  assert.equal(life.reopenButton(closed, false), 'enabled');
  assert.equal(life.reopenButton(closed, true), 'busy');
  assert.equal(life.reopenButton({ state: 'archived', closed: 'x' }, false), 'disabled');
  assert.equal(life.reopenButton({ state: 'archiving', closed: 'x' }, false), 'disabled');
  assert.equal(life.reopenButton({ state: 'closed', closed: 'x', untrusted: true }, false), 'hidden');
  assert.equal(life.reopenButton({ state: 'erased', closed: null }, false), 'hidden');
  assert.equal(life.reopenButton({ closed: 'x' }, false), 'enabled', 'an older server with only the closed stamp still reads as Closed');
});

test('p2: each retry item offers what the case state allows', () => {
  const open = { state: 'open', closed: null };
  assert.equal(life.retryAction({ kind: 'environments' }, open), 'retry');
  assert.equal(life.retryAction({ kind: 'tag' }, open), 'retry');
  assert.equal(life.retryAction({ kind: 'audit-source' }, open), 'retry');
  assert.equal(life.retryAction({ kind: 'import' }, open), 'add-file', 'a file has to be picked again');
  assert.equal(life.retryAction({ kind: 'tag' }, { state: 'closed', closed: 'x' }), 'reopen-first');
  assert.equal(life.retryAction({ kind: 'tag' }, { state: 'archived', closed: 'x' }), 'hidden');
  assert.equal(life.retryAction({ kind: 'tag' }, { state: 'open', closed: null, untrusted: true }), 'hidden');
});

/* ------------------------------------------------------------------ the story and the screens */

test('p2: the story shows Closed and Reopened from the history, and ignores kinds it has no words for', () => {
  const events = story.buildStory({ info: { id: CASE_ID, title: 'T', created: '2001-01-01T00:00:00Z', closed: null, bytes: 0, imports: 0 }, imports: [], health: [], theories: [], history: [{ at: '2001-01-02T00:00:00Z', kind: 'closed' }, { at: '2001-01-03T00:00:00Z', kind: 'reopened' }, { at: '2001-01-04T00:00:00Z', kind: 'created' }, { at: '2001-01-05T00:00:00Z', kind: 'whatever' }] });
  const titles = events.map((e) => e.title);
  assert.ok(titles.includes(copy.CASE_HOME_TEXT.eventClosed));
  assert.ok(titles.includes(copy.CASE_HOME_TEXT.eventReopened));
  assert.equal(events.length, 3, 'the start, the close and the reopen');
  assert.equal(new Set(events.map((e) => e.id)).size, events.length, 'ids are stable and unique');
});

test('p2: the new strings pass the copy lint', () => {
  assert.deepEqual(lintScreenStrings(copy.caseHomeCopyEntries(), deck.terms), []);
  assert.deepEqual(lintScreenStrings(importCopy.importCopyEntries(), deck.terms), []);
});

test('p2: Case home and the case list wire Reopen, the retry list and the M2 notice with their guards', () => {
  const home = read('src/lib/components/CaseHome.svelte');
  assert.match(home, /reopenButton\(info, reopening\)/);
  assert.match(home, /reopenCase\(id, believedState\(info\)\)/);
  assert.match(home, /if \(!info \|\| reopening \|\| reopenState !== 'enabled'\) return;/, 'a second press while it runs does nothing');
  assert.match(home, /CASE_HOME_TEXT\.changedElsewhere/);
  assert.match(home, /CASE_HOME_TEXT\.retryHeading/);
  assert.match(home, /history: info\?\.history/);
  const panel = read('src/lib/components/CasePanel.svelte');
  assert.match(panel, /closeCase\(c\.id, believedState\(c\)/);
  assert.match(panel, /reopenCase\(c\.id, believedState\(c\)/);
  assert.match(panel, /createCase\(title, requestId/);
  assert.match(panel, /if \(busy \|\| reopeningId\) return;/);
});

/* ------------------------------------------------------------------ the request id */

function withStorage(fn) {
  const saved = Object.getOwnPropertyDescriptor(globalThis, 'sessionStorage');
  const items = new Map();
  Object.defineProperty(globalThis, 'sessionStorage', { configurable: true, writable: true, value: { getItem: (k) => (items.has(k) ? items.get(k) : null), setItem: (k, v) => { items.set(k, String(v)); }, removeItem: (k) => { items.delete(k); } } });
  try { return fn(items); } finally { if (saved) Object.defineProperty(globalThis, 'sessionStorage', saved); else delete globalThis.sessionStorage; }
}

test('p2: a request id has the shape the server accepts and is new each time', () => {
  const a = create.newRequestId();
  const b = create.newRequestId();
  assert.match(a, /^[A-Za-z0-9_-]{8,64}$/);
  assert.notEqual(a, b);
});

test('p2: a reload keeps the same id for the same title, a new title gets a new one, and a reset forgets it', () => {
  withStorage((items) => {
    const first = create.requestIdFor('Week 42');
    assert.equal(create.requestIdFor('Week 42'), first, 'a reload mid-create sends the same id');
    assert.equal(create.requestIdFor('  Week 42  '), first, 'spaces around the title do not make a second case');
    const other = create.requestIdFor('Week 43');
    assert.notEqual(other, first);
    create.forgetRequestId();
    assert.equal(items.size, 0);
    assert.notEqual(create.requestIdFor('Week 43'), other, 'a new wizard run is a new case');
  });
});

test('p2: a broken or blocked sessionStorage still gives a usable id', () => {
  const saved = Object.getOwnPropertyDescriptor(globalThis, 'sessionStorage');
  Object.defineProperty(globalThis, 'sessionStorage', { configurable: true, get() { throw new Error('blocked'); } });
  try {
    assert.match(create.requestIdFor('T'), /^[A-Za-z0-9_-]{8,64}$/);
    create.forgetRequestId();
  } finally { if (saved) Object.defineProperty(globalThis, 'sessionStorage', saved); else delete globalThis.sessionStorage; }
  withStorage((items) => {
    items.set('unravis.createRequest', '{not json');
    assert.match(create.requestIdFor('T'), /^[A-Za-z0-9_-]{8,64}$/, 'a damaged note is replaced');
    items.set('unravis.createRequest', JSON.stringify({ title: 'T', id: 'short' }));
    assert.notEqual(create.requestIdFor('T'), 'short', 'an id of the wrong shape is never sent');
  });
});

/* ------------------------------------------------------------------ the list the wizard saves */

test('p2: the wizard saves failures after the case, with kinds kept apart and a payload only where a retry can use it', () => {
  const a = src.makeSource('discover', 'f1', 'W3SVC1');
  const b = src.makeSource('discover', 'f2', 'W3SVC2');
  const c = src.makeSource('mongo-database', 'conn|audit1', 'audit1');
  const state = sub.blankSubmitState();
  state.caseId = CASE_ID;
  state.envs = { status: 'failed', reason: 'boom' };
  state.tags[src.sourceKey(a)] = { status: 'failed', reason: 'tag-failed' };
  state.imports[src.sourceKey(b)] = { status: 'failed', reason: 'bad-file', outcome: null, percent: null };
  state.audit[src.sourceKey(c)] = { status: 'failed', reason: 'unreachable' };
  state.tags[src.sourceKey(c)] = { status: 'failed', reason: 'tag-failed' };
  const items = sub.retryItemsOf(state, { envs: ['prod'], sources: [a, b, c] });
  assert.deepEqual(items.map((i) => i.kind), ['environments', 'tag', 'import', 'tag', 'audit-source']);
  assert.deepEqual(items[0].payload, { environments: ['prod'] });
  assert.equal(items[1].payload.label, 'W3SVC1', 'a plain tag can be redone by the server');
  assert.equal(items[2].payload, undefined, 'a file has to be picked again, so the item carries nothing');
  assert.equal(items[3].payload, undefined, 'a Mongo tag needs a connection only the wizard holds');
  assert.notEqual(items[2].kind, items[4].kind, 'import failed and not added to analysis stay apart');
  for (const item of items) assert.ok(!JSON.stringify(item).includes('password'));
  assert.deepEqual(sub.retryItemsOf(sub.blankSubmitState(), { envs: [], sources: [] }), [], 'no case, nothing to save to');
});
