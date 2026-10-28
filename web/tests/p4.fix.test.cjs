const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

// P4 fix pass: a wrong case key does not lock the app (real Response objects), and the other P4 review fixes.

const root = path.resolve(__dirname, '..');
function loadTypeScript(relativePath, runes = false, cache = new Map()) {
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
      if (fs.existsSync(plain)) return loadTypeScript(path.relative(root, plain), runes, cache);
      if (fs.existsSync(store)) return loadTypeScript(path.relative(root, store), runes, cache);
    }
    return nodeRequire(request);
  };
  new Function('require', 'module', 'exports', '$state', output)(load, module, module.exports, runes ? (value) => value : undefined);
  return module.exports;
}
const read = (relative) => fs.readFileSync(path.join(root, relative), 'utf8');

const TOKEN = 'link-session-token-0123456789abcdef';
const ID = 'k7m2qx4a' + 'abcdefghij' + 'klmnopqr';
const PASS = 'unravis demo case key';

// Runs `fn` with stand-ins for the browser globals api.ts reads at load time, and a fetch that answers from `responses`.
async function withApi(responses, fn) {
  const names = ['location', 'history', 'sessionStorage', 'fetch'];
  const saved = Object.fromEntries(names.map((name) => [name, Object.getOwnPropertyDescriptor(globalThis, name)]));
  const calls = [];
  const items = new Map([['unravis.session', TOKEN]]);
  const values = {
    location: { hash: '', search: '', pathname: '/' },
    history: { state: null, replaceState: () => {} },
    sessionStorage: { getItem: (k) => (items.has(k) ? items.get(k) : null), setItem: (k, v) => { items.set(k, String(v)); }, removeItem: (k) => { items.delete(k); } },
    fetch: async (url, init = {}) => {
      calls.push({ url: String(url), method: init.method ?? 'GET', headers: new Headers(init.headers), body: init.body });
      const next = responses.shift() ?? { status: 200, body: {} };
      if (next.network) throw new TypeError('Failed to fetch');
      const make = () => ({ ok: next.status >= 200 && next.status < 300, status: next.status, json: async () => next.body, text: async () => JSON.stringify(next.body ?? {}), headers: { get: () => null } });
      return new Response(JSON.stringify(next.body ?? {}), { status: next.status, headers: { 'Content-Type': 'application/json' } });
    }
  };
  for (const name of names) Object.defineProperty(globalThis, name, { configurable: true, writable: true, value: values[name] });
  try {
    return await fn({ calls, items });
  } finally {
    for (const name of names) {
      if (saved[name]) Object.defineProperty(globalThis, name, saved[name]);
      else delete globalThis[name];
    }
  }
}


const shared = new Map();
const lockSignal = loadTypeScript('src/lib/lockSignal.ts', false, shared);

test('a wrong case key (423 wrong-case-key) does not tell the app the master key is locked', async () => {
  await withApi([{ status: 423, body: { reason: 'wrong-case-key' } }, { status: 423, body: { reason: 'case-locked' } }, { status: 423, body: { reason: 'locked' } }], async () => {
    let signals = 0;
    lockSignal.onCasesLocked(() => { signals++; });
    const api = loadTypeScript('src/lib/caseKeyApi.ts', false, shared);
    await assert.rejects(() => api.unlockCase(ID, PASS), (error) => error.reason === 'wrong-case-key');
    assert.equal(signals, 0, 'a wrong case key stays on the case card');
    await assert.rejects(() => api.getCaseKeyStatus(ID), (error) => error.reason === 'case-locked');
    assert.equal(signals, 0);
    await assert.rejects(() => api.getCaseKeyStatus(ID), (error) => error.reason === 'locked');
    assert.equal(signals, 1, 'a real master lock still reaches the unlock screen');
  });
});

test('signalIfAppLocked leaves a body that was already read alone', async () => {
  let signals = 0;
  lockSignal.onCasesLocked(() => { signals++; });
  const response = new Response(JSON.stringify({ reason: 'wrong-case-key' }), { status: 423 });
  await response.json();
  await lockSignal.signalIfAppLocked(response);
  assert.equal(signals, 0);
  await lockSignal.signalIfAppLocked(new Response(JSON.stringify({ reason: 'locked' }), { status: 423 }));
  assert.equal(signals, 1);
});
