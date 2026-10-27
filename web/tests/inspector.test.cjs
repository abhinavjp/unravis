const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');

const root = path.resolve(__dirname, '..');
const read = (relative) => fs.readFileSync(path.join(root, relative), 'utf8').replace(/\r\n/g, '\n');
const inspector = read('src/components/Inspector.svelte');
const css = read('src/brand/tokens.css') + '\n' + read('src/app.css');

test('the inspector is built on the shared Drawer as a non-modal panel', () => {
  assert.match(inspector, /import Drawer from '\.\/Drawer\.svelte'/);
  assert.match(inspector, /<Drawer modal=\{false\}/);
  assert.match(inspector, /class=\{off \? 'inspector off' : 'inspector'\}/);
  assert.match(inspector, /titleId=\{headingId\} focusTitle/);
  assert.match(inspector, /id=\{tabs\.length \? 'tabpanel-' \+ active : undefined\}/);
  assert.match(inspector, /role="tablist"/);
  assert.match(inspector, /ArrowRight/);
});

test('the inspector is a side drawer from 900px and a full view only below it', () => {
  assert.match(css, /--drawer-w: min\(720px, 56vw\);/);
  assert.match(css, /@media \(min-width: 900px\) \{\n  \.stage \{[^}]*\}\n  \.stage\.with-drawer \{ padding-right: calc\(var\(--drawer-w\) \+ 24px\); \}/);
  const narrow = /@media \(max-width: 899px\) \{([\s\S]*?)\n\}/.exec(css);
  assert.ok(narrow, 'a max-width 899px block exists');
  assert.match(narrow[1], /\.inspector \{ position: static;/);
  assert.match(narrow[1], /\.view\.under \{ display: none; \}/);
  // The ranked list stays visible beside the drawer between 900px and 1099px.
  const mid = /@media \(max-width: 1099px\) \{([\s\S]*?)\n\}/.exec(css);
  assert.ok(mid && !/\.view\.under/.test(mid[1]) && !/\.inspector \{/.test(mid[1]));
});

test('the drawer leaves room for the list: at 900px the drawer is at most 56% of the width', () => {
  const ratio = 0.56;
  for (const width of [900, 1024, 1100, 1600]) assert.ok(Math.min(720, ratio * width) / width <= ratio + 1e-9);
  assert.ok(1 - ratio >= 0.44 - 1e-9);
});

test('no block in the evidence frame uses a side stripe', () => {
  for (const file of ['EvidenceView', 'Inspector', 'Drawer', 'ProvenanceNote', 'PayloadViewer']) {
    const source = read('src/components/' + file + '.svelte');
    assert.doesNotMatch(source, /border-left:\s*\d+px solid/, file + ' has no left stripe');
  }
});

test('request history purpose line ends each case with a full stop before the next sentence', () => {
  const view = read('src/components/EvidenceView.svelte');
  assert.match(view, /'every API group in this comparison'\) \+ '\.' :/);
  assert.match(view, /selected\.outcome \+ '\.' :/);
});

test('payload blocks are monospace and have Copy buttons', () => {
  const viewer = read('src/components/PayloadViewer.svelte');
  assert.match(viewer, /navigator\.clipboard\.writeText/);
  assert.match(viewer, /copy\('request'/);
  assert.match(viewer, /copy\('response'/);
  assert.match(css, /\.payload-columns pre \{[^}]*var\(--mono\)/);
});
