// Settings that cut the work of a chart (BL-429). Pure functions, so a test can read what a chart asks for without a browser.
//
// What is already true everywhere (and chartPerf.test.cjs keeps true): only the Canvas renderer is registered, every option sets
// animation: false, nothing calls appendData (it has known bugs), and every chart made with echarts.init is disposed when its screen goes.
// What this adds: symbols only where they help, down-sampling and the large/progressive modes for big series, and a size watcher that does
// nothing while the page is hidden.
import { isHidden, onVisibilityChange } from './visibility';

/** A line with this many points or more draws no dot on every point (a dot per point on a dense line is noise and thousands of shapes). */
export const SYMBOL_MAX_POINTS = 40;
/** From this many points a bar or scatter series is drawn in ECharts' large mode. Lines are not sampled here: the server already sends at most one point per pixel column (BL-430), and sampling changes the data index a tooltip reads. */
export const LARGE_MIN_POINTS = 2000;
/** Large series are drawn in chunks of this many points, once they pass PROGRESSIVE_THRESHOLD, so one slow frame never blocks input. */
export const PROGRESSIVE_CHUNK = 400;
export const PROGRESSIVE_THRESHOLD = 3000;

type Cell = number | null | undefined | { value?: unknown } | readonly unknown[];

const valueOf = (cell: Cell): unknown => {
  if (cell == null) return null;
  if (Array.isArray(cell)) return cell[1] ?? null;
  if (typeof cell === 'object') return (cell as { value?: unknown }).value ?? null;
  return cell;
};

/** True for each point that has a value while both neighbours have none: with no line to either side only a dot shows it. */
export function lonePointFlags(data: ArrayLike<Cell>): boolean[] {
  const present = (i: number): boolean => i >= 0 && i < data.length && valueOf(data[i]) != null;
  const flags: boolean[] = [];
  for (let i = 0; i < data.length; i++) flags.push(valueOf(data[i]) != null && (data.length < 3 || (!present(i - 1) && !present(i + 1))));
  return flags;
}

/**
 * Symbol options for one line series.
 *  - fewer than SYMBOL_MAX_POINTS points: a dot on every point (as before);
 *  - more: no dots, except a point that stands alone between gaps keeps its dot (nothing else would show it);
 * Hovering still shows the point under the pointer.
 */
export function lineSeriesPerf(data: ArrayLike<Cell>, size: number): { showSymbol: boolean; symbolSize: number | ((value: unknown, params: { dataIndex: number }) => number) } {
  if (data.length < SYMBOL_MAX_POINTS) return { showSymbol: true, symbolSize: size };
  const lone = lonePointFlags(data);
  if (lone.some(Boolean)) return { showSymbol: true, symbolSize: (_value, params) => (lone[params.dataIndex] ? size : 0) };
  return { showSymbol: false, symbolSize: size };
}

/**
 * Options for a bar or scatter series: from LARGE_MIN_POINTS the cheap drawing mode (one path for all points, no per-point style or
 * label) in chunks; below that nothing is added, so a small chart looks and behaves as before. Uses constant colour and size only.
 */
export function bigSeriesPerf(n: number): { large?: true; largeThreshold?: number; progressive?: number; progressiveThreshold?: number } {
  if (n < LARGE_MIN_POINTS) return {};
  return { large: true, largeThreshold: LARGE_MIN_POINTS, progressive: PROGRESSIVE_CHUNK, progressiveThreshold: PROGRESSIVE_THRESHOLD };
}

/**
 * ResizeObserver that waits while the page is hidden: a size change seen then is remembered and `onResize` runs once when the page is shown.
 * The first call of `onResize` is not made here (the caller's own init does it). Returns the function that stops watching.
 */
export function watchSize(nodes: Element | readonly Element[], onResize: () => void): () => void {
  if (typeof ResizeObserver === 'undefined') return () => {};
  let late = false;
  const observer = new ResizeObserver(() => {
    if (isHidden()) { late = true; return; }
    onResize();
  });
  for (const node of Array.isArray(nodes) ? nodes : [nodes as Element]) observer.observe(node);
  const off = onVisibilityChange(() => {
    if (late && !isHidden()) { late = false; onResize(); }
  });
  return () => { observer.disconnect(); off(); };
}
