const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');

const root = path.resolve(__dirname, '..');
const read = (relative) => fs.readFileSync(path.join(root, relative), 'utf8').replace(/\r\n/g, '\n');
const drawer = read('src/components/Drawer.svelte');

// The five existing drawers pass only the original props, so every new prop must default to the old behaviour.
test('new Drawer props default to the old modal behaviour', () => {
  assert.match(drawer, /modal = true,/);
  assert.match(drawer, /focusTitle = false,/);
  assert.match(drawer, /closeTitle = '',/);
  assert.match(drawer, /stopEscape = true,/);
  assert.match(drawer, /returnFocus = false,/);
  assert.match(drawer, /headerExtra\?: Snippet;/);
  assert.match(drawer, /footer\?: Snippet;/);
});

test('modal drawers keep the scrim, dialog role, Esc and Tab trap; panels have none of them', () => {
  assert.match(drawer, /\{#if modal\}<button type="button" class="drawer-scrim"/);
  assert.match(drawer, /role=\{modal \? 'dialog' : undefined\}/);
  assert.match(drawer, /aria-modal=\{modal \? 'true' : undefined\}/);
  assert.match(drawer, /onkeydown=\{modal \? onKey : undefined\}/);
  assert.match(drawer, /this=\{modal \? 'div' : 'aside'\}/);
  assert.match(drawer, /'connections-drawer '/);
});

test('the template has a header with title, intro and close, an optional footer, and a 24px+ close target', () => {
  assert.match(drawer, /<h2 id=\{titleId\}/);
  assert.match(drawer, /\{#if intro\}<p>\{intro\}<\/p>\{\/if\}/);
  assert.match(drawer, /\{#if footer\}<div class="drawer-footer"/);
  const width = /\.drawer-close \{[^}]*width: (\d+)px; height: (\d+)px/.exec(drawer);
  assert.ok(width && Number(width[1]) >= 24 && Number(width[2]) >= 24);
});

test('drawer template uses chevron disclosures and plain finding rows, not markers, cards or pills', () => {
  assert.match(drawer, /details > summary::-webkit-details-marker\) \{ display: none/);
  assert.match(drawer, /details > summary::after\)/);
  assert.match(drawer, /\.finding\) \{[^}]*border: 0; border-top: 1px solid var\(--hairline\)/);
  assert.match(drawer, /\.low-n\) \{[^}]*border: 0/);
});

test('every existing caller still passes the original required props', () => {
  for (const file of ['Glossary', 'PatternsPanel', 'LedgerPanel', 'ExportPanel', 'ConnectionsDrawer']) {
    const source = read('src/components/' + file + '.svelte');
    const tag = /<Drawer\b[^>]*>/.exec(source);
    assert.ok(tag, file + ' uses Drawer');
    for (const prop of ['title=', 'titleId=', 'closeLabel=']) assert.ok(tag[0].includes(prop), file + ' passes ' + prop);
  }
});

// After a fully successful add the connections drawer closes; a partial failure keeps it open for retry.
test('addSource closes the connections drawer only when every database was added', () => {
  const state = read('src/lib/appState.svelte.ts');
  const body = state.slice(state.indexOf('export async function addSource'), state.indexOf('export async function deleteSource'));
  assert.match(body, /allAdded = true;/);
  assert.match(body, /if \(allAdded\) await closeConnections\(\);/);
  assert.ok(body.indexOf('allAdded = true;') > body.indexOf('if (failures.length)'), 'only the no-failure branch sets allAdded');
  assert.match(body, /sourceError = `Added \$\{addedCount\} of/);
});
