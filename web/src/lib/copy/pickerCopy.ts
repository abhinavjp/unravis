import type { LintEntry } from './copylint';

/**
 * Words for the time-window picker (WindowPicker.svelte, StampField.svelte). Plain words, no jargon. Every string here passes the copy lint
 * (tests/windowPicker.test.cjs). Text that takes a value is a function, listed in `pickerCopyEntries()` with an example.
 */
export const PICKER_TEXT = {
  /** The trigger when no period can be read yet. */
  choose: 'Choose a period',
  zone: 'UTC',
  quickHeading: 'Quick ranges',
  timesHeading: 'Times · UTC',
  start: 'Start',
  end: 'End (not included)',
  cancel: 'Cancel',
  apply: 'Apply',
  previousMonth: 'Previous month',
  nextMonth: 'Next month',
  hintFirst: 'Click the start day, then the end day.',
  hintSecond: 'Now click the end day.',
  /** Shown beside the times while the end is not after the start, and again if Apply is pressed. */
  orderProblem: 'The end must be after the start.',
  /** Shown while a box is empty or holds text that is not a date or a time. */
  incomplete: 'Pick a start and an end first.',
  /** Under a date box that holds text that is not a real day. */
  badDate: 'Use a real date, written like 2026-10-26.',
  /** Under a time box that holds text that is not a time. */
  badTime: 'Use a time from 00:00 to 23:59, written like 10:30.',
  datePlaceholder: 'YYYY-MM-DD',
  timePlaceholder: 'HH:mm',
  otherDay: 'Ends on a different day',
  to: 'to',
  weekdays: ['S', 'M', 'T', 'W', 'T', 'F', 'S'],
  quickPick: 'Quick pick'
} as const;

export type PickerPart = 'start date' | 'start time' | 'end date' | 'end time';

/** The label without a trailing "(UTC)": every picker already says UTC on its own, so the spoken names do not say it twice. */
const bare = (label: string): string => label.replace(/\s*\(UTC\)\s*$/, '');

/** The spoken name of one text box: "Incident window start date". */
export const partLabel = (label: string, part: PickerPart): string => `${bare(label)} ${part}`;

/** The name of the button that opens the popover: "Incident window: 26 Oct 2026 10:30 → 11:00, UTC". */
export const triggerLabel = (label: string, summary: string): string => `${bare(label)}: ${summary || PICKER_TEXT.choose}, ${PICKER_TEXT.zone}`;

/** The name of the popover: "Choose the incident window (UTC)". */
export const dialogLabel = (label: string): string => `Choose the ${bare(label).toLowerCase()} (${PICKER_TEXT.zone})`;

/** The spoken name of a calendar day: "October 26, 2026, start". `role` is 'start', 'end' or '' for a day outside the pick. */
export const dayLabel = (name: string, role: 'start' | 'end' | 'between' | 'none'): string => (role === 'start' || role === 'end' ? `${name}, ${role}` : name);

export const pickerCopyEntries = (): LintEntry[] => [
  ...Object.entries(PICKER_TEXT)
    .filter(([, text]) => typeof text === 'string')
    .map(([id, text]) => ({ id: 'picker.' + id, text: text as string })),
  { id: 'picker.partLabel', text: partLabel('Incident window', 'start date') },
  { id: 'picker.triggerLabel', text: triggerLabel('Incident window', '26 Oct 2026 10:30 → 11:00') },
  { id: 'picker.dialogLabel', text: dialogLabel('Incident window') },
  { id: 'picker.dayLabel', text: dayLabel('October 26, 2026', 'start') }
];
