const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

// UX-3: the Review step of the install wizard shows the app pools and services the owner picked.

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

const bb = loadTypeScript('src/lib/blackbox.ts');
const { BB_TEXT, blackboxCopyEntries } = loadTypeScript('src/lib/copy/blackboxCopy.ts');
const { lintScreenStrings } = loadTypeScript('src/lib/copy/copylint.ts');
const { deck } = loadTypeScript('src/lib/copy/deck.ts');

test('targetNamesByKind splits by kind and keeps the input order inside each group', () => {
  const targets = [
    { kind: 'service', name: 'SvcOne' },
    { kind: 'appPool', name: 'PoolA' },
    { kind: 'appPool', name: 'PoolB' },
    { kind: 'service', name: 'SvcTwo' }
  ];
  const groups = bb.targetNamesByKind(targets);
  assert.deepEqual(groups, { appPools: ['PoolA', 'PoolB'], services: ['SvcOne', 'SvcTwo'] });
  assert.deepEqual(targets.length, 4, 'the list given is not changed');
});

test('targetNamesByKind handles an empty list and a single kind', () => {
  assert.deepEqual(bb.targetNamesByKind([]), { appPools: [], services: [] });
  assert.deepEqual(bb.targetNamesByKind([{ kind: 'appPool', name: 'PoolA' }]), { appPools: ['PoolA'], services: [] });
  assert.deepEqual(bb.targetNamesByKind([{ kind: 'service', name: 'SvcOne' }]), { appPools: [], services: ['SvcOne'] });
});

test('reviewTargetsText joins the names and omits an empty group', () => {
  assert.equal(BB_TEXT.reviewTargetsText(['PoolA', 'PoolB'], ['SvcOne']), 'App pools: PoolA, PoolB. Services: SvcOne.');
  assert.equal(BB_TEXT.reviewTargetsText(['PoolA'], []), 'App pools: PoolA.');
  assert.equal(BB_TEXT.reviewTargetsText([], ['SvcOne']), 'Services: SvcOne.');
  assert.equal(BB_TEXT.reviewTargetsText([], []), 'none');
});

test('reviewTargetsNone says no app pool or service was picked', () => {
  assert.equal(typeof BB_TEXT.reviewTargetsNone, 'string');
  assert.ok(BB_TEXT.reviewTargetsNone.length > 0, 'reviewTargetsNone is not empty');
  assert.match(BB_TEXT.reviewTargetsNone, /no app pool or service/i);
  assert.match(BB_TEXT.reviewTargetsNone, /machine-wide/i);
});

test('reviewTargets is the Review row label', () => {
  assert.equal(typeof BB_TEXT.reviewTargets, 'string');
  assert.ok(BB_TEXT.reviewTargets.length > 0);
});

test('the review copy passes the screen-string lint', () => {
  const entries = blackboxCopyEntries();
  assert.ok(entries.some((e) => e.id === 'blackbox.reviewTargets'), 'reviewTargets is in the lint list');
  assert.ok(entries.some((e) => e.id === 'blackbox.reviewTargetsNone'), 'reviewTargetsNone is in the lint list');
  assert.ok(entries.some((e) => e.id.startsWith('blackbox.reviewTargetsText')), 'reviewTargetsText is sampled for the lint');
  assert.deepEqual(lintScreenStrings(entries, deck.terms), []);
});
