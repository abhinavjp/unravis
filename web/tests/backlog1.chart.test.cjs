const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');

// Backlog 1 P6: the cover sentence replaces the bars and the status line of a lane whose imported files sit outside the window.
// The Svelte file is read as text.

const root = path.resolve(__dirname, '..');
const src = fs.readFileSync(path.join(root, 'src/components/TracingChart.svelte'), 'utf8');

test('the cover sentence is imported from lanes and sits before the status line', () => {
  assert.match(src, /laneImportedLine,/);
  assert.match(src, /const importedLine = \(lane: TracingLane\): string => laneImportedLine\(lane, response\.window, spanAt\);/);
  assert.match(src, /importedLine\(lane\) \|\| laneStatusLineFor\(lane, unselected, SOURCES_TEXT\.laneUnselected\)/);
});

test('a lane with the cover sentence draws no bars', () => {
  assert.match(src, /!!grid && !importedLine\(lane\) && laneDrawsBars\(/);
});

test('the status line element and lane state attribute are unchanged', () => {
  assert.match(src, /data-testid="lane-status"/);
  assert.match(src, /data-state=\{lane\.state\}/);
});
