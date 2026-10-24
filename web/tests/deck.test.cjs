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

const { deck } = loadTypeScript('src/lib/copy/deck.ts');
const terms = deck.terms;

const REQUIRED_IDS = [
  'p50', 'p95', 'coverage', 'episode', 'correlation', 'strong-correlation', 'symptom', 'evidence-label',
  'join', 'not-observed', 'time-outside-the-pipeline', 'low-sample'
];
const NOT_MEANING_REQUIRED = [
  'correlation', 'strong-correlation', 'symptom', 'evidence-label', 'not-observed', 'low-sample', 'coverage',
  'p95', 'p50', 'join', 'episode', 'time-outside-the-pipeline'
];
const BANNED = ['caused', 'because', 'waiting on', 'blocked by', 'due to', 'the database was waiting', 'proven'];
const words = (text) => text.trim().split(/\s+/).filter(Boolean);
const sentences = (text) => text.trim().split(/[.!?]+(?:\s+|$)/).filter((part) => part.trim().length > 0);
const allText = (term) => [term.term, term.hint, term.definition, term.notMeaning, ...(term.aliases ?? [])];

test('deck ids are unique kebab-case', () => {
  const ids = terms.map((term) => term.id);
  assert.equal(new Set(ids).size, ids.length);
  for (const id of ids) assert.match(id, /^[a-z0-9]+(-[a-z0-9]+)*$/, id);
});

test('deck has every required id and a sensible size', () => {
  const ids = new Set(terms.map((term) => term.id));
  for (const id of REQUIRED_IDS) assert.ok(ids.has(id), `missing term ${id}`);
  assert.ok(terms.length >= 25 && terms.length <= 40, `unexpected term count ${terms.length}`);
});

test('deck fields are non-empty', () => {
  for (const term of terms) {
    for (const field of ['id', 'term', 'hint', 'definition', 'notMeaning']) {
      assert.equal(typeof term[field], 'string', `${term.id}.${field}`);
      assert.ok(term[field].trim().length > 0, `${term.id}.${field} is empty`);
    }
  }
});

test('deck requires a notMeaning line where a reader could over-read', () => {
  for (const id of NOT_MEANING_REQUIRED) {
    const term = terms.find((entry) => entry.id === id);
    assert.ok(term, `missing term ${id}`);
    assert.ok(term.notMeaning.trim().length > 0, `${id} needs notMeaning`);
  }
});

test('deck hints are one sentence of at most 20 words', () => {
  for (const term of terms) {
    assert.ok(words(term.hint).length <= 20, `${term.id} hint has ${words(term.hint).length} words`);
    assert.equal(sentences(term.hint).length, 1, `${term.id} hint must be one sentence`);
  }
});

test('deck definitions have exactly two sentences', () => {
  for (const term of terms) assert.equal(sentences(term.definition).length, 2, `${term.id} definition: ${term.definition}`);
});

test('deck copy has no causal or certainty wording', () => {
  for (const term of terms) {
    for (const text of allText(term)) {
      const lower = text.toLowerCase();
      for (const phrase of BANNED) assert.ok(!lower.includes(phrase), `${term.id} contains "${phrase}"`);
    }
  }
});

test('strong correlation states the 3-episode standard and stays consistent with its neighbours (roadmap 8.6)', () => {
  const strong = terms.find((term) => term.id === 'strong-correlation');
  assert.match(strong.hint, /at least 3 (separate |independent )?episodes/);
  assert.match(strong.definition, /at least 3 (separate |independent )?episodes/);
  assert.match(strong.definition, /how often/);
  assert.match(strong.definition, /did not fit/);
  assert.match(strong.definition, /other ways to explain|alternative/);
  assert.doesNotMatch(strong.definition + strong.hint, /more than one episode/);
  assert.ok(strong.notMeaning.trim().length > 0);
  // The neighbouring entries do not contradict it: one episode is not a pattern, and the three labels are unchanged.
  assert.match(terms.find((term) => term.id === 'episode').notMeaning, /One episode is not yet a pattern/);
  assert.match(terms.find((term) => term.id === 'evidence-label').definition, /Symptom, Correlation or Strong correlation/);
  assert.ok(terms.some((term) => term.id === 'p90'), 'P90 is shown on screen and needs a term');
});
