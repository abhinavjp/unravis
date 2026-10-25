// Phase 05 clumsy-user review (create wizard: TSK-17 shell, TSK-18 sources step, TSK-19 review/create/import).
// Tests named 'bug: ...' are CONFIRMED defects and FAIL on purpose until fixed; when one is fixed, rename it to 'fixed: ...' and keep it.
// Tests named 'p05: ...' pin behaviour that holds.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

const root = path.resolve(__dirname, '..');
// One module cache for the whole file, so a CaseApiError / ApiError is the same class in the test and in the code under test.
// The three wizard stores keep their state in `$state(...)`; outside Svelte it is a plain value, which is all these tests need.
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

const { CaseApiError } = load('src/lib/cases.ts');
const { ApiError } = load('src/lib/api.ts');
const sub = load('src/lib/wizardSubmit.ts');
const src = load('src/lib/wizardSources.ts');
const envs = load('src/lib/wizardEnvs.ts');
const flow = load('src/lib/wizardFlow.ts');
const conns = load('src/lib/wizardConnections.ts');
const W = load('src/lib/wizard.svelte.ts');
const C = load('src/lib/wizardCreate.svelte.ts');
const S = load('src/lib/wizardSources.svelte.ts');
const { afterUnlock } = load('src/lib/screenFlow.ts');

const WINDOW = { from: '2001-01-01T00:00:00Z', to: '2001-01-02T00:00:00Z' };
const finding = (id, over = {}) => ({ id, detector: 'iis', kind: 'log-set', importer: 'iis', host: 'h', display: 'W3SVC' + id, confidence: 1, shows: 'x', how: '', sizeBytes: 1, files: 1, oldest: '', newest: '', needsAdmin: false, cost: 'small', verified: true, unreadable: '', suggest: null, ...over });
const doneScan = (findings) => ({ state: 'done', startedAt: new Date().toISOString(), findings, gaps: [], host: null, progress: [] });
const tick = (ms = 5) => new Promise((resolve) => setTimeout(resolve, ms));

/** A fake server that remembers what it was told, even when the reply to the caller is lost. */
function fakeApi(o = {}) {
  const log = [];
  const server = { cases: [], tags: [], audit: [] };
  let creates = 0;
  let imports423 = o.imports423 ?? 0;
  const api = {
    log, server,
    async createCase(title) {
      creates += 1; log.push('createCase');
      if (o.create423 && creates === 1) throw new CaseApiError('locked', 423, 'locked');
      server.cases.push(title);
      if (o.createLosesReply && creates === 1) throw new TypeError('network dropped after the server answered');
      if (o.slow) await tick(20);
      return { id: 'c' + server.cases.length };
    },
    /** The list route of the real server: a case made by an earlier try, even when its reply was lost. */
    async findRecentCase(title) { log.push('find'); const at = server.cases.indexOf(title); return at >= 0 ? { id: 'c' + (at + 1) } : null; },
    async putEnvironments() { log.push('env'); },
    async postSourceTag(caseId, tag) { log.push('tag:' + tag.label); server.tags.push(tag.label); },
    async testConnection(id) { log.push('test:' + id); if (o.testError) throw o.testError; return {}; },
    async addAuditSource(id, database) { log.push('audit:' + database); if (o.auditError) throw o.auditError; server.audit.push(database); return {}; },
    async confirmHost() {},
    async startImports(caseId, request) {
      log.push('start:' + request.scanId + ':' + request.findingIds.length);
      if (imports423 > 0) { imports423 -= 1; throw new CaseApiError('locked', 423, 'locked'); }
      return request.findingIds.map((findingId) => ({ findingId, jobId: 'j' + findingId }));
    },
    async watchJob() { return { kind: 'added', rows: 1 }; },
    async importFile() { return { kind: 'added', rows: 1 }; }
  };
  return api;
}

/** A scan client whose second scan waits at a gate, so a test can stand "in the middle of a rescan". */
function scanClient(findings, { rescanFinds = [] } = {}) {
  let scans = 0;
  let release = () => {};
  const gate = new Promise((resolve) => { release = resolve; });
  return {
    release,
    client: {
      async startScan() { scans += 1; if (scans >= 2) await gate; return 'scan' + scans; },
      async getScan() { return doneScan(scans >= 2 ? rescanFinds : findings); },
      async cancelScan() {}
    }
  };
}

function freshDraft(title = 'Week 42 incident') {
  W.resetWizard();
  W.wizard.title = title;
}

/* ====================================================================== confirmed defects */

test('fixed: a rescan in progress, with scan sources chosen, must stop Create (blockers() is empty)', async () => {
  freshDraft();
  const f = finding('f1');
  const { client, release } = scanClient([f]);
  await S.startScan({ client });
  S.tickFinding('f1', true);
  assert.equal(W.wizard.sources.length, 1, 'setup: one scan source chosen');
  const pending = S.startScan({ client }); // "Scan again", never finishing in this test
  await tick();
  C.setAck(true);
  try {
    assert.equal(S.sourcesView.phase, 'scanning', 'setup: a rescan is running and the scan id is cleared');
    assert.notEqual(C.blockers().length, 0, 'Create stays open while the scan the chosen sources belong to is gone');
  } finally { release(); await pending; }
});

test('fixed: Create during a rescan makes the case, then fails every scan source with "no-window" ("No period was chosen")', async () => {
  freshDraft();
  const f = finding('f1');
  const { client, release } = scanClient([f]);
  await S.startScan({ client });
  S.tickFinding('f1', true);
  const pending = S.startScan({ client });
  await tick();
  C.setAck(true);
  const api = fakeApi();
  let result;
  try { result = await C.createNow(api); } finally { release(); await pending; }
  const noWindow = result && result.failures.some((failure) => failure.reason === 'no-window');
  assert.ok(!noWindow, 'the person did choose a period; the failure is the missing scan, and by then the case already exists: ' + JSON.stringify(result && result.failures) + ' calls=' + api.log.join(','));
});

test('fixed: a lost reply to createCase, then a second click on Create, makes a second case on the server', async () => {
  freshDraft('Lost reply case');
  const api = fakeApi({ createLosesReply: true });
  const first = await C.createNow(api);
  assert.ok(first, 'setup: the first click ran');
  assert.equal(C.caseMade(), false, 'setup: the wizard believes no case was made, so Create is open again');
  await C.createNow(api);
  assert.equal(api.server.cases.length, 1, 'the server holds ' + api.server.cases.length + ' cases named "Lost reply case"');
});

test('fixed: an ApiError (429 too many connection tests) from the tag or audit step is reported as "unreachable"', async () => {
  freshDraft('Mongo case');
  const id = conns.holdConnection({ uri: 'db1:27017', username: '', password: '', authSource: '', tlsMode: 'default', caCertificate: '' });
  const db = src.makeSource('mongo-database', src.databaseRef(id, 'audit1'), 'audit1');
  const rate = new ApiError('Too many connection tests in the last minute. Wait a minute, then test again.', undefined, undefined, 429);
  const api = fakeApi({ testError: rate });
  const state = sub.blankSubmitState();
  const input = { title: 'Mongo case', envs: [], sources: [{ ...db, classification: 'audit' }], window: null, scanId: '', files: {}, ackText: '', host: null };
  const result = await sub.submitWizard(input, api, state);
  const reasons = result.failures.map((failure) => failure.reason);
  assert.ok(reasons.length > 0, 'setup: the step failed');
  assert.ok(!reasons.includes('unreachable'), 'a 429 from this very server is told as "The server could not be reached." (reasons: ' + reasons.join(',') + ')');
  conns.releaseConnections();
});

test('fixed: hostLabel shows the tail of a password that holds an @ inside a connection option', () => {
  const label = src.hostLabel('mongodb://db1:27017/?tlsCertificateKeyFilePassword=hu@nter2');
  assert.ok(!label.includes('nter2'), 'the host label for the screen was "' + label + '"');
});

test('fixed: a database name with a doubled space is tagged under another label than the one the server was tested with', () => {
  const connection = { id: 'conn-1', databases: ['audit  main'] };
  const result = src.classifyDatabase([], connection, 'audit  main', 'audit');
  assert.ok(result.ok);
  assert.equal(result.list[0].label, 'audit  main', 'the tag POST sends label "' + result.list[0].label + '"; the server only accepts the exact tested name');
});

test('fixed: Lock on the create screen returns to the start screen, not to the case being made', () => {
  const state = read('src/lib/appState.svelte.ts');
  const body = state.slice(state.indexOf('export async function lockCases'));
  const fn = body.slice(0, body.search(/\r?\n\}\r?\n/));
  assert.match(fn, /'create'/, 'lockCases sets unlockOrigin for running only; the 423 path (noteLocked) also keeps create');
  assert.equal(afterUnlock('create'), 'create', 'setup: afterUnlock itself does return to create');
});

/* ====================================================================== behaviour that holds */

test('p05: a double click on Create makes one case and the second call is refused', async () => {
  freshDraft('Double click');
  const api = fakeApi({ slow: true });
  const [a, b] = await Promise.all([C.createNow(api), C.createNow(api)]);
  assert.equal(api.server.cases.length, 1);
  assert.ok(a && b === null);
});

test('p05: a 423 while creating the case keeps the draft and the retry makes the case once', async () => {
  freshDraft('Locked at start');
  const api = fakeApi({ create423: true });
  await C.createNow(api);
  assert.equal(C.createView.submit.locked, true);
  const again = await C.createNow(api);
  assert.equal(api.server.cases.length, 1);
  assert.ok(again && again.caseId);
});

test('p05: a 423 during the imports, then unlock and retry, does not make a second case or repeat a tag', async () => {
  freshDraft('Locked mid imports');
  const f = finding('f1');
  const { client, release } = scanClient([f]);
  await S.startScan({ client });
  release();
  S.tickFinding('f1', true);
  C.setAck(true);
  const api = fakeApi({ imports423: 1 });
  await C.createNow(api);
  assert.equal(C.createView.submit.locked, true);
  assert.equal(C.caseMade(), true);
  const done = await C.createNow(api);
  assert.equal(api.server.cases.length, 1, 'one case');
  assert.equal(api.server.tags.length, 1, 'one tag');
  assert.ok(done && done.failures.length === 0);
});

test('p05: the same finding ticked twice is one source; a finding that cannot be read cannot be ticked', () => {
  const f = finding('f1');
  const once = src.toggleFinding([], f, true);
  const twice = src.toggleFinding(once, f, true);
  assert.equal(twice.length, 1);
  for (const bad of [finding('c1', { kind: 'case-file' }), finding('c2', { importer: '' }), finding('c3', { verified: false })]) {
    assert.equal(src.toggleFinding([], bad, true).length, 0, bad.kind + ' ' + bad.importer + ' ' + bad.verified);
  }
});

test('p05: the draft holds at most 200 sources and the 201st is refused, a repeat collapses', () => {
  let list = [];
  for (let i = 0; i < 205; i += 1) { const added = src.addSource(list, src.makeSource('discover', 'f' + i, 'F' + i)); if (added.ok) list = added.list; }
  assert.equal(list.length, src.MAX_SOURCES);
  assert.equal(src.MAX_SOURCES, 200);
  assert.equal(src.addSource(list, src.makeSource('discover', 'f0', 'again')).reason, 'duplicate');
});

test('p05: odd titles: blank, emoji, RTL and very long are judged by characters, format characters are refused', () => {
  assert.equal(flow.titleProblem('   '), 'empty');
  assert.equal(flow.titleProblem('Outage \u{1F525}\u{1F525}'), null);
  assert.equal(flow.titleProblem('\u05D0\u05D1\u05D2 incident'), null);
  assert.equal(flow.titleProblem('a\u202Eb incident'), 'bad-characters');
  assert.equal(flow.titleProblem('x'.repeat(500)), 'too-long');
  assert.equal(flow.titleProblem('a\u0000b incident'), 'bad-characters');
});

test('p05: environment names are bounded, repeats collapse, the chevron and control characters are removed', () => {
  const parsed = envs.parseEnvs('Prod, prod, PROD\nUAT; a\u202Eb, x\u203Ay, ' + 'z'.repeat(200));
  assert.ok(parsed.names.every((n) => [...n].length <= 60));
  assert.equal(parsed.names.filter((n) => n.toLowerCase() === 'prod').length, 1);
  assert.ok(parsed.names.every((n) => !/[\u202E\u203A]/.test(n)));
  const many = envs.parseEnvs(Array.from({ length: 100 }, (_, i) => 'e' + i).join(','));
  assert.ok(many.names.length <= 32);
});

test('p05: the address box refuses a password, a user name and a full URI, and takes a plain host', () => {
  for (const text of ['mongodb://u:p@h/db', 'user:pw@host:27017', 'host/path', 'host password=abc', 'http://h:1/', 'a@b', '']) {
    assert.equal(src.parseAddress(text, 1433).ok, false, text);
  }
  assert.deepEqual(src.parseAddress('good.host:8080', 1433), { ok: true, host: 'good.host', port: 8080 });
  assert.equal(src.parseAddress('host:99999', 1433).ok, false);
});

test('p05: a failed Mongo test shows no password, no user name, no address', async () => {
  freshDraft();
  const input = { uri: 'mongodb://bob:hunter22@db1:27017/', username: 'bob', password: 'hunter22', authSource: '', tlsMode: 'default', caCertificate: '' };
  const outcome = await S.testMongo(input, '', { testConnection: async () => { throw new Error('Authentication failed for bob (hunter22) at mongodb://bob:hunter22@db1:27017/ for db1:27017'); } });
  assert.equal(outcome.ok, false);
  for (const secret of ['hunter22', 'mongodb://', 'bob:']) assert.ok(!outcome.text.includes(secret), secret + ' in "' + outcome.text + '"');
});

test('p05: a huge, odd or hostile Mongo reply is bounded and never reaches the page as markup', async () => {
  freshDraft();
  const input = { uri: 'db1:27017', username: '', password: '', authSource: '', tlsMode: 'default', caCertificate: '' };
  const names = Array.from({ length: 5000 }, (_, i) => (i === 0 ? '<img src=x onerror=alert(1)>' : 'db' + i)).concat([42, null, '', 'x'.repeat(500)]);
  const outcome = await S.testMongo(input, '', { testConnection: async () => ({ databases: names, warning: 'w'.repeat(10) }) });
  assert.equal(outcome.ok, true);
  assert.ok(S.sourcesView.connections.at(-1).databases.length <= 200);
  assert.ok(S.sourcesView.connections.at(-1).databases.every((d) => typeof d === 'string' && d.length <= 120));
  const bad = await S.testMongo(input, '', { testConnection: async () => null });
  assert.equal(bad.ok, true, 'a null reply is tolerated');
  assert.equal(S.sourcesView.connections.at(-1).databases.length, 0);
});

test('p05: resetWizard clears the draft, the run state, the scan and the held connections', async () => {
  freshDraft('Leaves nothing');
  const input = { uri: 'db1:27017', username: 'u', password: 'pw-123456', authSource: '', tlsMode: 'default', caCertificate: '' };
  await S.testMongo(input, '', { testConnection: async () => ({ databases: ['a'] }) });
  const id = S.sourcesView.connections[0].id;
  assert.ok(conns.heldConnection(id), 'setup: the connection is held in memory');
  C.setAck(true);
  W.resetWizard();
  assert.equal(W.wizard.title, '');
  assert.equal(W.wizard.sources.length, 0);
  assert.equal(conns.heldConnection(id), null);
  assert.equal(conns.heldCount(), 0);
  assert.equal(S.sourcesView.connections.length, 0);
  assert.equal(C.createView.ack, false);
  assert.equal(C.createView.result, null);
  assert.equal(C.caseMade(), false);
});

test('p05: the draft snapshot and the run state hold no connection secret', async () => {
  freshDraft('No secrets');
  const input = { uri: 'mongodb://bob:hunter22@db1:27017/', username: 'bob', password: 'hunter22', authSource: '', tlsMode: 'default', caCertificate: '' };
  await S.testMongo(input, '', { testConnection: async () => ({ databases: ['audit1'] }) });
  const id = S.sourcesView.connections[0].id;
  S.classifyMongo?.(id, 'audit1', 'audit');
  const api = fakeApi({ testError: new CaseApiError('boom bob hunter22 mongodb://bob:hunter22@db1', 500, 'boom') });
  const result = await C.createNow(api);
  const dump = JSON.stringify({ draft: W.draftSnapshot(), view: C.createView, result, sources: S.sourcesView });
  for (const secret of ['hunter22', 'bob:', 'mongodb://']) assert.ok(!dump.includes(secret), secret + ' found in wizard state');
  W.resetWizard();
});

test('p05: a failed step is kept as a short reason code, never the server text', async () => {
  freshDraft('Reason codes');
  const f = finding('f1');
  const { client, release } = scanClient([f]);
  await S.startScan({ client });
  release();
  S.tickFinding('f1', true);
  C.setAck(true);
  const api = fakeApi();
  api.startImports = async () => { throw new CaseApiError('SECRET-LEAK C:\\Users\\bob\\file.log', 500, 'some-code'); };
  const result = await C.createNow(api);
  const dump = JSON.stringify(result);
  assert.ok(!dump.includes('SECRET-LEAK') && !dump.includes('bob'));
  assert.ok(result.failures.length > 0);
});

test('p05: source labels and paths are only ever printed as text, never as markup', () => {
  const files = ['CreateWizard', 'ReviewStep', 'CreateProgress', 'SourceGallery', 'SourceTagRow', 'MongoClassify', 'LocateFolder', 'ReachableCheck'];
  for (const name of files) {
    const dir = name === 'CreateWizard' ? 'src/components/' : 'src/components/wizard/';
    const text = read(dir + name + '.svelte');
    assert.ok(!/\{@html/.test(text), name + ' uses {@html}');
    assert.ok(!/innerHTML\s*=/.test(text), name + ' assigns innerHTML');
  }
});

test('p05: while a case is being made the guide cannot be left: Back, Next, the steps and Leave are disabled, and a reload is warned about', () => {
  const wizard = read('src/components/CreateWizard.svelte');
  assert.match(wizard, /class="quiet-button wiz-leave" disabled=\{busy\}/);
  assert.match(wizard, /secondary-button" disabled=\{busy\}/);
  assert.match(wizard, /primary-button" disabled=\{busy \|\| nextBlocked\}/);
  assert.match(wizard, /disabled=\{busy \|\| !view\.enabled\}/);
  assert.match(wizard, /beforeunload/);
  assert.match(wizard, /if \(event\.key === 'Escape'\) \{ event\.stopPropagation\(\); keepWorking\(\); \}/);
  const landing = read('src/components/Landing.svelte');
  assert.match(landing, /busy=\{createView\.running \|\| caseMade\(\)\}/);
});

test('p05: the progress panel is announced, takes focus at the end, lists failures as an alert, and the button to create is gone once the case exists', () => {
  const progress = read('src/components/wizard/CreateProgress.svelte');
  assert.match(progress, /role="status" tabindex="-1"/);
  assert.match(progress, /role="alert"/);
  assert.match(progress, /box\?\.focus\(\)/);
  const landing = read('src/components/Landing.svelte');
  assert.match(landing, /\{#if !caseMade\(\)\}<button type="button" class="primary-button" disabled=\{createView\.running \|\| blockers\(\)\.length > 0\}/);
});

test('p05: an empty selection makes an empty case, and a Mongo-only run needs no notice', async () => {
  freshDraft('Empty case');
  assert.deepEqual(C.blockers(), []);
  const api = fakeApi();
  const result = await C.createNow(api);
  assert.ok(result && result.caseId);
  assert.equal(api.server.cases.length, 1);
});

/* ====================================================================== extra checks for the fixes */

test('p05: hostLabel keeps the host of an address whose password holds a / or whose option holds an @', () => {
  assert.equal(src.hostLabel('mongodb://u:p/ss@host/db'), 'host');
  assert.equal(src.hostLabel('mongodb://u:pw@db1:27017,db2:27017/?tlsCertificateKeyFilePassword=hu@nter2'), 'db1:27017 and 1 more');
  assert.equal(src.hostLabel('mongodb://db1:27017/db#a@b'), 'db1:27017');
});

test('p05: a database name that looks like a password or is too long is refused as a source, any other name is kept exactly', () => {
  const connection = { id: 'conn-1', databases: ['user:pw@host', 'x'.repeat(81), 'plain'] };
  assert.equal(src.classifyDatabase([], connection, 'user:pw@host', 'audit').reason, 'bad-name');
  assert.equal(src.classifyDatabase([], connection, 'x'.repeat(81), 'audit').reason, 'bad-name');
  assert.equal(src.classifyDatabase([], connection, 'plain', 'audit').list[0].label, 'plain');
});

test('p05: an ApiError 423 from a step stops the run as locked, and 400 and 429 get their own plain reasons', async () => {
  freshDraft('Locked api error');
  const id = conns.holdConnection({ uri: 'db1:27017', username: '', password: '', authSource: '', tlsMode: 'default', caCertificate: '' });
  const db = src.makeSource('mongo-database', src.databaseRef(id, 'audit1'), 'audit1');
  const input = { title: 'Mongo case', envs: [], sources: [{ ...db, classification: 'audit' }], window: null, scanId: '', files: {}, ackText: '', host: null };
  const locked = await sub.submitWizard(input, fakeApi({ testError: new ApiError('locked', undefined, undefined, 423) }), sub.blankSubmitState());
  assert.equal(locked.locked, true);
  const bad = await sub.submitWizard(input, fakeApi({ testError: new ApiError('no', undefined, undefined, 400) }), sub.blankSubmitState());
  assert.deepEqual(bad.failures.map((f) => f.reason), ['bad-request']);
  const rate = await sub.submitWizard(input, fakeApi({ testError: new ApiError('slow down', undefined, undefined, 429) }), sub.blankSubmitState());
  assert.deepEqual(rate.failures.map((f) => f.reason), ['rate-limited']);
  const offline = await sub.submitWizard(input, fakeApi({ testError: new TypeError('failed to fetch') }), sub.blankSubmitState());
  assert.deepEqual(offline.failures.map((f) => f.reason), ['unreachable']);
  conns.releaseConnections();
});

test('p05: a retry after a failed create asks for a case made by the first try, and makes one only when there is none', async () => {
  freshDraft('Retry looks first');
  const api = fakeApi();
  api.createCase = async () => { api.log.push('createCase'); throw new CaseApiError('boom', 500, 'boom'); };
  await C.createNow(api);
  assert.equal(C.caseMade(), false);
  assert.ok(!api.log.includes('find'), 'the first try has nothing to look for');
  const second = await C.createNow(api);
  assert.ok(api.log.includes('find'), 'the second try looks first');
  assert.equal(api.log.filter((entry) => entry === 'createCase').length, 2, 'nothing was found, so it makes one');
  assert.equal(second.caseId, '');
});

test('p05: the review names a missing scan, and the fixed lines for the new reasons are plain', () => {
  const copy = load('src/lib/copy/wizardCreateCopy.ts');
  for (const reason of ['no-scan', 'rate-limited', 'bad-request']) assert.notEqual(copy.reasonLine(reason), copy.reasonLine('nothing-like-this'), reason);
  assert.match(read('src/components/wizard/ReviewStep.svelte'), /scanBusy/);
});
