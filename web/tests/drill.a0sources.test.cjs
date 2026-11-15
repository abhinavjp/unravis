const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');

const src = fs.readFileSync(
  path.join(__dirname, '..', 'src', 'components', 'TraceSources.svelte'),
  'utf8',
);

test('not-imported chip is disabled', () => {
  assert.ok(src.includes('disabled={disabled || chip.noFile === true}'));
});

test('not-imported chip points at the reason paragraph', () => {
  assert.ok(src.includes(
    "aria-describedby={chip.noFile ? 'ts-not-imported-reason' : undefined}",
  ));
});

test('reason paragraph is rendered with its testid and id', () => {
  assert.ok(src.includes('{#if chips.some((c) => c.noFile)}'));
  assert.ok(src.includes('id="ts-not-imported-reason"'));
  assert.ok(src.includes('data-testid="chip-not-imported-reason"'));
  assert.ok(src.includes('{SOURCES_TEXT.notImportedTitle}</p>'));
});

test('existing title and flag span are kept', () => {
  assert.ok(src.includes('SOURCES_TEXT.notImportedTitle : undefined'));
  assert.ok(src.includes('data-testid="chip-not-imported"'));
});
