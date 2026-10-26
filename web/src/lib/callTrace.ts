// Call trace: one audited API call as a timeline (parts of the call, then SQL / Mongo / Redis / IIS lanes on one clock).
// Types mirror internal/calltrace/types.go. Everything here is plain TypeScript with no DOM, so it can be tested alone.
import { api } from './api';
import { formatDuration } from './format';
import { formatTime } from './time';

export type LaneKey = 'sql' | 'mongo' | 'redis' | 'iis' | 'other';
export type LaneState = 'events' | 'none-seen' | 'no-data' | 'clock-unchecked' | 'no-case' | 'incomplete' | 'out-of-range';
export type CheckStatus = 'consistent' | 'contradicted' | 'not-enough-data' | 'not-testable';

export interface TracePart { key: string; label: string; ms: number }
export interface TraceCall {
  id: string; sourceId: string; controller: string; action: string; outcome: string;
  start: string; end: string | null; durationMs: number | null; clockAnomaly: boolean; parts: TracePart[];
}
export interface LaneEvent { id: string; source: string; label: string; start: string; end: string; durationMs?: number; severity?: string }
export interface TraceLane { key: string; label: string; state: string; sources: string[]; events: LaneEvent[]; hidden: number }
export interface TraceCheck { id: string; title: string; status: string; detail: string; needs?: string; callIds: string[]; label: string }
export interface TraceWindow { from: string; to: string }
export interface Trace { call: TraceCall; window: TraceWindow; lanes: TraceLane[]; checks: TraceCheck[] }
export interface CallTraceResult { traces: Trace[]; compare: TraceCheck[]; sharedWindow?: TraceWindow; truncated: boolean }

export interface CallRef { sourceId: string; id: string }
/** The body of POST /api/call-trace and POST /api/cases/{id}/call-trace. */
export interface CallTraceRequest { calls: CallRef[]; beforeMin: number; afterMin: number }
export interface TrayCall extends CallRef { label: string }

export const MAX_CALLS = 3;
export const DEFAULT_MARGIN_MIN = 2;
export const MAX_MARGIN_MIN = 30;
export const MARGIN_CHOICES = [1, 2, 5, 10, 30] as const;

export const clampMargin = (value: number): number => (Number.isFinite(value) ? Math.min(MAX_MARGIN_MIN, Math.max(0, value)) : DEFAULT_MARGIN_MIN);

/* ------------------------------------------------------------------ request */

const asArray = <T>(value: T[] | null | undefined): T[] => (Array.isArray(value) ? value : []);

/** Go sends null for an empty slice. Turn each one into an empty list and refuse an answer that is not a result at all. */
export function normalizeResult(raw: unknown): CallTraceResult {
  const value = raw as Partial<CallTraceResult> | null;
  if (!value || typeof value !== 'object' || !Array.isArray(value.traces)) throw new Error('unreadable');
  const traces = value.traces.map((trace): Trace => ({
    ...trace,
    call: { ...trace.call, parts: asArray(trace.call?.parts) },
    lanes: asArray(trace.lanes).map((lane) => ({ ...lane, sources: asArray(lane.sources), events: asArray(lane.events), hidden: lane.hidden ?? 0 })),
    checks: asArray(trace.checks).map((check) => ({ ...check, callIds: asArray(check.callIds) }))
  }));
  return {
    traces,
    compare: asArray(value.compare).map((check) => ({ ...check, callIds: asArray(check.callIds) })),
    sharedWindow: value.sharedWindow ?? undefined,
    truncated: !!value.truncated
  };
}

/** Asks for one to three calls. With a case id the case's files are read; without one only the audit record is. */
export async function fetchCallTrace(calls: CallRef[], before: number, after: number, caseId: string, signal?: AbortSignal): Promise<CallTraceResult> {
  if (calls.length < 1 || calls.length > MAX_CALLS) throw new Error('Choose one to three calls.');
  const raw = await api.callTrace({ calls: calls.map((call) => ({ sourceId: call.sourceId, id: call.id })), beforeMin: clampMargin(before), afterMin: clampMargin(after) }, caseId, signal);
  return normalizeResult(raw);
}

/* ------------------------------------------------------------------ the tray (pure part) */

export type AddOutcome = 'added' | 'duplicate' | 'full' | 'missing';
export const trayKey = (call: CallRef): string => call.sourceId + '\u0000' + call.id;

/** Adds a call to a list of chosen calls. The list is returned unchanged when the call is refused, and the outcome says why. */
export function addCall(list: TrayCall[], call: TrayCall, max = MAX_CALLS): { list: TrayCall[]; outcome: AddOutcome } {
  if (!call.id || !call.sourceId) return { list, outcome: 'missing' };
  if (list.some((item) => trayKey(item) === trayKey(call))) return { list, outcome: 'duplicate' };
  if (list.length >= max) return { list, outcome: 'full' };
  return { list: [...list, { sourceId: call.sourceId, id: call.id, label: call.label }], outcome: 'added' };
}

export const removeCall = (list: TrayCall[], call: CallRef): TrayCall[] => list.filter((item) => trayKey(item) !== trayKey(call));

/* ------------------------------------------------------------------ geometry */

/** realClock: positions are real times, one shared clock. alignedToStart: positions are offsets from the start of each call. */
export type TimeMode = 'realClock' | 'alignedToStart';
/** An axis in milliseconds. In real clock mode these are epoch times; in aligned mode they are offsets from a call's start. */
export interface Axis { startMs: number; endMs: number }
export interface Box { left: number; width: number }

/** Narrowest width, in percent of the axis, that an event may be drawn with, so a 1 ms event can still be seen and focused. */
export const MIN_EVENT_PCT = 0.8;

const ms = (value: string): number => Date.parse(value);

/** Where a time sits on the axis, 0 to 100. */
export function pctOf(axis: Axis, at: number): number {
  const span = axis.endMs - axis.startMs;
  if (!(span > 0) || !Number.isFinite(at)) return 0;
  return Math.max(0, Math.min(100, ((at - axis.startMs) / span) * 100));
}

/** Position and width, in percent, of a span. A span outside the axis gives null. A very short span gets the minimum width and stays inside. */
export function spanBox(axis: Axis, startMs: number, endMs: number, minPct = MIN_EVENT_PCT): Box | null {
  if (!Number.isFinite(startMs)) return null;
  const end = Number.isFinite(endMs) && endMs >= startMs ? endMs : startMs;
  if (end < axis.startMs || startMs > axis.endMs) return null;
  const left = pctOf(axis, startMs);
  const right = pctOf(axis, end);
  const width = Math.min(100, Math.max(minPct, right - left));
  return { left: Math.min(left, 100 - width), width };
}

/** A time on the chosen axis: itself in real clock mode, the offset from the call's start in aligned mode. */
export const toAxisMs = (mode: TimeMode, callStartMs: number, at: number): number => (mode === 'realClock' ? at : at - callStartMs);

/** The window real clock mode draws, or null when it is not offered: the shared window, or the one call's own window. */
export function realClockWindow(result: CallTraceResult): TraceWindow | null {
  if (result.sharedWindow) return result.sharedWindow;
  return result.traces.length === 1 ? result.traces[0].window : null;
}

export const realClockOffered = (result: CallTraceResult): boolean => realClockWindow(result) !== null;

/** The axis for the chosen mode. Aligned mode reaches from the earliest window start to the latest window end, each measured from its own call's start. */
export function buildAxis(mode: TimeMode, result: CallTraceResult): Axis | null {
  if (mode === 'realClock') {
    const window = realClockWindow(result);
    return window ? padAxis({ startMs: ms(window.from), endMs: ms(window.to) }) : null;
  }
  if (!result.traces.length) return null;
  let startMs = Infinity;
  let endMs = -Infinity;
  for (const trace of result.traces) {
    const at = ms(trace.call.start);
    startMs = Math.min(startMs, ms(trace.window.from) - at);
    endMs = Math.max(endMs, ms(trace.window.to) - at);
  }
  return padAxis({ startMs, endMs });
}

function padAxis(axis: Axis): Axis | null {
  if (!Number.isFinite(axis.startMs) || !Number.isFinite(axis.endMs)) return null;
  return axis.endMs > axis.startMs ? axis : { startMs: axis.startMs - 500, endMs: axis.startMs + 500 };
}

/** Box of the call itself on the axis. A call with no usable end is a thin start mark. */
export function callBox(axis: Axis, mode: TimeMode, call: TraceCall): Box | null {
  const start = ms(call.start);
  const end = call.end && !call.clockAnomaly ? ms(call.end) : start;
  return spanBox(axis, toAxisMs(mode, start, start), toAxisMs(mode, start, end));
}

/** Box of one lane event on the axis. */
export function eventBox(axis: Axis, mode: TimeMode, call: TraceCall, event: LaneEvent): Box | null {
  const at = ms(call.start);
  return spanBox(axis, toAxisMs(mode, at, ms(event.start)), toAxisMs(mode, at, ms(event.end)));
}

/** Gives each box a row so boxes that overlap are stacked instead of hidden behind each other. The last row takes any overflow. */
export function packRows(boxes: Array<Box | null>, maxRows = 3, gapPct = 0.3): number[] {
  const rowEnds: number[] = [];
  const rows = new Array<number>(boxes.length).fill(0);
  const order = boxes.map((box, index) => ({ box, index })).filter((item) => item.box).sort((a, b) => a.box!.left - b.box!.left);
  for (const { box, index } of order) {
    let row = rowEnds.findIndex((end) => end + gapPct <= box!.left);
    if (row === -1) row = rowEnds.length < maxRows ? rowEnds.length : maxRows - 1;
    rowEnds[row] = Math.max(rowEnds[row] ?? -Infinity, box!.left + box!.width);
    rows[index] = row;
  }
  return rows;
}

export interface PartSegment { key: string; label: string; ms: number; widthPct: number }

/** The parts that sit inside the total (audit, API, other), as shares of one bar. Payload is not one of them. */
export function partSegments(parts: TracePart[]): PartSegment[] {
  const inside = parts.filter((part) => part.key !== 'payload' && Number.isFinite(part.ms) && part.ms > 0);
  const total = inside.reduce((sum, part) => sum + part.ms, 0);
  return total > 0 ? inside.map((part) => ({ key: part.key, label: part.label, ms: part.ms, widthPct: (part.ms / total) * 100 })) : [];
}

/** The payload record time, shown as a marker beside the bar. Null when it is not known. */
export const payloadPart = (parts: TracePart[]): TracePart | null => parts.find((part) => part.key === 'payload' && Number.isFinite(part.ms)) ?? null;

export interface Tick { pct: number; label: string }

/** "+30 s", "-2 min", "0 ms": an offset from the start of the call. */
export const offsetLabel = (offsetMs: number): string => {
  const rounded = Math.round(offsetMs);
  return (rounded < 0 ? '-' : rounded > 0 ? '+' : '') + formatDuration(Math.abs(rounded));
};

const OFFSET_UNITS = [{ name: 'ms', ms: 1 }, { name: 's', ms: 1000 }, { name: 'min', ms: 60_000 }, { name: 'h', ms: 3_600_000 }] as const;

/** One unit for a whole offset axis: the largest unit that the farthest tick still reaches twice (2 min, not 120 s). */
function offsetUnit(axis: Axis): (typeof OFFSET_UNITS)[number] {
  const far = Math.max(Math.abs(axis.startMs), Math.abs(axis.endMs));
  let unit: (typeof OFFSET_UNITS)[number] = OFFSET_UNITS[0];
  for (const candidate of OFFSET_UNITS) if (far >= candidate.ms * 2 || (candidate.ms === 1 && far > 0)) unit = candidate;
  return unit;
}

/** Tick labels of an offset axis in one unit, rounded to the step between ticks: "-2 min", "-1 min", "0 min", "+1 min", "+2 min". */
function offsetTicks(axis: Axis, steps: number): string[] {
  const unit = offsetUnit(axis);
  const step = (axis.endMs - axis.startMs) / steps / unit.ms;
  const places = step >= 10 ? 0 : step >= 1 ? 1 : 2;
  return Array.from({ length: steps + 1 }, (_, i) => {
    const value = Number(((axis.startMs + ((axis.endMs - axis.startMs) * i) / steps) / unit.ms).toFixed(places));
    return (value < 0 ? '-' : value > 0 ? '+' : '') + Math.abs(value) + ' ' + unit.name;
  });
}

/** Evenly spaced tick labels along the axis. Real clock shows times of day in the chosen zone; aligned shows offsets in one unit. */
export function tickMarks(axis: Axis, mode: TimeMode, zone: string, count = 5): Tick[] {
  const span = axis.endMs - axis.startMs;
  const steps = Math.max(1, count - 1);
  const precision = span <= 10_000 ? 'ms' : 'second';
  const offsets = mode === 'realClock' ? [] : offsetTicks(axis, steps);
  return Array.from({ length: steps + 1 }, (_, i) => {
    const at = axis.startMs + (span * i) / steps;
    return { pct: (i / steps) * 100, label: mode === 'realClock' ? formatTime(at, { zone, date: false, suffix: false, precision }) : offsets[i] };
  });
}

/** Where the call starts on the axis (the guide line): its own start in real clock mode, zero in aligned mode, per call. */
export const startPct = (axis: Axis, mode: TimeMode, call: TraceCall): number => pctOf(axis, toAxisMs(mode, ms(call.start), ms(call.start)));

/** 1-based numbers of the calls a check names, in trace order, so a check can say "Calls 1 and 2". */
export function callNumbers(check: TraceCheck, traces: Trace[]): number[] {
  return traces.map((trace, index) => (check.callIds.includes(trace.call.id) ? index + 1 : 0)).filter((n) => n > 0);
}

/** One line that describes an event for a tooltip, a label or a list. */
export function eventText(event: LaneEvent, zone: string): string {
  const when = formatTime(event.start, { zone, precision: 'ms' });
  const length = event.durationMs == null ? 'length unknown' : formatDuration(event.durationMs);
  return `${event.label} · ${length} · ${when}`;
}
