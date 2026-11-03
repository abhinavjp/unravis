const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

// Tooltips use the dictionary (WP-09, TR-22): the hero P95 line, the failure-trend "missing timing" wording,
// and the call trace lane head title all come from explain(), not a second copy of the words.

const root = path.resolve(__dirname, '..');
const cache = new Map();
function loadTypeScript(relativePath) {
  const filename = path.join(root, relativePath);
  if (cache.has(filename)) return cache.get(filename);
  const source = fs.readFileSync(filename, 'utf8');
  const output = ts.transpileModule(source, {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022, esModuleInterop: true }
  }).outputText;
  const module = { exports: {} };
  cache.set(filename, module.exports); // set before running, so a cycle resolves to the same (empty) object instead of looping
  const nodeRequire = createRequire(filename);
  const load = (request) => /^\.\.?\//.test(request) && fs.existsSync(path.join(path.dirname(filename), request + '.ts'))
    ? loadTypeScript(path.relative(root, path.join(path.dirname(filename), request + '.ts')))
    : nodeRequire(request);
  new Function('require', 'module', 'exports', output)(load, module, module.exports);
  cache.set(filename, module.exports);
  return module.exports;
}

const dict = loadTypeScript('src/lib/copy/dictionary.ts');
const { heroOption } = loadTypeScript('src/lib/charts.ts');
const chartsSource = fs.readFileSync(path.join(root, 'src/lib/charts.ts'), 'utf8');
const callTraceSource = fs.readFileSync(path.join(root, 'src/components/CallTrace.svelte'), 'utf8');

test('tooltip wiring: the hero tooltip ends with the P95 explanation', () => {
  const option = heroOption([{ at: 0, count: 5, latency: 200, max: 400 }], 'UTC', true);
  const text = option.tooltip.formatter([{ dataIndex: 0 }]);
  assert.ok(text.includes('Requests: 5'), text);
  assert.ok(text.includes('P95:'), text);
  assert.ok(text.endsWith(dict.explain('latency.p95')), text);
});

test('tooltip wiring: the hero tooltip without latency has no explanation', () => {
  const option = heroOption([{ at: 0, count: 5, latency: 200, max: 400 }], 'UTC', false);
  const text = option.tooltip.formatter([{ dataIndex: 0 }]);
  assert.ok(!text.includes('About 95 of every 100'), text);
});

test('tooltip wiring: tooltip text is escaped', () => {
  const original = dict.DICTIONARY['latency.p95'].plain;
  dict.DICTIONARY['latency.p95'].plain = '<b>&';
  try {
    const option = heroOption([{ at: 0, count: 5, latency: 200, max: 400 }], 'UTC', true);
    const text = option.tooltip.formatter([{ dataIndex: 0 }]);
    assert.ok(text.includes('&lt;b&gt;&amp;'), text);
    assert.ok(!text.includes('<b>'), text);
  } finally {
    dict.DICTIONARY['latency.p95'].plain = original;
  }
});

test('tooltip wiring: charts.ts reads the missing-timing wording from the dictionary', () => {
  assert.match(chartsSource, /explain\('incomplete'\)/);
  assert.match(chartsSource, /explain\('latency\.p95'\)/);
  assert.ok(!chartsSource.includes('Missing timing (not a failure)'), 'the literal wording should no longer appear in charts.ts');
});

test('tooltip wiring: the call trace lane head carries the dictionary title', () => {
  assert.match(callTraceSource, /title=\{explain\('lane\.' \+ lane\.key\)/);
  const hits = callTraceSource.match(/explain\(/g) ?? [];
  assert.equal(hits.length, 1, 'explain( should appear exactly once (the title attribute)');
});
