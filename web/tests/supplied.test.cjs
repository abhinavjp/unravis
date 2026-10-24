const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

// Card U3: the "Settings the owner told you" group. All values are synthetic.
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

const CASE_ID = 'abcd2345abcd2345abcd234567';
const TOKEN = 'session-token-1';

const realFetch = globalThis.fetch;
function mockServer(handler) {
  const calls = [];
  globalThis.sessionStorage = { getItem: () => TOKEN, setItem() {}, removeItem() {} };
  globalThis.fetch = async (url, init = {}) => {
    const call = { url: String(url), method: init.method ?? 'GET', body: init.body };
    calls.push(call);
    const answer = await handler(call);
    return new Response(JSON.stringify(answer.body ?? {}), { status: answer.status ?? 200, headers: { 'Content-Type': 'application/json' } });
  };
  return calls;
}
test.afterEach(() => { globalThis.fetch = realFetch; delete globalThis.sessionStorage; });

const synthetic = () => new File(['line one\n'], 'web-a.log', { type: 'text/plain', lastModified: 1000000000000 });
const NUMBER = { key: 'slowms', kind: 'number', min: 0, max: 600000, integer: true };
const DECIMAL = { key: 'ratio', kind: 'number', min: -5, max: 5, integer: false };
const ENUM = { key: 'log-time', kind: 'enum', values: ['utc', 'local'] };
const TEXT = { key: 'pattern', kind: 'text', maxLen: 10 };

/* ------------------------------------------------------------------ checking what was typed */

test('a blank box is not sent and is not a problem', () => {
  for (const spec of [NUMBER, ENUM, TEXT]) {
    assert.deepEqual(cases.checkSupplied(spec, ''), { value: null, problem: null });
    assert.deepEqual(cases.checkSupplied(spec, '   '), { value: null, problem: null });
  }
});

test('a number is checked for shape, whole-ness and range, and sent as typed', () => {
  assert.deepEqual(cases.checkSupplied(NUMBER, ' 250 '), { value: '250', problem: null });
  assert.deepEqual(cases.checkSupplied(NUMBER, '0'), { value: '0', problem: null });
  assert.deepEqual(cases.checkSupplied(NUMBER, '600000'), { value: '600000', problem: null });
  assert.equal(cases.checkSupplied(NUMBER, '600001').problem, 'too-high');
  assert.equal(cases.checkSupplied(NUMBER, '-1').problem, 'too-low');
  assert.equal(cases.checkSupplied(NUMBER, '2.5').problem, 'not-whole');
  for (const bad of ['abc', '1e3', '0x10', '1,000', '.5', '5.', '+5', '1 2', '1234567890123456']) assert.equal(cases.checkSupplied(NUMBER, bad).problem, 'not-a-number', bad);
  assert.deepEqual(cases.checkSupplied(DECIMAL, '-2.25'), { value: '-2.25', problem: null });
  assert.equal(cases.checkSupplied(DECIMAL, '5.5').problem, 'too-high');
});

test('a fixed set accepts only its own values', () => {
  assert.deepEqual(cases.checkSupplied(ENUM, 'utc'), { value: 'utc', problem: null });
  assert.equal(cases.checkSupplied(ENUM, 'UTC').problem, 'not-in-set');
  assert.equal(cases.checkSupplied(ENUM, 'gmt').problem, 'not-in-set');
});

test('text is held to its byte length, and hidden characters are refused', () => {
  assert.deepEqual(cases.checkSupplied(TEXT, '0123456789'), { value: '0123456789', problem: null });
  assert.equal(cases.checkSupplied(TEXT, '01234567890').problem, 'too-long');
  assert.equal(cases.checkSupplied(TEXT, 'ééééé').problem, null, 'ten bytes in five letters fits');
  assert.equal(cases.checkSupplied(TEXT, 'éééééé').problem, 'too-long', 'twelve bytes is over, though only six letters');
  for (const bad of ['a\nb', 'a\rb', 'a\tb', 'a\u0000b', 'a​b', 'a b', 'a‮b', 'a﻿b']) assert.equal(cases.checkSupplied(TEXT, bad).problem, 'bad-characters', JSON.stringify(bad));
  assert.equal(cases.checkSupplied(ENUM, 'u\ntc').problem, 'bad-characters');
});

test('checkAllSupplied looks only at the declared keys and drops blanks', () => {
  const out = cases.checkAllSupplied([NUMBER, ENUM, TEXT], { slowms: '100', 'log-time': '', pattern: 'a\nb', undeclared: 'x' });
  assert.deepEqual(out.values, { slowms: '100' });
  assert.deepEqual(out.problems, { pattern: 'bad-characters' });
  assert.deepEqual(cases.checkAllSupplied([], { slowms: '100' }), { values: {}, problems: {} });
});

/* ------------------------------------------------------------------ the importer list */

test('the importer list is read defensively and keeps only specs it can use', () => {
  const list = cases.parseImporters({
    importers: [
      { name: 'mongod-json', supplied: [{ key: 'slowms', kind: 'number', min: 0, max: 5, integer: true }, { key: 'Bad Key', kind: 'text', maxLen: 5 }, { key: 'odd', kind: 'mystery' }, { key: 'empty', kind: 'enum', values: [] }, { key: 'inverted', kind: 'number', min: 9, max: 1 }] },
      { name: 'iis-w3c', supplied: [] },
      { name: 'no-list' },
      { supplied: [] },
      { name: 'log4net-text', supplied: [{ key: 'pattern', kind: 'text' }, { key: 'wide', kind: 'text', maxLen: 99999 }, { key: 'log-time', kind: 'enum', values: ['utc'] }] }
    ]
  });
  assert.deepEqual(list.map((i) => i.name), ['mongod-json', 'iis-w3c', 'no-list', 'log4net-text']);
  assert.deepEqual(list[0].supplied, [{ key: 'slowms', kind: 'number', min: 0, max: 5, integer: true }]);
  assert.deepEqual(list[1].supplied, []);
  assert.deepEqual(list[2].supplied, []);
  assert.equal(list[3].supplied[0].maxLen, 512, 'a text key with no length gets the server limit');
  assert.equal(list[3].supplied[1].maxLen, 512, 'a length over the server limit is capped');
  assert.deepEqual(cases.parseImporters(null), []);
  assert.deepEqual(cases.parseImporters({ importers: 'nope' }), []);
});

test('listImporters asks GET /api/importers', async () => {
  const calls = mockServer(() => ({ body: { importers: [{ name: 'mongod-json', supplied: [{ key: 'slowms', kind: 'number', min: 0, max: 5, integer: true }] }] } }));
  const list = await cases.listImporters();
  assert.equal(calls.length, 1);
  assert.equal(calls[0].method, 'GET');
  assert.match(calls[0].url, /\/api\/importers$/);
  assert.equal(list[0].supplied[0].key, 'slowms');
});

/* ------------------------------------------------------------------ what goes on the wire */

test('settings travel as encoded s.<key> query parameters, never in the path, and blanks are left out', async () => {
  const calls = mockServer(() => ({ body: { importer: 'log4net-text', report: {} } }));
  await cases.previewImport({ caseId: CASE_ID, file: synthetic(), importer: 'log4net-text', host: 'web-a', supplied: { pattern: '%d [%t] & = ?', 'log-time': 'utc', slowms: '', 'Bad Key': 'x' } });
  const url = new URL(calls[0].url, 'http://localhost');
  assert.equal(url.pathname, '/api/cases/' + CASE_ID + '/imports');
  assert.equal(url.searchParams.get('s.pattern'), '%d [%t] & = ?');
  assert.equal(url.searchParams.get('s.log-time'), 'utc');
  assert.equal(url.searchParams.has('s.slowms'), false);
  assert.equal(url.searchParams.has('s.Bad Key'), false);
  assert.equal(url.searchParams.get('preview'), '1');
  assert.doesNotMatch(url.pathname, /utc|%d/);
  assert.match(calls[0].url, /s\.pattern=%25d\+%5B%25t%5D\+%26\+%3D\+%3F/, 'the value is URL-encoded');
  assert.doesNotMatch(calls[0].url, /s\.pattern=[^&]*[ =?[\]]/, 'no raw separator or space inside the value');
  assert.ok(!(calls[0].body instanceof FormData && [...calls[0].body.keys()].some((k) => k.startsWith('s.'))), 'not in the form body either');
});

test('a commit carries the same settings, and with no importer nothing is sent', async () => {
  const calls = mockServer(() => ({ body: { record: { id: 1, status: 'ok' } } }));
  await cases.commitImport({ caseId: CASE_ID, file: synthetic(), importer: 'mongod-json', host: '', supplied: { slowms: '250' }, ackText: 'x' });
  assert.equal(new URL(calls[0].url, 'http://localhost').searchParams.get('s.slowms'), '250');
  assert.equal(new URL(calls[0].url, 'http://localhost').searchParams.has('preview'), false);
  await cases.previewImport({ caseId: CASE_ID, file: synthetic(), importer: '', host: '', supplied: { slowms: '250' } });
  assert.doesNotMatch(calls[1].url, /s\./, 'the type is not known yet, so no setting is sent');
  await cases.previewImport({ caseId: CASE_ID, file: synthetic(), importer: 'mongod-json', host: '' });
  assert.doesNotMatch(calls[2].url, /s\./);
});

test('at most 16 settings are sent', async () => {
  const calls = mockServer(() => ({ body: {} }));
  const supplied = {};
  for (let i = 0; i < 20; i++) supplied['k' + String(i).padStart(2, '0')] = '1';
  await cases.previewImport({ caseId: CASE_ID, file: synthetic(), importer: 'x', host: '', supplied });
  const keys = [...new URL(calls[0].url, 'http://localhost').searchParams.keys()].filter((k) => k.startsWith('s.'));
  assert.equal(keys.length, 16);
});

test('the import list carries what was supplied, as strings only', async () => {
  mockServer(() => ({ body: { imports: [
    { id: 4, importer: 'mongod-json', status: 'ok', supplied: { slowms: '250', pid: 12, 'Bad Key': 'x', 'log-time': 'utc' } },
    { id: 5, status: 'ok' },
    { id: 6, status: 'ok', supplied: 'x' }
  ] } }));
  const records = await cases.listImports(CASE_ID);
  assert.deepEqual(records[0].supplied, { slowms: '250', 'log-time': 'utc' });
  assert.deepEqual(records[1].supplied, {});
  assert.deepEqual(records[2].supplied, {});
});

/* ------------------------------------------------------------------ the preview and the gate */

test('the preview key changes with the settings, and is the old key when there are none', () => {
  const f = { name: 'web-a.log', size: 20, lastModified: 1 };
  const bare = cases.previewKey(f, 'mongod-json', 'web-a');
  assert.equal(cases.previewKey(f, 'mongod-json', 'web-a', {}), bare);
  const a = cases.previewKey(f, 'mongod-json', 'web-a', { slowms: '100' });
  assert.notEqual(a, bare);
  assert.notEqual(a, cases.previewKey(f, 'mongod-json', 'web-a', { slowms: '101' }));
  assert.equal(a, cases.previewKey(f, 'mongod-json', 'web-a', { slowms: '100' }));
  assert.equal(cases.previewKey(null, 'x', '', { slowms: '1' }), '');
});

test('a setting that does not fit holds the import back', () => {
  const report = { importer: 'x', rows: 1, emitted: 1, rejected: 0, errors: [] };
  const ready = { caseOpen: true, previewedKey: 'k', currentKey: 'k', importer: 'mongod-json', report, unknownHost: false, acknowledged: true, busy: false };
  assert.equal(cases.commitBlock(ready), null);
  assert.equal(cases.commitBlock({ ...ready, settingsProblem: false }), null);
  assert.equal(cases.commitBlock({ ...ready, settingsProblem: true }), 'settings');
  assert.equal(cases.commitBlock({ ...ready, settingsProblem: true, unknownHost: true }), 'unknown-host');
  assert.ok(importCopy.IMPORT_TEXT.blocked.settings);
});

/* ------------------------------------------------------------------ the words */

test('every key an importer declares has a label and a hint', () => {
  const keys = ['slowms', 'app-request-min-ms', 'pattern', 'log-time', 'slowlog-log-slower-than', 'slowlog-max-len', 'exported-at', 'latency-monitor-threshold', 'year', 'date', 'pid'];
  for (const key of keys) {
    assert.ok(importCopy.SUPPLIED_LABELS[key], key + ' has a label');
    assert.ok(importCopy.SUPPLIED_HINTS[key], key + ' has a hint');
    assert.equal(importCopy.suppliedLabel(key), importCopy.SUPPLIED_LABELS[key]);
  }
  assert.equal(importCopy.suppliedLabel('brand-new'), 'brand-new');
  assert.equal(importCopy.suppliedHint('brand-new'), '');
  assert.equal(importCopy.suppliedLabel('toString'), 'toString', 'inherited names are not keys');
  assert.equal(importCopy.suppliedValueLabel('log-time', 'utc'), 'UTC');
  assert.equal(importCopy.suppliedValueLabel('log-time', 'local'), 'local');
  assert.equal(importCopy.suppliedValueLabel('toString', 'x'), 'x');
});

test('range and problem sentences name the limits in plain words', () => {
  assert.equal(importCopy.suppliedRangeText(NUMBER), 'A whole number from 0 to 600000.');
  assert.equal(importCopy.suppliedRangeText(DECIMAL), 'A number from -5 to 5.');
  assert.equal(importCopy.suppliedRangeText(TEXT), 'Up to 10 bytes of plain text.');
  assert.equal(importCopy.suppliedRangeText(ENUM), '');
  assert.equal(importCopy.suppliedProblemText('too-high', NUMBER), 'The largest value is 600000.');
  assert.equal(importCopy.suppliedProblemText('too-low', NUMBER), 'The smallest value is 0.');
  assert.equal(importCopy.suppliedProblemText('too-long', TEXT), 'This is too long. The limit is 10 bytes.');
  for (const problem of ['not-a-number', 'not-whole', 'too-low', 'too-high', 'not-in-set', 'too-long', 'bad-characters']) assert.ok(importCopy.suppliedProblemText(problem, NUMBER).length > 10, problem);
});

test('the summary note and the history pairs', () => {
  assert.equal(importCopy.suppliedSummaryNote(0, 0), '');
  assert.equal(importCopy.suppliedSummaryNote(2, 0), ' (2 set)');
  assert.equal(importCopy.suppliedSummaryNote(2, 1), ' (needs fixing)');
  assert.deepEqual(importCopy.suppliedPairs({ slowms: '250', 'log-time': 'utc' }), [
    { key: 'log-time', label: 'Log times are in', value: 'UTC' },
    { key: 'slowms', label: 'Slow operation limit (ms)', value: '250' }
  ]);
  assert.deepEqual(importCopy.suppliedPairs(undefined), []);
  assert.deepEqual(importCopy.suppliedPairs({}), []);
});

test('the refusal copy for a bad setting is shown, without echoing the server message', () => {
  const text = importCopy.errorText({ status: 422, reason: 'bad-supplied', message: 'SENTINEL-9c1 value leaked' });
  assert.equal(text, importCopy.REASON_TEXT['bad-supplied']);
  assert.doesNotMatch(text, /SENTINEL/);
});

test('the new copy passes the copy lint and is in the lint list', () => {
  assert.deepEqual(lintCopy(importCopy.importCopyEntries(), deck.terms), []);
  const text = importCopy.importCopyEntries().map((e) => e.text);
  for (const key of Object.keys(importCopy.SUPPLIED_HINTS)) assert.ok(text.includes(importCopy.SUPPLIED_HINTS[key]), key + ' hint is linted');
  for (const key of Object.keys(importCopy.SUPPLIED_LABELS)) assert.ok(text.includes(importCopy.SUPPLIED_LABELS[key]), key + ' label is linted');
  assert.ok(text.includes(importCopy.IMPORT_TEXT.suppliedHeading));
  assert.ok(text.includes(importCopy.IMPORT_TEXT.suppliedIntro));
  assert.ok(text.includes(importCopy.IMPORT_TEXT.pickerRefused));
  assert.doesNotMatch(text.join('\n'), /\b(?:caused|because|due to|proven)\b/i);
});

/* ------------------------------------------------------------------ the screen, read as source */

const panel = read('src/lib/components/ImportPanel.svelte');
const group = read('src/lib/components/SuppliedSettings.svelte');

test('the group is a collapsed native disclosure with real labels', () => {
  assert.match(group, /<details class="supplied" bind:open>/);
  assert.match(group, /let open = \$state\(false\)/, 'collapsed by default');
  assert.match(group, /<summary>/);
  assert.match(group, /<label for=\{id\}>/);
  assert.match(group, /aria-describedby/);
  assert.match(group, /role="alert"/);
  assert.match(group, /aria-invalid/);
});

test('the group shows only the keys the chosen importer declares, and only once the type is known', () => {
  assert.match(panel, /importerInfos\.find\(\(info\) => info\.name === importer\)\?\.supplied \?\? \[\]/);
  assert.match(panel, /\{#if importer && file && specs\.length > 0\}/);
  assert.match(group, /\{#each specs as spec \(spec\.key\)\}/);
});

test('values are sent through the checked set, and the screen keeps nothing in the URL or the log', () => {
  assert.match(panel, /supplied: sentSupplied/);
  assert.match(panel, /previewKey\(sent, importer, host, sentSupplied\)/);
  assert.match(panel, /settingsProblem/);
  for (const source of [panel, group]) assert.doesNotMatch(source, /console\.|localStorage|sessionStorage|location\.(?:search|hash)|history\.(?:push|replace)State/);
});

test('a refused file with no type can still be given one, and the history lists the settings', () => {
  assert.match(panel, /undetected = true/);
  assert.match(panel, /pickerRefused/);
  assert.match(panel, /suppliedPairs\(r\.supplied\)/);
  assert.match(panel, /IMPORT_TEXT\.suppliedHistory/);
});
