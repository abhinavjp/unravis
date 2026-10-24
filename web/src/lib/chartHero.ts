/** One interval of the Pulse timeline. latency is the interval's P95; max its slowest request. */
export interface HeroSlot { at: number; count: number; latency: number | null; max?: number | null }

/**
 * Values for the dashed "no timed requests here" bridge: across each run of empty intervals that sits between two intervals with a value,
 * a straight line from one to the other (end points included). Everything else is null, so solid segments are never drawn over.
 * A run at the very start or end has nothing on one side and gets no bridge. The bridge is a drawing aid, not a measurement.
 */
export function gapBridge(values: Array<number | null>): Array<number | null> {
  const out: Array<number | null> = values.map(() => null);
  let previous = -1;
  for (let i = 0; i < values.length; i++) {
    const value = values[i];
    if (value == null) continue;
    if (previous >= 0 && i - previous > 1) {
      const from = values[previous] as number;
      for (let k = previous; k <= i; k++) out[k] = from + (value - from) * (k - previous) / (i - previous);
    }
    previous = i;
  }
  return out;
}

const median = (values: number[]) => {
  if (!values.length) return null;
  const sorted = values.slice().sort((a, b) => a - b);
  const mid = Math.floor(sorted.length / 2);
  return sorted.length % 2 ? sorted[mid] : (sorted[mid - 1] + sorted[mid]) / 2;
};

/** A slowest request at least this many times the usual interval P95 (and at least a second) is flagged as an outlier on the timeline. */
export const OUTLIER_FACTOR = 10;
export const OUTLIER_FLOOR_MS = 1000;

export interface HeroOutliers {
  /** The median of the interval P95s: what a normal interval looks like in this period. */
  typical: number | null;
  /** Indexes of intervals whose slowest request is an outlier. */
  flagged: number[];
  /** The indexes that get a printed value: the slowest few. */
  labelled: number[];
}

/** Which intervals hold a slowest request far above the usual P95, so a one-minute request is never lost between bars. */
export function heroOutliers(slots: readonly HeroSlot[], labelLimit = 3): HeroOutliers {
  const typical = median(slots.flatMap(slot => slot.latency != null && Number.isFinite(slot.latency) ? [slot.latency] : []));
  const limit = Math.max(OUTLIER_FLOOR_MS, (typical ?? 0) * OUTLIER_FACTOR);
  const flagged = slots.flatMap((slot, index) => slot.max != null && Number.isFinite(slot.max) && slot.max >= limit ? [index] : []);
  const labelled = flagged.slice().sort((a, b) => (slots[b].max as number) - (slots[a].max as number) || a - b).slice(0, labelLimit).sort((a, b) => a - b);
  return { typical, flagged, labelled };
}
