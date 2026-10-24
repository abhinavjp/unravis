import { auditCreate, notObservedGap } from './copy/wordings';
import { MIN_SAMPLE } from './thresholds';
import { bucketForWindows } from './time';
import type {
  AuditSeriesDay, EndpointDailyDay, EvidenceLabel, PatternAuditStall, PatternConcentration, PatternCoverage, PatternEpisode, PatternInput, PatternOnsetMinute,
  PatternIdleBand, PatternIdleGaps, PatternRepeat, PatternReport, PatternStall, PatternVolley, Query, StallBand, StepChange, SuggestedSplit
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
export function windowInputSeconds(query: Pick<Query, 'sourceIds' | 'start' | 'end'>, stallSec = '', gapSec = '', idleGaps = false, extras: PatternExtras = {}): { input: PatternInput } | { error: string } {
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
  const built = windowInput(query, stallRaw, gapRaw);
  // The quiet-gap check reads every request in the window, so it is sent only when asked for.
  if ('input' in built && idleGaps) built.input.idleGaps = true;
  if ('input' in built) {
    const failed = applyExtras(built.input, query, extras);
    if (failed) return failed;
  }
  return built;
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

// ---- B6g: audit create after quiet gaps ----

/** The lift is read only when both groups have at least this many requests (the same floor as everywhere else). */
export const IDLE_MIN_ROWS = MIN_SAMPLE;
/** Raised: a lift of at least 5. Flat: a lift of at most 1.5 with at least 50 requests in both groups. */
export const IDLE_RAISED_LIFT = 5;
export const IDLE_FLAT_LIFT = 1.5;
export const IDLE_FLAT_MIN_ROWS = 50;

const gapWords = (seconds: number): string => seconds < 60 ? `${seconds} s` : `${Number((seconds / 60).toFixed(1))} min`;

/** The name of a band from its limits: "Under 30 s", "1 to 4 min", "10 min or more". An open end is a null or zero `toS`. */
export function bandLabel(band: Pick<PatternIdleBand, 'fromS' | 'toS'>): string {
  const open = band.toS === null || band.toS === undefined || band.toS <= 0;
  if (open) return `${gapWords(band.fromS)} or more`;
  if (!(band.fromS > 0)) return `Under ${gapWords(band.toS as number)}`;
  const to = band.toS as number;
  // Two minute values share one unit: "1 to 4 min", not "1 min to 4 min".
  if (band.fromS >= 60 && to >= 60) return `${Number((band.fromS / 60).toFixed(1))} to ${gapWords(to)}`;
  return `${gapWords(band.fromS)} to ${gapWords(to)}`;
}

export interface IdleBandRow { key: string; label: string; n: number; thin: boolean; median: string; slowest: string; slowRate: string }

const dash = '–';
const msCell = (ms: number | null | undefined, thin: boolean): string => thin || ms === null || ms === undefined || !Number.isFinite(ms) ? dash : secondsLabel(ms);
const rateCell = (rate: number | null | undefined, thin: boolean): string => thin || rate === null || rate === undefined || !Number.isFinite(rate) ? dash : shareText(rate);

/** One row for each band. A band under 20 requests shows its count and no statistics. */
export function idleBandRows(bands: ReadonlyArray<PatternIdleBand> | undefined): IdleBandRow[] {
  return (bands ?? []).map((band) => {
    const thin = !(band.n >= MIN_SAMPLE);
    return { key: `band-${band.fromS}`, label: bandLabel(band), n: band.n, thin, median: msCell(band.medianMs, thin), slowest: msCell(band.p95Ms, thin), slowRate: rateCell(band.slowRate, thin) };
  });
}

export type IdleReading = 'raised' | 'flat' | 'between' | 'withheld';
export interface IdleLiftRow { key: string; gS: number; reading: IdleReading; before: number; after: number; line: string }

const isPartial = (partial: boolean | string | undefined): boolean => !!partial;

/** Requests with a gap under `gS` and at or above it, summed from the bands. A band that straddles `gS` cannot be split, so it counts on neither side. */
export function idleGroups(bands: ReadonlyArray<PatternIdleBand>, gS: number): { below: number; atOrAbove: number } {
  let below = 0, atOrAbove = 0;
  for (const band of bands) {
    const open = band.toS === null || band.toS === undefined || band.toS <= 0;
    if (band.fromS >= gS) atOrAbove += band.n;
    else if (!open && (band.toS as number) <= gS) below += band.n;
  }
  return { below, atOrAbove };
}

/** One line for each gap limit. Withheld when the read was partial, the server withheld it, or either group is under 20 requests. */
export function idleLiftRows(idle: Pick<PatternIdleGaps, 'bands' | 'lift' | 'partial'>): IdleLiftRow[] {
  return (idle.lift ?? []).map((lift) => {
    const where = gapWords(lift.gS);
    const { below, atOrAbove } = idleGroups(idle.bands ?? [], lift.gS);
    const row = (reading: IdleReading, line: string): IdleLiftRow => ({ key: `lift-${lift.gS}`, gS: lift.gS, reading, before: below, after: atOrAbove, line });
    if (isPartial(idle.partial)) return row('withheld', `${where}: ${PATTERNS_TEXT.idlePartial}`);
    const reason = typeof lift.withheld === 'string' ? lift.withheld.trim() : '';
    if (lift.withheld || lift.value === null || lift.value === undefined || !Number.isFinite(lift.value)) return row('withheld', reason ? `${where}: not compared (${reason}).` : `${where}: not compared.`);
    if (below < IDLE_MIN_ROWS || atOrAbove < IDLE_MIN_ROWS) return row('withheld', `${where}: too few requests to compare (fewer than ${IDLE_MIN_ROWS}).`);
    const times = timesText(lift.value);
    const line = `After a quiet gap of ${where} or more, slow audit creates were ${times} as common as after a shorter gap.`;
    if (lift.value >= IDLE_RAISED_LIFT) return row('raised', line);
    if (lift.value <= IDLE_FLAT_LIFT && below >= IDLE_FLAT_MIN_ROWS && atOrAbove >= IDLE_FLAT_MIN_ROWS) return row('flat', `${line} That is about the same, so quiet gaps do not stand out.`);
    return row('between', line);
  });
}

export type IdleSection =
  | { kind: 'hidden' }
  | { kind: 'note'; text: string }
  | { kind: 'rows'; bands: IdleBandRow[]; lifts: IdleLiftRow[]; excluded: string; partial: string; raised: boolean };

/** One sentence on requests left out of the check. Empty when none were. */
export const excludedLine = (count: number): string =>
  count > 0 ? `${count} Hangfire ${count === 1 ? 'request was' : 'requests were'} left out of this check.` : '';

/**
 * What the quiet-gap section shows. Hidden unless it was asked for or the server sent it. Asked for and not sent gives the standard absence line.
 * A capped read of every request withholds the lifts. The bands still show, with the partial note.
 */
export function idleSection(report: Pick<PatternReport, 'idleGaps'>, asked: boolean): IdleSection {
  const idle = report.idleGaps;
  if (!idle) return asked ? { kind: 'note', text: notObservedGap('the server did not send this check') } : { kind: 'hidden' };
  const bands = idleBandRows(idle.bands);
  if (bands.length === 0) return { kind: 'note', text: notObservedGap('no audit create requests were read for this check') };
  const lifts = idleLiftRows(idle);
  return {
    kind: 'rows', bands, lifts, excluded: excludedLine(idle.excludedHangfire), partial: isPartial(idle.partial) ? (typeof idle.partial === 'string' && idle.partial.trim() ? `${PATTERNS_TEXT.idlePartial} ${idle.partial.trim()}` : PATTERNS_TEXT.idlePartial) : '',
    raised: lifts.some((l) => l.reading === 'raised')
  };
}

// ---- B9 (dating a step), the audit-create series and the fixed-duration check ----

/** The longest span B9 reads, in whole local days. It is its own window, apart from the range chips. */
export const MAX_STEP_DAYS = 120;
export const DEFAULT_STEP_DAYS = 90;
export const STEP_DAY_CHOICES = [30, 60, 90, 120] as const;
/** The fewest timed requests a day needs. Mirrors the server. */
export const DAY_MIN_REQUESTS = 20;

export const STEP_TEXT = {
  stepOptIn: 'Also look for a step in daily response times',
  stepOptInNote: 'This reads the log one day at a time, so it takes longer. It does not run on a live source.',
  stepDaysLabel: 'Days to read',
  seriesOptIn: 'Also show audit create by day and look for a fixed delay',
  seriesOptInNote: 'This reads every request in the window, so it takes longer.',
  stepTitle: 'Step in daily response times',
  stepIntro: 'This reads the 10 busiest operations one whole day at a time. For each one it looks for a single date after which the slowest 1 in 20 response times were at least 2 times as long, or as short. A date is reported only when 3 or more operations share it, give or take a day.',
  stepNotRun: 'this check did not run, see the notes in the summary',
  stepCaveat: 'This dates a change in the numbers. It does not explain the change. Each daily figure is accurate to about 5%.',
  stepColOperation: 'Operation',
  stepColDays: 'Days',
  stepColBefore: 'Before',
  stepColAfter: 'After',
  stepColChange: 'Change',
  stepColResult: 'Result',
  stepShared: 'Shares the date',
  stepOwn: 'Step on its own date',
  stepNone: 'No step',
  splitTitle: 'Compare before and after this date',
  splitOffer: 'Suggested split',
  splitAsk: 'Compare the days before and after this date on the page',
  splitConfirm: 'The page will compare these two ranges. Your current range will change.',
  splitUse: 'Use this split',
  splitCancel: 'Keep my range',
  splitDone: 'The page is now set to compare these two ranges.',
  splitBad: 'These two ranges cannot be used by the page.',
  splitBefore: 'Before',
  splitAfter: 'After',
  splitNote: 'The date is a suggestion. You decide whether the split makes sense.',
  seriesTitle: 'Audit create by day',
  seriesIntro: 'For each whole day, this shows how long audit create took, and what share of the total request time it was.',
  seriesNote: 'Audit create is measured from the client side and covers setup work and the database write, not just the write.',
  seriesShareNote: 'Share of request time is the audit create time added up, divided by the total request time added up, for that day.',
  seriesPartial: 'Partial read. These counts are lower bounds.',
  seriesNone: 'no days with audit create times were read',
  seriesColDay: 'Day',
  seriesColRequests: 'Requests',
  seriesColMedian: 'Median',
  seriesColSlowest: 'Slowest 1 in 20',
  seriesColShare: 'Share of request time',
  seriesPartDay: 'part day',
  bandTitle: 'Delays of about the same length',
  bandNone: 'No band of equal delays stood out.',
  dailyTitle: 'Daily response times',
  dailyIntro: 'One row for each whole day. Days with fewer than 20 timed requests show a dash instead of a time.',
  dailyOpen: 'Daily view',
  dailyClose: 'Close daily view',
  dailyLoading: 'Reading the log one day at a time…',
  dailyFailed: 'The daily view did not finish. Try again.',
  dailyEmpty: 'No days in this range.',
  dailyDays: 'Days',
  dailyFew: 'few requests',
  dailyPart: 'part day',
  dailyColDay: 'Day',
  dailyColRequests: 'Requests',
  dailyColMedian: 'Median',
  dailyColSlowest: 'Slowest 1 in 20',
  dailyChart: 'Slowest 1 in 20 by day'
} as const;

/** The days the daily view offers. A live source is limited to 14 by the server and says so. */
export const DAILY_DAY_CHOICES = [14, 30, 60, 120] as const;
export const DEFAULT_DAILY_DAYS = 14;

/** The window B9 reads: the last `days` days up to the end of the page's range. */
export function stepWindow(query: Pick<Query, 'end'>, days: number): { start: string; end: string } | null {
  const end = Date.parse(query.end);
  if (!Number.isFinite(end) || !Number.isInteger(days) || days < 1 || days > MAX_STEP_DAYS) return null;
  return { start: new Date(end - days * MS_PER_DAY).toISOString(), end: new Date(end).toISOString() };
}

export interface PatternExtras { stepDays?: number; auditSeries?: boolean; zone?: string }

/** The opt-in fields B9 and the audit-create series add to a request. Nothing is added unless one is asked for. */
export function applyExtras(input: PatternInput, query: Pick<Query, 'end'>, extras: PatternExtras): { error: string } | null {
  const days = extras.stepDays ?? 0;
  if (days > 0) {
    const window = stepWindow(query, days);
    if (!window) return { error: `Days to read must be a whole number from 1 to ${MAX_STEP_DAYS}.` };
    input.stepChange = window;
  }
  if (extras.auditSeries) input.auditSeries = true;
  if ((days > 0 || extras.auditSeries) && extras.zone) input.timeZone = extras.zone;
  return null;
}

const dashText = '–';
const secondsCell = (ms: number | null | undefined): string => ms === null || ms === undefined || !Number.isFinite(ms) ? dashText : secondsLabel(ms);

/** A p95 or median in the unit a person reads: "0.3 s", or a dash for a day with no figure. */
export const dayTimeCell = secondsCell;

export interface StepRow {
  key: string; operation: string; volume: number; days: number; found: boolean; shares: boolean; date: string;
  before: string; after: string; change: string; result: string; reason: string;
}

const ratioText = (ratio: number | undefined, before: number | undefined, after: number | undefined): string => {
  if (ratio === undefined || !Number.isFinite(ratio) || ratio <= 0) return dashText;
  const word = timesText(ratio);
  return before !== undefined && after !== undefined && after < before ? `${word} shorter` : `${word} longer`;
};

/** One row for each operation. Operations with a step come first, busiest first. */
export function stepRows(step: Pick<StepChange, 'operations' | 'sharing'>): StepRow[] {
  const sharing = new Set(step.sharing ?? []);
  return [...step.operations].map((op) => {
    const shares = op.found && sharing.has(op.operation);
    return {
      key: 'step-' + op.operation, operation: op.operation, volume: op.volume, days: op.daysUsed, found: op.found, shares, date: op.found ? (op.date ?? '') : '',
      before: op.found ? secondsCell(op.beforeMs) : dashText, after: op.found ? secondsCell(op.afterMs) : dashText,
      change: op.found ? ratioText(op.ratio, op.beforeMs, op.afterMs) : dashText,
      result: shares ? STEP_TEXT.stepShared : op.found ? STEP_TEXT.stepOwn : STEP_TEXT.stepNone,
      reason: op.found ? '' : (op.reason ?? '').trim()
    };
  }).sort((a, b) => Number(b.found) - Number(a.found) || b.volume - a.volume || a.operation.localeCompare(b.operation));
}

/**
 * The query fields for a suggested split, or null when the two ranges cannot be used by the page.
 * The page's own rules apply: the earlier range ends where the later one starts or before, and neither is over 7 days.
 */
export type SplitPatch = Pick<Query, 'start' | 'end' | 'baselineStart' | 'baselineEnd' | 'bucketSeconds'>;
export function splitScope(split: Pick<SuggestedSplit, 'baselineStart' | 'baselineEnd' | 'start' | 'end'> | undefined | null): SplitPatch | null {
  if (!split) return null;
  const baselineStart = Date.parse(split.baselineStart), baselineEnd = Date.parse(split.baselineEnd), start = Date.parse(split.start), end = Date.parse(split.end);
  if (![baselineStart, baselineEnd, start, end].every(Number.isFinite)) return null;
  if (!(baselineStart < baselineEnd && baselineEnd <= start && start < end)) return null;
  if (end - start > 7 * MS_PER_DAY || baselineEnd - baselineStart > 7 * MS_PER_DAY) return null;
  return {
    start: new Date(start).toISOString(), end: new Date(end).toISOString(), baselineStart: new Date(baselineStart).toISOString(), baselineEnd: new Date(baselineEnd).toISOString(),
    bucketSeconds: bucketForWindows(new Date(start).toISOString(), new Date(end).toISOString(), new Date(baselineStart).toISOString(), new Date(baselineEnd).toISOString())
  };
}

/** One sentence on days B9 could not use. Empty when none. */
export const stepDaysLine = (step: Pick<StepChange, 'daysRead' | 'daysLeftOut'>): string => {
  const read = `${step.daysRead} whole ${step.daysRead === 1 ? 'day was' : 'days were'} read.`;
  return step.daysLeftOut > 0 ? `${read} ${step.daysLeftOut} read only in part and left out.` : read;
};

export type StepSection =
  | { kind: 'hidden' }
  | { kind: 'note'; text: string }
  | { kind: 'rows'; rows: StepRow[]; date: string; daysLine: string; split: SuggestedSplit | null; scope: ReturnType<typeof splitScope> };

/**
 * What the step section shows. Hidden unless it was asked for. Asked for and not run gives the standard absence line.
 * The split is offered only when a date was shared and the page can use both ranges.
 */
export function stepSection(report: Pick<PatternReport, 'stepChange'>, asked: boolean): StepSection {
  const step = report.stepChange;
  if (!step) return asked ? { kind: 'note', text: notObservedGap(STEP_TEXT.stepNotRun) } : { kind: 'hidden' };
  const scope = step.date ? splitScope(step.split) : null;
  return { kind: 'rows', rows: stepRows(step), date: step.date ?? '', daysLine: stepDaysLine(step), split: scope ? step.split ?? null : null, scope };
}

export interface AuditDayRow { key: string; date: string; n: number; median: string; slowest: string; share: string; partDay: boolean }

/** One row for each day. A day with too few rows, or a part day, shows its count and dashes. */
export function auditDayRows(days: ReadonlyArray<AuditSeriesDay> | undefined): AuditDayRow[] {
  return (days ?? []).map((day) => ({
    key: 'day-' + day.date, date: day.date, n: day.n, median: secondsCell(day.p50Ms), slowest: secondsCell(day.p95Ms),
    share: day.insertShare === null || day.insertShare === undefined || !Number.isFinite(day.insertShare) ? dashText : shareText(day.insertShare), partDay: !!day.partDay
  }));
}

/** The band of equal delays as one line: "5 delays between 26.2 s and 28.9 s, on 2 days (2026-10-18, 2026-10-19)." */
export function bandText(band: StallBand): string {
  const days = band.days.length;
  return `${band.count} ${band.count === 1 ? 'delay' : 'delays'} between ${secondsNumber(band.fromMs)} s and ${secondsNumber(band.toMs)} s, on ${days} ${days === 1 ? 'day' : 'days'} (${band.days.join(', ')}).`;
}

/** One sentence on rows with no audit create time. Empty when none. */
export const withoutAuditLine = (count: number): string =>
  count > 0 ? `${count} ${count === 1 ? 'request has' : 'requests have'} no audit create time and ${count === 1 ? 'was' : 'were'} left out.` : '';

export type AuditSeriesSection =
  | { kind: 'hidden' }
  | { kind: 'note'; text: string }
  | { kind: 'rows'; days: AuditDayRow[]; band: string; excluded: string; without: string; partial: string };

/** What the audit-create series shows. Hidden unless asked for. Asked for and not run gives the standard absence line. */
export function auditSeriesSection(report: Pick<PatternReport, 'auditSeries'>, asked: boolean): AuditSeriesSection {
  const series = report.auditSeries;
  if (!series) return asked ? { kind: 'note', text: notObservedGap(STEP_TEXT.stepNotRun) } : { kind: 'hidden' };
  const days = auditDayRows(series.days);
  if (days.length === 0) return { kind: 'note', text: notObservedGap(STEP_TEXT.seriesNone) };
  const reason = (series.partialReason ?? '').trim();
  return {
    kind: 'rows', days, band: series.band ? bandText(series.band) : STEP_TEXT.bandNone, excluded: excludedLine(series.excludedHangfire), without: withoutAuditLine(series.withoutAudit),
    partial: series.partial ? (reason ? `${STEP_TEXT.seriesPartial} ${reason}.` : STEP_TEXT.seriesPartial) : ''
  };
}

export interface DailyRow { key: string; date: string; count: number; median: string; slowest: string; note: string; slowestMs: number | null }

/** One row for each day of the daily view. A day with few requests or a part day has no time, and says why. */
export function dailyRows(days: ReadonlyArray<EndpointDailyDay> | undefined): DailyRow[] {
  return (days ?? []).map((day) => ({
    key: 'daily-' + day.date, date: day.date, count: day.count, median: secondsCell(day.p50Ms), slowest: secondsCell(day.p95Ms),
    note: day.partial ? STEP_TEXT.dailyPart : day.low ? STEP_TEXT.dailyFew : '', slowestMs: day.p95Ms ?? null
  }));
}

/**
 * Points for the small chart: one x per day, y from 0 (top is the slowest day). A day with no figure leaves a gap, so the line is split into runs.
 * `width` and `height` are the drawing size. Returns the runs of "x,y" pairs.
 */
export function dailyChartRuns(rows: ReadonlyArray<Pick<DailyRow, 'slowestMs'>>, width = 300, height = 56): string[][] {
  const values = rows.map((row) => row.slowestMs).filter((v): v is number => v !== null && Number.isFinite(v));
  if (values.length === 0) return [];
  const top = Math.max(...values) || 1;
  const step = rows.length > 1 ? width / (rows.length - 1) : 0;
  const runs: string[][] = [];
  let run: string[] = [];
  rows.forEach((row, i) => {
    if (row.slowestMs === null || !Number.isFinite(row.slowestMs)) { if (run.length) runs.push(run); run = []; return; }
    const x = rows.length > 1 ? i * step : width / 2;
    run.push(`${Number(x.toFixed(1))},${Number((height - (row.slowestMs / top) * height).toFixed(1))}`);
  });
  if (run.length) runs.push(run);
  return runs;
}

/** The window of the daily view: the last `days` days up to the end of the page's range. */
export function dailyWindow(query: Pick<Query, 'end'>, days: number): { start: string; end: string } | null { return stepWindow(query, days); }

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
