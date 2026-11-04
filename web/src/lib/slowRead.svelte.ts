import { SLOW_READ_MS } from './slowRead';

/**
 * Tracks whether a read has been running for a while. Call it while a component is being set up: it registers an effect that
 * starts a timer when `isLoading()` turns true and clears it (and the flag) when the read ends or the component goes away.
 */
export function createSlowRead(isLoading: () => boolean, delayMs: number = SLOW_READ_MS): { readonly slow: boolean } {
  let slow = $state(false);
  $effect(() => {
    if (!isLoading()) { slow = false; return; }
    const timer = setTimeout(() => { slow = true; }, delayMs);
    return () => { clearTimeout(timer); slow = false; };
  });
  return { get slow() { return slow; } };
}
