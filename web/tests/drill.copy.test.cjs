const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

// Drill pop-up copy: the deck strings, the lint entries, and the sample output of each function.

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

const { DRILL_TEXT, DRILL_HELP, drillCopyEntries } = loadTypeScript('src/lib/drill/drillCopy.ts');

const HELP_KEYS = [
  'title', 'whereTimeWent', 'partFilters', 'partOther', 'bar', 'notRecorded',
  'pressureTitle', 'stepsTitle', 'filtersTitle', 'hitCount', 'openAnalysis', 'noTimings',
  'otherUnknown', 'factsTitle', 'factEndpoint', 'factStatus', 'factStarted', 'factDuration',
  'laneApi', 'laneIis', 'laneDb', 'atOnce', 'minimap', 'windowControl', 'nearbyCalls', 'allSources'
];
const { lintScreenStrings } = loadTypeScript('src/lib/copy/copylint.ts');
const { deck } = loadTypeScript('src/lib/copy/deck.ts');

test('plain strings match the brief exactly', () => {
  assert.equal(DRILL_TEXT.title, 'Slow call');
  assert.equal(DRILL_TEXT.openAnalysis, 'Open in Analysis');
  assert.equal(DRILL_TEXT.finding, 'Finding the slowest call in this slot...');
  assert.equal(DRILL_TEXT.notRecorded, 'not recorded');
  assert.equal(DRILL_TEXT.noTimings, 'The timings for this call are not available in the saved record.');
  assert.equal(DRILL_HELP.noTimings, 'Open this call in Analysis to see the whole request.');
  assert.equal(DRILL_TEXT.breakdownWindow, 'This window: the call plus or minus 15 minutes');
  assert.equal(DRILL_TEXT.cutFull, 'Cut at 32 KB.');
  assert.equal(DRILL_TEXT.revealNote, 'This text can hold real values. It is not saved and is cleared when this window closes.');
});

test('functions return the brief sample output', () => {
  assert.equal(DRILL_TEXT.hitCount(3), 'Request number 3 on this server since it started');
  assert.equal(DRILL_TEXT.runs(1), '1 run');
  assert.equal(DRILL_TEXT.runs(3), '3 runs');
  assert.equal(DRILL_TEXT.nearbyAround('10:00', '10:30'), 'Calls from 10:00 to 10:30');
  assert.equal(DRILL_TEXT.chipUser(7), 'Only user 7');
  assert.equal(DRILL_TEXT.chipApi('Orders API'), 'Only Orders API');
  assert.equal(DRILL_TEXT.widen(30), 'Widen to 30 minutes');
  assert.equal(DRILL_TEXT.merged(12, 3), '12 calls, 3 more than loaded');
  assert.equal(DRILL_TEXT.mergedNone(12), '12 calls, none more than loaded');
  assert.equal(DRILL_TEXT.truncated(200), 'Showing 200 of more');
});

test('lint entries use drill ids and cover every key', () => {
  const entries = drillCopyEntries();
  const ids = entries.map((e) => e.id);
  assert.equal(new Set(ids).size, ids.length);
  assert.ok(ids.every((id) => id.startsWith('drill.') || id.startsWith('help.')));
  assert.equal(entries.length, Object.keys(DRILL_TEXT).length + HELP_KEYS.length);
  for (const key of HELP_KEYS) assert.ok(ids.includes('help.' + key), 'missing help.' + key);
});

test('the pop-up never blames old rows for missing timings', () => {
  assert.equal('oldRow' in DRILL_TEXT, false);
  assert.equal('oldRow' in DRILL_HELP, false);
  for (const [key, value] of [...Object.entries(DRILL_TEXT), ...Object.entries(DRILL_HELP)]) {
    if (typeof value === 'string') assert.doesNotMatch(value, /saved before|older calls|old row/i, key);
  }
});

test('DRILL_HELP has exactly the brief keys with short non-empty values', () => {
  assert.deepEqual(Object.keys(DRILL_HELP).sort(), [...HELP_KEYS].sort());
  assert.equal(Object.keys(DRILL_HELP).length, 26);
  for (const [key, value] of Object.entries(DRILL_HELP)) {
    assert.equal(typeof value, 'string', key);
    assert.ok(value.length > 0, key + ' is empty');
    assert.ok(value.length < 160, key + ' is ' + value.length + ' characters');
  }
});

test('slow call screen copy', () => {
  assert.equal(DRILL_TEXT.crumbCall('10:14:07'), 'Slow call 10:14:07');
  assert.equal(DRILL_TEXT.windowLine('09:59', '10:29', 15, 15), 'Showing 09:59 to 10:29 (15 min before, 15 min after this call)');
  assert.equal(DRILL_TEXT.laneSlow(1), '1 slow in this window');
  assert.equal(DRILL_TEXT.laneSlow(3), '3 slow in this window');
  assert.equal(
    DRILL_TEXT.mapCaption(5),
    'The two hours around this call, one column per 5 minutes. The outlined box is what the big chart shows. The red line is the slow call.'
  );
  assert.equal(DRILL_TEXT.dbSrcFile('11 Nov 2026'), 'Query Store file imported 11 Nov 2026');
  assert.equal(DRILL_TEXT.tabNearbyCalls(12), 'Nearby calls (exact, API only · 12)');
  assert.equal(DRILL_TEXT.askingWider, 'Asking the server for calls in this wider window...');
});

test('failure and gap lines exist and use no banned wording', () => {
  const banned = /because|due to|caus|waiting on|blocked by|led to|results? in|responsible for|leak|starv|saturat|proven/i;
  const strings = [
    DRILL_TEXT.chipUserNone, DRILL_TEXT.chipApiNone, DRILL_TEXT.slowRowsFailed,
    DRILL_TEXT.overviewFailed, DRILL_TEXT.eventsTruncated, DRILL_TEXT.rangeTo('10:00', '10:30')
  ];
  for (const s of strings) {
    assert.equal(typeof s, 'string');
    assert.ok(s.length > 0);
    assert.doesNotMatch(s, banned, s);
  }
  assert.equal(DRILL_TEXT.chipUserNone, 'Same user: not recorded');
  assert.equal(DRILL_TEXT.chipApiNone, 'Same endpoint: not recorded');
  assert.equal(DRILL_TEXT.rangeTo('10:00', '10:30'), '10:00 to 10:30');
});

test('drill copy passes the copy lint', () => {
  assert.deepEqual(lintScreenStrings(drillCopyEntries(), deck.terms), []);
});
