// The one window the whole Slow call screen follows: presets, custom values, Earlier/Later,
// Back to this call and the wide-window rule. Pure; no browser or network code.

const MINUTE_MS = 60000;

export const WINDOW_PRESETS = [5, 10, 15, 30, 60] as const; // minutes each side
export const WINDOW_MIN = 1;
export const WINDOW_MAX = 120; // custom, minutes
export const OVERVIEW_MINUTES = 60; // strip = call start +/- 60 min
export const WIDE_SECONDS = 901; // beyond +/-15 min the server is asked

export interface CallWindow {
  before: number; // minutes before the call
  after: number; // minutes after the call
  shiftMs: number; // ms the window has moved from the call
}

export const DEFAULT_WINDOW: CallWindow = { before: 15, after: 15, shiftMs: 0 };

export function clampMinutes(value: unknown): number | null {
  if (typeof value !== 'number' || !Number.isInteger(value)) return null;
  return value >= WINDOW_MIN && value <= WINDOW_MAX ? value : null;
}

export function windowBounds(callStartMs: number, w: CallWindow): { start: number; end: number } {
  return {
    start: callStartMs - w.before * MINUTE_MS + w.shiftMs,
    end: callStartMs + w.after * MINUTE_MS + w.shiftMs
  };
}

export function overviewBounds(callStartMs: number): { start: number; end: number } {
  return {
    start: callStartMs - OVERVIEW_MINUTES * MINUTE_MS,
    end: callStartMs + OVERVIEW_MINUTES * MINUTE_MS
  };
}

// True when the window already shows the whole strip, so the strip is hidden.
export function coversOverview(callStartMs: number, w: CallWindow): boolean {
  const win = windowBounds(callStartMs, w);
  const ov = overviewBounds(callStartMs);
  return win.start <= ov.start && win.end >= ov.end;
}

export function presetOf(w: CallWindow): number | null {
  if (w.shiftMs !== 0 || w.before !== w.after) return null;
  return (WINDOW_PRESETS as readonly number[]).includes(w.before) ? w.before : null;
}

// Allowed shift range that keeps the window inside the overview; null when it cannot fit.
function shiftRange(w: CallWindow): { lo: number; hi: number } | null {
  const lo = (w.before - OVERVIEW_MINUTES) * MINUTE_MS;
  const hi = (OVERVIEW_MINUTES - w.after) * MINUTE_MS;
  return lo > hi ? null : { lo, hi };
}

function clampShift(w: CallWindow, shiftMs: number): CallWindow {
  const range = shiftRange(w);
  if (!range) return w;
  const next = Math.min(range.hi, Math.max(range.lo, Math.round(shiftMs)));
  return next === w.shiftMs ? w : { ...w, shiftMs: next };
}

// Earlier (-1) or Later (+1): move half a window, kept inside the overview.
export function stepWindow(_callStartMs: number, w: CallWindow, dir: -1 | 1): CallWindow {
  const half = ((w.before + w.after) / 2) * MINUTE_MS;
  const next = clampShift(w, w.shiftMs + dir * half);
  // A step never moves the window against its direction (a window left out of range stays put).
  return (next.shiftMs - w.shiftMs) * dir > 0 ? next : w;
}

// Pull the shift back inside the overview; call when before or after change (preset or custom).
export function clampWindow(w: CallWindow): CallWindow {
  return clampShift(w, w.shiftMs);
}

export function canStep(callStartMs: number, w: CallWindow, dir: -1 | 1): boolean {
  return stepWindow(callStartMs, w, dir).shiftMs !== w.shiftMs;
}

// Minimap drag: put the middle of the window on centreMs, kept inside the overview.
export function centreOn(callStartMs: number, w: CallWindow, centreMs: number): CallWindow {
  const offsetMs = ((w.after - w.before) / 2) * MINUTE_MS;
  return clampShift(w, centreMs - callStartMs - offsetMs);
}

export function backToCall(w: CallWindow): CallWindow {
  return w.shiftMs === 0 ? w : { ...w, shiftMs: 0 };
}

// More than 15 min on a side, or moved away from the call: the server is asked.
export function isWide(w: CallWindow): boolean {
  return w.before * 60 > WIDE_SECONDS - 1 || w.after * 60 > WIDE_SECONDS - 1 || w.shiftMs !== 0;
}

// True when any part of the call falls inside the window.
export function callInside(callStartMs: number, callEndMs: number, w: CallWindow): boolean {
  const win = windowBounds(callStartMs, w);
  const end = Number.isFinite(callEndMs) ? Math.max(callEndMs, callStartMs) : callStartMs;
  return callStartMs <= win.end && end >= win.start;
}
