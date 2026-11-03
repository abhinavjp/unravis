const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const ts = require('typescript');

const root = path.resolve(__dirname, '..');
function load(relativePath) {
  const source = fs.readFileSync(path.join(root, relativePath), 'utf8');
  const output = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022, esModuleInterop: true } }).outputText;
  const module = { exports: {} };
  new Function('require', 'module', 'exports', output)(require, module, module.exports);
  return module.exports;
}
const { formatScreenRoute, parseScreenRoute, restorableScreen, guidedStepOf, MAX_GUIDED_STEP } = load('src/lib/screenRoute.ts');
const STEP_COUNT = Number(/export const STEP_COUNT = (\d+)/.exec(fs.readFileSync(path.join(root, 'src/lib/guided/stepModel.ts'), 'utf8'))[1]);

test('screen routes round-trip through the address', () => {
  const routes = [{ screen: 'start' }, { screen: 'create' }, { screen: 'case' }, { screen: 'running' }, { screen: 'case', guided: 0 }, { screen: 'case', guided: 3 }, { screen: 'case', guided: 10 }];
  for (const route of routes) assert.deepEqual(parseScreenRoute(formatScreenRoute(route)), route);
  assert.equal(formatScreenRoute({ screen: 'start' }), '#/cases');
  assert.equal(formatScreenRoute({ screen: 'case', guided: 3 }), '#/case/guided/3');
  assert.equal(formatScreenRoute({ screen: 'running' }), '#/case/analysis');
});

test('the last guided step matches the step model', () => {
  assert.equal(MAX_GUIDED_STEP, STEP_COUNT - 1);
  assert.equal(parseScreenRoute('#/case/guided/' + STEP_COUNT), null);
});

test('addresses that are not routes read as null', () => {
  for (const hash of ['', '#', '#/', '#t=abc', '#/where?lens=requests', '#/op?c=A&a=B', '#/cases/extra', '#/case/guided', '#/case/guided/x', '#/case/guided/-1', '#/case/guided/11', '#/case/guided/003', '#/unknown', 'cases']) {
    assert.equal(parseScreenRoute(hash), null, hash);
  }
});

test('a trailing slash and a query are tolerated and never read', () => {
  assert.deepEqual(parseScreenRoute('#/cases/'), { screen: 'start' });
  assert.deepEqual(parseScreenRoute('#/case?x=1'), { screen: 'case' });
  assert.deepEqual(parseScreenRoute('/case'), { screen: 'case' });
});

test('an address never carries more than a screen name and a step', () => {
  for (const route of [{ screen: 'start' }, { screen: 'create' }, { screen: 'case' }, { screen: 'running' }, { screen: 'case', guided: 4 }]) {
    assert.match(formatScreenRoute(route), /^#\/[a-z]+(\/[a-z]+(\/\d{1,2})?)?$/);
  }
  assert.equal(formatScreenRoute({ screen: 'case', guided: 99 }), '#/case/guided/10');
  assert.equal(formatScreenRoute({ screen: 'case', guided: -5 }), '#/case/guided/0');
  assert.equal(formatScreenRoute({ screen: 'case', guided: 2.5 }), '#/case/guided/0');
});

test('restoring needs an open case and falls back to the start screen', () => {
  const open = { caseOpen: true, locked: false };
  assert.equal(restorableScreen({ screen: 'case', guided: 2 }, open), 'case');
  assert.equal(restorableScreen({ screen: 'running' }, open), 'running');
  assert.equal(restorableScreen({ screen: 'create' }, open), 'create');
  assert.equal(restorableScreen({ screen: 'case' }, { caseOpen: false, locked: false }), 'start');
  assert.equal(restorableScreen({ screen: 'running' }, { caseOpen: false, locked: false }), 'start');
  assert.equal(restorableScreen({ screen: 'case' }, { caseOpen: true, locked: true }), 'start');
  assert.equal(restorableScreen(null, open), 'start');
});

test('guidedStepOf reads only a guided route', () => {
  assert.equal(guidedStepOf({ screen: 'case', guided: 5 }), 5);
  assert.equal(guidedStepOf({ screen: 'case' }), null);
  assert.equal(guidedStepOf(null), null);
});
