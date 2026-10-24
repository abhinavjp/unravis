/**
 * The one time-zone choice for every timestamp and chart axis: UTC or the browser's own zone. UTC is the default because logs and
 * the data source speak UTC. The choice is remembered in this browser (localStorage, guarded: it can be blocked or full).
 * `zone` is always a valid IANA name, so it can be handed straight to formatTime and the charts.
 */
export type TimeZoneChoice = 'utc' | 'local';
const KEY = 'unravis.timeZone';

const browserZone = (() => {
  try { return Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC'; } catch { return 'UTC'; }
})();

const stored = (): TimeZoneChoice => {
  try { return localStorage.getItem(KEY) === 'local' ? 'local' : 'utc'; } catch { return 'utc'; }
};

let choice = $state<TimeZoneChoice>(stored());

export const timeZone = {
  get choice() { return choice; },
  /** The IANA zone name to format in: "UTC" or the browser zone. */
  get zone() { return choice === 'utc' ? 'UTC' : browserZone; },
  /** The browser's own zone, shown in the toggle: "Asia/Dubai". */
  get browserZone() { return browserZone; },
  set(next: TimeZoneChoice) {
    choice = next;
    try { localStorage.setItem(KEY, next); } catch { /* storage unavailable: the choice lasts until the page reloads */ }
  },
  /** Accepts a zone name: UTC selects UTC, any other zone selects the browser's zone. */
  setZone(zoneName: string) { timeZone.set(zoneName === 'UTC' ? 'utc' : 'local'); },
  toggle() { timeZone.set(choice === 'utc' ? 'local' : 'utc'); }
};
