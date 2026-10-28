const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

// Scenario 1 fixes for the app shell and case screens (lane L1). Each packet appends its own tests below.

const root = path.resolve(__dirname, '..');
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
const read = (relative) => fs.readFileSync(path.join(root, relative), 'utf8');

test('D-A-15: set-up opens from a case page and returns there', () => {
  const flow = loadTypeScript('src/lib/screenFlow.ts');
  assert.equal(flow.canGo('case', 'setup'), true);
  assert.equal(flow.canGo('setup', 'case'), true);
  assert.equal(flow.canGo('running', 'setup'), false);
  assert.equal(flow.canGo('setup', 'running'), false);
  assert.equal(flow.canGo('unlock', 'setup'), false);
  assert.equal(flow.setupReturn('case'), 'case');
  assert.equal(flow.setupReturn('start'), 'start');
  assert.equal(flow.setupReturn(null), 'start');
  assert.equal(flow.setupReturn('running'), 'start');
});

test('D-A-15: go() sends set-up back to its origin and openKeySetup records it', () => {
  const src = read('src/lib/appState.svelte.ts');
  const go = src.slice(src.indexOf('export function go(to: Screen)'), src.indexOf('// Reads the key state once the app info is in'));
  assert.match(go, /if \(screen === 'setup' && to === 'start' && setupOrigin\) to = setupReturn\(setupOrigin\);\s*const move = moveScreen\(screen, to\);/);
  assert.match(go, /screen = move\.screen;\s*if \(move\.screen !== 'setup'\) setupOrigin = null;/);
  assert.match(src, /export function openKeySetup\(\) \{\s*const from = screen;\s*if \(go\('setup'\)\) setupOrigin = from === 'case' \? 'case' : null;\s*\}/);
  assert.match(src, /unlockOrigin = null;\s*setupOrigin = null;\s*lockError = '';/);
  assert.match(src, /go\('start'\);\s*go\('setup'\);/);
});
