const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

const root = path.resolve(__dirname, '..');
// Modules are cached by file, so cases.ts is one instance for the test, discover.ts and the fake (a CaseApiError is the same class everywhere).
const loaded = new Map();
function loadTypeScript(relativePath) {
  const filename = path.join(root, relativePath);
  if (loaded.has(filename)) return loaded.get(filename).exports;
  const source = fs.readFileSync(filename, 'utf8');
  const output = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022, esModuleInterop: true } }).outputText;
  const module = { exports: {} };
  loaded.set(filename, module);
  const nodeRequire = createRequire(filename);
  const load = (request) => /^\.\.?\//.test(request) && fs.existsSync(path.join(path.dirname(filename), request + '.ts'))
    ? loadTypeScript(path.relative(root, path.join(path.dirname(filename), request + '.ts')))
    : nodeRequire(request);
  new Function('require', 'module', 'exports', output)(load, module, module.exports);
  return module.exports;
}
const copyOf = (name) => fs.readFileSync(path.join(root, 'src/lib/copy', name), 'utf8');
const read = (relative) => fs.readFileSync(path.join(root, relative), 'utf8');

const cases = loadTypeScript('src/lib/cases.ts');
const discover = loadTypeScript('src/lib/discover.ts');
const copy = loadTypeScript('src/lib/copy/discoverCopy.ts');
const fake = loadTypeScript('src/lib/discoverFake.ts');
const intent = loadTypeScript('src/lib/discoverIntent.ts');
const { deck } = loadTypeScript('src/lib/copy/deck.ts');
const { lintScreenStrings } = loadTypeScript('src/lib/copy/copylint.ts');

const { fakeFinding, createFakeClient, FAKE_SCAN_ID } = fake;
const { CaseApiError } = cases;

// Synthetic values only: made-up ids, a made-up machine, dates in 2001.
const CASE_ID = 'abcd2345abcd2345abcd234567';
const TOKEN = 'session-token-1';
const noSleep = async () => {};
const MB = 1024 * 1024;
const GB = 1024 * MB;

const realFetch = globalThis.fetch;
function mockServer(handler) {
  const calls = [];
  globalThis.sessionStorage = { getItem: () => TOKEN, setItem() {}, removeItem() {} };
  globalThis.fetch = async (url, init = {}) => {
    const call = { url: String(url), method: init.method ?? 'GET', body: init.body, headers: init.headers };
    calls.push(call);
    const answer = await handler(call, calls);
    if (answer instanceof Error) throw answer;
    return new Response(answer.status === 204 ? null : JSON.stringify(answer.body ?? {}), { status: answer.status ?? 200, headers: { 'Content-Type': 'application/json' } });
  };
  return calls;
}
test.afterEach(() => { globalThis.fetch = realFetch; delete globalThis.sessionStorage; discover.resetEntryCache(); });

const f = (id, extra = {}) => fakeFinding({ id, ...extra });

/* ------------------------------------------------------------------ copy */

test('every string and sample of the discover copy passes the copy lint', () => {
  const entries = copy.discoverCopyEntries();
  assert.ok(entries.length > 100, 'the lint covers the strings and the sample outputs');
  assert.deepEqual(lintScreenStrings(entries, deck.terms), []);
});

test('the copy holds the exact strings the spec names', () => {
  assert.equal(copy.DISCOVER_TEXT.windowsOnly, 'Scanning is available on Windows only.');
  assert.equal(copy.DISCOVER_TEXT.unavailable, 'Scanning is not available in this build.');
  assert.deepEqual(Object.values(copy.DISCOVER_TEXT.groups), ['Web server', 'Windows', 'App logs', 'Databases', 'Live']);
  assert.equal(copy.DISCOVER_TEXT.tagAdmin, 'Needs administrator');
  assert.equal(copy.DISCOVER_TEXT.tagLarge, 'Large');
  assert.equal(copy.DISCOVER_TEXT.tagExport, 'Export needed');
  assert.equal(copy.estimateText(1288490188), 'About 1.2 GB will be read.');
});

test('a wording key the build does not know never shows as a raw key', () => {
  const text = copy.showsText('discover.not-a-detector.shows', '');
  assert.equal(text, copy.DISCOVER_TEXT.genericShows);
  assert.ok(!/discover\./.test(text));
  assert.equal(copy.howText('discover.not-a-detector.how'), copy.DISCOVER_TEXT.genericHow);
  assert.equal(copy.howText('../../x'), copy.DISCOVER_TEXT.genericHow);
  assert.equal(copy.wordingName('discover.iis.shows'), 'iis');
  assert.equal(copy.wordingName('iis'), '');
  assert.equal(copy.showsText('discover.__proto__.shows', ''), copy.DISCOVER_TEXT.genericShows);
});

test('a reason or a state nobody named still gets a plain line', () => {
  assert.equal(copy.gapReasonText('something-new'), copy.DISCOVER_TEXT.gapUnknown);
  assert.equal(copy.gapReasonText('constructor'), copy.DISCOVER_TEXT.gapUnknown);
  assert.equal(copy.windowProblemText('nonsense'), '');
  assert.equal(copy.blockedText('nonsense'), '');
  assert.ok(copy.rowOutcomeText({ kind: 'failed', reason: 'zzz' }).length > 0);
  assert.ok(copy.rowOutcomeText({ kind: 'refused', reason: 'zzz' }).startsWith('Refused.'));
});

test('counts read as plain English', () => {
  assert.equal(copy.importButtonText(1), 'Import 1 source');
  assert.equal(copy.importButtonText(3), 'Import 3 sources');
  assert.equal(copy.foundText(1, 0), 'Found 1 source.');
  assert.equal(copy.foundText(4, 2), 'Found 4 sources. 2 places could not be used.');
  assert.equal(copy.updatedText(null), '');
  assert.equal(copy.updatedText(0), 'updated just now');
  assert.equal(copy.updatedText(3), 'updated 3 min ago');
  assert.equal(copy.finishedText(3, 4), 'Done. Added 3 of 4 sources.');
});

/* ------------------------------------------------------------------ entry state */

test('capabilities that say unsupported give the Windows-only state', async () => {
  const client = createFakeClient({ capabilities: { supported: false, elevated: false, os: 'linux' } });
  assert.deepEqual(await discover.loadEntryState(client), { kind: 'unsupported' });
});

test('capabilities that say supported give a ready state that carries the privilege', async () => {
  assert.deepEqual(await discover.loadEntryState(createFakeClient({ capabilities: { supported: true, elevated: true, os: 'windows' } })), { kind: 'ready', elevated: true });
  assert.deepEqual(await discover.loadEntryState(createFakeClient()), { kind: 'ready', elevated: false });
});

test('a 404 on the route means "not in this build"; any other failure hides the entry quietly', async () => {
  const noRoute = createFakeClient({ capabilities: new CaseApiError('Not found.', 404, '') });
  assert.deepEqual(await discover.loadEntryState(noRoute), { kind: 'unavailable' });
  const named = createFakeClient({ capabilities: new CaseApiError('No such route.', 404, 'no-route') });
  assert.deepEqual(await discover.loadEntryState(named), { kind: 'unavailable' });
  for (const error of [new CaseApiError('Down.', 500, 'server'), new CaseApiError('Offline.', 0, 'unreachable'), new CaseApiError('Who?', 401, 'no_session'), new CaseApiError('Case is gone.', 404, 'case-not-found')]) {
    assert.deepEqual(await discover.loadEntryState(createFakeClient({ capabilities: error })), { kind: 'hidden' }, error.reason);
  }
});

test('the real client reads capabilities from the server and sends the session token', async () => {
  const calls = mockServer(() => ({ body: { supported: true, elevated: false, os: 'windows' } }));
  assert.deepEqual(await discover.loadEntryState(discover.discoverClient), { kind: 'ready', elevated: false });
  assert.match(calls[0].url, /\/api\/discover\/capabilities$/);
  assert.equal(new Headers(calls[0].headers).get('X-Audit-Token'), TOKEN);
});

test('the real client treats a missing route as "not in this build"', async () => {
  mockServer(() => ({ status: 404, body: { error: 'Not found.' } }));
  assert.deepEqual(await discover.loadEntryState(discover.discoverClient), { kind: 'unavailable' });
});

test('a capabilities answer that does not fit the shape is not trusted', async () => {
  mockServer(() => ({ body: { supported: 'yes' } }));
  assert.deepEqual(await discover.loadEntryState(discover.discoverClient), { kind: 'hidden' });
  assert.equal(discover.parseCapabilities(null), null);
  assert.equal(discover.parseCapabilities([]), null);
});

test('the entry state is asked once per client, and a quiet failure is asked again', async () => {
  const client = createFakeClient();
  const [a, b] = await Promise.all([discover.cachedEntryState(client), discover.cachedEntryState(client)]);
  assert.deepEqual(a, b);
  assert.equal(client.calls.filter((c) => c.name === 'capabilities').length, 1);

  const flaky = createFakeClient({ capabilities: new CaseApiError('Down.', 500, 'server') });
  assert.equal((await discover.cachedEntryState(flaky)).kind, 'hidden');
  await Promise.resolve();
  assert.equal((await discover.cachedEntryState(flaky)).kind, 'hidden');
  assert.equal(flaky.calls.filter((c) => c.name === 'capabilities').length, 2, 'a hidden answer is not kept');

  const other = createFakeClient({ capabilities: { supported: false, elevated: false, os: 'linux' } });
  assert.equal((await discover.cachedEntryState(other)).kind, 'unsupported', 'another client is asked on its own');
  assert.equal((await discover.cachedEntryState(other, true)).kind, 'unsupported');
  assert.equal(other.calls.filter((c) => c.name === 'capabilities').length, 2, 'force asks again');
});

/* ------------------------------------------------------------------ the checklist */

test('a large source is never ticked for you, whatever the server said about its cost', () => {
  const findings = [
    f('small-one', { cost: 'small', sizeBytes: 5 * MB }),
    f('medium-one', { cost: 'medium', sizeBytes: 200 * MB }),
    f('large-one', { cost: 'large', sizeBytes: 5 * GB }),
    f('lying-small', { cost: 'small', sizeBytes: 2 * GB }),
    f('exactly-1gb', { cost: 'small', sizeBytes: GB }),
    f('lying-medium', { cost: 'medium', sizeBytes: 3 * GB })
  ];
  const picked = discover.defaultSelection(findings, true);
  assert.deepEqual([...picked].sort(), ['medium-one', 'small-one']);
  assert.equal(discover.effectiveCost(findings[3]), 'large');
  assert.equal(discover.effectiveCost(findings[4]), 'large');
  assert.equal(discover.effectiveCost(findings[0]), 'small');
});

test('"tick all except large" never adds a large source and keeps what was already ticked', () => {
  const findings = [f('small-one', { sizeBytes: MB }), f('large-one', { cost: 'large', sizeBytes: 4 * GB }), f('other-small', { sizeBytes: MB })];
  const next = discover.selectAllSmall(findings, new Set(['large-one']));
  assert.ok(next.has('small-one') && next.has('other-small'));
  assert.ok(next.has('large-one'), 'a large source ticked by hand stays ticked');
  const fresh = discover.selectAllSmall(findings, new Set());
  assert.ok(!fresh.has('large-one'));
});

test('a large source can be ticked one at a time', () => {
  const picked = discover.toggled(new Set(), 'large-one', true);
  assert.ok(picked.has('large-one'));
  assert.ok(!discover.toggled(picked, 'large-one', false).has('large-one'));
});

test('a source that needs administrator rights is not ticked for a standard user', () => {
  const findings = [f('open-one'), f('admin-one', { needsAdmin: true })];
  assert.deepEqual([...discover.defaultSelection(findings, false)], ['open-one']);
  assert.deepEqual([...discover.defaultSelection(findings, true)].sort(), ['admin-one', 'open-one']);
});

test('export-needed, remote-only and live rows can never be ticked or imported', () => {
  const rows = [
    f('export-row', { kind: 'export-needed', importer: '', how: 'discover.blg.how' }),
    f('remote-row', { kind: 'remote-only', importer: '' }),
    f('live-row', { kind: 'live-series', importer: 'live-sampler' }),
    f('file-row')
  ];
  assert.deepEqual([...discover.defaultSelection(rows, true)], ['file-row']);
  assert.deepEqual(discover.pickedFindings(rows, new Set(['export-row', 'remote-row', 'live-row', 'file-row'])).map((r) => r.id), ['file-row']);
  assert.equal(discover.pickedFindings(rows, new Set(['ghost-id'])).length, 0, 'an id the list does not hold is dropped');
});

test('rows fall into the five groups the spec names', () => {
  const group = (detector, importer = '', kind = 'log-file') => discover.groupOf({ detector, importer, kind });
  assert.equal(group('iis', 'iis-w3c'), 'web');
  assert.equal(group('nginx', 'nginx-access'), 'web');
  assert.equal(group('events', 'windows-evtx'), 'windows');
  assert.equal(group('perfmon', ''), 'windows');
  assert.equal(group('log4net', 'log4net'), 'app');
  assert.equal(group('something-new'), 'app');
  assert.equal(group('mongo', 'mongo-log'), 'databases');
  assert.equal(group('redis', ''), 'databases');
  assert.equal(group('host', 'live-sampler', 'live-series'), 'live');
});

/* ------------------------------------------------------------------ the answer's shape */

test('a path the server sends by mistake is dropped when a finding is read', () => {
  const body = { id: 'k3m7q2x5v8b4n6c9', kind: 'log-file', detector: 'iis', importer: 'iis-w3c', display: 'W3SVC1', path: 'C:\\inetpub\\logs\\u_ex010911.log', fullPath: '/var/log/x.log', file: 'x.log', sizeBytes: 10 };
  const parsed = discover.parseFinding(body);
  assert.ok(parsed);
  assert.ok(!JSON.stringify(parsed).includes('inetpub'));
  assert.ok(!JSON.stringify(parsed).includes('/var/log'));
  assert.deepEqual(Object.keys(parsed).filter((key) => /path|file$/i.test(key)), []);
  const scan = discover.parseScan({ state: 'done', findings: [body], gaps: [{ detector: 'iis', kind: 'log-set', display: 'W3SVC9', reason: 'unreadable', path: 'D:\\secret' }] });
  assert.ok(!JSON.stringify(scan).includes('secret'));
  assert.ok(!JSON.stringify(scan).includes('inetpub'));
});

test('a finding with a bad id or kind is refused, and numbers are kept in range', () => {
  assert.equal(discover.parseFinding({ id: 'a b', kind: 'log-file' }), null);
  assert.equal(discover.parseFinding({ id: 'ab', kind: 'log-file' }), null);
  assert.equal(discover.parseFinding({ id: '../../etc/passwd', kind: 'log-file' }), null);
  assert.equal(discover.parseFinding({ id: 'valid-id-1', kind: 'registry-key' }), null);
  assert.equal(discover.parseFinding('text'), null);
  const odd = discover.parseFinding({ id: 'valid-id-1', kind: 'log-file', sizeBytes: -5, files: -2, confidence: 400, oldest: 'not a date', cost: 'huge' });
  assert.equal(odd.sizeBytes, 0);
  assert.equal(odd.files, 0);
  assert.equal(odd.confidence, 100);
  assert.equal(odd.oldest, '');
  assert.equal(odd.cost, 'small', 'an unknown cost falls back to the size');
  assert.equal(discover.parseFinding({ id: 'valid-id-1', kind: 'log-file', sizeBytes: 3 * GB }).cost, 'large');
});

test('a scan answer that does not fit the shape is refused; an unknown gap reason is kept as empty', () => {
  for (const bad of [null, [], 'x', {}, { state: 'weird' }]) assert.equal(discover.parseScan(bad), null, JSON.stringify(bad));
  const scan = discover.parseScan({ state: 'done', findings: [{ id: 'bad id', kind: 'log-file' }, { id: 'good-id-1', kind: 'log-file' }], gaps: [{ detector: 'sql', kind: 'x', display: 'd', reason: 'new-reason' }], progress: [{ detector: 'iis', state: 'done' }, { detector: 'x', state: 'nope' }] });
  assert.equal(scan.findings.length, 1);
  assert.equal(scan.gaps[0].reason, '');
  assert.deepEqual(scan.progress, [{ detector: 'iis', state: 'done' }]);
  assert.equal(scan.host, null);
});

test('the sampler answer is read, and an unknown state is treated as idle', () => {
  assert.equal(discover.parseSampler({ state: 'recording', samples: 7 }).samples, 7);
  assert.equal(discover.parseSampler({ state: 'odd' }).state, 'idle');
  assert.equal(discover.parseSampler({ state: 'stopped', stopReason: 'cap' }).stopReason, 'cap');
  assert.equal(discover.parseSampler({}), null);
});

/* ------------------------------------------------------------------ the time window */

const WINDOW = { from: '2001-09-11T08:00:00Z', to: '2001-09-11T10:00:00Z' };

test('the estimate sums each ticked source by the share of its own time span the window covers', () => {
  const a = { sizeBytes: 100 * MB, oldest: '2001-09-11T00:00:00Z', newest: '2001-09-11T12:00:00Z' };
  const b = { sizeBytes: 60 * MB, oldest: '2001-09-11T08:00:00Z', newest: '2001-09-11T10:00:00Z' };
  // a: 2h of 12h = one sixth; b: all of it.
  const expected = Math.round(100 * MB / 6 + 60 * MB);
  assert.equal(discover.estimateBytes([a, b], WINDOW), expected);
  assert.equal(discover.estimateBytes([a], WINDOW), Math.round(100 * MB / 6));
  assert.equal(discover.estimateBytes([], WINDOW), 0);
});

test('a source outside the window adds nothing; a source with no span counts whole', () => {
  const before = { sizeBytes: 50 * MB, oldest: '2001-09-10T00:00:00Z', newest: '2001-09-10T06:00:00Z' };
  const noSpan = { sizeBytes: 7 * MB, oldest: '', newest: '' };
  assert.equal(discover.estimateBytes([before], WINDOW), 0);
  assert.equal(discover.estimateBytes([before, noSpan], WINDOW), 7 * MB);
  assert.equal(discover.estimateBytes([noSpan], { from: WINDOW.to, to: WINDOW.from }), 0, 'a backwards window reads nothing');
});

test('a wider window never reads less', () => {
  const src = { sizeBytes: 100 * MB, oldest: '2001-09-10T00:00:00Z', newest: '2001-09-12T00:00:00Z' };
  const narrow = discover.estimateBytes([src], { from: '2001-09-11T00:00:00Z', to: '2001-09-11T06:00:00Z' });
  const wide = discover.estimateBytes([src], { from: '2001-09-11T00:00:00Z', to: '2001-09-11T18:00:00Z' });
  assert.ok(wide > narrow);
  assert.ok(wide <= src.sizeBytes);
});

test('the fixed choices make a window ending now, and the incident choice needs a real period', () => {
  const now = Date.parse('2001-09-11T12:00:00Z');
  assert.deepEqual(discover.windowFor('1h', now, null, { from: '', to: '' }), { from: '2001-09-11T11:00:00Z', to: '2001-09-11T12:00:00Z' });
  assert.deepEqual(discover.windowFor('7d', now, null, { from: '', to: '' }), { from: '2001-09-04T12:00:00Z', to: '2001-09-11T12:00:00Z' });
  assert.equal(discover.windowFor('incident', now, null, { from: '', to: '' }), null);
  const incident = discover.incidentWindow('2001-09-11T08:00:00.000Z', '2001-09-11T09:30:00.000Z');
  assert.deepEqual(incident, { from: '2001-09-11T08:00:00Z', to: '2001-09-11T09:30:00Z' });
  assert.deepEqual(discover.windowFor('incident', now, incident, { from: '', to: '' }), incident);
  assert.equal(discover.incidentWindow('2001-09-11T09:00:00Z', '2001-09-11T08:00:00Z'), null);
  assert.equal(discover.incidentWindow('nonsense', '2001-09-11T08:00:00Z'), null);
  assert.equal(discover.defaultWindowChoice(incident), 'incident');
  assert.equal(discover.defaultWindowChoice(null), '24h');
});

test('custom times are read as UTC and a date that does not exist is refused', () => {
  assert.equal(discover.parseCustomTime('2001-09-11T08:00'), Date.UTC(2001, 8, 11, 8, 0));
  assert.equal(discover.parseCustomTime('2001-09-11 08:30'), Date.UTC(2001, 8, 11, 8, 30));
  assert.equal(discover.parseCustomTime('2001-02-31 08:00'), null);
  assert.equal(discover.parseCustomTime('2001-09-11 25:00'), null);
  assert.equal(discover.parseCustomTime('11/09/2001 08:00'), null);
  assert.equal(discover.parseCustomTime(''), null);
  const win = discover.windowFor('custom', 0, null, { from: '2001-09-11 08:00', to: '2001-09-11 10:00' });
  assert.deepEqual(win, WINDOW);
  assert.equal(discover.windowFor('custom', 0, null, { from: '2001-09-11 08:00', to: 'soon' }), null);
});

test('a window must exist, run forward, and be 31 days or less', () => {
  assert.equal(discover.windowProblem(null), 'missing');
  assert.equal(discover.windowProblem({ from: 'x', to: 'y' }), 'missing');
  assert.equal(discover.windowProblem({ from: WINDOW.to, to: WINDOW.from }), 'order');
  assert.equal(discover.windowProblem({ from: WINDOW.from, to: WINDOW.from }), 'order');
  assert.equal(discover.windowProblem(WINDOW), null);
  assert.equal(discover.windowProblem({ from: '2001-08-01T00:00:00Z', to: '2001-09-01T00:00:00Z' }), null, '31 days exactly is allowed');
  assert.equal(discover.windowProblem({ from: '2001-08-01T00:00:00Z', to: '2001-09-01T00:00:01Z' }), 'too-long');
  for (const problem of ['missing', 'order', 'too-long']) assert.ok(copy.windowProblemText(problem).length > 0);
});

test('age in minutes is never negative and a scan older than an hour is expired', () => {
  const now = Date.parse('2001-09-11T12:00:00Z');
  assert.equal(discover.ageMinutes('2001-09-11T11:57:00Z', now), 3);
  assert.equal(discover.ageMinutes('2001-09-11T12:30:00Z', now), 0);
  assert.equal(discover.ageMinutes('', now), null);
  assert.equal(discover.scanExpired('2001-09-11T11:00:01Z', now), false);
  assert.equal(discover.scanExpired('2001-09-11T11:00:00Z', now), true);
  assert.equal(discover.scanExpired('', now), false);
});

/* ------------------------------------------------------------------ errors */

test('errors are sorted into the cases the panel words differently', () => {
  const err = (status, reason) => new CaseApiError('Message.', status, reason);
  assert.equal(discover.classifyError(err(409, 'scan-busy')), 'busy');
  assert.equal(discover.classifyError(err(429, 'import-busy')), 'import-busy');
  assert.equal(discover.classifyError(err(429, '')), 'import-busy');
  assert.equal(discover.classifyError(err(409, 'sampler-busy')), 'sampler-busy');
  assert.equal(discover.classifyError(err(404, 'scan-expired')), 'expired');
  assert.equal(discover.classifyError(err(404, 'scan-unknown')), 'expired');
  assert.equal(discover.classifyError(err(404, '')), 'unavailable');
  assert.equal(discover.classifyError(err(404, 'case-not-found')), 'other', 'a named 404 is a real answer, not a missing route');
  assert.equal(discover.classifyError(err(401, '')), 'session');
  assert.equal(discover.classifyError(err(0, 'unreachable')), 'unreachable');
  assert.equal(discover.classifyError(new TypeError('boom')), 'unreachable');
  assert.equal(discover.classifyError(err(500, 'server')), 'other');
  for (const problem of ['unavailable', 'busy', 'import-busy', 'sampler-busy', 'expired', 'unreachable', 'session', 'other']) assert.ok(copy.problemText(problem).length > 0, problem);
  assert.equal(copy.problemText('unavailable'), 'Scanning is not available in this build.');
});

test('a refusal on one import becomes that row\'s own result', () => {
  const err = (status, reason) => new CaseApiError('Message.', status, reason);
  assert.deepEqual(discover.outcomeFromError(err(409, 'duplicate')), { kind: 'duplicate' });
  assert.deepEqual(discover.outcomeFromError(err(403, 'needs-elevation')), { kind: 'needs-admin' });
  assert.deepEqual(discover.outcomeFromError(err(403, 'access-denied')), { kind: 'needs-admin' });
  assert.deepEqual(discover.outcomeFromError(err(499, 'cancelled')), { kind: 'cancelled' });
  assert.deepEqual(discover.outcomeFromError(err(422, 'unscrubbed-column')), { kind: 'refused', reason: 'unscrubbed-column' });
  assert.equal(discover.outcomeFromError(err(500, 'server')).kind, 'failed');
  assert.equal(discover.outcomeFromError(err(0, 'unreachable')).kind, 'failed');
  assert.equal(discover.outcomeFromError(new Error('x')).kind, 'failed');
  assert.equal(copy.rowOutcomeText({ kind: 'needs-admin' }), 'Needs administrator');
  assert.equal(copy.rowOutcomeText({ kind: 'duplicate' }), 'Nothing new to add');
});

/* ------------------------------------------------------------------ the fake client, scans and jobs */

test('waiting on a scan returns when it stops running, with updates along the way', async () => {
  const client = createFakeClient({ findings: [f('one-id-1'), f('two-id-2')], runningAnswers: 2, progress: [{ detector: 'iis', state: 'running' }] });
  const id = await client.startScan();
  assert.equal(id, FAKE_SCAN_ID);
  const seen = [];
  const scan = await discover.waitForScan(client, id, { sleep: noSleep, onUpdate: (s) => seen.push(s.state) });
  assert.deepEqual(seen, ['running', 'running', 'done']);
  assert.equal(scan.findings.length, 2);
});

test('a scan that ends cancelled or failed is returned as that, not thrown', async () => {
  for (const finalState of ['cancelled', 'failed']) {
    const client = createFakeClient({ finalState, runningAnswers: 0 });
    const scan = await discover.waitForScan(client, await client.startScan(), { sleep: noSleep });
    assert.equal(scan.state, finalState);
  }
});

test('a few lost answers are tolerated; a refusal or too many lost answers end the wait', async () => {
  let n = 0;
  const lossy = createFakeClient();
  const real = lossy.getScan;
  lossy.getScan = async (...args) => { if (n++ < 3) throw new CaseApiError('Offline.', 0, 'unreachable'); return real(...args); };
  const scan = await discover.waitForScan(lossy, FAKE_SCAN_ID, { sleep: noSleep });
  assert.equal(scan.state, 'done');

  const gone = createFakeClient({ failures: { getScan: new CaseApiError('Gone.', 404, 'scan-expired') } });
  await assert.rejects(discover.waitForScan(gone, FAKE_SCAN_ID, { sleep: noSleep }), (e) => discover.classifyError(e) === 'expired');

  const down = createFakeClient({ failures: { getScan: new CaseApiError('Offline.', 0, 'unreachable') } });
  await assert.rejects(discover.waitForScan(down, FAKE_SCAN_ID, { sleep: noSleep }), (e) => e.reason === 'unreachable');
  assert.equal(down.calls.filter((c) => c.name === 'getScan').length, discover.SCAN_MAX_UNREACHABLE);
});

test('aborting the wait stops it', async () => {
  const client = createFakeClient({ runningAnswers: 1000 });
  const controller = new AbortController();
  const sleep = async () => { controller.abort(); throw new DOMException('Aborted', 'AbortError'); };
  await assert.rejects(discover.waitForScan(client, FAKE_SCAN_ID, { sleep, signal: controller.signal }), (e) => e.name === 'AbortError');
});

test('watching an import follows queued, running and done, and reports rows added', async () => {
  const client = createFakeClient({ outcomes: { 'one-id-1': { rows: 4200 } } });
  const refs = await client.startImports(CASE_ID, { scanId: FAKE_SCAN_ID, findingIds: ['one-id-1'], window: WINDOW, ackGating: true, ackText: 'x' });
  const states = [];
  const outcome = await discover.watchJob(client, CASE_ID, refs[0], { sleep: noSleep, onUpdate: (w) => states.push(w.job.state) });
  assert.deepEqual(states, ['queued', 'running', 'done']);
  assert.deepEqual(outcome, { kind: 'added', rows: 4200 });
});

test('one source failing does not stop the others, and each failure is its own result', async () => {
  const client = createFakeClient({
    outcomes: {
      'dup-id-1': { duplicate: true },
      'admin-id-1': { error: new CaseApiError('Needs rights.', 403, 'needs-elevation') },
      'bad-id-1': { error: new CaseApiError('Refused.', 422, 'unsupported-format') },
      'down-id-1': { error: new CaseApiError('Broke.', 500, 'server') }
    }
  });
  const ids = ['ok-id-1', 'dup-id-1', 'admin-id-1', 'bad-id-1', 'down-id-1'];
  const refs = await client.startImports(CASE_ID, { scanId: FAKE_SCAN_ID, findingIds: ids, window: WINDOW, ackGating: true, ackText: 'x' });
  const outcomes = await Promise.all(refs.map((ref) => discover.watchJob(client, CASE_ID, ref, { sleep: noSleep })));
  assert.deepEqual(outcomes.map((o) => o.kind), ['added', 'duplicate', 'needs-admin', 'refused', 'failed']);
});

test('watching a job that cannot be reached ends as a failed row, and an abort is passed on', async () => {
  const client = createFakeClient({ failures: { getJob: new CaseApiError('Offline.', 0, 'unreachable') } });
  const outcome = await discover.watchJob(client, CASE_ID, { findingId: 'x-id-1', jobId: '0'.repeat(32) }, { sleep: noSleep });
  assert.equal(outcome.kind, 'failed');
  assert.equal(client.calls.filter((c) => c.name === 'getJob').length, 5);

  const abortClient = createFakeClient({ failures: { getJob: new CaseApiError('x', 500, 'server') } });
  abortClient.getJob = async () => { throw new DOMException('Aborted', 'AbortError'); };
  await assert.rejects(discover.watchJob(abortClient, CASE_ID, { findingId: 'x-id-1', jobId: '0'.repeat(32) }, { sleep: noSleep }), (e) => e.name === 'AbortError');
});

/* ------------------------------------------------------------------ the real client's requests */

test('the real client starts a scan and rejects an id that is not a scan id', async () => {
  const calls = mockServer((call) => (call.method === 'POST' ? { status: 202, body: { scanId: FAKE_SCAN_ID } } : { status: 204 }));
  assert.equal(await discover.discoverClient.startScan(), FAKE_SCAN_ID);
  assert.equal(calls[0].method, 'POST');
  assert.match(calls[0].url, /\/api\/discover\/scans$/);
  await discover.discoverClient.cancelScan(FAKE_SCAN_ID);
  assert.equal(calls[1].method, 'DELETE');
  assert.match(calls[1].url, new RegExp('/api/discover/scans/' + FAKE_SCAN_ID + '$'));
  await assert.rejects(discover.discoverClient.getScan('../../etc'), (e) => e.reason === 'bad-scan-id');
  assert.equal(calls.length, 2, 'a bad id makes no request');
});

test('a scan that is busy comes back as busy', async () => {
  mockServer(() => ({ status: 409, body: { error: 'A scan is running.', reason: 'scan-busy' } }));
  await assert.rejects(discover.discoverClient.startScan(), (e) => discover.classifyError(e) === 'busy');
});

test('an import request holds ids, a window and the notice text, and never a path', async () => {
  const calls = mockServer(() => ({ status: 202, body: { jobs: [{ findingId: 'one-id-1', jobId: '0'.repeat(24) + '00000001' }] } }));
  const refs = await discover.discoverClient.startImports(CASE_ID, { scanId: FAKE_SCAN_ID, findingIds: ['one-id-1'], window: WINDOW, ackGating: true, ackText: 'Notice text.', path: 'C:\\x.log', paths: ['D:\\y'] });
  assert.equal(refs.length, 1);
  assert.match(calls[0].url, new RegExp('/api/cases/' + CASE_ID + '/discover/imports$'));
  const body = JSON.parse(calls[0].body);
  assert.deepEqual(Object.keys(body).sort(), ['ackGating', 'ackText', 'findingIds', 'scanId', 'window']);
  assert.equal(body.ackGating, true);
  assert.deepEqual(body.window, WINDOW);
  assert.ok(!/[A-Za-z]:\\/.test(calls[0].body), 'no drive path in the body');
});

test('an import request with no sources, too many, or an unsafe id is refused before it is sent', async () => {
  const calls = mockServer(() => ({ status: 202, body: { jobs: [] } }));
  const base = { scanId: FAKE_SCAN_ID, window: WINDOW, ackGating: true, ackText: 'x' };
  await assert.rejects(discover.discoverClient.startImports(CASE_ID, { ...base, findingIds: [] }), (e) => e.reason === 'bad-findings');
  await assert.rejects(discover.discoverClient.startImports(CASE_ID, { ...base, findingIds: Array.from({ length: 65 }, (_, i) => 'id-' + String(i).padStart(4, '0')) }), (e) => e.reason === 'bad-findings');
  await assert.rejects(discover.discoverClient.startImports(CASE_ID, { ...base, findingIds: ['C:\\logs\\x.log'] }), (e) => e.reason === 'bad-findings');
  await assert.rejects(discover.discoverClient.startImports(CASE_ID, { ...base, scanId: 'nope', findingIds: ['one-id-1'] }), (e) => e.reason === 'bad-scan-id');
  assert.equal(calls.length, 0);
  assert.equal(discover.MAX_FINDINGS_PER_CALL, 64);
});

test('a busy import and an answer with no jobs are told apart', async () => {
  mockServer(() => ({ status: 429, body: { error: 'Busy.', reason: 'import-busy' } }));
  await assert.rejects(discover.discoverClient.startImports(CASE_ID, { scanId: FAKE_SCAN_ID, findingIds: ['one-id-1'], window: WINDOW, ackGating: true, ackText: 'x' }), (e) => discover.classifyError(e) === 'import-busy');
  mockServer(() => ({ status: 202, body: { jobs: [{ findingId: 'one-id-1', jobId: 'short' }] } }));
  await assert.rejects(discover.discoverClient.startImports(CASE_ID, { scanId: FAKE_SCAN_ID, findingIds: ['one-id-1'], window: WINDOW, ackGating: true, ackText: 'x' }), (e) => e.reason === 'unreadable');
});

test('the sampler request carries the interval and the notice text, and the status is read back', async () => {
  const calls = mockServer((call) => (call.method === 'DELETE' ? { status: 204 } : { body: { state: 'recording', since: '2001-09-11T08:00:00Z', samples: 3, stopReason: '' } }));
  const started = await discover.discoverClient.startSampler(CASE_ID, { intervalSec: 15, ackText: 'Notice text.' });
  assert.equal(started.state, 'recording');
  assert.deepEqual(JSON.parse(calls[0].body), { intervalSec: 15, ackGating: true, ackText: 'Notice text.' });
  assert.equal((await discover.discoverClient.getSampler(CASE_ID)).samples, 3);
  await discover.discoverClient.stopSampler(CASE_ID);
  assert.equal(calls[2].method, 'DELETE');
  assert.match(calls[2].url, new RegExp('/api/cases/' + CASE_ID + '/sampler$'));
});

test('another case is already recording', async () => {
  mockServer(() => ({ status: 409, body: { error: 'Recording elsewhere.', reason: 'sampler-busy' } }));
  await assert.rejects(discover.discoverClient.startSampler(CASE_ID, { intervalSec: 5, ackText: 'x' }), (e) => discover.classifyError(e) === 'sampler-busy');
});

/* ------------------------------------------------------------------ the sampler through the fake */

test('the fake sampler records, counts and stops', async () => {
  const client = createFakeClient();
  assert.equal((await client.getSampler(CASE_ID)).state, 'idle');
  assert.equal((await client.startSampler(CASE_ID, { intervalSec: 15, ackText: 'x' })).state, 'recording');
  assert.ok((await client.getSampler(CASE_ID)).samples > 0);
  await client.stopSampler(CASE_ID);
  assert.equal((await client.getSampler(CASE_ID)).state, 'stopped');
  assert.deepEqual([...discover.SAMPLER_INTERVALS], [5, 15, 30, 60]);
});

/* ------------------------------------------------------------------ the intent flag */

test('the discover request is taken once', () => {
  assert.equal(intent.takeDiscoverIntent(), false);
  intent.requestDiscover();
  assert.equal(intent.takeDiscoverIntent(), true);
  assert.equal(intent.takeDiscoverIntent(), false);
});

/* ------------------------------------------------------------------ the screens (structure) */

test('the panel shows the notice and its confirmation box, and sends the notice text with every import and recording', () => {
  const panel = read('src/components/DiscoverPanel.svelte');
  assert.ok(!/GATING_BANNER/.test(panel), 'the notice is shown once, at the top of the window, not repeated here');
  assert.match(panel, /\{DISCOVER_TEXT\.noticePointer\}/);
  assert.match(panel, /<label class="ack">\s*<input type="checkbox" bind:checked=\{acknowledged\} \/>\s*<span>\{DISCOVER_TEXT\.ack\}<\/span>/, 'the confirmation is a labelled checkbox');
  assert.equal((panel.match(/ackText: ackTextFor\(\)/g) ?? []).length, 2, 'imports and recording both carry the notice text');
  assert.match(panel, /ackGating: true/);
});

test('the panel never sends or shows a path, and names sources by the id the server gave', () => {
  const panel = read('src/components/DiscoverPanel.svelte');
  assert.ok(!/\.path\b|filePath|fullPath/.test(panel));
  assert.match(panel, /findingIds: sending\.map\(\(f\) => f\.id\)/);
});

test('progress and results are announced, controls are labelled, and the step headings take focus', () => {
  const panel = read('src/components/DiscoverPanel.svelte');
  assert.ok((panel.match(/aria-live="polite"/g) ?? []).length >= 3);
  assert.match(panel, /role="alert"/);
  assert.match(panel, /<fieldset class="group"/);
  assert.match(panel, /<legend/);
  assert.match(panel, /tabindex="-1"/);
  assert.match(panel, /aria-describedby/);
  assert.match(panel, /role="radiogroup"|<fieldset[^>]*window/i);
  assert.match(panel, /<progress/);
});

test('a large source has no default tick path in the panel and "tick all" uses the small-only function', () => {
  const panel = read('src/components/DiscoverPanel.svelte');
  assert.match(panel, /defaultSelection\(/);
  assert.match(panel, /selectAllSmall\(/);
  assert.ok(!/picked = new Set\(findings\.map/.test(panel), 'there is no select-everything shortcut');
  assert.match(panel, /DISCOVER_TEXT\.tagLarge/);
  assert.match(panel, /DISCOVER_TEXT\.tagAdmin/);
  assert.match(panel, /DISCOVER_TEXT\.tagExport/);
});

test('gap rows are disabled and give their reason', () => {
  const panel = read('src/components/DiscoverPanel.svelte');
  assert.match(panel, /gapReasonText\(/);
  assert.match(panel, /disabled/);
});

test('the entry shows a button only when scanning works, and one plain line otherwise', () => {
  const entry = read('src/components/DiscoverEntry.svelte');
  assert.match(entry, /entry\.kind === 'ready'/);
  assert.match(entry, /DISCOVER_TEXT\.windowsOnly/);
  assert.match(entry, /DISCOVER_TEXT\.unavailable/);
  assert.match(entry, /cachedEntryState/);
});

test('the start screen and the case tools both offer the entry', () => {
  assert.match(read('src/components/Landing.svelte'), /<DiscoverEntry onOpen=\{openDiscover\} \/>/);
  assert.match(read('src/components/Landing.svelte'), /requestDiscover\(\)/);
  const tools = read('src/lib/components/CaseTools.svelte');
  assert.match(tools, /onDiscover/);
  assert.match(tools, /<DiscoverEntry/);
});

test('the import drawer hosts the panel, passes the case, and opens it when the start screen asked', () => {
  const drawer = read('src/lib/components/ImportPanel.svelte');
  assert.match(drawer, /<DiscoverPanel/);
  assert.match(drawer, /takeDiscoverIntent\(\)/);
  assert.match(drawer, /onDiscover=\{discoverOpen \? undefined : openDiscover\}/);
  assert.match(drawer, /caseId=\{selectedId\}/);
});

test('the copy has no wording that names a cause', () => {
  const text = copy.discoverCopyEntries().map((e) => e.text).join('\n');
  assert.ok(!/\b(because|due to|caused|leak|starv|saturat)/i.test(text));
});

/* ------------------------------------------------------------------ UI defects found in the browser check */

test('a cancel control ignores clicks for a short moment after it appears (a double-click must not cancel)', () => {
  assert.ok(discover.CANCEL_GUARD_MS >= 500 && discover.CANCEL_GUARD_MS <= 1000);
  assert.equal(discover.cancelAllowed(1000, 1000), false);
  assert.equal(discover.cancelAllowed(1000, 1000 + discover.CANCEL_GUARD_MS - 1), false);
  assert.equal(discover.cancelAllowed(1000, 1000 + discover.CANCEL_GUARD_MS), true);
});

test('the scan and import starts ignore a second click, and the cancel buttons wait for the guard', () => {
  const panel = read('src/components/DiscoverPanel.svelte');
  assert.match(panel, /async function startScan\(\) \{[^}]*if \(step === 'scanning' \|\| step === 'importing'\) return;/s);
  assert.match(panel, /if \(cancellingScan \|\| !cancelAllowed\(scanShownAt, now\(\)\)\) return;/);
  assert.match(panel, /if \(cancellingImport \|\| !cancelAllowed\(importShownAt, now\(\)\)\) return;/);
  assert.match(panel, /disabled=\{cancellingScan \|\| !scanCancelReady\}/);
  assert.match(panel, /disabled=\{cancellingImport \|\| !importCancelReady\}/);
});

test('a scan the person cancelled is a plain note, not an error', () => {
  const panel = read('src/components/DiscoverPanel.svelte');
  assert.match(panel, /info = DISCOVER_TEXT\.scanCancelled/);
  assert.ok(!/problem = DISCOVER_TEXT\.scanCancelled/.test(panel));
  assert.match(panel, /\{#if info\}<p class="note" role="status">\{info\}<\/p>\{\/if\}/);
});

test('a day count says "1 day ago" and "2 days ago"', () => {
  assert.equal(copy.updatedText(1440), 'updated 1 day ago');
  assert.equal(copy.updatedText(1440 * 2), 'updated 2 days ago');
  assert.equal(copy.updatedText(90), 'updated 1 h ago');
  assert.equal(copy.updatedText(null), '');
});

test('a source that needs an export is not called one Unravis can read', () => {
  const generic = copy.showsText('', '', false);
  const needs = copy.showsText('', '', true);
  assert.notEqual(needs, generic);
  assert.equal(needs, copy.DISCOVER_TEXT.exportShows);
  assert.ok(!/can read\.$/.test(needs));
  // A named wording keeps its own text either way.
  assert.equal(copy.showsText('', 'no-such-importer', true), copy.DISCOVER_TEXT.exportShows);
});

test('the size line has no file count for a live series and no empty size for an export step', () => {
  assert.equal(copy.rowMetaText({ kind: 'live-series', files: 0, sizeBytes: 0, confidence: 95 }, null), '');
  assert.equal(copy.rowMetaText({ kind: 'export-needed', files: 0, sizeBytes: 0, confidence: 95 }, null), 'Strong match.');
  assert.equal(copy.rowMetaText({ kind: 'files', files: 14, sizeBytes: 50 * MB, confidence: 60 }, 3), '14 files, 50 MB, updated 3 min ago. Possible match.');
});

test('the confirmation does not say "this file", and points to the one notice at the top', () => {
  const ack = copy.DISCOVER_TEXT.ack;
  assert.ok(!/this file/i.test(ack));
  assert.match(ack, /import or record/);
  assert.match(copy.DISCOVER_TEXT.noticePointer, /top of this window/);
});

test('the panel hides the notice, the ack and the import button when a scan found nothing, except the live ack', () => {
  const panel = read('src/components/DiscoverPanel.svelte');
  assert.match(panel, /\{#if findings\.length > 0\}\s*<section class="notice"/);
  assert.match(panel, /\{#if findings\.length === 0 && !recording\}\s*<p class="note">\{DISCOVER_TEXT\.noticePointer\}<\/p>\s*<label class="ack">/);
});

test('each result row has a mark and a word, so success and failure are not told by colour alone', () => {
  assert.equal(copy.rowOutcomeTone({ kind: 'added' }), 'good');
  assert.equal(copy.rowOutcomeTone({ kind: 'duplicate' }), 'neutral');
  assert.equal(copy.rowOutcomeTone({ kind: 'cancelled' }), 'neutral');
  assert.equal(copy.rowOutcomeTone({ kind: 'refused' }), 'bad');
  assert.equal(copy.rowOutcomeTone({ kind: 'failed' }), 'bad');
  assert.equal(copy.rowOutcomeTone(null), 'pending');
  const words = ['good', 'neutral', 'bad'].map(copy.toneLabel);
  assert.equal(new Set(words).size, 3);
  assert.ok(words.every((word) => word.length > 0));
  assert.equal(copy.toneLabel('pending'), '');
  const panel = read('src/components/DiscoverPanel.svelte');
  assert.match(panel, /toneLabel\(tone\)/);
  assert.match(panel, /<span aria-hidden="true">/);
});

test('a 500 with no known reason says the server had a problem, not that it refused the request', () => {
  const text = copy.problemText('other', { status: 500, reason: '', message: '' });
  assert.equal(text, copy.DISCOVER_TEXT.serverProblem);
  assert.ok(!/refused/i.test(text));
  assert.ok(!/refused/i.test(copy.rowOutcomeText({ kind: 'failed' })));
});

test('each custom time is named and checked on its own: zone, seconds and an impossible date', () => {
  const problem = discover.customTimeProblem;
  assert.equal(problem(''), 'empty');
  assert.equal(problem('  '), 'empty');
  assert.equal(problem('2001-09-11 08:00'), null);
  assert.equal(problem('2001-09-11 08:00Z'), 'zone');
  assert.equal(problem('2001-09-11 08:00+02:00'), 'zone');
  assert.equal(problem('2001-09-11 08:00:30'), 'seconds');
  assert.equal(problem('2001-02-30 08:00'), 'date');
  assert.equal(problem('2001-09-11 25:00'), 'date');
  assert.equal(problem('soon'), 'format');
  const messages = ['empty', 'zone', 'seconds', 'date', 'format'].map((key) => copy.timeFieldText('from', key));
  assert.equal(new Set(messages).size, 5, 'each fault has its own message');
  assert.match(copy.timeFieldText('from', 'empty'), /start/);
  assert.match(copy.timeFieldText('to', 'empty'), /end/);
  assert.equal(copy.timeFieldText('from', 'nope'), '');
});

test('the custom time boxes carry aria-invalid and aria-describedby, and one fault shows one message', () => {
  const panel = read('src/components/DiscoverPanel.svelte');
  assert.equal((panel.match(/aria-invalid=/g) ?? []).length, 2);
  assert.equal((panel.match(/aria-describedby=\{'discover-custom-help'/g) ?? []).length, 2);
  assert.match(panel, /id="discover-from-problem"/);
  assert.match(panel, /id="discover-to-problem"/);
  assert.match(panel, /id="discover-custom-help"/);
  assert.match(panel, /const rangeShown = \$derived\(rangeProblem !== null && !fieldsBad/);
  assert.match(panel, /block === 'window' && windowShown/, 'the blocked line does not repeat a window message');
});

test('the tick boxes and their labels are at least 24px, and a gap row keeps its text colour', () => {
  const panel = read('src/components/DiscoverPanel.svelte');
  assert.match(panel, /\.pick input, \.ack input, \.choice input \{[^}]*width: 24px; height: 24px/);
  assert.match(panel, /\.pick, \.ack, \.choice \{[^}]*min-height: 24px/);
  assert.ok(!/\.row\.gap\s*\{[^}]*opacity/.test(panel), 'no opacity on a gap row');
});

function channel(v) { const c = v / 255; return c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4; }
function luminance(hex) { const n = parseInt(hex.slice(1), 16); return 0.2126 * channel(n >> 16) + 0.7152 * channel((n >> 8) & 255) + 0.0722 * channel(n & 255); }
function ratio(a, b) { const [hi, lo] = [luminance(a), luminance(b)].sort((x, y) => y - x); return (hi + 0.05) / (lo + 0.05); }

test('the secondary text colour used for a gap reason clears 4.5:1 on the surface in dark and light', () => {
  const css = read('src/brand/tokens.css') + '\n' + read('src/app.css');
  const pairs = [...css.matchAll(/--surface:\s*(#[0-9a-f]{6});[\s\S]*?--text-2:\s*(#[0-9a-f]{6});/gi)];
  assert.ok(pairs.length >= 2, 'dark and light token sets are found');
  for (const [, surface, text] of pairs) assert.ok(ratio(text, surface) >= 4.5, text + ' on ' + surface + ' is ' + ratio(text, surface).toFixed(2));
});

test('the resource panel shows a loading line, not "No counters were read", while a read is going', () => {
  const panel = read('src/components/ResourcePanel.svelte');
  assert.match(panel, /\{#if item\.summary\.length === 0 && busy\}\s*<p class="line">\{resourceCopy\.loading\}<\/p>\s*\{:else if item\.summary\.length === 0\}/);
});

test('the strip chart starts only once its box has a size', () => {
  const chart = read('src/components/resource/StripChart.svelte');
  assert.ok(!/chart = echarts\.init\(node\);/.test(chart));
  assert.match(chart, /target\.clientWidth > 0 && target\.clientHeight > 0\) chart = echarts\.init\(target\)/);
});

test('the real client lists a proposed host once and does not list it twice', async () => {
  const host = { label: 'local', proposed: true, tz: 'Etc/UTC', cores: 4, aliases: ['WEB-A'] };
  let listed = { version: 1, hosts: [] };
  const calls = mockServer((call) => {
    if (call.method === 'PUT') { listed = JSON.parse(call.body); return { status: 200, body: listed }; }
    return { status: 200, body: listed };
  });
  await discover.discoverClient.confirmHost(host);
  assert.equal(calls.filter((c) => c.method === 'PUT').length, 1);
  assert.equal(listed.hosts[0].label, 'local');
  assert.equal(listed.hosts[0].tz, 'Etc/UTC');
  assert.equal(listed.hosts[0].logicalCores, 4);
  assert.deepEqual(listed.hosts[0].aliases, ['WEB-A']);
  await discover.discoverClient.confirmHost(host);
  assert.equal(calls.filter((c) => c.method === 'PUT').length, 1, 'a listed label makes no second save');
});

/* ------------------------------------------------------------------ clumsy-user fixes */

test('a 404 after the person cancelled a scan is the cancel working, not an old scan', () => {
  const gone = new CaseApiError('Not found.', 404, 'scan-not-found');
  assert.equal(discover.cancelledByUser(gone, true), true);
  assert.equal(discover.cancelledByUser(new CaseApiError('Not found.', 404, ''), true), true);
  // Without a cancel, the same answer still means the scan expired.
  assert.equal(discover.cancelledByUser(gone, false), false);
  assert.equal(discover.classifyError(gone), 'expired');
  // A cancel does not hide other faults.
  assert.equal(discover.cancelledByUser(new CaseApiError('Down.', 500, ''), true), false);
  assert.equal(discover.cancelledByUser(new CaseApiError('No answer.', 0, 'unreachable'), true), false);
  assert.equal(discover.cancelledByUser(new Error('x'), true), false);
  const panel = read('src/components/DiscoverPanel.svelte');
  assert.match(panel, /if \(cancelledByUser\(error, cancelWanted\)\) info = DISCOVER_TEXT\.scanCancelled;/);
});

test('no recording is shown once the case is closed, erased or not chosen', () => {
  const recording = { state: 'recording', since: '2001-09-11T08:00:00Z', samples: 7, stopReason: '' };
  assert.equal(discover.activeSampler(recording, true), recording);
  assert.equal(discover.activeSampler(recording, false), null);
  assert.equal(discover.activeSampler(null, true), null);
  const panel = read('src/components/DiscoverPanel.svelte');
  assert.match(panel, /const recording = \$derived\(liveSampler\?\.state === 'recording'\);/);
  assert.match(panel, /if \(!ready\) \{ sampler = null; recordingInterval = null; \}/);
  assert.match(copyOf('importCopy.ts'), /Any live recording for it stops\./);
});

test('another case or an erased case clears the import results', () => {
  const panel = read('src/components/DiscoverPanel.svelte');
  const body = panel.slice(panel.indexOf('function clearForCaseChange()'), panel.indexOf('$effect(() => {\n    const id = caseId;'));
  assert.match(body, /generation\+\+;/);
  assert.match(body, /rows = \[\];/);
  assert.match(body, /info = DISCOVER_TEXT\.caseChanged;/);
  assert.match(body, /step = scan \? 'list' : 'intro';/);
  assert.match(panel, /if \(id !== lastCase\) \{ lastCase = id; clearForCaseChange\(\); \}/);
  assert.ok(copy.DISCOVER_TEXT.caseChanged.length > 0);
});

test('a recording that is already running is read when the panel opens, and shows no interval it does not know', () => {
  assert.equal(copy.liveToggleLabel(15), 'Record CPU and memory every 15 seconds');
  assert.equal(copy.liveToggleLabel(null), 'Record CPU and memory');
  assert.ok(!/\d/.test(copy.liveToggleLabel(null)));
  const panel = read('src/components/DiscoverPanel.svelte');
  assert.match(panel, /else if \(sampler === null\) void refreshSampler\(true\);/);
  assert.match(panel, /const labelInterval = \$derived\(recording && recordingInterval === null \? null : interval\);/);
  assert.match(panel, /liveToggleLabel\(labelInterval\)/);
  assert.match(panel, /recordingInterval = interval;/);
  // The intro step shows the recording too, so it is not hidden until a scan.
  assert.match(panel, /\{#if recording\}\{@render liveSection\(\)\}\{\/if\}/);
});

test('a count of one reads as singular, and Import names no count when nothing is ticked', () => {
  assert.equal(copy.rowOutcomeText({ kind: 'added', rows: 1 }), 'Added 1 row');
  assert.equal(copy.rowOutcomeText({ kind: 'added', rows: 2 }), 'Added 2 rows');
  assert.equal(copy.rowOutcomeText({ kind: 'added', rows: 0 }), 'Added 0 rows');
  assert.equal(copy.rowOutcomeText({ kind: 'added', rows: 41200 }), 'Added 41,200 rows');
  assert.equal(copy.importButtonText(0), 'Import');
  assert.equal(copy.importButtonText(1), 'Import 1 source');
  const panel = read('src/components/DiscoverPanel.svelte');
  assert.match(panel, /disabled=\{block !== null \|\| chosen\.length === 0\} onclick=\{runImports\}>\{importButtonText\(chosen\.length\)\}/);
});

/* ------------------------------------------------------------------ verified findings, locate and probe (TSK-05) */

const SCAN = FAKE_SCAN_ID;
const FINDING_ID = 'k3m7q2x5v8b4n6c9d2f5g7h3j4';

test('an unverified log-file finding is not selectable, ticked or imported', () => {
  const unverified = f('unverified-1', { kind: 'log-file', importer: 'log4net-text', verified: false, unreadable: 'discover.cannotopen.shows' });
  const verified = f('verified-1', { kind: 'log-file', importer: 'log4net-text', verified: true });
  assert.equal(discover.isSelectable(unverified), false);
  assert.equal(discover.isSelectable(verified), true);
  assert.equal(discover.isSelectable({ kind: 'log-file', importer: 'iis-w3c' }), false, 'a finding with no verified flag is not trusted');
  assert.deepEqual([...discover.defaultSelection([unverified, verified], true)], ['verified-1']);
  assert.deepEqual([...discover.selectAllSmall([unverified, verified], new Set())], ['verified-1']);
  assert.deepEqual(discover.pickedFindings([unverified, verified], new Set(['unverified-1', 'verified-1'])).map((x) => x.id), ['verified-1']);
});

test('demo and fake findings are verified, so they stay selectable', () => {
  const finding = fakeFinding();
  assert.equal(finding.verified, true);
  assert.equal(finding.unreadable, '');
  assert.equal(finding.suggest, null);
  assert.equal(discover.isSelectable(finding), true);
});

test('a finding is read with its verified, unreadable and suggest fields', () => {
  const body = { id: 'valid-id-1', kind: 'log-file', importer: 'log4net-text', verified: true, unreadable: '', suggest: { portal: 'Orders', api: 'orders-api' } };
  const parsed = discover.parseFinding(body);
  assert.equal(parsed.verified, true);
  assert.deepEqual(parsed.suggest, { portal: 'Orders', api: 'orders-api' });
  for (const truthy of ['true', 1, 'yes', null, undefined]) assert.equal(discover.parseFinding({ ...body, verified: truthy }).verified, false, 'only a literal true verifies: ' + String(truthy));
  const bare = discover.parseFinding({ id: 'valid-id-1', kind: 'log-file', importer: 'log4net-text' });
  assert.equal(bare.verified, false);
  assert.equal(bare.suggest, null);
  assert.equal(discover.parseFinding({ ...body, suggest: { portal: '', api: '' } }).suggest, null);
});

test('readinessLabel gives the plain reason for a file that is not ready, and nothing otherwise', () => {
  const blocked = f('blocked-1', { kind: 'log-file', importer: 'log4net-text', verified: false, unreadable: 'discover.emptyfile.shows' });
  assert.equal(discover.readinessLabel(blocked), copy.unreadableText('discover.emptyfile.shows'));
  assert.notEqual(discover.readinessLabel(blocked), '');
  assert.equal(discover.readinessLabel(f('ok-1')), '', 'a ready file needs no reason');
  const noReason = f('blocked-2', { kind: 'log-set', importer: '', verified: false, unreadable: '' });
  assert.notEqual(discover.readinessLabel(noReason), '', 'an unverified file with no key still gets a general line');
  assert.ok(!discover.readinessLabel(noReason).includes('discover.'));
  assert.equal(discover.readinessLabel(f('x-1', { kind: 'export-needed', importer: '', verified: false })), '', 'rows that are not files have no file reason');
});

test('every reason the server can send has plain, linted copy', () => {
  const unreadable = ['cannotopen', 'emptyfile', 'wrongformat', 'verifytimeout'];
  for (const name of unreadable) {
    const text = copy.unreadableText('discover.' + name + '.shows');
    assert.ok(text.length > 10 && !text.includes('discover.'), name);
    assert.notEqual(text, copy.unreadableText(''), name + ' has its own line');
  }
  for (const reason of [...discover.LOCATE_FAILURES, ...discover.LOCATE_REFUSALS, 'unknown']) {
    const text = copy.locateReasonText(reason);
    assert.ok(text.length > 10, reason);
    if (reason !== 'unknown') assert.notEqual(text, copy.locateReasonText('unknown'), reason + ' has its own line');
  }
  for (const reason of [...discover.PROBE_REASONS, 'unknown']) {
    const text = copy.probeReasonText(reason);
    assert.ok(text.length > 10, reason);
    if (reason !== 'unknown') assert.notEqual(text, copy.probeReasonText('unknown'), reason + ' has its own line');
  }
  assert.equal(copy.locateReasonText('constructor'), copy.locateReasonText('unknown'));
  assert.equal(copy.probeReasonText('__proto__'), copy.probeReasonText('unknown'));
  const ids = new Set(copy.discoverCopyEntries().map((e) => e.id));
  for (const reason of [...discover.LOCATE_FAILURES, ...discover.LOCATE_REFUSALS]) assert.ok(ids.has('discover.locate.' + reason), reason + ' is linted');
  for (const reason of discover.PROBE_REASONS) assert.ok(ids.has('discover.probe.' + reason), reason + ' is linted');
  for (const name of unreadable) assert.ok(ids.has('discover.unreadable.' + name), name + ' is linted');
});

test('locateFinding sends the token and puts the path only in the body', async () => {
  const typed = 'D:\\Apps\\Orders\\Logs';
  const calls = mockServer(() => ({ body: { verified: true, files: 3, reason: '', finding: { id: FINDING_ID, kind: 'log-set', detector: 'applog', importer: 'log4net-text', verified: true, files: 3 } } }));
  const result = await discover.locateFinding(SCAN, FINDING_ID, '  ' + typed + '  ');
  assert.equal(calls.length, 1);
  assert.equal(calls[0].method, 'POST');
  assert.match(calls[0].url, new RegExp('/api/discover/scans/' + SCAN + '/findings/' + FINDING_ID + '/locate$'));
  assert.equal(new Headers(calls[0].headers).get('X-Audit-Token'), TOKEN);
  assert.deepEqual(JSON.parse(calls[0].body), { path: typed });
  assert.ok(!calls[0].url.includes('Orders') && !decodeURIComponent(calls[0].url).includes('Apps'), 'the path is never in the URL');
  assert.equal(result.ok, true);
  assert.equal(result.files, 3);
  assert.equal(result.finding.verified, true);
  assert.equal(result.finding.importer, 'log4net-text');
});

test('locateFinding turns a failed check or a refused path into a result, and throws on real faults', async () => {
  mockServer(() => ({ body: { verified: false, files: 0, reason: 'not-found' } }));
  assert.deepEqual(await discover.locateFinding(SCAN, FINDING_ID, 'D:\\x'), { ok: false, files: 0, reason: 'not-found' });
  mockServer(() => ({ body: { verified: false, files: 2, reason: 'a-reason-nobody-named' } }));
  assert.deepEqual(await discover.locateFinding(SCAN, FINDING_ID, 'D:\\x'), { ok: false, files: 2, reason: 'unknown' });
  mockServer(() => ({ status: 400, body: { error: 'Enter the full path.', reason: 'path-not-absolute' } }));
  assert.deepEqual(await discover.locateFinding(SCAN, FINDING_ID, 'x'), { ok: false, files: 0, reason: 'path-not-absolute' });
  mockServer(() => ({ status: 404, body: { error: 'Gone.', reason: 'scan-expired' } }));
  await assert.rejects(() => discover.locateFinding(SCAN, FINDING_ID, 'D:\\x'), (e) => e instanceof CaseApiError && e.reason === 'scan-expired' && discover.classifyError(e) === 'expired');
  mockServer(() => ({ status: 429, body: { error: 'Slow down.', reason: 'rate-limited' } }));
  await assert.rejects(() => discover.locateFinding(SCAN, FINDING_ID, 'D:\\x'), (e) => e instanceof CaseApiError && e.status === 429);
  // A "verified" answer that does not carry the right finding is not trusted.
  mockServer(() => ({ body: { verified: true, files: 1, finding: { id: 'some-other-id', kind: 'log-file', verified: true } } }));
  await assert.rejects(() => discover.locateFinding(SCAN, FINDING_ID, 'D:\\x'));
  mockServer(() => ({ body: { verified: true, files: 1, finding: { id: FINDING_ID, kind: 'log-file', verified: false } } }));
  await assert.rejects(() => discover.locateFinding(SCAN, FINDING_ID, 'D:\\x'));
  mockServer(() => ({ body: { nothing: true } }));
  await assert.rejects(() => discover.locateFinding(SCAN, FINDING_ID, 'D:\\x'));
});

test('locateFinding refuses an empty path, a huge path and a bad id without any call', async () => {
  const calls = mockServer(() => ({ body: {} }));
  assert.deepEqual(await discover.locateFinding(SCAN, FINDING_ID, '   '), { ok: false, files: 0, reason: 'path-not-absolute' });
  assert.deepEqual(await discover.locateFinding(SCAN, FINDING_ID, 'D:\\' + 'a'.repeat(discover.MAX_LOCATE_PATH)), { ok: false, files: 0, reason: 'path-unsafe' });
  await assert.rejects(() => discover.locateFinding(SCAN, '../../etc', 'D:\\x'), (e) => e instanceof CaseApiError && e.reason === 'bad-finding-id');
  await assert.rejects(() => discover.locateFinding('not a scan id', FINDING_ID, 'D:\\x'));
  assert.equal(calls.length, 0);
});

test('probeReachable sends the token and the host and port in the body', async () => {
  const calls = mockServer(() => ({ body: { reachable: true, reason: '' } }));
  assert.deepEqual(await discover.probeReachable(' db.internal ', 1433), { reachable: true, reason: '' });
  assert.equal(calls.length, 1);
  assert.equal(calls[0].method, 'POST');
  assert.match(calls[0].url, /\/api\/probe\/reachable$/);
  assert.equal(new Headers(calls[0].headers).get('X-Audit-Token'), TOKEN);
  assert.deepEqual(JSON.parse(calls[0].body), { host: 'db.internal', port: 1433 });
  assert.ok(!calls[0].url.includes('db.internal'));
});

test('probeReachable maps each reason, and refuses a bad host or port without a call', async () => {
  for (const reason of discover.PROBE_REASONS) {
    mockServer(() => ({ body: { reachable: false, reason } }));
    assert.deepEqual(await discover.probeReachable('db', 1433), { reachable: false, reason });
  }
  mockServer(() => ({ body: { reachable: false, reason: 'weird' } }));
  assert.deepEqual(await discover.probeReachable('db', 1433), { reachable: false, reason: 'unknown' });
  mockServer(() => ({ body: { reachable: 'maybe' } }));
  await assert.rejects(() => discover.probeReachable('db', 1433));
  const calls = mockServer(() => ({ body: { reachable: true } }));
  for (const [host, port, reason] of [['', 1433, 'bad-host'], ['a b', 1433, 'bad-host'], ['x'.repeat(300), 1433, 'bad-host'], ['db', 0, 'bad-port'], ['db', 70000, 'bad-port'], ['db', 1.5, 'bad-port'], ['db', NaN, 'bad-port']]) {
    await assert.rejects(() => discover.probeReachable(host, port), (e) => e instanceof CaseApiError && e.reason === reason, host + ':' + port);
  }
  assert.equal(calls.length, 0);
  mockServer(() => ({ status: 429, body: { error: 'Slow down.', reason: 'rate-limited' } }));
  await assert.rejects(() => discover.probeReachable('db', 1433), (e) => e instanceof CaseApiError && e.status === 429);
});
