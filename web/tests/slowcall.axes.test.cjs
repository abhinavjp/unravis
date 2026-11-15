const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

// Slow call screen: the chart axes are labelled, a "?" explains how to read them, and the points are joined.

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
const lanes = fs.readFileSync(path.join(root, 'src/components/CallLanes.svelte'), 'utf8');
const minimap = fs.readFileSync(path.join(root, 'src/components/CallMinimap.svelte'), 'utf8');

test('the copy says what across and up mean, in plain words', () => {
  assert.match(DRILL_TEXT.chartReadLine, /left to right/i);
  assert.match(DRILL_TEXT.chartReadLine, /time/i);
  assert.match(DRILL_TEXT.chartReadLine, /up/i);
  assert.match(DRILL_TEXT.chartReadLine, /running at once/i);
  assert.match(DRILL_TEXT.yPeak('4.2'), /4\.2/);
  assert.equal(DRILL_TEXT.yZero, '0');
  assert.match(DRILL_TEXT.xTitle('EDT'), /EDT/);
  assert.match(DRILL_TEXT.laneShape('4.2'), /4\.2/);
  assert.match(DRILL_TEXT.laneShape('4.2'), /running at once|at once/);
  for (const key of ['helpButton', 'helpAcross', 'helpUp', 'helpDots', 'legendWidth', 'legendHatched', 'legendCall', 'mapAxisLine']) {
    assert.equal(typeof DRILL_TEXT[key], 'string', key);
    assert.ok(DRILL_TEXT[key].length > 5, key);
  }
});

test('each lane shows its y scale: a dashed peak guide, the peak number and a zero', () => {
  assert.match(lanes, /class="peakline"/);
  assert.match(lanes, /stroke-dasharray/);
  assert.match(lanes, /class="y-peak"/);
  assert.match(lanes, /class="y-zero"/);
  assert.match(lanes, /DRILL_TEXT\.yPeak\(/);
  assert.match(lanes, /DRILL_TEXT\.yZero/);
  assert.match(lanes, /SHAPE_H/);
});

test('the x axis has a title that names the time zone', () => {
  assert.match(lanes, /class="scall-xtitle"/);
  assert.match(lanes, /DRILL_TEXT\.xTitle\(zoneName\)/);
});

test('one line under the title always says how to read the chart', () => {
  assert.match(lanes, /class="scall-readline"/);
  assert.match(lanes, /DRILL_TEXT\.chartReadLine/);
});

test('a visible "?" button opens the explainer and says so for assistive technology', () => {
  assert.match(lanes, /<button[^>]*class="scall-help"/);
  assert.match(lanes, /aria-expanded=\{helpOpen\}/);
  assert.match(lanes, /aria-controls="scall-help-\{patternId\}"/);
  assert.match(lanes, /id="scall-help-\{patternId\}"/);
  assert.match(lanes, /hidden=\{!helpOpen\}/);
  assert.match(lanes, /type="button"/);
  assert.match(lanes, /DRILL_TEXT\.helpButton/);
  for (const key of ['helpAcross', 'helpUp', 'helpDots', 'legendWidth', 'legendHatched', 'legendCall']) {
    assert.match(lanes, new RegExp(`DRILL_TEXT\\.${key}`), key);
  }
});

test('Escape inside the explainer closes it and returns focus to the button', () => {
  assert.match(lanes, /e\.key === 'Escape'/);
  assert.match(lanes, /helpBtn\?\.focus\(\)/);
});

test('the points are joined: a line through each time slot, with a dot on every point', () => {
  assert.match(lanes, /class="shape"/);
  assert.match(lanes, /class="dot"/);
  assert.match(lanes, /<ellipse/);
  assert.match(lanes, /function shapePoints\(/);
  // joined by straight segments (L), no step segments that go flat then straight up
  const fn = lanes.slice(lanes.indexOf('function shapePath('), lanes.indexOf('const drawnBars'));
  assert.doesNotMatch(fn, /L\$\{n2\(x\(cell\.end\)\)\} \$\{y\}/);
});

test('the minimap states what a column means and that rows have their own scale', () => {
  assert.match(minimap, /class="scall-map-axis"/);
  assert.match(minimap, /DRILL_TEXT\.mapAxisLine/);
  assert.match(DRILL_TEXT.mapAxisLine, /time/i);
  assert.match(DRILL_TEXT.mapAxisLine, /taller|higher/i);
  assert.match(DRILL_TEXT.mapAxisLine, /own/i);
});

test('the new copy stays short and plain', () => {
  const banned = /because|due to|caus|waiting on|blocked by|led to|results? in|responsible for|leak|starv|saturat|proven/i;
  for (const key of ['chartReadLine', 'helpButton', 'helpAcross', 'helpUp', 'helpDots', 'mapAxisLine']) {
    assert.ok(DRILL_TEXT[key].length < 240, key);
    assert.doesNotMatch(DRILL_TEXT[key], banned, key);
  }
});

// Layout stability: the case column must not shrink-wrap to its content, or hovering the lanes moves the whole page sideways.
test('the case column has a fixed width, so hover text cannot change its width', () => {
  const home = fs.readFileSync(path.resolve(__dirname, '..', 'src/lib/components/CaseHome.svelte'), 'utf8');
  assert.match(home, /\.case-home \{[^}]*width: 100%;[^}]*max-width: 880px/);
});
