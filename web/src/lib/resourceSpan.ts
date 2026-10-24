/**
 * The "no data in this window" notice for the resource panel, with no DOM. The panel follows the window on screen, which starts at "now",
 * so a case that holds only older files shows nothing until the window is moved. When the answer for the window on screen is empty and the
 * case's files say what time they cover, the panel says so and offers to show that period. Nothing here guesses a time: the span is only
 * what the imports reported.
 */
import { formatTime } from './time';
import type { ResourceView, ViewWindow } from './resource';

export const spanCopy = {
  button: 'Show that period',
  buttonLabel: 'Show the resource counters for the period the files cover',
  shown: 'Showing the period the files cover.',
  back: 'Back to the window on screen'
} as const;

/** The server refuses a resource or series window wider than 200 years; this stays under it. */
export const MAX_PERIOD_MS = 199 * 365 * 24 * 3600 * 1000;
/** A span narrower than this is widened around its middle so the strips have something to draw. */
export const MIN_PERIOD_MS = 60 * 1000;

export interface Span { fromMs: number; toMs: number }
export interface SpanNotice { text: string; window: ViewWindow }
interface RecordLike { status?: string; quality?: { coverageFirst?: string | null; coverageLast?: string | null } | null }

/**
 * The first and last time the imported files cover: the earliest coverageFirst and the latest coverageLast among the imports that were kept.
 * An import that was refused or was a duplicate adds nothing, and one with no readable pair of times is skipped. Null when no import has a span.
 */
export function caseSpan(records: ReadonlyArray<RecordLike> | null | undefined): Span | null {
  let from = Infinity, to = -Infinity;
  for (const record of records ?? []) {
    if (record.status && record.status !== 'ok') continue;
    const first = Date.parse(record.quality?.coverageFirst ?? ''), last = Date.parse(record.quality?.coverageLast ?? '');
    if (!Number.isFinite(first) || !Number.isFinite(last) || last < first) continue;
    from = Math.min(from, first);
    to = Math.max(to, last);
  }
  return Number.isFinite(from) && Number.isFinite(to) ? { fromMs: from, toMs: to } : null;
}

/** True when the answer holds anything measured: a counter summary, a coverage fraction above zero, a rule row or a start or stop marker. */
export function resourceHasData(resource: Pick<ResourceView, 'hosts' | 'rules' | 'markers'>): boolean {
  if (resource.rules.length > 0 || resource.markers.length > 0) return true;
  return resource.hosts.some(host => host.summary.length > 0 || host.coverage.some(item => item.fraction > 0));
}

/** The window "Show that period" sets: the span itself, widened to a minute when it is narrower, and never past the server's width limit. */
export function periodWindow(span: Span): ViewWindow {
  const width = span.toMs - span.fromMs;
  if (width < MIN_PERIOD_MS) {
    const middle = span.fromMs + width / 2;
    return { fromMs: Math.round(middle - MIN_PERIOD_MS / 2), toMs: Math.round(middle + MIN_PERIOD_MS / 2) };
  }
  return { fromMs: span.fromMs, toMs: span.fromMs + Math.min(width, MAX_PERIOD_MS) };
}

/** "These files cover 3 Feb 2001 04:00:00 UTC to 3 Feb 2001 05:00:00 UTC." One time when the two ends are the same instant. */
export function spanText(span: Span, zone: string): string {
  const from = formatTime(span.fromMs, { zone, year: true }), to = formatTime(span.toMs, { zone, year: true });
  return from === to || span.fromMs === span.toMs ? 'These files cover ' + from + '.' : 'These files cover ' + from + ' to ' + to + '.';
}

/**
 * The notice, or null. It shows only when the answer was read for the current view (not busy, no error), holds no data, the case has a span,
 * and the view does not already hold the whole period (a button that changes nothing would only mislead).
 */
export function spanNotice(input: {
  resource: Pick<ResourceView, 'hosts' | 'rules' | 'markers'> | null; settled: boolean; span: Span | null; view: ViewWindow | null; zone: string;
}): SpanNotice | null {
  const { resource, settled, span, view, zone } = input;
  if (!resource || !settled || !span || !view) return null;
  if (resourceHasData(resource)) return null;
  const window = periodWindow(span);
  if (view.fromMs <= window.fromMs && view.toMs >= window.toMs) return null;
  return { text: spanText(span, zone), window };
}
