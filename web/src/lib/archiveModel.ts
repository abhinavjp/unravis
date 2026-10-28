/**
 * What the page knows about archive, check, free and restore (P3). Pure: no DOM, no fetch, no words.
 * The shapes follow internal/server/archive_dto.go and archive_jobs.go. Every parser keeps only the fields it
 * knows and never a key: a file's key shows as "Key ending xxxx" (the last four characters of its key check value).
 */

export type KeyKind = 'master' | 'typed' | 'case';

export interface ArchiveCounts { imports: number; sources: number; events: number }

/** One case file made from a case. */
export interface ArchiveFile {
  name: string;
  folder: string;
  bytes: number;
  keyKind: KeyKind;
  /** The last four characters of the key check value; empty when the server sent none. */
  keyEnding: string;
  made: string;
  counts: ArchiveCounts;
}

export interface RestoreLine { at: string; code: string }

/** The archive record of a case (state archived, or a case restored once). */
export interface ArchiveInfo {
  active: boolean;
  at: string;
  freed: boolean;
  freedAt: string;
  /** The case file of the archive in force. */
  current: ArchiveFile | null;
  /** The whole list (the case view only). */
  files: ArchiveFile[];
  restores: RestoreLine[];
}

const asNumber = (value: unknown): number => (typeof value === 'number' && Number.isFinite(value) ? value : 0);
const asString = (value: unknown): string => (typeof value === 'string' ? value : '');
const asObject = (value: unknown): Record<string, unknown> | null => (value && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, unknown> : null);

export function parseArchiveFile(raw: unknown): ArchiveFile | null {
  const r = asObject(raw);
  if (!r || typeof r.name !== 'string' || !r.name) return null;
  const counts = asObject(r.counts);
  const ending = asString(r.keyEnding);
  return {
    name: r.name,
    folder: asString(r.folder),
    bytes: Math.max(0, asNumber(r.bytes)),
    keyKind: r.keyKind === 'typed' ? 'typed' : r.keyKind === 'case' ? 'case' : 'master',
    keyEnding: /^[0-9a-f]{1,8}$/i.test(ending) ? ending.toLowerCase() : '',
    made: asString(r.made),
    counts: { imports: asNumber(counts?.imports), sources: asNumber(counts?.sources), events: asNumber(counts?.events) }
  };
}

/** The archive record from a case answer, or null when the case has none (or the answer is not the documented shape). */
export function parseArchive(raw: unknown): ArchiveInfo | null {
  const r = asObject(raw);
  if (!r) return null;
  const files = (Array.isArray(r.files) ? r.files : []).map(parseArchiveFile).filter((f): f is ArchiveFile => f !== null);
  const restores = (Array.isArray(r.restores) ? r.restores : [])
    .map((x): RestoreLine | null => { const o = asObject(x); return o ? { at: asString(o.at), code: asString(o.code) } : null; })
    .filter((x): x is RestoreLine => x !== null);
  return { active: r.active === true, at: asString(r.at), freed: r.freed === true, freedAt: asString(r.freedAt), current: parseArchiveFile(r.current), files, restores };
}

/* ------------------------------------------------------------------ jobs */

export type FileJobKind = 'archive' | 'verify';
export type FileJobState = 'running' | 'done' | 'failed' | 'stopped' | 'idle';

/** What GET and POST on .../archive and .../verify say about the job. */
export interface FileJob {
  kind: FileJobKind;
  state: FileJobState;
  phase: string;
  /** 0 to 100. */
  percent: number;
  reason: string;
  /** The server's fixed sentence for a failed or stopped job. Shown only through archiveCopy, never raw. */
  error: string;
  name: string;
  folder: string;
  bytes: number;
  /** Verify only: a passed check that still opens the door to Free. */
  verified: boolean;
}

const JOB_STATES: readonly string[] = ['running', 'done', 'failed', 'stopped', 'idle'];

export function parseFileJob(raw: unknown, kind: FileJobKind): FileJob | null {
  const r = asObject(raw);
  if (!r || typeof r.state !== 'string' || !JOB_STATES.includes(r.state)) return null;
  return {
    kind,
    state: r.state as FileJobState,
    phase: asString(r.phase),
    percent: Math.max(0, Math.min(100, Math.floor(asNumber(r.percent)))),
    reason: asString(r.reason),
    error: asString(r.error),
    name: asString(r.name),
    folder: asString(r.folder),
    bytes: Math.max(0, asNumber(r.bytes)),
    verified: r.verified === true
  };
}

/** True while the job still has work to do on the server, so the page keeps asking. */
export const jobRunning = (job: Pick<FileJob, 'state'> | null): boolean => job?.state === 'running';

/* ------------------------------------------------------------------ orphan files */

/** A finished case file an archive left behind without committing. Unravis never deletes or adopts it. */
export interface OrphanFile { name: string; folder: string; caseId: string; code: string; at: string; bytes: number }

export interface OrphanReport { files: OrphanFile[]; verifyCleanupPending: boolean }

export function parseOrphans(raw: unknown): OrphanReport {
  const r = asObject(raw);
  const files = (Array.isArray(r?.files) ? r.files as unknown[] : [])
    .map((x): OrphanFile | null => {
      const o = asObject(x);
      if (!o || typeof o.name !== 'string' || !o.name) return null;
      return { name: o.name, folder: asString(o.folder), caseId: asString(o.caseId), code: asString(o.code), at: asString(o.at), bytes: Math.max(0, asNumber(o.bytes)) };
    })
    .filter((x): x is OrphanFile => x !== null);
  return { files, verifyCleanupPending: r?.verifyCleanupPending === true };
}

/** The orphan files that belong on one case's health panel: its own, and any the server could not name a case for. */
export const orphansFor = (report: OrphanReport, caseId: string): OrphanFile[] => report.files.filter((f) => !f.caseId || f.caseId === caseId);

/* ------------------------------------------------------------------ rules */

/** "Key ending 7f3a", or empty when no ending is known. */
/** The full path of a case file: the folder it was saved to and the name the server gave it. Empty when the file is not known. */
export const archiveFilePath = (file: Pick<ArchiveFile, 'folder' | 'name'> | null | undefined): string =>
  !file || !file.name ? '' : file.folder ? file.folder.replace(/[\\/]+$/, '') + '\\' + file.name : file.name;

export const keyEndingOf = (file: Pick<ArchiveFile, 'keyEnding'> | null | undefined): string => file?.keyEnding ?? '';

/** The case code typed to free the space: the first 8 characters of the id, case and spaces around it do not matter. */
export function freeConfirmed(typed: string, caseId: string): boolean {
  return /^[a-z2-7]{26}$/.test(caseId) && typed.trim().toLowerCase() === caseId.slice(0, 8);
}

/** A passphrase for a typed-key file must be at least this long (server: bad-passphrase). */
export const MIN_PASSPHRASE = 12;
export const passphraseOk = (value: string): boolean => [...value].length >= MIN_PASSPHRASE;

/** The folder text is trimmed before it is sent; an empty folder is not sent at all. */
export const folderText = (typed: string): string => typed.trim();

export interface ArchiveRowFacts {
  archived: boolean;
  freed: boolean;
  missing: boolean;
  keptCopy: boolean;
}

/**
 * Which way back an archived case offers. Restore in place is a record move and exists only while the working copy is kept and not missing.
 * Restore as new needs the case file only. Free needs a kept working copy. Erase is always offered.
 */
export interface ArchivedActions { restoreInPlace: boolean; restoreAsNew: boolean; free: boolean; erase: boolean }

export function archivedActions(c: { state?: string; missing?: boolean; archive?: { freed: boolean; current: ArchiveFile | null } | null }): ArchivedActions {
  const archived = c.state === 'archived';
  const freed = !!c.archive?.freed;
  const kept = archived && !freed && !c.missing;
  return { restoreInPlace: kept, restoreAsNew: archived && !!c.archive?.current, free: kept, erase: true };
}

/** The chips on an archived row, in order, as ids the screen turns into fixed words. */
export function archivedChips(c: { missing?: boolean; archive?: { freed: boolean; current: ArchiveFile | null } | null }): Array<'archived' | 'kept' | 'freed' | 'missing' | 'key'> {
  const out: Array<'archived' | 'kept' | 'freed' | 'missing' | 'key'> = ['archived'];
  if (c.missing) out.push('missing');
  else out.push(c.archive?.freed ? 'freed' : 'kept');
  if (c.archive?.current?.keyEnding) out.push('key');
  return out;
}

/** Cases made before a master key was set up, or with the key locked, take a typed passphrase for a new file. */
export const needsTypedPassphrase = (lock: string): boolean => lock === 'none';

/** The four drawers a case can open: archive, the two ways back, and free the space. */
export type ArchiveDrawerKind = 'archive' | 'restore-in-place' | 'restore-as-new' | 'free';
