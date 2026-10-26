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

const cases = loadTypeScript('src/lib/cases.ts');
const importCopy = loadTypeScript('src/lib/copy/importCopy.ts');

// Synthetic values only: random-looking ids, a made-up host and file.
const CASE_ID = 'abcd2345abcd2345abcd234567';
const JOB_ID = '0123456789abcdef0123456789abcdef';
const TOKEN = 'session-token-1';

const realFetch = globalThis.fetch;
function mockServer(handler) {
  const calls = [];
  globalThis.sessionStorage = { getItem: () => TOKEN, setItem() {}, removeItem() {} };
  globalThis.fetch = async (url, init = {}) => {
    const call = { url: String(url), method: init.method ?? 'GET', body: init.body, signal: init.signal };
    calls.push(call);
    const answer = await handler(call, calls);
    if (answer instanceof Error) throw answer;
    return new Response(answer.status === 204 ? null : JSON.stringify(answer.body ?? {}), { status: answer.status ?? 200, headers: { 'Content-Type': 'application/json' } });
  };
  return calls;
}
test.afterEach(() => { globalThis.fetch = realFetch; delete globalThis.sessionStorage; });

const synthetic = (name = 'web-a.log') => new File(['line one\nline two\n'], name, { type: 'text/plain' });
const recordJson = (extra = {}) => ({ id: 3, importer: 'iis-w3c', fileName: 'web-a.log', sha256: 'f'.repeat(64), bytes: 17, rows: 2, rejected: 0, hostLabel: 'web-a', tzRule: 'utc-field', quality: {}, knownGaps: [], imported: '2001-02-03T04:05:06Z', status: 'ok', supplied: {}, ...extra });
const job = (state, extra = {}) => ({ id: JOB_ID, state, bytesRead: 0, bytesTotal: 0, ...extra });
const noSleep = async () => {};

/* ------------------------------------------------------------------ the answer's shape */

test('a job answer is read into the same pieces a direct answer has', () => {
  const queued = cases.parseImportJob(job('queued'));
  assert.equal(queued.state, 'queued');
  assert.equal(queued.answer, null);
  assert.equal(queued.failure, null);

  const done = cases.parseImportJob(job('done', { status: 200, bytesRead: 17, bytesTotal: 17, result: { importer: 'iis-w3c', score: 0.9, tied: [], record: recordJson() } }));
  assert.equal(done.state, 'done');
  assert.equal(done.answer.importer, 'iis-w3c');
  assert.equal(done.answer.record.id, 3);
  assert.equal(done.answer.record.status, 'ok');
});

test('a failed job carries the same refusal a direct call gives: message, status, reason, column and import id', () => {
  const failed = cases.parseImportJob(job('failed', { status: 422, failure: { error: 'Refused.', reason: 'unscrubbed-column', column: 'sql_text' } }));
  assert.ok(failed.failure instanceof cases.CaseApiError);
  assert.equal(failed.failure.status, 422);
  assert.equal(failed.failure.reason, 'unscrubbed-column');
  assert.equal(failed.failure.column, 'sql_text');
  assert.equal(importCopy.errorText(failed.failure), importCopy.errorText({ status: 422, reason: 'unscrubbed-column', column: 'sql_text', message: '' }));

  const dup = cases.parseImportJob(job('failed', { status: 409, failure: { error: 'Already imported.', reason: 'duplicate', importId: 7 } }));
  assert.equal(dup.failure.importId, 7);
  assert.match(importCopy.errorText(dup.failure), /import #7/);

  const bare = cases.parseImportJob(job('failed'));
  assert.equal(bare.failure.status, 500, 'a failed job with no detail still fails');
});

test('an answer that does not fit the shape is refused, not shown', () => {
  for (const bad of [null, undefined, 'text', 7, [], {}, job('unknown'), { ...job('queued'), id: 'short' }, { ...job('queued'), id: JOB_ID.toUpperCase() }, job('done'), job('done', { result: 'x' })]) {
    assert.equal(cases.parseImportJob(bad), null, JSON.stringify(bad));
  }
  const odd = cases.parseImportJob(job('running', { bytesRead: -5, bytesTotal: 'many' }));
  assert.equal(odd.bytesRead, 0);
  assert.equal(odd.bytesTotal, 0);
});

test('progress is a whole percent, unknown until the size is known, and never above 100', () => {
  const pct = (bytesRead, bytesTotal) => cases.importProgressPercent({ bytesRead, bytesTotal });
  assert.equal(pct(0, 0), null);
  assert.equal(pct(5, 0), null);
  assert.equal(pct(0, 100), 0);
  assert.equal(pct(40, 100), 40);
  assert.equal(pct(399, 1000), 39);
  assert.equal(pct(100, 100), 100);
  assert.equal(pct(500, 100), 100);
  assert.equal(pct(-5, 100), 0);
});

test('the progress line says reading with a percent, and finishing at the last byte', () => {
  const text = importCopy.jobProgressText;
  assert.equal(text('queued', null), 'Waiting to start…');
  assert.equal(text('running', null), 'Reading file…');
  assert.equal(text('running', 40), 'Reading file… 40%');
  assert.equal(text('running', 99), 'Reading file… 99%');
  assert.equal(text('running', 100), 'Finishing up…');
  assert.doesNotMatch(text('running', 100), /100/);
});

/* ------------------------------------------------------------------ the requests */

test('a background preview sends async=1 and the file only; a commit sends the notice first', async () => {
  const calls = mockServer(() => ({ status: 202, body: job('queued') }));
  const first = await cases.startPreviewJob({ caseId: CASE_ID, file: synthetic(), importer: 'iis-w3c', host: 'web-a' });
  assert.equal(first.id, JOB_ID);
  const preview = new URL(calls[0].url, 'http://x');
  assert.equal(preview.pathname, '/api/cases/' + CASE_ID + '/imports');
  assert.equal(calls[0].method, 'POST');
  assert.equal(preview.searchParams.get('async'), '1');
  assert.equal(preview.searchParams.get('preview'), '1');
  assert.deepEqual([...calls[0].body.keys()], ['file']);

  await cases.startCommitJob({ caseId: CASE_ID, file: synthetic(), importer: 'iis-w3c', ackText: importCopy.ackTextFor() });
  const commit = new URL(calls[1].url, 'http://x');
  assert.equal(commit.searchParams.get('async'), '1');
  assert.equal(commit.searchParams.get('preview'), null);
  assert.deepEqual([...calls[1].body.keys()], ['ackGating', 'ackText', 'file'], 'the server wants the notice before the file');
});

test('the direct calls are unchanged: no async parameter', async () => {
  const calls = mockServer(() => ({ body: { importer: 'iis-w3c', score: 1, tied: [] } }));
  await cases.previewImport({ caseId: CASE_ID, file: synthetic(), importer: 'iis-w3c' });
  await cases.commitImport({ caseId: CASE_ID, file: synthetic(), importer: 'iis-w3c', ackText: 'x' });
  for (const call of calls) assert.equal(new URL(call.url, 'http://x').searchParams.get('async'), null);
});

test('a refused start throws the usual refusal; an unreadable 202 is refused', async () => {
  mockServer(() => ({ status: 429, body: { error: 'Busy.', reason: 'import-busy' } }));
  await assert.rejects(cases.startPreviewJob({ caseId: CASE_ID, file: synthetic() }), (e) => e.status === 429 && e.reason === 'import-busy');
  mockServer(() => ({ status: 202, body: { nope: true } }));
  await assert.rejects(cases.startPreviewJob({ caseId: CASE_ID, file: synthetic() }), (e) => e.reason === 'unreadable');
});

test('asking about and cancelling a job use the job path, and a bad id never reaches the network', async () => {
  const calls = mockServer((call) => (call.method === 'DELETE' ? { status: 204 } : { body: job('running', { bytesRead: 5, bytesTotal: 10 }) }));
  const got = await cases.fetchImportJob(CASE_ID, JOB_ID);
  assert.equal(got.bytesRead, 5);
  await cases.cancelImportJob(CASE_ID, JOB_ID);
  assert.deepEqual(calls.map((c) => [c.method, c.url]), [['GET', '/api/cases/' + CASE_ID + '/imports/jobs/' + JOB_ID], ['DELETE', '/api/cases/' + CASE_ID + '/imports/jobs/' + JOB_ID]]);
  await assert.rejects(cases.fetchImportJob(CASE_ID, '../x'), (e) => e.reason === 'bad-job-id');
  await assert.rejects(cases.cancelImportJob(CASE_ID, JOB_ID + '/x'), (e) => e.reason === 'bad-job-id');
  await assert.rejects(cases.fetchImportJob('nope', JOB_ID), (e) => e.reason === 'bad-case-id');
  assert.equal(calls.length, 2);
});

/* ------------------------------------------------------------------ waiting for a job */

test('waiting asks until the job is done, reports each answer, and returns the direct answer', async () => {
  const answers = [job('running', { bytesRead: 10, bytesTotal: 100 }), job('running', { bytesRead: 60, bytesTotal: 100 }), job('done', { status: 200, bytesRead: 100, bytesTotal: 100, result: { importer: 'iis-w3c', score: 1, tied: [], record: recordJson() } })];
  const calls = mockServer(() => ({ body: answers.shift() }));
  const seen = [];
  const sleeps = [];
  const answer = await cases.waitForImportJob(CASE_ID, cases.parseImportJob(job('queued')), { onUpdate: (j) => seen.push(j.state), sleep: async (ms) => { sleeps.push(ms); } });
  assert.equal(answer.record.status, 'ok');
  assert.deepEqual(seen, ['queued', 'running', 'running', 'done']);
  assert.equal(calls.length, 3);
  assert.deepEqual(sleeps, [1000, 1000, 1000], 'one second between answers');
});

test('a job that fails throws the refusal; a job already failed makes no request', async () => {
  const calls = mockServer(() => ({ body: job('failed', { status: 422, failure: { error: 'Refused.', reason: 'reject-fraction' } }) }));
  await assert.rejects(cases.waitForImportJob(CASE_ID, cases.parseImportJob(job('running')), { sleep: noSleep }), (e) => e instanceof cases.CaseApiError && e.reason === 'reject-fraction' && e.status === 422);
  assert.equal(calls.length, 1);
  await assert.rejects(cases.waitForImportJob(CASE_ID, cases.parseImportJob(job('failed', { status: 409, failure: { error: 'x', reason: 'case-closed' } })), { sleep: noSleep }), (e) => e.reason === 'case-closed');
  assert.equal(calls.length, 1);
});

test('a server that no longer knows the job ends the wait with job-not-found, in words', async () => {
  mockServer(() => ({ status: 404, body: { error: 'Gone.', reason: 'job-not-found' } }));
  await assert.rejects(cases.waitForImportJob(CASE_ID, cases.parseImportJob(job('running')), { sleep: noSleep }), (e) => {
    assert.equal(e.reason, 'job-not-found');
    assert.match(importCopy.errorText(e), /no longer has this import/);
    return true;
  });
});

test('a few lost answers in a row are tolerated, and the count starts again after one gets through', async () => {
  let n = 0;
  mockServer(() => {
    n++;
    // Four lost, one through, four lost, then done: never five in a row.
    if (n === 5) return { body: job('running') };
    if (n === 10) return { body: job('done', { status: 200, result: { importer: 'x', score: 1, tied: [], record: recordJson() } }) };
    return new TypeError('network down');
  });
  const answer = await cases.waitForImportJob(CASE_ID, cases.parseImportJob(job('running')), { sleep: noSleep });
  assert.equal(answer.record.status, 'ok');
  assert.equal(n, 10);
});

test('five lost answers in a row end the wait as unreachable', async () => {
  let n = 0;
  mockServer(() => { n++; return new TypeError('network down'); });
  await assert.rejects(cases.waitForImportJob(CASE_ID, cases.parseImportJob(job('running')), { sleep: noSleep }), (e) => e.reason === 'unreachable');
  assert.equal(n, cases.JOB_MAX_UNREACHABLE);
});

test('aborting stops the asking at once and sends nothing more, and the job is not cancelled', async () => {
  const calls = mockServer(() => ({ body: job('running') }));
  const controller = new AbortController();
  const waiting = cases.waitForImportJob(CASE_ID, cases.parseImportJob(job('running')), { signal: controller.signal });
  // The real wait is a second long; abort during it.
  await new Promise((resolve) => setTimeout(resolve, 20));
  controller.abort();
  await assert.rejects(waiting, (e) => e instanceof DOMException && e.name === 'AbortError');
  assert.equal(calls.length, 0, 'no request after the abort, and no DELETE');

  const early = new AbortController();
  early.abort();
  await assert.rejects(cases.waitForImportJob(CASE_ID, cases.parseImportJob(job('running')), { signal: early.signal }), (e) => e.name === 'AbortError');
  assert.equal(calls.length, 0);
});

/* ------------------------------------------------------------------ the screen */

const panel = read('src/lib/components/ImportPanel.svelte');
const appStateSource = read('src/lib/appState.svelte.ts');

test('the screen sends files as background jobs, polls through the client, and stops asking when it is closed', () => {
  assert.match(panel, /startPreviewJob\(/);
  assert.match(panel, /startCommitJob\(/);
  assert.match(panel, /waitForImportJob\(/);
  assert.doesNotMatch(panel, /\bpreviewImport\(|\bcommitImport\(/, 'the direct calls are no longer used here');
  assert.match(panel, /return \(\) => leaveJob\(\)/);
  assert.match(panel, /function leaveJob\(\) \{\s*controller\?\.abort\(\);/);
  assert.match(read('src/lib/cases.ts'), /export const JOB_POLL_MS = 1000;/);
});

test('a running import is kept in the app state, outside the drawer, and the drawer picks it up again', () => {
  assert.match(appStateSource, /export const importWork = \{\s*get running\(\) \{ return runningImport; \}, set running\(value\) \{ runningImport = value; \}/);
  assert.match(panel, /import \{ importWork(, setActiveCase)? \} from '..\/appState\.svelte'/);
  assert.match(panel, /let selectedId = \$state\(untrack\(\(\) => (takeCaseIntent\(\) \|\| )?importWork\.running\?\.caseId (\?\?|\|\|) ''\)\)/);
  assert.match(panel, /void resumeRunning\(id\)/);
  assert.match(panel, /importWork\.running = \{ caseId, jobId: first\.id \}/);
  // Closing the drawer only stops the asking: an aborted wait must not forget the job.
  assert.match(panel, /if \(isAbort\(error\)\) return;\s*forgetRunning\(first\.id\);/);
});

test('the screen shows the progress line and a cancel button, in the copy file', () => {
  assert.match(panel, /jobProgressText\(job\.state, percent\)/);
  assert.match(panel, /onclick=\{cancelJob\}/);
  assert.match(panel, /IMPORT_TEXT\.jobCancel\b/);
  for (const key of ['jobQueued', 'jobReading', 'jobFinishing', 'jobCancel', 'jobCancelling', 'jobCancelNote', 'jobResumed', 'jobCloseNote']) assert.ok(importCopy.IMPORT_TEXT[key], key);
  assert.ok(importCopy.REASON_TEXT['job-not-found']);
  assert.ok(importCopy.REASON_TEXT['bad-job-id']);
});

test('the job files hold no variable named state', () => {
  for (const source of [panel, read('src/lib/cases.ts')]) assert.doesNotMatch(source, /\b(?:let|const|var)\s+state\b/);
});
