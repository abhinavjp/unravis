const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

// Review 1 of the scenario fixes. Reads the source like the other scn1 tests; the erase call runs against a fetch stand-in.

const root = path.resolve(__dirname, '..');
function loadTypeScript(relativePath, cache = new Map()) {
  const filename = path.join(root, relativePath);
  if (cache.has(filename)) return cache.get(filename).exports;
  const output = ts.transpileModule(fs.readFileSync(filename, 'utf8'), { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022, esModuleInterop: true } }).outputText;
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
  new Function('require', 'module', 'exports', output)(load, module, module.exports);
  return module.exports;
}
const read = (relative) => fs.readFileSync(path.join(root, relative), 'utf8');

const TOKEN = 'link-session-token-0123456789abcdef';
const ID = 'k7m2qx4a' + 'abcdefghij' + 'klmnopqr';

async function withApi(fn) {
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
      const make = () => ({ ok: true, status: 200, json: async () => ({}), text: async () => '{}', headers: { get: () => null } });
      return { ...make(), clone: make };
    }
  };
  for (const name of names) Object.defineProperty(globalThis, name, { configurable: true, writable: true, value: values[name] });
  try {
    return await fn({ calls });
  } finally {
    for (const name of names) {
      if (saved[name]) Object.defineProperty(globalThis, name, saved[name]);
      else delete globalThis[name];
    }
  }
}

test('Review1-3: erase sends the belief in X-Expect-State, and sends none when there is none', async () => {
  await withApi(async ({ calls }) => {
    const cases = loadTypeScript('src/lib/cases.ts');
    await cases.eraseCase(ID, ' K7M2-QX4A ', 'archived');
    await cases.eraseCase(ID);
    assert.equal(calls[0].method, 'DELETE');
    assert.equal(calls[0].headers.get('X-Expect-State'), 'archived');
    assert.deepEqual(JSON.parse(calls[0].body), { confirm: 'K7M2-QX4A' });
    assert.equal(calls[1].headers.get('X-Expect-State'), null);
  });
});

test('Review1-3: every erase caller passes the case state it shows', () => {
  assert.match(read('src/components/EraseCase.svelte'), /eraseCase\(caseId, entered, believed, controller\.signal\)/);
  assert.match(read('src/lib/components/CaseHome.svelte'), /<EraseCase [^>]*believed=\{info \? believedState\(info\) : undefined\}/);
  assert.match(read('src/lib/components/CasePanel.svelte'), /eraseCase\(id, entered, row \? believedState\(row\) : undefined, controller\?\.signal\)/);
  assert.match(read('src/lib/components/ArchivedList.svelte'), /eraseCase\(id, undefined, row \? believedState\(row\) : undefined\)/);
  assert.match(read('src/components/StartScreen.svelte'), /eraseCase\(id, undefined, row \? believedState\(row\) : undefined\)/);
});

const keeperCard = read('src/components/sources/KeeperCard.svelte');

test('Review1-4: "Yes remove" moves the focus to the dialog box before the buttons are disabled', () => {
  assert.match(keeperCard, /if \(kind === 'remove'\) dialogEl\?\.focus\(\);\s*busy = kind;/);
  assert.match(keeperCard, /role="alertdialog"[^>]*tabindex="-1"/);
});

test('Review1-5: a refused removal shows its alert only after the question has closed', () => {
  assert.match(keeperCard, /failure = error instanceof Error && error\.message \? error\.message : KEEPER_TEXT\.failed;/);
  assert.match(keeperCard, /\} finally \{ busy = ''; asking = false; back = kind === 'remove'; \}\s*await tick\(\);\s*problem = failure;\s*await focusCard\(\);/);
  assert.doesNotMatch(keeperCard, /catch \(error\) \{\s*problem = /);
});

test('Review1-6: the remove question is dropped when the view leaves "moved"', () => {
  assert.match(keeperCard, /\$effect\(\(\) => \{\s*if \(view !== 'moved'\) asking = false;\s*\}\);/);
});
