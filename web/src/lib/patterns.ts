import { auditCreate, notObservedGap } from './copy/wordings';
import { MIN_SAMPLE } from './thresholds';
import type {
  EvidenceLabel, PatternAuditStall, PatternConcentration, PatternCoverage, PatternEpisode, PatternInput, PatternOnsetMinute,
  PatternRepeat, PatternReport, PatternStall, PatternVolley, Query
} from './types';

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
  none: 'None in this episode.',
  // B3a: stall onsets at one clock minute
  onsetTitle: 'Slow requests at one clock minute',
  onsetIntro: 'This looks at the first slow request of each episode. It checks whether episodes began near the same minute of the hour on different days.',
  onsetHint: 'A scheduled task could line up with this; the job list import is needed to check.',
  onsetNeedsDays: 'This check needs a window of at least 3 days.',
  onsetNone: 'No clock minute stood out as the start of episodes on 3 or more different days.',
  onsetLabel: 'Symptom',
  // A3: concentration
  concTitle: 'Operations and users with more slow requests than traffic',
  concIntro: 'Each row compares one operation or user share of the slow requests with its share of all requests. A ratio of 2 or more is shown as concentrated.',
  concNone: 'No operation or user had a share of slow requests that was twice its share of traffic.',
  concSingle: 'single episode',
  concSpread: 'Slow requests are spread like traffic.',
  // B12r: repeats
  repeatsTitle: 'Same operation started again',
  repeatsIntro: 'A user started the same operation again while an earlier one was still running. The later request does not have to be slow itself.',
  repeatsNone: 'No user started the same operation again while it was running.',
  repeatsSameRequest: 'Same request',
  repeatsDifferentRequest: 'Same operation, different request',
  repeatsMixedRequest: 'Some with the same request, some different',  repeatsLabel: 'Correlation',
  // B6g: audit create after quiet gaps
  idleTitle: 'Audit create after quiet gaps',
  idleIntro: 'This sorts requests by how long the source was quiet before each one started. It then compares audit create times across those groups.',
  idleCaveat: 'Audit create covers setup work and the database write, not just the write.',
  idleSources: 'The source does not say which server handled a request, so gaps are measured across all servers writing to it.',
  idleOptIn: 'Also check audit create after quiet gaps',
  idleOptInNote: 'This reads every request in the window, so it takes longer.',
  idlePartial: 'Partial read. The comparison is withheld.',
  idleLabel: 'Symptom',
  idleRaised: 'Raised after quiet gaps',
  idleThin: 'low n'
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

// ---- Shared small helpers for the added rules ----

/** True when the stall read hit the row cap. An older server sends only `truncated`, and that means the stall side. */
export const stallsCapped = (report: Pick<PatternReport, 'truncated' | 'stallsTruncated'>): boolean =>
  report.stallsTruncated !== undefined ? !!report.stallsTruncated : !!report.truncated;

/** How many episodes a field names. The contract sends either a count or the list of episode indexes. */
export function episodeCount(value: number | ReadonlyArray<unknown> | string | null | undefined): number {
  if (Array.isArray(value)) return new Set(value.map(String)).size;
  const n = typeof value === 'number' ? value : typeof value === 'string' && value.trim() !== '' ? Number(value) : 0;
  return Number.isFinite(n) && n > 0 ? Math.floor(n) : 0;
}

/** A fraction from 0 to 1 as a percentage: 0.4 gives "40%", 0.05 gives "5%", 0.0123 gives "1.2%". */
export function shareText(fraction: number): string {
  if (!Number.isFinite(fraction) || fraction < 0) return 'unknown';
  const percent = fraction * 100;
  return (percent >= 10 ? Math.round(percent) : Number(percent.toFixed(1))) + '%';
}

/** A ratio as plain words: 8 gives "8 times", 2.5 gives "2.5 times", 1 gives "1 time". */
export function timesText(ratio: number): string {
  if (!Number.isFinite(ratio) || ratio < 0) return 'unknown';
  const value = ratio >= 10 ? Math.round(ratio) : Number(ratio.toFixed(1));
  return `${value} ${value === 1 ? 'time' : 'times'}`;
}

const MS_PER_DAY = 86_400_000;
const windowMsOf = (query: Pick<Query, 'start' | 'end'>): number => {
  const span = Date.parse(query.end) - Date.parse(query.start);
  return Number.isFinite(span) && span > 0 ? span : 0;
};

// ---- B3a: stall onsets at one clock minute ----

/** The shortest window in which the same minute of the hour can repeat on three different days. */
export const ONSET_MIN_DAYS = 3;

/**
 * How many minutes a zone's clock sits ahead of UTC within the hour, from 0 to 59 (0 for UTC and every whole-hour zone, 30 for Asia/Kolkata).
 * The report carries no zone, so its minute is read as the UTC minute and shifted by this amount to match the times on screen.
 */
export function zoneMinuteShift(zone: string, atMs: number): number {
  if (!Number.isFinite(atMs)) return 0;
  try {
    const minuteIn = (timeZone: string) => Number(new Intl.DateTimeFormat('en-GB', { timeZone, minute: '2-digit' }).formatToParts(new Date(atMs)).find((part) => part.type === 'minute')?.value);
    const shift = minuteIn(zone || 'UTC') - minuteIn('UTC');
    return Number.isFinite(shift) ? ((shift % 60) + 60) % 60 : 0;
  } catch { return 0; }
}

export const shiftMinute = (minute: number, shift: number): number => ((Math.round(minute) + shift) % 60 + 60) % 60;
/** A minute of the hour with its colon and two digits: 7 gives ":07". */
export const minuteText = (minute: number): string => ':' + String(shiftMinute(minute, 0)).padStart(2, '0');
/** Says which clock the minutes are read on, so ":07" is never ambiguous. */
export const onsetZoneNote = (zone: string): string => `Minutes are read on the ${zone || 'UTC'} clock.`;

export interface OnsetRow { key: string; minute: number; minuteText: string; days: number; onsets: number; line: string }

/** The recurring onset minutes, strongest first. `shift` is zoneMinuteShift for the zone on screen. */
export function onsetRows(onsets: ReadonlyArray<PatternOnsetMinute> | undefined, shift = 0): OnsetRow[] {
  const seen = new Set<number>();
  const rows: OnsetRow[] = [];
  for (const row of onsets ?? []) {
    if (!Number.isFinite(row.minute) || !Number.isFinite(row.days)) continue;
    const minute = shiftMinute(row.minute, shift);
    if (seen.has(minute)) continue;
    seen.add(minute);
    const days = Math.max(0, Math.floor(row.days));
    rows.push({
      key: 'onset-' + minute, minute, minuteText: minuteText(minute), days, onsets: Math.max(0, Math.floor(row.onsets) || 0),
      line: `Slow requests started near minute ${minuteText(minute)} of the hour on ${days} different ${days === 1 ? 'day' : 'days'} in this window.`
    });
  }
  return rows.sort((a, b) => b.days - a.days || b.onsets - a.onsets || a.minute - b.minute);
}

export type OnsetSection =
  | { kind: 'hidden' }
  | { kind: 'note'; text: string }
  | { kind: 'rows'; rows: OnsetRow[] };

/**
 * What the clock-minute section shows. It is hidden when the server sent nothing and could not have (a short window, no cap).
 * A capped stall read withholds the check, and a window under 3 days cannot show a repeat across days.
 */
export function onsetSection(report: Pick<PatternReport, 'onsetMinutes' | 'truncated' | 'stallsTruncated'>, query: Pick<Query, 'start' | 'end'>, shift = 0): OnsetSection {
  const longEnough = windowMsOf(query) >= ONSET_MIN_DAYS * MS_PER_DAY;
  if (longEnough && stallsCapped(report)) return { kind: 'note', text: notObservedGap('stall rows were capped, so this check was not run') };
  if (!report.onsetMinutes) return { kind: 'hidden' };
  const rows = onsetRows(report.onsetMinutes, shift);
  if (rows.length) return { kind: 'rows', rows };
  return { kind: 'note', text: longEnough ? PATTERNS_TEXT.onsetNone : PATTERNS_TEXT.onsetNeedsDays };
}

// ---- A3: concentration ----

/** The same limits the server applies (A3): a ratio of at least 2, at least 10 slow requests, at least 20 requests in all. */
export const CONCENTRATION_MIN_LIFT = 2;
export const CONCENTRATION_MIN_STALLS = 10;
/** The ratio at or below which slow requests are spread like traffic, with at least 50 slow requests behind it. */
export const CONCENTRATION_SPREAD_LIFT = 1.2;
export const CONCENTRATION_SPREAD_MIN_STALLS = 50;
/** An operation or user must appear in this many separate episodes before the row is called a Correlation. */
export const CONCENTRATION_MIN_EPISODES = 3;

export type ConcentrationReading = 'concentrated' | 'spread' | 'between' | 'withheld';
export interface ConcentrationRow {
  key: string; dimension: PatternConcentration['dimension']; keyText: string; reading: ConcentrationReading;
  /** Correlation or Symptom for a concentrated row, null for the rest. */
  label: EvidenceLabel | null; episodes: number; episodesText: string; stallN: number; allN: number; lowN: boolean; line: string;
}

const dimensionText = (row: Pick<PatternConcentration, 'dimension' | 'key'>) => row.dimension === 'user' ? `User ${row.key}` : row.key;
export const episodesText = (count: number): string => count === 1 ? PATTERNS_TEXT.concSingle : `${count} episodes`;

export function concentrationRow(row: PatternConcentration): ConcentrationRow {
  const keyText = dimensionText(row);
  const episodes = episodeCount(row.episodes);
  const reason = typeof row.withheld === 'string' ? row.withheld.trim() : '';
  const thin = !(row.allN >= MIN_SAMPLE);
  const base = { key: `${row.dimension}:${row.key}`, dimension: row.dimension, keyText, episodes, episodesText: episodesText(episodes), stallN: row.stallN, allN: row.allN, lowN: thin };
  if (row.withheld || thin || !Number.isFinite(row.lift)) {
    // A server reason is shown as sent. Otherwise a thin cell says why, and nothing else about it is shown.
    const line = reason ? `${keyText}: not compared (${reason}).`
      : thin ? `${keyText}: too few requests to compare (fewer than ${MIN_SAMPLE}).`
      : `${keyText}: not compared.`;
    return { ...base, reading: 'withheld', label: null, line };
  }
  const slow = shareText(row.stallShare), traffic = shareText(row.trafficShare);
  if (row.lift >= CONCENTRATION_MIN_LIFT && row.stallN >= CONCENTRATION_MIN_STALLS) {
    return {
      ...base, reading: 'concentrated', label: episodes >= CONCENTRATION_MIN_EPISODES ? 'Correlation' : 'Symptom',
      line: `${keyText} had ${slow} of slow requests but ${traffic} of all requests in this window (${timesText(row.lift)} its share of traffic).`
    };
  }
  if (row.lift <= CONCENTRATION_SPREAD_LIFT && row.stallN >= CONCENTRATION_SPREAD_MIN_STALLS) {
    return { ...base, reading: 'spread', label: null, line: `${keyText} had ${slow} of slow requests and ${traffic} of all requests in this window. ${PATTERNS_TEXT.concSpread}` };
  }
  return { ...base, reading: 'between', label: null, line: `${keyText} had ${slow} of slow requests and ${traffic} of all requests in this window.` };
}

const READING_ORDER: Record<ConcentrationReading, number> = { concentrated: 0, spread: 1, between: 2, withheld: 3 };

export type ConcentrationSection =
  | { kind: 'hidden' }
  | { kind: 'note'; text: string }
  | { kind: 'rows'; rows: ConcentrationRow[] };

/** What the concentration section shows. A capped stall read withholds the whole comparison, since its counts would be too low. */
export function concentrationSection(report: Pick<PatternReport, 'concentration' | 'truncated' | 'stallsTruncated'>): ConcentrationSection {
  if (!report.concentration) return { kind: 'hidden' };
  if (stallsCapped(report)) return { kind: 'note', text: notObservedGap('stall rows were capped, so the comparison was not made') };
  const rows = report.concentration.map(concentrationRow).sort((a, b) => READING_ORDER[a.reading] - READING_ORDER[b.reading] || b.stallN - a.stallN || a.key.localeCompare(b.key));
  return rows.length ? { kind: 'rows', rows } : { kind: 'note', text: PATTERNS_TEXT.concNone };
}

// ---- B12r: the same operation started again while the first was running ----

export type RepeatKind = 'same' | 'different' | 'mixed';
export interface RepeatRow { key: string; episode: number; kind: RepeatKind; kindText: string; repeats: number; longestChain: number; line: string }

/**
 * Whether the repeats carried the same request size. A true or a count equal to the repeats is the same request.
 * A count of zero, or false, is a different request. A count in between is a mix, and says so.
 */
export function repeatKind(sameSize: boolean | number, repeats: number): RepeatKind {
  if (typeof sameSize === 'boolean') return sameSize ? 'same' : 'different';
  if (!Number.isFinite(sameSize) || sameSize <= 0) return 'different';
  return sameSize >= repeats ? 'same' : 'mixed';
}

const repeatKindText = (kind: RepeatKind): string => kind === 'same' ? PATTERNS_TEXT.repeatsSameRequest : kind === 'different' ? PATTERNS_TEXT.repeatsDifferentRequest : PATTERNS_TEXT.repeatsMixedRequest;

export function repeatRow(row: PatternRepeat, index = 0): RepeatRow {
  const repeats = Math.max(0, Math.floor(row.repeats) || 0);
  const longestChain = Math.max(0, Math.floor(row.longestChain) || 0);
  const kind = repeatKind(row.sameSize, repeats);
  const times = `${repeats} ${repeats === 1 ? 'time' : 'times'}`;
  const chain = longestChain > 1 ? ` The longest run of starts was ${longestChain}.` : '';
  return {
    key: `repeat-${row.episode}-${row.userId}-${row.operation}-${index}`, episode: row.episode, kind, kindText: repeatKindText(kind), repeats, longestChain,
    line: `User ${row.userId} started ${row.operation} again ${times} while an earlier one was still running.${chain}`
  };
}

/** The repeats that belong to one episode, most repeated first. */
export function repeatsOf(report: Pick<PatternReport, 'repeats'>, episode: Pick<PatternEpisode, 'index'>): RepeatRow[] {
  return (report.repeats ?? []).map((row, i) => ({ row, i })).filter(({ row }) => row.episode === episode.index)
    .map(({ row, i }) => repeatRow(row, i)).sort((a, b) => b.repeats - a.repeats || a.key.localeCompare(b.key));
}

/**
 * One line for the summary: how many cases there were and in how many episodes, or the standard absence line.
 * Null when the server did not send the check at all.
 */
export function repeatsSummary(report: Pick<PatternReport, 'repeats'>): string | null {
  if (!report.repeats) return null;
  if (report.repeats.length === 0) return PATTERNS_TEXT.repeatsNone;
  const cases = report.repeats.length;
  const episodes = new Set(report.repeats.map((row) => row.episode)).size;
  return `${cases} ${cases === 1 ? 'case' : 'cases'} of an operation started again while it was running, in ${episodes} ${episodes === 1 ? 'episode' : 'episodes'}.`;
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
