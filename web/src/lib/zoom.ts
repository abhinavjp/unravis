/**
 * Time drill-down, pure logic. One "view window" sits inside the window the user queried. A stack of windows lets Back step out one
 * level: whole period, day, hour, minute. Nothing here reads the DOM, the clock or the network, and nothing ever leaves the queried window.
 *
 * Day and hour edges are local to the display zone, so a daylight-saving day is 23 or 25 hours long and still one "day". Server
 * slots stay aligned to the epoch (as every chart already draws them), so in a zone with a half-hour offset the first and last
 * slot of an hour view are partial; they are drawn the way partial slots are drawn everywhere else.
 */
import type { Query } from './types';
import { bucketForWindows, countEpochBuckets } from './time';
import { chartRange, chartTime, zoneOffsetMs } from './chartTime';
import { zoomCopy, zoomText } from './copy/zoomCopy';

const SECOND = 1000, MINUTE = 60 * SECOND, HOUR = 60 * MINUTE, DAY = 24 * HOUR;
export const MAX_POINTS = 120;
export const MIN_BUCKET_SECONDS = 10;
export const MAX_BUCKET_SECONDS = 86_400;
/** The smallest window a zoom may produce. */
export const MIN_VIEW_MS = MINUTE;

export interface ZoomWindow { start: number; end: number }
export type ZoomLevel = 'window' | 'day' | 'hour' | 'minute';
export interface ZoomSlot { at: number; start: number; end: number }
/** `root` is the queried window; `frames` are the windows zoomed into, outermost first. Never edit one: every function returns a new stack. */
export interface ZoomStack { root: ZoomWindow; frames: ZoomWindow[] }

export const isWindow = (value: unknown): value is ZoomWindow => {
  if (typeof value !== 'object' || value === null) return false;
  const { start, end } = value as ZoomWindow;
  return typeof start === 'number' && typeof end === 'number' && Number.isFinite(start) && Number.isFinite(end) && end > start;
};
export const sameWindow = (a: ZoomWindow, b: ZoomWindow) => a.start === b.start && a.end === b.end;
export const widthOf = (win: ZoomWindow) => win.end - win.start;
/** The part of `a` that lies inside `b`, or null when they share no time. */
export function intersect(a: ZoomWindow, b: ZoomWindow): ZoomWindow | null {
  if (!isWindow(a) || !isWindow(b)) return null;
  const start = Math.max(a.start, b.start), end = Math.min(a.end, b.end);
  return end > start ? { start, end } : null;
}

// ---------------------------------------------------------------------------------------------------------------------
// Levels and zone-local edges.
// ---------------------------------------------------------------------------------------------------------------------
/** The level a window of this width belongs to. Null for a width that is not a positive number. */
export function levelForWidth(widthMs: number): ZoomLevel | null {
  if (!Number.isFinite(widthMs) || widthMs <= 0) return null;
  if (widthMs > 36 * HOUR) return 'window';
  if (widthMs > 90 * MINUTE) return 'day';
  if (widthMs > 5 * MINUTE) return 'hour';
  return 'minute';
}
const NEXT_LEVEL: Record<ZoomLevel, ZoomLevel | null> = { window: 'day', day: 'hour', hour: 'minute', minute: null };

const zoneChecked = new Map<string, string>();
/** A zone name Intl accepts; anything else reads as UTC. */
export function safeZone(zone: string): string {
  const known = zoneChecked.get(zone);
  if (known) return known;
  let result = 'UTC';
  try { zoneOffsetMs(0, zone); result = zone || 'UTC'; } catch { result = 'UTC'; }
  zoneChecked.set(zone, result);
  return result;
}
/** The instant whose wall clock in `zone` reads `localMs` (a wall clock written as if it were UTC). A time the clocks skip lands just after the gap. */
function localToInstant(localMs: number, zone: string): number {
  // The two offsets that can apply nearby: the one a day before and the one a day after. A wall clock read twice takes the first
  // time; one the clocks skip takes the moment they change.
  const candidates = [...new Set([zoneOffsetMs(localMs - DAY, zone), zoneOffsetMs(localMs + DAY, zone)])].map(offset => localMs - offset);
  const valid = candidates.filter(at => at + zoneOffsetMs(at, zone) === localMs);
  return valid.length ? Math.min(...valid) : Math.max(...candidates);
}
/** Local midnight at or before `ms`, in `zone`. */
export function startOfLocalDay(ms: number, zone: string): number {
  const z = safeZone(zone);
  const local = ms + zoneOffsetMs(ms, z);
  return localToInstant(Math.floor(local / DAY) * DAY, z);
}
/** The local calendar day that holds `ms`: 24 hours, or 23 or 25 across a clock change. */
export function dayWindow(ms: number, zone: string): ZoomWindow | null {
  if (!Number.isFinite(ms)) return null;
  const z = safeZone(zone);
  const start = startOfLocalDay(ms, z);
  const local = start + zoneOffsetMs(start, z);
  return { start, end: localToInstant(Math.floor(local / DAY) * DAY + DAY, z) };
}
/** The local clock hour that holds `ms`. */
export function hourWindow(ms: number, zone: string): ZoomWindow | null {
  if (!Number.isFinite(ms)) return null;
  const local = ms + zoneOffsetMs(ms, safeZone(zone));
  const start = ms - (((local % HOUR) + HOUR) % HOUR);
  return { start, end: start + HOUR };
}
export function minuteWindow(ms: number): ZoomWindow | null {
  if (!Number.isFinite(ms)) return null;
  const start = Math.floor(ms / MINUTE) * MINUTE;
  return { start, end: start + MINUTE };
}
const windowOfLevel = (level: ZoomLevel, ms: number, zone: string): ZoomWindow | null =>
  level === 'day' ? dayWindow(ms, zone) : level === 'hour' ? hourWindow(ms, zone) : level === 'minute' ? minuteWindow(ms) : null;

// ---------------------------------------------------------------------------------------------------------------------
// Slots and the interval.
// ---------------------------------------------------------------------------------------------------------------------
/** The slots every chart draws for a window: epoch-aligned, first slot clipped to the start, never more than 120. */
export function slotsOf(win: ZoomWindow, bucketSeconds: number): ZoomSlot[] {
  const slots: ZoomSlot[] = [];
  const width = bucketSeconds * SECOND;
  if (!isWindow(win) || !Number.isFinite(width) || width <= 0) return slots;
  for (let at = Math.floor(win.start / width) * width; at < win.end && slots.length < MAX_POINTS; at += width) {
    slots.push({ at, start: Math.max(win.start, at), end: Math.min(win.end, at + width) });
  }
  return slots;
}
/** The reference for a zoomed read: the same length of time right before the view. */
export function referenceFor(view: ZoomWindow): ZoomWindow { const width = widthOf(view); return { start: view.start - width, end: view.start }; }

export type BucketSource = 'automatic' | 'manual';
export interface BucketChoice { seconds: number; source: BucketSource; /** The user's number would give more than 120 points, so a longer one was used. */ raised: boolean; asked: number | null }
const LADDER = [10, 15, 30, 60, 120, 300, 600, 900, 1800, 3600, 7200, 10_800, 21_600, 43_200, 86_400];
const PREFERRED: Record<ZoomLevel, number> = { window: 0, day: 3600, hour: 60, minute: 10 };
const iso = (ms: number) => new Date(ms).toISOString();
const fits = (view: ZoomWindow, seconds: number) => countEpochBuckets(view.start, view.end, seconds) <= MAX_POINTS && countEpochBuckets(referenceFor(view).start, referenceFor(view).end, seconds) <= MAX_POINTS;
/** The smallest width at or above `seconds` that keeps both the view and its reference at 120 points or fewer. Null when none does. */
function fitSeconds(view: ZoomWindow, seconds: number, onLadder: boolean): number | null {
  let width = Math.min(MAX_BUCKET_SECONDS, Math.max(MIN_BUCKET_SECONDS, Math.ceil(seconds)));
  if (onLadder) width = LADDER.find(step => step >= width) ?? MAX_BUCKET_SECONDS;
  if (fits(view, width)) return width;
  const step = LADDER.find(item => item > width && fits(view, item));
  return step ?? null;
}
/**
 * The interval for a view. A number the user set still wins, unless it would give more than 120 points, in which case the next
 * length that fits is used and `raised` says so. Otherwise the level picks it: hours for a day, minutes for an hour, 10 seconds
 * for a minute, and the app's own rule (bucketForWindows) for a long window. Null for a window that cannot be drawn.
 */
export function bucketForView(view: ZoomWindow, options: { manualSeconds?: number | null } = {}): BucketChoice | null {
  if (!isWindow(view)) return null;
  const level = levelForWidth(widthOf(view));
  if (!level) return null;
  const ref = referenceFor(view);
  const manual = options.manualSeconds;
  if (typeof manual === 'number' && Number.isFinite(manual) && manual > 0) {
    const asked = Math.min(MAX_BUCKET_SECONDS, Math.max(MIN_BUCKET_SECONDS, Math.round(manual)));
    const used = fitSeconds(view, asked, false);
    return used == null ? null : { seconds: used, source: 'manual', raised: used !== asked, asked: Math.round(manual) };
  }
  const floor = bucketForWindows(iso(view.start), iso(view.end), iso(ref.start), iso(ref.end));
  const used = fitSeconds(view, Math.max(PREFERRED[level], floor), true);
  return used == null ? null : { seconds: used, source: 'automatic', raised: false, asked: null };
}
/** The query for one zoomed read: the same filters, the view as the window, the equal-length window before it as the reference. */
export function zoomQuery(base: Query, view: ZoomWindow, bucketSeconds: number): Query {
  const ref = referenceFor(view);
  return { ...base, start: iso(view.start), end: iso(view.end), baselineStart: iso(ref.start), baselineEnd: iso(ref.end), bucketSeconds };
}
/** Whether the interval the query carries is the one the app would pick: if not, the user typed it. */
export function isManualBucket(query: Pick<Query, 'start' | 'end' | 'baselineStart' | 'baselineEnd' | 'bucketSeconds'>): boolean {
  return query.bucketSeconds !== bucketForWindows(query.start, query.end, query.baselineStart, query.baselineEnd);
}

// ---------------------------------------------------------------------------------------------------------------------
// The stack.
// ---------------------------------------------------------------------------------------------------------------------
/** A stack for a queried window. Null when that window is empty, backwards or not a number. */
export function createStack(root: ZoomWindow): ZoomStack | null { return isWindow(root) ? { root: { start: root.start, end: root.end }, frames: [] } : null; }
export const currentOf = (stack: ZoomStack): ZoomWindow => stack.frames.length ? stack.frames[stack.frames.length - 1] : stack.root;
export const isRoot = (stack: ZoomStack) => stack.frames.length === 0;
export const canBack = (stack: ZoomStack) => stack.frames.length > 0;
export const levelOf = (stack: ZoomStack): ZoomLevel => levelForWidth(widthOf(currentOf(stack))) ?? 'minute';
/** Zoom into a window. It is cut to the queried window, so a zoom never reaches outside it; one that is empty, too short or the same as now changes nothing. */
export function push(stack: ZoomStack, win: ZoomWindow): ZoomStack {
  const inside = isWindow(win) ? intersect(win, stack.root) : null;
  if (!inside || widthOf(inside) < Math.min(MIN_VIEW_MS, widthOf(stack.root))) return stack;
  if (sameWindow(inside, currentOf(stack))) return stack;
  if (sameWindow(inside, stack.root)) return reset(stack);
  return { root: stack.root, frames: [...stack.frames, inside] };
}
/** One step out. At the top it does nothing. */
export function back(stack: ZoomStack): ZoomStack { return stack.frames.length ? { root: stack.root, frames: stack.frames.slice(0, -1) } : stack; }
export function reset(stack: ZoomStack): ZoomStack { return stack.frames.length ? { root: stack.root, frames: [] } : stack; }
/** Go to the step at `index` of the trail (0 is the whole period). */
export function jumpTo(stack: ZoomStack, index: number): ZoomStack {
  if (!Number.isInteger(index) || index < 0 || index > stack.frames.length) return stack;
  return index === stack.frames.length ? stack : { root: stack.root, frames: stack.frames.slice(0, index) };
}
const replaceTop = (stack: ZoomStack, win: ZoomWindow): ZoomStack => {
  if (!stack.frames.length) return push(stack, win);
  const inside = intersect(win, stack.root);
  if (!inside) return stack;
  if (sameWindow(inside, stack.root)) return reset(stack);
  return sameWindow(inside, currentOf(stack)) ? stack : { root: stack.root, frames: [...stack.frames.slice(0, -1), inside] };
};

/** The window one level down for a clicked slot: its day, hour or minute in the display zone, cut to the view. Null when there is no deeper level. */
export function childWindow(view: ZoomWindow, slot: Pick<ZoomSlot, 'start' | 'end'>, zone: string, root?: ZoomWindow): ZoomWindow | null {
  if (!isWindow(view) || !isWindow(slot as ZoomWindow)) return null;
  const level = levelForWidth(widthOf(view));
  const next = level ? NEXT_LEVEL[level] : null;
  if (!next) return null;
  const middle = slot.start + (slot.end - slot.start) / 2;
  const whole = windowOfLevel(next, middle, zone);
  const inside = whole ? intersect(whole, view) : null;
  const bounded = inside && root ? intersect(inside, root) : inside;
  if (!bounded || widthOf(bounded) < Math.min(MIN_VIEW_MS, widthOf(view)) || widthOf(bounded) >= widthOf(view)) return null;
  return bounded;
}
/** Zoom into a clicked slot. A slot that cannot be zoomed into changes nothing. */
export function zoomIntoSlot(stack: ZoomStack, slot: Pick<ZoomSlot, 'start' | 'end'>, zone: string): ZoomStack {
  const child = childWindow(currentOf(stack), slot, zone, stack.root);
  return child ? push(stack, child) : stack;
}
/**
 * A dragged range as a window: both ends move out to the edges of the slots they fall in, then the range is cut to the view.
 * Null when it is not a range, is shorter than a minute once cut, or covers the whole view (nothing to zoom into).
 */
export function snapRange(fromMs: number, toMs: number, bucketSeconds: number, view: ZoomWindow): ZoomWindow | null {
  if (!Number.isFinite(fromMs) || !Number.isFinite(toMs) || !isWindow(view)) return null;
  const width = bucketSeconds * SECOND;
  if (!Number.isFinite(width) || width <= 0) return null;
  const low = Math.min(fromMs, toMs), high = Math.max(fromMs, toMs);
  const snapped = intersect({ start: Math.floor(low / width) * width, end: Math.max(Math.ceil(high / width) * width, Math.floor(low / width) * width + width) }, view);
  if (!snapped || widthOf(snapped) < Math.min(MIN_VIEW_MS, widthOf(view)) || sameWindow(snapped, view)) return null;
  return snapped;
}
export function zoomToRange(stack: ZoomStack, fromMs: number, toMs: number, bucketSeconds: number): ZoomStack {
  const range = snapRange(fromMs, toMs, bucketSeconds, currentOf(stack));
  return range ? push(stack, range) : stack;
}

/** Move the view one view width earlier (-1) or later (1). A view that is a whole local day moves by whole local days. It stops at the queried window. */
export function shift(stack: ZoomStack, direction: -1 | 1, zone: string): ZoomStack {
  const view = currentOf(stack);
  if (isRoot(stack)) return stack;
  const day = dayWindow(view.start, zone);
  const clipped = day ? intersect(day, stack.root) : null;
  if (day && clipped && sameWindow(clipped, view)) {
    const neighbour = dayWindow(direction < 0 ? day.start - 1 : day.end, zone);
    const cut = neighbour ? intersect(neighbour, stack.root) : null;
    return cut ? replaceTop(stack, cut) : stack;
  }
  const width = widthOf(view);
  const delta = direction < 0 ? Math.max(-width, stack.root.start - view.start) : Math.min(width, stack.root.end - view.end);
  return delta === 0 ? stack : replaceTop(stack, { start: view.start + delta, end: view.end + delta });
}
/** Zoom in or out by a factor of two around the centre. Out becomes the whole period once it would be as wide as the queried window. */
export function scale(stack: ZoomStack, direction: 'in' | 'out'): ZoomStack {
  const view = currentOf(stack), width = widthOf(view), centre = view.start + width / 2;
  if (direction === 'in') {
    const next = Math.round(width / 2);
    if (next < Math.min(MIN_VIEW_MS, widthOf(stack.root))) return stack;
    const start = Math.round(centre - next / 2);
    return push(stack, { start, end: start + next });
  }
  if (isRoot(stack)) return stack;
  const next = width * 2;
  if (next >= widthOf(stack.root)) return reset(stack);
  let start = Math.round(centre - next / 2);
  start = Math.min(Math.max(start, stack.root.start), stack.root.end - next);
  return replaceTop(stack, { start, end: start + next });
}
export const canShift = (stack: ZoomStack, direction: -1 | 1, zone: string) => shift(stack, direction, zone) !== stack;
export const canScale = (stack: ZoomStack, direction: 'in' | 'out') => scale(stack, direction) !== stack;

// ---------------------------------------------------------------------------------------------------------------------
// Keyboard.
// ---------------------------------------------------------------------------------------------------------------------
export type ChartKeyAction = { type: 'cursor'; delta: number } | { type: 'cursor-edge'; edge: 'first' | 'last' } | { type: 'zoom' } | { type: 'back' } | { type: 'zoom-in' } | { type: 'zoom-out' };
/** What a key does on a focused chart. Null for any key (or modified key) that is not ours, so the page keeps its own shortcuts. */
export function chartKeyAction(key: string, modifiers: { ctrl?: boolean; meta?: boolean; alt?: boolean } = {}): ChartKeyAction | null {
  if (modifiers.ctrl || modifiers.meta || modifiers.alt) return null;
  switch (key) {
    case 'ArrowLeft': return { type: 'cursor', delta: -1 };
    case 'ArrowRight': return { type: 'cursor', delta: 1 };
    case 'Home': return { type: 'cursor-edge', edge: 'first' };
    case 'End': return { type: 'cursor-edge', edge: 'last' };
    case 'Enter': return { type: 'zoom' };
    case 'Backspace': case 'Escape': return { type: 'back' };
    case '+': case '=': return { type: 'zoom-in' };
    case '-': case '_': return { type: 'zoom-out' };
    default: return null;
  }
}
/** The slot the keyboard cursor is on after a move. -1 means no cursor yet: the first arrow lands on the first or last slot. */
export function moveCursor(index: number, action: ChartKeyAction, count: number): number {
  if (!Number.isInteger(count) || count <= 0) return -1;
  if (action.type === 'cursor-edge') return action.edge === 'first' ? 0 : count - 1;
  if (action.type !== 'cursor') return index;
  if (!Number.isInteger(index) || index < 0) return action.delta < 0 ? count - 1 : 0;
  return Math.min(count - 1, Math.max(0, index + action.delta));
}
/** Apply a key to the stack. `cursor` is the slot the keyboard is on. Moves of the cursor itself are the chart's job, not the stack's. */
export function applyChartKey(stack: ZoomStack, action: ChartKeyAction, options: { slots: ZoomSlot[]; cursor: number; zone: string }): ZoomStack {
  switch (action.type) {
    case 'zoom': { const slot = options.slots[options.cursor]; return slot ? zoomIntoSlot(stack, slot, options.zone) : stack; }
    case 'back': return back(stack);
    case 'zoom-in': return scale(stack, 'in');
    case 'zoom-out': return scale(stack, 'out');
    default: return stack;
  }
}

// ---------------------------------------------------------------------------------------------------------------------
// Words for the strip.
// ---------------------------------------------------------------------------------------------------------------------
/** "15 minutes", "23 hours", "7 days". Plain whole numbers; one decimal only where a whole number would mislead. */
export function durationLabel(ms: number): string {
  if (!Number.isFinite(ms) || ms <= 0) return 'no time';
  const plural = (value: number, unit: string) => value + ' ' + unit + (value === 1 ? '' : 's');
  const round = (value: number) => Number.isInteger(value) ? value : Math.round(value * 10) / 10;
  if (ms < 90 * SECOND) return plural(Math.round(ms / SECOND), 'second');
  if (ms < 90 * MINUTE) return plural(Math.round(ms / MINUTE), 'minute');
  if (ms < 48 * HOUR) return plural(round(ms / HOUR), 'hour');
  return plural(round(ms / DAY), 'day');
}
/** An interval in its own whole unit: "10 seconds", "1 minute", "15 minutes", "1 hour", "1 day". */
function intervalLabel(seconds: number): string {
  const plural = (value: number, unit: string) => value + ' ' + unit + (value === 1 ? '' : 's');
  if (seconds >= 86_400 && seconds % 86_400 === 0) return plural(seconds / 86_400, 'day');
  if (seconds >= 3600 && seconds % 3600 === 0) return plural(seconds / 3600, 'hour');
  if (seconds >= 60 && seconds % 60 === 0) return plural(seconds / 60, 'minute');
  return plural(seconds, 'second');
}
/** One window as a short label: a day by its date, an hour or minute by its clock, anything else as a range. */
export function windowLabel(win: ZoomWindow, zone: string): string {
  const day = dayWindow(win.start, zone);
  if (day && sameWindow(day, win)) return chartTime(win.start, zone, { time: false, zone: false });
  const hour = hourWindow(win.start, zone);
  if (hour && sameWindow(hour, win)) return chartTime(win.start, zone, { seconds: false, zone: false });
  const minute = minuteWindow(win.start);
  if (minute && sameWindow(minute, win)) return chartTime(win.start, zone, { seconds: false, zone: false, date: false });
  return chartRange(win.start, win.end, zone, { seconds: widthOf(win) < 10 * MINUTE });
}
export function slotDescription(slot: ZoomSlot, index: number, count: number, zone: string): string {
  return zoomText.slot(index + 1, count, chartRange(slot.start, slot.end, zone, { seconds: slot.end - slot.start < MINUTE }));
}

export interface Crumb { index: number; label: string; current: boolean }
export interface OverviewModel { bars: number[]; left: number; width: number; hasData: boolean; alt: string }
export interface StripModel {
  level: ZoomLevel; levelName: string; summary: string; rangeLine: string; intervalLine: string; asOfLine: string; scopeLine: string; referenceLine: string; message: string; messageTone: 'info' | 'warn' | 'error' | '';
  crumbs: Crumb[]; isRoot: boolean; loading: boolean;
  canBack: boolean; canEarlier: boolean; canLater: boolean; canIn: boolean; canOut: boolean; canReset: boolean;
  overview: OverviewModel;
}
export interface StripInput {
  stack: ZoomStack; zone: string; bucket: BucketChoice | null; loading: boolean; error: string; hasData: boolean | null;
  /** When the zoomed read finished (ms), or null. */ asOf: number | null;
  /** Requests in each slot of the whole period, oldest first; empty when not known. */ overviewCounts: number[];
}
/** Everything the strip shows, as plain values. The component only draws this. */
export function stripModel(input: StripInput): StripModel {
  const { stack, zone, bucket, loading, error } = input;
  const view = currentOf(stack), level = levelOf(stack), top = isRoot(stack);
  const crumbs: Crumb[] = [{ index: 0, label: durationLabel(widthOf(stack.root)), current: top }, ...stack.frames.map((frame, i) => ({ index: i + 1, label: windowLabel(frame, zone), current: i === stack.frames.length - 1 }))];
  const rootRange = chartRange(stack.root.start, stack.root.end, zone, { seconds: false });
  const rangeLine = zoomText.showing(chartRange(view.start, view.end, zone, { seconds: widthOf(view) < 10 * MINUTE }));
  let intervalLine = '';
  if (bucket) intervalLine = bucket.raised && bucket.asked != null ? zoomText.intervalRaised(intervalLabel(bucket.seconds), intervalLabel(bucket.asked)) : zoomText.interval(intervalLabel(bucket.seconds), bucket.source);
  let message = '', messageTone: StripModel['messageTone'] = '';
  if (error) { message = zoomText.cannotRead(error); messageTone = 'error'; }
  else if (loading) { message = zoomCopy.loading; messageTone = 'info'; }
  else if (!top && input.hasData === false) { message = zoomCopy.noData; messageTone = 'warn'; }
  const bars = input.overviewCounts.map(count => Number.isFinite(count) && count > 0 ? count : 0);
  const peak = bars.reduce((most, count) => Math.max(most, count), 0);
  const rootWidth = widthOf(stack.root);
  const left = (view.start - stack.root.start) / rootWidth, width = widthOf(view) / rootWidth;
  return {
    level, levelName: zoomCopy.levelNames[level], summary: zoomText.collapsed(crumbs[crumbs.length - 1].label), rangeLine, intervalLine,
    asOfLine: !top && input.asOf != null && !loading && !error ? zoomText.asOf(chartTime(input.asOf, zone, { date: false, zone: true })) : '',
    scopeLine: top ? '' : zoomText.scope(rootRange), referenceLine: top ? '' : zoomCopy.referenceNote, message, messageTone,
    crumbs, isRoot: top, loading,
    canBack: canBack(stack), canEarlier: canShift(stack, -1, zone), canLater: canShift(stack, 1, zone), canIn: canScale(stack, 'in'), canOut: canScale(stack, 'out'), canReset: !top,
    overview: { bars: peak > 0 ? bars.map(count => count / peak) : bars.map(() => 0), left, width, hasData: peak > 0, alt: zoomText.overviewAlt(chartRange(view.start, view.end, zone, { seconds: false }), durationLabel(rootWidth)) }
  };
}
