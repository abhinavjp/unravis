/**
 * `use:focusEmpty` for the message that stands in for a chart with nothing to draw (an empty zoom window). When a chart in the same stage
 * had keyboard focus, the chart is hidden as the message appears and the browser would drop focus to the page. The message takes it instead
 * (tabindex -1: reachable by script, not by Tab). Backspace, Escape, + and - still step the zoom, as they do on the chart.
 */
import { chartKeyAction } from './zoom';
import { focusWouldBeLost } from './focusLoss';
import { zoomView } from './zoomView.svelte';

export function focusEmpty(node: HTMLElement) {
  node.tabIndex = -1;
  if (focusWouldBeLost(node.parentElement, node, document.activeElement, document.body)) node.focus({ preventScroll: true });
  const onKey = (event: KeyboardEvent) => {
    const action = chartKeyAction(event.key, { ctrl: event.ctrlKey, meta: event.metaKey, alt: event.altKey });
    if (!action || action.type === 'cursor' || action.type === 'cursor-edge' || action.type === 'zoom') return;
    if (!zoomView.key(action, -1)) return;
    event.preventDefault(); event.stopPropagation();
    // The step may bring the chart back and remove this message while it holds focus: hand focus to the chart.
    const stage = node.parentElement;
    requestAnimationFrame(() => {
      const active = document.activeElement;
      if (!active || active === document.body) (stage?.querySelector('[tabindex="0"]') as HTMLElement | null)?.focus({ preventScroll: true });
    });
  };
  node.addEventListener('keydown', onKey);
  return { destroy() { node.removeEventListener('keydown', onKey); } };
}
