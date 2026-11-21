import { zoneOffsetMs } from './chartTime';
import { localToInstant, safeZone } from './zoom';
import { formatStamp, parseStamp } from './tracing/pickerModel';

/**
 * The time pickers keep `YYYY-MM-DDTHH:mm` stamps in UTC, the shape every check and the server read. A person types the wall clock of the
 * zone they chose, so a picker turns the stamp into that wall clock to show it and back to UTC when the person changes it.
 * Text that is not a whole stamp (empty, half typed) passes through unchanged. In UTC both are the identity.
 */
export function utcToWall(stamp: string, zone: string): string {
  const ms = parseStamp(stamp);
  if (!Number.isFinite(ms)) return stamp;
  const z = safeZone(zone);
  return z === 'UTC' ? stamp : formatStamp(ms + zoneOffsetMs(ms, z));
}

/** The UTC stamp for a wall clock in `zone`. A wall time read twice (clocks going back) takes the first; one the clocks skip lands just after the gap. */
export function wallToUtc(stamp: string, zone: string): string {
  const ms = parseStamp(stamp);
  if (!Number.isFinite(ms)) return stamp;
  const z = safeZone(zone);
  return z === 'UTC' ? stamp : formatStamp(localToInstant(ms, z));
}
