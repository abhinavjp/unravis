import { CaseApiError, getHostEntries, putHostEntries } from './cases';
import type { HostEntry } from './cases';
import { labelProblem, zoneNameProblem } from './zoneChoice';

/**
 * The list of computers the "Which computer wrote it?" choice offers (packet P4). The list is the server's own (GET and PUT /api/hosts), shared
 * by every case. The wizard reads it, and adds one computer at a time. Adding one reads the list again first, so a computer someone else
 * saved a moment ago is never overwritten. In memory only.
 */

export interface HostsApi {
  get(): Promise<HostEntry[]>;
  put(entries: HostEntry[]): Promise<HostEntry[]>;
}

export const realHostsApi: HostsApi = { get: () => getHostEntries(), put: (entries) => putHostEntries(entries) };

export interface HostsView {
  /** The computers in the list, or null before the first read (or after one that failed). */
  entries: HostEntry[] | null;
  loading: boolean;
  /** The last read failed. */
  failed: boolean;
}

export const hostsView = $state<HostsView>({ entries: null, loading: false, failed: false });

let inflight: Promise<void> | null = null;

/** Reads the list. Safe to call from many rows at once: they share one read. `force` reads again even when the list is known. */
export function loadHosts(force = false, api: HostsApi = realHostsApi): Promise<void> {
  if (inflight) return inflight;
  if (hostsView.entries !== null && !force) return Promise.resolve();
  hostsView.loading = true;
  inflight = (async () => {
    try {
      hostsView.entries = await api.get();
      hostsView.failed = false;
    } catch {
      hostsView.failed = true;
    } finally {
      hostsView.loading = false;
      inflight = null;
    }
  })();
  return inflight;
}

export type SaveHostResult = { ok: true; label: string } | { ok: false; reason: 'label' | 'zone' | 'taken' | 'locked' | 'failed' };

const blankEntry = (label: string, tz: string): HostEntry => ({ label, aliases: [], role: '', tz, logicalCores: null, runtime: '' });

const failure = (error: unknown): SaveHostResult => ({ ok: false, reason: error instanceof CaseApiError && error.status === 423 ? 'locked' : 'failed' });

/** Saves go one after another: each reads the list and writes it back, so two at once would have the second write drop the first computer. */
let writeTail: Promise<unknown> = Promise.resolve();

/** Runs `work` after the save before it has finished. A failed save does not block the next one; the caller still gets its own result. */
function queued<T>(work: () => Promise<T>): Promise<T> {
  const run = writeTail.then(work);
  writeTail = run.then(
    () => undefined,
    () => undefined
  );
  return run;
}

/**
 * Adds a computer to the list. The name and zone are checked first, with the server's own rules. `zoneOptional` lets the zone stay empty
 * (a kind that only labels UTC times needs no zone). A name already in the list is refused, not replaced.
 */
export async function saveHost(label: string, tz: string, zoneOptional: boolean, api: HostsApi = realHostsApi): Promise<SaveHostResult> {
  const name = label.trim();
  const zone = tz.trim();
  if (labelProblem(name, null) !== '') return { ok: false, reason: 'label' };
  if (!(zoneOptional && zone === '') && zoneNameProblem(zone) !== '') return { ok: false, reason: 'zone' };
  try {
    return await queued(async () => {
      const fresh = await api.get();
      if (labelProblem(name, fresh) === 'taken') { hostsView.entries = fresh; return { ok: false, reason: 'taken' } as const; }
      hostsView.entries = await api.put([...fresh, blankEntry(name, zone)]);
      hostsView.failed = false;
      return { ok: true, label: name } as const;
    });
  } catch (error) {
    return failure(error);
  }
}

/** Gives a computer in the list a time zone (it had none). The rest of its entry is kept as it is. */
export async function saveHostZone(label: string, tz: string, api: HostsApi = realHostsApi): Promise<SaveHostResult> {
  const zone = tz.trim();
  if (zoneNameProblem(zone) !== '') return { ok: false, reason: 'zone' };
  try {
    return await queued(async () => {
      const fresh = await api.get();
      if (!fresh.some((entry) => entry.label === label)) return { ok: false, reason: 'failed' } as const;
      hostsView.entries = await api.put(fresh.map((entry) => (entry.label === label ? { ...entry, tz: zone } : entry)));
      hostsView.failed = false;
      return { ok: true, label } as const;
    });
  } catch (error) {
    return failure(error);
  }
}

/** Forgets the list (a test, or after the master key was locked). */
export function resetHosts(): void {
  hostsView.entries = null;
  hostsView.loading = false;
  hostsView.failed = false;
  inflight = null;
  // Drop the queue too: a save still in flight after a reset must not hold the next one back.
  writeTail = Promise.resolve();
}
