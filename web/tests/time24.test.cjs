const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const { loadTypeScript } = require('./helpers/loadTs.cjs');

// BL-495 part: the 24-hour time box of the When filters (BL-181). Logic tests of the parser and the arrow-key step, the range a typed value
// produces through the same zone code the native box fed, and source pins that no `type="time"` box is left. Everything here is synthetic.
const root = path.resolve(__dirname, '..');
const t24 = loadTypeScript('src/lib/time24.ts');
const zi = loadTypeScript('src/lib/zonedInput.ts');
const read = (relative) => fs.readFileSync(path.join(root, relative), 'utf8');

test('TIME24-01: 24-hour text becomes HH:MM:SS.mmm, whatever short form was typed', () => {
  assert.equal(t24.parseTime24('00:00'), '00:00:00.000');
  assert.equal(t24.parseTime24('12:00'), '12:00:00.000');
  assert.equal(t24.parseTime24('13:05'), '13:05:00.000');
  assert.equal(t24.parseTime24('8:45'), '08:45:00.000', 'one digit of hour is fine');
  assert.equal(t24.parseTime24('23:59:59.999'), '23:59:59.999');
  assert.equal(t24.parseTime24('23:59:59'), '23:59:59.000');
  assert.equal(t24.parseTime24('10:00:30.5'), '10:00:30.500', 'a short fraction is padded on the right, as the native box did');
  assert.equal(t24.parseTime24('10:00:30.05'), '10:00:30.050');
  assert.equal(t24.parseTime24('10:00:30,25'), '10:00:30.250', 'a comma point from a pasted log line');
  assert.equal(t24.parseTime24('  13:05  '), '13:05:00.000', 'a paste with spaces round it');
});

test('TIME24-02: empty, 12-hour and malformed text is refused, never guessed', () => {
  for (const bad of ['', ' ', '24:00', '25:10', '12:60', '12:30:60', '8:45 AM', '8:45 pm', '08:45:00.000 AM', '1305', '13', '13:5', '13:05:3', 'ab:cd', '13:05:30.1234', '-1:00', '13.05', '13:05:30.']) {
    assert.equal(t24.parseTime24(bad), null, JSON.stringify(bad));
  }
});

test('TIME24-03: arrow keys step the segment under the caret and wrap inside it', () => {
  assert.deepEqual(t24.stepTime24('13:05:30.250', 1, 1), { text: '14:05:30.250', select: [0, 2] });
  assert.deepEqual(t24.stepTime24('23:05:30.250', 0, 1), { text: '00:05:30.250', select: [0, 2] }, 'hour 23 + 1 is 00, the day does not change here');
  assert.deepEqual(t24.stepTime24('00:05:30.250', 2, -1), { text: '23:05:30.250', select: [0, 2] }, 'a caret on the colon steps the hour before it');
  assert.deepEqual(t24.stepTime24('13:59:30.250', 4, 1), { text: '13:00:30.250', select: [3, 5] });
  assert.deepEqual(t24.stepTime24('13:00:30.250', 4, -1), { text: '13:59:30.250', select: [3, 5] });
  assert.deepEqual(t24.stepTime24('13:05:59.250', 7, 1), { text: '13:05:00.250', select: [6, 8] });
  assert.deepEqual(t24.stepTime24('13:05:30.999', 11, 1), { text: '13:05:30.000', select: [9, 12] });
  assert.deepEqual(t24.stepTime24('13:05:30.000', 12, -1), { text: '13:05:30.999', select: [9, 12] });
  assert.deepEqual(t24.stepTime24('13:05', 1, 1), { text: '14:05:00.000', select: [0, 2] }, 'a short form is normalised first');
  assert.equal(t24.stepTime24('nonsense', 1, 1), null);
  assert.equal(t24.stepTime24('', 0, 1), null);
});

// The wire value: what the old native box handed to fromZonedParts (its own shortest value) and what the new box hands it must give the same instant.
const native = (typed) => typed; // `<input type="time" step="0.001">` reported HH:MM, HH:MM:SS or HH:MM:SS.mmm; none of them is 12-hour.
const asked = (date, typed, zone, previous) => zi.fromZonedParts(date, t24.parseTime24(typed), zone, previous);
const wasAsked = (date, typed, zone, previous) => zi.fromZonedParts(date, native(typed), zone, previous);

test('TIME24-04: a typed time produces the same range the native box produced, in UTC and in the Times in zones', () => {
  const cases = [
    ['2026-10-20', '10:00', 'Europe/London', '2026-10-20T09:00:00.000Z'],
    ['2026-11-22', '10:00', 'Europe/London', '2026-11-22T10:00:00.000Z'],
    ['2026-10-20', '10:00', 'Asia/Kolkata', '2026-10-20T04:30:00.000Z'],
    ['2026-10-20', '10:00', 'Australia/Sydney', '2026-10-19T23:00:00.000Z'],
    ['2026-10-20', '13:05', 'UTC', '2026-10-20T13:05:00.000Z'],
    ['2026-10-20', '00:00', 'UTC', '2026-10-20T00:00:00.000Z'],
    ['2026-10-20', '12:00', 'UTC', '2026-10-20T12:00:00.000Z'],
    ['2026-10-20', '23:59:59.999', 'UTC', '2026-10-20T23:59:59.999Z'],
    ['2026-10-20', '10:00:30.5', 'UTC', '2026-10-20T10:00:30.500Z'],
    ['2026-10-20', '8:45', 'UTC', '2026-10-20T08:45:00.000Z']
  ];
  for (const [date, typed, zone, utc] of cases) {
    assert.equal(asked(date, typed, zone), utc, `${date} ${typed} ${zone}`);
    if (/^\d{2}:/.test(typed)) assert.equal(asked(date, typed, zone), wasAsked(date, typed, zone), 'same value as the native box');
  }
});

test('TIME24-05: the clock-change rules still apply to a typed time (skipped hour refused, repeated hour keeps the later pass)', () => {
  assert.throws(() => asked('2026-03-29', '01:30', 'Europe/London'), /clocks change/);
  assert.equal(asked('2026-10-25', '01:30', 'Europe/London'), '2026-10-25T00:30:00.000Z', 'the first 01:30');
  const later = '2026-10-25T01:30:00.000Z'; // the second 01:30, on winter time
  assert.equal(zi.toZonedInput(later, 'Europe/London'), '2026-10-25T01:30:00.000');
  assert.equal(asked('2026-10-25', '01:30:15', 'Europe/London', later), '2026-10-25T01:30:15.000Z', 'only the seconds changed: still the later pass');
});

test('TIME24-06: a box value round trips: toZonedInput text, committed as typed text, gives the same instant', () => {
  const instant = '2026-10-20T09:00:00.123Z';
  for (const zone of ['UTC', 'Europe/London', 'Asia/Kolkata', 'Australia/Sydney', 'America/New_York']) {
    const box = zi.toZonedInput(instant, zone);
    assert.equal(t24.parseTime24(box.slice(11, 23)), box.slice(11, 23), 'the box text is already in the normal form');
    assert.equal(asked(box.slice(0, 10), box.slice(11, 23), zone), instant, zone);
  }
});

test('TIME24-07: no native time box is left in the When filter; the box is a labelled 24-hour text box with a hint and an error link', () => {
  const field = read('src/components/DateTimeField.svelte');
  const when = read('src/components/filters/WhenGroup.svelte');
  assert.ok(!/type=["']time["']/.test(field + when), 'no <input type="time">');
  assert.match(field, /<label for=\{`\$\{id\}-time`\}>Time\{#if zone\}\{' '\}\(\{zone\}\)\{\/if\}<\/label>/, 'the label still names the zone');
  assert.match(field, /aria-describedby=/);
  assert.match(field, /aria-invalid=/);
  assert.doesNotMatch(field, /inputmode=/, 'no numeric keypad: a phone keypad of digits has no ":" key');
  assert.doesNotMatch(field, /maxlength/, 'a long paste is refused with the message, not cut');
  assert.match(read('src/app.css'), /input\.time24 \{ font-family: var\(--mono\)/, 'the same mono face as the Date box');
  assert.match(when, /errorId="when-time-error"/);
  assert.match(when, /class="inline-error" role="alert" id="when-time-error"/);
  assert.match(read('src/app.css'), /\.date-time-controls input\[aria-invalid='true'\]/);
});

test('TIME24-08: what a run would use is what the box shows: refused text stays refused when only the date changes', () => {
  assert.equal(t24.timeForQuery('13:05'), '13:05:00.000');
  assert.equal(t24.timeForQuery(' 2:30 PM '), '2:30 PM', 'a 12-hour text is handed over as typed, so the filter refuses it again');
  assert.equal(t24.timeForQuery(''), '', 'empty stays empty');
  assert.equal(t24.parseTime24(t24.timeForQuery('2:30 PM')), null);
  const field = read('src/components/DateTimeField.svelte');
  assert.match(field, /onchange=\{\(event\) => onChange\(event\.currentTarget\.value, timeForQuery\(text\)\)\}/, 'a date change sends the box text, not the older committed time');
  assert.doesNotMatch(field, /onChange\([^)]*committed/, 'no change handler sends the older committed time');
});
