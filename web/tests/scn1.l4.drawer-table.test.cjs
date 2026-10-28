const assert = require('node:assert/strict');
const test = require('node:test');
const { read } = require('./scn1.l4.helpers.cjs');
const css = read('src/app.css');
const rule = (selector) => {
  const start = css.indexOf(selector + ' {');
  assert.ok(start >= 0, 'missing rule: ' + selector);
  return css.slice(start, css.indexOf('}', start) + 1);
};

test('D-A-40: the drawer hides the wide columns including API ms', () => {
  assert.match(css, /\.inspector \.request-table :is\(th, td\):is\(:nth-child\(2\), :nth-child\(5\), :nth-child\(7\), :nth-child\(8\), :nth-child\(9\), :nth-child\(10\)\) \{ display: none; \}/);
});

test('D-A-40: the long columns have narrower caps in the drawer', () => {
  assert.match(rule('.inspector .request-table td:nth-child(3)'), /max-width: 170px/);
  assert.match(rule('.inspector .request-table td small'), /max-width: 170px/);
  assert.match(rule('.inspector .request-table td:nth-child(4) small'), /white-space: normal/);
  assert.match(rule('.inspector .request-table td:first-child'), /white-space: normal/);
});

test('D-A-40: the last column is narrower and its buttons stack', () => {
  assert.match(rule('.inspector .request-table td:last-child'), /min-width: 130px/);
  assert.match(rule('.inspector .request-table td:last-child'), /overflow: visible/);
  assert.match(rule('.inspector .request-table td:last-child button'), /display: block/);
});

test('D-A-40: the table outside the drawer is unchanged', () => {
  assert.match(css, /\.request-table td \{ max-width: 240px; overflow: hidden; text-overflow: ellipsis; \}/);
  assert.match(css, /\.request-table td:nth-child\(3\) \{ max-width: 280px; \}/);
  assert.match(css, /td small \{ display: block; margin-top: 2px; color: var\(--text-2\); font-size: 12px; max-width: 280px;/);
});

test('D-A-40: every new drawer rule is scoped to the inspector', () => {
  const block = css.slice(css.indexOf('/* In the drawer the table must fit'), css.indexOf('.request-table td { max-width: 240px'));
  const lines = block.split(/\r?\n/).filter(l => l.includes('{'));
  assert.ok(lines.length >= 8);
  for (const line of lines) assert.match(line, /^\.inspector \.request-table /);
});
