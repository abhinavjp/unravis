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
  const output = ts.transpileModule(source, {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022, esModuleInterop: true }
  }).outputText;
  const module = { exports: {} };
  const nodeRequire = createRequire(filename);
  const load = (request) => /^\.\.?\//.test(request) && fs.existsSync(path.join(path.dirname(filename), request + '.ts'))
    ? loadTypeScript(path.relative(root, path.join(path.dirname(filename), request + '.ts')))
    : nodeRequire(request);
  new Function('require', 'module', 'exports', output)(load, module, module.exports);
  return module.exports;
}
const read = (relative) => fs.readFileSync(path.join(root, relative), 'utf8');

const collect = loadTypeScript('src/lib/collect.ts');
const cases = loadTypeScript('src/lib/cases.ts');
const importCopy = loadTypeScript('src/lib/copy/importCopy.ts');
const { lintScreenStrings, fleschKincaidGrade, MAX_GRADE, MIN_WORDS_FOR_GRADE } = loadTypeScript('src/lib/copy/copylint.ts');
const { deck } = loadTypeScript('src/lib/copy/deck.ts');

// Synthetic values only: invented hosts, no real names.
const CASE_ID = 'abcd2345abcd2345abcd234567';
const HOST = 'web-a.example.test';
const item = (id, rank, extra = {}) => ({ id, host: HOST, title: 'Title ' + id, why: 'Why ' + id + '.', how: 'How ' + id + '.', importer: 'win-counters', rank, ...extra });
const answer = (...items) => ({ items });
const golden = () => JSON.parse(read('../internal/collect/testdata/wording.golden.json'));

/* ------------------------------------------------------------------ shape check */

test('parseCollect accepts the documented shape and puts the items in rank order', () => {
  const items = collect.parseCollect(answer(item('b', 2), item('a', 1), item('c', 3, { host: '' })));
  assert.deepEqual(items.map((i) => i.id), ['a', 'b', 'c']);
  assert.equal(items[2].host, '', 'an empty host means the whole case');
  assert.deepEqual(collect.parseCollect({ items: [] }), []);
});

test('parseCollect refuses anything that is not the documented shape', () => {
  const twelve = Array.from({ length: 12 }, (_, i) => item('x' + i, i + 1));
  assert.equal(collect.parseCollect({ items: twelve }).length, 12);
  const bad = [
    null, undefined, 'items', 7, [], {}, { items: null }, { items: {} }, { items: 'x' },
    { items: [...twelve, item('x13', 13)] },
    answer(null), answer('text'), answer(item('a', 0)), answer(item('a', 1.5)), answer(item('a', '1')), answer(item('a', -1)),
    answer(item('', 1)), answer(item('a', 1, { title: '' })), answer(item('a', 1, { title: 5 })), answer(item('a', 1, { why: null })),
    answer(item('a', 1, { how: undefined })), answer(item('a', 1, { host: null })), answer(item('a', 1, { importer: '' })),
    answer(item('a', 1, { importer: ['win-counters'] }))
  ];
  for (const value of bad) assert.equal(collect.parseCollect(value), null, JSON.stringify(value));
});

/* ------------------------------------------------------------------ the rows the list shows */

test('collectRows keeps rank order, gives every row a unique key and names the file types', () => {
  const rows = collect.collectRows(collect.parseCollect(answer(
    item('app-log', 2, { importer: 'log4net-text, serilog-clef' }),
    item('host-counters', 1),
    item('host-counters', 3, { host: 'web-b.example.test' }),
    item('host-counters', 4, { host: 'web-b.example.test' })
  )));
  assert.deepEqual(rows.map((row) => row.rank), [1, 2, 3, 4]);
  assert.equal(new Set(rows.map((row) => row.key)).size, 4);
  assert.deepEqual(rows[0].fileTypes, [importCopy.importerLabel('win-counters')]);
  assert.deepEqual(rows[1].fileTypes, [importCopy.importerLabel('log4net-text'), importCopy.importerLabel('serilog-clef')]);
  assert.equal(rows[0].host, HOST);
  assert.equal(rows[0].title, 'Title host-counters');
});

test('an importer name the screen does not know is shown as sent; an empty list gives no rows', () => {
  assert.deepEqual(collect.fileTypesOf('brand-new-importer'), ['brand-new-importer']);
  assert.deepEqual(collect.fileTypesOf(' a ,, b '), ['a', 'b']);
  assert.deepEqual(collect.collectRows([]), []);
});

test('every importer name the server can send has a plain file type name', () => {
  const source = read('../internal/collect/collect.go');
  const names = new Set();
  for (const line of source.split(/\r?\n/)) {
    const one = /^\s+\w+\s+=\s+"([a-z0-9-]+)"\s*$/.exec(line);
    if (one) names.add(one[1]);
    if (line.includes('var appLogImporters')) for (const m of line.matchAll(/"([a-z0-9-]+)"/g)) names.add(m[1]);
  }
  const sendable = [...names];
  assert.ok(sendable.length >= 20, 'found ' + sendable.length + ' names');
  for (const name of sendable) assert.notEqual(importCopy.importerLabel(name), name, name + ' has no file type name');
});

/* ------------------------------------------------------------------ the request (mocked fetch; no network) */

function mockServer(handler) {
  const calls = [];
  globalThis.sessionStorage = { getItem: () => 'session-token-1', setItem() {}, removeItem() {} };
  globalThis.fetch = async (url, init = {}) => {
    calls.push({ url: String(url), method: init.method ?? 'GET', headers: init.headers instanceof Headers ? init.headers : new Headers(init.headers) });
    const result = await handler(String(url));
    return new Response(JSON.stringify(result.body ?? {}), { status: result.status ?? 200, headers: { 'Content-Type': 'application/json' } });
  };
  return calls;
}
const realFetch = globalThis.fetch;
test.afterEach(() => { globalThis.fetch = realFetch; delete globalThis.sessionStorage; });

test('fetchCollect asks for the case route with the session token and returns the parsed items', async () => {
  const calls = mockServer(() => ({ body: answer(item('a', 1), item('b', 2)) }));
  const items = await cases.fetchCollect(CASE_ID);
  assert.deepEqual(items.map((i) => i.id), ['a', 'b']);
  assert.equal(calls.length, 1);
  const url = new URL(calls[0].url, 'http://x');
  assert.equal(url.pathname, '/api/cases/' + CASE_ID + '/collect');
  assert.equal(url.search, '', 'nothing is sent in the query');
  assert.equal(calls[0].method, 'GET');
  assert.equal(calls[0].headers.get('X-Audit-Token'), 'session-token-1');
});

test('fetchCollect keeps a refusal reason and calls a wrong shape unreadable', async () => {
  mockServer(() => ({ status: 404, body: { error: 'The case was not found.', reason: 'case-not-found' } }));
  await assert.rejects(cases.fetchCollect(CASE_ID), (error) => error.reason === 'case-not-found' && error.status === 404 && cases.isCaseGone(error));
  mockServer(() => ({ status: 503, body: { error: 'Cases are not available.' } }));
  await assert.rejects(cases.fetchCollect(CASE_ID), (error) => error.status === 503);
  mockServer(() => ({ body: { items: null } }));
  await assert.rejects(cases.fetchCollect(CASE_ID), (error) => error.reason === 'unreadable');
  // No session: no request at all.
  const calls = mockServer(() => ({ body: answer() }));
  globalThis.sessionStorage = { getItem: () => null, setItem() {}, removeItem() {} };
  await assert.rejects(cases.fetchCollect(CASE_ID), (error) => error.reason === 'no_session');
  assert.equal(calls.length, 0);
});

test('fetchCollect passes the abort signal on', async () => {
  const controller = new AbortController();
  globalThis.sessionStorage = { getItem: () => 'session-token-1', setItem() {}, removeItem() {} };
  globalThis.fetch = async (_url, init = {}) => {
    assert.equal(init.signal, controller.signal);
    throw new DOMException('Aborted', 'AbortError');
  };
  await assert.rejects(cases.fetchCollect(CASE_ID, controller.signal), (error) => error instanceof DOMException && error.name === 'AbortError');
});

/* ------------------------------------------------------------------ copy */

test('the empty state says what the card asks for', () => {
  assert.equal(importCopy.IMPORT_TEXT.collectEmpty, 'Nothing more to suggest from these files.');
  assert.equal(importCopy.IMPORT_TEXT.collectHeading, 'What to collect next');
});

test('the screen words for the list pass the copy lint', () => {
  const strings = Object.entries(importCopy.IMPORT_TEXT)
    .filter(([key, value]) => key.startsWith('collect') && typeof value === 'string')
    .map(([key, value]) => ({ id: 'collect.text.' + key, text: value }));
  assert.ok(strings.length >= 8);
  assert.deepEqual(lintScreenStrings(strings, deck.terms), []);
});

// The server's sentences are written in Go (internal/collect) and pinned in a golden file. The same file is read here, so a sentence
// that is too hard to read, or that names a cause, fails in both places.
test('every sentence the server can send passes the copy lint and the reading grade', () => {
  const entries = [];
  for (const row of golden()) {
    for (const field of ['title', 'why', 'how']) {
      assert.equal(typeof row[field], 'string');
      assert.ok(row[field].length > 0, row.id + ' ' + field);
      entries.push({ id: 'collect.' + row.id + '.' + field, text: row[field] });
    }
  }
  assert.ok(entries.length >= 54);
  assert.deepEqual(lintScreenStrings(entries, deck.terms), []);
  for (const entry of entries.filter((e) => e.text.split(' ').length >= MIN_WORDS_FOR_GRADE)) {
    const grade = fleschKincaidGrade(entry.text);
    assert.ok(grade <= MAX_GRADE, entry.id + ' is grade ' + grade.toFixed(1) + ': ' + entry.text);
  }
});

test('the server sentences hold no diagnosis words or case values', () => {
  const banned = /\b(?:leak\w*|starv\w*|saturat\w*|caus\w*|because|due to|bottleneck|culprit|root cause)\b/i;
  for (const row of golden()) {
    for (const field of ['title', 'why', 'how']) {
      assert.doesNotMatch(row[field], banned, row.id + ' ' + field);
      assert.doesNotMatch(row[field], /example\.test|web-a|\d{4}-\d{2}-\d{2}/, row.id + ' ' + field);
    }
  }
});

/* ------------------------------------------------------------------ the component */

test('the list sits in a collapsed details group that loads on open and has an empty state', () => {
  const source = read('src/lib/components/CaseTools.svelte');
  assert.match(source, /<details class="collect" ontoggle=\{onCollectToggle\}>/, 'collapsed by default: no open attribute');
  assert.match(source, /<summary id="collect-heading">\{IMPORT_TEXT\.collectHeading\}<\/summary>/);
  assert.match(source, /IMPORT_TEXT\.collectEmpty/);
  assert.match(source, /IMPORT_TEXT\.collectFileType/);
  assert.match(source, /fetchCollect\(caseId, mine\.signal\)/);
  assert.match(source, /collectController\?\.abort\(\)/, 'a read in flight is cancelled when the panel goes');
  assert.doesNotMatch(source, /\{@html/, 'server text is shown as text');
});
