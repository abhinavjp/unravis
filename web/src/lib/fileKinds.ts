/**
 * The catalog of file kinds the "Import files" panel knows (gap G1/G2, wireframe w37). Pure: no DOM, no fetch, no state.
 *
 * What this file holds:
 *  - the kinds, named exactly as the server's importers name them (iis-w3c, iis-httperr, iis-freb, sql-xe, sql-errorlog, sql-querystore, win-events),
 *    plus `app-log` for the many text formats the server tells apart by reading the file;
 *  - `detectKind(name)`: a guess from the file name alone. It is a convenience for the list, never the final word. The server still reads the
 *    file and decides, unless the person changed the kind (see `importerFor`);
 *  - the time-zone rule of each kind: some write UTC, some write the server's local time and need the person to say which zone.
 * The words shown to the person are in copy/fileKindsCopy.ts.
 */

/** Kinds the person can pick. The ids are importer names, except `app-log` (the server tells the formats apart). */
export const FILE_KIND_IDS = ['iis-w3c', 'iis-httperr', 'iis-freb', 'sql-xe', 'sql-errorlog', 'sql-querystore', 'win-events', 'app-log'] as const;
export type CatalogKind = typeof FILE_KIND_IDS[number];

export const isCatalogKind = (value: string): value is CatalogKind => (FILE_KIND_IDS as readonly string[]).includes(value);

/**
 * Choices that are not importer names: the Hangfire jobs and Redis slow log CSV exports and "Another file or export". They are sent with no
 * importer name, so the server reads the file and decides (at git HEAD the sources step sent no importer for these either). They are offered by
 * the wizard's panel only; the import drawer of an open case has its own "pick the type" path for a file the server cannot place.
 */
export const EXTRA_KIND_IDS = ['hangfire-csv', 'redis-slowlog-csv', 'file'] as const;
export type ExtraKind = typeof EXTRA_KIND_IDS[number];
export const isExtraKind = (value: string): value is ExtraKind => (EXTRA_KIND_IDS as readonly string[]).includes(value);

/** What a row of the panel can be set to. */
export type PanelKind = CatalogKind | ExtraKind;
export const isPanelKind = (value: string): value is PanelKind => isCatalogKind(value) || isExtraKind(value);

/** How a kind writes its times. `utc`: always UTC. `local`: the clock of the server that wrote it. `varies`: depends on the program that wrote it. */
export type TimeZoneRule = 'utc' | 'local' | 'varies';

const RULES: Readonly<Record<CatalogKind, TimeZoneRule>> = {
  'iis-w3c': 'utc',
  'iis-freb': 'utc',
  'iis-httperr': 'utc',
  'sql-xe': 'utc',
  'sql-querystore': 'utc',
  'sql-errorlog': 'local',
  'win-events': 'local',
  'app-log': 'varies'
};

export const timeZoneRule = (kind: string): TimeZoneRule | null => (isCatalogKind(kind) ? RULES[kind] : null);

/** True when the person has to say which time zone the file's times are in (the kind writes local time or it varies). */
export const needsTimeZone = (kind: string): boolean => {
  const rule = timeZoneRule(kind);
  return rule === 'local' || rule === 'varies';
};

/**
 * Which computer the server needs to be told for a kind. `none`: the file says it (or no host is used). `optional`: only a file with no host
 * column needs one. `host`: a label from the host list is required (the file carries no machine name). `host-tz`: the label is required and
 * that host must also have a time zone, because the file writes that computer's local time. Read from the importers (internal/importer/*).
 */
export type HostNeed = 'none' | 'optional' | 'host' | 'host-tz';

const HOST_NEEDS: Readonly<Record<CatalogKind, HostNeed>> = {
  'iis-w3c': 'none',
  'iis-freb': 'none',
  'iis-httperr': 'host',
  'sql-xe': 'optional',
  'sql-querystore': 'host',
  'sql-errorlog': 'host-tz',
  'win-events': 'host-tz',
  'app-log': 'optional'
};

export const hostNeed = (kind: string): HostNeed => (isCatalogKind(kind) ? HOST_NEEDS[kind] : 'none');

/** True when the person must pick a computer before the file can be read. */
export const needsHost = (kind: string): boolean => {
  const need = hostNeed(kind);
  return need === 'host' || need === 'host-tz';
};

/** True when the kind can use a computer but does not need one (the picker is offered, not required). */
export const offersHost = (kind: string): boolean => hostNeed(kind) === 'optional';

/**
 * The importer name to ask the server for, or '' to let the server decide from the file. `app-log` is '' because several importers read
 * app logs and only the file itself says which. Any text that is not a catalog kind is ''.
 */
export const importerFor = (kind: string): string => (isCatalogKind(kind) && kind !== 'app-log' ? kind : '');

/* ------------------------------------------------------------------ names */

/** The last part of a path, in either slash style. A file picked from a folder can carry its folder in `webkitRelativePath`. */
export const baseName = (name: string): string => {
  const text = String(name ?? '');
  const cut = Math.max(text.lastIndexOf('/'), text.lastIndexOf('\\'));
  return cut >= 0 ? text.slice(cut + 1) : text;
};

const COMPRESSED = /\.(?:zip|gz|gzip|7z|rar|tar|tgz|bz2|xz|zst|cab)$/i;

/** A compressed file. The server does not unpack it, so the person has to extract it first. */
export const isCompressed = (name: string): boolean => COMPRESSED.test(baseName(name).trim());

/**
 * Guesses the kind from the file name. Case does not matter and a folder in front of the name is ignored. Returns '' when nothing fits,
 * and for a compressed file (it is refused before any guess matters).
 * The order matters: the more specific names come first (`httperr1.log` is not an IIS web log although it ends in .log).
 */
export function detectKind(name: string): CatalogKind | '' {
  const n = baseName(name).trim().toLowerCase();
  if (n === '' || isCompressed(n)) return '';
  if (/^httperr\d*(?:\.\w+)?$/.test(n) || /httperr/.test(n)) return 'iis-httperr';
  if (/^fr\d+\.xml$/.test(n) || /freb|failed.?req/.test(n)) return 'iis-freb';
  if (/\.xel$/.test(n) || /(?:^|[^a-z])xe[^a-z].*\.csv$/.test(n) || /extended.?events?/.test(n)) return 'sql-xe';
  if (/query.?store/.test(n)) return 'sql-querystore';
  if (/\.evtx$/.test(n) || /event.?log/.test(n) || /^win.?events?/.test(n)) return 'win-events';
  if (/^errorlog(?:\.\d+)?$/.test(n) || /error.?log/.test(n)) return 'sql-errorlog';
  if (/^u_(?:ex|extend|inetsv)\d*/.test(n) || /w3svc/.test(n) || /(?:^|[^a-z])iis(?:[^a-z]|$)/.test(n)) return 'iis-w3c';
  return '';
}

/** Why a file cannot be added, or ''. Only the name and size are known at this point. */
export type FileRefusal = '' | 'compressed' | 'empty';

export const refusalOf = (name: string, size: number): FileRefusal => (isCompressed(name) ? 'compressed' : size <= 0 ? 'empty' : '');

/** One file on the list before it is added: what it is called, how big, what the name suggests, and what the person chose. */
export interface StagedFile {
  /** Position in the list, stable while the list lives. */
  id: number;
  file: File;
  name: string;
  size: number;
  /** What the name suggests, or ''. */
  suggested: CatalogKind | '';
  /** What the person chose: a catalog kind, one of the extra choices, or '' for "Don't use this file". */
  choice: PanelKind | '';
  /** The person picked this kind themselves, instead of accepting the suggestion. */
  changed: boolean;
  refusal: FileRefusal;
  /** The label of the computer the file came from, from the host list, or ''. Only used by a kind that needs or offers one. */
  host: string;
  /** The add-file card the file was chosen in (wizard only), or '' for the general area. A card shows only its own files. */
  origin?: string;
}

/** Builds the list rows for files the person chose or dropped. A refused file has no choice. A file with no suggestion is left out until the person picks a kind. */
export function stageFiles(files: readonly File[], startId = 1): StagedFile[] {
  return files.map((file, i) => {
    const name = file.webkitRelativePath || file.name;
    const refusal = refusalOf(file.name, file.size);
    const suggested = refusal === '' ? detectKind(file.name) : '';
    return { id: startId + i, file, name, size: file.size, suggested, choice: suggested, changed: false, refusal, host: '' };
  });
}

/** The rows that will be added: not refused and with a kind. */
export const usableFiles = (list: readonly StagedFile[]): StagedFile[] => list.filter((item) => item.refusal === '' && item.choice !== '');

/** The rows that write local time and so need a time zone. */
export const needingZone = (list: readonly StagedFile[]): StagedFile[] => usableFiles(list).filter((item) => needsTimeZone(item.choice));

/** The rows that still need a computer picked before they can be read. */
export const needingHost = (list: readonly StagedFile[]): StagedFile[] => usableFiles(list).filter((item) => needsHost(item.choice) && item.host === '');

/** The distinct kinds in use, in catalog order (for the "what will be read" lines). */
export const kindsInUse = (list: readonly StagedFile[]): PanelKind[] => {
  const used = new Set<string>(usableFiles(list).map((item) => item.choice));
  return [...FILE_KIND_IDS, ...EXTRA_KIND_IDS].filter((id) => used.has(id));
};
