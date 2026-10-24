/** A duration on a chart axis or in a tooltip: whole milliseconds below a second, then seconds with as few decimals as read well. Never "0.0 ms". */
export const axisDuration = (ms: number): string => {
  if (!Number.isFinite(ms)) return '';
  if (Math.abs(ms) < 1000) return Math.round(ms) + ' ms';
  const seconds = ms / 1000;
  return Number(seconds.toFixed(seconds < 10 ? 2 : 1)).toLocaleString('en-US') + ' s';
};

const unitOf = (ms: number) => Math.abs(ms) >= 1000 ? 's' : 'ms';
const bare = (ms: number) => unitOf(ms) === 's' ? String(Number((ms / 1000).toFixed(2))) : String(Math.round(ms));
/** "50–75 ms", "1–2 s", "500 ms–1 s": the unit is printed once when both ends share it. */
export const rangeLabel = (lower: number, upper: number | null): string => {
  if (upper == null) return '≥ ' + axisDuration(lower);
  return unitOf(lower) === unitOf(upper) ? bare(lower) + '–' + axisDuration(upper) : axisDuration(lower) + '–' + axisDuration(upper);
};

export interface RawBin { lower: number; upper: number | null; current: number; reference: number }
export interface NiceBin extends RawBin { label: string }

const linearSteps = [1, 2, 5, 10, 20, 25, 50, 100, 200, 250, 500, 1000, 2000, 2500, 5000, 10_000, 20_000, 25_000, 50_000, 100_000, 250_000, 500_000, 1_000_000];
/** 1, 2, 5, 10, 20, 50 ... the ones that bracket lo and hi. */
const logEdges = (lo: number, hi: number): number[] => {
  const all: number[] = [];
  for (let d = 1; d <= 1e9; d *= 10) all.push(d, d * 2, d * 5);
  const below = all.filter(edge => edge <= lo).pop() ?? all[0];
  const above = all.find(edge => edge >= hi) ?? all[all.length - 1];
  return all.filter(edge => edge >= below && edge <= above);
};

/**
 * Regroups the server's finer response-time ranges (each 10 % wider than the last) into ranges with round edges: 10, 20, 25 or 50 ms steps
 * when the data sits in a narrow band, 1-2-5 steps (10, 20, 50, 100 ... ms) when it spans orders of magnitude. A finer range goes whole into the round range
 * that holds its middle, so a request near an edge can land in the neighbour. Only ranges that hold requests set the span; an overflow range stays "at least".
 */
export function niceHistogram(bins: RawBin[]): NiceBin[] {
  const populated = bins.filter(bin => bin.current + bin.reference > 0);
  if (!populated.length) return [];
  const finite = populated.filter(bin => bin.upper != null);
  const overflow = populated.filter(bin => bin.upper == null);
  const lo = finite.length ? Math.min(...finite.map(bin => bin.lower)) : 0;
  const hi = finite.length ? Math.max(...finite.map(bin => bin.upper as number)) : 0;
  let edges: number[] = [];
  if (finite.length) {
    if (hi / Math.max(lo, 1) <= 12 && hi - lo >= 1) {
      const step = linearSteps.find(candidate => Math.ceil(hi / candidate) - Math.floor(lo / candidate) <= 10) ?? linearSteps[linearSteps.length - 1];
      for (let edge = Math.floor(lo / step) * step; edge < hi + step && edges.length < 40; edge += step) { edges.push(edge); if (edge >= hi) break; }
    } else {
      edges = logEdges(lo, hi);
      if (lo < 1) edges = [0, ...edges];
    }
    if (edges.length < 2) edges = [Math.floor(lo), Math.floor(lo) + 1];
  }
  const out: NiceBin[] = edges.slice(0, -1).map((lower, i) => ({ lower, upper: edges[i + 1], current: 0, reference: 0, label: rangeLabel(lower, edges[i + 1]) }));
  for (const bin of finite) {
    const middle = bin.lower <= 0 ? bin.upper as number / 2 : Math.sqrt(bin.lower * (bin.upper as number));
    const target = out.findIndex(item => middle >= item.lower && middle < (item.upper as number));
    const slot = out[target < 0 ? out.length - 1 : target];
    slot.current += bin.current; slot.reference += bin.reference;
  }
  if (overflow.length) {
    const lower = Math.min(...overflow.map(bin => bin.lower));
    out.push({ lower, upper: null, current: overflow.reduce((sum, bin) => sum + bin.current, 0), reference: overflow.reduce((sum, bin) => sum + bin.reference, 0), label: rangeLabel(lower, null) + ' (overflow)' });
  }
  return out;
}
