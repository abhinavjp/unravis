const assert = require('node:assert/strict');
const test = require('node:test');
const fs = require('node:fs');
const path = require('node:path');

const root = path.resolve(__dirname, '..');
const repo = path.resolve(root, '..');

function missing(dir, names) {
  return names.filter((n) => !fs.existsSync(path.join(dir, n)));
}

test('the guided unit tests and their fixture are still present', () => {
  const names = ['guided.logic.test.cjs', 'guided.steps.test.cjs', 'guided.ui.test.cjs', 'guided.fixture.cjs'];
  assert.deepEqual(missing(path.join(root, 'tests'), names), []);
});

test('the guided source folders are intact', () => {
  const names = [
    'afterFix.ts', 'api.ts', 'frame.ts', 'guided.svelte.ts', 'inflight.ts', 'nearby.ts',
    'normalize.ts', 'report.ts', 'stepModel.ts', 'tools.ts', 'types.ts',
  ];
  assert.deepEqual(missing(path.join(root, 'src', 'lib', 'guided'), names), []);
  const comp = path.join(root, 'src', 'lib', 'components', 'guided');
  assert.ok(fs.existsSync(path.join(comp, 'GuidedAnalysis.svelte')));
  const svelte = fs.readdirSync(comp).filter((f) => f.endsWith('.svelte'));
  assert.ok(svelte.length >= 17, 'expected at least 17 .svelte files, found ' + svelte.length);
});

test('the guided API routes are still registered', () => {
  const src = fs.readFileSync(path.join(repo, 'internal', 'server', 'routes.go'), 'utf8');
  for (const p of [
    '"/api/cases/{id}/investigation"',
    '"/api/cases/{id}/views"',
    '"/api/cases/{id}/checklist"',
    '"/api/cases/{id}/report"',
  ]) {
    assert.ok(src.includes(p), 'routes.go lost ' + p);
  }
});

test('the archived guided e2e specs still exist', () => {
  const dir = path.join(repo, 'tools', 'e2e', 'archive', 'guided');
  const names = [
    'guide-steps.spec.ts', 'guide-checklist.spec.ts', 'guided.spec.ts', 'export-ui.spec.ts',
    'manual-tools.spec.ts', 'exports-guided.spec.ts', 'keeper-update-guided.spec.ts',
    'support.ts', 'README.md',
  ];
  assert.deepEqual(missing(dir, names), []);
});

test('the guided shell and store say they are unlisted', () => {
  for (const f of [
    path.join(root, 'src', 'lib', 'components', 'guided', 'GuidedAnalysis.svelte'),
    path.join(root, 'src', 'lib', 'guided', 'guided.svelte.ts'),
  ]) {
    assert.match(fs.readFileSync(f, 'utf8'), /unlisted/);
  }
});
