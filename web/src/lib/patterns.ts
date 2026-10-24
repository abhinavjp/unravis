import { auditCreate } from './copy/wordings';
import type { EvidenceLabel, PatternAuditStall, PatternCoverage, PatternEpisode, PatternInput, PatternReport, PatternStall, PatternVolley, Query } from './types';

/**
 * Pure text and shape logic for the Patterns drawer (P2-lite). Every sentence the drawer builds lives here so the copy lint can read it.
 * The words describe what was measured. They never name a cause.
 */

/** The server defaults, shown beside the optional overrides. */
export const DEFAULT_STALL_MS = 45_000;
export const DEFAULT_EPISODE_GAP_SECONDS = 600;
/** The boxes in the drawer hold seconds, the unit people read. The server still takes milliseconds for the slow request limit. */
export const DEFAULT_STALL_SECONDS = DEFAULT_STALL_MS / 1000;

export const PATTERNS_TEXT = {
  title: 'Patterns',
  run: 'Look for patterns',
  running: 'Looking…',
  runNote: 'This reads the slowest requests in the window. It only runs when you press the button.',
  thresholds: 'Thresholds',
  intro: 'Patterns looks at the slowest requests in this window and groups the ones that happened close together. Each group is an episode. Several separate episodes may mean the same thing keeps happening.',
  rangeLabel: 'Range',
  rangeNote: 'This is the same range as the page behind this panel.',
  stallLabel: 'Slow request (s)',
  gapLabel: 'Gap that splits episodes (s)',
  thresholdNote: 'A request at or above the first limit counts as slow. Slow requests stay in one episode until the quiet gap between them is longer than the second limit.',
  thresholdsChanged: 'changed',
  reset: 'Reset to defaults',
  stale: 'The range or limits changed after this search. Press the button to look again.',
  stallBad: 'Slow request must be a number of seconds above zero, such as 45.',
  gapBad: 'The gap must be a whole number of seconds above zero, such as 600.',
  openRecord: 'Open record',
  cite: 'Cite in ledger',
  citeAdded: 'Added to the ledger drawer. Attach it to an entry there.',
  citeAlready: 'It is already in the ledger drawer.',
  citeCopied: 'The ledger could not take this directly. The reference is copied, so paste it into the ledger.',
  citeFailed: 'This could not be added to the ledger.',
  emptyTitle: 'No slow requests in this window',
  lowN: 'low n',
  truncated: 'Showing the slowest rows only; episode counts are partial.',
  auditsTruncated: 'Showing the newest audit save rows only; the slow audit save count is partial.',
  reference: 'Reference',
  referenceNote: 'Copy this into the ledger as evidence.',
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

/**
 * The row-cap notices, one per side that was cut. An older server sends only `truncated`;
 * then the stall notice is shown, as before.
 */
export function truncationNotices(report: Pick<PatternReport, 'truncated' | 'stallsTruncated' | 'auditsTruncated'>): string[] {
  const split = report.stallsTruncated !== undefined || report.auditsTruncated !== undefined;
  const stalls = split ? !!report.stallsTruncated : report.truncated;
  const audits = split && !!report.auditsTruncated;
  return [...(stalls ? [PATTERNS_TEXT.truncated] : []), ...(audits ? [PATTERNS_TEXT.auditsTruncated] : [])];
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

// ---- Range chips (the drawer mirrors the page's range) ----

/** The ranges the drawer offers, in order. They are ids of the page's own quick ranges, so one click here is one click there. */
export const PATTERN_RANGES = [
  { id: '15m', label: '15 min' },
  { id: '1h', label: '1 h' },
  { id: '4h', label: '4 h' },
  { id: '24h', label: '24 h' }
] as const;

// ---- Thresholds in seconds ----

const wholeSeconds = /^\d+$/;
const stallSeconds = /^\d+(?:\.\d{1,3})?$/;

/** True when either box differs from its default. A blank box counts as the default. */
export function thresholdsChanged(stallSec: string, gapSec: string): boolean {
  const stall = stallSec.trim(), gap = gapSec.trim();
  const stallMs = stall && stallSeconds.test(stall) ? Math.round(Number(stall) * 1000) : null;
  return (stall !== '' && stallMs !== DEFAULT_STALL_MS) || (gap !== '' && gap !== String(DEFAULT_EPISODE_GAP_SECONDS));
}

/**
 * The window plus the two limits, typed in seconds. A value equal to the default is not sent, so a default run is the same request as before.
 * An empty box means the default.
 */
export function windowInputSeconds(query: Pick<Query, 'sourceIds' | 'start' | 'end'>, stallSec = '', gapSec = ''): { input: PatternInput } | { error: string } {
  const stall = stallSec.trim(), gap = gapSec.trim();
  let stallRaw = '', gapRaw = '';
  if (stall) {
    const ms = stallSeconds.test(stall) ? Math.round(Number(stall) * 1000) : 0;
    if (!Number.isSafeInteger(ms) || ms <= 0) return { error: PATTERNS_TEXT.stallBad };
    if (ms !== DEFAULT_STALL_MS) stallRaw = String(ms);
  }
  if (gap) {
    if (!wholeSeconds.test(gap) || !Number.isSafeInteger(Number(gap)) || Number(gap) <= 0) return { error: PATTERNS_TEXT.gapBad };
    if (Number(gap) !== DEFAULT_EPISODE_GAP_SECONDS) gapRaw = String(Number(gap));
  }
  return windowInput(query, stallRaw, gapRaw);
}

/** The empty state: what was looked for, and the two things that would change the answer. */
export const emptyEpisodesText = (stallMs: number): string =>
  `No requests took ${secondsNumber(stallMs)} s or longer in this window. Try a wider range above, or lower the slow request limit under Thresholds.`;

// ---- Findings, merged ----

export interface FindingRow { key: string; label: EvidenceLabel; n: number; lowN: boolean; headline: string; evidenceId: string }
type FindingSource = Pick<PatternReport, 'findings' | 'episodes' | 'recurrence' | 'params' | 'truncated' | 'stallsTruncated'>;

/**
 * The findings as rows. The server sends "N requests took X s or longer" and, separately, "Recurring: E separate episodes, N requests in all".
 * That is one fact, so the two become one sentence. The merged row cites the episode finding's reference, since that is the one that counts episodes.
 */
export function findingRows(report: FindingSource): FindingRow[] {
  const toRow = (f: FindingSource['findings'][number]): FindingRow => ({ key: f.rule + f.evidenceId, label: f.label, n: f.n, lowN: f.lowN, headline: f.headline, evidenceId: f.evidenceId });
  const b1 = report.findings.find((f) => f.rule === 'B1');
  const b2 = report.findings.find((f) => f.rule === 'B2');
  const merge = !!b1 && !!b2 && report.episodes.length > 0;
  const rows: FindingRow[] = [];
  for (const finding of report.findings) {
    if (!merge || (finding.rule !== 'B1' && finding.rule !== 'B2')) { rows.push(toRow(finding)); continue; }
    if (finding.rule === 'B2' || !b1 || !b2) continue;
    const partial = report.stallsTruncated !== undefined ? !!report.stallsTruncated : report.truncated;
    const requests = `${b1.n} ${b1.n === 1 ? 'request' : 'requests'}`;
    const episodes = report.episodes.length === 1 ? 'in a single episode' : `in ${report.episodes.length} separate episodes`;
    const lead = partial ? `At least ${requests} took` : `${requests.charAt(0).toUpperCase()}${requests.slice(1)} took`;
    const tail = report.recurrence === 'two_episodes' ? ' That is not enough to call it recurring.' : '';
    rows.push({ key: 'B1B2' + b2.evidenceId, label: 'Symptom', n: b1.n, lowN: b1.lowN || b2.lowN, headline: `${lead} ${secondsNumber(report.params.stallMs)} s or longer, ${episodes}.${tail}`, evidenceId: b2.evidenceId });
  }
  return rows;
}

// ---- References for the ledger ----

/** The ledger accepts references that match this (lowercase letters and digits first, then letters, digits and : _ . -). */
export const EVIDENCE_ID_PATTERN = /^[a-z0-9][a-z0-9:_.\-]{0,119}$/;
export const isEvidenceId = (id: string): boolean => EVIDENCE_ID_PATTERN.test(id);

/** A stable reference to one episode, built from its start time. Empty when the start cannot make a valid reference. */
export function episodeEvidenceId(start: string): string {
  const id = 'episode:' + start.trim().toLowerCase().replace(/[^a-z0-9:_.\-]/g, '-');
  return isEvidenceId(id) ? id : '';
}

/** The short name shown in the ledger beside a reference, never longer than 60 characters. */
export function citeLabel(text: string): string {
  const flat = text.replace(/\s+/g, ' ').trim();
  return flat.length <= 60 ? flat : flat.slice(0, 59).trimEnd() + '…';
}
export const episodeCiteLabel = (timeText: string): string => citeLabel('Episode at ' + timeText);
