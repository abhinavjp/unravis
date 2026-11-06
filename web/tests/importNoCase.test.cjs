// IP-17: the header's Import with no case open must not do nothing. Source checks (node --test, no DOM).
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');

const root = path.resolve(__dirname, '..');
const read = (relative) => fs.readFileSync(path.join(root, relative), 'utf8');

test('IP-17: Import with no case open leads to the start screen and sets the hint, before any popup work', () => {
  const state = read('src/lib/appState.svelte.ts');
  assert.match(state, /export const IMPORT_CASE_HINT = 'Pick a case first, then choose Import files\.';/);
  const fn = state.slice(state.indexOf('function showImportPopup('));
  const body = fn.slice(0, fn.indexOf('\n}'));
  assert.match(body, /if \(!id\) \{[^}]*go\('start'\)[^}]*importCaseHint = true;[^}]*return;\s*\}/);
  assert.ok(body.indexOf("go('start')") < body.indexOf('requestCase(id)'), 'the no-case branch comes before the popup opens');
});

test('IP-17: opening a case clears the hint', () => {
  const state = read('src/lib/appState.svelte.ts');
  const fn = state.slice(state.indexOf('export function setActiveCase('));
  const body = fn.slice(0, fn.indexOf('\n}'));
  assert.match(body, /if \(id\) importCaseHint = false;/);
});

test('IP-17: the start screen shows the hint as a status line with a dismiss', () => {
  const landing = read('src/components/Landing.svelte');
  assert.match(landing, /IMPORT_CASE_HINT/);
  assert.match(landing, /\{#if app\.importCaseHint\}<p class="import-hint" role="status">/);
  assert.match(landing, /app\.importCaseHint = false;\s*\}\}>Dismiss<\/button>/);
});
