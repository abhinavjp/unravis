/**
 * Axis scales and number text for the Requests-over-time chart (BL-495): one precision per axis, the unit named once in the axis title,
 * and the same unit and precision for the P95 and the slowest request in a tooltip. Pure TypeScript, no DOM, so edge values are unit-tested.
 *
 * Why the chart sets its own tick step: with ECharts' automatic ticks a label set can read "0, 500 ms, 1 s, 1.5 s", or "3" next to "3.5".
 * Here the step is chosen first (1, 2 or 5 times a power of ten), the number of decimals follows from the step, and every label of the axis
 * is written with that many decimals. A locale only changes the thousands and decimal marks, never how many decimals there are.
 */

/** The 1, 2, 5 steps. */
const MULTIPLIERS = [1, 2, 5, 10];

const positive = (value: unknown): number => typeof value === 'number' && Number.isFinite(value) && value > 0 ? value : 0;

/** The smallest 1, 2 or 5 times a power of ten that is at least `raw`, and at least `floor`. Not a positive number gives `floor`. */
export const niceStep = (raw: number, floor = 0): number => {
  const want = Math.max(positive(raw), floor);
  if (want <= 0) return 1;
  const base = Math.pow(10, Math.floor(Math.log10(want)));
  for (const multiplier of MULTIPLIERS) {
    const candidate = multiplier * base;
    // A step is only "at least" the wanted size up to float noise (0.1 * 3 is 0.30000000000000004).
    if (candidate >= want * (1 - 1e-9)) return Number(candidate.toPrecision(12));
  }
  return 10 * base;
};

/** How many decimals a number needs to print exactly (0 to 4): 5 -> 0, 0.5 -> 1, 0.25 -> 2. */
export const decimalsFor = (value: number): number => {
  if (!Number.isFinite(value)) return 0;
  for (let digits = 0; digits <= 4; digits++) if (Math.abs(value * Math.pow(10, digits) - Math.round(value * Math.pow(10, digits))) < 1e-7) return digits;
  return 4;
};

/** A number with exactly `decimals` decimals and the locale's marks. A value that is not a finite number prints as an em dash, never "NaN". */
export const fixedNumber = (value: number | null | undefined, decimals: number, locale?: string | string[]): string => {
  if (value == null || !Number.isFinite(value)) return '—';
  const digits = Math.max(0, Math.min(6, Math.round(decimals)));
  // -0 would print "-0"; a value that rounds to zero prints as zero.
  const rounded = Number(value.toFixed(digits));
  return (Object.is(rounded, -0) ? 0 : rounded).toLocaleString(locale, { minimumFractionDigits: digits, maximumFractionDigits: digits });
};

export interface CountAxis {
  /** The top of the axis: a whole multiple of `interval`, at least the largest value. */
  max: number;
  /** The distance between ticks: a whole number of at least 1. */
  interval: number;
  /** A tick or figure: a whole number with the locale's thousands separators. */
  format: (value: number) => string;
  /** The longest label this axis can print, to size the margin. */
  widest: string;
}

/** The axis for counts of requests: whole numbers only, from 0. A count that is missing, negative or not finite counts as 0. */
export const countAxis = (largest: number, wantedTicks = 4, locale?: string | string[]): CountAxis => {
  const top = Math.ceil(positive(largest));
  const interval = niceStep(top / Math.max(1, wantedTicks), 1);
  const max = Math.max(wantedTicks > 0 ? interval : 1, Math.ceil(top / interval) * interval);
  const format = (value: number) => fixedNumber(value, 0, locale);
  return { max, interval, format, widest: format(max) };
};

export type DurationUnit = 'ms' | 's';

export interface DurationAxis {
  unit: DurationUnit;
  /** Milliseconds in one unit: 1 or 1000. */
  factor: number;
  /** The top of the axis in milliseconds: a whole multiple of `intervalMs`, at least the largest value. */
  maxMs: number;
  intervalMs: number;
  /** Decimals on every tick: the same on all of them. */
  decimals: number;
  /** A tick label: the number only, the unit is in the axis title. */
  format: (ms: number) => string;
  /** A figure that carries its unit ("1.5 s"), in the precision of this axis. */
  formatWithUnit: (ms: number) => string;
  widest: string;
}

/**
 * The axis for response times in milliseconds: milliseconds below one second at the top, seconds from there. Whole milliseconds, never a
 * fraction of one (a response under 1 ms still sits on a 0 to 1 ms axis); seconds with as many decimals as the step needs.
 */
export const durationAxis = (largestMs: number, wantedTicks = 4, locale?: string | string[]): DurationAxis => {
  const top = positive(largestMs);
  const unit: DurationUnit = top >= 1000 ? 's' : 'ms';
  const factor = unit === 's' ? 1000 : 1;
  const step = niceStep(top / factor / Math.max(1, wantedTicks), unit === 'ms' ? 1 : 0);
  const decimals = decimalsFor(step);
  const topInUnit = Math.max(step, Math.ceil(Number((top / factor / step).toPrecision(12))) * step);
  const format = (ms: number) => fixedNumber(ms / factor, decimals, locale);
  const maxMs = Number((topInUnit * factor).toPrecision(12));
  return {
    unit, factor, maxMs, intervalMs: Number((step * factor).toPrecision(12)), decimals, format,
    formatWithUnit: (ms: number) => format(ms) + ' ' + unit, widest: format(maxMs)
  };
};

/**
 * Several durations that are read side by side (the P95 and the slowest request of one interval): one unit and one number of decimals for all.
 * Seconds when every positive value is at least 100 ms (so the shortest still shows a digit), with one decimal when every value is 1 s or more and the largest is 10 s or more, and two otherwise,
 * else milliseconds with thousands separators ("68,000 ms"), so a short P95 is never rounded to "0.0 s". A value under 1 ms prints "<1 ms".
 * Missing or not finite values come back as null.
 */
export const durationSet = (valuesMs: ReadonlyArray<number | null | undefined>, locale?: string | string[]): Array<string | null> => {
  const finite = valuesMs.filter((value): value is number => typeof value === 'number' && Number.isFinite(value) && value >= 0);
  const largest = Math.max(0, ...finite);
  const smallestPositive = Math.min(Infinity, ...finite.filter(value => value > 0));
  const inSeconds = largest >= 1000 && smallestPositive >= 100;
  // One decimal is enough when even the shortest value is a second or more (65.7 s, 68.0 s); a shorter one keeps two digits (0.34 s, 60.00 s).
  const decimals = largest >= 10_000 && smallestPositive >= 1000 ? 1 : 2;
  return valuesMs.map(value => {
    if (typeof value !== 'number' || !Number.isFinite(value) || value < 0) return null;
    if (inSeconds) return fixedNumber(value / 1000, decimals, locale) + ' s';
    if (value > 0 && value < 1) return '<1 ms';
    return fixedNumber(value, 0, locale) + ' ms';
  });
};

/** The axis titles of the Requests-over-time chart. The unit of the latency axis follows its scale (see durationAxis). */
export const requestsAxisTitle = 'Requests per interval';
export const timeAxisTitle = (zone: string): string => 'Time (' + (zone.trim() || 'UTC') + ')';
export const latencyAxisTitle = (label: string, unit: DurationUnit): string => label + ' response time (' + unit + ')';

/**
 * Width in pixels of the widest label, for the margin beside an axis. Canvas text is not measured in tests, so this is an estimate: 7.2 px per
 * character at 12 px, which errs a little wide (a digit is 6.7 px) and so never clips.
 */
export const labelWidth = (label: string, perChar = 7.2): number => Math.ceil(label.length * perChar);

/**
 * The labels of a short list of tick values in milliseconds (the lane axis shows 0, half and the top): one unit for all, the unit kept in each
 * label, and the same decimals on every one, so a lane axis never reads "0 ms, 1.25 s, 2.5 s" or "0 ms, 1 ms, 1 ms" for 0, 0.5 and 1 ms.
 * Seconds from 1 s at the top; decimals are what the finest tick needs (at most 3). Values that are not finite print as an em dash.
 */
export const tickLabels = (valuesMs: ReadonlyArray<number>, locale?: string | string[]): string[] => {
  const finite = valuesMs.filter(value => Number.isFinite(value));
  const largest = Math.max(0, ...finite.map(Math.abs));
  const unit: DurationUnit = largest >= 1000 ? 's' : 'ms';
  const factor = unit === 's' ? 1000 : 1;
  const decimals = Math.min(3, Math.max(0, ...finite.map(value => decimalsFor(Number((value / factor).toPrecision(10))))));
  return valuesMs.map(value => Number.isFinite(value) ? fixedNumber(value / factor, decimals, locale) + ' ' + unit : '—');
};
