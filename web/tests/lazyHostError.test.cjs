const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');

// A chunk that cannot load must say so and offer a retry; it used to leave a blank page with the rest of it inert.
const host = fs.readFileSync(path.join(__dirname, '..', 'src', 'lib', 'components', 'LazyHost.svelte'), 'utf8');

test('failed load shows an alert with Try again and Close', () => {
  assert.match(host, /role="alert"/);
  assert.match(host, />Try again</);
  assert.match(host, />Close</);
  assert.match(host, /\(\) => \{ failed = true; \}/);
});

test('Close hands back to the opener onClose so the page unlocks', () => {
  assert.match(host, /typeof close === 'function'\) close\(\)/);
});
