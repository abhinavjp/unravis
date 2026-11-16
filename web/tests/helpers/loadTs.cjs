// Shared helper for the node --test suites: transpile a TypeScript file under web/ and load it
// as CommonJS. A './x' import inside a loaded file resolves to the x.ts file next to it, or to
// the x.svelte.ts store when only that exists. Loaded modules are cached by file name.
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const ts = require('typescript');

// web/ is two levels up from web/tests/helpers/.
const root = path.resolve(__dirname, '..', '..');
const cache = new Map();

function loadTypeScript(relativePath) {
  const filename = path.join(root, relativePath);
  if (cache.has(filename)) return cache.get(filename).exports;
  const source = fs.readFileSync(filename, 'utf8');
  const output = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022, esModuleInterop: true } }).outputText;
  const module = { exports: {} };
  cache.set(filename, module);
  const nodeRequire = createRequire(filename);
  const load = (request) => {
    if (/^\.\.?\//.test(request)) {
      const plain = path.join(path.dirname(filename), request + '.ts');
      const store = path.join(path.dirname(filename), request + '.svelte.ts');
      if (fs.existsSync(plain)) return loadTypeScript(path.relative(root, plain));
      if (fs.existsSync(store)) return loadTypeScript(path.relative(root, store));
    }
    return nodeRequire(request);
  };
  new Function('require', 'module', 'exports', '$state', output)(load, module, module.exports, (value) => value);
  return module.exports;
}

// The chip note in BlackboxSettings.svelte that maps a listed target's run state to its copy.
// Shared by the targets tests; whitespace tolerant so a harmless reformat of the component
// does not break the assertion.
const CHIP_NOTE_RE = /\{#if\s+targetRunState\(t\)\s*===\s*'stopped'\}\s*<span\s+class="bb-note">\{T\.targetsNotRunning\}<\/span>\s*\{:else\s+if\s+targetRunState\(t\)\s*===\s*'unknown'\}\s*<span\s+class="bb-note">\{T\.targetsStateUnknown\}<\/span>\s*\{\/if\}/;

module.exports = { loadTypeScript, CHIP_NOTE_RE };
