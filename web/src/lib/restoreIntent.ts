/**
 * A one-time request to open the "restore from a case file" part of the Enter card. The page for a case that cannot be opened has no
 * restore form of its own, so "Restore from a case file" leaves this here and goes to the start screen; the card takes it once when it
 * appears. Taking it clears it. (The same pattern as startIntent.ts.)
 */
let requested = false;

const listeners = new Set<() => void>();

/** A card that is already on screen listens here; a request then reaches it at once instead of waiting for the next time the card appears. */
export function onRestoreRequest(fn: () => void): () => void {
  listeners.add(fn);
  return () => { listeners.delete(fn); };
}

export function requestRestore(): void {
  if (listeners.size === 0) { requested = true; return; }
  for (const fn of [...listeners]) fn();
}

export function takeRestoreIntent(): boolean {
  const was = requested;
  requested = false;
  return was;
}
