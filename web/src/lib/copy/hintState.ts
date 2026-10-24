/**
 * Shared state for the "?" hints (P14a). Pure and DOM-free so it can be tested on its own.
 *  - Only one hint is open at a time: opening one closes the others.
 *  - A hint's "Learn more in the glossary" button asks the page to open the glossary on one term, through a window event
 *    (the popover is moved to <body>, so a bubbled event would never reach the app).
 */

export interface HintBus {
  /** The id of the open hint, or null. */
  current(): string | null;
  /** Open this hint. Any other open hint closes. */
  open(id: string): void;
  /** Close this hint only if it is the open one (a late close from a hint that was already replaced does nothing). */
  close(id: string): void;
  /** Called with the open id (or null) after every change. Returns the unsubscribe function. */
  subscribe(listener: (id: string | null) => void): () => void;
}

export function createHintBus(): HintBus {
  let current: string | null = null;
  const listeners = new Set<(id: string | null) => void>();
  const set = (id: string | null) => {
    if (id === current) return;
    current = id;
    for (const listener of [...listeners]) listener(current);
  };
  return {
    current: () => current,
    open: id => set(id),
    close: id => { if (current === id) set(null); },
    subscribe(listener) { listeners.add(listener); return () => { listeners.delete(listener); }; },
  };
}

/** The one bus every Hint on the page shares. */
export const hintBus = createHintBus();

/** Window event a hint sends to open the glossary on one term. `detail` is `{ termId }`. */
export const GLOSSARY_EVENT = 'unravis:open-glossary';

/** The term id carried by a glossary request, or null when the event has no usable id. */
export function glossaryTermFrom(detail: unknown): string | null {
  const id = (detail as { termId?: unknown } | null | undefined)?.termId;
  return typeof id === 'string' && id ? id : null;
}
