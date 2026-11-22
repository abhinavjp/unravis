const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const { loadTypeScript } = require('./helpers/loadTs.cjs');

// UI sweep package F5: keyboard, focus and accessibility, and the setup window (findings SETUP-A11Y-01 .. 21).
const root = path.resolve(__dirname, '..');
const read = (file) => fs.readFileSync(path.join(root, file), 'utf8');
const focus = loadTypeScript('src/lib/screenFocus.ts');
const setup = loadTypeScript('setup/logic.ts');

test('SWEEP-F5-03: every working screen names the tab and the case page names itself', () => {
  assert.equal(focus.screenTitle('start'), 'All cases');
  assert.equal(focus.screenTitle('create'), 'New case');
  assert.equal(focus.screenTitle('running', 0), 'Analysis');
  assert.equal(focus.screenTitle('running', 1), 'Where is it?');
  assert.equal(focus.screenTitle('case'), '', 'the case page sets the case title itself');
  assert.equal(focus.screenTitle('loading'), '');
  assert.equal(focus.placeKey('running', 2), 'running:2');
  assert.equal(focus.placeKey('case', 2), 'case', 'depth only counts on the analysis screen');
});

test('SWEEP-F5-03: focus counts as lost on the body, the main area, nothing, or a node that left the page', () => {
  const body = { tagName: 'BODY' }, main = { tagName: 'MAIN' };
  assert.equal(focus.focusWasLost(null, body, main), true);
  assert.equal(focus.focusWasLost(body, body, main), true);
  assert.equal(focus.focusWasLost(main, body, main), true);
  assert.equal(focus.focusWasLost({ tagName: 'HTML' }, body, main), true);
  assert.equal(focus.focusWasLost({ tagName: 'BUTTON', isConnected: false }, body, main), true);
  assert.equal(focus.focusWasLost({ tagName: 'H3', isConnected: true }, body, main), false, 'a screen that placed focus itself is left alone');
});

function fakeDoc({ active = null, headings = [], inert = false } = {}) {
  const calls = [];
  const body = { tagName: 'BODY' };
  const main = {
    tagName: 'MAIN',
    hasAttribute: (name) => name === 'inert' && inert,
    focus: () => { doc.activeElement = main; calls.push('main'); },
    querySelectorAll: () => headings
  };
  const doc = { activeElement: active ?? body, body, getElementById: (id) => (id === 'main-content' ? main : null) };
  doc.calls = calls; doc.main = main;
  return doc;
}
const heading = (name, shown = true, hidden = false) => {
  const h = { tagName: 'H2', name, isConnected: true, tabIndex: -2, getClientRects: () => (shown ? [{}] : []), closest: () => (hidden ? {} : null), hasAttribute: () => false };
  h.focus = () => { h.doc.activeElement = h; h.doc.calls.push(name); };
  return h;
};
const wait = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

test('SWEEP-F5-03: after a move the first visible heading takes focus', async () => {
  const hidden = heading('Zoom', true, true), off = heading('Where', false), shown = heading('Cases');
  const doc = fakeDoc({ headings: [hidden, off, shown] });
  [hidden, off, shown].forEach((h) => { h.doc = doc; });
  focus.settleFocus(doc, 3, 5);
  await wait(20);
  assert.deepEqual(doc.calls, ['Cases']);
  assert.equal(shown.tabIndex, -1, 'the heading can be focused');
});

test('SWEEP-F5-03: a screen that already placed focus is not overridden, and a modal blocks it', async () => {
  const placed = { tagName: 'H3', isConnected: true };
  const doc = fakeDoc({ active: placed, headings: [] });
  focus.settleFocus(doc, 3, 5);
  await wait(20);
  assert.deepEqual(doc.calls, []);
  const modal = fakeDoc({ inert: true });
  focus.settleFocus(modal, 3, 5);
  await wait(20);
  assert.deepEqual(modal.calls, []);
});

test('SWEEP-F5-03: a screen still loading parks on the main area and takes the heading once it is drawn', async () => {
  const late = heading('Case title');
  const doc = fakeDoc({ headings: [] });
  late.doc = doc;
  focus.settleFocus(doc, 6, 10);
  await wait(15);
  assert.deepEqual(doc.calls, ['main']);
  doc.main.querySelectorAll = () => [late];
  await wait(60);
  assert.deepEqual(doc.calls, ['main', 'Case title']);
});

test('SWEEP-F5-07: a name starts with the words that are drawn', () => {
  const picker = loadTypeScript('src/lib/copy/pickerCopy.ts');
  assert.equal(picker.triggerLabel('Incident window', '20 Oct 2026 09:00 → 10:00', 'London time', '1 h'), 'Incident window: 20 Oct 2026 09:00 → 10:00 London time · 1 h');
  const wizard = loadTypeScript('src/lib/copy/wizardCopy.ts');
  assert.equal(wizard.stepButtonLabel(1, 'Name', 'current'), '1 Name, current step');
  assert.match(read('src/components/WhereList.svelte'), /aria-label=\{\(index \+ 1\) \+ ' ' \+ row\.controller/);
  assert.match(read('src/components/UpdateBanner.svelte'), /aria-label=\{`\$\{stage === 'ready' \? 'Later' : 'Not now'\}\. Hide the update bar/);
});

test('SWEEP-F5-16: the Next notice names the fields in words', () => {
  assert.equal(setup.problemNotice([{ field: 'location', message: 'x' }]), 'Fix the folder, then press Next.');
  assert.equal(setup.problemNotice([{ field: 'location', message: 'x' }, { field: 'bb-port', message: 'y' }]), 'Fix the folder and the port, then press Next.');
  assert.equal(setup.problemNotice([{ field: 'location' }, { field: 'bb-name' }, { field: 'bb-port' }, { field: 'bb-port' }]), 'Fix the folder, the Blackbox name and the port, then press Next.');
  assert.doesNotMatch(setup.problemNotice([{ field: 'location' }]), /red/);
});

test('SWEEP-F5-08/09: a greyed footer button says why', () => {
  assert.equal(setup.uninstallWhy({ deleteCases: false, deleteRecordings: false }, 'web-01'), '');
  assert.equal(setup.uninstallWhy({ deleteCases: true, confirmCases: '' }, ''), 'Type DELETE in the box above to go on.');
  assert.equal(setup.uninstallWhy({ deleteCases: true, confirmCases: 'DELETE' }, ''), '');
  assert.equal(setup.uninstallWhy({ deleteRecordings: true, confirmRecordings: 'x' }, 'web-01'), 'Type the Blackbox name in the box above to go on.');
  assert.equal(setup.uninstallWhy({ deleteCases: true, confirmCases: '', deleteRecordings: true, confirmRecordings: '' }, 'web-01'), 'Type both confirmations above to go on.');
  const app = read('setup/App.svelte');
  assert.match(app, /kind=\{deleting \? 'danger' : 'primary'\}/);
  assert.match(app, /aria-describedby=\{finishWhy \? 'finish-why' : undefined\}/);
  assert.match(app, /aria-describedby=\{removeWhy \? 'remove-why' : undefined\}/);
});

test('SWEEP-F5-19: ready checks fold into one line', () => {
  assert.equal(setup.readyLine(6, true), '6 more checks are ready');
  assert.equal(setup.readyLine(1, true), '1 more check is ready');
  assert.equal(setup.readyLine(7, false), 'All 7 checks are ready');
  const list = read('setup/CheckList.svelte');
  assert.match(list, /<details class="ready"/);
  assert.match(list, /rowNote/);
});

test('SWEEP-F5-05/06: the setup window keeps its progress under forced colors and its footer in view when zoomed', () => {
  const app = read('setup/App.svelte');
  assert.match(app, /@media \(forced-colors: active\)[\s\S]*\.bar \{ border: 1px solid CanvasText; \}[\s\S]*\.bar span \{ background: Highlight/);
  const narrow = app.slice(app.indexOf('@media (max-width: 700px)'));
  assert.doesNotMatch(narrow, /\.page \{ height: auto/);
  assert.doesNotMatch(narrow, /\.frame \{ height: auto/);
  assert.doesNotMatch(app, /min-height: 480px/);
  assert.match(read('src/brand/components/StepRail.svelte'), /forced-colors: active[\s\S]*border-left: 3px solid Highlight/);
  assert.match(read('src/brand/components/BrandMark.svelte'), /forced-colors: active[\s\S]*CanvasText/);
});

test('SWEEP-F5-03: focus on an element that was just hidden counts as lost', () => {
  const body = { tagName: 'BODY' }, main = { tagName: 'MAIN' };
  assert.equal(focus.focusWasLost({ tagName: 'H2', isConnected: true, getClientRects: () => [] }, body, main), true);
  assert.equal(focus.focusWasLost({ tagName: 'H2', isConnected: true, getClientRects: () => [{}] }, body, main), false);
});

test('SWEEP-F5-S1: the Close Unravis box shows only under an Unravis open row that is not Ready', () => {
  assert.equal(setup.showsCloseBox({ id: 'app-running', status: 'partly' }), true);
  assert.equal(setup.showsCloseBox({ id: 'app-running', status: 'failed' }), true);
  assert.equal(setup.showsCloseBox({ id: 'app-running', status: 'ready' }), false, 'Unravis is not open: no box');
  assert.equal(setup.showsCloseBox({ id: 'disk-space', status: 'partly' }), false);
});

test('SWEEP-F5-N7: a case that is already made clears the stored create draft instead of saving it', () => {
  const store = new Map();
  global.sessionStorage = { getItem: (k) => (store.has(k) ? store.get(k) : null), setItem: (k, v) => store.set(k, String(v)), removeItem: (k) => store.delete(k) };
  const wiz = loadTypeScript('src/lib/wizard.svelte.ts');
  wiz.wizard.title = 'Checkout slowdown';
  wiz.persistDraft(false);
  assert.equal(store.size, 1, 'a dirty draft is kept for a reload');
  wiz.persistDraft(true);
  assert.equal(store.size, 0, 'once the case exists the draft is dropped');
  wiz.wizard.title = '';
  delete global.sessionStorage;
});
