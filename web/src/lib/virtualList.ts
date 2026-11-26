// The maths behind the update history list: only the rows near the visible part exist in the page, the rest is empty space of the right height.
// Row heights are measured once a row is on screen; until then an estimate stands in for them.

/** offsets[i] is where row i starts; the last entry is the total height. */
export function offsets(heights: readonly number[]): number[] {
  const out = new Array<number>(heights.length + 1);
  out[0] = 0;
  for (let i = 0; i < heights.length; i++) out[i + 1] = out[i] + heights[i];
  return out;
}

/** The row that holds the vertical position y (clamped to the first and last row). */
export function indexAt(offs: readonly number[], y: number): number {
  const rows = offs.length - 1;
  if (rows <= 0) return 0;
  let lo = 0;
  let hi = rows - 1;
  while (lo < hi) {
    const mid = (lo + hi + 1) >> 1;
    if (offs[mid] <= y) lo = mid; else hi = mid - 1;
  }
  return lo;
}

/** The rows to build for a scroll position: those in view plus `overscan` pixels either side. `end` is one past the last row. */
export function windowRange(offs: readonly number[], scrollTop: number, viewport: number, overscan = 500): { start: number; end: number } {
  const rows = offs.length - 1;
  if (rows <= 0) return { start: 0, end: 0 };
  const start = indexAt(offs, Math.max(0, scrollTop - overscan));
  const end = Math.min(rows, indexAt(offs, scrollTop + viewport + overscan) + 1);
  return { start, end };
}

/** The estimate for a row nobody has measured yet. */
export function estimateRow(release: { highlights: readonly { image?: string }[]; changes: readonly unknown[] }): number {
  const items = release.highlights.reduce((sum, item) => sum + (item.image ? 96 : 40), 0);
  return 78 + items + (release.changes.length ? 40 : 0);
}
