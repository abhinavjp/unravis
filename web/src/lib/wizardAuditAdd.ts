import { ApiError } from './api';
import { DEFAULT_COLLECTION } from './wizardConnections';

/**
 * Adding the audit database a person classified in the create wizard (TSK-19). The wizard knows the database but often not the collection
 * that holds its audit log (the default name is only a guess). The server now says exactly what is wrong with a collection:
 *  - `collection_missing`: the database has no collection of that name. `collections` lists the audit-looking ones.
 *  - `permission_denied`: the account may not read it. `collections` lists the audit-looking ones it may read (MongoDB 4.0 and newer).
 *  - `source-exists` (HTTP 409): this database and collection are already a source. That is the wanted end state, not a failure.
 * One more name is tried only when:
 *  - the name the server offers is the one asked for in another spelling (a case difference means the same thing), whatever name was asked
 *    for, or
 *  - the asked-for name is the default guess (DEFAULT_COLLECTION) and exactly one audit-looking collection is offered.
 * A name the person typed is never replaced by a different one: more than one offered name, or a typed name the server does not know, is a
 * choice the person makes, so the failure is kept and named (the offered names travel with it). When the second try fails too, the FIRST
 * failure is the one reported: it is the one about the name the person asked for.
 */
export type AddAuditResult<S> =
  | { kind: 'added'; source: S; collection: string; requestedCollection: string }
  | { kind: 'exists'; collection: string; requestedCollection: string };

const RETRY_CODES: readonly string[] = ['collection_missing', 'permission_denied'];

/** The one collection worth trying after `tried` failed, or '' when there is none or more than one. */
export function singleCandidate(error: unknown, tried: string): string {
  if (!(error instanceof ApiError) || !error.code || !RETRY_CODES.includes(error.code)) return '';
  const others = [...new Set((error.collections ?? []).filter((name) => name !== '' && name.toLowerCase() !== tried.toLowerCase()))];
  return others.length === 1 ? others[0] : '';
}

/** True when `name` is the collection asked for in another spelling: the same collection, so the server's own spelling wins. */
const sameNameOtherCase = (name: string, tried: string): boolean => name !== '' && name !== tried && name.toLowerCase() === tried.toLowerCase();

/** The one name to try after `tried` failed, or '' to keep the failure as it is. */
function retryCollection(error: unknown, tried: string, defaultGuess: string): string {
  if (!(error instanceof ApiError) || !error.code || !RETRY_CODES.includes(error.code)) return '';
  const spelled = (error.collections ?? []).find((name) => sameNameOtherCase(name, tried));
  if (spelled) return spelled;
  // A name the person typed is never replaced by a different one; only the untyped default guess gives way to one audit-looking alternative.
  if (tried !== defaultGuess) return '';
  return singleCandidate(error, tried);
}

export const isSourceExists = (error: unknown): boolean => error instanceof ApiError && error.code === 'source-exists';

/**
 * Adds the source with `collection`, and once more with a corrected name when the rules above allow it. The name that was asked for comes
 * back next to the one that worked, so the screen can say when they differ.
 */
export async function addAuditWithFallback<S>(add: (collection: string) => Promise<S>, collection: string, defaultGuess: string = DEFAULT_COLLECTION): Promise<AddAuditResult<S>> {
  const requested = collection;
  let tried = collection;
  let firstError: unknown = null;
  for (let attempt = 0; ; attempt++) {
    try {
      return { kind: 'added', source: await add(tried), collection: tried, requestedCollection: requested };
    } catch (error) {
      if (isSourceExists(error)) return { kind: 'exists', collection: tried, requestedCollection: requested };
      if (attempt > 0) throw firstError ?? error; // the retry failed too: the name that was asked for is the one the report tells about
      const next = retryCollection(error, tried, defaultGuess);
      if (next === '') throw error;
      firstError = error;
      tried = next;
    }
  }
}
