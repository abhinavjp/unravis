const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');

// Hub fix D, the screen side, checked by the source as the other component tests do: the Add data cards of the import drawer (screen 112) and how
// they route, the per-type cards of the wizard files step (screens 37 and 38), the restored kinds in the select, and the staged list kept in the store.

const root = path.resolve(__dirname, '..');
const read = (relative) => fs.readFileSync(path.join(root, relative), 'utf8');
const hub = read('src/components/sources/SourcesHub.svelte');
const files = read('src/components/wizard/ImportFilesPanel.svelte');
const app = read('src/lib/appState.svelte.ts');
const nav = read('src/lib/sourcesNav.svelte.ts');
const guides = read('src/components/wizard/FileGuides.svelte');

/* ------------------------------------------------------------------ D: the drawer */

test('D: the entry points that already chose files say so', () => {
  assert.match(app, /requestCase\(id\);\s+requestFilesView\(\);/);
  assert.match(app, /function showImportPopup\(/);
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
