/**
 * The pure rules behind the time-window picker (WindowPicker.svelte and StampField.svelte). No Svelte, no DOM, no clock:
 * every function gets the moment it needs. Values are `YYYY-MM-DDTHH:mm` strings that are always read as UTC, the same shape the
 * tracing and guided checks already use, so the screens that own those checks do not change.
 */

export const MINUTE_MS = 60_000;
export const HOUR_MS = 3_600_000;
export const DAY_MS = 86_400_000;

export const MONTH_NAMES = ['January', 'February', 'March', 'April', 'May', 'June', 'July', 'August', 'September', 'October', 'November', 'December'] as const;
const MONTH_SHORT = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'] as const;

const pad = (n: number, width = 2): string => String(n).padStart(width, '0');

/** A start and an end as `YYYY-MM-DDTHH:mm` strings (UTC). Either can be empty while the person is still typing. */
export interface Range { start: string; end: string }

/** A moment as `YYYY-MM-DDTHH:mm` in UTC. */
export function formatStamp(ms: number): string {
  const d = new Date(ms);
  return `${pad(d.getUTCFullYear(), 4)}-${pad(d.getUTCMonth() + 1)}-${pad(d.getUTCDate())}T${pad(d.getUTCHours())}:${pad(d.getUTCMinutes())}`;
}

const STAMP = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2})$/;

/** Milliseconds for a whole `YYYY-MM-DDTHH:mm` (UTC), or NaN. A day that does not exist (2026-02-31) is refused by the round trip. */
export function parseStamp(value: string): number {
  const m = STAMP.exec(value);
  if (!m) return NaN;
  const ms = Date.UTC(+m[1], +m[2] - 1, +m[3], +m[4], +m[5]);
  return Number.isFinite(ms) && formatStamp(ms) === value ? ms : NaN;
}

const DATE_TEXT = /^(\d{4})-(\d{2})-(\d{2})$/;
const TIME_TEXT = /^(\d{2}):(\d{2})$/;

/** True for a real calendar day written `YYYY-MM-DD`. */
export function isDateText(text: string): boolean {
  return DATE_TEXT.test(text) && Number.isFinite(parseStamp(text + 'T00:00'));
}

/** True for a 24-hour time written `HH:mm` (00:00 to 23:59). */
export function isTimeText(text: string): boolean {
  const m = TIME_TEXT.exec(text);
  return !!m && +m[1] <= 23 && +m[2] <= 59;
}

/** The date part and the time part of a stamp. An empty or malformed stamp gives two empty strings. */
export function splitStamp(value: string): { date: string; time: string } {
  return STAMP.test(value) ? { date: value.slice(0, 10), time: value.slice(11) } : { date: '', time: '' };
}

export interface Reading {
  /** The value to keep: the new stamp, '' when a box was emptied on purpose, or the previous value when the text is not usable. */
  value: string;
  badDate: boolean;
  badTime: boolean;
}

/**
 * Reads the two text boxes of one moment. An empty box is a deliberate clearing: the value becomes '' and the screen's own check says
 * what is missing. Text that is not a real date or time never replaces the previous good value; the box is marked instead.
 */
export function readParts(date: string, time: string, previous: string): Reading {
  const d = date.trim();
  const t = time.trim();
  const badDate = d !== '' && !isDateText(d);
  const badTime = t !== '' && !isTimeText(t);
  if (badDate || badTime) return { value: previous, badDate, badTime };
  if (d === '' || t === '') return { value: '', badDate: false, badTime: false };
  return { value: `${d}T${t}`, badDate: false, badTime: false };
}

/**
 * Syncs the two text boxes with a value set from outside. With both boxes usable it is the old rule: an equal value leaves the text
 * alone, a different one replaces both boxes. With a box marked bad the effect only runs because the value changed outside (the person's
 * own edits keep the previous value while a box is bad), so the good box takes the matching part of the new stamp and the bad box keeps
 * the text the person is still fixing.
 */
export function syncParts(date: string, time: string, outside: string): { date: string; time: string; badDate: boolean; badTime: boolean; changed: boolean } {
  const r = readParts(date, time, outside);
  const next = splitStamp(outside);
  if (!r.badDate && !r.badTime) {
    if (r.value === outside) return { date, time, badDate: false, badTime: false, changed: false };
    return { date: next.date, time: next.time, badDate: false, badTime: false, changed: true };
  }
  if (r.badDate && r.badTime) return { date, time, badDate: true, badTime: true, changed: false };
  if (r.badDate) return { date, time: next.time, badDate: true, badTime: false, changed: next.time !== time };
  return { date: next.date, time, badDate: false, badTime: true, changed: next.date !== date };
}

/**
 * The shortest honest words for a length of time: 30 min, 1 h, 2 h 30 min, 3 days, 1 day 6 h, 1 day 6 h 30 min.
 * Over a day nothing is dropped: the days, then the hours when they are not zero, then the minutes when they are not zero.
 */
export function formatSpan(ms: number): string {
  const minutes = Math.round(ms / MINUTE_MS);
  if (minutes < 60) return `${minutes} min`;
  if (minutes < 24 * 60) {
    const h = Math.floor(minutes / 60);
    const m = minutes % 60;
    return m ? `${h} h ${m} min` : `${h} h`;
  }
  const days = Math.floor(minutes / (24 * 60));
  const h = Math.floor((minutes % (24 * 60)) / 60);
  const m = minutes % 60;
  const dayWord = days === 1 ? 'day' : 'days';
  const parts = [`${days} ${dayWord}`];
  if (h) parts.push(`${h} h`);
  if (m) parts.push(`${m} min`);
  return parts.join(' ');
}

const dayLabel = (ms: number): string => {
  const d = new Date(ms);
  return `${d.getUTCDate()} ${MONTH_SHORT[d.getUTCMonth()]} ${d.getUTCFullYear()}`;
};

/** `26 Oct 2026 10:30 → 11:00` on one day, `26 Oct 2026 10:30 → 27 Oct 2026 02:00` across days. Empty when the range cannot be read. */
export function rangeText(range: Range): string {
  const s = parseStamp(range.start);
  const e = parseStamp(range.end);
  if (!Number.isFinite(s) || !Number.isFinite(e) || e <= s) return '';
  const first = `${dayLabel(s)} ${range.start.slice(11)}`;
  return range.start.slice(0, 10) === range.end.slice(0, 10) ? `${first} → ${range.end.slice(11)}` : `${first} → ${dayLabel(e)} ${range.end.slice(11)}`;
}

/** The length of a range, in words. Empty when the range cannot be read. */
export function rangeSpanText(range: Range): string {
  const s = parseStamp(range.start);
  const e = parseStamp(range.end);
  return Number.isFinite(s) && Number.isFinite(e) && e > s ? formatSpan(e - s) : '';
}

/** One entry in the Quick ranges column. `range` is null when it cannot be worked out yet (no usable incident window). */
export interface QuickRange { id: string; label: string; range: Range | null }

const QUICK_LENGTHS: Array<[string, string, number]> = [
  ['15m', 'Last 15 min', 15 * MINUTE_MS],
  ['1h', 'Last hour (default)', HOUR_MS],
  ['4h', 'Last 4 hours', 4 * HOUR_MS],
  ['24h', 'Last 24 hours', DAY_MS],
  ['7d', 'Last 7 days', 7 * DAY_MS],
  ['30d', 'Last 30 days', 30 * DAY_MS]
];

/** The quick ranges for the incident window: each ends at `nowMs` (floored to the minute). */
export function incidentQuickRanges(nowMs: number): QuickRange[] {
  const end = Math.floor(nowMs / MINUTE_MS) * MINUTE_MS;
  return QUICK_LENGTHS.map(([id, label, length]) => ({ id, label, range: { start: formatStamp(end - length), end: formatStamp(end) } }));
}

/** The chip row on the page ("Quick range", above the two windows): the same lengths as the popover, without the "(default)" note. */
export function incidentQuickChips(nowMs: number): QuickRange[] {
  return incidentQuickRanges(nowMs).map((quick) => ({ ...quick, label: quick.label.replace(/\s*\(default\)\s*$/, '') }));
}

/** The four window fields of the timeline screen, in the shape of `WindowFields` (kept structural so this module stays free of the screen's types). */
export interface WindowFour { incidentStart: string; incidentEnd: string; referenceStart: string; referenceEnd: string }

/** The fields after a quick range for the incident: the incident becomes the range and the comparison period the equal period right before it. */
export function withQuickRange<T extends WindowFour>(fields: T, quick: QuickRange): T {
  if (!quick.range) return fields;
  const before = equalPeriodBefore(quick.range);
  return { ...fields, incidentStart: quick.range.start, incidentEnd: quick.range.end, ...(before ? { referenceStart: before.start, referenceEnd: before.end } : {}) };
}

/** True when the fields hold exactly this quick range and the equal period before it (the chip then shows as pressed). */
export function quickRangeIsActive(fields: WindowFour, quick: QuickRange): boolean {
  if (!quick.range) return false;
  const before = equalPeriodBefore(quick.range);
  return fields.incidentStart === quick.range.start && fields.incidentEnd === quick.range.end
    && !!before && fields.referenceStart === before.start && fields.referenceEnd === before.end;
}

/** The period of the same length that ends exactly where `range` starts. Null when `range` is not a usable start and end. */
export function equalPeriodBefore(range: Range): Range | null {
  const s = parseStamp(range.start);
  const e = parseStamp(range.end);
  if (!Number.isFinite(s) || !Number.isFinite(e) || e <= s) return null;
  return { start: formatStamp(s - (e - s)), end: range.start };
}

/** The same clock times one day earlier. Null when `range` is not a usable start and end. */
export function sameTimeYesterday(range: Range): Range | null {
  const s = parseStamp(range.start);
  const e = parseStamp(range.end);
  if (!Number.isFinite(s) || !Number.isFinite(e) || e <= s) return null;
  return { start: formatStamp(s - DAY_MS), end: formatStamp(e - DAY_MS) };
}

/** The quick ranges for the comparison period, worked out from the incident window. */
export function comparisonQuickRanges(incident: Range): QuickRange[] {
  return [
    { id: 'before', label: 'The equal period right before the investigation window', range: equalPeriodBefore(incident) },
    { id: 'yesterday', label: 'Same time yesterday', range: sameTimeYesterday(incident) }
  ];
}

export interface Cell { day: number; date: string }
export interface Month {
  year: number;
  /** 0 to 11. */
  month: number;
  title: string;
  /** Empty cells before day 1 (the week starts on Sunday). */
  lead: number;
  days: Cell[];
}

/** Days in a month (leap years included). `month` is 0 to 11. */
export function daysInMonth(year: number, month: number): number {
  const d = new Date(Date.UTC(2000, month + 1, 0));
  d.setUTCFullYear(year, month + 1, 0);
  return d.getUTCDate();
}

/** The year and month `delta` months from the given one. */
export function shiftMonth(year: number, month: number, delta: number): { year: number; month: number } {
  const total = year * 12 + month + delta;
  return { year: Math.floor(total / 12), month: ((total % 12) + 12) % 12 };
}

/** The calendar grid for one month (`month` is 0 to 11). The week starts on Sunday. */
export function monthGrid(year: number, month: number): Month {
  const first = new Date(Date.UTC(2000, 0, 1));
  first.setUTCFullYear(year, month, 1);
  const days: Cell[] = [];
  for (let d = 1; d <= daysInMonth(year, month); d++) days.push({ day: d, date: `${pad(year, 4)}-${pad(month + 1)}-${pad(d)}` });
  return { year, month, title: `${MONTH_NAMES[month]} ${year}`, lead: first.getUTCDay(), days };
}

/** The spoken name of a calendar day, for its aria-label: `October 26, 2026`. */
export function dayName(date: string): string {
  const m = DATE_TEXT.exec(date);
  return m ? `${MONTH_NAMES[+m[2] - 1]} ${+m[3]}, ${m[1]}` : date;
}

/** The two days picked on the calendar. `picking` is true after the first click, while the end day is still to come. */
export interface DayPick { startDay: string; endDay: string; picking: boolean }

/** One click on a day: the first click sets the start and waits; the second sets the end, and an earlier day swaps the two. */
export function pickDay(state: DayPick, date: string): DayPick {
  if (!state.picking) return { startDay: date, endDay: '', picking: true };
  return date < state.startDay ? { startDay: date, endDay: state.startDay, picking: false } : { startDay: state.startDay, endDay: date, picking: false };
}

/** Where a day sits in the picked range, for its look and its spoken name. */
export function dayRole(state: DayPick, date: string): 'start' | 'end' | 'between' | 'none' {
  if (date === state.startDay) return 'start';
  if (state.endDay && date === state.endDay) return 'end';
  if (state.endDay && date > state.startDay && date < state.endDay) return 'between';
  return 'none';
}

/** The text boxes of the popover, as typed. */
export interface DraftText { startDate: string; startTime: string; endDate: string; endTime: string }

export type DraftState = 'ok' | 'incomplete' | 'order';

/** Reads a draft: the range when both ends are whole, and why not otherwise (`incomplete`, or `order` when the end is not after the start). */
export function readDraft(text: DraftText): { state: DraftState; range: Range | null } {
  const range: Range = { start: `${text.startDate.trim()}T${text.startTime.trim()}`, end: `${text.endDate.trim()}T${text.endTime.trim()}` };
  const s = parseStamp(range.start);
  const e = parseStamp(range.end);
  if (!Number.isFinite(s) || !Number.isFinite(e)) return { state: 'incomplete', range: null };
  if (e <= s) return { state: 'order', range };
  return { state: 'ok', range };
}

/** A draft with a range's two ends spread over its four boxes. */
export function draftFrom(range: Range): DraftText {
  const a = splitStamp(range.start);
  const b = splitStamp(range.end);
  return { startDate: a.date, startTime: a.time, endDate: b.date, endTime: b.time };
}

/** A calendar pick written into the draft: the two days replace the date boxes and the times stay. One click so far sets both days to it. */
export function draftWithDays(text: DraftText, pick: DayPick): DraftText {
  return { ...text, startDate: pick.startDay, endDate: pick.endDay || pick.startDay };
}
