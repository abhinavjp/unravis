import type { BreakdownDim, BreakdownRow, Query, RecordFilter } from './types';

/** The Query keys each breakdown dimension owns. A row's filter replaces exactly these keys. */
const dimensionKeys: Record<BreakdownDim, (keyof RecordFilter)[]> = {
  statusCode: ['statusCodes'],
  userId: ['userIds', 'userIdMissing'],
  uiVersion: ['uiVersions'],
  userAgent: ['userAgents'],
  userIp: ['userIps'],
  pollingType: ['pollingTypes']
};
const allKeys = (Object.values(dimensionKeys) as (keyof RecordFilter)[][]).flat();

export const breakdownDims: { dim: BreakdownDim; label: string }[] = [
  { dim: 'statusCode', label: 'Status code' },
  { dim: 'userId', label: 'User' },
  { dim: 'uiVersion', label: 'UI version' },
  { dim: 'userAgent', label: 'User agent' },
  { dim: 'pollingType', label: 'Polling type' },
  { dim: 'userIp', label: 'User IP' }
];

const isSet = (value: unknown) => Array.isArray(value) ? value.length > 0 : value === true;

/** The filter fields that are actually active on a query (empty lists and false flags are not). */
export function activeRecordFilter(query: RecordFilter): RecordFilter {
  const out: Record<string, unknown> = {};
  for (const key of allKeys) {
    const value = query[key];
    if (isSet(value)) out[key] = Array.isArray(value) ? [...value] : value;
  }
  return out as RecordFilter;
}

export const hasRecordFilter = (query: RecordFilter) => Object.keys(activeRecordFilter(query)).length > 0;

/** The Query fragment a row selects, or null for the Other row. The server builds it; the UI applies it unchanged. */
export const breakdownRowFilter = (row: BreakdownRow): RecordFilter | null => row.filter && hasRecordFilter(row.filter) ? row.filter : null;

const dimensionOf = (key: keyof RecordFilter): BreakdownDim => {
  for (const dim of Object.keys(dimensionKeys) as BreakdownDim[]) if (dimensionKeys[dim].includes(key)) return dim;
  throw new Error('unknown record filter key ' + key);
};

function drop(query: Query, dims: Set<BreakdownDim>): Query {
  const next: Query = { ...query };
  for (const key of allKeys) if (dims.has(dimensionOf(key))) delete next[key];
  return next;
}

/** Applies a row's filter to the query. The row's dimension is replaced; every other filter and query field is kept. Never mutates its input. */
export function mergeRecordFilter(query: Query, filter: RecordFilter | null): Query {
  if (!filter) return query;
  const touched = new Set<BreakdownDim>();
  for (const key of allKeys) if (isSet(filter[key])) touched.add(dimensionOf(key));
  const next = drop(query, touched);
  return { ...next, ...activeRecordFilter(filter) };
}

/** The query without one dimension's filter, so that dimension's breakdown still lists every value. */
export const withoutDimension = (query: Query, dim: BreakdownDim): Query => drop(query, new Set([dim]));

/** The query with no record filter at all. */
export const clearRecordFilter = (query: Query): Query => drop(query, new Set(Object.keys(dimensionKeys) as BreakdownDim[]));

/** Readable chips for the active filter, one per dimension: for example "User agent: Demo Browser/1.0". */
export function recordFilterChips(query: RecordFilter, missingLabel = '(not yet written)'): { dim: BreakdownDim; text: string }[] {
  const chips: { dim: BreakdownDim; text: string }[] = [];
  for (const { dim, label } of breakdownDims) {
    const values: string[] = [];
    if (dim === 'userId') {
      if (query.userIdMissing) values.push(missingLabel);
      for (const id of query.userIds ?? []) values.push(String(id));
    } else {
      const list = dim === 'statusCode' ? query.statusCodes : dim === 'uiVersion' ? query.uiVersions : dim === 'userAgent' ? query.userAgents : dim === 'userIp' ? query.userIps : query.pollingTypes;
      for (const value of list ?? []) values.push(value.trim() === '' ? missingLabel : value);
    }
    if (values.length) chips.push({ dim, text: label + ': ' + values.join(', ') });
  }
  return chips;
}
