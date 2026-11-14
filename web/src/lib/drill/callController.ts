/**
 * The Slow call screen's one place for reads. Finds the call, reads the overview strip and the visible window
 * from the tracing endpoint, the slowest calls of the window, the nearby calls and the event markers, and tells
 * the screen through `onChange` whenever something changed. Plain TypeScript: no Svelte runes, no direct fetch.
 */
import type { ActivityInput, Page, Query, RecordDetail, RequestRecord } from '../types';
import type { TracingResponse } from '../tracing/model';
import type { CallEvent, CallEvents } from './callData';
import { coversOverview, DEFAULT_WINDOW, overviewBounds, windowBounds, type CallWindow } from './callWindow';
import { activityInput, needsServer, type CallRef, type Loaded, type NearbyChip } from './nearbyCalls';
import { parsePerf, type DrillRecord } from './perf';
import { drillQuery, pickCall } from './target';
import type { SlowCallTarget } from './callTarget';

/** The most slow-call rows the screen keeps for one window. */
const SLOW_ROWS_MAX = 50;

export interface CallDeps {
  slowest(query: Query, signal: AbortSignal): Promise<Page>;
  record(sourceId: string, id: string, signal: AbortSignal): Promise<RecordDetail>;
  /** null = a stale answer: a newer tracing read took its place and will answer. */
  tracing(caseId: string, startIso: string, endIso: string, sourceIds: string[], signal: AbortSignal): Promise<TracingResponse | null>;
  activity(input: ActivityInput, signal: AbortSignal): Promise<Page>;
  events(caseId: string, fromIso: string, toIso: string, signal: AbortSignal): Promise<CallEvents>;
}

export type CallPhase = 'finding' | 'ready' | 'not-found' | 'failed';

export interface CallState {
  phase: CallPhase;
  record: DrillRecord | null;
  callStartMs: number;
  callEndMs: number;
  window: CallWindow;
  chip: NearbyChip;
  overview: TracingResponse | null;
  overviewFailed: boolean;
  /** True when the slowest-calls read of the window failed; the rows are then empty because they are not known. */
  slowRowsFailed: boolean;
  visible: TracingResponse | null;
  visibleLoading: boolean;
  visibleFailed: boolean;
  /** The slowest API calls in the visible window (at most 50). */
  slowRows: RequestRecord[];
  nearby: { rows: RequestRecord[]; loaded: Loaded | null; asking: boolean; failed: boolean };
  events: CallEvent[];
  eventsTruncated: boolean;
}

export interface CallController {
  state(): CallState;
  start(): void;
  retry(): void;
  /** While dragging: updates the window only, no read. */
  preview(w: CallWindow): void;
  /** Drag end, preset, Earlier/Later: sets the window and reads. */
  commit(w: CallWindow): void;
  setChip(c: NearbyChip): void;
  dispose(): void;
}

function initialState(): CallState {
  return {
    phase: 'finding',
    record: null,
    callStartMs: Number.NaN,
    callEndMs: Number.NaN,
    window: { ...DEFAULT_WINDOW },
    chip: 'everyone',
    overview: null,
    overviewFailed: false,
    slowRowsFailed: false,
    visible: null,
    visibleLoading: false,
    visibleFailed: false,
    slowRows: [],
    nearby: { rows: [], loaded: null, asking: false, failed: false },
    events: [],
    eventsTruncated: false
  };
}

const iso = (ms: number): string => new Date(ms).toISOString();

export function createCallController(
  target: SlowCallTarget,
  deps: CallDeps,
  onChange: (s: CallState) => void,
  onFound?: (call: { sourceId: string; id: string }) => void
): CallController {
  let s: CallState = initialState();
  let disposed = false;
  let started = false;
  /** Counts window reads; an answer from an older count is dropped. */
  let readSeq = 0;
  /** True while the visible window is the overview itself, so the overview answer is reused. */
  let visibleFromOverview = false;
  let findCtl: AbortController | null = null;
  let overviewCtl: AbortController | null = null;
  let windowCtls: AbortController[] = [];
  let nearbyCtl: AbortController | null = null;

  const snapshot = (): CallState => ({ ...s, window: { ...s.window }, nearby: { ...s.nearby } });
  const emit = (): void => {
    if (!disposed) onChange(snapshot());
  };

  function abortWindowReads(): void {
    for (const ctl of windowCtls) ctl.abort();
    windowCtls = [];
    nearbyCtl?.abort();
    nearbyCtl = null;
  }

  function abortAll(): void {
    findCtl?.abort();
    findCtl = null;
    overviewCtl?.abort();
    overviewCtl = null;
    abortWindowReads();
  }

  function callRef(): CallRef | null {
    const rec = s.record;
    if (!rec) return null;
    return {
      id: rec.id,
      sourceId: rec.sourceId,
      controller: rec.controller,
      action: rec.action,
      userId: rec.userId,
      startMs: s.callStartMs,
      endMs: s.callEndMs
    };
  }

  /** Reads the visible window from the tracing endpoint. */
  function readVisible(mySeq: number, ctl: AbortController, startIso: string, endIso: string): void {
    s.visibleLoading = true;
    s.visibleFailed = false;
    deps.tracing(target.caseId, startIso, endIso, target.sourceIds, ctl.signal).then(
      (answer) => {
        if (disposed || ctl.signal.aborted || mySeq !== readSeq) return;
        if (answer === null) return;
        s.visible = answer;
        s.visibleLoading = false;
        emit();
      },
      () => {
        if (disposed || ctl.signal.aborted || mySeq !== readSeq) return;
        s.visibleLoading = false;
        s.visibleFailed = true;
        emit();
      }
    );
  }

  function readSlowRows(mySeq: number, ctl: AbortController, startIso: string, endIso: string): void {
    s.slowRowsFailed = false;
    deps.slowest(drillQuery({ start: startIso, end: endIso }, target.sourceIds), ctl.signal).then(
      (page) => {
        if (disposed || ctl.signal.aborted || mySeq !== readSeq) return;
        s.slowRows = page.rows.slice(0, SLOW_ROWS_MAX);
        s.slowRowsFailed = false;
        emit();
      },
      () => {
        if (disposed || ctl.signal.aborted || mySeq !== readSeq) return;
        s.slowRows = [];
        s.slowRowsFailed = true;
        emit();
      }
    );
  }

  function readEvents(mySeq: number, ctl: AbortController, startIso: string, endIso: string): void {
    deps.events(target.caseId, startIso, endIso, ctl.signal).then(
      (answer) => {
        if (disposed || ctl.signal.aborted || mySeq !== readSeq) return;
        s.events = answer.events;
        s.eventsTruncated = answer.truncated;
        emit();
      },
      () => {
        if (disposed || ctl.signal.aborted || mySeq !== readSeq) return;
        s.events = [];
        s.eventsTruncated = false;
        emit();
      }
    );
  }

  /** Asks the server for nearby calls when the loaded answer does not cover the window and chip. */
  function readNearby(): void {
    nearbyCtl?.abort();
    nearbyCtl = null;
    const call = callRef();
    if (!call) return;
    const b = windowBounds(s.callStartMs, s.window);
    const startMs = b.start;
    const endMs = b.end;
    if (!needsServer(s.nearby.loaded, s.chip, startMs, endMs)) {
      if (s.nearby.asking) {
        s.nearby = { ...s.nearby, asking: false };
        emit();
      }
      return;
    }
    const mySeq = readSeq;
    const ctl = new AbortController();
    nearbyCtl = ctl;
    const chip = s.chip;
    s.nearby.asking = true;
    s.nearby.failed = false;
    deps.activity(activityInput(target.sourceIds, startMs, endMs, chip, call), ctl.signal).then(
      (page) => {
        if (disposed || ctl.signal.aborted || mySeq !== readSeq) return;
        s.nearby = {
          rows: page.rows,
          loaded: { start: startMs, end: endMs, chip, truncated: page.truncated === true || (typeof page.nextCursor === 'string' && page.nextCursor !== '') },
          asking: false,
          failed: false
        };
        emit();
      },
      () => {
        if (disposed || ctl.signal.aborted || mySeq !== readSeq) return;
        s.nearby = { ...s.nearby, asking: false, failed: true };
        emit();
      }
    );
  }

  /** Starts every read that depends on the window. Older window reads are aborted first. */
  function readWindow(): void {
    abortWindowReads();
    const mySeq = ++readSeq;
    if (!s.record) return;
    const b = windowBounds(s.callStartMs, s.window);
    const startIso = iso(b.start);
    const endIso = iso(b.end);
    const fresh = (): AbortController => {
      const ctl = new AbortController();
      windowCtls.push(ctl);
      return ctl;
    };
    // The visible window: reuse the overview answer when the window shows the whole strip.
    if (coversOverview(s.callStartMs, s.window) && !s.overviewFailed) {
      visibleFromOverview = true;
      s.visibleFailed = false;
      if (s.overview) {
        s.visible = s.overview;
        s.visibleLoading = false;
      } else {
        s.visibleLoading = true;
      }
    } else {
      visibleFromOverview = false;
      readVisible(mySeq, fresh(), startIso, endIso);
    }
    readSlowRows(mySeq, fresh(), startIso, endIso);
    readNearby();
    readEvents(mySeq, fresh(), startIso, endIso);
    emit();
  }

  function readOverview(): void {
    overviewCtl?.abort();
    const ctl = new AbortController();
    overviewCtl = ctl;
    s.overviewFailed = false;
    const ob = overviewBounds(s.callStartMs);
    deps.tracing(target.caseId, iso(ob.start), iso(ob.end), target.sourceIds, ctl.signal).then(
      (answer) => {
        if (disposed || ctl.signal.aborted) return;
        if (answer === null) return;
        s.overview = answer;
        if (visibleFromOverview) {
          s.visible = answer;
          s.visibleLoading = false;
        }
        emit();
      },
      () => {
        if (disposed || ctl.signal.aborted) return;
        s.overviewFailed = true;
        if (visibleFromOverview) {
          // The strip failed, so the window needs its own read.
          visibleFromOverview = false;
          const wb = windowBounds(s.callStartMs, s.window);
          const vctl = new AbortController();
          windowCtls.push(vctl);
          readVisible(readSeq, vctl, iso(wb.start), iso(wb.end));
        }
        emit();
      }
    );
  }

  /** The record is known: build the screen state from it and run every read. */
  function callFound(rec: RecordDetail): void {
    const record: DrillRecord = { ...rec, perf: parsePerf((rec as { perf?: unknown }).perf) };
    const anchor = Date.parse(record.initiatedAt ?? record.completedAt ?? '');
    const startMs = Number.isFinite(anchor) ? anchor : Date.parse(target.start);
    if (!Number.isFinite(startMs)) {
      s.phase = 'failed';
      emit();
      return;
    }
    const completed = Date.parse(record.completedAt ?? '');
    let endMs = startMs;
    if (Number.isFinite(completed) && completed >= startMs) endMs = completed;
    else if (typeof record.durationMs === 'number' && Number.isFinite(record.durationMs) && record.durationMs >= 0) endMs = startMs + record.durationMs;
    s.record = record;
    s.callStartMs = startMs;
    s.callEndMs = endMs;
    s.phase = 'ready';
    onFound?.({ sourceId: record.sourceId, id: record.id });
    readOverview();
    readWindow();
  }

  function find(): void {
    findCtl?.abort();
    const ctl = new AbortController();
    findCtl = ctl;
    const fail = (): void => {
      if (disposed || ctl.signal.aborted) return;
      s.phase = 'failed';
      emit();
    };
    const readRecord = (call: { sourceId: string; id: string }): void => {
      deps.record(call.sourceId, call.id, ctl.signal).then((rec) => {
        if (disposed || ctl.signal.aborted) return;
        callFound(rec);
      }, fail);
    };
    if (target.call) {
      readRecord(target.call);
      return;
    }
    deps.slowest(drillQuery({ start: target.start, end: target.end }, target.sourceIds), ctl.signal).then((page) => {
      if (disposed || ctl.signal.aborted) return;
      const call = pickCall(page);
      if (!call) {
        s.phase = 'not-found';
        emit();
        return;
      }
      readRecord(call);
    }, fail);
  }

  return {
    state: () => snapshot(),
    start() {
      if (started || disposed) return;
      started = true;
      emit();
      find();
    },
    retry() {
      if (disposed) return;
      started = true;
      if (s.record) {
        // The call is known: read the strip and the window again.
        s.overview = null;
        s.visible = null;
        s.nearby = { rows: [], loaded: null, asking: false, failed: false };
        readOverview();
        readWindow();
        return;
      }
      abortAll();
      s = { ...initialState(), window: s.window, chip: s.chip };
      emit();
      find();
    },
    preview(w) {
      if (disposed) return;
      s.window = { ...w };
      emit();
    },
    commit(w) {
      if (disposed) return;
      s.window = { ...w };
      if (!s.record) {
        emit();
        return;
      }
      readWindow();
    },
    setChip(c) {
      if (disposed || c === s.chip) return;
      s.chip = c;
      if (!s.record) {
        emit();
        return;
      }
      // Only the nearby calls depend on the chip: no other read starts.
      readNearby();
      emit();
    },
    dispose() {
      disposed = true;
      abortAll();
    }
  };
}
