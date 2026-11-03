/**
 * Chart tooltip text (roadmap WP-17c): the plain-word lines for a tracing lane head and for one chart bar,
 * built from the tooltip dictionary behind `explain`. Pure strings only: no DOM, no HTML, no state.
 */
import { explain } from '../copy/dictionary';
import type { TracingLaneKey } from './model';

/** The dictionary id behind each lane's head hint. */
export const LANE_DICT_ID: Record<TracingLaneKey, string> = {
  audit: 'lane.api-audit',
  sql: 'lane.sql',
  iis: 'lane.iis'
};

/** Every fixed sentence this module can put on screen, in one place for the copy lint. */
export const TOOLTIP_TEXT = {
  noEvents: 'No events in this bucket',
  readJustNow: 'Read just now',
  fromSavedLogs: 'From saved logs',
  tapAgain: 'Tap this bar again to open it in the analysis.'
} as const;

/** True for a finger or a pen (or an unknown pointer on a coarse-pointer screen); a mouse never is. */
export function isTouchPointer(pointerType: string | undefined, coarse: boolean): boolean {
  if (pointerType === 'touch' || pointerType === 'pen') return true;
  return pointerType !== 'mouse' && coarse;
}

/** The trailing word two formatted times share when it holds a letter ("UTC", "GMT+4"); '' when they do not share one. */
function sharedZoneWord(a: string, b: string): string {
  const pattern = /\s(\S*[A-Za-z]\S*)$/;
  const first = pattern.exec(a);
  const second = pattern.exec(b);
  return first && second && first[1] === second[1] ? first[1] : '';
}

/** The plain hint shown on a lane head; '' when the key is not a lane. */
export function laneHint(key: string): string {
  if (typeof key !== 'string' || !Object.hasOwn(LANE_DICT_ID, key)) return '';
  return explain(LANE_DICT_ID[key as TracingLaneKey]);
}

/** A count with thousands separators, the one format used on axis labels and in tooltips. */
export function formatCount(count: number): string {
  return count.toLocaleString('en-GB');
}

/** What one chart bar tells the user, before any trimming. */
export interface BarTipInput {
  /** The lane the bar sits in. */
  key: TracingLaneKey;
  /** The bucket's start, end and zone name, already formatted for the screen. */
  startText: string;
  endText: string;
  zoneName: string;
  /** How many events the bucket holds, and (audit and IIS only) how many were 5xx answers. */
  count: number;
  errors: number;
  /** Where the events came from, and for a saved read how fresh it is ('' when unknown). */
  origin: 'live' | 'case';
  asOfText: string;
  /** A short lane-level caveat, '' when there is none. */
  note: string;
}

/** The tooltip lines for one chart bar, in reading order; empty lines are dropped. */
export function barTipLines(input: BarTipInput): string[] {
  // The zone is named once. Both times carry the same zone word ("... 10:20 to ... 10:25 UTC", "... GMT+4"): it stays on the end only.
  // Otherwise the zone name is added only when the times do not already end with it.
  const shared = sharedZoneWord(input.startText, input.endText);
  const zoneStated = shared !== '' || input.zoneName === '' || input.endText.endsWith(input.zoneName);
  const startText = shared ? input.startText.slice(0, input.startText.length - shared.length - 1) : input.startText;
  const lines: string[] = [`${startText} to ${input.endText}${zoneStated ? '' : ` (${input.zoneName})`}`];
  if (input.count === 0) {
    lines.push(TOOLTIP_TEXT.noEvents);
  } else {
    const noun = input.key === 'sql' ? 'call' : 'request';
    lines.push(`${formatCount(input.count)} ${noun}${input.count === 1 ? '' : 's'}`);
    // The API audit and IIS lanes both count 5xx answers; SQL has no such count.
    if ((input.key === 'audit' || input.key === 'iis') && input.errors > 0) {
      lines.push(`${formatCount(input.errors)} server error${input.errors === 1 ? '' : 's'} (5xx)`);
      lines.push(explain('status.5xx'));
    }
    if (input.key === 'iis') lines.push(explain('iis.point'));
  }
  const originLine = (input.origin === 'live' ? TOOLTIP_TEXT.readJustNow : TOOLTIP_TEXT.fromSavedLogs)
    + (input.asOfText ? ` (as of ${input.asOfText})` : '');
  lines.push(originLine);
  if (input.note) lines.push(input.note);
  return lines.filter((line) => line !== '');
}

/**
 * The bucket slot a point at `fraction` of the chart (0 to 1) lands in: `floor(fraction * n)` clamped to
 * `0..n-1`, or -1 when there is no slot to pick or the fraction is not a finite number.
 */
export function slotAtFraction(fraction: number, n: number): number {
  if (n < 1 || !Number.isFinite(fraction)) return -1;
  return Math.min(n - 1, Math.max(0, Math.floor(fraction * n)));
}

/** Every fixed sentence this module can put on screen, as entries for the copy lint. */
export function tooltipCopyEntries(): Array<{ id: string; text: string }> {
  return [
    { id: 'tooltip.no-events', text: TOOLTIP_TEXT.noEvents },
    { id: 'tooltip.read-just-now', text: TOOLTIP_TEXT.readJustNow },
    { id: 'tooltip.from-saved-logs', text: TOOLTIP_TEXT.fromSavedLogs },
    { id: 'tooltip.tap-again', text: TOOLTIP_TEXT.tapAgain },
    { id: 'tooltip.requests', text: '1,200 requests' },
    { id: 'tooltip.request-one', text: '1 request' },
    { id: 'tooltip.calls', text: '1,200 calls' },
    { id: 'tooltip.call-one', text: '1 call' },
    { id: 'tooltip.server-errors', text: '3 server errors (5xx)' },
    { id: 'tooltip.server-error-one', text: '1 server error (5xx)' },
    { id: 'tooltip.as-of', text: 'Read just now (as of 10:05)' }
  ];
}
