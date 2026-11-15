// FIX-37 / DATA-01: a check verdict carries row counts only when the file was counted.
// A verdict without counts must stay "not counted" (undefined), never turn into 0. Behaviour test on the real parser
// and on the popup state's fallback verdict.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

const root = path.resolve(__dirname, '..');
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

const cases = load('src/lib/cases.ts');
const { createPopupState } = load('src/lib/components/importPopup/popupState.svelte.ts');

const file = (extra) => ({ key: 'k1', name: 'a.log', size: 10, verdict: 'ready', importer: 'iis-w3c', tied: [], serverName: '', needsZone: false, reason: '', ...extra });
const parse = (extra) => cases.parseCheckReport({ files: [file(extra)], checkedAt: '2001-01-01T00:00:00Z' }).files[0];

test('a verdict that carries counts keeps them, zero included', () => {
  const v = parse({ rows: 5, emitted: 4, rejected: 0 });
  assert.equal(v.rows, 5);
  assert.equal(v.emitted, 4);
  assert.equal(v.rejected, 0);
});

test('a verdict without counts stays uncounted, not zero', () => {
  const v = parse({ verdict: 'needs-computer', serverName: 'web-a' });
  assert.equal(v.rows, undefined);
  assert.equal(v.emitted, undefined);
  assert.equal(v.rejected, undefined);
});

test('a count that is not a number is treated as not counted', () => {
  const v = parse({ rows: 'many', emitted: null, rejected: {} });
  assert.equal(v.rows, undefined);
  assert.equal(v.emitted, undefined);
  assert.equal(v.rejected, undefined);
});

test('the popup fallback verdict for a file the server left out has no counts', async () => {
  const popup = createPopupState({ check: async () => ({ files: [], checkedAt: '' }), runner: () => async () => ({}), addComputer: async () => ({ ok: true, label: 'x' }) });
  await popup.addFiles([new File(['x'], 'notes.txt')]);
  popup.continueToCheck();
  for (let i = 0; i < 8; i++) await new Promise((resolve) => setImmediate(resolve));
  assert.equal(popup.report.files.length, 1);
  const v = popup.report.files[0];
  assert.equal(v.verdict, 'not-supported');
  assert.equal(v.rows, undefined);
  assert.equal(v.emitted, undefined);
  assert.equal(v.rejected, undefined);
});
