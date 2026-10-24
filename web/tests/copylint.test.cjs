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

const { lintCopy, fleschKincaidGrade, JARGON } = loadTypeScript('src/lib/copy/copylint.ts');
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
    `${wordings.auditCreate(28)} This was slow.`,
    `Result: ${wordings.overlapAmbiguous(3)}`
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
