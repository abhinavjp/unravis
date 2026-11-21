const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');

const root = path.resolve(__dirname, '..');
const read = (relative) => fs.readFileSync(path.join(root, relative), 'utf8');

function svelteFiles(dir) {
  const out = [];
  for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
    const full = path.join(dir, entry.name);
    if (entry.isDirectory()) out.push(...svelteFiles(full));
    else if (entry.name.endsWith('.svelte')) out.push(full);
  }
  return out;
}

test('toolbar is mounted only by the timeline screen', () => {
  const users = svelteFiles(path.join(root, 'src'))
    .filter((file) => fs.readFileSync(file, 'utf8').includes('TraceToolbar'))
    .map((file) => path.basename(file))
    .sort();
  assert.deepEqual(users.filter((name) => name !== 'TraceToolbar.svelte'), ['TracingPaper.svelte']);
  const tray = read('src/components/CallTrace.svelte');
  assert.ok(!tray.includes('TraceToolbar'));
  assert.ok(!tray.includes('exportScrub'));
});

test('timeline screen wires toggles, scrub state and chart visibility', () => {
  const src = read('src/components/TracingPaper.svelte');
  for (const needle of ['<TraceToolbar', 'toggleLane(', 'exportScrub.forCase(caseId)', '{visible}', 'data-region="toolbar"']) {
    assert.ok(src.includes(needle), 'TracingPaper.svelte must contain ' + needle);
  }
  assert.match(src, /<TracingChart[^>]*\{visible\}/);
  assert.match(src, /data-region="toolbar">[\s\S]*<TraceToolbar/);
});
