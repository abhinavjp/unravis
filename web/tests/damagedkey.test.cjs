const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

// The damaged master key file: status, failure mappings, the confirm gate of the start-over, and the screens that route to it.
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

const mk = loadTypeScript('src/lib/masterKey.ts');
const copy = loadTypeScript('src/lib/copy/masterKeyCopy.ts');
const enter = loadTypeScript('src/lib/copy/enterCopy.ts');
const restore = loadTypeScript('src/lib/restoreCase.ts');
const flow = loadTypeScript('src/lib/screenFlow.ts');
const startScreen = loadTypeScript('src/lib/startScreen.ts');
const { lintScreenStrings } = loadTypeScript('src/lib/copy/copylint.ts');
const { deck } = loadTypeScript('src/lib/copy/deck.ts');

const TOKEN = 'link-session-token-0123456789abcdef';

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
    return await fn({ api: loadTypeScript('src/lib/masterKeyApi.ts'), calls });
  } finally {
    for (const name of names) {
      if (saved[name]) Object.defineProperty(globalThis, name, saved[name]);
      else delete globalThis[name];
    }
  }
}

test('damaged: the status answer is accepted, and closes the cases like locked', () => {
  assert.deepEqual(mk.parseStatus({ state: 'damaged', dpapiCases: 0 }), { state: 'damaged', dpapiCases: 0 });
  assert.equal(mk.parseStatus({ state: 'broken', dpapiCases: 0 }), null, 'an unknown state is still not a status');
  assert.equal(mk.closesCases('damaged'), true);
  assert.equal(mk.closesCases('locked'), true);
  for (const s of ['none', 'unlocked', 'unknown', null]) assert.equal(mk.closesCases(s), false, String(s));
  assert.equal(mk.offersSetup('damaged'), false, 'no set-up link over a damaged file');
  assert.equal(mk.offersLock('damaged'), false);
});

test('damaged: M12 count (freed archives) is kept only when above 0, and the warning reads right', () => {
  assert.deepEqual(mk.parseStatus({ state: 'damaged', dpapiCases: 0, freedArchives: 2 }), { state: 'damaged', dpapiCases: 0, freedArchives: 2 });
  assert.deepEqual(mk.parseStatus({ state: 'damaged', dpapiCases: 0, freedArchives: 0 }), { state: 'damaged', dpapiCases: 0 });
  assert.deepEqual(mk.parseStatus({ state: 'damaged', dpapiCases: 0, freedArchives: 'x' }), { state: 'damaged', dpapiCases: 0 });
  assert.equal(copy.freedArchivesWarning(1), '1 archived case can only be opened with the old key. Their working copies were freed.');
  assert.equal(copy.freedArchivesWarning(2), '2 archived cases can only be opened with the old key. Their working copies were freed.');
  const view = read('src/components/MasterKeyDamaged.svelte');
  assert.match(view, /freedArchivesWarning\(freed\)/);
  assert.match(view, /getMasterKeyStatus/);
});

test('damaged: the first screen is unlock, and the start screen shows nothing from the cases', () => {
  assert.equal(flow.initialScreen({ demo: false, deepLink: false, resume: false, lock: 'damaged' }), 'unlock');
  assert.equal(flow.initialScreen({ demo: false, deepLink: true, resume: true, lock: 'damaged' }), 'unlock');
  assert.equal(flow.initialScreen({ demo: true, deepLink: false, resume: false, lock: 'damaged' }), 'running', 'the demo has no key');
  const view = startScreen.startView('damaged', [{ id: 'a', title: 'Secret case', created: 1, closed: false, imports: 1 }]);
  assert.equal(view.locked, true);
  assert.deepEqual([view.cards.length, view.rows.length, view.total, view.offerSetup], [0, 0, 0, false]);
});

test('damaged: unlock cannot move straight to setup, so the way on is unlock -> start -> setup', () => {
  assert.equal(flow.canGo('unlock', 'setup'), false);
  assert.equal(flow.canGo('unlock', 'start'), true);
  assert.equal(flow.canGo('start', 'setup'), true);
  assert.equal(flow.canGo('setup', 'unlock'), true, 'a damaged answer to set-up can send the person to the damaged screen');
  const src = read('src/lib/appState.svelte.ts');
  assert.match(src, /export function finishReset\(status: KeyStatus\) \{[^}]*go\('start'\);\s*go\('setup'\);/);
});

test('damaged: a 409 key-file-damaged from unlock is its own message, never the wrong-key one', () => {
  const failure = mk.unlockFailureOf({ status: 409, reason: 'key-file-damaged' });
  assert.deepEqual(failure, { kind: 'damaged' });
  const text = mk.unlockMessage(failure);
  assert.match(text, /damaged/);
  assert.match(text, /Nothing was changed/);
  assert.notEqual(text, mk.UNLOCK_MESSAGE);
  assert.doesNotMatch(text, /not the master key/i);
  // The reason wins over the status.
  assert.equal(mk.unlockFailureOf({ status: 401, reason: 'key-file-damaged' }).kind, 'damaged');
  // A 409 for something else is still the conflict message.
  assert.equal(mk.unlockFailureOf({ status: 409, reason: 'not-set' }).kind, 'not-set');
});

test('damaged: set-up maps 409 key-file-damaged, 400 blank and 400 invalid-characters, each to its own text', () => {
  const cases = [
    [{ status: 409, reason: 'key-file-damaged' }, 'damaged'],
    [{ status: 400, reason: 'blank' }, 'blank'],
    [{ status: 400, reason: 'invalid-characters' }, 'invalid-characters']
  ];
  const texts = new Set();
  for (const [error, kind] of cases) {
    const failure = mk.setupFailureOf(error);
    assert.equal(failure.kind, kind);
    const text = copy.setupErrorText(failure.kind);
    assert.ok(text.length > 20 && !/could not be set\. Try again/.test(text), kind + ' has its own wording');
    texts.add(text);
  }
  assert.equal(texts.size, 3);
  assert.match(copy.setupErrorText('blank'), /8 characters/);
  assert.match(copy.setupErrorText('blank'), /spaces or invisible/);
  assert.match(copy.setupErrorText('invalid-characters'), /cannot be saved/);
  assert.match(copy.setupErrorText('damaged'), /damaged/);
});

test('damaged: the client checks mirror the server (12 characters in all, 8 that show, no broken text)', () => {
  const ok = (pass) => mk.setupProblem(pass, pass, true);
  assert.equal(ok('a b c d e f g h'), null, '8 visible characters in 15 is enough');
  assert.equal(ok(' '.repeat(12)), 'blank');
  assert.equal(ok('\u200b'.repeat(12)), 'blank');
  assert.equal(ok('\u3000'.repeat(12)), 'blank');
  assert.equal(ok('\u202e'.repeat(12)), 'blank', 'bidi marks show nothing');
  assert.equal(ok('abcdefg' + ' '.repeat(10)), 'blank', 'seven visible characters');
  assert.equal(ok('short'), 'too-short');
  assert.equal(ok('abcdefghijk\ud800'), 'invalid-characters', 'a lone high surrogate');
  assert.equal(ok('abcdefghijk\ude00'), 'invalid-characters', 'a lone low surrogate');
  assert.equal(ok('abcdefghijk\ufffd'), 'invalid-characters', 'the replacement character');
  assert.equal(ok('abcdefgh\ud83d\ude00\ud83d\ude00\ud83d\ude00\ud83d\ude00'), null, 'a real pair is fine');
  assert.equal(mk.setupProblem('abcdefghijkl', 'abcdefghijkx', true), 'mismatch');
  assert.equal(mk.setupProblem('abcdefghijkl', 'abcdefghijkl', false), 'acknowledgement-required');
  assert.equal(mk.visibleCount('a b'), 2);
  assert.match(mk.strengthHint(' '.repeat(14)), /not spaces or invisible/);
});

test('damaged: restore maps 400 path-too-long to the path message with the cursor on the path', () => {
  const failure = restore.restoreFailureOf({ status: 400, reason: 'path-too-long' });
  assert.equal(failure.kind, 'path-too-long');
  const view = restore.restoreView(failure);
  assert.equal(view.message, enter.ENTER_TEXT.pathTooLong);
  assert.equal(view.focus, 'path');
  // A plain 400 is still the generic one.
  assert.equal(restore.restoreFailureOf({ status: 400, reason: 'bad-request' }).kind, 'bad-request');
});

test('damaged: a 423 on restore reads differently while the key file is damaged', () => {
  const failure = restore.restoreFailureOf({ status: 423, reason: 'locked' });
  assert.equal(failure.kind, 'locked');
  assert.equal(restore.restoreView(failure).message, enter.ENTER_TEXT.locked);
  assert.equal(restore.restoreView(failure, 'locked').message, enter.ENTER_TEXT.locked);
  assert.equal(restore.restoreView(failure, 'damaged').message, enter.ENTER_TEXT.damaged);
  assert.match(enter.ENTER_TEXT.damaged, /damaged/);
  assert.doesNotMatch(enter.ENTER_TEXT.damaged, /Unlock them/);
  assert.equal(restore.restoreNeedsKey('damaged'), false, 'a damaged file is not the same as no key');
  assert.match(read('src/components/EnterCase.svelte'), /restoreView\(restoreFailureOf\(error\), app\.lockState\)/);
});

test('damaged: each start-over failure maps to its own message', () => {
  const table = [
    [{ status: 409, reason: 'not-damaged' }, 'not-damaged'],
    [{ status: 400, reason: 'acknowledgement-required' }, 'acknowledgement-required'],
    [{ status: 500, reason: 'reset-failed' }, 'failed'],
    [{ status: 503, reason: 'master-key-unavailable' }, 'unavailable'],
    [{ status: 0, reason: 'unreachable' }, 'network'],
    [{ status: 418, reason: '' }, 'other']
  ];
  const texts = new Set();
  for (const [error, kind] of table) {
    assert.equal(mk.resetFailureOf(error).kind, kind);
    texts.add(copy.resetErrorText(kind));
  }
  assert.equal(texts.size, table.length);
  assert.equal(mk.resetFailureOf(null).kind, 'other');
});

test('damaged: the start-over sends nothing without the box, and one POST with it', async () => {
  assert.equal(mk.canReset(false), false);
  assert.equal(mk.canReset(true), true);
  await withApi([], async ({ api, calls }) => {
    for (const bad of [false, undefined, null, 0, 'yes', 1]) {
      await assert.rejects(() => api.resetDamagedKey(bad), (e) => e.reason === 'acknowledgement-required' && e.status === 0);
    }
    assert.equal(calls.length, 0, 'no request without the tick');
  });
  await withApi([{ status: 200, body: { state: 'none', dpapiCases: 2, keptAs: 'masterkey.dat.damaged-20261025T101500Z' } }], async ({ api, calls }) => {
    const result = await api.resetDamagedKey(true);
    assert.equal(calls.length, 1);
    assert.equal(calls[0].method, 'POST');
    assert.equal(calls[0].url, '/api/master-key/reset-damaged');
    assert.deepEqual(JSON.parse(calls[0].body), { acknowledged: true });
    assert.equal(calls[0].headers.get('X-Audit-Token'), TOKEN);
    assert.deepEqual(result, { status: { state: 'none', dpapiCases: 2 }, keptAs: 'masterkey.dat.damaged-20261025T101500Z' });
  });
});

test('damaged: only a plain file name is kept from the answer, never a path', async () => {
  for (const keptAs of ['C:\\Users\\me\\masterkey.dat.damaged-1', '/home/me/x', '../x', 'a b', '', 'x'.repeat(200), 42, null]) {
    await withApi([{ status: 200, body: { state: 'none', dpapiCases: 0, keptAs } }], async ({ api }) => {
      const result = await api.resetDamagedKey(true);
      assert.equal('keptAs' in result, false, JSON.stringify(keptAs));
      assert.equal(result.status.state, 'none');
    });
  }
});

test('damaged: a refused start-over carries its reason; the answer must still be a status', async () => {
  await withApi([{ status: 409, body: { reason: 'not-damaged', state: 'locked' } }], async ({ api }) => {
    await assert.rejects(() => api.resetDamagedKey(true), (e) => e.status === 409 && e.reason === 'not-damaged' && mk.resetFailureOf(e).kind === 'not-damaged');
  });
  await withApi([{ status: 200, body: { nonsense: true } }], async ({ api }) => {
    await assert.rejects(() => api.resetDamagedKey(true), (e) => e.reason === 'unreadable');
  });
  await withApi([{ network: true }], async ({ api }) => {
    await assert.rejects(() => api.resetDamagedKey(true), (e) => mk.resetFailureOf(e).kind === 'network');
  });
});

test('damaged: the screen warns, gates the button on the box, and keeps focus and alerts accessible', () => {
  const src = read('src/components/MasterKeyDamaged.svelte');
  assert.match(src, /type="checkbox"[^>]*bind:checked=\{ack\}/);
  assert.match(src, /disabled=\{!ready\}/);
  assert.match(src, /const ready = \$derived\(canReset\(ack\) && !busy\)/);
  assert.match(src, /if \(busy \|\| !canReset\(ack\)\) return;/);
  assert.match(src, /resetDamagedKey\(true\)/);
  assert.match(src, /role="alert"/);
  assert.match(src, /<h2[^>]*tabindex="-1"/);
  assert.match(src, /title\?\.focus\(\)/);
  assert.match(src, /box\?\.focus\(\)/);
  assert.match(src, /busy = false;\s*await tick\(\);\s*box\?\.focus\(\)/, 'busy is cleared before the focus returns');
  assert.doesNotMatch(src, /\{@html/);
  assert.doesNotMatch(src, /localStorage|sessionStorage|console\./);
  // The warning and the box label are what the person reads before the tick.
  assert.match(copy.DAMAGED_TEXT.warning, /stay unreadable/);
  assert.match(copy.DAMAGED_TEXT.ackLabel, /stay unreadable/);
  assert.match(copy.DAMAGED_TEXT.unchanged, /Nothing was changed/);
  assert.match(copy.DAMAGED_TEXT.keptAside, /kept aside, not deleted/);
  assert.equal(copy.DAMAGED_TEXT.reset, 'Start over with a new master key');
  // The new status is held until Continue, which hands it to the app.
  assert.match(src, /function carryOn\(\) \{\s*if \(result\) finishReset\(result\);/);
});

test('damaged: the unlock screen routes to it, and the unlock and set-up screens react to a damaged answer', () => {
  const landing = read('src/components/Landing.svelte');
  assert.match(landing, /app\.lockState === 'damaged'\}<MasterKeyDamaged \/>\{:else\}<MasterKeyUnlock \/>/);
  const unlock = read('src/components/MasterKeyUnlock.svelte');
  assert.match(unlock, /failure\.kind === 'damaged'\) noteKeyStatus\(\{ state: 'damaged', dpapiCases: 0 \}\)/);
  assert.match(unlock, /status\.state === 'damaged'\) noteKeyStatus\(status\)/);
  assert.equal((unlock.match(/<input /g) || []).length, 1);
  assert.equal((unlock.match(/<button /g) || []).length, 1);
  const setup = read('src/components/MasterKeySetup.svelte');
  assert.match(setup, /failure\.kind === 'damaged'\) \{[^}]*noteKeyStatus\(\{ state: 'damaged', dpapiCases: 0 \}\);\s*go\('unlock'\);/);
  const app = read('src/lib/appState.svelte.ts');
  assert.match(app, /if \(lockState !== 'damaged'\) lockState = 'locked';/, 'a 423 does not hide the damaged state');
});

test('damaged: every new sentence passes the copy lint and none holds a key or a path', () => {
  assert.deepEqual(lintScreenStrings(copy.masterKeyLintEntries(), deck.terms).map((f) => f.id + ': ' + f.message), []);
  assert.deepEqual(lintScreenStrings(enter.enterLintEntries(), deck.terms).map((f) => f.id + ': ' + f.message), []);
  const all = [...Object.values(copy.DAMAGED_TEXT), ...['acknowledgement-required', 'not-damaged', 'failed', 'unavailable', 'network', 'other'].map((k) => copy.resetErrorText(k))];
  for (const text of all) assert.doesNotMatch(text, /[A-Za-z]:\\|\/Users\/|masterkey\.dat/i, text);
});

test('damaged: the status carries what a start-over would do, and the screen says it before the tick (M12)', () => {
  const status = mk.parseStatus({ state: 'damaged', dpapiCases: 0, reset: { masterKeyCases: 2, ownKeyCases: 1, archiveOnly: 3 } });
  assert.deepEqual(status.reset, { masterKeyCases: 2, ownKeyCases: 1, archiveOnly: 3 });
  assert.equal(mk.parseStatus({ state: 'unlocked', dpapiCases: 0 }).reset, undefined, 'no block, no counts');
  assert.deepEqual(mk.parseStatus({ state: 'damaged', reset: { masterKeyCases: -4, ownKeyCases: 'x', archiveOnly: 2.9 } }).reset, { masterKeyCases: 0, ownKeyCases: 0, archiveOnly: 2 });
  assert.deepEqual(copy.resetImpactLines(null), []);
  assert.deepEqual(copy.resetImpactLines({ masterKeyCases: 0, ownKeyCases: 0, archiveOnly: 0 }), []);
  const lines = copy.resetImpactLines({ masterKeyCases: 2, ownKeyCases: 1, archiveOnly: 3 });
  assert.equal(lines.length, 3);
  assert.match(lines[0], /^2 cases .* old master key .* unreadable/);
  assert.match(lines[1], /^1 case with its own key is not affected/);
  assert.equal(lines[2], '3 archived cases can only be opened with the old key.');
  assert.equal(copy.archiveOnlyText(1), '1 archived case can only be opened with the old key.');
  // A case with its own key is never counted among the ones that stay unreadable.
  assert.doesNotMatch(copy.resetImpactLines({ masterKeyCases: 0, ownKeyCases: 2, archiveOnly: 0 }).join(' '), /unreadable/);
  const src = read('src/components/MasterKeyDamaged.svelte');
  assert.match(src, /impact = resetImpactLines\(status\.reset \? \{ \.\.\.status\.reset, archiveOnly: 0 \} : null\)/, 'the M12 line is said once, in its own warning');
  assert.match(src, /freed = status\.freedArchives \?\? status\.reset\?\.archiveOnly \?\? 0/);
  assert.match(src, /\{#each impact as line/);
  assert.match(read('src/lib/copy/masterKeyCopy.ts'), /impactMany/, 'the lines are in the copy lint');
});
