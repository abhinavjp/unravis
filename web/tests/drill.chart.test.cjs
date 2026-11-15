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

test('the click hit-tests the diamonds and IIS dots before selecting', () => {
  assert.match(src, /import \{ hitFlagged/);
  assert.match(src, /\.panel\[data-lane="audit"\] \.mark, /);
  assert.match(src, /getBoundingClientRect\(\)/);
  assert.match(src, /hitFlagged\(rects, event\.clientX, event\.clientY, 6\)/);
  const i = src.indexOf('function onOverlayClick');
  assert.ok(i >= 0);
  const click = src.slice(i);
  assert.ok(click.indexOf('if (onDiamond') < click.indexOf('selectAt(slot)'));
  assert.ok(click.indexOf('if (onDiamond') < click.indexOf('showTouchTip(slot, event)'));
});

test('diamonds in the IIS and SQL lanes and IIS dots also open the Slow call screen', () => {
  assert.ok(src.includes('.panel[data-lane="iis"] .mark,'), 'IIS marks, flagged or not, are click targets');
  assert.ok(src.includes('.panel[data-lane="iis"] .dot'));
  assert.ok(src.includes('.panel[data-lane="sql"] .mark.flagged'));
  assert.ok(src.includes('<span class="dot" data-slot={dot.index}'));
  const i = src.indexOf('function slotOpensCall');
  assert.ok(i >= 0);
  const j = src.indexOf('function diamondAt');
  assert.ok(j >= 0);
  const fn = src.slice(i, j);
  assert.ok(fn.includes('anyMark(hero)'), 'every API-lane mark opens Call detail');
  assert.ok(!fn.includes('anyMark(webHero)'), 'IIS-lane mark does not open Call detail independently');
  assert.ok(!fn.includes('webHero?.dots.some'), 'IIS dots do not open Call detail independently');
  assert.ok(src.includes('.panel[data-lane="audit"] .mark,'), 'API marks, flagged or not, are click targets');
  assert.doesNotMatch(src, /auditMarkFlagged/);
});

test('Shift+Enter opens a flagged bucket; plain Enter still selects', () => {
  assert.match(src, /event\.shiftKey && onDiamond && slotOpensCall\(current\)\) onDiamond\(current\); else onSelect\?\.\(current\)/);
  assert.match(src, /onSelect\?\.\(selectedSlot\)/);
});
