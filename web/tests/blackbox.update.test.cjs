const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const { loadTypeScript } = require('./helpers/loadTs.cjs');

// Update the recorder: the panel offers it only when the installed recorder is older, says what it does in
// plain words, calls the one route, and shows the result or the failure without a raw error.

const root = path.resolve(__dirname, '..');
const read = (r) => fs.readFileSync(path.join(root, r), 'utf8').replace(/\r\n/g, '\n');
const copy = loadTypeScript('src/lib/copy/blackboxCopy.ts');
const { BB_TEXT: T } = copy;
const panel = read('src/components/BlackboxPanel.svelte');
const api = read('src/lib/blackbox.ts');

test('the update copy says what it does, that Windows asks once, and that recordings and settings are kept', () => {
  assert.equal(T.updateButton, 'Update the recorder');
  assert.match(T.updateText, /swaps in the new one/);
  assert.match(T.updateText, /one administrator approval/);
  assert.match(T.updateText, /recordings and settings are kept/);
  assert.match(T.updatedRestarted, /recordings and settings were kept/);
  assert.match(T.updatedStopped, /stays stopped until you press Start/);
  assert.match(T.updateCurrent, /Already up to date/);
  assert.match(T.workingUpdate, /Windows may ask for approval/);
});

test('the update copy has no dash and is in the copy lint list', () => {
  const ids = copy.blackboxCopyEntries().map((e) => e.id);
  for (const key of ['updateTitle', 'updateText', 'updateButton', 'workingUpdate', 'updatedRestarted', 'updatedStopped', 'updateCurrent']) {
    assert.ok(ids.includes('blackbox.' + key), key + ' is not linted');
    assert.doesNotMatch(T[key], /[–—]|--/, key);
  }
});

test('the client posts to the update route and the this-pc answer carries the update fields', () => {
  assert.match(api, /updateThisPc: \(\) => call<UpdateAnswer>\('POST', '\/this-pc\/update', \{\}\)/);
  assert.match(api, /updateAvailable\?: boolean/);
  assert.match(api, /recorderVersion\?: string/);
  assert.match(api, /programVersion\?: string/);
});

test('the update row shows only for an installed recorder that is older, and is disabled while another action runs', () => {
  assert.match(panel, /\{#if thisPc\.installed && thisPc\.updateAvailable\}/);
  assert.match(panel, /disabled=\{!!working\} onclick=\{update\}>\{T\.updateButton\}/);
  const block = panel.slice(panel.indexOf('thisPc.updateAvailable'), panel.indexOf('const controls = pcControls'));
  assert.match(block, /T\.updateText/);
  assert.doesNotMatch(block, /\bupdate\(/, 'the row only wires the handler');
});

test('the update action uses act(): working words, the three results, and a failure shown in plain words', () => {
  const m = panel.match(/const update = \(\) => act\(([\s\S]*?)\n  \}\);/);
  assert.ok(m, 'update handler not found');
  assert.match(m[1], /T\.workingUpdate/);
  assert.match(m[1], /blackboxApi\.updateThisPc\(\)/);
  assert.match(m[1], /T\.updateCurrent/);
  assert.match(m[1], /T\.updatedRestarted/);
  assert.match(m[1], /T\.updatedStopped/);
  // act() turns any thrown refusal into the panel's own words and then reloads the list.
  const act = panel.slice(panel.indexOf('async function act('), panel.indexOf('const check ='));
  assert.match(act, /actionError = failed\(error\)/);
  assert.match(act, /await load\(\)/);
});
