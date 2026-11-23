import type { RequestRecord } from './types';
import { formatTime } from './time';

// The names of the two buttons at the end of a row of the request list (All operations). The visible words come first (WCAG label in name), then the
// request they belong to: its API, the time it started to the millisecond and the source it was read from, so that two rows never read the same
// ("Nearby activity" twice in a column tells a screen reader user nothing about which call it opens; two sources can hold the same API at the same moment).
export const REQUEST_ACTION_TEXT = {
  nearby: 'Nearby activity',
  inspect: 'Inspect',
  loading: 'Loading…',
  unknownTime: 'unknown time'
} as const;

/** "Payroll.Run at 08:59:52.123, Demo West": the API (controller and action, as the row shows it), the start time in the table's zone (no date and no zone name: the heading has them) and the source. */
export const requestLabel = (row: Pick<RequestRecord, 'controller' | 'action' | 'initiatedAt'>, zone: string, source = ''): string => {
  const api = [row.controller, row.action].filter(Boolean).join('.') || 'unnamed API';
  const time = formatTime(row.initiatedAt, { zone, precision: 'ms', date: false, suffix: false, fallback: REQUEST_ACTION_TEXT.unknownTime });
  return `${api} at ${time}${source ? ', ' + source : ''}`;
};

export const nearbyActivityName = (row: Pick<RequestRecord, 'controller' | 'action' | 'initiatedAt'>, zone: string, source = ''): string =>
  `${REQUEST_ACTION_TEXT.nearby} for ${requestLabel(row, zone, source)}`;

/** While the record loads the button shows "Loading…", so the name starts with those words too. */
export const inspectName = (row: Pick<RequestRecord, 'controller' | 'action' | 'initiatedAt'>, zone: string, loading = false, source = ''): string =>
  `${loading ? REQUEST_ACTION_TEXT.loading + ' record' : REQUEST_ACTION_TEXT.inspect + ' record'} for ${requestLabel(row, zone, source)}`;
