import { tick } from 'svelte';
import { codeModeFlow } from './codeModeFlow.svelte';

/**
 * Whether the code-mode popup is on screen. The popup is a modal like the import popup: the page behind it is inert (`panels.modalOpen`),
 * and closing it puts the keyboard back where it was. It opens on the Result step when code mode is on (see `CodeModeFlow.open`).
 */

let open = $state(false);
let returnFocus: HTMLElement | null = null;

export const codeUi = {
  get open() { return open; }
};

/** Opens the popup. `from` is the button the person used; the keyboard goes back to it when the popup closes. */
export function openCodeMode(from: HTMLElement | null = null): void {
  returnFocus = from ?? (typeof document !== 'undefined' && document.activeElement instanceof HTMLElement ? document.activeElement : null);
  codeModeFlow.open();
  open = true;
}

export async function closeCodeMode(): Promise<void> {
  open = false;
  await tick();
  // The badge is gone when code mode was just turned off: the keyboard then goes to the page itself.
  const back = returnFocus && returnFocus.isConnected ? returnFocus : document.getElementById('main-content');
  back?.focus();
  returnFocus = null;
}
