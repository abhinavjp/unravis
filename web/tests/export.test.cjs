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
  // A relative import of another TypeScript file in src/lib is transpiled the same way.
  const load = (request) => request.startsWith('./') && fs.existsSync(path.join(path.dirname(filename), request + '.ts'))
    ? loadTypeScript(path.relative(root, path.join(path.dirname(filename), request + '.ts')))
    : nodeRequire(request);
  new Function('require', 'module', 'exports', output)(load, module, module.exports);
  return module.exports;
}

const exp = loadTypeScript('src/lib/export.ts');
const { deck } = loadTypeScript('src/lib/copy/deck.ts');
const { lintCopy } = loadTypeScript('src/lib/copy/copylint.ts');

const record = (id, lowN = false) => ({
  version: 1, evidenceId: id, source: 'Orders', windowStart: '2026-10-24T10:00:00Z', windowEnd: '2026-10-24T11:00:00Z', joinClass: 'none', tolerance: '0 ms',
  provenance: { kind: 'query', ref: 'r1', hash: 'abc' }, label: 'Symptom', rule: 'B1', headline: '12 requests took 45 s or longer.',
  n: { masked: lowN, text: lowN ? '<20' : '42', value: lowN ? undefined : 42 }, lowN, knownGaps: [], sourceQuality: {}
});
const exported = (over = {}) => ({
  manifest: { schemaVersion: 1, toolVersion: 'x', createdAt: '2026-10-24T12:00:00Z', bodySha256: 'f00d', masking: { statements: ['Counts below 20 are shown as <20.'] } },
  body: { records: [record('a'), record('b', true)], ...over }
});
const preview = (e = exported()) => ({ kind: 'preview', preview: { previewId: 'p1', export: e } });

test('describeCount shows the server text, and falls back without guessing', () => {
  assert.equal(exp.describeCount({ masked: true, text: '<20' }), '<20');
  assert.equal(exp.describeCount({ masked: false, text: '42', value: 42 }), '42');
  assert.equal(exp.describeCount({ masked: true, text: '' }), '<20');
  assert.equal(exp.describeCount({ masked: false, text: '', value: 7 }), '7');
  assert.equal(exp.describeCount({ masked: false, text: '' }), '');
});

test('exportSummary counts findings, low n, pseudonyms and ledger entries', () => {
  const s = exp.exportSummary(exported());
  assert.equal(s.records, 2);
  assert.equal(s.lowN, 1);
  assert.equal(s.hash, 'f00d');
  assert.equal(s.empty, false);
  assert.deepEqual(s.statements, ['Counts below 20 are shown as <20.']);
  assert.equal(s.line, '2 findings, 1 low n');
  const full = exp.exportSummary(exported({ pseudonyms: [{ label: 'user-3fa91c2e', requests: { masked: true, text: '<20' } }], ledger: [{ text: 't', state: 'open', evidenceIds: [] }] }));
  assert.equal(full.line, '2 findings, 1 low n, 1 pseudonym, 1 ledger entry');
  const none = exp.exportSummary(exported({ records: [] }));
  assert.equal(none.empty, true);
  assert.equal(none.line, '0 findings, 0 low n');
});

test('qualityRows keeps only the facts that are present', () => {
  assert.deepEqual(exp.qualityRows({}), []);
  assert.deepEqual(exp.qualityRows(undefined), []);
  assert.deepEqual(exp.qualityRows({ sourceVersion: '7.0', timezoneRule: 'UTC', threshold: '' }), [
    { label: 'Source version', value: '7.0' }, { label: 'Time zone rule', value: 'UTC' }
  ]);
});

test('exportInput carries the window and both options', () => {
  assert.deepEqual(exp.exportInput({ sourceIds: ['a'], start: 's', end: 'e' }, true, false), { sourceIds: ['a'], start: 's', end: 'e', includePseudonyms: true, includeLedger: false });
});

test('downloadName strips folders and unsafe characters and has a fallback', () => {
  assert.equal(exp.downloadName({ filename: 'evidence-2026-10-24.json' }), 'evidence-2026-10-24.json');
  assert.equal(exp.downloadName({ filename: '../../etc/passwd' }), 'passwd');
  assert.equal(exp.downloadName({ filename: 'C:\\temp\\a<b>.json' }), 'ab.json');
  assert.equal(exp.downloadName({ filename: '' }), 'evidence-export.json');
  assert.equal(exp.downloadName({ filename: '..' }), 'evidence-export.json');
  assert.equal(exp.downloadName({}), 'evidence-export.json');
});

test('canSave is true only for a shown preview with findings', () => {
  assert.equal(exp.canSave({ kind: 'idle' }), false);
  assert.equal(exp.canSave({ kind: 'previewing' }), false);
  assert.equal(exp.canSave(preview()), true);
  assert.equal(exp.canSave(preview(exported({ records: [] }))), false);
  assert.equal(exp.canSave({ ...preview(), kind: 'saving' }), false);
  assert.equal(exp.canSave({ ...preview(), kind: 'saved', filename: 'f', sha256: 's' }), false);
});

test('errorText shows a server message as written and gives a bare 503 or 404 a plain sentence', () => {
  const T = exp.EXPORT_TEXT;
  const err = (message, status) => Object.assign(new Error(message), { status });
  assert.equal(exp.errorText(err('Export is turned off for this build.', 503), 'preview'), 'Export is turned off for this build.');
  assert.equal(exp.errorText(err('Request failed (503)', 503), 'preview'), T.unavailable);
  assert.equal(exp.errorText(err('Request failed (404)', 404), 'save'), T.goneSave);
  assert.equal(exp.errorText(err('Request failed (404)', 404), 'preview'), T.notFound);
  assert.equal(exp.errorText(err('', 500), 'save'), T.failedSave);
  assert.equal(exp.errorText('boom', 'preview'), T.failedPreview);
  assert.equal(exp.savedLine('abc123'), 'Saved. SHA-256 of the saved file: abc123');
});

test('contentMatches accepts only content whose SHA-256 equals the returned hash', async () => {
  const content = '{\n  "n": "<20",\n  "label": "Ünïcode"\n}\n';
  const good = require('node:crypto').createHash('sha256').update(content, 'utf8').digest('hex');
  assert.equal(await exp.sha256Hex(content), good);
  assert.equal(await exp.contentMatches(content, good), true);
  assert.equal(await exp.contentMatches(content, good.toUpperCase()), true);
  assert.equal(await exp.contentMatches(content + ' ', good), false);
  assert.equal(await exp.contentMatches(content, good.replace(/^./, (c) => (c === '0' ? '1' : '0'))), false);
  assert.equal(await exp.contentMatches(content, ''), false);
  assert.equal(await exp.contentMatches(content, 'fakehash123'), false);
  assert.equal(await exp.contentMatches(content, undefined), false);
});

test('the panel checks the hash before saving and keys pseudonym rows by index', () => {
  const panel = fs.readFileSync(path.join(root, 'src/components/ExportPanel.svelte'), 'utf8');
  const save = panel.slice(panel.indexOf('async function save()'), panel.indexOf('</script>'));
  assert.ok(save.indexOf('contentMatches(file.content, file.sha256)') > 0, 'save checks the hash');
  assert.ok(save.indexOf('contentMatches(') < save.indexOf('new Blob('), 'the hash is checked before the file is made');
  assert.match(panel, /exp\.body\.pseudonyms as row, i \(i\)/);
  assert.doesNotMatch(panel, /exact (bytes|file)/i);
  assert.doesNotMatch(JSON.stringify(exp.EXPORT_TEXT), /exact (bytes|file)/i);
});

test('every fixed string passes the copy lint and avoids causal or certainty wording', () => {
  const strings = [
    ...Object.values(exp.EXPORT_TEXT),
    ...Object.values(exp.RECORD_FIELDS),
    ...['Source version', 'Threshold', 'First record covered', 'Last record covered', 'Row selection', 'Time zone rule'],
    exp.savedLine('0123456789abcdef'),
    exp.exportSummary(exported({ pseudonyms: [], ledger: [] })).line
  ];
  assert.deepEqual(lintCopy(strings.map((text, i) => ({ id: `e${i}`, text })), deck.terms), []);
  for (const text of strings) assert.doesNotMatch(text, /\b(caused|because|due to|waiting on|blocked by|proven|complete)\b/i, text);
});

test('the new glossary terms exist with a does-not-mean line', () => {
  for (const id of ['pseudonym', 'thin-cell']) {
    const term = deck.terms.find((t) => t.id === id);
    assert.ok(term, id);
    assert.ok(term.notMeaning.trim().length > 0, id);
  }
});
