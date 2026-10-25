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
  const output = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022, esModuleInterop: true } }).outputText;
  const module = { exports: {} };
  const nodeRequire = createRequire(filename);
  const load = (request) => /^\.\.?\//.test(request) && fs.existsSync(path.join(path.dirname(filename), request + '.ts'))
    ? loadTypeScript(path.relative(root, path.join(path.dirname(filename), request + '.ts')))
    : nodeRequire(request);
  new Function('require', 'module', 'exports', output)(load, module, module.exports);
  return module.exports;
}

const copy = loadTypeScript('src/lib/copy/screenCopy.ts');
const { deck } = loadTypeScript('src/lib/copy/deck.ts');
const { lintCopy } = loadTypeScript('src/lib/copy/copylint.ts');
const { findTerm, searchTerms } = loadTypeScript('src/lib/copy/lookup.ts');

test('p95Change compares with the earlier period and flags only a clear rise', () => {
  assert.equal(copy.p95Change(500, 100).text, '5.0x the earlier period');
  assert.equal(copy.p95Change(500, 100).severe, true);
  assert.equal(copy.p95Change(150, 100).severe, false);
  assert.equal(copy.p95Change(200, 100).severe, false, 'exactly twice is not more than twice');
  assert.equal(copy.p95Change(1200, 100).text, '12x the earlier period');
  assert.equal(copy.p95Change(102, 100).text, 'about the same as the earlier period');
  assert.equal(copy.p95Change(50, 100).text, '0.5x the earlier period');
  assert.equal(copy.p95Change(50, 100).severe, false);
});

test('p95Change returns null when it cannot compare', () => {
  assert.equal(copy.p95Change(null, 100), null);
  assert.equal(copy.p95Change(100, null), null);
  assert.equal(copy.p95Change(100, 0), null);
  assert.equal(copy.p95Change(NaN, 100), null);
  assert.equal(copy.p95Change(-1, 100), null);
});

test('p95Sentence appears only for a severe rise and names no cause', () => {
  const severe = copy.p95Change(500, 100);
  const sentence = copy.p95Sentence(severe, 15 * 60 * 1000);
  assert.match(sentence, /^P95 is about 5 times what it was in the previous 15 minutes\./);
  assert.equal(copy.p95Sentence(copy.p95Change(150, 100), 900000), '');
  assert.equal(copy.p95Sentence(null, 900000), '');
  assert.match(copy.p95Sentence(severe, null), /in the earlier period\./);
  assert.deepEqual(lintCopy([{ id: 'p95-sentence', text: sentence }], deck.terms), []);
});

test('periodLengthMs reads two timestamps', () => {
  assert.equal(copy.periodLengthMs('2026-10-24T10:00:00Z', '2026-10-24T10:15:00Z'), 900000);
  assert.equal(copy.periodLengthMs('2026-10-24T10:15:00Z', '2026-10-24T10:00:00Z'), null);
  assert.equal(copy.periodLengthMs('nope', '2026-10-24T10:00:00Z'), null);
  assert.equal(copy.periodLengthMs(undefined, undefined), null);
});

test('tenantPlain says what can be told, with the share when the server gave one', () => {
  assert.equal(copy.tenantPlain('available'), copy.TENANT_TEXT.available);
  assert.equal(copy.tenantPlain('unavailable'), copy.TENANT_TEXT.unavailable);
  assert.match(copy.tenantPlain('unavailable: account id 12.5% populated'), /Only 12\.5% of the records have an account id\.$/);
  assert.equal(copy.tenantPlain(undefined), copy.TENANT_TEXT.unavailable);
});

test('provenanceSummary collapses only fully finished reads', () => {
  const read = (sourceId, completeness) => ({ sourceId, completeness });
  assert.equal(copy.provenanceSummary([read('a', 'complete')]), 'Read every matching record from 1 source.');
  assert.equal(copy.provenanceSummary([read('a', 'complete'), read('a', 'ran_to_end'), read('b', 'complete')]), 'Read every matching record from 2 sources.');
  assert.equal(copy.provenanceSummary([read('a', 'complete'), read('b', 'partial')]), '');
  assert.equal(copy.provenanceSummary([]), '');
  assert.equal(copy.provenanceSummary(null), '');
});

test('glossaryCount reports a count, or suggests terms that exist', () => {
  assert.equal(copy.glossaryCount(1, ''), '1 term');
  assert.equal(copy.glossaryCount(12, 'p'), '12 terms');
  const none = copy.glossaryCount(0, 'xyz');
  assert.match(none, /^No terms match "xyz"\. Try P95/);
  assert.equal(copy.glossaryCount(0, '   '), 'No terms to show.');
  for (const word of ['P95', 'incomplete', 'episode', 'coverage']) assert.ok(searchTerms(word).length > 0, word + ' should find a term');
});

test('the new on-screen strings pass the copy lint', () => {
  const entries = [
    ...copy.HELP_STEPS.map((text, i) => ({ id: 'help-step-' + i, text })),
    { id: 'help-intro', text: copy.HELP_TEXT.startIntro },
    ...Object.entries(copy.KIND_TITLES).map(([id, text]) => ({ id: 'kind-' + id, text })),
    ...Object.entries(copy.TENANT_TEXT).map(([id, text]) => ({ id: 'tenant-' + id, text })),
    { id: 'summary', text: copy.provenanceSummary([{ sourceId: 'a', completeness: 'complete' }]) }
  ];
  assert.deepEqual(lintCopy(entries, deck.terms), []);
});

test('the deck stays within its cap and knows the new terms and aliases', () => {
  assert.ok(deck.terms.length <= 53);
  assert.ok(findTerm('confounder'));
  assert.ok(findTerm('tenant'));
  assert.equal(searchTerms('Customer')[0]?.id, 'tenant');
  assert.ok(searchTerms('Few samples').some((t) => t.id === 'low-sample'));
  assert.ok(searchTerms('Join class').some((t) => t.id === 'join'));
});
