import type { Provenance } from '../types';
import { durationWords } from './plainSummary';

/**
 * Plain-English strings and small pure helpers for the landing screen, the help drawer and the query controls.
 * They live here, not in components, so the copy lint and the tests can read them.
 * Rule: say what was measured, never what caused it, and never sound more certain than the data is.
 */

/* ------------------------------------------------------------------ pulse: P95 against the earlier period */

export interface P95Change {
  /** Current P95 divided by the earlier period's P95. */
  ratio: number;
  /** Short text for under the figure, for example "5.0x the earlier period". */
  text: string;
  /** True when the P95 is more than twice the earlier period's, so the figure is shown in the warning colour. */
  severe: boolean;
}

const SEVERE_RATIO = 2;
const SAME_BAND = 0.05;
const finitePositive = (value: number | null | undefined): value is number => value != null && Number.isFinite(value) && value > 0;
const timesText = (ratio: number) => (ratio >= 10 ? String(Math.round(ratio)) : ratio.toFixed(1)) + 'x';

/** How the current P95 compares with the earlier period's. Null when either value is missing, or the earlier one is zero. */
export function p95Change(currentMs: number | null | undefined, referenceMs: number | null | undefined): P95Change | null {
  if (!finitePositive(referenceMs) || currentMs == null || !Number.isFinite(currentMs) || currentMs < 0) return null;
  const ratio = currentMs / referenceMs;
  if (Math.abs(ratio - 1) <= SAME_BAND) return { ratio, text: 'about the same as the earlier period', severe: false };
  if (ratio > 1) return { ratio, text: timesText(ratio) + ' the earlier period', severe: ratio > SEVERE_RATIO };
  return { ratio, text: timesText(ratio) + ' the earlier period', severe: false };
}

/**
 * One sentence under the figures when the P95 clearly rose. It states the comparison and points to the ranked view;
 * it names the operation with the biggest rise when one is passed, and never gives a reason.
 */
export function p95Sentence(change: P95Change | null, referenceMs: number | null | undefined, topName: string = ''): string {
  if (!change || !change.severe) return '';
  const period = finitePositive(referenceMs) ? 'the previous ' + durationWords(referenceMs) : 'the earlier period';
  const ratio = change.ratio >= 10 ? Math.round(change.ratio) : Number(change.ratio.toFixed(1));
  const named = topName ? ' The biggest rise is in ' + topName + '.' : '';
  return 'P95 is about ' + ratio + ' times what it was in ' + period + '.' + named + ' Open "Where is it?" to see which operations changed most.';
}

/** The length of the earlier period in milliseconds, or null when its ends are not readable. */
export function periodLengthMs(start: string | null | undefined, end: string | null | undefined): number | null {
  const from = start ? Date.parse(start) : NaN;
  const to = end ? Date.parse(end) : NaN;
  return Number.isFinite(from) && Number.isFinite(to) && to > from ? to - from : null;
}

/** Notes the zoom strip draws itself. The strip's other wording lives in zoomCopy.ts. */
export const ZOOM_NOTES = {
  tinyDrag: 'That drag was too short to change the window. Drag across more of the strip, or use Zoom in.',
  emptyForApi: 'No usable timings for this API in this zoomed window.',
  emptyForApiMore: 'Other APIs may have data here. Zoom out, or choose another API above.'
} as const;

/* ------------------------------------------------------------------ coverage: tenant line */

export const TENANT_TEXT = {
  available: 'We can tell which customer these requests belong to in this source.',
  unavailable: 'We cannot tell which customer these requests belong to in this source.',
  account: 'The account id is shown as it was recorded. Unravis has not checked who it belongs to.'
} as const;

/** The plain tenant sentence. The server's own wording stays available as the tooltip, so nothing is lost. */
export function tenantPlain(attribution: string | null | undefined): string {
  if (attribution === 'available') return TENANT_TEXT.available;
  const share = /(\d+(?:\.\d+)?)\s*%\s*populated/i.exec(attribution ?? '');
  return TENANT_TEXT.unavailable + (share ? ' Only ' + share[1] + '% of the records have an account id.' : '');
}

/* ------------------------------------------------------------------ provenance: one line instead of four blocks */

/** Reads that finished with no time limit, row limit or read error. Hint-mode and partial reads are never summarised. */
const readEverything = (p: Provenance) => p.completeness === 'complete' || p.completeness === 'ran_to_end';

/** "Read every matching record from 2 sources." when every read finished, otherwise an empty string (the rows are then shown in full). */
export function provenanceSummary(list: Provenance[] | null | undefined): string {
  if (!Array.isArray(list) || list.length === 0 || !list.every(readEverything)) return '';
  const sources = new Set(list.map((p) => p.sourceId)).size;
  return 'Read every matching record from ' + sources + (sources === 1 ? ' source.' : ' sources.');
}

/* ------------------------------------------------------------------ help and glossary */

export const HELP_STEPS: readonly string[] = [
  'Pick a time range at the top of the page.',
  'Tap the figure that looks wrong. This opens a ranked list of operations.',
  'Open an operation to see its requests, then open one request to see its record.'
];

export const HELP_TEXT = {
  title: 'Help and glossary',
  startHeading: 'Start here',
  startIntro: 'Unravis compares a recent period with the one just before it. It shows what changed, not why.',
  termsHeading: 'Glossary'
} as const;

/** The line under the glossary search box. An empty result suggests terms that exist. */
export function glossaryCount(shown: number, query: string): string {
  if (shown > 0) return shown + (shown === 1 ? ' term' : ' terms');
  const text = query.trim();
  const suffix = ' Try P95, incomplete, episode or coverage.';
  return text ? 'No terms match "' + text.slice(0, 40) + '".' + suffix : 'No terms to show.';
}

/* ------------------------------------------------------------------ query controls */

/** Short, plain descriptions for the request-type buttons. Each says what the group is, not how healthy it is. */
export const KIND_TITLES = {
  all: 'Every request type. Latency is shown for one type at a time.',
  synchronous: 'Requests that return their answer when the work is done.',
  asynchronous: 'Requests accepted first and finished later in the background.',
  hangfire: 'Background jobs run by Hangfire.',
  http: 'Synchronous, asynchronous and requests whose type is not known.'
} as const;
