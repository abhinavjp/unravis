const assert = require('node:assert/strict');
const test = require('node:test');
const { loadTypeScript, read } = require('./scn1.l4.helpers.cjs');
const { ZOOM_NOTES } = loadTypeScript('src/lib/copy/screenCopy.ts');
const { lintCopy } = loadTypeScript('src/lib/copy/copylint.ts');
const { deck } = loadTypeScript('src/lib/copy/deck.ts');
const { zoomCopy } = loadTypeScript('src/lib/copy/zoomCopy.ts');
const strip = read('src/components/ZoomStrip.svelte');
const charts = read('src/components/Charts.svelte');

test('D-A-44: the zoom notes have the exact wording', () => {
  assert.equal(ZOOM_NOTES.tinyDrag, 'That drag was too short to change the window. Drag across more of the strip, or use Zoom in.');
  assert.equal(ZOOM_NOTES.emptyForApi, 'No usable timings for this API in this zoomed window.');
  assert.equal(ZOOM_NOTES.emptyForApiMore, 'Other APIs may have data here. Zoom out, or choose another API above.');
  assert.ok(ZOOM_NOTES.tinyDrag.includes(zoomCopy.zoomIn));
});

test('D-A-44: the zoom notes pass the copy lint', () => {
  assert.deepEqual(lintCopy(Object.entries(ZOOM_NOTES).map(([id, text]) => ({ id, text })), deck.terms), []);
});

test('D-A-44: the strip says so when a drag changes nothing', () => {
  assert.match(strip, /import \{ ZOOM_NOTES \} from '\.\.\/lib\/copy\/screenCopy';/);
  assert.match(strip, /if \(current\.moved && sameWindow\(next, view\)\) dragNote = ZOOM_NOTES\.tinyDrag;\s*else emit\(next\);/);
  assert.match(strip, /\{#if dragNote\}<p class="zoom-message" data-tone="info">\{dragNote\}<\/p>\{\/if\}/);
  assert.match(strip, /\{model\.notice\} \{dragNote\} \{announcement\}/);
});

test('D-A-44: a new press and a changed window clear the note', () => {
  assert.match(strip, /dragNote = '';\s*flush\(\);/);
  assert.match(strip, /\$effect\(\(\) => \{ void view\.start; void view\.end; dragNote = ''; \}\);/);
});

test('D-A-44: the strip keeps the rules zoomStrip.test.cjs pins', () => {
  assert.doesNotMatch(strip, /<details|<summary|bind:open|\bopen\b/);
  const { compile } = require('svelte/compiler');
  assert.deepEqual(compile(strip, { filename: 'ZoomStrip.svelte', generate: 'client' }).warnings.map(x => x.code), []);
});

test('D-A-44: the evidence chart explains an empty zoomed window', () => {
  assert.match(charts, /import \{ ZOOM_NOTES \} from '\.\.\/lib\/copy\/screenCopy';/);
  assert.match(charts, /\{:else if zoomedView && !zoomView\.loading\}<strong>\{ZOOM_NOTES\.emptyForApi\}<\/strong><p>\{ZOOM_NOTES\.emptyForApiMore\}<\/p>\{:else\}<strong>No usable timings in this investigation period<\/strong>/);
  assert.ok(charts.indexOf('{#if zoomView.emptyMessage}') < charts.indexOf('{:else if zoomedView'));
});
