/**
 * Calls for archive, check, free and restore (P3). Every call carries what the page believes the case is
 * (X-Expect-State). A passphrase travels only in a JSON body, never in a URL, and is never kept here.
 */
import { send, json, casePath, toCase, toErased, believedState, EXPECT_STATE_HEADER, CaseApiError, type CaseInfo, type CaseState, type ErasedLine } from './cases';
import { parseFileJob, parseOrphans, type FileJob, type FileJobKind, type OrphanReport } from './archiveModel';

const expecting = (believed?: CaseState): Record<string, string> => (believed ? { [EXPECT_STATE_HEADER]: believed } : {});

/** The belief a page sends for a case it is showing. */
export const beliefOf = (c: Pick<CaseInfo, 'state' | 'closed'>): CaseState => believedState(c);

function asJob(raw: unknown, kind: FileJobKind): FileJob {
  const job = parseFileJob(raw, kind);
  if (!job) throw new CaseApiError('The server sent an answer that could not be read.', 200, 'unreadable');
  return job;
}

/** One page of cases. The working list carries the archived total so the toggle can say how many there are. */
export interface CasesPage { cases: CaseInfo[]; archivedTotal: number; offset: number; pageSize: number; erased: ErasedLine[] }

function toPage(body: { cases?: Partial<CaseInfo>[]; archivedTotal?: unknown; offset?: unknown; pageSize?: unknown; erased?: unknown }): CasesPage {
  const num = (v: unknown, d: number): number => (typeof v === 'number' && Number.isFinite(v) && v >= 0 ? Math.floor(v) : d);
  return {
    cases: (body.cases ?? []).map(toCase).filter((c) => /^[a-z2-7]{26}$/.test(c.id)),
    archivedTotal: num(body.archivedTotal, 0),
    offset: num(body.offset, 0),
    pageSize: num(body.pageSize, 50),
    // The lines left by erased cases (id and time only). The archived list never carries them.
    erased: Array.isArray(body.erased) ? body.erased.map(toErased).filter((l): l is ErasedLine => l !== null) : []
  };
}

export async function listCasesPage(signal?: AbortSignal): Promise<CasesPage> {
  return toPage(await json(await send('/cases', {}, signal)));
}

/** The archived list, 50 at a time. It never mixes with the working list. */
export async function listArchivedCases(offset = 0, signal?: AbortSignal): Promise<CasesPage> {
  const at = Math.max(0, Math.floor(offset));
  return toPage(await json(await send('/cases?archived=1&offset=' + at, {}, signal)));
}

/* ------------------------------------------------------------------ archive */

/** Starts the archive. The folder is a full path typed by the person; a passphrase is sent only when no master key is in use. */
export async function startArchive(id: string, believed: CaseState, folder: string, passphrase?: string, signal?: AbortSignal): Promise<FileJob> {
  const body: { path: string; passphrase?: string } = { path: folder };
  if (passphrase) body.passphrase = passphrase;
  const answer = await json(await send(casePath(id, '/archive'), { method: 'POST', headers: expecting(believed), body: JSON.stringify(body) }, signal));
  return asJob(answer, 'archive');
}

export async function getArchiveJob(id: string, signal?: AbortSignal): Promise<FileJob> {
  return asJob(await json(await send(casePath(id, '/archive'), {}, signal)), 'archive');
}

export async function cancelArchive(id: string, signal?: AbortSignal): Promise<void> {
  await send(casePath(id, '/archive'), { method: 'DELETE' }, signal);
}

/* ------------------------------------------------------------------ check (verify) */

export async function startVerify(id: string, believed: CaseState, passphrase?: string, signal?: AbortSignal): Promise<FileJob> {
  const body: { passphrase?: string } = {};
  if (passphrase) body.passphrase = passphrase;
  return asJob(await json(await send(casePath(id, '/verify'), { method: 'POST', headers: expecting(believed), body: JSON.stringify(body) }, signal)), 'verify');
}

export async function getVerify(id: string, signal?: AbortSignal): Promise<FileJob> {
  return asJob(await json(await send(casePath(id, '/verify'), {}, signal)), 'verify');
}

export async function cancelVerify(id: string, signal?: AbortSignal): Promise<void> {
  await send(casePath(id, '/verify'), { method: 'DELETE' }, signal);
}

/* ------------------------------------------------------------------ free and restore */

/** Frees the working copy. `typed` is the case code the person typed; the server compares it again. */
export async function freeCase(id: string, believed: CaseState, typed: string, signal?: AbortSignal): Promise<void> {
  await send(casePath(id, '/free'), { method: 'POST', headers: expecting(believed), body: JSON.stringify({ confirm: typed.trim().toLowerCase() }) }, signal);
}

/** Moves the case back from the archive without touching the file. It ends Closed. */
export async function restoreInPlace(id: string, believed: CaseState, signal?: AbortSignal): Promise<void> {
  await send(casePath(id, '/restore-in-place'), { method: 'POST', headers: expecting(believed), body: '{}' }, signal);
}

/** Makes a new case from the archived file of this case. A typed passphrase goes along only for a file sealed with one. */
export async function restoreAsNew(id: string, believed: CaseState, passphrase?: string, signal?: AbortSignal): Promise<CaseInfo> {
  const body: { passphrase?: string } = {};
  if (passphrase) body.passphrase = passphrase;
  return toCase(await json(await send(casePath(id, '/restore-as-new'), { method: 'POST', headers: expecting(believed), body: JSON.stringify(body) }, signal)));
}

/* ------------------------------------------------------------------ orphan files */

export async function fetchOrphans(signal?: AbortSignal): Promise<OrphanReport> {
  return parseOrphans(await json(await send('/cases/orphan-files', {}, signal)));
}
