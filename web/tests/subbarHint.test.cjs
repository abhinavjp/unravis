const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');

// The scope bar's height is remembered per window width so the first load can hold its space (no layout shift).
const app = fs.readFileSync(path.join(__dirname, '..', 'src', 'App.svelte'), 'utf8');
const hint = fs.readFileSync(path.join(__dirname, '..', 'src', 'lib', 'subbarHint.ts'), 'utf8');

test('App holds the scope bar space only while loading, hidden from assistive tech', () => {
  assert.match(app, /subbar-reserve[^>]*aria-hidden="true"/);
  assert.match(app, /\{#if subbarShown\}<SubBar \/>\{:else if \(!info \|\| app\.screen === 'loading'\) && subbarHint > 0\}/);
});

test('hint storage is guarded and bounded', () => {
  assert.equal((hint.match(/try \{/g) ?? []).length >= 2, true);
  assert.match(hint, /saved\.h < 600/);
  assert.match(hint, /saved\.b === bucket\(width\)/);
});
