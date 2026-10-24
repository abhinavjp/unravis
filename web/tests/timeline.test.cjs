const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

const root = path.resolve(__dirname, '..');
function loadTypeScript(relativePath) {
  const filename = path.join(root, relativePath);
  const source = fs.readFileSync(filename, 'utf8');
  const output = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022, esModuleInterop: true } }).outputText;
  const module = { exports: {} };
  const nodeRequire = createRequire(filename);
  const load = (request) => /^\.\.?\//.test(request) && fs.existsSync(path.join(path.dirname(filename), request + '.ts'))
    ? loadTypeScript(path.relative(root, path.join(path.dirname(filename), request + '.ts')))
    : nodeRequire(request);
  new Function('require', 'module', 'exports', output)(load, module, module.exports);
  return module.exports;
}

const tl = loadTypeScript('src/lib/timeline.ts');
const w = loadTypeScript('src/lib/copy/wordings.ts');
const { deck } = loadTypeScript('src/lib/copy/deck.ts');
const { lintCopy } = loadTypeScript('src/lib/copy/copylint.ts');

const lint = (texts) => lintCopy(texts.map((text, i) => ({ id: 't' + i, text })), deck.terms);

// What internal/timeline writes for a disabled view (copied from its fixed sentences).
const REASONS = {
  'no-sources': 'The timeline needs at least 2 sources. No file has been imported yet.',
  'single-source': 'The timeline needs at least 2 sources. Only 1 has been imported.',
  'tz-rule': 'The timeline needs a checked time zone rule for each source. Missing for: iis-w3c, sql-xe.'
};

const SOURCES = [
  { name: 'iis-w3c', hosts: ['web-a'], tzRules: ['utc-field'], validated: true, files: 1 },
  { name: 'sql-xe', hosts: ['sql-a'], tzRules: ['host-local'], validated: true, files: 1 }
];
const row = (over) => ({ kind: 'overlap', time: '2001-02-03T04:01:45Z', end: '2001-02-03T04:02:20Z', intervalId: 'audit-1', label: 'Correlation', wording: 'x', ...over });
const auditRow = (id, time, seconds) => row({ kind: 'audit', intervalId: id, time, end: time, label: 'Symptom', wording: w.auditCreate(seconds) });
const view = (rows, over = {}) => ({ enabled: true, sources: SOURCES, rows, unlisted: 0, truncated: false, ...over });

test('a disabled view for a single source, a missing rule or no files parses and shows its reason', () => {
  for (const [code, reason] of Object.entries(REASONS)) {
    const v = tl.parseTimelineView({ enabled: false, reasonCode: code, reason, sources: [], rows: [], unlisted: 0, truncated: false });
    assert.ok(v, code);
    assert.equal(v.enabled, false);
    assert.equal(v.reason, reason);
    assert.equal(v.rows.length, 0);
  }
});

test('a disabled view must carry a reason and no rows; an enabled view must carry no reason', () => {
  assert.equal(tl.parseTimelineView({ enabled: false, sources: [], rows: [] }), null, 'no reason');
  assert.equal(tl.parseTimelineView({ enabled: false, reason: REASONS['single-source'], sources: [], rows: [auditRow('a', '2001-02-03T04:00:00Z', 28)] }), null, 'rows while off');
  assert.equal(tl.parseTimelineView({ enabled: true, reason: 'x', sources: SOURCES, rows: [] }), null, 'reason while on');
});

test('parseTimelineView refuses shapes it does not know', () => {
  assert.equal(tl.parseTimelineView(null), null);
  assert.equal(tl.parseTimelineView('x'), null);
  assert.equal(tl.parseTimelineView({}), null);
  assert.equal(tl.parseTimelineView(view([row({ label: 'Strong correlation' })])), null, 'only Symptom or Correlation');
  assert.equal(tl.parseTimelineView(view([row({ label: 'Proven cause' })])), null);
  assert.equal(tl.parseTimelineView(view([row({ wording: '' })])), null, 'a row without wording');
  assert.equal(tl.parseTimelineView(view([row({ kind: 'composite' })])), null, 'no composite rows');
  assert.equal(tl.parseTimelineView(view([row({ time: 'not a time' })])), null);
  assert.equal(tl.parseTimelineView(view([row({ join: 'maybe' })])), null);
  assert.equal(tl.parseTimelineView(view([row({ candidates: -1 })])), null);
  assert.equal(tl.parseTimelineView(view([], { sources: [{ name: 1 }] })), null);
});

test('a valid view round trips and fills defaults', () => {
  const rows = [auditRow('audit-1', '2001-02-03T04:01:40Z', 28), row({ source: 'sql-xe', host: 'sql-a', eventKind: 'lock-wait', evidenceId: 'ev:a:sql-xe:0', join: 'unique', durationMs: 35000, wording: w.overlapUnique('sql-xe', 'lock wait', 35) })];
  const v = tl.parseTimelineView({ enabled: true, sources: SOURCES, rows });
  assert.deepEqual(v.rows, rows);
  assert.equal(v.unlisted, 0);
  assert.equal(v.truncated, false);
});

test('an ambiguous match lists exactly k candidate rows in its group', () => {
  for (const k of [2, 3, 5]) {
    const candidates = Array.from({ length: k }, (_, i) => row({
      source: 'sql-xe', host: 'sql-a', eventKind: 'lock-wait', evidenceId: `ev:a:sql-xe:${i}`, join: 'ambiguous', candidates: k,
      time: `2001-02-03T04:01:${String(40 + i).padStart(2, '0')}Z`, wording: w.overlapAmbiguous(k)
    }));
    const v = tl.parseTimelineView(view([auditRow('audit-1', '2001-02-03T04:01:40Z', 28), ...candidates]));
    const groups = tl.timelineGroups(v);
    assert.equal(groups.length, 1);
    const listed = groups[0].rows.filter((r) => r.join === 'ambiguous');
    assert.equal(listed.length, k);
    assert.equal(new Set(listed.map((r) => r.evidenceId)).size, k, 'every candidate once');
    for (const r of listed) assert.equal(r.wording, `Possible overlap (ambiguous: ${k} candidates).`);
  }
});

test('groups keep the server order, one per interval, and drop rows with no interval', () => {
  const v = view([
    auditRow('a', '2001-02-03T04:00:10Z', 28),
    row({ intervalId: 'a', source: 'iis-w3c', kind: 'absence', join: 'none', label: 'Symptom', wording: w.notObservedGap('this source states no recording threshold') }),
    auditRow('b', '2001-02-03T04:05:00Z', 30),
    row({ intervalId: 'b', source: 'sql-xe', join: 'unique', wording: w.overlapUnique('sql-xe', 'lock wait', 35) }),
    row({ intervalId: 'orphan', source: 'sql-xe', join: 'unique', wording: w.overlapUnique('sql-xe', 'lock wait', 35) })
  ]);
  const groups = tl.timelineGroups(v);
  assert.deepEqual(groups.map((g) => g.intervalId), ['a', 'b']);
  assert.deepEqual(groups.map((g) => g.rows.length), [1, 1]);
});

test('rows are shown on one clock: UTC by default, another zone on request', () => {
  const r = row({ time: '2001-02-03T04:01:45Z' });
  assert.equal(tl.rowClock(r, 'UTC'), '3 Feb 04:01:45');
  assert.equal(tl.rowClock(r, 'Pacific/Auckland'), '3 Feb 17:01:45');
});

test('time rules and subjects are written in plain words', () => {
  assert.equal(tl.ruleWords('host-local'), 'host time zone');
  assert.equal(tl.ruleWords('guess'), 'not checked');
  assert.equal(tl.rowSubject(row({ source: 'sql-xe', host: 'sql-a', eventKind: 'lock-wait' })), 'sql-xe, sql-a, lock wait');
  assert.equal(tl.rowSubject(row({})), '');
});

test('unlistedLine counts events and stays empty for none', () => {
  assert.equal(tl.unlistedLine(0), '');
  assert.equal(tl.unlistedLine(NaN), '');
  assert.match(tl.unlistedLine(1), /^1 other event was read but not listed\. It did not overlap/);
  assert.match(tl.unlistedLine(4), /^4 other events were read but not listed\. They did not overlap/);
});

test('every fixed string, reason and row wording passes the copy lint', () => {
  const texts = [
    ...Object.values(tl.TIMELINE_TEXT),
    ...Object.values(REASONS),
    tl.unlistedLine(1), tl.unlistedLine(2), tl.unlistedLine(250),
    ...Object.values(tl.TIMELINE_TEXT).map(String),
    ...['utc-field', 'offset-in-field', 'header-bias', 'host-local', 'other'].map(tl.ruleWords),
    w.auditCreate(28),
    w.overlapUnique('sql-xe', 'lock wait', 35),
    w.overlapAmbiguous(3),
    w.notObservedThreshold('sql-xe', 'lock waits over 30 s and latch waits over 15 s'),
    w.notObservedGap('this interval is outside the time range of the file'),
    w.notObservedGap('this source states no recording threshold'),
    w.notObservedGap('the time range of this file is not known'),
    w.notObservedGap('the files from this source state different thresholds')
  ];
  assert.deepEqual(lint(texts), []);
});

test('no timeline text names a cause', () => {
  const all = [...Object.values(tl.TIMELINE_TEXT), ...Object.values(REASONS)].join(' ');
  assert.doesNotMatch(all, /\b(caus\w*|because|due to|waiting on|blocked by|prov\w+)\b/i);
});

test('Timeline.svelte compiles without warnings and keeps its text out of the markup', async () => {
  const { compile } = require('svelte/compiler');
  const source = fs.readFileSync(path.join(root, 'src/lib/components/Timeline.svelte'), 'utf8');
  const result = compile(source, { filename: 'Timeline.svelte', generate: 'client' });
  assert.deepEqual(result.warnings.map((x) => x.code), []);
  // Words shown to people come from timeline.ts (linted above), never typed into the component.
  const markup = source.slice(source.indexOf('</script>'), source.indexOf('<style>'));
  const literal = markup.replace(/\{[^}]*\}/g, '').replace(/<[^>]*>/g, ' ').replace(/\s+/g, ' ').trim();
  assert.equal(literal, '', 'static text in markup: ' + literal);
});

test('each row carries the zone offset of its own instant, so a repeated clock hour reads two ways', () => {
  // Europe/London clocks go back at 01:00 UTC on 28 Oct 2001: 01:30 happens twice.
  const first = tl.rowClockWithOffset(row({ time: '2001-10-28T00:30:00Z' }), 'Europe/London');
  const second = tl.rowClockWithOffset(row({ time: '2001-10-28T01:30:00Z' }), 'Europe/London');
  assert.equal(first, '28 Oct 01:30:00 GMT+1');
  assert.match(second, /^28 Oct 01:30:00 GMT(\+0)?$/); // ICU versions differ on "GMT" and "GMT+0"
  assert.notEqual(first, second);
  // The offset is the one in force at that row's time, not the one in force now.
  assert.match(tl.rowClockWithOffset(row({ time: '2001-01-15T12:00:00Z' }), 'Europe/London'), /^15 Jan 12:00:00 GMT(\+0)?$/);
  assert.equal(tl.rowClockWithOffset(row({ time: '2001-07-15T12:00:00Z' }), 'Europe/London'), '15 Jul 13:00:00 GMT+1');
  // UTC rows stay plain; the heading names the zone.
  assert.equal(tl.rowClockWithOffset(row({ time: '2001-02-03T04:01:45Z' }), 'UTC'), '3 Feb 04:01:45');
});
