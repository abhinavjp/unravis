import type { InvestigationRead, ReportAnswer } from './types';

/**
 * The report export: what to warn about before it is saved, a safe file name and a readable size. The report itself is built on the server from
 * the request that made the answer on screen. It holds no payloads, no user ids, no connection details and no passwords.
 */

/** Sentences for what the person should know before saving. An empty list means the report matches the screen and read everything. */
export function reportWarnings(read: InvestigationRead | undefined, outOfDate: boolean, truncatedText: string, outOfDateText: string): string[] {
  const out: string[] = [];
  if (outOfDate) out.push(outOfDateText);
  if (read?.truncated) out.push(truncatedText);
  for (const w of read?.warnings ?? []) if (w && !out.includes(w)) out.push(w);
  return out;
}

/**
 * The warning codes sent with the report request. The server prints a fixed sentence for each in the saved file, so the file carries what
 * the screen said. Codes only, never text.
 */
export function reportWarningCodes(outOfDate: boolean, read: InvestigationRead | undefined): string[] {
  const out: string[] = [];
  if (outOfDate) out.push('stale');
  if (read?.truncated) out.push('truncated');
  return out;
}

/** A file name with no folders and no control characters, or the fallback for its format. */
export function reportFileName(answer: Pick<ReportAnswer, 'filename' | 'format'>): string {
  const last = (answer.filename ?? '').split(/[\/]/).pop() ?? '';
  // eslint-disable-next-line no-control-regex
  const clean = last.replace(/[\u0000-\u001f<>:"|?*]/g, '').trim();
  if (clean && clean !== '.' && clean !== '..') return clean;
  return answer.format === 'csv' ? 'unravis-data.csv' : 'unravis-report.html';
}

export function byteLabel(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes < 0) return '0 bytes';
  if (bytes < 1024) return `${Math.round(bytes)} bytes`;
  if (bytes < 1024 * 1024) return `${Math.round(bytes / 1024)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}
