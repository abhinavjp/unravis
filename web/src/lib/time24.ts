/**
 * The 24-hour time box of the When filters (BL-495, owner rule BL-181: every time box is 24-hour). It replaces the browser's own
 * `<input type="time">`, which shows "08:45:00.000 AM" on a 12-hour browser or computer setting. The box holds text in one fixed form,
 * `HH:MM:SS.mmm`, the same form the native box handed to `zonedInput.fromZonedParts`, so the wire values do not change.
 */
export const TIME24_PLACEHOLDER = 'HH:MM:SS.mmm';
export const TIME24_HINT = 'Type the time in 24-hour form, for example 13:05 or 13:05:30.250.';

const pad = (value: number, width = 2) => String(value).padStart(width, '0');

/**
 * `HH:MM:SS.mmm` for typed or pasted text, or null when it is not a 24-hour time. Accepts `H:MM`, `HH:MM`, `HH:MM:SS` and
 * `HH:MM:SS.f` (one to three digits after the point or comma, padded on the right as the native box did). Hours run 00 to 23,
 * minutes and seconds 00 to 59. Spaces around the text are ignored; "AM" and "PM" are not accepted, so a 12-hour paste is refused
 * and the person sees the 24-hour message instead of a silently wrong hour.
 */
export const parseTime24 = (text: string): string | null => {
  const match = /^(\d{1,2}):(\d{2})(?::(\d{2})(?:[.,](\d{1,3}))?)?$/.exec(text.trim());
  if (!match) return null;
  const hour = +match[1], minute = +match[2], second = +(match[3] ?? 0);
  if (hour > 23 || minute > 59 || second > 59) return null;
  const milli = Number((match[4] ?? '').padEnd(3, '0') || 0);
  return `${pad(hour)}:${pad(minute)}:${pad(second)}.${pad(milli, 3)}`;
};

type Segment = 'hour' | 'minute' | 'second' | 'milli';
const SEGMENTS: { id: Segment; start: number; end: number; size: number }[] = [
  { id: 'hour', start: 0, end: 2, size: 24 },
  { id: 'minute', start: 3, end: 5, size: 60 },
  { id: 'second', start: 6, end: 8, size: 60 },
  { id: 'milli', start: 9, end: 12, size: 1000 }
];

/** The segment the caret is in (a caret on a colon belongs to the segment before it). */
const segmentAt = (caret: number) => SEGMENTS.find((s) => caret <= s.end) ?? SEGMENTS[3];

/**
 * ArrowUp (delta 1) and ArrowDown (delta -1) on a segment, as the native box does: the hour, minute, second or millisecond under the caret
 * moves by one and wraps inside its own range without touching the others. Null when the text is not a time yet. `select` is the part of the
 * new text to select afterwards, so the next press steps the same segment.
 */
export const stepTime24 = (text: string, caret: number, delta: 1 | -1): { text: string; select: [number, number] } | null => {
  const parsed = parseTime24(text);
  if (!parsed) return null;
  const seg = segmentAt(caret);
  const values = { hour: +parsed.slice(0, 2), minute: +parsed.slice(3, 5), second: +parsed.slice(6, 8), milli: +parsed.slice(9, 12) };
  values[seg.id] = (values[seg.id] + delta + seg.size) % seg.size;
  const next = `${pad(values.hour)}:${pad(values.minute)}:${pad(values.second)}.${pad(values.milli, 3)}`;
  return { text: next, select: [seg.start, seg.end] };
};
