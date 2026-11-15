// UI-01 BEHAVIOUR test: Esc inside a text field must not close the Slow call screen.
//
// This is a real behaviour test, not a source-text/regex check. It compiles and EXECUTES the actual
// exported `onWindowKey` from src/lib/appState.svelte.ts and drives it with synthetic Esc events and
// mock targets, then asserts on the observable effect (was the call closed / did the screen step up).
//
// Why a harness instead of importing the module: appState.svelte.ts is a Svelte 5 rune module with
// ~40 imports (several of them .svelte.ts rune modules), $state/$derived/$effect runes, browser globals
// (sessionStorage, document, HTMLElement, Intl) and top-level side effects. It cannot be loaded by
// `node --test`. Following the packet, we build the smallest feasible harness: we take the REAL function
// source (the same `\n}\n` boundary the backstop tests use), strip its types with the TypeScript
// compiler, and run it inside a closure that supplies the module state it reads (slowCall, the drawer
// flags, document, HTMLElement) and spies for closeSlowCall / stepUpOnce. The logic under test is the
// shipped code; only the surrounding rune state is mocked. If the editable-target guard is removed or
// reordered, these assertions fail.
const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const ts = require('typescript');

const src = fs
  .readFileSync(path.join(__dirname, '..', 'src', 'lib', 'appState.svelte.ts'), 'utf8')
  .replace(/\r\n/g, '\n');

// Extract the real exported function, exactly as the backstop tests isolate it.
function onWindowKeySource() {
  const start = src.indexOf('export function onWindowKey(');
  assert.ok(start >= 0, 'onWindowKey missing from appState.svelte.ts');
  const end = src.indexOf('\n}\n', start);
  assert.ok(end > start, 'onWindowKey body not found');
  return src.slice(start, end + 2); // include the closing brace
}

// Strip the TypeScript annotations so the real body can run under node.
function compileHandler() {
  const js = ts.transpileModule(onWindowKeySource().replace(/^export\s+/, ''), {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
  }).outputText;
  // Build a factory whose closure provides every free variable the function reads. closeSlowCall and
  // stepUpOnce are spies: closeSlowCall clears the call (the screen closes), stepUpOnce records a step.
  const factory = new Function(
    'env',
    `
    let importPopup = env.importPopup;
    let drillOpen = env.drillOpen;
    let glossaryOpen = env.glossaryOpen;
    let coverageSource = env.coverageSource;
    let sidePanel = env.sidePanel;
    let connectionsOpen = env.connectionsOpen;
    let slowCall = env.slowCall;
    const document = env.document;
    const HTMLElement = env.HTMLElement;
    const calls = { closeSlowCall: 0, stepUpOnce: 0, closeGlossary: 0, closeCoverage: 0, closeSidePanel: 0, closeConnections: 0 };
    const closeGlossary = () => { calls.closeGlossary++; };
    const closeCoverage = () => { calls.closeCoverage++; };
    const closeSidePanel = () => { calls.closeSidePanel++; };
    const closeConnections = () => { calls.closeConnections++; };
    const closeSlowCall = () => { calls.closeSlowCall++; slowCall = null; };
    const stepUpOnce = () => { calls.stepUpOnce++; };
    ${js}
    return { onWindowKey, calls, getSlowCall: () => slowCall };
  `,
  );
  return factory;
}
const factory = compileHandler();

// A minimal DOM element. `instanceof HTMLElement` is what the guard checks, so targets are instances
// of this same class. closest() answers the two selectors the handler uses (.popover, [hidden]).
class HTMLElement {
  constructor(tagName, opts = {}) {
    this.tagName = tagName;
    this.isContentEditable = !!opts.isContentEditable;
    this._inPopover = !!opts.popover;
    this._hidden = !!opts.hidden;
  }
  closest(selector) {
    if (selector === '.popover') return this._inPopover ? this : null;
    if (selector === '[hidden]') return this._hidden ? this : null;
    return null;
  }
  contains() { return false; }
}

function makeDocument(openMenus = []) {
  return { body: new HTMLElement('BODY'), querySelectorAll: () => openMenus };
}

// An open <details> menu, enough for the handler's menu branch.
function makeMenu() {
  return {
    open: true,
    closest: () => null,
    contains: () => false,
    querySelector: () => ({ focus() {} }),
  };
}

function baseEnv(overrides = {}) {
  return {
    importPopup: false,
    drillOpen: false,
    glossaryOpen: false,
    coverageSource: null,
    sidePanel: null,
    connectionsOpen: false,
    slowCall: { caseId: 'case-1', id: 'call-1' }, // a call is open on the Slow call screen
    document: makeDocument([]),
    HTMLElement,
    ...overrides,
  };
}

const esc = (target) => ({ key: 'Escape', defaultPrevented: false, target });
const run = (env, target) => {
  const h = factory(env);
  h.onWindowKey(esc(target));
  return h;
};

// ---- The fix: an editable target keeps the screen open (the typed values are not lost). ----

test('Esc with an INPUT target does NOT close the Slow call screen', () => {
  const h = run(baseEnv(), new HTMLElement('INPUT'));
  assert.equal(h.calls.closeSlowCall, 0, 'Esc inside an INPUT must not close the call');
  assert.equal(h.calls.stepUpOnce, 0, 'Esc inside an INPUT must not step up either');
  assert.ok(h.getSlowCall(), 'the call stays open so the typed window values are kept');
});

test('Esc with a TEXTAREA target does NOT close the Slow call screen', () => {
  const h = run(baseEnv(), new HTMLElement('TEXTAREA'));
  assert.equal(h.calls.closeSlowCall, 0);
  assert.equal(h.calls.stepUpOnce, 0);
  assert.ok(h.getSlowCall());
});

test('Esc with a contenteditable target does NOT close the Slow call screen', () => {
  const h = run(baseEnv(), new HTMLElement('DIV', { isContentEditable: true }));
  assert.equal(h.calls.closeSlowCall, 0);
  assert.equal(h.calls.stepUpOnce, 0);
  assert.ok(h.getSlowCall());
});

test('the Custom window fields (scall-before / scall-after are INPUTs) keep the screen open', () => {
  // The two number boxes in CallWindowBar are the reported case; both are INPUT elements.
  for (const tagName of ['INPUT', 'INPUT']) {
    const h = run(baseEnv(), new HTMLElement(tagName));
    assert.equal(h.calls.closeSlowCall, 0);
    assert.ok(h.getSlowCall());
  }
});

// ---- Control: a non-editable target still closes the screen. ----

test('Esc with a plain (non-editable) target DOES close the Slow call screen', () => {
  const h = run(baseEnv(), new HTMLElement('DIV'));
  assert.equal(h.calls.closeSlowCall, 1, 'Esc on a plain element closes the call');
  assert.equal(h.getSlowCall(), null, 'the call is cleared');
  assert.equal(h.calls.stepUpOnce, 0, 'closing the call returns before stepUpOnce');
});

test('Esc on the document body (loose focus) still closes the screen', () => {
  const env = baseEnv();
  const h = run(env, env.document.body);
  assert.equal(h.calls.closeSlowCall, 1);
  assert.equal(h.getSlowCall(), null);
});

// ---- The existing exemptions are preserved. ----

test('a SELECT target is still exempt (Esc does not close the screen)', () => {
  const h = run(baseEnv(), new HTMLElement('SELECT'));
  assert.equal(h.calls.closeSlowCall, 0);
  assert.ok(h.getSlowCall());
});

test('a target inside .popover is still exempt (Esc does not close the screen)', () => {
  const h = run(baseEnv(), new HTMLElement('DIV', { popover: true }));
  assert.equal(h.calls.closeSlowCall, 0);
  assert.ok(h.getSlowCall());
});

// ---- The menu -> slowCall -> stepUpOnce order is preserved (behaviourally). ----

test('an open menu closes first; the call is not closed while a menu was open', () => {
  const menu = makeMenu();
  const env = baseEnv({ document: makeDocument([menu]) });
  const h = factory(env);
  h.onWindowKey(esc(env.document.body)); // loose focus, menu open, call open
  assert.equal(menu.open, false, 'the open menu closes on Esc');
  assert.equal(h.calls.closeSlowCall, 0, 'the menu closes before the call, so the call is untouched');
  assert.ok(h.getSlowCall(), 'the call stays open');
});

test('with no call open, Esc on a plain target steps up once', () => {
  const h = run(baseEnv({ slowCall: null }), new HTMLElement('DIV'));
  assert.equal(h.calls.closeSlowCall, 0);
  assert.equal(h.calls.stepUpOnce, 1, 'Esc steps one level up when there is no call to close');
});

test('Esc inside a field never steps up, even with no call open (it belongs to the field)', () => {
  const h = run(baseEnv({ slowCall: null }), new HTMLElement('INPUT'));
  assert.equal(h.calls.stepUpOnce, 0);
  assert.equal(h.calls.closeSlowCall, 0);
});

// ---- The handler still ignores non-Esc and already-handled events. ----

test('a non-Escape key does nothing', () => {
  const h = factory(baseEnv());
  h.onWindowKey({ key: 'Enter', defaultPrevented: false, target: new HTMLElement('DIV') });
  assert.equal(h.calls.closeSlowCall, 0);
  assert.equal(h.calls.stepUpOnce, 0);
  assert.ok(h.getSlowCall());
});

test('an already-defaultPrevented Esc does nothing', () => {
  const h = factory(baseEnv());
  h.onWindowKey({ key: 'Escape', defaultPrevented: true, target: new HTMLElement('DIV') });
  assert.equal(h.calls.closeSlowCall, 0);
  assert.ok(h.getSlowCall());
});