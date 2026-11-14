const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

// Slow call target: build from a slot, encode, strict parse.

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

const { makeCallTarget, withCall, encodeCallTarget, parseCallTarget } = loadTypeScript('src/lib/drill/callTarget.ts');

const window = {
  start: '2026-01-01T10:00:00.000Z',
  end: '2026-01-01T11:00:00.000Z',
  axisStart: '2026-01-01T10:00:00.000Z',
  axisEnd: '2026-01-01T11:00:00.000Z',
  bucketSeconds: 60
};
const CASE = 'abcdefghijklmnopqrstuvwxyz'.slice(0, 20) + '234567';
const lane = { sourcesRead: [{ sourceId: 'a', label: 'A' }, { sourceId: 'b', label: 'B' }] };

function good() {
  return { caseId: CASE, slot: 3, start: '2026-01-01T10:03:00.000Z', end: '2026-01-01T10:04:00.000Z', sourceIds: ['a', 'b'] };
}
function parseOf(obj) {
  return parseCallTarget(JSON.stringify(obj));
}

test('makeCallTarget builds the slot bounds and the source ids', () => {
  assert.equal(CASE.length, 26);
  const t = makeCallTarget(CASE, window, 3, lane, []);
  assert.deepEqual(t, good());
});

test('makeCallTarget returns null for an undefined lane, a bad slot or a bad case id', () => {
  assert.equal(makeCallTarget(CASE, window, 3, undefined, []), null);
  assert.equal(makeCallTarget(CASE, window, -1, lane, []), null);
  assert.equal(makeCallTarget(CASE, { ...window, axisStart: 'nope' }, 3, lane, []), null);
  assert.equal(makeCallTarget('SHORT', window, 3, lane, []), null);
  assert.equal(makeCallTarget(CASE.toUpperCase(), window, 3, lane, []), null);
});

test('round trip: parse(encode(t)) deep-equals t, with and without a call', () => {
  const t = good();
  assert.deepEqual(parseCallTarget(encodeCallTarget(t)), t);
  const withIt = withCall(t, { sourceId: 'a', id: 'r1' });
  assert.deepEqual(parseCallTarget(encodeCallTarget(withIt)), withIt);
});

test('withCall adds the call and does not change its input', () => {
  const t = good();
  const next = withCall(t, { sourceId: 'a', id: 'r1' });
  assert.deepEqual(next.call, { sourceId: 'a', id: 'r1' });
  assert.equal('call' in t, false);
  assert.notEqual(next, t);
  assert.notEqual(next.sourceIds, t.sourceIds);
});

test('parseCallTarget rejects null, long text, bad JSON and non-objects', () => {
  assert.equal(parseCallTarget(null), null);
  assert.equal(parseCallTarget(JSON.stringify({ ...good(), pad: 'x'.repeat(8000) })), null);
  assert.equal(parseCallTarget('{not json'), null);
  assert.equal(parseCallTarget('42'), null);
  assert.equal(parseCallTarget('null'), null);
  assert.equal(parseCallTarget('[]'), null);
});

test('parseCallTarget rejects a bad case id', () => {
  assert.equal(parseOf({ ...good(), caseId: 'abc' }), null);
  assert.equal(parseOf({ ...good(), caseId: CASE.replace('a', '1') }), null);
  assert.equal(parseOf({ ...good(), caseId: 5 }), null);
});

test('parseCallTarget rejects a non-integer or negative slot', () => {
  assert.equal(parseOf({ ...good(), slot: 1.5 }), null);
  assert.equal(parseOf({ ...good(), slot: -1 }), null);
  assert.equal(parseOf({ ...good(), slot: '3' }), null);
  assert.notEqual(parseOf({ ...good(), slot: 0 }), null);
});

test('parseCallTarget rejects start or end that do not parse, or end not after start', () => {
  assert.equal(parseOf({ ...good(), start: 'nope' }), null);
  assert.equal(parseOf({ ...good(), end: 'nope' }), null);
  assert.equal(parseOf({ ...good(), end: good().start }), null);
  assert.equal(parseOf({ ...good(), end: '2026-01-01T10:00:00.000Z' }), null);
  assert.equal(parseOf({ ...good(), start: undefined }), null);
});

test('parseCallTarget rejects bad source id lists', () => {
  assert.equal(parseOf({ ...good(), sourceIds: 'a' }), null);
  assert.equal(parseOf({ ...good(), sourceIds: Array.from({ length: 9 }, (_, i) => 's' + i) }), null);
  assert.equal(parseOf({ ...good(), sourceIds: [''] }), null);
  assert.equal(parseOf({ ...good(), sourceIds: [7] }), null);
  assert.equal(parseOf({ ...good(), sourceIds: ['x'.repeat(201)] }), null);
  assert.deepEqual(parseOf({ ...good(), sourceIds: [] }).sourceIds, []);
  assert.equal(parseOf({ ...good(), sourceIds: Array.from({ length: 8 }, (_, i) => 's' + i) }).sourceIds.length, 8);
});

test('parseCallTarget rejects a call with a bad sourceId or id', () => {
  assert.equal(parseOf({ ...good(), call: { sourceId: '', id: 'r1' } }), null);
  assert.equal(parseOf({ ...good(), call: { sourceId: 'a', id: '' } }), null);
  assert.equal(parseOf({ ...good(), call: { sourceId: 'a', id: 9 } }), null);
  assert.equal(parseOf({ ...good(), call: { sourceId: 'a', id: 'x'.repeat(201) } }), null);
  assert.equal(parseOf({ ...good(), call: { sourceId: 'x'.repeat(201), id: 'r1' } }), null);
  assert.equal(parseOf({ ...good(), call: 'r1' }), null);
  assert.equal(parseOf({ ...good(), call: null }), null);
});

test('parseCallTarget drops unknown keys, also inside call', () => {
  const parsed = parseOf({ ...good(), x: 1, call: { sourceId: 'a', id: 'r1', y: 2 } });
  assert.equal('x' in parsed, false);
  assert.deepEqual(parsed, { ...good(), call: { sourceId: 'a', id: 'r1' } });
  const plain = parseOf({ ...good(), x: 'extra' });
  assert.equal('x' in plain, false);
  assert.deepEqual(plain, good());
});
