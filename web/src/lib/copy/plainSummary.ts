/**
 * Plain-language results (roadmap §8.14 part 5, P14a). Each builder turns data the UI already holds into
 * `{ summary, detail }`: a one-line summary in everyday words, and the technical detail one click deeper.
 *
 * Rules every builder follows (roadmap §6.6 and the §8.14 writing rules):
 *  - The summary states what was MEASURED, never a cause. No causal verbs, no blame.
 *  - Numbers carry a unit and the window they cover ("about 28 seconds, on 24 Oct, 14:45–14:50 UTC").
 *  - Evidence strength uses only the labels in `EvidenceLabel` (a plain measurement is a Symptom).
 *  - `detail` ends with a "What this does not mean" sentence wherever a reader could over-read.
 *  - Absence and linkage sentences are built by calling `./wordings`, never paraphrased.
 *
 * Which existing field feeds each builder (nothing here fetches data; callers pass these values in):
 *  - plainAuditCreateSummary   <- RequestRecord.auditCreateTimeMs (lib/types.ts).
 *                                 AuditCreateTime = CreatedOn - RequestInitiatedAt (docs/api-audit-log-schema.md): it includes driver,
 *                                 connection pool and network time, not only the database.
 *                                 The `window` is the audit-create interval itself: initiatedAt -> createdOn, NOT initiatedAt -> completedAt
 *                                 (the action runs after createdOn). The caller must pass those two timestamps.
 *  - plainTypicalVsSlowest     <- Comparison (lib/types.ts): current.p50, current.p95, current.count, reference.p50, reference.p95,
 *                                 reference.count, state, optional p95Ratio; windows from Query.start/end and Query.baselineStart/baselineEnd;
 *                                 metric from Query.metric.
 *  - plainLowSample            <- Stats.count (valid timings) and Stats.events (all documents) of one cohort, or Rollup.completed and
 *                                 Rollup.events; Comparison.state === 'insufficient_data' is the same condition. Floors mirror
 *                                 percentileFloors in lib/volume.ts (20 for P95, 100 for P99).
 *  - plainIncomplete           <- Rollup.completed / incomplete / pending / invalid / events (or Stats.count / incomplete / pending /
 *                                 invalid / events) and the Query window.
 *
 * Readability: every summary and detail is written to pass the copy lint's reading-level limit (Flesch-Kincaid grade 8.5), with short
 * sentences and everyday words. The exact §6.6 wordings are the only sentences exempt. The copy lint sweep in the tests checks this.
 *
 * This file imports only ./wordings (and erased types) so it stays loadable on its own in the pure tests.
 */
import { auditCreate, notObservedGap, notObservedThreshold, overlapAmbiguous, overlapUnique, plainAuditCreate } from './wordings';
import type { EvidenceLabel } from './types';
import type { Comparison, Metric } from '../types';

export interface PlainSummary {
  /** One line in everyday words: what was measured, with unit and window. */
  summary: string;
  /** The technical detail, shown one click deeper. Ends with "What this does not mean" where a reader could over-read. */
  detail: string;
}

/** A period as ISO timestamps (Query.start/end or Query.baselineStart/baselineEnd, or one request's start and end). */
export interface TimeWindow { start?: string | null; end?: string | null }

/** The evidence label for a bare measurement: it shows that something happened, not why. */
const measurementLabel: EvidenceLabel = 'Symptom';

const finite = (value: number | null | undefined): value is number => value != null && Number.isFinite(value);
const MONTHS = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];
const two = (value: number) => String(value).padStart(2, '0');
const count = (value: number) => value.toLocaleString('en-US');
const requests = (value: number) => value === 1 ? '1 request' : count(value) + ' requests';

/** Rounds to a few significant digits so a number reads naturally ("28", "2.4", "0.02"). */
const rounded = (value: number): number => {
  if (value >= 10) return Math.round(value);
  if (value >= 1) return Number(value.toFixed(1));
  return Number(value.toFixed(2));
};

/** A window in UTC for a sentence: "on 24 Oct, 14:45–14:50 UTC", or a from/to form when it crosses midnight. Empty when it cannot be stated. */
export function windowText(window: TimeWindow | null | undefined): string {
  const start = window?.start ? new Date(window.start) : null;
  const end = window?.end ? new Date(window.end) : null;
  if (!start || !Number.isFinite(start.getTime())) return '';
  const day = (date: Date) => date.getUTCDate() + ' ' + MONTHS[date.getUTCMonth()];
  const withSeconds = (date: Date | null) => !!date && Number.isFinite(date.getTime()) && (date.getUTCSeconds() !== 0 || date.getUTCMilliseconds() !== 0);
  const seconds = withSeconds(start) || withSeconds(end);
  const clock = (date: Date) => two(date.getUTCHours()) + ':' + two(date.getUTCMinutes()) + (seconds ? ':' + two(date.getUTCSeconds()) : '');
  if (!end || !Number.isFinite(end.getTime()) || end.getTime() <= start.getTime()) return 'on ' + day(start) + ', ' + clock(start) + ' UTC';
  if (day(start) === day(end) && start.getUTCFullYear() === end.getUTCFullYear()) return 'on ' + day(start) + ', ' + clock(start) + '–' + clock(end) + ' UTC';
  return 'from ' + day(start) + ', ' + clock(start) + ' to ' + day(end) + ', ' + clock(end) + ' UTC';
}

/** A duration in words with its unit: "120 milliseconds", "2.4 seconds", "3.5 minutes". */
export function durationWords(ms: number): string {
  if (ms < 1000) return rounded(ms) + ' ' + (rounded(ms) === 1 ? 'millisecond' : 'milliseconds');
  if (ms < 60_000) return rounded(ms / 1000) + ' ' + (rounded(ms / 1000) === 1 ? 'second' : 'seconds');
  return rounded(ms / 60_000) + ' ' + (rounded(ms / 60_000) === 1 ? 'minute' : 'minutes');
}

const where = (window: TimeWindow | null | undefined) => windowText(window) || 'in the selected period';
const notMeaning = (text: string) => 'What this does not mean: ' + text;
const evidence = (label: EvidenceLabel) => 'Evidence strength: ' + label + '.';
const join = (...parts: Array<string | false | null | undefined>) => parts.filter((part): part is string => !!part).join(' ');

/* ------------------------------------------------------------------ audit create */

/** A join result for the audit interval, when a linked source has been checked (built only with ./wordings). */
export type AuditLinkage =
  | { kind: 'overlap'; source: string; event: string; seconds: number }
  | { kind: 'ambiguous'; candidates: number }
  | { kind: 'notObservedThreshold'; source: string; threshold: string }
  | { kind: 'notObservedGap'; detail: string };

export interface AuditCreateInput {
  /** RequestRecord.auditCreateTimeMs; null or absent for rows without RequestInitiatedAt. */
  auditCreateTimeMs: number | null | undefined;
  /** The audit-create interval: RequestInitiatedAt (start) to CreatedOn (end). Not the whole request. */
  window?: TimeWindow | null;
  /** Optional: what a linked source showed for this interval. Absent means no source was checked. */
  linkage?: AuditLinkage | null;
}

/** Below this the audit create time is reported as "under 5 milliseconds". */
const TINY_MS = 5;

const linkageSentence = (linkage: AuditLinkage): string => {
  switch (linkage.kind) {
    case 'overlap': return overlapUnique(linkage.source, linkage.event, linkage.seconds);
    case 'ambiguous': return overlapAmbiguous(linkage.candidates);
    case 'notObservedThreshold': return notObservedThreshold(linkage.source, linkage.threshold);
    default: return notObservedGap(linkage.detail);
  }
};

/**
 * The time the audit row took to insert before the action ran, as measured from the application side.
 * The window to pass is the audit-create interval, initiatedAt -> createdOn (not initiatedAt -> completedAt).
 * Under 5 milliseconds it says "under 5 milliseconds" (via durationWords) instead of rounding to "0 seconds".
 */
export function plainAuditCreateSummary(input: AuditCreateInput): PlainSummary {
  const ms = input.auditCreateTimeMs;
  const when = windowText(input.window);
  if (!finite(ms) || ms < 0) {
    return {
      summary: 'No audit create time was recorded for this request' + (when ? ' ' + when : '') + '.',
      detail: join(
        'The record has no AuditCreateTime. Older rows that lack RequestInitiatedAt do not carry it.',
        notMeaning('a missing time does not mean the audit step was fast or slow. It means nothing was measured.')
      )
    };
  }
  const tiny = ms < TINY_MS;
  // Under 5 ms the fixed seconds wording would round to "0 seconds"; say it in milliseconds instead.
  const seconds = tiny ? Number((ms / 1000).toFixed(3)) : rounded(ms / 1000);
  const lead = tiny
    ? 'Saving the record took under ' + durationWords(TINY_MS) + ' before the action ran. This was measured from the application side.'
    : plainAuditCreate(seconds);
  return {
    summary: join(lead, when && 'That was ' + when + '.'),
    detail: join(
      auditCreate(seconds),
      'That time covers the application driver, the connection pool and the network, as well as the database server.',
      'It starts when the request begins and ends when the audit record is saved.',
      'Time in the browser, and on the way to the application, is not included.',
      input.linkage ? linkageSentence(input.linkage) : '',
      evidence(measurementLabel),
      notMeaning('it does not show where the time went. The database may or may not have played a part, and this number alone cannot say.')
    )
  };
}

/* ------------------------------------------------------------------ typical vs slowest */

export interface TypicalVsSlowestInput {
  comparison: Pick<Comparison, 'controller' | 'action' | 'state' | 'current' | 'reference'> & Partial<Pick<Comparison, 'p95Ratio' | 'medianRatio'>>;
  /** Query.start / Query.end. */
  window: TimeWindow;
  /** Query.baselineStart / Query.baselineEnd. */
  referenceWindow?: TimeWindow | null;
  /** Query.metric: total time (audit create plus API time) or API time only. Defaults to total. */
  metric?: Metric;
}

const ratioWords = (ratio: number) => rounded(ratio) + ' times';

/** Typical (P50) and slowest 1 in 20 (P95) request times, with the measured change against the earlier period. */
export function plainTypicalVsSlowest(input: TypicalVsSlowestInput): PlainSummary {
  const { comparison, metric = 'total' } = input;
  const name = [comparison.controller, comparison.action].filter(Boolean).join('.') || 'These requests';
  const now = where(input.window);
  const before = windowText(input.referenceWindow);
  const p50 = comparison.current.p50?.valueMs, p95 = comparison.current.p95?.valueMs;
  const nowP50 = finite(p50) && p50 >= 0 ? p50 : null, nowP95 = finite(p95) && p95 >= 0 ? p95 : null;
  const n = comparison.current.count;
  const timeKind = metric === 'api' ? 'Times are API processing time only.' : 'Times are the total time: audit create plus API time.';

  if (!(n > 0) || (nowP50 == null && nowP95 == null)) {
    return {
      summary: name + ': no requests with a usable timing were found ' + now + '.',
      detail: join(
        'Requests with no usable timing are left out of typical and slowest times.',
        notMeaning('having no timings does not mean there were no requests, or that they were fast. Check the unfinished count.')
      )
    };
  }

  const lead = join(
    name + ', ' + now + ':' + (nowP50 != null ? ' the typical request took about ' + durationWords(nowP50) + '.' : ''),
    nowP95 != null && (nowP50 != null ? 'The slowest 1 in 20 took' : 'the slowest 1 in 20 took') + ' about ' + durationWords(nowP95) + ' or longer.'
  );

  // The change is only stated when the comparison has enough timings in both periods (state 'compared').
  const refP50Raw = comparison.reference.p50?.valueMs, refP95Raw = comparison.reference.p95?.valueMs;
  const refP50 = finite(refP50Raw) && refP50Raw >= 0 ? refP50Raw : null, refP95 = finite(refP95Raw) && refP95Raw > 0 ? refP95Raw : null;
  const then = before ? ' (' + before.replace(/^on /, '') + ')' : '';
  let change = '';
  let ratioLine = '';
  if (comparison.state === 'compared' && nowP95 != null && refP95 != null) {
    const delta = nowP95 - refP95;
    const ratio = finite(comparison.p95Ratio) ? comparison.p95Ratio : nowP95 / refP95;
    if (Math.abs(delta) / refP95 < 0.05) change = 'That slowest time was about the same as in the earlier period' + then + '.';
    else change = 'That slowest time was about ' + durationWords(Math.abs(delta)) + (delta > 0 ? ' longer' : ' shorter') + ' than in the earlier period' + then + '.';
    ratioLine = 'The slowest 1 in 20 (P95) is about ' + ratioWords(ratio) + ' the earlier P95.';
  } else if (comparison.state === 'insufficient_data') change = 'There were too few timed requests in one period to state a change.';
  else if (comparison.state === 'missing_reference') change = 'No timed requests were found in the earlier period, so no change is stated.';
  else if (comparison.state === 'undefined_baseline') change = 'The earlier time was too close to zero to state a change.';

  const refCount = comparison.reference.count;
  return {
    summary: join(lead, change),
    detail: join(
      'P50 (the typical request, half were quicker): ' + (nowP50 != null ? 'about ' + durationWords(nowP50) : 'not available') +
        (refP50 != null ? ', earlier about ' + durationWords(refP50) : '') + '.',
      'P95 (95 of every 100 requests were quicker): ' + (nowP95 != null ? 'about ' + durationWords(nowP95) : 'not available, it needs at least 20 timed requests') +
        (refP95 != null ? ', earlier about ' + durationWords(refP95) : '') + '.',
      ratioLine,
      'Based on ' + requests(n) + ' ' + now + '.',
      finite(refCount) && refCount > 0 && 'The earlier period had ' + requests(refCount) + (before ? ' ' + before : '') + '.',
      'Values are histogram estimates, so the real figures can differ a little.',
      timeKind,
      evidence(measurementLabel),
      notMeaning('it shows how long requests took, not why. A slower period can have many reasons, and this change alone does not name one.')
    )
  };
}

/* ------------------------------------------------------------------ low sample / coverage */

/** Which percentile the reader is about to trust; each has its own minimum number of timed requests. */
export type SampleLevel = 'p95' | 'p99';
/** Minimum timed requests per percentile. Mirrors percentileFloors in lib/volume.ts. */
export const sampleFloors: Record<SampleLevel, number> = { p95: 20, p99: 100 };

export interface LowSampleInput {
  /** Stats.count (or Rollup.completed): requests with a usable timing. */
  timed: number | null | undefined;
  /** Stats.events (or Rollup.events): every request in the group, including unfinished and unusable ones. */
  events?: number | null;
  window?: TimeWindow | null;
  /** The percentile that needs the sample. Defaults to p95. */
  level?: SampleLevel;
}

/** How many requests the numbers rest on, and whether that is enough to read much into them. */
export function plainLowSample(input: LowSampleInput): PlainSummary {
  const timed = finite(input.timed) && input.timed > 0 ? Math.floor(input.timed) : 0;
  const events = finite(input.events) && input.events >= timed ? Math.floor(input.events) : null;
  const level = input.level ?? 'p95';
  const floor = sampleFloors[level];
  const when = where(input.window);
  const floors = 'The app shows the slowest 1 in 20 only with at least ' + sampleFloors.p95 + ' timed requests. It shows the slowest 1 in 100 only with at least ' + sampleFloors.p99 + '.';
  const partial = events != null && events > timed;
  const coverage = partial ? 'Only ' + count(timed) + ' of ' + requests(events) + ' had a usable timing' : '';

  if (timed === 0) {
    return {
      summary: 'No requests with a usable timing were found ' + when + (events ? ', out of ' + requests(events) : '') + '.',
      detail: join(
        'There is nothing to take a typical or slowest time from.',
        floors,
        notMeaning('having no timings does not mean the system is fine or broken. It means there is nothing to measure.')
      )
    };
  }
  if (timed < floor) {
    return {
      summary: (partial ? coverage + ' ' + when + '.' : 'Only ' + requests(timed) + ' had a usable timing ' + when + '.') + ' Do not read much into these numbers.',
      detail: join(
        'With ' + requests(timed) + ' timed, one or two slow requests can move the numbers a lot.',
        floors,
        partial ? 'Requests with no usable timing are left out of the times.' : '',
        notMeaning('a small count does not mean things are fine, and it does not mean they are broken. It means there is too little to say.')
      )
    };
  }
  return {
    summary: (partial ? coverage + ' ' + when + '.' : requests(timed) + ' had a usable timing ' + when + '.') + ' That is enough to give ' + (level === 'p99' ? 'the slowest 1 in 100' : 'a typical and a slowest') + ' time, as an estimate.',
    detail: join(
      floors,
      partial ? 'Requests with no usable timing are left out of the times.' : '',
      notMeaning('enough requests make the numbers steadier. They do not explain a slow time. They do not show that a change will last.')
    )
  };
}

/* ------------------------------------------------------------------ incomplete / pending */

export interface IncompleteInput {
  /** Rollup.completed or Stats.count: requests with a final result and usable timing. */
  completed: number | null | undefined;
  /** Rollup.incomplete or Stats.incomplete: unfinished requests, not counting pending ones. */
  incomplete?: number | null;
  /** Rollup.pending or Stats.pending: asynchronous requests that were accepted and have no final result yet. */
  pending?: number | null;
  /** Rollup.invalid or Stats.invalid: requests whose timing could not be used. Never part of the share. */
  invalid?: number | null;
  window?: TimeWindow | null;
}

/**
 * A share of requests as text. A share below 100% is never rounded up to "100%" (499 of 500 is "over 99%"),
 * and a share above zero is never shown as "0%" ("under 1%").
 */
export function shareText(part: number, total: number): string {
  if (!(total > 0) || !(part > 0)) return '0%';
  const pct = part / total * 100;
  if (pct >= 100) return '100%';
  if (pct < 1) return 'under 1%';
  if (pct >= 99) return 'over 99%';
  return rounded(pct) + '%';
}

const whole = (value: number | null | undefined) => finite(value) && value > 0 ? Math.floor(value) : 0;

/** Requests that had no final result when the period was read, split into unfinished and accepted-but-pending. */
export function plainIncomplete(input: IncompleteInput): PlainSummary {
  const completed = whole(input.completed), incomplete = whole(input.incomplete), pending = whole(input.pending), invalid = whole(input.invalid);
  const unfinished = incomplete + pending;
  const base = completed + unfinished;
  const when = where(input.window);
  const unusable = invalid
    ? ' ' + (invalid === 1 ? '1 more request had a timing that could not be used. It is' : count(invalid) + ' more requests had a timing that could not be used. They are') + ' not counted here.'
    : '';

  if (base === 0 && invalid === 0) {
    return {
      summary: 'No requests were found ' + when + '.',
      detail: notMeaning('an empty period does not show whether the system was working. It means no requests are in this search.')
    };
  }
  if (unfinished === 0) {
    return {
      summary: (base > 0 ? 'All ' + requests(base) + ' had a final result ' + when + '.' : 'No requests with a final result were found ' + when + '.') + unusable,
      detail: join(
        'Completed: ' + count(completed) + '. Unfinished: 0. Pending: 0.',
        notMeaning('having every request finished does not mean every request was quick or successful. Look at the times and errors as well.')
      )
    };
  }
  const pieces = [
    incomplete > 0 && count(incomplete) + ' had no recorded finish',
    pending > 0 && count(pending) + ' ' + (pending === 1 ? 'was accepted but has' : 'were accepted but have') + ' no finished record yet'
  ].filter(Boolean).join(' and ');
  return {
    summary: count(unfinished) + ' of ' + requests(base) + ' (' + shareText(unfinished, base) + ') had no final result ' + when + '. Of these, ' + pieces + '.' + unusable,
    detail: join(
      'Completed: ' + count(completed) + '. Incomplete (no recorded finish): ' + count(incomplete) + '. Pending (async, accepted, no final result yet): ' + count(pending) + '.',
      'The share is unfinished requests divided by completed plus unfinished. Unusable requests are not in it.',
      'Unfinished requests are left out of the typical and slowest times. They are never counted as fast or successful.',
      pending > 0 && 'A pending request was accepted, and its work may still be running.',
      evidence(measurementLabel),
      notMeaning('an unfinished request is not a failed request. It may still be running, or its finish may not have been saved. It does not show an outage.')
    )
  };
}
