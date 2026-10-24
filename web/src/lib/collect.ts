/**
 * The "What to collect next" list, with no DOM: the shape check for GET /api/cases/{id}/collect and the rows the list shows.
 * The server writes every sentence (internal/collect); this file only checks the shape and names the file types.
 * Nothing here says why something was slow. Each item is a log that is not in the case yet.
 */
import { importerLabel } from './copy/importCopy';

export interface CollectItem { id: string; host: string; title: string; why: string; how: string; importer: string; rank: number }

export interface CollectRow {
  /** Unique within one list, for keyed rendering. */
  key: string;
  rank: number;
  title: string;
  /** The host the log is missing from; empty when the whole case is meant. */
  host: string;
  why: string;
  how: string;
  /** The names of the file types to import, as the import screen shows them. */
  fileTypes: string[];
}

/** The server never sends more than 12; a longer list is not the documented shape. */
export const MAX_COLLECT_ITEMS = 12;

const isObject = (value: unknown): value is Record<string, unknown> => typeof value === 'object' && value !== null && !Array.isArray(value);
const isText = (value: unknown): value is string => typeof value === 'string';

function parseItem(value: unknown): CollectItem | null {
  if (!isObject(value)) return null;
  const { id, host, title, why, how, importer, rank } = value;
  if (!isText(id) || !id || !isText(host) || !isText(title) || !title || !isText(why) || !isText(how) || !isText(importer) || !importer) return null;
  if (typeof rank !== 'number' || !Number.isInteger(rank) || rank < 1) return null;
  return { id, host, title, why, how, importer, rank };
}

/** GET /api/cases/{id}/collect. Null when the answer is not the documented shape: an object with a list called items. */
export function parseCollect(value: unknown): CollectItem[] | null {
  if (!isObject(value) || !Array.isArray(value.items) || value.items.length > MAX_COLLECT_ITEMS) return null;
  const items: CollectItem[] = [];
  for (const raw of value.items) {
    const item = parseItem(raw);
    if (!item) return null;
    items.push(item);
  }
  return items.sort((a, b) => a.rank - b.rank);
}

/** The file types one item reads: its importer names (joined with ", " when several) turned into the names the import screen uses. */
export function fileTypesOf(importer: string): string[] {
  return importer.split(',').map((name) => name.trim()).filter(Boolean).map(importerLabel);
}

/** The rows to draw, in rank order. An empty list means the empty state. */
export function collectRows(items: CollectItem[]): CollectRow[] {
  const used = new Set<string>();
  return [...items].sort((a, b) => a.rank - b.rank).map((item) => {
    let key = item.id + '@' + item.host;
    for (let n = 2; used.has(key); n++) key = item.id + '@' + item.host + '#' + n;
    used.add(key);
    return { key, rank: item.rank, title: item.title, host: item.host, why: item.why, how: item.how, fileTypes: fileTypesOf(item.importer) };
  });
}
