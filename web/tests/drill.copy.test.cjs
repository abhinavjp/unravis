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
  assert.equal(DRILL_TEXT.title, 'Call detail');
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
  assert.equal(DRILL_TEXT.crumbCall('10:14:07'), 'Call detail 10:14:07');
  assert.equal(DRILL_TEXT.windowLine('09:59', '10:29', 15, 15), 'Showing 09:59 to 10:29 (15 min before, 15 min after this call)');
  assert.equal(DRILL_TEXT.laneSlow(1), '1 slow in this window');
  assert.equal(DRILL_TEXT.laneSlow(3), '3 slow in this window');
  assert.equal(
    DRILL_TEXT.mapCaption(5),
    'The two hours around this call, one column per 5 minutes. The outlined box is what the big chart shows. The red line is this call.'
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

test('UI-13: Query Store is explained in plain words where a first-time reader meets it', () => {
  assert.match(DRILL_HELP.laneDb, /Query Store is the database's own record of slow queries/);
  assert.match(DRILL_TEXT.dbMsgLive(15), /Query Store, the database's own record of slow queries\. The database owner must switch it on\./);
  assert.match(DRILL_TEXT.dbMsgOff, /needs the database owner/);
  assert.ok(DRILL_HELP.laneDb.length < 160);
});

// ---- the Blackbox measure/unit copy and the no-invented-date rule (W13-01, W13-03, W13-04) ----

test('the blackbox source copy names machine-wide batch throughput, with a sent cadence or an unknown one', () => {
  assert.equal(DRILL_TEXT.dbSrcBlackbox('20 Oct 2026', 15), 'Blackbox machine-wide batches per second, read every 15 s since 20 Oct 2026');
  assert.equal(DRILL_TEXT.dbSrcBlackbox('20 Oct 2026'), 'Blackbox machine-wide batches per second since 20 Oct 2026');
  assert.equal(DRILL_TEXT.dbSrcBlackbox(null, 5), 'Blackbox machine-wide batches per second, read every 5 s, recording start unknown');
  assert.equal(DRILL_TEXT.dbSrcBlackbox(null), 'Blackbox machine-wide batches per second, recording start unknown');
  assert.equal(DRILL_TEXT.dbShortBlackbox(30), 'batches/s every 30 s');
  assert.equal(DRILL_TEXT.dbShortBlackbox(null), 'batches/s, read interval unknown');
  for (const text of [DRILL_TEXT.dbSrcBlackbox('20 Oct 2026', 60), DRILL_TEXT.dbMsgBlackbox(60), DRILL_TEXT.dbShortBlackbox(60)]) {
    assert.doesNotMatch(text, /15 to 60/, 'the old DMV cadence range is gone: ' + text);
  }
  // UX-9 raised the cap: the message now defines batch, host counter and instance in plain words.
  // It renders as a wrapping paragraph (CallInspector's scall-db-state <p>), never a fixed-width cell.
  assert.ok(DRILL_TEXT.dbMsgBlackbox(null).length < 520);
  assert.ok(DRILL_TEXT.dbMsgBlackbox(60).length < 520);
  assert.ok(DRILL_TEXT.dbShortBlackbox(null).length < 60);
});

test('the throughput unit has its own shape, readout and legend words', () => {
  assert.equal(DRILL_TEXT.unitBatches, 'batches per second');
  assert.equal(DRILL_TEXT.laneRateShape('100'), 'Height = batches per second, 0 to 100');
  assert.equal(DRILL_TEXT.readDbRate('100'), 'Database batches per second 100');
  assert.equal(DRILL_TEXT.readDbRateNone, 'Database batches per second not recorded');
  assert.equal(DRILL_TEXT.readDbNoAtOnce, 'running at once not recorded');
  assert.equal(DRILL_TEXT.laneNoCallTimes, 'call times not recorded here');
  assert.match(DRILL_TEXT.legendRate, /batches per second/);
  assert.match(DRILL_TEXT.legendRate, /not how many ran at once/);
  // the concurrency words are unchanged for the lanes that measure concurrency
  assert.equal(DRILL_TEXT.laneShape('4.2'), 'Height = running at once, 0 to 4.2');
  assert.equal(DRILL_TEXT.readDb('4.2'), 'Database running at once (average) 4.2');
});

test('a missing import date is stated as unknown, never as a sample date', () => {
  assert.equal(DRILL_TEXT.dbSrcFile(null), 'Query Store file, import date not recorded');
  assert.doesNotMatch(DRILL_TEXT.dbMsgFile(null), /11 Nov 2026/);
  assert.equal(DRILL_TEXT.dbSrcFile('11 Nov 2026'), 'Query Store file imported 11 Nov 2026');
});

// ---- UX-2: the chart help describes the batch-rate database lane, not a count ----

test('UX-2: the rate read line and the up-help addition say what the axis measures', () => {
  assert.equal(typeof DRILL_TEXT.chartReadLineRate, 'string');
  assert.match(DRILL_TEXT.chartReadLineRate, /batches per second/);
  assert.doesNotMatch(DRILL_TEXT.chartReadLineRate, /Up is how many were running at once/);
  assert.equal(typeof DRILL_TEXT.helpUpDbRate, 'string');
  assert.match(DRILL_TEXT.helpUpDbRate, /batches per second/);
  assert.match(DRILL_TEXT.helpUpDbRate, /not how many ran at once/);
});

test('UX-2: the at-once read line and up help are unchanged', () => {
  assert.equal(
    DRILL_TEXT.chartReadLine,
    'Left to right is time. Up is how many were running at once, so a higher line is a busier moment. Each lane has its own scale.'
  );
  assert.equal(
    DRILL_TEXT.helpUp,
    'Up = how many were running at the same moment. The bottom is 0 and the dashed line marks the busiest moment in that lane. Each lane has its own scale, so compare a lane with itself.'
  );
});

test('UX-2: the new keys are linted and use no banned wording', () => {
  const ids = drillCopyEntries().map((e) => e.id);
  assert.ok(ids.includes('drill.chartReadLineRate'), 'chartReadLineRate is linted');
  assert.ok(ids.includes('drill.helpUpDbRate'), 'helpUpDbRate is linted');
  const banned = /because|due to|caus|waiting on|blocked by|led to|results? in|responsible for|leak|starv|saturat|proven/i;
  for (const s of [DRILL_TEXT.chartReadLineRate, DRILL_TEXT.helpUpDbRate]) assert.doesNotMatch(s, banned, s);
});

test('the new unit copy uses no banned wording', () => {
  const banned = /because|due to|caus|waiting on|blocked by|led to|results? in|responsible for|leak|starv|saturat|proven/i;
  const strings = [
    DRILL_TEXT.unitBatches, DRILL_TEXT.laneRateShape('100'), DRILL_TEXT.laneNoCallTimes,
    DRILL_TEXT.readDbRate('100'), DRILL_TEXT.readDbRateNone, DRILL_TEXT.readDbNoAtOnce, DRILL_TEXT.legendRate,
    DRILL_TEXT.dbSrcBlackbox(null), DRILL_TEXT.dbMsgBlackbox(null), DRILL_TEXT.dbShortBlackbox(null)
  ];
  for (const s of strings) assert.doesNotMatch(s, banned, s);
});

// ---- UX-9: a first-time investigator can say what the DB lane number means and what it does not ----

test('UX-9: the blackbox message defines a batch, the host counter and an instance in plain words', () => {
  for (const msg of [DRILL_TEXT.dbMsgBlackbox(15), DRILL_TEXT.dbMsgBlackbox(null)]) {
    assert.match(msg, /A batch is one request; it may hold several queries\./, msg);
    assert.match(msg, /host counter: SQL Server's own count of batches per second/, msg);
    assert.match(msg, /added up across every SQL Server instance on the machine/, msg);
    assert.match(msg, /an instance is one running copy of SQL Server/, msg);
    assert.match(msg, /every app, not only this one/, msg);
  }
});

test('UX-9: the message says work arrived, not work running at once, and gaps stay unknown', () => {
  for (const msg of [DRILL_TEXT.dbMsgBlackbox(5), DRILL_TEXT.dbMsgBlackbox(60), DRILL_TEXT.dbMsgBlackbox(null)]) {
    assert.match(msg, /work that arrived, not how many ran at once or how long it took/, msg);
    assert.match(msg, /Gaps stay unknown, not zero\./, msg);
  }
});

test('UX-9: the old undefined jargon is gone from the DB lane copy, or defined where it stays', () => {
  for (const msg of [DRILL_TEXT.dbMsgBlackbox(15), DRILL_TEXT.dbMsgBlackbox(null)]) {
    assert.doesNotMatch(msg, /added up over the instances there/, 'bare "instances" is gone: ' + msg);
    assert.doesNotMatch(msg, /its host counter for SQL batches/, 'the undefined "host counter" phrasing is gone: ' + msg);
    // "host counter" may stay only because the same sentence defines it as SQL Server's own machine-wide count.
    if (msg.includes('host counter')) assert.match(msg, /host counter: SQL Server's own count/, msg);
  }
  assert.doesNotMatch(DRILL_TEXT.dbSrcBlackbox('20 Oct 2026', 15), /host/, 'the source label needs no undefined "host"');
  assert.doesNotMatch(DRILL_TEXT.dbSrcBlackbox(null), /host/);
  assert.doesNotMatch(DRILL_TEXT.legendRate, /host/);
});

test('UX-9: the rate legend says the area is work that arrived and defines a batch', () => {
  assert.match(DRILL_TEXT.legendRate, /batches per second that arrived/);
  assert.match(DRILL_TEXT.legendRate, /a batch is one request/);
  assert.match(DRILL_TEXT.legendRate, /not how many ran at once/);
});

test('UX-9: the at-once lane text is unchanged', () => {
  assert.equal(DRILL_TEXT.unitAtOnce, 'running at once');
  assert.equal(DRILL_TEXT.unitAtOnceAvg, 'running at once (average)');
  assert.equal(DRILL_TEXT.laneShape('4.2'), 'Height = running at once, 0 to 4.2');
  assert.equal(DRILL_TEXT.mapAxisLine, 'Across is time. A taller column means more running at once, and each row has its own scale.');
  assert.equal(DRILL_TEXT.readDb('4.2'), 'Database running at once (average) 4.2');
  assert.equal(DRILL_TEXT.readDbPeak('4.2'), 'Database running at once (average), highest while it ran 4.2');
});

test('UX-9: the reworded DB lane copy still passes the copy lint', () => {
  assert.deepEqual(lintScreenStrings(drillCopyEntries(), deck.terms), []);
});
