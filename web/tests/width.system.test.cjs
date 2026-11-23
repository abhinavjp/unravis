// Width system (23 Nov 2026): data pages use the window up to --page-max, forms and one-question pages stay narrow, prose keeps a readable measure.
const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const read = (p) => fs.readFileSync(path.join(__dirname, '..', p), 'utf8').replace(/\r\n/g, '\n');

test('one page-width token, 2000px, used by the stage, the scope bar and the record page', () => {
  const css = read('src/app.css');
  const tokens = read('src/brand/tokens.css');
  assert.match(tokens, /--page-max: 2000px;\n\s+--form-max: 880px;\n\s+--read-max: 78ch;/);
  assert.match(css, /\.stage-inner \{ max-width: var\(--page-max\);/);
  assert.match(css, /\.scope-main \{[^}]*max-width: var\(--page-max\);/);
  assert.match(css, /\.scope \.interval-note \{ max-width: var\(--page-max\);/);
  assert.doesNotMatch(css, /max-width: 1560px/, 'no screen keeps the old 1560px cap');
});

test('the case page is a side column plus a wide column of blocks on a wide window, one column below 1100px', () => {
  const home = read('src/lib/components/CaseHome.svelte');
  assert.match(home, /const homeShown = \$derived\(/);
  assert.match(home, /\.home-cols \{ display: grid; gap: 20px; min-width: 0; \}/);
  assert.match(home, /@media \(min-width: 1100px\) \{\n\s+\.home-cols \{ grid-template-columns: minmax\(300px, 380px\) minmax\(0, 1fr\);/);
  assert.match(home, /@media \(min-width: 1360px\) \{\n\s+\.home-blocks \{ grid-template-columns: repeat\(2, minmax\(0, 1fr\)\);/);
  assert.match(home, /\.block \.note, \.block \.empty \{ max-width: var\(--read-max\); \}/);
  // The key, erase, locked and untrusted pages keep the narrow column.
  assert.match(home, /\.case-home \{[^}]*max-width: 880px/);
  // The long blocks span the wide column; the short ones sit side by side.
  for (const id of ['home-retries', 'home-story', 'home-health', 'home-sources']) assert.match(home, new RegExp('<section class="block full" aria-labelledby="' + id + '">'));
  for (const id of ['home-analysis', 'home-tracing', 'home-theories', 'home-files', 'home-key']) assert.match(home, new RegExp('<section class="block" aria-labelledby="' + id + '">'));
});

test('the create guide, its card grids and the charts window use the wide page', () => {
  assert.match(read('src/components/CreateWizard.svelte'), /\.wizard \{[^}]*max-width: min\(var\(--page-max\), 1560px\);/);
  assert.match(read('src/components/wizard/ImportFilesPanel.svelte'), /\.card-grid \{[^}]*repeat\(auto-fill, minmax\(260px, 1fr\)\)/);
  assert.match(read('src/components/sourcewiz/SourceStep.svelte'), /\.tiles \{[^}]*repeat\(auto-fill, minmax\(240px, 1fr\)\)/);
  assert.match(read('src/components/BlackboxCharts.svelte'), /\.bbc \{ width: min\(1840px, calc\(100vw - 32px\)\);/);
});

test('the setup window keeps a readable column and its footer lines up with it', () => {
  const setup = read('setup/App.svelte');
  assert.match(setup, /\.content \{[^}]*padding: 20px max\(24px, calc\(\(100% - 1040px\) \/ 2\)\) 16px;/);
  assert.match(setup, /footer \{[^}]*padding: 12px max\(24px, calc\(\(100% - 1040px\) \/ 2\)\);/);
});
