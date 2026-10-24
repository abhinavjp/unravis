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

// Tests for the incident-gap addendum rules (B3a, A3, B12r, B6g) against fixture JSON in the contract's field names.
const patterns = loadTypeScript('src/lib/patterns.ts');
const { deck } = loadTypeScript('src/lib/copy/deck.ts');
const { lintCopy } = loadTypeScript('src/lib/copy/copylint.ts');
const { MIN_SAMPLE } = loadTypeScript('src/lib/thresholds.ts');

const DAY = 86_400_000;
const lintOk = (strings) => {
  assert.deepEqual(lintCopy(strings.map((text, i) => ({ id: 'a' + i, text })), deck.terms), []);
  for (const text of strings) assert.doesNotMatch(text, /\b(caused|because|due to|waiting on|blocked by|retry|complete|proven)\b/i, text);
};
const week = { start: '2026-10-17T00:00:00Z', end: '2026-10-24T00:00:00Z' };
const day = { start: '2026-10-23T00:00:00Z', end: '2026-10-24T00:00:00Z' };

// ------------------------------------------------------------------ B3a

test('episodeCount reads a count, a list of indexes or a numeric string', () => {
  assert.equal(patterns.episodeCount(3), 3);
  assert.equal(patterns.episodeCount([0, 2, 2, '5']), 3);
  assert.equal(patterns.episodeCount('4'), 4);
  for (const bad of [undefined, null, '', 'x', -1, NaN, []]) assert.equal(patterns.episodeCount(bad), 0, String(bad));
});

test('minute helpers: colon form, the shift for a zone and the hour wrap', () => {
  assert.equal(patterns.minuteText(7), ':07');
  assert.equal(patterns.minuteText(59), ':59');
  assert.equal(patterns.shiftMinute(50, 30), 20);
  assert.equal(patterns.shiftMinute(10, 0), 10);
  const at = Date.parse('2026-10-24T10:00:00Z');
  assert.equal(patterns.zoneMinuteShift('UTC', at), 0);
  assert.equal(patterns.zoneMinuteShift('Asia/Dubai', at), 0);
  assert.equal(patterns.zoneMinuteShift('Asia/Kolkata', at), 30);
  assert.equal(patterns.zoneMinuteShift('Asia/Kathmandu', at), 45);
  assert.equal(patterns.zoneMinuteShift('Not/AZone', at), 0);
  assert.equal(patterns.zoneMinuteShift('UTC', NaN), 0);
  assert.equal(patterns.onsetZoneNote('Asia/Kolkata'), 'Minutes are read on the Asia/Kolkata clock.');
});

test('onset rows: the sentence, the order, the zone shift and bad rows', () => {
  const rows = patterns.onsetRows([
    { minute: 40, days: 3, onsets: 3, episodes: [0, 1, 2] },
    { minute: 7, days: 4, onsets: 5, episodes: [3, 4, 5, 6, 7] },
    { minute: 7, days: 9, onsets: 9, episodes: 9 },
    { minute: NaN, days: 3, onsets: 3, episodes: [] }
  ]);
  assert.deepEqual(rows.map((r) => r.minuteText), [':07', ':40']);
  assert.equal(rows[0].line, 'Slow requests started near minute :07 of the hour on 4 different days in this window.');
  const shifted = patterns.onsetRows([{ minute: 40, days: 3, onsets: 3, episodes: [] }], 30);
  assert.equal(shifted[0].minuteText, ':10');
  assert.equal(patterns.onsetRows(undefined).length, 0);
});

test('the onset section: rows, absence, short windows and a capped read', () => {
  const rows = [{ minute: 7, days: 3, onsets: 3, episodes: [0, 1, 2] }];
  assert.equal(patterns.onsetSection({ truncated: false }, week).kind, 'hidden');
  assert.deepEqual(patterns.onsetSection({ truncated: false, onsetMinutes: rows }, week).rows.map((r) => r.minute), [7]);
  assert.deepEqual(patterns.onsetSection({ truncated: false, onsetMinutes: [] }, week), { kind: 'note', text: patterns.PATTERNS_TEXT.onsetNone });
  assert.deepEqual(patterns.onsetSection({ truncated: false, onsetMinutes: [] }, day), { kind: 'note', text: patterns.PATTERNS_TEXT.onsetNeedsDays });
  // Withheld when the stall read was capped, even if rows arrived.
  const capped = patterns.onsetSection({ truncated: true, stallsTruncated: true, onsetMinutes: rows }, week);
  assert.equal(capped.kind, 'note');
  assert.equal(capped.text, 'Not observed (gap or sampling: stall rows were capped, so this check was not run).');
  // An older server with only `truncated` is treated as a capped stall read.
  assert.equal(patterns.onsetSection({ truncated: true, onsetMinutes: rows }, week).kind, 'note');
  // A cap on the audit side alone does not withhold B3a.
  assert.equal(patterns.onsetSection({ truncated: true, stallsTruncated: false, auditsTruncated: true, onsetMinutes: rows }, week).kind, 'rows');
  // A short window with a cap says nothing: the check could not have run.
  assert.equal(patterns.onsetSection({ truncated: true, stallsTruncated: true }, day).kind, 'hidden');
  assert.equal(3 * DAY, 259_200_000);
});

// ------------------------------------------------------------------ A3

const conc = (over = {}) => ({ dimension: 'operation', key: 'Orders / Save', stallShare: 0.4, trafficShare: 0.05, lift: 8, stallN: 40, allN: 100, episodes: 3, withheld: false, ...over });

test('share and ratio text', () => {
  assert.equal(patterns.shareText(0.4), '40%');
  assert.equal(patterns.shareText(0.05), '5%');
  assert.equal(patterns.shareText(0.0123), '1.2%');
  assert.equal(patterns.shareText(1), '100%');
  assert.equal(patterns.shareText(NaN), 'unknown');
  assert.equal(patterns.timesText(8), '8 times');
  assert.equal(patterns.timesText(2.46), '2.5 times');
  assert.equal(patterns.timesText(12.4), '12 times');
  assert.equal(patterns.timesText(1), '1 time');
});

test('a concentrated operation across 3 episodes is a Correlation with the plan sentence', () => {
  const row = patterns.concentrationRow(conc());
  assert.equal(row.reading, 'concentrated');
  assert.equal(row.label, 'Correlation');
  assert.equal(row.line, 'Orders / Save had 40% of slow requests but 5% of all requests in this window (8 times its share of traffic).');
  assert.equal(row.episodesText, '3 episodes');
});

test('one episode, or two, is only a Symptom, and a single episode says so', () => {
  const one = patterns.concentrationRow(conc({ episodes: 1 }));
  assert.equal(one.label, 'Symptom');
  assert.equal(one.episodesText, 'single episode');
  assert.equal(patterns.concentrationRow(conc({ episodes: [0, 1] })).label, 'Symptom');
  assert.equal(patterns.concentrationRow(conc({ episodes: [0, 1, 4] })).label, 'Correlation');
  assert.equal(patterns.concentrationRow(conc({ episodes: undefined })).label, 'Symptom');
});

test('lift and count edges follow the rule: at least 2, 10 slow, 20 in all', () => {
  assert.equal(patterns.concentrationRow(conc({ lift: 2, stallN: 10, allN: 20 })).reading, 'concentrated');
  assert.equal(patterns.concentrationRow(conc({ lift: 1.99, stallN: 40 })).reading, 'between');
  assert.equal(patterns.concentrationRow(conc({ lift: 3, stallN: 9 })).reading, 'between');
  assert.equal(patterns.concentrationRow(conc({ lift: 3, allN: MIN_SAMPLE - 1 })).reading, 'withheld');
  assert.equal(patterns.concentrationRow(conc({ lift: Number.NaN })).reading, 'withheld');
});

test('stalls spread like traffic need a ratio of 1.2 or less and 50 slow requests', () => {
  const spread = patterns.concentrationRow(conc({ stallShare: 0.4, trafficShare: 0.4, lift: 1, stallN: 50, allN: 500 }));
  assert.equal(spread.reading, 'spread');
  assert.match(spread.line, /Slow requests are spread like traffic\.$/);
  assert.equal(spread.label, null);
  assert.equal(patterns.concentrationRow(conc({ lift: 1.2, stallN: 49 })).reading, 'between');
  assert.equal(patterns.concentrationRow(conc({ lift: 1.21, stallN: 80 })).reading, 'between');
});

test('a thin cell is withheld: no shares, no ratio, a user id still named', () => {
  const thin = patterns.concentrationRow(conc({ dimension: 'user', key: '7', allN: 12, lift: 9, withheld: false }));
  assert.equal(thin.reading, 'withheld');
  assert.equal(thin.lowN, true);
  assert.equal(thin.line, 'User 7: too few requests to compare (fewer than 20).');
  assert.doesNotMatch(thin.line, /%|times/);
  const flagged = patterns.concentrationRow(conc({ withheld: 'traffic share not read' }));
  assert.equal(flagged.line, 'Orders / Save: not compared (traffic share not read).');
  assert.equal(patterns.concentrationRow(conc({ withheld: true, allN: 400 })).line, 'Orders / Save: not compared.');
});

test('the concentration section orders rows, withholds when capped and says so when empty', () => {
  const report = (over) => ({ truncated: false, concentration: [conc({ key: 'B', lift: 1.5, stallN: 20 }), conc({ key: 'W', allN: 3 }), conc({ key: 'C', lift: 3 }), conc({ key: 'S', lift: 1, stallN: 60 })], ...over });
  const section = patterns.concentrationSection(report());
  assert.deepEqual(section.rows.map((r) => r.key), ['operation:C', 'operation:S', 'operation:B', 'operation:W']);
  assert.equal(patterns.concentrationSection({ truncated: false }).kind, 'hidden');
  assert.deepEqual(patterns.concentrationSection({ truncated: false, concentration: [] }), { kind: 'note', text: patterns.PATTERNS_TEXT.concNone });
  const capped = patterns.concentrationSection(report({ truncated: true, stallsTruncated: true }));
  assert.deepEqual(capped, { kind: 'note', text: 'Not observed (gap or sampling: stall rows were capped, so the comparison was not made).' });
});

test('the glossary has one new term for concentration, and the deck stays within its cap', () => {
  const term = deck.terms.find((t) => t.id === 'concentration');
  assert.ok(term);
  assert.ok(term.aliases.includes('Lift'));
  assert.ok(deck.terms.length <= 52, String(deck.terms.length));
});

test('B3a and A3 copy passes the lint and names no cause', () => {
  const T = patterns.PATTERNS_TEXT;
  const onset = patterns.onsetRows([{ minute: 7, days: 3, onsets: 3, episodes: [0, 1, 2] }, { minute: 41, days: 1, onsets: 1, episodes: [4] }])
    .map((r) => r.line);
  const concLines = [
    conc(), conc({ episodes: 1 }), conc({ dimension: 'user', key: '12', lift: 4.4, stallShare: 0.0123, trafficShare: 0.0028 }),
    conc({ lift: 1, stallN: 70, stallShare: 0.4, trafficShare: 0.4 }), conc({ lift: 1.6, stallN: 30 }), conc({ allN: 5 }), conc({ withheld: 'traffic share not read' })
  ].map((c) => patterns.concentrationRow(c).line);
  lintOk([
    T.onsetTitle, T.onsetIntro, T.onsetHint, T.onsetNeedsDays, T.onsetNone, T.concTitle, T.concIntro, T.concNone, T.concSpread,
    patterns.onsetZoneNote('Asia/Kolkata'), ...onset, ...concLines,
    patterns.onsetSection({ truncated: true, stallsTruncated: true }, week).text,
    patterns.concentrationSection({ truncated: true, stallsTruncated: true, concentration: [] }).text,
    ...deck.terms.filter((t) => t.id === 'concentration').flatMap((t) => [t.hint, t.definition, t.notMeaning])
  ]);
  // The hint copy is exactly the plan sentence.
  assert.equal(T.onsetHint, 'A scheduled task could line up with this; the job list import is needed to check.');
});

// ------------------------------------------------------------------ B12r

const rep = (o = {}) => ({ episode: 0, userId: 12, operation: 'Orders / Save', repeats: 3, sameSize: true, longestChain: 3, ...o });

test('repeatKind reads a flag or a count of same-size repeats', () => {
  assert.equal(patterns.repeatKind(true, 3), 'same');
  assert.equal(patterns.repeatKind(false, 3), 'different');
  assert.equal(patterns.repeatKind(3, 3), 'same');
  assert.equal(patterns.repeatKind(0, 3), 'different');
  assert.equal(patterns.repeatKind(1, 3), 'mixed');
});

test('repeatRow says started again, names the user and operation, and never says retry', () => {
  const same = patterns.repeatRow(rep());
  assert.equal(same.line, 'User 12 started Orders / Save again 3 times while an earlier one was still running. The longest run of starts was 3.');
  assert.equal(same.kindText, 'Same request');
  const diff = patterns.repeatRow(rep({ sameSize: false, repeats: 1, longestChain: 1 }));
  assert.equal(diff.kindText, 'Same operation, different request');
  assert.equal(diff.line, 'User 12 started Orders / Save again 1 time while an earlier one was still running.');
  assert.doesNotMatch(same.line + diff.line, /retry/i);
});

test('repeatsOf keeps one episode, most repeated first', () => {
  const report = { repeats: [rep({ episode: 1, repeats: 2 }), rep({ episode: 0, repeats: 2, userId: 3 }), rep({ episode: 0, repeats: 5 })] };
  const rows = patterns.repeatsOf(report, { index: 0 });
  assert.deepEqual(rows.map((r) => r.repeats), [5, 2]);
  assert.deepEqual(patterns.repeatsOf({}, { index: 0 }), []);
});

test('repeatsSummary is null when not sent, an absence line when empty, and counts otherwise', () => {
  assert.equal(patterns.repeatsSummary({}), null);
  assert.equal(patterns.repeatsSummary({ repeats: [] }), patterns.PATTERNS_TEXT.repeatsNone);
  assert.equal(patterns.repeatsSummary({ repeats: [rep(), rep({ userId: 4 }), rep({ episode: 2 })] }), '3 cases of an operation started again while it was running, in 2 episodes.');
  assert.equal(patterns.repeatsSummary({ repeats: [rep()] }), '1 case of an operation started again while it was running, in 1 episode.');
});

test('a B12r finding is a Correlation row and its copy passes the lint', () => {
  const finding = { rule: 'B12r', label: 'Correlation', n: 3, lowN: false, headline: 'Users started the same operation again while it was running.', evidenceId: 'b12r:x' };
  const rows = patterns.findingRows({ findings: [finding], episodes: [], recurrence: 'none', params: { stallMs: 45000 }, truncated: false });
  assert.equal(rows[0].label, 'Correlation');
  const T = patterns.PATTERNS_TEXT;
  lintOk([
    T.repeatsTitle, T.repeatsIntro, T.repeatsNone, T.repeatsSameRequest, T.repeatsDifferentRequest, T.repeatsMixedRequest,
    patterns.repeatRow(rep()).line, patterns.repeatRow(rep({ sameSize: false, repeats: 1, longestChain: 1 })).line,
    patterns.repeatsSummary({ repeats: [rep()] })
  ]);
});

// ------------------------------------------------------------------ B6g

const bands = (counts = [200, 150, 120, 40, 60]) => [
  { fromS: 0, toS: 30, n: counts[0], medianMs: 900, p95Ms: 3000, slowRate: 0.01 },
  { fromS: 30, toS: 60, n: counts[1], medianMs: 950, p95Ms: 3200, slowRate: 0.012 },
  { fromS: 60, toS: 240, n: counts[2], medianMs: 1000, p95Ms: 4000, slowRate: 0.02 },
  { fromS: 240, toS: 600, n: counts[3], medianMs: 2500, p95Ms: 9000, slowRate: 0.1 },
  { fromS: 600, toS: null, n: counts[4], medianMs: 3000, p95Ms: 12000, slowRate: 0.2 }
];
const idle = (o = {}) => ({ bands: bands(), lift: [{ gS: 60, value: 1.2, withheld: false }, { gS: 240, value: 6, withheld: false }, { gS: 600, value: 9, withheld: false }], excludedHangfire: 12, partial: false, ...o });

test('bandLabel names every band from its limits', () => {
  assert.deepEqual(bands().map(patterns.bandLabel), ['Under 30 s', '30 s to 1 min', '1 to 4 min', '4 to 10 min', '10 min or more']);
  assert.equal(patterns.bandLabel({ fromS: 600, toS: 0 }), '10 min or more');
});

test('idleBandRows shows no statistics for a band under 20 requests', () => {
  const rows = patterns.idleBandRows(bands([200, 19, 120, 40, 60]));
  assert.equal(rows[1].thin, true);
  assert.deepEqual([rows[1].median, rows[1].slowest, rows[1].slowRate], ['–', '–', '–']);
  assert.equal(rows[0].median, '0.9 s');
  assert.equal(rows[3].slowRate, '10%');
});

test('idleLiftRows marks a lift of 5 or more with enough requests as raised', () => {
  const rows = patterns.idleLiftRows(idle());
  assert.deepEqual(rows.map((r) => r.reading), ['flat', 'raised', 'raised']);
  assert.equal(rows[1].line, 'After a quiet gap of 4 min or more, slow audit creates were 6 times as common as after a shorter gap.');
  assert.equal(rows[1].before, 470);
  assert.equal(rows[1].after, 100);
});

test('idleLiftRows calls a lift of 1.5 or less flat only with 50 requests in both groups', () => {
  const flat = patterns.idleLiftRows(idle({ lift: [{ gS: 240, value: 1.1, withheld: false }] }));
  assert.equal(flat[0].reading, 'flat');
  assert.match(flat[0].line, /about the same/);
  const thin = patterns.idleLiftRows(idle({ bands: bands([200, 150, 120, 25, 20]), lift: [{ gS: 240, value: 1.1, withheld: false }] }));
  assert.equal(thin[0].reading, 'between');
});

test('idleLiftRows withholds on a partial read, a thin group, a server reason or no value', () => {
  assert.equal(patterns.idleLiftRows(idle({ partial: true }))[1].reading, 'withheld');
  assert.match(patterns.idleLiftRows(idle({ partial: true }))[1].line, /Partial read/);
  const thin = patterns.idleLiftRows(idle({ bands: bands([200, 150, 120, 10, 9]) }));
  assert.equal(thin[1].reading, 'withheld');
  assert.equal(thin[1].line, '4 min: too few requests to compare (fewer than 20).');
  assert.equal(patterns.idleLiftRows(idle({ lift: [{ gS: 60, value: 5, withheld: 'slow rate not read' }] }))[0].line, '1 min: not compared (slow rate not read).');
  assert.equal(patterns.idleLiftRows(idle({ lift: [{ gS: 60, value: null, withheld: false }] }))[0].reading, 'withheld');
});

test('idleSection hides unless asked, notes a missing check, and carries the Hangfire count and partial note', () => {
  assert.deepEqual(patterns.idleSection({}, false), { kind: 'hidden' });
  assert.deepEqual(patterns.idleSection({}, true), { kind: 'note', text: 'Not observed (gap or sampling: the server did not send this check).' });
  const section = patterns.idleSection({ idleGaps: idle() }, true);
  assert.equal(section.kind, 'rows');
  assert.equal(section.excluded, '12 Hangfire requests were left out of this check.');
  assert.equal(section.raised, true);
  assert.equal(section.partial, '');
  assert.equal(patterns.idleSection({ idleGaps: idle({ excludedHangfire: 1, partial: true }) }, false).excluded, '1 Hangfire request was left out of this check.');
  assert.equal(patterns.idleSection({ idleGaps: idle({ partial: true }) }, false).partial, patterns.PATTERNS_TEXT.idlePartial);
  assert.equal(patterns.idleSection({ idleGaps: idle({ excludedHangfire: 0 }) }, false).excluded, '');
  assert.equal(patterns.idleSection({ idleGaps: { bands: [], lift: [], excludedHangfire: 0, partial: false } }, false).kind, 'note');
});

test('windowInputSeconds sends idleGaps only when asked', () => {
  const q = { sourceIds: ['s1'], start: '2026-10-23T00:00:00Z', end: '2026-10-24T00:00:00Z' };
  assert.equal('idleGaps' in patterns.windowInputSeconds(q, '', '').input, false);
  assert.equal(patterns.windowInputSeconds(q, '', '', true).input.idleGaps, true);
  assert.equal(patterns.windowInputSeconds(q, 'x', '', true).error, patterns.PATTERNS_TEXT.stallBad);
});

test('B6g copy passes the lint and stays a Symptom', () => {
  const T = patterns.PATTERNS_TEXT;
  const lifts = patterns.idleLiftRows(idle()).map((r) => r.line);
  const flat = patterns.idleLiftRows(idle({ lift: [{ gS: 240, value: 1.1, withheld: false }] })).map((r) => r.line);
  const withheld = [
    ...patterns.idleLiftRows(idle({ partial: true })).map((r) => r.line),
    ...patterns.idleLiftRows(idle({ bands: bands([200, 150, 120, 10, 9]) })).map((r) => r.line),
    ...patterns.idleLiftRows(idle({ lift: [{ gS: 60, value: 5, withheld: 'slow rate not read' }] })).map((r) => r.line)
  ];
  lintOk([
    T.idleTitle, T.idleIntro, T.idleCaveat, T.idleSources, T.idleOptIn, T.idleOptInNote, T.idlePartial, T.idleRaised,
    ...lifts, ...flat, ...withheld, patterns.excludedLine(12), patterns.excludedLine(1), patterns.idleSection({}, true).text,
    patterns.idleSection({ idleGaps: { bands: [], lift: [], excludedHangfire: 0, partial: false } }, false).text
  ]);
  assert.equal(T.idleLabel, 'Symptom');
});
