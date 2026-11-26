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
  const output = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022, esModuleInterop: true } }).outputText;
  const module = { exports: {} };
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

const SENTENCE = 'Unravis could not be reached. It may have been closed or updated. Start Unravis again, then reload this page.';
const TOKEN = 'link-session-token-0123456789abcdef+/=';

// Loads a fresh api.ts with a fetch that behaves as `behave` says, a tab that holds a session token, and runs fn.
async function withFetch(behave, fn) {
  const names = ['location', 'history', 'sessionStorage', 'fetch'];
  const saved = Object.fromEntries(names.map((name) => [name, Object.getOwnPropertyDescriptor(globalThis, name)]));
  const store = new Map([['unravis.session', TOKEN]]);
  const values = {
    location: { hash: '', search: '', pathname: '/' },
    history: { state: null, replaceState: () => {} },
    sessionStorage: { getItem: (k) => (store.has(k) ? store.get(k) : null), setItem: (k, v) => { store.set(k, String(v)); }, removeItem: (k) => { store.delete(k); } },
    fetch: behave
  };
  for (const name of names) Object.defineProperty(globalThis, name, { configurable: true, writable: true, value: values[name] });
  try {
    return await fn(loadTypeScript('src/lib/api.ts'));
  } finally {
    for (const name of names) {
      if (saved[name]) Object.defineProperty(globalThis, name, saved[name]);
      else delete globalThis[name];
    }
  }
}

test('a network failure from fetch becomes one plain-words ApiError with code unreachable and status 0', async () => {
  await withFetch(async () => { throw new TypeError('Failed to fetch'); }, async (module) => {
    for (const call of [() => module.api.info(), () => module.api.sources()]) {
      await assert.rejects(call(), (error) => {
        assert.ok(error instanceof module.ApiError);
        assert.equal(error.code, 'unreachable');
        assert.equal(error.status, 0);
        assert.equal(error.message, SENTENCE);
        assert.doesNotMatch(error.message, /failed to fetch/i);
        return true;
      });
    }
  });
});

test('the other browsers\' wording of a network failure gets the same sentence', async () => {
  for (const text of ['NetworkError when attempting to fetch resource.', 'Load failed', 'Network request failed']) {
    await withFetch(async () => { throw new TypeError(text); }, async (module) => {
      await assert.rejects(module.api.info(), (error) => error.code === 'unreachable' && error.message === SENTENCE);
    });
  }
});

test('a cancel by the person keeps its own abort error and is not called unreachable', async () => {
  const controller = new AbortController();
  await withFetch((url, init) => new Promise((resolve, reject) => {
    init.signal.addEventListener('abort', () => reject(new DOMException('The operation was aborted.', 'AbortError')), { once: true });
  }), async (module) => {
    const pending = module.api.record('s', 'r', controller.signal);
    controller.abort();
    await assert.rejects(pending, (error) => error.name === 'AbortError' && !(error instanceof module.ApiError));
  });
});

test('an answer from the app (500 with its own words) is not turned into unreachable', async () => {
  await withFetch(async () => ({ ok: false, status: 500, json: async () => ({ error: 'The disk is full.' }), headers: { get: () => null } }), async (module) => {
    await assert.rejects(module.api.info(), (error) => error instanceof module.ApiError && error.status === 500 && error.message === 'The disk is full.');
  });
});

test('the export download says the same sentence when the app cannot be reached', async () => {
  await withFetch(async () => { throw new TypeError('Failed to fetch'); }, async (module) => {
    await assert.rejects(module.api.export({}, ''), (error) => error instanceof module.ApiError && error.code === 'unreachable' && error.message === SENTENCE);
  });
});

test('the start screen keeps its own wording for the new error, and a restore failure reads it as unreachable', () => {
  const { UNREACHABLE_MESSAGE, isUnreachableError } = loadTypeScript('src/lib/copy/unreachableCopy.ts');
  assert.equal(UNREACHABLE_MESSAGE, SENTENCE);
  const { classifyStartupFailure, STARTUP_TEXT } = loadTypeScript('src/lib/copy/startupCopy.ts');
  const failure = Object.assign(new Error(SENTENCE), { code: 'unreachable', status: 0 });
  const startup = classifyStartupFailure({ hadToken: true, cause: failure });
  assert.equal(startup.kind, 'unreachable');
  assert.equal(startup.message, STARTUP_TEXT.unreachable);
  assert.equal(startup.canRetry, true);
  const { classifyRestoreFailure } = loadTypeScript('src/lib/caseWorkspace.ts');
  assert.equal(classifyRestoreFailure([failure], true), 'unreachable');
  assert.equal(isUnreachableError(Object.assign(new Error('x'), { reason: 'unreachable' })), true);
  assert.equal(isUnreachableError(Object.assign(new Error('x'), { code: 'timeout', status: 0 })), false);
  assert.equal(isUnreachableError(null), false);
});

test('the unreachable sentence passes the copy lint and reads at or below grade 8.5', () => {
  const { unreachableLintEntries } = loadTypeScript('src/lib/copy/unreachableCopy.ts');
  const { lintCopy, fleschKincaidGrade, MAX_GRADE } = loadTypeScript('src/lib/copy/copylint.ts');
  const { deck } = loadTypeScript('src/lib/copy/deck.ts');
  const entries = unreachableLintEntries();
  assert.deepEqual(lintCopy(entries, deck.terms), []);
  for (const entry of entries) {
    assert.ok(fleschKincaidGrade(entry.text) <= MAX_GRADE, entry.id + ' ' + fleschKincaidGrade(entry.text));
    assert.doesNotMatch(entry.text, /\b(because|caused?|due to|led to|waiting)\b/i);
  }
});
