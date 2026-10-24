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
const { deck } = loadTypeScript('src/lib/copy/deck.ts');
const { lintCopy } = loadTypeScript('src/lib/copy/copylint.ts');

// All values are synthetic: case ids are random-looking base32, hosts are web-a, files are web-a.log.
const CASE_ID = 'abcd2345abcd2345abcd234567';
const OTHER_ID = 'zzzz2222zzzz2222zzzz222233';
const TOKEN = 'session-token-1';
const BANNER = 'Parsers are built and tested on synthetic files only. Importing real client exports needs: R2 preservation, R8 content review, R11 AI/MCP approval, R12 DBA dry run (SQL files), R13 Mongo access, R16 retention.';
const ERASE_SENTENCE = 'Case data deleted and its encryption key destroyed. Remaining fragments on disk are unreadable.';

/* ------------------------------------------------------------------ a mocked server for the API contract */

const realFetch = globalThis.fetch;
function mockServer(handler) {
  const calls = [];
  globalThis.sessionStorage = { getItem: () => TOKEN, setItem() {}, removeItem() {} };
  globalThis.fetch = async (url, init = {}) => {
    const headers = init.headers instanceof Headers ? init.headers : new Headers(init.headers);
    const call = { url: String(url), method: init.method ?? 'GET', headers, body: init.body };
    calls.push(call);
    const answer = await handler(call);
    if (answer instanceof Response) return answer;
    return new Response(answer.status === 204 ? null : JSON.stringify(answer.body ?? {}), { status: answer.status ?? 200, headers: { 'Content-Type': 'application/json' } });
  };
  return calls;
}
test.afterEach(() => { globalThis.fetch = realFetch; delete globalThis.sessionStorage; });

const synthetic = (name = 'web-a.log', text = 'line one\nline two\n') => new File([text], name, { type: 'text/plain', lastModified: 1000000000000 });
const caseJson = (extra = {}) => ({ id: CASE_ID, title: 'Slow week', created: '2001-02-03T04:05:06Z', closed: null, bytes: 4096, imports: 1, ...extra });
const reportJson = (extra = {}) => ({
  importer: 'iis-w3c', rows: 120, emitted: 118, rejected: 2, errors: [{ line: 4, reason: 'field-count' }, { line: 9, reason: 'field-count' }, { line: 11, reason: 'bad-time' }],
  observedHosts: [], first: '2001-02-03T04:00:00Z', last: '2001-02-03T05:00:00Z', quality: { timezoneRule: 'utc-field' }, fieldRules: { time: 'utc-field' },
  knownGaps: ['W3C time semantics unverified'], ...extra
});

/* ------------------------------------------------------------------ the notice, the acknowledgement and the erase sentence */

test('the gating notice is the card text, word for word', () => {
  assert.equal(importCopy.GATING_BANNER, BANNER);
  assert.equal(importCopy.ackTextFor(), BANNER, 'the exact notice that was acknowledged is what is sent as ackText');
});

test('the erase sentence is the storage decision sentence, and nothing says "securely wiped"', () => {
  assert.equal(importCopy.ERASE_DONE, ERASE_SENTENCE);
  const everything = JSON.stringify([importCopy.IMPORT_TEXT, importCopy.REASON_TEXT]) + read('src/lib/components/ImportPanel.svelte') + read('src/lib/components/CasePanel.svelte');
  assert.doesNotMatch(everything, /securely wiped/i);
});

/* ------------------------------------------------------------------ copy lint */

test('all import copy passes the copy lint (reading grade 8.5 or lower, no cause words)', () => {
  const entries = importCopy.importCopyEntries();
  assert.ok(entries.length > 80, 'the lint list covers the fixed text and the built sentences');
  assert.deepEqual(lintCopy(entries, deck.terms), []);
});

test('every refusal reason in the plan has plain words, and the lint list holds them', () => {
  const planned = ['field-count', 'bad-time', 'tz-unlabelled', 'dst-ambiguous', 'dst-nonexistent', 'bad-number', 'line-too-long', 'encoding', 'unknown-host', 'unscrubbed-column', 'unsupported-format', 'archive-extract-first', 'tz-mismatch', 'reject-fraction'];
  const ids = new Set(importCopy.importCopyEntries().map((e) => e.id));
  for (const reason of planned) {
    assert.ok(importCopy.reasonText(reason), reason + ' has text');
    assert.ok(ids.has('import.reason.' + reason), reason + ' is linted');
  }
  assert.equal(importCopy.reasonText('something-new'), null);
  assert.equal(importCopy.reasonText('toString'), null, 'inherited names are not reasons');
});

test('import copy names no cause and does not call imported rows proof', () => {
  const text = importCopy.importCopyEntries().map((e) => e.text).join('\n');
  assert.doesNotMatch(text, /\b(?:caused|because|due to|proven|strong correlation)\b/i);
  assert.match(importCopy.IMPORT_TEXT.readOnlyNote, /never proof of a cause/);
});

test('errorText names a duplicate import, a refused column header, and shows a missing session message', () => {
  assert.equal(importCopy.errorText({ status: 409, reason: 'duplicate', importId: 7, message: 'x' }), 'This file was already imported (import #7).');
  assert.match(importCopy.errorText({ status: 422, reason: 'unscrubbed-column', column: 'sql_text', message: '' }), /Column: sql_text\.$/);
  assert.equal(importCopy.errorText({ status: 401, reason: 'no_session', message: 'Open Unravis from the link it printed when it started, or restart it.' }), 'Open Unravis from the link it printed when it started, or restart it.');
  assert.match(importCopy.errorText({ status: 500, reason: '', message: 'SENTINEL-7f3a leaked' }), /^The server refused this request/, 'the server message is never echoed');
  assert.doesNotMatch(importCopy.errorText({ status: 500, reason: '', message: 'SENTINEL-7f3a leaked' }), /SENTINEL/);
  assert.match(importCopy.errorText({ status: 422, reason: 'unknown-host', message: '' }), /unknown host/);
});

test('small text helpers', () => {
  assert.equal(importCopy.formatBytes(0), '0 B');
  assert.equal(importCopy.formatBytes(1536), '1.5 KB');
  assert.equal(importCopy.formatBytes(64 * 1024 * 1024), '64 MB');
  assert.equal(importCopy.importerLabel('iis-w3c'), 'IIS web log');
  assert.equal(importCopy.importerLabel('brand-new'), 'brand-new');
  assert.equal(importCopy.caseStats({ imports: 1, bytes: 2048 }), '1 import, 2 KB');
  assert.equal(importCopy.caseStats({ imports: 3, bytes: 0 }), '3 imports, 0 B');
  assert.equal(importCopy.coverageText({ first: null, last: null }), importCopy.IMPORT_TEXT.noTimeCovered);
  assert.match(importCopy.coverageText({ first: '2001-02-03T04:00:00Z', last: '2001-02-03T05:00:00Z' }), /3 Feb 2001 04:00:00.05:00:00 UTC/);
  assert.deepEqual(importCopy.qualityDetails({ sourceVersion: 'iis-10', sampling: '' }), [{ label: 'Source version', value: 'iis-10' }]);
  assert.equal(importCopy.statusLabel('quarantined'), 'Refused');
  assert.equal(importCopy.statusLabel('odd'), 'odd');
});

/* ------------------------------------------------------------------ the API contract, mocked */

test('case routes: list, create, close, erase send the contract and the session token', async () => {
  const calls = mockServer((call) => {
    if (call.method === 'GET' && call.url === '/api/cases') return { body: { cases: [caseJson(), { id: 'not-an-id', title: 'x' }] } };
    if (call.method === 'POST' && call.url === '/api/cases') return { status: 201, body: caseJson({ title: JSON.parse(call.body).title }) };
    if (call.method === 'POST' && call.url === '/api/cases/' + CASE_ID + '/close') return { body: caseJson({ closed: '2001-02-04T00:00:00Z' }) };
    if (call.method === 'DELETE' && call.url === '/api/cases/' + CASE_ID) return { status: 204 };
    return { status: 404, body: { error: 'nope', reason: 'not-found' } };
  });
  const list = await cases.listCases();
  assert.equal(list.length, 1, 'an id that is not 26 base32 characters is dropped');
  assert.equal(list[0].title, 'Slow week');
  const made = await cases.createCase('  Week two  ');
  assert.equal(made.title, 'Week two');
  const closed = await cases.closeCase(CASE_ID);
  assert.equal(closed.closed, '2001-02-04T00:00:00Z');
  await cases.eraseCase(CASE_ID);
  assert.deepEqual(calls.map((c) => c.method + ' ' + c.url), ['GET /api/cases', 'POST /api/cases', 'POST /api/cases/' + CASE_ID + '/close', 'DELETE /api/cases/' + CASE_ID]);
  for (const call of calls) assert.equal(call.headers.get('X-Audit-Token'), TOKEN);
  assert.deepEqual(JSON.parse(calls[1].body), { title: 'Week two' });
  assert.deepEqual(JSON.parse(calls[3].body), { confirm: CASE_ID }, 'erase echoes the id as the server confirmation');
  assert.equal(calls[3].headers.get('Content-Type'), 'application/json');
});

test('a bad case id never reaches the network', async () => {
  const calls = mockServer(() => ({ body: {} }));
  for (const bad of ['', '../..', 'ABCD2345ABCD2345ABCD234567', CASE_ID + '/x', CASE_ID.slice(1)]) {
    await assert.rejects(cases.listImports(bad), (e) => e.reason === 'bad-case-id');
    await assert.rejects(cases.eraseCase(bad), (e) => e.reason === 'bad-case-id');
  }
  assert.equal(calls.length, 0);
});

test('no session token means no request', async () => {
  const calls = mockServer(() => ({ body: {} }));
  globalThis.sessionStorage = { getItem: () => '', setItem() {}, removeItem() {} };
  await assert.rejects(cases.listCases(), (e) => e.status === 401 && e.reason === 'no_session');
  assert.equal(calls.length, 0);
});

test('hosts: labels only, sorted', async () => {
  mockServer(() => ({ body: { version: 1, hosts: [{ label: 'web-b', aliases: ['WEB-B'], tz: 'UTC' }, { label: 'sql-a' }, { label: 'web-a' }] } }));
  assert.deepEqual(await cases.listHosts(), ['sql-a', 'web-a', 'web-b']);
});

test('preview: multipart file, preview=1, importer and host in the query, no acknowledgement, file name never in the URL', async () => {
  const calls = mockServer(() => ({ body: { importer: 'iis-w3c', score: 90, tied: [], report: reportJson() } }));
  const answer = await cases.previewImport({ caseId: CASE_ID, file: synthetic('web-a.log'), host: 'web-a' });
  const call = calls[0];
  assert.equal(call.method, 'POST');
  assert.equal(call.url, '/api/cases/' + CASE_ID + '/imports?host=web-a&preview=1');
  assert.doesNotMatch(call.url, /web-a\.log/);
  assert.ok(call.body instanceof FormData);
  assert.equal(call.body.get('file').name, 'web-a.log');
  assert.equal(call.body.has('ackGating'), false);
  assert.equal(call.body.has('ackText'), false);
  assert.equal(call.headers.get('Content-Type'), null, 'the browser sets the multipart boundary');
  assert.equal(call.headers.get('X-Audit-Token'), TOKEN);
  assert.equal(answer.importer, 'iis-w3c');
  assert.equal(answer.report.emitted, 118);
  assert.equal(answer.record, null);
});

test('preview with an importer picked sends it; a tie comes back as names and no report', async () => {
  const calls = mockServer((call) => (call.url.includes('importer=')
    ? { body: { importer: 'sql-pdh', score: 80, tied: [], report: reportJson({ importer: 'sql-pdh' }) } }
    : { body: { importer: '', score: 70, tied: ['wincounters', 'sql-pdh'] } }));
  const tie = await cases.previewImport({ caseId: CASE_ID, file: synthetic() });
  assert.equal(calls[0].url, '/api/cases/' + CASE_ID + '/imports?preview=1');
  assert.deepEqual(tie.tied, ['wincounters', 'sql-pdh']);
  assert.equal(tie.report, null);
  const picked = await cases.previewImport({ caseId: CASE_ID, file: synthetic(), importer: 'sql-pdh' });
  assert.equal(calls[1].url, '/api/cases/' + CASE_ID + '/imports?importer=sql-pdh&preview=1');
  assert.equal(picked.report.importer, 'sql-pdh');
});

test('commit sends ackGating=true and the exact notice as ackText, with no preview flag', async () => {
  const record = { id: 3, importer: 'iis-w3c', fileName: 'web-a.log', sha256: 'f'.repeat(64), bytes: 20, rows: 118, rejected: 2, hostLabel: 'web-a', tzRule: 'utc-field', quality: {}, knownGaps: [], imported: '2001-02-03T06:00:00Z', status: 'ok' };
  const calls = mockServer(() => ({ status: 201, body: { importer: 'iis-w3c', score: 90, tied: [], record, report: reportJson() } }));
  const answer = await cases.commitImport({ caseId: CASE_ID, file: synthetic(), importer: 'iis-w3c', host: 'web-a', ackText: importCopy.ackTextFor() });
  const call = calls[0];
  assert.equal(call.url, '/api/cases/' + CASE_ID + '/imports?importer=iis-w3c&host=web-a');
  assert.equal(call.body.get('ackGating'), 'true');
  assert.equal(call.body.get('ackText'), BANNER);
  assert.equal(answer.record.id, 3);
  assert.equal(importCopy.importedText(answer.record), 'Imported 118 rows from web-a.log. 2 rejected. This is import #3.');
});

test('refusals carry the reason, a refused column header and the earlier import number', async () => {
  mockServer((call) => {
    if (call.url.includes('importer=a')) return { status: 422, body: { error: 'refused', reason: 'unknown-host' } };
    if (call.url.includes('importer=b')) return { status: 422, body: { error: 'refused', reason: 'unscrubbed-column', column: 'sql_text' } };
    if (call.url.includes('importer=c')) return { status: 409, body: { error: 'dup', reason: 'duplicate', importId: 5 } };
    if (call.url.includes('importer=d')) return new Response('<html>', { status: 502 });
    return { status: 200, body: { importer: 'x' } };
  });
  const run = (importer) => cases.previewImport({ caseId: CASE_ID, file: synthetic(), importer });
  await assert.rejects(run('a'), (e) => e.reason === 'unknown-host' && importCopy.isUnknownHost(e));
  await assert.rejects(run('b'), (e) => e.reason === 'unscrubbed-column' && e.column === 'sql_text');
  await assert.rejects(run('c'), (e) => e.reason === 'duplicate' && e.importId === 5 && importCopy.errorText(e) === 'This file was already imported (import #5).');
  await assert.rejects(run('d'), (e) => e.status === 502 && e.reason === '');
});

test('an unreachable server becomes a refusal with a plain reason, and an abort passes through', async () => {
  globalThis.sessionStorage = { getItem: () => TOKEN, setItem() {}, removeItem() {} };
  globalThis.fetch = async () => { throw new TypeError('network down'); };
  await assert.rejects(cases.listCases(), (e) => e.reason === 'unreachable' && importCopy.errorText(e) === importCopy.REASON_TEXT.unreachable);
  globalThis.fetch = async () => { throw new DOMException('aborted', 'AbortError'); };
  await assert.rejects(cases.listCases(), (e) => e.name === 'AbortError');
});

test('a thin server answer is normalised, never trusted', async () => {
  mockServer(() => ({ body: { importer: 'iis-w3c', report: { rows: 'x', errors: [{ line: 1 }, null], knownGaps: [1, 'ok'] } } }));
  const answer = await cases.previewImport({ caseId: CASE_ID, file: synthetic() });
  assert.deepEqual(answer.tied, []);
  assert.equal(answer.report.rows, 0);
  assert.deepEqual(answer.report.knownGaps, ['ok']);
  assert.deepEqual(answer.report.errors.map((e) => e.reason), ['', '']);
  assert.deepEqual(answer.report.observedHosts, []);
});

/* ------------------------------------------------------------------ rules the screen follows */

test('erase needs the case code typed', () => {
  assert.equal(cases.caseCode(CASE_ID), 'abcd2345');
  assert.equal(cases.eraseConfirmed('abcd2345', CASE_ID), true);
  assert.equal(cases.eraseConfirmed('  ABCD2345 ', CASE_ID), true);
  assert.equal(cases.eraseConfirmed('', CASE_ID), false);
  assert.equal(cases.eraseConfirmed('abcd234', CASE_ID), false);
  assert.equal(cases.eraseConfirmed('erase', CASE_ID), false);
  assert.equal(cases.eraseConfirmed('abcd2345', 'abcd2345'), false, 'not a real case id');
  assert.equal(cases.eraseConfirmed(OTHER_ID.slice(0, 8), CASE_ID), false);
});

test('size is checked against the importer cap, or the largest cap before the type is known', () => {
  const MiB = 1024 * 1024;
  assert.equal(cases.fileSizeProblem(1000, 'iis-w3c'), null);
  assert.deepEqual(cases.fileSizeProblem(0), { kind: 'empty' });
  assert.deepEqual(cases.fileSizeProblem(17 * MiB, 'schedule'), { kind: 'too-big', cap: 16 * MiB });
  assert.equal(cases.fileSizeProblem(17 * MiB), null, 'no importer yet: only the largest cap applies');
  assert.deepEqual(cases.fileSizeProblem(2 * 1024 * MiB + 1), { kind: 'too-big', cap: 2 * 1024 * MiB });
  assert.equal(cases.capFor('hosts-json-not-an-importer'), 2 * 1024 * MiB);
  assert.equal(cases.capFor('mongod-json'), 2 * 1024 * MiB);
  assert.equal(cases.capFor('iis-httperr'), 64 * MiB);
  assert.match(importCopy.sizeProblemText({ kind: 'too-big', cap: 16 * MiB }, 17 * MiB), /This file is 17 MB\. The limit for this file type is 16 MB\./);
});

test('commit stays blocked until a current preview, no unknown host, rows to keep, and the box is ticked', () => {
  const report = JSON.parse(JSON.stringify(reportJson()));
  const ready = { caseOpen: true, previewedKey: 'k', currentKey: 'k', importer: 'iis-w3c', report, unknownHost: false, acknowledged: true, busy: false };
  assert.equal(cases.commitBlock(ready), null);
  assert.equal(cases.commitBlock({ ...ready, acknowledged: false }), 'ack');
  assert.equal(cases.commitBlock({ ...ready, caseOpen: false }), 'closed');
  assert.equal(cases.commitBlock({ ...ready, busy: true }), 'busy');
  assert.equal(cases.commitBlock({ ...ready, report: null }), 'no-preview');
  assert.equal(cases.commitBlock({ ...ready, importer: '' }), 'no-preview');
  assert.equal(cases.commitBlock({ ...ready, currentKey: 'other' }), 'stale');
  assert.equal(cases.commitBlock({ ...ready, report: { ...report, emitted: 0 } }), 'nothing');
  // An unknown host blocks commit even when everything else is ready, and even with no report.
  assert.equal(cases.commitBlock({ ...ready, unknownHost: true }), 'unknown-host');
  assert.equal(cases.commitBlock({ ...ready, unknownHost: true, report: null }), 'unknown-host');
  for (const code of ['closed', 'busy', 'no-preview', 'stale', 'unknown-host', 'nothing', 'ack']) assert.ok(importCopy.IMPORT_TEXT.blocked[code], code + ' has text');
});

test('a preview holds only for the same file, importer and host', () => {
  const f = { name: 'web-a.log', size: 20, lastModified: 1 };
  assert.equal(cases.previewKey(null, 'x', ''), '');
  assert.equal(cases.previewKey(f, 'iis-w3c', 'web-a'), cases.previewKey({ ...f }, 'iis-w3c', 'web-a'));
  assert.notEqual(cases.previewKey(f, 'iis-w3c', 'web-a'), cases.previewKey(f, 'iis-w3c', 'web-b'));
  assert.notEqual(cases.previewKey(f, 'iis-w3c', 'web-a'), cases.previewKey(f, 'iis-httperr', 'web-a'));
  assert.notEqual(cases.previewKey(f, 'iis-w3c', 'web-a'), cases.previewKey({ ...f, size: 21 }, 'iis-w3c', 'web-a'));
});

test('reason counts are most common first', () => {
  assert.deepEqual(cases.reasonCounts(reportJson().errors), [{ reason: 'field-count', count: 2 }, { reason: 'bad-time', count: 1 }]);
  assert.deepEqual(cases.reasonCounts([]), []);
});

/* ------------------------------------------------------------------ the components, read as source */

const importPanel = read('src/lib/components/ImportPanel.svelte');
const casePanel = read('src/lib/components/CasePanel.svelte');

test('the notice is always rendered, outside every condition', () => {
  const markup = importPanel.slice(importPanel.indexOf('<Drawer'));
  const notice = markup.indexOf('GATING_BANNER');
  assert.ok(notice > 0, 'the panel renders the banner');
  const before = markup.slice(0, notice);
  const opens = (before.match(/\{#(?:if|each)\b/g) ?? []).length;
  const closes = (before.match(/\{\/(?:if|each)\}/g) ?? []).length;
  assert.equal(opens, closes, 'no {#if} or {#each} block is open where the banner is rendered');
  assert.ok(notice < markup.indexOf('<CasePanel'), 'it comes before the case list');
});

test('the Import button is off while anything blocks it, and the checkbox text is the acknowledgement that is sent', () => {
  assert.match(importPanel, /disabled=\{block !== null\}/);
  assert.match(importPanel, /bind:checked=\{acknowledged\}/);
  assert.match(importPanel, /\{ACK_LABEL\}/);
  assert.match(importPanel, /ackText: ackTextFor\(\)/);
  assert.match(importPanel, /if \(block !== null \|\| !file\) return;/, 'doImport re-checks the block');
  assert.match(importPanel, /commitBlock\(/);
});

test('an unknown host shows "unknown host" and the screen feeds it into the commit block', () => {
  assert.match(importPanel, /isUnknownHost\(error\)\) unknownHost = true/);
  assert.match(importPanel, /unknownHostBadge/);
  assert.equal(importCopy.IMPORT_TEXT.unknownHostBadge, 'unknown host');
});

test('erase is typed: the confirm button needs eraseConfirmed, and the form cannot submit without it', () => {
  assert.match(casePanel, /eraseConfirmed\(typed, eraseId\)/);
  assert.match(casePanel, /disabled=\{busy \|\| !confirmed\}/);
  assert.match(casePanel, /if \(busy \|\| !confirmed\) return;/);
  assert.match(casePanel, /\{ERASE_DONE\}/);
});

test('the components only talk to the server through cases.ts, and never name a variable "state"', () => {
  for (const [name, source] of [['ImportPanel', importPanel], ['CasePanel', casePanel]]) {
    assert.doesNotMatch(source, /\bfetch\(/, name + ' has no direct fetch');
    assert.doesNotMatch(source, /\b(?:let|const|var)\s+state\b/, name + ' has no variable named state');
    assert.doesNotMatch(source, /\{@html/, name + ' renders text, never markup');
  }
});

test('local only: none of the four files names an outside address', () => {
  for (const file of ['src/lib/cases.ts', 'src/lib/copy/importCopy.ts', 'src/lib/components/ImportPanel.svelte', 'src/lib/components/CasePanel.svelte']) {
    assert.doesNotMatch(read(file), /https?:\/\//, file);
  }
  assert.match(read('src/lib/cases.ts'), /const base = '\/api';/);
});

test('the screen touches none of the files that belong to the integrator', () => {
  const sources = [read('src/lib/cases.ts'), read('src/lib/copy/importCopy.ts'), importPanel, casePanel].join('\n');
  assert.doesNotMatch(sources, /from '\.\.?\/(?:types|deck|wordings)'/, 'no import of types.ts, deck.ts or wordings.ts values');
});
