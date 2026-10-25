/**
 * Overview brush geometry, pure. The brush is the strip under a chart: it shows the whole queried window (the root) and the zoomed
 * window (the view) as a selection with two edges. Everything here maps a pointer or a key to the next view window and nothing else.
 *
 * Every result is a valid window inside the root, with its start before its end and at least MIN_VIEW_MS wide (or the whole root when
 * the root is narrower than that). Edges snap to the epoch grid of `stepMs` (the same grid the backend buckets on), then clamp to the root.
 * A drag that resizes keeps the edges at least one step apart and never lets one cross the other.
 */
import { MIN_VIEW_MS, isWindow, widthOf } from './zoom';
import type { ZoomWindow } from './zoom';

export type BrushMode = 'start' | 'end' | 'move' | 'new' | 'none';
type Handle = 'start' | 'end';

const MIN_STEP_MS = 10_000;
const EDGE_TOLERANCE = 1e-9;

/** One bucket in milliseconds, never under the 10 second floor of the backend. */
export function brushStepMs(bucketSeconds: number): number {
  return Number.isFinite(bucketSeconds) ? Math.max(MIN_STEP_MS, bucketSeconds * 1000) : MIN_STEP_MS;
}

const clamp = (value: number, low: number, high: number) => Math.min(Math.max(value, low), high);
const floorOf = (root: ZoomWindow) => Math.min(MIN_VIEW_MS, widthOf(root));
/** The narrowest window the keyboard can make: one minute, or the whole root when it is narrower than that. */
export function brushFloor(root: ZoomWindow): number { return floorOf(root); }
/** The narrowest a resize drag can make the view: a step (at least a minute), but never wider than the view it started from. */
const resizeFloor = (root: ZoomWindow, view: ZoomWindow, stepMs: number) => Math.min(Math.max(MIN_VIEW_MS, stepMs), widthOf(view), widthOf(root));
/** The nearest point of the epoch grid, then held inside the root (so at the root's own edges it may be off the grid). */
const snapTo = (root: ZoomWindow, ms: number, stepMs: number) => clamp(Math.round(ms / stepMs) * stepMs, root.start, root.end);
const usable = (root: ZoomWindow, stepMs: number) => isWindow(root) && Number.isFinite(stepMs) && stepMs > 0;

/**
 * Order two edges, keep them inside the root and make the window at least the floor wide. A window that is too narrow grows away from
 * the clamped side: back from the root's end, forward from anywhere else (and back again if that would pass the end).
 */
function finish(root: ZoomWindow, a: number, b: number): ZoomWindow {
  const floor = floorOf(root);
  let low = clamp(Math.min(a, b), root.start, root.end);
  let high = clamp(Math.max(a, b), root.start, root.end);
  if (high - low < floor) {
    if (high >= root.end) low = high - floor;
    else {
      high = low + floor;
      if (high > root.end) { high = root.end; low = high - floor; }
    }
  }
  return { start: low, end: high };
}

/** What a press at `xFrac` (0 to 1 across the track) grabs: an edge within `handleFrac`, the body inside the selection, or a new range. */
export function brushHit(xFrac: number, sel: { left: number; right: number }, handleFrac: number): BrushMode {
  // Inside a narrow selection the edge zones shrink to a third each, so the middle third can still grab the body to move it.
  const inside = xFrac >= sel.left && xFrac <= sel.right;
  const reach = inside ? Math.min(handleFrac, Math.max(sel.right - sel.left, 0) / 3) : handleFrac;
  const toStart = Math.abs(xFrac - sel.left), toEnd = Math.abs(xFrac - sel.right);
  const nearStart = toStart <= reach + EDGE_TOLERANCE, nearEnd = toEnd <= reach + EDGE_TOLERANCE;
  if (nearStart || nearEnd) return nearStart && (!nearEnd || toStart <= toEnd) ? 'start' : 'end';
  return xFrac > sel.left && xFrac < sel.right ? 'move' : 'new';
}

/**
 * The window a drag makes. `fromMs` is where the pointer went down and `toMs` where it is now, as times on the root. Order of work:
 * apply the delta, snap, order the edges, enforce the floor, clamp to the root.
 */
export function brushDrag(mode: BrushMode, root: ZoomWindow, startView: ZoomWindow, fromMs: number, toMs: number, stepMs: number): ZoomWindow {
  if (mode === 'none' || !usable(root, stepMs) || !isWindow(startView) || !Number.isFinite(fromMs) || !Number.isFinite(toMs)) return startView;
  const delta = toMs - fromMs;
  switch (mode) {
    case 'start': {
      const floor = resizeFloor(root, startView, stepMs), end = clamp(startView.end, root.start, root.end);
      return finish(root, clamp(snapTo(root, startView.start + delta, stepMs), root.start, Math.max(root.start, end - floor)), end);
    }
    case 'end': {
      const floor = resizeFloor(root, startView, stepMs), start = clamp(startView.start, root.start, root.end);
      return finish(root, start, clamp(snapTo(root, startView.end + delta, stepMs), Math.min(root.end, start + floor), root.end));
    }
    case 'move': return shiftTo(root, startView, snapTo(root, startView.start + delta, stepMs));
    case 'new': {
      const a = snapTo(root, fromMs, stepMs), b = snapTo(root, toMs, stepMs);
      // Both ends on one grid point: the drag was too short to mean a range, so nothing changes (never a one-minute sliver).
      return a === b ? startView : finish(root, a, b);
    }
  }
}

/** The view moved so that it starts at `start`, with the same width, held inside the root. */
function shiftTo(root: ZoomWindow, view: ZoomWindow, start: number): ZoomWindow {
  const width = Math.min(widthOf(view), widthOf(root));
  const next = clamp(start, root.start, root.end - width);
  return { start: next, end: next + width };
}

/** A click on the track: the view keeps its width and centres on `atMs`, on the grid and inside the root. */
export function brushCentre(root: ZoomWindow, view: ZoomWindow, atMs: number, stepMs: number): ZoomWindow {
  if (!usable(root, stepMs) || !isWindow(view) || !Number.isFinite(atMs)) return view;
  return shiftTo(root, view, snapTo(root, atMs - widthOf(view) / 2, stepMs));
}

/**
 * Keyboard on one handle, after the WAI-ARIA multi-thumb slider pattern. Arrows move that edge one step; the edges never cross and stay
 * one floor apart. PageUp shifts the whole window later by its width and PageDown earlier. Home and End send the handle to the
 * smallest and largest value it may take: the root's edge on its own side, or one floor from the other handle. Null for any other key.
 */
export function brushKey(handle: Handle, key: string, root: ZoomWindow, view: ZoomWindow, stepMs: number): ZoomWindow | null {
  if (!isWindow(root) || !isWindow(view)) return null;
  const floor = floorOf(root);
  const edge = (value: number) => handle === 'start'
    ? { start: clamp(value, root.start, view.end - floor), end: view.end }
    : { start: view.start, end: clamp(value, view.start + floor, root.end) };
  const current = handle === 'start' ? view.start : view.end;
  switch (key) {
    case 'ArrowRight': case 'ArrowUp': return usable(root, stepMs) ? edge(current + stepMs) : null;
    case 'ArrowLeft': case 'ArrowDown': return usable(root, stepMs) ? edge(current - stepMs) : null;
    case 'PageUp': return shiftTo(root, view, view.start + widthOf(view));
    case 'PageDown': return shiftTo(root, view, view.start - widthOf(view));
    case 'Home': return edge(handle === 'start' ? root.start : view.start + floor);
    case 'End': return edge(handle === 'start' ? view.end - floor : root.end);
    default: return null;
  }
}
