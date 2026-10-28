import type { ConnectionWord } from '../../lib/lanes';

// The last test of each saved connection, kept in memory only while the app is open, so leaving the connections page and coming back
// does not reset every row to "Not tested". It holds a word, a time and a sentence, nothing secret.
export type TestResult = { word: ConnectionWord; at: string; problem: string };

let kept: Record<string, TestResult> = {};
export const recallTests = (): Record<string, TestResult> => kept;
export function rememberTest(key: string, result: TestResult): Record<string, TestResult> {
  kept = { ...kept, [key]: result };
  return kept;
}
export function dropTest(key: string): Record<string, TestResult> {
  const next = { ...kept };
  delete next[key];
  kept = next;
  return kept;
}
export function resetTests(): void { kept = {}; }

/** The saved connection lists changed (one was forgotten, saved or edited). Other panels that show the list re-read it. */
export const CONNECTIONS_CHANGED = 'unravis:connections-changed';
export function announceConnectionsChanged(): void {
  if (typeof window !== 'undefined') window.dispatchEvent(new Event(CONNECTIONS_CHANGED));
}
export function onConnectionsChanged(listener: () => void): () => void {
  if (typeof window === 'undefined') return () => {};
  window.addEventListener(CONNECTIONS_CHANGED, listener);
  return () => window.removeEventListener(CONNECTIONS_CHANGED, listener);
}
