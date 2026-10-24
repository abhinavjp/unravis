/**
 * Theme: dark first, a full light theme through prefers-color-scheme, and a manual override stored on the document element
 * (data-theme) so the CSS tokens switch without a reload. Charts draw on a canvas, so they read `theme.epoch` to rebuild on a change.
 */
export type ThemeChoice = 'auto' | 'light' | 'dark';
const KEY = 'unravis.theme';

const stored = (): ThemeChoice => {
  try {
    const value = localStorage.getItem(KEY);
    return value === 'light' || value === 'dark' ? value : 'auto';
  } catch { return 'auto'; }
};

let choice = $state<ThemeChoice>('auto');
let epoch = $state(0);
let started = false;

const apply = (value: ThemeChoice) => {
  if (typeof document === 'undefined') return;
  if (value === 'auto') document.documentElement.removeAttribute('data-theme');
  else document.documentElement.setAttribute('data-theme', value);
  epoch++;
};

export const theme = {
  get choice() { return choice; },
  /** Changes whenever the effective theme may have changed; read it inside an effect to redraw. */
  get epoch() { return epoch; },
  /** Reads the saved choice and follows the system theme while on auto. Safe to call more than once. */
  start() {
    if (started || typeof document === 'undefined') return;
    started = true;
    choice = stored();
    apply(choice);
    try { window.matchMedia('(prefers-color-scheme: light)').addEventListener('change', () => { if (choice === 'auto') epoch++; }); } catch { /* no media query support */ }
  },
  set(next: ThemeChoice) {
    choice = next;
    try { if (next === 'auto') localStorage.removeItem(KEY); else localStorage.setItem(KEY, next); } catch { /* storage unavailable */ }
    apply(next);
  },
  /** Cycles auto, light, dark. */
  cycle() { theme.set(choice === 'auto' ? 'light' : choice === 'light' ? 'dark' : 'auto'); }
};
