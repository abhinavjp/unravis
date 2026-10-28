/**
 * A one-time request to open the "restore from a case file" part of the Enter card. The page for a case that cannot be opened has no
 * restore form of its own, so "Restore from a case file" leaves this here and goes to the start screen; the card takes it once when it
 * appears. Taking it clears it. (The same pattern as startIntent.ts.)
 */
let requested = false;

export function requestRestore(): void { requested = true; }

export function takeRestoreIntent(): boolean {
  const was = requested;
  requested = false;
  return was;
}
