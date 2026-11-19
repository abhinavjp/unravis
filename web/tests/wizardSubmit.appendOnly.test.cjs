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

const sub = loadTypeScript('src/lib/wizardSubmit.ts');
const sources = loadTypeScript('src/lib/wizardSources.ts');

function fakeApi() {
  const log = [];
  const none = async () => { throw new Error('unexpected call'); };
  return {
    log,
    createCase: async () => { log.push('createCase'); return { id: 'new' }; },
    putEnvironments: async () => { log.push('putEnvironments'); return []; },
    postSourceTag: async () => { log.push('postSourceTag'); return {}; },
    testConnection: none, addAuditSource: none,
    saveRetries: async () => { log.push('saveRetries'); return {}; },
    startImports: none, watchJob: none, importFile: none
  };
}

test('adding to an existing case never replaces its environments, makes a case or rewrites the retry list', async () => {
  const api = fakeApi();
  const state = sub.blankSubmitState();
  state.caseId = 'existing-case';
  const redis = sources.makeSource('redis', 'cache.local:6379', 'Cache');
  const input = { appendOnly: true, title: '', envs: ['prod', 'uat'], sources: [redis], window: null, scanId: '', files: {}, ackText: '', host: null };
  const result = await sub.submitWizard(input, api, state);
  assert.equal(result.caseId, 'existing-case');
  assert.ok(!api.log.includes('putEnvironments'), 'calls: ' + api.log.join(','));
  assert.ok(!api.log.includes('createCase'), 'calls: ' + api.log.join(','));
  assert.ok(!api.log.includes('saveRetries'), 'calls: ' + api.log.join(','));
  assert.ok(api.log.includes('postSourceTag'), 'the source is still tagged: ' + api.log.join(','));
});

test('the same run without appendOnly does replace the environments (control)', async () => {
  const api = fakeApi();
  const state = sub.blankSubmitState();
  state.caseId = 'existing-case';
  const input = { title: 'x', envs: ['prod'], sources: [], window: null, scanId: '', files: {}, ackText: '', host: null };
  await sub.submitWizard(input, api, state);
  assert.ok(api.log.includes('putEnvironments'));
});
