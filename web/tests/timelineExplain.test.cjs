const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

// The explanation block of the Investigation Timeline: its fixed words pass the copy lint, and its
// "Timeline details" rows follow the lane rules (lane order, skipped lanes, cut-short wording).

const root = path.resolve(__dirname, '..');
const cache = new Map();
function loadTypeScript(relativePath) {
  const filename = path.join(root, relativePath);
  if (cache.has(filename)) return cache.get(filename).exports;
  const source = fs.readFileSync(filename, 'utf8');
  const output = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022, esModuleInterop: true } }).outputText;
  const module = { exports: {} };
  cache.set(filename, module);
  const nodeRequire = createRequire(filename);
  const load = (request) => {
    if (/^\.\.?\//.test(request)) {
      const plain = path.join(path.dirname(filename), request + '.ts');
      if (fs.existsSync(plain)) return loadTypeScript(path.relative(root, plain));
    }
    return nodeRequire(request);
  };
  new Function('require', 'module', 'exports', output)(load, module, module.exports);
  return module.exports;
}

const explain = loadTypeScript('src/lib/tracing/explain.ts');
const { lintScreenStrings } = loadTypeScript('src/lib/copy/copylint.ts');
const { deck } = loadTypeScript('src/lib/copy/deck.ts');

const lane = (over) => ({
  key: 'audit', label: '', state: 'events', reason: '', origin: 'live', asOf: '', consistency: 'live-non-snapshot',
  complete: false, truncated: false, scanned: 0, hidden: 0, sources: [], buckets: [], ...over
});

test('the fixed words of the explanation block pass the copy lint', () => {
  assert.deepEqual(lintScreenStrings(explain.explainCopyEntries(), deck.terms), []);
});

test('readLines has exactly four lines', () => {
  const lines = explain.EXPLAIN_TEXT.readLines;
  assert.equal(lines.length, 4);
  assert.equal(lines[0], 'Time runs left to right. Each column is one time slot; the slot size is shown above the chart.');
  assert.equal(lines[1], 'The height of a bar is how many events fell in that slot. The number on the left is the tallest bar.');
  assert.equal(lines[2], 'Red marks are failed requests or errors. Hatched areas mean the source was not read there.');
  assert.equal(lines[3], 'Click and drag across the chart to pick a time range. Nothing is changed in your data.');
});

test('axisLines names the zone and the slot size', () => {
  const lines = explain.axisLines('15 minutes', 'UTC');
  assert.equal(lines.length, 3);
  assert.equal(lines[0], 'Left axis: a count of events per slot, from 0 up to the tallest bar in that lane. Each lane has its own scale.');
  assert.equal(lines[1], 'Bottom axis: time in UTC, in slots of 15 minutes.');
  assert.equal(lines[2], 'Right axis (API lane): how long requests took, from 0 up to the slowest.');
});

test('empty zone and slot size fall back to plain words', () => {
  assert.equal(explain.axisLines('', '')[1], 'Bottom axis: time in the case time zone, in slots of one slot.');
  assert.equal(explain.axisLines('15 minutes', '')[1], 'Bottom axis: time in the case time zone, in slots of 15 minutes.');
  assert.equal(explain.axisLines('', 'UTC')[1], 'Bottom axis: time in UTC, in slots of one slot.');
});

test('detailsRows builds one row per lane in lane order, skipping lanes not present', () => {
  const lanes = [
    lane({
      key: 'iis', origin: 'case', asOf: '', truncated: true, complete: true, consistency: 'stored-as-of'
    }),
    lane({
      key: 'audit', origin: 'live', asOf: '2026-10-05T00:00:00Z', complete: true,
      sources: [{ sourceId: 'a', label: 'Main audit' }, { sourceId: 'b', label: 'Web audit' }]
    })
  ];
  const rows = explain.detailsRows(lanes, 'UTC', () => '5 Oct 00:00');
  assert.equal(rows.length, 2, 'the sql lane is not in the input and is skipped');
  assert.deepEqual(rows[0], {
    key: 'audit', name: 'API requests (audit log)', where: 'Live connection',
    when: 'As of 5 Oct 00:00 (UTC)', whole: 'Complete', sources: 'Main audit, Web audit'
  });
  assert.deepEqual(rows[1], {
    key: 'iis', name: 'Web server (IIS)', where: 'Imported files',
    when: 'Not recorded', whole: 'Cut short, events may be missing', sources: 'None'
  });
});

test('a lane neither cut short nor complete is Not complete', () => {
  const rows = explain.detailsRows([lane({ key: 'sql', origin: 'case', consistency: 'snapshot' })], 'UTC', (iso) => iso);
  assert.deepEqual(rows.map((row) => [row.key, row.whole]), [['sql', 'Not complete']]);
});
