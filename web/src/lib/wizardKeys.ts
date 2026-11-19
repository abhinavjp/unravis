// Keyboard rules for the Blackbox wizard pop-up. Pure TypeScript with no Svelte or DOM imports, so the node tests can load it.
// WizardDialog.svelte calls these; the decisions live here so they can be tested without a browser.

// The few things we read off a focused element. A real HTMLElement fits; a plain object fits in a test.
export interface KeyTarget {
  tagName?: string | null;
  type?: string | null;
  isContentEditable?: boolean;
  getAttribute?: (name: string) => string | null;
  closest?: (selector: string) => unknown;
}

// Elements whose own Enter must keep working: a button presses, a link opens, a summary toggles, a text area adds a line, a select opens.
const OWN_ENTER_TAGS = new Set(['BUTTON', 'SUMMARY', 'TEXTAREA', 'SELECT', 'A', 'DETAILS']);
// Input types that act like a button.
const BUTTON_INPUT_TYPES = new Set(['button', 'submit', 'reset', 'image', 'file', 'color']);
// ARIA roles that act on Enter.
const OWN_ENTER_ROLES = new Set(['button', 'link', 'menuitem', 'tab', 'combobox', 'listbox', 'option', 'switch']);

// True when Enter should run the dialog's primary action (Next, Done).
// False when the focused element handles Enter itself, or when the owner marked it with data-own-enter (or sits inside something marked).
// `needsButton` is for the last step, where the primary action really installs or builds something: then Enter never runs it from
// a heading, the body or a field. Only the Place or Build button itself acts, through its own native click, so a stray Enter
// (for example one left over from the Next press that opened the step) can never start the work.
export function enterDoesPrimary(target: KeyTarget | null | undefined, needsButton = false): boolean {
  if (needsButton) return false;
  if (!target) return true;
  const tag = (target.tagName ?? '').toUpperCase();
  if (OWN_ENTER_TAGS.has(tag)) return false;
  if (tag === 'INPUT' && BUTTON_INPUT_TYPES.has((target.type ?? '').toLowerCase())) return false;
  if (target.isContentEditable) return false;
  const role = target.getAttribute?.('role');
  if (role && OWN_ENTER_ROLES.has(role.toLowerCase())) return false;
  if (target.getAttribute?.('data-own-enter') != null) return false;
  if (target.closest?.('[data-own-enter]')) return false;
  return true;
}

// What Esc (and the close button) does.
//   'note'  busy: do nothing except say why, so work in progress is never cut off
//   'keep'  the leave question is already showing: Esc answers it with "keep going"
//   'leave' the wizard has finished, so there is nothing to lose: leave at once
//   'ask'   otherwise: show the leave question first
export type EscapeAction = 'note' | 'keep' | 'leave' | 'ask';

export function escapeAction(busy: boolean, finished: boolean, asking = false): EscapeAction {
  if (busy) return 'note';
  if (asking) return 'keep';
  if (finished) return 'leave';
  return 'ask';
}

// Tab trap: where focus goes next so it stays inside a list of `count` focusable elements.
// Returns the index to focus, or null when the browser should move focus on its own (a normal step inside the list).
// `index` is the focused element's position, or -1 when focus is outside the list.
export function wrapFocus(count: number, index: number, shift: boolean): number | null {
  if (count <= 0) return null;
  if (index < 0) return shift ? count - 1 : 0;
  if (shift && index === 0) return count - 1;
  if (!shift && index === count - 1) return 0;
  return null;
}
