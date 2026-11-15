// Overview strip (minimap) model for the Slow call screen: pure maths, no drawing.

export interface MapCell { start: number; end: number; atOnce: number | null }
export interface MapColumn { start: number; end: number; height: number | null } // 0..1 of the row's own peak; null = not recorded
export interface MapRow { key: 'audit' | 'iis' | 'sql'; columns: MapColumn[]; peak: { atOnce: number; start: number } | null }

export const KEY_STEP_FRACTION = 0.05; // arrow key moves the box 5% of the strip

function isNumber(value: number | null): value is number {
  return typeof value === 'number' && Number.isFinite(value);
}

// Each row is scaled to its own busiest column. Gaps stay gaps (null); an all-zero row draws height 0.
export function mapRow(key: MapRow['key'], cells: readonly MapCell[]): MapRow {
  let peak: MapRow['peak'] = null;
  for (const cell of cells) {
    if (isNumber(cell.atOnce) && (peak === null || cell.atOnce > peak.atOnce)) {
      peak = { atOnce: cell.atOnce, start: cell.start };
    }
  }
  const max = peak === null ? 0 : peak.atOnce;
  const columns = cells.map((cell): MapColumn => ({
    start: cell.start,
    end: cell.end,
    height: !isNumber(cell.atOnce) ? null : max > 0 ? Math.min(1, Math.max(0, cell.atOnce / max)) : 0
  }));
  return { key, columns, peak };
}

// The box over the strip for the visible window, as fractions of the strip. Clamped to stay inside it.
export function boxOf(
  overview: { start: number; end: number },
  win: { start: number; end: number }
): { left: number; width: number } {
  const span = overview.end - overview.start;
  if (!(span > 0)) return { left: 0, width: 1 };
  const rawWidth = (win.end - win.start) / span;
  const width = Number.isFinite(rawWidth) ? Math.min(1, Math.max(Number.EPSILON, rawWidth)) : 1;
  const rawLeft = (win.start - overview.start) / span;
  const left = Number.isFinite(rawLeft) ? Math.min(1 - width, Math.max(0, rawLeft)) : 0;
  return { left, width };
}

// Where a time sits along the strip (0..1), or null when it is outside the overview.
export function fractionOf(overview: { start: number; end: number }, tMs: number): number | null {
  const span = overview.end - overview.start;
  if (!(span > 0) || !Number.isFinite(tMs) || tMs < overview.start || tMs > overview.end) return null;
  return (tMs - overview.start) / span;
}

// The time (ms) under a pointer position along the strip; the fraction is clamped to 0..1.
export function centreFromFraction(overview: { start: number; end: number }, fraction: number): number {
  const f = Number.isFinite(fraction) ? Math.min(1, Math.max(0, fraction)) : 0;
  return overview.start + f * (overview.end - overview.start);
}

// Whole minutes per strip column: 60 -> 1, 300 -> 5, rounds up, at least 1.
export function columnMinutes(bucketSeconds: number): number {
  if (!Number.isFinite(bucketSeconds)) return 1;
  return Math.max(1, Math.ceil(bucketSeconds / 60));
}
