const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');

// Backlog 1, P3: a source that was listed but never imported shows a text mark on its chip. The Svelte file is read as text.

const root = path.resolve(__dirname, '..');
const read = (relative) => fs.readFileSync(path.join(root, relative), 'utf8');

const src = read('src/components/TraceSources.svelte');

test('chip keeps its toggle and text, and shows a text mark with a title', () => {
  assert.match(src, /aria-pressed=\{isChipOn\(selection, chip\.id\)\}/);
  assert.match(src, /\{chipText\(chip\)\}/);
  assert.match(src, /title=\{chip\.noFile \? SOURCES_TEXT\.notImportedTitle : undefined\}/);
  assert.match(src, /\{#if chip\.noFile\}<span class="ts-chip-flag" data-testid="chip-not-imported">/);
});
