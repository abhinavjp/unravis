const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

// Partial imported files: the lane line says some files have no recorded range.

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
const model = loadTypeScript('src/lib/tracing/model.ts');
const { lintScreenStrings } = loadTypeScript('src/lib/copy/copylint.ts');
const { deck } = loadTypeScript('src/lib/copy/deck.ts');

const lane = (extra = {}) => ({
  key: 'iis', label: 'Web server', state: 'out-of-range', reason: '', origin: 'case', asOf: '', consistency: 'live-non-snapshot',
  complete: true, truncated: false, scanned: 0, hidden: 0, sources: [], buckets: [], ...extra
});
const at = (v) => v.slice(11, 16);
const win = { start: '2001-02-03T04:00:00Z', end: '2001-02-03T05:00:00Z' };
const span = { importedFrom: '2001-01-01T00:00:00Z', importedTo: '2001-01-01T01:00:00Z' };
const SOME = 'Some imported files cover 00:00 to 01:00; the other files have no recorded range.';
const OUTSIDE = 'Imported files cover 00:00 to 01:00; nothing falls inside this window.';

test('partial imported files outside the window say some files have no range', () => {
  assert.equal(lanes.laneImportedLine(lane({ ...span, importedPartial: true }), win, at), SOME);
});

test('partial with state events and no events still gives the some-sentence', () => {
  const events = lane({ ...span, importedPartial: true, state: 'events', scanned: 0, buckets: [] });
  assert.equal(lanes.laneImportedLine(events, win, at), SOME);
});

test('not partial keeps the existing outside sentence', () => {
  assert.equal(lanes.laneImportedLine(lane({ ...span }), win, at), OUTSIDE);
});

test('parse keeps importedPartial only when it is exactly true', () => {
  const base = { key: 'iis', label: 'Web server', state: 'out-of-range', buckets: [], ...span };
  const response = (laneExtra) => ({
    window: { start: win.start, end: win.end, axisStart: win.start, axisEnd: win.end, bucketSeconds: 60 },
    lanes: [{ ...base, ...laneExtra }]
  });
  assert.equal(model.parseTracing(response({ importedPartial: true })).lanes[0].importedPartial, true);
  assert.equal(model.parseTracing(response({ importedPartial: 'yes' })).lanes[0].importedPartial, undefined);
  assert.equal(model.parseTracing(response({})).lanes[0].importedPartial, undefined);
});

test('copy lint accepts the new copy entries', () => {
  const problems = lintScreenStrings(lanes.laneCopyEntries(), deck.terms);
  assert.deepEqual(problems, []);
});
