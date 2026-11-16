import { DRILL_TEXT } from './drillCopy';
import type { LaneMeasure } from './callLanes';

/**
 * Which state the Database lane is in on the Slow call screen, and the words that go with it.
 * Pure: it only decides from data the app already has. All text comes from DRILL_TEXT.
 */
export type DbStateKey = 'qs' | 'qsfile' | 'api' | 'off' | 'ro' | 'late' | 'denied' | 'events' | 'notread' | 'blackbox';

export const DB_STATE_KEYS: readonly DbStateKey[] = ['qs', 'qsfile', 'api', 'off', 'ro', 'late', 'denied', 'events', 'notread'];

export interface QueryStoreInfo {
  state: 'on' | 'off' | 'read-only' | 'denied' | 'unknown';
  firstData?: string | null;
  intervalMinutes?: number | null;
}

export interface DbStateInput {
  /** Tracing SQL lane reason code: 'held-sql' | 'layered-sql' | 'qs-file' | 'api-seen' | 'blackbox' | '' ... */
  laneReasonCode: string;
  laneHasShape: boolean;
  /**
   * The lane holds valid recorded observations (a drawable number in its own measure). Source availability
   * rests on this, independently of whether concurrency can be drawn: a Blackbox lane with recorded batch
   * counts is a real source even though it measures throughput, not "running at once" (W13-01).
   * Absent = fall back to laneHasShape.
   */
  laneHasObservations?: boolean;
  laneAsOf: string | null;
  /** When the lane's own source is the Blackbox recorder: the server's `since`, the first sample it kept. Null when it said none. */
  laneSince: string | null;
  /** The source's own observation cadence in seconds, when it sent one. Null/absent = cadence unknown; never guessed. */
  laneCadenceSeconds?: number | null;
  blockSeconds: number | null;
  /** Newest saved SQL lane day, null when none. */
  queryStore: QueryStoreInfo | null;
  /** Imported sql-xe / sql-errorlog events in the window. */
  hasEvents: boolean;
  windowStart: number;
  windowEnd: number;
  /** Demo switch; wins over everything. */
  pretend?: DbStateKey | null;
}

export interface DbStateView {
  key: DbStateKey;
  source: string;
  message: string;
  short: string;
  shape: 'blocks' | 'apiseen' | 'markers' | 'none';
  /** What the lane's numbers measure; the chart's labels and readouts must use this unit. */
  measure: LaneMeasure;
}

export interface DbStateFormat {
  date: (iso: string) => string;
  time: (iso: string) => string;
}

// Demonstration values: only ever used behind the explicit demo switch (`pretend`), never for real data (W13-04).
const SAMPLE_DATE = '11 Nov 2026';
const SAMPLE_TIME = '10:30';
const SAMPLE_BLOCK_MINUTES = 15;

const SHAPE: Record<DbStateKey, DbStateView['shape']> = {
  qs: 'blocks',
  qsfile: 'blocks',
  ro: 'blocks',
  late: 'blocks',
  blackbox: 'blocks',
  api: 'apiseen',
  events: 'markers',
  off: 'none',
  denied: 'none',
  notread: 'none'
};

/** Picks the state. `unknown` (the state row was missing or an error) is not a state of its own:
 *  it matches no rule below, so the lane falls through to what the data shows, and ends at `notread`. */
function pickKey(input: DbStateInput): DbStateKey {
  if (input.pretend) return input.pretend;
  // The server named this lane's own source: the Blackbox recorder holds it, whatever Query Store itself is doing.
  // It counts when the lane holds valid recorded observations — batch throughput counts, not only concurrency —
  // so a real recording is never called "Not recorded"; an empty lane still never claims a source (never a zero lane).
  if (input.laneReasonCode === 'blackbox' && (input.laneHasObservations ?? input.laneHasShape)) return 'blackbox';
  const qs = input.queryStore;
  if (qs?.state === 'denied') return 'denied';
  if (qs?.state === 'off') return input.hasEvents ? 'events' : 'off';
  if (input.laneReasonCode === 'qs-file' && input.laneHasShape) return 'qsfile';
  if ((input.laneReasonCode === 'held-sql' || input.laneReasonCode === 'layered-sql') && input.laneHasShape) {
    const first = qs?.firstData ? Date.parse(qs.firstData) : NaN;
    if (Number.isFinite(first) && first > input.windowStart) return 'late';
    if (qs?.state === 'read-only') return 'ro';
    return 'qs';
  }
  if (input.laneReasonCode === 'api-seen' && input.laneHasShape) return 'api';
  if (input.hasEvents) return 'events';
  return 'notread';
}

export function dbState(input: DbStateInput, fmt: DbStateFormat): DbStateView {
  const key = pickKey(input);
  const T = DRILL_TEXT;
  const minutes = input.blockSeconds != null && input.blockSeconds > 0 ? Math.max(1, Math.round(input.blockSeconds / 60)) : SAMPLE_BLOCK_MINUTES;
  // A real date is only ever a date the source sent and the parser could read. The sample date is demo-only (W13-04).
  const date = input.laneAsOf ? fmt.date(input.laneAsOf) : input.pretend ? SAMPLE_DATE : null;
  const since = input.laneSince ? fmt.date(input.laneSince) : null;
  const cadence = typeof input.laneCadenceSeconds === 'number' && Number.isFinite(input.laneCadenceSeconds) && input.laneCadenceSeconds > 0
    ? input.laneCadenceSeconds
    : null;
  const firstData = input.queryStore?.firstData;
  const time = firstData ? fmt.time(firstData) : SAMPLE_TIME;
  const shape = SHAPE[key];
  // The 'blackbox' reason code is the host batch-throughput contract; every other state draws concurrency.
  const measure: LaneMeasure = key === 'blackbox' ? 'batch-rate' : 'at-once';
  switch (key) {
    case 'qs':
      return { key, source: T.dbSrcLive, message: T.dbMsgLive(minutes), short: T.laneBlocks(minutes), shape, measure };
    case 'qsfile':
      return { key, source: T.dbSrcFile(date), message: T.dbMsgFile(date), short: T.laneBlocks(minutes), shape, measure };
    case 'api':
      return { key, source: T.dbSrcApi, message: T.dbMsgApi, short: T.laneApiOnly, shape, measure };
    case 'off':
      return { key, source: T.dbSrcNone, message: T.dbMsgOff, short: T.dbShortOff, shape, measure };
    case 'ro':
      return { key, source: T.dbSrcReadOnly, message: T.dbMsgReadOnly, short: T.laneBlocks(minutes), shape, measure };
    case 'late':
      return { key, source: T.dbSrcLate(time), message: T.dbMsgLate(time), short: T.dbShortLate, shape, measure };
    case 'denied':
      return { key, source: T.dbSrcNone, message: T.dbMsgDenied, short: T.dbShortDenied, shape, measure };
    case 'events':
      return { key, source: T.dbSrcEvents, message: T.dbMsgEvents, short: T.dbShortEvents, shape, measure };
    case 'blackbox':
      return { key, source: T.dbSrcBlackbox(since, cadence), message: T.dbMsgBlackbox(cadence), short: T.dbShortBlackbox(cadence), shape, measure };
    case 'notread':
      return { key, source: T.dbSrcNone, message: T.dbMsgNotRead, short: T.notRecorded, shape, measure };
  }
}
