import type { Query, Source } from './types';

export const INVESTIGATION_STORAGE_KEY = 'api-audit-explorer.investigation.v1';
const VERSION = 1;
const MAX_SNAPSHOT_LENGTH = 16_384;
export type InvestigationMode = 'live' | 'demo';
export const investigationStorageKey = (mode: InvestigationMode) => `${INVESTIGATION_STORAGE_KEY}.${mode}`;
const validZone = (zone: string) => {
  try { new Intl.DateTimeFormat(undefined, { timeZone: zone }); return true; } catch { return false; }
};

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
      controller: query.controller, action: query.action
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
      || typeof query.kind !== 'string' || !['synchronous', 'asynchronous', 'hangfire', 'all'].includes(query.kind)
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
        controller: q.controller, action: q.action
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
    controller: query.controller, action: query.action, sourceIds: [...new Set(ids)], zone: snapshot.zone, targetMs: snapshot.targetMs
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
