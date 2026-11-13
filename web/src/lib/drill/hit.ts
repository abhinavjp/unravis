export type FlagMark = { slot: number; left: number; top: number; right: number; bottom: number };

// Returns the slot of the mark whose padded rect contains (x, y) and whose centre is nearest the point, or null.
export function hitFlagged(marks: FlagMark[], x: number, y: number, pad = 6): number | null {
  let best: number | null = null;
  let bestDistance = Infinity;
  for (const mark of marks) {
    if (x < mark.left - pad || x > mark.right + pad || y < mark.top - pad || y > mark.bottom + pad) continue;
    const cx = (mark.left + mark.right) / 2;
    const cy = (mark.top + mark.bottom) / 2;
    const distance = (x - cx) ** 2 + (y - cy) ** 2;
    if (distance < bestDistance) {
      bestDistance = distance;
      best = mark.slot;
    }
  }
  return best;
}
