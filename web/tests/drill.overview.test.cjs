const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

// Drill: where the time went, slow groups and server pressure (pure).

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

const { whereTimeWent, slowLines, pressureLines, formatMs, callFacts } = loadTypeScript('src/lib/drill/overview.ts');

const perfWith = (over = {}) => ({
  hitCount: 1, filterTotalMs: 10, sqlCount: 2, sqlMs: 100, redisCount: 1, redisMs: 20,
  httpCount: 1, httpMs: 30,
  filters: [], steps: [],
  slowSql: [{ count: 2, ms: 90 }],
  slowHttp: [{ count: 1, ms: 30 }, { count: 3, ms: 12 }],
  pressure: [{ name: 'cpu', value: 0.5 }, { name: 'mem', value: null }],
  textStored: null,
  ...over
});

const partOf = (result, key) => result.parts.find((p) => p.key === key);

test('an old row without perf gives null', () => {
  assert.equal(whereTimeWent(null, 500), null);
  assert.deepEqual(pressureLines(null), []);
});

test('only the parts recorded per call are listed: filters, then everything else', () => {
  const result = whereTimeWent(perfWith(), 400);
  assert.deepEqual(result.parts.map((p) => p.key), ['filters', 'other']);
  assert.deepEqual(partOf(result, 'filters'), { key: 'filters', ms: 10, share: 3 });
  assert.deepEqual(partOf(result, 'other'), { key: 'other', ms: 390, share: 98 });
});

test('SQL, Redis and HTTP time are never listed, even when the record has them', () => {
  const keys = whereTimeWent(perfWith(), 400).parts.map((p) => p.key);
  for (const gone of ['sql', 'redis', 'http']) assert.ok(!keys.includes(gone), gone);
});

test('a filter time larger than the duration gives other 0', () => {
  const result = whereTimeWent(perfWith({ filterTotalMs: 500 }), 100);
  assert.equal(partOf(result, 'other').ms, 0);
  assert.equal(partOf(result, 'other').share, 0);
});

test('a missing filterTotalMs stays null and is not counted as 0', () => {
  const result = whereTimeWent(perfWith({ filterTotalMs: null }), 400);
  assert.equal(partOf(result, 'filters').ms, null);
  assert.equal(partOf(result, 'filters').share, null);
  assert.equal(partOf(result, 'other').ms, null, 'other is not recorded when a known part is missing');
  assert.equal(partOf(result, 'other').share, null);
});

test('durationMs null gives null shares and null other', () => {
  const result = whereTimeWent(perfWith(), null);
  assert.equal(partOf(result, 'filters').ms, 10);
  assert.equal(partOf(result, 'filters').share, null);
  assert.equal(partOf(result, 'other').ms, null);
  assert.equal(partOf(result, 'other').share, null);
});

test('durationMs 0 gives null shares', () => {
  const result = whereTimeWent(perfWith(), 0);
  assert.equal(partOf(result, 'filters').share, null);
  assert.equal(partOf(result, 'other').share, null);
});

test('slowLines keeps server order with index', () => {
  assert.deepEqual(slowLines([{ count: 2, ms: 90 }, { count: null, ms: 5 }]), [
    { index: 0, count: 2, ms: 90 },
    { index: 1, count: null, ms: 5 }
  ]);
  assert.deepEqual(slowLines([]), []);
});

test('pressureLines returns name and value', () => {
  assert.deepEqual(pressureLines(perfWith()), [
    { name: 'cpu', value: 0.5 },
    { name: 'mem', value: null }
  ]);
});

test('formatMs rounds first, then picks the unit', () => {
  assert.equal(formatMs(0), '0 ms');
  assert.equal(formatMs(0.4), '0 ms');
  assert.equal(formatMs(999.4), '999 ms');
  assert.equal(formatMs(999.6), '1.0 s');
  assert.equal(formatMs(1500), '1.5 s');
  assert.equal(formatMs(120000), '120.0 s');
});

const filtersOnly = () => perfWith({
  hitCount: null, sqlCount: null, sqlMs: null, redisCount: null, redisMs: null, httpCount: null, httpMs: null,
  filterTotalMs: 12, filters: [{ name: 'Auth', ms: 7 }], slowSql: [], slowHttp: [], pressure: [], textStored: null
});

test('with only the filter time known: filters keep their time, everything else is what is left', () => {
  const result = whereTimeWent(filtersOnly(), 400);
  assert.deepEqual(result.parts.map((p) => p.key), ['filters', 'other']);
  assert.deepEqual(partOf(result, 'filters'), { key: 'filters', ms: 12, share: 3 });
  assert.deepEqual(partOf(result, 'other'), { key: 'other', ms: 388, share: 97 });
});

const fact = (facts, key) => facts.find((f) => f.key === key).value;

test('callFacts gives endpoint, status code, start time and total time in that order', () => {
  const facts = callFacts({
    controller: 'Orders', action: 'Create', url: '/api/orders', status: '500',
    initiatedAt: '2026-10-24T15:19:37.000Z', durationMs: 1500
  }, 'UTC');
  assert.deepEqual(facts.map((f) => f.key), ['endpoint', 'status', 'started', 'duration']);
  assert.equal(fact(facts, 'endpoint'), 'Orders / Create');
  assert.equal(fact(facts, 'status'), '500');
  assert.match(fact(facts, 'started'), /2026/);
  assert.match(fact(facts, 'started'), /15:19:37/);
  assert.equal(fact(facts, 'duration'), '1.5 s');
});

test('callFacts uses null for missing values and falls back to the url when there is no controller or action', () => {
  const facts = callFacts({ controller: '', action: ' ', url: '/api/x', status: '', initiatedAt: null, durationMs: null }, 'UTC');
  assert.equal(fact(facts, 'endpoint'), '/api/x');
  assert.equal(fact(facts, 'status'), null);
  assert.equal(fact(facts, 'started'), null);
  assert.equal(fact(facts, 'duration'), null);
  const none = callFacts({ controller: 'A', action: '', url: '', status: '200', initiatedAt: 'not a date', durationMs: -1 }, 'UTC');
  assert.equal(fact(none, 'endpoint'), 'A');
  assert.equal(fact(none, 'started'), null);
  assert.equal(fact(none, 'duration'), null);
  assert.equal(fact(callFacts({ controller: '', action: '', url: '', status: '', initiatedAt: null, durationMs: 0 }, 'UTC'), 'duration'), '0 ms');
});
