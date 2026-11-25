// BL-495f: how the "?" hint opens (decision 2026-11-23). Read from the source; the browser behaviour is pinned by tools/e2e/specs/ui-hint-keys.spec.ts.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');

const src = fs.readFileSync(path.resolve(__dirname, '..', 'src/components/Hint.svelte'), 'utf8');
const markup = src.slice(src.indexOf('{#if term}'), src.indexOf('<style>'));

test('BL-495f: the hint trigger is one native button that opens on its click (Enter and Space are that click) and says it opens a dialog', () => {
  const trigger = markup.match(/<button type="button" class="hint-button"[^>]*>/);
  assert.ok(trigger, 'the trigger is a <button type="button">');
  assert.match(trigger[0], /aria-haspopup="dialog"/);
  assert.match(trigger[0], /aria-expanded=\{open\}/);
  assert.match(trigger[0], /onclick=\{toggle\}/);
});

test('BL-495f: nothing opens a hint on hover or on focus alone (touch cannot hover; a popover on focus would pull the focus from a person tabbing past)', () => {
  assert.doesNotMatch(src, /onmouseenter|onmouseover|onpointerenter|onpointerover|onfocusin|onfocus=|mouseenter|pointerenter/);
  assert.match(src, /e\.key !== 'Escape'|event\.key !== 'Escape'/, 'Esc closes it');
  assert.match(src, /onfocusout=\{leave\}/, 'focus leaving both the button and the popover closes it');
  assert.match(src, /window\.addEventListener\('pointerdown', away\)/, 'a press elsewhere closes it');
});
