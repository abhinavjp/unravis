const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const ts = require('typescript');
const { compile } = require('svelte/compiler');
const { render } = require('svelte/server');
const { loadTypeScript } = require('./helpers/loadTs.cjs');

// The Review step rendered by Svelte itself (server render, no DOM): a log folder typed twice must not break it
// (Svelte 5 throws each_key_duplicate on a repeated key).

const root = path.join(__dirname, '..');
const w = loadTypeScript('src/lib/blackboxWizard.ts');

const mods = {
  '../../lib/blackbox': loadTypeScript('src/lib/blackbox.ts'),
  '../../lib/blackboxCatalogue': loadTypeScript('src/lib/blackboxCatalogue.ts'),
  '../../lib/copy/blackboxWizardCopy': loadTypeScript('src/lib/copy/blackboxWizardCopy.ts'),
  '../../lib/blackboxWizard': w
};

function loadReview() {
  const file = path.join(root, 'src/components/blackboxWizard/StepReview.svelte');
  const compiled = compile(fs.readFileSync(file, 'utf8'), { filename: 'StepReview.svelte', generate: 'server' });
  const js = ts.transpileModule(compiled.js.code, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022, esModuleInterop: true } }).outputText;
  const held = { exports: {} };
  const load = (request) => mods[request] ?? require(request);
  new Function('require', 'module', 'exports', js)(load, held, held.exports);
  return held.exports.default;
}

function wizardWith(logPathsText) {
  const s = w.initialState();
  return { ...s, where: 'pc', settings: { ...s.settings, name: 'web-a' }, logPathsText };
}

test('Review renders with log folders typed twice and lists each folder once', () => {
  const Review = loadReview();
  let body;
  assert.doesNotThrow(() => { body = render(Review, { props: { wizard: wizardWith('C:\\a\nC:\\A\nC:\\b\nC:\\a') } }).body; });
  assert.equal((body.match(/<code[^>]*>C:\\a<\/code>/g) ?? []).length, 1);
  assert.equal((body.match(/<code[^>]*>C:\\b<\/code>/g) ?? []).length, 1);
  assert.doesNotMatch(body, /<code[^>]*>C:\\A<\/code>/);
});

test('Review keys the log folder rows by position, so a repeated path in the payload cannot throw', () => {
  const source = fs.readFileSync(path.join(root, 'src/components/blackboxWizard/StepReview.svelte'), 'utf8');
  assert.match(source, /\{#each payload\.logPaths as path, i \(i\)\}/);
});
