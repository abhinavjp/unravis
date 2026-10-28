const assert = require('node:assert/strict');
const test = require('node:test');
const { read } = require('./scn1.l4.helpers.cjs');
const header = read('src/components/AppHeader.svelte');
const style = header.slice(header.indexOf('<style>'));
// The body of the first @media block that starts with this exact prefix: up to the line holding only a closing brace.
const mediaBody = (prefix) => {
  const start = style.indexOf(prefix);
  assert.ok(start >= 0, 'missing media block: ' + prefix);
  const end = style.indexOf('\n  }', start);
  return style.slice(start, end);
};

test('D-C-06: from 721px the topbar and the actions may wrap', () => {
  const wide = mediaBody('@media (min-width: 721px)');
  assert.match(wide, /\.topbar \{ flex: 0 0 auto; flex-wrap: wrap;/);
  assert.match(wide, /\.actions \{ flex-shrink: 1; min-width: 0; flex-wrap: wrap; justify-content: flex-end;/);
  assert.match(wide, /\.status \{ flex-shrink: 1; min-width: 0; flex-wrap: wrap; \}/);
});

test('D-C-06: the wrap block comes after the base rules, so it wins', () => {
  assert.ok(style.indexOf('@media (min-width: 721px)') > style.indexOf('.actions { margin-left: auto;'));
  assert.ok(style.indexOf('@media (min-width: 721px)') > style.indexOf('.status, .utilities {'));
});

test('D-C-06: the narrow layout is unchanged', () => {
  const narrow = mediaBody('@media (max-width: 720px)');
  assert.match(narrow, /\.topbar \{ padding: 0 14px; gap: 8px; flex-wrap: nowrap;/);
  assert.match(narrow, /\.crumbbar \{ position: sticky; top: var\(--header-h\);/);
  assert.doesNotMatch(narrow, /flex-wrap: wrap/);
});

test('D-C-06: the base rules and the pinned header rules stay', () => {
  assert.match(style, /\.topbar \{ flex-wrap: nowrap; gap: 12px; \}/);
  assert.match(style, /\.actions \{ margin-left: auto; display: flex; align-items: center; flex-wrap: nowrap; flex-shrink: 0; gap: 12px; \}/);
  assert.match(style, /@media \(min-width: 1200px\) \{ \.reference \{ display: inline; \}/);
});

test('D-C-06: the markup keeps every action inside .actions', () => {
  const markup = header.slice(header.indexOf('</script>'), header.indexOf('<style>'));
  assert.match(markup, /<div class="actions">\s*<div class="status">/);
  assert.match(markup, /<div class="utilities">/);
  assert.match(markup, /<TimeZoneToggle \/>/);
  assert.match(markup, /class="header-button theme-toggle"/);
});

test('D-C-06: the stale one-row comment is gone', () => {
  assert.doesNotMatch(header, /One row at every width: nothing in the bar wraps/);
  assert.doesNotMatch(header, /always on one row/);
});
