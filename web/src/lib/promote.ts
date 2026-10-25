/**
 * "Investigate this window", pure logic. The zoom lens is a view inside the queried window; promoting it makes the lens window the
 * queried window and reruns the full analysis on it. This module decides what the new window is and keeps the way back. It reads
 * no state, no clock and no network: appState.svelte.ts holds the trail and does the rerun.
 */
import { bucketForWindows } from './time';
import { intervalFits, scopeProblem } from './autorun';
import type { Query } from './types';
import { durationLabel, isManualBucket, isWindow, MIN_VIEW_MS, type ZoomWindow } from './zoom';
import { zoomCopy, zoomText } from './copy/zoomCopy';

/** The five query fields a promotion changes. The formats match `Query`: ISO strings and whole seconds. */
export interface WindowFields { start: string; end: string; baselineStart: string; baselineEnd: string; bucketSeconds: number }
/** One step back: the window before the promotion, the window the promotion made, and the lens that was investigated. */
export interface TrailEntry { from: WindowFields; to: { start: string; end: string }; lens: ZoomWindow }

export const TRAIL_LIMIT = 10;
const SECOND = 1000;
/** The server's cap on one window. */
const MAX_WINDOW_MS = 7 * 86_400_000;

const iso = (ms: number) => new Date(ms).toISOString();
const copyFields = (fields: WindowFields): WindowFields => ({
  start: fields.start, end: fields.end, baselineStart: fields.baselineStart, baselineEnd: fields.baselineEnd, bucketSeconds: fields.bucketSeconds
});

/** The queried window as milliseconds: the root of the zoom stack that analysis opens. */
export function rootOf(fields: Pick<WindowFields, 'start' | 'end'>): ZoomWindow {
  return { start: Date.parse(fields.start), end: Date.parse(fields.end) };
}

/**
 * The query window a lens window becomes. Edges are rounded outward to whole seconds (the lens carries milliseconds after a scale or
 * a shift) and then held inside the current window, itself shrunk inward to whole seconds. Null, never a clipped guess, when the lens
 * is unreadable, shares under a minute with the current window, is the whole current window, or is wider than 7 days.
 * The reference is the equal-length window just before it. The interval is the one the user typed only while it was typed before, still
 * fits both windows, and is smaller than the new window; otherwise it is the app's own rule.
 */
export function promotedScope(current: WindowFields, view: ZoomWindow | null): WindowFields | null {
  if (!isWindow(view)) return null;
  const now = rootOf(current);
  if (!isWindow(now)) return null;
  const start = Math.max(Math.floor(view.start / SECOND) * SECOND, Math.ceil(now.start / SECOND) * SECOND);
  const end = Math.min(Math.ceil(view.end / SECOND) * SECOND, Math.floor(now.end / SECOND) * SECOND);
  const width = end - start;
  if (!(width >= MIN_VIEW_MS) || width > MAX_WINDOW_MS) return null;
  if (start <= now.start && end >= now.end) return null; // it is the window already on screen
  const range = { start: iso(start), end: iso(end), baselineStart: iso(start - width), baselineEnd: iso(start) };
  const keep = isManualBucket(current) && Number.isInteger(current.bucketSeconds) && current.bucketSeconds < width / SECOND && intervalFits(range, current.bucketSeconds);
  return { ...range, bucketSeconds: keep ? current.bucketSeconds : bucketForWindows(range.start, range.end, range.baselineStart, range.baselineEnd) };
}

/** Whether the button may act, and the first reason it may not (root, then a run in progress, then unapplied changes). */
export function investigateState(input: { isRoot: boolean; runLoading: boolean; stale: boolean }): { enabled: boolean; reason: string } {
  if (input.isRoot) return { enabled: false, reason: zoomCopy.investigateTop };
  if (input.runLoading) return { enabled: false, reason: zoomCopy.investigateBusy };
  if (input.stale) return { enabled: false, reason: zoomCopy.investigateStale };
  return { enabled: true, reason: '' };
}

/**
 * Everything a click on "Investigate this window" needs, or the reason it does nothing. The loading check comes first and is the
 * guard against a double click: the first click starts a run, so the second one is refused before it can add a second trail entry.
 */
export function planInvestigation(input: { current: WindowFields; view: ZoomWindow | null; runLoading: boolean; stale: boolean; trail: TrailEntry[] }): { patch: WindowFields; trail: TrailEntry[] } | { refused: string } {
  const state = investigateState({ isRoot: input.view == null, runLoading: input.runLoading, stale: input.stale });
  if (!state.enabled) return { refused: state.reason };
  const view = input.view as ZoomWindow;
  const patch = promotedScope(input.current, view);
  if (!patch) return { refused: zoomCopy.investigateCannot };
  const entry: TrailEntry = { from: copyFields(input.current), to: { start: patch.start, end: patch.end }, lens: { start: view.start, end: view.end } };
  return { patch, trail: pushTrail(input.trail, entry) };
}

/** A new trail with `entry` last. The oldest entries past the limit are dropped. */
export function pushTrail(trail: TrailEntry[], entry: TrailEntry): TrailEntry[] {
  const next = [...trail, entry];
  return next.length > TRAIL_LIMIT ? next.slice(next.length - TRAIL_LIMIT) : next;
}
/** The last entry and the trail without it. */
export function popTrail(trail: TrailEntry[]): { entry: TrailEntry | null; trail: TrailEntry[] } {
  return trail.length ? { entry: trail[trail.length - 1], trail: trail.slice(0, -1) } : { entry: null, trail: [] };
}
/** The entry "Back to ..." would undo: the last one, and only while the query window is still the one that promotion made. */
export function backTarget(trail: TrailEntry[], current: { start: string; end: string }): TrailEntry | null {
  const last = trail[trail.length - 1];
  return last && last.to.start === current.start && last.to.end === current.end ? last : null;
}
/**
 * What a click on "Back to ..." needs, or why it does nothing, decided before anything is written (the checks Investigate makes).
 * `unapplied` is a panel edit not yet run, and `datesValid` false is a date draft that is not a time yet: either would be run along
 * with the restored window or dropped by the remount, so Back refuses. A restored scope that could not run (no source checked, say)
 * is refused too: the window, the trail and the pending lens would otherwise change with no run to show for it.
 * An empty `reason` means there is nothing to undo, and nothing to say.
 */
export function planBack(input: { trail: TrailEntry[]; current: { start: string; end: string }; scope: Pick<Query, 'sourceIds' | 'start' | 'end' | 'baselineStart' | 'baselineEnd' | 'bucketSeconds'>; unapplied: boolean; datesValid: boolean }): { ok: true; entry: TrailEntry; trail: TrailEntry[] } | { ok: false; reason: string } {
  if (!backTarget(input.trail, input.current)) return { ok: false, reason: '' };
  if (input.unapplied || !input.datesValid) return { ok: false, reason: zoomCopy.backStale };
  const popped = popTrail(input.trail);
  if (!popped.entry) return { ok: false, reason: '' };
  if (scopeProblem({ ...input.scope, ...popped.entry.from }, true)) return { ok: false, reason: zoomCopy.backProblem };
  return { ok: true, entry: popped.entry, trail: popped.trail };
}
/** "Back to 7 days":the length of the window the entry returns to. */
export function backLabel(entry: TrailEntry): string {
  const { start, end } = rootOf(entry.from);
  return zoomText.backTo(durationLabel(end - start));
}
