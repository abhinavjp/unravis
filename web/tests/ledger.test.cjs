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
  const load = (request) => /^\.\.?\//.test(request) && fs.existsSync(path.join(path.dirname(filename), request + '.ts'))
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
    { id: 'presence.one', text: ledger.presenceText(1, 1) },
    { id: 'identifier.warning', text: ledger.identifierWarning('caused by user 1003 bursts') },
    { id: 'strong.progress', text: ledger.strongProgress(ledger.editorFromHypothesis(hypothesis())) }
  ];
  assert.deepEqual(lintScreenStrings(strings, deck.terms), []);
});

test('the notice says the ledger keeps references only', () => {
  assert.equal(ledger.ledgerCopy.notice, 'The ledger keeps hypotheses and references only. Do not paste request rows, ids, users or IPs.');
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
    id: 'h1', text: 'The save slowed down at the same time as a job', state: 'open', evidenceIds: [], notes: '', settlingObservation: '',
    facts: ledger.defaultFacts(), guards: [], createdAt: '2026-10-24T00:00:00Z', updatedAt: '2026-10-24T00:00:00Z', history: [], ...overrides
  };
}

test('the editor round-trips a hypothesis into the same input', () => {
  const facts = {
    presentK: 3, totalN: 3, counterexamples: ['Episode 4: absent'], alternatives: [{ text: 'Backup job', status: 'ruled_out' }],
    joinAmbiguousPct: 4.5, timezoneRecorded: true, confounderOverlap: false, coverageGap: false, overlapOnly: false
  };
  const h = hypothesis({ state: 'supported_by_correlation', evidenceIds: ['prov-1', 'prov-2'], notes: 'n', facts });
  assert.deepEqual(ledger.buildInput(ledger.editorFromHypothesis(h)), { text: h.text, state: h.state, evidenceIds: ['prov-1', 'prov-2'], notes: 'n', settlingObservation: '', facts });
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
    text: 'A hypothesis', state: 'contradicted', evidenceIds: ['a', 'b', 'c'], notes: ' keep ', settlingObservation: '',
    facts: {
      presentK: 2, totalN: 5, counterexamples: ['one, with comma', 'two'], alternatives: [{ text: 'Cache', status: 'open' }],
      timezoneRecorded: true, confounderOverlap: false, coverageGap: true, overlapOnly: false, joinAmbiguousPct: 12.5
    }
  });
});

test('join ambiguity is optional and never clamped into range; numbers are whole and never negative', () => {
  const editor = ledger.editorFromHypothesis(hypothesis());
  assert.equal('joinAmbiguousPct' in ledger.buildInput(editor).facts, false);
  assert.equal(ledger.joinProblem(editor), '');
  for (const bad of ['150', '100.5', '-3', '-0.1', 'abc', 'Infinity']) {
    editor.joinPct = bad;
    assert.equal(ledger.parsePercent(bad), undefined, bad);
    assert.equal('joinAmbiguousPct' in ledger.buildInput(editor).facts, false, bad);
    assert.equal(ledger.joinProblem(editor), ledger.ledgerCopy.joinProblem, bad);
  }
  for (const [ok, value] of [['0', 0], ['100', 100], [' 12.5 ', 12.5]]) {
    editor.joinPct = ok;
    assert.equal(ledger.buildInput(editor).facts.joinAmbiguousPct, value, ok);
    assert.equal(ledger.joinProblem(editor), '', ok);
  }
  editor.joinPct = '  '; assert.equal(ledger.joinProblem(editor), '');
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

test('whole-number boxes accept digits or nothing, and say so otherwise', () => {
  for (const ok of ['', '  ', '0', '12', ' 7 ']) assert.equal(ledger.numberProblem(ok), '', ok);
  for (const bad of ['abc', '1.5', '-2', '3a', '1 2']) assert.equal(ledger.numberProblem(bad), 'Enter a whole number.', bad);
});

test('isDirty compares what Save would send, so an untouched draft is clean', () => {
  const h = hypothesis({ evidenceIds: ['a'] });
  const editor = ledger.editorFromHypothesis(h);
  assert.equal(ledger.isDirty(h, editor), false);
  editor.notes = 'x';
  assert.equal(ledger.isDirty(h, editor), true);
  editor.notes = '';
  editor.evidenceText = 'a\n\n';
  assert.equal(ledger.isDirty(h, editor), false);
  editor.alternatives.push({ text: '  ', status: 'open' });
  assert.equal(ledger.isDirty(h, editor), false);
  editor.alternatives.push({ text: 'Cache', status: 'open' });
  assert.equal(ledger.isDirty(h, editor), true);
});

function readyEditor() {
  const editor = ledger.editorFromHypothesis(hypothesis({ evidenceIds: ['episode:a'] }));
  Object.assign(editor, {
    presentK: '3', totalN: '3', joinPct: '4', timezoneRecorded: true, state: 'supported_by_strong_correlation',
    alternatives: [{ text: 'Backup job', status: 'ruled_out' }]
  });
  return editor;
}

test('strong correlation checks match the server guards, in the same words', () => {
  const empty = ledger.editorFromHypothesis(hypothesis());
  assert.deepEqual(ledger.strongReasons(empty), [
    '0 independent episodes; strong correlation needs at least 3 (a volley counts as one episode).',
    'The presence rate is not stated (k of n episodes with the linked event present).',
    'No alternative explanation is named; name every plausible alternative with its status.',
    'The join class is not stated (share of rows with an ambiguous join).',
    "The source's timezone mapping is not recorded.",
    'No evidence references are linked.'
  ]);
  assert.equal(ledger.strongProgress(empty), '4 of 10 checks met for strong correlation.');
  const ready = readyEditor();
  assert.deepEqual(ledger.strongReasons(ready), []);
  assert.equal(ledger.strongProgress(ready), '10 of 10 checks met for strong correlation.');
});

test('each strong check blocks on its own', () => {
  const cases = [
    [{ coverageGap: true }, /coverage gap/],
    [{ totalN: '2', presentK: '2' }, /needs at least 3/],
    [{ totalN: '5', presentK: '3', counterText: 'a\nb' }, /below the minimum/],
    [{ totalN: '4', presentK: '4', joinPct: '10.5' }, /above the 10% limit/],
    [{ joinPct: '' }, /join class is not stated/],
    [{ joinPct: '150' }, /join class is not stated/],
    [{ timezoneRecorded: false }, /timezone mapping/],
    [{ confounderOverlap: true }, /confounder/],
    [{ overlapOnly: true }, /temporal overlap alone/],
    [{ evidenceText: ' , ' }, /No evidence references/],
    [{ alternatives: [{ text: ' ', status: 'open' }] }, /No alternative/]
  ];
  for (const [change, pattern] of cases) {
    const reasons = ledger.strongReasons(Object.assign(readyEditor(), change));
    assert.ok(reasons.some((r) => pattern.test(r)), JSON.stringify(change) + ' -> ' + reasons.join(' | '));
  }
  // One counterexample is needed for each episode where it was absent.
  const four = Object.assign(readyEditor(), { totalN: '5', presentK: '4', counterText: '' });
  assert.deepEqual(ledger.strongReasons(four), ['0 counterexample(s) listed, but 1 independent episode lacked the linked event; list each one.']);
  assert.deepEqual(ledger.strongReasons(Object.assign(four, { counterText: 'Episode 5: absent' })), []);
});

test('identifierWarning flags ids, IPs, emails and uuids, and leaves normal sentences alone', () => {
  const tail = 'The ledger should not hold identifiers.';
  assert.equal(ledger.identifierWarning('caused by user 1003 bursts'), 'This looks like a user id. ' + tail);
  assert.equal(ledger.identifierWarning('from 10.20.30.40 mostly'), 'This looks like an IP address. ' + tail);
  assert.equal(ledger.identifierWarning('fe80:0:0:0:1:2:3:4'), 'This looks like an IP address. ' + tail);
  assert.equal(ledger.identifierWarning('ask ana@example.com'), 'This looks like an email address. ' + tail);
  assert.equal(ledger.identifierWarning('req 3f2b8c1e-1111-4222-8333-444455556666'), 'This looks like an id. ' + tail);
  assert.equal(ledger.identifierWarning('tenant acme421 was busy'), 'This looks like an id. ' + tail);
  for (const fine of ['', 'The save slowed down with the nightly job', 'Episode 2 at 18:19:37, 3 of 3 episodes', 'episode:2026-10-24T18:19:37Z', 'Version 1.2.3 rolled out', 'user reports of slow saves']) {
    assert.equal(ledger.identifierWarning(fine), '', fine);
  }
});

test('citations are de-duplicated, trimmed, and fall back to the id for a blank label', () => {
  const first = ledger.addCitation([], ' episode:2026-10-24T18:19:37Z ', ' Episode 2 ');
  assert.deepEqual(first, [{ id: 'episode:2026-10-24T18:19:37Z', label: 'Episode 2' }]);
  assert.equal(ledger.addCitation(first, 'episode:2026-10-24T18:19:37Z', 'Again'), first);
  assert.equal(ledger.addCitation(first, '  ', 'x'), first);
  assert.deepEqual(ledger.addCitation(first, 'finding:slow', ' ').map((c) => c.label), ['Episode 2', 'finding:slow']);
  assert.deepEqual(first.length, 1);
});

test('appendEvidence keeps existing references, adds new ones once', () => {
  assert.equal(ledger.appendEvidence('a\nb', ['b', 'c']), 'a\nb\nc');
  assert.equal(ledger.appendEvidence('', ['x', 'x']), 'x');
  assert.equal(ledger.appendEvidence('a, b', []), 'a\nb');
});
