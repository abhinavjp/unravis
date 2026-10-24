const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

const root = path.resolve(__dirname, '..');
function loadTypeScript(relativePath) {
  const filename = path.join(root, relativePath);
  const source = fs.readFileSync(filename, 'utf8');
  const output = ts.transpileModule(source, {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022, esModuleInterop: true }
  }).outputText;
  const module = { exports: {} };
  const nodeRequire = createRequire(filename);
  const load = (request) => /^\.\.?\//.test(request) && fs.existsSync(path.join(path.dirname(filename), request + '.ts'))
    ? loadTypeScript(path.relative(root, path.join(path.dirname(filename), request + '.ts')))
    : nodeRequire(request);
  new Function('require', 'module', 'exports', output)(load, module, module.exports);
  return module.exports;
}

const TOKEN = 'link-session-token-0123456789abcdef+/=';

function memoryStorage(initial = {}) {
  const items = new Map(Object.entries(initial));
  return {
    items,
    getItem: (key) => (items.has(key) ? items.get(key) : null),
    setItem: (key, value) => { items.set(key, String(value)); },
    removeItem: (key) => { items.delete(key); }
  };
}

// browser installs stand-ins for the browser globals api.ts reads at load time,
// loads a fresh copy of the module, runs fn, and restores the real globals.
async function browser({ hash = '', search = '', storage = memoryStorage(), responses = [] }, fn) {
  const names = ['location', 'history', 'sessionStorage', 'fetch'];
  const saved = Object.fromEntries(names.map((name) => [name, Object.getOwnPropertyDescriptor(globalThis, name)]));
  const replaced = [];
  const calls = [];
  const values = {
    location: { hash, search, pathname: '/' },
    history: { state: null, replaceState: (state, title, url) => replaced.push(url) },
    sessionStorage: storage,
    fetch: async (url, init) => {
      calls.push({ url, headers: new Headers(init.headers) });
      const next = responses.shift() ?? { status: 200, body: {} };
      return {
        ok: next.status >= 200 && next.status < 300, status: next.status,
        json: async () => next.body, blob: async () => 'csv', headers: { get: () => null }
      };
    }
  };
  for (const name of names) {
    if (name === 'sessionStorage' && typeof storage === 'function') {
      Object.defineProperty(globalThis, name, { configurable: true, get: storage });
    } else {
      Object.defineProperty(globalThis, name, { configurable: true, writable: true, value: values[name] });
    }
  }
  try {
    return await fn({ module: loadTypeScript('src/lib/api.ts'), calls, replaced });
  } finally {
    for (const name of names) {
      if (saved[name]) Object.defineProperty(globalThis, name, saved[name]);
      else delete globalThis[name];
    }
  }
}

test('the page never carries the session token', () => {
  const html = fs.readFileSync(path.join(root, 'index.html'), 'utf8');
  assert.doesNotMatch(html, /audit-session/);
  const api = fs.readFileSync(path.join(root, 'src/lib/api.ts'), 'utf8');
  assert.doesNotMatch(api, /querySelector|audit-session/);
  const vite = fs.readFileSync(path.join(root, 'vite.config.ts'), 'utf8');
  assert.doesNotMatch(vite, /transformIndexHtml|audit-session/);
});

test('the launch link token is used, kept for the tab and removed from the address bar', async () => {
  const storage = memoryStorage();
  await browser({ hash: '#t=' + encodeURIComponent(TOKEN), search: '?view=1', storage }, async ({ module, calls, replaced }) => {
    await module.api.info();
    assert.equal(calls.length, 1);
    assert.equal(calls[0].headers.get('X-Audit-Token'), TOKEN);
    assert.deepEqual(replaced, ['/?view=1']);
    assert.equal(storage.getItem('unravis.session'), TOKEN);
  });
});

test('a reload without the fragment reuses the token kept for this tab', async () => {
  const storage = memoryStorage({ 'unravis.session': TOKEN });
  await browser({ storage }, async ({ module, calls, replaced }) => {
    await module.api.sources();
    assert.equal(calls[0].headers.get('X-Audit-Token'), TOKEN);
    assert.deepEqual(replaced, []);
  });
});

test('an unrelated fragment is left alone', async () => {
  await browser({ hash: '#section' }, async ({ module, calls, replaced }) => {
    await assert.rejects(module.api.info(), (error) => error.code === 'no_session');
    assert.equal(calls.length, 0);
    assert.deepEqual(replaced, []);
  });
});

test('without a token no request is sent and the UI says how to recover', async () => {
  await browser({}, async ({ module, calls }) => {
    const expected = 'Open Unravis from the link it printed when it started, or restart it.';
    assert.equal(module.NO_SESSION_MESSAGE, expected);
    await assert.rejects(module.api.info(), (error) => error instanceof module.ApiError && error.message === expected && error.status === 401);
    await assert.rejects(module.api.export({ kind: 'all' }, ''), (error) => error.message === expected);
    assert.equal(calls.length, 0);
  });
});

test('a refused token is forgotten and the next call does not send it again', async () => {
  const storage = memoryStorage({ 'unravis.session': TOKEN });
  await browser({ storage, responses: [{ status: 401, body: { error: 'missing or invalid session token' } }] }, async ({ module, calls }) => {
    await assert.rejects(module.api.info(), (error) => error.message === module.NO_SESSION_MESSAGE);
    assert.equal(storage.getItem('unravis.session'), null);
    await assert.rejects(module.api.info(), (error) => error.code === 'no_session');
    assert.equal(calls.length, 1);
  });
});

test('a refused export token is forgotten too', async () => {
  const storage = memoryStorage({ 'unravis.session': TOKEN });
  await browser({ storage, responses: [{ status: 401, body: {} }] }, async ({ module }) => {
    await assert.rejects(module.api.export({ kind: 'all' }, ''), (error) => error.message === module.NO_SESSION_MESSAGE);
    assert.equal(storage.getItem('unravis.session'), null);
  });
});

test('blocked session storage does not stop the launch link from working', async () => {
  const blocked = () => { throw new Error('SecurityError'); };
  await browser({ hash: '#t=' + encodeURIComponent(TOKEN), storage: blocked }, async ({ module, calls, replaced }) => {
    await module.api.info();
    assert.equal(calls[0].headers.get('X-Audit-Token'), TOKEN);
    assert.deepEqual(replaced, ['/']);
  });
  const failingWrites = { getItem: () => null, setItem: () => { throw new Error('QuotaExceededError'); }, removeItem: () => {} };
  await browser({ hash: '#t=' + encodeURIComponent(TOKEN), storage: failingWrites }, async ({ module, calls }) => {
    await module.api.info();
    assert.equal(calls[0].headers.get('X-Audit-Token'), TOKEN);
  });
});
