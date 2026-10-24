/**
 * `use:zoomable` makes a time chart a zoom surface. A click or tap zooms into that slot, a drag zooms to a range, and a focused chart
 * answers the arrow, Enter, Backspace, Escape, + and - keys. Slots are the zoom store's own, and a chart draws one category per slot, so the
 * category index and the slot index are the same number.
 *
 * It listens on the chart's container only. The drag starts inside the plot area, so the chart's own slider and legend keep working.
 */
import type { echarts } from './charts';
import { chartKeyAction, moveCursor } from './zoom';
import { zoomView } from './zoomView.svelte';
import { zoomCopy } from './copy/zoomCopy';

type Chart = ReturnType<typeof echarts.init>;
export interface ZoomableOptions {
  /** The chart, read each time it is needed (it is created after the container mounts). */
  chart: () => Chart | undefined;
  /** False when the chart handles clicks itself and calls zoomView.zoomSlot. Default true. */
  click?: boolean;
}
const DRAG_PIXELS = 6;

export function zoomable(node: HTMLElement, initial: ZoomableOptions) {
  let options = initial;
  let cursor = -1;
  let start: { x: number; y: number } | null = null;
  let band: HTMLElement | null = null;

  node.tabIndex = 0;
  node.setAttribute('aria-description', zoomCopy.howTo);
  node.style.touchAction = 'pan-y';

  const local = (event: PointerEvent) => { const box = node.getBoundingClientRect(); return { x: event.clientX - box.left, y: event.clientY - box.top }; };
  const indexAt = (chart: Chart, x: number, y: number): number | null => {
    const count = zoomView.slots.length;
    if (!count) return null;
    let converted: unknown;
    try { converted = chart.convertFromPixel({ gridIndex: 0 }, [x, y]); } catch { return null; }
    const value = Array.isArray(converted) ? Number(converted[0]) : NaN;
    return Number.isFinite(value) ? Math.min(count - 1, Math.max(0, Math.round(value))) : null;
  };
  const inPlot = (chart: Chart, x: number, y: number) => { try { return chart.containPixel('grid', [x, y]); } catch { return false; } };

  const drawBand = (from: number, to: number) => {
    if (!band) {
      band = document.createElement('div');
      band.setAttribute('aria-hidden', 'true');
      band.style.cssText = 'position:absolute;top:0;bottom:0;pointer-events:none;background:var(--accent);opacity:.16;border-left:1px solid var(--accent);border-right:1px solid var(--accent);z-index:2';
      if (getComputedStyle(node).position === 'static') node.style.position = 'relative';
      node.appendChild(band);
    }
    band.style.left = Math.min(from, to) + 'px'; band.style.width = Math.abs(to - from) + 'px';
  };
  const clearBand = () => { band?.remove(); band = null; };

  const onMove = (event: PointerEvent) => { if (start && Math.abs(local(event).x - start.x) >= DRAG_PIXELS) drawBand(start.x, local(event).x); };
  const stopTracking = () => { window.removeEventListener('pointermove', onMove); window.removeEventListener('pointerup', onUp); window.removeEventListener('pointercancel', onCancel); clearBand(); start = null; };
  function onCancel() { stopTracking(); }
  function onUp(event: PointerEvent) {
    const from = start; const chart = options.chart();
    stopTracking();
    if (!from || !chart) return;
    const at = local(event);
    const moved = Math.abs(at.x - from.x) >= DRAG_PIXELS;
    const a = indexAt(chart, from.x, from.y), b = indexAt(chart, at.x, at.y);
    if (a == null || b == null) return;
    cursor = -1;
    if (moved) zoomView.zoomIndexRange(a, b);
    else if (options.click !== false) zoomView.zoomSlot(b);
  }
  function onDown(event: PointerEvent) {
    if (event.button !== 0 || !event.isPrimary) return;
    const chart = options.chart();
    if (!chart) return;
    const point = local(event);
    if (!inPlot(chart, point.x, point.y)) return;
    start = point;
    window.addEventListener('pointermove', onMove); window.addEventListener('pointerup', onUp); window.addEventListener('pointercancel', onCancel);
  }

  function onKey(event: KeyboardEvent) {
    const action = chartKeyAction(event.key, { ctrl: event.ctrlKey, meta: event.metaKey, alt: event.altKey });
    const chart = options.chart();
    if (!action || !chart) return;
    if (action.type === 'cursor' || action.type === 'cursor-edge') {
      const count = zoomView.slots.length;
      if (!count) return;
      event.preventDefault();
      cursor = moveCursor(cursor, action, count);
      zoomView.describe(cursor);
      try { chart.dispatchAction({ type: 'showTip', seriesIndex: 0, dataIndex: cursor }); } catch { /* the chart has no tooltip to show */ }
      return;
    }
    if (zoomView.key(action, cursor)) { event.preventDefault(); event.stopPropagation(); cursor = -1; }
  }
  function onBlur() {
    cursor = -1;
    // A focused chart that is hidden or removed blurs in the middle of Svelte's own update, where changing state is not allowed. Wait for the update to finish.
    queueMicrotask(() => zoomView.clearAnnouncement());
    try { options.chart()?.dispatchAction({ type: 'hideTip' }); } catch { /* nothing to hide */ } }

  node.addEventListener('pointerdown', onDown);
  node.addEventListener('keydown', onKey);
  node.addEventListener('blur', onBlur);
  return {
    update(next: ZoomableOptions) { options = next; },
    destroy() { stopTracking(); node.removeEventListener('pointerdown', onDown); node.removeEventListener('keydown', onKey); node.removeEventListener('blur', onBlur); }
  };
}
