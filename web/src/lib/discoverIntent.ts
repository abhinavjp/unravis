/**
 * A one-time request to open the import drawer with the "This computer" panel showing. The start screen has no case and no drawer of its
 * own, so its button leaves this request and opens the drawer; the drawer takes the request once when it opens. Taking it clears it, so
 * opening the drawer later by another route does not show the panel.
 */
let requested = false;

export function requestDiscover(): void { requested = true; }

export function takeDiscoverIntent(): boolean {
  const was = requested;
  requested = false;
  return was;
}
