const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

const root = path.resolve(__dirname, '..');
// Like the loader in the other tests, but one module is loaded once, so the lock signal the API raises reaches the listener this test sets.
function makeLoader() {
  const cache = new Map();
  function load(relativePath) {
    const filename = path.join(root, relativePath);
    if (cache.has(filename)) return cache.get(filename).exports;
    const source = fs.readFileSync(filename, 'utf8');
    const output = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022, esModuleInterop: true } }).outputText;
    const module = { exports: {} };
    cache.set(filename, module);
    const nodeRequire = createRequire(filename);
    const req = (request) => /^\.\.?\//.test(request) && fs.existsSync(path.join(path.dirname(filename), request + '.ts'))
      ? load(path.relative(root, path.join(path.dirname(filename), request + '.ts')))
      : nodeRequire(request);
    new Function('require', 'module', 'exports', output)(req, module, module.exports);
    return module.exports;
  }
  return load;
}
const read = (relative) => fs.readFileSync(path.join(root, relative), 'utf8');

const pure = makeLoader();
const { normalisePath, checkRestoreInput, restoreFailureOf, restoreView, retryAfterOf, restoreNeedsKey, CASE_FILE_EXTENSION } = pure('src/lib/restoreCase.ts');
const { ENTER_TEXT, RESTORE_FAILED, throttledText } = pure('src/lib/copy/enterCopy.ts');

const TOKEN = 'link-session-token-0123456789abcdef';
const PASS = 'correct horse battery staple';
const FILE = 'C:\\Cases\\mycase.unravis-case';
const NEW_ID = 'abcdefghijklmnopqrstuvwxy2';

async function withApi(responses, fn) {
  const names = ['location', 'history', 'sessionStorage', 'fetch'];
  const saved = Object.fromEntries(names.map((name) => [name, Object.getOwnPropertyDescriptor(globalThis, name)]));
  const calls = [];
  const items = new Map([['unravis.session', TOKEN]]);
  const values = {
    location: { hash: '', search: '', pathname: '/' },
    history: { state: null, replaceState: () => {} },
    sessionStorage: { getItem: (k) => (items.has(k) ? items.get(k) : null), setItem: (k, v) => { items.set(k, String(v)); }, removeItem: (k) => { items.delete(k); } },
    fetch: async (url, init) => {
      calls.push({ url, method: init.method, headers: new Headers(init.headers), body: init.body });
      const next = responses.shift() ?? { status: 200, body: {} };
      if (next.network) throw new TypeError('Failed to fetch');
      const headers = next.headers ?? {};
      return { ok: next.status >= 200 && next.status < 300, status: next.status, json: async () => next.body, headers: { get: (name) => headers[name] ?? null } };
    }
  };
  for (const name of names) Object.defineProperty(globalThis, name, { configurable: true, writable: true, value: values[name] });
  try {
    const load = makeLoader();
    return await fn({ api: load('src/lib/caseFileApi.ts'), lock: load('src/lib/lockSignal.ts'), calls, items });
  } finally {
    for (const name of names) {
      if (saved[name]) Object.defineProperty(globalThis, name, saved[name]);
      else delete globalThis[name];
    }
  }
}

test('a typed path loses the quotes File Explorer adds and the spaces around it', () => {
  assert.equal(normalisePath('  "' + FILE + '"  '), FILE);
  assert.equal(normalisePath(FILE), FILE);
  assert.equal(normalisePath(''), '');
  assert.equal(normalisePath('"'), '"');
  assert.equal(normalisePath(undefined), '');
});

test('the form asks for a path first, then a full path on this computer, then the right ending, then a key', () => {
  const none = checkRestoreInput('', '');
  assert.deepEqual(none, { ok: false, field: 'path', message: ENTER_TEXT.pathEmpty });
  assert.equal(checkRestoreInput('   ', PASS).message, ENTER_TEXT.pathEmpty);
  for (const bad of ['mycase.unravis-case', '.\\mycase.unravis-case', '\\\\server\\share\\a.unravis-case', '\\\\?\\C:\\a.unravis-case', '/etc/a.unravis-case', 'C:a.unravis-case', 'https://x.test/a.unravis-case']) {
    assert.deepEqual(checkRestoreInput(bad, PASS), { ok: false, field: 'path', message: ENTER_TEXT.pathShape }, bad);
  }
  assert.deepEqual(checkRestoreInput('C:\\Cases\\notes.txt', PASS), { ok: false, field: 'path', message: ENTER_TEXT.pathName });
  assert.deepEqual(checkRestoreInput('C:\\Cases\\', PASS), { ok: false, field: 'path', message: ENTER_TEXT.pathName });
  assert.deepEqual(checkRestoreInput('C:\\' + 'a'.repeat(2100) + CASE_FILE_EXTENSION, PASS), { ok: false, field: 'path', message: ENTER_TEXT.pathTooLong });
  const edge = 'C:\\' + 'a'.repeat(520 - 3 - CASE_FILE_EXTENSION.length) + CASE_FILE_EXTENSION;
  assert.equal(edge.length, 520);
  assert.equal(checkRestoreInput(edge, PASS).ok, true, '520 bytes is the server limit');
  assert.equal(checkRestoreInput('C:\\a' + edge.slice(3), PASS).ok, false, '521 bytes is over it');
  assert.equal(checkRestoreInput('C:\\' + '\u00e9'.repeat(260 - 1) + CASE_FILE_EXTENSION, PASS).ok, false, 'bytes, not characters, are counted');
  assert.deepEqual(checkRestoreInput(FILE, ''), { ok: false, field: 'key', message: ENTER_TEXT.keyEmpty });
  assert.deepEqual(checkRestoreInput(FILE, PASS), { ok: true, path: FILE });
  assert.deepEqual(checkRestoreInput('"' + FILE + '"', PASS), { ok: true, path: FILE }, 'quotes from Copy as path');
  assert.deepEqual(checkRestoreInput('c:/Cases/MyCase.UNRAVIS-CASE', PASS), { ok: true, path: 'c:/Cases/MyCase.UNRAVIS-CASE' }, 'slashes and capitals');
});

test('the master key is never trimmed: a key made of spaces still counts as typed', () => {
  assert.equal(checkRestoreInput(FILE, '   ').ok, true);
});

test('no message the form builds ever holds the key or the path', () => {
  const secret = 'hunter2-secret-phrase';
  const odd = 'C:\\private\\folder\\case.txt';
  const messages = [checkRestoreInput(odd, secret).message, checkRestoreInput(FILE, '').message];
  for (const kind of ['bad-case-file', 'bad-path', 'throttled', 'locked', 'no-key', 'busy', 'network', 'other']) messages.push(restoreView({ kind, retryAfterSeconds: 9 }).message);
  for (const message of messages) assert.ok(!message.includes(secret) && !message.includes('private'), message);
});

test('every refusal maps to a kind', () => {
  assert.deepEqual(restoreFailureOf({ status: 422, reason: 'bad-case-file' }), { kind: 'bad-case-file' });
  assert.deepEqual(restoreFailureOf({ status: 422, reason: 'anything' }), { kind: 'bad-case-file' });
  assert.deepEqual(restoreFailureOf({ status: 429, reason: 'throttled', retryAfterSeconds: 12 }), { kind: 'throttled', retryAfterSeconds: 12 });
  assert.deepEqual(restoreFailureOf({ status: 429 }), { kind: 'throttled', retryAfterSeconds: 5 }, 'no figure given');
  assert.deepEqual(restoreFailureOf({ status: 429, retryAfterSeconds: 0 }), { kind: 'throttled', retryAfterSeconds: 5 });
  assert.deepEqual(restoreFailureOf({ status: 423, reason: 'locked' }), { kind: 'locked' });
  assert.deepEqual(restoreFailureOf({ status: 409, reason: 'no-master-key' }), { kind: 'no-key' });
  assert.deepEqual(restoreFailureOf({ status: 409, reason: 'busy' }), { kind: 'busy' });
  assert.deepEqual(restoreFailureOf({ status: 400, reason: 'bad-path' }), { kind: 'bad-path' });
  assert.deepEqual(restoreFailureOf({ status: 0, reason: 'unreachable' }), { kind: 'network' });
  assert.deepEqual(restoreFailureOf({ status: 500 }), { kind: 'other' });
  assert.deepEqual(restoreFailureOf({ status: 413, reason: 'too-big' }), { kind: 'other' });
  assert.deepEqual(restoreFailureOf(new Error('boom')), { kind: 'other' });
  assert.deepEqual(restoreFailureOf(null), { kind: 'other' });
  assert.deepEqual(restoreFailureOf('text'), { kind: 'other' });
});

test('a damaged file, a wrong key and a wrong file all read as the same one sentence', () => {
  const reasons = [
    { status: 422, reason: 'bad-case-file' }, // wrong key
    { status: 422, reason: 'bad-case-file' }, // damaged
    { status: 422, reason: 'bad-case-file' }, // not a case file
    { status: 422 }
  ];
  const views = reasons.map((r) => restoreView(restoreFailureOf(r)));
  for (const view of views) {
    assert.equal(view.message, RESTORE_FAILED);
    assert.equal(view.offerSetup, false);
    assert.equal(view.waitSeconds, 0);
    assert.equal(view.focus, 'key');
  }
  assert.match(RESTORE_FAILED, /file could not be opened with that master key/);
});

test('a throttled restore shows the wait in plain words and holds the button for that long', () => {
  const view = restoreView(restoreFailureOf({ status: 429, retryAfterSeconds: 7 }));
  assert.equal(view.waitSeconds, 7);
  assert.equal(view.message, throttledText(7));
  assert.match(view.message, /7 seconds/);
  assert.equal(restoreView({ kind: 'throttled', retryAfterSeconds: 1 }).waitSeconds, 1);
  assert.equal(restoreView({ kind: 'throttled', retryAfterSeconds: 0.2 }).waitSeconds, 1, 'rounded up');
  assert.match(restoreView({ kind: 'throttled', retryAfterSeconds: 90 }).message, /minute/);
});

test('a locked, missing or busy master key says what to do and does not blame the file', () => {
  assert.equal(restoreView({ kind: 'locked' }).message, ENTER_TEXT.locked);
  assert.deepEqual(restoreView({ kind: 'no-key' }), { message: ENTER_TEXT.needKey, offerSetup: true, waitSeconds: 0, focus: 'none' });
  assert.equal(restoreView({ kind: 'busy' }).message, ENTER_TEXT.busy);
  assert.equal(restoreView({ kind: 'network' }).message, ENTER_TEXT.network);
  assert.equal(restoreView({ kind: 'other' }).message, ENTER_TEXT.other);
  assert.equal(restoreView({ kind: 'bad-path' }).focus, 'path');
  for (const kind of ['locked', 'busy', 'network', 'other', 'no-key']) assert.notEqual(restoreView({ kind }).message, RESTORE_FAILED, kind);
});

test('Retry-After: the header first, then the body, at most an hour, junk ignored', () => {
  assert.equal(retryAfterOf('12', undefined), 12);
  assert.equal(retryAfterOf(' 12 ', 99), 12, 'the header wins');
  assert.equal(retryAfterOf(null, 8), 8);
  assert.equal(retryAfterOf(undefined, 8.2), 9);
  assert.equal(retryAfterOf('0', 6), 6, 'a zero header falls back to the body');
  assert.equal(retryAfterOf('Wed, 21 Oct 2026 07:28:00 GMT', undefined), undefined, 'a date is not used');
  assert.equal(retryAfterOf('-5', undefined), undefined);
  assert.equal(retryAfterOf('1.5', undefined), undefined);
  assert.equal(retryAfterOf('999999', undefined), 3600);
  assert.equal(retryAfterOf(null, 'ten'), undefined);
  assert.equal(retryAfterOf(null, Infinity), undefined);
  assert.equal(retryAfterOf(null, undefined), undefined);
});

test('the form needs a master key on this PC only when none is set', () => {
  assert.equal(restoreNeedsKey('none'), true);
  for (const state of ['locked', 'unlocked', 'unknown']) assert.equal(restoreNeedsKey(state), false, state);
});

test('restore sends the key and the path only in the request body, with the session token', async () => {
  await withApi([{ status: 201, body: { id: NEW_ID, title: 'Restored', created: 1, closed: false, bytes: 3, imports: 0 } }], async ({ api, calls }) => {
    const restored = await api.restoreCaseFile(FILE, PASS);
    assert.deepEqual(restored, { id: NEW_ID, title: 'Restored' });
    assert.equal(calls.length, 1);
    const [call] = calls;
    assert.equal(call.url, '/api/cases/restore');
    assert.equal(call.method, 'POST');
    assert.equal(call.headers.get('X-Audit-Token'), TOKEN);
    assert.deepEqual(JSON.parse(call.body), { path: FILE, passphrase: PASS });
    assert.ok(!call.url.includes('correct') && !call.url.includes('Cases'), 'nothing secret in the URL');
    for (const [name, value] of call.headers) assert.ok(!value.includes(PASS), 'not in header ' + name);
  });
});

test('export sends to the case route and the key only in the body', async () => {
  await withApi([{ status: 200, body: { path: FILE, name: 'mycase.unravis-case', bytes: 42 } }], async ({ api, calls }) => {
    const out = await api.exportCaseFile(NEW_ID, 'C:\\Cases', PASS);
    assert.deepEqual(out, { path: FILE, name: 'mycase.unravis-case', bytes: 42 });
    assert.equal(calls[0].url, '/api/cases/' + NEW_ID + '/export');
    assert.ok(!calls[0].url.includes('correct'));
    assert.deepEqual(JSON.parse(calls[0].body), { path: 'C:\\Cases', passphrase: PASS });
  });
});

test('a refused restore throws an error with a status, a reason and a wait, and no key or path in its text', async () => {
  const cases = [
    { response: { status: 422, body: { reason: 'bad-case-file', message: 'wrong key for ' + FILE } }, status: 422, reason: 'bad-case-file' },
    { response: { status: 429, body: { reason: 'throttled', retryAfterSeconds: 7 }, headers: { 'Retry-After': '9' } }, status: 429, reason: 'throttled', wait: 9 },
    { response: { status: 429, body: { reason: 'throttled', retryAfterSeconds: 7 } }, status: 429, reason: 'throttled', wait: 7 },
    { response: { status: 409, body: { reason: 'no-master-key' } }, status: 409, reason: 'no-master-key' },
    { response: { status: 409, body: { reason: 'busy' } }, status: 409, reason: 'busy' },
    { response: { status: 400, body: { reason: 'bad-path' } }, status: 400, reason: 'bad-path' },
    { response: { network: true }, status: 0, reason: 'unreachable' }
  ];
  for (const item of cases) {
    await withApi([item.response], async ({ api }) => {
      await assert.rejects(api.restoreCaseFile(FILE, PASS), (error) => {
        assert.equal(error.name, 'CaseFileApiError');
        assert.equal(error.status, item.status);
        assert.equal(error.reason, item.reason);
        assert.equal(error.retryAfterSeconds, item.wait);
        assert.ok(!error.message.includes(PASS) && !error.message.includes('Cases') && !error.message.includes('wrong key'), error.message);
        assert.ok(!JSON.stringify(error).includes(PASS));
        // And the card's view of it needs nothing more than that.
        const view = restoreView(restoreFailureOf(error));
        assert.ok(!view.message.includes(PASS));
        return true;
      }, JSON.stringify(item.response));
    });
  }
});

test('the 422 answer becomes the one sentence whatever the server says in its body', async () => {
  for (const body of [{ reason: 'bad-case-file' }, { reason: 'bad-case-file', detail: 'authentication failed' }, {}]) {
    await withApi([{ status: 422, body }], async ({ api }) => {
      const error = await api.restoreCaseFile(FILE, PASS).catch((e) => e);
      assert.equal(restoreView(restoreFailureOf(error)).message, RESTORE_FAILED);
    });
  }
});

test('a locked server (423) tells the app to show the unlock screen', async () => {
  await withApi([{ status: 423, body: { reason: 'locked' } }], async ({ api, lock }) => {
    let signalled = 0;
    lock.onCasesLocked(() => { signalled++; });
    const error = await api.restoreCaseFile(FILE, PASS).catch((e) => e);
    assert.equal(error.status, 423);
    assert.equal(signalled, 1);
    assert.deepEqual(restoreFailureOf(error), { kind: 'locked' });
  });
});

test('a wrong export key (401 wrong-key) keeps the session; any other 401 forgets it', async () => {
  await withApi([{ status: 401, body: { reason: 'wrong-key' } }], async ({ api, items }) => {
    const error = await api.exportCaseFile(NEW_ID, 'C:\\Cases', PASS).catch((e) => e);
    assert.equal(error.status, 401);
    assert.equal(error.reason, 'wrong-key');
    assert.equal(items.get('unravis.session'), TOKEN, 'the session is kept');
  });
  await withApi([{ status: 401, body: { reason: 'bad-token' } }], async ({ api, items }) => {
    const error = await api.restoreCaseFile(FILE, PASS).catch((e) => e);
    assert.equal(error.status, 401);
    assert.equal(error.reason, 'no_session');
    assert.equal(items.has('unravis.session'), false, 'the session is forgotten');
  });
});

test('no token means no request is made at all', async () => {
  await withApi([], async ({ api, calls, items }) => {
    items.clear();
    const error = await api.restoreCaseFile(FILE, PASS).catch((e) => e);
    assert.equal(error.status, 401);
    assert.equal(calls.length, 0);
  });
});

test('a success answer without a usable case code is refused', async () => {
  for (const body of [{}, { id: 7 }, { id: '../x' }, { id: 'SHORT' }, null]) {
    await withApi([{ status: 201, body }], async ({ api }) => {
      const error = await api.restoreCaseFile(FILE, PASS).catch((e) => e);
      assert.equal(error.reason, 'unreadable', JSON.stringify(body));
    });
  }
});

test('the API file never logs, stores or builds a URL from the key', () => {
  const src = read('src/lib/caseFileApi.ts');
  const code = src.replace(/\/\*[\s\S]*?\*\//g, '').replace(/\/\/.*$/gm, '');
  assert.doesNotMatch(code, /localStorage|console\./);
  assert.doesNotMatch(code, /sessionStorage/, 'the session is read through api.ts only');
  assert.doesNotMatch(code, /(?:path|url|base)\s*\+[^\n]*passphrase|\?[a-z]+=|encodeURIComponent\(passphrase/i);
  assert.equal((code.match(/JSON\.stringify/g) ?? []).length, 1, 'one place builds a body');
});
