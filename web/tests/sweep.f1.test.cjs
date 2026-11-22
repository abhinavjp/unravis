const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');
const { compile } = require('svelte/compiler');
const { render } = require('svelte/server');
const { loadTypeScript } = require('./helpers/loadTs.cjs');

// UI sweep, package F1 (the shell, shared CSS and shared help components). Layout, hover, focus and keyboard behaviour is measured in a real browser by
// tools/e2e/specs/ui-sweep-f1.spec.ts (SWEEP-F1-01 to 12). These tests render the shared help components in node and check what the person gets in the page.
const root = path.resolve(__dirname, '..');
const help = loadTypeScript('src/lib/copy/helpCopy.ts').help;
const eliState = { on: false };
function loadComponent(file, resolve) {
  const full = path.join(root, file);
  const compiled = compile(fs.readFileSync(full, 'utf8'), { filename: path.basename(file), generate: 'server' });
  const js = ts.transpileModule(compiled.js.code, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText;
  const module = { exports: {} };
  const nodeRequire = createRequire(full);
  new Function('require', 'module', 'exports', js)((request) => resolve(request) ?? nodeRequire(request), module, module.exports);
  return module.exports.default;
}
const deps = (request) => ({ '../lib/copy/helpCopy': { help }, '../lib/eli10.svelte': { eli10: eliState } })[request];
const HelpNote = loadComponent('src/components/HelpNote.svelte', deps);
const FieldHelp = loadComponent('src/components/FieldHelp.svelte', deps);
const html = (component, props, on = false) => { eliState.on = on; return render(component, { props }).body; };
const KEY = Object.keys(help).find((key) => !key.startsWith('rf-'));

test('SWEEP-F1-05 (CASES-23) a help note starts closed, and with Explain simply on it starts open with a button that still works', () => {
  const off = html(HelpNote, { id: KEY });
  assert.match(off, /class="help-toggle[^"]*"[^>]*aria-expanded="false"/);
  assert.match(off, /<div class="help-simple[^"]*"[^>]*hidden/, 'the block is kept in the page, hidden');
  const on = html(HelpNote, { id: KEY }, true);
  assert.match(on, /class="help-toggle[^"]*"[^>]*aria-expanded="true"/);
  assert.doesNotMatch(on, /class="help-toggle[^"]*"[^>]*disabled/, 'not disabled: the person can fold it');
  assert.doesNotMatch(on, /<div class="help-simple[^"]*"[^>]*hidden/);
  assert.match(on, /aria-label="Explain in simple words"/);
  assert.ok(on.includes(help[KEY].simple) && on.includes(help[KEY].plain));
});

test('SWEEP-F1-03 (ANALYSIS-06) a field help is named after its field when the row passes the name, and falls back to a plain name', () => {
  const named = html(FieldHelp, { id: 'outcome', label: 'Status' });
  assert.match(named, /aria-label="About Status"/);
  assert.match(named, /role="group" aria-label="About Status"/);
  const unnamed = html(FieldHelp, { id: 'outcome' });
  assert.match(unnamed, /aria-label="About this filter"/);
  const units = html(FieldHelp, { id: 'outcome', label: 'Total time (ms)' });
  assert.match(units, /aria-label="About Total time"/, 'a unit in brackets is left out of the name');
});

test('SWEEP-F1-03 (ANALYSIS-05) with Explain simply on, a field help block is open, its button is off and says why', () => {
  const on = html(FieldHelp, { id: 'outcome', label: 'Status' }, true);
  assert.match(on, /class="field-help-toggle[^"]*"[^>]*aria-expanded="true"[^>]*aria-label="About Status \(open: Explain simply is on\)"[^>]*disabled/);
  assert.doesNotMatch(on, /id="field-help-outcome"[^>]*hidden/);
  const off = html(FieldHelp, { id: 'outcome', label: 'Status' });
  assert.match(off, /id="field-help-outcome"[^>]*hidden/);
});
