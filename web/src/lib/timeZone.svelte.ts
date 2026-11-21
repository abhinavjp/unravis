/**
 * The one time-zone choice for every timestamp, chart axis and time picker. London is the default (owner call, 2026-11-21); Dublin, UTC,
 * India, Australia (Sydney) and this computer's own zone can be picked instead. Logs and the data source still speak UTC: only what is
 * shown and typed follows the choice. The choice is remembered in this browser (localStorage, guarded: it can be blocked or full).
 * `zone` is always a valid IANA name, so it can be handed straight to formatTime, the charts and the pickers.
 */
export type TimeZoneChoice = 'london' | 'dublin' | 'utc' | 'india' | 'australia' | 'local';
const KEY = 'unravis.timeZone';
export const DEFAULT_TIME_ZONE: TimeZoneChoice = 'london';

const browserZone = (() => {
  try { return Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC'; } catch { return 'UTC'; }
})();

/** The choices in the order the header lists them, with the IANA zone and the short name a picker shows beside a time. */
export const TIME_ZONE_OPTIONS: readonly { id: TimeZoneChoice; label: string; zone: string; short: string }[] = [
  { id: 'london', label: 'London', zone: 'Europe/London', short: 'London time' },
  { id: 'dublin', label: 'Dublin', zone: 'Europe/Dublin', short: 'Dublin time' },
  { id: 'utc', label: 'UTC', zone: 'UTC', short: 'UTC' },
  { id: 'india', label: 'India', zone: 'Asia/Kolkata', short: 'India time' },
  { id: 'australia', label: 'Australia (Sydney)', zone: 'Australia/Sydney', short: 'Sydney time' },
  { id: 'local', label: 'This computer', zone: browserZone, short: browserZone }
];

const isChoice = (text: string | null): text is TimeZoneChoice => TIME_ZONE_OPTIONS.some((o) => o.id === text);

const stored = (): TimeZoneChoice => {
  try { const text = localStorage.getItem(KEY); return isChoice(text) ? text : DEFAULT_TIME_ZONE; } catch { return DEFAULT_TIME_ZONE; }
};

let choice = $state<TimeZoneChoice>(stored());
const option = (id: TimeZoneChoice) => TIME_ZONE_OPTIONS.find((o) => o.id === id) ?? TIME_ZONE_OPTIONS[0];

export const timeZone = {
  get choice() { return choice; },
  /** The IANA zone name to format and type in: "Europe/London", "UTC", or the browser zone. */
  get zone() { return option(choice).zone; },
  /** The words a picker shows beside a typed time: "London time", "UTC". */
  get short() { return option(choice).short; },
  /** The browser's own zone, shown in the "This computer" choice: "Asia/Dubai". */
  get browserZone() { return browserZone; },
  set(next: TimeZoneChoice) {
    choice = isChoice(next) ? next : DEFAULT_TIME_ZONE;
    try { localStorage.setItem(KEY, choice); } catch { /* storage unavailable: the choice lasts until the page reloads */ }
  }
};
