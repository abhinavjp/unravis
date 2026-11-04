const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createRequire } = require('node:module');
const test = require('node:test');
const ts = require('typescript');

// The time-window picker: the pure rules in pickerModel.ts, its words, and the wiring into the screens.

const root = path.resolve(__dirname, '..');
const cache = new Map();
function loadTypeScript(relativePath) {
  const filename = path.join(root, relativePath);
  if (cache.has(filename)) return cache.get(filename).exports;
  const source = fs.readFileSync(filename, 'utf8');
  const output = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022, esModuleInterop: true } }).outputText;
  const module = { exports: {} };
  cache.set(filename, module);
  const nodeRequire = createRequire(filename);
  const load = (request) => {
    if (/^\.\.?\//.test(request)) {
      const plain = path.join(path.dirname(filename), request + '.ts');
      if (fs.existsSync(plain)) return loadTypeScript(path.relative(root, plain));
    }
    return nodeRequire(request);
  };
  new Function('require', 'module', 'exports', output)(load, module, module.exports);
  return module.exports;
}
const read = (relativePath) => fs.readFileSync(path.join(root, relativePath), 'utf8');
const m = loadTypeScript('src/lib/tracing/pickerModel.ts');
const windows = loadTypeScript('src/lib/tracing/windows.ts');

const NOW = Date.UTC(2026, 9, 20, 9, 0, 40); // 2026-10-20 09:00:40 UTC

test('quick ranges end at the floored minute and look back by their length', () => {
  const q = m.incidentQuickRanges(NOW);
  assert.deepEqual(q.map((r) => r.label), ['Last 15 min', 'Last hour (default)', 'Last 4 hours', 'Last 24 hours', 'Last 7 days', 'Last 30 days']);
  const byId = Object.fromEntries(q.map((r) => [r.id, r.range]));
  assert.deepEqual(byId['15m'], { start: '2026-10-20T08:45', end: '2026-10-20T09:00' });
  assert.deepEqual(byId['1h'], { start: '2026-10-20T08:00', end: '2026-10-20T09:00' });
  assert.deepEqual(byId['4h'], { start: '2026-10-20T05:00', end: '2026-10-20T09:00' });
  assert.deepEqual(byId['24h'], { start: '2026-10-19T09:00', end: '2026-10-20T09:00' });
  assert.deepEqual(byId['7d'], { start: '2026-10-13T09:00', end: '2026-10-20T09:00' });
  assert.deepEqual(byId['30d'], { start: '2026-09-20T09:00', end: '2026-10-20T09:00' });
});

test('every quick range passes the same window checks as a typed one', () => {
  for (const q of m.incidentQuickRanges(NOW)) {
    const cmp = m.equalPeriodBefore(q.range);
    const problems = windows.checkWindows({ incidentStart: q.range.start, incidentEnd: q.range.end, referenceStart: cmp.start, referenceEnd: cmp.end }).errors;
    assert.deepEqual(problems, [], q.id);
  }
});

test('comparison ranges: the equal period before, and the same time yesterday', () => {
  const incident = { start: '2026-10-20T08:00', end: '2026-10-20T09:30' };
  assert.deepEqual(m.equalPeriodBefore(incident), { start: '2026-10-20T06:30', end: '2026-10-20T08:00' });
  assert.deepEqual(m.sameTimeYesterday(incident), { start: '2026-10-19T08:00', end: '2026-10-19T09:30' });
  const quick = m.comparisonQuickRanges(incident);
  assert.deepEqual(quick.map((r) => r.id), ['before', 'yesterday']);
  assert.equal(m.equalPeriodBefore({ start: '', end: '' }), null);
  assert.equal(m.sameTimeYesterday({ start: '2026-10-20T09:00', end: '2026-10-20T08:00' }), null);
  assert.equal(m.comparisonQuickRanges({ start: 'x', end: 'y' })[0].range, null);
});

test('calendar grid: leading blanks, 28/29/30/31 days', () => {
  const oct = m.monthGrid(2026, 9);
  assert.equal(oct.title, 'October 2026');
  assert.equal(oct.days.length, 31);
  assert.equal(oct.lead, 4, 'October 1 2026 is a Thursday, the week starts on Sunday');
  assert.equal(oct.days[25].date, '2026-10-26');
  assert.equal(m.monthGrid(2026, 1).days.length, 28);
  assert.equal(m.monthGrid(2026, 1).lead, 0, 'February 1 2026 is a Sunday');
  assert.equal(m.monthGrid(2028, 1).days.length, 29, 'a leap year');
  assert.equal(m.monthGrid(2100, 1).days.length, 28, '2100 is not a leap year');
  assert.equal(m.monthGrid(2000, 1).days.length, 29, '2000 is');
  assert.equal(m.monthGrid(2026, 8).days.length, 30);
  assert.equal(m.monthGrid(2026, 11).days.length, 31);
  assert.equal(m.monthGrid(2026, 5).lead, 1);
});

test('months step across a year edge', () => {
  assert.deepEqual(m.shiftMonth(2026, 11, 1), { year: 2027, month: 0 });
  assert.deepEqual(m.shiftMonth(2026, 0, -1), { year: 2025, month: 11 });
  assert.deepEqual(m.shiftMonth(2026, 5, -17), { year: 2025, month: 0 });
  assert.equal(m.dayName('2026-10-26'), 'October 26, 2026');
});

test('applying a range: the first click starts, the second ends, an earlier second click swaps', () => {
  let pick = { startDay: '', endDay: '', picking: false };
  pick = m.pickDay(pick, '2026-10-12');
  assert.deepEqual(pick, { startDay: '2026-10-12', endDay: '', picking: true });
  assert.equal(m.dayRole(pick, '2026-10-12'), 'start');
  const done = m.pickDay(pick, '2026-10-15');
  assert.deepEqual(done, { startDay: '2026-10-12', endDay: '2026-10-15', picking: false });
  assert.equal(m.dayRole(done, '2026-10-13'), 'between');
  assert.equal(m.dayRole(done, '2026-10-15'), 'end');
  assert.equal(m.dayRole(done, '2026-10-20'), 'none');
  const swapped = m.pickDay(pick, '2026-10-05');
  assert.deepEqual(swapped, { startDay: '2026-10-05', endDay: '2026-10-12', picking: false });
  assert.deepEqual(m.pickDay(done, '2026-10-30'), { startDay: '2026-10-30', endDay: '', picking: true }, 'a third click starts again');
  const same = m.pickDay(pick, '2026-10-12');
  assert.deepEqual(same, { startDay: '2026-10-12', endDay: '2026-10-12', picking: false }, 'one day is allowed (the times make it a range)');
});

test('the calendar days replace the date boxes and leave the times', () => {
  const text = { startDate: '2026-10-01', startTime: '10:30', endDate: '2026-10-01', endTime: '11:00' };
  assert.deepEqual(m.draftWithDays(text, { startDay: '2026-10-12', endDay: '2026-10-15', picking: false }), { startDate: '2026-10-12', startTime: '10:30', endDate: '2026-10-15', endTime: '11:00' });
  assert.equal(m.draftWithDays(text, { startDay: '2026-10-12', endDay: '', picking: true }).endDate, '2026-10-12', 'one click so far sets both days');
});

test('stamps round trip and a day that does not exist is refused', () => {
  const ms = Date.UTC(2026, 9, 26, 10, 30);
  assert.equal(m.formatStamp(ms), '2026-10-26T10:30');
  assert.equal(m.parseStamp('2026-10-26T10:30'), ms);
  assert.equal(m.parseStamp(m.formatStamp(ms)), ms);
  assert.ok(Number.isNaN(m.parseStamp('2026-02-31T10:00')));
  assert.ok(Number.isNaN(m.parseStamp('2026-02-29T10:00')), '2026 is not a leap year');
  assert.ok(Number.isFinite(m.parseStamp('2028-02-29T10:00')));
  assert.ok(Number.isNaN(m.parseStamp('2026-10-26T24:00')));
  assert.ok(Number.isNaN(m.parseStamp('2026-10-26 10:30')));
  assert.ok(Number.isNaN(m.parseStamp('')));
  assert.equal(m.isDateText('2026-02-31'), false);
  assert.equal(m.isDateText('2026-10-26'), true);
  assert.equal(m.isDateText('26/10/2026'), false);
  assert.equal(m.isTimeText('23:59'), true);
  assert.equal(m.isTimeText('24:00'), false);
  assert.equal(m.isTimeText('9:30'), false);
  assert.deepEqual(m.splitStamp('2026-10-26T10:30'), { date: '2026-10-26', time: '10:30' });
  assert.deepEqual(m.splitStamp(''), { date: '', time: '' });
});

test('typing: a good pair is the value, an empty box clears, bad text keeps the previous value and is marked', () => {
  const prev = '2026-10-26T10:30';
  assert.deepEqual(m.readParts('2026-10-27', '08:15', prev), { value: '2026-10-27T08:15', badDate: false, badTime: false });
  assert.deepEqual(m.readParts('', '08:15', prev), { value: '', badDate: false, badTime: false });
  assert.deepEqual(m.readParts('2026-10-27', '', prev), { value: '', badDate: false, badTime: false });
  assert.deepEqual(m.readParts('2026-02-31', '08:15', prev), { value: prev, badDate: true, badTime: false });
  assert.deepEqual(m.readParts('2026-10-2', '08:15', prev), { value: prev, badDate: true, badTime: false });
  assert.deepEqual(m.readParts('2026-10-27', '25:00', prev), { value: prev, badDate: false, badTime: true });
  assert.deepEqual(m.readParts('x', 'y', prev), { value: prev, badDate: true, badTime: true });
  assert.equal(m.readParts(' 2026-10-27 ', ' 08:15 ', prev).value, '2026-10-27T08:15', 'spaces around the text are ignored');
});

test('syncing with an outside value: a bad box keeps its text, the good box follows', () => {
  const outside = '2026-10-27T08:15';
  assert.deepEqual(m.syncParts('2026-02-31', '10:30', outside), { date: '2026-02-31', time: '08:15', badDate: true, badTime: false, changed: true }, 'bad date, good time: the time is replaced, the date text stays');
  assert.deepEqual(m.syncParts('2026-10-26', '25:00', outside), { date: '2026-10-27', time: '25:00', badDate: false, badTime: true, changed: true }, 'bad time, good date: the date is replaced, the time text stays');
  assert.deepEqual(m.syncParts('2026-02-31', '25:00', outside), { date: '2026-02-31', time: '25:00', badDate: true, badTime: true, changed: false }, 'both bad: nothing moves');
  assert.deepEqual(m.syncParts('2026-10-27', '08:15', outside), { date: '2026-10-27', time: '08:15', badDate: false, badTime: false, changed: false }, 'nothing bad and the value is equal: unchanged');
  assert.deepEqual(m.syncParts('2026-10-26', '10:30', outside), { date: '2026-10-27', time: '08:15', badDate: false, badTime: false, changed: true }, 'nothing bad and the value differs: both boxes are replaced');
  assert.deepEqual(m.syncParts('2026-02-31', '10:30', ''), { date: '2026-02-31', time: '', badDate: true, badTime: false, changed: true }, 'an empty outside value clears the valid box');
});

test('a cleared box still produces the screen\'s own "needs a start and an end, in UTC" message', () => {
  const cleared = m.readParts('', '08:15', '2026-10-26T10:30').value;
  const checked = windows.checkWindows({ incidentStart: cleared, incidentEnd: '2026-10-26T11:00', referenceStart: '2026-10-26T08:00', referenceEnd: '2026-10-26T09:00' });
  assert.ok(JSON.stringify(checked).includes('needs a start and an end, in UTC'), JSON.stringify(checked));
});

test('draft: an end that is not after the start is refused, an unfinished box is incomplete', () => {
  const ok = { startDate: '2026-10-26', startTime: '10:30', endDate: '2026-10-26', endTime: '11:00' };
  assert.deepEqual(m.readDraft(ok), { state: 'ok', range: { start: '2026-10-26T10:30', end: '2026-10-26T11:00' } });
  assert.equal(m.readDraft({ ...ok, endTime: '10:30' }).state, 'order', 'end equals start');
  assert.equal(m.readDraft({ ...ok, endTime: '09:00' }).state, 'order');
  assert.equal(m.readDraft({ ...ok, endDate: '2026-10-25' }).state, 'order');
  assert.equal(m.readDraft({ ...ok, endDate: '2026-10-27', endTime: '09:00' }).state, 'ok', 'a later day is fine whatever the time');
  assert.deepEqual(m.readDraft({ ...ok, startDate: '' }), { state: 'incomplete', range: null });
  assert.equal(m.readDraft({ ...ok, endTime: '9' }).state, 'incomplete');
  assert.equal(m.readDraft({ ...ok, startDate: '2026-02-31' }).state, 'incomplete');
  assert.deepEqual(m.draftFrom({ start: '2026-10-26T10:30', end: '2026-10-27T02:00' }), { startDate: '2026-10-26', startTime: '10:30', endDate: '2026-10-27', endTime: '02:00' });
});

test('the summary and the span read in plain words', () => {
  assert.equal(m.rangeText({ start: '2026-10-26T10:30', end: '2026-10-26T11:00' }), '26 Oct 2026 10:30 → 11:00');
  assert.equal(m.rangeText({ start: '2026-10-26T10:30', end: '2026-10-27T02:00' }), '26 Oct 2026 10:30 → 27 Oct 2026 02:00');
  assert.equal(m.rangeText({ start: '2026-10-26T10:30', end: '2026-10-26T10:30' }), '');
  assert.equal(m.rangeText({ start: '', end: '' }), '');
  assert.equal(m.rangeSpanText({ start: '2026-10-26T10:30', end: '2026-10-26T11:00' }), '30 min');
  assert.equal(m.formatSpan(60 * 60_000), '1 h');
  assert.equal(m.formatSpan(150 * 60_000), '2 h 30 min');
  assert.equal(m.formatSpan(3 * 86_400_000), '3 days');
  assert.equal(m.formatSpan(30 * 3_600_000), '1 day 6 h');
  assert.equal(m.formatSpan(24 * 3_600_000 + 59 * 60_000), '1 day 59 min');
  assert.equal(m.formatSpan(30 * 3_600_000 + 30 * 60_000), '1 day 6 h 30 min');
  assert.equal(m.formatSpan(2 * 86_400_000 + 60_000), '2 days 1 min');
});

test('the picker words pass the copy lint and carry no banned word', () => {
  const { lintScreenStrings } = loadTypeScript('src/lib/copy/copylint.ts');
  const { deck } = loadTypeScript('src/lib/copy/deck.ts');
  const { pickerCopyEntries, PICKER_TEXT, dayLabel, partLabel, triggerLabel, dialogLabel } = loadTypeScript('src/lib/copy/pickerCopy.ts');
  const entries = pickerCopyEntries();
  assert.ok(entries.length > 20);
  assert.deepEqual(lintScreenStrings(entries, deck.terms), []);
  assert.equal(PICKER_TEXT.zone, 'UTC');
  assert.equal(dayLabel('October 26, 2026', 'start'), 'October 26, 2026, start');
  assert.equal(dayLabel('October 26, 2026', 'end'), 'October 26, 2026, end');
  assert.equal(dayLabel('October 26, 2026', 'none'), 'October 26, 2026');
  assert.equal(partLabel('Incident window', 'start date'), 'Incident window start date');
  assert.equal(triggerLabel('Incident window', ''), 'Incident window: Choose a period, UTC');
  assert.equal(dialogLabel('Incident window'), 'Choose the incident window (UTC)');
});

test('wiring: the screens use the picker and no browser date-time box is left', () => {
  assert.match(read('src/components/TracingPaper.svelte'), /import WindowPicker from '\.\.\/lib\/components\/WindowPicker\.svelte'/);
  assert.match(read('src/lib/components/guided/Step0Frame.svelte'), /import WindowPicker from '\.\.\/WindowPicker\.svelte'/);
  assert.match(read('src/lib/components/guided/Step0Frame.svelte'), /import StampField from '\.\.\/StampField\.svelte'/);
  assert.match(read('src/lib/components/guided/NearbyActivity.svelte'), /<StampField/);
  assert.match(read('src/components/BlackboxHostCard.svelte'), /<WindowPicker/);
  const found = [];
  const walk = (dir) => {
    for (const entry of fs.readdirSync(path.join(root, dir), { withFileTypes: true })) {
      const rel = dir + '/' + entry.name;
      if (entry.isDirectory()) walk(rel);
      else if (/\.(svelte|ts|css)$/.test(entry.name) && /type=["']datetime-local["']|input\[type=["']datetime-local["']\]/.test(read(rel))) found.push(rel);
    }
  };
  walk('src');
  assert.deepEqual(found, [], 'no <input type="datetime-local"> and no CSS selector for one');
});

test('wiring: the picker keeps its keyboard and screen-reader contract', () => {
  const picker = read('src/lib/components/WindowPicker.svelte');
  assert.match(picker, /role="dialog"/);
  assert.match(picker, /aria-label=\{dialogLabel\(label\)\}/);
  assert.match(picker, /aria-pressed/);
  assert.match(picker, /dayLabel\(/);
  assert.match(picker, /aria-invalid/);
  assert.match(picker, /focus-visible/);
  assert.match(picker, /Escape/);
  assert.match(picker, /aria-haspopup="dialog"/);
  assert.match(picker, /max-width: 760px/);
  const stamp = read('src/lib/components/StampField.svelte');
  assert.match(stamp, /aria-label=\{`\$\{label\} date`\}/);
  assert.ok(stamp.includes('<TimeBox label={`${label} time`}'));
  assert.match(stamp, /aria-invalid/);
});

test('wiring: Escape closes the popover from anywhere, the boxes allow : and -, the hint sits with both variants', () => {
  const picker = read('src/lib/components/WindowPicker.svelte');
  assert.match(picker, /<svelte:window[^>]*onkeydown=\{onWindowKey\}[^>]*\/>/, 'the window element carries the key handler');
  assert.match(picker, /function onWindowKey\(event: KeyboardEvent\) \{\s*if \(open && event\.key === 'Escape'\) closePopover\(true\);\s*\}/, 'the handler checks open and Escape and refocuses the trigger');
  const stamp = read('src/lib/components/StampField.svelte');
  for (const source of [picker, stamp]) {
    assert.ok(!source.includes('inputmode="numeric"'), 'a digits-only keypad cannot type : or -');
    assert.ok(source.includes('inputmode="text"'));
  }
  assert.equal(picker.match(/id=\{hintId\}/g).length, 2, 'the hint element exists in the popover branch and the inline branch');
});

test('wiring: Enter applies the draft from a text box only; on a select it is left to the select', () => {
  const picker = read('src/lib/components/WindowPicker.svelte');
  const fn = /function onDialogKey\(event: KeyboardEvent\) \{([\s\S]*?)\n  \}/.exec(picker);
  assert.ok(fn, 'onDialogKey exists');
  assert.match(fn[1], /event\.key === 'Enter' && event\.target instanceof HTMLInputElement/);
  assert.ok(!fn[1].includes('HTMLSelectElement'), 'Enter on a select is no longer intercepted');
  assert.match(fn[1], /event\.key !== 'Escape'/, 'the Escape handling stands');
});
