const assert = require('node:assert/strict');
const test = require('node:test');
const { read } = require('./scn1.l4.helpers.cjs');
const css = read('src/app.css');
const marker = '@media (pointer: coarse), (max-width: 720px) {';
const rule = (selector) => {
  const start = css.indexOf(selector + ' {');
  assert.ok(start >= 0, 'missing rule: ' + selector);
  return css.slice(start, css.indexOf('}', start) + 1);
};

test('D-C-25: one touch and narrow block exists, and it is the last thing in the file', () => {
  const start = css.indexOf(marker);
  assert.ok(start > 0);
  assert.equal(css.indexOf(marker, start + 1), -1, 'only one such block');
  const block = css.slice(start);
  assert.equal((block.match(/@media/g) ?? []).length, 1);
});

test('D-C-25: the block lifts every listed control to 44px', () => {
  const block = css.slice(css.indexOf(marker));
  for (const selector of ['.header-button', '.chip', '.segmented button', '.more-filters > summary', '.filter-chip', '.menu > summary', '.primary-button', '.secondary-button', '.quiet-button']) {
    assert.ok(block.includes(selector), selector);
  }
  assert.match(block, /min-height: 44px; \}/);
  assert.match(block, /\.theme-toggle, \.icon-button \{ min-width: 44px; min-height: 44px; \}/);
});

test('D-C-25: the block holds only min-height and min-width, no colours, no !important', () => {
  const body = css.slice(css.indexOf(marker)).replace(marker, '').replace(/min-(width|height): 44px/g, '');
  assert.doesNotMatch(body, /!important|#[0-9a-fA-F]{3,8}\b|rgb|padding|font-size|width: \d|height: \d/);
});

test('D-C-25: the desktop sizes are unchanged', () => {
  assert.match(rule('.header-button'), /min-height: 32px/);
  assert.match(rule('.theme-toggle'), /width: 32px/);
  assert.match(rule('.chip'), /min-height: 28px/);
  assert.match(rule('.segmented button'), /min-height: 28px/);
  assert.match(rule('.more-filters > summary'), /min-height: 28px/);
  assert.match(rule('.filter-chip'), /min-height: 28px/);
  assert.match(rule('.menu > summary'), /min-height: 28px/);
  assert.match(rule('.icon-button'), /width: 28px; height: 28px/);
  assert.match(rule('.primary-button, .secondary-button, .quiet-button'), /padding: 6px 12px/);
});

test('D-C-25: the topbar height is untouched', () => {
  assert.match(css, /\.topbar \{ position: relative; z-index: 12; flex: 0 0 var\(--header-h\); min-height: var\(--header-h\);/);
});
