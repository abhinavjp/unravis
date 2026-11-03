const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');

// WP-64: Guided analysis stays in the code but is no longer listed on the case home.
// These tests read the sources as text, as tests/tracingPaper.test.cjs does.

const root = path.resolve(__dirname, '..');
const read = (relative) => fs.readFileSync(path.join(root, relative), 'utf8');

const home = () => read('src/lib/components/CaseHome.svelte');

test('the case home no longer lists the Guided section', () => {
  const src = home();
  assert.ok(!src.includes('home-guided'), 'CaseHome.svelte must not contain home-guided');
  assert.ok(!src.includes('GUIDED_TEXT'), 'CaseHome.svelte must not contain GUIDED_TEXT');
});

test('the case home still mounts GuidedAnalysis and keeps its open logic', () => {
  const src = home();
  assert.match(src, /<GuidedAnalysis /);
  for (const needle of ['guidedOpenFor', 'guidedFor', 'guidedOpen']) {
    assert.ok(src.includes(needle), 'CaseHome.svelte must still contain ' + needle);
  }
});

test('the tracing and analysis sections have heading ids and point at them with aria-labelledby', () => {
  const src = home();
  for (const id of ['home-tracing', 'home-analysis']) {
    assert.ok(src.includes('<h3 id="' + id + '">'), id + ' must be a heading id');
    assert.ok(src.includes('aria-labelledby="' + id + '"'), id + ' must be named by aria-labelledby');
  }
  for (const match of src.matchAll(/aria-labelledby="([^"]+)"/g)) {
    assert.ok(src.includes('id="' + match[1] + '"'), match[1] + ' must point at an existing id');
  }
});

test('the words Open guided analysis are not on the case home or in its copy', () => {
  for (const file of ['src/lib/components/CaseHome.svelte', 'src/lib/copy/caseHomeCopy.ts']) {
    assert.ok(!read(file).includes('Open guided analysis'), file + ' must not contain Open guided analysis');
  }
});
