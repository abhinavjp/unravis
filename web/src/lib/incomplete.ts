import type { Query, Rollup } from './types';
import type { OverviewDrill } from './overview';

/**
 * How long an unfinished request must have been open before the stale list counts it as stale.
 * This is a code-derived default, not measured from live data: no production audit data was read to choose it,
 * so treat it as a starting point and change it in the panel for the system you are looking at.
 */
export const DEFAULT_STALE_MINUTES = 10;
/** The server accepts an age range up to 1e9 ms (about 16 666 minutes); a larger threshold would be refused. */
export const MAX_STALE_MINUTES = 16666;

/** The counts a completion rate is worked out from. Invalid rows are part of events but never of the rate. */
export interface CompletionCounts { events?: number; complete?: number; incomplete?: number; pending?: number }

/**
 * complete / (complete + incomplete + pending). Invalid rows are counted apart and never enter the denominator.
 * Null, never 0%, when there are no events or nothing to divide by: a missing rate is not a failing one.
 */
export const completionRate = (counts: CompletionCounts): number | null => {
  const complete = counts.complete ?? 0;
  const denominator = complete + (counts.incomplete ?? 0) + (counts.pending ?? 0);
  if (!((counts.events ?? 0) > 0) || !(denominator > 0)) return null;
  return complete / denominator;
};

/** An operation's completion rate: the server's share when it sent one, otherwise worked out from the counts. */
export const rollupCompletionRate = (rollup: Rollup): number | null =>
  rollup.completionShare != null && Number.isFinite(rollup.completionShare)
    ? rollup.completionShare
    : completionRate({ events: rollup.events, complete: rollup.completed, incomplete: rollup.incomplete, pending: rollup.pending });

export interface RankedOperation { rollup: Rollup; rate: number; unfinished: number }

/**
 * Operations with a completion rate, the lowest first. A tie lists the larger unfinished backlog first, then keeps the order given.
 * Operations with no rate are left out rather than shown as 0%.
 */
export const rankByCompletion = (rollups: Rollup[]): RankedOperation[] => {
  const ranked: RankedOperation[] = [];
  for (const rollup of rollups) {
    const rate = rollupCompletionRate(rollup);
    if (rate == null) continue;
    ranked.push({ rollup, rate, unfinished: (rollup.incomplete ?? 0) + (rollup.pending ?? 0) });
  }
  return ranked
    .map((item, index) => ({ item, index }))
    .sort((a, b) => a.item.rate - b.item.rate || b.item.unfinished - a.item.unfinished || a.index - b.index)
    .map(entry => entry.item);
};

/** The minutes a person typed, or null when it is not a plain positive number within the server's limit (no signs, exponents or units). */
export const parseStaleMinutes = (text: string): number | null => {
  const trimmed = text.trim();
  if (!/^\d+(\.\d+)?$/.test(trimmed)) return null;
  const minutes = Number(trimmed);
  return Number.isFinite(minutes) && minutes > 0 && minutes <= MAX_STALE_MINUTES ? minutes : null;
};

/** The threshold as the milliseconds the age range travels in. */
export const staleThresholdMs = (minutes: number) => Math.round(minutes * 60000);

/** What the stale list asks of the shared request list: the drill plus the filters and order only this list uses. */
export interface StaleDrill extends OverviewDrill {
  completion: 'incomplete' | 'pending';
  order: 'oldest';
  incompleteAgeMs: { min: number };
}

const minutesText = (minutes: number) => Number.isInteger(minutes) ? String(minutes) : String(Number(minutes.toFixed(2)));

/**
 * The drill the stale list opens in the request list: unfinished (or pending) requests open at least the threshold, oldest first.
 * With an operation it narrows to that API; without one it covers every API, under queryKind (the kind the query already filters on).
 */
export const staleDrill = (
  operation: Pick<Rollup, 'controller' | 'action' | 'kind'> | null,
  completion: 'incomplete' | 'pending',
  minutes: number,
  queryKind = 'all'
): StaleDrill => {
  const kind = operation ? (operation.kind ? (operation.kind === 'unknown' ? 'http_unknown' : operation.kind) : 'all') : queryKind;
  const name = operation ? operation.controller + '.' + operation.action : 'Every API';
  const drill: StaleDrill = {
    kind: kind as OverviewDrill['kind'], state: 'all', outcome: '',
    label: name + ' · ' + (completion === 'pending' ? 'pending async' : 'incomplete, includes pending') + ' for at least ' + minutesText(minutes) + ' min · oldest first',
    note: '',
    completion, order: 'oldest', incompleteAgeMs: { min: staleThresholdMs(minutes) }
  };
  if (operation) { drill.controller = operation.controller; drill.action = operation.action; }
  return drill;
};

/** Every latency range the server counts as one; none of them can be combined with an age range. */
const LATENCY_RANGE_KEYS = ['observedMs', 'apiTimeMs', 'auditCreateTimeMs'] as const;

/**
 * Applies a stale drill's completion and age range to a copy of the query. A latency range (observed, API time or audit create time)
 * describes completed rows and the server refuses it together with an age range, so every one of them is dropped.
 */
export const applyStaleFilters = (query: Query, drill: Pick<OverviewDrill, 'completion' | 'incompleteAgeMs'>): Query => {
  if (drill.completion) query.completion = drill.completion;
  if (drill.incompleteAgeMs) {
    query.incompleteAgeMs = { ...drill.incompleteAgeMs };
    for (const key of LATENCY_RANGE_KEYS) delete query[key];
  }
  return query;
};

export interface StaleButtonText { text: string; tip: string }

/**
 * What a stale list button says. The "incomplete" list also returns pending asynchronous requests (they are unfinished rows too),
 * so its count is incomplete plus pending. Both counts cover every age in the query: the list itself only holds those open at least the threshold.
 */
export const staleButtonText = (completion: 'incomplete' | 'pending', counts: { incomplete: number; pending: number }, minutes: number | null): StaleButtonText => {
  const age = minutes == null ? 'the stale threshold' : minutesText(minutes) + ' min';
  const allAges = 'The count covers every age in this search; the list only holds requests open at least ' + age + ', oldest first.';
  if (completion === 'pending') {
    return {
      text: 'List pending async, oldest first (' + counts.pending.toLocaleString() + ' in total)',
      tip: 'Accepted asynchronous requests with no final result yet. ' + allAges
    };
  }
  return {
    text: 'List incomplete, oldest first (' + (counts.incomplete + counts.pending).toLocaleString() + ' in total, includes pending)',
    tip: 'Unfinished requests, including pending asynchronous ones. ' + allAges
  };
};