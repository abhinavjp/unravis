import type { Quantile, Stats } from './types';

export type PercentileLevel = 'p95' | 'p99';

/** Histogram quantiles are only quoted above these sample counts (matches the server's floors). */
export const percentileFloors: Record<PercentileLevel, number> = { p95: 20, p99: 100 };

export const p99NeedsSamples = 'needs ≥ 100 samples';

/**
 * Converts "at least the cohort's P95/P99" into a minimum in milliseconds for the
 * slowest-request query. The value is a histogram estimate, so it carries the bin
 * width as error. Returns null when the cohort is too small or has no usable value.
 */
export function percentileThreshold(stats: Stats | null | undefined, level: PercentileLevel): number | null {
  if (!stats || stats.count < percentileFloors[level]) return null;
  const value = (level === 'p99' ? stats.p99 : stats.p95)?.valueMs;
  return value != null && Number.isFinite(value) && value > 0 ? value : null;
}

const duration = (ms: number) => ms >= 1000
  ? (ms / 1000).toLocaleString(undefined, { maximumFractionDigits: 2 }) + ' s'
  : ms.toLocaleString(undefined, { maximumFractionDigits: 1 }) + ' ms';

export const percentileLabel = (level: PercentileLevel, ms: number) =>
  `≥ ${level.toUpperCase()} of the selected API group ≈ ${duration(ms)} (histogram estimate)`;

export const quantileText = (quantile: Quantile | null | undefined, missing = 'N/A') =>
  quantile?.valueMs == null ? missing : '≈ ' + quantile.valueMs.toFixed(1) + ' ms';
