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
