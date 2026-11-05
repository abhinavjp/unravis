const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

const root = path.resolve(__dirname, '..');
const loaded = new Map();
function loadTypeScript(relativePath) {
  const filename = path.join(root, relativePath);
  if (loaded.has(filename)) return loaded.get(filename).exports;
  const source = fs.readFileSync(filename, 'utf8');
  const output = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022, esModuleInterop: true } }).outputText;
  const module = { exports: {} };
  loaded.set(filename, module);
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
  new Function('require', 'module', 'exports', output)(load, module, module.exports);
  return module.exports;
}

const { ApiError } = loadTypeScript('src/lib/api.ts');
const { addAuditWithFallback, singleCandidate } = loadTypeScript('src/lib/wizardAuditAdd.ts');
const sub = loadTypeScript('src/lib/wizardSubmit.ts');
const sources = loadTypeScript('src/lib/wizardSources.ts');
const copy = loadTypeScript('src/lib/copy/wizardCreateCopy.ts');

// Synthetic names only.
const missing = (collections) => new ApiError('no collection', 'collection_missing', 'source', 400, undefined, undefined, collections);
const denied = (collections) => new ApiError('not allowed', 'permission_denied', 'source', 400, undefined, undefined, collections);

test('a missing collection with one audit-looking alternative is tried once with that one', async () => {
  const calls = [];
  const add = async (collection) => { calls.push(collection); if (collection === 'ApiAuditLog') throw missing(['SystemApiAuditLog']); return { id: 's1' }; };
  const result = await addAuditWithFallback(add, 'ApiAuditLog');
  assert.deepEqual(calls, ['ApiAuditLog', 'SystemApiAuditLog']);
  assert.deepEqual(result, { kind: 'added', source: { id: 's1' }, collection: 'SystemApiAuditLog', requestedCollection: 'ApiAuditLog' });
});

test('a refused collection with one readable audit-looking alternative is tried with that one', async () => {
  const add = async (collection) => { if (collection === 'ApiAuditLog') throw denied(['SystemApiAuditLog']); return { id: 's1' }; };
  assert.equal((await addAuditWithFallback(add, 'ApiAuditLog')).collection, 'SystemApiAuditLog');
  assert.equal((await addAuditWithFallback(add, 'ApiAuditLog')).requestedCollection, 'ApiAuditLog');
});

test('a name the person typed is never swapped for a different one, even when one alternative is offered', async () => {
  const names = [];
  const add = async (collection) => { names.push(collection); throw missing(['SystemApiAuditLog']); };
  await assert.rejects(() => addAuditWithFallback(add, 'OrdersAudit'), (error) => error instanceof ApiError && error.code === 'collection_missing');
  assert.deepEqual(names, ['OrdersAudit'], 'no second try after a typed name failed');
});

test('the same name in the spelling the server uses is tried once, even when it was typed', async () => {
  const calls = [];
  const add = async (collection) => { calls.push(collection); if (collection === 'ordersaudit') throw missing(['OrdersAudit']); return { id: 's2' }; };
  const result = await addAuditWithFallback(add, 'ordersaudit');
  assert.deepEqual(calls, ['ordersaudit', 'OrdersAudit']);
  assert.deepEqual(result, { kind: 'added', source: { id: 's2' }, collection: 'OrdersAudit', requestedCollection: 'ordersaudit' });
});

test('several alternatives are a choice for the person: the failure is kept', async () => {
  let calls = 0;
  const add = async () => { calls++; throw missing(['AuditA', 'AuditB']); };
  await assert.rejects(() => addAuditWithFallback(add, 'ApiAuditLog'), (error) => error instanceof ApiError && error.code === 'collection_missing' && error.collections.length === 2);
  assert.equal(calls, 1);
});

test('no alternative, or the same name in another case, is not retried by the single-candidate rule', () => {
  assert.equal(singleCandidate(missing([]), 'ApiAuditLog'), '');
  assert.equal(singleCandidate(missing(['apiauditlog']), 'ApiAuditLog'), '');
  assert.equal(singleCandidate(new ApiError('x', 'credentials_rejected', 'source', 400, undefined, undefined, ['Other']), 'ApiAuditLog'), '');
  assert.equal(singleCandidate(new Error('x'), 'ApiAuditLog'), '');
});

test('the retry happens once, and when it fails too the FIRST failure is reported', async () => {
  const calls = [];
  const first = missing(['AuditB']);
  const second = missing(['ApiAuditLog']);
  const add = async (collection) => { calls.push(collection); throw collection === 'ApiAuditLog' ? first : second; };
  await assert.rejects(() => addAuditWithFallback(add, 'ApiAuditLog'), (error) => error === first, 'the failure about the name that was asked for wins');
  assert.deepEqual(calls, ['ApiAuditLog', 'AuditB']);
});

test('a source that already exists is the wanted end state', async () => {
  const add = async () => { throw new ApiError('already', 'source-exists', undefined, 409); };
  assert.deepEqual(await addAuditWithFallback(add, 'ApiAuditLog'), { kind: 'exists', collection: 'ApiAuditLog', requestedCollection: 'ApiAuditLog' });
});

test('any other failure passes through untouched', async () => {
  const boom = new ApiError('Request validation failed.', undefined, undefined, 400);
  await assert.rejects(() => addAuditWithFallback(async () => { throw boom; }, 'ApiAuditLog'), (error) => error === boom);
});

// ---- the screen copy for the swap and the offered names

test('the swap note and the "Did you mean" line are registered copy', () => {
  assert.equal(copy.swappedCollectionText('SystemApiAuditLog', 'ApiAuditLog'), 'Used collection SystemApiAuditLog instead of ApiAuditLog.');
  assert.equal(copy.suggestionLine(['AuditEvents', 'ApiAuditLog']), 'Did you mean: AuditEvents, ApiAuditLog?');
  assert.equal(copy.suggestionLine([]), '');
  assert.equal(copy.suggestionLine(), '');
  assert.equal(copy.suggestionLine(['1', '2', '3', '4', '5', '6']), 'Did you mean: 1, 2, 3, 4, 5?', 'at most five names');
});

// ---- the wizard names the real reason

const CONN = 'abc123def456';
const mongoSource = (name) => ({ ...sources.makeSource('mongo-database', 'mongo:' + CONN + '/' + name, name), classification: 'audit' });

async function runWith(auditError, addAnswer) {
  const api = {
    async createCase() { return { id: 'case1' }; },
    async putEnvironments() { return []; },
    async postSourceTag() { return {}; },
    async testConnection() { return {}; },
    async addAuditSource() { if (addAnswer) return addAnswer; throw auditError; },
    async startImports() { return []; },
    async watchJob() { return { kind: 'added', rows: 1 }; },
    async importFile() { return { kind: 'added', rows: 1 }; }
  };
  const input = { title: 'T', envs: [], sources: [mongoSource('orders')], window: null, scanId: '', host: null, files: {}, ackText: 'x' };
  return sub.submitWizard(input, api, sub.blankSubmitState());
}

for (const [code, reason] of [
  ['collection_missing', 'collection-missing'], ['permission_denied', 'no-access'], ['credentials_rejected', 'password-rejected'],
  ['unsupported_version', 'old-server'], ['dns_failure', 'db-unreachable'], ['connection_refused', 'db-unreachable']
]) {
  test('a 400 with code ' + code + ' is reported as ' + reason + ', not as a generic bad request', async () => {
    const result = await runWith(new ApiError('x', code, 'source', 400));
    assert.equal(result.failures.length, 1);
    assert.deepEqual([result.failures[0].step, result.failures[0].reason], ['audit-source', reason]);
    assert.notEqual(copy.reasonLine(reason), copy.reasonLine('bad-request'));
    assert.notEqual(copy.reasonLine(reason), 'The server could not finish it.');
  });
}

test('the offered collection names travel with a failed audit step, capped at five', async () => {
  const result = await runWith(new ApiError('no collection', 'collection_missing', 'source', 400, undefined, undefined, ['A', 'B', 'C', 'D', 'E', 'F']));
  assert.deepEqual(result.failures[0].suggestions, ['A', 'B', 'C', 'D', 'E']);
});

test('a failed audit step without offered names carries none', async () => {
  const result = await runWith(new ApiError('x', 'collection_missing', 'source', 400));
  assert.equal('suggestions' in result.failures[0], false);
});

test('a bare 400 stays the generic bad request', async () => {
  const result = await runWith(new ApiError('Request validation failed.', undefined, undefined, 400));
  assert.equal(result.failures[0].reason, 'bad-request');
});

test('the new reasons are short ids the case can keep in its retry list', () => {
  for (const reason of ['collection-missing', 'no-access', 'password-rejected', 'old-server', 'db-unreachable']) {
    assert.match(reason, /^[a-z0-9-]{1,40}$/);
  }
});
