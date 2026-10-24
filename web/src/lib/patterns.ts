import { auditCreate } from './copy/wordings';
import type { PatternAuditStall, PatternCoverage, PatternEpisode, PatternInput, PatternReport, PatternStall, PatternVolley, Query } from './types';

/**
 * Pure text and shape logic for the Patterns drawer (P2-lite). Every sentence the drawer builds lives here so the copy lint can read it.
 * The words describe what was measured. They never name a cause.
 */

/** The server defaults, shown beside the optional overrides. */
export const DEFAULT_STALL_MS = 45_000;
export const DEFAULT_EPISODE_GAP_SECONDS = 600;

export const PATTERNS_TEXT = {
  title: 'Patterns',
  run: 'Look for patterns',
  running: 'Looking…',
  runNote: 'This reads the slowest requests in the window. It only runs when you press the button.',
  thresholds: 'Thresholds',
  stallLabel: 'Slow request threshold (ms)',
  gapLabel: 'Gap between episodes (seconds)',
  thresholdNote: 'Leave a box empty to use the default.',
  lowN: 'low n',
  truncated: 'Showing the slowest rows only; episode counts are partial.',
  failed: 'The search did not finish. Try again.',
  badThreshold: 'Enter a whole number above zero, or leave the box empty.',
  episodes: 'Episodes',
  episodesNone: 'No episodes in this window.',
  auditStalls: 'Slow audit saves',
  volleys: 'Request bursts from one user',
  stalls: 'Slow requests',
  none: 'None in this episode.'
} as const;

/** Milliseconds as seconds with at most one decimal: 45000 gives "45 s", 61300 gives "61.3 s". */
export function secondsLabel(ms: number): string {
  if (!Number.isFinite(ms)) return '0 s';
  return Number((ms / 1000).toFixed(1)) + ' s';
}

/** The same number without the unit, for sentences that carry their own "s". */
export function secondsNumber(ms: number): number {
  return Number.isFinite(ms) ? Number((ms / 1000).toFixed(1)) : 0;
}

export type Verdict = Pick<PatternReport, 'recurrence' | 'episodes' | 'params'>;

/** The one-line verdict. A single episode states its request count, never a count of separate stalls. */
export function verdictText(report: Verdict): string {
  switch (report.recurrence) {
    case 'recurring':
      return `Recurring across ${report.episodes.length} independent episodes`;
    case 'two_episodes':
      return 'Two independent episodes. Not enough to call it recurring';
    case 'single_episode':
      return `Single episode, ${report.episodes[0]?.requests ?? 0} requests`;
    default:
      return `No stalls at or above ${secondsNumber(report.params.stallMs)} s in this window`;
  }
}

export const nBadge = (n: number) => `n = ${n}`;

/** Counts of rows that could not be placed. Always shown, even at zero. */
export function qualityLine(quality: PatternReport['quality']): string {
  return `${quality.incomplete} incomplete, ${quality.pending} pending, ${quality.invalid} invalid`;
}

const pct = (value: number | undefined) => value == null || !Number.isFinite(value) ? 'unknown' : Number(value.toFixed(1)) + '%';

/** The coverage line: the two percentages when the check ran, otherwise the server's note. */
export function coverageLine(coverage: PatternCoverage | undefined): string | null {
  if (!coverage) return null;
  if (!coverage.run) return coverage.note;
  return `Request start time is filled in on ${pct(coverage.requestInitiatedAtPct)} of rows, and account id on ${pct(coverage.accountIdPct)}.`;
}

/** The exact permitted wording for an audit-create measurement (roadmap 6.6). */
export const auditStallLine = (stall: Pick<PatternAuditStall, 'auditCreateTimeMs'>): string => auditCreate(secondsNumber(stall.auditCreateTimeMs));

/** The window the search will read, for the drawer header. */
export function windowInput(query: Pick<Query, 'sourceIds' | 'start' | 'end'>, stallRaw = '', gapRaw = ''): { input: PatternInput } | { error: string } {
  const input: PatternInput = { sourceIds: query.sourceIds, start: query.start, end: query.end };
  for (const [raw, key] of [[stallRaw, 'stallMs'], [gapRaw, 'episodeGapSeconds']] as const) {
    const text = raw.trim();
    if (!text) continue;
    const value = Number(text);
    if (!/^\d+$/.test(text) || !Number.isSafeInteger(value) || value <= 0) return { error: PATTERNS_TEXT.badThreshold };
    input[key] = value;
  }
  return { input };
}

export const stallsOf = (report: Pick<PatternReport, 'stalls'>, episode: Pick<PatternEpisode, 'index'>): PatternStall[] => report.stalls.filter((s) => s.episode === episode.index);
export const volleysOf = (report: Pick<PatternReport, 'volleys'>, episode: Pick<PatternEpisode, 'index'>): PatternVolley[] => report.volleys.filter((v) => v.episode === episode.index);

/** A volley's end gaps as one short string. Ends with no recorded finish are counted, not guessed. */
export function endGapsText(volley: Pick<PatternVolley, 'endGapsMs' | 'incompleteEnds'>): string {
  const gaps = volley.endGapsMs.length ? volley.endGapsMs.map((g) => `${g} ms`).join(', ') : 'none';
  return volley.incompleteEnds > 0 ? `${gaps} (${volley.incompleteEnds} with no finish time)` : gaps;
}

export const operationText = (row: { controller: string; action: string }) => row.action ? `${row.controller} / ${row.action}` : row.controller;
