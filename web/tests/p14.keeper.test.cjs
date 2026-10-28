const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

// The key keeper on the connections page (P14): which state shows, a stopped keeper is not the demo, the move card holds the one main
// button, and no password ever reaches this page.
const root = path.resolve(__dirname, '..');
const cache = new Map();
function loadTypeScript(relativePath) {
  const filename = path.join(root, relativePath);
  if (cache.has(filename)) return cache.get(filename).exports;
  const source = fs.readFileSync(filename, 'utf8');
  const output = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022, esModuleInterop: true } }).outputText;
  const module = { exports: {} };
  cache.set(filename, module);
  const nodeRequire = createRequire(filename);
  const load = (request) => {
    if (/^\.\.?\//.test(request)) {
      const plain = path.join(path.dirname(filename), request + '.ts');
      if (fs.existsSync(plain)) return loadTypeScript(path.relative(root, plain));
    }
    return nodeRequire(request);
  };
  new Function('require', 'module', 'exports', output)(load, module, module.exports);
  return module.exports;
}
const read = (relative) => fs.readFileSync(path.join(root, relative), 'utf8');
const keeper = loadTypeScript('src/lib/keeper.ts');
const copy = loadTypeScript('src/lib/copy/sourcesCopy.ts');
const { ApiError } = loadTypeScript('src/lib/api.ts');
const { lintScreenStrings } = loadTypeScript('src/lib/copy/copylint.ts');

const none = { sql: 0, mongo: 0, blackbox: 0 };
const status = (over = {}) => ({ mode: 'shared', running: true, pending: none, oldFilesPresent: false, ...over });

test('keeperView: per-user PCs show nothing, the shared layout shows its state', () => {
  assert.equal(keeper.keeperView(null), 'none');
  assert.equal(keeper.keeperView({ mode: 'user', running: false, pending: none, oldFilesPresent: false }), 'none');
  assert.equal(keeper.keeperView(status()), 'ready');
  assert.equal(keeper.keeperView(status({ running: false, problem: 'stopped' })), 'stopped');
  assert.equal(keeper.keeperView(status({ running: false })), 'stopped', 'not running with no reason reads as stopped');
  assert.equal(keeper.keeperView(status({ running: false, problem: 'impostor' })), 'impostor');
  assert.equal(keeper.keeperView(status({ pending: { sql: 1, mongo: 0, blackbox: 0 }, oldFilesPresent: true })), 'migrate');
  assert.equal(keeper.keeperView(status({ oldFilesPresent: true })), 'moved');
  assert.equal(
    keeper.keeperView(status({ running: false, problem: 'stopped', pending: { sql: 2, mongo: 0, blackbox: 0 }, oldFilesPresent: true })),
    'stopped', 'a stopped keeper comes before the move: nothing can be moved into it'
  );
});

test('keeperPreviewFrom: only the named previews, anywhere in the address', () => {
  assert.equal(keeper.keeperPreviewFrom('#keeper=stopped'), 'stopped');
  assert.equal(keeper.keeperPreviewFrom('#t=abc&keeper=migrate'), 'migrate');
  assert.equal(keeper.keeperPreviewFrom('#keeper=saved'), 'saved');
  assert.equal(keeper.keeperPreviewFrom('#keeper=moved'), 'moved');
  assert.equal(keeper.keeperPreviewFrom('#keeper=password'), '');
  assert.equal(keeper.keeperPreviewFrom('#case=keeper'), '');
  assert.equal(keeper.keeperPreviewFrom(''), '');
  assert.equal(keeper.keeperPreview(), '', 'no address in node');
});

test('isKeeperDown: only the keeper codes, never a plain 503', () => {
  assert.equal(keeper.isKeeperDown(new ApiError('x', 'keeper-stopped', undefined, 503)), true);
  assert.equal(keeper.isKeeperDown(new ApiError('x', 'keeper-impostor', undefined, 503)), true);
  assert.equal(keeper.isKeeperDown(new ApiError('x', undefined, undefined, 503)), false, 'the demo answers a plain 503');
  assert.equal(keeper.isKeeperDown(new ApiError('x', 'keeper-busy', undefined, 409)), false);
  assert.equal(keeper.isKeeperDown(null), false);
});

test('keeperPendingText counts each kind in plain words', () => {
  assert.equal(copy.keeperPendingText(2, 1, 1), '2 SQL Server connections, 1 MongoDB link and 1 Blackbox read key wait to be moved.');
  assert.equal(copy.keeperPendingText(1, 0, 0), '1 SQL Server connection waits to be moved.');
  assert.equal(copy.keeperPendingText(0, 3, 0), '3 MongoDB links wait to be moved.');
  assert.equal(copy.keeperPendingText(0, 0, 0), 'Nothing waits to be moved.');
});

test('keeper words pass the copy lint and use no status word of their own', () => {
  const entries = copy.sourcesCopyEntries().filter((e) => e.id.startsWith('sources.keeper.'));
  assert.ok(entries.length > 10);
  assert.deepEqual(lintScreenStrings(entries, []), []);
  for (const word of ['Works', 'Failed', 'Ready', 'Not tested']) {
    assert.ok(!Object.values(copy.KEEPER_TEXT).some((t) => t === word), word + ' stays a test or readiness word');
  }
});

test('the connections page: a keeper 503 is not the demo, the move card holds the main button, the held sticker shows', () => {
  const page = read('src/components/sources/ConnectionsPage.svelte');
  assert.match(page, /demo = refused\(a\) && refused\(b\) && !keeperDown/);
  assert.match(page, /loadFailed = [^\n]*!keeperDown/);
  assert.match(page, /const primary = \$derived\(promoted && !offerMove\)/, 'the add button gives way to the move card');
  assert.match(page, /<KeeperCard primary=\{promoted\}/, 'the move card is the main button only where the page is promoted');
  assert.match(page, /KEEPER_TEXT\.held/);
  assert.match(page, /shared \? KEEPER_TEXT\.origin : CONN_TEXT\.origin/);
  assert.match(page, /api\.keeperStatus\(keeperPreview\(\)\)/);
  const card = read('src/components/sources/KeeperCard.svelte');
  assert.equal((card.match(/btn-primary/g) || []).length, 1, 'one main button, on the move card');
  assert.equal((card.match(/class="btn-primary"/g) || []).length, 0, 'outlined unless promoted (case home has its own main button)');
  assert.match(card, /role="alert" data-keeper=\{view\}/, 'a stopped keeper is an alert');
  assert.match(card, /removeAsk/, 'removing the old copies asks once');
});

test('the keeper card keeps the keyboard in place and offers a way on after a refusal', () => {
  const card = read('src/components/sources/KeeperCard.svelte');
  assert.match(card, /await tick\(\)/, 'focus moves only after the new content is drawn');
  assert.equal((card.match(/data-keeper-head/g) || []).length >= 4, true, 'every card heading can take the focus');
  assert.match(card, /tabindex="-1" data-keeper-head/);
  assert.match(card, /\[data-keep\]/, 'opening the remove question focuses "Keep them", the safe answer');
  assert.match(card, /role="alertdialog"[^>]*onkeydown=\{escape\}/, 'Escape closes the remove question');
  assert.match(card, /event\.key === 'Escape'/);
  assert.match(card, /code === 'not-verified' \|\| code === 'duplicates-unconfirmed'/, 'a refused removal fetches the counts again');
  assert.match(card, /KEEPER_TEXT\.moveAgain/, 'the moved card offers the move again after a refusal');
  assert.match(card, /keeperRemoveOld\(duplicates > 0\)/, 'copies someone else saved first are confirmed only when shown');
  assert.match(card, /keeperDuplicatesText\(duplicates\)/);
  const page = read('src/components/sources/ConnectionsPage.svelte');
  assert.match(page, /keeperUnusable \? KEEPER_TEXT\.heldDown : KEEPER_TEXT\.held/, 'a stopped keeper does not promise a usable password');
  assert.match(page, /KEEPER_TEXT\.notNowUndo/, '"Not now" can be taken back');
  assert.match(page, /keeper = next; await load\(false\)/, 'a move or removal keeps the state it answered');
});

test('keeperDuplicatesText says whose copy was kept', () => {
  assert.match(copy.keeperDuplicatesText(1), /^1 of your old saved connections was already saved/);
  assert.match(copy.keeperDuplicatesText(3), /^3 of your old saved connections were already saved/);
  assert.match(copy.keeperDuplicatesText(2), /may not be yours/);
});

test('no password field or value reaches the keeper side of the page', () => {
  const api = read('src/lib/api.ts');
  const block = api.split('\n').filter((l) => /keeper/i.test(l)).join('\n');
  assert.doesNotMatch(block, /password/i);
  for (const file of ['src/lib/keeper.ts', 'src/components/sources/KeeperCard.svelte']) {
    const text = read(file);
    assert.doesNotMatch(text, /type="password"|\.password\b|password:/, file + ' never names a password field');
  }
});

test('old per-user cases are said on the start screen, never opened or moved', () => {
  const start = loadTypeScript('src/lib/copy/startCopy.ts');
  assert.match(start.oldCasesText(1), /^1 case from before/);
  assert.match(start.oldCasesText(4), /^4 cases from before .* are still in your own Windows folder/);
  assert.match(start.oldCasesText(4), /has not deleted them/);
  const issues = lintScreenStrings(start.startLintEntries().filter((e) => e.id.startsWith('start.sample.old')), []);
  assert.deepEqual(issues, []);
  const archive = read('src/lib/archiveApi.ts');
  assert.match(archive, /oldCases: num\(body\.oldCases, 0\)/);
  const screen = read('src/components/StartScreen.svelte');
  assert.match(screen, /oldCases = page\.oldCases/);
  assert.match(screen, /\{#if phase === 'ready' && oldCases > 0\}<p class="card-note" role="note" data-old-cases>\{oldCasesText\(oldCases\)\}<\/p>\{\/if\}/);
});

test('the keeper card re-reads the page when the preview in the address changes (D-A-35)', () => {
  const card = read('src/components/sources/KeeperCard.svelte');
  assert.match(card, /import \{ onMount, tick \} from 'svelte';/);
  assert.match(card, /import \{ (?:holdBackground, )?keeperView, onKeeperPreview, (?:trapTarget, )?type KeeperStatus \} from '\.\.\/\.\.\/lib\/keeper';/);
  assert.match(card, /async function refreshFromPreview\(\) \{\s*if \(busy !== ''\) return;\s*try \{ await onRetry\(\); \} catch \{[^}]*\}\s*\}/);
  assert.match(card, /onMount\(\(\) => onKeeperPreview\(\(\) => void refreshFromPreview\(\)\)\);/, 'the unsubscribe function is returned to onMount');
  assert.doesNotMatch(card, /type="password"|\.password\b|password:/);
  const keeperTs = read('src/lib/keeper.ts');
  assert.match(keeperTs, /export function onKeeperPreview\(listener: \(\) => void\): \(\) => void \{/);
});

test('trapTarget keeps Tab inside a small dialog and wraps at both ends (D-C-04)', () => {
  assert.equal(keeper.trapTarget(0, -1, false), -1, 'no controls: nowhere to go');
  assert.equal(keeper.trapTarget(0, 0, true), -1);
  assert.equal(keeper.trapTarget(3, -1, false), 0, 'from outside, Tab enters at the first');
  assert.equal(keeper.trapTarget(3, -1, true), 2, 'from outside, Shift+Tab enters at the last');
  assert.equal(keeper.trapTarget(3, 0, false), 1);
  assert.equal(keeper.trapTarget(3, 2, false), 0, 'Tab on the last wraps to the first');
  assert.equal(keeper.trapTarget(3, 0, true), 2, 'Shift+Tab on the first wraps to the last');
  assert.equal(keeper.trapTarget(3, 1, true), 0);
  assert.equal(keeper.trapTarget(1, 0, false), 0);
  assert.equal(keeper.trapTarget(1, 0, true), 0);
});

test('holdBackground makes everything outside the dialog inert and gives it all back (D-C-04)', () => {
  const make = (name, children = []) => {
    const node = { name, inert: false, children, parentElement: null };
    for (const child of children) child.parentElement = node;
    return node;
  };
  const dialog = make('dialog');
  const note = make('note');
  const card = make('card', [make('head'), dialog, note]);
  const aside = make('aside');
  const main = make('main', [card, aside]);
  const nav = make('nav');
  const body = make('body', [nav, main]);
  dialog.ownerDocument = { body };
  const already = make('already');
  already.inert = true;
  card.children.push(already); already.parentElement = card;
  const release = keeper.holdBackground(dialog);
  for (const outside of [card.children[0], note, aside, nav]) assert.equal(outside.inert, true, outside.name + ' is held');
  for (const inside of [dialog, card, main, body]) assert.equal(inside.inert, false, inside.name + ' stays usable');
  release();
  for (const node of [card.children[0], note, aside, nav, dialog, card, main, body]) assert.equal(node.inert, false, node.name + ' is free again');
  assert.equal(already.inert, true, 'something that was inert before stays inert');
});

test('the remove question is a modal that returns focus to "Remove old copies" (D-C-04)', () => {
  const card = read('src/components/sources/KeeperCard.svelte');
  assert.match(card, /<div role="alertdialog" aria-modal="true" aria-label=\{KEEPER_TEXT\.removeAsk\} tabindex="-1" onkeydown=\{escape\} bind:this=\{dialogEl\}>/);
  assert.match(card, /role="alertdialog"[^>]*onkeydown=\{escape\}/, 'the older pin still matches');
  assert.match(card, /<button type="button" class="btn" data-remove-old onclick=\{\(\) => void ask\(true\)\}>\{KEEPER_TEXT\.removeOld\}<\/button>/);
  assert.match(card, /if \(open\) problem = ''; else back = true;/, 'every close of the question asks for the focus to go back');
  assert.match(card, /\} finally \{ busy = ''; asking = false; back = kind === 'remove'; \}/);
  assert.match(card, /const first = asking \? '\[data-keep\]' : returning \? '\[data-remove-old\]' : '\[data-keeper-head\]';/);
  assert.match(card, /\?\? box\.querySelector<HTMLElement>\('\[data-keeper-head\]'\) \?\? box\.querySelector<HTMLElement>\('\[data-keeper-done\]'\)/);
  assert.match(card, /if \(event\.key === 'Escape' && busy === ''\) \{ event\.preventDefault\(\); void ask\(false\); return; \}/);
  assert.match(card, /if \(event\.key !== 'Tab'\) return;/);
  assert.match(card, /trapTarget\(buttons\.length, buttons\.indexOf\(document\.activeElement as HTMLButtonElement\), event\.shiftKey\)/);
  assert.match(card, /if \(next >= 0\) buttons\[next\]\.focus\(\); else dialog\.focus\(\);/);
  assert.match(card, /\$effect\(\(\) => \{\s*const dialog = dialogEl;\s*if \(!dialog\) return;\s*return holdBackground\(dialog\);\s*\}\);/, 'the cleanup of the effect releases the page');
  assert.equal((card.match(/btn-primary/g) || []).length, 1, 'still one main button');
  assert.doesNotMatch(card, /type="password"|\.password\b|password:|console\./);
});
