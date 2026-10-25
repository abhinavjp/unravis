const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

const root = path.resolve(__dirname, '..');
// One instance per file, so a CaseApiError is the same class in the test, in cases.ts and in wizardSubmit.ts.
const loaded = new Map();
function loadTypeScript(relativePath) {
  const filename = path.join(root, relativePath);
  if (loaded.has(filename)) return loaded.get(filename).exports;
  const source = fs.readFileSync(filename, 'utf8');
  const output = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022, esModuleInterop: true } }).outputText;
  const module = { exports: {} };
  loaded.set(filename, module);
  const nodeRequire = createRequire(filename);
  const load = (request) => {
    if (/^\.\.?\//.test(request)) {
      const plain = path.join(path.dirname(filename), request + '.ts');
      const store = path.join(path.dirname(filename), request + '.svelte.ts');
      if (fs.existsSync(plain)) return loadTypeScript(path.relative(root, plain));
      if (fs.existsSync(store)) return loadTypeScript(path.relative(root, store));
    }
    return nodeRequire(request);
  };
  new Function('require', 'module', 'exports', output)(load, module, module.exports);
  return module.exports;
}
const read = (relative) => fs.readFileSync(path.join(root, relative), 'utf8');

const cases = loadTypeScript('src/lib/cases.ts');
const sub = loadTypeScript('src/lib/wizardSubmit.ts');
const sources = loadTypeScript('src/lib/wizardSources.ts');
const copy = loadTypeScript('src/lib/copy/wizardCreateCopy.ts');
const { lintScreenStrings } = loadTypeScript('src/lib/copy/copylint.ts');
const { deck } = loadTypeScript('src/lib/copy/deck.ts');
const { CaseApiError } = cases;

// Synthetic values only: made-up names, a made-up connection id, dates in 2001.
const WINDOW = { from: '2001-01-01T00:00:00Z', to: '2001-01-02T00:00:00Z' };
const CONN = 'abc123def456';

const scan = (id, label, tags = {}) => ({ ...sources.makeSource('discover', id, label), ...tags });
const file = (label, ref, kind = 'iis-logs') => sources.makeSource(kind, ref, label);
const mongo = (name, classification) => ({ ...sources.makeSource('mongo-database', 'mongo:' + CONN + '/' + name, name), classification });
const FILE_A = { name: 'a.log' };
const FILE_B = { name: 'b.log' };

function input(over = {}) {
  return { title: 'Slowdown week 42', envs: ['Production'], sources: [], window: WINDOW, scanId: 'scan-1', host: null, files: {}, ackText: 'I understand', ...over };
}

/** A fake api that logs every call in order. `fail` maps a call name to an error thrown the first time it runs (or always, with `always`). */
function fakeApi({ fail = {}, always = {}, outcomes = {}, jobDelay = 0 } = {}) {
  const log = [];
  const once = new Set();
  const maybe = (name, arg) => {
    log.push(name + (arg === undefined ? '' : ':' + arg));
    if (always[name]) throw always[name];
    if (fail[name] && !once.has(name)) { once.add(name); throw fail[name]; }
  };
  let nextCase = 0;
  const api = {
    log,
    async createCase(title) { maybe('createCase', title); nextCase++; return { id: 'case' + nextCase }; },
    async putEnvironments(id, envs) { maybe('putEnvironments', envs.join('+')); return envs; },
    async postSourceTag(id, tag) { maybe('postSourceTag', tag.kind + '/' + tag.label); return tag; },
    async testConnection(connId) { maybe('testConnection', connId); return {}; },
    async addAuditSource(connId, database) { maybe('addAuditSource', database); return {}; },
    async confirmHost() { maybe('confirmHost'); return {}; },
    async startImports(id, request) { maybe('startImports', request.findingIds.join('+')); api.lastRequest = request; return request.findingIds.map((findingId) => ({ findingId, jobId: 'job-' + findingId })); },
    async watchJob(id, ref, onProgress) {
      maybe('watchJob', ref.findingId);
      onProgress(50);
      if (jobDelay) await new Promise((r) => setTimeout(r, jobDelay));
      return outcomes[ref.findingId] ?? { kind: 'added', rows: 10 };
    },
    async importFile(id, f) { maybe('importFile', f.name); return outcomes[f.name] ?? { kind: 'added', rows: 5 }; }
  };
  return api;
}

const run = (inp, api, state = sub.blankSubmitState()) => sub.submitWizard(inp, api, state).then((result) => ({ result, state }));

// ---- the happy path

test('success: the case, then environments, then tags, then imports, in that order', async () => {
  const api = fakeApi();
  const inp = input({ sources: [scan('f1', 'W3SVC1', { env: 'Production' }), file('Upload logs', 'iis:x'), mongo('orders', 'audit')], files: { 'iis-logs|iis:x': FILE_A } });
  const { result, state } = await run(inp, api);
  // The scan jobs and the files run side by side, so after the tags only the order inside each chain is fixed.
  assert.deepEqual(api.log.slice(0, 7), [
    'createCase:Slowdown week 42', 'putEnvironments:Production',
    'postSourceTag:discover/W3SVC1', 'postSourceTag:iis-logs/Upload logs',
    'testConnection:' + CONN, 'postSourceTag:mongo-database/orders', 'addAuditSource:orders'
  ]);
  assert.deepEqual(api.log.slice(7).filter((c) => !c.startsWith('importFile')), ['startImports:f1', 'watchJob:f1']);
  assert.deepEqual(api.log.slice(7).filter((c) => c.startsWith('importFile')), ['importFile:a.log']);
  assert.equal(result.caseId, 'case1');
  assert.equal(result.finished, true);
  assert.equal(result.locked, false);
  assert.equal(result.imported, 2);
  assert.deepEqual(result.failures, []);
  assert.equal(sub.shouldOpenAnalysis(result), true);
  assert.equal(state.envs.status, 'done');
});

test('the scan request carries the window and the ticked notice text', async () => {
  const api = fakeApi();
  await run(input({ sources: [scan('f1', 'W3SVC1')] }), api);
  assert.deepEqual(api.lastRequest, { scanId: 'scan-1', findingIds: ['f1'], window: WINDOW, ackGating: true, ackText: 'I understand' });
});

test('an unconfirmed host is confirmed before the scan findings are imported', async () => {
  const api = fakeApi();
  await run(input({ sources: [scan('f1', 'W3SVC1')], host: { proposed: true } }), api);
  assert.ok(api.log.indexOf('confirmHost') < api.log.indexOf('startImports:f1'));
});

test('an empty selection makes an empty case and still finishes', async () => {
  const api = fakeApi();
  const { result } = await run(input({ sources: [], envs: [] }), api);
  assert.deepEqual(api.log, ['createCase:Slowdown week 42']);
  assert.equal(result.finished, true);
  assert.equal(result.imported, 0);
  assert.equal(sub.shouldOpenAnalysis(result), true);
});

test('named-only kinds are tagged and never imported', async () => {
  const api = fakeApi();
  const { result } = await run(input({ envs: [], sources: [sources.makeSource('sql-server', 'db1:1433', 'Orders SQL'), sources.makeSource('redis', 'r1:6379', 'Cache')] }), api);
  assert.deepEqual(api.log, ['createCase:Slowdown week 42', 'postSourceTag:sql-server/Orders SQL', 'postSourceTag:redis/Cache']);
  assert.equal(result.imported, 0);
  assert.deepEqual(result.failures, []);
});

test('a Mongo connection is tested once however many of its databases are chosen', async () => {
  const api = fakeApi();
  await run(input({ envs: [], sources: [mongo('a', 'other'), mongo('b', 'unused'), mongo('c', 'audit')] }), api);
  assert.equal(api.log.filter((c) => c.startsWith('testConnection')).length, 1);
  assert.equal(api.log.filter((c) => c.startsWith('addAuditSource')).length, 1);
});

// ---- nothing before submit

test('cancel before submit: zero calls', async () => {
  const api = fakeApi();
  const state = sub.blankSubmitState();
  sub.prepareState(input({ sources: [scan('f1', 'W3SVC1')] }), state);
  assert.deepEqual(api.log, []);
  assert.equal(state.caseId, '');
});

test('double click: one case, one run', async () => {
  const api = fakeApi({ jobDelay: 5 });
  const state = sub.blankSubmitState();
  const inp = input({ sources: [scan('f1', 'W3SVC1')] });
  const first = sub.submitWizard(inp, api, state);
  const second = sub.submitWizard(inp, api, state);
  assert.equal(first, second);
  await Promise.all([first, second]);
  assert.equal(api.log.filter((c) => c.startsWith('createCase')).length, 1);
  assert.equal(api.log.filter((c) => c.startsWith('startImports')).length, 1);
});

// ---- failures keep the case

test('environments call fails: the case is kept, the failure reported, the rest still runs', async () => {
  const api = fakeApi({ fail: { putEnvironments: new CaseApiError('no', 422, 'bad-tag') } });
  const { result, state } = await run(input({ sources: [scan('f1', 'W3SVC1')] }), api);
  assert.equal(result.caseId, 'case1');
  assert.equal(state.envs.status, 'failed');
  assert.deepEqual(result.failures, [{ step: 'environments', key: '', label: '', reason: 'bad-tag' }]);
  assert.ok(api.log.includes('startImports:f1'));
  assert.equal(result.imported, 1);
  assert.equal(sub.shouldOpenAnalysis(result), false);
  assert.ok(!api.log.some((c) => /delete/i.test(c)));
});

test('one of three imports fails: the other two finish', async () => {
  const api = fakeApi({ outcomes: { f2: { kind: 'refused', reason: 'unsupported-format' } } });
  const { result } = await run(input({ envs: [], sources: [scan('f1', 'A'), scan('f2', 'B'), scan('f3', 'C')] }), api);
  assert.equal(result.imported, 2);
  assert.equal(result.finished, true);
  assert.deepEqual(result.failures, [{ step: 'import', key: 'discover|f2', label: 'B', reason: 'unsupported-format' }]);
  assert.equal(sub.shouldOpenAnalysis(result), false);
});

test('a file that fails does not stop the next file', async () => {
  const api = fakeApi({ outcomes: { 'a.log': { kind: 'failed', reason: 'unreachable' } } });
  const { result } = await run(input({ envs: [], sources: [file('A', 'iis:a'), file('B', 'iis:b')], files: { 'iis-logs|iis:a': FILE_A, 'iis-logs|iis:b': FILE_B } }), api);
  assert.equal(result.imported, 1);
  assert.equal(result.failures.length, 1);
  assert.ok(api.log.includes('importFile:b.log'));
});

test('a file source with no file is skipped and reported, never sent', async () => {
  const api = fakeApi();
  const { result } = await run(input({ envs: [], sources: [file('A', 'iis:a')] }), api);
  assert.ok(!api.log.some((c) => c.startsWith('importFile')));
  assert.deepEqual(result.failures, [{ step: 'import', key: 'iis-logs|iis:a', label: 'A', reason: 'no-file' }]);
});

test('a tag that fails is reported; the import of that source still runs', async () => {
  const api = fakeApi({ always: { postSourceTag: new CaseApiError('bad', 422, 'bad-tag') } });
  const { result } = await run(input({ envs: [], sources: [scan('f1', 'A')] }), api);
  assert.equal(result.failures[0].step, 'tag');
  assert.ok(api.log.includes('startImports:f1'));
});

test('a Mongo tag the server refuses (not tested) is reported and the audit source is not added', async () => {
  const api = fakeApi({ always: { postSourceTag: new CaseApiError('no', 422, 'database-not-tested') } });
  const { result } = await run(input({ envs: [], sources: [mongo('orders', 'audit')] }), api);
  assert.equal(result.failures[0].reason, 'database-not-tested');
  assert.ok(!api.log.some((c) => c.startsWith('addAuditSource')));
});

test('the case cannot be made: nothing else is called, the reason is kept, and a second try can succeed', async () => {
  const api = fakeApi({ fail: { createCase: new CaseApiError('down', 500, 'unreachable') } });
  const state = sub.blankSubmitState();
  const inp = input({ sources: [scan('f1', 'A')] });
  const first = await run(inp, api, state);
  assert.deepEqual(api.log, ['createCase:Slowdown week 42']);
  assert.equal(first.result.caseId, '');
  assert.deepEqual(first.result.failures, [{ step: 'case', key: '', label: '', reason: 'unreachable' }]);
  const second = await run(inp, api, state);
  assert.equal(second.result.caseId, 'case1');
  assert.equal(second.result.imported, 1);
});

// ---- the master key locks in the middle

test('423 on a tag: the run stops, nothing is made twice after unlocking', async () => {
  const api = fakeApi({ fail: { postSourceTag: new CaseApiError('locked', 423, 'locked') } });
  const state = sub.blankSubmitState();
  const inp = input({ sources: [scan('f1', 'A'), scan('f2', 'B')] });
  const first = await run(inp, api, state);
  assert.equal(first.result.locked, true);
  assert.equal(first.result.finished, false);
  assert.equal(first.result.caseId, 'case1');
  assert.ok(!api.log.some((c) => c.startsWith('startImports')));
  assert.equal(sub.shouldOpenAnalysis(first.result), false);

  const second = await run(inp, api, state);
  assert.equal(second.result.locked, false);
  assert.equal(second.result.finished, true);
  assert.equal(api.log.filter((c) => c.startsWith('createCase')).length, 1, 'the case is not made again');
  assert.equal(api.log.filter((c) => c.startsWith('putEnvironments')).length, 1, 'the environments are not written again');
  assert.equal(api.log.filter((c) => c === 'postSourceTag:discover/A').length, 2, 'only the tag that did not go through is retried');
  assert.equal(api.log.filter((c) => c === 'postSourceTag:discover/B').length, 1);
  assert.equal(second.result.imported, 2);
});

test('423 inside an import: that import runs again after unlocking, finished ones do not', async () => {
  let lockOnce = true;
  const api = fakeApi();
  const original = api.watchJob;
  api.watchJob = async (id, ref, onProgress) => {
    if (ref.findingId === 'f2' && lockOnce) { lockOnce = false; api.log.push('watchJob:f2:locked'); return { kind: 'refused', reason: 'locked' }; }
    return original(id, ref, onProgress);
  };
  const state = sub.blankSubmitState();
  const inp = input({ envs: [], sources: [scan('f1', 'A'), scan('f2', 'B')] });
  const first = await run(inp, api, state);
  assert.equal(first.result.locked, true);
  assert.equal(state.imports['discover|f1'].status, 'done');
  assert.equal(state.imports['discover|f2'].status, 'waiting');
  const second = await run(inp, api, state);
  assert.equal(second.result.locked, false);
  assert.equal(second.result.imported, 2);
  assert.deepEqual(api.log.filter((c) => c.startsWith('startImports')), ['startImports:f1+f2', 'startImports:f2']);
});

test('423 on the case itself: no case exists yet, and the next call makes it once', async () => {
  const api = fakeApi({ fail: { createCase: new CaseApiError('locked', 423, 'locked') } });
  const state = sub.blankSubmitState();
  const inp = input({ envs: [], sources: [] });
  const first = await run(inp, api, state);
  assert.equal(first.result.locked, true);
  assert.equal(first.result.caseId, '');
  assert.deepEqual(first.result.failures, []);
  const second = await run(inp, api, state);
  assert.equal(second.result.caseId, 'case1');
  assert.equal(api.log.filter((c) => c.startsWith('createCase')).length, 2);
});

// ---- the plan helpers

test('importPlan and filesMissing count what the review will do', () => {
  const list = [scan('f1', 'A'), file('B', 'iis:b'), file('C', 'iis:c', 'app-log'), sources.makeSource('redis', 'r:1', 'R')];
  assert.deepEqual(sub.importPlan(list), { scan: 1, files: 2, tagOnly: 1 });
  assert.deepEqual(sub.filesMissing(list, { 'iis-logs|iis:b': FILE_A }).map((s) => s.label), ['C']);
});

test('reviewCounts counts distinct names, and a source with no portal is shared', () => {
  const list = [{ portal: 'Portal A', api: 'Orders' }, { portal: 'portal a', api: '' }, { portal: '', api: 'Orders' }, { portal: '', api: '' }];
  assert.deepEqual(sub.reviewCounts(['Production', 'Test'], list), { environments: 2, portals: 1, apis: 1, shared: 2 });
  assert.deepEqual(sub.reviewCounts([], []), { environments: 0, portals: 0, apis: 0, shared: 0 });
});

test('no call carries a password, a connection string or a path', async () => {
  const api = fakeApi();
  const calls = [];
  for (const name of ['createCase', 'putEnvironments', 'postSourceTag', 'testConnection', 'addAuditSource', 'startImports']) {
    const original = api[name];
    api[name] = async (...args) => { calls.push(JSON.stringify(args)); return original(...args); };
  }
  await run(input({ sources: [scan('f1', 'A'), mongo('orders', 'audit')] }), api);
  for (const call of calls) assert.doesNotMatch(call, /mongodb:\/\/|password|secret|[A-Z]:\\/i);
});

// ---- the screens

test('the review shows the gating notice and the tick box before the Create button', () => {
  const source = read('src/components/wizard/ReviewStep.svelte');
  assert.match(source, /GATING_BANNER/);
  assert.match(source, /ACK_LABEL/);
  assert.match(source, /type="checkbox"/);
  // The button is in the footer of the frame, drawn after the review snippet, so the notice is always above it.
  const landing = read('src/components/Landing.svelte');
  assert.match(landing, /createButtonText\(/);
  assert.match(landing, /disabled=\{createView\.running \|\| blockers\(\)\.length > 0\}/, 'the button waits for the notice');
  assert.ok(landing.indexOf('<ReviewStep') < landing.indexOf('createButtonText('), 'the review is drawn before the button');
  assert.match(read('src/lib/wizardCreate.svelte.ts'), /no-ack/);
  assert.match(source, /HelpNote id="wizard-review"/);
});

test('the progress screen announces progress and problems', () => {
  const source = read('src/components/wizard/CreateProgress.svelte');
  assert.match(source, /role="status"/);
  assert.match(source, /role="alert"/);
});

test('the real adapter holds no secret and the draft is the only input', () => {
  const source = read('src/lib/wizardSubmitApi.ts');
  assert.doesNotMatch(source, /localStorage|sessionStorage/);
});

// ---- the words

test('the summary line says all four counts, singular and plural', () => {
  assert.equal(copy.summaryText({ environments: 2, portals: 3, apis: 1, shared: 4 }), '2 environments, 3 portals, 1 API, 4 shared sources');
  assert.equal(copy.summaryText({ environments: 1, portals: 1, apis: 0, shared: 1 }), '1 environment, 1 portal, 0 APIs, 1 shared source');
});

test('a blank name reads "not named"', () => {
  assert.equal(copy.tagLine({ env: 'Production', portal: '', api: '' }), 'Production > not named > not named');
});

test('the create button changes for an empty case and while busy', () => {
  assert.equal(copy.createButtonText(0, false), 'Create empty case');
  assert.equal(copy.createButtonText(3, false), 'Create case');
  assert.equal(copy.createButtonText(3, true), 'Creating…');
});

test('every failure line names its source and never leaks a raw code', () => {
  const line = copy.failureText({ step: 'tag', key: 'k', label: 'W3SVC1', reason: 'bad-tag' });
  assert.match(line, /W3SVC1/);
  assert.doesNotMatch(line, /bad-tag/);
  assert.equal(copy.reasonLine('something-new'), 'The server could not finish it.');
});

test('the copy passes the copy lint', () => {
  const entries = copy.wizardCreateCopyEntries();
  assert.ok(entries.length > 30);
  const problems = lintScreenStrings(entries, deck.terms).map((f) => f.id + ': ' + f.message);
  assert.deepEqual(problems, []);
});
