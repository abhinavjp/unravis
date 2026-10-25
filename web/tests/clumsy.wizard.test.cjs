// Phase 06 clumsy-user review (TSK-21 item 3): the master key and case-file flows end to end, web side.
// Tests named 'bug: ...' are CONFIRMED defects and FAIL on purpose until fixed; when one is fixed, rename it to 'fixed: ...' and keep it.
// Tests named 'p06: ...' pin behaviour that holds.
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

const mk = load('src/lib/masterKey.ts');
const code = load('src/lib/caseCode.ts');
const restore = load('src/lib/restoreCase.ts');
const flow = load('src/lib/screenFlow.ts');
const enter = load('src/lib/copy/enterCopy.ts');

const ID = 'abcdefghijklmnopqrstuvwxyz'.slice(0, 26).replace(/[01]/g, '2');
const REAL_ID = 'k3j2h4g5f6d7s2a3q4w5e6r7t2';
const PASS = 'alpha bravo charlie delta';

// ---------------------------------------------------------------------------------------------------------------------------------
// Confirmed defects, now fixed (kept as 'fixed:' guards)
// ---------------------------------------------------------------------------------------------------------------------------------

// A Mac keyboard and a pasted password-manager entry can spell the same words composed (NFC) and decomposed (NFD). The server folds both to
// one key and accepts them (Go test TestP06_SetupAcceptsNFCAndNFDSpellings), but the set-up form compares the raw strings, so the button
// stays off and the screen says the two keys do not match, for two entries that look identical.
test('fixed: set-up treats an NFC and an NFD spelling of the same key as a mismatch', () => {
  const nfc = 'caf\u00e9 cr\u00e8me br\u00fbl\u00e9e';
  const nfd = 'cafe\u0301 cre\u0300me bru\u0302le\u0301e';
  assert.notEqual(nfc, nfd);
  assert.equal(mk.setupProblem(nfc, nfd, true), null, 'the server accepts these two as one key');
  assert.equal(mk.canSubmitSetup(nfc, nfd, true), true);
});

// A trailing space is part of the key (the server never trims, Go test TestP06_TrailingWhitespaceIsPartOfTheKey). A person who pastes a key with a
// trailing space into BOTH fields sets a key they can never type again, and the strength line says nothing.
test('fixed: the strength line does not warn about a space at either end of the key', () => {
  const plain = mk.strengthHint(PASS);
  assert.notEqual(mk.strengthHint(PASS + ' '), plain, 'a trailing space is counted as part of the key and nothing says so');
  assert.notEqual(mk.strengthHint(' ' + PASS), plain, 'same for a leading space');
});

// Full-width and other compatibility letters are not folded in a case code, so a code typed with a Japanese-IME keyboard in full-width mode
// reads as "does not match", the same sentence as a wrong code. (Letters only; digits 2-7 are not affected by case folding.)
test('fixed: a case code typed in full-width letters is not recognised', () => {
  const fullWidth = [...REAL_ID].map((ch) => (/[a-z]/.test(ch) ? String.fromCharCode(ch.charCodeAt(0) - 0x61 + 0xff41) : ch)).join('');
  assert.notEqual(fullWidth, REAL_ID);
  assert.equal(code.normaliseCode(fullWidth), REAL_ID);
});

// Re-wrapping older cases is offered exactly once: on the success panel that follows the first key set-up. Close that panel, restart, or open
// the app on a PC that already has a key and DPAPI cases, and nothing in the UI can reach rewrapCases again. A cut during the one re-wrap
// (Go test TestBug_HalfRewrappedCaseIsUnopenable500) then leaves a case that gives 500, with no screen to retry from.
test('fixed: no screen other than the first set-up success panel can re-wrap older cases', () => {
  const users = [];
  const walk = (dir) => {
    for (const name of fs.readdirSync(path.join(root, dir))) {
      const rel = dir + '/' + name;
      if (fs.statSync(path.join(root, rel)).isDirectory()) walk(rel);
      else if (/\.(svelte|ts)$/.test(name) && /rewrapCases\(/.test(read(rel)) && !/export (async )?function rewrapCases/.test(read(rel))) users.push(rel);
    }
  };
  walk('src');
  assert.ok(users.some((file) => !/MasterKeySetup\.svelte$/.test(file)), 'rewrapCases is only called from ' + users.join(', '));
});

test('p06: Home offers the retry whenever the key is open and older cases are left, and only then', () => {
  assert.equal(mk.offersRewrap('unlocked', 1), true);
  assert.equal(mk.offersRewrap('unlocked', 0), false);
  for (const s of ['none', 'locked', 'damaged', 'unknown', null]) assert.equal(mk.offersRewrap(s, 3), false, String(s));
  assert.equal(mk.offersRewrap('unlocked', NaN), false);
  const start = read('src/components/StartScreen.svelte');
  assert.match(start, /offersRewrap\(lock, app\.dpapiCases\)/);
  assert.match(start, /<RewrapNotice /);
  assert.match(read('src/components/RewrapNotice.svelte'), /rewrapCases\(\{ all: true \}\)/);
});

test('p06: a space at either end is flagged in the strength line but the key itself is never changed', () => {
  assert.equal(mk.hasEdgeSpace(PASS), false);
  assert.equal(mk.hasEdgeSpace(PASS + ' '), true);
  assert.equal(mk.hasEdgeSpace(' ' + PASS), true);
  assert.equal(mk.hasEdgeSpace(''), false);
  assert.ok(mk.strengthHint(PASS + ' ').startsWith(mk.strengthHint(PASS)));
  assert.equal(mk.setupProblem(PASS + ' ', PASS + ' ', true), null, 'a key with a trailing space is still allowed');
  assert.equal(mk.setupProblem(PASS + ' ', PASS, true), 'mismatch', 'but it must be typed the same twice');
  assert.equal(mk.keysMatch('café', 'café'), true);
  assert.equal(mk.keysMatch('cafe', 'café'), false);
});

test('p06: a case code with full-width letters and digits, or full-width dashes and spaces, is read as plain', () => {
  const wide = (text) => [...text].map((ch) => (/[a-z]/.test(ch) ? String.fromCharCode(ch.charCodeAt(0) - 0x61 + 0xff41) : /[2-7]/.test(ch) ? String.fromCharCode(ch.charCodeAt(0) - 0x30 + 0xff10) : ch)).join('');
  assert.equal(code.normaliseCode(wide(REAL_ID)), REAL_ID);
  assert.equal(code.normaliseCode(wide(REAL_ID.toUpperCase()).replace(/(.{5})/g, '$1－')), REAL_ID);
  assert.equal(code.normaliseCode('　' + wide(REAL_ID) + '　'), REAL_ID);
  assert.equal(code.normaliseCode(wide(REAL_ID).replace(/.$/, '１')), null, 'a full-width 1 is still not in the alphabet');
});

// ---------------------------------------------------------------------------------------------------------------------------------
// Behaviour that holds
// ---------------------------------------------------------------------------------------------------------------------------------

test('p06: normaliseCode forgives spaces, dashes, case, invisible marks and a trailing newline', () => {
  const variants = [
    REAL_ID,
    REAL_ID.toUpperCase(),
    '  ' + REAL_ID + '  ',
    REAL_ID + '\n',
    REAL_ID + '\r\n',
    REAL_ID.replace(/(.{4})/g, '$1 '),
    REAL_ID.replace(/(.{5})/g, '$1-'),
    REAL_ID.replace(/(.{5})/g, '$1\u2011'),
    REAL_ID.replace(/(.{5})/g, '$1\u2013'),
    '\u200b' + REAL_ID + '\ufeff',
    REAL_ID.slice(0, 13) + '\u00ad' + REAL_ID.slice(13)
  ];
  for (const v of variants) assert.equal(code.normaliseCode(v), REAL_ID, JSON.stringify(v));
});

test('p06: normaliseCode refuses the wrong length, wrong letters, lookalikes and a huge paste', () => {
  const refused = [
    '',
    REAL_ID.slice(1),
    REAL_ID + 'a',
    REAL_ID.replace(/.$/, '1'),
    REAL_ID.replace(/.$/, '0'),
    REAL_ID.replace(/.$/, '8'),
    REAL_ID.replace(/.$/, '\u0430'),
    REAL_ID.slice(0, 25) + '\u0131',
    REAL_ID + ' ' + REAL_ID,
    'x'.repeat(100000),
    ' '.repeat(300) + REAL_ID
  ];
  for (const v of refused) assert.equal(code.normaliseCode(v), null, JSON.stringify(v).slice(0, 60));
  assert.equal(code.normaliseCode(null), null);
  assert.equal(code.normaliseCode(undefined), null);
  assert.equal(code.normaliseCode(12345), null);
});

test('p06: resolveCode says one sentence for every refusal and lets only an abort through', async () => {
  const calls = [];
  const bad = await code.resolveCode('nope', async (id) => { calls.push(id); });
  assert.deepEqual(bad, { ok: false, message: enter.CODE_NOT_FOUND });
  assert.equal(calls.length, 0, 'a malformed code never reaches the server');
  for (const err of [new Error('x'), Object.assign(new Error('x'), { status: 404 }), Object.assign(new Error('x'), { status: 423 }), new TypeError('Failed to fetch')]) {
    const out = await code.resolveCode(REAL_ID, async () => { throw err; });
    assert.deepEqual(out, { ok: false, message: enter.CODE_NOT_FOUND });
  }
  const ok = await code.resolveCode(' ' + REAL_ID.toUpperCase() + ' ', async (id) => { calls.push(id); });
  assert.deepEqual(ok, { ok: true, id: REAL_ID });
  assert.deepEqual(calls, [REAL_ID]);
  await assert.rejects(code.resolveCode(REAL_ID, async () => { throw new DOMException('gone', 'AbortError'); }), (e) => e.name === 'AbortError');
});

test('p06: the code field takes a code with separators and invisible marks without cutting it short', () => {
  const source = read('src/components/EnterCase.svelte');
  const m = /id="\{uid\}-code"[^>]*maxlength="(\d+)"/.exec(source);
  assert.ok(m, 'the code field has a maxlength');
  // 26 letters with a zero-width mark between each is 52 characters; the field must not truncate a paste of that size.
  assert.ok(Number(m[1]) >= 52, 'maxlength ' + m[1]);
});

test('p06: a pasted case-file path loses its quotes, invisible marks and outer spaces, and nothing else', () => {
  const want = 'C:\\Cases\\one.unravis-case';
  for (const v of [want, '"' + want + '"', '  "' + want + '"  ', '\u202a' + want, '"\u202a' + want + '"', want + '\u200e', ' ' + want + ' ']) {
    assert.equal(restore.normalisePath(v), want, JSON.stringify(v));
  }
  assert.equal(restore.normalisePath(null), '');
  assert.equal(restore.normalisePath('""'), '');
  assert.equal(restore.normalisePath('"'), '"');
});

test('p06: the restore form refuses an empty, relative, network, device, long or wrongly named path before asking', () => {
  const key = 'k';
  const field = (p, k = key) => { const r = restore.checkRestoreInput(p, k); return r.ok ? 'ok' : r.field + ':' + r.message; };
  assert.equal(field(''), 'path:' + enter.ENTER_TEXT.pathEmpty);
  assert.equal(field('   '), 'path:' + enter.ENTER_TEXT.pathEmpty);
  for (const p of ['one.unravis-case', '.\\one.unravis-case', '..\\one.unravis-case', '\\\\server\\share\\one.unravis-case', '\\\\?\\C:\\one.unravis-case', '\\\\.\\C:\\one.unravis-case', 'CON', 'C:one.unravis-case', '/etc/one.unravis-case', 'file:///C:/one.unravis-case', 'https://x/one.unravis-case']) {
    assert.equal(field(p), 'path:' + enter.ENTER_TEXT.pathShape, p);
  }
  assert.equal(field('C:\\' + 'a'.repeat(600) + '.unravis-case'), 'path:' + enter.ENTER_TEXT.pathTooLong);
  assert.equal(field('C:\\' + '\u00e9'.repeat(300) + '.unravis-case'), 'path:' + enter.ENTER_TEXT.pathTooLong, 'counted in UTF-8 bytes, not characters');
  assert.equal(field('C:\\one.zip'), 'path:' + enter.ENTER_TEXT.pathName);
  assert.equal(field('C:\\one.unravis-case.txt'), 'path:' + enter.ENTER_TEXT.pathName);
  assert.equal(field('C:\\one.unravis-case', ''), 'key:' + enter.ENTER_TEXT.keyEmpty);
  assert.equal(field('C:\\one.UNRAVIS-CASE'), 'ok');
  assert.equal(field('c:/Cases/one.unravis-case'), 'ok');
});

test('p06: the restore key is never trimmed, so a space counts as the key', () => {
  const r = restore.checkRestoreInput('C:\\one.unravis-case', ' ');
  assert.equal(r.ok, true);
});

test('p06: every restore refusal maps to one small set and none echoes a path or a key', () => {
  const f = (status, reason, retryAfterSeconds) => restore.restoreFailureOf({ status, reason, retryAfterSeconds });
  assert.deepEqual(f(422, 'bad-case-file'), { kind: 'bad-case-file' });
  assert.deepEqual(f(429, 'throttled', 7), { kind: 'throttled', retryAfterSeconds: 7 });
  assert.deepEqual(f(429, 'throttled'), { kind: 'throttled', retryAfterSeconds: 5 });
  assert.deepEqual(f(423, 'locked'), { kind: 'locked' });
  assert.deepEqual(f(0, 'unreachable'), { kind: 'network' });
  assert.deepEqual(f(409, 'no-master-key'), { kind: 'no-key' });
  assert.deepEqual(f(503, 'busy'), { kind: 'busy' });
  assert.deepEqual(f(400, 'bad-path'), { kind: 'bad-path' });
  assert.deepEqual(f(500, 'store-error'), { kind: 'other' });
  assert.deepEqual(restore.restoreFailureOf(null), { kind: 'other' });
  assert.deepEqual(restore.restoreFailureOf('boom'), { kind: 'other' });
  // A wrong key and a damaged file read as the same sentence, so the screen never says which one it was.
  assert.equal(restore.restoreView(f(422, 'bad-case-file')).message, enter.RESTORE_FAILED);
  assert.equal(restore.restoreView(f(422, undefined)).message, enter.RESTORE_FAILED);
  // The wait is whole seconds, at least one, and the button is held off for it.
  assert.equal(restore.restoreView({ kind: 'throttled', retryAfterSeconds: 0.2 }).waitSeconds, 1);
  assert.equal(restore.restoreView({ kind: 'throttled', retryAfterSeconds: 59.1 }).waitSeconds, 60);
  // While the key file is damaged, 'locked' does not tell the person to type a key that cannot work.
  assert.equal(restore.restoreView({ kind: 'locked' }, 'damaged').message, enter.ENTER_TEXT.damaged);
  assert.equal(restore.restoreView({ kind: 'locked' }, 'locked').message, enter.ENTER_TEXT.locked);
  assert.equal(restore.restoreView({ kind: 'no-key' }).offerSetup, true);
});

test('p06: a Retry-After header or body figure is read as whole seconds, capped at an hour', () => {
  assert.equal(restore.retryAfterOf('12'), 12);
  assert.equal(restore.retryAfterOf(' 12 '), 12);
  assert.equal(restore.retryAfterOf('Wed, 21 Oct 2026 07:28:00 GMT', 4.2), 5);
  assert.equal(restore.retryAfterOf('0', 3), 3);
  assert.equal(restore.retryAfterOf('99999999'), undefined);
  assert.equal(restore.retryAfterOf('-5'), undefined);
  assert.equal(restore.retryAfterOf('', 99999), 3600);
  assert.equal(restore.retryAfterOf(null, undefined), undefined);
  assert.equal(restore.retryAfterOf(undefined, NaN), undefined);
});

test('p06: unlock refusals map to the right screen action, and a wrong key is only a 401 with that reason', () => {
  const u = (status, reason, retryAfterSeconds) => mk.unlockFailureOf({ status, reason, retryAfterSeconds });
  assert.deepEqual(u(401, 'wrong-key'), { kind: 'wrong-key' });
  assert.deepEqual(u(401, undefined), { kind: 'other' }, 'a lost session is not shown as a wrong key');
  assert.deepEqual(u(429, 'throttled', 30), { kind: 'throttled', retryAfterSeconds: 30 });
  assert.deepEqual(u(429, 'throttled', -1), { kind: 'throttled', retryAfterSeconds: 5 });
  assert.deepEqual(u(0, 'unreachable'), { kind: 'network' });
  assert.deepEqual(u(422, 'key-file-damaged'), { kind: 'damaged' });
  assert.deepEqual(u(409, 'not-set'), { kind: 'not-set' });
  assert.deepEqual(u(409, 'something'), { kind: 'conflict' });
  assert.deepEqual(u(500, 'store-error'), { kind: 'server' });
  assert.deepEqual(mk.unlockFailureOf(undefined), { kind: 'other' });
  assert.equal(mk.unlockMessage({ kind: 'wrong-key' }), mk.UNLOCK_MESSAGE);
});

test('p06: set-up validation order holds for the mismatches a hurried person makes', () => {
  assert.equal(mk.setupProblem(PASS, PASS, true), null);
  assert.equal(mk.setupProblem(PASS, PASS + ' ', true), 'mismatch', 'one trailing space on one side');
  assert.equal(mk.setupProblem(PASS, PASS.toUpperCase(), true), 'mismatch', 'case matters');
  assert.equal(mk.setupProblem(PASS, '', true), 'mismatch', 'an empty confirmation');
  assert.equal(mk.setupProblem(PASS, PASS, false), 'acknowledgement-required');
  assert.equal(mk.setupProblem('short', 'short', true), 'too-short');
  assert.equal(mk.setupProblem(' '.repeat(20), ' '.repeat(20), true), 'blank');
  assert.equal(mk.setupProblem('\u200b'.repeat(20), '\u200b'.repeat(20), true), 'blank');
  assert.equal(mk.setupProblem('x'.repeat(1025), 'x'.repeat(1025), true), 'too-long');
  assert.equal(mk.setupProblem('\ud800'.repeat(1) + 'x'.repeat(20), '\ud800' + 'x'.repeat(20), true), 'invalid-characters');
  assert.equal(mk.setupProblem(PASS + '\ufffd', PASS + '\ufffd', true), 'invalid-characters');
  // NFD is counted as composed when sizing, so a key of 12 composed letters typed decomposed is long enough.
  const nfd12 = 'e\u0301'.repeat(12);
  assert.equal(mk.setupProblem(nfd12, nfd12, true), null);
  assert.equal(mk.setupProblem(nfd12.slice(0, -2), nfd12.slice(0, -2), true), 'too-short');
});

test('p06: a damaged or locked key keeps the cases closed and Lock is offered only while unlocked', () => {
  for (const s of ['locked', 'damaged']) assert.equal(mk.closesCases(s), true, s);
  for (const s of ['none', 'unlocked', 'unknown', null]) assert.equal(mk.closesCases(s), false, String(s));
  assert.equal(mk.offersLock('unlocked'), true);
  assert.equal(mk.offersLock('locked'), false);
  assert.equal(mk.offersSetup('none'), true);
  assert.equal(mk.offersSetup('unknown'), false);
  assert.equal(mk.parseStatus({ state: 'unlocked' }).dpapiCases, 0);
  assert.equal(mk.parseStatus({ state: 'locked', dpapiCases: -3 }).dpapiCases, 0);
  assert.equal(mk.parseStatus({ state: 'locked', dpapiCases: 2.9 }).dpapiCases, 2);
  assert.equal(mk.parseStatus({ state: 'open' }), null, 'a strange answer is never taken as unlocked');
  assert.equal(mk.parseStatus(null), null);
  assert.equal(mk.parseStatus('unlocked'), null);
});

test('p06: after a restart the app opens on the unlock screen when the key is locked, and Back cannot skip it', () => {
  const facts = (over) => ({ demo: false, deepLink: false, resume: false, lock: 'unlocked', ...over });
  assert.equal(flow.initialScreen(facts({ lock: 'locked' })), 'unlock');
  assert.equal(flow.initialScreen(facts({ lock: 'damaged', deepLink: true, resume: true })), 'unlock', 'a deep link or a remembered investigation does not bypass the lock');
  assert.equal(flow.initialScreen(facts({ lock: 'unknown' })), 'start');
  assert.equal(flow.initialScreen(facts({ demo: true, lock: 'locked' })), 'running');
  // From the unlock screen the only ways out are the screens that the key opens; setup and loading are not reachable.
  assert.equal(flow.canGo('unlock', 'setup'), false);
  assert.equal(flow.canGo('unlock', 'loading'), false);
  assert.equal(flow.go('unlock', 'setup').ok, false);
  // Every work screen can drop back to unlock when the key locks mid-task.
  for (const s of ['start', 'create', 'running']) assert.equal(flow.canGo(s, 'unlock'), true, s);
  assert.equal(flow.afterUnlock('running'), 'running');
  assert.equal(flow.afterUnlock('create'), 'create');
  assert.equal(flow.afterUnlock(null), 'start');
  assert.equal(flow.afterUnlock('setup'), 'start');
  assert.equal(flow.afterUnlock('unlock'), 'start');
});

test('p06: a case drawer is closed on every screen but the analysis', () => {
  for (const s of flow.SCREENS) assert.equal(flow.leavesCaseWork(s), s !== 'running', s);
});

test('p06: the set-up and unlock screens clear the key from the form the moment it is read', () => {
  const setup = read('src/components/MasterKeySetup.svelte');
  assert.match(setup, /const first = pass, second = confirm;\s*pass = ''; confirm = '';/);
  const unlock = read('src/components/MasterKeyUnlock.svelte');
  assert.match(unlock, /pass = ''/);
  // Double-click: both forms ignore a second submit while one is in flight.
  assert.match(setup, /if \(busy \|\| !canSubmitSetup/);
  assert.match(unlock, /if \(busy/);
});
