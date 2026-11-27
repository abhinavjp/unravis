const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const { loadTypeScript } = require('./helpers/loadTs.cjs');

// The Servers screen: the rules it follows (case windows to hours, who may be ticked, what goes in a request body), its words,
// and how it is wired into the app. Nothing here talks to a server.

const root = path.resolve(__dirname, '..');
const read = (relative) => fs.readFileSync(path.join(root, relative), 'utf8');

const sv = loadTypeScript('src/lib/servers.ts');
const { SV_TEXT, serversCopyEntries } = loadTypeScript('src/lib/copy/serversCopy.ts');
const { lintScreenStrings } = loadTypeScript('src/lib/copy/copylint.ts');
const { deck } = loadTypeScript('src/lib/copy/deck.ts');
const { toFieldUtc } = loadTypeScript('src/lib/tracing/windows.ts');

const HOUR = 3600_000;
const DAY = 24 * HOUR;
const NOW = Date.UTC(2026, 10, 26, 12, 0);
const fields = (iS, iE, rS, rE) => ({ incidentStart: toFieldUtc(iS), incidentEnd: toFieldUtc(iE), referenceStart: toFieldUtc(rS), referenceEnd: toFieldUtc(rE) });

/* ------------------------------------------------------------ case windows */

test('a window start becomes whole hours back from now, at least one', () => {
  assert.equal(sv.sinceHoursFrom(NOW - 5 * HOUR, NOW), 5);
  assert.equal(sv.sinceHoursFrom(NOW - 5 * HOUR - 1, NOW), 6);
  assert.equal(sv.sinceHoursFrom(NOW, NOW), 1);
  assert.equal(sv.sinceHoursFrom(NOW + HOUR, NOW), 1);
});

test('windows the timeline starts with are not treated as the case range', () => {
  const start = NOW - 3 * HOUR;
  const untouched = fields(start, start + HOUR, start - HOUR, start);
  assert.equal(sv.looksLikeDefaultWindows(untouched), true);
  assert.equal(sv.caseRange(untouched, NOW).state, 'default-shaped');
  assert.equal(sv.caseRange(null, NOW).state, 'none');
  assert.equal(sv.caseRange(undefined, NOW).state, 'none');
  assert.equal(sv.caseRange({ incidentStart: '', incidentEnd: '', referenceStart: '', referenceEnd: '' }, NOW).state, 'none');
});

test('a chosen window gives the hours back to the earlier start and to the incident start', () => {
  const w = fields(NOW - 6 * HOUR, NOW - 3 * HOUR, NOW - 30 * HOUR, NOW - 6 * HOUR);
  const range = sv.caseRange(w, NOW);
  assert.equal(range.state, 'ok');
  assert.equal(range.windowHours, 6);
  assert.equal(range.bothHours, 30);
  assert.equal(sv.rangeHours('case', range, NOW), 30);
  assert.equal(sv.rangeHours('window', range, NOW), 6);
  assert.equal(sv.rangeHours('24', range, NOW), 24);
  assert.equal(sv.rangeHours('72', range, NOW), 72);
  assert.equal(sv.rangeHours('168', range, NOW), 168);
  assert.deepEqual(sv.rangeChoices(range), ['case', 'window', '24', '72', '168']);
});

test('without a usable case range only the fixed ranges are offered and the case ones give no hours', () => {
  const none = sv.caseRange(null, NOW);
  assert.deepEqual(sv.rangeChoices(none), ['24', '72', '168']);
  assert.equal(sv.rangeHours('case', none, NOW), null);
  assert.equal(sv.rangeHours('window', none, NOW), null);
  assert.equal(sv.rangeHours('72', none, NOW), 72);
});

test('a window longer than 7 days is refused, for the incident window and for the comparison period', () => {
  const longIncident = fields(NOW - 9 * DAY, NOW - DAY, NOW - 10 * DAY, NOW - 9 * DAY);
  assert.deepEqual(sv.caseRange(longIncident, NOW), { state: 'too-long', which: 'window' });
  const longComparison = fields(NOW - 2 * DAY, NOW - DAY, NOW - 10 * DAY, NOW - 2 * DAY);
  assert.deepEqual(sv.caseRange(longComparison, NOW), { state: 'too-long', which: 'comparison' });
  const exactlySeven = fields(NOW - 2 * DAY, NOW - DAY, NOW - 9 * DAY, NOW - 2 * DAY);
  assert.equal(sv.caseRange(exactlySeven, NOW).state, 'ok');
  assert.match(sv.caseRangeProblem({ state: 'too-long', which: 'window' }), /incident window/);
  assert.match(sv.caseRangeProblem({ state: 'too-long', which: 'comparison' }), /comparison period/);
  assert.equal(sv.caseRangeProblem(sv.caseRange(exactlySeven, NOW)), '');
});

test('windows that start more than a year back are refused, and the hours never pass what the backend takes', () => {
  const old = fields(NOW - 400 * DAY, NOW - 399 * DAY, NOW - 401 * DAY, NOW - 400 * DAY);
  assert.equal(sv.caseRange(old, NOW).state, 'too-old');
  assert.equal(sv.MAX_SINCE_HOURS, 8784);
  const body = sv.buildPullBody({ machine: 'a', pullKind: 'blackbox', dir: '/x' }, 99999);
  assert.equal(body.sinceHours, 8784);
  assert.equal(sv.buildPullBody({ machine: 'a', pullKind: 'blackbox', dir: '/x' }, -4).sinceHours, 0);
  assert.equal(sv.buildPullBody({ machine: 'a', pullKind: 'blackbox', dir: '/x' }, 7.9).sinceHours, 7);
  assert.equal(sv.buildPullBody({ machine: 'a', pullKind: 'blackbox', dir: '/x' }, Number.NaN).sinceHours, 0);
});

/* ---------------------------------------------------------------- failures */

test('a refusal is shown in the backend words first and its next step after them', () => {
  const f = sv.failureFromResponse(409, { error: 'A pull is already running on this server.', reason: 'busy', next: 'Wait for it to end.' });
  assert.equal(f.reason, 'busy');
  assert.equal(sv.failureText(f), 'A pull is already running on this server. Wait for it to end.');
  assert.equal(sv.failureText({ status: 409, error: 'Said it. Wait for it to end.', reason: 'busy', next: 'Wait for it to end.' }), 'Said it. Wait for it to end.');
});

test('a refusal with no sentence falls back to the table, and anything unreadable becomes a plain general one', () => {
  for (const reason of Object.keys(sv.FAILURE_TEXT)) {
    assert.equal(sv.failureText({ status: 0, error: '', reason }), sv.FAILURE_TEXT[reason]);
  }
  assert.equal(sv.failureFromResponse(500, 'oops').reason, 'other');
  assert.equal(sv.failureFromResponse(500, 'oops').error, sv.FAILURE_TEXT.other);
  assert.equal(sv.failureFromResponse(500, { reason: 'unlisted' }).error, 'The request failed (500).');
  assert.equal(sv.failureText({ status: 0, error: '', reason: 'never-heard-of-it' }), sv.FAILURE_TEXT.other);
});

test('a changed host key keeps both fingerprints from the refusal', () => {
  const f = sv.failureFromResponse(409, { reason: 'host-key-changed', pinned: 'SHA256:old', presented: 'SHA256:new', next: 'Ask your admin.' });
  assert.equal(f.pinned, 'SHA256:old');
  assert.equal(f.presented, 'SHA256:new');
  assert.equal(sv.failureFix('host-key-changed'), 'trust');
});

test('each failure points to the one place that fixes it', () => {
  assert.equal(sv.failureFix('need-host-key'), 'trust');
  assert.equal(sv.failureFix('sign-in'), 'connect');
  assert.equal(sv.failureFix('folder-not-allowed'), 'folders');
  assert.equal(sv.failureFix('machine-not-listed'), 'machines');
  assert.equal(sv.failureFix('busy'), 'wait');
  assert.equal(sv.failureFix('timeout'), 'none');
});

test('an error that is not a refusal never shows its own text', () => {
  assert.equal(sv.errorText(new Error('boom with a secret inside')), sv.FAILURE_TEXT.offline);
  assert.equal(sv.errorText(new sv.ServersError({ status: 400, error: 'No.', reason: 'x' })), 'No.');
});

/* ---------------------------------------------------------------- machines */

test('only an accepted machine that is not known to be off may be ticked', () => {
  assert.equal(sv.machineChoice({ state: 'accepted', online: true }).selectable, true);
  assert.equal(sv.machineChoice({ state: 'accepted', online: false }).selectable, false);
  assert.equal(sv.machineChoice({ state: 'accepted' }).selectable, true);
  assert.equal(sv.machineChoice({ state: 'pending' }).selectable, false);
  assert.equal(sv.machineChoice({ state: 'denied' }).selectable, false);
  assert.equal(sv.machineChoice({ state: 'rejected' }).selectable, false);
});

test('ticking follows the pick rules and stops at 20 machines', () => {
  const machines = [
    { id: 'a', state: 'accepted', online: true, chosen: true },
    { id: 'b', state: 'accepted', online: false },
    { id: 'c', state: 'pending' },
    { id: 'd', state: 'accepted', online: true },
    { id: 'e', state: 'accepted', online: false, chosen: true }
  ];
  assert.deepEqual(sv.initialPicks(machines), ['a']);
  assert.deepEqual(sv.togglePick(['a'], machines, 'b', true), ['a']);
  assert.deepEqual(sv.togglePick(['a'], machines, 'c', true), ['a']);
  assert.deepEqual(sv.togglePick(['a'], machines, 'zz', true), ['a']);
  assert.deepEqual(sv.togglePick(['a'], machines, 'd', true), ['a', 'd']);
  assert.deepEqual(sv.togglePick(['a', 'd'], machines, 'd', true), ['a', 'd']);
  assert.deepEqual(sv.togglePick(['a', 'd'], machines, 'a', false), ['d']);
  const many = Array.from({ length: 25 }, (_, i) => ({ id: 'm' + i, state: 'accepted', online: true }));
  const full = many.slice(0, 20).map((m) => m.id);
  assert.deepEqual(sv.togglePick(full, many, 'm21', true), full);
  assert.equal(sv.initialPicks(many.map((m) => ({ ...m, chosen: true }))).length, 20);
});

test('the machine counts split accepted, online, off, waiting and refused', () => {
  const machines = [
    { id: 'a', state: 'accepted', online: true }, { id: 'b', state: 'accepted', online: false }, { id: 'c', state: 'accepted' },
    { id: 'd', state: 'pending' }, { id: 'e', state: 'denied' }
  ];
  assert.deepEqual(sv.machineCounts(machines), { accepted: 3, online: 1, offline: 1, waiting: 1, refused: 1 });
});

/* ----------------------------------------------------------------- folders */

test('a folder path must be a full path to a folder', () => {
  assert.equal(sv.folderProblem('/var/log/redis'), '');
  assert.equal(sv.folderProblem('D:\\Logs'), '');
  assert.notEqual(sv.folderProblem(''), '');
  assert.notEqual(sv.folderProblem('logs'), '');
  assert.notEqual(sv.folderProblem('/var/../etc'), '');
  assert.notEqual(sv.folderProblem('D:\\'), '');
  assert.notEqual(sv.folderProblem('/'), '');
  assert.notEqual(sv.folderProblem('D:\\Log*'), '');
});

test('adding and removing a folder gives a new map and leaves the old one alone', () => {
  const before = { blackbox: ['/opt/bb'] };
  const added = sv.withFolder(before, 'iis', 'D:\\Logs\\W3SVC1');
  assert.deepEqual(added, { blackbox: ['/opt/bb'], iis: ['D:\\Logs\\W3SVC1'] });
  assert.deepEqual(before, { blackbox: ['/opt/bb'] });
  assert.deepEqual(sv.withFolder(added, 'iis', 'D:\\Logs\\W3SVC1'), added);
  assert.deepEqual(sv.withFolder(before, 'iis', 'relative'), before);
  assert.deepEqual(sv.withFolder(before, 'nothing', '/x'), before);
  assert.deepEqual(sv.withoutFolder(added, 'iis', 'D:\\Logs\\W3SVC1'), { blackbox: ['/opt/bb'] });
});

test('bulk folder additions are valid, idempotent and do not mutate the input', () => {
  const before = { other: ['/existing'] };
  const expected = { other: ['/existing'], blackbox: ['D:\\UnravisBlackbox'], iis: ['C:\\inetpub\\logs\\LogFiles\\W3SVC1'] };
  const added = sv.withFolders(before, [...sv.QUICK_FOLDERS, { kind: 'iis', path: 'relative' }]);
  assert.deepEqual(added, expected);
  assert.deepEqual(before, { other: ['/existing'] });
  assert.notEqual(added.other, before.other);
  assert.deepEqual(sv.withFolders(added, sv.QUICK_FOLDERS), expected);
});

test('missing quick folders reflects the folders already listed', () => {
  assert.deepEqual(sv.missingQuickFolders({}), sv.QUICK_FOLDERS);
  assert.deepEqual(sv.missingQuickFolders(sv.withFolders({}, sv.QUICK_FOLDERS)), []);
  assert.deepEqual(sv.missingQuickFolders({ blackbox: ['D:\\UnravisBlackbox'] }), [sv.QUICK_FOLDERS[1]]);
});

test('only a folder that could be read becomes something to copy, with the pull kind the backend uses', () => {
  const result = { folders: [
    { kind: 'blackbox', path: '/opt/bb', ok: true, files: 3 },
    { kind: 'sql', path: '/var/opt/mssql/log', ok: true, files: 1 },
    { kind: 'other', path: '/srv/app', ok: true, files: 9 },
    { kind: 'redis', path: '/var/log/redis', ok: false, files: 0, error: 'No such folder.' },
    { kind: 'unknown-kind', path: '/x', ok: true, files: 1 }
  ] };
  const targets = sv.readableTargets('web-1', result);
  assert.deepEqual(targets.map((t) => t.pullKind), ['blackbox', 'sqlserver', 'applog']);
  assert.deepEqual(targets.map((t) => t.dir), ['/opt/bb', '/var/opt/mssql/log', '/srv/app']);
  assert.equal(targets[0].key, 'web-1|blackbox|/opt/bb');
  assert.equal(targets[0].machine, 'web-1');
});

test('the checks of a machine list only what the server answered', () => {
  const result = { host: { os: 'linux', description: 'Ubuntu', clockOffset: 0 }, folders: [{ kind: 'blackbox', path: '/opt/bb', ok: true, files: 2 }, { kind: 'iis', path: '/nope', ok: false, files: 0, error: 'No such folder.' }] };
  const lines = sv.testChecks(result, 2);
  assert.deepEqual(lines.map((l) => l.id), ['master', 'machine', 'folder:blackbox:/opt/bb', 'folder:iis:/nope']);
  assert.equal(lines[2].ok, true);
  assert.equal(lines[3].note, 'No such folder.');
  assert.equal(sv.testVerdict(result), 'partial');
  assert.equal(sv.testVerdict({ ...result, folders: [result.folders[0]] }), 'ready');
  assert.equal(sv.testVerdict({ ...result, folders: [result.folders[1]] }), 'none');
  assert.equal(sv.testVerdict({ ...result, folders: [] }), 'none');
  assert.equal(sv.testVerdict(null), 'none');
  assert.equal(sv.testChecks(result, 0).some((l) => l.id === 'no-folders'), true);
});

/* ------------------------------------------------------------------- forms */

const goodForm = () => ({ name: '', host: 'salt.example.test', port: '22', user: 'ops', os: 'linux', auth: 'password', seedMachine: 'web-1' });
const savedServer = (fields = {}) => ({ ...goodForm(), id: 'saved', port: 22, hasSecret: true, ...fields });

test('the connect form names what is missing in plain sentences', () => {
  assert.deepEqual(sv.connectProblems(goodForm(), true), []);
  assert.equal(sv.connectProblems({ ...goodForm(), host: ' ' }, true).length, 1);
  assert.equal(sv.connectProblems({ ...goodForm(), port: '0' }, true).length, 1);
  assert.equal(sv.connectProblems({ ...goodForm(), port: '70000' }, true).length, 1);
  assert.equal(sv.connectProblems({ ...goodForm(), port: 'abc' }, true).length, 1);
  assert.equal(sv.connectProblems({ ...goodForm(), user: '' }, true).length, 1);
  assert.match(sv.connectProblems(goodForm(), false)[0], /password/i);
  assert.match(sv.connectProblems({ ...goodForm(), auth: 'key' }, false)[0], /key file/i);
  assert.equal(sv.connectProblems({ host: '', port: '', user: '', os: 'linux', auth: 'password' }, false).length, 4);
});

test('the draft matches a connection by address, operating system and sign-in type', () => {
  const entry = savedServer();
  const form = { ...goodForm(), host: ' SALT.example.test ', user: ' ops ', port: '022' };
  assert.equal(sv.matchesConnection(form, entry), true);
  for (const change of [{ host: 'other.test' }, { port: '2222' }, { user: 'OPS' }, { os: 'windows' }, { auth: 'key' }]) {
    assert.equal(sv.matchesConnection({ ...form, ...change }, entry), false);
  }
  assert.equal(sv.matchesConnection(form, null), false);
});

test('duplicate lookup matches just the host, port and case-sensitive user', () => {
  const entry = savedServer({ os: 'windows', auth: 'key' });
  const list = [savedServer({ user: 'OPS' }), savedServer({ port: 2222 }), entry];
  assert.equal(sv.findConnection({ ...goodForm(), host: 'SALT.EXAMPLE.TEST' }, list), entry);
  assert.equal(sv.findConnection({ ...goodForm(), host: 'different.test' }, list), undefined);
  assert.equal(sv.findConnection(goodForm(), null), undefined);
});

const makeFlow = (serversApi) => {
  const { ServersFlow } = loadTypeScript('src/lib/serversFlow.svelte.ts', { './serversApi': { serversApi } });
  return new ServersFlow({ windows: () => null, now: () => NOW });
};

test('bulk folder additions save once and skip unchanged maps', async () => {
  const writes = [];
  const flow = makeFlow({ patch: async (id, body) => { writes.push({ id, body }); return savedServer(body); } });
  flow.server = savedServer({ folders: {} });
  assert.equal(await flow.addFolders(sv.QUICK_FOLDERS), true);
  assert.equal(writes.length, 1);
  assert.deepEqual(writes[0].body.folders, sv.withFolders({}, sv.QUICK_FOLDERS));
  assert.equal(await flow.addFolders([...sv.QUICK_FOLDERS, { kind: 'iis', path: 'relative' }]), true);
  assert.equal(writes.length, 1);
});

test('Back keeps the draft, cancels key fetching, and Next reuses the saved entry', async () => {
  const requests = [];
  let adds = 0;
  const flow = makeFlow({
    add: async () => { adds++; return savedServer(); },
    hostKeys: (id, signal) => new Promise((resolve) => requests.push({ id, signal, resolve }))
  });
  flow.draft = { ...goodForm(), host: 'SALT.example.test' };
  assert.equal(await flow.connect(flow.draft, 'fixture-password', ''), true);
  assert.equal(flow.step, 'trust');
  assert.equal(flow.busy, 'keys');
  flow.go('connect');
  assert.equal(flow.busy, '');
  assert.equal(requests[0].signal.aborted, true);
  assert.equal(flow.draft.host, 'SALT.example.test');
  assert.equal(await flow.connect(flow.draft, '', ''), true);
  assert.equal(adds, 1);
  assert.deepEqual(requests.map(({ id }) => id), ['saved', 'saved']);

  requests[0].resolve({ keys: [{ fingerprint: 'old' }] });
  await Promise.resolve();
  assert.deepEqual(flow.hostKeys, []);
  assert.equal(flow.busy, 'keys');
  requests[1].resolve({ keys: [{ fingerprint: 'current' }] });
  await Promise.resolve();
  assert.equal(flow.hostKeys[0].fingerprint, 'current');
  assert.equal(flow.error, null);
});

test('Next with a new secret updates the current entry and skips trust when it is pinned', async () => {
  const entry = savedServer({ hostKey: 'pinned' });
  const writes = [];
  const flow = makeFlow({
    setSecret: async (id, auth) => { writes.push([id, auth]); },
    machines: async () => ({ machines: [] })
  });
  flow.server = entry;
  assert.equal(await flow.connect(goodForm(), 'fixture-password', ''), true);
  assert.deepEqual(writes, [['saved', 'password']]);
  assert.equal(flow.step, 'pick');
});

test('a duplicate refreshes the list and uses the saved or newly entered secret', async () => {
  const entry = savedServer({ hostKey: 'pinned' });
  for (const secret of ['', 'fixture-password']) {
    const writes = [];
    let lists = 0;
    const flow = makeFlow({
      add: async () => { throw new sv.ServersError({ status: 409, reason: 'duplicate', error: 'Already saved.' }); },
      list: async () => { lists++; return { items: [entry] }; },
      setSecret: async (id) => { writes.push(id); },
      machines: async () => ({ machines: [] })
    });
    flow.draft = goodForm();
    assert.equal(await flow.connect(flow.draft, secret, ''), true);
    assert.equal(lists, 1);
    assert.deepEqual(writes, secret ? ['saved'] : []);
    assert.equal(flow.server.id, 'saved');
    assert.equal(flow.step, 'pick');
  }
});

test('correcting a new untrusted connection removes only the earlier entry created here', async () => {
  const older = savedServer({ id: 'older', host: 'older.example.test' });
  const forgotten = [];
  const flow = makeFlow({
    add: async (body) => savedServer({ id: body.host, host: body.host }),
    hostKeys: async () => ({ keys: [] }),
    forget: async (id) => { forgotten.push(id); }
  });
  flow.server = older;
  flow.list = [older];
  for (const host of ['typo.example.test', 'correct.example.test']) {
    flow.draft = { ...goodForm(), host };
    assert.equal(await flow.connect(flow.draft, 'fixture-password', ''), true);
    flow.go('connect');
  }
  assert.deepEqual(forgotten, ['typo.example.test']);
  assert.deepEqual(flow.list.map(({ id }) => id), ['older', 'correct.example.test']);
});

test('machine duplicates keep the strongest state and the first position and equal entry', () => {
  const m = (id, state, chosen = false) => ({ id, state, chosen });
  const accepted = { ...m('a', 'accepted', true), online: true };
  assert.deepEqual(sv.dedupeMachines([m('a', 'denied'), m('b', 'rejected'), accepted, m('b', 'pending'), m('a', 'accepted')]), [accepted, m('b', 'pending')]);
});

test('request deadline reports timeout and parent cancellation stays an abort', async () => {
  const parent = new AbortController();
  let timedSignal;
  await assert.rejects(sv.withTimeout(parent.signal, 5, (signal) => { timedSignal = signal; return new Promise(() => {}); }), (e) => e.failure?.reason === 'timeout');
  assert.equal(timedSignal.aborted, true);
  const other = new AbortController();
  const pending = sv.withTimeout(other.signal, 1000, () => new Promise(() => {}));
  other.abort();
  await assert.rejects(pending, (e) => e.name === 'AbortError');
});

test('Linux machine listing keeps sudo on after a successful retry and rolls it back after failure', async () => {
  const original = { status: 502, reason: 'salt-master', error: 'The original refusal.' };
  for (const succeeds of [true, false]) {
    const writes = [];
    let reads = 0;
    const flow = makeFlow({
      patch: async (id, body) => { writes.push(body.saltSudo); return savedServer(body); },
      machines: async () => {
        if (++reads === 1) throw new sv.ServersError(original);
        if (!succeeds) throw new sv.ServersError({ ...original, error: 'The retry refusal.' });
        return { machines: [{ id: 'web-1', state: 'accepted', online: true, chosen: true }] };
      }
    });
    flow.server = savedServer({ saltSudo: false });
    await flow.loadMachines();
    assert.equal(reads, 2);
    assert.deepEqual(writes, succeeds ? [true] : [true, false]);
    assert.equal(flow.sudo, succeeds);
    assert.equal(flow.server.saltSudo, succeeds);
    assert.deepEqual(flow.picks, succeeds ? ['web-1'] : []);
    assert.equal(flow.note, succeeds ? SV_TEXT.pickSudoSwitched : '');
    assert.equal(flow.error, succeeds ? null : original);
    assert.equal(flow.busy, '');
  }
});

test('leaving during a sudo retry rolls back the setting without showing a stale error', async () => {
  const writes = [];
  let finishEnable;
  let started;
  const enabling = new Promise((resolve) => { started = resolve; });
  const flow = makeFlow({
    patch: async (id, body) => {
      writes.push(body.saltSudo);
      if (body.saltSudo) {
        started();
        await new Promise((resolve) => { finishEnable = resolve; });
      }
      return savedServer(body);
    },
    machines: async () => { throw new sv.ServersError({ status: 502, reason: 'salt-master', error: 'Refused.' }); }
  });
  flow.server = savedServer({ saltSudo: false });
  const loading = flow.loadMachines();
  await enabling;
  flow.go('connect');
  assert.equal(flow.busy, '');
  finishEnable();
  await loading;
  assert.deepEqual(writes, [true, false]);
  assert.equal(flow.step, 'connect');
  assert.equal(flow.sudo, false);
  assert.equal(flow.error, null);
});

test('a machine name is letters, digits, dots, dashes and underscores', () => {
  assert.equal(sv.isMachineId('web-1'), true);
  assert.equal(sv.isMachineId('web_1.example'), true);
  assert.equal(sv.isMachineId(''), false);
  assert.equal(sv.isMachineId('-web'), false);
  assert.equal(sv.isMachineId('web 1'), false);
  assert.equal(sv.isMachineId('web;1'), false);
});

/* ------------------------------------------------- request bodies, no leaks */

test('the add body is the one body that holds the secret and it goes through a Salt master only', () => {
  const body = sv.buildAddBody(goodForm(), 'the-password', '');
  assert.equal(body.secret, 'the-password');
  assert.deepEqual(body.transports, ['salt']);
  assert.deepEqual(body.minionIds, []);
  assert.equal(body.saltSudo, false);
  assert.equal(body.name, 'salt.example.test');
  assert.equal(body.port, 22);
  assert.equal(body.passphrase, '');
  const key = sv.buildAddBody({ ...goodForm(), auth: 'key', name: ' Main ' }, '-----KEY-----', 'pass');
  assert.equal(key.passphrase, 'pass');
  assert.equal(key.name, 'salt.example.test');
  // a passphrase is dropped for a password sign in
  assert.equal(sv.buildAddBody(goodForm(), 'x', 'leftover').passphrase, '');
  assert.deepEqual(sv.buildSecretBody('password', 's', 'leftover'), { auth: 'password', secret: 's', passphrase: '' });
});

test('no other body carries a secret key at any depth', () => {
  const target = { machine: 'web-1', pullKind: 'blackbox', dir: '/opt/bb' };
  const bodies = [
    sv.buildPatchBody({ minionIds: ['a', 'b'], saltSudo: true, folders: { blackbox: ['/opt/bb'] }, name: ' x ' }),
    sv.buildPatchBody({}),
    sv.buildPullBody(target, 24),
    sv.buildPullBody(target, 24, 'job-1'),
    sv.buildPinBody('SHA256:abc'),
    sv.buildReplaceBody('SHA256:old', 'SHA256:new'),
    sv.buildMachineBody('web-1')
  ];
  for (const body of bodies) assert.deepEqual(sv.secretKeysIn(body), [], JSON.stringify(body));
  assert.deepEqual(sv.secretKeysIn(sv.buildAddBody(goodForm(), 's', 'p')).sort(), ['passphrase', 'secret']);
  assert.deepEqual(sv.secretKeysIn({ a: [{ token: 1 }], b: { c: { password: 2 } } }).sort(), ['password', 'token']);
});

test('the patch body sends only what it is given and the pull body names the folder it reads', () => {
  assert.deepEqual(sv.buildPatchBody({ saltSudo: false }), { saltSudo: false });
  assert.deepEqual(sv.buildPatchBody({ minionIds: ['a'] }), { minionIds: ['a'] });
  assert.deepEqual(sv.buildPullBody({ machine: 'm', pullKind: 'sqlserver', dir: '/d' }, 24), { machine: 'm', kind: 'sqlserver', dir: '/d', pattern: '', sinceHours: 24 });
  assert.equal(sv.buildPullBody({ machine: 'm', pullKind: 'sqlserver', dir: '/d' }, 24, 'j1').resume, 'j1');
});

/* ------------------------------------------------------------ plans, jobs */

test('a plan with a problem or no files is never counted', () => {
  const ok = { kind: 'blackbox', dir: '/a', files: [{ name: 'a', size: 1, mtime: '2026-11-26T10:00:00Z' }, { name: 'b', size: 2, mtime: '2026-11-26T11:00:00Z' }], skipped: [], bytes: 3 };
  const bad = { ...ok, problem: 'Too large.' };
  const empty = { ...ok, files: [], bytes: 0 };
  assert.deepEqual(sv.pickedTotals([ok, bad, empty, null, ok]), { items: 2, files: 4, bytes: 6 });
  const sum = sv.planSummary(ok);
  assert.equal(sum.files, 2);
  assert.equal(sum.empty, false);
  assert.notEqual(sum.oldest, sum.newest);
  assert.equal(sv.planSummary(bad).problem, 'Too large.');
  assert.equal(sv.planSummary(empty).empty, true);
});

test('the progress is a share of the planned bytes and never 100 until the copy is done', () => {
  const progress = { file: 'a', fileBytes: 1, jobBytes: 50, filesTotal: 2, filesDone: 1 };
  assert.equal(sv.jobPercent({ state: 'running', progress }, 100), 50);
  assert.equal(sv.jobPercent({ state: 'running', progress: { ...progress, jobBytes: 500 } }, 100), 99);
  assert.equal(sv.jobPercent({ state: 'running', progress }, 0), null);
  assert.equal(sv.jobPercent({ state: 'running' }, 100), null);
  assert.equal(sv.jobPercent({ state: 'done' }, 0), 100);
});

test('the result counts only the files that were copied', () => {
  const result = { files: [
    { name: 'a', outName: 'a', rawBytes: 5, outBytes: 5, status: 'done', verified: true, linesScrubbed: 2, linesDropped: 1 },
    { name: 'b', outName: 'b', rawBytes: 7, outBytes: 7, status: 'done', verified: false, changedWhileCopying: true, linesScrubbed: 0, linesDropped: 0 },
    { name: 'c', outName: 'c', rawBytes: 9, outBytes: 9, status: 'failed', verified: false, linesScrubbed: 5, linesDropped: 5 }
  ] };
  assert.deepEqual(sv.resultSummary(result), { files: 2, verified: 1, notChecked: 1, changed: 1, linesHidden: 2, linesDropped: 1, bytes: 12 });
  assert.equal(sv.resultSummary(undefined).files, 0);
});

test('a copied file is fetched from the copy of its own server and job, with the name made safe for a URL', () => {
  assert.equal(sv.pulledFileUrl('s 1', 'j/1', 'a b.log'), '/api/servers/s%201/pulls/j%2F1/files/a%20b.log');
});

test('sizes and counts read as plain words', () => {
  assert.equal(sv.formatBytes(0), '0 B');
  assert.match(sv.formatBytes(1536), /KB/);
  assert.match(sv.formatBytes(5 * 1024 * 1024), /MB/);
  assert.equal(sv.countText(1, 'file'), '1 file');
  assert.equal(sv.countText(3, 'file'), '3 files');
});

/* -------------------------------------------------------- commands to copy */

test('the PC key check reads the fingerprint in Windows PowerShell', () => {
  const input = { os: 'linux', host: 'salt.example.test', port: 2222 };
  const lines = sv.accessCommands('key', input).split('\n');
  assert.ok(lines.includes('ssh -v -o BatchMode=yes -o StrictHostKeyChecking=no -o UserKnownHostsFile=NUL -o ConnectTimeout=8 -p 2222 probe@salt.example.test 2>&1 | Select-String "Server host key"'));
  assert.match(lines.filter((line) => line.startsWith('#')).join(' '), /sends no password.*Permission denied.*expected/i);
  assert.ok(lines.includes('ssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub'));
  assert.match(sv.accessCommands('key', { ...input, host: 'host; whoami' }), /probe@<address> /);
});

test('the commands for the person to run only read, and name the machine and folders given', () => {
  const input = { os: 'linux', host: 'salt.example.test', port: 22, user: 'ops', machines: ['web-1'], folders: { blackbox: ['/opt/bb'] }, sudo: false };
  for (const part of ['key', 'salt', 'admin-key', 'admin-salt']) {
    const text = sv.accessCommands(part, input);
    assert.equal(typeof text, 'string');
    assert.ok(text.length > 0, part);
    assert.equal(/\brm\b|\bdel\b|systemctl (restart|stop)|salt-key (-a|-d|-A|-D)/i.test(text), false, part + ' must not change anything');
  }
  assert.match(sv.accessCommands('salt', input), /web-1/);
});

/* --------------------------------------------------------------------- copy */

test('every word of the Servers panel passes the copy lint', () => {
  assert.deepEqual(lintScreenStrings(serversCopyEntries(), deck.terms), []);
});

test('the copy lint includes every Servers text entry', () => {
  const entries = new Map(serversCopyEntries().map(({ id, text }) => [id, text]));
  for (const [name, value] of Object.entries(SV_TEXT)) {
    const id = 'servers.' + name;
    if (typeof value === 'string') assert.equal(entries.get(id), value, id);
    else if (Array.isArray(value)) value.forEach((text, i) => assert.equal(entries.get(`${id}.${i}`), text));
    else assert.ok([...entries].some(([key, text]) => (key === id || key.startsWith(id + '.')) && text), id);
  }
});

test('the folder guidance has text and is included in copy lint', () => {
  const entries = new Map(serversCopyEntries().map(({ id, text }) => [id, text]));
  for (const key of ['foldersWhy', 'foldersWhyShort', 'foldersAddSuggested', 'reachNoFolders']) {
    assert.ok(SV_TEXT[key].trim(), key);
    assert.equal(entries.get('servers.' + key), SV_TEXT[key]);
  }
});

test('the Servers words never use the internal terms, and Salt is only the Salt master', () => {
  const entries = serversCopyEntries();
  assert.ok(entries.length > 80);
  for (const entry of entries) {
    assert.equal(/transport|minion|allowlist/i.test(entry.text), false, entry.id + ': ' + entry.text);
    for (const m of entry.text.matchAll(/\bSalt\b(?! master)/g)) assert.fail(entry.id + ' says Salt on its own: ' + entry.text + ' at ' + m.index);
  }
});

test('the panel names the actions the way the owner asked', () => {
  assert.equal(SV_TEXT.title, 'Servers');
  assert.match(SV_TEXT.trustGo, /Trust/);
  assert.match(SV_TEXT.hasGo(2), /^Copy 2 items/);
  assert.match(SV_TEXT.readOnly, /only reads/);
  assert.equal(SV_TEXT.steps.length, 7);
});

/* ------------------------------------------------------------------ wiring */

test('the header opens the Servers panel and the app mounts it', () => {
  const header = read('src/components/AppHeader.svelte');
  assert.match(header, /id: 'servers', label: 'Servers', open: \(\) => openSidePanel\('servers'\)/);
  const app = read('src/App.svelte');
  assert.match(app, /ServersPanel = lazyComponent\(\(\) => import\('\.\/components\/ServersPanel\.svelte'\)\)/);
  assert.match(app, /panels\.sidePanel === 'servers'/);
  const state = read('src/lib/appState.svelte.ts');
  assert.match(state, /'servers'/);
});

test('the panel never writes the case windows or keeps a secret in its state', () => {
  const files = ['src/components/ServersPanel.svelte', 'src/lib/serversFlow.svelte.ts', 'src/lib/serversApi.ts',
    ...fs.readdirSync(path.join(root, 'src/components/servers')).map((f) => 'src/components/servers/' + f)];
  for (const file of files) {
    const text = read(file);
    assert.equal(/saveWindows|forgetWindows|savedWindows\s*=[^=]/.test(text), false, file + ' writes the case windows');
    assert.equal(/localStorage|sessionStorage|console\.(log|info|debug)/.test(text), false, file + ' stores or logs');
  }
  const flow = read('src/lib/serversFlow.svelte.ts');
  assert.equal(/\b(password|passphrase|secret)\s*=\s*\$state/i.test(flow), false);
  const connect = read('src/components/servers/ConnectStep.svelte');
  assert.match(connect, /if \(await flow\.connect\([^\n]+\)\) clearSecrets\(\)/);
  assert.match(connect, /if \([^\n]*await flow\.saveSecret\([^\n]+\)\) clearSecrets\(\)/);
  assert.equal(/finally \{[^}]*clearSecrets\(\)/.test(connect), false);
});
