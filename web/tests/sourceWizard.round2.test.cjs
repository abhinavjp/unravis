const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const read = (p) => fs.readFileSync(path.join(__dirname, '..', p), 'utf8');

test('a drop on the tile screen is read and staged, not discarded', () => {
  const step = read('src/components/sourcewiz/SourceStep.svelte');
  assert.match(step, /filesFromDrop\(items, plain\)/);
  assert.match(step, /if \(closed\) return;/);
  assert.match(step, /<ImportFilesPanel \{seed\}/);
  assert.match(step, /await open\('files', '', \{ files: dropped\.files/, 'the seed is set before the panel mounts, or its mount-time staging sees nothing');
  assert.match(read('src/components/wizard/ImportFilesPanel.svelte'), /onMount\(\(\) => \{ if \(seed && seed\.files\.length > 0\) void take\(seed\.files, seed\.folder, seed\.says\)/);
});

test('add mode never rewrites the case environment list', () => {
  const sub = read('src/lib/wizardSubmit.ts');
  assert.match(sub, /input\.envs\.length > 0 && !input\.appendOnly/);
  assert.match(sub, /input\.envs\.length === 0 \|\| input\.appendOnly/);
});

test('review text follows spec 9.4 and the footer is accurate for Blackbox and Another day', () => {
  const copy = read('src/lib/copy/sourceWizardCopy.ts');
  assert.match(copy, /stays as it is/);
  assert.match(copy, /Out of date, rerun/);
  assert.match(copy, /footerAdd:.*Another day imports as soon as you start it/);
  const flow = read('src/components/sourcewiz/AddSourcesFlow.svelte');
  assert.match(flow, /WIZ\.reviewStays/);
  assert.match(flow, /WIZ\.reviewStale/);
  assert.match(flow, /<CreateProgress adding/);
});

test('blackbox card drops a stale check and reports a refused list add', () => {
  const bb = read('src/components/sourcewiz/BlackboxCard.svelte');
  assert.match(bb, /const asked = location;/);
  assert.match(bb, /if \(location === asked\) verdict/);
  assert.match(bb, /WIZ\.blackbox\.notListed/);
});
