const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

// Slow call screen: its history mark is read as the case page with the timeline open.

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

const { CASE_TRACING_MARK, CASE_CALL_MARK, isCallMark, parseScreenMark, isHistoryScreen } = loadTypeScript('src/lib/screenFlow.ts');

test('the Slow call mark is one level under the timeline mark', () => {
  assert.equal(CASE_CALL_MARK, 'case/tracing/call');
});

test('isCallMark is true only for the Slow call mark', () => {
  assert.equal(isCallMark(CASE_CALL_MARK), true);
  assert.equal(isCallMark(CASE_TRACING_MARK), false);
  assert.equal(isCallMark('case'), false);
  assert.equal(isCallMark(''), false);
});

test('parseScreenMark reads the Slow call mark as the case page with the timeline open', () => {
  assert.deepEqual(parseScreenMark(CASE_CALL_MARK), { screen: 'case', tracing: true });
  assert.deepEqual(parseScreenMark(CASE_TRACING_MARK), { screen: 'case', tracing: true });
  assert.deepEqual(parseScreenMark('case'), { screen: 'case', tracing: false });
});

test('the Slow call mark is not a history screen of its own', () => {
  assert.equal(isHistoryScreen(CASE_CALL_MARK), false);
});
