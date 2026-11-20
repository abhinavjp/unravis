const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');

const css = fs.readFileSync(path.resolve(__dirname, '..', 'src/app.css'), 'utf8');

// UX-8: at 390 px, Tab moved focus to controls fully under the pinned wizard bar.
test('a page with the pinned wizard bar keeps room under focused controls', () => {
  assert.match(css, /html:has\(\.wizard \.bar\) \{ scroll-padding-bottom: 96px; \}/);
});
