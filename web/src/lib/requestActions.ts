import type { RequestRecord } from './types';
import { formatTime } from './time';
import { chartRange } from './chartTime';

// The names of the two buttons at the end of a row of the request list (All operations). The visible words come first (WCAG label in name), then the
// request they belong to: its API, the time it started to the millisecond, the source it was read from and the record id, so that two rows never read
// the same ("Nearby activity" twice in a column tells a screen reader user nothing about which call it opens). The id is what makes the name unique on live
// data: the same API from the same source can start in the same millisecond, a list can span days (the clock time repeats) and the hour a clock change
// repeats has the same clock time twice. The record id is what the old name of Inspect held.
// The same names are used by the slowest-request list of the Slowest tab (TrafficSlowness: "Nearby activity") and its "Inspect window" buttons are named by the
// time window they open (windowInspectName), since a window belongs to the whole comparison, not to one source.
export const REQUEST_ACTION_TEXT = {
  nearby: 'Nearby activity',
  inspect: 'Inspect',
  inspectWindow: 'Inspect window',
  loading: 'Loading…',
  unknownTime: 'unknown time'
} as const;

type Named = Pick<RequestRecord, 'id' | 'controller' | 'action' | 'initiatedAt'>;

/** "Payroll.Run at 08:59:52.123, Demo West, record r-124": the API (controller and action, as the row shows it), the start time in the table's zone (no date and no zone name: the heading has them), the source and the record id (the only part that is unique). */
export const requestLabel = (row: Named, zone: string, source = ''): string => {
  const api = [row.controller, row.action].filter(Boolean).join('.') || 'unnamed API';
  const time = formatTime(row.initiatedAt, { zone, precision: 'ms', date: false, suffix: false, fallback: REQUEST_ACTION_TEXT.unknownTime });
  return `${api} at ${time}${source ? ', ' + source : ''}${row.id ? ', record ' + row.id : ''}`;
};

export const nearbyActivityName = (row: Named, zone: string, source = ''): string =>
  `${REQUEST_ACTION_TEXT.nearby} for ${requestLabel(row, zone, source)}`;

/** While the record loads the button shows "Loading…", so the name starts with those words too. */
export const inspectName = (row: Named, zone: string, loading = false, source = ''): string =>
  `${loading ? REQUEST_ACTION_TEXT.loading + ' record' : REQUEST_ACTION_TEXT.inspect + ' record'} for ${requestLabel(row, zone, source)}`;

type Window = { start: number; end: number; partial?: boolean };

/** "Inspect window for 20 Oct 08:00:00 to 08:05:00 UTC (partial)": the window the row of the table of exact time windows shows (its start and end in the table's zone, the zone named once; a clock change names the zone at each end). Every window of one list starts at a different time, so no two names are the same. */
export const windowInspectName = (row: Window, zone: string): string =>
  `${REQUEST_ACTION_TEXT.inspectWindow} for ${chartRange(row.start, row.end, zone)}${row.partial ? ' (partial)' : ''}`;
