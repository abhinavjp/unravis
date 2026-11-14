// Source-text checks for the drill pop-up guard in appState.svelte.ts.
const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');

// W13-05: normalize line endings before parsing. The repo checks out CRLF on
// Windows while CI may see LF; an LF-only delimiter matched nothing on CRLF, so
// slice(start, -1) returned the rest of the file and unrelated code satisfied the
// assertion. Fail explicitly when the closing delimiter is absent.
const src = fs.readFileSync(path.join(__dirname, '..', 'src', 'lib', 'appState.svelte.ts'), 'utf8').replace(/\r\n/g, '\n');

test('drillOpen state and setter exist, and it is not a modal term', () => {
  assert.ok(src.includes('let drillOpen = $state(false);'), 'drillOpen state missing');
  assert.ok(src.includes('export function setDrillOpen(v: boolean) { drillOpen = v; }'), 'setDrillOpen missing');
  assert.ok(!src.includes('importPopup != null || drillOpen'), 'drillOpen must not be in modalOpen: the pop-up lives inside main, which modalOpen makes inert');
});

test('onWindowKey ignores Esc while drill is open, before stepUpOnce', () => {
  const start = src.indexOf('export function onWindowKey(');
  assert.ok(start >= 0, 'onWindowKey missing');
  const end = src.indexOf('\n}\n', start);
  assert.ok(end > start, 'onWindowKey: closing delimiter not found');
  const body = src.slice(start, end);
  const guard = body.indexOf('if (drillOpen) return;');
  const step = body.indexOf('stepUpOnce();');
  assert.ok(guard >= 0, 'drillOpen guard missing in onWindowKey');
  assert.ok(step >= 0, 'stepUpOnce call missing in onWindowKey');
  assert.ok(guard < step, 'drillOpen guard must come before stepUpOnce');
});
