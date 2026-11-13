const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

// Drill: defensive parser for the server's perf block.

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

const { parsePerf } = loadTypeScript('src/lib/drill/perf.ts');

const allNullScalars = {
  hitCount: null, filterTotalMs: null, sqlCount: null, sqlMs: null,
  redisCount: null, redisMs: null, httpCount: null, httpMs: null,
};

test('null input gives null', () => {
  assert.equal(parsePerf(null), null);
  assert.equal(parsePerf(undefined), null);
  assert.equal(parsePerf('perf'), null);
  assert.equal(parsePerf([]), null);
});

test('empty object gives every scalar null and every list empty', () => {
  assert.deepEqual(parsePerf({}), {
    ...allNullScalars,
    filters: [], steps: [], slowSql: [], slowHttp: [], pressure: [], textStored: null,
  });
});

test('a string ms is null, not 0', () => {
  const perf = parsePerf({ sqlMs: '12', filters: [{ name: 'f', ms: '5' }] });
  assert.equal(perf.sqlMs, null);
  assert.deepEqual(perf.filters, [{ name: 'f', ms: null }]);
});

test('a negative number is null', () => {
  const perf = parsePerf({ hitCount: -1, redisMs: -0.5 });
  assert.equal(perf.hitCount, null);
  assert.equal(perf.redisMs, null);
});

test('five slowSql entries are cut to three', () => {
  const slowSql = [1, 2, 3, 4, 5].map((n) => ({ count: n, ms: n * 10 }));
  const perf = parsePerf({ slowSql });
  assert.equal(perf.slowSql.length, 3);
  assert.deepEqual(perf.slowSql[2], { count: 3, ms: 30 });
});

test('Infinity is null', () => {
  const perf = parsePerf({ sqlCount: Infinity, httpMs: -Infinity, filterTotalMs: NaN });
  assert.equal(perf.sqlCount, null);
  assert.equal(perf.httpMs, null);
  assert.equal(perf.filterTotalMs, null);
});

test('list entries without a name are dropped and names are cut to 100 characters', () => {
  const perf = parsePerf({
    steps: [{ ms: 1 }, { name: 'ok', ms: 2 }, { name: 'x'.repeat(150), ms: 3 }],
    pressure: [{ name: '', value: 1 }, { name: 'cpu', value: 0 }],
  });
  assert.equal(perf.steps.length, 2);
  assert.equal(perf.steps[1].name.length, 100);
  assert.deepEqual(perf.pressure, [{ name: 'cpu', value: 0 }]);
});

test('textStored keeps booleans only', () => {
  assert.equal(parsePerf({ textStored: true }).textStored, true);
  assert.equal(parsePerf({ textStored: 'yes' }).textStored, null);
});
