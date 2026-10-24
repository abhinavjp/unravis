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
  const load = (request) => /^\.\.?\//.test(request) && fs.existsSync(path.join(path.dirname(filename), request + '.ts'))
    ? loadTypeScript(path.relative(root, path.join(path.dirname(filename), request + '.ts')))
    : nodeRequire(request);
  new Function('require', 'module', 'exports', output)(load, module, module.exports);
  return module.exports;
}

const patterns = loadTypeScript('src/lib/patterns.ts');
const { deck } = loadTypeScript('src/lib/copy/deck.ts');
const { lintCopy } = loadTypeScript('src/lib/copy/copylint.ts');

const params = { stallMs: 45000, episodeGapSeconds: 600, auditStallMs: 5000, auditStallShare: 0.3, volleyWindowMs: 1000 };
const episode = (index) => ({ index, start: '2026-10-24T10:00:00Z', end: '2026-10-24T10:05:00Z', requests: 2, users: 1, maxMs: 61300, operations: ['A / b'] });
const finding = (rule, over = {}) => ({ rule, label: 'Symptom', headline: 'server headline', n: 6, lowN: false, evidenceId: 'p2:' + rule.toLowerCase() + ':abcd', ...over });
const report = (over = {}) => ({
  findings: [finding('B1'), finding('B2')], episodes: [episode(0), episode(1), episode(2)], recurrence: 'recurring', params, truncated: false, ...over
});
const query = { sourceIds: ['a'], start: 's', end: 'e' };
const ID = /^[a-z0-9][a-z0-9:_.\-]{0,119}$/;

test('the slow-request and episode findings become one sentence', () => {
  const rows = patterns.findingRows(report());
  assert.equal(rows.length, 1);
  assert.equal(rows[0].headline, '6 requests took 45 s or longer, in 3 separate episodes.');
  assert.equal(rows[0].evidenceId, 'p2:b2:abcd');
  assert.equal(rows[0].n, 6);
});

test('the merged sentence handles one episode, two episodes, a partial read and low n', () => {
  assert.equal(patterns.findingRows(report({ episodes: [episode(0)], recurrence: 'single_episode', findings: [finding('B1', { n: 1 }), finding('B2', { n: 1 })] }))[0].headline,
    '1 request took 45 s or longer, in a single episode.');
  assert.equal(patterns.findingRows(report({ episodes: [episode(0), episode(1)], recurrence: 'two_episodes' }))[0].headline,
    '6 requests took 45 s or longer, in 2 separate episodes. That is not enough to call it recurring.');
  assert.equal(patterns.findingRows(report({ stallsTruncated: true }))[0].headline, 'At least 6 requests took 45 s or longer, in 3 separate episodes.');
  assert.equal(patterns.findingRows(report({ truncated: true }))[0].headline, 'At least 6 requests took 45 s or longer, in 3 separate episodes.');
  assert.equal(patterns.findingRows(report({ findings: [finding('B1'), finding('B2', { lowN: true })] }))[0].lowN, true);
});

test('other findings keep their place and a lone finding is not merged', () => {
  const rows = patterns.findingRows(report({ findings: [finding('B1'), finding('B2'), finding('B6', { headline: 'Audit create took 7 s as measured from the client side.' }), finding('B12', { label: 'Correlation' })] }));
  assert.deepEqual(rows.map((r) => r.label), ['Symptom', 'Symptom', 'Correlation']);
  assert.equal(rows[1].headline, 'Audit create took 7 s as measured from the client side.');
  const alone = patterns.findingRows(report({ findings: [finding('B1')], episodes: [] }));
  assert.equal(alone.length, 1);
  assert.equal(alone[0].headline, 'server headline');
  assert.deepEqual(patterns.findingRows(report({ findings: [], episodes: [] })), []);
});

test('episode references are valid ledger references', () => {
  const id = patterns.episodeEvidenceId('2026-10-24T18:19:37Z');
  assert.equal(id, 'episode:2026-10-24t18:19:37z');
  assert.match(id, ID);
  assert.match(patterns.episodeEvidenceId('2026-10-24T18:19:37.123456789+04:00'), ID);
  assert.equal(patterns.episodeEvidenceId(''), 'episode:');
  assert.equal(patterns.isEvidenceId('episode:'), true);
  assert.equal(patterns.episodeEvidenceId('x'.repeat(200)), '');
  assert.equal(patterns.isEvidenceId('p2:b1:0123456789abcdef'), true);
  for (const bad of ['', 'Upper', ':start', '-x', 'has space', 'a/b', 'x'.repeat(121)]) assert.equal(patterns.isEvidenceId(bad), false, bad);
});

test('cite labels are short and flat', () => {
  assert.equal(patterns.episodeCiteLabel('18:19'), 'Episode at 18:19');
  assert.equal(patterns.citeLabel('  a\n  b  '), 'a b');
  const long = patterns.citeLabel('word '.repeat(40));
  assert.ok(long.length <= 60, long);
  assert.ok(long.endsWith('…'));
});

test('thresholds are typed in seconds and sent in the units the server takes', () => {
  assert.deepEqual(patterns.windowInputSeconds(query, '45', '600'), { input: { sourceIds: ['a'], start: 's', end: 'e' } });
  assert.deepEqual(patterns.windowInputSeconds(query, '', ''), { input: { sourceIds: ['a'], start: 's', end: 'e' } });
  assert.deepEqual(patterns.windowInputSeconds(query, ' 30 ', '300'), { input: { sourceIds: ['a'], start: 's', end: 'e', stallMs: 30000, episodeGapSeconds: 300 } });
  assert.deepEqual(patterns.windowInputSeconds(query, '12.5', ''), { input: { sourceIds: ['a'], start: 's', end: 'e', stallMs: 12500 } });
  for (const bad of ['0', '-1', 'abc', '1e3', '1.2345', '45 s']) assert.deepEqual(patterns.windowInputSeconds(query, bad, ''), { error: patterns.PATTERNS_TEXT.stallBad }, bad);
  for (const bad of ['0', '-1', 'abc', '1.5', '10 s']) assert.deepEqual(patterns.windowInputSeconds(query, '', bad), { error: patterns.PATTERNS_TEXT.gapBad }, bad);
});

test('the thresholds-changed flag follows the defaults', () => {
  assert.equal(patterns.thresholdsChanged('45', '600'), false);
  assert.equal(patterns.thresholdsChanged('', ''), false);
  assert.equal(patterns.thresholdsChanged('45.0', '600'), false);
  assert.equal(patterns.thresholdsChanged('30', '600'), true);
  assert.equal(patterns.thresholdsChanged('45', '300'), true);
});

test('the empty state names the limit and what to change', () => {
  assert.equal(patterns.emptyEpisodesText(45000), 'No requests took 45 s or longer in this window. Try a wider range above, or lower the slow request limit under Thresholds.');
  assert.match(patterns.emptyEpisodesText(12500), /12\.5 s/);
});

test('range chips use the page quick range ids', () => {
  assert.deepEqual(patterns.PATTERN_RANGES.map((r) => r.id), ['15m', '1h', '4h', '24h']);
});

test('new copy passes the copy lint and names no cause', () => {
  const strings = [
    patterns.PATTERNS_TEXT.intro, patterns.PATTERNS_TEXT.thresholdNote, patterns.PATTERNS_TEXT.stale, patterns.PATTERNS_TEXT.citeCopied,
    patterns.PATTERNS_TEXT.stallBad, patterns.PATTERNS_TEXT.gapBad, patterns.PATTERNS_TEXT.emptyTitle,
    patterns.emptyEpisodesText(45000),
    ...patterns.findingRows(report()).map((r) => r.headline),
    ...patterns.findingRows(report({ episodes: [episode(0), episode(1)], recurrence: 'two_episodes' })).map((r) => r.headline)
  ];
  assert.deepEqual(lintCopy(strings.map((text, i) => ({ id: 'r' + i, text })), deck.terms), []);
  for (const text of strings) assert.doesNotMatch(text, /\b(caused|because|due to|waiting on|blocked by)\b/i, text);
});
