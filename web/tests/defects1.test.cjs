const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

// Open defects 1: one primary button on Case home, the code message clears, a wrong case key is a 401, a ratio shows as a percentage.

const root = path.resolve(__dirname, '..');
function loadTypeScript(relativePath, cache = new Map()) {
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
      if (fs.existsSync(plain)) return loadTypeScript(path.relative(root, plain), cache);
      if (fs.existsSync(store)) return loadTypeScript(path.relative(root, store), cache);
    }
    return nodeRequire(request);
  };
  new Function('require', 'module', 'exports', '$state', output)(load, module, module.exports, (value) => value);
  return module.exports;
}
const read = (relative) => fs.readFileSync(path.join(root, relative), 'utf8');

const TOKEN = 'link-session-token-0123456789abcdef';
const ID = 'k7m2qx4a' + 'abcdefghij' + 'klmnopqr';
const PASS = 'unravis demo case key';

// Stand-ins for the browser globals the api modules read, and a fetch that answers from `responses`.
async function withApi(responses, fn) {
  const names = ['location', 'history', 'sessionStorage', 'fetch'];
  const saved = Object.fromEntries(names.map((name) => [name, Object.getOwnPropertyDescriptor(globalThis, name)]));
  const items = new Map([['unravis.session', TOKEN]]);
  const values = {
    location: { hash: '', search: '', pathname: '/' },
    history: { state: null, replaceState: () => {} },
    sessionStorage: { getItem: (k) => (items.has(k) ? items.get(k) : null), setItem: (k, v) => { items.set(k, String(v)); }, removeItem: (k) => { items.delete(k); } },
    fetch: async () => {
      const next = responses.shift() ?? { status: 200, body: {} };
      return new Response(JSON.stringify(next.body ?? {}), { status: next.status, headers: { 'Content-Type': 'application/json' } });
    }
  };
  for (const name of names) Object.defineProperty(globalThis, name, { configurable: true, writable: true, value: values[name] });
  try {
    return await fn({ items });
  } finally {
    for (const name of names) {
      if (saved[name]) Object.defineProperty(globalThis, name, saved[name]);
      else delete globalThis[name];
    }
  }
}

test('Defect 1: Case home has one primary button; Run checks is outlined', () => {
  const panel = read('src/lib/components/HealthPanel.svelte');
  assert.doesNotMatch(panel, /primary-button/, 'the health panel offers no filled button');
  assert.match(panel, /class="secondary-button"[^>]*onclick=\{run\}/);
  const home = read('src/lib/components/CaseHome.svelte');
  const block = home.slice(home.indexOf('id="home-analysis"'), home.indexOf('id="home-erase"'));
  assert.equal((block.match(/class="primary-button"/g) ?? []).length, 1, 'Open the analysis is the one main button');
});

test('Defect 4: the "That code does not match" line clears as soon as the code is edited', () => {
  const card = read('src/components/EnterCase.svelte');
  assert.match(card, /bind:value=\{code\}[^>]*oninput=\{\(\) => \{ codeProblem = ''; \}\}/);
});

test('Defect 3: a wrong case key answers 401 wrong-case-key and keeps the session', async () => {
  await withApi([{ status: 401, body: { reason: 'wrong-case-key' } }], async ({ items }) => {
    const api = loadTypeScript('src/lib/caseKeyApi.ts');
    const caseKey = loadTypeScript('src/lib/caseKey.ts');
    await assert.rejects(() => api.unlockCase(ID, PASS), (error) => {
      assert.equal(error.status, 401);
      assert.equal(error.reason, 'wrong-case-key');
      assert.equal(caseKey.caseKeyFailureOf(error).kind, 'wrong-key');
      assert.equal(api.isCaseKeyRefusal(error), true);
      return true;
    });
    assert.equal(items.get('unravis.session'), TOKEN, 'a wrong key is not a refused session');
  });
});

test('Defect 3: a locked case with no key attempt stays 423 case-locked', async () => {
  await withApi([{ status: 423, body: { reason: 'case-locked' } }], async () => {
    const api = loadTypeScript('src/lib/caseKeyApi.ts');
    await assert.rejects(() => api.getCaseKeyStatus(ID), (error) => error.status === 423 && api.isCaseKeyRefusal(error));
  });
});

test('Defect 3: a refused session (401, any other reason) is still not a case key answer', async () => {
  await withApi([], async () => {
    const api = loadTypeScript('src/lib/caseKeyApi.ts');
    assert.equal(api.isCaseKeyRefusal({ status: 401, reason: 'no_session' }), false);
  });
});

test('Defect 3: the shared case client keeps the session on a 401 that names a wrong case key, and forgets it otherwise', async () => {
  await withApi([{ status: 401, body: { reason: 'wrong-case-key' } }, { status: 401, body: { reason: 'something-else' } }], async ({ items }) => {
    const cases = loadTypeScript('src/lib/cases.ts');
    await assert.rejects(() => cases.send('/cases/' + ID), (error) => error.status === 401 && error.reason === 'wrong-case-key');
    assert.equal(items.get('unravis.session'), TOKEN);
    await assert.rejects(() => cases.send('/cases/' + ID), (error) => error.reason === 'no_session');
    assert.equal(items.has('unravis.session'), false);
  });
});
