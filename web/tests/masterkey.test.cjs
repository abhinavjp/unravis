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
  const output = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022, esModuleInterop: true } }).outputText;
  const module = { exports: {} };
  const nodeRequire = createRequire(filename);
  const load = (request) => /^\.\.?\//.test(request) && fs.existsSync(path.join(path.dirname(filename), request + '.ts'))
    ? loadTypeScript(path.relative(root, path.join(path.dirname(filename), request + '.ts')))
    : nodeRequire(request);
  new Function('require', 'module', 'exports', output)(load, module, module.exports);
  return module.exports;
}
const read = (relative) => fs.readFileSync(path.join(root, relative), 'utf8');

const masterKey = loadTypeScript('src/lib/masterKey.ts');
const { canSubmitSetup, setupProblem, unlockMessage, unlockFailureOf, setupFailureOf, strengthHint, parseStatus, characterKinds, offersSetup, offersLock, UNLOCK_MESSAGE, MIN_PASSPHRASE_CHARS } = masterKey;
const { masterKeyLintEntries, KEY_TEXT, setupErrorText, rewrapOffer, rewrapAction, rewrapDone } = loadTypeScript('src/lib/copy/masterKeyCopy.ts');
const { lintScreenStrings } = loadTypeScript('src/lib/copy/copylint.ts');
const { deck } = loadTypeScript('src/lib/copy/deck.ts');

const TOKEN = 'link-session-token-0123456789abcdef';
const PASS = 'correct horse battery staple';

// Loads masterKeyApi with stand-ins for the browser globals api.ts reads at load time, like the idiom in session.test.cjs.
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
      return { ok: next.status >= 200 && next.status < 300, status: next.status, json: async () => next.body, headers: { get: () => null } };
    }
  };
  for (const name of names) Object.defineProperty(globalThis, name, { configurable: true, writable: true, value: values[name] });
  try {
    return await fn({ api: loadTypeScript('src/lib/masterKeyApi.ts'), calls, items });
  } finally {
    for (const name of names) {
      if (saved[name]) Object.defineProperty(globalThis, name, saved[name]);
      else delete globalThis[name];
    }
  }
}

test('the set-up button needs 12 characters, a match and the tick', () => {
  const twelve = 'abcdefghijkl';
  assert.equal(canSubmitSetup(twelve, twelve, true), true);
  assert.equal(canSubmitSetup(twelve.slice(0, 11), twelve.slice(0, 11), true), false, '11 characters');
  assert.equal(canSubmitSetup(twelve, twelve + 'x', true), false, 'no match');
  assert.equal(canSubmitSetup(twelve, twelve, false), false, 'not ticked');
  assert.equal(canSubmitSetup('', '', true), false, 'empty');
  assert.equal(canSubmitSetup(twelve, '', true), false, 'second entry empty');
  assert.equal(MIN_PASSPHRASE_CHARS, 12);
});

test('the set-up gate counts characters as a person does and stops at 1024 bytes', () => {
  const emoji = '\u{1F511}'.repeat(12);
  assert.equal(canSubmitSetup(emoji, emoji, true), true, '12 emoji are 12 characters');
  const big = 'a'.repeat(1025);
  assert.equal(setupProblem(big, big, true), 'too-long');
  const edge = 'a'.repeat(1024);
  assert.equal(setupProblem(edge, edge, true), null);
});

test('the set-up problem names the first missed rule', () => {
  assert.equal(setupProblem('short', 'short', true), 'too-short');
  assert.equal(setupProblem('abcdefghijkl', 'abcdefghijkm', true), 'mismatch');
  assert.equal(setupProblem('abcdefghijkl', 'abcdefghijkl', false), 'acknowledgement-required');
  assert.equal(setupProblem('abcdefghijkl', 'abcdefghijkl', true), null);
});

test('only a refused key says "not the master key"; a throttled try, a dead network and a server problem say what happened', () => {
  const wrong = unlockMessage(unlockFailureOf({ status: 401, reason: 'wrong-key' }));
  const network = unlockMessage(unlockFailureOf({ status: 0, reason: 'unreachable' }));
  const other = unlockMessage(unlockFailureOf(new Error('boom')));
  const throttled = unlockMessage(unlockFailureOf({ status: 429, reason: 'throttled', retryAfterSeconds: 8 }));
  const notSet = unlockMessage(unlockFailureOf({ status: 409, reason: 'not-set' }));
  const conflict = unlockMessage(unlockFailureOf({ status: 409, reason: 'busy' }));
  const locked = unlockMessage(unlockFailureOf({ status: 423, reason: 'locked' }));
  const server = unlockMessage(unlockFailureOf({ status: 500, reason: '' }));
  assert.equal(wrong, 'That is not the master key for this PC.');
  assert.equal(wrong, UNLOCK_MESSAGE);
  const rest = { network, other, throttled, notSet, conflict, locked, server };
  for (const [name, text] of Object.entries(rest)) {
    assert.ok(text.length > 0 && !text.includes('not the master key'), name + ' must not blame the key');
  }
  assert.equal(new Set([wrong, ...Object.values(rest)]).size, 8, 'each failure has its own words');
  assert.match(throttled, /8 seconds/);
  assert.match(network, /could not be reached/);
});

test('only a 401 with the wrong-key reason is a wrong key', () => {
  assert.equal(unlockFailureOf({ status: 401, reason: 'wrong-key' }).kind, 'wrong-key');
  assert.notEqual(unlockFailureOf({ status: 401, reason: 'no_session' }).kind, 'wrong-key');
  assert.notEqual(unlockFailureOf({ status: 401 }).kind, 'wrong-key');
  assert.equal(unlockFailureOf({ status: 409, reason: 'not-set' }).kind, 'not-set');
  assert.equal(unlockFailureOf({ status: 409, reason: 'anything' }).kind, 'conflict');
  assert.equal(unlockFailureOf({ status: 423 }).kind, 'locked');
  assert.equal(unlockFailureOf({ status: 503 }).kind, 'server');
});

test('a throttled answer without a wait time still gets a wait message', () => {
  const failure = unlockFailureOf({ status: 429 });
  assert.equal(failure.kind, 'throttled');
  assert.ok(failure.retryAfterSeconds > 0);
  assert.match(unlockMessage(failure), /Try again in \d+ seconds?\./);
});

test('the unlock message for a refused key never says which part was wrong', () => {
  assert.doesNotMatch(unlockMessage({ kind: 'wrong-key' }), /username|user name|account|password is|no such|does not exist|unreachable|offline|network/i);
});

test('the strength hint talks about length and mix, and never claims safety', () => {
  const samples = ['', 'a', 'abcdefgh', 'abcdefghijkl', 'Abcdefghijkl1', 'Correct-Horse-Battery-9', 'aaaaaaaaaaaaaaaaaaaaaaaa', 'P@ssw0rd!P@ssw0rd!', '\u{1F511}'.repeat(20)];
  for (const sample of samples) {
    const hint = strengthHint(sample);
    assert.ok(hint.trim().length > 0);
    assert.doesNotMatch(hint, /secure|safe|strong|unbreakable|uncrackable|protected|guarantee|hack/i, JSON.stringify(sample));
  }
  assert.match(strengthHint(''), /12/);
  assert.match(strengthHint('abc'), /9 more/);
  assert.equal(characterKinds('abcDEF12!'), 4);
  assert.equal(characterKinds('abc'), 1);
});

test('no screen copy for the key promises safety', () => {
  for (const [id, text] of Object.entries(KEY_TEXT)) assert.doesNotMatch(text, /\bsecure\b|\bunbreakable\b|\bhack-?proof\b/i, id);
  assert.doesNotMatch(strengthHint('Correct-Horse-Battery-9'), /secure/i);
});

test('the copy says what the key protects, that it is not sent, that it is asked once, and that nobody can recover it', () => {
  assert.match(KEY_TEXT.protects, /cases/i);
  assert.match(KEY_TEXT.notSent, /not sent|never sent|stays on/i);
  assert.match(KEY_TEXT.noRecovery, /recover|reset/i);
  assert.match(KEY_TEXT.setupIntro + ' ' + KEY_TEXT.unlockIntro, /once/i);
  assert.match(KEY_TEXT.ackLabel, /^I understand that if I forget this key, my cases cannot be recovered\.$/);
});

test('set-up failures keep the server reason, and throttling keeps its wait', () => {
  assert.equal(setupFailureOf({ status: 400, reason: 'too-short' }).kind, 'too-short');
  assert.equal(setupFailureOf({ status: 409, reason: 'already-set' }).kind, 'already-set');
  assert.equal(setupFailureOf({ status: 409, reason: 'setup-incomplete' }).kind, 'incomplete');
  assert.equal(setupFailureOf({ status: 0 }).kind, 'network');
  assert.deepEqual(setupFailureOf({ status: 429, retryAfterSeconds: 6 }), { kind: 'throttled', retryAfterSeconds: 6 });
  assert.equal(setupFailureOf({ status: 500 }).kind, 'other');
  for (const kind of ['too-short', 'too-long', 'mismatch', 'acknowledgement-required', 'already-set', 'network', 'other']) assert.ok(setupErrorText(kind).trim().length > 0, kind);
});

test('status answers are read strictly, and a strange answer is never "unlocked"', () => {
  assert.deepEqual(parseStatus({ state: 'unlocked', dpapiCases: 3 }), { state: 'unlocked', dpapiCases: 3 });
  assert.deepEqual(parseStatus({ state: 'none' }), { state: 'none', dpapiCases: 0 });
  assert.equal(parseStatus({ state: 'open' }), null);
  assert.equal(parseStatus(null), null);
  assert.equal(parseStatus('unlocked'), null);
  assert.equal(offersSetup('none'), true);
  assert.equal(offersSetup('locked'), false);
  assert.equal(offersLock('unlocked'), true);
  assert.equal(offersLock('none'), false);
  assert.equal(offersLock('unknown'), false);
});

test('the rewrap offer states the count', () => {
  assert.match(rewrapOffer(3), /3/);
  assert.match(rewrapAction(3), /3/);
  assert.match(rewrapOffer(1), /1 /);
  assert.ok(rewrapDone(2, 1).includes('2'));
});

test('the passphrase goes only in the setup and unlock bodies, never a URL or header', async () => {
  await withApi([{ status: 200, body: { state: 'unlocked', dpapiCases: 2 } }, { status: 200, body: { state: 'unlocked', dpapiCases: 0 } }], async ({ api, calls }) => {
    const set = await api.setupMasterKey(PASS, PASS);
    assert.deepEqual(set, { state: 'unlocked', dpapiCases: 2 });
    await api.unlockMasterKey(PASS);
    assert.equal(calls[0].url, '/api/master-key/setup');
    assert.deepEqual(JSON.parse(calls[0].body), { passphrase: PASS, confirm: PASS, acknowledged: true });
    assert.deepEqual(JSON.parse(calls[1].body), { passphrase: PASS });
    for (const call of calls) {
      assert.equal(call.method, 'POST');
      assert.equal(call.headers.get('X-Audit-Token'), TOKEN);
      assert.ok(!call.url.includes(PASS) && !call.url.includes('?'));
      for (const [name, value] of call.headers) assert.ok(!value.includes(PASS), name);
    }
  });
});

test('status, lock and rewrap send no passphrase and use the right routes', async () => {
  const results = [{ caseId: 'a', status: 'rewrapped' }, { caseId: 'b', status: 'already' }, { caseId: 'c', status: 'failed', reason: 'io' }];
  await withApi([
    { status: 200, body: { state: 'locked', dpapiCases: 1 } },
    { status: 200, body: { state: 'locked', dpapiCases: 1 } },
    { status: 200, body: { results } }
  ], async ({ api, calls }) => {
    assert.deepEqual(await api.getMasterKeyStatus(), { state: 'locked', dpapiCases: 1 });
    await api.lockMasterKey();
    const out = await api.rewrapCases({ all: true });
    assert.deepEqual(calls.map((c) => c.method + ' ' + c.url), ['GET /api/master-key/status', 'POST /api/master-key/lock', 'POST /api/master-key/rewrap']);
    assert.equal(calls[0].body, undefined);
    assert.deepEqual(JSON.parse(calls[2].body), { all: true });
    assert.deepEqual(api.rewrapCounts(out), { done: 1, failed: 1 });
  });
});

test('a wrong key does not forget the session, and the error never holds the passphrase', async () => {
  await withApi([{ status: 401, body: { reason: 'wrong-key' } }, { status: 429, body: { reason: 'throttled', retryAfterSeconds: 7 } }, { network: true }], async ({ api, items }) => {
    const errors = [];
    for (let i = 0; i < 3; i++) {
      try { await api.unlockMasterKey(PASS); assert.fail('expected a refusal'); } catch (error) { if (error.code === 'ERR_ASSERTION') throw error; errors.push(error); }
    }
    assert.equal(items.get('unravis.session'), TOKEN, 'a 401 on unlock keeps the session');
    assert.deepEqual(errors.map((e) => [e.status, e.reason]), [[401, 'wrong-key'], [429, 'throttled'], [0, 'unreachable']]);
    assert.equal(errors[1].retryAfterSeconds, 7);
    for (const error of errors) {
      assert.ok(!(error.message + error.stack + JSON.stringify(error)).includes(PASS), 'the error carries no passphrase');
    }
    assert.deepEqual(errors.map((e) => unlockMessage(unlockFailureOf(e))).map((m) => m.startsWith(UNLOCK_MESSAGE)), [true, false, false], 'only the refused key blames the key');
  });
});

test('the passphrase is absent from any serialised state, the page source and storage calls', () => {
  // The only holders are local component variables, cleared on submit.
  const unlock = read('src/components/MasterKeyUnlock.svelte');
  const setup = read('src/components/MasterKeySetup.svelte');
  assert.match(unlock, /const attempt = pass;\s*pass = '';/);
  assert.match(setup, /const first = pass, second = confirm;\s*pass = ''; confirm = ''; ack = false;/);
  for (const [name, src] of [['unlock', unlock], ['setup', setup]]) {
    assert.match(src, /type="password"/, name);
    assert.match(src, /autocomplete="off"/, name);
    assert.doesNotMatch(src, /localStorage|sessionStorage|console\.|document\.cookie|location\.|history\.|JSON\.stringify|>\s*\{(?:pass|confirm|first|second|attempt)\}\s*</, name);
  }
  for (const file of ['src/lib/masterKey.ts', 'src/lib/masterKeyApi.ts', 'src/lib/copy/masterKeyCopy.ts']) {
    assert.doesNotMatch(read(file), /localStorage|console\.|document\.cookie/, file);
  }
  const state = read('src/lib/appState.svelte.ts');
  assert.doesNotMatch(state, /passphrase|\bpass\b/i);
});

test('the unlock screen has one field and one button, submits on Enter, and the setup gate uses canSubmitSetup', () => {
  const unlock = read('src/components/MasterKeyUnlock.svelte');
  assert.equal((unlock.match(/<input /g) ?? []).length, 1);
  assert.equal((unlock.match(/<button /g) ?? []).length, 1);
  assert.match(unlock, /<form onsubmit=\{submit\}>/);
  assert.match(unlock, /type="submit"/);
  assert.match(unlock, /<HelpNote id="unlock" \/>/);
  const setup = read('src/components/MasterKeySetup.svelte');
  assert.match(setup, /disabled=\{!ready \|\| busy\}/);
  assert.match(setup, /canSubmitSetup\(pass, confirm, ack\)/);
  assert.match(setup, /type="checkbox"/);
  assert.match(setup, /<HelpNote id="masterkey" \/>/);
});

test('the header offers Lock only while the key is open, and the case routes hand 423 to the unlock screen', () => {
  const header = read('src/components/AppHeader.svelte');
  assert.match(header, /offersLock\(app\.lockState\)/);
  assert.match(header, /lockCases/);
  const cases = read('src/lib/cases.ts');
  assert.match(cases, /await signalIfAppLocked\(response\);/);
});

test('master key copy passes the copy lint', () => {
  assert.deepEqual(lintScreenStrings(masterKeyLintEntries(), deck.terms).map((f) => f.id + ': ' + f.message), []);
});
