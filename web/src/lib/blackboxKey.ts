// The read key of the Blackbox on this PC is shown only in the moment it is asked for (A33, wireframe 65). These helpers
// hold nothing: the key goes from the answer to the screen or the clipboard and is not kept anywhere else.

/** How long the key stays on the screen before it is hidden again. */
export const KEY_SHOW_MS = 30000;

/** Fetches the key, puts it on the clipboard and lets it go. Never throws. */
export async function copyKeyOnce(fetchKey: () => Promise<string>, write: (text: string) => Promise<void>): Promise<'copied' | 'no-key' | 'no-clipboard'> {
  let key = '';
  try {
    key = await fetchKey();
  } catch {
    return 'no-key';
  }
  if (!key) return 'no-key';
  try {
    await write(key);
    return 'copied';
  } catch {
    return 'no-clipboard';
  } finally {
    key = '';
  }
}

/**
 * A token for one Show. A late answer may set the key only while its token is still the current one: Hide, the 30 second
 * timer and closing the screen each call `cancel`, so an answer that lands after any of them is dropped.
 */
export function makeRevealGuard(): { next: () => number; valid: (token: number) => boolean; cancel: () => void } {
  let current = 0;
  return {
    next: () => ++current,
    valid: (token: number) => token === current,
    cancel: () => { current++; }
  };
}
