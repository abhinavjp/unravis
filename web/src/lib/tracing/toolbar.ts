/**
 * The trace toolbar (WP-10): lane visibility state for the Investigation Timeline and the exact words of the
 * scrub pill. Pure TypeScript; the screen component is TraceToolbar.svelte and WP-11 mounts it.
 *
 * The scrub pill always states the real export setting in plain words. It never says stored data is altered:
 * the setting only decides what the next evidence export does with IP, referer and cookie hash (TR-23).
 */
import type { LintEntry } from '../copy/copylint';
import type { TracingLaneKey } from './model';
import { LANE_ORDER } from './lanes';

/** Lane order (audit, sql, iis) comes from lanes.ts, the one place it is defined; re-exported for the toolbar. */
export { LANE_ORDER };

export type LaneVisibility = Record<TracingLaneKey, boolean>;

/** Every lane on: the state the timeline starts in. */
export function allLanesVisible(): LaneVisibility {
  return { audit: true, sql: true, iis: true };
}

/** A new object with the named key flipped; the input is left unchanged. */
export function toggleLane(visibility: LaneVisibility, key: TracingLaneKey): LaneVisibility {
  return { ...visibility, [key]: !visibility[key] };
}

/** The lanes currently on, in audit, sql, iis order. */
export function visibleKeys(visibility: LaneVisibility): TracingLaneKey[] {
  return LANE_ORDER.filter((key) => visibility[key]);
}

export const TOOLBAR_TEXT = {
  ariaLabel: 'Timeline controls',
  lanesLabel: 'Show rows',
  audit: 'API audit',
  sql: 'SQL',
  iis: 'IIS',
  scrubDefault: 'Default: export hides IP, referer and cookie hash',
  scrubOn: 'Export hides IP, referer and cookie hash',
  scrubOff: 'Export includes IP, referer and cookie hash (scrub is off)',
  scrubHint: 'This is the setting the next evidence export will use. Nothing saved in the case is changed here.'
} as const;

export const toolbarCopyEntries = (): LintEntry[] =>
  Object.entries(TOOLBAR_TEXT).map(([id, text]) => ({ id: 'trace-toolbar.' + id, text }));

/** The pill sentence. `null` (or a preview that has not arrived yet) states the default. */
export function scrubPillText(scrub: boolean | null): string {
  if (scrub === true) return TOOLBAR_TEXT.scrubOn;
  if (scrub === false) return TOOLBAR_TEXT.scrubOff;
  return TOOLBAR_TEXT.scrubDefault;
}
