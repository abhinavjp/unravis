const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

// FIX-30 / UI-03 and UI-09: old slow rows are not shown under a new axis; the screen memoises the window-independent parts.

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

const { createCallController } = loadTypeScript('src/lib/drill/callController.ts');

const CALL_START = Date.parse('2026-11-12T10:00:00.000Z');
function deferred() {
  let resolve, reject;
  const promise = new Promise((res, rej) => { resolve = res; reject = rej; });
  return { promise, resolve, reject };
}
const tick = () => new Promise((r) => setImmediate(r));
const page = (rows) => ({ rows: rows || [], nextCursor: '', sources: [], truncated: false });
const record = () => ({
  id: 'r1', sourceId: 's1', controller: 'Orders', action: 'Get', kind: 'http', outcome: 'ok', state: 'valid', status: '200', url: '/x',
  initiatedAt: new Date(CALL_START).toISOString(), completedAt: new Date(CALL_START + 5000).toISOString(), durationMs: 5000,
  apiTimeMs: 4000, auditCreateTimeMs: 100, payloadRecordTimeMs: 10, requestKB: 1, responseKB: 1, userId: 7,
  payload: { request: '', response: '', truncated: false }
});

async function ready() {
  const calls = { slowest: [], record: [], tracing: [], activity: [], events: [] };
  const mk = (name) => (...args) => { const d = deferred(); calls[name].push({ args, d, signal: args[args.length - 1] }); return d.promise; };
  const deps = { slowest: mk('slowest'), record: mk('record'), tracing: mk('tracing'), activity: mk('activity'), events: mk('events') };
  const target = { caseId: 'abcdefghijklmnopqrstuvwxyz', slot: 1, start: '2026-11-12T09:59:00.000Z', end: '2026-11-12T10:01:00.000Z', sourceIds: ['s1'], call: { sourceId: 's1', id: 'r1' } };
  const ctl = createCallController(target, deps, () => {});
  ctl.start();
  calls.record[0].d.resolve(record());
  await tick();
  return { calls, ctl };
}

test('UI-03: a new window read clears the old rows and says it is loading; the answer fills them and ends the loading', async () => {
  const { calls, ctl } = await ready();
  assert.equal(ctl.state().slowRowsLoading, true, 'the first read is loading');
  calls.slowest[0].d.resolve(page([{ id: 'a', sourceId: 's1' }, { id: 'b', sourceId: 's1' }]));
  await tick();
  assert.equal(ctl.state().slowRows.length, 2);
  assert.equal(ctl.state().slowRowsLoading, false);

  ctl.commit({ before: 30, after: 30, shiftMs: 0 });
  let s = ctl.state();
  assert.deepEqual(s.slowRows, [], 'the old rows are not shown under the new axis');
  assert.equal(s.slowRowsLoading, true);
  assert.equal(s.slowRowsFailed, false);

  calls.slowest[1].d.resolve(page([{ id: 'c', sourceId: 's1' }]));
  await tick();
  s = ctl.state();
  assert.deepEqual(s.slowRows.map((r) => r.id), ['c']);
  assert.equal(s.slowRowsLoading, false);
});

test('UI-03: the loading flag is also published to the screen at the moment the read starts', async () => {
  const calls = { slowest: [], record: [], tracing: [], activity: [], events: [] };
  const mk = (name) => (...args) => { const d = deferred(); calls[name].push({ args, d, signal: args[args.length - 1] }); return d.promise; };
  const deps = { slowest: mk('slowest'), record: mk('record'), tracing: mk('tracing'), activity: mk('activity'), events: mk('events') };
  const seen = [];
  const ctl = createCallController({ caseId: 'abcdefghijklmnopqrstuvwxyz', slot: 1, start: '2026-11-12T09:59:00.000Z', end: '2026-11-12T10:01:00.000Z', sourceIds: ['s1'], call: { sourceId: 's1', id: 'r1' } }, deps, (st) => seen.push(st));
  ctl.start();
  calls.record[0].d.resolve(record());
  await tick();
  calls.slowest[0].d.resolve(page([{ id: 'a', sourceId: 's1' }]));
  await tick();
  seen.length = 0;
  ctl.commit({ before: 30, after: 30, shiftMs: 0 });
  assert.ok(seen.length >= 1);
  const last = seen[seen.length - 1];
  assert.deepEqual(last.slowRows, []);
  assert.equal(last.slowRowsLoading, true);
});

test('UI-03: a failed read ends the loading with empty rows and the failed flag; a dropped older answer neither fills nor ends it', async () => {
  const { calls, ctl } = await ready();
  calls.slowest[0].d.reject(new Error('down'));
  await tick();
  let s = ctl.state();
  assert.equal(s.slowRowsLoading, false);
  assert.equal(s.slowRowsFailed, true);
  assert.deepEqual(s.slowRows, []);

  ctl.commit({ before: 10, after: 10, shiftMs: 0 });
  const older = calls.slowest[1];
  ctl.commit({ before: 5, after: 5, shiftMs: 0 });
  older.d.resolve(page([{ id: 'old', sourceId: 's1' }]));
  await tick();
  s = ctl.state();
  assert.deepEqual(s.slowRows, [], 'the older answer is dropped');
  assert.equal(s.slowRowsLoading, true, 'the newest read is still running');
  calls.slowest[2].d.resolve(page([{ id: 'new', sourceId: 's1' }]));
  await tick();
  assert.deepEqual(ctl.state().slowRows.map((r) => r.id), ['new']);
  assert.equal(ctl.state().slowRowsLoading, false);
});

// ---- the screen ---------------------------------------------------------------------------------------------------------------
const screen = fs.readFileSync(path.join(root, 'src/components/SlowCallScreen.svelte'), 'utf8').replace(/\r\n/g, '\n');

test('UI-03: the screen shows a plain-words loading line in a status region while the rows load', () => {
  assert.match(screen, /\{#if bench\.s\.slowRowsLoading\}\s*<p class="note" role="status" data-testid="scall-slowrows-loading">\{DRILL_TEXT\.slowRowsLoading\}<\/p>/);
  assert.doesNotMatch(screen, /SLOW_ROWS_LOADING/);
  const copy = fs.readFileSync(path.join(root, 'src/lib/drill/drillCopy.ts'), 'utf8');
  assert.match(copy, /slowRowsLoading: 'Loading the new window\.\.\.'/);
});

test('UI-09: the window-independent parts are derived on their own, not rebuilt inside bench', () => {
  assert.match(screen, /let callState = \$state\.raw<CallState \| null>\(null\);/, 'raw state keeps the response objects identical between snapshots');
  assert.match(screen, /const overviewResp = \$derived\(callState\?\.overview \?\? null\);/);
  assert.match(screen, /const mapRows = \$derived\.by\(/);
  assert.match(screen, /const nearbyAll = \$derived\.by\(/);
  const benchStart = screen.indexOf('const bench = $derived.by(');
  const benchEnd = screen.indexOf('\n  });', benchStart);
  assert.ok(benchStart > 0 && benchEnd > benchStart);
  const bench = screen.slice(benchStart, benchEnd);
  assert.doesNotMatch(bench, /mapRow\(/, 'mapRow is not called per drag preview');
  assert.doesNotMatch(bench, /s\.nearby\.rows\.map\(/, 'the nearby mapping is not redone per drag preview');
  const mapDerived = screen.slice(screen.indexOf('const mapRows = $derived.by('), benchStart);
  assert.doesNotMatch(mapDerived, /callState/, 'mapRows reads only the overview answer, the call start and the saved day');
  assert.match(screen, /mapRows,/, 'bench still returns mapRows');
});
