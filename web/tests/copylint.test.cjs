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

const { lintCopy, lintTerms, lintScreenStrings, fleschKincaidGrade, JARGON } = loadTypeScript('src/lib/copy/copylint.ts');
const wordings = loadTypeScript('src/lib/copy/wordings.ts');

function term(name, aliases = []) {
  return { id: name.toLowerCase().replace(/\W+/g, '-'), term: name, hint: 'h', definition: 'd', notMeaning: 'n', aliases };
}
const TERMS = [
  term('P50', ['median']), term('P95'), term('P99'), term('Percentile'), term('Coverage'), term('Episode'),
  term('Correlation'), term('Join'), term('Baseline'), term('Sampling'), term('Low sample')
];
const lint = (text, terms = TERMS) => lintCopy([{ id: 'x', text }], terms);
const rules = (text, terms) => lint(text, terms).map((f) => f.rule);

const CAUSAL = ['caused', 'because', 'waiting on', 'blocked by', 'due to', 'the database was waiting', 'the API waited for'];
const WEAK_LABELS = ['Symptom', 'Correlation', 'symptom', 'CORRELATION'];

const POSITIVE = [
  'Saving the record took about 28 seconds before the action ran, as measured from the application side.',
  wordings.plainAuditCreate(28),
  wordings.notObservedThreshold('system_health', 'lock waits above 30 s'),
  wordings.notObservedGap('the source was off from 14:40 to 14:45'),
  wordings.overlapUnique('SQL Server', 'lock wait', 28),
  wordings.overlapAmbiguous(3),
  wordings.auditCreate(28),
  `${wordings.overlapAmbiguous(2)} ${wordings.auditCreate(31.5)}`,
  'Strong correlation',
  'Symptom: the saving step was slower than usual.',
  'Correlation: two things happened together. It does not prove one caused the other.',
  'This does not show that the database was waiting.',
  'It is not proof of a cause.',
  'The middle response time was 90 ms. One in twenty requests took longer than 400 ms.',
  'Coverage is the share of the window that the source recorded.',
  'The P95 is the time that 95 out of 100 requests beat.',
  'It does not prove one caused the other.',
  "It doesn't prove that the slow save was caused by the database.",
  "This isn't proof that the lock was the reason.",
  "It can't show that the save was slow due to the lock.",
  'This cannot prove that the cause was the lock.',
  'It is not the cause of the delay.',
  'It does not mean the queue is responsible for the delay.',
  `${wordings.auditCreate(28)} This was slow.`,
  'Short.',
  ''
];

test('positive fixtures pass the copy lint', () => {
  for (const text of POSITIVE) assert.deepEqual(lint(text), [], text);
});

test('the section 8.14 plain example passes on its own', () => {
  assert.deepEqual(lint('Saving the record took about 28 seconds before the action ran, as measured from the application side.', []), []);
});

test('each causal phrase with each weak label fails', () => {
  for (const phrase of CAUSAL) {
    for (const label of WEAK_LABELS) {
      for (const text of [
        `${label}: the slow save happened ${phrase} the database.`,
        `The database ${phrase} the delay (${label}).`,
        `${label} ${phrase.toUpperCase()} the slow save.`
      ]) {
        assert.ok(rules(text).includes('causal'), text);
      }
    }
  }
});

test('causal phrases fail with no label, and with only the strong label', () => {
  for (const phrase of CAUSAL) {
    assert.ok(rules(`The save was slow ${phrase} the database.`).includes('causal'), phrase);
    assert.ok(rules(`Strong correlation: the save was slow ${phrase} the database.`).includes('causal'), phrase);
  }
});

test('"Strong correlation" alone is not a weak label, but the weak label next to it still is', () => {
  const message = lint('Strong correlation: slow because of the queue.')[0].message;
  assert.match(message, /names a cause/);
  const weak = lint('Strong correlation, but only a Symptom here: slow because of the queue.')[0].message;
  assert.match(weak, /weak evidence label/);
});

test('"proven" claims fail; a negated "prove" or "proof" passes', () => {
  assert.ok(rules('The database delay is proven.').includes('causal'));
  assert.ok(rules('This proves the cause.').includes('causal'));
  assert.ok(rules('The link has been proven by the data.').includes('causal'));
  assert.ok(rules('That is proof of the cause.').includes('causal'));
  assert.deepEqual(lint('This does not prove anything.'), []);
  assert.deepEqual(lint('That is not proof of anything.'), []);
  assert.ok(rules('This is not proven.').includes('causal'));
});

test('paraphrased section 6.6 wordings fail', () => {
  const paraphrases = [
    'Not observed in system_health. It only records waits above 30 s.',
    'Not observed, so SQL was not waiting.',
    'Not observed in system_health; this source only records lock waits above 30 s.',
    'Not observed (sampling gap: the source was off).',
    'A SQL Server session had a lock wait of 28 s overlapping this interval.',
    'Possible overlap with 3 candidates.',
    'Possible overlap (ambiguous: 3 options).',
    'Audit create took 28 s from the database side.',
    'Audit create took 28 seconds as measured from the client side.',
    `Result: ${wordings.overlapAmbiguous(3)}`,
    `Detail. ${wordings.notObservedGap('the source was off')} Not observed (sampling gap: the source was off).`,
    'Some words first. Audit create took 28 seconds from the database side. Some words after.',
    `Intro sentence here. ${wordings.overlapUnique('SQL', 'lock wait', 28).replace(' (Correlation)', '')} More.`
  ];
  for (const text of paraphrases) {
    assert.ok(rules(text, TERMS).includes('wording'), text);
  }
});

test('wordings are matched from the same templates for any argument', () => {
  assert.deepEqual(lint(wordings.notObservedThreshold('a, b (c)', '15 s: latch')), []);
  assert.deepEqual(lint(wordings.overlapUnique('SQL', 'lock wait', 0.5), TERMS), []);
});

test('a hard-to-read paragraph fails the reading level', () => {
  const hard = 'Notwithstanding considerable methodological heterogeneity, the aforementioned instrumentation demonstrates substantially disproportionate computational responsiveness characteristics throughout administrative infrastructure environments.';
  assert.ok(fleschKincaidGrade(hard) > 8.5);
  assert.ok(rules(hard).includes('reading-level'));
});

test('reading level helper behaves on plain and empty text', () => {
  assert.ok(fleschKincaidGrade('The cat sat on the mat. The dog ran to the park.') < 3);
  assert.equal(fleschKincaidGrade(''), 0);
  assert.ok(Number.isFinite(fleschKincaidGrade('P95 was 12.5 ms.')));
});

test('short entries skip the reading-level check', () => {
  assert.deepEqual(lint('Instrumentation heterogeneity'), []);
});

test('an undefined jargon word fails; a glossary term or alias passes', () => {
  assert.ok(rules('The P95 was high today.', []).includes('undefined-term'));
  assert.deepEqual(lint('The P95 was high today.'), []);
  assert.ok(rules('Sampling can hide short events.', TERMS.filter((t) => t.term !== 'Sampling')).includes('undefined-term'));
  assert.deepEqual(lint('The typical request was quick.', []), []);
  const aliased = [term('Typical time', ['median', 'p50'])];
  assert.deepEqual(lint('The p50 is shown here.', aliased), []);
  assert.deepEqual(lint('Each episode is listed.', [term('Episode', [])]), []);
  assert.deepEqual(lint('Two joins were used.', [term('Join', [])]), []);
});

test('every JARGON word is flagged when the glossary is empty', () => {
  assert.ok(JARGON.length >= 9);
  for (const word of ['P50', 'P95', 'P99', 'coverage', 'episode', 'correlation', 'join', 'baseline', 'sampling']) {
    assert.ok(JARGON.includes(word), word);
  }
  for (const word of JARGON) {
    const findings = lint(`The ${word} is shown.`, []);
    assert.ok(findings.some((f) => f.rule === 'undefined-term'), word);
  }
});

test('findings carry the entry id', () => {
  const findings = lintCopy([{ id: 'a', text: 'Fine words.' }, { id: 'b', text: 'It was slow because of that.' }], TERMS);
  assert.deepEqual(findings.map((f) => f.id), ['b']);
  assert.equal(findings[0].rule, 'causal');
});

// ---- Review fixes: scoped negation ----

test('a negation exempts a causal word only inside its own clause', () => {
  const failing = [
    'This does not prove a link, but it is because of the database.',
    'It does not show much, yet the save was slow due to the lock.',
    'It does not mean X, it was due to SQL.',
    'It does not mean X; it was due to SQL.',
    'It does not mean X. It was due to SQL.',
    'It does not prove a link, and the lock is the reason.',
    "It doesn't show much, but the save was blocked by the lock.",
    'It was not slow and it was blocked by the queue.'
  ];
  for (const text of failing) assert.ok(rules(text).includes('causal'), text);
  const passing = [
    'It does not prove one caused the other.',
    "doesn't prove that X caused Y",
    "It doesn't prove that X caused Y.",
    "It isn't proof of a link caused by the lock.",
    "It can't tell us the save was slow because of the lock.",
    'It cannot show that the delay was due to the lock.',
    'It is not because of the lock.'
  ];
  for (const text of passing) assert.deepEqual(lint(text), [], text);
});

test('contractions count as negation for "prove" and "proof"', () => {
  assert.deepEqual(lint("This doesn't prove anything."), []);
  assert.deepEqual(lint("That isn't proof of anything."), []);
  assert.deepEqual(lint("It can't prove a link."), []);
  assert.deepEqual(lint('It cannot prove a link.'), []);
  assert.ok(rules('This will prove the link.').includes('causal'));
  assert.ok(rules('That is proof of the link.').includes('causal'));
});

// ---- Review fixes: more causal phrases ----

test('more causal phrases fail alone, and pass when negated', () => {
  const failing = [
    'The save took 28 seconds and the root cause was the lock.',
    'The save was slow. The cause of the delay was the queue.',
    'The lock causes slow saves.',
    'The lock is causing slow saves.',
    'A cause was found for the delay.',
    'The save waited for the database.',
    'The save is waiting for the lock.',
    'The queue is the reason for the delay.',
    'The lock was the reason.',
    'The lock led to the delay.',
    'The delay resulted from the lock.',
    'The lock results in slow saves.',
    'The queue is responsible for the delay.'
  ];
  for (const text of failing) {
    assert.deepEqual(rules(text), ['causal'], text);
  }
  const passing = [
    'It does not mean the lock causes slow saves.',
    'This does not show the root cause.',
    'It is not the cause of the slow save.',
    "It doesn't show that the save waited for the database.",
    'It does not prove the queue led to the delay.',
    'It does not show that the delay resulted from the lock.',
    'The save took 28 seconds. Nothing here is a reason to worry.'
  ];
  for (const text of passing) assert.deepEqual(lint(text), [], text);
});

// ---- Review fixes: wording inside longer text ----

test('a permitted wording may be a whole sentence inside longer text', () => {
  const texts = [
    `Details follow. ${wordings.notObservedGap('the source was off from 14:40 to 14:45')} Check the coverage note.`,
    `${wordings.notObservedThreshold('system_health', 'lock waits above 30 s')} Nothing else is known.`,
    `Here is what we saw. ${wordings.overlapUnique('SQL Server', 'lock wait', 28)} It is listed once.`,
    `The save was slow. ${wordings.auditCreate(28)}`,
    `One. ${wordings.overlapAmbiguous(3)} Two.`,
    `Intro here.\n${wordings.auditCreate(28)}\nOutro here.`
  ];
  for (const text of texts) assert.deepEqual(lint(text), [], text);
});

test('a paraphrased wording inside longer text still fails', () => {
  const texts = [
    'Details follow. Not observed (sampling gap: the source was off). Done.',
    `${wordings.notObservedGap('the source was off')} Not observed in system_health. It only records waits above 30 s.`,
    'The save was slow. Audit create took 28 seconds from the database side.',
    `Intro sentence. Possible overlap with 3 candidates. ${wordings.auditCreate(28)}`,
    'One. A SQL Server session had a lock wait of 28 s overlapping this interval. Two.',
    `Note: ${wordings.auditCreate(28)}`
  ];
  for (const text of texts) assert.ok(rules(text, []).includes('wording'), text);
});

test('reading level scores the remainder without the exact permitted sentences', () => {
  const hardDetail = 'notwithstanding considerable methodological heterogeneity throughout administrative infrastructure environments';
  const gap = wordings.notObservedGap(hardDetail);
  const text = `The save was slow today. ${gap} It was then fast again.`;
  assert.ok(fleschKincaidGrade(text) > 8.5);
  assert.deepEqual(rules(text), []);
  const hardRemainder = 'Notwithstanding considerable methodological heterogeneity, the aforementioned instrumentation demonstrates substantially disproportionate computational responsiveness characteristics throughout administrative infrastructure environments.';
  assert.ok(rules(`${wordings.auditCreate(28)} ${hardRemainder}`, []).includes('reading-level'));
});

// ---- Review fixes: term lint and screen strings ----

function fullTerm(name, fields = {}) {
  return {
    id: name.toLowerCase().replace(/\W+/g, '-'),
    term: name,
    hint: 'A short plain hint.',
    definition: 'A plain definition. It has two short sentences.',
    notMeaning: 'It does not mean more than it says.',
    ...fields
  };
}

test('lintTerms passes plain terms and exempts the bare label from the wording rule only', () => {
  const terms = [fullTerm('Not observed'), fullTerm('Strong correlation'), fullTerm('Possible overlap'), fullTerm('Symptom')];
  assert.deepEqual(lintTerms(terms), []);
});

test('lintTerms checks the hint, definition and notMeaning fields', () => {
  const cases = [
    [{ hint: 'The save was slow because of the lock.' }, 'causal', 'hint'],
    [{ definition: 'It is the root cause. It is short.' }, 'causal', 'definition'],
    [{ notMeaning: 'It is proven to be true.' }, 'causal', 'notMeaning'],
    [{ hint: 'Not observed (sampling gap: the source was off).' }, 'wording', 'hint'],
    [{ definition: 'Say this. Audit create took 28 seconds from the database side.' }, 'wording', 'definition'],
    [{ notMeaning: 'The P95 is a term.' }, 'undefined-term', 'notMeaning']
  ];
  for (const [fields, rule, field] of cases) {
    const findings = lintTerms([fullTerm('Probe', fields)]);
    assert.ok(findings.some((f) => f.rule === rule && f.id === `probe.${field}`), `${field}: ${JSON.stringify(findings)}`);
  }
});

test('lintTerms also lints the name, except a bare wording label', () => {
  const caused = lintTerms([fullTerm('Slow because of the lock')]);
  assert.ok(caused.some((f) => f.rule === 'causal'));
  const phrase = lintTerms([fullTerm('Not observed because the lock waited')]);
  assert.ok(phrase.some((f) => f.rule === 'wording'));
  assert.ok(phrase.some((f) => f.rule === 'causal'));
  assert.deepEqual(lintTerms([fullTerm('Not observed')]), []);
  assert.deepEqual(lintTerms([]), []);
});

test('lintScreenStrings is the entry point for fixed UI strings and matches lintCopy', () => {
  const entries = [{ id: 'a', text: 'Fine words.' }, { id: 'b', text: 'It was slow because of that.' }, { id: 'c', text: 'Not observed (sampling gap: x).' }];
  assert.deepEqual(lintScreenStrings(entries, TERMS), lintCopy(entries, TERMS));
  assert.deepEqual(lintScreenStrings(entries, TERMS).map((f) => f.id), ['b', 'c']);
});
