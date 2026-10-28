import { parseLocalUtc, toLocalUtc, type Frame } from './frame';
import { fmtMs, fmtNumber, fmtPct, fmtWhen, markFor, type ChangeMark } from './stepModel';
import type { Change, Result } from './types';

/**
 * After the fix: the same hours on a later day, set beside the incident day. The engine is run once with the later day as the incident window and
 * the earlier (slow) day as the reference window, so every change reads "against the earlier day". This file only classifies that answer. It
 * never calls a day fixed unless the slow end is back inside the normal range, and it never compares against minutes that do not exist.
 */

export type FixOutcome = 'fixed' | 'better' | 'unchanged' | 'worse' | 'no-data' | 'too-few' | 'partly' | 'unlike';

/** Calls per second that differ by this many percent make the days unlike. */
export const UNLIKE_CALLS_PCT = 50;
/** Half the summed difference between the two days' route shares. 0.3 means 30 in 100 calls moved to other routes. */
export const MIX_SHIFT = 0.3;
/** A day with fewer than this share of its minutes testable is only partly there. */
export const PARTLY_SHARE = 0.8;

export interface FixContext {
  /** The slow end of the normal period of the case (the reference of the main run), in ms. Without it a day is "better" at best, never "fixed". */
  normalP95?: number;
  /** The length of each window in minutes. */
  windowMinutes: number;
  /** Words for the later day, such as "27 Oct 10:30 to 11:00". */
  laterLabel: string;
  /**
   * The case's own figures for the earlier day (the incident day of the main run), used for the Earlier day column when the comparison run
   * has none. Give it only when the same API is compared; for another API these figures would be about something else.
   */
  earlier?: { p95?: number; p50?: number; mean?: number; l?: number };
}

export interface FixRow { measure: string; earlier: string; later: string; change: string; mark: ChangeMark | null }

export interface FixVerdict {
  outcome: FixOutcome;
  /** The heading: "Fixed", "Better, not fixed", ... */
  word: string;
  sentence: string;
  rows: FixRow[];
  warnings: string[];
  /** One line for "What we learned". */
  recordLine: string;
  /** False when there is nothing to record: no data for that day. */
  canRecord: boolean;
}

export const OUTCOME_WORD: Readonly<Record<FixOutcome, string>> = {
  fixed: 'Fixed', better: 'Better, not fixed', unchanged: 'Unchanged', worse: 'Worse', 'no-data': 'No data for that day', 'too-few': 'Too few calls', partly: 'Partly there', unlike: 'The days are not alike'
};

const pct = (from: number | undefined, to: number | undefined): number | undefined => (typeof from === 'number' && typeof to === 'number' && from > 0 ? ((to - from) / from) * 100 : undefined);

/** Half the summed difference of the route shares, from 0 (same mix) to 1 (nothing in common). */
export function mixShift(result: Result): number {
  const routes = result.facts.routes;
  const inc = routes.reduce((t, r) => t + r.incident.n, 0);
  const ref = routes.reduce((t, r) => t + r.reference.n, 0);
  if (!inc || !ref) return 0;
  return routes.reduce((t, r) => t + Math.abs(r.incident.n / inc - r.reference.n / ref), 0) / 2;
}

/** True when `later` is above `normal` by the engine's own rule: both the percent mark and the millisecond mark. */
export function aboveNormal(normal: number, later: number, th: { changeMinPercent: number; changeMinMs: number }): boolean {
  if (!(normal > 0)) return false;
  const delta = later - normal;
  return delta >= th.changeMinMs && (delta / normal) * 100 >= th.changeMinPercent;
}

function rowsFor(result: Result, earlier: FixContext['earlier']): FixRow[] {
  const c = result.facts.change;
  const l = result.facts.load;
  const row = (measure: string, change: Change, fallback: number | undefined): FixRow => ({
    measure, earlier: fmtMs(change.refMs ?? fallback), later: fmtMs(change.incMs), change: change.known && typeof change.deltaPct === 'number' ? fmtPct(change.deltaPct) : 'No data', mark: markFor(change)
  });
  const refL = l.refL ?? earlier?.l;
  const lChange = pct(refL, l.incL);
  return [
    row('P95 (slow end)', c.p95, earlier?.p95),
    row('P50 (middle)', c.p50, earlier?.p50),
    row('D (whole call, average)', c.mean, earlier?.mean),
    { measure: 'L (calls in flight)', earlier: fmtNumber(refL, 1), later: fmtNumber(l.incL, 1), change: lChange === undefined ? 'No data' : fmtPct(lChange), mark: null }
  ];
}

const ofLine = (ctx: FixContext, p95: Change, word: string, extra = ''): string => {
  const line = `After the fix, ${ctx.laterLabel} UTC: P95 ${fmtMs(p95.refMs)} to ${fmtMs(p95.incMs)}. ${word}.${extra ? ' ' + extra : ''}`;
  return line.length <= 300 ? line : line.slice(0, 299) + '…';
};

export function classifyDay(result: Result, ctx: FixContext): FixVerdict {
  const f = result.facts;
  const p95 = f.change.p95;
  const th = result.thresholds;
  const rows = rowsFor(result, ctx.earlier);
  const warnings: string[] = [];
  const done = (outcome: FixOutcome, sentence: string, record = outcome !== 'no-data' && outcome !== 'too-few', extra = ''): FixVerdict => ({
    outcome, word: OUTCOME_WORD[outcome], sentence, rows, warnings, canRecord: record,
    recordLine: record ? ofLine(ctx, p95, OUTCOME_WORD[outcome], extra) : ''
  });

  if (f.counts.incident === 0) return done('no-data', 'Unravis found no calls on that day, in those hours. Nothing is compared against zero. Pick another day, or add the data for it.');
  if (f.counts.reference === 0) return done('no-data', 'The earlier day has no calls in the audit data, so there is nothing to compare with.');
  if (!p95.known) return done('too-few', 'Too few calls on one of the days to read the slow end. Pick a wider window or another day.');

  const callsChange = pct(f.reference.perSec, f.incident.perSec);
  const mix = mixShift(result);
  const minutes = ctx.windowMinutes;
  const missing = minutes > 0 && f.incident.testableMinutes < minutes * PARTLY_SHARE;
  const unlike = (callsChange !== undefined && Math.abs(callsChange) >= UNLIKE_CALLS_PCT) || mix >= MIX_SHIFT;
  const unlikeWhy = callsChange !== undefined && Math.abs(callsChange) >= UNLIKE_CALLS_PCT
    ? `Calls per second ${callsChange < 0 ? 'fell' : 'rose'} ${Math.abs(Math.round(callsChange))} % and the mix of APIs may have changed.`
    : 'The mix of APIs changed a lot between the two days.';

  if (missing) {
    warnings.push(`Only ${f.incident.testableMinutes} of ${minutes} minutes of that day have data. The numbers use those minutes. Missing minutes are not counted as zero.`);
    if (unlike) warnings.push(unlikeWhy);
    return done('partly', `Part of that day has no data. On the minutes that exist, the slow end went from ${fmtMs(p95.refMs)} to ${fmtMs(p95.incMs)}. Unravis does not call it fixed.`, true, 'Not all minutes had data.');
  }
  if (unlike) {
    warnings.push(unlikeWhy);
    return done('unlike', `${unlikeWhy} The days are not alike, so Unravis does not call it fixed.`, true, `Warning: ${unlikeWhy}`);
  }

  if (p95.meaningful && p95.direction === 'up') return done('worse', `The slow end is higher than on the earlier day: ${fmtMs(p95.refMs)} to ${fmtMs(p95.incMs)}.`);
  if (p95.meaningful && p95.direction === 'down') {
    const normal = ctx.normalP95;
    if (typeof normal === 'number' && typeof p95.incMs === 'number' && !aboveNormal(normal, p95.incMs, th)) {
      return done('fixed', `The slow end fell from ${fmtMs(p95.refMs)} to ${fmtMs(p95.incMs)}. That is back inside the normal range of ${fmtMs(normal)}.`);
    }
    if (typeof normal !== 'number') warnings.push('The normal period of the case has no slow end, so Unravis cannot say it is back to normal.');
    return done('better', `The slow end fell from ${fmtMs(p95.refMs)} to ${fmtMs(p95.incMs)}${typeof normal === 'number' ? `, but it is still above the normal ${fmtMs(normal)}` : ''}.`);
  }
  return done('unchanged', `The slow end did not move by the rule (${th.changeMinPercent} % and ${th.changeMinMs} ms): ${fmtMs(p95.refMs)} against ${fmtMs(p95.incMs)}.`);
}

/** `27 Oct 10:30 to 11:00`: the later day's hours in UTC, from two ISO times. */
/**
 * A verdict belongs to the answer it was compared against. When a new run replaces that answer the verdict is no longer about what is on
 * screen, so it is not shown.
 */
export const verdictIsCurrent = (madeFor: unknown, now: unknown): boolean => madeFor !== null && madeFor !== undefined && madeFor === now;

/**
 * The verdict to put on screen, or null. It shows only when the last compare finished (`phase` done), nothing is wrong with the day just
 * picked (`problem` empty), and it was made for the answer now on screen. A day that cannot be compared, such as one that overlaps the incident,
 * gets its one message and no table left over from an earlier day.
 */
export function verdictToShow(made: FixVerdict | null, madeFor: unknown, now: unknown, phase: string, problem: string): FixVerdict | null {
  if (!made || problem || phase !== 'done') return null;
  return verdictIsCurrent(madeFor, now) ? made : null;
}

export function dayLabel(startIso: string, endIso: string): string {
  const s = fmtWhen(startIso);
  const e = fmtWhen(endIso);
  if (s === 'No data' || e === 'No data') return 'the chosen hours';
  return s.slice(0, 6) === e.slice(0, 6) ? `${s} to ${e.slice(7)}` : `${s} to ${e}`;
}

/**
 * The frame for the comparison run: the incident window of the case becomes the earlier (reference) window, and the same hours on `day`
 * (`YYYY-MM-DD`, UTC) become the incident window. Returns null when the day cannot be read or the incident window cannot be used. A later
 * window that shares time with the earlier one is returned as it is: `frameProblems` says so in words.
 */
export function laterFrame(frame: Frame, day: string): Frame | null {
  const dayMs = parseLocalUtc(`${day}T00:00`);
  const start = parseLocalUtc(frame.incidentStart);
  const end = parseLocalUtc(frame.incidentEnd);
  if (!Number.isFinite(dayMs) || !Number.isFinite(start) || !Number.isFinite(end) || end <= start) return null;
  const startOfDay = Math.floor(start / 86400000) * 86400000;
  const laterStart = dayMs + (start - startOfDay);
  return {
    ...frame,
    incidentStart: toLocalUtc(laterStart), incidentEnd: toLocalUtc(laterStart + (end - start)),
    referenceStart: frame.incidentStart, referenceEnd: frame.incidentEnd
  };
}

/**
 * True when `day` (`YYYY-MM-DD`, UTC) is a day after the incident day of `frame`. The comparison is the same hours on a later day, so the
 * incident day itself, and any day before it, cannot be that. False also when either date cannot be read.
 */
export function isLaterDay(frame: Frame, day: string): boolean {
  const dayMs = parseLocalUtc(`${day}T00:00`);
  const start = parseLocalUtc(frame.incidentStart);
  if (!Number.isFinite(dayMs) || !Number.isFinite(start)) return false;
  return dayMs > Math.floor(start / 86400000) * 86400000;
}
