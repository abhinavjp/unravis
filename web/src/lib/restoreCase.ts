import { ENTER_TEXT, RESTORE_FAILED, throttledText } from './copy/enterCopy';

/**
 * Restoring a case from a case file: what the form checks before it asks, and how a refusal reads on screen. Pure: no DOM, no fetch,
 * and nothing in here keeps a master key or a path. The request itself is in caseFileApi.ts.
 *
 * Every bad-case-file reason (wrong key, wrong path, damaged file, a folder) is the one sentence RESTORE_FAILED. The only things that read
 * differently are the ones that say nothing about the file: a throttled wait, a locked or missing master key on this PC, a busy server.
 */

export const CASE_FILE_EXTENSION = '.unravis-case';
/** The longest path the server takes, counted in UTF-8 bytes (internal/server/case_file.go maxCaseFilePath). Keep the two equal. */
export const MAX_PATH_BYTES = 520;
/** A full path on this computer: a drive letter, a colon and a slash. Network paths (\\server\share) and device paths do not match. */
const DRIVE_PATH = /^[A-Za-z]:[\\/]/;
/** Invisible format characters (direction marks, zero-width, BOM) that Explorer and chat apps add around a copied path. */
const FORMAT_CHARS = /\p{Cf}/gu;

/** The typed path without the quotes File Explorer puts around "Copy as path", the invisible marks, and spaces around it. */
export function normalisePath(input: string): string {
  let text = typeof input === 'string' ? input.replace(FORMAT_CHARS, '').trim() : '';
  if (text.length >= 2 && text.startsWith('"') && text.endsWith('"')) text = text.slice(1, -1).replace(FORMAT_CHARS, '').trim();
  return text;
}

const pathBytes = (path: string): number => new TextEncoder().encode(path).length;

export type RestoreCheck =
  | { ok: true; path: string }
  | { ok: false; field: 'path' | 'key'; message: string };

/** What the form checks before it sends anything. The master key is only tested for being empty; its spaces count, so it is never trimmed. */
export function checkRestoreInput(rawPath: string, key: string): RestoreCheck {
  const path = normalisePath(rawPath);
  if (!path) return { ok: false, field: 'path', message: ENTER_TEXT.pathEmpty };
  if (!DRIVE_PATH.test(path)) return { ok: false, field: 'path', message: ENTER_TEXT.pathShape };
  if (pathBytes(path) > MAX_PATH_BYTES) return { ok: false, field: 'path', message: ENTER_TEXT.pathTooLong };
  if (!path.toLowerCase().endsWith(CASE_FILE_EXTENSION)) return { ok: false, field: 'path', message: ENTER_TEXT.pathName };
  if (!key) return { ok: false, field: 'key', message: ENTER_TEXT.keyEmpty };
  return { ok: true, path };
}

export type RestoreFailureKind = 'bad-case-file' | 'bad-path' | 'path-too-long' | 'bad-request' | 'throttled' | 'locked' | 'no-key' | 'busy' | 'network' | 'other';

export interface RestoreFailure { kind: RestoreFailureKind; retryAfterSeconds?: number }

interface FailureShape { status?: unknown; reason?: unknown; retryAfterSeconds?: unknown }

/** Turns what a failed call carried (a status, a reason code and a wait time; never a key or a path) into the small set the card acts on. */
export function restoreFailureOf(error: unknown): RestoreFailure {
  const e: FailureShape = error && typeof error === 'object' ? error as FailureShape : {};
  const status = typeof e.status === 'number' ? e.status : -1;
  const wait = typeof e.retryAfterSeconds === 'number' && e.retryAfterSeconds > 0 ? e.retryAfterSeconds : undefined;
  if (status === 429 || e.reason === 'throttled') return { kind: 'throttled', retryAfterSeconds: wait ?? 5 };
  if (status === 423 || e.reason === 'locked') return { kind: 'locked' };
  if (status === 0 || e.reason === 'unreachable') return { kind: 'network' };
  if (e.reason === 'no-master-key') return { kind: 'no-key' };
  if (e.reason === 'busy') return { kind: 'busy' };
  if (status === 422 || e.reason === 'bad-case-file') return { kind: 'bad-case-file' };
  if (e.reason === 'bad-path') return { kind: 'bad-path' };
  if (e.reason === 'path-too-long') return { kind: 'path-too-long' };
  if (status === 400 || e.reason === 'bad-request') return { kind: 'bad-request' };
  return { kind: 'other' };
}

/** What the card shows after a refusal. `offerSetup` shows the master key set-up link; `waitSeconds` holds the button off for that long. */
export interface RestoreView {
  message: string;
  offerSetup: boolean;
  waitSeconds: number;
  /** The field to put the cursor in so the next try is easy; 'none' when retyping would not help. */
  focus: 'path' | 'key' | 'none';
}

/** `keyState` is what the app last learned about the master key. While it is damaged, a 423 means "no key can open the file", not "type your key". */
export function restoreView(failure: RestoreFailure, keyState?: 'none' | 'locked' | 'unlocked' | 'damaged' | 'unknown'): RestoreView {
  switch (failure.kind) {
    case 'bad-case-file': return { message: RESTORE_FAILED, offerSetup: false, waitSeconds: 0, focus: 'key' };
    case 'bad-path': return { message: ENTER_TEXT.pathShape, offerSetup: false, waitSeconds: 0, focus: 'path' };
    case 'path-too-long': return { message: ENTER_TEXT.pathTooLong, offerSetup: false, waitSeconds: 0, focus: 'path' };
    case 'bad-request': return { message: ENTER_TEXT.badRequest, offerSetup: false, waitSeconds: 0, focus: 'path' };
    case 'throttled': {
      const seconds = Math.max(1, Math.ceil(failure.retryAfterSeconds ?? 1));
      return { message: throttledText(seconds), offerSetup: false, waitSeconds: seconds, focus: 'none' };
    }
    case 'locked': return { message: keyState === 'damaged' ? ENTER_TEXT.damaged : ENTER_TEXT.locked, offerSetup: false, waitSeconds: 0, focus: 'none' };
    case 'no-key': return { message: ENTER_TEXT.needKey, offerSetup: true, waitSeconds: 0, focus: 'none' };
    case 'busy': return { message: ENTER_TEXT.busy, offerSetup: false, waitSeconds: 0, focus: 'none' };
    case 'network': return { message: ENTER_TEXT.network, offerSetup: false, waitSeconds: 0, focus: 'none' };
    default: return { message: ENTER_TEXT.other, offerSetup: false, waitSeconds: 0, focus: 'none' };
  }
}

/** The seconds a refusal asks for: the Retry-After header when it is a whole number, otherwise the body's own figure. At most an hour. */
export function retryAfterOf(header: string | null | undefined, body?: unknown): number | undefined {
  const fromHeader = typeof header === 'string' && /^\s*\d{1,6}\s*$/.test(header) ? Number(header) : undefined;
  const fromBody = typeof body === 'number' && Number.isFinite(body) && body > 0 ? body : undefined;
  const seconds = fromHeader !== undefined && fromHeader > 0 ? fromHeader : fromBody;
  return seconds === undefined ? undefined : Math.min(3600, Math.ceil(seconds));
}

/**
 * The card needs a master key on this PC before a restore can work. True when the status says none is set: the card then offers the
 * set-up instead of the form. An unreadable status ('unknown') still shows the form; the server answers 409 if there is no key.
 */
export const restoreNeedsKey = (state: 'none' | 'locked' | 'unlocked' | 'damaged' | 'unknown'): boolean => state === 'none';
