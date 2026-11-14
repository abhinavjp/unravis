const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

// BBX-5-17 / BL-103: the Blackbox SQL lane source in the slow-call database lane.
// The server (BBX-5-16) names the lane source "blackbox" and sends the moment its recording began (`since`);
// the web says, to someone who has never seen this screen: "Blackbox sampling every 15 to 60 s since <date>".
// The range is the truth: BL-101/118 sample every 15 s, BL-102/118/119 every 60 s.

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

const { dbState, DB_STATE_KEYS } = loadTypeScript('src/lib/drill/dbState.ts');
const { DRILL_TEXT: T, DRILL_HELP, drillCopyEntries } = loadTypeScript('src/lib/drill/drillCopy.ts');
const { lintScreenStrings } = loadTypeScript('src/lib/copy/copylint.ts');
const { deck } = loadTypeScript('src/lib/copy/deck.ts');
const model = loadTypeScript('src/lib/tracing/model.ts');

const fmt = { date: (iso) => 'D(' + iso.slice(0, 10) + ')', time: (iso) => 'T(' + iso.slice(11, 16) + ')' };
const WS = Date.parse('2026-11-11T10:00:00Z');
const WE = Date.parse('2026-11-11T11:00:00Z');
const SINCE = '2026-10-20T08:30:00Z';
const base = {
  laneReasonCode: '', laneHasShape: false, laneAsOf: null, laneSince: null, blockSeconds: null,
  queryStore: null, hasEvents: false, windowStart: WS, windowEnd: WE
};
const run = (over) => dbState({ ...base, ...over }, fmt);
const blackbox = (over = {}) => run({ laneReasonCode: 'blackbox', laneHasShape: true, laneSince: SINCE, ...over });

// ---- the state itself ----

test('blackbox: the server source key picks its own state', () => {
  const v = blackbox();
  assert.equal(v.key, 'blackbox');
  assert.equal(v.source, T.dbSrcBlackbox('D(2026-10-20)'));
});

test('blackbox: the label a first-time investigator reads, with the date', () => {
  const v = blackbox();
  assert.match(v.source, /^Blackbox sampling every 15 to 60 s since /);
  assert.ok(v.source.includes('Blackbox sampling every 15 to 60 s since'), v.source);
  assert.ok(v.source.includes('D(2026-10-20)'), 'the label carries the date the sampling began: ' + v.source);
  assert.equal(T.dbSrcBlackbox('20 Oct 2026'), 'Blackbox sampling every 15 to 60 s since 20 Oct 2026');
});

test('blackbox: it says what the source is and stays calm, with the usual short label and shape', () => {
  const v = blackbox();
  assert.equal(v.message, T.dbMsgBlackbox);
  assert.equal(typeof v.message, 'string');
  assert.ok(v.message.length > 0 && v.message.length < 320, 'message length ' + v.message.length);
  assert.equal(v.short, T.dbShortBlackbox);
  assert.ok(v.short.length > 0 && v.short.length < 60, 'short length ' + v.short.length);
  assert.equal(v.shape, 'blocks');
  assert.equal(DB_STATE_KEYS.includes('blackbox'), false, 'blackbox is a real server state, not a demo pretend switch');
});

test('blackbox: no since from the server still names the source, on the sample date', () => {
  const v = run({ laneReasonCode: 'blackbox', laneHasShape: true });
  assert.equal(v.key, 'blackbox');
  assert.equal(v.source, T.dbSrcBlackbox('11 Nov 2026'));
});

test('blackbox: the lane source wins over a Query Store state that says otherwise', () => {
  // The drawing shows the Blackbox blocks, so the words must not claim the lane records nothing.
  for (const queryStore of [{ state: 'off' }, { state: 'denied' }, { state: 'read-only', firstData: '2026-11-01T00:00:00Z' }]) {
    assert.equal(blackbox({ queryStore }).key, 'blackbox', JSON.stringify(queryStore));
  }
  assert.equal(blackbox({ blockSeconds: 0 }).key, 'blackbox');
});

test('blackbox: nothing drawn on the lane is not a claim, and never a zero lane', () => {
  const empty = run({ laneReasonCode: 'blackbox', laneHasShape: false, laneSince: SINCE });
  assert.notEqual(empty.key, 'blackbox');
  assert.equal(empty.key, 'notread');
  assert.equal(empty.source, T.dbSrcNone);
  assert.equal(empty.short, T.notRecorded);
  assert.equal(empty.shape, 'none');
});

test('an absent source still falls through to "Not recorded", exactly as before', () => {
  const v = run({});
  assert.equal(v.key, 'notread');
  assert.equal(v.source, 'Not recorded');
  assert.equal(v.message, T.dbMsgNotRead);
  assert.equal(v.short, T.notRecorded);
  assert.equal(v.shape, 'none');
  assert.doesNotMatch(v.source, /Blackbox/i);
  // and an unreadable Query Store row still follows the data, not the new key
  assert.equal(run({ queryStore: { state: 'unknown' } }).key, 'notread');
  assert.equal(run({ laneReasonCode: 'blackbox', laneHasShape: true, queryStore: null }).key, 'blackbox');
});

test('the other database source labels are untouched', () => {
  assert.equal(T.dbSrcLive, 'Query Store (live)');
  assert.equal(T.dbSrcFile('11 Nov 2026'), 'Query Store file imported 11 Nov 2026');
  assert.equal(T.dbSrcApi, 'Seen by the API (time inside the database)');
  assert.equal(T.dbSrcLate('10:30'), 'Query Store (live), nothing before 10:30');
  assert.equal(T.dbSrcReadOnly, 'Query Store (live), read-only');
  assert.equal(T.dbSrcEvents, 'SQL Server event files, markers only');
  assert.equal(T.dbSrcNone, 'Not recorded');
  // the states they belong to still read the same
  assert.equal(run({ laneReasonCode: 'held-sql', laneHasShape: true, blockSeconds: 1800, queryStore: { state: 'on', firstData: '2026-11-01T00:00:00Z' } }).source, T.dbSrcLive);
  assert.equal(run({ laneReasonCode: 'api-seen', laneHasShape: true }).source, T.dbSrcApi);
});

// ---- copy registration: a function key is not auto-counted, so the SAMPLE entry and the fns row must exist ----

test('dbSrcBlackbox is registered in the lint entries, and the count guard holds', () => {
  const entries = drillCopyEntries();
  const ids = entries.map((e) => e.id);
  assert.ok(ids.includes('drill.dbSrcBlackbox'), 'drill.dbSrcBlackbox is linted');
  assert.ok(ids.includes('drill.dbMsgBlackbox'), 'drill.dbMsgBlackbox is linted');
  assert.ok(ids.includes('drill.dbShortBlackbox'), 'drill.dbShortBlackbox is linted');
  assert.equal(ids.filter((id) => id === 'drill.dbSrcBlackbox').length, 1, 'one entry per key');
  assert.equal(new Set(ids).size, ids.length, 'entry ids are unique');
  assert.equal(entries.length, Object.keys(T).length + Object.keys(DRILL_HELP).length);
  const hit = entries.find((e) => e.id === 'drill.dbSrcBlackbox');
  assert.match(hit.text, /^Blackbox sampling every 15 to 60 s since \S+ \S+ \d{4}$/);
});

test('no help key was added: the help deck keeps its fixed 26 keys', () => {
  assert.equal(Object.keys(DRILL_HELP).length, 26);
  assert.equal(Object.keys(DRILL_HELP).filter((k) => /blackbox/i.test(k)).length, 0);
});

test('the new copy passes the copy lint with the rest of the drill deck', () => {
  assert.deepEqual(lintScreenStrings(drillCopyEntries(), deck.terms), []);
});

test('the new copy uses no banned wording and names no cause', () => {
  const banned = /because|due to|caus|waiting on|blocked by|led to|results? in|responsible for|leak|starv|saturat|proven/i;
  const texts = [T.dbSrcBlackbox('20 Oct 2026'), T.dbMsgBlackbox, T.dbShortBlackbox];
  for (const text of texts) {
    assert.equal(typeof text, 'string');
    assert.doesNotMatch(text, banned, text);
  }
});

// ---- the wire: the server's `since` reaches the screen ----

test('parseTracing keeps the server since on the lane, and drops one it cannot read', () => {
  const WINDOW = {
    start: '2026-11-11T10:00:00Z', end: '2026-11-11T11:00:00Z',
    axisStart: '2026-11-11T10:00:00Z', axisEnd: '2026-11-11T11:00:00Z', bucketSeconds: 60
  };
  const lane = (over) => ({
    key: 'sql', label: 'SQL Server', state: 'events', origin: 'case', asOf: '2026-11-11T11:05:00Z',
    complete: true, truncated: false, reason: '', scanned: 10, hidden: 0, sources: [],
    buckets: [{ start: '2026-11-11T10:00:00Z', count: 3 }], ...over
  });
  const withSince = model.parseTracing({ window: WINDOW, lanes: [lane({ reasonCode: 'blackbox', since: SINCE })], allLanesStored: true });
  assert.equal(withSince.lanes[0].since, SINCE);
  assert.equal(withSince.lanes[0].reasonCode, 'blackbox');
  const noSince = model.parseTracing({ window: WINDOW, lanes: [lane()], allLanesStored: true });
  assert.equal('since' in noSince.lanes[0], false);
  const badSince = model.parseTracing({ window: WINDOW, lanes: [lane({ since: 'not a date' })], allLanesStored: true });
  assert.equal('since' in badSince.lanes[0], false);
});

test('the screen hands the server since to the database state, and adds no new polling', () => {
  const source = fs.readFileSync(path.join(root, 'src', 'components', 'SlowCallScreen.svelte'), 'utf8');
  assert.match(source, /laneSince:/);
  assert.match(source, /sqlLane\?\.since/);
  assert.doesNotMatch(source, /setInterval|EventSource|new WebSocket/);
  assert.equal((source.match(/dbState\(/g) ?? []).length, 1, 'one database state call');
});
