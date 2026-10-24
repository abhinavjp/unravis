/**
 * The time drill-down store. It follows the analysis on screen: the queried window is the top of a stack of zoom steps, and the charts
 * read `analysisFor(analysis)`, which is the answer for the view window once one has been read, and the analysis itself at the top.
 *
 * What it never does: widen the queried window, change `results.analysis`, or touch the evidence memo (which forbids the analyze read).
 * A zoom is one read of the same endpoint with the view as the window. Its own small cache holds whole answers only (see isStorable),
 * is keyed by the exact query and the evidence epoch, and is dropped whenever the analysis on screen changes.
 */
import { untrack } from 'svelte';
import type { Analysis, Query } from './types';
import { api } from './api';
import { currentEpoch, isStorable } from './evidenceMemo';
import { timeZone } from './timeZone.svelte';
import {
  applyChartKey, bucketForView, createStack, currentOf, isManualBucket, isRoot, jumpTo, back, reset, scale, shift, slotDescription, slotsOf, stripModel, zoomIntoSlot, zoomQuery, zoomToRange,
  type BucketChoice, type ChartKeyAction, type StripModel, type ZoomSlot, type ZoomStack
} from './zoom';
import { zoomCopy } from './copy/zoomCopy';

const CACHE_LIMIT = 24;
interface CacheEntry { analysis: Analysis; at: number }

let base = $state.raw<Analysis | null>(null);
let stack = $state.raw<ZoomStack | null>(null);
let answer = $state.raw<Analysis | null>(null);
let loading = $state(false);
let error = $state('');
let asOf = $state<number | null>(null);
let open = $state(false);
let announcement = $state('');

let controller: AbortController | null = null;
let run = 0;
const cache = new Map<string, CacheEntry>();

const message = (cause: unknown) => cause instanceof Error && cause.message ? cause.message : 'The server did not answer.';
const aborted = (cause: unknown) => cause instanceof DOMException && cause.name === 'AbortError';
const asWindow = (query: Pick<Query, 'start' | 'end'>) => ({ start: Date.parse(query.start), end: Date.parse(query.end) });

/** An answer with nothing in it, for a window that could not be read: the charts then say so instead of drawing the whole period. */
function emptyFor(source: Analysis, query: Query): Analysis {
  return { ...source, query, apis: [], timeline: [], overview: [], loadWindows: [], requestVolume: [], rollups: [], provenance: [], warnings: [] };
}
function hasRequests(result: Analysis): boolean {
  return (result.loadWindows ?? []).some(item => item.count > 0) || (result.requestVolume ?? []).some(item => item.count > 0) || result.timeline.some(item => item.points.some(point => (point.count ?? 0) > 0));
}

const bucket = $derived.by((): BucketChoice | null => {
  if (!base || !stack) return null;
  if (isRoot(stack)) return { seconds: base.query.bucketSeconds, source: isManualBucket(base.query) ? 'manual' : 'automatic', raised: false, asked: null };
  return bucketForView(currentOf(stack), { manualSeconds: isManualBucket(base.query) ? base.query.bucketSeconds : null });
});
const slots = $derived.by((): ZoomSlot[] => (stack && bucket ? slotsOf(currentOf(stack), bucket.seconds) : []));
const overviewCounts = $derived.by((): number[] => {
  if (!base || !stack) return [];
  const windows = base.loadWindows ?? [];
  if (!windows.length) return [];
  const counts = new Map(windows.map(item => [Date.parse(item.at), item.count]));
  return slotsOf(stack.root, base.query.bucketSeconds).map(slot => counts.get(slot.at) ?? 0);
});
const model = $derived.by((): StripModel | null => {
  if (!stack) return null;
  return stripModel({
    stack, zone: timeZone.zone, bucket, loading, error, asOf,
    hasData: answer && !isRoot(stack) ? hasRequests(answer) : null,
    overviewCounts
  });
});

function cacheGet(key: string): CacheEntry | undefined {
  const hit = cache.get(key);
  if (hit) { cache.delete(key); cache.set(key, hit); }
  return hit;
}
function cacheSet(key: string, entry: CacheEntry) {
  cache.delete(key); cache.set(key, entry);
  while (cache.size > CACHE_LIMIT) { const oldest = cache.keys().next().value; if (oldest === undefined) break; cache.delete(oldest); }
}

function clearAnswer() { answer = null; loading = false; error = ''; asOf = null; }

/** Read the view window. At the top of the stack the analysis on screen is the answer and nothing is read. */
function refresh() {
  controller?.abort(); controller = null;
  const sequence = ++run;
  if (!base || !stack || isRoot(stack)) { clearAnswer(); return; }
  const choice = bucket;
  const source = base;
  if (!choice) {
    // Not even the longest interval fits 120 points: say so, and draw nothing for this window.
    answer = emptyFor(source, zoomQuery(source.query, currentOf(stack), source.query.bucketSeconds));
    loading = false; asOf = null; error = zoomCopy.cannotRead; return;
  }
  const query = zoomQuery(source.query, currentOf(stack), choice.seconds);
  const key = currentEpoch() + '|' + JSON.stringify(query);
  const hit = cacheGet(key);
  if (hit) { answer = hit.analysis; asOf = hit.at; loading = false; error = ''; return; }
  controller = new AbortController();
  loading = true; error = '';
  api.analyze(query, controller.signal).then(result => {
    if (sequence !== run || base !== source) return;
    const at = Date.now();
    if (isStorable(result)) cacheSet(key, { analysis: result, at });
    answer = result; asOf = at; loading = false; error = '';
  }, cause => {
    if (sequence !== run || aborted(cause)) return;
    answer = emptyFor(source, query); asOf = null; loading = false; error = message(cause);
  });
}

function apply(next: ZoomStack | null): boolean {
  if (!next || next === stack) return false;
  stack = next;
  announcement = '';
  refresh();
  return true;
}

export const zoomView = {
  /** Called whenever the analysis on screen changes. A new analysis is a new top: the old steps and answers are dropped. */
  follow(analysis: Analysis | null) {
    untrack(() => {
      if (analysis === base) return;
      base = analysis; cache.clear(); open = open && analysis != null; announcement = '';
      stack = analysis ? createStack(asWindow(analysis.query)) : null;
      refresh();
    });
  },
  /** The analysis the time charts should draw: the view window's answer when zoomed and read, else the analysis itself. */
  analysisFor(analysis: Analysis): Analysis {
    return base === analysis && stack && !isRoot(stack) && answer ? answer : analysis;
  },
  get zoomed() { return stack != null && !isRoot(stack); },
  /** Nothing in the view window was found: the charts say so. */
  get empty() { return model?.isRoot === false && answer != null && !loading && !error && !hasRequests(answer); },
  get model() { return model; },
  get slots() { return slots; },
  get bucketSeconds() { return bucket?.seconds ?? 0; },
  get loading() { return loading; },
  get open() { return open; },
  set open(value: boolean) { open = value; },
  /** The answer on screen when it is not the whole period's, for the same notes (partial, capped, as of) the whole period shows. */
  get viewAnalysis(): Analysis | null { return stack && !isRoot(stack) && !loading && !error ? answer : null; },
  get announcement() { return announcement; },

  back() { return apply(stack ? back(stack) : null); },
  reset() { return apply(stack ? reset(stack) : null); },
  jumpTo(index: number) { return apply(stack ? jumpTo(stack, index) : null); },
  earlier() { return apply(stack ? shift(stack, -1, timeZone.zone) : null); },
  later() { return apply(stack ? shift(stack, 1, timeZone.zone) : null); },
  zoomIn() { return apply(stack ? scale(stack, 'in') : null); },
  zoomOut() { return apply(stack ? scale(stack, 'out') : null); },
  /** A click or tap on the slot at `index` of the chart. */
  zoomSlot(index: number): boolean {
    const slot = slots[index];
    return stack && slot ? apply(zoomIntoSlot(stack, slot, timeZone.zone)) : false;
  },
  /** A drag from one slot to another (either order). */
  zoomIndexRange(from: number, to: number): boolean {
    if (!stack || !bucket || !slots.length) return false;
    const low = slots[Math.max(0, Math.min(from, to))], high = slots[Math.min(slots.length - 1, Math.max(from, to))];
    return low && high ? apply(zoomToRange(stack, low.start, high.end, bucket.seconds)) : false;
  },
  /** A key on a focused chart. Moving the cursor is the chart's own business; this returns true when the key changed the zoom. */
  key(action: ChartKeyAction, cursor: number): boolean {
    return stack ? apply(applyChartKey(stack, action, { slots, cursor, zone: timeZone.zone })) : false;
  },
  /** Say which slot the keyboard is on, for screen readers. */
  describe(index: number) { const slot = slots[index]; announcement = slot ? slotDescription(slot, index, slots.length, timeZone.zone) : ''; return announcement; },
  clearAnnouncement() { announcement = ''; }
};
