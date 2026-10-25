const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');

const root = path.resolve(__dirname, '..');
const source = fs.readFileSync(path.join(root, 'src/lib/chartZoom.ts'), 'utf8');
// Code only: the header comment may mention words the assertions forbid.
const code = source.replace(/\/\*[\s\S]*?\*\//g, '').replace(/^\s*\/\/.*$/gm, '');

test('the drag band carries a readout', () => {
  assert.match(code, /createElement\('div'\)[\s\S]*?className\s*=\s*'readout'|classList\.add\('readout'\)/, 'a child element with class readout');
  assert.match(code, /zoomView\.readout\(/, 'its text comes from the zoom store');
  assert.match(code, /readout\.textContent\s*=|readout!?\.textContent\s*=/, 'the text is set as text, never as HTML');
  assert.doesNotMatch(code, /innerHTML/);
  const style = code.match(/readout\.style\.cssText\s*=\s*'([^']*)'/);
  assert.ok(style, 'the readout is styled in one cssText');
  const css = style[1];
  assert.ok(css.includes('var(--surface)') && css.includes('var(--line)') && css.includes('var(--text)'));
  assert.ok(css.includes('font-variant-numeric:tabular-nums') && css.includes('12px'));
  assert.doesNotMatch(css, /#[0-9a-f]{3,8}\b|rgb\(|hsl\(/i, 'colours only through tokens');
  const tokens = [...(css + code).matchAll(/var\((--[a-z0-9-]+)\)/g)].map((m) => m[1]);
  for (const token of tokens) assert.ok(['--accent', '--surface', '--line', '--text', '--text-2'].includes(token), `unexpected token ${token}`);
  assert.match(code, /var\(--text-2\)/, 'an out-of-range readout is dimmer');
});

test('the readout is clamped inside the container, is not exposed to assistive tech and does not take the pointer', () => {
  assert.match(code, /Math\.min\([\s\S]*?offsetWidth/);
  assert.match(code, /Math\.max\(/);
  assert.match(code, /readout\.setAttribute\('aria-hidden', 'true'\)/);
  assert.match(code, /pointer-events:none/);
  assert.match(code, /band\.setAttribute\('aria-hidden', 'true'\)/, 'the band keeps aria-hidden');
});

test('a range under the floor lowers the band to .08', () => {
  assert.match(code, /\.ok/);
  assert.match(code, /opacity\s*=\s*[^;]*\.08/);
  assert.match(code, /opacity:\.16/, 'the normal band opacity is unchanged');
});

test('the readout is removed when the band is removed', () => {
  assert.match(code, /const clearBand = \(\) => \{[^}]*readout[^}]*\}/);
  assert.match(code, /readout\?\.remove\(\)|readout!?\.remove\(\)/);
});

test('the readout updates only when the slot pair changes', () => {
  assert.match(code, /lastFrom/);
  assert.match(code, /lastTo/);
  assert.match(code, /\(from === lastFrom && to === lastTo\)/, 'compare the pair before writing');
  const compare = code.search(/=== lastFrom && [a-z]+ === lastTo/);
  const write = code.search(/readout\.textContent\s*=/);
  assert.ok(compare > -1 && write > compare, 'the comparison runs before the text is written');
  assert.match(code, /lastFrom = -1/, 'the pair is reset when the band is removed');
});

test('a drag move stays cheap: one index lookup, widths cached, no layout reads while moving', () => {
  const move = code.slice(code.indexOf('const onMove'), code.indexOf('const stopTracking'));
  assert.ok(move.length > 20);
  assert.equal((move.match(/indexAt\(/g) || []).length, 1, 'the start slot is found once, on pointerdown');
  assert.doesNotMatch(move, /offsetWidth|getBoundingClientRect|getComputedStyle/, 'no forced layout per move');
  assert.doesNotMatch(code, /requestAnimationFrame|setInterval|setTimeout/, 'no new loops or timers');
});

test('no transition or animation', () => {
  assert.doesNotMatch(code, /transition/i);
  assert.doesNotMatch(code, /animation/i);
  assert.doesNotMatch(code, /\.animate\(/);
});

test('crosshair over the plot', () => {
  assert.match(code, /pointermove/);
  assert.match(code, /if \(inPlot\([^)]*\)\) \{[^}]*cursor = 'crosshair'/, "cursor = 'crosshair' inside the inPlot branch");
  assert.match(code, /style\.cursor\s*=\s*''/, 'the cursor is restored outside the plot');
  assert.match(code, /pointerleave/, 'and when the pointer leaves the chart');
  assert.match(code, /removeEventListener\('pointermove'/);
  assert.match(code, /removeEventListener\('pointerleave'/);
});

test('existing drag, touch and keyboard behaviour is unchanged', () => {
  assert.match(code, /const DRAG_PIXELS = 6;/);
  assert.match(code, /node\.style\.touchAction = 'pan-y'/);
  assert.match(code, /queueMicrotask\(\(\) => zoomView\.clearAnnouncement\(\)\)/);
  assert.match(code, /zoomView\.zoomIndexRange\(a, b\)/);
  assert.match(code, /zoomView\.zoomSlot\(b\)/);
  assert.match(code, /chartKeyAction\(event\.key/);
  assert.match(code, /zoomView\.key\(action, cursor\)/);
  assert.match(code, /export function zoomable\(node: HTMLElement, initial: ZoomableOptions\)/);
});
