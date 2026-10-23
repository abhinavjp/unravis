import { serializeRecordFilters } from './investigation';
import type { BreakdownDim, BreakdownFilter, BreakdownRow, Query, RecordFilters } from './types';

/** Every record filter the query carries. The Record type makes a new RecordFilters key fail to compile until it is listed here. */
const recordKeySet: Record<keyof RecordFilters, true> = {
  userIds: true, uiVersions: true, userAgentContains: true, userIps: true, urlContains: true,
  auditId: true, statusCodes: true, completion: true, operation: true
};
const recordKeys = Object.keys(recordKeySet) as (keyof RecordFilters)[];

/** The Query keys each breakdown dimension owns. A row's filter replaces exactly these keys. Polling type has no record filter. */
const dimensionKeys: Record<BreakdownDim, (keyof RecordFilters)[]> = {
  statusCode: ['statusCodes'],
  userId: ['userIds'],
  uiVersion: ['uiVersions'],
  userAgent: ['userAgentContains'],
  userIp: ['userIps'],
  pollingType: []
};

export const breakdownDims: { dim: BreakdownDim; label: string }[] = [
  { dim: 'statusCode', label: 'Status code' },
  { dim: 'userId', label: 'User' },
  { dim: 'uiVersion', label: 'UI version' },
  { dim: 'userAgent', label: 'User agent' },
  { dim: 'pollingType', label: 'Polling type' },
  { dim: 'userIp', label: 'User IP' }
];

function dropKeys<T extends object>(query: T, keys: readonly (keyof RecordFilters)[]): T {
  const next = { ...query } as Record<string, unknown>;
  for (const key of keys) delete next[key];
  return next as T;
}

/** The record filters that are in use on a query, in the Task 5 wire form (unused ones are absent). */
export const activeRecordFilter = (query: Partial<Record<keyof RecordFilters, unknown>>): RecordFilters => serializeRecordFilters(query);

export const hasRecordFilter = (query: Partial<Record<keyof RecordFilters, unknown>>) => Object.keys(serializeRecordFilters(query)).length > 0;

/**
 * The Query fragment a row selects, or null when the row cannot be listed (a missing value, a polling type,
 * a value too long or cut off, or the Other row). The server builds it; the UI only checks that it still
 * survives the shared record filter serialization.
 */
export function breakdownRowFilter(row: BreakdownRow): BreakdownFilter | null {
  if (!row.filter) return null;
  const { userIds, uiVersions, userAgentContains, userIps, statusCodes } = serializeRecordFilters(row.filter);
  const out: BreakdownFilter = {
    ...(userIds ? { userIds } : {}), ...(uiVersions ? { uiVersions } : {}), ...(userAgentContains ? { userAgentContains } : {}),
    ...(userIps ? { userIps } : {}), ...(statusCodes ? { statusCodes } : {})
  };
  return Object.keys(out).length ? out : null;
}

/** Applies a row's filter to the query. The row's dimension is replaced; every other filter and query field is kept. Never mutates its input. */
export function mergeRecordFilter(query: Query, filter: BreakdownFilter | null): Query {
  if (!filter) return query;
  const touched = breakdownDims.flatMap(({ dim }) => dimensionKeys[dim].filter(key => filter[key as keyof BreakdownFilter] !== undefined));
  const merged = { ...dropKeys(query, touched), ...filter };
  return { ...dropKeys(merged, recordKeys), ...serializeRecordFilters(merged) };
}

/** The query without one dimension's filter, so that dimension's breakdown still lists every value. */
export const withoutDimension = (query: Query, dim: BreakdownDim): Query => dropKeys(query, dimensionKeys[dim]);

/** The query with no record filter at all. */
export const clearRecordFilter = (query: Query): Query => dropKeys(query, recordKeys);

/** The base query with the drill-down's record filters in place of its own. An empty drill-down leaves the base untouched. */
export const applyDrill = (base: Query, drill: RecordFilters): Query => hasRecordFilter(drill) ? { ...clearRecordFilter(base), ...serializeRecordFilters(drill) } : base;

/** Readable chips for the breakdown dimensions that are filtered on a query: for example "User agent contains: Demo Browser/1.0". */
export function recordFilterChips(query: Partial<Record<keyof RecordFilters, unknown>>): { dim: BreakdownDim; text: string }[] {
  const active = serializeRecordFilters(query);
  const chips: { dim: BreakdownDim; text: string }[] = [];
  const add = (dim: BreakdownDim, label: string, values: readonly (string | number)[] | string | undefined) => {
    const list = typeof values === 'string' ? [values] : (values ?? []);
    if (list.length) chips.push({ dim, text: label + ': ' + list.join(', ') });
  };
  add('statusCode', 'Status code', active.statusCodes);
  add('userId', 'User', active.userIds);
  add('uiVersion', 'UI version', active.uiVersions);
  add('userAgent', 'User agent contains', active.userAgentContains);
  add('userIp', 'User IP', active.userIps);
  return chips;
}
