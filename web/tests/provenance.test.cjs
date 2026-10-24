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

const prov = loadTypeScript('src/lib/provenance.ts');
const { deck } = loadTypeScript('src/lib/copy/deck.ts');
const { lintScreenStrings, lintTerms } = loadTypeScript('src/lib/copy/copylint.ts');

// JSON fixtures that follow the Go contract (C2, C3). No Go dependency.
const base = {
  sourceId: 's1', window: 'current', start: '2026-10-24T00:00:00Z', end: '2026-10-24T06:00:00Z',
  queryMode: 'exact', scanClass: 'narrow', completeness: 'complete',
  docsReturned: 5, docsExamined: 1200, sampleSize: 5, elapsedMs: 2400, maxTimeMs: 10000
};
const complete = { ...base };
const ranToEnd = { ...base, completeness: 'ran_to_end', docsExamined: null };
const partialTimeout = { ...base, completeness: 'partial', partialReason: 'timeout', docsExamined: null };
const partialRows = { ...base, completeness: 'partial', partialReason: 'rowcap', docsExamined: null };
const partialError = { ...base, completeness: 'partial', partialReason: 'error', docsExamined: null };
const margin = {
  marginMs: 61000, observedMs: 900, padMs: 60000, percentile: 99.9, sampleSize: 10000,
  sampleFrom: '2026-10-23T00:00:00Z', sampleTo: '2026-10-24T00:00:00Z', calibratedAt: '2026-10-24T01:00:00Z',
  label: 'bounded by margin 61 s, calibrated on 10000 indexed rows; rows outside 61 s would be missed'
};
const hint = { ...base, queryMode: 'hint', completeness: 'hint', docsExamined: null, margin };
const fallback = { ...base, completeness: 'ran_to_end', docsExamined: null, note: 'too few rows to calibrate the _id margin' };
const broad = { ...base, scanClass: 'broad', completeness: 'ran_to_end', docsExamined: null };

const row = (kind, day, docs, nn) => ({
  kind, day, docs,
  requestInitiatedAt: { nonNull: nn[0], pct: docs ? Math.round((nn[0] / docs) * 1000) / 10 : 0 },
  createdOn: { nonNull: nn[1], pct: docs ? Math.round((nn[1] / docs) * 1000) / 10 : 0 },
  apiTime: { nonNull: nn[2], pct: docs ? Math.round((nn[2] / docs) * 1000) / 10 : 0 },
  accountId: { nonNull: nn[3], pct: docs ? Math.round((nn[3] / docs) * 1000) / 10 : 0 }
});
const report = {
  sourceId: 's1',
  rows: [row('synchronous', '2026-10-23', 200, [200, 200, 190, 100]), row('hangfire', '2026-10-24', 50, [50, 50, 0, 0])],
  total: row('all', '', 250, [250, 250, 190, 100]),
  tenantAttribution: 'tenant attribution unavailable (40 % populated)',
  provenance: [{ ...base, window: 'coverage', scanClass: 'broad', completeness: 'ran_to_end', docsExamined: null }]
};

test('provenanceLine complete', () => {
  assert.equal(prov.provenanceLine(complete),
    'Complete: every matching record in this window was read. Examined 1,200, returned 5, in 2.4 s.');
});

test('provenanceLine ran_to_end never says Complete', () => {
  const line = prov.provenanceLine(ranToEnd);
  assert.equal(line, 'Finished with no time limit, row limit or read error. Records examined are not counted on a live source.');
  assert.ok(!line.startsWith('Complete'));
});

test('provenanceLine complete without a count is not shown as Complete', () => {
  assert.equal(prov.provenanceLine({ ...complete, docsExamined: null }), prov.provenanceLine(ranToEnd));
});

test('provenanceLine partial names each reason', () => {
  assert.equal(prov.provenanceLine(partialTimeout), 'Partial: the 10 s time limit was reached. Some matching records may be missing.');
  assert.equal(prov.provenanceLine(partialRows), 'Partial: the row limit was reached. Some matching records may be missing.');
  assert.equal(prov.provenanceLine(partialError), 'Partial: a read error stopped the query. Some matching records may be missing.');
});

test('provenanceLine hint includes the verbatim sentence', () => {
  const line = prov.provenanceLine(hint);
  assert.ok(line.includes('bounded by margin 61 s, calibrated on 10000 indexed rows; rows outside 61 s would be missed'), line);
});

test('hint sentence is rebuilt from the margin when the label is absent', () => {
  const { label, ...noLabel } = margin;
  assert.equal(prov.hintSentence(noLabel), margin.label);
});

test('formatMargin and formatElapsed', () => {
  assert.equal(prov.formatMargin(61000), '61 s');
  assert.equal(prov.formatMargin(61999), '61 s');
  assert.equal(prov.formatElapsed(850), '850 ms');
  assert.equal(prov.formatElapsed(2400), '2.4 s');
  assert.equal(prov.formatElapsed(3000), '3 s');
  assert.equal(prov.formatElapsed(125000), '2 min 5 s');
});

test('scanNote covers the broad scan and the hint fallback', () => {
  assert.equal(prov.scanNote(complete), '');
  assert.equal(prov.scanNote(broad), 'This query reads the whole window, not an index.');
  assert.equal(prov.scanNote(fallback), 'Hint mode was not available (too few rows to calibrate the _id margin); this result used exact mode.');
  assert.equal(prov.scanNote(hint), '');
});

test('provenanceRows: one row per entry, with source and window labels', () => {
  const rows = prov.provenanceRows([complete, { ...partialTimeout, window: 'reference', sourceId: 's2' }], new Map([['s1', 'Orders DB']]));
  assert.equal(rows.length, 2);
  assert.equal(rows[0].label, 'Orders DB · Current period');
  assert.equal(rows[1].label, 's2 · Earlier period');
  assert.deepEqual(rows[0].terms, ['complete']);
  assert.deepEqual(rows[1].terms, ['partial']);
  assert.equal(rows[1].tone, 'warn');
  assert.deepEqual(prov.provenanceRows([hint], new Map())[0].terms, ['query-mode', 'margin']);
});

test('absent provenance renders nothing', () => {
  assert.deepEqual(prov.provenanceRows(undefined, new Map()), []);
  assert.deepEqual(prov.provenanceRows(null, new Map()), []);
  assert.deepEqual(prov.provenanceRows([], new Map()), []);
});

test('coverage refusal renders cov.refused verbatim', () => {
  const expected = 'This check reads every record in the window, so it does not run on a live source by default. Run it on a restored copy, or start Unravis with --allow-broad-scans.';
  assert.equal(prov.COVERAGE_REFUSED, expected);
  assert.equal(prov.coverageRefusalText({ refused: 'anything the server said' }), expected);
});

test('coverageRowsForDisplay lists rows then the total, with one-decimal percentages', () => {
  const rows = prov.coverageRowsForDisplay(report);
  assert.equal(rows.length, 3);
  assert.deepEqual(rows[0], { key: 'synchronous|2026-10-23', kind: 'Synchronous', day: '2026-10-23', docs: '200', fields: ['100.0 %', '100.0 %', '95.0 %', '50.0 %'], total: false });
  assert.equal(rows[1].kind, 'Hangfire');
  assert.deepEqual(rows[1].fields, ['100.0 %', '100.0 %', '0.0 %', '0.0 %']);
  assert.equal(rows[2].total, true);
  assert.equal(rows[2].kind, 'All');
  assert.equal(rows[2].day, 'All days');
  assert.equal(rows[2].docs, '250');
});

test('coverageRowsForDisplay copes with a missing report or empty rows', () => {
  assert.deepEqual(prov.coverageRowsForDisplay(undefined), []);
  const none = prov.coverageRowsForDisplay({ ...report, rows: [], total: row('all', '', 0, [0, 0, 0, 0]) });
  assert.equal(none.length, 1);
  assert.equal(none[0].docs, '0');
});

test('tenantLine shows the server text verbatim when unavailable', () => {
  assert.equal(prov.tenantLine(report), 'Tenant check: tenant attribution unavailable (40 % populated)');
  assert.equal(prov.tenantLine({ ...report, tenantAttribution: 'available' }), 'Tenant check: tenant attribution is available.');
});

test('coverageRange clamps to the last 31 days and rejects bad input', () => {
  const day = 86400000;
  const end = '2026-10-24T00:00:00.000Z';
  const short = prov.coverageRange('2026-10-23T00:00:00.000Z', end);
  assert.deepEqual(short, { start: '2026-10-23T00:00:00.000Z', end, clamped: false });
  const long = prov.coverageRange('2026-08-01T00:00:00.000Z', end);
  assert.equal(long.clamped, true);
  assert.equal(Date.parse(long.end) - Date.parse(long.start), 31 * day);
  assert.equal(prov.coverageRange('nope', end), null);
  assert.equal(prov.coverageRange(end, end), null);
});

test('every new string passes lintScreenStrings', () => {
  const rendered = [
    complete, ranToEnd, partialTimeout, partialRows, partialError, hint, fallback, broad
  ].flatMap((p) => [prov.provenanceLine(p), prov.scanNote(p)]).filter(Boolean);
  const entries = [...prov.allCopyStrings(), ...rendered, prov.tenantLine(report), prov.tenantLine({ ...report, tenantAttribution: 'available' })]
    .map((text, i) => ({ id: 's' + i, text }));
  assert.ok(entries.length >= 20);
  assert.deepEqual(lintScreenStrings(entries, deck.terms), []);
  for (const { text } of entries) assert.doesNotMatch(text, /\bproven\b|\bcause/i, text);
});

test('glossary entries for the 5 new terms exist with hint/definition/notMeaning', () => {
  for (const id of ['query-mode', 'complete', 'partial', 'margin', 'coverage-census']) {
    const term = deck.terms.find((entry) => entry.id === id);
    assert.ok(term, 'missing term ' + id);
    for (const field of ['term', 'hint', 'definition', 'notMeaning']) assert.ok(term[field].trim().length > 0, id + '.' + field);
    const all = [term.term, term.hint, term.definition, term.notMeaning].join(' ').toLowerCase();
    assert.doesNotMatch(all, /\bproven\b|\bcause/, id);
  }
  assert.deepEqual(['Query mode', 'Complete', 'Partial', 'Margin', 'Coverage census'],
    ['query-mode', 'complete', 'partial', 'margin', 'coverage-census'].map((id) => deck.terms.find((entry) => entry.id === id).term));
  assert.deepEqual(lintTerms(deck.terms), []);
});

test('every glossary id used by the provenance rows exists in the deck', () => {
  const ids = new Set(deck.terms.map((term) => term.id));
  for (const p of [complete, ranToEnd, partialTimeout, hint, fallback, broad]) {
    for (const id of prov.provenanceRows([p], new Map())[0].terms) assert.ok(ids.has(id), id);
  }
  assert.ok(ids.has(prov.COVERAGE_TERM));
});
