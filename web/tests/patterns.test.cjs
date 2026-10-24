const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

const root = path.resolve(__dirname, '..');
function loadTypeScript(relativePath) {
  const filename = path.join(root, relativePath);
  const source = fs.readFileSync(filename, 'utf8');
  const output = ts.transpileModule(source, {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022, esModuleInterop: true }
  }).outputText;
  const module = { exports: {} };
  const nodeRequire = createRequire(filename);
  // A relative import of another TypeScript file in src/lib is transpiled the same way.
  const load = (request) => request.startsWith('./') && fs.existsSync(path.join(path.dirname(filename), request + '.ts'))
    ? loadTypeScript(path.relative(root, path.join(path.dirname(filename), request + '.ts')))
    : nodeRequire(request);
  new Function('require', 'module', 'exports', output)(load, module, module.exports);
  return module.exports;
}

const patterns = loadTypeScript('src/lib/patterns.ts');
const { deck } = loadTypeScript('src/lib/copy/deck.ts');
const { lintCopy } = loadTypeScript('src/lib/copy/copylint.ts');

const params = { stallMs: 45000, episodeGapSeconds: 600, auditStallMs: 5000, auditStallShare: 0.3, volleyWindowMs: 1000 };
const episode = (index, requests) => ({ index, start: '2026-10-24T10:00:00Z', end: '2026-10-24T10:05:00Z', requests, users: 1, maxMs: 61300, operations: ['A / b'] });
const verdict = (recurrence, episodes = [], p = params) => patterns.verdictText({ recurrence, episodes, params: p });

test('verdict text for each recurrence value', () => {
  assert.equal(verdict('recurring', [episode(0, 2), episode(1, 3), episode(2, 1), episode(3, 1)]), 'Recurring across 4 independent episodes');
  assert.equal(verdict('two_episodes', [episode(0, 2), episode(1, 3)]), 'Two independent episodes. Not enough to call it recurring');
  assert.equal(verdict('single_episode', [episode(0, 7)]), 'Single episode, 7 requests');
  assert.equal(verdict('none'), 'No stalls at or above 45 s in this window');
  assert.equal(verdict('none', [], { ...params, stallMs: 12500 }), 'No stalls at or above 12.5 s in this window');
});

test('a single episode is never phrased as a count of stalls', () => {
  assert.doesNotMatch(verdict('single_episode', [episode(0, 7)]), /stalls/i);
});

test('seconds labels', () => {
  assert.equal(patterns.secondsLabel(45000), '45 s');
  assert.equal(patterns.secondsLabel(61300), '61.3 s');
  assert.equal(patterns.secondsLabel(61349), '61.3 s');
  assert.equal(patterns.secondsLabel(0), '0 s');
  assert.equal(patterns.secondsLabel(Number.NaN), '0 s');
});

test('quality, n badge and coverage lines', () => {
  assert.equal(patterns.qualityLine({ incomplete: 3, pending: 0, invalid: 1 }), '3 incomplete, 0 pending, 1 invalid');
  assert.equal(patterns.nBadge(4), 'n = 4');
  assert.equal(patterns.coverageLine(undefined), null);
  assert.equal(patterns.coverageLine({ run: false, note: 'The check did not run on a live source.' }), 'The check did not run on a live source.');
  assert.equal(patterns.coverageLine({ run: true, requestInitiatedAtPct: 98.25, accountIdPct: 91, note: '' }),
    'Request start time is filled in on 98.3% of rows, and account id on 91%.');
});

test('the audit line is the exact permitted wording', () => {
  assert.equal(patterns.auditStallLine({ auditCreateTimeMs: 28000 }), 'Audit create took 28 s as measured from the client side.');
  assert.equal(patterns.auditStallLine({ auditCreateTimeMs: 31450 }), 'Audit create took 31.4 s as measured from the client side.');
});

test('windowInput passes the window and only valid overrides', () => {
  const query = { sourceIds: ['a'], start: 's', end: 'e' };
  assert.deepEqual(patterns.windowInput(query), { input: { sourceIds: ['a'], start: 's', end: 'e' } });
  assert.deepEqual(patterns.windowInput(query, ' 30000 ', '300'), { input: { sourceIds: ['a'], start: 's', end: 'e', stallMs: 30000, episodeGapSeconds: 300 } });
  for (const bad of ['0', '-5', '1.5', 'abc', '1e3']) {
    assert.ok('error' in patterns.windowInput(query, bad), bad);
    assert.ok('error' in patterns.windowInput(query, '', bad), bad);
  }
});

test('episode helpers and volley text', () => {
  const report = { stalls: [{ episode: 0, id: 'a' }, { episode: 1, id: 'b' }], volleys: [{ episode: 1, userId: 7 }] };
  assert.deepEqual(patterns.stallsOf(report, { index: 0 }).map((s) => s.id), ['a']);
  assert.deepEqual(patterns.volleysOf(report, { index: 1 }).map((v) => v.userId), [7]);
  assert.equal(patterns.endGapsText({ endGapsMs: [120, 80], incompleteEnds: 0 }), '120 ms, 80 ms');
  assert.equal(patterns.endGapsText({ endGapsMs: [], incompleteEnds: 0 }), 'none');
  assert.equal(patterns.endGapsText({ endGapsMs: [5], incompleteEnds: 2 }), '5 ms (2 with no finish time)');
  assert.equal(patterns.operationText({ controller: 'Orders', action: 'Save' }), 'Orders / Save');
  assert.equal(patterns.operationText({ controller: 'Orders', action: '' }), 'Orders');
});

test('every string the helpers produce passes the copy lint and has no causal or "complete" wording', () => {
  const strings = [
    ...Object.values(patterns.PATTERNS_TEXT),
    verdict('recurring', [episode(0, 2), episode(1, 3), episode(2, 1)]),
    verdict('two_episodes', [episode(0, 2), episode(1, 3)]),
    verdict('single_episode', [episode(0, 7)]),
    verdict('none'),
    patterns.qualityLine({ incomplete: 3, pending: 2, invalid: 1 }),
    patterns.coverageLine({ run: true, requestInitiatedAtPct: 98.2, accountIdPct: 91, note: '' }),
    patterns.auditStallLine({ auditCreateTimeMs: 28000 }),
    patterns.endGapsText({ endGapsMs: [5], incompleteEnds: 2 }),
    patterns.secondsLabel(61300),
    patterns.nBadge(3)
  ];
  const entries = strings.map((text, i) => ({ id: `p${i}`, text }));
  assert.deepEqual(lintCopy(entries, deck.terms), []);
  for (const text of strings) {
    assert.doesNotMatch(text, /\b(caused|because|due to|waiting on|blocked by|complete)\b/i, text);
  }
});
