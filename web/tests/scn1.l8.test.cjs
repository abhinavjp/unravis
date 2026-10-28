const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

// Scenario 1 defect tests for lane L8 (Blackbox). Each test pins one defect so it cannot come back.

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
const read = (relative) => fs.readFileSync(path.join(root, relative), 'utf8').replace(/\r\n/g, '\n');

const bb = loadTypeScript('src/lib/blackbox.ts');
const { BB_TEXT: T } = loadTypeScript('src/lib/copy/blackboxCopy.ts');
const keyLib = loadTypeScript('src/lib/blackboxKey.ts');

// D-C-01: a keeper-held answer is told apart, copyKeyOnce does not write to the clipboard for it, and the box shows one calm line.
test('D-C-01: isKeeperHeldError matches only a refusal whose reason is keeper-held', () => {
  const held = new bb.BlackboxError({ status: 403, error: 'The key keeper holds this read key and never gives it out.', reason: 'keeper-held' });
  assert.equal(keyLib.isKeeperHeldError(held), true);
  assert.equal(keyLib.isKeeperHeldError(new bb.BlackboxError({ status: 404, error: 'x', reason: 'no-key' })), false);
  assert.equal(keyLib.isKeeperHeldError(new bb.BlackboxError({ status: 403, error: 'x', reason: 'not-this-pc' })), false);
  assert.equal(keyLib.isKeeperHeldError(new Error('keeper-held')), false);
  assert.equal(keyLib.isKeeperHeldError(null), false);
  assert.equal(keyLib.isKeeperHeldError(undefined), false);
  assert.equal(keyLib.isKeeperHeldError('keeper-held'), false);
});

test('D-C-01: copyKeyOnce answers keeper-held and never touches the clipboard for it', async () => {
  let wrote = 0;
  const held = new bb.BlackboxError({ status: 403, error: 'held', reason: 'keeper-held' });
  const got = await keyLib.copyKeyOnce(() => Promise.reject(held), async () => { wrote++; });
  assert.equal(got, 'keeper-held');
  assert.equal(wrote, 0);
  // The other answers are unchanged.
  assert.equal(await keyLib.copyKeyOnce(() => Promise.reject(new Error('refused')), async () => { wrote++; }), 'no-key');
  assert.equal(await keyLib.copyKeyOnce(() => Promise.resolve(''), async () => { wrote++; }), 'no-key');
  assert.equal(await keyLib.copyKeyOnce(() => Promise.resolve('ubk-x'), async () => { throw new Error('denied'); }), 'no-clipboard');
  assert.equal(await keyLib.copyKeyOnce(() => Promise.resolve('ubk-x'), async () => { wrote++; }), 'copied');
  assert.equal(wrote, 1);
});

test('D-C-01: the Read key box handles keeper-held with one calm line and keeps its guards', () => {
  const src = read('src/components/BlackboxReadKey.svelte');
  assert.match(src, /import \{ copyKeyOnce, isKeeperHeldError, KEY_SHOW_MS, makeRevealGuard \} from '\.\.\/lib\/blackboxKey';/);
  assert.match(src, /let held = \$state\(false\);/);
  assert.match(src, /catch \(error\) \{\s*if \(!guard\.valid\(mine\)\) return;\s*letGo\(\);\s*if \(isKeeperHeldError\(error\)\) held = true;\s*else problem = /);
  assert.match(src, /else if \(done === 'keeper-held'\) held = true;/);
  assert.equal((src.match(/disabled=\{!!busy \|\| held\}/g) ?? []).length, 2);
  assert.match(src, /\{#if held\}<p class="bb-note" role="status">\{T\.keyHeld\}<\/p>\{\/if\}/);
  assert.doesNotMatch(src, /localStorage|sessionStorage|indexedDB|app\.|console\./);
  assert.match(T.keyHeld, /key keeper/);
  assert.doesNotMatch(T.keyHeld, /[–—]| - |error|failed/i);
});
