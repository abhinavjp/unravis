const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

// Gate 4 fixes (5 Nov 2026): the Suspects rail shows a hidden lane, one IIS guide in the wizard, the stale zip copy, the wizard lists a file
// once, a double click removes one file, and a second Escape answers the drawer's leave question.

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
const fileKindsCopy = loadTypeScript('src/lib/copy/fileKindsCopy.ts');
const { lintScreenStrings } = loadTypeScript('src/lib/copy/copylint.ts');

test('gate4 8: Show on chart passes the suspect lane and the timeline switches that lane on', () => {
  const rail = read('src/components/SuspectsRail.svelte');
  assert.match(rail, /onShow: \(slot: number, lane\?: TracingLaneKey\) => void/);
  assert.match(rail, /\{@const lane = suspect\.momentLane \?\? undefined\}/);
  assert.match(rail, /onShow\(slot, lane\)/);
  const paper = read('src/components/TracingPaper.svelte');
  assert.match(paper, /const showOnChart = \(slot: number, lane\?: TracingLaneKey\) =>/);
  assert.match(paper, /if \(lane && !visible\[lane\]\) visible = \{ \.\.\.visible, \[lane\]: true \};/);
});

test('gate4 10: the wizard shows one IIS guide (the card guide) and the drawer keeps the detailed one', () => {
  const panel = read('src/components/wizard/ImportFilesPanel.svelte');
  assert.equal(panel.match(/<IisGuide/g).length, 1, 'one IisGuide, in the drawer');
  assert.match(panel, /\{#if drawer\}\s*<p class="hint supported">\{BATCH_TEXT\.supported\}<\/p>\s*<IisGuide \/>/);
  assert.match(panel, /\{#if !drawer\}<FileGuides \/>\{\/if\}/);
  assert.ok(fileKindsCopy.CARD_GUIDES.some((guide) => guide.id === 'iis'));
});

test('gate4 11: the supported line no longer says to extract a zip first', () => {
  const text = fileKindsCopy.supportedText();
  assert.doesNotMatch(text, /Extract compressed files first/);
  assert.match(text, /\.zip of them is opened for you/);
  assert.match(text, /compressed/i);
  assert.deepEqual(lintScreenStrings([{ id: 'files.sample.supported', text }]), []);
});

test('gate4 G4-01: a file chosen twice in the wizard is listed once and the person is told quietly', () => {
  assert.equal(fileKindsCopy.alreadyListedText(1), 'That file is already in the list, so it was not added again.');
  assert.equal(fileKindsCopy.alreadyListedText(3), '3 of those files are already in the list, so they were not added again.');
  assert.match(fileKindsCopy.alreadyListedText(2), /already in the list/i);
  const panel = read('src/components/wizard/ImportFilesPanel.svelte');
  assert.match(panel, /a\.lastModified === b\.lastModified/);
  assert.match(panel, /const fresh = drawer \? opened\.files : newFiles\(opened\.files\);/);
  assert.match(panel, /noteBad = says\.length > 0;/, 'the repeat line is not an error');
});

test('gate4 G4-02: only the first click of a double click removes a row', () => {
  const panel = read('src/components/wizard/ImportFilesPanel.svelte');
  assert.match(panel, /function onRemoveClick\(event: MouseEvent, id: number\) \{\s*if \(event\.detail > 1\) return;\s*void removeRow\(id\);/);
  assert.match(panel, /onclick=\{\(event\) => onRemoveClick\(event, item\.id\)\}/);
});

test('gate4 G4-03: Escape while the leave question shows answers it as Keep editing', () => {
  const panel = read('src/lib/components/ImportPanel.svelte');
  assert.match(panel, /const requestClose = \(\) => \{ if \(leaveAsk\) keepEditing\(\); else leaveThen\(onClose\); \};/);
});

test('gate4 plausible: the bar-size line says bars are grouped whenever a bar holds more than a minute, as the axis caption does', () => {
  const lanes = loadTypeScript('src/lib/tracing/lanes.ts');
  const start = Date.parse('2001-03-04T10:00:00Z');
  const window = (hours, bucketSeconds) => ({ start: new Date(start).toISOString(), end: new Date(start + hours * 3600e3).toISOString(), axisStart: new Date(start).toISOString(), axisEnd: new Date(start + hours * 3600e3).toISOString(), bucketSeconds });
  const sixHours = lanes.barSizeParts(lanes.slotGrid(window(6, 300)), 300);
  assert.match(sixHours.rest, /longer periods group more time into each bar/);
  assert.match(lanes.axisCaption('UTC', 300, false), /Longer periods group more time/);
  const oneHour = lanes.barSizeParts(lanes.slotGrid(window(1, 60)), 60);
  assert.doesNotMatch(oneHour.rest, /longer periods/);
});
