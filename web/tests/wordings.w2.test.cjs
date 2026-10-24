// Wave-2 wordings (plan section 4.0) and the diagnosis-word fixture.
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

const SAMPLES = [
  w.metricRose('web-a', 'working set', '1.2 GB', '40 min', '0.8 GB', '2.0 GB'),
  w.metricSampleOverlap('Redis INFO', 'blocked clients', '3'),
  w.markerBefore('12 s', 'Redis restart', 'redis-a'),
  w.episodeShare('Thread pool queue above 100', 4, 5, 2, 60),
  w.topInWindow('CPU', 'w3wp 82 %, sqlservr 9 %')
];

test('the wave-2 wordings read as the plan states', () => {
  assert.deepEqual(SAMPLES, [
    'web-a: working set rose by 1.2 GB over 40 min (from 0.8 GB to 2.0 GB).',
    'A Redis INFO sample showed blocked clients at 3 during this interval (Correlation).',
    'This interval starts 12 s after a Redis restart on redis-a (Correlation).',
    'Thread pool queue above 100 in 4 of 5 stall episodes, and in 2 of 60 comparison windows outside stalls.',
    'Highest CPU in this window: w3wp 82 %, sqlservr 9 %.'
  ]);
});

test('the wave-2 wordings pass the copy lint', () => {
  for (const s of SAMPLES) assert.deepEqual(rules(s), [], s);
});

test('paraphrased wave-2 wordings fail', () => {
  for (const s of [
    'Redis INFO showed blocked clients at 3 during this interval (Correlation).',
    'The pool was low in 4 of 5 stalls, and in 2 of 60 comparison windows outside stalls today.',
    'This interval starts soon after a restart.',
    'Top CPU in this window: w3wp.'
  ]) {
    assert.ok(rules(s).includes('wording'), s);
  }
});

test('diagnosis words never pass, negated or not', () => {
  for (const s of ['Memory leak on web-a.', 'Thread pool starvation.', 'The pool is saturated.',
    'This is not a leak.', 'The queue was starving.', 'CPU saturation was seen.']) {
    assert.ok(rules(s).includes('causal'), s);
  }
  for (const s of ['The cause is unknown.', 'It was slow because of that.']) {
    assert.ok(rules(s).includes('causal'), s);
  }
});
