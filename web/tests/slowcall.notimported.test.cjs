const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

// P10: a lane whose file is listed but not imported says "not imported", not "no file".

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
const { buildLane, laneGapText } = loadTypeScript('src/lib/drill/callLanes.ts');

const T0 = Date.parse('2026-03-01T10:00:00Z');
const win = { axisStart: '2026-03-01T10:00:00Z', bucketSeconds: 60 };

test('P10: buildLane with not-imported reasonCode produces gap not-imported', () => {
  const lane = buildLane({ key: 'sql', state: 'no-data', reasonCode: 'not-imported', buckets: [], events: [] }, 'sql', win, T0, T0 + 120000);
  assert.equal(lane.gap, 'not-imported');
});

test('P10: laneGapText for not-imported returns the correct copy string', () => {
  const lane = buildLane({ key: 'sql', state: 'no-data', reasonCode: 'not-imported', buckets: [], events: [] }, 'sql', win, T0, T0 + 120000);
  const text = laneGapText(lane.gap);
  assert.equal(text, 'The file for this lane has not been imported. Import it to see it here.');
  assert.equal(text, DRILL_TEXT.laneNotImported);
});

test('P10: the not-imported text contains no digit', () => {
  const lane = buildLane({ key: 'sql', state: 'no-data', reasonCode: 'not-imported', buckets: [], events: [] }, 'sql', win, T0, T0 + 120000);
  const text = laneGapText(lane.gap);
  assert.doesNotMatch(text, /\d/);
});
