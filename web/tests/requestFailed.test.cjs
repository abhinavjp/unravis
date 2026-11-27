const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const { loadTypeScript } = require('./helpers/loadTs.cjs');

// W-18: no "Request failed (500)" on screen. A failed request with no message from the server says one plain sentence; the status stays in the
// error log entry. The AI access panel adds the reason the server gave to its own "could not stop / resume" sentence, only when one was sent.
const SENTENCE = 'Something went wrong in Unravis. Try again. If it keeps happening, open Help and copy the error log.';
const JOB_ID = '0123456789abcdef0123456789abcdef';
const TOKEN = 'link-session-token-0123456789abcdef+/=';
const { REQUEST_FAILED_MESSAGE, isBareFailure, requestFailedLintEntries } = loadTypeScript('src/lib/copy/requestFailedCopy.ts');
const { MCP_TEXT } = loadTypeScript('src/lib/copy/mcpGrantsCopy.ts');
const read = (p) => fs.readFileSync(path.join(__dirname, '..', p), 'utf8');

// Loads a fresh api.ts with a recording error log, a tab that holds a session token, and a fetch that answers `respond`.
async function withApi(respond, fn) {
  const names = ['location', 'history', 'sessionStorage', 'fetch'];
  const saved = Object.fromEntries(names.map((name) => [name, Object.getOwnPropertyDescriptor(globalThis, name)]));
  const store = new Map([['unravis.session', TOKEN]]);
  const values = {
    location: { hash: '', search: '', pathname: '/' },
    history: { state: null, replaceState: () => {} },
    sessionStorage: { getItem: (k) => (store.has(k) ? store.get(k) : null), setItem: (k, v) => { store.set(k, String(v)); }, removeItem: (k) => { store.delete(k); } },
    fetch: respond
  };
  for (const name of names) Object.defineProperty(globalThis, name, { configurable: true, writable: true, value: values[name] });
  const logged = [];
  const log = { configureClientLog: () => {}, dropClientLog: () => {}, reportAction: () => {}, reportError: (entry) => { logged.push(entry); } };
  try {
    return await fn(loadTypeScript('src/lib/api.ts', { './clientLog': log }), logged);
  } finally {
    for (const name of names) {
      if (saved[name]) Object.defineProperty(globalThis, name, saved[name]);
      else delete globalThis[name];
    }
  }
}

test('the sentence is the approved one', () => {
  assert.equal(REQUEST_FAILED_MESSAGE, SENTENCE);
});

test('a 500 with an empty body says the plain sentence, and the error log entry carries 500', async () => {
  await withApi(async () => new Response('', { status: 500 }), async ({ api, ApiError }, logged) => {
    await assert.rejects(api.info(), (error) => {
      assert.ok(error instanceof ApiError);
      assert.equal(error.message, SENTENCE);
      assert.equal(error.status, 500);
      assert.doesNotMatch(error.message, /Request failed|\(500\)|\b500\b/);
      return true;
    });
    assert.equal(logged.length, 1);
    assert.equal(logged[0].status, 500, 'the status stays in the log entry');
    assert.equal(logged[0].message, SENTENCE);
  });
});

test('a body that is not JSON, and a 502, give the same sentence; the status is in the log entry', async () => {
  await withApi(async () => new Response('<html>Bad gateway</html>', { status: 502 }), async ({ api }, logged) => {
    await assert.rejects(api.info(), (error) => error.message === SENTENCE && error.status === 502);
    assert.equal(logged[0].status, 502);
  });
});

test('a message the server wrote is shown as it is', async () => {
  await withApi(async () => new Response(JSON.stringify({ error: 'The source is not set up.', code: 'no_source' }), { status: 500, headers: { 'Content-Type': 'application/json' } }), async ({ api }) => {
    await assert.rejects(api.info(), (error) => error.message === 'The source is not set up.' && error.code === 'no_source');
  });
});

// Loads a fresh cases.ts with a recording error log, a tab that holds a session token, and a fetch that answers `respond`.
async function withCases(respond, fn) {
  const names = ['location', 'history', 'sessionStorage', 'fetch'];
  const saved = Object.fromEntries(names.map((name) => [name, Object.getOwnPropertyDescriptor(globalThis, name)]));
  const store = new Map([['unravis.session', TOKEN]]);
  const values = {
    location: { hash: '', search: '', pathname: '/' },
    history: { state: null, replaceState: () => {} },
    sessionStorage: { getItem: (k) => (store.has(k) ? store.get(k) : null), setItem: (k, v) => { store.set(k, String(v)); }, removeItem: (k) => { store.delete(k); } },
    fetch: respond
  };
  for (const name of names) Object.defineProperty(globalThis, name, { configurable: true, writable: true, value: values[name] });
  const logged = [];
  const log = { configureClientLog: () => {}, dropClientLog: () => {}, reportAction: () => {}, reportError: (entry) => { logged.push(entry); } };
  try {
    const api = loadTypeScript('src/lib/api.ts', { './clientLog': log });
    return await fn(loadTypeScript('src/lib/cases.ts', { './clientLog': log, './api': api }), logged);
  } finally {
    for (const name of names) {
      if (saved[name]) Object.defineProperty(globalThis, name, saved[name]);
      else delete globalThis[name];
    }
  }
}

test('a case route refused with an empty or non-JSON 500 says the plain sentence, and the error log entry carries 500', async () => {
  for (const body of ['', '<html>Bad gateway</html>']) {
    await withCases(async () => new Response(body, { status: 500, headers: { 'X-Request-Id': 'req-abc' } }), async (cases, logged) => {
      const error = await cases.listHosts().then(() => null, (e) => e);
      assert.equal(error.message, SENTENCE);
      assert.equal(error.status, 500);
      assert.equal(logged.length, 1, 'the status is written to the log');
      assert.equal(logged[0].status, 500);
      assert.equal(logged[0].message, SENTENCE);
      assert.equal(logged[0].reqId, 'req-abc');
    });
  }
});

test('a case route refused with a blank error string says the plain sentence and logs the status', async () => {
  for (const blank of ['', '   ']) {
    await withCases(async () => new Response(JSON.stringify({ error: blank, reason: 'oops' }), { status: 500, headers: { 'Content-Type': 'application/json' } }), async (cases, logged) => {
      const error = await cases.listHosts().then(() => null, (e) => e);
      assert.equal(error.message, SENTENCE);
      assert.equal(error.reason, 'oops');
      assert.equal(logged.length, 1);
      assert.equal(logged[0].status, 500);
      assert.equal(logged[0].code, 'oops');
    });
  }
});

test('a case route refused with a message the server wrote shows it, and writes nothing extra to the log', async () => {
  await withCases(async () => new Response(JSON.stringify({ error: 'That case is archived.', reason: 'case-archived' }), { status: 409, headers: { 'Content-Type': 'application/json' } }), async (cases, logged) => {
    const error = await cases.listHosts().then(() => null, (e) => e);
    assert.equal(error.message, 'That case is archived.');
    assert.equal(logged.length, 0);
  });
});

test('a failed import job with no error text says the plain sentence and logs the status', async () => {
  await withCases(async () => new Response('{}'), async (cases, logged) => {
    const job = cases.parseImportJob({ id: JOB_ID, state: 'failed', status: 422, failure: { reason: 'bad-file' } });
    assert.ok(job, 'a failed job still parses');
    assert.equal(job.failure.message, SENTENCE);
    assert.equal(job.failure.status, 422);
    assert.equal(job.failure.reason, 'bad-file');
    assert.equal(logged.length, 1);
    assert.equal(logged[0].status, 422);
    assert.equal(logged[0].code, 'bad-file');
    const bare = cases.parseImportJob({ id: JOB_ID, state: 'failed', failure: { error: '  ' } });
    assert.equal(bare.failure.message, SENTENCE);
    assert.equal(bare.failure.status, 500);
    assert.equal(logged[1].status, 500);
    const worded = cases.parseImportJob({ id: JOB_ID, state: 'failed', failure: { error: 'The file is empty.' } });
    assert.equal(worded.failure.message, 'The file is empty.');
    assert.equal(logged.length, 2, 'a message the server wrote is not logged again');
  });
});

test('the wait loops fall back to the plain sentence, and log the status, when a failed job carries no error', async () => {
  await withCases(async () => new Response('{}'), async (cases, logged) => {
    const failed = (kind) => ({ id: JOB_ID, state: 'failed', kind, bytesRead: 0, bytesTotal: 0, answer: null, checkReport: null, failure: null });
    for (const [wait, kind] of [[cases.waitForImportJob, 'import'], [cases.waitForCheckJob, 'check']]) {
      logged.length = 0;
      const error = await wait('case0123456789abcdef', failed(kind)).then(() => null, (e) => e);
      assert.equal(error.message, SENTENCE, kind);
      assert.equal(error.status, 500);
      assert.equal(logged.length, 1, kind);
      assert.equal(logged[0].status, 500);
    }
  });
});

test('a blank error string from the server does not hide the plain sentence (shared layer and export download)', async () => {
  const blank = async () => new Response(JSON.stringify({ error: '' }), { status: 500, headers: { 'Content-Type': 'application/json' } });
  await withApi(blank, async ({ api }, logged) => {
    await assert.rejects(api.info(), (error) => error.message === SENTENCE);
    const query = { sourceIds: [], start: '', end: '' };
    await assert.rejects(api.export(query, 'key'), (error) => error.message === SENTENCE);
    assert.equal(logged.length, 2);
    assert.equal(logged[1].status, 500);
    assert.equal(logged[1].message, SENTENCE);
  });
});

test('the export download falls back to the same plain sentence, not "Export failed (N)"', () => {
  assert.doesNotMatch(read('src/lib/api.ts'), /Export failed \(/);
});

test('no screen text is built from "Request failed (N)" any more', () => {
  const walk = (dir) => fs.readdirSync(dir, { withFileTypes: true }).flatMap((e) => (e.isDirectory() ? walk(path.join(dir, e.name)) : [path.join(dir, e.name)]));
  const offenders = walk(path.join(__dirname, '..', 'src')).filter((f) => /\.(ts|svelte)$/.test(f)).filter((f) => /Request failed \(|'Request failed/.test(fs.readFileSync(f, 'utf8')));
  // The one place that still names it is the recogniser for text from an older run (a regular expression, not a message).
  assert.deepEqual(offenders.map((f) => path.basename(f)), ['requestFailedCopy.ts']);
});

test('the export drawer treats the new sentence like the old bare status: the status picks a more exact sentence', () => {
  const exp = loadTypeScript('src/lib/export.ts');
  const err = (message, status) => Object.assign(new Error(message), { status });
  assert.equal(exp.errorText(err(SENTENCE, 503), 'preview'), exp.EXPORT_TEXT.unavailable);
  assert.equal(exp.errorText(err(SENTENCE, 404), 'save'), exp.EXPORT_TEXT.goneSave);
  assert.equal(exp.errorText(err(SENTENCE, 500), 'save'), exp.EXPORT_TEXT.failedSave);
  assert.equal(exp.errorText(err('Request failed (503)', 503), 'preview'), exp.EXPORT_TEXT.unavailable);
  assert.equal(exp.errorText(err('Export is turned off for this build.', 503), 'preview'), 'Export is turned off for this build.');
});

test('isBareFailure knows the sentence and the old bare status, and nothing a server wrote', () => {
  assert.equal(isBareFailure(SENTENCE), true);
  assert.equal(isBareFailure(' Request failed (500) '), true);
  assert.equal(isBareFailure('The source is not set up.'), false);
  assert.equal(isBareFailure(''), false);
});

function fakeMcp(status, body) {
  globalThis.sessionStorage = { getItem: () => TOKEN, setItem: () => {}, removeItem: () => {} };
  globalThis.fetch = async () => new Response(body === undefined ? '' : JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
}

test('AI access: a stop refused with a reason shows the panel sentence, then the reason', async () => {
  const mcp = loadTypeScript('src/lib/mcpGrantsApi.ts');
  fakeMcp(500, { error: 'The service did not answer.' });
  const error = await mcp.mcpApi.stop().then(() => null, (e) => e);
  assert.equal(error.fromServer, true);
  assert.equal(mcp.sentenceWithReason(MCP_TEXT.stopFailed, error), MCP_TEXT.stopFailed + ' The service did not answer.');
  const again = await mcp.mcpApi.resume().then(() => null, (e) => e);
  assert.equal(mcp.sentenceWithReason(MCP_TEXT.resumeFailed, again), MCP_TEXT.resumeFailed + ' The service did not answer.');
});

test('AI access: a stop refused with no message shows only the panel sentence, never a status number', async () => {
  const mcp = loadTypeScript('src/lib/mcpGrantsApi.ts');
  fakeMcp(500, undefined);
  const error = await mcp.mcpApi.stop().then(() => null, (e) => e);
  assert.equal(error.fromServer, false);
  assert.equal(error.message, SENTENCE, 'no "The request was refused (500)."');
  assert.equal(mcp.sentenceWithReason(MCP_TEXT.stopFailed, error), MCP_TEXT.stopFailed);
  assert.equal(mcp.sentenceWithReason(MCP_TEXT.resumeFailed, new Error('plain')), MCP_TEXT.resumeFailed);
  assert.equal(mcp.sentenceWithReason(MCP_TEXT.resumeFailed, undefined), MCP_TEXT.resumeFailed);
});

test('the AI access panel builds both failure sentences through sentenceWithReason', () => {
  const panel = read('src/components/McpAccessPanel.svelte');
  assert.match(panel, /stopError = sentenceWithReason\(T\.stopFailed, error\)/);
  assert.match(panel, /stopError = sentenceWithReason\(T\.resumeFailed, error\)/);
});

test('the sentence passes the copy lint and reads at or below grade 8.5', () => {
  const { lintCopy, fleschKincaidGrade, MAX_GRADE } = loadTypeScript('src/lib/copy/copylint.ts');
  const { deck } = loadTypeScript('src/lib/copy/deck.ts');
  const entries = requestFailedLintEntries();
  assert.deepEqual(lintCopy(entries, deck.terms), []);
  for (const entry of entries) assert.ok(fleschKincaidGrade(entry.text) <= MAX_GRADE, entry.id + ' ' + fleschKincaidGrade(entry.text));
});
