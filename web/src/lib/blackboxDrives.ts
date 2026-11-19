import { forgetSession, NO_SESSION_MESSAGE, readSessionToken } from './api';
import { BlackboxError, type Refusal } from './blackbox';

/**
 * Client for the drives route of the wizard's Folder step (contract C-3, rule S-11). A typed fetch and
 * one filter; the step's own rules live in blackboxWizard.ts and its words in copy/blackboxWizardCopy.ts.
 *
 * The contract (JSON is camelCase; the request carries X-Audit-Token, like every Blackbox call):
 *   GET /api/blackbox/this-pc/drives -> { drives }
 * The route is read-only and this PC only. A drive is a letter, a label, a type, the Windows flag and
 * space numbers: never a path. A refusal is a BlackboxError in the same style as blackboxApi's, so no
 * error text from the operating system reaches the page.
 */

const base = '/api/blackbox';

/** The drive types the route reports: a fixed disk, a USB-style disk, a network drive, anything else. */
export type DriveType = 'fixed' | 'removable' | 'network' | 'other';

/** One drive of this PC, as the drives route lists it. The letter only: the step builds any path itself. */
export interface DriveInfo {
  letter: string;
  label: string;
  type: DriveType;
  /** True on the drive Windows itself runs from. It stays listed, and the C: tick guards it. */
  system: boolean;
  freeBytes: number | null;
  totalBytes: number | null;
}

export interface DrivesAnswer { drives: DriveInfo[] }

/* ---------------------------------------------------------------- calls */

async function readRefusal(response: Response): Promise<Refusal> {
  let body: Partial<Refusal> = {};
  try { body = (await response.json()) as Partial<Refusal>; } catch { /* the status is enough */ }
  return {
    status: response.status,
    error: typeof body.error === 'string' && body.error ? body.error : 'The Blackbox request failed (' + response.status + ').',
    reason: typeof body.reason === 'string' && body.reason ? body.reason : 'other',
    what: body.what, next: body.next, state: body.state, existing: body.existing
  };
}

/**
 * The drives of this PC, one call in the same fetch and error style as blackboxApi: no call without a
 * session token, X-Audit-Token on the request, the session dropped when the token is refused, and a
 * BlackboxError for any other answer.
 */
export async function fetchDrives(): Promise<DrivesAnswer> {
  const token = readSessionToken();
  if (!token) throw new BlackboxError({ status: 401, error: NO_SESSION_MESSAGE, reason: 'no_session' });
  let response: Response;
  try {
    response = await fetch(base + '/this-pc/drives', { method: 'GET', credentials: 'same-origin', headers: { 'X-Audit-Token': token } });
  } catch (error) {
    if (error instanceof DOMException && error.name === 'AbortError') throw error;
    throw new BlackboxError({ status: 0, error: 'Unravis could not be reached. Check that it is still running.', reason: 'offline' });
  }
  if (response.status === 401) {
    forgetSession();
    throw new BlackboxError({ status: 401, error: NO_SESSION_MESSAGE, reason: 'no_session' });
  }
  if (!response.ok) throw new BlackboxError(await readRefusal(response));
  return (await response.json()) as DrivesAnswer;
}

/* ---------------------------------------------------------------- rules */

/**
 * The drives the Folder step shows as cards, best first (S-11). Fixed drives that are not the Windows
 * drive come first: they are the only candidates for the default folder, and the first entry is one
 * whenever the machine has one. The Windows drive stays in the list, after them, marked by its own
 * `system` flag. USB and network drives are listed last, flagged by their own `type`, and never sit
 * above a plain fixed drive. A drive of any other type (a CD drive, say) is left out. The list given
 * is never changed.
 */
export function eligibleDrives(drives: DriveInfo[]): DriveInfo[] {
  const listed = drives.filter((d) => d.system || d.type === 'fixed' || d.type === 'removable' || d.type === 'network');
  const plainFixed = listed.filter((d) => d.type === 'fixed' && !d.system);
  const windows = listed.filter((d) => d.system);
  const removable = listed.filter((d) => d.type === 'removable' && !d.system);
  const network = listed.filter((d) => d.type === 'network' && !d.system);
  return [...plainFixed, ...windows, ...removable, ...network];
}
