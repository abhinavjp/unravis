const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

// The tracing client (WP-16a): request/response shapes, the per-tab request sequence, the error sentence
// map, and the shell copy's screen-string lint.

const root = path.resolve(__dirname, '..');
const cache = new Map();
function loadTypeScript(relativePath) {
  const filename = path.join(root, relativePath);
  if (cache.has(filename)) return cache.get(filename).exports;
  const source = fs.readFileSync(filename, 'utf8');
  const output = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022, esModuleInterop: true } }).outputText;
  const module = { exports: {} };
  cache.set(filename, module);
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
  new Function('require', 'module', 'exports', '$state', output)(load, module, module.exports, (value) => value);
  return module.exports;
}

const model = loadTypeScript('src/lib/tracing/model.ts');
const cases = loadTypeScript('src/lib/cases.ts');
const copy = loadTypeScript('src/lib/copy/tracingCopy.ts');
const { lintScreenStrings } = loadTypeScript('src/lib/copy/copylint.ts');
const { deck } = loadTypeScript('src/lib/copy/deck.ts');

const { nextRequestSeq, isStale, buildTracingBody, parseTracing, tracingErrorText } = model;
const { CaseApiError } = cases;
const { TRACING_TEXT, tracingCopyEntries } = copy;

const INCIDENT = { start: '2026-03-10T10:00:00Z', end: '2026-03-10T11:00:00Z' };
const REFERENCE = { start: '2026-03-03T10:00:00Z', end: '2026-03-03T11:00:00Z' };
const WINDOW = { start: INCIDENT.start, end: INCIDENT.end, axisStart: INCIDENT.start, axisEnd: INCIDENT.end, bucketSeconds: 60 };

function lane(overrides = {}) {
  return {
    key: 'sql', label: 'SQL Server', state: 'events', origin: 'case', asOf: '2026-03-10T11:05:00Z',
    complete: true, truncated: false, reason: '', scanned: 10, hidden: 0,
    sources: [], buckets: [{ start: INCIDENT.start, count: 3 }],
    events: [], ...overrides
  };
}

test('requestSeq is strictly increasing and stale responses are ignored', () => {
  const a = nextRequestSeq();
  const b = nextRequestSeq();
  assert.ok(b > a);
  assert.equal(isStale(1, 2), true);
  assert.equal(isStale(2, 2), false);
});

test('request body carries only the schema fields', () => {
  const noIds = JSON.parse(buildTracingBody({ incident: INCIDENT, reference: REFERENCE }, 7));
  assert.deepEqual(Object.keys(noIds).sort(), ['incident', 'reference', 'requestSeq']);
  assert.deepEqual(noIds.incident, INCIDENT);
  assert.deepEqual(noIds.reference, REFERENCE);
  assert.equal(noIds.requestSeq, 7);

  const withIds = JSON.parse(buildTracingBody({ incident: INCIDENT, reference: REFERENCE, sourceIds: ['cs_1', 'imp_2'] }, 8));
  assert.deepEqual(Object.keys(withIds).sort(), ['incident', 'reference', 'requestSeq', 'sourceIds']);
  assert.deepEqual(withIds.sourceIds, ['cs_1', 'imp_2']);
});

test('parseTracing rejects bad shapes and fills consistency defaults', () => {
  // A lane list that is not an array is unreadable.
  assert.throws(() => parseTracing({ window: WINDOW, lanes: {}, allLanesStored: true }), (e) => e instanceof CaseApiError && e.reason === 'unreadable');

  // A live lane without `consistency` becomes live-non-snapshot, and mixedConsistency follows it.
  const liveNoConsistency = parseTracing({
    window: WINDOW,
    lanes: [lane({ key: 'audit', origin: 'live', consistency: undefined })],
    allLanesStored: false
  });
  assert.equal(liveNoConsistency.lanes[0].consistency, 'live-non-snapshot');
  assert.equal(liveNoConsistency.mixedConsistency, true);

  // A case-only response keeps allLanesStored exactly as sent.
  const caseOnly = parseTracing({ window: WINDOW, lanes: [lane({ origin: 'case' })], allLanesStored: true });
  assert.equal(caseOnly.allLanesStored, true);

  // A server bucket without `errors` gets errors 0.
  assert.deepEqual(caseOnly.lanes[0].buckets[0], { start: INCIDENT.start, count: 3, errors: 0 });

  // disabled: "tz-rule" and truncated: true are kept; a missing top-level `truncated` derives from the lanes.
  const withDisabledAndTruncated = parseTracing({
    window: WINDOW,
    lanes: [lane({ disabled: 'tz-rule' }), lane({ key: 'iis', truncated: true })],
    allLanesStored: true
  });
  assert.equal(withDisabledAndTruncated.lanes[0].disabled, 'tz-rule');
  assert.equal(withDisabledAndTruncated.lanes[1].truncated, true);
  assert.equal(withDisabledAndTruncated.truncated, true);

  // window.axisStart, axisEnd and bucketSeconds are read from window, and a missing requestSeq stays undefined.
  const withWindow = parseTracing({ window: WINDOW, lanes: [lane()], allLanesStored: true });
  assert.equal(withWindow.window.axisStart, WINDOW.axisStart);
  assert.equal(withWindow.window.axisEnd, WINDOW.axisEnd);
  assert.equal(withWindow.window.bucketSeconds, WINDOW.bucketSeconds);
  assert.equal(withWindow.requestSeq, undefined);
});

test('error text is fixed per status', () => {
  const of = (status, reason) => new CaseApiError('server said something that should never show', status, reason);
  assert.equal(tracingErrorText(of(400, 'bad-window')), TRACING_TEXT.errBadWindow);
  assert.equal(tracingErrorText(of(423, 'case-locked')), TRACING_TEXT.errLocked);
  assert.equal(tracingErrorText(of(409, 'case-closed')), TRACING_TEXT.errClosed);
  assert.equal(tracingErrorText(of(503, 'unavailable')), TRACING_TEXT.errUnavailable);
  assert.equal(tracingErrorText(of(500, 'boom')), TRACING_TEXT.errServer);
  assert.equal(tracingErrorText(of(0, 'unreachable')), TRACING_TEXT.errServer);
  for (const error of [of(400, 'bad-window'), of(423, 'case-locked'), of(409, 'case-closed'), of(503, 'unavailable'), of(500, 'boom'), of(0, 'unreachable')]) {
    assert.ok(!tracingErrorText(error).includes('server said something'));
  }
});

test('tracing copy passes the screen-string lint', () => {
  assert.deepEqual(lintScreenStrings(tracingCopyEntries(), deck.terms), []);
});
