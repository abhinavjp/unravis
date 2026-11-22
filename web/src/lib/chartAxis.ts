/**
 * Axis scales and number text for the Requests-over-time chart (BL-495): one precision per axis, the unit named once in the axis title,
 * and one rule (durationText) for every single duration on a chart: diamond, tooltip, note, lane text and screen-reader text.
 * Pure TypeScript, no DOM, so edge values are unit-tested.
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
  widest: string;
}

/**
 * The axis for response times in milliseconds: milliseconds below one second at the top, seconds from there. Whole milliseconds, never a
 * fraction of one (a response under 1 ms still sits on a 0 to 1 ms axis); seconds with as many decimals as the step needs.
 */
export const durationAxis = (largestMs: number, wantedTicks = 4, locale?: string | string[]): DurationAxis => {
  const top = positive(largestMs);
  // Seconds from the same point durationText switches (a top that rounds to 1000 ms is a second), so the axis and the diamond agree.
  const unit: DurationUnit = Math.round(top) >= 1000 ? 's' : 'ms';
  const factor = unit === 's' ? 1000 : 1;
  const step = niceStep(top / factor / Math.max(1, wantedTicks), unit === 'ms' ? 1 : 0);
  const decimals = decimalsFor(step);
  const topInUnit = Math.max(step, Math.ceil(Number((top / factor / step).toPrecision(12))) * step);
  const format = (ms: number) => fixedNumber(ms / factor, decimals, locale);
  const maxMs = Number((topInUnit * factor).toPrecision(12));
  return {
    unit, factor, maxMs, intervalMs: Number((step * factor).toPrecision(12)), decimals, format,
    widest: format(maxMs)
  };
};

/**
 * THE way one duration is written wherever a single request or interval figure is shown (a diamond label, a tooltip line, the sentence under a chart):
 * under 1 ms "<1 ms" (0 is "0 ms"), up to 999 ms whole milliseconds, from one second on seconds with one decimal ("65.7 s", "68.0 s"), and from 1,000 s on whole seconds ("3,600 s"; a decimal there is false precision).
 * The unit depends on the value alone, never on its neighbours, so one request reads the same on every surface and never "68,000 ms".
 * Two figures that are both a second or more therefore share a unit and a precision. Missing, negative or not finite values give null.
 */
export const durationText = (ms: number | null | undefined, locale?: string | string[]): string | null => {
  if (typeof ms !== 'number' || !Number.isFinite(ms) || ms < 0) return null;
  if (ms === 0) return '0 ms';
  if (ms < 1) return '<1 ms';
  // A value that rounds to 1000 ms is a second: 999.6 and 1000 must not read "1,000 ms" and "1.0 s".
  if (Math.round(ms) < 1000) return fixedNumber(ms, 0, locale) + ' ms';
  const seconds = ms / 1000;
  return fixedNumber(seconds, Math.round(seconds * 10) / 10 >= 1000 ? 0 : 1, locale) + ' s';
};

/** `durationText` for several values (the P95 and the slowest request of one interval). */
export const durationSet = (valuesMs: ReadonlyArray<number | null | undefined>, locale?: string | string[]): Array<string | null> =>
  valuesMs.map(value => durationText(value, locale));

/** The axis titles of the Requests-over-time chart. The unit of the latency axis follows its scale (see durationAxis). */
export const requestsAxisTitle = 'Requests per interval';
export const timeAxisTitle = (zone: string): string => 'Time (' + (zone.trim() || 'UTC') + ')';
/** The right axis holds the P95 line and the slowest request of each interval, so it is titled for the response time, not for the P95 alone. */
export const latencyAxisTitle = (unit: DurationUnit): string => 'Response time (' + unit + ')';

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
