// Articles and sample counts in the overlap and sample wordings. The same sentences are pinned in
// internal/evidence/wording_test.go (TestOverlapAndSampleArticles, TestArticle).
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

const { lintCopy } = loadTypeScript('src/lib/copy/copylint.ts');
const w = loadTypeScript('src/lib/copy/wordings.ts');

const term = (name) => ({ id: name.toLowerCase(), term: name, hint: 'h', definition: 'd', notMeaning: 'n', aliases: [] });
const TERMS = [term('Episode'), term('Correlation'), term('Sampling')];
const rules = (text) => lintCopy([{ id: 'x', text }], TERMS).map((f) => f.rule);

const SENTENCES = [
  [w.overlapUnique('ASP.NET Core console log', 'late heartbeat', 1.5),
    'An ASP.NET Core console log session had a late heartbeat of 1.5 s overlapping this interval (Correlation).'],
  [w.overlapUnique('IIS log', 'error entry', 2),
    'An IIS log session had an error entry of 2 s overlapping this interval (Correlation).'],
  [w.overlapUnique('Redis SLOWLOG', 'slow command', 1.012),
    'A Redis SLOWLOG session had a slow command of 1.012 s overlapping this interval (Correlation).'],
  [w.overlapUnique('SQL Server', 'lock wait', 28),
    'A SQL Server session had a lock wait of 28 s overlapping this interval (Correlation).'],
  [w.metricSampleOverlap('ASP.NET', 1, 'ASP.NET Requests Queued', '4'),
    'An ASP.NET sample showed ASP.NET Requests Queued at 4 during this interval (Correlation).'],
  [w.metricSampleOverlap('MongoDB serverStatus', 1, 'MongoDB queue length', '9'),
    'A MongoDB serverStatus sample showed MongoDB queue length at 9 during this interval (Correlation).'],
  [w.metricSampleOverlap('MongoDB serverStatus', 3, 'MongoDB queue length', '9'),
    '3 MongoDB serverStatus samples showed MongoDB queue length at 9 during this interval (Correlation).'],
  [w.metricSampleOverlap('Redis INFO', 2, 'Redis blocked clients', '3'),
    '2 Redis INFO samples showed Redis blocked clients at 3 during this interval (Correlation).']
];

test('the overlap and sample wordings take the right article and number', () => {
  for (const [got, want] of SENTENCES) assert.equal(got, want);
});

test('every article sentence passes the copy lint and keeps the Correlation label', () => {
  for (const [got] of SENTENCES) {
    assert.deepEqual(rules(got), [], got);
    assert.ok(got.endsWith('(Correlation).'), got);
  }
});

test('a paraphrase of an article sentence still fails', () => {
  assert.ok(rules('A ASP.NET Core console log session had a late heartbeat of 1.5 s overlapping this interval.').includes('wording'));
  assert.ok(rules('Many MongoDB serverStatus samples showed MongoDB queue length at 9 during this interval (Correlation).').includes('wording'));
});

test('article picks a or an by the first word, as the Go twin does', () => {
  const table = {
    'ASP.NET Core log': 'an', 'IIS log': 'an', 'Event Viewer': 'an', 'error entry': 'an', '8 ball': 'an',
    'HTTP proxy': 'an', 'ERROR entry': 'an', 'SLOWLOG': 'a', 'unique wait': 'a', 'use case': 'a',
    'MongoDB serverStatus': 'a', 'Redis INFO': 'a', 'mongod.log': 'a', '.NET runtime counter': 'a',
    'SQL Server': 'a', 'USB drive': 'a', 'CPU': 'a', 'log4net log': 'a', 'slow command': 'a', '': 'a'
  };
  for (const [phrase, want] of Object.entries(table)) assert.equal(w.article(phrase), want, phrase);
  assert.equal(w.indefinite('ASP.NET', true), 'An ASP.NET');
  assert.equal(w.indefinite('error'), 'an error');
});
