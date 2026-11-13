const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');

// W-chart: a red diamond click and Shift+Enter open the drill-in. The Svelte file is read as text.
const src = fs.readFileSync(path.resolve(__dirname, '..', 'src/components/TracingChart.svelte'), 'utf8');

test('the onDiamond prop exists and is optional', () => {
  assert.match(src, /onDiamond\?: \(slot: number\) => void;/);
});

test('the flagged mark carries its slot', () => {
  assert.match(src, /class:flagged=\{mark\.flagged\} data-slot=\{mark\.index\}/);
});

test('the click hit-tests the API lane diamonds before selecting', () => {
  assert.match(src, /import \{ hitFlagged/);
  assert.match(src, /\.panel\[data-lane="audit"\] \.mark\.flagged/);
  assert.match(src, /getBoundingClientRect\(\)/);
  assert.match(src, /hitFlagged\(rects, event\.clientX, event\.clientY, 6\)/);
  const click = src.slice(src.indexOf('function onOverlayClick'));
  assert.ok(click.indexOf('if (onDiamond)') < click.indexOf('selectAt(slot)'));
  assert.ok(click.indexOf('if (onDiamond)') < click.indexOf('showTouchTip(slot, event)'));
});

test('Shift+Enter opens a flagged bucket; plain Enter still selects', () => {
  assert.match(src, /event\.shiftKey && onDiamond && auditMarkFlagged\(current\)\) onDiamond\(current\); else onSelect\?\.\(current\)/);
  assert.match(src, /onSelect\?\.\(selectedSlot\)/);
});
