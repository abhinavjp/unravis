const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

// Slow call screen: the overview strip (minimap) component, checked as source text.

const root = path.resolve(__dirname, '..');
function loadTypeScript(relativePath) {
  const filename = path.join(root, relativePath);
  const source = fs.readFileSync(filename, 'utf8');
  const output = ts.transpileModule(source, {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022, esModuleInterop: true }
  }).outputText;
  const module = { exports: {} };
  const nodeRequire = createRequire(filename);
  const load = (request) => /^\.\.?\//.test(request) && fs.existsSync(path.join(path.dirname(filename), request + '.ts'))
    ? loadTypeScript(path.relative(root, path.join(path.dirname(filename), request + '.ts')))
    : nodeRequire(request);
  new Function('require', 'module', 'exports', output)(load, module, module.exports);
  return module.exports;
}

const { DRILL_TEXT } = loadTypeScript('src/lib/drill/drillCopy.ts');
const source = fs.readFileSync(path.join(root, 'src/components/CallMinimap.svelte'), 'utf8');
const functionBody = (name) => {
  const start = source.indexOf(`function ${name}(`);
  assert.ok(start >= 0, `function ${name} exists`);
  const next = source.indexOf('\n  function ', start + 1);
  const end = next >= 0 ? next : source.indexOf('</script>', start);
  return source.slice(start, end);
};

test('the strip, the box, the caption and the hidden note are all present', () => {
  for (const id of ['scall-map', 'scall-map-box', 'scall-map-caption', 'scall-map-hidden']) {
    assert.ok(source.includes(`data-testid="${id}"`), `has data-testid ${id}`);
  }
  assert.match(source, /<p class="scall-map-hidden" data-testid="scall-map-hidden">\{DRILL_TEXT\.mapHidden\}<\/p>/);
  assert.match(source, /\{#if hidden\}/);
  assert.match(source, /role="slider"/);
  assert.match(source, /tabindex="0"/);
  assert.match(source, /aria-valuemin="0"/);
  assert.match(source, /aria-valuemax="100"/);
  assert.match(source, /aria-valuenow=\{/);
  assert.match(source, /aria-valuetext=\{/);
  assert.match(source, /aria-label=\{DRILL_TEXT\.mapBoxLabel\}/);
});

test('props match the packet', () => {
  for (const prop of ['rows', 'overview', 'win', 'call', 'bucketSeconds', 'zone', 'hidden', 'onPreview', 'onCommit']) {
    assert.match(source, new RegExp(`\\b${prop}\\b`), `prop ${prop}`);
  }
  assert.match(source, /onPreview: \(centreMs: number\) => void/);
  assert.match(source, /onCommit: \(centreMs: number\) => void/);
  assert.match(source, /rows: MapRow\[\]/);
});

test('the geometry comes from callMap, not from the component', () => {
  assert.match(source, /from '\.\.\/lib\/drill\/callMap'/);
  for (const name of ['boxOf', 'fractionOf', 'centreFromFraction', 'columnMinutes', 'KEY_STEP_FRACTION']) {
    assert.ok(source.includes(name), `uses ${name}`);
  }
});

test('dragging previews while moving and commits when let go', () => {
  assert.match(source, /setPointerCapture/);
  assert.match(source, /onpointerdown=\{onBoxDown\}/);
  assert.match(source, /onpointermove=\{onBoxMove\}/);
  assert.match(source, /onpointerup=\{onBoxUp\}/);
  assert.match(source, /onpointercancel=\{onBoxUp\}/);
  assert.match(functionBody('onBoxMove'), /onPreview\(/);
  assert.doesNotMatch(functionBody('onBoxMove'), /onCommit\(/);
  assert.match(functionBody('onBoxUp'), /onCommit\(/);
  assert.doesNotMatch(functionBody('onBoxUp'), /onPreview\(/);
  assert.match(functionBody('onBoxDown'), /setPointerCapture/);
  assert.match(source, /centreFromFraction\(overview,/);
});

test('keys move the box and a click outside it moves the window there', () => {
  const keys = functionBody('onBoxKey');
  assert.match(keys, /'ArrowLeft'/);
  assert.match(keys, /'ArrowRight'/);
  assert.match(keys, /'Home'/);
  assert.match(keys, /KEY_STEP_FRACTION/);
  assert.match(keys, /onCommit\(/);
  assert.match(keys, /call\.startMs \+ call\.endMs/);
  assert.match(source, /onclick=\{onStripClick\}/);
  assert.match(functionBody('onStripClick'), /onCommit\(/);
  assert.match(functionBody('onStripClick'), /boxEl\.contains/);
});

test('rows: label, columns, hatched gaps, titles, peak and the red line', () => {
  assert.match(source, /ROW_H = 20/);
  assert.match(source, /col\.height \* ROW_H/);
  assert.match(source, /<rect class="gap"/);
  assert.match(source, /fill="url\(#\{patternId\}\)"/);
  assert.equal((source.match(/<pattern /g) ?? []).length, 1, 'one shared pattern');
  assert.match(source, /instanceCounter/, 'pattern id is unique per instance');
  assert.match(source, /<title>/);
  assert.match(source, /DRILL_TEXT\.mapCellNone\(/);
  assert.match(source, /DRILL_TEXT\.mapCell\(/);
  assert.match(source, /DRILL_TEXT\.mapPeak\(/);
  assert.match(source, /DRILL_TEXT\.mapNoData/);
  assert.match(source, /DRILL_TEXT\.mapThisCall\(/);
  assert.match(source, /fractionOf\(overview, call\.startMs\)/);
  assert.match(source, /boxOf\(overview, win\)/);
  assert.match(source, /\{DRILL_TEXT\.mapDrag\}/);
  for (const key of ['laneApi', 'laneIis', 'laneDb']) assert.ok(source.includes(`DRILL_TEXT.${key}`), `uses ${key}`);
  for (const lane of ['audit', 'iis', 'sql']) assert.ok(source.includes(`var(--lane-${lane})`), `uses --lane-${lane}`);
});

test('the caption says how many minutes a column is, and the longer help sits in a details block', () => {
  assert.match(source, /mapCaption\(columnMinutes\(/);
  assert.match(source, /<details[^>]*>\s*<summary>\{DRILL_TEXT\.mapHow\}<\/summary>\s*<p>\{DRILL_TEXT\.mapCaptionMore\}<\/p>\s*<\/details>/);
});

test('the strip is at most 84 px tall, never scrolls, and only the box turns off touch scrolling', () => {
  assert.doesNotMatch(source, /overflow(-y|-x)?:\s*(auto|scroll)/);
  assert.match(source, /\.scall-map-strip \{[^}]*max-height: 84px;/);
  assert.equal((source.match(/touch-action:/g) ?? []).length, 1, 'touch-action appears once');
  assert.match(source, /\.scall-map-box \{[^}]*touch-action: none;/);
});

test('every copy key the component uses exists, and it holds no sentence of its own', () => {
  const used = new Set([...source.matchAll(/DRILL_TEXT\.(\w+)/g)].map((m) => m[1]));
  for (const key of used) assert.ok(key in DRILL_TEXT, `DRILL_TEXT.${key} exists`);
  for (const key of ['mapHidden', 'mapHow', 'mapCaption', 'mapCaptionMore', 'mapPeak', 'mapNoData', 'mapCell', 'mapCellNone', 'mapThisCall', 'mapDrag', 'mapBoxLabel']) {
    assert.ok(used.has(key), `component uses ${key}`);
  }
  const markup = source.replace(/<script[\s\S]*?<\/script>/g, '').replace(/<style[\s\S]*?<\/style>/g, '');
  let flat = markup;
  for (let i = 0; i < 4; i += 1) flat = flat.replace(/\{[^{}]*\}/g, ' ');
  const text = flat.replace(/<!--[\s\S]*?-->/g, ' ').replace(/<[^>]*>/g, ' ').replace(/\s+/g, ' ').trim();
  assert.equal(text, '', `leftover text: ${text}`);
});
