// Number and duration wording shared by every screen, so the same figure always reads the same way.

const NONE = '—';

/** A whole count with thousands separators: 1234 -> "1,234". Not a number -> an em dash. */
export const formatCount = (value: number | null | undefined): string =>
  value === null || value === undefined || !Number.isFinite(value) ? NONE : Math.round(value).toLocaleString();

/** "1 event", "20 events". Pass the plural when it is not just an added s. */
export const countNoun = (value: number, singular: string, plural = singular + 's'): string =>
  formatCount(value) + ' ' + (value === 1 ? singular : plural);

/**
 * A count next to the figure it is part of, in fixed words: formatCountOf(506, 508, 'request') -> "506 of 508 requests".
 * Say what the smaller figure leaves out right beside it, for example "506 of 508 requests have a final result".
 */
export const formatCountOf = (part: number, whole: number, singular: string, plural = singular + 's'): string =>
  formatCount(part) + ' of ' + formatCount(whole) + ' ' + (whole === 1 ? singular : plural);

/**
 * One duration formatter. Under a second it is milliseconds, under a minute it is seconds with one decimal, then minutes and seconds,
 * then hours and minutes: 700 -> "700 ms", 8700 -> "8.7 s", 68000 -> "1 min 8 s". Not a number -> an em dash.
 */
export const formatDuration = (ms: number | null | undefined): string => {
  if (ms === null || ms === undefined || !Number.isFinite(ms)) return NONE;
  if (ms < 0) return '−' + formatDuration(-ms);
  if (ms < 1) return ms === 0 ? '0 ms' : '<1 ms';
  if (ms < 10) return ms.toFixed(1) + ' ms';
  if (Math.round(ms) < 1000) return Math.round(ms) + ' ms';
  const tenths = Math.round(ms / 100);
  if (tenths < 600) return (tenths / 10).toFixed(1) + ' s';
  const seconds = Math.round(ms / 1000);
  if (seconds < 3600) return Math.floor(seconds / 60) + ' min' + (seconds % 60 ? ' ' + (seconds % 60) + ' s' : '');
  const minutes = Math.round(seconds / 60);
  return Math.floor(minutes / 60) + ' h' + (minutes % 60 ? ' ' + (minutes % 60) + ' min' : '');
};
