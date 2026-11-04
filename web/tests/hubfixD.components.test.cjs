const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');

// Hub fix D, the screen side, checked by the source as the other component tests do: the Add data cards of the import drawer (screen 112) and how
// they route, the per-type cards of the wizard files step (screens 37 and 38), the restored kinds in the select, and the staged list kept in the store.

const root = path.resolve(__dirname, '..');
const read = (relative) => fs.readFileSync(path.join(root, relative), 'utf8');
const panel = read('src/lib/components/ImportPanel.svelte');
const grid = read('src/components/sources/AddDataPanel.svelte');
const hub = read('src/components/sources/SourcesHub.svelte');
const files = read('src/components/wizard/ImportFilesPanel.svelte');
const batch = read('src/lib/components/ImportBatch.svelte');
const app = read('src/lib/appState.svelte.ts');
const nav = read('src/lib/sourcesNav.svelte.ts');
const guides = read('src/components/wizard/FileGuides.svelte');

/* ------------------------------------------------------------------ D: the drawer */

test('D: the drawer opens on the cards unless files were asked for, and the intent is taken once at open', () => {
  assert.match(panel, /takeFilesViewIntent\(\) \|\| discoverAtOpen \? 'files' : 'grid'/);
  assert.match(panel, /const showGrid = \$derived\(caseOpen && view === 'grid'\)/);
  assert.match(panel, /<AddDataPanel scope="drawer"/);
});

test('D: the entry points that already chose files say so', () => {
  assert.match(app, /requestCase\(activeCaseId\);\s+requestFilesView\(\);/);
  assert.match(app, /requestFilesView\(\); openSidePanel\('import'\)/);
});

test('D: the file list stays mounted while the cards show, so staged files are not lost', () => {
  assert.match(panel, /<div class="files-view" hidden=\{showGrid\}>/);
  assert.match(panel, /\.files-view\[hidden\] \{ display: none; \}/);
  assert.match(panel, /bind:busy=\{batchBusy\} bind:staged/);
});

test('D: Files opens the list and focuses its heading; Back returns to the Files card', () => {
  const fn = panel.slice(panel.indexOf('function chooseCard('));
  assert.match(fn, /what === 'files'\) \{\s+view = 'files';/);
  assert.match(panel, /id="file-heading" tabindex="-1" bind:this=\{fileHeadingEl\}/);
  assert.match(panel, /data-back-to-cards onclick=\{backToCards\}/);
  assert.match(panel, /\[data-drawer-card="files"\] button/);
});

test('D: the other cards go to existing flows', () => {
  const fn = panel.slice(panel.indexOf('function chooseCard('), panel.indexOf('function backToCards'));
  assert.match(fn, /what === 'blackbox'\) \{ openSidePanel\('blackbox'\)/);
  assert.match(fn, /requestSourcesTab\(what === 'sql' \? 'sql' : what === 'mongo' \? 'mongo-add' : 'day'\)/);
  assert.match(fn, /openCaseHome\(\);\s+onClose\(\);/);
  assert.match(nav, /wantTab: '' \| 'sql' \| 'mongo-add' \| 'day'/);
  assert.match(hub, /sourcesNav\.wantTab/);
  assert.match(hub, /want === 'mongo-add'\) void tick\(\)\.then\(\(\) => \{ addingAudit = true; \}\)/);
});

test('D: leaving with staged files asks first, through Close, Escape, the scrim and the leaving cards', () => {
  assert.match(panel, /const requestClose = \(\) => leaveThen\(onClose\);/);
  assert.match(panel, /onClose=\{requestClose\}/);
  assert.match(panel, /if \(heldFiles === 0\) \{ action\(\); return; \}/);
  assert.match(panel, /role="alertdialog" aria-labelledby="leave-head" aria-describedby="leave-text" data-leave/);
  assert.match(panel, /const heldFiles = \$derived\(staged \+ \(file \? 1 : 0\)\)/);
});

test('D: another case starts on the cards again, the first one picked does not', () => {
  assert.match(panel, /if \(id !== caseSeen\) \{ if \(caseSeen !== ''\) view = 'grid'; caseSeen = id; \}/);
});

test('D: the grid has its own ids and marks so it never collides with the case page copy', () => {
  assert.match(grid, /'data-drawer-card': id/);
  assert.match(grid, /'drawer-add-head' : 'add-head'/);
  assert.match(grid, /ADD_TEXT\.drawerLead/);
});

test('D: while an import is sending every card says why it is off', () => {
  assert.match(grid, /\{#if busy\}<p class="hint" data-busy role="status">\{ADD_TEXT\.busy\}<\/p>\{\/if\}/);
  assert.match(grid, /const off = \$derived\(readOnly \|\| busy\)/);
  assert.match(panel, /busy=\{sending\}/);
});

test('D: the drawer counts what is staged and not imported', () => {
  assert.match(files, /pending = \$bindable\(0\)/);
  assert.match(files, /if \(drawer\) pending = list\.filter\(\(item\) => results\[item\.id\] === undefined\)\.length/);
  assert.match(batch, /bind:pending=\{staged\}/);
});

/* ------------------------------------------------------------------ C: the cards of the wizard step */

test('C: every card has its own Add button and file input, and the inputs are named', () => {
  assert.match(files, /data-file-card=\{card\.id\}/);
  assert.match(files, /aria-label=\{cardInputLabel\(card\)\}/);
  assert.match(files, /bind:this=\{cardInputs\[card\.id\]\}/);
  assert.match(files, /\{card\.button\}/);
});

test('C: each card lists only its own staged files, one table per group', () => {
  assert.match(files, /list\.filter\(\(item\) => item\.origin === card\.id\)/);
  assert.match(files, /data-group=\{group\.id\}/);
});

test('C: the cards are a layer on the single panel, which stays, and the drawer has none', () => {
  assert.match(files, /not sure|CARDS_TEXT\.unsure/i);
  assert.match(files, /\{#if !drawer\}<FileGuides \/>\{\/if\}/);
  assert.match(guides, /data-guide/);
  assert.match(guides, /CARD_GUIDES/);
});

test('F35: the restored kinds are in the select for the wizard only, and the drawer refuses them', () => {
  assert.match(files, /\{#if !drawer\}/);
  assert.match(files, /EXTRA_KIND_IDS|EXTRA_KIND_LABELS/);
  assert.match(files, /isPanelKind/);
});

test('F37: the wizard keeps its list in the store and only the drawer ends its own with the panel', () => {
  assert.match(files, /drawer \? createStaging\(\) : wizardStaging/);
  assert.match(files, /onDestroy\(\(\) => \{ if \(drawer\) staging\.cancel\(\); \}\);/);
});
