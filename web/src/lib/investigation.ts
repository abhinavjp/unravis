import type { Completion, NumberRange, Query, RangeKey, RecordFilters, Source } from './types';

export const INVESTIGATION_STORAGE_KEY = 'api-audit-explorer.investigation.v1';
const VERSION = 1;
const MAX_SNAPSHOT_LENGTH = 16_384;
export type InvestigationMode = 'live' | 'demo';
export const investigationStorageKey = (mode: InvestigationMode) => `${INVESTIGATION_STORAGE_KEY}.${mode}`;
const validZone = (zone: string) => {
  try { new Intl.DateTimeFormat(undefined, { timeZone: zone }); return true; } catch { return false; }
};

const COMPLETIONS: readonly string[] = ['complete', 'incomplete', 'pending', 'invalid'];
/** The server refuses more than 20 values in a list filter and more than 50 user ids. */
export const MAX_FILTER_VALUES = 20;
export const MAX_USER_IDS = 50;
const MAX_USER_ID = 2_147_483_647;
/** User ids are stored as 32-bit integers, so the server accepts 1 to 2147483647. */
export const isValidUserId = (id: number): boolean => Number.isSafeInteger(id) && id >= 1 && id <= MAX_USER_ID;
/** An operation is Controller.Action: a dot with text on both sides. */
export const isValidOperation = (text: string): boolean => {
  const value = text.trim();
  const dot = value.indexOf('.');
  return dot > 0 && dot < value.length - 1;
};
/** Splits comma-separated text into trimmed, distinct, non-empty items. */
export const splitList = (text: string): string[] => [...new Set(text.split(',').map(item => item.trim()).filter(Boolean))];
/** How many items in the comma-separated text come after the first `max`, and so are not used. */
export const itemsOverLimit = (text: string, max: number): number => Math.max(0, splitList(text).length - max);
const sameItems =(a: readonly string[], b: readonly string[]) => a.length === b.length && a.every((item, index) => item === b[index]);
/**
 * syncListDraft returns the text a comma-list box should show for the applied
 * filter values. The draft is kept as typed while it parses (keeping only items
 * that pass `keep`, in the numeric case compared as numbers) to exactly the applied
 * values, so "1, " or a rejected token survives typing; otherwise it is rewritten
 * from the applied values, which is how a restored or cleared query reaches the box.
 */
export const syncListDraft = (draft: string, applied: readonly (string | number)[] | undefined, keep: (item: string) => boolean = () => true, max = Infinity): string => {
  const want = (applied ?? []).map(String);
  const numeric = (applied ?? []).some(item => typeof item === 'number');
  const parsed = splitList(draft).filter(keep).slice(0, max).map(item => (numeric ? String(Number(item)) : item));
  return sameItems([...new Set(parsed)], want) ? draft : want.join(', ');
};
/** The same for the Operation box, whose applied value is undefined while the draft is invalid. */
export const syncOperationDraft = (draft: string, applied: string | undefined): string => {
  const parsed = draft.trim() && isValidOperation(draft) ? draft.trim() : undefined;
  return parsed === applied ? draft : (applied ?? '');
};
/** The server rejects a millisecond bound above 1e9 and a payload-size bound above 1e7 KB. */
export const MAX_RANGE_MS = 1_000_000_000;
export const MAX_RANGE_KB = 10_000_000;
/** Range filters in the order they are serialized, with the unit and the largest bound the server accepts. */
export const RANGE_FIELDS: readonly { key: RangeKey; unit: 'ms' | 'KB'; limit: number }[] = [
  { key: 'observedMs', unit: 'ms', limit: MAX_RANGE_MS },
  { key: 'apiTimeMs', unit: 'ms', limit: MAX_RANGE_MS },
  { key: 'auditCreateTimeMs', unit: 'ms', limit: MAX_RANGE_MS },
  { key: 'requestKb', unit: 'KB', limit: MAX_RANGE_KB },
  { key: 'responseKb', unit: 'KB', limit: MAX_RANGE_KB },
  { key: 'incompleteAgeMs', unit: 'ms', limit: MAX_RANGE_MS }
];
const NUMBER_TEXT = /^(\d+\.?\d*|\.\d+)$/;
const SIGNED_NUMBER_TEXT = /^-?(\d+\.?\d*|\.\d+)$/;
/**
 * rangeBoundProblem explains why a typed bound cannot be applied, or returns ''
 * when it is empty or fine. Negative numbers, anything that is not a plain decimal
 * and anything above the server's limit are all problems.
 */
export const rangeBoundProblem = (text: string, limit: number): string => {
  const value = text.trim();
  if (!value) return '';
  if (!SIGNED_NUMBER_TEXT.test(value)) return `"${value}" is not a number`;
  if (!NUMBER_TEXT.test(value)) return `${value} is negative`;
  return Number(value) > limit ? `${value} is above the limit of ${limit.toLocaleString('en-US')}` : '';
};
/**
 * buildRange turns the two typed bounds into the range the query carries, and
 * says what is wrong with them. A bound that has a problem is not applied, and
 * a minimum above the maximum applies neither, so the query never holds a range
 * the server would reject. A range with no usable bound is undefined.
 */
export const buildRange = (minText: string, maxText: string, limit: number): { range: NumberRange | undefined; problems: string[] } => {
  const problems: string[] = [];
  const read = (label: string, text: string): number | undefined => {
    const problem = rangeBoundProblem(text, limit);
    if (problem) problems.push(`${label}: ${problem}`);
    return !problem && text.trim() ? Number(text.trim()) : undefined;
  };
  const min = read('Minimum', minText);
  const max = read('Maximum', maxText);
  if (min != null && max != null && min > max) return { range: undefined, problems: [...problems, 'The minimum is above the maximum, so neither is applied'] };
  const range: NumberRange = {};
  if (min != null) range.min = min;
  if (max != null) range.max = max;
  return { range: min != null || max != null ? range : undefined, problems };
};
const sameRange = (a: NumberRange | undefined, b: NumberRange | undefined) => (a?.min ?? null) === (b?.min ?? null) && (a?.max ?? null) === (b?.max ?? null);
/**
 * syncRangeDraft is syncListDraft for a min/max pair: the typed text is kept while
 * it builds to exactly the applied range (so "5." or a rejected bound survives
 * typing), and otherwise both boxes are rewritten from the applied range, which is
 * how a restored or cleared query reaches them.
 */
export const syncRangeDraft = (draft: { min: string; max: string }, applied: NumberRange | undefined, limit: number): { min: string; max: string } =>
  sameRange(buildRange(draft.min, draft.max, limit).range, applied) ? draft : { min: applied?.min != null ? String(applied.min) : '', max: applied?.max != null ? String(applied.max) : '' };
const cleanRange = (value: unknown, limit: number): NumberRange | undefined => {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return undefined;
  const raw = value as Record<string, unknown>;
  const usable = (bound: unknown): number | undefined => typeof bound === 'number' && Number.isFinite(bound) && bound >= 0 && bound <= limit ? bound : undefined;
  const min = usable(raw.min);
  const max = usable(raw.max);
  if (min != null && max != null && min > max) return undefined;
  if (min == null && max == null) return undefined;
  return { ...(min != null ? { min } : {}), ...(max != null ? { max } : {}) };
};
const RANGE_IMPLIED: Record<RangeKey, { completion: readonly string[]; label: string }> = {
  observedMs: { completion: ['complete'], label: 'Complete' },
  apiTimeMs: { completion: ['complete'], label: 'Complete' },
  auditCreateTimeMs: { completion: ['complete'], label: 'Complete' },
  requestKb: { completion: [], label: '' },
  responseKb: { completion: [], label: '' },
  incompleteAgeMs: { completion: ['incomplete', 'pending'], label: 'Incomplete or Pending' }
};
/**
 * rangeConflict describes why the server would refuse the ranges together with
 * the completion filter: a time range only describes complete records and the age
 * range only incomplete ones. It returns '' when they agree.
 */
export const rangeConflict = (filters: Pick<RecordFilters, RangeKey | 'completion'>): string => {
  const used = (key: RangeKey) => filters[key] != null && (filters[key]!.min != null || filters[key]!.max != null);
  const time = (['observedMs', 'apiTimeMs', 'auditCreateTimeMs'] as RangeKey[]).some(used);
  const age = used('incompleteAgeMs');
  if (time && age) return 'A time range describes complete requests and the age range incomplete ones, so they cannot be combined.';
  const completion = filters.completion;
  if (!completion || completion === 'all') return '';
  if (time && !RANGE_IMPLIED.observedMs.completion.includes(completion)) return 'A time range only describes complete requests. Set Completion to Any or Complete.';
  if (age && !RANGE_IMPLIED.incompleteAgeMs.completion.includes(completion)) return 'The age range only describes incomplete requests. Set Completion to Any, Incomplete or Pending.';
  return '';
};
const cleanStrings = (value: unknown, max: number): string[] | undefined => {
  if (!Array.isArray(value)) return undefined;
  const items = [...new Set(value.filter((item): item is string => typeof item === 'string').map(item => item.trim()).filter(Boolean))].slice(0, max);
  return items.length ? items : undefined;
};
const cleanText = (value: unknown): string | undefined => {
  const text = typeof value === 'string' ? value.trim() : '';
  return text ? text : undefined;
};

/**
 * serializeRecordFilters keeps only the record filters that are in use, in a
 * fixed key order, so that an unused filter never reaches the server, the saved
 * investigation or the staleness fingerprint. Anything that is not the expected
 * type is dropped, which lets a snapshot saved before these filters existed (or
 * a hand-edited one) load as "no filters".
 */
export function serializeRecordFilters(source: Partial<Record<keyof RecordFilters, unknown>> | null | undefined): RecordFilters {
  const out: RecordFilters = {};
  if (!source) return out;
  if (Array.isArray(source.userIds)) {
    const ids = [...new Set(source.userIds.filter((id): id is number => typeof id === 'number' && isValidUserId(id)))].slice(0, MAX_USER_IDS);
    if (ids.length) out.userIds = ids;
  }
  const uiVersions = cleanStrings(source.uiVersions, MAX_FILTER_VALUES);
  if (uiVersions) out.uiVersions = uiVersions;
  const agent = cleanText(source.userAgentContains);
  if (agent) out.userAgentContains = agent;
  const ips = cleanStrings(source.userIps, MAX_FILTER_VALUES);
  if (ips) out.userIps = ips;
  const url = cleanText(source.urlContains);
  if (url) out.urlContains = url;
  const auditId = cleanText(source.auditId);
  if (auditId) out.auditId = auditId;
  const statusCodes = cleanStrings(source.statusCodes, MAX_FILTER_VALUES);
  if (statusCodes) out.statusCodes = statusCodes;
  if (typeof source.completion === 'string' && COMPLETIONS.includes(source.completion)) out.completion = source.completion as Completion;
  const operation = cleanText(source.operation);
  if (operation && isValidOperation(operation)) out.operation = operation;
  for (const { key, limit } of RANGE_FIELDS) {
    const range = cleanRange(source[key], limit);
    if (range) out[key] = range;
  }
  return out;
}

export interface InvestigationSnapshot {
  version: 1;
  query: Omit<Query, 'sourceIds'>;
  selectedSources: Array<{ id: string; selectionKey?: string }>;
  knownSources: Array<{ id: string; selectionKey?: string }>;
  zone: string;
  targetMs: number | null;
}

const validUtc = (value: unknown): value is string => typeof value === 'string'
  && /^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d\.\d{3}Z$/.test(value)
  && Number.isFinite(Date.parse(value)) && new Date(value).toISOString() === value;

type SourceDescriptor = InvestigationSnapshot['knownSources'][number];
const descriptor = (source: Source): SourceDescriptor => ({ id: source.id, ...(source.selectionKey ? { selectionKey: source.selectionKey } : {}) });
const sameDescriptor = (saved: SourceDescriptor, source: Source) => saved.selectionKey
  ? source.selectionKey === saved.selectionKey
  : source.id === saved.id;
const uniqueDescriptors = (descriptors: SourceDescriptor[]) => descriptors.filter((item, index) =>
  descriptors.findIndex(other => other.selectionKey && item.selectionKey
    ? other.selectionKey === item.selectionKey
    : !other.selectionKey && !item.selectionKey && other.id === item.id) === index);

export function createInvestigationSnapshot(query: Query, sources: Source[], zone: string, targetMs: number | null, previous: InvestigationSnapshot | null = null): InvestigationSnapshot {
  const sourceById = new Map(sources.map(source => [source.id, source]));
  const currentSelected = query.sourceIds.flatMap(id => {
    const source = sourceById.get(id);
    return source ? [descriptor(source)] : [];
  });
  const currentKnown = uniqueDescriptors(sources.map(descriptor));
  const unresolvedKnown = (previous?.knownSources ?? []).filter(saved => !sources.some(source => sameDescriptor(saved, source)));
  const knownSources = [...currentKnown, ...unresolvedKnown.slice(0, Math.max(0, 8 - currentKnown.length))];
  const retained = (saved: SourceDescriptor) => knownSources.some(known => known.selectionKey && saved.selectionKey
    ? known.selectionKey === saved.selectionKey
    : !known.selectionKey && !saved.selectionKey && known.id === saved.id);
  const unresolvedSelected = (previous?.selectedSources ?? []).filter(saved => retained(saved) && !sources.some(source => sameDescriptor(saved, source)));
  return {
    version: VERSION,
    query: {
      start: query.start, end: query.end, baselineStart: query.baselineStart, baselineEnd: query.baselineEnd,
      kind: query.kind, outcome: query.outcome, metric: query.metric, bucketSeconds: query.bucketSeconds,
      controller: query.controller, action: query.action, ...serializeRecordFilters(query)
    },
    selectedSources: uniqueDescriptors([...currentSelected, ...unresolvedSelected]).slice(0, 8),
    knownSources,
    zone: validZone(zone) ? zone : 'UTC',
    targetMs: targetMs != null && Number.isFinite(targetMs) && targetMs > 0 ? targetMs : null
  };
}

export function validateInvestigationSnapshot(value: unknown): value is InvestigationSnapshot {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return false;
  const snapshot = value as Record<string, unknown>;
  if (snapshot.version !== VERSION || !snapshot.query || typeof snapshot.query !== 'object' || Array.isArray(snapshot.query)
      || !Array.isArray(snapshot.selectedSources) || snapshot.selectedSources.length > 8
      || (snapshot.knownSources !== undefined && (!Array.isArray(snapshot.knownSources) || snapshot.knownSources.length > 8))
      || typeof snapshot.zone !== 'string' || !validZone(snapshot.zone)
      || !(snapshot.targetMs === null || (typeof snapshot.targetMs === 'number' && Number.isFinite(snapshot.targetMs) && snapshot.targetMs > 0))) return false;
  const query = snapshot.query as Record<string, unknown>;
  if (![query.start, query.end, query.baselineStart, query.baselineEnd].every(validUtc)) return false;
  const start = Date.parse(query.start as string); const end = Date.parse(query.end as string);
  const baselineStart = Date.parse(query.baselineStart as string); const baselineEnd = Date.parse(query.baselineEnd as string);
  if (!(baselineStart < baselineEnd && baselineEnd <= start && start < end)
      || end - start > 7 * 86_400_000 || baselineEnd - baselineStart > 7 * 86_400_000
      || typeof query.kind !== 'string' || !['synchronous', 'asynchronous', 'hangfire', 'http', 'http_unknown', 'all'].includes(query.kind)
      || typeof query.outcome !== 'string' || !['success', 'failure', 'client_error', 'server_error', 'all'].includes(query.outcome)
      || typeof query.metric !== 'string' || !['total', 'api'].includes(query.metric)
      || !Number.isInteger(query.bucketSeconds) || Number(query.bucketSeconds) < 10 || Number(query.bucketSeconds) > 86_400
      || typeof query.controller !== 'string' || query.controller.length > 200
      || typeof query.action !== 'string' || query.action.length > 200) return false;
  const validDescriptor = (item: unknown) => !!item && typeof item === 'object' && !Array.isArray(item)
    && typeof (item as Record<string, unknown>).id === 'string' && !!(item as Record<string, unknown>).id
    && ((item as Record<string, unknown>).id as string).length <= 256
    && ((item as Record<string, unknown>).selectionKey === undefined
      || (typeof (item as Record<string, unknown>).selectionKey === 'string' && !!(item as Record<string, unknown>).selectionKey
        && ((item as Record<string, unknown>).selectionKey as string).length <= 128));
  return snapshot.selectedSources.every(validDescriptor) && (snapshot.knownSources === undefined || snapshot.knownSources.every(validDescriptor));
}

export function parseInvestigationSnapshot(raw: string | null): InvestigationSnapshot | null {
  if (!raw || raw.length > MAX_SNAPSHOT_LENGTH) return null;
  try {
    const parsed: unknown = JSON.parse(raw);
    if (!validateInvestigationSnapshot(parsed)) return null;
    const q = parsed.query;
    return {
      version: VERSION,
      query: {
        start: q.start, end: q.end, baselineStart: q.baselineStart, baselineEnd: q.baselineEnd,
        kind: q.kind, outcome: q.outcome, metric: q.metric, bucketSeconds: q.bucketSeconds,
        controller: q.controller, action: q.action, ...serializeRecordFilters(q)
      },
      selectedSources: parsed.selectedSources.map(source => ({ id: source.id, ...(source.selectionKey ? { selectionKey: source.selectionKey } : {}) })),
      knownSources: (parsed.knownSources ?? parsed.selectedSources).map(source => ({ id: source.id, ...(source.selectionKey ? { selectionKey: source.selectionKey } : {}) })),
      zone: parsed.zone,
      targetMs: parsed.targetMs
    };
  }
  catch { return null; }
}

export function restoreInvestigation(snapshot: InvestigationSnapshot, sources: Source[]): Query & { zone: string; targetMs: number | null } {
  const ids = snapshot.selectedSources.flatMap(saved => {
    if (saved.selectionKey) {
      const match = sources.find(source => source.selectionKey === saved.selectionKey);
      return match ? [match.id] : [];
    }
    return sources.some(source => source.id === saved.id) ? [saved.id] : [];
  });
  const query = snapshot.query;
  return {
    start: query.start, end: query.end, baselineStart: query.baselineStart, baselineEnd: query.baselineEnd,
    kind: query.kind, outcome: query.outcome, metric: query.metric, bucketSeconds: query.bucketSeconds,
    controller: query.controller, action: query.action, ...serializeRecordFilters(query), sourceIds: [...new Set(ids)], zone: snapshot.zone, targetMs: snapshot.targetMs
  };
}

export function shouldSelectAddedSource(source: Source, snapshot: InvestigationSnapshot | null): boolean {
  if (!snapshot) return true;
  const known = snapshot.knownSources.find(saved => sameDescriptor(saved, source));
  if (!known) return true;
  return snapshot.selectedSources.some(saved => sameDescriptor(saved, source));
}

export function hasUnresolvedKnownSources(snapshot: InvestigationSnapshot | null, sources: Source[]): boolean {
  return !!snapshot?.knownSources.some(saved => !sources.some(source => sameDescriptor(saved, source)));
}

export function forgetInvestigationSource(snapshot: InvestigationSnapshot | null, source: Source): InvestigationSnapshot | null {
  if (!snapshot) return null;
  return {
    ...snapshot,
    knownSources: snapshot.knownSources.filter(saved => !sameDescriptor(saved, source)),
    selectedSources: snapshot.selectedSources.filter(saved => !sameDescriptor(saved, source))
  };
}

export function readInvestigationSnapshot(storage?: Pick<Storage, 'getItem'>, mode: InvestigationMode = 'live'): InvestigationSnapshot | null {
  try { return parseInvestigationSnapshot((storage ?? window.sessionStorage).getItem(investigationStorageKey(mode))); } catch { return null; }
}

export function writeInvestigationSnapshot(storage: Pick<Storage, 'setItem'> | undefined, snapshot: InvestigationSnapshot, mode: InvestigationMode = 'live'): boolean {
  if (!validateInvestigationSnapshot(snapshot)) return false;
  try { (storage ?? window.sessionStorage).setItem(investigationStorageKey(mode), JSON.stringify(parseInvestigationSnapshot(JSON.stringify(snapshot)))); return true; } catch { return false; }
}

export function clearInvestigationSnapshot(storage?: Pick<Storage, 'removeItem'>, mode: InvestigationMode = 'live'): void {
  try { (storage ?? window.sessionStorage).removeItem(investigationStorageKey(mode)); } catch { /* unavailable storage is nonfatal */ }
}
