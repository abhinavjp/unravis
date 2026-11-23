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
  assert.doesNotMatch(css, /max-width: 1560px/, 'the stage and the scope bar no longer keep the old 1560px cap');
  // Two screens still have their own cap, min(--page-max, 1560px): the record page and the create guide.
});

test('the case page is a side column plus a wide column of blocks when the page itself is 1300px wide, one column below', () => {
  const home = read('src/lib/components/CaseHome.svelte');
  assert.match(home, /const homeShown = \$derived\(/);
  assert.match(home, /\.case-home\.home \{ container-type: inline-size; \}/, 'decided by the width of the page itself, not the window');
  assert.match(home, /@container \(min-width: 1300px\) \{\n\s+\.home-cols \{ grid-template-columns: minmax\(300px, 360px\) minmax\(0, 1fr\);/);
  assert.match(home, /\.home-blocks \{ grid-template-columns: repeat\(2, minmax\(0, 1fr\)\);/);
  assert.match(home, /\.home-blocks \.block \.note, \.home-blocks \.block \.empty \{ max-width: var\(--read-max\); \}/);
  assert.match(home, /\.block \{ display: grid; align-content: start;/, 'a short block next to a tall one must not stretch its content');
  assert.match(home, /max-height: calc\(100dvh - 180px\); overflow-y: auto;/, 'a tall side column scrolls on its own, so its buttons stay reachable');
  // The key, erase, locked and untrusted pages keep the narrow form column.
  assert.match(home, /\.case-home \{[^}]*max-width: var\(--form-max\)/);
  // The long blocks span the wide column; the short ones sit side by side.
  for (const id of ['home-retries', 'home-story', 'home-theories', 'home-health', 'home-sources']) assert.match(home, new RegExp('<section class="block full" aria-labelledby="' + id + '">'));
  for (const id of ['home-analysis', 'home-tracing', 'home-files', 'home-key']) assert.match(home, new RegExp('<section class="block" aria-labelledby="' + id + '">'));
});

test('the blocks of the case page keep their reading and tab order (tools/e2e HAND-16)', () => {
  const home = read('src/lib/components/CaseHome.svelte');
  const order = ['home-retries', 'home-analysis', 'home-tracing', 'home-story', 'home-theories', 'home-health', 'home-sources', 'home-files', 'home-key', 'home-erase'];
  const at = order.map((id) => home.indexOf('aria-labelledby="' + id + '"'));
  assert.ok(at.every((n) => n > 0), 'every block is there');
  assert.deepEqual([...at].sort((x, y) => x - y), at, 'in this order, as before the width change');
});

test('the create guide, its card grids and the charts window use the wide page', () => {
  assert.match(read('src/components/CreateWizard.svelte'), /\.wizard \{[^}]*max-width: min\(var\(--page-max\), 1560px\);/);
  assert.match(read('src/components/wizard/ImportFilesPanel.svelte'), /\.card-grid \{[^}]*repeat\(auto-fill, minmax\(260px, 1fr\)\)/);
  assert.match(read('src/components/BlackboxCharts.svelte'), /\.bbc \{ width: min\(1840px, calc\(100vw - 32px\)\);/);
});

test('the setup window keeps a readable column and its footer lines up with it', () => {
  const setup = read('setup/App.svelte');
  assert.match(setup, /\.content \{[^}]*padding: 20px max\(24px, calc\(\(100% - 1040px\) \/ 2\)\) 16px;/);
  assert.match(setup, /footer \{[^}]*padding: 12px max\(24px, calc\(\(100% - 1040px\) \/ 2\)\);/);
});
