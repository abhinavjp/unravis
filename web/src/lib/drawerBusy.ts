/**
 * The one rule for closing a drawer that may be busy (RV-UI-13): while it is saving something nothing closes it, whether the
 * request came from Esc, the dimmed page, the close button or the page-level Esc. Returns true when it closed.
 */
export function closeUnlessBusy(busy: boolean, close: () => void): boolean {
  if (busy) return false;
  close();
  return true;
}
