const assert = require('node:assert/strict');
const test = require('node:test');
const { loadTypeScript, change, unknown, step, suspect, result } = require('./guided.fixture.cjs');

const m = loadTypeScript('src/lib/guided/stepModel.ts');
const norm = loadTypeScript('src/lib/guided/normalize.ts');
const { lintCopy } = loadTypeScript('src/lib/copy/copylint.ts');
const { deck } = loadTypeScript('src/lib/copy/deck.ts');
const copy = loadTypeScript('src/lib/copy/guidedCopy.ts');

const WORDS = ['Ready', 'Partly ready', 'Cannot test yet', 'Failed'];

test('the rail has eleven steps, 0 to 10, and shows the engine word for every state', () => {
  assert.equal(m.STEPS.length, 11);
  assert.deepEqual(m.STEPS.map((s) => s.number), [0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10]);
  for (const word of WORDS) {
    const r = result({ steps: Array.from({ length: 11 }, (_, i) => step(i, word)) });
    for (let n = 0; n <= 10; n++) assert.equal(m.stickerFor(r, n, 'done').word, word);
  }
  assert.equal(m.stickerFor(result(), 3, 'done').tone, 'ready');
  assert.equal(m.stickerFor(result({ steps: [step(3, 'Partly ready')] }), 3, 'done').tone, 'partly');
  assert.equal(m.stickerFor(result({ steps: [step(3, 'Cannot test yet')] }), 3, 'done').tone, 'cannot');
  assert.equal(m.stickerFor(result({ steps: [step(3, 'Failed')] }), 3, 'done').tone, 'failed');
});

test('before an answer a step is Waiting, during a run Running, and neither is a pass word', () => {
  assert.deepEqual(m.stickerFor(null, 4, 'idle'), { word: 'Waiting', tone: 'waiting', outOfDate: false });
  assert.equal(m.stickerFor(null, 4, 'failed').word, 'Waiting');
  assert.equal(m.stickerFor(result(), 4, 'running').word, 'Running');
  assert.equal(m.stickerFor(result({ steps: [] }), 4, 'done').word, 'Waiting');
});

test('out of date marks every step after Step 0 and leaves the word alone', () => {
  const items = m.railItems(result(), 'done', 2, true);
  assert.equal(items[0].sticker.outOfDate, false);
  assert.ok(items.slice(1).every((i) => i.sticker.outOfDate && WORDS.includes(i.sticker.word)));
  assert.equal(items.filter((i) => i.current).length, 1);
});

test('an unknown readiness word from the server reads as Cannot test yet, never as a pass', () => {
  const r = norm.normalizeResult({ steps: [{ number: 1, name: 'x', readiness: 'Passed' }, { number: 2, readiness: 'Ready' }] });
  assert.equal(r.steps[0].readiness, 'Cannot test yet');
  assert.equal(r.steps[1].readiness, 'Ready');
});

test('clampStep keeps a step in range and survives junk', () => {
  assert.equal(m.clampStep(-4), 0);
  assert.equal(m.clampStep(99), 10);
  assert.equal(m.clampStep(NaN), 0);
  assert.equal(m.clampStep(2.5), 0);
  assert.equal(m.clampStep(10), 10);
});

test('numbers that are missing read as No data and never as zero', () => {
  assert.equal(m.fmtMs(undefined), 'No data');
  assert.equal(m.fmtMs(NaN), 'No data');
  assert.equal(m.fmtMs(0), '0 ms');
  assert.equal(m.fmtCount(undefined), 'No data');
  assert.equal(m.fmtPct(undefined), 'No data');
  assert.equal(m.fmtPct(733.4), '+733 %');
  assert.equal(m.fmtPct(-12), '-12 %');
  assert.equal(m.fmtWhen('nonsense'), 'No data');
  assert.equal(m.fmtWhen('2026-10-17T10:42:00Z'), '17 Oct 10:42');
  assert.equal(m.fmtChange(unknown()), 'No data');
  assert.equal(m.fmtChange(change(500, 3000)), '+500 % (+2,500 ms)');
});

test('the change mark follows the rule, and an unknown change is grey', () => {
  assert.equal(m.markFor(change(500, 3000)).symbol, '▲');
  assert.equal(m.markFor(change(3000, 500)).symbol, '▼');
  assert.equal(m.markFor(change(400, 410)).symbol, '●');
  assert.equal(m.markFor(change(400, 410)).tone, 'grey');
  assert.equal(m.markFor(unknown()).word, 'Not sure');
  assert.equal(m.markFor(undefined).tone, 'grey');
});

test('Step 1 cards say when a window is empty and sources split into Ready and Cannot test yet', () => {
  const r = result();
  const cards = m.trustCards(r);
  assert.equal(cards.length, 4);
  assert.equal(cards[0].value, '400');
  const empty = result({ facts: { ...r.facts, counts: { ...r.facts.counts, incident: 0 } } });
  assert.equal(m.notEnoughData(empty), true);
  assert.equal(m.notEnoughData(r), false);
  assert.equal(m.trustCards(empty)[0].note, 'No call started in this window.');
  const rows = m.sourceRows(r);
  assert.deepEqual(rows.map((x) => [x.name, x.state]), [['Audit', 'Ready'], ['MongoDB', 'Cannot test yet']]);
});

test('Step 2 lists the biggest rise first, counts what moved, and knows when nothing moved', () => {
  const r = result();
  const rows = m.shapeRows(r);
  assert.equal(rows[0].route, 'A.One');
  assert.equal(rows[0].mark.symbol, '▲');
  assert.equal(m.movedCount(r), 1);
  assert.equal(m.nothingMoved(r), false);
  const flat = result({ facts: { ...r.facts, routes: [r.facts.routes[1]] } });
  assert.equal(m.nothingMoved(flat), true);
  const none = result({ facts: { ...r.facts, routes: [] } });
  assert.equal(m.nothingMoved(none), false);
  const unk = result({ facts: { ...r.facts, routes: [{ route: 'X.Y', incident: { n: 1 }, reference: { n: 0 }, p95Change: unknown() }] } });
  assert.equal(m.nothingMoved(unk), false);
  assert.equal(m.shapeRows(unk)[0].incident, 'No data');
});

test('Step 3 draws only tested parts, on one scale, and names the part that could not be tested', () => {
  const r = result();
  const rows = m.partRows(r);
  assert.equal(rows[0].label, 'Audit set-up (A)');
  const o = rows.find((x) => x.key === 'O');
  assert.equal(o.tested, false);
  assert.equal(o.change, 'Cannot test yet');
  assert.equal(o.normal, 'No data');
  const bars = m.partBars(r);
  assert.deepEqual(bars.incident.map((b) => b.key), ['A', 'C']);
  assert.equal(Math.round(bars.incident.reduce((t, b) => t + b.pct, 0)), 100);
  assert.ok(bars.normal.reduce((t, b) => t + b.pct, 0) < 100);
  assert.ok(bars.incident.every((b) => b.ms > 0));
});

test('Step 4 shows four load measures, with No data for a missing figure', () => {
  const r = result();
  const rows = m.loadRows(r);
  assert.equal(rows.length, 4);
  assert.equal(rows[2].direction, 'Higher');
  const gone = result({ facts: { ...r.facts, load: { ...r.facts.load, incL: undefined, refL: undefined, lDirection: 'unknown' } } });
  assert.equal(m.loadRows(gone)[2].incident, 'No data');
  assert.equal(m.loadRows(gone)[2].direction, 'Not sure');
});

test('steps 6 to 9 each read their own suspect group and Step 10 sorts into three groups', () => {
  const r = result();
  assert.deepEqual(m.suspectsForStep(r, 4).map((s) => s.id), ['L1']);
  assert.deepEqual(m.suspectsForStep(r, 7).map((s) => s.id), ['D1']);
  assert.deepEqual(m.suspectsForStep(r, 8).map((s) => s.id), ['M1']);
  assert.deepEqual(m.suspectsForStep(r, 9).map((s) => s.id), ['H1']);
  assert.deepEqual(m.suspectsForStep(r, 2), []);
  const g = m.suspectGroups(r);
  assert.deepEqual(g.ranked.map((s) => s.id), ['L1', 'D1']);
  assert.deepEqual(g.notTested.map((s) => s.id), ['M1']);
  assert.deepEqual(g.setAside.map((s) => s.id), ['H1']);
});

test('the cheapest check is the first red or orange suspect check; none when nothing is ranked', () => {
  assert.equal(m.cheapestCheck(result()).suspect.id, 'L1');
  const greyOnly = result({ suspects: [suspect('M1', 'grey')] });
  assert.equal(m.cheapestCheck(greyOnly), null);
  assert.equal(m.cheapestCheck(result({ suspects: [] })), null);
});

test('origin rows and the named, no-clear-origin and not-enough-data states', () => {
  const r = result();
  assert.equal(m.hasNamedOrigin(r), true);
  assert.equal(m.originRows(r)[0].when, '17 Oct 10:42');
  const none = result({ origin: { status: 'no clear origin', roles: [{ tier: 'app', label: 'App', role: 'Moved with others', why: 'x' }, { tier: 'sql', label: 'SQL', role: 'Moved with others', why: 'y' }], note: '', clockNote: '' } });
  assert.equal(m.hasNamedOrigin(none), false);
  assert.equal(m.originRows(none)[0].when, 'Not dated');
  const bare = norm.normalizeResult({});
  assert.equal(bare.origin.status, 'not enough data');
  assert.deepEqual(m.originRows(bare), []);
  assert.deepEqual(m.suspectGroups(bare), { ranked: [], notTested: [], setAside: [] });
});

test('a grey suspect is never called observed, and a tier that moved on its own data is', () => {
  const grey = suspect('M1', 'grey', { evidence: [{ signal: 's', role: 'tier', value: 'yes', why: '' }] });
  assert.equal(m.evidenceKind(grey), 'Inferred');
  const red = suspect('L1', 'red', { evidence: [{ signal: 's', role: 'tier', value: 'yes', why: '' }] });
  assert.equal(m.evidenceKind(red), 'Observed');
  assert.equal(m.evidenceKind(suspect('L2', 'red')), 'Inferred');
});

test('the checklist groups by list, counts done and open, and suggests each check once', () => {
  const mk = (id, list, text, order = 0) => ({ id, list, text, order, created: '2026-10-17T00:00:0' + id + 'Z', updated: '' });
  const items = [mk('1', 'todo', 'a'), mk('2', 'doing', 'b'), mk('3', 'learned', 'c'), mk('4', 'ruled_out', 'd'), mk('5', 'todo', 'e', -1)];
  const g = m.groupItems(items);
  assert.deepEqual(g.todo.map((i) => i.id), ['5', '1']);
  assert.deepEqual(m.counters(items), { done: 2, open: 3 });
  assert.deepEqual(m.counters([]), { done: 0, open: 0 });
  const r = result();
  const first = m.suggestedChecks(r, []);
  assert.deepEqual(first.map((x) => x.suspect), ['L1', 'D1', 'M1']);
  const again = m.suggestedChecks(r, first.map((x, i) => mk(String(i + 1), 'todo', x.text)));
  assert.deepEqual(again, []);
  const dup = result({ suspects: [suspect('L1', 'red'), suspect('L1', 'red')] });
  assert.equal(m.suggestedChecks(dup, []).length, 1);
  assert.ok(m.suggestedChecks(result({ suspects: [suspect('L1', 'red', { nextCheck: 'x'.repeat(400) })] }), [])[0].text.length <= 300);
});

test('normalizers survive missing and wrong-typed fields', () => {
  const a = norm.normalizeAnswer({ result: null, read: { requests: 'x', warnings: [1, 'ok'] } });
  assert.deepEqual(a.read, { requests: 0, iisRows: 0, truncated: false, warnings: ['ok'] });
  assert.deepEqual(a.result.steps, []);
  assert.equal(a.result.thresholds.changeMinPercent, 30);
  assert.deepEqual(norm.normalizeAnswer(undefined).result.suspects, []);
  assert.equal(norm.normalizeItem({ list: 'bogus' }).list, 'todo');
  assert.deepEqual(norm.normalizeView('junk'), { id: '', name: '', tool: '', saved: '', settings: {} });
  assert.equal(norm.normalizeReport({ format: 'csv' }).format, 'csv');
  assert.equal(norm.normalizeReport({ bytes: 'x' }).bytes, 0);
});

test('the guided copy passes the copy lint and carries the fixed status words', () => {
  assert.deepEqual(lintCopy(copy.guidedLintEntries(), deck.terms), []);
  assert.ok(copy.GUIDED_TEXT.nutshell === 'In a nutshell');
  assert.equal(copy.refusalText('case-closed', copy.SAVED_VIEW_ERRORS, 'x'), copy.SAVED_VIEW_ERRORS['case-closed']);
  assert.equal(copy.refusalText('zzz', copy.SAVED_VIEW_ERRORS, 'fallback'), 'fallback');
});
