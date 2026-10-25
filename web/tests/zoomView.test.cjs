const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');
const { compileModule } = require('svelte/compiler');

const root = path.resolve(__dirname, '..');

// Load a plain .ts module, or a .svelte.ts store through compileModule. `stubs` replaces imports by their request string.
function load(relativePath, stubs = {}) {
  const filename = path.join(root, relativePath);
  const source = fs.readFileSync(filename, 'utf8');
  const options = { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022, esModuleInterop: true };
  let js = ts.transpileModule(source, { compilerOptions: { ...options, module: ts.ModuleKind.ESNext } }).outputText;
  if (/\.svelte\.ts$/.test(relativePath)) js = compileModule(js, { filename: path.basename(filename), generate: 'client' }).js.code;
  js = ts.transpileModule(js, { compilerOptions: options }).outputText;
  const module = { exports: {} };
  const nodeRequire = createRequire(filename);
  const need = (request) => {
    if (request in stubs) return stubs[request];
    const base = path.join(path.dirname(filename), request);
    if (/^\.\.?\//.test(request)) {
      for (const suffix of ['.svelte.ts', '.ts']) if (fs.existsSync(base + suffix)) return load(path.relative(root, base + suffix), stubs);
    }
    return nodeRequire(request);
  };
  new Function('require', 'module', 'exports', js)(need, module, module.exports);
  return module.exports;
}

const MIN = 60_000, HOUR = 3_600_000, DAY = 86_400_000;
const at = (text) => Date.parse(text);
// Synthetic windows only: a week from Monday 22 Oct 2001 to Monday 29 Oct 2001 (UTC).
const week = { start: at('2001-10-22T00:00:00Z'), end: at('2001-10-29T00:00:00Z') };

const calls = [];
const api = { analyze: (query) => { calls.push(query); return Promise.resolve(analysisFor(query)); } };
const analysisFor = (query) => ({
  query, sources: [{ ok: true }], provenance: [{ completeness: 'complete' }], apis: [], overview: [], rollups: [], warnings: [],
  loadWindows: [{ at: query.start, count: 3 }], requestVolume: [], timeline: []
});
const { bucketForWindows } = load('src/lib/time.ts');
// The query the app itself would send for a window: its own interval rule, so the interval counts as automatic.
const queryFor = (win) => {
  const fields = {
    start: new Date(win.start).toISOString(), end: new Date(win.end).toISOString(),
    baselineStart: new Date(win.start - (win.end - win.start)).toISOString(), baselineEnd: new Date(win.start).toISOString()
  };
  return { ...fields, bucketSeconds: bucketForWindows(fields.start, fields.end, fields.baselineStart, fields.baselineEnd) };
};
const stubs = { './api': { api }, './timeZone.svelte': { timeZone: { zone: 'UTC' } } };

function fresh() {
  calls.length = 0;
  const { zoomView } = load('src/lib/zoomView.svelte.ts', stubs);
  return zoomView;
}
const settle = () => new Promise((resolve) => setTimeout(resolve, 0));

test('setWindow at a lower level replaces the top and makes one read', async () => {
  const zoomView = fresh();
  zoomView.follow(analysisFor(queryFor(week)));
  assert.equal(zoomView.view, null);
  const day = { start: at('2001-10-24T00:00:00Z'), end: at('2001-10-25T00:00:00Z') };
  zoomView.setWindow(day);
  assert.deepEqual(zoomView.view, day);
  assert.equal(calls.length, 1);
  const hour = { start: at('2001-10-24T14:00:00Z'), end: at('2001-10-24T15:00:00Z') };
  zoomView.setWindow(hour);
  zoomView.setWindow({ start: at('2001-10-24T14:00:00Z'), end: at('2001-10-24T14:30:00Z') });
  assert.equal(zoomView.model.crumbs.length, 2, 'the window replaced the top: still one step below the whole period');
  assert.deepEqual(zoomView.view, { start: at('2001-10-24T14:00:00Z'), end: at('2001-10-24T14:30:00Z') });
  assert.equal(calls.length, 3, 'one read per change');
  // The same window again does nothing and reads nothing.
  zoomView.setWindow({ start: at('2001-10-24T14:00:00Z'), end: at('2001-10-24T14:30:00Z') });
  assert.equal(calls.length, 3);
  await settle();
});

test('setWindow refuses a window under the floor or outside the queried window', async () => {
  const zoomView = fresh();
  zoomView.follow(analysisFor(queryFor(week)));
  zoomView.setWindow({ start: at('2001-10-24T14:00:00Z'), end: at('2001-10-24T14:00:30Z') });
  zoomView.setWindow({ start: week.end, end: week.end + HOUR });
  assert.equal(zoomView.view, null);
  assert.equal(calls.length, 0);
  await settle();
});

test('readout resolves slot indices and reports the snapped range', async () => {
  const zoomView = fresh();
  zoomView.follow(analysisFor(queryFor(week)));
  zoomView.setWindow({ start: at('2001-10-24T14:00:00Z'), end: at('2001-10-24T15:00:00Z') });
  assert.equal(zoomView.bucketSeconds, 30);
  const result = zoomView.readout(10, 19);
  assert.equal(result.ok, true);
  assert.ok(result.text.includes('5 minutes'), result.text);
  assert.equal(zoomView.readout(4, 4).ok, false);
  await settle();
});

test('expect reopens the lens on the next matching follow only', async () => {
  const zoomView = fresh();
  const lens = { start: at('2001-10-24T14:00:00Z'), end: at('2001-10-24T15:00:00Z') };
  zoomView.follow(analysisFor(queryFor(week)));
  zoomView.expect(week, lens);
  // A follow with a different root does not reopen it, and it clears the pending lens.
  const other = { start: at('2001-10-24T00:00:00Z'), end: at('2001-10-25T00:00:00Z') };
  zoomView.follow(analysisFor(queryFor(other)));
  assert.equal(zoomView.view, null);
  zoomView.follow(analysisFor(queryFor(week)));
  assert.equal(zoomView.view, null, 'the pending lens was cleared by the earlier follow');
  // Expect again, then follow with the matching root: the lens opens.
  zoomView.expect(week, lens);
  zoomView.follow(analysisFor(queryFor(week)));
  assert.deepEqual(zoomView.view, lens);
  // Used once only.
  zoomView.follow(analysisFor(queryFor(week)));
  assert.equal(zoomView.view, null);
  await settle();
});

test('expect ignores a lens that is not inside the new root', async () => {
  const zoomView = fresh();
  zoomView.expect(week, { start: week.end, end: week.end + HOUR });
  zoomView.follow(analysisFor(queryFor(week)));
  assert.equal(zoomView.view, null);
  await settle();
});
