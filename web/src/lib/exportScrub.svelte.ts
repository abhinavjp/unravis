/**
 * The export scrub setting last seen in an export preview, so the timeline toolbar can state it (TR-23). Null until a preview has run
 * in this session. In memory only: it is never stored, put in a URL or sent anywhere.
 */
let value = $state<boolean | null>(null);

export const exportScrub = {
  get value() { return value; },
  set(next: boolean | null) { value = next; }
};
