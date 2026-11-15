/**
 * Nearby calls model for the Slow call screen. Pure functions, no DOM, no fetch.
 * Decides which rows show for the window and chip, when the server must be asked,
 * what to ask, and what the "from this call" and light totals cells say.
 */

export type NearbyChip = 'everyone' | 'user' | 'api';

/** Server MaxPageSize for /api/activity. */
export const NEARBY_LIMIT = 100;

export interface NearbyRow {
  id: string;
  sourceId: string;
  controller: string;
  action: string;
  initiatedAt: string;
  durationMs: number | null;
  status?: number;
  userId?: number | null;
  light?: { sqlMs: number | null; redisMs: number | null; httpMs: number | null } | null;
}

export interface CallRef {
  id: string;
  sourceId: string;
  controller: string;
  action: string;
  userId?: number | null;
  startMs: number;
  endMs: number;
}

/** What the last server answer covered. */
export interface Loaded {
  start: number;
  end: number;
  chip: NearbyChip;
  truncated: boolean;
}

const MINUS = '−';

function hasUser(call: CallRef): boolean {
  return call.userId !== null && call.userId !== undefined;
}

/** 'user' needs a userId (0 is a real user); 'api' needs controller and action. */
export function chipAvailable(chip: NearbyChip, call: CallRef): boolean {
  if (chip === 'user') return hasUser(call);
  if (chip === 'api') return call.controller !== '' && call.action !== '';
  return true;
}

export function filterRows(
  rows: readonly NearbyRow[],
  chip: NearbyChip,
  call: CallRef,
  start: number,
  end: number
): NearbyRow[] {
  if (!chipAvailable(chip, call)) return [];
  const timed: { row: NearbyRow; at: number }[] = [];
  for (const row of rows) {
    const at = Date.parse(row.initiatedAt);
    if (Number.isNaN(at) || at < start || at > end) continue;
    if (chip === 'user' && row.userId !== call.userId) continue;
    if (chip === 'api' && (row.controller !== call.controller || row.action !== call.action)) continue;
    timed.push({ row, at });
  }
  timed.sort((a, b) => a.at - b.at);
  return timed.map((item) => item.row);
}

export function needsServer(loaded: Loaded | null, chip: NearbyChip, start: number, end: number): boolean {
  if (loaded === null) return true;
  if (chip !== loaded.chip && loaded.chip !== 'everyone') return true;
  if (chip !== 'everyone' && loaded.truncated) return true;
  if (start < loaded.start || end > loaded.end) return true;
  return false;
}

export function activityInput(
  sourceIds: readonly string[],
  start: number,
  end: number,
  chip: NearbyChip,
  call: CallRef
): { sourceIds: string[]; start: string; end: string; cursor: string; limit: number; userId?: number; operation?: string } {
  const input: { sourceIds: string[]; start: string; end: string; cursor: string; limit: number; userId?: number; operation?: string } = {
    sourceIds: [...sourceIds],
    start: new Date(start).toISOString(),
    end: new Date(end).toISOString(),
    cursor: '',
    limit: NEARBY_LIMIT
  };
  if (chip === 'user' && call.userId !== null && call.userId !== undefined) input.userId = call.userId;
  if (chip === 'api' && chipAvailable('api', call)) input.operation = `${call.controller}.${call.action}`;
  return input;
}

/**
 * Where a row sits against the call. The call's own row and a row that started while the call ran
 * get words; any other row gets a signed gap measured from the nearest edge of the call
 * (before it started: minus; after it ended: plus).
 */
export function gapLabel(rowStartMs: number, call: CallRef, text: { thisCall: string; during: string }): string {
  if (rowStartMs === call.startMs) return text.thisCall;
  if (rowStartMs >= call.startMs && rowStartMs <= call.endMs) return text.during;
  const before = rowStartMs < call.startMs;
  const gapMs = before ? call.startMs - rowStartMs : rowStartMs - call.endMs;
  const seconds = Math.round(gapMs / 1000);
  const sign = before ? MINUS : '+';
  if (seconds < 60) return `${sign}${seconds} s`;
  return `${sign}${Math.round(seconds / 60)} min`;
}

/** null = the whole row is not recorded. A null number stays null, never 0. */
export function lightCells(row: NearbyRow): { sqlMs: number | null; redisMs: number | null; httpMs: number | null } | null {
  if (row.light === null || row.light === undefined) return null;
  return { sqlMs: row.light.sqlMs, redisMs: row.light.redisMs, httpMs: row.light.httpMs };
}

export function isThisCall(row: NearbyRow, call: CallRef): boolean {
  return row.id === call.id && row.sourceId === call.sourceId;
}
