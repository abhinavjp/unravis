const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

// Backlog 1 P1: the client reads the noFile mark on a source and the imported span on a lane.

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

const model = loadTypeScript('src/lib/tracing/model.ts');

const window = { start: '2026-10-01T00:00:00Z', end: '2026-10-12T00:00:00Z', axisStart: '2026-10-01T00:00:00Z', axisEnd: '2026-10-12T00:00:00Z', bucketSeconds: 3600 };

function withSources(sources) {
  return { window, lanes: [], sources, auditSources: [], savedNotConnected: [] };
}

function withLane(extra) {
  return {
    window,
    lanes: [{ key: 'iis', label: 'IIS', state: 'out-of-range', reason: '', origin: 'live', asOf: '', consistency: 'live-non-snapshot',
      complete: true, truncated: false, scanned: 0, hidden: 0, sources: [], buckets: [], ...extra }],
    sources: [], auditSources: [], savedNotConnected: []
  };
}

test('a source with no import keeps noFile; others stay as before', () => {
  const r = model.parseTracing(withSources([{ sourceId: 'a1', label: 'Orders' }, { sourceId: 'b2', label: 'u_ex.log', noFile: true }]));
  assert.deepEqual(r.sources, [{ sourceId: 'a1', label: 'Orders' }, { sourceId: 'b2', label: 'u_ex.log', noFile: true }]);
  assert.equal('noFile' in r.sources[0], false);
});

test('a source with noFile false is read without the mark', () => {
  const r = model.parseTracing(withSources([{ sourceId: 'c3', label: 'x.log', noFile: false }]));
  assert.equal('noFile' in r.sources[0], false);
});

test('a lane keeps the imported span only when both ends are dates', () => {
  const ok = model.parseTracing(withLane({ importedFrom: '2001-01-01T00:00:00Z', importedTo: '2001-01-01T01:00:00Z' }));
  assert.equal(ok.lanes[0].importedFrom, '2001-01-01T00:00:00Z');
  assert.equal(ok.lanes[0].importedTo, '2001-01-01T01:00:00Z');
  const bad = model.parseTracing(withLane({ importedFrom: 'soon', importedTo: '2001-01-01T01:00:00Z' }));
  assert.equal(bad.lanes[0].importedFrom, undefined);
  assert.equal(bad.lanes[0].importedTo, undefined);
  const half = model.parseTracing(withLane({ importedFrom: '2001-01-01T00:00:00Z' }));
  assert.equal('importedFrom' in half.lanes[0], false);
});

test('a plain lane carries no imported span', () => {
  const plain = model.parseTracing(withLane({}));
  assert.equal('importedFrom' in plain.lanes[0], false);
  assert.equal('importedTo' in plain.lanes[0], false);
});
