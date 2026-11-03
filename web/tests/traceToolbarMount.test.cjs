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

test('export scrub state is in memory and set by the preview', () => {
  const state = read('src/lib/exportScrub.svelte.ts');
  assert.ok(state.includes('$state<boolean | null>(null)'));
  assert.doesNotMatch(state, /localStorage|sessionStorage|location/);
  const tools = read('src/lib/components/CaseTools.svelte');
  assert.equal(tools.split('exportScrub.set(').length - 1, 1);
  assert.ok(tools.indexOf('exportScrub.set(') > tools.indexOf('previewCaseExport('));
});

test('export flow is untouched', () => {
  const tools = read('src/lib/components/CaseTools.svelte');
  for (const needle of ['previewCaseExport(caseId, mine.signal)', 'canSaveCaseExport', 'confirmCaseExport']) {
    assert.ok(tools.includes(needle), 'CaseTools.svelte must still contain ' + needle);
  }
});
