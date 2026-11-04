import { fileKindLabel } from './copy/fileKindsCopy';
import { isCatalogKind, timeZoneRule } from './fileKinds';
import { cleanTag, LABEL_MAX } from './wizardSources';
import type { HostRef } from './zoneChoice';

/**
 * The Sources list of a case (packet P11, wireframe srcItemHTML): each source with its Environment, Portal and API tags, and the state of its
 * time zone. Pure: no DOM, no fetch.
 *
 * Time-zone state. The server decides which files the timeline may draw (traceRuleValidated): a file needs one of the three self-describing
 * rules, or the host-local rule with a computer named. This module mirrors that rule and adds two honest states:
 *  - known: the file says its zone itself (UTC field, offset in the value, a header bias), or it names a computer whose zone is saved.
 *  - assumed UTC: the rule is "UTC field" but the type is one that writes the clock of its computer, or varies by log program. The importer
 *    took UTC; the file did not say so.
 *  - needs a check: the server cannot place the file (no rule, or local time with no computer), or its computer is gone from the list or has no
 *    zone. The timeline leaves such a file out.
 * An import's rule cannot be changed after the import, so a state is only ever shown here, never edited in place.
 */

export type TzState = 'known' | 'assumed-utc' | 'needs-check';
export type TzWhy = 'utc' | 'offset' | 'header' | 'host' | 'assumed' | 'no-rule' | 'no-host' | 'host-gone' | 'no-zone';

export interface TzView { state: TzState; why: TzWhy; /** The zone name when there is one ('UTC', 'Asia/Dubai'), else ''. */ zone: string; /** The computer, when the rule names one. */ host: string }

/** The part of an import record this module reads. */
export interface ListImport { id: number; importer: string; fileName: string; hostLabel: string; tzRule: string; status: string; rows: number }
/** The part of a source tag this module reads. */
export interface ListTag { id: number; importId: number; kind: string; label: string; env: string; portal: string; api: string; classification: string }

const KNOWN_RULES = ['utc-field', 'offset-in-field', 'header-bias', 'host-local'];

/** The state of one imported file. `hosts` is null before the list of computers has been read: a computer already named is then trusted. */
export function tzOfImport(rec: Pick<ListImport, 'importer' | 'tzRule' | 'hostLabel'>, hosts: readonly HostRef[] | null): TzView {
  const host = rec.hostLabel.trim();
  if (!KNOWN_RULES.includes(rec.tzRule)) return { state: 'needs-check', why: 'no-rule', zone: '', host };
  switch (rec.tzRule) {
    case 'utc-field': {
      const own = timeZoneRule(rec.importer);
      return own === 'local' || own === 'varies' ? { state: 'assumed-utc', why: 'assumed', zone: 'UTC', host } : { state: 'known', why: 'utc', zone: 'UTC', host };
    }
    case 'offset-in-field': return { state: 'known', why: 'offset', zone: '', host };
    case 'header-bias': return { state: 'known', why: 'header', zone: '', host };
    default: {
      if (host === '') return { state: 'needs-check', why: 'no-host', zone: '', host };
      if (hosts === null) return { state: 'known', why: 'host', zone: '', host };
      const entry = hosts.find((item) => item.label === host);
      if (!entry) return { state: 'needs-check', why: 'host-gone', zone: '', host };
      if (entry.tz.trim() === '') return { state: 'needs-check', why: 'no-zone', zone: '', host };
      return { state: 'known', why: 'host', zone: entry.tz.trim(), host };
    }
  }
}

/** A tag with no file behind it. A MongoDB database keeps its dates in UTC; the other kinds do not read times from a file, so they carry no state. */
export function tzOfTag(tag: Pick<ListTag, 'kind'>): TzView | null {
  return tag.kind === 'mongo-database' ? { state: 'known', why: 'utc', zone: 'UTC', host: '' } : null;
}

export type LaneSticker = '' | 'API audit' | 'SQL' | 'IIS' | 'Windows events' | 'App data' | 'Blackbox';

/** The small grey word that says which lane a source feeds. */
export function stickerOfTag(tag: Pick<ListTag, 'kind' | 'classification'>): LaneSticker {
  const kind = tag.kind;
  if (kind === 'mongo-database') return tag.classification === 'audit' ? 'API audit' : tag.classification === 'other' ? 'App data' : '';
  if (kind === 'blackbox') return 'Blackbox';
  if (kind === 'sql-server' || kind.startsWith('sql-')) return 'SQL';
  if (kind === 'iis-logs' || kind.startsWith('iis-')) return 'IIS';
  if (kind === 'win-events') return 'Windows events';
  if (kind === 'app-log' || kind === 'hangfire-csv' || kind === 'redis-slowlog-csv') return 'App data';
  return '';
}

export const stickerOfImporter = (importer: string): LaneSticker => stickerOfTag({ kind: importer, classification: '' });

export interface ListedSource {
  /** Stable for the list: 'f<importId>' for a file, 't<tagId>' for a tag with no file. */
  key: string;
  name: string;
  /** What the source is, in words: the file type, or the kind of tag. */
  kindText: string;
  sticker: LaneSticker;
  env: string;
  portal: string;
  api: string;
  /** The computer named for a file, or ''. */
  host: string;
  tz: TzView | null;
  isFile: boolean;
}

/** True when a tag kind fits an import: the same name, or an app-log tag against the importer the server detected in the file (any name that is not a catalog kind itself, such as 'serilog-clef'). */
export function kindMatches(tagKind: string, importer: string): boolean {
  return tagKind === importer || (tagKind === 'app-log' && !isCatalogKind(importer));
}

const TAG_KIND_TEXT: Readonly<Record<string, string>> = {
  'mongo-database': 'MongoDB database', 'sql-server': 'SQL Server', redis: 'Redis', blackbox: 'Blackbox recording', 'iis-logs': 'IIS logs',
  'app-log': 'App log', 'hangfire-csv': 'Hangfire export', 'redis-slowlog-csv': 'Redis slow log', file: 'File', discover: 'Found on this computer'
};

const kindTextOfTag = (kind: string): string => TAG_KIND_TEXT[kind] ?? (fileKindLabel(kind) || kind);
const kindTextOfImporter = (importer: string): string => fileKindLabel(importer) || importer;

/**
 * Builds the rows. A tag that names an import is folded into that file's row. A tag the wizard made before any import (import 0) of a file kind
 * is folded into the unclaimed file of the same type whose name is the tag's label, when that match is unique on both sides; with no name match
 * it folds into the one unclaimed file of the same type, when there is exactly one. Otherwise it keeps its own row, so nothing is guessed.
 * A tag whose label is the name of any file in the case, even one whose import failed or was a duplicate, belongs to that file and is never
 * handed to another file by the fallback. A tag that names an import that is missing or not ok keeps its own row. Only files that were kept
 * (status ok) are listed.
 */
export function buildSourceList(tags: readonly ListTag[], imports: readonly ListImport[], hosts: readonly HostRef[] | null): ListedSource[] {
  const files = imports.filter((rec) => rec.status === 'ok');
  const claimed = new Map<number, ListTag>();
  /** Ids of tags already given to a file, so one tag is never used twice or counted against another tag's pairing. */
  const claimedTags = new Set<number>();
  const loose: ListTag[] = [];
  for (const tag of tags) {
    if (tag.importId > 0 && files.some((rec) => rec.id === tag.importId) && !claimed.has(tag.importId)) { claimed.set(tag.importId, tag); claimedTags.add(tag.id); }
    else loose.push(tag);
  }
  const rest: ListTag[] = [];
  // A tag tied to an import that is gone or not ok is never folded into another file by guessing.
  const pending = loose.filter((tag) => {
    if (tag.importId !== 0) { rest.push(tag); return false; }
    return true;
  });
  // The wizard posts one tag per file before the imports exist, and the label is the file's name: pair on that first.
  for (const tag of pending) {
    const sameName = files.filter((rec) => kindMatches(tag.kind, rec.importer) &&!claimed.has(rec.id) && cleanTag(rec.fileName, LABEL_MAX) === tag.label);
    const sameTags = pending.filter((other) => !claimedTags.has(other.id) && other.kind === tag.kind && other.label === tag.label);
    if (sameName.length === 1 && sameTags.length === 1) { claimed.set(sameName[0].id, tag); claimedTags.add(tag.id); }
  }
  for (const tag of pending) {
    if (claimedTags.has(tag.id)) continue;
    // The label is the name of a file in the case (its import may have failed or be a duplicate): the tag belongs to that file, so it keeps
    // its own row instead of taking the environment to another file of the same kind.
    if (imports.some((rec) => kindMatches(tag.kind, rec.importer) && cleanTag(rec.fileName, LABEL_MAX) === tag.label)) { rest.push(tag); continue; }
    const same = files.filter((rec) => kindMatches(tag.kind, rec.importer) && !claimed.has(rec.id));
    const sameTags = pending.filter((other) => !claimedTags.has(other.id) && other.kind === tag.kind);
    if (same.length === 1 && sameTags.length === 1) { claimed.set(same[0].id, tag); claimedTags.add(tag.id); }
    else rest.push(tag);
  }
  const rows: ListedSource[] = [];
  for (const rec of files) {
    const tag = claimed.get(rec.id);
    rows.push({
      key: 'f' + rec.id, name: rec.fileName, kindText: kindTextOfImporter(rec.importer), sticker: stickerOfImporter(rec.importer), env: tag?.env ?? '', portal: tag?.portal ?? '', api: tag?.api ?? '',
      host: rec.hostLabel.trim(), tz: tzOfImport(rec, hosts), isFile: true
    });
  }
  for (const tag of rest) {
    rows.push({
      key: 't' + tag.id, name: tag.label, kindText: kindTextOfTag(tag.kind), sticker: stickerOfTag(tag), env: tag.env, portal: tag.portal, api: tag.api, host: '', tz: tzOfTag(tag), isFile: false
    });
  }
  const order: LaneSticker[] = ['API audit', 'SQL', 'IIS', 'Windows events', 'App data', 'Blackbox', ''];
  return rows.map((row, index) => ({ row, index })).sort((a, b) => order.indexOf(a.row.sticker) - order.indexOf(b.row.sticker) || a.index - b.index).map((item) => item.row);
}

/** The distinct environment names in the list, in the order they first appear. */
export function envNames(list: readonly ListedSource[]): string[] {
  const seen: string[] = [];
  for (const row of list) if (row.env !== '' && !seen.includes(row.env)) seen.push(row.env);
  return seen;
}

/** The rows of one environment; an empty `env` is no filter. */
export const filterByEnv = (list: readonly ListedSource[], env: string): ListedSource[] => (env === '' ? [...list] : list.filter((row) => row.env === env));

export interface TzCounts { known: number; assumed: number; needs: number }

export function tzCounts(list: readonly ListedSource[]): TzCounts {
  const counts: TzCounts = { known: 0, assumed: 0, needs: 0 };
  for (const row of list) {
    if (!row.tz) continue;
    if (row.tz.state === 'known') counts.known++;
    else if (row.tz.state === 'assumed-utc') counts.assumed++;
    else counts.needs++;
  }
  return counts;
}
