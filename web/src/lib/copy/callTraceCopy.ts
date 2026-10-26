import type { LintEntry } from './copylint';

/**
 * Words for the call trace inside Nearby activity. It shows what overlapped in time with one call. Every sentence says "during this call",
 * never that something caused something else.
 */
export const CALL_TRACE_TEXT = {
  heading: 'Call trace',
  close: 'Close trace',
  chartLabel: 'Trace chart. It scrolls sideways.',
  intro: 'One call, then what SQL Server, MongoDB, Redis and IIS did during it.',
  overlapNote: 'This view shows what overlapped in time with the call. It does not say why.',
  splitNote: 'The audit record has no timings for each filter. So the call is split into audit write, API work and other time.',
  payloadNote: 'Payload record time is measured after the work, so it sits outside the total.',

  traceThis: 'Trace this call',
  traceThisHint: 'Show what SQL Server, MongoDB, Redis and IIS did during this call.',
  addCompare: 'Add to compare',
  added: 'Added',
  trayHeading: 'Calls to compare',
  trayClear: 'Clear',
  trayRemove: 'Remove',
  trayFull: 'Three calls are chosen. Remove one to add another.',
  trayDuplicate: 'This call is already chosen.',
  trayNoId: 'This row has no id, so it cannot be traced.',
  trayCapHint: 'You can compare up to three calls.',

  modeLabel: 'Time axis',
  modeReal: 'Real clock',
  modeAligned: 'Aligned to start',
  modeRealHint: 'All calls share one clock.',
  modeAlignedHint: 'Each call starts at zero, so the calls line up.',
  realUnavailable: 'Real clock is off here. These calls are more than 2 hours apart.',
  marginLabel: 'Time around the call',

  partsHeading: 'How the time of the call was split',
  callBar: 'The call',
  payloadMarker: 'Payload record',
  noFinish: 'This call has no usable finish time, so only its start is drawn.',
  clockAnomaly: 'The log shows this call finishing before it started. The clock may be off, so no end is drawn.',
  noEvents: 'Nothing to draw on this row.',
  truncated: 'The read reached its limit, so some events may be missing.',

  loading: 'Reading what happened during this call…',
  failed: 'The call trace could not be read.',
  unreadable: 'The server sent an answer that could not be read.',
  retry: 'Try again',
  empty: 'Choose a call to trace.',

  checksHeading: 'Checks for this call',
  compareHeading: 'Checks across these calls',
  checksNone: 'No checks apply here.',
  needs: 'Needs',
  symptom: 'Symptom',

  statusConsistent: 'Consistent with this idea',
  statusContradicted: 'The data does not fit this idea',
  statusNotEnoughData: 'Not enough data',
  statusNotTestable: 'Not testable here',

  stateNoData: 'This case has no file for this source',
  stateNoneSeen: 'Files cover this time and nothing was seen',
  stateClockUnchecked: 'Time zone not checked, so nothing is drawn',
  stateNoCase: 'Open a case to see this source',
  stateIncomplete: 'Read hit its limit; events may be missing',
  stateOutOfRange: 'Files do not cover this time',

  listSummary: 'Read this trace as a list',
  listParts: 'Time split',
  listEvents: 'Events',
  listWhen: 'Start',
  listLength: 'Length',
  listWhat: 'What'
} as const;

export const STATUS_TEXT: Record<string, string> = {
  consistent: CALL_TRACE_TEXT.statusConsistent,
  contradicted: CALL_TRACE_TEXT.statusContradicted,
  'not-enough-data': CALL_TRACE_TEXT.statusNotEnoughData,
  'not-testable': CALL_TRACE_TEXT.statusNotTestable
};

/** A mark that is not a colour, so a status reads without colour. */
export const STATUS_ICON: Record<string, string> = {
  consistent: '✓',
  contradicted: '✕',
  'not-enough-data': '?',
  'not-testable': '–'
};

export const LANE_STATE_TEXT: Record<string, string> = {
  'no-data': CALL_TRACE_TEXT.stateNoData,
  'none-seen': CALL_TRACE_TEXT.stateNoneSeen,
  'clock-unchecked': CALL_TRACE_TEXT.stateClockUnchecked,
  'no-case': CALL_TRACE_TEXT.stateNoCase,
  incomplete: CALL_TRACE_TEXT.stateIncomplete,
  'out-of-range': CALL_TRACE_TEXT.stateOutOfRange
};

export const statusText = (status: string): string => STATUS_TEXT[status] ?? CALL_TRACE_TEXT.statusNotEnoughData;
export const statusIcon = (status: string): string => STATUS_ICON[status] ?? '?';

export const callName = (index: number): string => `Call ${index + 1}`;
export const compareButton = (count: number): string => `Compare (${count})`;
export const callsAre = (numbers: number[]): string => {
  if (numbers.length === 0) return '';
  if (numbers.length === 1) return `Call ${numbers[0]}`;
  return `Calls ${numbers.slice(0, -1).join(', ')} and ${numbers[numbers.length - 1]}`;
};
export const marginOption = (minutes: number): string => `${minutes} min`;
export const hiddenMore = (count: number): string => `${count} more ${count === 1 ? 'event is' : 'events are'} hidden. The longest are shown.`;

export const callTraceCopyEntries = (): LintEntry[] => [
  ...Object.entries(CALL_TRACE_TEXT).map(([id, text]) => ({ id: 'callTrace.' + id, text })),
  { id: 'callTrace.sample.hidden', text: hiddenMore(12) },
  { id: 'callTrace.sample.hiddenOne', text: hiddenMore(1) },
  { id: 'callTrace.sample.compare', text: compareButton(2) },
  { id: 'callTrace.sample.calls', text: callsAre([1, 2]) },
  { id: 'callTrace.sample.callsThree', text: callsAre([1, 2, 3]) }
];
