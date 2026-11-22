/**
 * Where the keyboard is and what the tab is called after the app moves to another screen (SETUP-A11Y-03).
 * A control that was pressed goes away with its screen, and focus then drops to the page body: the next Tab starts again at the skip
 * link and costs a full pass through the header. So after a move the new screen's first heading takes the focus (or the main area while
 * the screen is still loading), but only when focus was lost: a screen that already put focus somewhere (the create guide's step
 * heading, a drawer's title) is left alone. Pure helpers here; App.svelte runs them.
 */
import type { Screen } from './screenFlow';

/** The name a screen gives the tab ("All cases - Unravis"). The case page names itself (the case title) and returns ''. */
export function screenTitle(screen: Screen, depth = 0): string {
  switch (screen) {
    case 'start': return 'All cases';
    case 'create': return 'New case';
    case 'unlock': return 'Unlock';
    case 'setup': return 'Master key';
    case 'running': return depth === 1 ? 'Where is it?' : depth === 2 ? 'Evidence' : depth >= 3 ? 'Record' : 'Analysis';
    default: return '';
  }
}

/** A key for "the place the person is at": a change of key is a move worth moving focus for. Depth only counts on the analysis screen. */
export function placeKey(screen: Screen, depth: number): string {
  return screen === 'running' ? screen + ':' + depth : screen;
}

/** True when nothing useful holds focus: the page body, the document itself, nothing, a node that left the page, or the main area we parked on. */
export function focusWasLost(active: Element | null, body: Element | null, main: Element | null): boolean {
  if (!active || active === body) return true;
  if (active === main) return true;
  if (active.tagName === 'HTML') return true;
  return (active as { isConnected?: boolean }).isConnected === false;
}

type Doc = {
  activeElement: Element | null;
  body: Element | null;
  getElementById(id: string): HTMLElement | null;
};

/** The first heading of the main area that is on screen (not hidden, not inside an inert or collapsed view). */
export function firstHeading(main: ParentNode): HTMLElement | null {
  const found = [...main.querySelectorAll<HTMLElement>('h1, h2, h3')];
  return found.find((heading) => heading.getClientRects().length > 0 && !heading.closest('[inert], [hidden], [aria-hidden="true"], .off, .visually-hidden')) ?? null;
}

/**
 * After a move: wait for the screen to draw, then give focus to its first heading when focus was lost; while no heading is there yet
 * (a lazy screen still loading) park on the main area and look again a few times. Stops as soon as someone else has taken focus, or a
 * modal is open. Returns a function that cancels it.
 */
export function settleFocus(doc: Doc = document, tries = 12, wait = 80): () => void {
  let cancelled = false;
  let timer: ReturnType<typeof setTimeout> | undefined;
  const attempt = (left: number) => {
    if (cancelled) return;
    const main = doc.getElementById('main-content');
    if (!main || main.hasAttribute('inert')) return;
    if (!focusWasLost(doc.activeElement, doc.body, main)) return;
    const heading = firstHeading(main);
    if (heading) {
      if (!heading.hasAttribute('tabindex')) heading.tabIndex = -1;
      heading.focus({ preventScroll: true });
      return;
    }
    if (doc.activeElement !== main) main.focus({ preventScroll: true });
    if (left > 0) timer = setTimeout(() => attempt(left - 1), wait);
  };
  timer = setTimeout(() => attempt(tries), 0);
  return () => { cancelled = true; clearTimeout(timer); };
}
