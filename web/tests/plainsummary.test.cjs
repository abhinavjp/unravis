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
  // A relative import of another TypeScript file in src/lib is transpiled the same way.
  const load = (request) => request.startsWith('./') && fs.existsSync(path.join(path.dirname(filename), request + '.ts'))
    ? loadTypeScript(path.relative(root, path.join(path.dirname(filename), request + '.ts')))
    : nodeRequire(request);
  new Function('require', 'module', 'exports', output)(load, module, module.exports);
  return module.exports;
}

const plain = loadTypeScript('src/lib/copy/plainSummary.ts');
const words = loadTypeScript('src/lib/copy/wordings.ts');
const { lintCopy, fleschKincaidGrade, MAX_GRADE } = loadTypeScript('src/lib/copy/copylint.ts');
const { deck } = loadTypeScript('src/lib/copy/deck.ts');

const banned = /caused|because|waiting on|blocked by|due to|proven|\bcaus(e|es|ing)\b|\bblame/i;
const window = { start: '2026-10-24T14:45:00.000Z', end: '2026-10-24T14:50:00.000Z' };
const earlier = { start: '2026-10-24T14:30:00.000Z', end: '2026-10-24T14:45:00.000Z' };
const allowedLabels = ['Symptom', 'Correlation', 'Strong correlation'];

function check(result) {
  assert.equal(typeof result.summary, 'string');
  assert.equal(typeof result.detail, 'string');
  assert.ok(result.summary.length > 0 && result.detail.length > 0);
  for (const text of [result.summary, result.detail]) {
    assert.doesNotMatch(text, banned);
    assert.doesNotMatch(text, /\b(undefined|NaN|null)\b/);
    for (const [, label] of text.matchAll(/Evidence strength: ([^.]+)\./g)) assert.ok(allowedLabels.includes(label), label);
  }
  assert.ok(!result.summary.includes('\n'));
  return result;
}
const q = (valueMs) => valueMs == null ? null : { lowerMs: valueMs, upperMs: valueMs, valueMs };
const stats = (count, p50, p95) => ({ count, meanMs: null, minMs: null, maxMs: null, p50: q(p50), p95: q(p95), histogram: [] });
const comparison = (over = {}) => ({ controller: 'Orders', action: 'List', state: 'compared', current: stats(400, 120, 2400), reference: stats(380, 100, 800), ...over });

test('windowText states UTC day and clock, and handles odd input', () => {
  assert.equal(plain.windowText(window), 'on 24 Oct, 14:45–14:50 UTC');
  assert.equal(plain.windowText({ start: '2026-10-23T23:00:00Z', end: '2026-10-24T01:00:00Z' }), 'from 23 Oct, 23:00 to 24 Oct, 01:00 UTC');
  assert.equal(plain.windowText({ start: '2026-10-24T14:45:10Z', end: '2026-10-24T14:45:40Z' }), 'on 24 Oct, 14:45:10–14:45:40 UTC');
  assert.equal(plain.windowText({ start: '2026-10-24T14:45:00Z' }), 'on 24 Oct, 14:45 UTC');
  assert.equal(plain.windowText(null), '');
  assert.equal(plain.windowText({ start: 'nonsense', end: 'x' }), '');
});

test('durationWords picks a unit', () => {
  assert.equal(plain.durationWords(120), '120 milliseconds');
  assert.equal(plain.durationWords(2400), '2.4 seconds');
  assert.equal(plain.durationWords(28000), '28 seconds');
  assert.equal(plain.durationWords(210000), '3.5 minutes');
  assert.equal(plain.durationWords(1), '1 millisecond');
});

test('audit create: states the measurement with window, built from plainAuditCreate and auditCreate', () => {
  const r = check(plain.plainAuditCreateSummary({ auditCreateTimeMs: 28000, window }));
  assert.equal(r.summary, 'Saving the record took about 28 seconds before the action ran, as measured from the application side. That was on 24 Oct, 14:45–14:50 UTC.');
  assert.ok(r.summary.startsWith(words.plainAuditCreate(28)));
  assert.ok(r.detail.includes(words.auditCreate(28)));
  assert.match(r.detail, /driver/);
  assert.match(r.detail, /connection pool/);
  assert.match(r.detail, /network/);
  assert.match(r.detail, /Evidence strength: Symptom\./);
  assert.match(r.detail, /What this does not mean:/);
  // It says what was measured, from the application side only; no claim about what a person sees.
  assert.doesNotMatch(r.detail, /delay a person|roughly this time/);
  assert.match(r.detail, /browser/);
});

test('audit create: small, zero, missing and invalid values', () => {
  assert.match(check(plain.plainAuditCreateSummary({ auditCreateTimeMs: 20, window })).summary, /about 0\.02 seconds/);
  // Under 5 ms it is stated in milliseconds, never as "about 0 seconds".
  for (const ms of [0, 2, 4.9]) {
    const r = check(plain.plainAuditCreateSummary({ auditCreateTimeMs: ms, window }));
    assert.match(r.summary, /^Saving the record took under 5 milliseconds before the action ran\. This was measured from the application side\. That was on 24 Oct/);
    assert.doesNotMatch(r.summary, /about 0|0 seconds/);
  }
  assert.match(plain.plainAuditCreateSummary({ auditCreateTimeMs: 5, window }).summary, /about 0\.01 seconds/);
  assert.match(check(plain.plainAuditCreateSummary({ auditCreateTimeMs: 1500 })).summary, /about 1\.5 seconds before the action ran, as measured from the application side\.$/);
  for (const value of [null, undefined, -5, NaN]) {
    const r = check(plain.plainAuditCreateSummary({ auditCreateTimeMs: value, window }));
    assert.match(r.summary, /No audit create time was recorded/);
    assert.doesNotMatch(r.summary, /took about/);
  }
});

test('audit create: linkage sentences come from wordings, unchanged', () => {
  const cases = [
    [{ kind: 'overlap', source: 'SQL Server', event: 'lock wait', seconds: 12 }, words.overlapUnique('SQL Server', 'lock wait', 12)],
    [{ kind: 'ambiguous', candidates: 3 }, words.overlapAmbiguous(3)],
    [{ kind: 'notObservedThreshold', source: 'system_health', threshold: 'lock waits above 30 s' }, words.notObservedThreshold('system_health', 'lock waits above 30 s')],
    [{ kind: 'notObservedGap', detail: 'retention ended 24 Oct' }, words.notObservedGap('retention ended 24 Oct')]
  ];
  for (const [linkage, sentence] of cases) {
    const r = check(plain.plainAuditCreateSummary({ auditCreateTimeMs: 28000, window, linkage }));
    assert.ok(r.detail.includes(sentence), sentence);
    assert.doesNotMatch(r.summary, /not observed|overlap/i);
  }
});

test('typical vs slowest: states both times, window and measured change', () => {
  const r = check(plain.plainTypicalVsSlowest({ comparison: comparison(), window, referenceWindow: earlier }));
  assert.match(r.summary, /^Orders\.List, on 24 Oct, 14:45–14:50 UTC: the typical request took about 120 milliseconds\. The slowest 1 in 20 took about 2\.4 seconds or longer\./);
  assert.match(r.summary, /about 1\.6 seconds longer than in the earlier period \(24 Oct, 14:30–14:45 UTC\)\./);
  assert.match(r.detail, /about 3 times the earlier P95/);
  assert.match(r.detail, /400 requests/);
  assert.match(r.detail, /Evidence strength: Symptom\./);
  assert.match(r.detail, /What this does not mean:/);
});

test('typical vs slowest: shorter, about the same, and metric wording', () => {
  const faster = check(plain.plainTypicalVsSlowest({ comparison: comparison({ current: stats(400, 80, 500), reference: stats(400, 100, 800) }), window, referenceWindow: earlier }));
  assert.match(faster.summary, /about 300 milliseconds shorter than in the earlier period/);
  const same = check(plain.plainTypicalVsSlowest({ comparison: comparison({ current: stats(400, 100, 810), reference: stats(400, 100, 800) }), window, referenceWindow: earlier }));
  assert.match(same.summary, /about the same as in the earlier period/);
  assert.match(plain.plainTypicalVsSlowest({ comparison: comparison(), window, metric: 'api' }).detail, /API processing time only/);
  assert.match(plain.plainTypicalVsSlowest({ comparison: comparison(), window }).detail, /total time/);
  const ratio = plain.plainTypicalVsSlowest({ comparison: comparison({ p95Ratio: 3.4 }), window, referenceWindow: earlier });
  assert.match(ratio.detail, /about 3\.4 times/);
});

test('typical vs slowest: missing baseline or tiny n states no change', () => {
  for (const [state, text] of [['insufficient_data', /too few timed requests/], ['missing_reference', /No timed requests were found in the earlier period/], ['undefined_baseline', /too close to zero/]]) {
    const r = check(plain.plainTypicalVsSlowest({ comparison: comparison({ state, reference: stats(0, null, null) }), window, referenceWindow: earlier }));
    assert.match(r.summary, text);
    assert.doesNotMatch(r.summary, /longer than|shorter than/);
  }
  const noRef = check(plain.plainTypicalVsSlowest({ comparison: comparison({ reference: stats(0, null, null) }), window }));
  assert.doesNotMatch(noRef.summary, /longer than|shorter than/);
  // Under 20 timings the server sends a P50 but no P95: say only what exists.
  const p50Only = check(plain.plainTypicalVsSlowest({ comparison: comparison({ state: 'insufficient_data', current: stats(7, 120, null) }), window }));
  assert.match(p50Only.summary, /typical request took about 120 milliseconds\./);
  assert.doesNotMatch(p50Only.summary, /slowest 1 in 20 took/);
  assert.match(p50Only.detail, /needs at least 20 timed requests/);
  // Missing window falls back to words, never "undefined".
  assert.match(check(plain.plainTypicalVsSlowest({ comparison: comparison(), window: {} })).summary, /in the selected period:/);
});

test('typical vs slowest: no timed requests', () => {
  for (const current of [stats(0, null, null), stats(5, null, null)]) {
    const r = check(plain.plainTypicalVsSlowest({ comparison: comparison({ state: 'missing_current', current }), window }));
    assert.match(r.summary, /no requests with a usable timing were found on 24 Oct/);
    assert.match(r.detail, /What this does not mean:/);
  }
  assert.match(check(plain.plainTypicalVsSlowest({ comparison: comparison({ controller: '', action: '' }), window })).summary, /^These requests, on 24 Oct/);
});

test('low sample: tiny n warns, enough n says so, both state the window', () => {
  const small = check(plain.plainLowSample({ timed: 7, window }));
  assert.equal(small.summary, 'Only 7 requests had a usable timing on 24 Oct, 14:45–14:50 UTC. Do not read much into these numbers.');
  assert.match(small.detail, /at least 20 timed requests/);
  assert.match(small.detail, /What this does not mean:/);
  const one = check(plain.plainLowSample({ timed: 1, window }));
  assert.match(one.summary, /Only 1 request had/);
  const coverage = check(plain.plainLowSample({ timed: 7, events: 40, window }));
  assert.match(coverage.summary, /^Only 7 of 40 requests had a usable timing on 24 Oct/);
  assert.match(coverage.detail, /left out of the times/);
  const enough = check(plain.plainLowSample({ timed: 400, window }));
  assert.match(enough.summary, /^400 requests had a usable timing on 24 Oct, 14:45–14:50 UTC\. That is enough/);
  assert.match(enough.detail, /What this does not mean/);
});

test('low sample: floors differ by percentile; zero, null and bad values', () => {
  assert.equal(plain.sampleFloors.p95, 20);
  assert.equal(plain.sampleFloors.p99, 100);
  assert.match(check(plain.plainLowSample({ timed: 19, window })).summary, /^Only 19 requests/);
  assert.match(check(plain.plainLowSample({ timed: 20, window })).summary, /^20 requests had a usable timing/);
  assert.match(check(plain.plainLowSample({ timed: 99, window, level: 'p99' })).summary, /^Only 99 requests/);
  assert.match(check(plain.plainLowSample({ timed: 100, window, level: 'p99' })).summary, /slowest 1 in 100/);
  for (const timed of [0, null, undefined, NaN, -3]) {
    const r = check(plain.plainLowSample({ timed, window }));
    assert.match(r.summary, /^No requests with a usable timing were found/);
  }
  assert.match(check(plain.plainLowSample({ timed: 0, events: 12, window })).summary, /out of 12 requests/);
  // events smaller than timed is inconsistent input: ignore it rather than print a wrong ratio.
  assert.doesNotMatch(plain.plainLowSample({ timed: 30, events: 10, window }).summary, / of 10 /);
  assert.match(check(plain.plainLowSample({ timed: 7 })).summary, /in the selected period/);
});

test('incomplete: counts unfinished and pending with the window and a share', () => {
  const r = check(plain.plainIncomplete({ completed: 328, incomplete: 9, pending: 3, window }));
  assert.equal(r.summary, '12 of 340 requests (3.5%) had no final result on 24 Oct, 14:45–14:50 UTC. Of these, 9 had no recorded finish and 3 were accepted but have no finished record yet.');
  assert.match(r.detail, /Incomplete \(no recorded finish\): 9\. Pending/);
  assert.match(r.detail, /never counted as fast or successful/);
  assert.match(r.detail, /not a failed request/);
});

test('incomplete: only one kind, singular, invalid rows, none, empty', () => {
  assert.match(check(plain.plainIncomplete({ completed: 99, pending: 1, window })).summary, /1 of 100 requests \(1%\).*Of these, 1 was accepted but has no finished record yet\.$/);
  assert.match(check(plain.plainIncomplete({ completed: 5, incomplete: 5, window })).summary, /Of these, 5 had no recorded finish\.$/);
  const withInvalid = check(plain.plainIncomplete({ completed: 8, incomplete: 2, invalid: 4, window }));
  assert.match(withInvalid.summary, /4 more requests had a timing that could not be used\. They are not counted here\./);
  assert.match(withInvalid.detail, /Unusable requests are not in it/);
  const none = check(plain.plainIncomplete({ completed: 50, window }));
  assert.equal(none.summary, 'All 50 requests had a final result on 24 Oct, 14:45–14:50 UTC.');
  assert.match(none.detail, /does not mean every request was quick or successful/);
  for (const input of [{ completed: 0 }, { completed: null }, { completed: undefined, incomplete: NaN }, {}]) {
    assert.match(check(plain.plainIncomplete({ ...input, window })).summary, /^No requests were found on 24 Oct/);
  }
  assert.match(check(plain.plainIncomplete({ completed: 0, invalid: 3, window })).summary, /^No requests with a final result were found/);
  assert.match(check(plain.plainIncomplete({ completed: 0, pending: 4, window })).summary, /^4 of 4 requests \(100%\)/);
});

test('incomplete: a share below 100% is never rounded up to 100%, and never 0% when non-zero', () => {
  assert.match(plain.plainIncomplete({ completed: 1, incomplete: 499, window }).summary, /^499 of 500 requests \(over 99%\)/);
  assert.match(plain.plainIncomplete({ completed: 1, incomplete: 99999, window }).summary, /\(over 99%\)/);
  assert.match(plain.plainIncomplete({ completed: 4999, incomplete: 1, window }).summary, /\(under 1%\)/);
  assert.match(plain.plainIncomplete({ completed: 100000, pending: 1, window }).summary, /\(under 1%\)/);
  assert.match(plain.plainIncomplete({ completed: 2, incomplete: 98, window }).summary, /\(98%\)/);
  for (const [done, open] of [[1, 499], [1, 999], [9, 991], [0, 7], [3, 1], [199, 1]]) {
    const { summary } = plain.plainIncomplete({ completed: done, incomplete: open, window });
    const share = summary.match(/\(([^)]+)\)/)[1];
    if (done > 0) assert.notEqual(share, '100%', summary);
    assert.notEqual(share, '0%', summary);
  }
  assert.equal(plain.shareText(499, 500), 'over 99%');
  assert.equal(plain.shareText(500, 500), '100%');
  assert.equal(plain.shareText(1, 1000), 'under 1%');
  assert.equal(plain.shareText(0, 10), '0%');
});

test('pending requests are not called background jobs (the deck defines that term for Hangfire)', () => {
  const r = plain.plainIncomplete({ completed: 10, incomplete: 2, pending: 3, window });
  for (const text of [r.summary, r.detail]) assert.doesNotMatch(text, /background job/i);
});

test('no output anywhere uses a banned phrase, across a sweep of inputs', () => {
  const values = [null, 0, 1, 19, 20, 99, 100, 5000, 1e7];
  const out = [];
  for (const v of values) {
    out.push(plain.plainAuditCreateSummary({ auditCreateTimeMs: v, window }));
    out.push(plain.plainLowSample({ timed: v, events: v == null ? null : v + 3, window }));
    out.push(plain.plainIncomplete({ completed: v, incomplete: v, pending: 1, invalid: 1, window }));
    for (const state of ['compared', 'insufficient_data', 'missing_current', 'missing_reference', 'undefined_baseline', 'other']) {
      out.push(plain.plainTypicalVsSlowest({ comparison: comparison({ state, current: stats(v ?? 0, v, v == null ? null : v * 3), reference: stats(v ?? 0, v, v) }), window, referenceWindow: earlier }));
    }
  }
  assert.ok(out.length > 50);
  out.forEach(check);
});

// ---- Copy lint sweep over every generated string ----------------------------------------------------------------------------

/** The exact §6.6 wording templates, as regexes, so those sentences can be set aside when grading reading level. */
const escapeRe = (text) => text.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
const wordingRegexes = [
  words.notObservedThreshold('@@T@@', '@@T@@'), words.notObservedGap('@@T@@'), words.overlapUnique('@@T@@', '@@T@@', '@@N@@'),
  words.overlapAmbiguous('@@N@@'), words.auditCreate('@@N@@'), words.plainAuditCreate('@@N@@')
].map((sample) => new RegExp(escapeRe(sample).split('@@T@@').join('[^\\n]+?').split('@@N@@').join('\\d+(?:\\.\\d+)?'), 'g'));
const withoutFixedWordings = (text) => wordingRegexes.reduce((rest, re) => rest.replace(re, ' '), text).replace(/\s+/g, ' ').trim();

function representativeSummaries() {
  const out = [];
  const add = (label, result) => { out.push({ id: label + ' summary', text: result.summary }, { id: label + ' detail', text: result.detail }); };
  const linkages = [
    undefined,
    { kind: 'overlap', source: 'SQL Server', event: 'lock wait', seconds: 12 },
    { kind: 'ambiguous', candidates: 3 },
    { kind: 'notObservedThreshold', source: 'system_health', threshold: 'lock waits above 30 s' },
    { kind: 'notObservedGap', detail: 'retention ended 24 Oct' }
  ];
  for (const ms of [null, -1, 0, 2, 4.9, 5, 20, 120, 1500, 28000, 210000, 7_200_000]) {
    for (const [i, linkage] of linkages.entries()) add('audit ' + ms + ' ' + i, plain.plainAuditCreateSummary({ auditCreateTimeMs: ms, window, linkage }));
    add('audit nowindow ' + ms, plain.plainAuditCreateSummary({ auditCreateTimeMs: ms }));
  }
  const states = ['compared', 'insufficient_data', 'missing_current', 'missing_reference', 'undefined_baseline'];
  for (const state of states) {
    for (const [current, reference] of [
      [stats(400, 120, 2400), stats(380, 100, 800)], [stats(400, 80, 500), stats(400, 100, 800)], [stats(400, 100, 810), stats(400, 100, 800)],
      [stats(7, 120, null), stats(0, null, null)], [stats(0, null, null), stats(0, null, null)], [stats(25, 30000, 90000), stats(25, 20, 40)]
    ]) {
      for (const metric of ['total', 'api']) {
        add('typical ' + state, plain.plainTypicalVsSlowest({ comparison: comparison({ state, current, reference }), window, referenceWindow: earlier, metric }));
      }
    }
  }
  add('typical nowindow', plain.plainTypicalVsSlowest({ comparison: comparison(), window: {} }));
  for (const [timed, events] of [[0, null], [0, 12], [1, null], [7, null], [7, 40], [19, 19], [20, 20], [99, 120], [100, 100], [400, 400], [5000, 5100]]) {
    for (const level of ['p95', 'p99']) add('sample ' + timed + '/' + events + ' ' + level, plain.plainLowSample({ timed, events, window, level }));
  }
  add('sample nowindow', plain.plainLowSample({ timed: 7 }));
  for (const input of [
    { completed: 328, incomplete: 9, pending: 3 }, { completed: 99, pending: 1 }, { completed: 5, incomplete: 5 }, { completed: 8, incomplete: 2, invalid: 4 },
    { completed: 8, incomplete: 2, invalid: 1 }, { completed: 50 }, { completed: 0 }, { completed: 0, invalid: 3 }, { completed: 0, pending: 4 },
    { completed: 1, incomplete: 499 }, { completed: 4999, incomplete: 1 }, { completed: 1, incomplete: 1 }, { completed: 1000, incomplete: 400, pending: 600, invalid: 30 }
  ]) add('incomplete ' + JSON.stringify(input), plain.plainIncomplete({ ...input, window }));
  return out;
}

test('copy lint sweep: every generated summary and detail passes every copy lint rule', () => {
  const entries = representativeSummaries();
  assert.ok(entries.length > 200);
  assert.deepEqual(lintCopy(entries, deck.terms), []);
});
