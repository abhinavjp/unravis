const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const { loadTypeScript } = require('./helpers/loadTs.cjs');

// Blackbox charts: "What happened", status chips, plain help, quick questions, Latest compared and Everything.
// Text from the recorded PC is untrusted; every test of it uses hostile input.

const h = loadTypeScript('src/lib/blackboxHappened.ts');
const c = loadTypeScript('src/lib/blackboxCompare.ts');
const s = loadTypeScript('src/lib/blackboxSeries.ts');
const { BBC_TEXT } = loadTypeScript('src/lib/copy/blackboxChartsCopy.ts');
const { lintCopy } = loadTypeScript('src/lib/copy/copylint.ts');
const read = (p) => fs.readFileSync(path.join(__dirname, '..', p), 'utf8');

const T0 = Date.parse('2026-11-18T06:00:00Z');
const base = (extra = {}) => ({
  start: '2026-11-18T06:00:00Z', end: '2026-11-18T07:00:00Z', bucketSeconds: 15, intervalSeconds: 15, samples: 240,
  lines: [], groups: [], processes: [], eventIds: [], dumps: [], gaps: [], markers: [], truncated: false,
  read: { files: 1, samples: 240, torn: 0, dropped: null, missing: null }, ...extra
});

test('safeText keeps a file name only, drops control and direction characters, and cuts long text', () => {
  assert.equal(h.safeText('C:\\Windows\\Temp\\w3wp.dmp'), 'w3wp.dmp');
  assert.equal(h.safeText('/var/crash/secret/dump.dmp'), 'dump.dmp');
  assert.equal(h.safeText('a\u0000b\u202ec\nd'), 'a b c d');
  assert.equal(h.safeText('x'.repeat(500)).length, 60);
  assert.equal(h.safeText(undefined), '');
  assert.equal(h.safeText(42), '');
  // markup is not removed (the view prints text only) but it carries no folder part either
  assert.equal(h.safeText('<img src=x onerror=alert(1)>.dmp'), '<img src=x onerror=alert(1)>.dmp');
});

test('dumps, restarts and gaps become one list, newest first, with plain titles and no folders', () => {
  const a = base({
    dumps: [{ name: 'D:\\secret\\w3wp.exe.1.dmp', size: 412 * 1048576, mtime: '2026-11-18T06:30:00Z', target: 'C:\\Windows\\System32\\w3wp.exe' }],
    gaps: [{ from: '2026-11-18T06:10:00Z', to: '2026-11-18T06:12:00Z', seconds: 120, cause: 'crash' }],
    markers: [
      { t: '2026-11-18T06:12:10Z', kind: 'restart', detail: 'after-crash' }, // inside the crash gap: listed once
      { t: '2026-11-18T06:45:00Z', kind: 'clock-jump' },
      { t: '2026-11-18T06:50:00Z', kind: '<script>alert(1)</script>' }
    ]
  });
  const { items } = h.happenedItems(a);
  assert.deepEqual(items.map((i) => i.kind), ['note', 'note', 'dump', 'crash']);
  assert.equal(items[2].title, 'Crash dump: w3wp.exe.1.dmp');
  assert.equal(items[2].detail, 'Program: w3wp.exe. Size 412 MB.');
  assert.ok(!JSON.stringify(items).includes('secret'), 'no folder leaks');
  assert.equal(items[3].title, 'The recorder ended without a clean stop');
  assert.equal(items[3].detail, 'No samples for 2 min.');
  assert.equal(items[1].title, 'The PC clock jumped forward');
  assert.match(items[0].title, /^Recorder note: /);
  assert.ok(items[0].title.length < 70);
});

test('a restart note alone (too short to be a gap) is still listed, with the plain reason', () => {
  const { items } = h.happenedItems(base({ markers: [{ t: '2026-11-18T06:12:10Z', kind: 'restart', detail: 'after-stop' }] }));
  assert.equal(items.length, 1);
  assert.equal(items[0].kind, 'restart');
  assert.equal(items[0].detail, 'The earlier run had stopped cleanly.');
  // an unknown detail code is not shown
  assert.equal(h.happenedItems(base({ markers: [{ t: '2026-11-18T06:12:10Z', kind: 'restart', detail: '<b>x</b>' }] })).items[0].detail, '');
});

test('bad times are dropped and the list is capped, with the rest counted', () => {
  const many = Array.from({ length: 200 }, (_, i) => ({ name: 'd' + i + '.dmp', size: 1, mtime: new Date(T0 + i * 1000).toISOString() }));
  const r = h.happenedItems(base({ dumps: [...many, { name: 'bad.dmp', size: 1, mtime: 'nope' }] }));
  assert.equal(r.items.length, h.MAX_LISTED);
  assert.equal(r.more, 200 - h.MAX_LISTED);
  assert.equal(r.items[0].title, 'Crash dump: d199.dmp');
  assert.ok(h.MAX_DRAWN <= 60 && h.MAX_EVENT_DOTS <= 120);
});

test('Windows events are listed by severity then count, with a meaning only for well-known IDs', () => {
  const { rows } = h.eventRows([
    { channel: 2, eventId: 1000, level: 2, count: 2 }, { channel: 1, eventId: 41, level: 1, count: 1 },
    { channel: 1, eventId: 9999, level: 3, count: 5 }, { channel: 7, eventId: 5, level: 9, count: 1 }
  ]);
  assert.deepEqual(rows.map((r) => r.eventId), [41, 1000, 9999, 5]);
  assert.equal(rows[0].log, 'System');
  assert.equal(rows[0].level, 'Critical');
  assert.match(rows[0].meaning, /without a clean shutdown/);
  assert.equal(rows[2].meaning, '');
  assert.equal(rows[3].log, 'Log 7');
  assert.equal(rows[3].level, 'Level 9');
});

test('event dots come from the per-level count lines, capped, severe ones kept first', () => {
  const pt = (i, v) => [T0 + i * 1000, v, v, v];
  const lines = [
    { key: 'events.level2', points: Array.from({ length: 100 }, (_, i) => pt(i, 1)) },
    { key: 'events.level3', points: Array.from({ length: 100 }, (_, i) => pt(i + 1000, 2)) },
    { key: 'events.lost', points: [pt(5, 3)] }, { key: 'cpu', points: [pt(5, 3)] }, { key: 'events.level1', points: [pt(3, 0)] }
  ];
  const r = h.eventDots(lines);
  assert.equal(r.dots.length, h.MAX_EVENT_DOTS);
  assert.equal(r.more, 200 - h.MAX_EVENT_DOTS);
  assert.equal(r.dots.filter((d) => d.level === 2).length, 100, 'errors are kept before warnings');
  assert.ok(r.dots.every((d, i, a) => !i || a[i - 1].t <= d.t), 'in time order');
});

test('recording health says samples, gaps, unread files and the Blackbox state in plain words', () => {
  const rows = h.healthRows(base({
    samples: 200, gaps: [{ from: '2026-11-18T06:10:00Z', to: '2026-11-18T06:12:00Z', seconds: 120, cause: 'late' }],
    read: { files: 3, samples: 200, torn: 2, dropped: ['a'], missing: ['b'] }, state: { label: 'Ready', detail: 'Recording now.', tone: 'ok' }
  }));
  const by = Object.fromEntries(rows.map((r) => [r.label, r]));
  assert.equal(by['Samples in this range'].value, '200 of about 240 (83 %)');
  assert.equal(by['Samples in this range'].tone, 'warn');
  assert.equal(by['Time with no recording'].value, '2 min in 1 gap');
  assert.equal(by['Recording files'].value, '2 of 3 could not be read to the end');
  assert.equal(by['Damaged lines'].value, '2 skipped');
  assert.equal(by['Blackbox state'].value, 'Ready: Recording now.');
  assert.equal(h.healthRows(base()).find((r) => r.label === 'Time with no recording').value, 'None');
});

test('status chips: Saved copy, Running late, Partly ready, each only when true', () => {
  const now = Date.parse('2026-11-18T07:00:00Z');
  const live = { state: 'running', intervalSeconds: 15, effectiveIntervalSeconds: 15, lastSample: '2026-11-18T06:59:50Z' };
  assert.deepEqual(h.statusChips(base(), live, now), []);
  assert.deepEqual(h.statusChips(base(), { ...live, state: 'stopped' }, now).map((x) => x.id), ['saved']);
  assert.deepEqual(h.statusChips(base(), { ...live, lastSample: '2026-11-18T05:00:00Z' }, now).map((x) => x.id), ['saved']);
  assert.deepEqual(h.statusChips(base(), { ...live, effectiveIntervalSeconds: 60 }, now).map((x) => x.id), ['late']);
  assert.deepEqual(h.statusChips(base({ gaps: [{ from: 'x', to: 'y', seconds: 5, cause: 'late' }] }), live, now).map((x) => x.id), ['late']);
  assert.deepEqual(h.statusChips(base({ read: { files: 2, samples: 1, torn: 0, dropped: ['f'], missing: null } }), live, now).map((x) => x.id), ['partly']);
  assert.deepEqual(h.statusChips(base({ groups: [{ group: 'clr', read: 0, missing: 0, slow: 4, busy: 0 }] }), live, now).map((x) => x.id), ['partly']);
  // a group nobody asked for (nothing missing, slow or busy) is not "partly"
  assert.deepEqual(h.statusChips(base({ groups: [{ group: 'clr', read: 0, missing: 0, slow: 0, busy: 0 }] }), live, now), []);
  assert.deepEqual(h.statusChips(null, undefined, now), []);
  for (const chip of h.statusChips(base({ truncated: true }), { ...live, state: 'stopped', effectiveIntervalSeconds: 60 }, now)) assert.ok(chip.hint.length > 20 && chip.text.length < 20);
});

test('the series reader keeps the state sentence for the health rows', () => {
  const a = s.readSeriesAnswer({ ...base(), state: { label: 'Ready', detail: 'ok', tone: 'ok', extra: 1 } });
  assert.deepEqual(a.state, { label: 'Ready', detail: 'ok', tone: 'ok' });
  assert.equal(s.readSeriesAnswer(base()).state, undefined);
});

test('every counter with a plain name has a plain meaning, and an unknown one still gets a sentence', () => {
  const names = read('src/lib/blackboxSeries.ts');
  const fields = [...names.slice(names.indexOf('const FIELD_NAMES'), names.indexOf('const EVENT_LEVELS')).matchAll(/(\w+): '/g)].map((x) => x[1]);
  assert.ok(fields.length >= 75);
  const missing = fields.filter((f) => !['pingLatencyMs'].includes(f)).filter((f) => c.meaningOf('g.x.' + f, 'count') === 'A count.');
  assert.deepEqual(missing, [], 'add a sentence for these in blackboxCompare.ts');
  assert.equal(c.meaningOf('newGroup.thing', 'ms'), 'A time in milliseconds.');
  assert.match(c.meaningOf('tcp.states.time-wait', 'count'), /"time-wait" state/);
  assert.match(c.meaningOf('events.level2', 'count'), /Error events/);
  assert.equal(c.meaningOf('x', 'furlongs'), 'A count.');
});

test('quick questions pick the counters that were recorded, in order, at most six, and nothing when none', () => {
  const lines = ['cpu', 'diskCpu.cpu.queueLength', 'memAvail', 'diskMs', 'diskQueue', 'diskCpu.C:.readMs', 'diskCpu.D:.readMs', 'diskCpu.C:.writeMs', 'events.level2'].map((key) => ({ key }));
  const q = Object.fromEntries(c.QUICK_QUESTIONS.map((x) => [x.id, x]));
  assert.deepEqual(c.quickKeys(q.cpu, lines), ['cpu', 'diskCpu.cpu.queueLength']);
  assert.deepEqual(c.quickKeys(q.mem, lines), ['memAvail']);
  assert.equal(c.quickKeys(q.disk, lines).length, 5);
  const wide = [...lines, ...['E:', 'F:', 'G:'].map((d) => ({ key: 'diskCpu.' + d + '.readMs' }))];
  assert.equal(c.quickKeys(q.disk, wide).length, c.QUICK_MAX);
  assert.deepEqual(c.quickKeys(q.events, lines), ['events.level2']);
  assert.deepEqual(c.quickKeys(q.web, lines), []);
  assert.ok(c.QUICK_QUESTIONS.every((x) => /\?$/.test(x.label) && x.hint.length > 20));
});

test('Latest compared puts the latest value between the counter\'s own lowest and highest', () => {
  const p = (v, lo, hi) => [T0, v, lo, hi];
  assert.equal(c.compareLatest([p(10, 10, 10), [T0 + 1, 100, 100, 100]]).position, 'high');
  assert.equal(c.compareLatest([p(100, 100, 100), [T0 + 1, 10, 10, 10]]).position, 'low');
  assert.equal(c.compareLatest([p(10, 10, 10), [T0 + 1, 100, 100, 100], [T0 + 2, 55, 55, 55]]).position, 'usual');
  assert.equal(c.compareLatest([p(7, 7, 7), [T0 + 1, 7, 7, 7]]).position, 'flat');
  assert.equal(c.compareLatest([]), null);
  const r = c.compareLatest([p(0, 0, 0), [T0 + 1, 50, 50, 50]]);
  assert.equal(r.at, 1);
  assert.deepEqual(['high', 'usual', 'low', 'flat'].map((k) => c.POSITION_WORDS[k]), ['Near its highest', 'In its usual range', 'Near its lowest', 'Did not change']);
});

test('the highest value comes with the time of the bucket that held it', () => {
  const f = s.lineFigures([[T0, 1, 1, 5], [T0 + 15000, 2, 1, 9], [T0 + 30000, 3, 1, 4]]);
  assert.equal(f.max, 9);
  assert.equal(f.maxAt, T0 + 15000);
});

test('shapePath is empty for one point, flat for a flat line, and never more than 30 points', () => {
  assert.equal(c.shapePath([[T0, 1, 1, 1]]), '');
  assert.match(c.shapePath([[T0, 5, 5, 5], [T0 + 1, 5, 5, 5]]), /^M0\.0 9\.0L80\.0 9\.0$/);
  const long = Array.from({ length: 600 }, (_, i) => [T0 + i, i, i, i]);
  assert.ok((c.shapePath(long).match(/[ML]/g) || []).length <= 30);
});

test('the new words pass the copy lint and the scale buttons use the plain names with a tooltip each', () => {
  const items = Object.entries(BBC_TEXT).map(([id, v]) => ({ id, text: typeof v === 'function' ? v('Some text', 'B') : v }));
  assert.deepEqual(lintCopy(items, []).filter((f) => f.severity === 'error'), []);
  const extra = [...c.QUICK_QUESTIONS.map((x) => ({ id: 'q-' + x.id, text: x.label + ' ' + x.hint }))];
  assert.deepEqual(lintCopy(extra, []).filter((f) => f.severity === 'error'), []);
  assert.equal(BBC_TEXT.linear, 'Even steps');
  assert.equal(BBC_TEXT.log, 'Times-10 steps');
  assert.match(BBC_TEXT.linearHelp, /linear scale/);
  assert.match(BBC_TEXT.logHelp, /log scale/);
  const view = read('src/components/BlackboxCharts.svelte');
  assert.match(view, /title=\{T\.linearHelp\}[\s\S]*?>\{T\.linear\}/);
  assert.match(view, /title=\{T\.logHelp\}[\s\S]*?>\{T\.log\}/);
});

test('copy: no double spaces, every string ends cleanly, no leftover "Linear" or "Log scale" wording', () => {
  for (const [k, v] of Object.entries(BBC_TEXT)) {
    const t = typeof v === 'function' ? v('Some text', 'B') : v;
    assert.ok(!/\s{2,}/.test(t), k + ' has a double space');
    assert.ok(t === t.trim(), k + ' has stray space');
    assert.ok(!/\b(teh|recieve|seperate|occured)\b/i.test(t), k + ' has a typo');
  }
  assert.doesNotMatch(Object.values(BBC_TEXT).filter((v) => typeof v === 'string').join(' '), /Switch to Linear/);
});

test('the view prints recorded text as plain text, frees the chart, caps what it draws and adds no network path', () => {
  const v = read('src/components/BlackboxCharts.svelte');
  assert.doesNotMatch(v, /\{@html/, 'no raw HTML anywhere');
  assert.doesNotMatch(v, /innerHTML|insertAdjacentHTML|document\.write/);
  assert.doesNotMatch(v, /fetch\(|XMLHttpRequest|WebSocket|EventSource|sendBeacon/);
  assert.match(v, /made\.off\('datazoom', onZoom\)/);
  assert.match(v, /made\.dispose\(\)/);
  assert.match(v, /observer\.disconnect\(\)/);
  assert.match(v, /\.slice\(0, MAX_LINES\)/);
  assert.match(v, /MAX_DRAWN/);
  assert.match(v, /renderMode: 'richText'/, 'the chart tooltip is plain text');
  // the only server call is the existing series route
  assert.equal((v.match(/blackboxApi\./g) || []).length, 1);
});

test('every button in the view has words or a label, and every control has a hint', () => {
  const v = read('src/components/BlackboxCharts.svelte');
  const buttons = [...v.matchAll(/<button\b([^>]*)>([^<]*)/g)];
  assert.ok(buttons.length >= 20);
  for (const [, attrs, text] of buttons) {
    assert.ok(/aria-label=/.test(attrs) || text.trim().length > 0, 'unnamed button: ' + attrs);
    assert.ok(/title=/.test(attrs) || /aria-label=/.test(attrs) || /\bclass="(quiet|secondary)-button"/.test(attrs), 'no hint: ' + attrs);
  }
  for (const [, attrs] of [...v.matchAll(/<(input|select)\b([^>]*)>/g)].map((m) => [m[0], m[2]])) {
    if (/type="checkbox"/.test(attrs)) continue;
    assert.ok(/aria-label=|title=/.test(attrs) || /bbc-field/.test(v), 'unlabelled field: ' + attrs);
  }
});
