import type { RecentConnection, Source } from './types';

/**
 * Which saved connection a connected source came from. A source on the server does not say; the page learns it when it adds the source
 * (from the saved record in use, or by saving the connection's details) and keeps it here for the tab. The case workspace names a
 * connection by this id, never by an address or a login.
 *
 * Holds only opaque ids: a source id (or its connection id) and the id of a saved record. No address, user name or password.
 */

export const SOURCE_SAVED_KEY = 'unravis.sourceSaved.v1';
const MAX_ENTRIES = 64;

type Registry = Record<string, string>;
type Store = Pick<Storage, 'getItem' | 'setItem'>;

let memory: Registry | null = null;

function defaultStore(): Store | null {
  try { return typeof sessionStorage === 'undefined' ? null : sessionStorage; } catch { return null; }
}

function readFrom(store: Store | null): Registry {
  try {
    const raw = store?.getItem(SOURCE_SAVED_KEY);
    const parsed: unknown = raw ? JSON.parse(raw) : {};
    if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) return {};
    const out: Registry = {};
    for (const [key, value] of Object.entries(parsed as Record<string, unknown>)) {
      if (typeof value === 'string' && value && key.length <= 256 && value.length <= 128) out[key] = value;
    }
    return out;
  } catch { return {}; }
}

function load(store: Store | null): Registry {
  if (!memory) memory = readFrom(store);
  return memory;
}

/** Remembers that these sources (and their connection) came from the saved record `savedId`. */
export function registerSourceSaved(source: Pick<Source, 'id' | 'connectionId'>, savedId: string, store: Store | null = defaultStore()): void {
  if (!savedId || savedId.length > 128) return;
  const registry = load(store);
  registry[source.id] = savedId;
  if (source.connectionId) registry[source.connectionId] = savedId;
  const keys = Object.keys(registry);
  for (const key of keys.slice(0, Math.max(0, keys.length - MAX_ENTRIES))) delete registry[key];
  try { store?.setItem(SOURCE_SAVED_KEY, JSON.stringify(registry)); } catch { /* the tab simply does not remember it */ }
}

/** Forgets a source that was removed. */
export function forgetSourceSaved(source: Pick<Source, 'id'>, store: Store | null = defaultStore()): void {
  const registry = load(store);
  if (!(source.id in registry)) return;
  delete registry[source.id];
  try { store?.setItem(SOURCE_SAVED_KEY, JSON.stringify(registry)); } catch { /* nothing to do */ }
}

/** Clears the in-memory copy (tests, and a page that starts over). */
export function resetSourceSaved(): void { memory = null; }

/**
 * The saved connection a source came from: the one registered for it, else the one registered for its connection, else the only saved record
 * whose name is the source's connection name. '' when it cannot be told.
 */
export function savedIdFor(source: Pick<Source, 'id' | 'connectionId' | 'connectionLabel'>, recents: readonly Pick<RecentConnection, 'id' | 'label'>[], store: Store | null = defaultStore()): string {
  const registry = load(store);
  const known = registry[source.id] || (source.connectionId ? registry[source.connectionId] : '');
  if (known) return known;
  const name = (source.connectionLabel ?? '').trim();
  if (!name) return '';
  const named = recents.filter(item => item.label.trim() === name);
  return named.length === 1 ? named[0].id : '';
}

/** Forgets every source that was registered under a saved record that has been forgotten. */
export function forgetSavedRecord(savedId: string, store: Store | null = defaultStore()): void {
  const registry = load(store);
  let changed = false;
  for (const key of Object.keys(registry)) if (registry[key] === savedId) { delete registry[key]; changed = true; }
  if (!changed) return;
  try { store?.setItem(SOURCE_SAVED_KEY, JSON.stringify(registry)); } catch { /* nothing to do */ }
}

export interface RecentDetails {
  uri: string; username: string;
  /** The audit collection. A saved record is one address with one collection, so a different collection is a different record. */
  collection?: string;
  authSource?: string; tlsMode?: string; caCertificate?: string;
}

/**
 * The saved record that matches the details just used. Saving connection details REPLACES the record at the same address, user, sign-in
 * database, TLS setting, certificate and collection, so a record that would be replaced is always found here: every one of those that is
 * given has to be equal. A record with only a similar name is not a match (it may be another server). Recents come newest first.
 */
export function matchRecent(details: RecentDetails, recents: readonly RecentConnection[]): RecentConnection | null {
  const uri = details.uri.trim();
  const username = details.username.trim();
  const collection = details.collection?.trim();
  return recents.find(item => item.uri.trim() === uri && item.username === username
    && (collection === undefined || item.collection.trim() === collection)
    && (details.authSource === undefined || item.authSource === details.authSource)
    && (details.tlsMode === undefined || item.tlsMode === details.tlsMode)
    && (details.caCertificate === undefined || item.caCertificate === details.caCertificate)) ?? null;
}

export interface ResolveSavedInput {
  details: RecentDetails;
  /** The saved record the connection was opened from, when known. Checked against the current list, never trusted on its own. */
  savedId?: string;
  /** The list of saved records the page holds now. */
  recents: readonly RecentConnection[];
  /** Reads the list again. null when it cannot be read: then nothing is made, because a record might already be there. */
  refresh: () => Promise<readonly RecentConnection[] | null>;
  /** Saves the details as a new record (without a password). Only called when no record matches. */
  create: () => Promise<RecentConnection | null>;
}

/**
 * The saved record for a connection that was just used: the one in use, else the one that matches, else a new one. Saving over a record that
 * exists would replace it, and so wipe its stored password, so it never creates while one matches. The list is read again before a record is
 * made, since the one held may be old; when it cannot be read nothing is made. Never throws.
 */
export async function resolveSavedRecent(input: ResolveSavedInput): Promise<{ recent: RecentConnection | null; created: boolean; recents: readonly RecentConnection[] }> {
  const find = (list: readonly RecentConnection[]): RecentConnection | null =>
    (input.savedId ? list.find(item => item.id === input.savedId) ?? null : null) ?? matchRecent(input.details, list);
  let list = input.recents;
  const known = find(list);
  if (known) return { recent: known, created: false, recents: list };
  try {
    const fresh = await input.refresh();
    if (!fresh) return { recent: null, created: false, recents: list };
    list = fresh;
  } catch { return { recent: null, created: false, recents: list }; }
  const found = find(list);
  if (found) return { recent: found, created: false, recents: list };
  try {
    const made = await input.create();
    return { recent: made, created: !!made, recents: made ? [made, ...list.filter(item => item.id !== made.id)] : list };
  } catch { return { recent: null, created: false, recents: list }; }
}

/** A readable default name for a connection from its address: the host name, else the text before the first path mark. */
export function suggestedConnectionLabel(uri: string): string {
  const address = uri.trim();
  try {
    const parsed = new URL(address.includes('://') ? address : `mongodb://${address}`);
    if (parsed.hostname) return parsed.hostname;
  } catch { /* backend validation gives the address error */ }
  return address.split(/[/?#]/, 1)[0] || 'MongoDB connection';
}
