const assert = require('node:assert/strict');
const test = require('node:test');
const { loadTypeScript, read } = require('./scn1.l4.helpers.cjs');
const autorun = loadTypeScript('src/lib/autorun.ts');
const time = loadTypeScript('src/lib/time.ts');
const scopeAt = (extra) => ({ ...time.defaultQuery(new Date('2026-10-22T12:00:00Z'), ['source-1']), ...extra });
const SENTENCE = 'An audit ID is exactly 24 hexadecimal characters.';

const invalid = ['not an id!!', 'abc', '0123456789abcdef0123456', '0123456789abcdef012345678', 'zzzzzzzzzzzzzzzzzzzzzzzz', ' '];
const runnable = [undefined, '', '0123456789abcdef01234567', '0123456789ABCDEF01234567'];

test('D-A-45: an invalid audit ID blocks the run and the hint says why', () => {
  for (const value of invalid) {
    assert.equal(autorun.scopeProblem(scopeAt({ auditId: value }), true), SENTENCE, JSON.stringify(value));
    assert.equal(autorun.scopeHint(scopeAt({ auditId: value }), true), SENTENCE, JSON.stringify(value));
  }
});

test('D-A-45: a valid or empty audit ID still runs', () => {
  for (const value of runnable) {
    assert.equal(autorun.scopeProblem(scopeAt({ auditId: value }), true), null, String(value));
    assert.equal(autorun.scopeHint(scopeAt({ auditId: value }), true), '', String(value));
  }
});

test('D-A-45: the audit ID check comes last', () => {
  assert.match(autorun.scopeProblem(scopeAt({ auditId: 'bad', sourceIds: [] }), true), /Select at least one source/);
  assert.equal(autorun.scopeHint(scopeAt({ auditId: 'bad', sourceIds: [] }), true), 'Choose a source to run the analysis.');
  assert.match(autorun.scopeProblem(scopeAt({ auditId: 'bad', bucketSeconds: 5 }), true), /Bucket size/);
});

test('D-A-45: the problem and the hint agree for every case above', () => {
  const queries = [...invalid, ...runnable].map((value) => scopeAt({ auditId: value }))
    .concat([scopeAt({ auditId: 'bad', sourceIds: [] }), scopeAt({ auditId: 'bad', bucketSeconds: 5 })]);
  for (const query of queries) assert.equal(Boolean(autorun.scopeProblem(query, true)), Boolean(autorun.scopeHint(query, true)));
});

test('D-A-45: the autoRunDecision skips an invalid audit ID', () => {
  const problem = autorun.scopeProblem(scopeAt({ auditId: 'bad' }), true);
  assert.equal(autorun.autoRunDecision({ problem, signature: 'a', shown: null, inflight: null }), 'skip');
});

test('D-A-45: the field and the gate share one rule', () => {
  const details = read('src/components/filters/DetailsGroup.svelte');
  assert.match(details, /import \{ AUDIT_ID_PATTERN \} from '\.\.\/\.\.\/lib\/autorun';/);
  assert.doesNotMatch(details, /const AUDIT_ID_PATTERN/);
  assert.match(details, /An audit ID is exactly 24 hexadecimal characters\./);
});
