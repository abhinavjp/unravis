/**
 * The export scrub setting last seen in an export preview, so the timeline toolbar can state it (TR-23). Null until a preview has run
 * in this session. In memory only: it is never stored, put in a URL or sent anywhere.
 * The value belongs to the case whose preview produced it: another case never reads it (forCase), and opening another case's tools
 * drops it (keepOnlyFor), so the toolbar never words case A's setting as the setting case B's export will use.
 */
let value = $state<boolean | null>(null);
let owner: string | null = null;

export const exportScrub = {
  get value() { return value; },
  /** The setting seen for this case, or null when it came from another case or none has been seen. */
  forCase(caseId: string): boolean | null { return owner === caseId ? value : null; },
  set(next: boolean | null, caseId: string | null = null) { value = next; owner = caseId; },
  /** Forget a setting that was seen for a different case. */
  keepOnlyFor(caseId: string) { if (owner !== null && owner !== caseId) { value = null; owner = null; } }
};
