// P3 fix pass: the page side of the UI review findings (drawer focus, case home after a move, master key wording, folders).
// Each test pins one finding so it cannot come back.
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
const read = (relative) => fs.readFileSync(path.join(root, relative), 'utf8');

// UI major 1: the four archive drawers take focus when they open (so Escape and Tab work from the first key press)
// and give it back to the button that opened them.
test('the archive, free and restore drawers take focus on open and return it', () => {
  for (const name of ['ArchiveDrawer', 'FreeDrawer', 'RestoreDrawer']) {
    const text = read('src/lib/components/' + name + '.svelte');
    const tag = text.match(/<Drawer [^>]*>/)[0];
    assert.match(tag, /\bfocusOnOpen\b/, name + ' must take focus');
    assert.match(tag, /\breturnFocus\b/, name + ' must return focus');
  }
  const drawer = read('src/components/Drawer.svelte');
  assert.match(drawer, /event\.key === 'Escape'/, 'the shared drawer closes on Escape');
  assert.match(drawer, /focusOnOpen\) titleElement\?\.focus/, 'and puts focus inside on open');
});

test('closing a drawer lets focus go back to the trigger, and falls back to the case name only when the trigger is gone', () => {
  const home = read('src/lib/components/CaseHome.svelte');
  const fn = home.match(/function closeArchiveDrawer\(\) \{[\s\S]*?\n  \}/)[0];
  assert.match(fn, /document\.activeElement/);
  assert.match(fn, /document\.body/);
  assert.match(fn, /headingEl\?\.focus\(\)/);
});
