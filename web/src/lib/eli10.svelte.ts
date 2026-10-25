/**
 * The "Explain simply" switch. One flag for the whole app: when it is on, every HelpNote shows its "In simple words" block.
 * It is kept in memory and saved to local storage. If storage is blocked or throws, the flag still works for this tab.
 */
const KEY = 'unravis.eli10';

function stored(): boolean {
  try { return localStorage.getItem(KEY) === '1'; } catch { return false; }
}

let on = $state(stored());

export const eli10 = {
  get on() { return on; },
  set(next: boolean) {
    on = next;
    try { localStorage.setItem(KEY, next ? '1' : '0'); } catch { /* storage unavailable: the flag stays in memory */ }
  },
  toggle() { eli10.set(!on); }
};
