import type { Kind, OverviewRow, Outcome, Rollup } from './types';

const missing = '—';
const finite = (value: number | null | undefined): value is number => value != null && Number.isFinite(value);
const sign = (value: number) => value > 0 ? '+' : '';

/** A relative change such as traffic: 0.25 is +25.0%. */
export const changeText = (value: number | null | undefined) => finite(value) ? sign(value) + (value * 100).toFixed(1) + '%' : missing;
/** A change in a share, as percentage points: 0.012 is +1.2 pp. */
export const pointsText = (value: number | null | undefined) => finite(value) ? sign(value) + (value * 100).toFixed(1) + ' pp' : missing;
/** A change in milliseconds; a second or more reads as seconds. */
export const msChangeText = (value: number | null | undefined) => {
  if (!finite(value)) return missing;
  const abs = Math.abs(value);
  const body = abs >= 1000 ? (abs / 1000).toFixed(1) + ' s' : abs < 10 ? abs.toFixed(1) + ' ms' : Math.round(abs) + ' ms';
  return (value < 0 ? '-' : value > 0 ? '+' : '') + body;
};
/** A share as a percentage. */
export const rateText = (value: number | null | undefined) => finite(value) ? (value * 100).toFixed(1) + '%' : missing;

export type OverviewTile = 'events' | 'completed' | 'clientErrors' | 'serverErrors';
export interface OverviewDrill { kind: Kind; state: string; outcome: Outcome | ''; label: string }

/** The drill-down a tile opens, using the filters the request list already supports. Rows with no recorded polling type are the http_unknown kind. */
export const overviewDrill = (kind: string, tile: OverviewTile): OverviewDrill => {
  // Same wording as presentation.kindLabel; not imported so this module stays loadable on its own in the pure tests.
  const label = kind === 'unknown' ? 'HTTP (polling type unknown)' : kind;
  const drillKind = (kind === 'unknown' ? 'http_unknown' : kind) as Kind;
  switch (tile) {
    case 'completed': return { kind: drillKind, state: 'complete', outcome: '', label: label + ' · Complete' };
    case 'clientErrors': return { kind: drillKind, state: 'all', outcome: 'client_error', label: label + ' · client errors (4xx)' };
    case 'serverErrors': return { kind: drillKind, state: 'all', outcome: 'server_error', label: label + ' · server errors (5xx)' };
    default: return { kind: drillKind, state: 'all', outcome: '', label: label + ' · every event' };
  }
};

const stateNotes: Record<string, string> = {
  compared: 'Both periods have at least 20 valid timings, so the changes are shown.',
  insufficient_data: 'At least one period has fewer than 20 valid timings, so response-time changes are hidden.',
  missing_current: 'No valid timings in the current period.',
  missing_reference: 'No valid timings in the reference period, so no changes are shown.'
};
export const overviewStateNote = (row: Pick<OverviewRow, 'state'>) => stateNotes[row.state] ?? '';

export type OperationSort = 'events' | 'trafficDelta' | 'p95DeltaMs' | 'clientErrorDelta' | 'serverErrorDelta' | 'completionDelta';

/** Sorts a copy by one numeric field; rows without a value always go last, whichever direction is chosen. */
export const sortOperations = (rows: Rollup[], field: OperationSort, direction: number): Rollup[] => rows.slice().sort((a, b) => {
  const left = a[field], right = b[field];
  if (!finite(left) && !finite(right)) return 0;
  if (!finite(left)) return 1;
  if (!finite(right)) return -1;
  return (left - right) * direction;
});
