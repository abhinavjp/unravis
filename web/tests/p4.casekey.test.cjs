const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

// P4: the optional per-case key and Erase. Pure rules, the key calls (with a fetch stand-in), the copy lint, the wizard's key step
// and the clumsy-person cases (wrong key again and again, a key left in the page, an erase typed wrong).

const root = path.resolve(__dirname, '..');
function loadTypeScript(relativePath, runes = false, cache = new Map()) {
  const filename = path.join(root, relativePath);
  if (cache.has(filename)) return cache.get(filename).exports;
  const source = fs.readFileSync(filename, 'utf8');
  const output = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022, esModuleInterop: true } }).outputText;
  const module = { exports: {} };
  cache.set(filename, module);
  const nodeRequire = createRequire(filename);
  const load = (request) => {
    if (/^\.\.?\//.test(request)) {
      const plain = path.join(path.dirname(filename), request + '.ts');
      const store = path.join(path.dirname(filename), request + '.svelte.ts');
      if (fs.existsSync(plain)) return loadTypeScript(path.relative(root, plain), runes, cache);
      if (fs.existsSync(store)) return loadTypeScript(path.relative(root, store), runes, cache);
    }
    return nodeRequire(request);
  };
  new Function('require', 'module', 'exports', '$state', output)(load, module, module.exports, runes ? (value) => value : undefined);
  return module.exports;
}
const read = (relative) => fs.readFileSync(path.join(root, relative), 'utf8');

const TOKEN = 'link-session-token-0123456789abcdef';
const ID = 'k7m2qx4a' + 'abcdefghij' + 'klmnopqr';
const PASS = 'unravis demo case key';

// Runs `fn` with stand-ins for the browser globals api.ts reads at load time, and a fetch that answers from `responses`.
async function withApi(responses, fn) {
  const names = ['location', 'history', 'sessionStorage', 'fetch'];
  const saved = Object.fromEntries(names.map((name) => [name, Object.getOwnPropertyDescriptor(globalThis, name)]));
  const calls = [];
  const items = new Map([['unravis.session', TOKEN]]);
  const values = {
    location: { hash: '', search: '', pathname: '/' },
    history: { state: null, replaceState: () => {} },
    sessionStorage: { getItem: (k) => (items.has(k) ? items.get(k) : null), setItem: (k, v) => { items.set(k, String(v)); }, removeItem: (k) => { items.delete(k); } },
    fetch: async (url, init = {}) => {
      calls.push({ url: String(url), method: init.method ?? 'GET', headers: new Headers(init.headers), body: init.body });
      const next = responses.shift() ?? { status: 200, body: {} };
      if (next.network) throw new TypeError('Failed to fetch');
      const make = () => ({ ok: next.status >= 200 && next.status < 300, status: next.status, json: async () => next.body, text: async () => JSON.stringify(next.body ?? {}), headers: { get: () => null } });
      return { ...make(), clone: make };
    }
  };
  for (const name of names) Object.defineProperty(globalThis, name, { configurable: true, writable: true, value: values[name] });
  try {
    return await fn({ calls, items });
  } finally {
    for (const name of names) {
      if (saved[name]) Object.defineProperty(globalThis, name, saved[name]);
      else delete globalThis[name];
    }
  }
}

const caseKey = loadTypeScript('src/lib/caseKey.ts');
const { lintScreenStrings } = loadTypeScript('src/lib/copy/copylint.ts');
const { deck } = loadTypeScript('src/lib/copy/deck.ts');
const keyCopy = loadTypeScript('src/lib/copy/caseKeyCopy.ts');
const eraseCopy = loadTypeScript('src/lib/copy/eraseCopy.ts');
const lockSignal = loadTypeScript('src/lib/lockSignal.ts');

// ---- the rules

test('a new case key follows the PC key rules: 12 characters, a matching repeat, no acknowledgement box', () => {
  assert.equal(caseKey.newKeyProblem('short', 'short'), 'too-short');
  assert.equal(caseKey.newKeyProblem('abcdefghijkl', 'abcdefghijkm'), 'mismatch');
  assert.equal(caseKey.newKeyProblem('abcdefghijkl', 'abcdefghijkl'), null, 'there is no tick box for a case key');
  assert.equal(caseKey.newKeyProblem('a'.repeat(1025), 'a'.repeat(1025)), 'too-long');
  assert.equal(caseKey.repeatMismatch('abcdefghijkl', ''), false, 'an empty repeat is not scolded');
  assert.equal(caseKey.repeatMismatch('abcdefghijkl', 'abc'), true);
});

test('the key ending sticker is shown only for four hex digits', () => {
  assert.equal(caseKey.keyEndingLabel('a1B2'), 'Key ending a1b2');
  assert.equal(caseKey.keyEndingLabel('a1b'), '');
  assert.equal(caseKey.keyEndingLabel('zzzz'), '');
  assert.equal(caseKey.keyEndingLabel(undefined), '');
  assert.deepEqual({ ...caseKey.parseCaseKeyStatus({ ownKey: true, locked: true, ending: 'ABCD' }) }, { ownKey: true, locked: true, ending: 'abcd' });
  assert.deepEqual({ ...caseKey.parseCaseKeyStatus({ ownKey: false, locked: true, ending: 'abcd' }) }, { ownKey: false, locked: false }, 'no key, so nothing to lock and no ending');
  assert.equal(caseKey.parseCaseKeyStatus({ locked: true }), null);
  assert.equal(caseKey.parseCaseKeyStatus(null), null);
});

test('a failed key call is reduced to a small set of kinds', () => {
  const kind = (e) => caseKey.caseKeyFailureOf(e).kind;
  assert.equal(kind({ status: 423, reason: 'wrong-case-key' }), 'wrong-key');
  assert.equal(kind({ status: 423, reason: 'case-locked' }), 'app-locked', 'only the two key reasons are read as a case key answer');
  assert.equal(kind({ status: 429, reason: 'throttled', retryAfterSeconds: 4 }), 'throttled');
  assert.equal(caseKey.caseKeyFailureOf({ status: 429, reason: 'throttled', retryAfterSeconds: 4 }).retryAfterSeconds, 4);
  assert.equal(kind({ status: 0, reason: 'unreachable' }), 'network');
  assert.equal(kind({ status: 409, reason: 'case-in-use' }), 'in-use');
  assert.equal(kind({ status: 409, reason: 'case-erasing' }), 'erasing');
  assert.equal(kind({ status: 409, reason: 'no-case-key' }), 'no-case-key');
  assert.equal(kind({ status: 409, reason: 'has-case-key' }), 'has-case-key');
  assert.equal(kind({ status: 404, reason: '' }), 'gone');
  assert.equal(kind({ status: 423, reason: 'locked' }), 'app-locked', 'the PC key lock is not a wrong case key');
  assert.equal(kind({ status: 503, reason: 'case-key-unavailable' }), 'unavailable');
  assert.equal(kind({ status: 500 }), 'server');
  assert.equal(kind(new Error('boom')), 'other');
  assert.equal(kind(null), 'other');
});

test('the Change and Remove buttons need the current key, and Change needs a good new one', () => {
  const ok = 'abcdefghijkl';
  assert.equal(caseKey.changeReady('remove', ok, '', ''), true);
  assert.equal(caseKey.changeReady('remove', '', '', ''), false);
  assert.equal(caseKey.changeReady('change', ok, ok + 'x', ok + 'x'), true);
  assert.equal(caseKey.changeReady('change', ok, ok + 'x', ok), false, 'repeat does not match');
  assert.equal(caseKey.changeReady('change', '', ok, ok), false, 'no current key');
});

test('a case-key 423 does not raise the whole-app lock screen', () => {
  assert.equal(lockSignal.isCaseKeyReason('case-locked'), true);
  assert.equal(lockSignal.isCaseKeyReason('wrong-case-key'), true);
  assert.equal(lockSignal.isCaseKeyReason('locked'), false);
  assert.equal(lockSignal.isCaseKeyReason(undefined), false);
});

// ---- the calls

test('the key calls send the key in the body only: never in the URL or a header', async () => {
  await withApi([{ status: 200, body: { ownKey: true, locked: false, ending: 'abcd' } }, { status: 200, body: { ownKey: true, locked: false, ending: 'beef' } }, { status: 200, body: { ownKey: false, locked: false } }, { status: 200, body: { ownKey: true, locked: true, ending: 'beef' } }], async ({ calls }) => {
    const api = loadTypeScript('src/lib/caseKeyApi.ts');
    const set = await api.setCaseKey(ID, PASS, PASS);
    assert.deepEqual({ ...set }, { ownKey: true, locked: false, ending: 'abcd' });
    await api.changeCaseKey(ID, PASS, PASS + ' two', PASS + ' two');
    await api.removeCaseKey(ID, PASS + ' two');
    await api.lockCase(ID);
    assert.deepEqual(calls.map((c) => c.method + ' ' + c.url), [
      'POST /api/cases/' + ID + '/key/set', 'POST /api/cases/' + ID + '/key/change', 'POST /api/cases/' + ID + '/key/remove', 'POST /api/cases/' + ID + '/key/lock'
    ]);
    assert.deepEqual(JSON.parse(calls[0].body), { passphrase: PASS, confirm: PASS });
    assert.deepEqual(JSON.parse(calls[1].body), { passphrase: PASS, newPassphrase: PASS + ' two', confirm: PASS + ' two' });
    assert.deepEqual(JSON.parse(calls[2].body), { passphrase: PASS + ' two' });
    assert.deepEqual(JSON.parse(calls[3].body), {}, 'lock sends an empty body');
    for (const call of calls) {
      assert.ok(!call.url.includes('unravis') && !call.url.includes('demo'), 'the key is not in the URL');
      for (const [name, value] of call.headers) assert.ok(!String(value).includes('unravis demo'), name + ' does not hold the key');
      assert.equal(call.headers.get('X-Audit-Token'), TOKEN);
    }
    assert.equal(calls[0].headers.get('Content-Type'), 'application/json');
  });
});

test('a wrong case key keeps its reason, throws fixed text, and never echoes the key', async () => {
  await withApi([{ status: 423, body: { error: 'ignored server wording ' + PASS, reason: 'wrong-case-key' } }], async () => {
    const api = loadTypeScript('src/lib/caseKeyApi.ts');
    await assert.rejects(() => api.unlockCase(ID, PASS), (error) => {
      assert.equal(error.status, 423);
      assert.equal(error.reason, 'wrong-case-key');
      assert.ok(!error.message.includes(PASS), 'the message does not hold the key');
      assert.ok(!error.message.includes('ignored server wording'), 'the server wording is not shown');
      assert.equal(caseKey.caseKeyFailureOf(error).kind, 'wrong-key');
      return true;
    });
  });
});

test('a bad case id never reaches the network', async () => {
  await withApi([], async ({ calls }) => {
    const api = loadTypeScript('src/lib/caseKeyApi.ts');
    for (const bad of ['', 'abc', '../../etc', ID + '/x', ID.toUpperCase()]) {
      await assert.rejects(() => api.unlockCase(bad, PASS), (error) => error.reason === 'bad-case-id');
    }
    assert.equal(calls.length, 0);
  });
});

test('five wrong tries then a wait: the sixth is a throttle with the seconds the server named', async () => {
  const wrong = { status: 423, body: { reason: 'wrong-case-key' } };
  await withApi([wrong, wrong, wrong, wrong, wrong, { status: 429, body: { reason: 'throttled', retryAfterSeconds: 1 } }], async () => {
    const api = loadTypeScript('src/lib/caseKeyApi.ts');
    const kinds = [];
    for (let i = 0; i < 6; i++) {
      try { await api.unlockCase(ID, 'not the key ' + i); kinds.push('opened'); } catch (error) { kinds.push(caseKey.caseKeyFailureOf(error)); }
    }
    assert.deepEqual(kinds.slice(0, 5).map((k) => k.kind), Array(5).fill('wrong-key'));
    assert.equal(kinds[5].kind, 'throttled');
    assert.equal(kinds[5].retryAfterSeconds, 1);
    const text = keyCopy.caseKeyErrorText(kinds[5], 'unlock');
    assert.ok(!/30 seconds/.test(text), 'no wait is promised that the server does not give');
  });
});

test('creating a case sends the key in the body only when one was asked for; erase sends the typed code', async () => {
  await withApi([{ status: 200, body: { id: ID, title: 'A' } }, { status: 200, body: { id: ID, title: 'B' } }, { status: 200, body: {} }], async ({ calls }) => {
    const cases = loadTypeScript('src/lib/cases.ts');
    await cases.createCase('A', 'req-1', undefined, { key: PASS, confirm: PASS });
    await cases.createCase('B', 'req-2');
    await cases.eraseCase(ID, '  K7M2-QX4A ');
    const first = JSON.parse(calls[0].body);
    assert.equal(first.caseKey, PASS);
    assert.equal(first.caseKeyConfirm, PASS);
    const second = JSON.parse(calls[1].body);
    assert.ok(!('caseKey' in second) && !('caseKeyConfirm' in second), 'no key, no key fields');
    assert.ok(!calls[0].url.includes(PASS));
    assert.deepEqual(JSON.parse(calls[2].body), { confirm: 'K7M2-QX4A' });
    assert.equal(calls[2].method, 'DELETE');
  });
});

test('the list answers cases and erased lines; a line holds an id and a time and nothing else', async () => {
  const listed = { cases: [{ id: ID, title: '(locked)', ownKey: true, keyLocked: true, keyEnding: 'abcd', created: '2026-10-20T09:00:00Z' }], erased: [{ id: 'k7m2qx4aabcdefghijklmnopqr', at: '2026-10-13T09:00:00Z', title: 'must not be kept', path: 'C:/secret' }, { id: 'bad id', at: 'x' }, null] };
  await withApi([{ status: 200, body: listed }, { status: 204, body: null }], async ({ calls }) => {
    const cases = loadTypeScript('src/lib/cases.ts');
    const got = await cases.listCasesAndErased();
    assert.equal(got.cases[0].keyLocked, true);
    assert.equal(got.cases[0].keyEnding, 'abcd');
    assert.deepEqual(got.erased.map((l) => ({ ...l })), [{ id: 'k7m2qx4aabcdefghijklmnopqr', at: '2026-10-13T09:00:00Z' }]);
    await cases.clearErasedLine('k7m2qx4aabcdefghijklmnopqr');
    assert.equal(calls[1].method, 'DELETE');
    assert.equal(calls[1].url, '/api/cases/erased/k7m2qx4aabcdefghijklmnopqr');
  });
});

// ---- Erase rules

test('Erase is offered unless an archive is running, and needs the short code', async () => {
  await withApi([], async () => {
    const erase = loadTypeScript('src/lib/erase.ts');
    assert.equal(erase.eraseOffered({ state: 'open', closed: null }), true);
    assert.equal(erase.eraseOffered({ state: 'closed', closed: 'x' }), true);
    assert.equal(erase.eraseOffered({ state: 'archiving', closed: 'x' }), false);
    assert.equal(erase.eraseReady('k7m2qx4a', ID), true);
    assert.equal(erase.eraseReady(' K7M2QX4A ', ID), true);
    assert.equal(erase.eraseReady('k7m2qx4', ID), false, 'one character short');
    assert.equal(erase.eraseReady('', ID), false);
    assert.equal(erase.eraseReady('k7m2qx4a', 'not-an-id'), false);
  });
});

test('an erase that did not finish reads as a fixed sentence, never the server wording or the typed code', async () => {
  await withApi([], async () => {
    const shared = new Map();
    const { CaseApiError } = loadTypeScript('src/lib/cases.ts', false, shared);
    const erase = loadTypeScript('src/lib/erase.ts', false, shared);
    const text = (status, reason) => erase.eraseFailureText(new CaseApiError('server says: secret path C:/x', status, reason));
    assert.equal(text(400, 'confirm-mismatch'), eraseCopy.ERASE_TEXT.mismatch);
    assert.equal(text(409, 'case-in-use'), 'The case is in use. Close its other tabs, then try again.');
    assert.equal(text(409, 'case-erasing'), 'This case is being erased.');
    assert.equal(text(500, 'ledger-not-stripped'), eraseCopy.ERASE_TEXT.failed);
    assert.equal(text(0, 'unreachable'), eraseCopy.ERASE_TEXT.network);
    assert.equal(erase.eraseFailureText(new Error('boom')), eraseCopy.ERASE_TEXT.network);
    for (const s of [text(500, 'x'), text(409, 'case-in-use')]) assert.ok(!s.includes('secret path'));
  });
});

test('the case-in-use and being-erased reasons have their own sentences', () => {
  const copy = loadTypeScript('src/lib/copy/importCopy.ts');
  const text = (reason, status = 409) => copy.errorText({ status, reason, message: 'server wording' });
  assert.equal(text('case-in-use'), 'The case is in use. Close its other tabs, then try again.');
  assert.equal(text('case-locked', 423), 'This case has its own key. Enter it to open the case.');
  assert.match(text('wrong-case-key', 423), /nothing changed/i);
  assert.match(text('untrusted-case'), /older version/);
  assert.ok(!/server wording/.test(text('throttled', 429)));
});

test('entering the code of a keyed case counts as found', async () => {
  const { resolveCode } = loadTypeScript('src/lib/caseCode.ts');
  const locked = await resolveCode('k7m2qx4aabcdefghijklmnopqr', async () => { throw { status: 423, reason: 'case-locked' }; });
  assert.deepEqual({ ...locked }, { ok: true, id: 'k7m2qx4aabcdefghijklmnopqr' });
  const missing = await resolveCode('k7m2qx4aabcdefghijklmnopqr', async () => { throw { status: 404, reason: 'case-not-found' }; });
  assert.equal(missing.ok, false);
  const wrongKey = await resolveCode('k7m2qx4aabcdefghijklmnopqr', async () => { throw { status: 423, reason: 'wrong-case-key' }; });
  assert.equal(wrongKey.ok, false, 'only "locked" proves the case exists');
});

// ---- the copy

test('the case key and Erase copy pass the plain-words lint', () => {
  assert.deepEqual(lintScreenStrings(keyCopy.caseKeyLintEntries(), deck.terms).map((f) => f.id + ': ' + f.message), []);
  assert.deepEqual(lintScreenStrings(eraseCopy.eraseLintEntries(), deck.terms).map((f) => f.id + ': ' + f.message), []);
});

test('no case key text promises 30 seconds or says the key can be recovered', () => {
  const all = keyCopy.caseKeyLintEntries().map((e) => e.text).join('\n');
  assert.ok(!/30 seconds|30 s\b/.test(all));
  assert.ok(!/can be recovered|we can recover|reset your key/i.test(all));
  assert.match(all, /cannot recover|cannot be recovered/i);
});

test('the Erase text says what goes, that a saved case file stays, and that it cannot be undone', () => {
  const body = eraseCopy.eraseBody();
  assert.match(body, /removes/);
  assert.match(body, /case file you saved is not touched/);
  assert.match(body, /cannot be undone/);
  assert.match(eraseCopy.eraseBody('D:\\Unravis archive'), /D:\\Unravis archive/);
  assert.equal(eraseCopy.erasePromptText('k7m2qx4a'), 'Type the case code k7m2qx4a to erase');
  assert.match(eraseCopy.erasedLineText('q2w7exak', '20 Oct 09:12 UTC'), /^Erased case q2w7exak/);
});

// ---- the start screen

test('the list shows a locked case by its short code with the key ending, and an untrusted case with an Open button', () => {
  const { startView } = loadTypeScript('src/lib/startScreen.ts');
  const view = startView('unlocked', [
    { id: ID, title: '(locked)', created: '2026-10-20T09:00:00Z', closed: null, imports: 0, ownKey: true, keyLocked: true, keyEnding: 'abcd' },
    { id: 'z'.repeat(0) + 'q2w7exak' + 'abcdefghijklmnopqr', title: '', created: '', closed: null, imports: 0, untrusted: true }
  ], 1, [{ id: 'k7m2qx4aabcdefghijklmnopqr', at: '2026-10-13T09:00:00Z' }]);
  assert.equal(view.rows[0].title, 'Case k7m2qx4a');
  assert.equal(view.rows[0].keyEnding, 'Key ending abcd');
  assert.equal(view.rows[0].keyLocked, true);
  assert.equal(view.rows[0].created + view.rows[0].counts + view.rows[1].created + view.rows[1].counts, '', 'no dates or counts are read from a case that is locked or not trusted');
  assert.equal(view.rows[1].title, 'Case q2w7exak');
  assert.equal(view.rows[1].untrusted, true);
  assert.equal(view.erased.length, 1);
  assert.match(view.erased[0].text, /Erased case k7m2qx4a/);
  assert.equal(view.erased[0].sticker, 'Erased');
  const locked = startView('locked', [], 1, [{ id: 'k7m2qx4aabcdefghijklmnopqr', at: '2026-10-13T09:00:00Z' }]);
  assert.deepEqual(locked.erased, [], 'a locked PC key shows nothing, not even an Erased line');
  const src = read('src/components/StartScreen.svelte');
  assert.ok(!/\{#if row\.untrusted\}\s*<span[^>]*role="note"/.test(src), 'an untrusted row has an Open button now');
});

// ---- the wizard's key step

const wizardStore = loadTypeScript('src/lib/wizard.svelte.ts', true);

test('the case key is off by default and a draft with it on is not blank', () => {
  wizardStore.resetWizard();
  assert.equal(wizardStore.wizard.caseKey.on, false);
  assert.equal(wizardStore.draftKeyProblem(), null);
  assert.equal(wizardStore.draftCaseKey(), null, 'nothing to send when the case gets no key of its own');
  wizardStore.wizard.caseKey.on = true;
  assert.equal(wizardStore.wizardIsDirty(), true);
  wizardStore.resetWizard();
});

test('Next is refused while the key asked for is not good, and goes ahead once it is', () => {
  wizardStore.resetWizard();
  wizardStore.wizard.title = 'Week 42';
  wizardStore.wizard.caseKey.on = true;
  assert.notEqual(wizardStore.draftKeyProblem(), null);
  assert.equal(wizardStore.nextStep(), false);
  assert.equal(wizardStore.wizard.step, 'name');
  wizardStore.wizard.caseKey.key = 'abcdefghijkl';
  wizardStore.wizard.caseKey.again = 'abcdefghijkx';
  assert.equal(wizardStore.draftKeyProblem(), 'mismatch');
  assert.equal(wizardStore.goToStep('review'), false, 'the strip cannot jump past it either');
  wizardStore.wizard.caseKey.again = 'abcdefghijkl';
  assert.equal(wizardStore.draftKeyProblem(), null);
  assert.equal(wizardStore.nextStep(), true);
  assert.deepEqual({ ...wizardStore.draftCaseKey() }, { key: 'abcdefghijkl', confirm: 'abcdefghijkl' });
  wizardStore.resetWizard();
});

test('turning the key off or throwing the draft away wipes the key and its repeat', () => {
  wizardStore.resetWizard();
  wizardStore.wizard.caseKey.on = true;
  wizardStore.wizard.caseKey.key = 'abcdefghijkl';
  wizardStore.wizard.caseKey.again = 'abcdefghijkl';
  wizardStore.wipeCaseKey();
  assert.equal(wizardStore.wizard.caseKey.key, '');
  assert.equal(wizardStore.wizard.caseKey.again, '');
  wizardStore.wizard.caseKey.key = 'abcdefghijkl';
  wizardStore.resetWizard();
  assert.deepEqual({ ...wizardStore.wizard.caseKey }, { on: false, key: '', again: '' });
  assert.ok(!JSON.stringify(wizardStore.draftSnapshot()).includes('abcdefghijkl'), 'the snapshot handed to the review never holds the key');
});

test('the run hands the key to the create call alone and keeps it out of the run state and the request id', async () => {
  const submit = loadTypeScript('src/lib/wizardSubmit.ts');
  const seen = [];
  const api = { createCase: async (title, requestId, key) => { seen.push({ title, requestId, key }); throw Object.assign(new Error('x'), { status: 500, reason: 'server' }); } };
  const state = submit.blankSubmitState();
  await submit.submitWizard({ title: 'Week 42', envs: [], sources: [], window: null, scanId: '', host: null, files: {}, ackText: '', caseKey: { key: PASS, confirm: PASS } }, api, state);
  assert.deepEqual({ ...seen[0].key }, { key: PASS, confirm: PASS });
  assert.ok(!seen[0].requestId.includes(PASS));
  assert.ok(!JSON.stringify(state).includes(PASS), 'the run state does not keep the key');
});

// ---- clumsy cases in the screens

test('the key screens clear the key when it is read and when the page goes, and keep it out of storage, the console and the URL', () => {
  for (const file of ['src/components/CaseKeyCard.svelte', 'src/components/CaseKeyPage.svelte', 'src/components/EraseCase.svelte']) {
    const src = read(file);
    assert.ok(!/localStorage|sessionStorage|console\.|location\.|history\./.test(src), file + ' keeps nothing outside the page');
    assert.ok(!/\$inspect/.test(src));
    assert.match(src, /onDestroy|return \(\) =>/, file + ' cleans up when it closes');
  }
  const card = read('src/components/CaseKeyCard.svelte');
  assert.match(card, /const attempt = pass;\s*pass = '';/, 'the field is cleared before the request is sent');
  assert.match(card, /onDestroy\(\(\) => \{ pass = ''/);
  assert.match(card, /type="password"/);
  const page = read('src/components/CaseKeyPage.svelte');
  assert.match(page, /const a = current, b = next, c = again;\s*wipe\(\);/);
  assert.equal((page.match(/type="password"/g) ?? []).length, 3);
  const wizard = read('src/components/CreateWizard.svelte');
  assert.equal((wizard.match(/type="password"/g) ?? []).length, 2);
});

test('the unlock button is held for the wait the server named, so a press is not wasted', () => {
  const card = read('src/components/CaseKeyCard.svelte');
  assert.match(card, /disabled=\{busy \|\| held\}/);
  assert.match(card, /failure\.retryAfterSeconds/);
  const page = read('src/components/CaseKeyPage.svelte');
  assert.match(page, /disabled=\{!ready \|\| busy \|\| held\}/);
});

test('Erase cannot be pressed with the wrong code, and the home page offers it only when it can work', () => {
  const dialog = read('src/components/EraseCase.svelte');
  assert.match(dialog, /disabled=\{!ready \|\| busy\}/);
  assert.match(dialog, /if \(busy \|\| !ready\) return;/);
  const home = read('src/lib/components/CaseHome.svelte');
  assert.match(home, /eraseOffered\(info\)/);
  assert.match(home, /phase === 'locked'/, 'a locked case opens on its key card');
  assert.match(home, /phase = 'untrusted'/);
  const panel = read('src/lib/components/CasePanel.svelte');
  assert.match(panel, /eraseCase\(id, entered, controller\?\.signal\)/);
});

test('each page has one main button: the key card, the key page, Erase and the untrusted page', () => {
  const count = (src) => (src.match(/class="primary-button/g) ?? []).length;
  assert.equal(count(read('src/components/CaseKeyCard.svelte')), 1);
  assert.equal(count(read('src/components/CaseKeyPage.svelte')), 1);
  const erase = read('src/components/EraseCase.svelte');
  assert.equal(count(erase), 1, 'one main button; the done state uses an outlined one');
  const home = read('src/lib/components/CaseHome.svelte');
  const untrusted = home.slice(home.indexOf("phase === 'untrusted'"), home.indexOf("view === 'key'"));
  assert.equal(count(untrusted), 1);
});
