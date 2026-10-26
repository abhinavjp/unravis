import type { ConnectionInput } from './types';

/**
 * Where the wizard keeps a MongoDB connection the person tested, until the case is made (TSK-19) or the wizard is left.
 * It is a plain module-level map: not reactive, never written to the draft, the URL, storage or a log. The draft refers to a connection by an
 * id only (see databaseRef in wizardSources.ts). This is the same kind of in-memory hold the existing connect flow uses for a typed password.
 *
 * TSK-19 reads a connection with `heldConnection(id)` to open the databases classified as audit through the existing connect flow, and calls
 * `releaseConnections()` when it is done (resetWizard does too).
 */

const held = new Map<string, ConnectionInput>();
/** The collection that holds the API audit log, per held connection. It is not secret. */
const collections = new Map<string, string>();

export const DEFAULT_COLLECTION = 'ApiAuditLog';

const ID_ALPHABET = 'abcdefghijklmnopqrstuvwxyz0123456789';

/** A new id of 12 letters and digits. Uses the browser's random source when there is one. */
export function newConnectionId(): string {
  const bytes = new Uint8Array(12);
  const c = (globalThis as { crypto?: { getRandomValues?: (a: Uint8Array) => Uint8Array } }).crypto;
  if (c?.getRandomValues) c.getRandomValues(bytes);
  else for (let i = 0; i < bytes.length; i++) bytes[i] = Math.floor(Math.random() * 256);
  let id = '';
  for (const byte of bytes) id += ID_ALPHABET[byte % ID_ALPHABET.length];
  return id;
}

/** Keeps a copy of the connection and returns its id. */
export function holdConnection(input: ConnectionInput, id: string = newConnectionId()): string {
  held.set(id, { ...input });
  return id;
}

/** A copy of the held connection, or null. For the create step only. */
export function heldConnection(id: string): ConnectionInput | null {
  const found = held.get(id);
  return found ? { ...found } : null;
}

export function holdCollection(id: string, collection: string): void {
  collections.set(id, collection.trim() === '' ? DEFAULT_COLLECTION : collection.trim());
}

export const heldCollection = (id: string): string => collections.get(id) ?? DEFAULT_COLLECTION;

export function releaseConnection(id: string): void {
  held.delete(id);
  collections.delete(id);
}

/** Forgets every held connection. */
export function releaseConnections(): void {
  held.clear();
  collections.clear();
}

export const heldCount = (): number => held.size;

/** The strings of a connection that must never appear in a message: the address, user name, password and certificate. */
export function secretsOf(input: Pick<ConnectionInput, 'uri' | 'username' | 'password' | 'caCertificate'>): string[] {
  return [input.uri, input.username, input.password, input.caCertificate].map((s) => s.trim()).filter((s) => s !== '');
}
