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

const ledger = loadTypeScript('src/lib/ledger.ts');
const { lintScreenStrings } = loadTypeScript('src/lib/copy/copylint.ts');
const { deck } = loadTypeScript('src/lib/copy/deck.ts');

const LABELS = {
  open: 'Open',
  supported_by_correlation: 'Supported by correlation',
  supported_by_strong_correlation: 'Supported by strong correlation',
  contradicted: 'Contradicted',
  not_testable: 'Not testable with available evidence'
};

test('state labels are exactly the five agreed labels, in selector order', () => {
  assert.deepEqual(ledger.STATE_LABEL, LABELS);
  assert.deepEqual(ledger.STATE_ORDER, Object.keys(LABELS));
  for (const [state, label] of Object.entries(LABELS)) assert.equal(ledger.stateLabel(state), label);
});

test('no state label, state id or copy string contains "proven"', () => {
  const all = [...Object.values(ledger.STATE_LABEL), ...ledger.STATE_ORDER, ...Object.values(ledger.ledgerCopy), ...Object.values(ledger.ALT_STATUS_LABEL)];
  for (const text of all) assert.doesNotMatch(text, /prov(?:en|e|es|ed)\b/i, text);
  assert.equal(ledger.STATE_ORDER.length, 5);
});

test('every label, notice and fixed sentence passes the copy lint', () => {
  const strings = [
    ...Object.entries(ledger.STATE_LABEL).map(([k, v]) => ({ id: 'state.' + k, text: v })),
    ...Object.entries(ledger.ALT_STATUS_LABEL).map(([k, v]) => ({ id: 'alt.' + k, text: v })),
    ...Object.entries(ledger.ledgerCopy).map(([k, v]) => ({ id: 'copy.' + k, text: v })),
    { id: 'presence', text: ledger.presenceText(3, 3) },
    { id: 'presence.one', text: ledger.presenceText(1, 1) }
  ];
  assert.deepEqual(lintScreenStrings(strings, deck.terms), []);
});

test('the notice says the ledger keeps references only', () => {
  assert.equal(ledger.ledgerCopy.notice, 'The ledger keeps hypotheses and references only: no request rows, ids, users or IPs.');
});

test('presenceText reads "k of n episodes" and is empty when nothing is stated', () => {
  assert.equal(ledger.presenceText(3, 3), '3 of 3 episodes');
  assert.equal(ledger.presenceText(4, 5), '4 of 5 episodes');
  assert.equal(ledger.presenceText(1, 1), '1 of 1 episode');
  assert.equal(ledger.presenceText(0, 0), '');
  assert.equal(ledger.presenceText(-2, 3), '0 of 3 episodes');
  assert.equal(ledger.presenceText(NaN, NaN), '');
});

test('facts default to nothing stated and nothing ticked', () => {
  assert.deepEqual(ledger.defaultFacts(), {
    presentK: 0, totalN: 0, counterexamples: [], alternatives: [],
    timezoneRecorded: false, confounderOverlap: false, coverageGap: false, overlapOnly: false
  });
  assert.notEqual(ledger.defaultFacts(), ledger.defaultFacts());
});

function hypothesis(overrides = {}) {
  return {
    id: 'h1', text: 'The save slowed down at the same time as a job', state: 'open', evidenceIds: [], notes: '',
    facts: ledger.defaultFacts(), guards: [], createdAt: '2026-10-24T00:00:00Z', updatedAt: '2026-10-24T00:00:00Z', history: [], ...overrides
  };
}

test('the editor round-trips a hypothesis into the same input', () => {
  const facts = {
    presentK: 3, totalN: 3, counterexamples: ['Episode 4: absent'], alternatives: [{ text: 'Backup job', status: 'ruled_out' }],
    joinAmbiguousPct: 4.5, timezoneRecorded: true, confounderOverlap: false, coverageGap: false, overlapOnly: false
  };
  const h = hypothesis({ state: 'supported_by_correlation', evidenceIds: ['prov-1', 'prov-2'], notes: 'n', facts });
  assert.deepEqual(ledger.buildInput(ledger.editorFromHypothesis(h)), { text: h.text, state: h.state, evidenceIds: ['prov-1', 'prov-2'], notes: 'n', facts });
});

test('buildInput parses lists, trims, drops blanks and builds the committed input shape', () => {
  const editor = ledger.editorFromHypothesis(hypothesis());
  Object.assign(editor, {
    text: '  A hypothesis  ', state: 'contradicted', evidenceText: 'a, b\n c ,,a\r\n', notes: ' keep ',
    presentK: '2', totalN: '5', counterText: 'one, with comma\n\n  two  ',
    alternatives: [{ text: ' Cache ', status: 'open' }, { text: '   ', status: 'ruled_out' }],
    joinPct: '12.5', timezoneRecorded: true, coverageGap: true
  });
  assert.deepEqual(ledger.buildInput(editor), {
    text: 'A hypothesis', state: 'contradicted', evidenceIds: ['a', 'b', 'c'], notes: ' keep ',
    facts: {
      presentK: 2, totalN: 5, counterexamples: ['one, with comma', 'two'], alternatives: [{ text: 'Cache', status: 'open' }],
      timezoneRecorded: true, confounderOverlap: false, coverageGap: true, overlapOnly: false, joinAmbiguousPct: 12.5
    }
  });
});

test('join ambiguity is optional and clamped; numbers are whole and never negative', () => {
  const editor = ledger.editorFromHypothesis(hypothesis());
  assert.equal('joinAmbiguousPct' in ledger.buildInput(editor).facts, false);
  editor.joinPct = '150'; assert.equal(ledger.buildInput(editor).facts.joinAmbiguousPct, 100);
  editor.joinPct = '-3'; assert.equal(ledger.buildInput(editor).facts.joinAmbiguousPct, 0);
  editor.joinPct = '0'; assert.equal(ledger.buildInput(editor).facts.joinAmbiguousPct, 0);
  editor.joinPct = 'abc'; assert.equal('joinAmbiguousPct' in ledger.buildInput(editor).facts, false);
  Object.assign(editor, { presentK: '2.9', totalN: '-4' });
  assert.deepEqual([ledger.buildInput(editor).facts.presentK, ledger.buildInput(editor).facts.totalN], [2, 0]);
  Object.assign(editor, { presentK: 'x', totalN: '' });
  assert.deepEqual([ledger.buildInput(editor).facts.presentK, ledger.buildInput(editor).facts.totalN], [0, 0]);
});

test('rateProblem flags present above examined and nothing else', () => {
  assert.equal(ledger.rateProblem({ presentK: '3', totalN: '3' }), '');
  assert.equal(ledger.rateProblem({ presentK: '0', totalN: '0' }), '');
  assert.equal(ledger.rateProblem({ presentK: '4', totalN: '3' }), ledger.ledgerCopy.rateProblem);
});

test('parseList and parseLines split as described', () => {
  assert.deepEqual(ledger.parseList('x,y\nz, x'), ['x', 'y', 'z']);
  assert.deepEqual(ledger.parseList('  '), []);
  assert.deepEqual(ledger.parseLines('a, b\n\n c '), ['a, b', 'c']);
});

test('the editor never turns a state into another one', () => {
  for (const state of ledger.STATE_ORDER) {
    assert.equal(ledger.buildInput(ledger.editorFromHypothesis(hypothesis({ state }))).state, state);
  }
});

test('history is shown as an earlier state with a date, never as support', () => {
  assert.equal(ledger.historyLine({ state: 'supported_by_strong_correlation', at: '2026-10-20T10:00:00Z', note: 'earlier incident' }),
    'Supported by strong correlation, 2026-10-20, earlier incident');
  assert.equal(ledger.historyLine({ state: 'open', at: 'bad', note: '' }), 'Open');
  assert.match(ledger.ledgerCopy.historyNote, /never support/);
});
