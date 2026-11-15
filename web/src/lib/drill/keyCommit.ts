// Keyboard moves on the overview strip (Slow call screen): the box previews at once, but the window is read only once the keys stop.
// Pure TypeScript: no Svelte, no DOM. The timer is looked up when it is used, so a test can fake it.

import { KEY_STEP_FRACTION, centreFromFraction } from './callMap';

/** How long the keys must be quiet before the move is committed (and the reads start). */
export const KEY_COMMIT_DELAY_MS = 250;

/** Page Up / Page Down move the box this share of the strip. */
export const PAGE_STEP_FRACTION = 0.25;

export interface KeyCommit<T> {
  /** Remembers the newest value and restarts the wait. Earlier values are dropped. */
  push(value: T): void;
  /** Commits the pending value now, if there is one. */
  flush(): void;
  /** Drops the pending value without committing. */
  cancel(): void;
  pending(): boolean;
}

export function createKeyCommit<T>(commit: (value: T) => void, delayMs: number = KEY_COMMIT_DELAY_MS): KeyCommit<T> {
  let timer: ReturnType<typeof setTimeout> | null = null;
  let value: T | undefined;
  let has = false;

  const drop = (): void => {
    if (timer !== null) clearTimeout(timer);
    timer = null;
  };
  const fire = (): void => {
    drop();
    if (!has) return;
    const v = value as T;
    has = false;
    value = undefined;
    commit(v);
  };

  return {
    push(v) {
      value = v;
      has = true;
      drop();
      timer = setTimeout(fire, delayMs);
    },
    flush: fire,
    cancel() {
      drop();
      has = false;
      value = undefined;
    },
    pending: () => has
  };
}

/**
 * Where a strip key moves the window centre to (ms), or null when the key is not a strip key.
 * Arrows: a small step. Page Up / Page Down: a bigger step (Page Up goes later, like a slider value going up).
 * Home: back to the middle of the call. End: the end of the strip. Moves stay inside the strip.
 */
export function stripKeyTarget(
  key: string,
  centreMs: number,
  overview: { start: number; end: number },
  callMidMs: number
): number | null {
  const span = overview.end - overview.start;
  const step = (fraction: number): number => {
    const from = span > 0 ? (centreMs - overview.start) / span : 0;
    return centreFromFraction(overview, from + fraction);
  };
  switch (key) {
    case 'ArrowLeft': return step(-KEY_STEP_FRACTION);
    case 'ArrowRight': return step(KEY_STEP_FRACTION);
    case 'PageDown': return step(-PAGE_STEP_FRACTION);
    case 'PageUp': return step(PAGE_STEP_FRACTION);
    case 'Home': return callMidMs;
    case 'End': return overview.end;
    default: return null;
  }
}
