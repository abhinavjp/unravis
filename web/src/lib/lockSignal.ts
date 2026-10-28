/**
 * One line from the case requests to the app: "the server says the master key is locked". cases.ts calls `signalCasesLocked` when a case
 * route answers 423; appState.svelte.ts listens and moves to the unlock screen. It carries no data, and it lives apart from both so
 * neither imports the other.
 */
let listener: (() => void) | null = null;

/** Registers the one listener. Returns a function that removes it (only if it is still the registered one). */
export function onCasesLocked(fn: () => void): () => void {
  listener = fn;
  return () => { if (listener === fn) listener = null; };
}

export function signalCasesLocked(): void {
  listener?.();
}

/** The status a case route uses when the master key is locked. */
export const LOCKED_STATUS = 423;

/**
 * A 423 can also mean one case's own key is not unlocked (reason `case-locked`) or was typed wrong (`wrong-case-key`). Those
 * belong to the case's key card, not to the lock screen of the whole app.
 */
export const CASE_KEY_REASONS: ReadonlySet<string> = new Set(['case-locked', 'wrong-case-key']);

/** True when a 423 body carries a case key reason. */
export function isCaseKeyReason(reason: unknown): boolean {
  return typeof reason === 'string' && CASE_KEY_REASONS.has(reason);
}

/**
 * For a 423 response: tell the app the master key is locked, unless the answer is about one case's own key. The body is read from a
 * clone, so the caller still reads the original.
 */
export async function signalIfAppLocked(response: Response): Promise<void> {
  if (response.status !== LOCKED_STATUS) return;
  let reason: unknown = '';
  try {
    const body = await response.clone().json() as { reason?: unknown } | null;
    reason = body && typeof body === 'object' ? body.reason : '';
  } catch { /* an unreadable body is the app lock, as before */ }
  if (!isCaseKeyReason(reason)) signalCasesLocked();
}
