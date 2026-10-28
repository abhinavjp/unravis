const assert = require('node:assert/strict');
const test = require('node:test');
const { loadTypeScript, change, step, result } = require('./guided.fixture.cjs');

const m = loadTypeScript('src/lib/guided/stepModel.ts');
const f = loadTypeScript('src/lib/guided/frame.ts');
const fix = loadTypeScript('src/lib/guided/afterFix.ts');
const t = loadTypeScript('src/lib/guided/tools.ts');
const n = loadTypeScript('src/lib/guided/nearby.ts');
const r = loadTypeScript('src/lib/guided/report.ts');

const WORDS = ['Ready', 'Partly ready', 'Cannot test yet', 'Failed'];
const TONES = { Ready: 'ready', 'Partly ready': 'partly', 'Cannot test yet': 'cannot', Failed: 'failed' };

/* ---------------------------------------------------------- every step, every state */

test('every step shows each of the four status words with its own tone, and out of date keeps the word', () => {
  for (let number = 0; number <= 10; number += 1) {
    for (const word of WORDS) {
      const answer = result({ steps: Array.from({ length: 11 }, (_, i) => step(i, i === number ? word : 'Ready')) });
      const s = m.stickerFor(answer, number, 'done');
      assert.equal(s.word, word, `step ${number}`);
      assert.equal(s.tone, TONES[word]);
      assert.equal(s.outOfDate, false);
      // Step 0 never goes out of date: it is the question.
      const stale = m.railItems(answer, 'done', number, true)[number].sticker;
      assert.equal(stale.word, word);
      assert.equal(stale.outOfDate, number > 0);
    }
  }
});

test('waiting and running are not pass words, and a run that failed before any answer says Failed', () => {
  for (let number = 0; number <= 10; number += 1) {
    assert.equal(m.stickerFor(null, number, 'idle').tone, 'waiting');
    assert.equal(m.stickerFor(result(), number, 'running').tone, 'running');
    assert.equal(m.stickerFor(null, number, 'failed').word, 'Failed');
  }
});

test('a failed rerun keeps the earlier answer on screen with its own words', () => {
  assert.equal(m.stickerFor(result(), 3, 'failed').word, 'Ready');
});

/* ---------------------------------------------------------- after the fix, later frame */

const frame = () => ({ ...f.emptyFrame(), incidentStart: '2026-10-17T10:30', incidentEnd: '2026-10-17T11:00', referenceStart: '2026-10-17T09:30', referenceEnd: '2026-10-17T10:30', route: 'A.One' });

test('the later frame moves the incident hours to the later day and the incident becomes the reference', () => {
  const later = fix.laterFrame(frame(), '2026-10-20');
  assert.equal(later.incidentStart, '2026-10-20T10:30');
  assert.equal(later.incidentEnd, '2026-10-20T11:00');
  assert.equal(later.referenceStart, '2026-10-17T10:30');
  assert.equal(later.referenceEnd, '2026-10-17T11:00');
  assert.equal(later.route, 'A.One');
});

test('the later frame is null for an unreadable day or an unusable incident, and a same-day pick is flagged as overlapping', () => {
  assert.equal(fix.laterFrame(frame(), ''), null);
  assert.equal(fix.laterFrame(frame(), '2026-02-31'), null);
  assert.equal(fix.laterFrame(frame(), 'tomorrow'), null);
  assert.equal(fix.laterFrame({ ...frame(), incidentStart: '' }, '2026-10-20'), null);
  assert.equal(fix.laterFrame({ ...frame(), incidentEnd: '2026-10-17T10:00' }, '2026-10-20'), null);
  const same = fix.laterFrame(frame(), '2026-10-17');
  assert.ok(f.frameProblems(same).length > 0);
});

/* ---------------------------------------------------------- slowest APIs */

const routes = () => ([
  { route: 'A.One', incident: { n: 300, p95: 3000 }, reference: { n: 300, p95: 500 }, p95Change: change(500, 3000) },
  { route: 'B.Two', incident: { n: 100, p95: 410 }, reference: { n: 300, p95: 400 }, p95Change: change(400, 410) },
  { route: 'C.Few', incident: { n: 3 }, reference: { n: 300, p95: 400 }, p95Change: { known: false, meaningful: false, direction: 'flat', markPercent: 30, markMs: 100 } }
]);
const withRoutes = (list) => { const base = result(); return { ...base, facts: { ...base.facts, routes: list } }; };

test('slowest APIs rank by the slow end, put an unknown one last, and honour the call minimum and the name filter', () => {
  const answer = withRoutes(routes());
  assert.deepEqual(t.slowestApiRows(answer, 0, '').map((x) => x.route), ['A.One', 'B.Two', 'C.Few']);
  assert.deepEqual(t.slowestApiRows(answer, 20, '').map((x) => x.route), ['A.One', 'B.Two']);
  assert.deepEqual(t.slowestApiRows(answer, 0, ' two ').map((x) => x.route), ['B.Two']);
  assert.equal(t.slowestApiRows(answer, Number.NaN, '').length, 3);
  assert.deepEqual(t.slowestApiRows(null, 0, ''), []);
  const few = t.slowestApiRows(answer, 0, 'few')[0];
  assert.equal(few.p95, 'No data');
  assert.equal(few.mark.tone, 'grey');
});

/* ---------------------------------------------------------- nearby activity */

test('duration bands: under 1 s fast, 1 to 2 s slow, over 2 s very slow, no time unknown and never fast', () => {
  assert.equal(n.durationBand(999), 'fast');
  assert.equal(n.durationBand(1000), 'slow');
  assert.equal(n.durationBand(2000), 'slow');
  assert.equal(n.durationBand(2001), 'very-slow');
  for (const bad of [undefined, null, Number.NaN, -1, Infinity]) assert.equal(n.durationBand(bad), 'unknown');
});

test('the nearby window is built from a UTC moment and whole-minute spans of 0 to 120', () => {
  const w = n.nearbyWindow('2026-10-17T10:42', 5, 10);
  assert.equal(w.start, '2026-10-17T10:37:00.000Z');
  assert.equal(w.end, '2026-10-17T10:52:00.000Z');
  assert.equal(n.nearbyWindow('', 5, 5), null);
  assert.equal(n.nearbyWindow('2026-10-17T10:42', 0, 0), null);
  assert.equal(n.nearbyWindow('2026-10-17T10:42', 121, 0), null);
  assert.equal(n.nearbyWindow('2026-10-17T10:42', -1, 5), null);
  assert.equal(n.nearbyWindow('2026-10-17T10:42', 1.5, 5), null);
  assert.equal(n.nearbyWindow('2026-10-17T10:42', 120, 120).start, '2026-10-17T08:42:00.000Z');
});

test('the nearby moment starts at the found onset, else the incident start, else nothing', () => {
  assert.equal(n.defaultAround('2026-10-17T10:42:00Z', '2026-10-17T10:30'), '2026-10-17T10:42');
  assert.equal(n.defaultAround('not a date', '2026-10-17T10:30'), '2026-10-17T10:30');
  assert.equal(n.defaultAround(undefined, ''), '');
});

/* ---------------------------------------------------------- report */

test('report warnings list out of date, the read limit and the server warnings once each', () => {
  assert.deepEqual(r.reportWarnings(undefined, false, 'cut', 'old'), []);
  assert.deepEqual(r.reportWarnings({ requests: 1, iisRows: 0, truncated: false, warnings: [] }, false, 'cut', 'old'), []);
  assert.deepEqual(r.reportWarnings({ requests: 1, iisRows: 0, truncated: true, warnings: ['w', 'w', ''] }, true, 'cut', 'old'), ['old', 'cut', 'w']);
});

test('report file names have no folders or control characters, with a fallback per format', () => {
  assert.equal(r.reportFileName({ filename: 'a/b/report.html', format: 'html' }), 'report.html');
  assert.equal(r.reportFileName({ filename: '', format: 'csv' }), 'unravis-data.csv');
  assert.equal(r.reportFileName({ filename: '..', format: 'html' }), 'unravis-report.html');
  assert.equal(r.reportFileName({ filename: 'a<b>:c.html', format: 'html' }), 'abc.html');
});

test('byte labels are short and never negative', () => {
  assert.equal(r.byteLabel(0), '0 bytes');
  assert.equal(r.byteLabel(512), '512 bytes');
  assert.equal(r.byteLabel(2048), '2 KB');
  assert.equal(r.byteLabel(3 * 1024 * 1024), '3.0 MB');
  assert.equal(r.byteLabel(-5), '0 bytes');
});

/* ---------------------------------------------------------- review fixes */

test('the run button is the primary only while no answer is on screen, so it never shares a screen with After the fix', () => {
  assert.equal(m.runIsPrimary(false), true);
  assert.equal(m.runIsPrimary(true), false);
});

test('a view is saved without an API name that holds a URL, and without one that is too long', () => {
  const base = { ...f.emptyFrame(), incidentStart: '2026-10-17T10:30', incidentEnd: '2026-10-17T11:00', referenceStart: '2026-10-17T09:30', referenceEnd: '2026-10-17T10:30' };
  assert.equal(t.viewSettings({ ...base, route: 'A.One' }).route, 'A.One');
  assert.equal('route' in t.viewSettings({ ...base, route: 'https://evil.example/x' }), false);
  assert.equal('route' in t.viewSettings({ ...base, route: 'x'.repeat(257) }), false);
  assert.equal('route' in t.viewSettings({ ...base, route: '' }), false);
  // What the client sends is what the client would read back.
  assert.equal(t.readViewSettings(t.viewSettings({ ...base, route: 'A.One' })).route, 'A.One');
});

test('an after-the-fix verdict is shown only for the answer it was compared against', () => {
  const answer = { id: 1 };
  assert.equal(fix.verdictIsCurrent(answer, answer), true);
  assert.equal(fix.verdictIsCurrent(answer, { id: 1 }), false);
  assert.equal(fix.verdictIsCurrent(answer, null), false);
  assert.equal(fix.verdictIsCurrent(null, null), false);
  assert.equal(fix.verdictIsCurrent(undefined, undefined), false);
});

test('the guided view is open only on the case it was opened for', () => {
  assert.equal(m.guidedOpenFor('c1', 'c1'), true);
  assert.equal(m.guidedOpenFor('c1', 'c2'), false);
  assert.equal(m.guidedOpenFor('', 'c1'), false);
  assert.equal(m.guidedOpenFor('', ''), false);
});

test('step stickers use the four fixed words, plus Waiting and Running before an answer, and nothing else', () => {
  const allowed = new Set([...WORDS, 'Waiting', 'Running']);
  const answer = result({ steps: Array.from({ length: 11 }, (_, i) => step(i, WORDS[i % 4])) });
  for (let number = 0; number <= 10; number += 1) {
    for (const phase of ['idle', 'running', 'done', 'failed']) {
      for (const r of [null, answer]) assert.ok(allowed.has(m.stickerFor(r, number, phase).word), `${number} ${phase}`);
    }
  }
});

test('the Manual tools windows panel does not fold itself when the first date is typed', () => {
  const fs = require('node:fs');
  const path = require('node:path');
  const src = fs.readFileSync(path.join(__dirname, '../src/lib/components/guided/ManualTools.svelte'), 'utf8');
  assert.ok(!/<details class="windows"[^>]*\bopen=\{store\.frame/.test(src), 'open must not follow the frame');
  assert.ok(/<details class="windows" bind:open=\{windowsOpen\}>/.test(src));
});

test('contract: the nearby page size never exceeds the server page limit', () => {
  const fs = require('node:fs');
  const path = require('node:path');
  const go = fs.readFileSync(path.join(__dirname, '../../internal/audit/models.go'), 'utf8');
  const m = /MaxPageSize\s*=\s*(\d+)/.exec(go);
  assert.ok(m, 'MaxPageSize not found in internal/audit/models.go');
  const serverMax = Number(m[1]);
  assert.ok(Number.isInteger(n.NEARBY_ROWS) && n.NEARBY_ROWS >= 1, 'a page of at least one row');
  assert.ok(n.NEARBY_ROWS <= serverMax, `the client asks for ${n.NEARBY_ROWS} rows, the server allows ${serverMax}`);
});

test('a day that cannot be compared leaves no table from an earlier day', () => {
  const answer = { id: 1 };
  const made = { outcome: 'no-data' };
  assert.equal(fix.verdictToShow(made, answer, answer, 'done', ''), made);
  // The overlap message is set: nothing else is shown, even if the old verdict was kept.
  assert.equal(fix.verdictToShow(made, answer, answer, 'done', 'The two windows overlap.'), null);
  assert.equal(fix.verdictToShow(made, answer, answer, 'idle', ''), null);
  assert.equal(fix.verdictToShow(made, answer, answer, 'running', ''), null);
  assert.equal(fix.verdictToShow(made, answer, answer, 'failed', ''), null);
  assert.equal(fix.verdictToShow(null, null, answer, 'done', ''), null);
  assert.equal(fix.verdictToShow(made, answer, { id: 1 }, 'done', ''), null);
});

test('the tab strip handles keys, and Left on the first tab goes to the last', () => {
  const m = loadTypeScript('src/lib/guided/stepModel.ts');
  assert.equal(m.nextTabIndex('ArrowLeft', 0, 5), 4);
  assert.equal(m.nextTabIndex('ArrowRight', 4, 5), 0);
  assert.equal(m.nextTabIndex('ArrowRight', 1, 5), 2);
  assert.equal(m.nextTabIndex('ArrowLeft', 3, 5), 2);
  assert.equal(m.nextTabIndex('Home', 3, 5), 0);
  assert.equal(m.nextTabIndex('End', 0, 5), 4);
  assert.equal(m.nextTabIndex('Enter', 0, 5), null);
  assert.equal(m.nextTabIndex('a', 2, 5), null);
  assert.equal(m.nextTabIndex('ArrowLeft', 0, 0), null);
});

test('a second press on the same key is ignored until the first finishes', () => {
  const { InFlight } = loadTypeScript('src/lib/guided/inflight.ts');
  const f = new InFlight();
  assert.equal(f.start('a'), true);
  assert.equal(f.start('a'), false);
  assert.equal(f.start('b'), true);
  f.finish('a');
  assert.equal(f.has('a'), false);
  assert.equal(f.start('a'), true);
});

test('Manual tools words describe the tools, not the steps; the sticker keeps a gap before Out of date', () => {
  const fs = require('node:fs');
  const path = require('node:path');
  const c = loadTypeScript('src/lib/copy/guidedCopy.ts');
  assert.ok(!/eleven steps/i.test(c.GUIDED_TEXT.introManual));
  assert.ok(/Eleven steps/.test(c.GUIDED_TEXT.intro));
  const src = fs.readFileSync(path.join(__dirname, '../src/lib/components/guided/GuidedAnalysis.svelte'), 'utf8');
  assert.ok(/store\.mode === 'guided' \? `\$\{T\.modeGuidedNow\} \$\{T\.intro\}` : `\$\{T\.modeManualNow\} \$\{T\.introManual\}`/.test(src));
  const sticker = fs.readFileSync(path.join(__dirname, '../src/lib/components/guided/Sticker.svelte'), 'utf8');
  assert.ok(/\.stale \{[^}]*margin-inline-start/.test(sticker), 'a visible gap before the dot');
  assert.ok(!/tone-waiting[^}]*background-size: 100% 100%/.test(sticker), 'the hatch must not run under the word');
});

test('switching mode keeps the keyboard on the page, and step pages cannot be pushed sideways by long text', () => {
  const fs = require('node:fs');
  const path = require('node:path');
  const read = (f) => fs.readFileSync(path.join(__dirname, '../src/lib/components/guided', f), 'utf8');
  const g = read('GuidedAnalysis.svelte');
  assert.ok(/<h2 tabindex="-1" bind:this=\{heading\}>/.test(g));
  assert.ok(/heading\?\.focus\(\)/.test(g));
  const shell = read('StepShell.svelte');
  for (const sel of ['.shell', '.nutshell', '.body', '.reasons']) {
    const line = shell.split('\n').find((l) => l.trim().startsWith(sel + ' {'));
    assert.ok(line && /minmax\(0, 1fr\)/.test(line), `${sel} needs a shrinkable column`);
  }
  assert.ok(/\.page \{[^}]*minmax\(0, 1fr\)/.test(g));
});
