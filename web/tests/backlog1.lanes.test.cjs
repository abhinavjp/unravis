const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

// Backlog 1, P5: the sentence that says what a lane's imported files cover when they sit outside the window.

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

const lanes = loadTypeScript('src/lib/tracing/lanes.ts');
const { lintScreenStrings } = loadTypeScript('src/lib/copy/copylint.ts');
const { deck } = loadTypeScript('src/lib/copy/deck.ts');

const lane = (extra = {}) => ({
  key: 'iis', label: 'Web server', state: 'out-of-range', reason: '', origin: 'case', asOf: '', consistency: 'live-non-snapshot',
  complete: true, truncated: false, scanned: 0, hidden: 0, sources: [], buckets: [], ...extra
});
const at = (v) => v.slice(11, 16);
const win = { start: '2001-02-03T04:00:00Z', end: '2001-02-03T05:00:00Z' };
const span = { importedFrom: '2001-01-01T00:00:00Z', importedTo: '2001-01-01T01:00:00Z' };

test('out-of-range lane with a span says what the files cover', () => {
  assert.equal(lanes.laneImportedLine(lane({ ...span }), win, at),
    'Imported files cover 00:00 to 01:00; nothing falls inside this window.');
});

test('an events lane that scanned 0 and sits outside gets the same sentence', () => {
  assert.match(lanes.laneImportedLine(lane({ state: 'events', scanned: 0, buckets: [], ...span }), win, at), /nothing falls inside this window/);
});

test('no span, a disabled lane, or a lane with events inside gets no sentence', () => {
  assert.equal(lanes.laneImportedLine(lane(), win, at), '');
  assert.equal(lanes.laneImportedLine(lane({ disabled: 'x', ...span }), win, at), '');
  assert.equal(lanes.laneImportedLine(lane({ state: 'events', scanned: 5, ...span }), win, at), '');
  const inside = { importedFrom: '2001-02-03T03:00:00Z', importedTo: '2001-02-03T06:00:00Z' };
  assert.equal(lanes.laneImportedLine(lane({ state: 'events', scanned: 0, buckets: [], ...inside }), win, at), '');
});

test('the cover sentence passes the copy lint', () => {
  const entries = lanes.laneCopyEntries();
  assert.ok(entries.some((e) => e.id === 'tracingRead.coverOutside'));
  assert.deepEqual(lintScreenStrings(entries, deck.terms), []);
});

test('an out-of-range lane whose window falls in a gap inside the combined span gets no sentence', () => {
  const gap = { importedFrom: '2001-02-01T00:00:00Z', importedTo: '2001-02-05T00:00:00Z' };
  assert.equal(lanes.laneImportedLine(lane({ ...gap }), win, at), '');
});
