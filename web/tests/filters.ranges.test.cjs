const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');

const root = path.resolve(__dirname, '..');
const read = (relative) => fs.readFileSync(path.join(root, relative), 'utf8');

test('RangesGroup is the only home of the range filters', () => {
  const src = read('src/components/filters/RangesGroup.svelte');
  assert.match(src, /RANGE_FIELDS/);
  assert.match(src, /rangeNote/);
  assert.match(src, /syncRangeDraft\(/);
  assert.match(src, /role="group" aria-labelledby="rf-\{field\.key\}-label"/);
  assert.match(src, /rf-\{field\.key\}-min|'rf-' \+ field\.key \+ '-min'/);
  assert.doesNotMatch(src, /<details/);
  const styleMatch = src.match(/<style>([\s\S]*)<\/style>/);
  if (styleMatch) {
    const style = styleMatch[1];
    assert.doesNotMatch(style, /#[0-9a-fA-F]{3,8}\b|rgba?\(|hsla?\(/);
  }
});

test('range errors sit in their own row and are announced', () => {
  const src = read('src/components/filters/RangesGroup.svelte');
  assert.match(src, /class="inline-error range-error"[^>]*role="alert">Not applied\. \{/);
  const styleMatch = src.match(/<style>([\s\S]*)<\/style>/);
  assert(styleMatch, 'component has a <style> block');
  const style = styleMatch[1];
  assert.match(style, /\.range-error\s*\{[^}]*grid-column:\s*1 \/ -1/);
  assert.match(src, /aria-describedby=/);
});

test('each range keeps its label and effect copy', () => {
  const src = read('src/components/filters/RangesGroup.svelte');
  const effects = [
    'Complete requests only, so it sets Completion to Complete. Its minimum also raises the slowest-requests floor.',
    'Complete requests only, so it sets Completion to Complete. Its minimum also raises the slowest-requests floor when the time measured is API time.',
    'Complete requests only, so it sets Completion to Complete.',
    'Requests that do not record a size never match a bounded range.',
    'Requests that do not record a size never match a bounded range.',
    'Incomplete and pending requests only, so it sets Completion to Incomplete. It cannot be combined with a time range.'
  ];
  for (const effect of effects) {
    assert.match(src, new RegExp(effect.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')));
  }
  assert.match(src, /Time without finishing/);
});

test('the range table drops its header and stacks at phone width', () => {
  const src = read('src/components/filters/RangesGroup.svelte');
  const styleMatch = src.match(/<style>([\s\S]*)<\/style>/);
  assert(styleMatch, 'component has a <style> block');
  const style = styleMatch[1];
  assert.match(style, /@media \(max-width: 720px\)[\s\S]*\.ranges-head\s*\{\s*display:\s*none/);
  assert.match(src, /aria-hidden="true"/);
});