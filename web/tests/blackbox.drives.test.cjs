const assert = require('node:assert/strict');
const test = require('node:test');
const { loadTypeScript } = require('./helpers/loadTs.cjs');

// TSK-05 (contract C-3): the drives client of the wizard's Folder step. One typed fetch in blackboxApi's
// error style, and one filter: plain fixed drives first, the Windows drive kept but marked, USB and
// network drives listed with their own flag and never the default (rule S-11).

const bd = loadTypeScript('src/lib/blackboxDrives.ts');
// Only for the BlackboxError identity: the drives client throws blackbox.ts's own error class.
const bb = loadTypeScript('src/lib/blackbox.ts');
const api = loadTypeScript('src/lib/api.ts');

const drive = (letter, type, system, extra = {}) =>
  ({ letter, label: 'Drive ' + letter, type, system, freeBytes: 100, totalBytes: 200, ...extra });

/* ------------------------------------------------------------------ the filter */

test('eligibleDrives puts fixed drives that are not the Windows drive first, in the order they were listed', () => {
  const c = drive('C', 'fixed', true);
  const e = drive('E', 'fixed', false);
  const d = drive('D', 'fixed', false);
  const got = bd.eligibleDrives([c, e, d]);
  assert.deepEqual(got.map((x) => x.letter), ['E', 'D', 'C']);
  // the entries are the drives themselves, not copies: their own flags travel with them
  assert.equal(got[0], e);
  assert.equal(got[2], c);
});

test('the first entry is a plain fixed drive whenever the machine has one, so a default taken from the top never lands on C:, USB or network', () => {
  const got = bd.eligibleDrives([drive('Z', 'network', false), drive('C', 'fixed', true), drive('E', 'fixed', false), drive('F', 'removable', false)]);
  assert.equal(got[0].letter, 'E');
  assert.equal(got[0].type, 'fixed');
  assert.equal(got[0].system, false);
});

test('the Windows drive stays in the list, after the plain fixed drives, marked by its own system flag', () => {
  const got = bd.eligibleDrives([drive('D', 'fixed', false), drive('C', 'fixed', true)]);
  assert.deepEqual(got.map((x) => x.letter), ['D', 'C']);
  assert.equal(got[1].system, true, 'the mark is the drive data itself, nothing is added to it');
});

test('a machine with only C: still lists C:, and an empty answer is an empty list', () => {
  assert.deepEqual(bd.eligibleDrives([drive('C', 'fixed', true)]).map((x) => x.letter), ['C']);
  assert.deepEqual(bd.eligibleDrives([]), []);
});

test('USB and network drives are listed too, after the Windows drive, and carry their type as the flag', () => {
  const usb = drive('F', 'removable', false);
  const net = drive('Z', 'network', false);
  const withFixed = bd.eligibleDrives([usb, net, drive('C', 'fixed', true), drive('D', 'fixed', false)]);
  assert.deepEqual(withFixed.map((x) => x.letter), ['D', 'C', 'F', 'Z']);
  // with no plain fixed drive at all they still show, so the step can give its USB and network notices
  const onlyExtras = bd.eligibleDrives([usb, net]);
  assert.deepEqual(onlyExtras.map((x) => x.letter), ['F', 'Z']);
  assert.equal(onlyExtras[0].type, 'removable');
  assert.equal(onlyExtras[1].type, 'network');
});

test('drives of any other type are left out, but the Windows drive is kept whatever its type says', () => {
  const cd = drive('R', 'other', false);
  const got = bd.eligibleDrives([cd, drive('D', 'fixed', false)]);
  assert.deepEqual(got.map((x) => x.letter), ['D']);
  // a Windows drive the server typed oddly is still listed once, in the Windows slot, never twice
  const odd = bd.eligibleDrives([drive('X', 'other', true), drive('N', 'network', true), drive('D', 'fixed', false)]);
  assert.deepEqual(odd.map((x) => x.letter), ['D', 'X', 'N']);
});

test('eligibleDrives never changes the list given and always hands back a new one', () => {
  const given = [drive('C', 'fixed', true), drive('D', 'fixed', false), drive('R', 'other', false)];
  const before = JSON.parse(JSON.stringify(given));
  const got = bd.eligibleDrives(given);
  assert.deepEqual(given, before, 'the list given is not changed');
  assert.notEqual(got, given, 'a new list comes back');
  assert.deepEqual(bd.eligibleDrives(given).map((x) => x.letter), got.map((x) => x.letter), 'the same list in gives the same order out');
});

/* ------------------------------------------------------------------ the client */

const TOKEN = 'bbx-drives-token';
const realFetch = globalThis.fetch;
test.afterEach(() => { globalThis.fetch = realFetch; delete globalThis.sessionStorage; });

const tokenStore = (token) => {
  const removed = [];
  return { getItem: () => token, setItem() {}, removeItem: (key) => { removed.push(key); }, removed };
};

test('fetchDrives reads GET /api/blackbox/this-pc/drives with the session token and nothing else on the request', async () => {
  const body = '{"drives":[{"letter":"D","label":"Data","type":"fixed","system":false,"freeBytes":123,"totalBytes":456}]}';
  const calls = [];
  globalThis.sessionStorage = tokenStore(TOKEN);
  globalThis.fetch = async (url, init = {}) => {
    calls.push({ url: String(url), method: init.method ?? 'GET', headers: init.headers, credentials: init.credentials, body: init.body });
    return new Response(body, { status: 200, headers: { 'Content-Type': 'application/json' } });
  };
  const got = await bd.fetchDrives();
  assert.deepEqual(got, { drives: [{ letter: 'D', label: 'Data', type: 'fixed', system: false, freeBytes: 123, totalBytes: 456 }] });
  assert.equal(calls.length, 1);
  assert.equal(calls[0].url, '/api/blackbox/this-pc/drives');
  assert.equal(calls[0].method, 'GET');
  assert.equal(calls[0].credentials, 'same-origin');
  assert.equal(calls[0].headers['X-Audit-Token'], TOKEN);
  assert.equal(calls[0].body, undefined, 'a drives read sends no body');
  // letter, label, type, system flag and space numbers: no path travels with a drive
  for (const d of got.drives) assert.deepEqual(Object.keys(d).sort(), ['freeBytes', 'label', 'letter', 'system', 'totalBytes', 'type']);
});

test('a call without a session token is refused before any request is made', async () => {
  let called = 0;
  globalThis.sessionStorage = tokenStore('');
  globalThis.fetch = async () => { called++; return new Response('{}', { status: 200 }); };
  await assert.rejects(bd.fetchDrives(), (error) => {
    assert.ok(error instanceof bb.BlackboxError);
    assert.equal(error.message, api.NO_SESSION_MESSAGE);
    assert.equal(error.refusal.status, 401);
    assert.equal(error.refusal.reason, 'no_session');
    return true;
  });
  assert.equal(called, 0, 'no request went out');
});

test('a refused token drops the session and comes back as the no-session refusal', async () => {
  const store = tokenStore(TOKEN);
  globalThis.sessionStorage = store;
  globalThis.fetch = async () => new Response(JSON.stringify({ error: 'bad token' }), { status: 401 });
  await assert.rejects(bd.fetchDrives(), (error) => {
    assert.ok(error instanceof bb.BlackboxError);
    assert.equal(error.refusal.status, 401);
    assert.equal(error.refusal.reason, 'no_session');
    assert.equal(error.message, api.NO_SESSION_MESSAGE);
    return true;
  });
  assert.deepEqual(store.removed, ['unravis.session'], 'the dead token is dropped from the tab');
});

test('a refused drives read is a BlackboxError holding what the server said, never a stack', async () => {
  globalThis.sessionStorage = tokenStore(TOKEN);
  globalThis.fetch = async () => new Response(JSON.stringify({
    error: 'The drives list is not available.',
    reason: 'unavailable',
    what: 'Unravis could not read the drives of this PC.',
    next: 'Type the folder instead.'
  }), { status: 422 });
  await assert.rejects(bd.fetchDrives(), (error) => {
    assert.ok(error instanceof bb.BlackboxError);
    assert.equal(error.name, 'BlackboxError');
    assert.equal(error.refusal.status, 422);
    assert.equal(error.refusal.error, 'The drives list is not available.');
    assert.equal(error.refusal.reason, 'unavailable');
    assert.equal(error.refusal.what, 'Unravis could not read the drives of this PC.');
    assert.equal(error.refusal.next, 'Type the folder instead.');
    assert.equal(error.message, 'The drives list is not available.');
    return true;
  });
});

test('a refusal with no readable body keeps the status and a plain reason', async () => {
  globalThis.sessionStorage = tokenStore(TOKEN);
  globalThis.fetch = async () => new Response('gatekeeper said no', { status: 500 });
  await assert.rejects(bd.fetchDrives(), (error) => {
    assert.ok(error instanceof bb.BlackboxError);
    assert.equal(error.refusal.status, 500);
    assert.equal(error.refusal.error, 'The Blackbox request failed (500).');
    assert.equal(error.refusal.reason, 'other');
    return true;
  });
});

test('an unreachable server is the offline refusal', async () => {
  globalThis.sessionStorage = tokenStore(TOKEN);
  globalThis.fetch = async () => { throw new Error('boom at line 3'); };
  await assert.rejects(bd.fetchDrives(), (error) => {
    assert.ok(error instanceof bb.BlackboxError);
    assert.equal(error.refusal.status, 0);
    assert.equal(error.refusal.error, 'Unravis could not be reached. Check that it is still running.');
    assert.equal(error.refusal.reason, 'offline');
    return true;
  });
});
