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
  const load = (request) => request.startsWith('./') && fs.existsSync(path.join(path.dirname(filename), request + '.ts'))
    ? loadTypeScript(path.relative(root, path.join(path.dirname(filename), request + '.ts')))
    : nodeRequire(request);
  new Function('require', 'module', 'exports', output)(load, module, module.exports);
  return module.exports;
}

const { findTerm, searchTerms, sortTerms, allTermsSorted } = loadTypeScript('src/lib/copy/lookup.ts');
const { deck } = loadTypeScript('src/lib/copy/deck.ts');

test('findTerm returns the deck entry for a known id', () => {
  const term = findTerm('p95');
  assert.ok(term);
  assert.equal(term.term, 'P95');
  assert.ok(term.hint && term.definition && term.notMeaning);
});

test('findTerm returns undefined for an unknown, empty or missing id', () => {
  assert.equal(findTerm('no-such-term'), undefined);
  assert.equal(findTerm(''), undefined);
  assert.equal(findTerm(undefined), undefined);
  assert.equal(findTerm(null), undefined);
  assert.equal(findTerm('__proto__'), undefined);
  assert.equal(findTerm('constructor'), undefined);
});

test('searchTerms is case-insensitive', () => {
  const lower = searchTerms('p95').map(term => term.id);
  assert.deepEqual(searchTerms('P95').map(term => term.id), lower);
  assert.ok(lower.includes('p95'));
  assert.ok(searchTerms('HISTOGRAM').some(term => term.id === 'histogram'));
});

test('searchTerms matches aliases', () => {
  assert.ok(searchTerms('median').some(term => term.id === 'p50'));
  assert.ok(searchTerms('LATENCY').some(term => term.id === 'response-time'));
  assert.ok(searchTerms('percentile').some(term => term.id === 'p95'));
});

test('searchTerms needs every word, and an empty or blank query lists everything', () => {
  assert.equal(searchTerms('').length, deck.terms.length);
  assert.equal(searchTerms('   ').length, deck.terms.length);
  assert.deepEqual(searchTerms('zzzz-not-a-word'), []);
  const both = searchTerms('slowest 95');
  assert.ok(both.length > 0 && both.length < deck.terms.length);
});

test('results are alphabetical by the word shown', () => {
  for (const list of [allTermsSorted(), searchTerms(''), searchTerms('time')]) {
    const names = list.map(term => term.term);
    const expected = [...names].sort((a, b) => a.localeCompare(b, 'en', { sensitivity: 'base', numeric: true }));
    assert.deepEqual(names, expected);
  }
  assert.equal(allTermsSorted().length, deck.terms.length);
});

test('sortTerms does not change the array it is given', () => {
  const input = [{ id: 'b', term: 'Bravo' }, { id: 'a', term: 'alpha' }, { id: 'c', term: 'Charlie' }];
  const copy = input.map(item => item.id);
  assert.deepEqual(sortTerms(input).map(item => item.id), ['a', 'b', 'c']);
  assert.deepEqual(input.map(item => item.id), copy);
});
