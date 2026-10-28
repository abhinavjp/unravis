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
const read = (relativePath) => fs.readFileSync(path.join(root, relativePath), 'utf8');

// L7-05: the saved report carries its warnings as codes.
test('reportWarningCodes sends codes only', () => {
  const r = loadTypeScript('src/lib/guided/report.ts');
  assert.deepEqual(r.reportWarningCodes(false, undefined), []);
  assert.deepEqual(r.reportWarningCodes(false, { requests: 1, iisRows: 0, truncated: false, warnings: ['free text'] }), []);
  assert.deepEqual(r.reportWarningCodes(true, undefined), ['stale']);
  assert.deepEqual(r.reportWarningCodes(true, { requests: 1, iisRows: 0, truncated: true, warnings: ['free text'] }), ['stale', 'truncated']);
});

test('the report export sends the warning codes with the include choices', () => {
  const svelte = read('src/lib/components/guided/ReportExport.svelte');
  assert.match(svelte, /reportWarningCodes\(store\.outOfDate\(sourceIds\), store\.answer\?\.read\)/);
  assert.match(svelte, /store\.buildReport\(format, sent\)/);
  assert.doesNotMatch(svelte, /\bstate\s*=/);
});

// L7-07: the export reads the ledger the person sees, and says so when a list is empty.
test('exportInput names the open case only when the ledger is asked for', () => {
  const { exportInput } = loadTypeScript('src/lib/export.ts');
  const q = { sourceIds: ['a'], start: '2026-01-01T00:00:00Z', end: '2026-01-02T00:00:00Z' };
  assert.equal(exportInput(q, false, true, 'case1').ledgerCaseId, 'case1');
  assert.equal('ledgerCaseId' in exportInput(q, false, false, 'case1'), false);
  assert.equal('ledgerCaseId' in exportInput(q, false, true), false);
  assert.equal('ledgerCaseId' in exportInput(q, false, true, ''), false);
});

test('the drawer shows the none-sentences from what was asked, not from the file', () => {
  const { listsAsked } = loadTypeScript('src/lib/export.ts');
  assert.deepEqual(listsAsked({ ledgerIncluded: true }), { ledger: true, pseudonyms: false });
  assert.deepEqual(listsAsked({ pseudonymsIncluded: true }), { ledger: false, pseudonyms: true });
  assert.deepEqual(listsAsked(undefined), { ledger: false, pseudonyms: false });
  const panel = read('src/components/ExportPanel.svelte');
  assert.match(panel, /exp\.body\.ledger \|\| asked\.ledger/);
  assert.match(panel, /exp\.body\.pseudonyms \|\| asked\.pseudonyms/);
  assert.match(panel, /exportInput\(query, includePseudonyms, includeLedger, ledger\.caseId\)/);
});

// L7-08: nothing to save, so no Save button.
test('the export drawer has no Save button for an empty preview', () => {
  const panel = read('src/components/ExportPanel.svelte');
  const block = panel.slice(panel.indexOf('<div class="export-save">'));
  const end = block.indexOf('</div>');
  const saveBlock = block.slice(0, end);
  assert.match(saveBlock, /\{:else if !summary\.empty\}/, 'the Save button only exists when the preview has findings');
  assert.ok(saveBlock.indexOf('{:else if !summary.empty}') < saveBlock.indexOf('EXPORT_TEXT.save}'), 'the button sits inside that branch');
  assert.doesNotMatch(saveBlock, /\{:else\}/, 'there is no unconditional Save button');
  assert.equal((panel.match(/class="primary-button"/g) ?? []).length, 2, 'the Preview button and the Save button are the only primary buttons');
});

// L7-10: Escape closes the report preview.
test('Escape closes the report preview and focus goes back to the Preview button', () => {
  const src = read('src/lib/components/guided/ReportExport.svelte');
  assert.match(src, /function closePreview\(\)\s*\{\s*preview = '';\s*previewButton\?\.focus\(\);/);
  assert.match(src, /if \(preview\) closeButton\?\.focus\(\)/);
  assert.match(src, /event\.key === 'Escape'[^}]*closePreview\(\)/);
  assert.match(src, /bind:this=\{previewButton\}/);
  assert.match(src, /bind:this=\{closeButton\}[^>]*onclick=\{closePreview\}/);
  assert.match(src, /<iframe title=\{T\.preview\} sandbox="" srcdoc=\{preview\}><\/iframe>/, 'the sandbox stays empty');
  assert.doesNotMatch(src, /(let|const)\s+state\b/);
});
