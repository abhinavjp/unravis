import type { RecordDetail } from '../types';

/** A null field means "not recorded", never 0. */
export interface PerfTotals {
  hitCount: number | null;
  filterTotalMs: number | null;
  sqlCount: number | null;
  sqlMs: number | null;
  redisCount: number | null;
  redisMs: number | null;
  httpCount: number | null;
  httpMs: number | null;
}

export interface PerfNamedMs { name: string; ms: number | null }
export interface PerfSlow { count: number | null; ms: number | null }
export interface PerfPressure { name: string; value: number | null }

export interface RecordPerf extends PerfTotals {
  filters: PerfNamedMs[];
  steps: PerfNamedMs[];
  slowSql: PerfSlow[];
  slowHttp: PerfSlow[];
  pressure: PerfPressure[];
  textStored: boolean | null;
}

export type DrillRecord = RecordDetail & { perf: RecordPerf | null };

const LIMITS = { filters: 20, slowSql: 3, slowHttp: 3, steps: 10, pressure: 8 } as const;
const NAME_MAX = 100;

type Obj = Record<string, unknown>;

function isObject(value: unknown): value is Obj {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function num(value: unknown): number | null {
  return typeof value === 'number' && Number.isFinite(value) && value >= 0 ? value : null;
}

function name(value: unknown): string | null {
  if (typeof value !== 'string') return null;
  const trimmed = value.trim();
  return trimmed === '' ? null : trimmed.slice(0, NAME_MAX);
}

function list<T>(value: unknown, max: number, pick: (item: Obj) => T | null): T[] {
  if (!Array.isArray(value)) return [];
  const out: T[] = [];
  for (const item of value) {
    if (out.length >= max) break;
    if (!isObject(item)) continue;
    const picked = pick(item);
    if (picked !== null) out.push(picked);
  }
  return out;
}

function namedMs(item: Obj): PerfNamedMs | null {
  const n = name(item.name);
  return n === null ? null : { name: n, ms: num(item.ms) };
}

function slow(item: Obj): PerfSlow {
  return { count: num(item.count), ms: num(item.ms) };
}

function pressure(item: Obj): PerfPressure | null {
  const n = name(item.name);
  return n === null ? null : { name: n, value: num(item.value) };
}

/** Defensive parser for the server's perf block. Unknown or invalid values become null; lists are capped. */
export function parsePerf(value: unknown): RecordPerf | null {
  if (!isObject(value)) return null;
  return {
    hitCount: num(value.hitCount),
    filterTotalMs: num(value.filterTotalMs),
    sqlCount: num(value.sqlCount),
    sqlMs: num(value.sqlMs),
    redisCount: num(value.redisCount),
    redisMs: num(value.redisMs),
    httpCount: num(value.httpCount),
    httpMs: num(value.httpMs),
    filters: list(value.filters, LIMITS.filters, namedMs),
    steps: list(value.steps, LIMITS.steps, namedMs),
    slowSql: list(value.slowSql, LIMITS.slowSql, slow),
    slowHttp: list(value.slowHttp, LIMITS.slowHttp, slow),
    pressure: list(value.pressure, LIMITS.pressure, pressure),
    textStored: typeof value.textStored === 'boolean' ? value.textStored : null,
  };
}
