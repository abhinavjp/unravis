const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

// The tooltip dictionary (WP-08): the 23 seeded entries behind explain(id), and the copy lint on all of them.

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

const { DICTIONARY, explain } = loadTypeScript('src/lib/copy/dictionary.ts');
const { lintTerms, lintScreenStrings } = loadTypeScript('src/lib/copy/copylint.ts');
const { deck } = loadTypeScript('src/lib/copy/deck.ts');
const dictionarySource = fs.readFileSync(path.join(root, 'src/lib/copy/dictionary.ts'), 'utf8');

// The 23 seeded entries (id, term, plain) from the packet; the plain wording is final.
const SEED = [
  { id: 'status.4xx', term: 'Client errors (4xx)', plain: 'Requests the server turned down, such as a missing page or a refused sign-in.' },
  { id: 'status.5xx', term: 'Server errors (5xx)', plain: 'Requests the server could not finish.' },
  { id: 'incomplete', term: 'Missing timing', plain: 'Missing timing (not a failure)' },
  { id: 'latency.p50', term: 'Typical time (P50)', plain: 'Half of the requests finished faster than this.' },
  { id: 'latency.p95', term: 'Slower-end time (P95)', plain: 'About 95 of every 100 requests finished within this time.' },
  { id: 'latency.p95Ratio', term: 'Slower-end spread', plain: 'How much longer the slower end took than the typical request.' },
  { id: 'lane.api-audit', term: 'API audit', plain: 'Requests recorded in the API audit log.' },
  { id: 'lane.sql', term: 'SQL', plain: 'Queries recorded by the SQL source.' },
  { id: 'lane.iis', term: 'IIS', plain: 'Requests recorded in the web server log.' },
  { id: 'lane.mongo', term: 'Mongo', plain: 'Operations recorded by the Mongo source.' },
  { id: 'lane.redis', term: 'Redis', plain: 'Commands recorded by the Redis source.' },
  { id: 'lane.other', term: 'Other sources', plain: 'Events from sources without a lane of their own.' },
  { id: 'event.duration', term: 'Duration', plain: 'How long the event took, as its source recorded it.' },
  { id: 'event.time-taken', term: 'Time taken', plain: 'Observed elapsed time, never API processing time.' },
  { id: 'iis.point', term: 'IIS point', plain: 'Drawn at its timestamp until the source semantics are confirmed.' },
  { id: 'gap.time-end', term: 'Time field', plain: 'The W3C time field is assumed to be the request end.' },
  { id: 'gap.resolution', term: 'Time resolution', plain: 'Log time has one-second resolution.' },
  { id: 'attr.client-ip', term: 'Client IP', plain: 'The address the request came from.' },
  { id: 'attr.referer', term: 'Referer', plain: 'The page that linked to the request.' },
  { id: 'attr.cookie', term: 'Cookie', plain: 'A digest, not the value.' },
  { id: 'join.unique', term: 'One match', plain: 'Exactly one record fits.' },
  { id: 'join.ambiguous', term: 'Several matches', plain: 'More than one record fits, so none is picked.' },
  { id: 'join.none', term: 'No match', plain: 'No record fits.' }
];

test('dictionary: every seeded id has a term and a plain text', () => {
  assert.deepEqual(Object.keys(DICTIONARY).sort(), SEED.map((entry) => entry.id).sort());
  for (const { id, term, plain } of SEED) {
    const entry = DICTIONARY[id];
    assert.ok(entry, `missing entry ${id}`);
    assert.equal(typeof entry.term, 'string', `${id} term`);
    assert.equal(typeof entry.plain, 'string', `${id} plain`);
    assert.ok(entry.term.trim().length > 0, `${id} term is empty`);
    assert.ok(entry.plain.trim().length > 0, `${id} plain is empty`);
    assert.ok(entry.term.length < 160, `${id} term is too long`);
    assert.ok(entry.plain.length < 160, `${id} plain is too long`);
  }
});

test('dictionary: the fixed wording is exact', () => {
  const fixed = {
    incomplete: 'Missing timing (not a failure)',
    'event.time-taken': 'Observed elapsed time, never API processing time.',
    'iis.point': 'Drawn at its timestamp until the source semantics are confirmed.',
    'gap.time-end': 'The W3C time field is assumed to be the request end.',
    'gap.resolution': 'Log time has one-second resolution.',
    'attr.cookie': 'A digest, not the value.'
  };
  for (const [id, plain] of Object.entries(fixed)) assert.equal(DICTIONARY[id].plain, plain, id);
});

test('dictionary: explain returns plain text and never throws', () => {
  assert.equal(explain('attr.client-ip'), 'The address the request came from.');
  for (const { id, plain } of SEED) assert.equal(explain(id), plain, id);
  for (const id of ['missing', '', undefined, null, 5, 'constructor', '__proto__', 'toString']) {
    assert.equal(explain(id), '', `explain(${JSON.stringify(id) ?? id})`);
  }
  // The fallback to the term lives in the module source, not only in the loaded behaviour.
  assert.match(dictionarySource, /entry\.plain\s*\|\|\s*entry\.term/);
});

test('dictionary: the copy lint finds nothing', () => {
  const ids = Object.keys(DICTIONARY);
  const mapped = ids.map((id) => ({
    id,
    term: DICTIONARY[id].term,
    hint: DICTIONARY[id].plain,
    definition: DICTIONARY[id].plain,
    notMeaning: DICTIONARY[id].plain
  }));
  const findings = lintTerms([...deck.terms, ...mapped])
    .filter((finding) => ids.some((id) => finding.id === id || finding.id.startsWith(id + '.')));
  assert.deepEqual(findings, []);
  const entries = ids.map((id) => ({ id, text: DICTIONARY[id].plain }));
  assert.deepEqual(lintScreenStrings(entries, deck.terms), []);
});

test('dictionary: the module has no imports', () => {
  const lines = dictionarySource.split(/\r?\n/);
  assert.ok(lines.every((line) => !line.startsWith('import ')), 'dictionary.ts must not import anything');
});
