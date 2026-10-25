/**
 * A one-time request to open the import panel on a chosen case. The start screen has no case panel of its own, so "Use an existing case"
 * leaves the case id here and opens the panel; the panel takes it once when it opens. Taking it clears it, so opening the panel later
 * by another route does not jump to an old choice. (The same pattern as discoverIntent.ts.)
 */
let requestedCase = '';

export function requestCase(id: string): void { requestedCase = id; }

export function takeCaseIntent(): string {
  const was = requestedCase;
  requestedCase = '';
  return was;
}
