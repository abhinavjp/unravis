const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

// The Investigation Timeline reading what it did not read: the banner, the chunked read, the "Sources traced" picker and the
// SQL / IIS empty state. Pure modules are run; the Svelte files are read as text.

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
const read = (relative) => fs.readFileSync(path.join(root, relative), 'utf8');

const lanes = loadTypeScript('src/lib/tracing/lanes.ts');
const windows = loadTypeScript('src/lib/tracing/windows.ts');
const sources = loadTypeScript('src/lib/tracing/sources.ts');
const model = loadTypeScript('src/lib/tracing/model.ts');

const lane = (extra = {}) => ({
  key: 'audit', label: 'API requests', state: 'events', reason: '', origin: 'live', asOf: '', consistency: 'live-non-snapshot',
  complete: true, truncated: false, scanned: 0, hidden: 0, sources: [], buckets: [], ...extra
});
const DAY = 86400000;
const T0 = Date.parse('2026-10-01T00:00:00Z');
const through = (iso) => 'T:' + iso.slice(5, 10);

/* ------------------------------------------------------------------ the banner contradiction */

test('a refused lane is never "empty": the banner and the hints agree (11-day window, API lane refused)', () => {
  const refused = lane({ state: 'incomplete', incompleteReason: 'broad-scan', complete: false, buckets: [] });
  const sql = lane({ key: 'sql', state: 'no-data' });
  const iis = lane({ key: 'iis', state: 'no-data' });
  const all = [refused, sql, iis];
  assert.equal(lanes.anyLaneCutShort(all), true, 'the banner shows');
  assert.equal(windows.windowIsEmpty(all), false, 'and the screen does not also say no events');
  assert.equal(windows.windowHasNoData(all), false, 'nor that there is no data');
  // Once every lane is read in full, the same shape of answer is a real empty window.
  const quiet = [lane({ buckets: [{ start: '2026-10-01T00:00:00Z', end: '2026-10-01T01:00:00Z', count: 0 }] })];
  assert.equal(lanes.anyLaneCutShort(quiet), false);
  assert.equal(windows.windowIsEmpty(quiet), true);
});

test('every way a lane can be unread counts, a switched-off time zone does not', () => {
  assert.equal(lanes.laneNotFullyRead(lane({ state: 'incomplete' })), true);
  assert.equal(lanes.laneNotFullyRead(lane({ incompleteReason: 'rowcap' })), true);
  assert.equal(lanes.laneNotFullyRead(lane({ incompleteReason: 'error' })), true);
  assert.equal(lanes.laneNotFullyRead(lane({ chunksTotal: 11, chunksRead: 4 })), true);
  assert.equal(lanes.laneNotFullyRead(lane({ chunksTotal: 11, chunksRead: 11 })), false);
  assert.equal(lanes.laneNotFullyRead(lane({ buckets: [{ start: 'a', end: 'b', count: 0, complete: false }] })), true);
  assert.equal(lanes.laneNotFullyRead(lane({ state: 'incomplete', disabled: 'tz-rule' })), false);
  assert.equal(lanes.laneNotFullyRead(lane({ state: 'no-data' })), false, 'a lane with no file read nothing and says so in its own words');
});

test('the banner lists what was read and what was not, lane by lane', () => {
  const api = lane({ state: 'events', chunksTotal: 11, chunksRead: 4, readThrough: '2026-10-05T00:00:00Z', incompleteReason: 'timeout' });
  const sql = lane({ key: 'sql', state: 'events', incompleteReason: 'rowcap' });
  const lines = lanes.readSummary([api, sql, lane({ key: 'iis', state: 'events' })], through);
  assert.equal(lines.length, 2, 'the lane read in full is not listed');
  assert.match(lines[0], /^API requests \(audit log\): Read 4 of 11 days, through T:10-05\./);
  assert.match(lines[0], /marked not read/);
  assert.match(lines[1], /^Database \(SQL Server\): read in part\./);
  const none = lanes.readSummary([lane({ state: 'incomplete', chunksTotal: 11, chunksRead: 0 })], through)[0];
  assert.match(none, /none of the 11 days were read\. That is not the same as no traffic\./);
  const refused = lanes.readSummary([lane({ state: 'incomplete', incompleteReason: 'broad-scan' })], through)[0];
  assert.match(refused, /not read\. The live audit source would not read a window this long/);
});

/* ------------------------------------------------------------------ the chunked read */

test('"Read N of M days" appears only for a chunked read, with the time it stopped at', () => {
  assert.equal(lanes.laneChunkLine(lane({ chunksTotal: 11, chunksRead: 4, readThrough: '2026-10-05T00:00:00Z' }), through), 'Read 4 of 11 days, through T:10-05');
  assert.equal(lanes.laneChunkLine(lane({ chunksTotal: 11, chunksRead: 11 }), through), 'Read 11 of 11 days');
  assert.equal(lanes.laneChunkLine(lane({ chunksTotal: 11, chunksRead: 0 }), through), 'Read 0 of 11 days');
  assert.equal(lanes.laneChunkLine(lane({ chunksTotal: 1, chunksRead: 1 }), through), '', 'one piece is not a chunked read');
  assert.equal(lanes.laneChunkLine(lane(), through), '');
});

test('unread days are found from the cells the server marked and from the read-through time', () => {
  const g = { firstMs: T0, widthMs: DAY, n: 6, windowStartMs: T0, windowEndMs: T0 + 6 * DAY };
  const marked = lane({ buckets: [0, 1, 2, 3, 4, 5].map((i) => ({ start: new Date(T0 + i * DAY).toISOString(), end: '', count: 0, complete: i < 3 })) });
  assert.deepEqual([...lanes.unreadSlots(g, marked)].sort(), [3, 4, 5]);
  const stopped = lane({ chunksTotal: 6, chunksRead: 2, readThrough: new Date(T0 + 2 * DAY).toISOString(), buckets: [] });
  assert.deepEqual([...lanes.unreadSlots(g, stopped)].sort(), [2, 3, 4, 5]);
  assert.deepEqual([...lanes.unreadSlots(g, lane({ chunksTotal: 6, chunksRead: 0 }))].sort(), [0, 1, 2, 3, 4, 5]);
  assert.equal(lanes.unreadSlots(g, lane()).size, 0);
  assert.equal(lanes.unreadSlots(g, lane({ disabled: 'tz-rule', chunksTotal: 6, chunksRead: 0 })).size, 0);
  assert.deepEqual(lanes.unreadRuns(new Set([1, 2, 4, 5]), 0, 6), [{ from: 1, to: 3 }, { from: 4, to: 6 }]);
  assert.deepEqual(lanes.unreadRuns(new Set([1, 2, 4, 5]), 2, 5), [{ from: 2, to: 3 }, { from: 4, to: 5 }], 'clipped to the zoomed range');
});

test('the wire carries the chunk and source fields through and leaves them off a plain lane', () => {
  const raw = {
    window: { start: '2026-10-01T00:00:00Z', end: '2026-10-12T00:00:00Z', axisStart: '2026-10-01T00:00:00Z', axisEnd: '2026-10-12T00:00:00Z', bucketSeconds: 3600 },
    lanes: [
      { key: 'audit', label: 'API', state: 'events', reason: '', origin: 'live', asOf: '2026-10-12T00:00:00Z', consistency: 'live-non-snapshot', complete: false, truncated: false, scanned: 1, hidden: 0, sources: [], buckets: [{ start: '2026-10-01T00:00:00Z', end: '2026-10-01T01:00:00Z', count: 2, complete: true }, { start: '2026-10-01T01:00:00Z', end: '2026-10-01T02:00:00Z', count: 0, complete: false }],
        chunksTotal: 11, chunksRead: 4, readThrough: '2026-10-05T00:00:00Z', sourcesRead: [{ sourceId: 'a1', label: 'Orders' }], savedNotConnected: ['orders_audit'] },
      { key: 'sql', label: 'SQL', state: 'no-data', reason: '', origin: 'stored', asOf: '', consistency: 'stored', complete: true, truncated: false, scanned: 0, hidden: 0, sources: [], buckets: [] }
    ],
    sources: [], auditSources: [{ sourceId: 'a1', label: 'Orders' }], savedNotConnected: ['orders_audit']
  };
  const parsed = model.parseTracing(raw);
  const audit = parsed.lanes[0];
  assert.equal(audit.chunksTotal, 11);
  assert.equal(audit.chunksRead, 4);
  assert.equal(audit.readThrough, '2026-10-05T00:00:00Z');
  assert.equal(audit.buckets[1].complete, false);
  assert.deepEqual(audit.sourcesRead, [{ sourceId: 'a1', label: 'Orders' }]);
  assert.deepEqual(audit.savedNotConnected, ['orders_audit']);
  assert.deepEqual(parsed.auditSources, [{ sourceId: 'a1', label: 'Orders' }]);
  assert.deepEqual(parsed.savedNotConnected, ['orders_audit']);
  const plain = parsed.lanes[1];
  assert.equal('chunksTotal' in plain, false);
  assert.equal('sourcesRead' in plain, false);
  const bare = model.parseTracing({ window: raw.window, lanes: [], sources: [] });
  assert.deepEqual(bare.auditSources, []);
  assert.deepEqual(bare.savedNotConnected, []);
});

/* ------------------------------------------------------------------ the SQL / IIS empty state */

test('a SQL or IIS lane with no file says it reads imported files only and offers Add files', () => {
  const noFile = lane({ key: 'sql', state: 'no-data' });
  assert.equal(lanes.laneNote(noFile), lanes.READ_TEXT.noFile);
  assert.match(lanes.READ_TEXT.noFile, /^This case has no file for this source\./);
  assert.match(lanes.READ_TEXT.noFile, /imported files only, not live connections/);
  assert.equal(lanes.laneNote(lane({ key: 'iis', state: 'no-data' })), lanes.READ_TEXT.noFile);
  assert.equal(lanes.READ_TEXT.addFiles, 'Add files');
  // The audit lane has live sources; its empty answer keeps the server's words.
  assert.notEqual(lanes.laneNote(lane({ state: 'no-data', reason: 'No audit source is open.' })), lanes.READ_TEXT.noFile);
  const chart = read('src/components/TracingChart.svelte');
  assert.match(chart, /data-testid="lane-add-files"/);
  assert.match(chart, /\{#if noFile && onAddFiles && !readOnly\}/, 'no button on a case that cannot be changed');
  assert.match(chart, /onclick=\{onAddFiles\}/);
  const paper = read('src/components/TracingPaper.svelte');
  assert.match(paper, /onAddFiles=\{openCaseImport\}/, 'the button opens the case Add data panel through appState');
  assert.match(paper, /openCaseImport, openConnections/);
});

/* ------------------------------------------------------------------ the Sources traced picker */

const chips = [
  { id: 'a1', label: 'Orders', kind: 'audit' },
  { id: 'a2', label: 'Payments', kind: 'audit' },
  { id: 'cs_1', label: 'web-a', kind: 'iis' },
  { id: 'imp_2', label: 'sql-trace', kind: 'sql' }
];

test('a source can be switched off and on again; all off is its own state, never "all"', () => {
  let sel = sources.ALL_SOURCES;
  sel = sources.toggleSource(sel, chips, 'a2');
  assert.deepEqual(sel, { mode: 'some', ids: ['a1', 'cs_1', 'imp_2'] });
  assert.equal(sources.isChipOn(sel, 'a2'), false);
  assert.equal(sources.isChipOn(sel, 'a1'), true);
  sel = sources.toggleSource(sel, chips, 'a2');
  assert.deepEqual(sel, sources.ALL_SOURCES, 'every chip on again is "all"');
  // Switch every one off, one at a time.
  for (const chip of chips) sel = sources.toggleSource(sel, chips, chip.id);
  assert.deepEqual(sel, sources.NO_SOURCES);
  for (const chip of chips) assert.equal(sources.isChipOn(sel, chip.id), false);
  // And back on from nothing.
  sel = sources.toggleSource(sel, chips, 'cs_1');
  assert.deepEqual(sel, { mode: 'some', ids: ['cs_1'] });
  assert.deepEqual(sources.toggleSource(sel, chips, 'unknown'), sel, 'an id that is not listed changes nothing');
});

test('a flip whose explicit list reaches the cap the server refuses does not happen (P1)', () => {
  const chipsOfN = (n) => Array.from({ length: n }, (_, i) => ({ id: 's' + (i + 1), label: 'Source ' + (i + 1), kind: 'file' }));
  const many = chipsOfN(52);
  assert.equal(sources.MAX_EXPLICIT_SOURCES, 50);
  assert.equal(sources.toggleSource(sources.ALL_SOURCES, many, 's1'), sources.ALL_SOURCES, '52 chips, one off would be 51 ids: the same selection comes back');
  const fiftyOne = chipsOfN(51);
  assert.deepEqual(sources.toggleSource(sources.ALL_SOURCES, fiftyOne, 's1'), { mode: 'some', ids: fiftyOne.slice(1).map((chip) => chip.id) }, '51 chips, one off: 50 ids is allowed');
  const fifty = chipsOfN(50);
  assert.equal(sources.toggleSource(sources.ALL_SOURCES, fifty, 's1').ids.length, 49, '50 chips, one off: 49 ids');
  const someFifty = { mode: 'some', ids: many.slice(0, 50).map((chip) => chip.id) };
  assert.equal(sources.toggleSource(someFifty, many, 's1').ids.length, 49, 'switching one of 50 off still works');
  assert.equal(sources.toggleSource(someFifty, many, 's51'), someFifty, 'a 51st id is refused');
});

test('the request: all sends no ids, some sends the ids, none sends nothing at all', () => {
  assert.deepEqual(sources.composeSourceIds(sources.ALL_SOURCES), { nothing: false, ids: [] });
  assert.deepEqual(sources.composeSourceIds({ mode: 'some', ids: ['a1', 'cs_1'] }), { nothing: false, ids: ['a1', 'cs_1'] });
  assert.deepEqual(sources.composeSourceIds(sources.NO_SOURCES), { nothing: true });
  assert.deepEqual(sources.composeSourceIds({ mode: 'some', ids: [] }), { nothing: true }, 'an empty list is never turned into "all"');
  const paper = read('src/components/TracingPaper.svelte');
  assert.match(paper, /if \(wanted\.nothing\) \{ stopReading\(\); return; \}/);
  assert.match(paper, /\{#if nothingSelected\}\s*<p role="status" class="none-selected" data-testid="tp-none-selected">\{SOURCES_TEXT\.noneSelected\}/);
  assert.match(paper, /function stopReading\(\) \{\s*controller\?\.abort\(\);/, 'a read in flight stops');
});

test('the summary line and the chip text follow the wireframe', () => {
  assert.equal(sources.chipText(chips[0]), 'API audit · Orders');
  assert.equal(sources.chipText(chips[2]), 'IIS · web-a');
  assert.equal(sources.chipText(chips[3]), 'SQL · sql-trace');
  assert.equal(sources.sourcesSummary(sources.ALL_SOURCES, chips), 'Tracing all 4 sources.');
  assert.equal(sources.sourcesSummary({ mode: 'some', ids: ['a1'] }, chips), 'Tracing 1 of 4 sources.');
  assert.equal(sources.sourcesSummary(sources.NO_SOURCES, chips), 'Tracing no sources.');
  assert.equal(sources.SOURCES_TEXT.noSources, 'This case has no sources yet.');
  assert.equal(sources.SOURCES_TEXT.savedNotConnected, 'saved, not connected');
  assert.equal(sources.SOURCES_TEXT.noneSelected, 'No sources are selected, so nothing is traced. Choose at least one source.');
});

test('the chips come from the answer: live audit sources, then files with the kind of the lane that read them', () => {
  const res = {
    auditSources: [{ sourceId: 'a1', label: 'Orders' }],
    sources: [{ sourceId: 'cs_1', label: 'web-a' }, { sourceId: 'imp_2', label: 'trace 2' }, { sourceId: 'imp_3', label: 'other' }],
    lanes: [lane({ key: 'sql', sources: [{ sourceId: 'imp_2', label: 'trace 2' }] }), lane({ key: 'iis', sources: [{ sourceId: 'cs_1', label: 'web-a' }] })],
    savedNotConnected: ['orders_audit']
  };
  assert.deepEqual(sources.chipsOf(res).map((chip) => chip.kind + ':' + chip.id), ['audit:a1', 'iis:cs_1', 'sql:imp_2', 'file:imp_3']);
  assert.deepEqual(sources.chipsOf({ ...res, sources: [{ sourceId: 'x', label: 'IIS site b' }], lanes: [] }).map((chip) => chip.kind), ['audit', 'iis']);
});

test('an answer to "all" replaces the list; an answer to a subset only adds, so switched-off sources stay on the row', () => {
  const known = { chips, saved: ['orders_audit'] };
  const subset = { auditSources: [], sources: [{ sourceId: 'cs_1', label: 'web-a (renamed)' }], lanes: [lane({ key: 'iis', sources: [{ sourceId: 'cs_1', label: 'x' }] })], savedNotConnected: [] };
  const merged = sources.mergeUniverse(known, subset, false);
  assert.equal(merged.chips.length, 4);
  assert.equal(merged.chips.find((chip) => chip.id === 'cs_1').label, 'web-a (renamed)');
  assert.deepEqual(merged.saved, ['orders_audit'], 'a file-only request lists no audit databases, so what was known stays');
  const all = { auditSources: [{ sourceId: 'a1', label: 'Orders' }], sources: [], lanes: [], savedNotConnected: [] };
  const replaced = sources.mergeUniverse(known, all, true);
  assert.deepEqual(replaced.chips.map((chip) => chip.id), ['a1']);
  assert.deepEqual(replaced.saved, [], 'an all-answer that lists audit sources also replaces the saved list');
  const unlisted = sources.mergeUniverse(known, { auditSources: [], sources: [], lanes: [], savedNotConnected: [] }, true);
  assert.deepEqual(unlisted.chips.map((chip) => chip.id), [], 'an all-answer lists everything: nothing listed means no audit source is left');
  assert.deepEqual(unlisted.saved, []);
  const failed = sources.mergeUniverse(known, { auditSources: [], sources: [], lanes: [lane({ key: 'audit', state: 'incomplete' })], savedNotConnected: [] }, true);
  assert.deepEqual(failed.chips.map((chip) => chip.id), ['a1', 'a2'], 'the listing itself failed, so the known audit chips stay');
  assert.deepEqual(failed.saved, ['orders_audit']);
  const auditSubset = sources.mergeUniverse(known, { auditSources: [{ sourceId: 'a1', label: 'Orders' }], sources: [], lanes: [], savedNotConnected: [] }, false);
  assert.deepEqual(auditSubset.chips.map((chip) => chip.id), ['a1', 'cs_1', 'imp_2'], 'a subset that lists audit sources replaces them; the file chips stay');
  assert.deepEqual(auditSubset.saved, [], 'and the saved list is the one the answer carried');
  const fileSubset = sources.mergeUniverse(known, { auditSources: [], sources: [], lanes: [], savedNotConnected: [] }, false);
  assert.deepEqual(fileSubset.chips.map((chip) => chip.id), ['a1', 'a2', 'cs_1', 'imp_2'], 'a file-only subset keeps the known audit chips');
  assert.deepEqual(fileSubset.saved, ['orders_audit']);
});

test('the choice is kept per case in this tab, "none" included, and "all" clears it', () => {
  const store = new Map();
  const storage = { getItem: (key) => (store.has(key) ? store.get(key) : null), setItem: (key, value) => store.set(key, value), removeItem: (key) => store.delete(key) };
  const universe = { chips, saved: ['orders_audit'] };
  assert.equal(sources.loadSources('c1', storage), null, 'nothing kept: the default is all');
  sources.saveSources('c1', { mode: 'some', ids: ['a1'] }, universe, storage);
  assert.deepEqual(sources.loadSources('c1', storage), { selection: { mode: 'some', ids: ['a1'] }, universe });
  sources.saveSources('c1', sources.NO_SOURCES, universe, storage);
  const back = sources.loadSources('c1', storage);
  assert.deepEqual(back.selection, { mode: 'none' }, 'nothing selected survives a reload and is not read as all');
  assert.deepEqual(back.universe, universe, 'the whole row can be drawn before any answer');
  assert.equal(sources.loadSources('c2', storage), null, 'another case keeps its own');
  sources.saveSources('c1', sources.ALL_SOURCES, universe, storage);
  assert.equal(sources.loadSources('c1', storage), null);
  assert.equal(store.size, 0);
  // Broken or hostile storage never throws.
  store.set('unravis.tracing.sources.c3', '{not json');
  assert.equal(sources.loadSources('c3', storage), null);
  store.set('unravis.tracing.sources.c3', JSON.stringify({ mode: 'some', ids: [], chips: 'x' }));
  assert.equal(sources.loadSources('c3', storage), null, 'a "some" with no ids is not a choice');
  const broken = { getItem: () => { throw new Error('blocked'); }, setItem: () => { throw new Error('blocked'); }, removeItem: () => { throw new Error('blocked'); } };
  assert.equal(sources.loadSources('c1', broken), null);
  assert.doesNotThrow(() => sources.saveSources('c1', sources.NO_SOURCES, universe, broken));
});

test('erasing a case drops its kept choice, and only its own (P5)', () => {
  const store = new Map();
  const storage = { removeItem: (key) => store.delete(key) };
  store.set('unravis.tracing.sources.c1', '{}');
  store.set('unravis.tracing.sources.c2', '{}');
  store.set('unravis.tracing.windows.c1', '{}');
  sources.forgetSources('c1', storage);
  assert.equal(store.has('unravis.tracing.sources.c1'), false);
  assert.equal(store.has('unravis.tracing.sources.c2'), true, 'another case keeps its choice');
  assert.equal(store.has('unravis.tracing.windows.c1'), true, 'only the sources key goes');
  assert.doesNotThrow(() => sources.forgetSources('c1', { removeItem: () => { throw new Error('blocked'); } }));
  assert.doesNotThrow(() => sources.forgetSources('', storage), 'an empty id removes nothing');
});

test('a SQL or IIS lane whose files are all switched off says so; one with no file at all does not', () => {
  assert.deepEqual(sources.unselectedLanes({ mode: 'some', ids: ['a1', 'a2', 'imp_2'] }, chips), ['iis']);
  assert.deepEqual(sources.unselectedLanes({ mode: 'some', ids: ['a1'] }, chips), ['sql', 'iis']);
  assert.deepEqual(sources.unselectedLanes(sources.ALL_SOURCES, chips), []);
  assert.deepEqual(sources.unselectedLanes(sources.NO_SOURCES, chips), ['sql', 'iis']);
  assert.deepEqual(sources.unselectedLanes({ mode: 'some', ids: ['a1'] }, chips.slice(0, 2)), [], 'no file chips: the lane really has no file');
  assert.equal(lanes.laneStatusLineFor(lane({ key: 'sql', state: 'no-data' }), ['sql'], 'OFF'), 'OFF');
  assert.equal(lanes.laneStatusLineFor(lane({ key: 'sql', state: 'no-data' }), [], 'OFF'), lanes.READ_TEXT.noFile);
});

test('the picker lists live sources and saved-not-connected databases, each with the wireframe words', () => {
  const picker = read('src/components/TraceSources.svelte');
  assert.match(picker, /aria-pressed=\{isChipOn\(selection, chip\.id\)\}/);
  assert.match(picker, /\{chipText\(chip\)\}/);
  assert.match(picker, /SOURCES_TEXT\.savedNotConnected/);
  assert.match(picker, /data-testid="source-saved"/);
  assert.match(picker, /onclick=\{onConnect\}/);
  assert.match(picker, /SOURCES_TEXT\.noSources/);
  assert.match(picker, /data-testid="sources-all"/);
  assert.match(picker, /data-testid="sources-none"/);
  assert.doesNotMatch(picker, /\{@html/);
  const paper = read('src/components/TracingPaper.svelte');
  assert.match(paper, /onConnect=\{\(\) => void openConnections\('new'\)\}/, 'Connect opens the existing connect flow');
  assert.match(paper, /<TraceSources /);
  // The files that other work owns are not touched by this screen.
  for (const text of [picker, paper, read('src/components/TracingChart.svelte')]) assert.doesNotMatch(text, /AddDataPanel|SourcesHub|wizard/i);
});

test('the lane shows which audit sources it read and which saved databases it could not', () => {
  const chart = read('src/components/TracingChart.svelte');
  assert.match(chart, /data-testid="lane-sources-read"/);
  assert.match(chart, /SOURCES_TEXT\.readLabel\}: \{lane\.sourcesRead\.map/);
  assert.match(chart, /data-testid="lane-saved"/);
  assert.match(chart, /data-testid="chunk-line"/);
  assert.match(chart, /data-testid="unread-run"/);
  assert.match(chart, /READ_TEXT\.notRead/);
  assert.match(chart, /unreadSlots\(grid, lane\)/);
});

test('a changed choice is sent as sourceIds, kept, and read again; a stale one falls back to all once', () => {
  const paper = read('src/components/TracingPaper.svelte');
  assert.match(paper, /sourceIds: wanted\.ids/);
  assert.match(paper, /function changeSelection\(next: SourceSelection\) \{\s*selection = next;\s*saveSources\(caseId, next, universe\);\s*void run\(lastBody \?\? undefined\);/);
  assert.match(paper, /mergeUniverse\(universe, res, wanted\.ids\.length === 0\)/);
  assert.match(paper, /error\.status === 404/);
  assert.match(paper, /await run\(body, false\)/, 'one retry only');
  assert.match(paper, /loadSources\(caseId\)/);
});

test('every new string passes the copy lint', () => {
  const { lintScreenStrings } = loadTypeScript('src/lib/copy/copylint.ts');
  const { deck } = loadTypeScript('src/lib/copy/deck.ts');
  const { waitCopyEntries } = loadTypeScript('src/lib/copy/waitCopy.ts');
  const entries = [...waitCopyEntries(), ...lanes.laneCopyEntries(), ...sources.sourcesCopyEntries()];
  assert.ok(entries.length > 30);
  assert.deepEqual(lintScreenStrings(entries, deck.terms), []);
});

test('a lane that finished every day but is still marked incomplete is not described as a stopped read', () => {
  const full = lane({ state: 'incomplete', incompleteReason: 'broad-scan', chunksTotal: 11, chunksRead: 11, buckets: [] });
  const line = lanes.readSummary([full], through)[0];
  assert.match(line, /^API requests \(audit log\): not read\./);
  assert.doesNotMatch(line, /days after that/);
});

test('the Add files button sits above the bucket-selector overlay so a person can click it', () => {
  const chart = read('src/components/TracingChart.svelte');
  assert.match(chart, /\.add-files \{ position: relative; z-index: 2;/);
});
