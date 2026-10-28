const assert = require('node:assert/strict');
const test = require('node:test');
const { loadTypeScript, read } = require('./scn1.l4.helpers.cjs');
const pulse = loadTypeScript('src/lib/pulse.ts');
const copy = loadTypeScript('src/lib/copy/screenCopy.ts');
const { lintCopy } = loadTypeScript('src/lib/copy/copylint.ts');
const { deck } = loadTypeScript('src/lib/copy/deck.ts');
// Same shapes as web/tests/pure.test.cjs lines 1400-1401.
const overviewRow = (kind, events, completed, clientErrors, serverErrors, p95Ms) => ({ kind, events, completed, clientErrors, serverErrors, p95: p95Ms == null ? null : { valueMs: p95Ms } });
const rollupRow = (controller, action, extra = {}) => ({ controller, action, kind: 'synchronous', requests: 10, failures: 0, perMinute: 1, ...extra });
const severe = copy.p95Change(500, 100);
const text = copy.p95Sentence(severe, 15 * 60 * 1000, 'Payroll.Run');

test('D-A-48: topRiseName picks the biggest known rise', () => {
  assert.equal(pulse.topRiseName([rollupRow('Payroll', 'Run', { p95DeltaMs: 400 }), rollupRow('Leave', 'Apply', { p95DeltaMs: 900 }), rollupRow('Tax', 'Calc', { p95DeltaMs: -50 })]), 'Leave.Apply');
});

test('D-A-48: topRiseName is empty when nothing rose or nothing is known', () => {
  assert.equal(pulse.topRiseName([]), '');
  assert.equal(pulse.topRiseName([rollupRow('A', 'B'), rollupRow('C', 'D', { p95DeltaMs: null }), rollupRow('E', 'F', { p95DeltaMs: 0 }), rollupRow('G', 'H', { p95DeltaMs: -10 }), rollupRow('I', 'J', { p95DeltaMs: NaN })]), '');
});

test('D-A-48: the headline names the top operation between the two existing sentences', () => {
  assert.equal(text, 'P95 is about 5 times what it was in the previous 15 minutes. The biggest rise is in Payroll.Run. Open "Where is it?" to see which operations changed most.');
});

test('D-A-48: without a name the sentence is unchanged', () => {
  assert.equal(copy.p95Sentence(severe, 15 * 60 * 1000), 'P95 is about 5 times what it was in the previous 15 minutes. Open "Where is it?" to see which operations changed most.');
  assert.equal(copy.p95Sentence(severe, 15 * 60 * 1000, ''), copy.p95Sentence(severe, 15 * 60 * 1000));
  assert.equal(copy.p95Sentence(copy.p95Change(150, 100), 900000, 'Payroll.Run'), '');
  assert.equal(copy.p95Sentence(null, 900000, 'Payroll.Run'), '');
});

test('D-A-48: the named headline passes the copy lint', () => {
  assert.deepEqual(lintCopy([{ id: 'p95-sentence-named', text }], deck.terms), []);
});

test('D-A-48: an empty period shows a dash in the 4xx and 5xx tiles', () => {
  const empty = pulse.pulseFigures([], 'synchronous', 'total', 'x');
  const client = empty.find(figure => figure.id === 'client');
  const server = empty.find(figure => figure.id === 'server');
  assert.equal(client.value, '—');
  assert.equal(client.hint, 'nothing to measure');
  assert.equal(server.value, '—');
  assert.equal(server.hint, 'nothing to measure');
});

test('D-A-48: a real zero with requests present still reads 0', () => {
  const zero = pulse.pulseFigures([overviewRow('synchronous', 80, 60, 0, 0, 250)], 'synchronous', 'total', 'x');
  for (const id of ['client', 'server']) {
    assert.equal(zero.find(f => f.id === id).value, '0');
    assert.equal(zero.find(f => f.id === id).hint, '0.0% of requests');
  }
  const some = pulse.pulseFigures([overviewRow('synchronous', 80, 60, 4, 1, 250)], 'synchronous', 'total', 'x');
  assert.equal(some.find(f => f.id === 'client').value, '4');
  assert.equal(some.find(f => f.id === 'client').hint, '5.0% of requests');
  assert.equal(some.find(f => f.id === 'server').value, '1');
  assert.equal(some.find(f => f.id === 'server').hint, '1.3% of requests');
});

test('D-A-48: Pulse passes the top operation to the headline', () => {
  const src = read('src/components/Pulse.svelte');
  assert.match(src, /topRiseName\(analysis\.rollups \?\? \[\]\)/);
  assert.match(src, /heroHasData, pulseFigures, singleKind, topRiseName, type Lens/);
});
