// Phase 04 clumsy-user review. Tests named 'fixed: ...' were CONFIRMED defects (written as failing 'bug:' tests) and now pass; keep them as regressions.
// Tests named 'p04: ...' pin behaviour that holds.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

const root = path.resolve(__dirname, '..');
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
const { normaliseCode, resolveCode, CODE_NOT_FOUND } = pure('src/lib/caseCode.ts');
const { normalisePath, checkRestoreInput, restoreFailureOf, restoreView } = pure('src/lib/restoreCase.ts');
const { canSubmitSetup, setupProblem, unlockMessage, unlockFailureOf, UNLOCK_MESSAGE } = pure('src/lib/masterKey.ts');
const { canGo, go, initialScreen } = pure('src/lib/screenFlow.ts');
const { startView, truncateTitle, caseRow } = pure('src/lib/startScreen.ts');

const CODE = 'abcdefghijklmnopqrstuvwxy2';
const TOKEN = 'link-session-token-0123456789abcdef';

// Loads the two API modules together so a 423 raised by caseFileApi reaches the listener this test registers.
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
      return { ok: next.status >= 200 && next.status < 300, status: next.status, json: async () => next.body, headers: { get: (h) => (next.headers ? next.headers[h] ?? null : null) } };
    }
  };
  for (const name of names) Object.defineProperty(globalThis, name, { configurable: true, writable: true, value: values[name] });
  try {
    const load = makeLoader();
    return await fn({ keyApi: load('src/lib/masterKeyApi.ts'), fileApi: load('src/lib/caseFileApi.ts'), signal: load('src/lib/lockSignal.ts'), calls, items });
  } finally {
    for (const name of names) {
      if (saved[name]) Object.defineProperty(globalThis, name, saved[name]);
      else delete globalThis[name];
    }
  }
}

/* ------------------------------------------------------------ the code */

test('p04: a pasted code survives spaces, dashes, case, tabs, newlines, NBSP and zero-width characters', () => {
  const variants = [
    CODE.toUpperCase(),
    CODE.slice(0, 13) + ' ' + CODE.slice(13),
    CODE.match(/.{1,4}/g).join('-'),
    CODE.match(/.{1,5}/g).join(' '),
    '\t' + CODE + '\r\n',
    '\u00a0' + CODE + '\u00a0',
    CODE.slice(0, 5) + '\u200b' + CODE.slice(5) + '\ufeff',
    CODE.slice(0, 5) + '\u00ad' + CODE.slice(5),
    CODE.split('').join('\u200d'),
    CODE.slice(0, 10) + '\u2011' + CODE.slice(10),
  ];
  for (const v of variants) assert.equal(normaliseCode(v), CODE, JSON.stringify(v));
});

test('p04: a pasted code that is not a code never matches', () => {
  const bad = [
    '', ' ', CODE.slice(1), CODE + 'a', CODE.slice(0, 25) + '0', CODE.slice(0, 25) + '1', CODE.slice(0, 25) + '8',
    'http://127.0.0.1:8765/#case=' + CODE, CODE + '\u0000', CODE.slice(0, 5) + '/' + CODE.slice(5),
    'a'.repeat(257), (CODE + ' ').repeat(20), '\u{1F600}' + CODE, CODE.slice(0, 25) + '\u00e9',
  ];
  for (const v of bad) assert.equal(normaliseCode(v), null, JSON.stringify(v));
  assert.equal(normaliseCode(null), null);
  assert.equal(normaliseCode(undefined), null);
  assert.equal(normaliseCode(12345), null);
});

test('fixed: bidi marks copied from a chat or File Explorer stop a code from matching', () => {
  // U+202A..U+202E and U+2066..U+2069 are the direction controls Windows and chat apps wrap around copied text.
  const marks = ['\u202a', '\u202b', '\u202c', '\u202d', '\u202e', '\u2066', '\u2067', '\u2068', '\u2069', '\u061c'];
  const failing = marks.filter((m) => normaliseCode(m + CODE + '\u202c') !== CODE);
  assert.deepEqual(failing.map((m) => 'U+' + m.codePointAt(0).toString(16).toUpperCase()), [], 'pasted codes wrapped in these marks are refused');
});

test('p04: every code failure is one sentence and an abort passes through', async () => {
  const seen = new Set();
  for (const input of ['', 'nope', CODE.toUpperCase()]) {
    const out = await resolveCode(input, async () => { throw new Error('404'); });
    assert.equal(out.ok, false);
    seen.add(out.message);
  }
  assert.equal(seen.size, 1);
  assert.equal([...seen][0], CODE_NOT_FOUND);
  const ok = await resolveCode(CODE.toUpperCase(), async (id) => { assert.equal(id, CODE); return {}; });
  assert.deepEqual(ok, { ok: true, id: CODE });
  const abort = new DOMException('x', 'AbortError');
  await assert.rejects(resolveCode(CODE, async () => { throw abort; }), (e) => e === abort);
  // An id the server must never be asked about is not asked.
  let asked = 0;
  await resolveCode('short', async () => { asked++; });
  assert.equal(asked, 0);
});

/* ------------------------------------------------------- the case file */

test('p04: a restore path is cleaned of quotes and spaces, the key never is', () => {
  assert.equal(normalisePath('  "C:\\a b\\x.unravis-case"  '), 'C:\\a b\\x.unravis-case');
  assert.equal(normalisePath('" C:\\x.unravis-case "'), 'C:\\x.unravis-case');
  assert.equal(normalisePath(null), '');
  const ok = checkRestoreInput('C:\\x.UNRAVIS-CASE', ' key with spaces ');
  assert.equal(ok.ok, true);
  assert.equal(checkRestoreInput('C:\\x.unravis-case', '').ok, false);
  assert.equal(checkRestoreInput('C:\\x.unravis-case', ' ').ok, true, 'a blank-only key is sent as typed; the server decides');
  for (const p of ['', '   ', 'x.unravis-case', '\\\\host\\share\\x.unravis-case', '\\\\?\\C:\\x.unravis-case', 'C:x.unravis-case', 'C:\\x.txt', 'C:\\']) {
    assert.equal(checkRestoreInput(p, 'k').ok, false, p);
  }
});

test('fixed: a path copied with its leading direction mark (U+202A) is refused as "not a full path"', () => {
  // The Properties > Security tab and some Explorer copies put U+202A in front of the drive letter.
  const result = checkRestoreInput('\u202aC:\\Cases\\mine.unravis-case', 'key');
  assert.equal(result.ok, true, 'the form should strip invisible direction marks the same way the case code does');
});

test('fixed: the form lets a path of 521..2048 characters through, the server answers a generic 400 the card cannot explain', () => {
  const long = 'C:\\' + 'a'.repeat(600) + '.unravis-case';
  const check = checkRestoreInput(long, 'key');
  assert.equal(check.ok, false, 'the server body limit for a path is 520 characters; the form allows 2048');
  // What the card says to the 400 the server would send for it.
  const view = restoreView(restoreFailureOf({ status: 400, reason: 'bad-request' }));
  assert.notEqual(view.message, 'The case file could not be restored. Try again.');
});

test('p04: restore failures map to the right card and only a wrong key refocuses the key field', () => {
  assert.equal(restoreFailureOf({ status: 422, reason: 'bad-case-file' }).kind, 'bad-case-file');
  assert.equal(restoreFailureOf({ status: 423 }).kind, 'locked');
  assert.equal(restoreFailureOf({ status: 429, retryAfterSeconds: 7 }).retryAfterSeconds, 7);
  assert.equal(restoreFailureOf({ status: 409, reason: 'no-master-key' }).kind, 'no-key');
  assert.equal(restoreFailureOf({ status: 409, reason: 'busy' }).kind, 'busy');
  assert.equal(restoreFailureOf({ status: 0 }).kind, 'network');
  assert.equal(restoreFailureOf(undefined).kind, 'other');
  assert.equal(restoreFailureOf('boom').kind, 'other');
  assert.equal(restoreView({ kind: 'throttled', retryAfterSeconds: 0.2 }).waitSeconds, 1);
  assert.equal(restoreView({ kind: 'throttled', retryAfterSeconds: 1e9 }).waitSeconds >= 1, true);
});

/* ---------------------------------------------------------- passphrases */

test('fixed: the set-up button counts a decomposed passphrase before the server normalises it', () => {
  // Twelve code points that are six letters after NFC. The server refuses (too-short); the button is on.
  const decomposed = 'e\u0301'.repeat(6) + 'e\u0301'.repeat(0) + 'a'.repeat(0);
  const twelveCodePoints = ('e\u0301').repeat(6);
  assert.equal([...twelveCodePoints].length, 12);
  assert.equal([...twelveCodePoints.normalize('NFC')].length, 6);
  assert.equal(canSubmitSetup(twelveCodePoints, twelveCodePoints, true), false, 'the button should follow the server rule (12 characters after NFC)');
  void decomposed;
});

test('p04: 11 versus 12 characters, 1024 versus 1025 bytes, and emoji count as people count them', () => {
  assert.equal(setupProblem('a'.repeat(11), 'a'.repeat(11), true), 'too-short');
  assert.equal(setupProblem('a'.repeat(12), 'a'.repeat(12), true), null);
  assert.equal(setupProblem('\u{1F600}'.repeat(11), '\u{1F600}'.repeat(11), true), 'too-short');
  assert.equal(setupProblem('\u{1F600}'.repeat(12), '\u{1F600}'.repeat(12), true), null);
  assert.equal(setupProblem('a'.repeat(1024), 'a'.repeat(1024), true), null);
  assert.equal(setupProblem('a'.repeat(1025), 'a'.repeat(1025), true), 'too-long');
  assert.equal(setupProblem('\u{1F600}'.repeat(257), '\u{1F600}'.repeat(257), true), 'too-long', '257 emoji are 1028 bytes');
  assert.equal(setupProblem('a'.repeat(12), 'a'.repeat(12) + ' ', true), 'mismatch', 'a trailing space is a mismatch, not trimmed away');
});

test('fixed: the set-up form accepts twelve blanks or twelve invisible characters as the master key', () => {
  for (const pass of [' '.repeat(12), '\u200b'.repeat(12), '\u3000'.repeat(12)]) {
    assert.equal(canSubmitSetup(pass, pass, true), false, JSON.stringify(pass));
  }
});

test('fixed: the unlock screen blames the key when the try was never judged (429) or the server was not reached', () => {
  const network = unlockMessage(unlockFailureOf({ status: 0, reason: 'unreachable' }));
  const throttled = unlockMessage(unlockFailureOf({ status: 429, reason: 'throttled', retryAfterSeconds: 8 }));
  const damaged = unlockMessage(unlockFailureOf({ status: 401, reason: 'wrong-key' }));
  assert.equal(damaged, UNLOCK_MESSAGE);
  assert.notEqual(network, UNLOCK_MESSAGE, 'a dead server is not a wrong key');
  assert.ok(!throttled.startsWith(UNLOCK_MESSAGE), 'a try that was not evaluated is not "not the master key"');
});

test('fixed: an unlock that the server refuses for a reason other than a wrong key still says "not the master key"', () => {
  // 409 not-set (the key file was removed under a running app) and 500 both land on the wrong-key sentence, with no way out.
  for (const error of [{ status: 409, reason: 'not-set' }, { status: 500, reason: '' }, { status: 401, reason: 'no_session' }]) {
    assert.notEqual(unlockMessage(unlockFailureOf(error)), UNLOCK_MESSAGE, JSON.stringify(error));
  }
});

/* ------------------------------------------------------------ the wire */

test('p04: the passphrase is only ever in the JSON body, never in the URL or a header', async () => {
  const secret = 'p04-SECRET-passphrase-value-xyz';
  await withApi([{ status: 401, body: { reason: 'wrong-key' } }, { status: 201, body: { id: CODE, title: 't' } }, { status: 200, body: { path: 'C:\\a', name: 'a', bytes: 1 } }, { status: 200, body: { state: 'unlocked', dpapiCases: 0 } }], async ({ keyApi, fileApi, calls }) => {
    await assert.rejects(keyApi.unlockMasterKey(secret));
    await fileApi.restoreCaseFile('C:\\x.unravis-case', secret);
    await fileApi.exportCaseFile(CODE, 'C:\\x.unravis-case', secret);
    await keyApi.setupMasterKey(secret, secret);
    assert.equal(calls.length, 4);
    for (const c of calls) {
      assert.equal(c.method, 'POST');
      assert.ok(!String(c.url).includes(secret), c.url);
      assert.ok(![...c.headers.values()].some((v) => v.includes(secret)), 'a header carries the passphrase');
      assert.ok(String(c.body).includes(secret), 'the body carries it');
      assert.equal(c.headers.get('X-Audit-Token'), TOKEN);
      assert.equal(c.headers.get('Content-Type'), 'application/json');
    }
  });
});

test('p04: errors thrown by the key and file calls never carry the passphrase or the path', async () => {
  const secret = 'p04-SECRET-passphrase-value-xyz';
  const p = 'C:\\private\\folder\\x.unravis-case';
  await withApi([{ status: 422, body: { error: 'echo ' + secret, reason: 'bad-case-file' } }, { status: 0, network: true }, { status: 401, body: { error: secret, reason: 'wrong-key' } }], async ({ keyApi, fileApi }) => {
    for (const run of [() => fileApi.restoreCaseFile(p, secret), () => fileApi.restoreCaseFile(p, secret), () => keyApi.unlockMasterKey(secret)]) {
      await assert.rejects(run(), (e) => {
        const text = JSON.stringify({ m: e.message, s: e.stack, r: e.reason, n: e.name });
        assert.ok(!text.includes(secret) && !text.includes('private'), text);
        return true;
      });
    }
  });
});

test('p04: 401 wrong-key keeps the session; any other 401 forgets it; 423 raises the lock signal; 429 carries its wait', async () => {
  await withApi([
    { status: 401, body: { reason: 'wrong-key' } },
    { status: 401, body: { reason: 'wrong-key' } },
    { status: 401, body: {} },
    { status: 423, body: { reason: 'locked' } },
    { status: 429, body: { reason: 'throttled', retryAfterSeconds: 3 }, headers: { 'Retry-After': '9' } },
  ], async ({ fileApi, keyApi, signal, items }) => {
    await assert.rejects(keyApi.unlockMasterKey('x'.repeat(12)), (e) => e.reason === 'wrong-key');
    assert.equal(items.get('unravis.session'), TOKEN, 'a wrong key must not log the tab out');
    await assert.rejects(fileApi.exportCaseFile(CODE, 'C:\\x.unravis-case', 'x'), (e) => e.reason === 'wrong-key');
    assert.equal(items.get('unravis.session'), TOKEN);
    await assert.rejects(fileApi.exportCaseFile(CODE, 'C:\\x.unravis-case', 'x'), (e) => e.status === 401 && e.reason === 'no_session');
    assert.equal(items.has('unravis.session'), false, 'a refused token is forgotten');
    let locked = 0;
    signal.onCasesLocked(() => { locked++; });
    // The tab no longer has a token: the call fails before the network, so the 423 below is never seen.
    await assert.rejects(fileApi.restoreCaseFile('C:\\x.unravis-case', 'x'), (e) => e.reason === 'no_session');
    assert.equal(locked, 0);
  });
  await withApi([{ status: 423, body: { reason: 'locked' } }, { status: 429, body: { reason: 'throttled', retryAfterSeconds: 3 }, headers: { 'Retry-After': '9' } }], async ({ fileApi, signal }) => {
    let locked = 0;
    signal.onCasesLocked(() => { locked++; });
    await assert.rejects(fileApi.restoreCaseFile('C:\\x.unravis-case', 'k'), (e) => e.status === 423);
    assert.equal(locked, 1);
    await assert.rejects(fileApi.restoreCaseFile('C:\\x.unravis-case', 'k'), (e) => e.status === 429 && e.retryAfterSeconds === 9, 'the header wins over the body');
    assert.equal(locked, 1, 'a 429 is not a lock');
  });
});

test('p04: an answer that is not JSON, or is JSON of the wrong shape, is an error not a crash', async () => {
  await withApi([{ status: 201, body: null }, { status: 201, body: { id: 'not-a-case-id' } }, { status: 200, body: { state: 'weird' } }, { status: 200, body: [] }], async ({ fileApi, keyApi }) => {
    await assert.rejects(fileApi.restoreCaseFile('C:\\x.unravis-case', 'k'), (e) => e.reason === 'unreadable');
    await assert.rejects(fileApi.restoreCaseFile('C:\\x.unravis-case', 'k'), (e) => e.reason === 'unreadable');
    await assert.rejects(keyApi.getMasterKeyStatus(), (e) => e.reason === 'unreadable', 'an unknown state is never taken as unlocked');
    await assert.rejects(keyApi.getMasterKeyStatus(), (e) => e.reason === 'unreadable');
  });
});

/* ------------------------------------------------------ the sources */

const PASSPHRASE_FILES = [
  'src/lib/masterKeyApi.ts', 'src/lib/caseFileApi.ts', 'src/lib/restoreCase.ts', 'src/lib/masterKey.ts', 'src/lib/caseCode.ts',
  'src/components/MasterKeySetup.svelte', 'src/components/MasterKeyUnlock.svelte', 'src/components/EnterCase.svelte',
];

test('p04: no passphrase-handling file logs, stores, or writes to the address', () => {
  for (const f of PASSPHRASE_FILES) {
    const code = read(f).replace(/\/\*[\s\S]*?\*\//g, '').replace(/^\s*\/\/.*$/gm, '');
    assert.doesNotMatch(code, /console\.\w+/, f + ' uses the console');
    assert.doesNotMatch(code, /localStorage|sessionStorage|indexedDB|document\.cookie/, f + ' touches storage');
    assert.doesNotMatch(code, /location\.(hash|search|href)\s*=|history\.(push|replace)State/, f + ' writes to the address');
    assert.doesNotMatch(code, /new URLSearchParams\([^)]*pass/i, f + ' puts a passphrase in a query');
  }
});

test('p04: each submit clears the typed key into a local before the request is sent', () => {
  const unlock = read('src/components/MasterKeyUnlock.svelte');
  assert.match(unlock, /const attempt = pass;\s*pass = '';[\s\S]*await unlockMasterKey\(attempt\)/);
  const enter = read('src/components/EnterCase.svelte');
  assert.match(enter, /pass = ''/);
  const setup = read('src/components/MasterKeySetup.svelte');
  assert.match(setup, /pass = ''/);
  assert.match(setup, /confirm = ''/);
});

test('p04: the double-submit guards exist on every form', () => {
  assert.match(read('src/components/MasterKeyUnlock.svelte'), /if \(busy\) return;/);
  assert.match(read('src/components/MasterKeySetup.svelte'), /if \(busy \|\| !canSubmitSetup/);
  const enter = read('src/components/EnterCase.svelte');
  assert.match(enter, /if \(codeBusy\) return;/);
  assert.match(enter, /if \(busy \|\| held\) return;/);
});

// The catch block of an async submit that puts the cursor back in a field while that field is still disabled={busy}: busy is only
// cleared in `finally`, which runs after the catch, so focus() is a no-op on a disabled input and the keyboard user loses their place.
function focusRunsWhileDisabled(source, fnName, busyName) {
  const start = source.indexOf('async function ' + fnName);
  assert.ok(start >= 0, fnName + ' not found');
  const rest = source.slice(start);
  const body = rest.slice(0, rest.search(/\n  }\n/));
  const busyOn = body.search(new RegExp(busyName + '\\s*=\\s*true'));
  assert.ok(busyOn >= 0, 'no ' + busyName + ' = true in ' + fnName);
  const finallyAt = body.indexOf('finally');
  const window = body.slice(busyOn, finallyAt < 0 ? undefined : finallyAt);
  const focusAt = window.indexOf('.focus()');
  if (focusAt < 0) return false;
  return !new RegExp(busyName + '\\s*=\\s*false').test(window.slice(0, focusAt));
}

test('fixed: after a wrong key on the unlock screen the cursor is not put back (the field is still disabled)', () => {
  assert.equal(focusRunsWhileDisabled(read('src/components/MasterKeyUnlock.svelte'), 'submit', 'busy'), false);
});

test('fixed: after a code that does not open a case the cursor is not put back (the field is still disabled)', () => {
  assert.equal(focusRunsWhileDisabled(read('src/components/EnterCase.svelte'), 'openByCode', 'codeBusy'), false);
});

test('fixed: after a refused restore the cursor is not put back in the key or path field (still disabled)', () => {
  assert.equal(focusRunsWhileDisabled(read('src/components/EnterCase.svelte'), 'restore', 'busy'), false);
});

test('fixed: a double-click on Lock sends two locks and the second one resets where unlocking returns to', () => {
  const source = read('src/lib/appState.svelte.ts');
  const start = source.indexOf('export async function lockCases');
  const body = source.slice(start, source.indexOf('\n}\n', start));
  const awaitAt = body.indexOf('await lockMasterKey');
  const originAt = body.indexOf('unlockOrigin =');
  const guarded = /if \(\w*(busy|locking|inFlight)\w*\) return|screen === 'unlock'\) return|screen !== 'unlock'/.test(body);
  assert.ok(originAt > awaitAt, 'sanity: the origin is read after the await in the current code');
  assert.ok(originAt < awaitAt || guarded, 'lockCases has no re-entry guard and reads the screen after the await, so the second reply sets the origin to "start"');
});

/* ------------------------------------------------------ screens, empty */

test('p04: the screen moves forbid a way around the unlock', () => {
  assert.equal(canGo('unlock', 'setup'), false);
  assert.equal(canGo('unlock', 'create'), true, 'TSK-19: only so a case being made can carry on after the key is typed (afterUnlock decides who gets there)');
  assert.equal(canGo('loading', 'setup'), false);
  assert.equal(canGo('setup', 'running'), false, 'set-up does not jump into the analysis');
  assert.equal(go('unlock', 'create').ok, true);
  assert.equal(canGo('running', 'unlock'), true);
  assert.equal(canGo('start', 'unlock'), true);
  for (const lock of ['locked']) {
    for (const deepLink of [true, false]) for (const resume of [true, false]) {
      assert.equal(initialScreen({ demo: false, deepLink, resume, lock }), 'unlock', 'a locked key comes before a deep link or a resume');
    }
  }
  assert.equal(initialScreen({ demo: false, deepLink: false, resume: false, lock: 'unknown' }), 'start');
  assert.equal(initialScreen({ demo: true, deepLink: false, resume: false, lock: 'locked' }), 'running');
});

test('p04: the start view shows nothing from the cases while locked, even if a list is passed in', () => {
  const view = startView('locked', [{ id: CODE, title: 'SECRET TITLE', created: '2026-01-01T00:00:00Z', closed: null, imports: 3 }]);
  assert.equal(view.locked, true);
  assert.deepEqual(view.rows, []);
  assert.deepEqual(view.cards, []);
  assert.doesNotMatch(JSON.stringify(view), /SECRET/);
});

test('p04: empty, huge and odd case lists make a sane start screen', () => {
  const empty = startView('unlocked', []);
  assert.ok(empty.empty.length > 0);
  assert.equal(empty.rows.length, 0);
  assert.equal(startView('none', []).offerSetup, true);
  assert.equal(startView('unlocked', []).offerSetup, false);
  assert.equal(startView(null, []).offerSetup, false);
  const many = Array.from({ length: 500 }, (_, i) => ({ id: CODE, title: 'case ' + i, created: '2026-01-01T00:00:00Z', closed: null, imports: 1 }));
  assert.equal(startView('unlocked', many).rows.length, 50);
  assert.equal(startView('unlocked', many, 0).rows.length, 50);
  assert.equal(startView('unlocked', many, NaN).rows.length, 50);
  assert.equal(startView('unlocked', many, 3).rows.length, 150);
  const odd = caseRow({ id: CODE, title: '   ', created: 'not a date', closed: null, imports: NaN });
  assert.ok(odd.title.length > 0 && odd.counts.length > 0);
  const emoji = truncateTitle('\u{1F600}'.repeat(200));
  assert.equal([...emoji].length, 60);
  assert.doesNotMatch(emoji, /[\ud800-\udbff](?![\udc00-\udfff])|(?<![\ud800-\udbff])[\udc00-\udfff]/, 'no lone surrogate');
  assert.equal(truncateTitle('a\n\n\t b'), 'a b');
  const markup = truncateTitle('<img src=x onerror=alert(1)>');
  assert.equal(markup, '<img src=x onerror=alert(1)>', 'cut, never interpreted: the screen renders text, not HTML');
});

test('p04: the start screen and cards render titles as text, never as HTML', () => {
  for (const f of ['src/components/StartScreen.svelte', 'src/components/EnterCase.svelte', 'src/components/MasterKeyUnlock.svelte', 'src/components/MasterKeySetup.svelte']) {
    assert.doesNotMatch(read(f), /\{@html/, f);
  }
});
