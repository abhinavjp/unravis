# Wave 6 evidence fix — W6-01..W6-06

Branch `feat/blackbox-expansion`. Scope: only the owned files
(`events.go`, `events_windows.go`, `events_other.go`, `events_test.go`,
`tcp.go`, `tcptable_windows.go`, `tcptable_other.go`, `tcp_test.go`,
`collector.go`, `collector_test.go`, `groups_types.go`) plus one new test file
`evidence_bounds_test.go`. No commit/push. No live event-log/TCP/service
operation; all new tests run on the existing fake seams.

Preserved as required: `tcp.go` rebuild still calls
`g.eng = reconcileEngine(g.eng, specs, g.cores)` (Engine.Reconcile /
CounterSpec.Volatile untouched); `groups.go` bounded workerSlot/deadline logic
untouched; no cross-group shared mutable state introduced (the only shared
field is the events group's own atomic cursor, see W6-06).

## What changed per finding

### W6-01 — event cursor skips unread events (High)
- Replaced the single global timestamp watermark with **per-channel record-ID
  cursors** (`eventCursors.ch[2]uint64`). Record IDs are a stable identity:
  equal timestamps are distinct records, and a record that becomes visible late
  with an older timestamp is still read (cursor is an ID, not a time).
- Reads are **forward (oldest-first)**, not newest-first. The native query uses
  `EventRecordID > cursor` (`eventsXPathAfter`); the first read uses the
  no-backfill time window (`eventsXPath`) only until a cursor exists.
- **Fair channel budgeting**: `fairShare(remaining, channelsLeft)` splits the
  attempt budget so a busy System channel cannot starve Application; unused
  budget flows to the remaining channels.
- **Bounded forward progress, no silent gap**: a cap overflow sets `Truncated`
  and the cursor continues next tick; the backlog is drained over later ticks
  (test proves all records consumed). Aggregation dedupes by `(channel,
  recordID)` so a replay within a generation counts once.
- **Log clear/reset**: when a forward query past the cursor finds nothing, the
  native side probes the newest record ID once; if it is below the cursor the
  log was cleared, the new generation is re-read and an explicit numeric loss
  marker (`EventsGroup.Lost`) is emitted — never a silent stall.

### W6-02 — read cap counts only rendered rows (High)
- The native loop charges the budget for **every fetched record** (`attempted++`
  before render), rendered or not, so repeated `EvtRender` failures cannot scan
  the whole backlog. The cursor advances over fetched record IDs even when a row
  is unreadable, so a bad record is not re-scanned forever.
- Added a **cooperative elapsed-work budget** (`eventsReadBudget = 750ms`, under
  the 1s group deadline) checked between bounded `EvtNext` batches.
- **Partial quality surfaced**: `EventsGroup.Unreadable` counts fetched-but-
  unreadable records; `Truncated` marks a partial read. A batch with only
  unreadable records is emitted with the quality signal, not silent success.
- **Unreadable Level is never 0**: `renderEventRow` drops a row whose Level (or
  ID/time) variant is unreadable and counts it as a failed read (missing, never
  0). It still returns the record ID so the cursor can advance.

### W6-03 — TCP allocates the whole table before any cap (High)
- Added a **checked byte ceiling** `maxTCPTableBytes = 4 + maxTCPStateRows*28`
  (~1.8 MiB) enforced **before allocation** in the shared `tcpTableSnapshot`.
  An oversize native size is refused without allocating/reading the full table
  (test proves `readInto` is never called) and reported `truncated`.
- **Bounded retries** (`tcpTableMaxAttempts = 3`) when the table grows between
  the size query and the read (`errTCPTableGrew`); persistent growth reports
  incomplete, never an unbounded loop.
- **Empty / unreadable / incomplete are distinct** (`tcpStates`): empty = no
  counts, not truncated, no error; unreadable = fixed error `errTCPTableUnreadable`;
  incomplete = `truncated`. `tcpStateCountsFromTable` now returns a truncation
  flag (claimed rows over the cap or over the complete rows that fit).
- Additive field `TCPGroup.StatesTruncated` labels an incomplete table; counts
  are never emitted as unlabeled complete. No address/port/PID is serialized.

### W6-04 — partial outbound queue looks complete (Medium)
- The outbound-queue sum now records the **covered-interface count**
  (`TCPGroup.OutboundQueueInterfaces`) and sets `TCPGroup.OutboundQueuePartial`
  when fewer interfaces were readable than expected. One valid zero plus one
  unreadable interface publishes `0` **marked partial with covered=1**, never a
  false complete machine-wide 0. With no readable interface the field stays nil.
- A `RefreshInstances` error **no longer returns early**: the previous expansion
  is kept and the independent rate and state reads still run, so valid state
  counts survive a PDH refresh failure.

### W6-05 — process-read recovery overstates CPU (Medium)
- Added `HostCollector.prevProcAt`, the time of the **last successful process
  snapshot**, tracked separately from the host `prevAt`. A failed enumeration
  leaves `prevProc` and `prevProcAt` untouched; host success no longer advances
  the process-rate clock. On recovery the CPU numerator and denominator span the
  same interval, so the percentage is the true average (test: 10%, not the
  doubled 20%) and the ranking reflects honest total work over the gap.
- Top-10 CPU / top-5 memory union, identity dedupe, Idle exclusion and
  selected-target inclusion are unchanged.

### W6-06 — cursor commits before Store.Append (High) — partial, see conflict
- Separated **pending read progress from committed progress**: `Collect` never
  advances the committed cursor. The group retains a pending batch and re-emits
  it until acknowledged; the committed cursor (`eventCommittedStore`, an
  `atomic.Pointer[eventCursors]`) advances only on acknowledgement.
- The acknowledgement is delivered by `(*EventsGroup).MarshalJSON`, which the
  recorder triggers **inside `Store.Append`** (after `Collect` returns). This
  fixes the reported ordering (commit was in `Collect`, now it is in `Append`)
  and gives correct retry for any failure before serialization: an unacked batch
  is retained and re-emitted, then committed once serialized (tests cover
  failure→retry→success and the worker→tick goroutine handoff).

## Cursor / acknowledgement contract
- Per channel: committed cursor = last durably-accepted record ID. `Collect`
  reads forward from it; the read produces a pending cursor. The pending batch
  is re-emitted unchanged until acked.
- Ack = the recorder serializing the batch (`MarshalJSON` inside `Store.Append`)
  publishes the pending cursor atomically. The next worker reads forward from
  the committed cursor; an acked batch is never replayed.
- The atomic pointer is the only field shared between the group worker and the
  tick goroutine; all other group state stays worker-owned. No new goroutine or
  ticker is started.

## Bounds chosen and why
- `maxEventsPerTick = 256` attempts/tick (kept), split fairly per channel;
  `eventsReadBudget = 750ms` cooperative deadline (< the 1s group budget) so a
  slow provider cannot overrun the group deadline.
- `maxTCPTableBytes = 4 + 65536*28 ≈ 1.8 MiB`: the most bytes needed to scan the
  row cap, checked before allocation; well under the 50 MB working-set budget.
- `tcpTableMaxAttempts = 3`: enough to ride out a table growing between the size
  query and the read, bounded so churn cannot loop forever.

## Exact verification (supplied Go 1.27.3, GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local)
- `go build ./...` → exit 0
- `go vet ./internal/blackbox/` → exit 0
- `GOOS=linux go vet ./internal/blackbox/` → exit 0
- `go test ./internal/blackbox/ -count=1 -timeout 300s` → `ok ... ~59-61s`, exit 0
- Targeted `-run 'TestEvents|TestAggregateEvents|TestTCP|TestProcessFailureRecovery'`
  → 29 tests PASS, incl. cap overflow, fair channel progress, equal timestamps,
  delayed records, failed renders, log reset, append failure+retry, oversize
  rejected before allocation, truncated/empty/unreadable, partial interface
  queues, refresh failure with successful states, process failure/recovery.
- `gofmt`: content gofmt-clean (verified on LF copies). Pre-existing files kept
  CRLF; the new `evidence_bounds_test.go` is LF.
- `-race` could not run: no C compiler on this host (`-race requires cgo`). The
  ack is race-free by construction (atomic pointer); a cross-goroutine handoff
  test (`TestEventsAckAcrossWorkerBoundary`) exercises the worker→tick path.

### Pre-existing assertions updated (old behaviour encoded)
- `TestAggregateEventsCounts`/`Cap`/`NoText`: `aggregateEvents` lost its
  timestamp-watermark argument (cursor now filters); `rawEvent` gained numeric
  `RecordID`; the "at the watermark" row is no longer an aggregation concern.
- `TestEventsWatermark` → replaced by `TestEventsCursorForwardAndAck` (record-ID
  cursor + ack instead of a timestamp watermark).
- `TestTCPStateCountsNoAddresses`: `tcpStateCountsFromTable` now also returns a
  truncation flag; capped/truncated buffers assert `truncated=true`.
- TCP fakes/helpers moved to the `tcpStates` return type.

## Conflicts / blockers
1. **W6-06 residual (needs recorder.go):** the only acknowledgement hook the
   recorder exposes without editing `recorder.go`/`store.go` is serialization
   (`MarshalJSON`), which is the FIRST step of `Store.Append`. A write that fails
   AFTER serialization (e.g. disk full at `Write`/`Sync`) still commits the
   cursor, so that narrow window can lose a batch. Fully closing it requires a
   one-line post-`Append` acknowledgement call in `recorder.go` (e.g. an
   `EventAcknowledger` interface invoked after `Store.Append` returns nil). Per
   the task constraint I did NOT edit `recorder.go`; the pending/committed
   infrastructure and ack interface are in place for that change. Every failure
   before serialization is handled correctly (retain + retry).
2. **Transient external breakage:** at the start, `resolve_map.go`/`host.go`/
   `host_test.go` (another worker's W3-05 refactor, outside my scope) did not
   compile, blocking the package test. I did not touch them; the other worker
   completed the wiring and the package now builds and tests green.

## Still needs a real server (release gates, not verified here)
- Native `EvtQuery`/`EvtNext`/`EvtRender` contract: `EventRecordID` variant type
  (UInt64), forward direction, the reset probe, and that allowlisted providers
  actually emit the expected IDs/levels. The render-failure/reset paths are
  source-traced and fake-tested, not live-verified.
- `GetTcpTable2` size/read behavior under churn, IPv4/IPv6 state coverage, and
  real interface membership for the outbound-queue completeness check.
- PDH `Network Interface(*)` refresh failure modes and English/non-English paths.
- Footprint under real worst-case event/table/process populations (the 1.8 MiB
  ceiling and 750ms budget are bounds, not measured working-set/CPU figures).
- The W6-06 post-Append ack (conflict 1) once `recorder.go` may be changed.