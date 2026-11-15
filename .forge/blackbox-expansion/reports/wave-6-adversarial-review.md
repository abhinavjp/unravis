## Executive Risk Summary

**Risk: High. Scope: canonical wave 6, HEAD `33c219b4b270229f44cc2b09ab234b1c6b0c72dc` (`33c219b`). Verdict: No-Go.**

Top three blockers:
1. The event cursor skips unread events after cap overflow or cross-channel skew, and moves before a recording is successfully written (W6-01/W6-06).
2. The event cap counts successfully rendered rows, not attempted reads; TCP allocates the full native table before applying its scan cap (W6-02/W6-03).
3. Partial TCP readings and process-read recovery can produce plausible but incorrect numbers without a corresponding field-quality signal (W6-04/W6-05).

Read [CLAUDE.md](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/CLAUDE.md#L1), [handoff rules](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/docs/research/blackbox-expansion-handoff.md#L18) and owned BBX-4-01 through BBX-4-05 packets. Full owned-source reads: [events.go](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/blackbox/events.go#L1), [events_windows.go](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/blackbox/events_windows.go#L1), [events_other.go](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/blackbox/events_other.go#L1), [tcp.go](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/blackbox/tcp.go#L1), [tcptable_windows.go](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/blackbox/tcptable_windows.go#L1), [tcptable_other.go](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/blackbox/tcptable_other.go#L1), [aspnet.go](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/blackbox/aspnet.go#L1), [mempressure.go](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/blackbox/mempressure.go#L1), [collector.go](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/blackbox/collector.go#L1) and [sample.go](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/blackbox/sample.go#L1), plus direct callers, native readers, consumers and tests. No historic draft or alternate worktree was used as proof.

**Evidence boundary:** findings are source-established conditional traces, not production incident reports. **Unverified Assumption:** the deployed event provider/ID pairs, XPath support, render variant types, TCP API address-family coverage and exact PDH paths/types/units match the code. In particular, ASP.NET `Requests Rejected` is tagged `KindPDHRate` (aspnet.go:24–29), while memory `Pages Input/sec` is exposed as `hardFaultsPerSec` (mempressure.go:24–29). No live provider metadata establishes that rejection output is a rate rather than a cumulative count, or that pages read equal fault events. These semantic assumptions require verification; they are not represented below as proven OS defects. Comments claiming earlier live verification are not evidence from this audit.

## Findings Table

| ID | Severity | Category | Component/Location | Summary |
|---|---|---|---|---|
| W6-01 | High | Event cursor / loss | events_windows.go:56–84; events.go:89–92, 140–153 | Newest-first capped reads advance one global watermark past unread events and other channels. |
| W6-02 | High | Event budget / partial reads | events_windows.go:97–142 | Failed renders do not consume the cap and disappear without an error or missing-quality signal. |
| W6-03 | High | TCP memory / truncation | tcptable_windows.go:25–39; tcp.go:184–207 | Native-table allocation is uncapped; capped/truncated state counts are presented as complete. |
| W6-04 | Medium | TCP partial availability | tcp.go:126–165 | Outbound queue can be a partial sum, even a false zero; wildcard refresh failure also suppresses independent states. |
| W6-05 | Medium | Process rate recovery | collector.go:141–152, 175–177, 252–254 | A failed process enumeration leaves the CPU numerator baseline older than its denominator timestamp. |
| W6-06 | High | Event cursor / persistence | events.go:148–157; recorder.go:207–247 | Event watermark commits before Store.Append; a failed write permanently skips those events. |

## Detailed Findings

**W6-01 — The cursor advances past data that was never read**

**The Flaw:** Event queries are newest-first (`evtQueryReverseDirection`, events_windows.go:28, 84). `queryEvents` reads System first, uses a shared cap and may never query Application (56–71; channel ordering in events.go:20–24). `Collect` advances a single timestamp to the maximum returned time (events.go:148–153). `aggregateEvents` discards events at or before it (89–92). There is no record-ID/bookmark or per-channel cursor.

**Exploit / Failure Scenario:**
1. Between ticks, System accumulates more allowlisted events than the configured cap. An Application crash also occurs in that interval.
2. The query returns only the newest System rows and stops at the cap; older System rows and the entire Application channel remain unread.
3. `Collect` advances the watermark to the newest returned System timestamp.
4. Subsequent queries no longer request those older events. No overflow/gap marker says they were lost.
5. Even below the cap, an event that becomes visible later with the same or an older timestamp than the watermark is discarded. Equal timestamps are not unique event identities.

This trace follows the source's cap/order/cursor logic; the cap is 256 (events.go:18), not a measured production event count. Real event arrival/visibility timing is **Unverified Assumption**. [events_test.go](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/blackbox/events_test.go#L84), lines 84–141, assumes the fake query supplies all newer rows; its aggregation-cap test does not model native query truncation. The strict timestamp test prevents simple replay duplicates by discarding equal times, but sacrifices legitimate events with the same timestamp.

**Remediation:** Use per-channel record IDs/bookmarks, bounded forward progress and fair channel budgeting. Advance only over consumed records; preserve continuation at a cap, or emit an explicit numeric/fixed-code loss marker if intentionally dropping backlog. Handle log clear/reset and duplicate IDs within channel/generation. Add overflow, equal-time, delayed-visibility, cross-channel starvation and replay tests.

**W6-02 — Render failures defeat the advertised event-read cap**

**The Flaw:** `queryEventChannel` loops on `len(rows) < limit` (events_windows.go:97–103) but increments rows only when `renderEventRow` succeeds (110–113). A failed `EvtRender`, insufficient returned values or unexpected ID/time type yields false with no surfaced error (125–135). Those failed reads do not consume the budget. Unexpected Level type is even converted to numeric zero (137–142), conflating unreadable level with a real value. Successfully read rows around a failed row can move the global watermark past it.

**Exploit / Failure Scenario:**
1. A matching backlog exists while `EvtRender` fails or returns an unsupported variant shape for many records.
2. Each `EvtNext` still fetches and closes handles, but `len(rows)` barely grows or remains zero.
3. The loop can scan the entire matching backlog rather than at most the advertised read cap. There is no elapsed-time check inside it.
4. If some newer rows render successfully, `Collect` returns partial evidence and moves the watermark past rejected rows without a partial-read signal. If none render, a later tick can retry the same large window again.

No particular server's variant behavior is asserted: triggering native failures/types are **Unverified Assumption**. The failure path, accepted-row budget and absence of diagnostics are verified. Existing pure event tests do not invoke this native render loop.

**Remediation:** Charge the cap for every fetched/attempted event, regardless of render success, and impose a cooperative elapsed-work budget between bounded native calls. Return partial-quality/error information, not silent success; keep the cursor at a safe continuation point. Do not default an unreadable Level to 0. Add fake native API seams for repeated render failure, unexpected variants, mixed valid/invalid batches and timeout behavior.

**W6-03 — TCP's scan cap neither bounds memory nor declares incomplete counts**

**The Flaw:** `tcpTableCounts` accepts the native size and allocates `make([]byte, size)` before applying any cap (tcptable_windows.go:25–34). `tcpStateCountsFromTable` later clamps the row count to `maxTCPStateRows` and to the number of complete rows that fit (tcp.go:184–194), returning ordinary counts without a truncation indicator (201–207). [groups_types.go](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/blackbox/groups_types.go#L124), lines 124–134, contains no coverage/completeness field.

**Exploit / Failure Scenario:**
1. The machine has a native TCP table larger than the scan cap.
2. The sampler first allocates space for the entire table, including native address/port/PID bytes; the later cap cannot undo that allocation or native population cost.
3. It counts only the first capped rows and emits the result with the same shape as a complete small table.
4. Connections in omitted rows do not contribute to their states. Under heavy connection churn, an investigator sees an undercount with no indication that evidence was truncated.

The allocation and truncation behavior is verified; exceeding the 50 MB working-set budget on a deployed machine is **Unverified Assumption**, not measured. [tcp_test.go](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/blackbox/tcp_test.go#L128), lines 128–140, explicitly expects truncated counts but cannot verify native allocation bounds. No address serialization or disk spill was found; the issue includes transient raw-table memory, not an invented persistent-address leak.

**Remediation:** Enforce a checked byte/row ceiling before allocation and bound retries if the table grows. If a complete table cannot be read within budget, emit fixed unavailable/truncated status rather than unlabeled complete counts. Add a native-size seam test proving oversize tables are rejected before allocating, and tests distinguishing empty, unreadable and incomplete tables.

**W6-04 — TCP partial failures can look like a healthy total**

**The Flaw:** The outbound queue loop sums every valid key and emits whenever `n > 0` (tcp.go:151–161); it never requires all expected interfaces to be readable. This differs from the explicit both-family completeness checks for retransmits/resets (138–149). Also, a `RefreshInstances` error returns at 128–129 before the independent `counts()` call (164), although a normal PDH collect error permits state collection.

**Exploit / Failure Scenario:**
1. Two queue paths are present. Interface A returns valid zero; B's counter is unreadable.
2. `n` is positive and the published machine-wide `OutboundQueue` becomes 0. The missing interface has effectively been treated as contributing zero without saying so.
3. Separately, an interface wildcard refresh fails while `GetTcpTable2` would succeed. The early return prevents the state query, losing independent evidence along with PDH data.

Both are deterministic error-handling traces. The actual interface status/provider behavior on a server remains **Unverified Assumption**. `TestTCPSumV4V6` uses valid queue readings and `TestTCPMissingOmitted` covers missing address-family rates/state-query errors, not partial interface queues or a refresh error (tcp_test.go:236–292).

**Remediation:** Require complete membership/readability for a total, or expose a clearly marked partial sum and covered-interface count. Separate refresh/rate and state-query outcomes so an unavailable PDH side does not suppress valid states. Test zero-plus-missing interface data and failing refresh with successful state counts.

**W6-05 — Process-read recovery overstates CPU and can change the busiest row**

**The Flaw:** On process enumeration error, `Collect` keeps `prevProc` but still advances the common `prevAt` (collector.go:141–152). At the next successful read, `topProcesses` subtracts CPU seconds from the older successful process snapshot (175–177), then divides by wall time since the more recent failed tick (147, 252–254). The source can fail during enumeration: [internal/sampler/source_windows.go](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/sampler/source_windows.go#L137), lines 137–158.

**Exploit / Failure Scenario:**
1. A process is read successfully at t0, establishing its cumulative CPU baseline.
2. The next process enumeration fails; host collection succeeds. `prevAt` advances, but process CPU baselines do not.
3. On recovery, the numerator covers both intervals and the denominator only the last interval. For equal intervals and steady CPU, the resulting percentage is doubled; work from the failed interval can also dominate the recovered interval's ranking.
4. The recovered row is emitted without a process-rate quality warning. Downstream host analysis uses its percentage unchanged.

This is a source/arithmetic trace, not a newly executed recovery test. [collector_test.go](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/blackbox/collector_test.go#L455), lines 455–473, checks failure reporting but not failure followed by recovery. Direct consumer [internal/server/blackbox.go](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/server/blackbox.go#L1192), lines 1192–1214, copies CPUPct; [internal/investigate/hostlane.go](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/investigate/hostlane.go#L179), lines 179–201 and 389–405, aggregates it for the busiest-process conclusion.

**Remediation:** Track the last successful process-snapshot timestamp separately, ideally with each valid CPU baseline, or invalidate process rate baselines on enumeration failure. Do not use host success to advance process-rate time. Add failure/recovery and partial-read tests with changing CPU distribution, asserting both correct percentage and ranking.

**W6-06 — Cursor commits before evidence is durably accepted**

**The Flaw:** `eventsGroup.Collect` advances its watermark before returning the group (events.go:148–157). [recorder.go](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/blackbox/recorder.go#L207), lines 207–247, collects first and calls `Store.Append` later. On append error, the sample is abandoned; there is no acknowledgement/rollback path to the event collector and no pending batch retained for retry.

**Exploit / Failure Scenario:**
1. Event collection obtains new allowlisted records and moves the in-memory watermark beyond them.
2. The subsequent sample write fails, for example because the recording volume cannot accept the append.
3. The recorder flags write failure and returns without storing that event batch.
4. On recovery, event queries start after the already advanced watermark. Those events are never emitted again, even though they still exist in the Windows log.

This is a verified caller/callee ordering flaw under an append failure, not an executed disk-full test. A generic write-failed marker cannot reconstruct the missing event IDs/counts. Retrying without a designed acknowledgement/deduplication rule would introduce the opposite problem: duplicates after an ambiguous partial write.

**Remediation:** Separate pending read progress from committed cursor progress. Acknowledge the event batch after successful sample append, or retain bounded pending batches with stable event identities and explicit overflow/loss handling. Define idempotence for partial writes and restart behavior. Test append failure, successful retry and ambiguous write outcomes with exact once-or-explicit-loss expectations; never silently skip a batch.

## Cross-Wave Impact

- **Rate-starvation dependency (W4-04):** TCP recreates its engine on any process-set change (tcp.go:126–132); this erases direct TCP rate baselines even when interfaces and TCP counters are unchanged. [engine.go](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/blackbox/engine.go#L91), lines 91–112, then gates two samples again. TCP's cumulative-reset `KindRate` conversion is present (tcp.go:86–92); the old raw-total-as-rate defect should not be reported against this HEAD.
- The top-list algorithm takes TopCPU=10 and TopMem=5, dedupes by `procID` and appends matching selected identities (sample.go:16–22; collector.go:190–240). Therefore the bound is at most the merged top lists plus distinct readable matching selected identities, not a universal 15 rows. Idle is excluded at collector.go:171–172. The memory ranking adds another full sort and selection scans; fewer emitted rows do not prove equal CPU cost. No measured cost equality is claimed.
- Selected inclusion depends on the wave-4 resolver and is conditional on process recording being enabled and a matching readable creation identity. [host.go](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/blackbox/host.go#L97), lines 97–106, wires the same callback; collector.go:141, 225–237 applies it. An out-of-process pool can resolve to its child, not w3wp. Existing `TestStep9StillFindsWebServer` uses a directly selected w3wp with fixed identity (collector_test.go:407–452); it is not evidence for the real child-retargeted layout. Resolver W4-01/W4-02 and wave-5 identity defects must be addressed together.
- Per-group timing is retrospective, not a hard deadline: [groups.go](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/blackbox/groups.go#L182), lines 182–196. This cannot stop an event scan/native call in progress. The existing fake gate substitutes groups and disables process collection: [budget_bench_test.go](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/blackbox/budget_bench_test.go#L208), lines 208–248. Its PASS does not certify event/TCP allocation, the dual process sort, native initialization or one-second enforcement.
- Event rendering requests only numeric system values (events_windows.go:36–41, 89–90); no message/EventData text was found. TCP serialization contains State/Count only, though the native temporary buffer necessarily contains more fields. The owned ASP.NET/memory collectors have no text-capture path; their native semantic/localization assumptions remain unverified. PDH factory failures and missing-group diagnostics depend on the foundation lifecycle described in W4-06, not a new allegation of SQL or text leakage.
- BBX-3-04 EventPipe, BBX-6-01 steal and BBX-6-03 allocation sampling are blocked/not built. None is counted as a missing-feature finding; VM scheduling claims or managed allocation conclusions must not be inferred from this wave's limited counters.

## Go / No-Go Verdict

**No-Go** until event-loss, bound enforcement and incorrect partial/recovery values are addressed.

Measurable release conditions:
- Event tests cover cap overflow, fair channel progress, equal timestamps, delayed records, failed renders, log resets, append failure and retry. Every eligible event is either represented once or covered by an explicit loss marker; no invisible cursor leap and no unbounded scan through failed renders.
- Oversize TCP size responses are rejected before allocating beyond a declared ceiling. Truncated/unreadable/empty tables are distinguishable, partial interface data never appears as a complete zero, and valid state counts survive PDH refresh failure.
- Failed/recovered process reads use aligned CPU numerator/time baselines. Tests retain top-10 CPU/top-5 memory union, identity dedupe, Idle exclusion and selected inclusion for every supported hosting layout; no blanket 15-row assertion when selected targets add rows.
- Verify real-server event provider/ID and variant contracts, IPv4/IPv6 state coverage, interface membership, ASP.NET cumulative-versus-rate semantics and pages-versus-faults units. Test English/non-English PDH paths. No new live service operations are authorized by this report; these are release-gate requirements for separately authorized validation.
- Measure actual native workloads, worst supported event/table/process population and target-resolution cost: under 0.5% of one core on average, under 50 MB working set and a bounded one-second group policy. The observed number of test allocations is not a working-set measurement.

**Validation performed:** offline bounded fake-reader test selection in `go test ./internal/blackbox` covering all reviewed-wave families plus top-process and `TestStep9StillFindsWebServer`, `-count=1 -timeout=60s`: PASS (`ok`, 0.822s). Separate fake-budget and fake-W3WP-refresh selection, `-count=1 -timeout=30s`: PASS (`ok`, 0.552s). `go vet ./internal/blackbox` for Windows and `GOOS=linux`: PASS. All commands used the supplied Go binary with `GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local`. Tests used existing fakes; no live event/TCP query, service mutation, network attack, full application build/full suite or disk-full experiment was run. The newly described adversarial scenarios were source-traced, not executed as new tests. No test-count totals or production measurements are invented. Only the three authorized Markdown reports were written.
