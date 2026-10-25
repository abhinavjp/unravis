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

const ledger = loadTypeScript('src/lib/ledger.ts');
const { theoryRows, clip, FIRST_COUNTER_MAX } = loadTypeScript('src/lib/theories.ts');
const { theoriesCopy, allTheoriesStrings } = loadTypeScript('src/lib/copy/theoriesCopy.ts');
const { lintScreenStrings } = loadTypeScript('src/lib/copy/copylint.ts');
const { deck } = loadTypeScript('src/lib/copy/deck.ts');

function hypothesis(over = {}) {
  return {
    id: 'h1', text: 'The audit insert waits on a lock', state: 'open', evidenceIds: [], notes: '', settlingObservation: '',
    facts: { presentK: 3, totalN: 4, counterexamples: [], alternatives: [], timezoneRecorded: false, confounderOverlap: false, coverageGap: false, overlapOnly: false },
    guards: [], createdAt: '2001-10-22T00:00:00Z', updatedAt: '2001-10-22T00:00:00Z', history: [], ...over
  };
}

test('every fixed string of the matrix and the settling box passes the copy lint', () => {
  const strings = allTheoriesStrings();
  assert.ok(strings.length >= 15);
  assert.deepEqual(lintScreenStrings(strings, deck.terms), []);
});

test('the three column headings use the agreed words', () => {
  assert.equal(theoriesCopy.columnSupports, 'What the data supports');
  assert.equal(theoriesCopy.columnCannotExplain, 'What it cannot explain');
  assert.equal(theoriesCopy.columnSettle, 'What would settle it');
  assert.equal(theoriesCopy.settlingLabel, 'One observation that would settle this');
  assert.equal(theoriesCopy.compareToggle, 'Compare theories');
  assert.equal(theoriesCopy.notWritten, 'not written yet');
});

test('a row shows k of n, the counterexample count with the first one, and the settling observation', () => {
  const rows = theoryRows([hypothesis({
    state: 'supported_by_correlation',
    settlingObservation: '  The stall is gone on the next quiet night.  ',
    facts: { ...hypothesis().facts, counterexamples: ['Stall on 22 Oct with no lock wait', 'Second one'] }
  })]);
  assert.equal(rows.length, 1);
  assert.deepEqual(rows[0], {
    id: 'h1', text: 'The audit insert waits on a lock', state: 'Supported by correlation', supports: '3 of 4 episodes',
    cannotExplain: '2 listed', firstCounter: 'Stall on 22 Oct with no lock wait', settle: 'The stall is gone on the next quiet night.'
  });
});

test('a theory with nothing stated reads as not stated, none listed and no observation', () => {
  const [row] = theoryRows([hypothesis({ facts: { ...hypothesis().facts, presentK: 0, totalN: 0 } })]);
  assert.equal(row.supports, 'Not stated');
  assert.equal(row.cannotExplain, 'None listed');
  assert.equal(row.firstCounter, '');
  assert.equal(row.settle, '');
});

test('singular episode, blank counterexamples ignored, old entries without the field are fine', () => {
  const old = hypothesis();
  delete old.settlingObservation;
  const [row] = theoryRows([{ ...old, facts: { ...old.facts, presentK: 1, totalN: 1, counterexamples: ['  ', '', 'Only real one'] } }]);
  assert.equal(row.supports, '1 of 1 episode');
  assert.equal(row.cannotExplain, '1 listed');
  assert.equal(row.firstCounter, 'Only real one');
  assert.equal(row.settle, '');
});

test('rows keep ledger order and skip entries with no text', () => {
  const rows = theoryRows([hypothesis({ id: 'a', text: 'First' }), hypothesis({ id: 'b', text: '   ' }), hypothesis({ id: 'c', text: 'Third' })]);
  assert.deepEqual(rows.map((r) => r.id), ['a', 'c']);
  assert.deepEqual(theoryRows([]), []);
});

test('the matrix never ranks, scores or suggests a state', () => {
  const [row] = theoryRows([hypothesis()]);
  assert.deepEqual(Object.keys(row).sort(), ['cannotExplain', 'firstCounter', 'id', 'settle', 'state', 'supports', 'text']);
});

test('a long first counterexample is cut at a word edge with an ellipsis', () => {
  const long = 'word '.repeat(60).trim();
  const out = clip(long);
  assert.ok(out.length <= FIRST_COUNTER_MAX);
  assert.ok(out.endsWith('…'));
  assert.equal(clip('short text'), 'short text');
  assert.equal(clip('a  b\n c'), 'a b c');
  assert.equal(clip('x'.repeat(200)).length, FIRST_COUNTER_MAX);
});

test('the editor carries the settling observation and Save sends it trimmed', () => {
  const h = hypothesis({ settlingObservation: 'Look at the next quiet night.' });
  const editor = ledger.editorFromHypothesis(h);
  assert.equal(editor.settling, 'Look at the next quiet night.');
  assert.equal(ledger.buildInput(editor).settlingObservation, 'Look at the next quiet night.');
  editor.settling = '   spaced out   ';
  assert.equal(ledger.buildInput(editor).settlingObservation, 'spaced out');
  assert.equal(ledger.SETTLING_MAX, 500);
});

test('an old entry without the field opens with an empty box and is not marked unsaved', () => {
  const old = hypothesis();
  delete old.settlingObservation;
  const editor = ledger.editorFromHypothesis(old);
  assert.equal(editor.settling, '');
  assert.equal(ledger.isDirty(old, editor), false);
});

test('typing in the settling box marks the entry unsaved; clearing it back does not', () => {
  const h = hypothesis({ settlingObservation: 'Original' });
  const editor = ledger.editorFromHypothesis(h);
  editor.settling = 'Changed';
  assert.equal(ledger.isDirty(h, editor), true);
  editor.settling = 'Original ';
  assert.equal(ledger.isDirty(h, editor), false);
});
