# P-24 Slow call controller (all reads, one place)

model: sonnet, effort: high. Reason: orchestrates five reads with abort, stale-answer and drag rules; plain TS so it can be tested with fakes.
risk: medium. Review batch: RB-5. Wave: W1. Requirements: R-02, R-12, integration point I-2.

## Outcome
One plain-TypeScript controller finds the call, reads the overview and the visible window from the tracing endpoint, reads nearby calls and event markers, and tells the screen when anything changed. Components hold no fetch logic.

## Write scope
- `web/src/lib/drill/callController.ts` (new)
- `web/tests/slowcall.controller.test.cjs` (new)

## Must not change
- `web/src/lib/api.ts`, `web/src/lib/tracing/model.ts`, `web/src/lib/drill/target.ts` (import only). No Svelte runes in this file (it must load in the `.cjs` test).

## Changes
1. Export:
```ts
export interface CallDeps {
  slowest(query: Query, signal: AbortSignal): Promise<Page>;                       // api.slowest
  record(sourceId: string, id: string, signal: AbortSignal): Promise<RecordDetail>; // api.record
  tracing(caseId: string, startIso: string, endIso: string, sourceIds: string[], signal: AbortSignal): Promise<TracingResponse | null>;  // null = stale answer
  activity(input: ActivityInput, signal: AbortSignal): Promise<Page>;               // api.activity
  events(caseId: string, fromIso: string, toIso: string, signal: AbortSignal): Promise<CallEvents>;   // fetchCallEvents
}
export type CallPhase = 'finding' | 'ready' | 'not-found' | 'failed';
export interface CallState {
  phase: CallPhase; record: DrillRecord | null; callStartMs: number; callEndMs: number;
  window: CallWindow; chip: NearbyChip;
  overview: TracingResponse | null; overviewFailed: boolean;
  visible: TracingResponse | null; visibleLoading: boolean; visibleFailed: boolean;
  slowRows: RequestRecord[];                                   // slowest API calls in the visible window (max 50)
  nearby: { rows: RequestRecord[]; loaded: Loaded | null; asking: boolean; failed: boolean };
  events: CallEvent[]; eventsTruncated: boolean;
}
export function createCallController(target: SlowCallTarget, deps: CallDeps, onChange: (s: CallState) => void, onFound?: (call: { sourceId: string; id: string }) => void): {
  state(): CallState; start(): void; retry(): void;
  preview(w: CallWindow): void;      // while dragging: updates state.window only, no read
  commit(w: CallWindow): void;       // drag end, preset, Earlier/Later: sets window and reads
  setChip(c: NearbyChip): void; dispose(): void;
};
```
2. Finding the call: if `target.call` is set, `record(...)` directly. Else `slowest(drillQuery({start: target.start, end: target.end}, target.sourceIds))` → `pickCall(page)` → `record`. No hit → `not-found`. On success `parsePerf` builds the `DrillRecord` exactly as `DrillPopup.svelte` does today (read it; copy the logic, not the markup), then `onFound(call)`.
3. Reads after the call is known, all in parallel, each with its own `AbortController`:
   - overview: `tracing(caseId, overviewBounds(callStart))`, once.
   - visible: `tracing(caseId, windowBounds(...))`; skipped when `coversOverview` or the window equals the overview (reuse the overview answer as `visible`).
   - slow rows: `slowest(drillQuery(windowBounds, target.sourceIds))`.
   - nearby: `activity(activityInput(...))` only when `needsServer(loaded, chip, start, end)`.
   - events: `events(caseId, start, end)` for the visible window.
4. `commit` aborts the previous visible, slow-rows, nearby and events reads before starting new ones. An answer that arrives after abort or after a newer `commit` is dropped (keep a counter). `preview` never reads. `dispose` aborts everything and stops calling `onChange`.
5. A failed overview or events read does not fail the screen: set the flag, keep the rest. A failed record read → `failed`.
6. The real `tracing` dep (built by P-25, not here) wraps `fetchTracing(caseId, body, nextRequestSeq(), signal)` from `web/src/lib/tracing/model.ts`. The body needs `incident` and `reference` ranges; P-25 mirrors how `TracingPaper.svelte` `run()` builds them.
7. Tests with fake deps (promises you resolve by hand): target with `call` skips `slowest`; no hit → `not-found`; `preview` makes zero dep calls; two quick `commit`s → the first visible answer is dropped; ±60 makes no second tracing call; chip change with an untruncated wide `everyone` load makes no `activity` call, a truncated one does; `dispose` then resolve → `onChange` not called again; events failure leaves `phase === 'ready'`.

## Proof
- [ ] `cd web && node --test tests/slowcall.controller.test.cjs` → pass, 0 fail, at least 10 tests.
- [ ] `cd web && npx svelte-check --threshold error` → 0 errors.
- [ ] `Select-String -Path web/src/lib/drill/callController.ts -Pattern '\$state|\$derived|\$effect|guided'` → no match.

## Depends on
P-04, P-05, P-06, P-08, P-10, P-11, P-12.
