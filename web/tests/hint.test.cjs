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
  const load = (request) => /^\.\.?\//.test(request) && fs.existsSync(path.join(path.dirname(filename), request + '.ts'))
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

const { createHintBus, glossaryTermFrom, GLOSSARY_EVENT } = loadTypeScript('src/lib/copy/hintState.ts');

test('hint bus keeps one hint open at a time', () => {
  const bus = createHintBus();
  const seen = [];
  const stop = bus.subscribe(id => seen.push(id));
  assert.equal(bus.current(), null);
  bus.open('a');
  bus.open('b');
  assert.equal(bus.current(), 'b');
  assert.deepEqual(seen, ['a', 'b']);
  bus.open('b');
  assert.deepEqual(seen, ['a', 'b']);
  stop();
  bus.open('c');
  assert.deepEqual(seen, ['a', 'b']);
});

test('hint bus close only affects the hint that is open', () => {
  const bus = createHintBus();
  bus.open('a');
  bus.open('b');
  bus.close('a');
  assert.equal(bus.current(), 'b');
  bus.close('b');
  assert.equal(bus.current(), null);
  bus.close('b');
  assert.equal(bus.current(), null);
});

test('hint bus listeners can unsubscribe while being notified', () => {
  const bus = createHintBus();
  let calls = 0;
  const stop = bus.subscribe(() => { calls++; stop(); });
  bus.subscribe(() => { calls++; });
  bus.open('a');
  assert.equal(calls, 2);
  bus.open('b');
  assert.equal(calls, 3);
});

test('glossaryTermFrom accepts only a non-empty string id', () => {
  assert.equal(GLOSSARY_EVENT, 'unravis:open-glossary');
  assert.equal(glossaryTermFrom({ termId: 'p95' }), 'p95');
  for (const bad of [undefined, null, {}, { termId: '' }, { termId: 5 }, 'p95']) assert.equal(glossaryTermFrom(bad), null);
});
