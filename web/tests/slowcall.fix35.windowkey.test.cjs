// FIX-35 / TEST-04 (a): the window key handler `onWindowKey` ignores Esc whose target is a text field.
//
// Behaviour test: it compiles and RUNS the shipped `onWindowKey` from src/lib/appState.svelte.ts (the module is a Svelte
// rune module with ~40 imports and browser globals, so it cannot be imported by node; the function is cut out by its own
// boundary, stripped of types, and run in a closure that supplies the state it reads, with spies for what it calls).
// slowcall.esc.test.cjs (UI-01) covers the plain field cases; this file adds the cross cases (a field inside an open menu,
// every editable tag, order against the import popup) and a mutation check that proves these assertions can fail.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const ts = require('typescript');

const src = fs
  .readFileSync(path.join(__dirname, '..', 'src', 'lib', 'appState.svelte.ts'), 'utf8')
  .replace(/\r\n/g, '\n');

function handlerSource() {
  const start = src.indexOf('export function onWindowKey(');
  assert.ok(start >= 0, 'onWindowKey missing from appState.svelte.ts');
  const end = src.indexOf('\n}\n', start);
  assert.ok(end > start, 'onWindowKey body not found');
  return src.slice(start, end + 2);
}

// Builds a handler factory from a function source (the real one, or a deliberately broken copy for the mutation check).
function compile(fnSource) {
  const js = ts.transpileModule(fnSource.replace(/^export\s+/, ''), {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
  }).outputText;
  return new Function(
    'env',
    `
    let importPopup = env.importPopup;
    let glossaryOpen = env.glossaryOpen;
    let coverageSource = env.coverageSource;
    let sidePanel = env.sidePanel;
    let connectionsOpen = env.connectionsOpen;
    let slowCall = env.slowCall;
    const document = env.document;
    const HTMLElement = env.HTMLElement;
    const calls = { closeSlowCall: 0, stepUpOnce: 0, closeGlossary: 0 };
    const closeGlossary = () => { calls.closeGlossary++; };
    const closeCoverage = () => {};
    const closeSidePanel = () => {};
    const closeConnections = () => {};
    const closeSlowCall = () => { calls.closeSlowCall++; slowCall = null; };
    const stepUpOnce = () => { calls.stepUpOnce++; };
    ${js}
    return { onWindowKey, calls, getSlowCall: () => slowCall };
  `,
  );
}
const factory = compile(handlerSource());

class HTMLElement {
  constructor(tagName, opts = {}) {
    this.tagName = tagName;
    this.isContentEditable = !!opts.isContentEditable;
    this.insideMenu = opts.insideMenu || null;
  }
  closest(selector) {
    if (selector === '[hidden]') return null;
    return null; // not in a .popover
  }
}

// An open <details> menu that contains the elements listed in `inside`.
function makeMenu(inside = []) {
  const menu = {
    open: true,
    focused: false,
    closest: () => null,
    contains: (el) => inside.includes(el),
    querySelector: () => ({ focus() { menu.focused = true; } }),
  };
  return menu;
}

function env(overrides = {}) {
  return {
    importPopup: false,
    glossaryOpen: false,
    coverageSource: null,
    sidePanel: null,
    connectionsOpen: false,
    slowCall: { caseId: 'case-1', id: 'call-1' },
    document: { body: new HTMLElement('BODY'), querySelectorAll: () => [] },
    HTMLElement,
    ...overrides,
  };
}
const esc = (target) => ({ key: 'Escape', defaultPrevented: false, target });

test('Esc in any editable element leaves the Slow call screen and its typed values alone', () => {
  const editable = [
    new HTMLElement('INPUT'), // the Custom window boxes (type=number)
    new HTMLElement('TEXTAREA'),
    new HTMLElement('SELECT'),
    new HTMLElement('DIV', { isContentEditable: true }),
  ];
  for (const target of editable) {
    const h = factory(env());
    h.onWindowKey(esc(target));
    assert.equal(h.calls.closeSlowCall, 0, `${target.tagName} must not close the screen`);
    assert.equal(h.calls.stepUpOnce, 0, `${target.tagName} must not step up`);
    assert.ok(h.getSlowCall(), `${target.tagName} keeps the call open`);
  }
});

test('Esc in a field inside an OPEN menu does not close the menu or the screen (the field owns the key)', () => {
  const field = new HTMLElement('INPUT');
  const menu = makeMenu([field]);
  const e = env({ document: { body: new HTMLElement('BODY'), querySelectorAll: () => [menu] } });
  const h = factory(e);
  h.onWindowKey(esc(field));
  assert.equal(menu.open, true, 'the menu stays open while the person types in it');
  assert.equal(h.calls.closeSlowCall, 0);
  assert.equal(h.calls.stepUpOnce, 0);
});

test('control: Esc on a plain element inside the same open menu closes the menu first, not the screen', () => {
  const plain = new HTMLElement('BUTTON');
  const menu = makeMenu([plain]);
  const e = env({ document: { body: new HTMLElement('BODY'), querySelectorAll: () => [menu] } });
  const h = factory(e);
  h.onWindowKey(esc(plain));
  assert.equal(menu.open, false);
  assert.equal(h.calls.closeSlowCall, 0);
});

test('while the import popup is open, Esc in the page behind it does nothing at all', () => {
  const h = factory(env({ importPopup: true }));
  h.onWindowKey(esc(new HTMLElement('DIV')));
  assert.equal(h.calls.closeSlowCall, 0);
  assert.equal(h.calls.stepUpOnce, 0);
  assert.ok(h.getSlowCall());
});

test('a target that is not an element (window, document) is treated as loose focus: the screen closes', () => {
  const h = factory(env());
  h.onWindowKey(esc({}));
  assert.equal(h.calls.closeSlowCall, 1);
});

// ---- Mutation check: prove these tests can fail. ----
// The same function with the editable-field guard removed must close the screen for an INPUT; the assertions above would then fail.
test('mutation: with the field guard removed, Esc in an INPUT closes the screen (so the tests above can fail)', () => {
  const real = handlerSource();
  const guard = "target.tagName === 'INPUT' || ";
  assert.ok(real.includes(guard), 'the guard text is still in the handler (update this test if the guard was reshaped)');
  const broken = compile(real.replace(guard, ''));
  const h = broken(env());
  h.onWindowKey(esc(new HTMLElement('INPUT')));
  assert.equal(h.calls.closeSlowCall, 1, 'without the guard the field Esc closes the screen');
  assert.equal(h.getSlowCall(), null);
});
