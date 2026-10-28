const assert = require('node:assert/strict');
const test = require('node:test');
const { loadTypeScript, read } = require('./scn1.l4.helpers.cjs');
const depth = loadTypeScript('src/lib/depth.ts');
const { lintCopy } = loadTypeScript('src/lib/copy/copylint.ts');
const { deck } = loadTypeScript('src/lib/copy/deck.ts');
const ids = (context) => depth.evidenceTabs({ operation: false, group: false, scoped: true, phases: false, failed: 0, unfinished: 0, ...context }).map(tab => tab.id);

test('D-A-50: a group without phases still gets the Phases tab', () => {
  assert.deepEqual(ids({ operation: true, group: true, phases: false }), ['timeline', 'phases', 'slowest', 'breakdown']);
  assert.deepEqual(ids({ group: true, phases: true }), ['timeline', 'phases', 'slowest', 'breakdown']);
});

test('D-A-50: no group means no Phases tab', () => {
  assert.ok(!ids({}).includes('phases'));
  assert.ok(!ids({ phases: true }).includes('phases'));
});

test('D-A-50: phasesNote says why for an unknown group and for any other empty group', () => {
  assert.equal(depth.phasesNote('unknown', false), 'No timings for requests of unknown result.');
  assert.equal(depth.phasesNote('success', false), 'No phase timings were recorded for this group.');
  assert.equal(depth.phasesNote('failure', false), 'No phase timings were recorded for this group.');
  assert.equal(depth.phasesNote('unknown', true), '');
  assert.equal(depth.phasesNote('success', true), '');
});

test('D-A-50: the notes pass the copy lint', () => {
  assert.deepEqual(lintCopy([{ id: 'phases-unknown', text: depth.phasesNote('unknown', false) }, { id: 'phases-none', text: depth.phasesNote('success', false) }], deck.terms), []);
});

test('D-A-50: the Phases pane shows the note', () => {
  const src = read('src/components/EvidenceView.svelte');
  assert.match(src, /phasesNote/);
  assert.match(src, /\{#if noPhases\}<p class="sample-note">\{noPhases\}<\/p>\{\/if\}/);
  assert.match(src, /import \{ phaseSegments \} from '\.\.\/lib\/phases';/);
});
