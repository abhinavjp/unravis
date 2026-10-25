const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');

// The When group of the More filters panel. Source-level pins, because the component is not mounted in node. Everything here is synthetic.
const root = path.resolve(__dirname, '..');
const read = (relative) => fs.readFileSync(path.join(root, relative), 'utf8');
const SOURCE = 'src/components/filters/WhenGroup.svelte';

test('WhenGroup owns the date windows, keeps ids, and shows UTC without a disclosure', () => {
  const src = read(SOURCE);
  for (const id of ['current-start', 'current-end', 'reference-start', 'reference-end']) assert.match(src, new RegExp(`id="${id}"`));
  assert.match(src, /id="current-range-label"/);
  assert.match(src, /id="reference-range-label"/);
  assert.match(src, /<Hint termId="reference-period"/);
  assert.match(src, /dateInputsValid = invalidWindows\.length === 0/);
  assert.doesNotMatch(src, /part !== 'bar'/);
  assert.doesNotMatch(src, /<details/);
  assert.match(src, /class="utc-echo"[\s\S]*?<time datetime=\{query\.start\}>/);
  assert.match(src, /<p class="inline-error" role="alert">\{timeInputError\}<\/p>/);
  assert.match(src, /Enter both a date and time for every range\./);
  assert.match(src, /Complete every date and time with a valid local value before running the comparison\./);
  assert.doesNotMatch(src.slice(src.indexOf('<style>')), /#[0-9a-fA-F]{3,8}\b|rgba?\(/);
});

test('the UTC echo hides while its window is invalid', () => {
  const src = read(SOURCE);
  assert.equal(src.split('class="utc-echo"').length - 1, 2);
  const guards = src.match(/\{#if !invalidWindows\.some\([^\n]*class="utc-echo"/g) ?? [];
  assert.equal(guards.length, 2);
});

test('WhenGroup is wrapped in a FilterGroup, keeps the copy and labels each window by its heading', () => {
  const src = read(SOURCE);
  assert.match(src, /<FilterGroup id="when" title="When">/);
  assert.match(src, /Times use your browser time zone: <strong>\{browserZone\}<\/strong>\./);
  assert.match(src, /Requests in the period you want to inspect\./);
  assert.match(src, /An earlier period to compare with\./);
  assert.match(src, /<section class="when-window" aria-labelledby="current-range-label">/);
  assert.match(src, /<section class="when-window" aria-labelledby="reference-range-label">/);
  assert.match(src, /<h3 id="current-range-label">Investigate<\/h3>/);
  assert.match(src, /<h3 id="reference-range-label">Reference<Hint termId="reference-period" \/><\/h3>/);
  assert.equal(src.split('<DateTimeField ').length - 1, 4);
  assert.equal(src.split('label="From"').length - 1, 2);
  assert.equal(src.split('label="To"').length - 1, 2);
  assert.match(src, /\{#if timeInputError\}[^\n]*\{\/if\}\s*<\/FilterGroup>/);
  assert.doesNotMatch(src, /Total request|Run comparison'/);
});

test('WhenGroup lays out windows and bounds with the planned breakpoints and no fixed widths', () => {
  const src = read(SOURCE);
  const style = src.slice(src.indexOf('<style>'));
  assert.match(style, /\.when-windows\s*\{[^}]*grid-template-columns:\s*minmax\(0,\s*1fr\)/);
  assert.match(style, /@media \(min-width: 721px\)[^@]*\.when-windows\s*\{[^}]*repeat\(2, minmax\(0,\s*1fr\)\)/);
  assert.match(style, /\.window-bounds\s*\{[^}]*grid-template-columns:\s*minmax\(0,\s*1fr\)/);
  assert.match(style, /@media \(min-width: 1100px\)[^@]*\.window-bounds\s*\{[^}]*repeat\(2, minmax\(0,\s*1fr\)\)/);
  const echo = style.match(/\.utc-echo\s*\{([^}]*)\}/)?.[1] ?? '';
  for (const rule of [/font-size:\s*12px/, /color:\s*var\(--text-2\)/, /font-variant-numeric:\s*tabular-nums/, /overflow-wrap:\s*anywhere/]) assert.match(echo, rule);
  assert.doesNotMatch(style, /animation|transition/);
});

test('WhenGroup keeps its window logic: quick-range repair state, interval notes and local conversion', () => {
  const src = read(SOURCE);
  assert.match(src, /let \{ query = \$bindable\(\), dateInputsValid = \$bindable\(\) \}/);
  assert.match(src, /if \(dateInputsValid\) untrack\(/);
  assert.match(src, /bucketForWindows\(query\.start, query\.end, query\.baselineStart, query\.baselineEnd\)/);
  assert.match(src, /intervalNote\.set\(typed \? intervalReplacedNote\(previous, query\) : ''\)/);
  assert.match(src, /fromLocalParts\(datePart, timePart\)/);
  assert.doesNotMatch(src, /onScopeChange/);
});
