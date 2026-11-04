import type { Source } from './types';

/**
 * The size line of an audit database in the Sources list (wireframe w48: "Mongo audit, 218 k docs"): how many documents it holds and how old the
 * newest one is. Pure: no DOM, no fetch. The server sends an estimate (collection statistics, never a scan); a count it could not read is -1.
 */
export interface SourceStatsView { docs: number; newestAt: string }

/** "218 k docs", "1.4 M docs", "950 docs". Empty when the count is not known. */
export function docsText(docs: number): string {
  if (!Number.isFinite(docs) || docs < 0) return '';
  if (docs === 1) return '1 doc';
  if (docs < 1000) return docs + ' docs';
  if (docs < 1_000_000) return (docs < 10_000 ? Math.round(docs / 100) / 10 : Math.round(docs / 1000)) + ' k docs';
  return Math.round(docs / 100_000) / 10 + ' M docs';
}

/** "newest 3 h ago", "newest 2 days ago". Empty when there is no time. A time in the future reads as just now. */
export function ageText(newestAt: string, now: number): string {
  const then = Date.parse(newestAt);
  if (!Number.isFinite(then)) return '';
  const minutes = Math.max(0, Math.floor((now - then) / 60_000));
  if (minutes < 2) return 'newest just now';
  if (minutes < 120) return 'newest ' + minutes + ' min ago';
  const hours = Math.floor(minutes / 60);
  if (hours < 48) return 'newest ' + hours + ' h ago';
  return 'newest ' + Math.floor(hours / 24) + ' days ago';
}

/** The whole line: count and age, joined. Empty when neither is known. */
export function sizeLine(view: SourceStatsView | null, now: number): string {
  if (!view) return '';
  return [docsText(view.docs), view.newestAt === '' ? '' : ageText(view.newestAt, now)].filter((part) => part !== '').join(' · ');
}

/** The audit sources whose database is the named one. A tag stands for a database; the server may hold several collections of it. */
export const auditSourcesOfDatabase = (sources: readonly Pick<Source, 'id' | 'database'>[], database: string): string[] =>
  sources.filter((source) => source.database === database).map((source) => source.id);

/** Adds the stats of several sources of one database: the counts add up, the newest time is the latest. A source whose count was unknown makes the sum unknown. */
export function mergeStats(parts: readonly SourceStatsView[]): SourceStatsView | null {
  if (parts.length === 0) return null;
  let docs = 0;
  let newest = '';
  for (const part of parts) {
    docs = docs < 0 || part.docs < 0 ? -1 : docs + part.docs;
    if (part.newestAt !== '' && (newest === '' || Date.parse(part.newestAt) > Date.parse(newest))) newest = part.newestAt;
  }
  return { docs, newestAt: newest };
}
