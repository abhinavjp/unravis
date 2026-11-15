const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');

const src = fs.readFileSync(path.resolve(__dirname, '..', 'src/components/SlowCallScreen.svelte'), 'utf8').replace(/\r\n/g, '\n');

test('opening another call focuses the screen title after scrolling and the next DOM update', () => {
  const start = src.indexOf('function openCall(');
  assert.ok(start >= 0, 'openCall exists');
  const end = src.indexOf('\n  function pickNearby(', start);
  assert.ok(end >= 0 && end > start, 'the next function bounds openCall');
  const fn = src.slice(start, end);
  const scroll = fn.indexOf('titleEl?.scrollIntoView?.(');
  const tick = fn.indexOf('tick()');
  const focus = fn.indexOf('titleEl?.focus({ preventScroll: true })');
  assert.ok(scroll >= 0, 'openCall scrolls to the title');
  assert.ok(tick >= 0, 'openCall waits for the next DOM update');
  assert.ok(focus >= 0, 'openCall focuses the title without scrolling again');
  assert.ok(tick > scroll && focus > tick, 'focus follows scrolling and tick');
  assert.match(fn, /void tick\(\)\.then\(\(\) => titleEl\?\.focus\(\{ preventScroll: true \}\)\);/);
  assert.match(src, /import \{[^}]*\btick\b[^}]*\} from 'svelte';/);
  assert.match(src, /<h2 id="scall-title" tabindex="-1" bind:this=\{titleEl\}/);
});
