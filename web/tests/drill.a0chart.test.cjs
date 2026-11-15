const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');

// Imported-range text: the year is shown when that date's year differs from the window's.

const root = path.resolve(__dirname, '..');
const source = fs.readFileSync(path.join(root, 'src/components/TracingChart.svelte'), 'utf8');

test('TracingChart defines spanAt for imported-range ends', () => {
  assert.ok(/function spanAt\(|const spanAt =/.test(source), 'spanAt is defined');
});

test('importedLine passes spanAt to laneImportedLine', () => {
  const line = source.split('\n').find((text) => text.includes('const importedLine'));
  assert.ok(line, 'importedLine is defined');
  assert.ok(/\bspanAt\b/.test(line), 'importedLine uses spanAt');
  assert.ok(!/,\s*at\)/.test(line), 'importedLine no longer passes at');
});
