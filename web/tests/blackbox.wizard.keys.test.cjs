const assert = require('node:assert/strict');
const test = require('node:test');
const { loadTypeScript } = require('./helpers/loadTs.cjs');

// TSK-06: the keyboard rules of the Blackbox wizard pop-up (Enter does Next, Esc asks first, Tab stays inside).

const { enterDoesPrimary, escapeAction, wrapFocus } = loadTypeScript('src/lib/wizardKeys.ts');

// A fake element: tag, optional type, role and data-own-enter, and an optional marked ancestor.
function el(tagName, extra = {}) {
  const attrs = extra.attrs || {};
  return {
    tagName,
    type: extra.type ?? null,
    isContentEditable: !!extra.editable,
    getAttribute: (name) => (name in attrs ? attrs[name] : null),
    closest: (selector) => (selector === '[data-own-enter]' && extra.insideOwnEnter ? {} : null)
  };
}

test('Enter does the primary action on a plain text input', () => {
  assert.equal(enterDoesPrimary(el('INPUT', { type: 'text' })), true);
  assert.equal(enterDoesPrimary(el('INPUT', { type: 'number' })), true);
  assert.equal(enterDoesPrimary(el('INPUT')), true);
});

test('Enter does the primary action on a radio or a tick box, and when nothing is focused', () => {
  assert.equal(enterDoesPrimary(el('INPUT', { type: 'radio' })), true);
  assert.equal(enterDoesPrimary(el('INPUT', { type: 'checkbox' })), true);
  assert.equal(enterDoesPrimary(el('DIALOG')), true);
  assert.equal(enterDoesPrimary(el('DIV')), true);
  assert.equal(enterDoesPrimary(null), true);
  assert.equal(enterDoesPrimary(undefined), true);
});

test('Enter keeps its own meaning on a button, summary, text area, select and link', () => {
  for (const tag of ['BUTTON', 'SUMMARY', 'TEXTAREA', 'SELECT', 'A']) {
    assert.equal(enterDoesPrimary(el(tag)), false, tag);
  }
  assert.equal(enterDoesPrimary(el('button')), false, 'lower case tag names count too');
});

test('Enter keeps its own meaning on an input that acts as a button', () => {
  for (const type of ['button', 'submit', 'reset', 'image', 'file', 'color']) {
    assert.equal(enterDoesPrimary(el('INPUT', { type })), false, type);
  }
  assert.equal(enterDoesPrimary(el('INPUT', { type: 'SUBMIT' })), false, 'type is not case sensitive');
});

test('Enter keeps its own meaning on a content-editable element and on button-like roles', () => {
  assert.equal(enterDoesPrimary(el('DIV', { editable: true })), false);
  for (const role of ['button', 'link', 'menuitem', 'tab', 'combobox', 'listbox', 'option', 'switch']) {
    assert.equal(enterDoesPrimary(el('DIV', { attrs: { role } })), false, role);
  }
  assert.equal(enterDoesPrimary(el('DIV', { attrs: { role: 'group' } })), true, 'a role that does not act on Enter is fine');
});

test('data-own-enter on the element or on an ancestor turns the primary action off', () => {
  assert.equal(enterDoesPrimary(el('INPUT', { type: 'text', attrs: { 'data-own-enter': '' } })), false);
  assert.equal(enterDoesPrimary(el('INPUT', { type: 'text', insideOwnEnter: true })), false);
});

test('on the Review step Enter never does the primary action from the page: only the button acts (defect A)', () => {
  // With needsButton on, nothing but the Place or Build button's own native click may start the install or the pack.
  assert.equal(enterDoesPrimary(el('H2'), true), false, 'focus on the heading');
  assert.equal(enterDoesPrimary(el('DIV'), true), false, 'focus on the body');
  assert.equal(enterDoesPrimary(el('DIALOG'), true), false, 'focus on the dialog');
  assert.equal(enterDoesPrimary(null, true), false, 'nothing focused');
  assert.equal(enterDoesPrimary(el('INPUT', { type: 'text' }), true), false);
  assert.equal(enterDoesPrimary(el('INPUT', { type: 'checkbox' }), true), false);
  assert.equal(enterDoesPrimary(el('BUTTON'), true), false, 'a button keeps its own click, the wizard adds none');
  // Off (every other step) the old rules hold.
  assert.equal(enterDoesPrimary(el('DIV'), false), true);
  assert.equal(enterDoesPrimary(el('INPUT', { type: 'text' })), true);
});

test('an object with no helpers is read safely', () => {
  assert.equal(enterDoesPrimary({ tagName: 'INPUT', type: 'text' }), true);
  assert.equal(enterDoesPrimary({}), true);
});

test('Esc while busy only says why, and never leaves', () => {
  assert.equal(escapeAction(true, false), 'note');
  assert.equal(escapeAction(true, true), 'note');
  assert.equal(escapeAction(true, false, true), 'note');
});

test('Esc asks before leaving, and leaves at once only when the wizard has finished', () => {
  assert.equal(escapeAction(false, false), 'ask');
  assert.equal(escapeAction(false, true), 'leave');
});

test('Esc on the leave question means keep going', () => {
  assert.equal(escapeAction(false, false, true), 'keep');
  assert.equal(escapeAction(false, true, true), 'keep');
});

test('wrapFocus wraps from the last to the first and from the first back to the last', () => {
  assert.equal(wrapFocus(4, 3, false), 0);
  assert.equal(wrapFocus(4, 0, true), 3);
});

test('wrapFocus leaves a normal step inside the list to the browser', () => {
  assert.equal(wrapFocus(4, 1, false), null);
  assert.equal(wrapFocus(4, 2, true), null);
  assert.equal(wrapFocus(4, 0, false), null);
  assert.equal(wrapFocus(4, 3, true), null);
});

test('wrapFocus pulls focus back in when it is outside the list, and copes with one or no items', () => {
  assert.equal(wrapFocus(4, -1, false), 0);
  assert.equal(wrapFocus(4, -1, true), 3);
  assert.equal(wrapFocus(1, 0, false), 0);
  assert.equal(wrapFocus(1, 0, true), 0);
  assert.equal(wrapFocus(0, -1, false), null);
});
