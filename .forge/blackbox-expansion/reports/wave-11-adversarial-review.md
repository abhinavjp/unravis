## Executive Risk Summary

**Overall risk: High. Verdict: No-Go for live acceptance of canonical wave 11.** No Critical issue is substantiated by this review. Findings below distinguish source-proven failures from deployment-dependent schema assumptions; passing fixture tests are not live proof.

**Scope:** repository `C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis`, HEAD `33c219b4b270229f44cc2b09ab234b1c6b0c72dc`. Canonical wave 11 is **BBX-5-11, BBX-5-12, BBX-5-13, BBX-5-14**: requests, waits, schedulers, semaphores, tempdb, filestats and AG. Filestats is not “wave 14”; the graph ends at wave 13. Authority: [`.forge/blackbox-expansion/GRAPH.md:44–50`](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/.forge/blackbox-expansion/GRAPH.md#L44), [`.forge/blackbox-expansion/GRAPH.md:67–69`](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/.forge/blackbox-expansion/GRAPH.md#L67).

**Top three blockers:**

1. **Cumulative semaphore values are narrowed before subtraction.** A cumulative value of 2,147,483,648 causes a decode error, not the claimed safe omission of an oversized delta. If this sampler is reached repeatedly, its failure can auto-disable the entire SQL stream (W11-01).
2. **Identity is not preserved.** Same-ID semaphore rows overwrite one another's baselines; AG output cannot distinguish local and remote replica rows with the same database ID (W11-02, W11-03). Validate actual DMV identity before accepting either series.
3. **The persisted evidence cannot reliably describe its coverage.** Baselines survive gaps/reconnects without epoch or interval metadata; SQL-limited results cannot trigger the advertised overflow flag. Null/missing columns can also become invented measurements (W11-04–06).

Full implementations of `requests.go`, `waits.go`, `schedulers.go`, `filestats.go`, `output.go`, `allowlist.go` and `runner.go` were read, together with their relevant tests, `sampler_registry_test.go`, `budget_test.go`, connection/backoff boundaries, the four packets, CLAUDE/handoff rules and C1–C14. The controlling rules require missing rather than zero, bounded overhead and live verification: [`docs/research/blackbox-expansion-handoff.md:18–27`](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/docs/research/blackbox-expansion-handoff.md#L18), [`docs/research/blackbox-sql-security-review.md:22–38`](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/docs/research/blackbox-sql-security-review.md#L22).

**Evidence limits:** the wave reviewer made no SQL connection, external request, service operation, credential access, mutation test or ACL change. No live DMV result metadata was captured. The principal reconciled this report with [W7-02 and its public Microsoft schema references](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/.forge/blackbox-expansion/reports/wave-7-adversarial-review.md#L41): the documented semaphore counters are bigint, relevant small-query counters are nullable, semaphore IDs are not unique across pools, and two source column names in the shipped SQL are invalid. Those documented contracts are not unknown; deployment values, applicability across supported versions, database context and replica behavior still need the explicit live checks below. Existing implementation reports are claims, not proof.

**Tests actually run:** local Go `C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/.dev/toolchains/go/bin/go.exe`, version `go1.27.3 windows/amd64`; working directory was the repository above. Every test command used `GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local` and `-mod=readonly`.

- `go test -mod=readonly ./internal/bbsql -run '^(TestRequests|TestWaits|TestSchedulers|TestSemaphores|TestTempdb|TestFileStats|TestAG|TestDecode|TestOutput|TestRegistry|TestBudgetFixtureMatchesTheRegistry)' -count=1 -timeout=60s` — **PASS**, 0.611 s.
- `go test -mod=readonly ./internal/bbsql -run '^TestSchedulersSemaphoresTempdbRegistered$' -count=2 -v -timeout=30s` — **FAIL on repetition 2**, 0.604 s: `schedulers_test.go:706` receives a zero-valued semaphore payload where a fresh baseline was expected (W11-07).
- `go test -mod=readonly ./internal/bbsql -run '^(TestLintC2|TestLintTOPCaps|TestEveryStatementHasItsOwnRowCap|TestSemaphoresCounterBeyondInt32)$' -count=1 -v -timeout=30s` — **PASS**, 0.481 s. The last test explicitly expects the erroneous narrow-decode outcome; cap lint passing does not prove truncation observability.

C13 runtime/disk-writing benchmarks and the full suite were not run under this report-only boundary. No live performance claim is made.

## Findings Table

| ID | Severity | Category | Component/Location | Summary |
|---|---|---|---|---|
| W11-01 | High | Numeric correctness / availability | `output.go:112–118`; `schedulers.go:149–188` | Cumulative semaphore counters must fit int32 before delta calculation; a valid wide input can fail the entire sampling tick. |
| W11-02 | High | Identity / delta integrity | `schedulers.go:119–120,167–198`; `allowlist.go:49–51` | Same semaphore ID across rows collapses baselines; no pool identity or duplicate rejection exists. Live pool semantics require validation. |
| W11-03 | Medium | Locality / identity | `filestats.go:118–136`; `output.go:139–142` | HADR gating does not establish locality; AG rows lose replica identity and remain ambiguous. |
| W11-04 | Medium | Missing-data / schema validation | `output.go:178–224`; `schedulers.go:39–51` | Unknown-field rejection is not required-field/null validation; absent readings can become RUNNING, zero counters or zero state. |
| W11-05 | Medium | Lifecycle / temporal integrity | `waits.go:52–94`; `filestats.go:36–95`; `schedulers.go:149–202` | Stateful samplers have no gap/epoch reset contract or elapsed-time metadata; reconnects retain old baselines. |
| W11-06 | Medium | Coverage / cap observability | `allowlist.go:29,49,60,67`; `output.go:173–176` | SQL returns at most the output cap, so the cap+1 overflow witness cannot occur on the normal path. |
| W11-07 | Low | State isolation / test reproducibility | `runner.go:89–95,174–181`; sampler `init()` functions | Registry snapshots share mutable sampler objects; repeated-test contamination is reproduced, multiple production runners are not established. |
| W11-08 | Medium | Performance assurance / acceptance gate | `budget_test.go:69–74,133–152,198–212`; `sampler_registry_test.go:115–145` | Budget tests replace real samplers with decoders, use small numeric values and do not measure source-scan cost. |

## Detailed Findings

### W11-01 — Cumulative semaphore narrowing can disable unrelated samplers

**The Flaw:**

`semaphoresSampler.Decode` first decodes cumulative `forced_grant_count` and `timeout_count` directly into `Semaphores.Forced/Timeouts`, both int32. Conversion to the int64 baseline and the `math.MaxInt32` delta guard happen only after that succeeds. Thus the guard cannot protect a cumulative input above MaxInt32. Evidence: [`internal/bbsql/output.go:112–118`](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/bbsql/output.go#L112), [`internal/bbsql/output.go:152–153`](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/bbsql/output.go#L152), [`internal/bbsql/schedulers.go:149–188`](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/bbsql/schedulers.go#L149).

The integration rejection reason incorrectly argues about an implausibly large *15-second delta*: [`.forge/blackbox-expansion/reports/BBX-5-11-12-13-17-integration.md:55–59`](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/.forge/blackbox-expansion/reports/BBX-5-11-12-13-17-integration.md#L55). The implementation fails on the *lifetime input*, even when the desired delta is one. Its assertion that the lane reader requires this shape is not established by the actual lane boundary described below.

**Exploit / Failure Scenario:**

Preconditions: a reachable semaphore sample contains a cumulative counter above 2,147,483,647; earlier samplers and connection controls succeed. The [documented DMV counter types are bigint](https://learn.microsoft.com/en-us/sql/relational-databases/system-dynamic-management-objects/sys-dm-exec-query-resource-semaphores-transact-sql?view=sql-server-ver17), confirmed in the wave-7 review. **Unverified Assumption:** sufficiently large lifetime values and supported-version behavior on the actual deployment; no live instance was queried.

1. SQL sends the uncast cumulative columns from [`internal/bbsql/allowlist.go:49–51`](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/bbsql/allowlist.go#L49).
2. A value of 2,147,483,648 reaches JSON decoding into int32 and returns `errDecode` at [`internal/bbsql/output.go:219–222`](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/bbsql/output.go#L219). The existing [`internal/bbsql/schedulers_test.go:504–513`](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/bbsql/schedulers_test.go#L504), rerun here, confirms this exact input failure. It does not prove a live DMV supplied it.
3. No delta or new baseline is calculated. This is not merely a missing semaphore row: [`internal/bbsql/runner.go:302–305`](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/bbsql/runner.go#L302) invokes the global failure path, closes the connection and persists failure state ([`internal/bbsql/runner.go:383–390`](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/bbsql/runner.go#L383)).
4. The unchanged high cumulative value fails again. Ten failing ticks latch auto-off for all SQL sampling ([`internal/bbsql/backoff.go:69–85`](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/bbsql/backoff.go#L69)). Earlier successful samplers in such a tick do not reach the end-of-tick success reset. Later kinds are skipped. An earlier statement failure can mask this path at HEAD; see Cross-Wave Impact.

**Remediation:**

Use a separate validated raw cumulative type with nullable int64 counters, calculate differences in the wide domain, then range-check the chosen output type. Widening the *input* does not require widening the emitted delta contract. Treat schema-defined nonapplicable counters separately from unreadable required data. Replace the error-expecting test with sequences `2147483648 → 2147483649`, MaxInt32 crossing, true reset, null and maximum supported values. Verify through the real runner that a small delta above the old lifetime ceiling does not increment global failures. Any output-width change needs explicit C9 compatibility review, not the rejected delta-versus-cumulative argument.

### W11-02 — Semaphore baselines merge different rows with the same ID

**The Flaw:**

The sampler uses `map[int32]semaphoreCumulative` and unconditionally assigns `nextPrev[r.ResourceSemaphoreID]`. It neither rejects duplicate keys nor has a pool discriminator. Both the input projection and output struct omit pool identity. Evidence: [`internal/bbsql/schedulers.go:119–120`](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/bbsql/schedulers.go#L119), [`internal/bbsql/schedulers.go:163–198`](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/bbsql/schedulers.go#L163), [`internal/bbsql/allowlist.go:49–51`](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/bbsql/allowlist.go#L49), [`internal/bbsql/output.go:112–118`](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/bbsql/output.go#L112).

**Exploit / Failure Scenario:**

Preconditions: two retained rows have the same semaphore ID but represent different pools. The public DMV reference checked in wave 7 explicitly documents that `resource_semaphore_id` is not unique across resource pools; the code omits the available `pool_id` discriminator. **Unverified Assumption:** the actual deployment's multi-pool population and values; no live result was obtained. The overwrite and arithmetic below follow directly from the code and the documented identity contract.

1. First sighting contains pool A/id 0 with forced=100, timeout=0 and pool B/id 0 with forced=10, timeout=0, in that order. `nextPrev[0]` ends at 10; the first row's history is gone.
2. Next sighting contains A=101 and B=11, both with unchanged timeouts. Both compare against the previous value 10. Output contains forced deltas **91 and 1**, both labelled resourceSemaphoreId=0, instead of two distinguishable deltas of 1.
3. Reversing equal-ID row order instead makes one pool look reset or changes the next baseline. `ORDER BY resource_semaphore_id` cannot break these ties, including at TOP 16. A cap bounds memory, not key correctness.
4. `Runner.Tick` writes these rows without an identity repair ([`internal/bbsql/runner.go:302–308`](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/bbsql/runner.go#L302)). Tests exercise synthetic unique IDs, including IDs 0 through 16 and shifted ranges, not duplicate pool-local IDs ([`internal/bbsql/schedulers_test.go:366–395`](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/bbsql/schedulers_test.go#L366)).

**Remediation:**

Validate the actual key and nullable columns on supported server versions. Coordinate a fixed allowlist projection of numeric pool ID with wave 7; carry the composite identity into raw rows, baseline keys, deterministic ordering and output. Numeric pool ID is not a pool name; review its C9 addition explicitly. Alternatively, define and validate an intentional SQL aggregate with correct counter lifecycle semantics. Reject duplicate full keys rather than silently overwrite. Test same-ID/different-pool rows, row permutation, pool creation/removal and cap-boundary ties. A decoder-only fix cannot recover an identifier the query discarded.

### W11-03 — HADR feature gating does not make AG rows local or identifiable

**The Flaw:**

The gate proves only `hadr == 1`. The query has no local-row predicate and projects only database ID and synchronization state. `agSampler.Decode` passes every retained row through; `AG` has no replica/group/locality discriminator. This is **identity collapse in the representation**, not an assertion that this function deduplicates or overwrites rows. Evidence: [`internal/bbsql/filestats.go:118–136`](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/bbsql/filestats.go#L118), [`internal/bbsql/allowlist.go:67–75`](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/bbsql/allowlist.go#L67), [`internal/bbsql/output.go:139–142`](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/bbsql/output.go#L139).

**Exploit / Failure Scenario:**

Preconditions: HADR gate succeeds; a local primary's DMV returns local and remote replica-state rows for one database. **Unverified Assumption:** this role-dependent DMV visibility, database-ID meaning, and nullable-state behavior on the deployed versions; no AG instance was queried.

1. The local connection passes C12. That connection restriction does not filter information about remote replicas already exposed by a local DMV.
2. Two retained rows, databaseId=7/state=2 and databaseId=7/state=0, survive as indistinguishable database identities. Their local/remote origin cannot be reconstructed from the NDJSON.
3. A later consumer selecting one row by database ID could choose the wrong state; a consumer retaining both cannot say which replica is affected. No such consumer behavior is claimed as observed at HEAD: the verified immediate impact is ambiguous persisted evidence.
4. `TOP 64 ORDER BY database_id` can additionally cut among equal-ID replica rows. A HADR-on test with one synthetic `{7,2}` row does not test this shape ([`internal/bbsql/filestats_test.go:207–225`](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/bbsql/filestats_test.go#L207)).

**Remediation:**

Choose the contract explicitly. For local-machine evidence, use a validated fixed local-row predicate and require one valid local state per intended identity. If cross-replica aggregate health is intended, obtain scope approval and define an unambiguous reviewed aggregate/identity without replica server names. Coordinate statement changes with wave 7. Test primary and secondary result shapes, multiple replicas, failover, duplicate database IDs, unavailable state and deterministic cap behavior. Do not infer locality from `IsHadrEnabled`.

### W11-04 — Missing required columns and NULL values can become real readings

**The Flaw:**

`DisallowUnknownFields` rejects extra fields, not absent required fields. `decodeRows` creates zero-valued structs and only validates wait-type syntax. For requests it converts *any* decoded empty WaitType into RUNNING, including an absent/null column. Integer nulls retain zero. Schedulers/tempdb check existing values for nil and reject completely empty maps, but accept a nonempty map missing one required total. AG lacks state-domain validation beyond uint8 width. Evidence: [`internal/bbsql/output.go:178–224`](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/bbsql/output.go#L178), [`internal/bbsql/schedulers.go:22–51`](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/bbsql/schedulers.go#L22), [`internal/bbsql/filestats.go:127–136`](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/bbsql/filestats.go#L127).

**Exploit / Failure Scenario:**

Preconditions: a successful Queryer result contains nulls or an incomplete required-column set. Missing columns would require an adapter/statement-contract defect; the fixed SELECT and normal `rows.Columns()` path do not let an HTTP user supply arbitrary Row maps. **Unverified Assumption:** which selected DMV columns actually return null under supported roles/states. The source-level acceptance defect is not a demonstrated remote-input exploit.

1. `DBQueryer.Query` scans columns into `any` and passes values through ([`internal/bbsql/conn.go:322–342`](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/bbsql/conn.go#L322)). JSON decoding into nonpointer scalar fields cannot represent “unknown.”
2. Static examples: a requests row containing only `tasks:4` becomes RUNNING with all other counts zero; `{current_tasks:8}` becomes runnableTasks=0; `{database_id:7, synchronization_state:nil}` becomes syncState=0. These examples are source traces, not newly executed tests. The requests SQL already uses ISNULL, so a real null wait type on that exact statement is not independently established.
3. Wait/file counters can be baselined as zero when a required numeric value is missing/null; a later readable value can then be attributed as an interval delta. File IDs missing from a map likewise become zero-valued keys.
4. The runner persists a successful decode and updates `lastOK` ([`internal/bbsql/runner.go:306–314`](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/bbsql/runner.go#L306)). Conversely, the semaphore blanket-null check drops even readable gauges beside a nonapplicable nullable counter ([`internal/bbsql/schedulers.go:163–166`](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/bbsql/schedulers.go#L163)); schema-specific null handling is needed, not a blanket conversion or blanket skip.

**Remediation:**

Define exact required columns and per-column nullability for each raw result type. Distinguish absent/null/unreadable from a genuine numeric zero. Map only a present, valid empty SQL wait-type value to RUNNING. Validate IDs, nonnegative counts and the confirmed synchronization-state enum; keep unsupported/future states explicitly missing or fixed-code invalid, not arbitrary numeric states. Preserve unknown-column rejection. Add table-driven tests deleting each required column, setting each to nil, testing real zeros and enum bounds; invalidate affected baselines rather than seeding zero. Do not fail an entire row merely because a schema-defined nonapplicable counter is null if independently readable gauges are part of the contract.

### W11-05 — Baselines outlive gaps and reconnects without interval or epoch evidence

**The Flaw:**

The three delta samplers hold counters but no observation time or source epoch. Empty successful replies clear state, while decode errors return without clearing it. The `Sampler` interface has no reset/gap callback. A query failure, skipped sampling, or connection replacement does not tell the sampler anything. `SampleDoc` has one timestamp and no delta start/elapsed/epoch. Evidence: [`internal/bbsql/waits.go:52–94`](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/bbsql/waits.go#L52), [`internal/bbsql/filestats.go:36–95`](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/bbsql/filestats.go#L36), [`internal/bbsql/schedulers.go:149–202`](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/bbsql/schedulers.go#L149), [`internal/bbsql/runner.go:43–48,112–118`](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/bbsql/runner.go#L43).

**Exploit / Failure Scenario:**

Preconditions: at least one baseline exists, then a gap occurs before the same key is observed again. This does not require a second runner or an attacker.

1. A wait/file/semaphore is baselined at t0. A later query fails, a disk check stops sampling, or the tick budget is spent before this kind. Relevant exits: [`internal/bbsql/runner.go:260–266,279–280,297–304`](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/bbsql/runner.go#L260).
2. `fail` drops the connection but not sampler state; `dropConn` clears connection fields only ([`internal/bbsql/runner.go:216–224`](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/bbsql/runner.go#L216), [`internal/bbsql/runner.go:383–388`](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/bbsql/runner.go#L383)). Ordinary hourly reconnection also preserves that state.
3. At t0+600 s, a same-key counter that increased from 100 to 700 emits delta=600 at one timestamp, just as a normal 60-second observation would. The subtraction can be correct over ten minutes, but it is not a measured one-minute amount; the record supplies no explicit interval/continuity evidence. Backoff and slow-demotion change this span too ([`internal/bbsql/runner.go:324–326`](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/bbsql/runner.go#L324)).
4. Worse, if the source reset or a numeric file identity was reused during the gap and the new counters already exceed the old ones, the less-than guards do not detect the epoch change. **Unverified Assumption:** that restart/reuse/overtaking sequence on a deployed instance; it was not induced. Source-level identity has only `(database_id,file_id)` ([`internal/bbsql/filestats.go:10–26`](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/bbsql/filestats.go#L10)).
5. Existing reconnect tests inject `baseSampler`, not real baseline-bearing samplers ([`internal/bbsql/runner_test.go:344–355`](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/bbsql/runner_test.go#L344)); they cannot establish delta continuity. Wait rejection tests explicitly require old baselines to survive ([`internal/bbsql/waits_test.go:407–423`](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/bbsql/waits_test.go#L407)).

**Remediation:**

Coordinate a sampler lifecycle contract with wave 10: per-runner observation time and source generation; explicit invalidation on unknown gaps, relevant decode failures and unproven reconnection epochs. Either baseline-only after a discontinuity or persist validated start/end/elapsed metadata for a continuous cumulative interval. Do not simply divide by the nominal cadence. Add real-sampler runner tests for query failure, partial tick, disk pause, demotion/recovery, reconnect, counter reset that overtakes the previous value, and file-key reuse. Preserve bounded maps and avoid adding text-bearing identifiers.

### W11-06 — SQL TOP hides overflow before the sampler can report truncation

**The Flaw:**

The fixed queries return TOP n where n equals the number of rows the decoder keeps. The decoder marks truncation only after seeing row n+1. The real reader's allowance for n+1 rows does not make SQL supply that extra row. A valid saturated result therefore reports no truncation regardless of how many source rows were excluded. Evidence: [`internal/bbsql/allowlist.go:29–39,49–69`](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/bbsql/allowlist.go#L29), [`internal/bbsql/output.go:20–27`](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/bbsql/output.go#L20), [`internal/bbsql/output.go:173–176`](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/bbsql/output.go#L173), [`internal/bbsql/conn.go:313–315`](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/bbsql/conn.go#L313).

**Exploit / Failure Scenario:**

Preconditions: an eligible source has more rows than its SQL TOP. The exact population on an owner server is unverified; the result-cap contradiction follows from the query and decoder alone.

1. An instance has 257 eligible files. `bb:filestats` returns only the first 256 ordered by database/file ID. A high-ID file carrying the incident's I/O can be excluded indefinitely even while idle low-ID files consume the selected set.
2. `filestatsSampler.Decode` sees exactly its cap, so `truncated=false`. That false flag is omitted from the persisted document ([`internal/bbsql/runner.go:112–118,306–308`](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/bbsql/runner.go#L112)). The retained row set is indistinguishable from a complete 256-file instance. Dropping idle output rows can conceal even the fact that the input hit the cap.
3. The same contract problem affects requests, waits, semaphores and AG. Wait selection is explicitly **lifetime cumulative wait_time_ms**, not highest current delta ([`internal/bbsql/allowlist.go:29–39`](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/bbsql/allowlist.go#L29)). A newly hot wait below that lifetime cutoff is unseen; re-entry consumes a baseline-only cycle ([`internal/bbsql/waits.go:75–81`](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/bbsql/waits.go#L75)). This selection policy is intentional in the packet, but must not be presented as complete interval evidence.
4. Cap tests inject over-cap rows that the corresponding fixed SQL cannot normally return; the lint test actually requires SQL TOP to equal the output cap ([`internal/bbsql/allowlist_test.go:290–296`](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/bbsql/allowlist_test.go#L290)). Their passing result does not establish C6 observability.

**Remediation:**

Separate source-fetch cap from output cap and coordinate fixed TOP n+1 statements with wave 7, retaining at most n output/baseline rows. Alternatively, mark exactly-at-cap results conservatively as “possibly truncated” and disclose selection explicitly. Keep identities and tie ordering deterministic, especially for pools/replicas. Test the actual SQL-shaped n+1 → n handoff plus 0, n−1 and n cases; retain coverage metadata even when all selected rows produce no deltas. Do not remove SQL caps or scan a plan cache to obtain current top waits.

### W11-07 — Registry snapshots share mutable sampler state

**The Flaw:**

The sampler `init()` functions register pointers to mutable objects. `Samplers()` copies only a slice of interface references; `NewRunner` receives the same objects, not fresh sampler instances. A mutex around the registry does not isolate baseline maps. Evidence: [`internal/bbsql/waits.go:101–103`](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/bbsql/waits.go#L101), [`internal/bbsql/filestats.go:139–142`](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/bbsql/filestats.go#L139), [`internal/bbsql/schedulers.go:209–213`](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/bbsql/schedulers.go#L209), [`internal/bbsql/runner.go:89–95,174–181`](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/bbsql/runner.go#L89).

**Exploit / Failure Scenario:**

Preconditions: two users of registry objects in one process, sequential or concurrent. The current production caller constructs one runner and immediately runs it ([`cmd/unravis-blackbox/sqlrunner.go:70–74`](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/cmd/unravis-blackbox/sqlrunner.go#L70)); no normal multi-runner production path was found. This limits severity to Low rather than claiming deployed cross-instance leakage.

1. One runner/test decodes a baseline into the global semaphore object.
2. Another `Samplers()` or `NewRunner()` obtains that same object and may emit a delta on what its caller regards as its first sample. Sequential re-creation does not reset state; separate SQL-service processes would reset it.
3. This contamination is reproduced without edits: `TestSchedulersSemaphoresTempdbRegistered -count=2` passes once and fails at [`internal/bbsql/schedulers_test.go:702–706`](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/bbsql/schedulers_test.go#L702) on repetition two with an emitted zero-valued semaphore row instead of nil.
4. If independent runners are introduced concurrently, their per-runner tick serialization still does not protect shared sampler maps. Data races/cross-history deltas are possible, but no live concurrent-runner crash was demonstrated. The existing snapshot test replaces a slice element only, not object state ([`internal/bbsql/runner_registry_test.go:96–112`](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/bbsql/runner_registry_test.go#L96)).

**Remediation:**

Register immutable factories/descriptors and allocate fresh sampler state per runner. A mutex alone would serialize the wrong shared history. Test independent sequential and concurrent runners with distinct counters; prove both first samples are baseline-only and run repeated/shuffled tests and the race detector. Keep current one-runner deployment constraints explicit until isolation exists.

### W11-08 — The performance gate does not exercise the shipped sampler work

**The Flaw:**

`newBudgetRunner` replaces the registry with `budgetSampler` doubles. Their Decode calls `decodeRows`, not the real waits/semaphore/file delta implementations; the AG double has no Gate. The test therefore omits baseline-map rebuilding, zero/reset/null policies, actual singleton state and the HADR query. The anti-drift test checks IDs, cadence and caps only. Evidence: [`internal/bbsql/budget_test.go:55–74`](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/bbsql/budget_test.go#L55), [`internal/bbsql/budget_test.go:133–152`](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/bbsql/budget_test.go#L133), [`internal/bbsql/budget_test.go:198–212`](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/bbsql/budget_test.go#L198), [`internal/bbsql/sampler_registry_test.go:115–145`](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/bbsql/sampler_registry_test.go#L115).

**Exploit / Failure Scenario:**

Preconditions: acceptance relies on C13 fixture results as evidence for wave-11 production behavior.

1. A sampler regression in retained baseline state or decode work need not affect this budget test at all. In particular, the working-set test claims to cover retained per-file/per-wait baselines but constructs the same stateless doubles ([`internal/bbsql/budget_test.go:490–507`](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/bbsql/budget_test.go#L490)).
2. “At-cap” fixtures maximize row count, not encoded width: request strings are short, counters are small functions of row index, semaphore counters are zero ([`internal/bbsql/budget_test.go:104–128`](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/bbsql/budget_test.go#L104)). Larger legal numbers and 60-character wait names increase storage beyond those measured figures. There is no runtime per-line byte limit in [`internal/bbsql/runner.go:557–570`](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/bbsql/runner.go#L557).
3. The written C13 rule says under 2 KB per sample. The tests instead check a realistic *mean* and permit 8 KB realistic / 40 KB at-cap maximum lines ([`internal/bbsql/budget_test.go:27–43`](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/bbsql/budget_test.go#L27)). An earlier report's reference to an owner decision is not independent proof that the governing acceptance requirement changed.
4. SQL result caps do not bound source work. The requests self-join/aggregation, scheduler/session SUMs, wait ranking, and all-database file-stats source occur before or underneath a small result ([`internal/bbsql/allowlist.go:15–62`](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/bbsql/allowlist.go#L15)). **Unverified Assumption:** actual execution-plan scan cost, duration, CPU impact and cancellation latency on loaded supported servers. No measured budget violation or application DoS is asserted; the missing evidence is the release-gate defect.

**Remediation:**

Benchmark fresh production sampler instances through a fake Queryer with evolving counters, churn, null/reset/reconnect cases, HADR enabled/disabled and actual object/array payloads. Measure allocations and retained memory after warmup with realistic and at-cap data, including maximum supported numeric widths and wait-name lengths. Obtain an explicit decision on the per-sample byte requirement and test it literally, or revise the governing requirement with authorization. Separately obtain approved live query plans and per-query/whole-tick overhead under representative session/file/pool counts. TOP, a client deadline and low deadlock priority are not substitutes for source-cost evidence. Keep a group disabled or slower if it cannot meet the agreed budget.

## Cross-Wave Impact

### Statement validity and runtime schema — wave 7 owns statement causes

These observations are necessary preconditions for sampler acceptance, not duplicate wave-7 SQL-validity findings:

- **Requests:** the source references `r.wait_time_ms` twice ([`internal/bbsql/allowlist.go:17–20`](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/bbsql/allowlist.go#L17)). W7-02 independently checked the public schema: `sys.dm_exec_requests` exposes `wait_time`, not `wait_time_ms`. It also confirms the semaphore source must be `timeout_error_count`, not `timeout_count`. These are documented source-column defects, not unresolved decoder assumptions. Validate the corrected exact batches and aggregation behavior with system requests, negative blocker IDs and multiple requests per session. The Go `tasks` field does not independently establish whether the SQL count means requests, worker tasks or user work. No successful live query is claimed here.
- **Tempdb database context:** the owner grant fixes `DEFAULT_DATABASE=master` ([`cmd/unravis-blackbox/grant.go:8–12`](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/cmd/unravis-blackbox/grant.go#L8)); `connDSN` does not select another database ([`internal/bbsql/conn.go:169–185`](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/bbsql/conn.go#L169)); `stmtTempdb` reads unqualified `sys.dm_db_session_space_usage` ([`internal/bbsql/allowlist.go:54–56`](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/bbsql/allowlist.go#L54)). **Unverified Assumption:** that DMV's tempdb-only availability/context behavior on supported versions. Validate from the granted login's actual default context, not an SSMS tab already using tempdb. A fixed, reviewed database-qualified source may be needed. The sampler's NULL guard cannot repair a query that fails in master.
- **Tempdb measurement meaning:** only allocation columns are summed; no deallocation or active-task counters are read. `tempdbSampler` emits those as `internalPages/userPages` without measurement-kind metadata ([`internal/bbsql/schedulers.go:89–104`](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/bbsql/schedulers.go#L89)). **Unverified Assumption:** exact session-counter update timing and lifetime/deallocation semantics. Do not interpret these as present tempdb occupancy until matched to a validated DMV measurement; this review does not invent an in-use requirement where the packet requested page totals.
- **SQL SUM and widths:** scheduler SUMs have no cast before aggregation ([`internal/bbsql/allowlist.go:42–45`](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/bbsql/allowlist.go#L42)). **Unverified Assumption:** source/result types and practical magnitudes. If SQL aggregates int inputs as int, widening the Go uint32 destination does not prevent SQL-side overflow. Validate input and aggregate result types, not only aliases. No reachable scheduler-overflow event was demonstrated. Tempdb's empty-SUM/null guards are present but do not cover missing required columns (W11-04).
- **Semaphore/AG keys and nulls:** W11-01–04 require actual `pool_id/resource_semaphore_id`, bigint/nullability and role-dependent AG metadata. The existing column-extraction test synthesizes `int64(1)` for every numeric alias ([`internal/bbsql/output_test.go:237–247`](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/bbsql/output_test.go#L237)); that proves neither SQL column existence nor DMV type/range/nullability. C1/C2 lint remains necessary but is not a schema validator.

### Runner boundaries — wave 10 owns scheduling and failure policy

`Samplers()` sorts by Kind, so the shipped order is **ag, filestats, requests, schedulers, semaphores, tempdb, waits** ([`internal/bbsql/runner.go:89–95`](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/bbsql/runner.go#L89)). `Tick` returns at a query/decode failure ([`internal/bbsql/runner.go:297–305`](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/bbsql/runner.go#L297)). Consequently a failing requests statement can prevent the semaphore/tempdb/waits defects from being reached on that tick. It is not evidence that their decoders are correct; W11-01's whole-service failure scenario explicitly requires earlier kinds to succeed. Filestats/AG paths can run before requests.

W11-05/07 need joint sampler/runner lifecycle changes; this report does not duplicate wave-10 rights checks, service concurrency, rotation or global backoff-policy findings. C3–C5, C7–C8 and C10–C12 were followed only far enough to bound practical sampler impact. C6, C9 and C13 have the direct wave-11 integration defects above; C14 matters here because it establishes default database context. No fixes to those controls were made.

### Downstream scope and cadence

The current lane reads host `blackbox.Sample.SQLHost.Instances[].BatchRequestsPerSec`, not `bbsql.Semaphores` or AG documents ([`internal/server/tracing_blackbox.go:50–66`](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/server/tracing_blackbox.go#L50), [`internal/server/tracing_blackbox.go:114–132`](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/server/tracing_blackbox.go#L114)). No production decoder of `bbsql.Semaphores` was found in the inspected direct path. Therefore neither existing UI misclassification nor an int32 semaphore lane-parser compatibility restriction is established. The confirmed sink for these findings is `SampleDoc` in the SQL NDJSON files, with stream-wide status/failure effects where stated. Later import/UI work must not reconstruct identities, missingness or intervals already lost by the sampler.

The registry currently agrees with the canonical nominal cadences: requests/schedulers/semaphores 15 s; waits/tempdb/filestats/AG 60 s ([`internal/bbsql/sampler_registry_test.go:23–30`](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/bbsql/sampler_registry_test.go#L23)). This is not a remaining 15-versus-60 registration defect. Gauge treatment of grantees/waiters and size-on-disk must also remain distinct from cumulative-counter subtraction. Runtime backoff still changes elapsed observation time (W11-05), and nominal-cadence parity does not validate the real sampler performance workload (W11-08).

## Go / No-Go Verdict

**No-Go for wave-11 live acceptance at HEAD `33c219b`.** This is a review recommendation, not an instruction to operate or alter any service. Required measurable gates:

1. **Schema/query gate:** on each supported SQL major version, capture exact result metadata and numeric-only samples under the intended granted login and default database. All seven sampler statements plus HADR gate must execute; confirm tempdb context, pool keys, wide/nullable counters, AG local/remote behavior and SUM result types. Resolve wave-7 statement failures before claiming end-to-end sampler success.
2. **Numeric gate:** tests using real sampler instances accept cumulative semaphore values above MaxInt32 and emit correct small deltas with zero added runner failures. Exercise reset, null and maximum supported values; no wrapping or invented negatives/zeros.
3. **Identity/locality gate:** same-ID/different-pool permutations yield identical correctly keyed deltas; no duplicate full identities survive. AG primary/secondary/multi-replica fixtures and approved live comparisons establish exactly what each emitted identity/state represents.
4. **Missingness/lifecycle gate:** deletion or nulling of every required column produces missing/fixed-code invalid data, never fabricated RUNNING/zero. Gap/reconnect/demotion/reset/reuse tests either rebaseline or emit validated interval/epoch metadata. No unproven cross-epoch deltas.
5. **Coverage gate:** source populations n−1, n and n+1 have explicit, tested completeness semantics at the real SQL/reader/decoder boundary. At most n rows/baselines survive; excluded files/waits/pools/replicas cannot masquerade as complete coverage.
6. **Isolation gate:** the reproduced `-count=2` failure is eliminated; two independent runners retain independent histories. Repeated/shuffled and race tests pass without relying on test-order resets of global sampler state.
7. **Performance gate:** benchmark the actual seven samplers through the fake Queryer, including HADR and evolving baseline state, at nominal and changed cadences. Demonstrate C13's <1% of one core and <10 MB extra memory, plus the explicitly approved per-sample byte limit with supported value widths. Separately demonstrate acceptable source-query overhead and C4 deadlines on an approved loaded server; mark any unmet live condition **Unverified Assumption**, not passed.

Only this review report was authored. Code, tests, configuration, ACLs, services, secrets and Git index/commit state were not changed by this review; no external data was sent.
