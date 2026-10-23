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
/**
 * A drill-down from the overview or the operation view. note says how the opened list can differ from the count that was clicked (empty when it cannot);
 * controller and action are only set by an operation drill and narrow the list to that API.
 */
export interface OverviewDrill { kind: Kind; state: string; outcome: Outcome | ''; label: string; note: string; controller?: string; action?: string }
/** What the strip knows about the gap between a tile and its list: the unknown-type row's incomplete+invalid count, this row's own unknown results, and the user's outcome filter. */
export interface DrillContext { unknownKindRows?: number; unknownResults?: number; outcomeFilter?: string; queryKind?: string }

// Same wording as presentation.kindLabel; not imported so this module stays loadable on its own in the pure tests.
const drillLabel = (kind: string) => kind === 'unknown' ? 'HTTP (polling type unknown)' : kind;
const drillKind = (kind: string) => (kind === 'unknown' ? 'http_unknown' : kind) as Kind;
const rowsText = (count: number, kind = '') => count + ' ' + (kind ? kind + ' ' : '') + (count === 1 ? 'row' : 'rows');
const unusableRows = 'incomplete or unusable';
const compatibleKind = (kind?: string) => kind === 'synchronous' || kind === 'asynchronous';
/**
 * Under a Synchronous or Asynchronous query valid rows of unknown request type are scoped out, so an unknown-type count leaves them out,
 * while the http_unknown list it opens includes every unknown-type row that matches the filters. There is no known bound, so no number.
 */
const unknownTypeGap = (kind: string, queryKind?: string) => kind === 'unknown' && compatibleKind(queryKind);
const unknownTypeNote = (queryKind?: string) => 'also lists any rows of unknown request type that the ' + queryKind + ' filter leaves out of this count';

/**
 * The drill-down a tile opens, using the filters the request list already supports. Rows with no recorded polling type are the http_unknown kind.
 * The list is not always identical to the tile. Incomplete and invalid rows of unknown request type cannot be told apart as Synchronous or
 * Asynchronous, so those two lists include them (exactly, for Events). Under a 4xx or 5xx filter the summary also keeps every row whose result is
 * unknown, so those lists hold up to that many more. The 4xx and 5xx drills also replace any other outcome filter the user had chosen.
 * Under a Synchronous or Asynchronous query the unknown row leaves out valid rows of unknown request type but its http_unknown list includes
 * them, so every tile of that row carries an unbounded note instead of a number.
 */
export const overviewDrill = (kind: string, tile: OverviewTile, context: DrillContext = {}): OverviewDrill => {
  const label = drillLabel(kind);
  const notes: string[] = [];
  const compatible = compatibleKind(kind);
  const gap = unknownTypeGap(kind, context.queryKind);
  const unknownKind = compatible ? Math.max(0, context.unknownKindRows ?? 0) : 0;
  const unknownResults = Math.max(0, context.unknownResults ?? 0);
  const base = { kind: drillKind(kind), state: 'all', outcome: '' as Outcome | '' };
  const classDrill = (outcome: 'client_error' | 'server_error', name: string): OverviewDrill => {
    const bound = unknownKind + unknownResults;
    if (gap) notes.push(unknownTypeNote(context.queryKind));
    else if (bound > 0) {
      const what = unknownKind > 0 && unknownResults > 0 ? 'with an unknown result or request type' : unknownKind > 0 ? 'of unknown request type' : 'whose result is unknown';
      notes.push('also lists up to ' + rowsText(bound, unknownKind > 0 && unknownResults === 0 ? unusableRows : '') + ' ' + what);
    }
    const chosen = context.outcomeFilter;
    if (chosen && chosen !== 'all' && chosen !== outcome) notes.push('replaces the ' + chosen.replaceAll('_', ' ') + ' outcome filter, so counts may differ');
    return { ...base, outcome, label: label + ' · ' + name, note: notes.join('; ') };
  };
  switch (tile) {
    case 'completed': return { ...base, state: 'complete', label: label + ' · Complete', note: gap ? unknownTypeNote(context.queryKind) : '' };
    case 'clientErrors': return classDrill('client_error', 'client errors (4xx)');
    case 'serverErrors': return classDrill('server_error', 'server errors (5xx)');
    default: return { ...base, label: label + ' · every event', note: gap ? unknownTypeNote(context.queryKind) : unknownKind > 0 ? 'also lists ' + rowsText(unknownKind, unusableRows) + ' of unknown request type' : '' };
  }
};

/**
 * The drill-down an operation row opens: every event of that controller, action and request type that matches the filters. Synchronous and
 * Asynchronous lists can also hold unknown-type rows that are incomplete or unusable. An unknown-type operation under a Synchronous or
 * Asynchronous query (queryKind) leaves out valid unknown-type rows that its list includes.
 */
export const operationDrill = (rollup: Pick<Rollup, 'controller' | 'action' | 'kind'>, queryKind = ''): OverviewDrill => {
  const kind = rollup.kind ?? '';
  const compatible = compatibleKind(kind);
  return {
    kind: kind ? drillKind(kind) : 'all', state: 'all', outcome: '',
    label: rollup.controller + '.' + rollup.action + ' · ' + (kind ? drillLabel(kind) : 'any request type') + ' · every event',
    note: compatible ? 'also lists any incomplete or unusable rows of unknown request type' : unknownTypeGap(kind, queryKind) ? unknownTypeNote(queryKind) : '',
    controller: rollup.controller, action: rollup.action
  };
};

/** The request-list heading for a drill: its label, then the note in brackets when the list can differ from the count that was clicked. */
export const drillHeading = (drill: Pick<OverviewDrill, 'label' | 'note'>) => drill.note ? drill.label + ' (' + drill.note + ')' : drill.label;

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
