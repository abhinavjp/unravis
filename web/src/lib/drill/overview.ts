import { formatTime } from '../time';
import type { PerfPressure, PerfSlow, RecordPerf } from './perf';

export type PartKey = 'filters' | 'other';

export interface TimePart {
  key: PartKey;
  /** null means "not recorded", never 0. */
  ms: number | null;
  /** Whole percent of durationMs, null when either side is null or durationMs is 0. */
  share: number | null;
}

export interface WhereTimeWent {
  parts: TimePart[];
}

export interface SlowLine {
  index: number;
  count: number | null;
  ms: number | null;
}

export interface PressureLine {
  name: string;
  value: number | null;
}

/** Whole ms under a second, one decimal in seconds. Rounds first so 999.6 reads "1.0 s", never "1000 ms". */
export function formatMs(ms: number): string {
  const rounded = Math.round(ms);
  if (rounded >= 1000) return (ms / 1000).toFixed(1) + ' s';
  return rounded + ' ms';
}

function share(ms: number | null, durationMs: number | null): number | null {
  if (ms === null || durationMs === null || durationMs === 0) return null;
  return Math.round((ms / durationMs) * 100);
}

/**
 * Splits the call's duration into the parts the server records for each call, plus "everything else". Returns null when there is no perf block.
 * SQL, Redis and HTTP time are not captured per request yet (they are separate calls for now), so they are left out here instead of showing
 * "not recorded". They return when the server records them per request.
 */
export function whereTimeWent(perf: RecordPerf | null, durationMs: number | null): WhereTimeWent | null {
  if (perf === null) return null;
  const known: { key: Exclude<PartKey, 'other'>; ms: number | null }[] = [{ key: 'filters', ms: perf.filterTotalMs }];
  // "Everything else" is what is left after the recorded parts. If a recorded part has no value the
  // remainder cannot be trusted, so it is not recorded either (null, never a guess).
  let other: number | null = null;
  if (durationMs !== null && known.every((part) => part.ms !== null)) {
    const sum = known.reduce((total, part) => total + (part.ms as number), 0);
    other = Math.max(0, durationMs - sum);
  }
  const parts: TimePart[] = [
    ...known.map((part) => ({ key: part.key, ms: part.ms, share: share(part.ms, durationMs) })),
    { key: 'other' as const, ms: other, share: share(other, durationMs) }
  ];
  return { parts };
}

export type FactKey = 'endpoint' | 'status' | 'started' | 'duration';

export interface CallFact {
  key: FactKey;
  /** The text to show, or null when the saved record has no value (the view prints "not recorded"). */
  value: string | null;
}

/** The part of a RecordDetail the facts list reads. */
export interface FactSource {
  controller: string;
  action: string;
  url: string;
  status: string;
  initiatedAt: string | null;
  durationMs: number | null;
}

const clean = (value: string | null | undefined): string => (typeof value === 'string' ? value.trim() : '');

/**
 * The basic facts of a call, always from the record already loaded, whether or not it has timings.
 * Order: endpoint, status code, start time, total duration. A missing value is null, never a guess.
 */
export function callFacts(record: FactSource, zone: string): CallFact[] {
  const endpointParts = [clean(record.controller), clean(record.action)].filter((part) => part !== '');
  const endpoint = endpointParts.length > 0 ? endpointParts.join(' / ') : clean(record.url) || null;
  const status = clean(record.status) || null;
  const started = record.initiatedAt ? formatTime(record.initiatedAt, { zone, year: true, fallback: '' }) : '';
  const duration = typeof record.durationMs === 'number' && Number.isFinite(record.durationMs) && record.durationMs >= 0 ? formatMs(record.durationMs) : null;
  return [
    { key: 'endpoint', value: endpoint },
    { key: 'status', value: status },
    { key: 'started', value: started === '' ? null : started },
    { key: 'duration', value: duration }
  ];
}

/** Slowest SQL or HTTP groups, in server order. */
export function slowLines(list: PerfSlow[]): SlowLine[] {
  return list.map((item, index) => ({ index, count: item.count, ms: item.ms }));
}

/** Server pressure at the start of the call, as name and value. */
export function pressureLines(perf: RecordPerf | null): PressureLine[] {
  if (perf === null) return [];
  return perf.pressure.map((item: PerfPressure) => ({ name: item.name, value: item.value }));
}
