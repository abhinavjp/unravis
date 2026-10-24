# Theory engine ("theory checks"): design

Status: design proposal, not built. Date: 2026-10-24. Inputs: the private theory list (`sql/theories.md`, read only and generalised here), roadmap rev 9 (`docs/superpowers/plans/2026-10-24-unravis-post-mvp-roadmap.md`), the decision rulings (R15), the T0.6 code reads, `internal/audit/patterns_*`, `internal/ledger`, `internal/evidence`.

Privacy: this document copies no client value from the theory list. It contains no host, address, port, date, user, count, package name, package version or filter class name. Filters are called the **permission filter**, the **audit filter** and the **token filter**. The remote service is **the Node permission-cache proxy**. Every number below is either a vendor default with a citation or a design threshold this document chooses. Before any use, mapping the `FilterName` strings to these three roles is an analyst setting (§10.2); it is never hard-coded.

---

## 0. Answer in one paragraph

**Can the engine test theories?** Yes. A fixed, predeclared registry of deterministic checks can test most of the 32 listed theories and 14 new ones against data. Each check reports one of four results: consistent with the theory, contradicted by it, inconclusive, or not testable here (naming the missing source and the phase that adds it).

**What the audit log can do alone.** About a third of the checks can run today on the audit log, and most of what it can do alone is *contradict* theories. The token-filter control, progress inside the stall core and peak in-flight load already cut several theories that the theory list calls open.

**Where the value is.** The highest-value checks are cross-source. Audit duration versus IIS `time-taken`, stall cores versus SQL waits and ERRORLOG, a canary decision table, request-queue counters, schedule boundaries, recycle events, slow mongod operations, App Insights dependency time and hang-dump stack summaries.

**What blocks them.** Five of the importers they need (P4, P5, P6, P7 and P9) are held pending third-party approval. The roadmap has no phase for four signals the theories need: canary probes, the Node proxy log, hang-dump summaries and change history. §6 proposes amendments and new tasks P15 to P19.

**The governance conflict.** The roadmap cut a verdict engine (P2b, decision 15, C7). This design is built to stay within the rules that cut was meant to protect. It never sets a ledger state, emits nothing above Correlation, and uses no p-values over user-picked sets. It still needs a new ruling before it is built (§1).

---

## 1. Position against the roadmap

| Roadmap constraint | How this design complies | Residual conflict |
|---|---|---|
| P2b engine cut. Decision 15: "the product never sets supported". No verdict engine. | The engine outputs a *check result*, which is not a ledger state. Ledger `Create` always starts `open`. Copy-to-ledger passes text and evidence IDs only, never `Facts` (the ledger contract says the product derives none of them). | A check result labelled "Consistent with theory" can still be read as a verdict. Needs a ruling. |
| C7 / §8.6 / §10.2: counts only, predeclared windows, no Holm or Benjamini–Hochberg, no automatic p-values over user-selected theory sets, no permutations without calibration. | The registry is fixed and versioned, so the family of checks is fixed. The primary criterion is an effect-size floor plus k-of-n independent episodes. No p-value is computed for display. Synthetic null worlds measure the false-support rate per check and across the whole registry. That calibration is the permutation step, done offline in tests (§12). | None, if the ruling accepts calibration done in tests. |
| §8.12 P8: thin timeline, no composite rules. | Cross-source checks are composite by definition. | Needs a ruling. |
| §6.6 wordings and copy lint. | All result copy is built from the §6.6 templates. The banned words ("caused", "because", "waiting on", "blocked by", "due to") never appear next to Symptom or Correlation. | None. |
| §5.4 stop rules 1 and 2: no slice without preserved evidence and an incident question. | Each rule declares its source, phase and incident question. A rule whose phase is cut or held shows NOT_TESTABLE_HERE and is never built ahead of its phase. | None. |
| ID collision: roadmap rules B1 to B13 (stall, episode and similar) against theory IDs B1 to B19. | Theory rules use the prefix `th:` in IDs. The UI always says "Theory B1" and the pattern engine says "Rule B1". Pattern evidence stays `p2:…`. | None. |

**Proposed ruling (next free R-number, call it R-TE).** Allow a fixed-registry theory-check engine. It may emit per-check results with labels up to Correlation and a "strong-eligible" flag. It may not write ledger state or show p-values. Composite cross-source checks are allowed only under the §6.2 join guards. Thresholds are frozen per engine version and changed only by a registry version bump with a recorded reason. If R-TE is refused, steps 1 and 2 of §11 still have value as a "data that bears on each theory" list with no result column. That is the fallback.

---

## 2. Measurement caveats every check inherits

| ID | Caveat | Consequence for checks |
|---|---|---|
| M1 | **Survivorship.** A failed synchronous insert leaves no document, because exceptions are swallowed. A request that never reaches the completion hook has no `RequestCompletedAt`. | Absence of rows is never evidence. Incomplete rows are censored, not dropped. In-flight counts from incomplete rows are valid only in live windows. |
| M2 | **Floor latency.** Time before the audit filter (HTTP.sys, IIS, the ASP.NET queue, earlier handlers) and after completion (payload serialisation, response send) is not measured. | Audit-only checks can never support B9 or B10. Audited load is a lower bound on load. |
| M3 | **Wall clock.** Timestamps come from `DateTime.UtcNow`, which can step. | A clock step can create or erase a stall (N1). Any negative duration, or `CreatedOn < RequestInitiatedAt`, sets a "clock anomaly" flag on the episode. That episode is then excluded from duration-based checks. |
| M4 | **`PayloadRecordTime` is not backlog.** `UpdatedOn` is set when the update is *queued* (T0.6). | The B4 prediction as written is not a test. B4 is rewritten (§7, B4r). |
| M5 | **No host field.** | Several web instances behind a balancer cannot be told apart (N9). Joins to per-host sources assume a single host unless P0 says otherwise. |
| M6 | **ObjectId.** Seconds resolution, generated by the driver, not monotonic. | `_id` bounds are a hint only (§6.4). Join tolerance is at least 1 s. |
| M7 | **`AccountId` sparse.** | Company-level checks run only if `AccountId` is at least 80% populated in the window (coverage probe, §6.3). Otherwise they report NOT_TESTABLE_HERE ("AccountId coverage below 80%"). |
| M8 | **Mixed rows.** Hangfire rows share the collection, and `RequestPollingType` carries trailing spaces. | Trim before comparing. Sync, async and Hangfire are always separate strata. |
| M9 | **Filter timings are partial.** `PreActionFilterTimings` exists only since instrumentation began, and that instrumentation deploy is itself a change. | Checks that use them need field presence of at least 95% of sync rows in each episode and its controls. Otherwise that episode is excluded. |
| M10 | **`AuditCreateTime` is not pure insert time.** It also covers config, user lookup, DI and MongoClient construction (T0.6). | The copy says "audit create took …" (§6.6) and never says "insert took". |
| M11 | **MongoClient per commit.** | The C# driver is documented to keep pools per client. Clients built with identical settings are believed to share a cluster through the driver's cluster registry [verify against driver source]. If they do not, every commit is a cold pool, which should make most inserts slow. The data shows otherwise, so record the finding as a T0.6 open item, not as a theory. |
| M12 | **Own-tool load.** Unravis's own broad scans hit the same Mongo. | Rule N12 checks the tool's own provenance log against stall cores before any Mongo-side theory is reported as consistent. |

---

## 3. Result model

### 3.1 Result states

Internal enum, then display copy:

| Enum | Display | Meaning |
|---|---|---|
| `SUPPORTED_BY_DATA` | "Consistent with theory" | The predicted signature is present at or above the effect floor in at least k of n independent episodes, and no contradiction test fired. |
| `CONTRADICTED_BY_DATA` | "Data contradicts theory" | A contradiction test fired in at least 80% of n ≥ 3 episodes with source quality complete. For threshold sources, it fires only where the predicted magnitude is at or above the recording threshold. |
| `INCONCLUSIVE` | "Not enough data" (reason given) | n < 3, below the effect floor, ambiguous joins above 10%, budget or row cap hit, or a tie group. |
| `NOT_TESTABLE_HERE` | "Not testable here: needs \<source\> (\<phase\>[, held])" | The source or field is absent. The message always names the source and the phase that adds it. |

`INCONCLUSIVE` is a fourth state beyond the three in the brief. Without it, low n would be forced into a false "supported" or "contradicted".

### 3.2 Labels

R15 labels are Symptom, Correlation and Strong correlation.
- A single-source audit check is capped at **Symptom**.
- A cross-source check with a unique or interval join and a validated clock pair is capped at **Correlation**.
- The engine never emits Strong correlation. It sets `strongEligible=true` when the §8.6 guard inputs are met:
  - 3 of 3, or at least 80% of 5 or more episodes
  - counterexamples listed
  - alternatives named, taken from the rule's tie group
  - not overlap-only
  - timezone recorded
  - ambiguity ≤ 10%
  - confounders checked

  Promotion happens only in the ledger, by the analyst.

### 3.3 How a result degrades

Each condition is checked in this order:

1. **Source absent.** NOT_TESTABLE_HERE, naming the source and phase, e.g. "needs IIS W3C with time-taken (P7, held)".
2. **Timezone rule missing** for any source or field. NOT_TESTABLE_HERE ("timezone rule missing for \<source\>; T0.5 host map"). Import already refuses unlabelled SQL times.
3. **Clock-sync source unknown** for a host pair (T0.5). The check runs with its label capped at Symptom. The copy says "Possible overlap (clock not validated)".
4. **Coverage gap** over an episode's core. That episode becomes "Not observed (gap or sampling: \<detail\>)" and leaves n. If the remaining n is below 3, the result is INCONCLUSIVE.
5. **Threshold source** (system_health locks over 30 s and latches over 15 s; mongod `slowms`; ERRORLOG 833 at 15 s). Absence can contradict only the predicted events at or above the threshold. Otherwise the copy is "Not observed in \<source\>. This source only records \<threshold\>; shorter waits would not appear."
6. **Sampled source** (App Insights). A missing sample is never counter-evidence, and contradiction is disabled.
7. **Ambiguous join share above 10%.** INCONCLUSIVE ("ambiguous: k candidates"). Ambiguous matches are never counted.
8. **Budget or row cap hit** (§10.3). INCONCLUSIVE ("budget"), with docsExamined recorded. A partial read never produces a result.

### 3.4 Evidence IDs

The format is `th:<theory>:<check>[:e<n>]:<16 hex>`, all lowercase, and it matches `^[a-z0-9][a-z0-9:_.\-]{0,119}$`. The hex part is the first 8 bytes of SHA-256 over this canonical string:

`th|<theory>|<check>|<windowStart RFC3339>|<windowEnd>|<sorted source labels joined by ,>|<canonical JSON of params>|<engineVersion>`

- `:e<n>` marks a per-episode record (the 1-based episode index of the report).
- Examples: `th:a7:inflight-peak:3fa91c2e5b7d4410` and `th:b19:gap-lift:e2:9c01d4aa71be3f02`.
- The maximum length is about 70 characters.
- The same inputs always give the same ID. A new engine version always gives a new ID.

### 3.5 Masking and wording

- Every count of requests or users below 20 shows as "<20", in the UI and in exports (`evidence.ThinCell`).
- Episode counts are shown exactly in the app, because an episode is a time interval, not a person. `evidence.Build` still masks them in exports under its blanket rule, so k-of-n prints as "<20 of <20" and the result category carries the meaning.
- Per-user rows (B12, N4, N5) show keyed pseudonyms only. They are withheld when any cell is below 20.
- `suppressComplements` applies to all theory records, since a subset count and a total count side by side are complements.
- Headlines are rebuilt from structured fields only, never from free text.

### 3.6 Statistics policy

- **Unit.** The independent episode, as defined by the patterns engine: stall rows at 45 s or more, merged by a 600 s gap (rules B1 and B2, R9).
- **Primary criterion.** An effect floor (a lift, share or magnitude, each declared per check) **and** k-of-n with k ≥ 3, plus ≥ 80% for contradiction.
- **No p-values in UI or export.** Exact binomial and Fisher tests exist only in the calibration tests (§12), where they justify the floors.
- **Fixed thresholds.** Thresholds come from vendor constants or are fixed in this document, never tuned to the case being looked at.

---

## 4. Shared definitions and extractors

All extractors are computed once per report, keyed by `(report hash, engineVersion)`, and are read-only.

| ID | Extractor | Definition | Source |
|---|---|---|---|
| X0 | Strata | Sync / async / Hangfire, after trimming `RequestPollingType`. | audit |
| X1 | Episodes | From `PatternReport.Episodes`.<br>• **onset** = the minimum `RequestInitiatedAt` of the episode's stall rows<br>• **span** = [onset, the maximum `RequestCompletedAt`]<br>• **core** = the intersection of all of the episode's stall-row intervals; if that is empty, the longest interval covered by at least half of them<br>• **release** = the 1 s bucket with the most completions within span ± 5 s | patterns |
| X2 | Neighbourhood | [onset − 30 min, span end + 30 min]. At most 20 episodes, the most recent first. | IntervalReader |
| X3 | Controls | 3 windows per episode, the same length as X2, at the same weekday and hour on the nearest days with no stall row within ± 30 min. If fewer than 3 exist, the same hour on any weekday. If still fewer than 3, checks that need controls are INCONCLUSIVE ("controls"). | IntervalReader |
| X4 | In-flight sweep | I(t) per stratum over X2 and X3. Incomplete rows are censored at the window end, live windows only (M1). | X2, X3 |
| X5 | Rates | Start rate s(t) and completion rate c(t), in 1 s and 10 s bins. | X2, X3 |
| X6 | Operation baselines | For each operation (Controller.Action): median and p95 duration, AuditCreateTime p95, and filter-timing p95 per role, all over X3. | X3 |
| X7 | Idle gaps | For each row: the gap since the previous audited row of any stratum, and since the previous row of the same operation. | X2, X3 |
| X8 | Filter shares | For each row with timings: elapsed time and share per role (permission, audit, token). Gated by presence (M9). | X2, X3 |
| X9 | Constant clusters | Histogram of stall durations, AuditCreateTime ≥ 1 s values and filter elapsed ≥ 1 s values, in 0.5 s bins. Peaks within ± 1.5 s of the owner-mapped constants in §4.1. | X1, X8 |
| X10 | Day series | Daily and hourly p95 and volume per top-10 operation, from the existing time-bucket aggregation (≤ `MaxTimeBuckets` 120). Multi-day ranges need P1b or a restored copy. | summary |
| X11 | Deploy markers | First-seen times of new fields, new operations and new `PayrollUIVersion` values. | X10 + distinct |
| X12 | Duplicates | Same user, same operation and same `RequestLengthInKiloBytes`, with the next start ≤ 30 s after the previous one while it is still in flight. Optionally an in-memory hash of the truncated payload prefix, never stored. | X2, X3 |
| X13 | Hangfire activity | Hangfire rows per minute. Cessation points are a run of 60 min or more with zero rows after a rate at or above its own median. | X10 |
| X14 | Own-tool log | Unravis provenance entries (query, window, start and end, docsExamined, appName) overlapping each core. | local |
| X15 | Source quality | For each imported source: version, threshold, coverage first and last, gaps, sampling, timezone rule and clock-sync source. These go into `evidence.SourceQuality`. | importers |

### 4.1 Known constants (for X9)

A peak at a constant is a *pointer to the component that owns it*. It is never a result by itself.

| Constant | Owner | Points to |
|---|---|---|
| 15 s | `SqlConnection.ConnectionTimeout`, which is also the pool wait limit | B2, N15 |
| 15 s | SQL I/O warning 833 threshold (server side) | N13 |
| 30 s | `SqlCommand.CommandTimeout` | A1, B2 |
| 30 s | Mongo `serverSelectionTimeoutMS`, `connectTimeoutMS` | B19, N2, N3, B3 |
| ≈21 s, 42 s [verify by capture] | Windows TCP SYN retransmission give-up | B8, N2 |
| 100 s | `HttpWebRequest.Timeout` (sync) | A6, B8, B7, B17 |
| 110 s | ASP.NET `executionTimeout` (only when debug is false) | A7, A5 |
| 120 s | Mongo C# `WaitQueueTimeout` | B19, N3, B3 |
| 300 s | `HttpWebRequest.ReadWriteTimeout` | A6 |
| ≈90 s after hang | WAS ping 30 s plus pingResponseTime 90 s, ending in event 5010 | B14, A7 |

---

## 5. Phase-attached classification

**Phase status.**
- **Held, pending third-party approval:** P4, P5, P6, P7 and P9.
- **Deferred:** P10, P11 and P12.
- **New, proposed here (§6.3):** P15 canary, P16 proxy log, P17 hang-dump summary, P18 change history, P19 client HAR.
- **Not held:** P1a, P1b (gated), P2-lite, P3 (needs T0.3, T0.11 and the golden fixture), P13 and P14.

**Join columns.** Class (unique, interval or bucket), tolerance, timezone rule. Clock skew comes from T0.5 for every pair across hosts. Audit and IIS rows written on the same host share a clock, so their skew is 0 given M5.

### 5.1 Theories from the private list

| Theory | Generic statement | Testable now (audit only) | First phase that can make it Consistent | Held | Join, tolerance, TZ | Max label |
|---|---|---|---|---|---|---|
| A1 | SQL saturation or blocking | No (NOT_TESTABLE_HERE) | P3 (system_health, ERRORLOG, PDH) + AM2 (Query Store and wait deltas) | No | interval / bucket. system_health ≈5 s sampling + skew. XE `timestamp_utc` is UTC; ring buffer and `last_execution_time` are server-local per the host map. | Correlation |
| A2 | Volume overload | Audited load: Consistent or Contradicted (Symptom) | P7 (unaudited load from IIS rows per minute) | Yes | 1 min bucket. W3C is UTC; IIS-format logs are local. | Correlation |
| A3 | Host or VM freeze | Contradiction only | P15 (on-host and off-host probes); P6 (Kernel-General 1, hypervisor) | P6 yes; P15 is new | point-in-interval, probe period + skew. Probes in UTC with Z. | Correlation |
| A4 | Step change | Symptom, needs multi-day (P1b or restored copy) | P18 change history | New | day or hour boundary, ± 1 day (± 1 h if the history has times) | Correlation |
| A5 | Shared component in request path | Co-stall + filter share + start/complete shape (Symptom) | P9 attribution; P17 dump | P9 yes; P17 new | per request (operation id); dump point-in-core | Correlation |
| A6 | Node permission-cache proxy stalls | Tie group via permission-filter share (Symptom) | P9; P15; P16 | P9 yes; new | per request / per probe | Correlation |
| A7 | Thread-pool starvation | Contradiction for audited load; start-dip shape | P6 queue counters; P7 residual; P17 | Yes; new | sample-in-interval, 15 s + skew. PDH CSV is local with the bias in its header [verify]. | Correlation |
| A8 | Synchronous audit create slow | Audit-create share (Symptom) | P4 (server side, M1 join); P15 insert canary | P4 yes | M1: slow op ts within [RequestInitiatedAt, CreatedOn] ± 1 s. mongod iso8601-local with offset. | Correlation |
| A9 | Recurring background job stopped | Cessation of Hangfire rows (Symptom) | P5 + AM5 (heartbeat and state history) | Yes | point, ± 1 min | Correlation |
| A10 | Cold start after recycle | Warm-up signature + orphan rows (Symptom) | P6 events; P7 WAS | Yes | event → onset within [0, 5 min], 2 s. Events are UTC. | Correlation |
| A11 | Same chronic problem on a given day | Day similarity (Symptom; descriptive) | (audit only, by nature) | — | none | Symptom |
| A12 | Host CPU step weeks earlier | No | P3 ring buffer ("overall host CPU; SQL attribution unknown", Symptom); P6 per-process (W7) | P6 yes | day change-point; sample-in-core 15 s | Correlation |
| A13 | Stalls recur at clock minutes | Onset-minute recurrence (Symptom) | Attribution passes to B13 | — | minute-of-hour ± 2 min | Symptom |
| B1 | Deploy, package or config change | In-audit deploy markers X11 (Symptom) | P18 | New | ± 1 day / ± 1 h | Correlation |
| B2 | SQL client pool exhaustion or login storm | 15 s / 30 s constant clusters (Symptom) | P6 pool counters + P3 (User Connections, Logins/sec) | P6 yes | sample-in-core | Correlation |
| B3 | Mongo server-side stall | Simultaneous audit-create stalls across users (Symptom) | P4 | Yes | M1, 1 s | Correlation |
| B4 | Background payload queue backlog | B4r live-only (Symptom); the historical prediction is invalid (M4) | T3 G (queue length) | — | none | Symptom |
| B5 | Permission-cache clear stampede | Tagged permission-edit operation before onset (Symptom) | P16 | New | point-before-onset, [−120 s, 0] | Correlation |
| B6 | Redis server latency | No | P10 | Deferred | — | — |
| B7 | Proxy event loop blocked | First-stalled-request company or user recurrence (Symptom) | P16 | New | per request | Correlation |
| B8 | Outbound connection limits or port exhaustion | Constant clusters + permission share (Symptom) | P6 (TCPIP 4227/4231, TCPv4 counters) + AM3 netstat | Yes | sample / event-in-core | Correlation |
| B9 | Queueing before the audit filter | No (outside the window, M2) | P7 residual + P6 queue counters | Yes | unique request match (see XS1) | Correlation |
| B10 | Payload or response work outside the window | Inside-window size check only (Symptom) | P7 + AM4 (`sc-bytes`) | Yes | unique request match | Correlation |
| B11 | Client retries amplify stalls | Duplicate lift (Symptom) | P19 HAR | New | per request | Correlation |
| B12 | Specific users or companies | Concentration (Symptom) | — | — | none | Symptom |
| B13 | Scheduled work at fixed minutes | A13 recurrence (Symptom) | P3 backups (ERRORLOG 18264); P5; P6 (Task Scheduler, Defender) | P3 no; P5/P6 yes | schedule boundary ± (60 s + skew + onset uncertainty). Uses cron `TimeZoneId`; msdb is server-local. | Correlation |
| B14 | In-process lock or spin | Tie only | P17 dump; P6 (w3wp CPU one core; lock contention counters) | P6 yes; new | dump point-in-core | Correlation |
| B15 | GC or paging pause | Token-filter control (contradiction) | P6; P3 (ERRORLOG 17890) | P6 yes | sample-in-core | Correlation |
| B16 | Noisy neighbour on host or VM | Token-filter control (contradiction) | P6 per-process + AM3 hypervisor counters | Yes | sample-in-core | Correlation |
| B17 | DNS, WPAD or CRL delays on new connections | Post-idle permission-filter slowness (Symptom) | P6 (Schannel, CAPI2 with operational log enabled) | Yes | event-in-core | Correlation; settled only by capture |
| B18 | Hangfire shares the API process | Hangfire progress during cores (Symptom) | P5 + AM5 (server table gives host and PID) | Yes | point | Correlation |
| B19 | Idle Mongo connections dropped | Gap-conditioned audit-create lift (Symptom) | P4 connection lines; P15 keep-alive canary (falsification) | P4 yes; new | M1, 1 s | Correlation |

### 5.2 Mapping theory-list sections C, D and E

**Section C (the discriminator table)** becomes deterministic decision tables:
- the canary rows are XS3
- the IIS row is XS1
- the hang-dump rows are XS9
- SLOWLOG/LATENCY is in P10, deferred, so its row is NOT_TESTABLE_HERE
- cache-clear is XS10 (P16)
- the quiet-gap row is B19 + P15
- the duplicate row is B11
- the scheduled-minute row is XS5

**Section D (data to collect).** Each item, with the phase that ingests it:

| D item | Phase | Status |
|---|---|---|
| D1 in-code timers | T3 G (one instrumentation change) | AM8 |
| D2 canary | P15 | new |
| D3 IIS, HTTPERR | P7 | held |
| D3 FREB | — | gap (AM4) |
| D4 counters | T0.2 → P6 | held |
| D5 dump | P17 | new |
| D6 proxy | P16 | new |
| D7 Redis | P10 | deferred |
| D8 mongod.log | P4 | held |
| D8 serverStatus, FTDC | — | deferred (access A3) |
| D9 SQL | P3 + AM2 | — |
| D10 events | P6 / P7 | held |
| D11 app logs | P12 | deferred |
| D11 Hangfire tables | P5 + AM5 | held |
| D12 change history | P18 | new; interim: P0 manual hint |
| D13 HAR | P19 | new |
| D14 runtime | T0.6 parameter | — |

**Section E (checks run by hand)** becomes registry rules:

| Check run by hand | Rule |
|---|---|
| In-flight | A7-a / A2-a |
| Audit create and payload time | A8-a, B19-a, B4r |
| Payload size | B10-a |
| Retries | B11-a |
| User concentration | B12-a |
| SQL schedule | B13 via P3 ERRORLOG backups and P5 |
| Sessions | B2 via P3 PDH |
| Heartbeats | A9 / B18 via P5 + AM5 |

---

## 6. Requirements back to phases, and proposed amendments

### 6.1 What each phase must expose

| Phase | Theories it unlocks | Fields and signals required for the predicates | Gap: proposed amendment |
|---|---|---|---|
| P1a (now) | All audit-only checks | `RequestInitiatedAt` index (A5 access), coverage probe, `AccountId` %, field presence % for timings, row cap, maxTimeMS, docsExamined | none |
| P1b (gated) | A4, A11, A12 trend side, B1 markers over 30–90 days | Daily per-operation p95 and volume | none |
| P3 (DBA export) | A1, A12 (Symptom), B2 (SQL side), B13 (backups), N10, N11, N13, B15 (17890) | **system_health:** `wait_info`, `xml_deadlock_report`, `scheduler_monitor_system_health_ring_buffer_recorded`.<br>**ERRORLOG messages:** 833, 3197/3198, 5144/5145, 17883/17884/17888, 17890, 18264, 18456, 701.<br>**PDH:** Batch Requests/sec, User Connections, Logins/sec, Processes blocked, Page life expectancy. | **AM2:**<br>(a) Query Store runtime and wait stats export, fixed fields: interval start/end, `query_hash`, executions, avg/max duration, wait category totals. Note the interval is hourly by default, so minute resolution is impossible.<br>(b) A DBA-run per-minute `sys.dm_os_wait_stats` delta CSV (B1 access).<br>(c) Default-trace autogrow events (EventClass 92/93) through `fn_trace_gettable` to CSV.<br>(d) The `blocked_process_report` custom XE session is a T3 G request, since system_health does not hold it. |
| P6 (held) | A7, A10, A12, B2, B8, B14, B15, B16, B17, B13 (host side), N1, N6, N2 | **Counters:**<br>• ASP.NET Requests Queued<br>• ASP.NET Apps Requests In Application Queue, Requests Executing<br>• HTTP Service Request Queues CurrentQueueSize<br>• Process V2 % CPU / cores and Thread Count (w3wp, node, mongod, sqlservr)<br>• .NET CLR Memory % Time in GC and # Gen 2 Collections<br>• .NET CLR LocksAndThreads Contention Rate/sec and Current Queue Length (lock waiters)<br>• .NET Data Provider for SqlServer pool counters<br>• Memory Available MBytes, Pages/sec<br>• TCPv4 Connections Established, Segments Retransmitted/sec<br>**Events:**<br>• WAS 5002/5010/5011/5013/5074–5080/5117/5186<br>• ASP.NET 1309<br>• .NET Runtime 1026<br>• Application Error 1000<br>• TCPIP 4227/4231<br>• Schannel 36887/36874<br>• Kernel-General 1 (system time changed)<br>• W32Time<br>• Kernel-Power 41/6008<br>• Task Scheduler 100/102/200/201<br>• Defender 1000/1001<br>Event IDs other than WAS, TCPIP and Kernel-General are [verify]. | **AM3:**<br>(a) **Correction to the theory list:** `.NET CLR LocksAndThreads: Current Queue Length` counts *threads waiting on a managed lock* (B14). It is not thread-pool queueing. A7 needs the ASP.NET queue counters and the w3wp Thread Count slope.<br>(b) The detailed SqlClient pool counters (free, active, pooled) need the `ConnectionPoolPerformanceCounterDetail` switch. That is a config change that needs approval.<br>(c) % Time in GC updates only at the end of a GC, so a long pause shows as a frozen value. Use the Gen 2 delta across the core.<br>(d) Task Scheduler Operational and CAPI2 Operational are off by default and must be enabled *before* the incident (stop rule 1).<br>(e) Add a per-minute `Get-NetTCPConnection` state count by remote port, from an Ops-run CSV, for B8. No phase holds netstat.<br>(f) In-guest hypervisor counters (for example VM stolen time) where the guest tools provide them [verify].<br>(g) The export must write UTC (`TimeCreated.ToUniversalTime()` or ISO with offset). Unlabelled times are refused. |
| P7 (held) | B9, B10, A2, A10 (WAS), N9, N14 | **W3C fields:** `date`, `time`, `cs-method`, `cs-uri-stem`, `c-ip`, `s-ip`, `sc-status`, `sc-substatus`, `sc-win32-status`, `time-taken`, `sc-bytes`, `cs-bytes`.<br>**HTTPERR:** `s-reason` (Timer_*, QueueFull, Connection_Abandoned_By_ReqQueue). | **AM4:**<br>(a) `sc-bytes` and `cs-bytes` are not in the default W3C field set. Enabling them affects future logs only.<br>(b) The W3C `time` field has 1 s resolution. Roadmap §6.2 treats it as request start; several sources treat it as the log-write (completion) time [verify on the golden fixture by testing both anchors].<br>(c) Behind a balancer, add `X-Forwarded-For` as a custom field so `c-ip` can match `UserIp`.<br>(d) FREB has no phase. Its traces carry headers and URLs, so leave it out unless a module-timing summary is specified.<br>(e) For small responses `time-taken` may exclude network send (§9.3 open item), so B10 slow-client detection is blind there. |
| P4 (held) | A8 (server side), B3, B19, N3, N7, N12 | **Slow-query lines (51803):** `attr.durationMillis`, `workingMillis` (8.0+), `ns`, `command.insert`, `flowControl.timeAcquiringMicros`, `storage.data.*Micros`, `waitForWriteConcernDurationMillis`, `appName`, `remote`.<br>**Connection lines:** 22943 accepted, 22944 ended.<br>**REPL** state transitions; checkpoint or WT slow messages [verify IDs].<br>**Source-quality record:** version, slowms, coverage, rotation gaps, offset. | **AM6:**<br>• Parse `appName` and `remote` (N12, B19).<br>• Record whether `remote` is the API host, pseudonymised.<br>• The driver settings (maxConnecting, maxIdleTime, timeouts) must come from T0.6, not guesses.<br>• Through 7.0 the slow threshold uses total time; from 8.0 it uses working time. On 8.0+ a ticket-queue stall can be missing from the log (§9.3), and the absence wording must say so. |
| P5 (held) | B13, A9, B18, A13 attribution | **Hangfire:** id, Cron, Queue, NextExecution, LastExecution, TimeZoneId.<br>**msdb:** job name, schedule, next run, last duration. | **AM5:**<br>(a) Schedules give *planned* times only, and the run time is "unknown". Add execution history with fixed fields: Hangfire state history (job id, state name, created at; no Job or arguments), msdb `sysjobhistory` (run date/time, duration, status) and `backupset` (start, finish).<br>(b) The Hangfire server heartbeat table (server id, last heartbeat, worker count). The server id carries host and PID, which settles B18 "same process" once pseudonymised. Both widen access rows B3/B4 and need approval. |
| P9 (held) | A6, A5, B8, B2, A1 (client view) | **Requests:** `sum(itemCount)`, duration, operation id.<br>**Dependencies:** type, pseudonymised target, duration, success, operation id. | **AM7:**<br>• App Insights does not auto-collect Mongo driver calls, so A8 is not testable through P9 without a custom dependency.<br>• Adaptive sampling may drop exactly the slow requests; check `itemCount` weighting.<br>• Whether the SQL dependency time includes pool wait and connection open is [verify]. |
| P10 (deferred) | B6 | SLOWLOG, LATENCY, INFO persistence | Stays deferred. B6 and the SLOWLOG row of section C stay NOT_TESTABLE_HERE ("needs Redis SLOWLOG/LATENCY (P10, deferred)"). |
| P12 (deferred) | B2, A8 (swallowed exceptions) | Error lines with timestamps | Stays deferred. Noted as a source of counterexamples. |
| T3 G (one change) | A5, A6, A7, A8, B4 | — | **AM8:** recommend one change that adds three fields per audited row: per-outbound-call elapsed time (proxy call, audit insert), `ThreadPool.GetAvailableThreads`, and host name (M5). It ends more ties than any other single change (§8, XS3 and XS9 tie groups). |

### 6.2 Held-phase blocking

| Held phase | Rules blocked that are not testable elsewhere | Rules that only lose an upgrade |
|---|---|---|
| P6 | B15, B16, A12 per-process, B17, B8 events, N1 confirmation, N6 confirmation | A7 (keeps the audit contradiction), A10 (keeps the audit signature) |
| P7 | B9, B10 outside window, A2 unaudited load, N9, N14 | A10 WAS (P6 also carries WAS) |
| P4 | B3 and A8 server side, N7, N12 confirmation | B19, N3 (keep the audit signature and P15) |
| P5 | B13 job attribution, A9, B18 | A13 (descriptive survives) |
| P9 | A6 attribution, A5 attribution | A6/A5 via P15, P16, P17 |

### 6.3 New tasks (no existing phase fits)

All are Ops-run file imports under the §6.7 import contract: case folder, SHA-256 idempotency, quarantine, formula escaping, 30-day retention, no row contents in logs. None needs a third-party approval beyond the client's own Ops. Each needs a §5.3 slice card before it is built.

- **P15 Canary probe CSV.**
  - Schema: `ts_utc` (ISO, Z), `vantage` (on-host or off-host), `probe` (enum: `node-health`, `proxy-cache-get`, `mongo-ping`, `mongo-insert-canary-coll`, `api-noauth`, `api-auth-light`, `tcp-connect-api`), `ms`, `outcome` (ok, timeout or error class).
  - Period 5–10 s.
  - The insert writes to a separate canary collection, never the audit collection.
  - Settles A3, A6, A8, B3, B7 and B19 (keep-alive falsification).
  - Precondition: the clock-sync source in T0.5.
- **P16 Proxy log and event-loop delay.**
  - Fixed fields: `ts_utc`, route class (get, set, clear-pattern), ms, value bytes, cache hit, Redis reconnect events, event-loop delay p99 per 10 s, process CPU.
  - No keys and no values.
  - Settles A6, B5, B7 and N8.
- **P17 Hang-dump stack summary.**
  - The Ops runner produces a *summary only*: dump UTC time, runtime, and threads classified by top managed frame class: `http-client`, `socket-connect`, `dns`, `mongo-driver`, `sqlclient-pool-wait`, `sqlclient-exec`, `monitor-wait`, `gc`, `session-state`, `idle-pool`, `other`.
  - Counts per class, with request threads flagged.
  - No locals, heap or strings.
  - The dump itself never enters Unravis.
  - Tooling depends on the runtime (D14): WinDbg/SOS or a ClrMD summariser for .NET Framework, `dotnet-dump` for .NET.
- **P18 Change history.** Fixed fields: change UTC time, component class (api, ui, proxy, iis-config, db), kind (deploy, config, package), ref hash. Interim: P0 manual hint entries.
- **P19 Client HAR summary (optional, lowest priority).** Per request: start, blocked/queueing ms, wait ms, retry flag, URL path class. Settles B11 and N5.

---

## 7. Audit-only predicates (shippable with P1a and P2-lite)

Notation:
- **E** is the episode set and **n = |E|** after exclusions (M3, M9, coverage).
- **k-of-n** means the number of episodes where the support condition holds.
- **Floor** is the effect size required.
- Every check's evidence ID is `th:<id>:<check>[:e<n>]:<hex>`.

A check that is not listed for a theory means that theory has no audit-only check.

**A2-a Audited load at onset.**
- Statistic: L = median s(t) over [onset − 5 min, onset] ÷ median over the same span in controls. Also I(onset) against the control p95 of I.
- Consistent: L ≥ 2 and I(onset) > control p95 in k ≥ 3 episodes.
- Contradicted: L ≤ 1.2 in ≥ 80% of n ≥ 3.
- Label: Symptom. The copy always says "audited load".
- Settled by: IIS requests per minute against audit rows per minute (P7).
- ID: `th:a2:onset-load`.

**A3-a Progress inside the core.**
- Contradiction per episode: at least one sync or async row with both start and completion inside the core and a duration under 1 s. "Progress observed during the stall core."
- Contradicted: in ≥ 80% of n ≥ 3.
- There is no supporting path. Zero completions where control λ·|core| ≥ 5 marks the episode as tie group {A3, A5, A7, B14, B15, N1} and makes it INCONCLUSIVE ("tie").
- Label: Symptom.
- Settled by: an off-host plus on-host canary (P15).
- ID: `th:a3:core-progress`.

**A4-a Step change** (P1b or a restored copy).
- Statistic: a two-segment fit on log daily p95 per top-10 operation, each segment ≥ 5 days.
- Floor: step ratio ≥ 2 with volume ratio within [0.67, 1.5] in at least 3 operations at the same split day.
- Contradicted: no split with ratio ≥ 1.5.
- Label: Symptom.
- Settled by: P18 change history.
- ID: `th:a4:changepoint`.

**A5-a Cross-operation co-stall.**
- Condition: in the core, ≥ 3 distinct operations and ≥ 3 distinct users have in-flight rows above 3× their own baseline p95.
- Result: Consistent for tie group {A5, A6, A7, A8, B3, B5, B6, B7, B14, B15, B19} in k ≥ 3 episodes.
- Contradicted: ≥ 80% of episodes involve a single operation (which points to an endpoint-specific cause).
- ID: `th:a5:co-stall`.

**A5-b Start/complete shape.**
- Shape S (starts continue): s(t) in the core ≥ 0.5 × control while c(t) ≈ 0 (≤ 10% of control). The audit filter is running, so the block is after `RequestInitiatedAt`. Points to A5, A6, A8, B3 and B19.
- Shape Q (starts dip): s(t) ≤ 0.2 × control with I(t) held, then a release with starts resuming in a staircase. Points to A7, B9 and A3.
- Result: each episode is classified S, Q or neither. Consistent for a group when it holds in k ≥ 3 episodes.
- Label: Symptom.
- ID: `th:a5:shape`.

**A5-c Filter share and the token-filter control.**
- Statistic: among stall rows with timings, the share held by the permission filter and by the audit filter.
- Points to the permission side (A6, B5, B7, B8, B17, N2, N8) when the permission share is ≥ 50% of duration in k ≥ 3 episodes. Points to the audit side (A8, B3, B19, N3, N7) when the audit share is ≥ 50%.
- **Token-filter control** (contradicts A3, B15, B16, A12 and N1 as stall mechanisms). Consider rows whose `RequestInitiatedAt` falls inside the core, so their filters ran during the stall. The contradiction fires when there are ≥ 5 such rows with token-filter elapsed ≤ 2× its baseline p95 while the permission or audit filter is ≥ 10× its baseline. Host-wide slowdowns would stretch every filter.
- Filter order must be confirmed in T0.6. If the token filter runs before `RequestInitiatedAt`, use rows started inside the core whose token filter still ran inside it.
- IDs: `th:a5:filter-share`, `th:a3:token-control` (also cited by B15, B16, A12 and N1).

**A6.** No audit-only check beyond the A5-c pointer and an X9 peak at 100 s or 300 s. Copy: "Permission filter accounts for most of stall time (Symptom)". The result stays INCONCLUSIVE ("tie: A6, B5, B7, B8, B17, N2, N8").

**A7-a Peak in-flight versus minimum threads.**
- Statistic: max I(t), sync + async, in the core ÷ the configured minimum worker threads, a T0.6 parameter. Without the parameter, INCONCLUSIVE.
- Contradicted (for audited traffic): the ratio < 0.5 in ≥ 80% of n ≥ 3. The copy always adds "Unaudited traffic is not measured here (needs P7)".
- Consistent path: A5-b shape Q, with release starts following a staircase of 0.5–3 starts per second (thread injection about every 500 ms under starvation).
- ID: `th:a7:inflight-peak`, `th:a7:ramp`.

**A8-a Audit-create share.**
- Statistic: per episode, the fraction of stall rows with AuditCreateTime ≥ 0.5 × duration, reusing pattern rule B6's thresholds of 5 s and 0.3.
- Consistent (as a contributor): ≥ 30% in k ≥ 3.
- Contradicted (as the main stall mechanism): ≥ 80% of stall rows have AuditCreateTime < 1% of duration in ≥ 80% of n ≥ 3.
- Label: Symptom.
- Settled by: the P4 M1 join.
- ID: `th:a8:create-share`.

**A9-a Hangfire cessation.**
- Detection: X13 cessation points.
- Output: a Symptom "Hangfire-written audit rows stopped at \<t\>".
- Link test: is the episode rate per day after the cessation ≥ 2× the rate before it? That rate step is Symptom only.
- Settled by: the P5 + AM5 heartbeat.
- ID: `th:a9:cessation`.

**A10-a Warm-up signature.**
- Signature: after an idle gap ≥ 20 min (the IIS idle-timeout default) or after an orphan cluster, the first 10 requests have a duration-to-baseline median ≥ 5, decaying to ≤ 1.5 by request 30.
- Orphan cluster: ≥ 3 incomplete rows older than 10 s flush + 110 s `executionTimeout`, created within 5 min before the warm-up. This marks "process ended with requests in flight (Symptom)", with the M1 caveat that exceptions also orphan rows.
- Contradicted (as the stall explanation): ≤ 20% of episodes have a warm-up within ± 10 min.
- IDs: `th:a10:warmup`, `th:a10:orphans`.

**A11-a Day similarity.**
- Statistic: the day's stall rate, p95 and episode count fall within the [min, max] of the other post-step days.
- Contradicted: outside 1.5× that range.
- Label: Symptom.
- ID: `th:a11:day-range`.

**A13-a Onset-minute recurrence.**
- Floor: ≥ 3 onsets on ≥ 3 distinct days fall within one ± 2 min minute-of-day window, and that count is ≥ 3× the uniform expectation n·5/1440.
- Contradicted: no window holds onsets on more than 1 day, with n ≥ 5.
- Label: Symptom. It never names a job.
- ID: `th:a13:minute`.

**B1-a Deploy markers.**
- An X11 marker (a new field, operation or UI version) within ± 1 day of the A4-a split.
- Label: Symptom.
- Copy: "A new field first appeared within a day of the step". It never says "deploy caused".
- ID: `th:b1:marker`.

**B2-a SQL-client constants.**
- An X9 peak at 15 s or 30 s, with error `StatusCode` rows at those durations.
- Label: Symptom.
- ID: `th:b2:constants`.

**B3-a Simultaneous audit-create stall.**
- Condition: ≥ 3 users have AuditCreateTime ≥ 5 s with `RequestInitiatedAt` in the same 1 s, in k ≥ 3 episodes.
- Result: tie group {B3, B19, N3, N7, N12, N2}. An X9 peak near 30 s or 120 s shifts the pointer to the driver side (B19, N2, N3).
- ID: `th:b3:simul-create`.

**B4r Payload backlog, rewritten.**
- Live windows only. Count rows with `RequestCompletedAt` older than 10 s flush + 30 s whose payload update has not landed, in each minute of a live core, against controls.
- Consistent: lift ≥ 3 in k ≥ 3.
- Historical windows: NOT_TESTABLE_HERE ("UpdatedOn is set at queue time (T0.6); needs a queue-length field (T3 G)").
- ID: `th:b4:live-backlog`.

**B5-a Permission edit before onset.**
- Needs the analyst to tag "permission-changing operations" as a registry parameter. Untagged gives NOT_TESTABLE_HERE ("tag permission-changing operations").
- Floor: a tagged operation in [onset − 120 s, onset] in k ≥ 3, and ≥ 3× its control rate.
- ID: `th:b5:edit-before`.

**B7-a First-stalled-request recurrence.**
- Condition: the earliest stall row of each episode comes from the same company (if `AccountId` is ≥ 80% populated) or the same user, in k ≥ 3 episodes.
- Label: Symptom, with pseudonyms.
- ID: `th:b7:first-request`.

**B8-a Outbound constants.** An X9 peak at about 21 s or 42 s, or 100 s, in permission-filter elapsed time. Label: Symptom. ID: `th:b8:constants`.

**B10-a Size inside the window.**
- Statistic: Spearman ρ between `ResponseLengthInKiloBytes` and duration, stratified by operation, in strata with ≥ 30 rows.
- Floor: median ρ ≥ 0.3 *and* stall rows with responses ≥ 100 KB ≥ 50%.
- Contradicted for the inside window: ≥ 50% of stall rows have both request and response < 1 KB.
- The copy always says "inside the measured window; time outside needs P7".
- ID: `th:b10:size-inside`.

**B11-a Duplicate lift.**
- Statistic: the X12 duplicate share among rows in the neighbourhood against controls.
- Consistent: lift ≥ 2 and duplicates ≥ 20% of stall rows in k ≥ 3.
- Contradicted: the stall-row duplicate share is ≤ the control share + 5 points in ≥ 80% of n ≥ 3.
- Masked below 20.
- ID: `th:b11:dup-lift`.

**B12-a Concentration.**
- Statistic: per pseudonym, its share of stall rows divided by its share of sync rows.
- Consistent: some user or company has lift ≥ 3 and is present in ≥ 3 episodes.
- Contradicted: no lift ≥ 2 with ≥ 3 episodes, and the top share is < 50%.
- Cells below 20 are withheld.
- ID: `th:b12:concentration`.

**B13-a.** Same as A13-a. Attribution needs XS5.

**B15, B16, A12.** The `th:a3:token-control` contradiction only.

**B17-a Post-idle permission slowness.**
- Statistic: permission-filter elapsed for rows after a gap ≥ 100 s (`MaxServicePointIdleTime` default, after which a new connection is opened) against rows after gaps < 30 s.
- Consistent: lift ≥ 5 with ≥ 20 post-gap rows.
- Contradicted: lift ≤ 1.5 with ≥ 50.
- Ties with N2 (proxy leg) and B8.
- ID: `th:b17:post-idle`.

**B18-a Hangfire progress in the core.**
- Condition: Hangfire rows complete at ≥ 50% of the control rate inside sync cores, in k ≥ 3. The copy: "Hangfire-written work continued during the stall".
- Interpretation: either Hangfire runs in a different process, or it does not share the stalled dependency. It does not settle B18 without P5 + AM5.
- ID: `th:b18:core-progress`.

**B19-a Gap-conditioned audit create.**
- Statistic: the share of AuditCreateTime ≥ 1 s among rows after gaps ≥ G, with G ∈ {60 s, 4 min, 10 min} predeclared (balancer default, driver `MaxConnectionIdleTime`), against rows after gaps < 30 s.
- Consistent: lift ≥ 5 at some G with ≥ 20 post-gap rows, plus an X9 peak at 1–3 s or about 30 s.
- Contradicted: lift ≤ 1.5 at every G with ≥ 50 post-gap rows.
- Label: Symptom.
- Falsification: P15 keep-alive insert canary every 30 s should drive the lift to about 1.
- ID: `th:b19:gap-lift`.

---

## 8. Cross-source rules (highest value)

Each rule lists its inputs per source, the predicate, the contradiction test, how it degrades, its label and what would settle it. The general degradation ladder in §3.3 always applies first.

### XS1. Audit against IIS: time outside the pipeline (B9, B10, A2, A7, N9, N14)

**Inputs.**
- Audit: `RequestInitiatedAt`, `RequestCompletedAt`, `RequestMainUrl` path, `UserIp`, operation.
- P7: `time`, `time-taken`, `cs-uri-stem`, `c-ip` (or `X-Forwarded-For`), `s-ip`, `sc-bytes`, `sc-status`, `sc-win32-status`.
- HTTPERR: `s-reason`.
- P6: Requests Queued, HTTP Service CurrentQueueSize.

**Join.** A unique match on path + client IP + time anchor.
- The anchor is IIS `time` against `RequestCompletedAt`, or against `RequestInitiatedAt` if the golden fixture shows `time` is the start.
- Tolerance is 1 s (W3C resolution) + 1 s (ObjectId and method) + skew, which is 0 on the same host.
- Classes are unique, ambiguous or none. Ambiguous is never counted.
- The rule needs a unique-match rate ≥ 90% of audited rows in the core and controls.

**Statistics.** Residual r = `time-taken` − audit duration, in ms.
- **B9 / A7 pre-filter:** the median r of stall rows in the core, against the control p95 of r.
- **B10:** Spearman ρ of r against `sc-bytes`, stratified by operation.
- **A2:** the ratio of IIS rows per minute to audit rows per minute in the neighbourhood, against controls.
- **N9:** the `s-ip` distribution of stall rows against all rows.

**Predicates.**
- **B9 Consistent:** median r ≥ max(5 s, 3 × control p95 r) in k ≥ 3, **and** P6 queue counters > 0 or HTTPERR queue reasons in the core. Without that corroboration, the copy is only "time outside the pipeline observed (Symptom)", per §6.2.
- **B10 Consistent:** ρ ≥ 0.3 in strata with ≥ 30 rows, and r for responses < 1 KB at the control level. If AM4(e) applies, the small-response stratum is excluded.
- **B9 / B10 Contradicted:** r ≤ control p95 for ≥ 80% of matched stall rows in ≥ 80% of n ≥ 3. Time outside the window is not growing.
- **A2 unaudited load:** the ratio ≥ 2 × control in k ≥ 3 is Consistent. Ratio ≤ 1.2 extends A2-a's contradiction to all traffic.
- **N9 Consistent:** ≥ 80% of stall rows share one `s-ip` value while that value carries < 60% of the traffic.

**Degradation.**
- Unique-match rate below 90%: fall back to per-minute aggregates (IIS rows with `time-taken` ≥ 45 s per minute against audit stall rows per minute). Label capped at Symptom.
- No `sc-bytes`: B10 is NOT_TESTABLE_HERE ("needs sc-bytes in W3C (P7 + AM4)").
- No P7 at all: "needs IIS W3C with time-taken (P7, held)".

**Settled by:** a FREB trace of one slow request, or the T3 G host and timing fields.

**IDs:** `th:b9:residual`, `th:b10:residual-size`, `th:a2:iis-ratio`, `th:n9:instance`.

### XS2. Stall cores against SQL evidence (A1, B2, N10, N11, N13, B13, B15, A12)

**Inputs.**
- P3: system_health (`wait_info` with duration, `xml_deadlock_report`, scheduler ring buffer with process and system CPU), the ERRORLOG messages listed in §6.1, and PDH SQL counters.
- AM2: Query Store intervals with max duration and wait categories, per-minute wait deltas, default-trace autogrow events.
- P6: SqlClient pool counters.

**Join.**
- Interval overlap with the core.
- Tolerance: 5 s (system_health sampling) + skew. Query Store has hourly granularity, so it is used only for magnitude bounds, never for timing.
- Time zones: XE `timestamp_utc` is UTC. The ring buffer and ERRORLOG are server-local, converted with the DST-aware host-map rule.

**Predicates.**
- **A1 Consistent:** in k ≥ 3 episodes, a uniquely overlapping SQL event:
  - a `wait_info` row, a deadlock, a non-yielding scheduler (17883 family) or an 833
  - **or** a per-minute wait-delta minute where some wait class is above 3× its control p95
  - **and** Query Store max duration in the covering interval ≥ 0.5 × the shortest stall duration.
- **A1 Contradicted** needs all of these: Query Store active and covering every core, with max duration < 0.1 × the shortest stall; per-minute wait deltas showing no class above its control p95; PDH Processes blocked = 0 through the cores. With system_health alone, only "a lock wait ≥ 30 s inside the core" can be contradicted (threshold rule).
- **B2 Consistent:** the SQL side is clean (A1 not Consistent) **and** P6 pool counters reach ≥ 90% of Max Pool Size, or HardConnects/sec or Logins/sec are ≥ 3× control, in the core in k ≥ 3. An X9 peak at 15 s strengthens it.
- **B2 Contradicted:** the pooled-connection count stays ≤ 50% of the maximum through every core. This needs the detail switch (AM3b). Otherwise INCONCLUSIVE.
- **N10:** an autogrow event (default trace, or 5144/5145) for the log file overlapping the core in k ≥ 3. IFI never applies to log files, except growth of 64 MB or less on SQL Server 2022.
- **N11:** a 3197→3198 freeze window overlapping the core in k ≥ 3.
- **N13:** an 833 in [onset − 60 s, core end] in k ≥ 3.
- **B13 (SQL backups):** ERRORLOG 18264 backup completion times feed XS5.
- **B15 (SQL host):** 17890 (SQL memory paged out) overlapping the core.
- **A12:** ring-buffer `SystemIdle` and SQL process CPU. Copy: "overall host CPU; SQL attribution unknown", Symptom.

**Degradation.**
- P3 missing: "needs SQL system_health/ERRORLOG export (P3)".
- AM2 missing: A1 can still be Consistent, but A1 Contradicted is limited to ≥ 30 s locks.
- B2 without P6: INCONCLUSIVE ("needs pool counters (P6, held)").
- Ring-buffer timezone missing: refused at import.

**Settled by:** per-minute wait deltas plus a custom blocked-process XE through the next stall (T3 G).

**IDs:** `th:a1:sql-overlap`, `th:a1:qs-bound`, `th:b2:pool`, `th:n10:autogrow`, `th:n11:freeze`, `th:n13:io833`.

### XS3. Canary decision table (A3, A6, A7, A8, B3, B6, B7, B9, B14, B15, B19)

**Inputs.** P15 probes, joined to the core by point-in-interval with tolerance = probe period + skew.

**Per probe and episode.** A probe is slow when its latency is ≥ max(1 s, 10 × the control median) or it times out, in ≥ 50% of its samples in the core. Sample coverage in the core below 50% makes the probe "Not observed (gap)".

**Decision table.** Rows are evaluated in fixed order. Each row sets points-to and away-from sets for the episode:

| # | Probe condition (in core) | Points to | Away from |
|---|---|---|---|
| 1 | `node-health` slow (on-host) | A6, B7, B6, N8 | A8, B3, B19 |
| 2 | `proxy-cache-get` slow, `node-health` fast | B6, A6 | B7 |
| 3 | `mongo-ping` or `mongo-insert-canary-coll` slow | A8, B3, N7 | A6, B7 |
| 4 | Mongo probes slow only after their own idle gap, fast with a 30 s keep-alive | B19, N3, N2 | B3 |
| 5 | All dependency probes fast, `api-auth-light` slow | A7, B9, B14, B15, A5 (in-process) | A6, A8, B3, B6, B7 |
| 6 | `api-noauth` fast, `api-auth-light` slow | permission path: A6, B5, B8, B17 | A7, B9 |
| 7 | On-host loopback probes all slow, off-host `tcp-connect-api` slow | A3, B16 | A6, A8 |
| 8 | On-host probes normal | — | A3 |

**Aggregation per theory.**
- Consistent: pointed to in k ≥ 3 episodes and never pointed away from in those episodes.
- Contradicted: pointed away from in ≥ 80% of n ≥ 3.
- **Ties:** theories pointed to by the same rows in the same episodes form a tie group. They share a rank and are never ordered within the group.

**Degradation.**
- No P15: NOT_TESTABLE_HERE ("needs canary probe CSV (P15, proposed)").
- Only an on-host vantage: rows 7 and 8 cannot separate A3 from on-host fate, so A3 is limited to Contradicted via row 8.
- Probe clock not in T0.5: Symptom only.

**Settled by:** row 5 needs P17 to split A7, B14 and B15. Row 1 needs P16 to split A6 from B7.

**IDs:** `th:<theory>:canary-row<k>`, e.g. `th:a6:canary-row1`.

### XS4. Perf counters against stall cores (A7, B14, B15, B16, A12, B8)

**Inputs.** The P6 counters in §6.1. The counter CSV header carries a timezone, which is mandatory [verify format].

**Join.** Sample-in-interval, tolerance = 15 s (sample interval) + skew.

**Predicates.**
- **A7 Consistent:** in k ≥ 3, ASP.NET Requests Queued or Requests In Application Queue > 0 for ≥ 2 consecutive samples overlapping the core, **and** w3wp Thread Count rising at 0.5–3 per second across the core (thread injection).
- **A7 Contradicted:** both queue counters stay at 0 across the whole core with full sample coverage, and Thread Count is flat, in ≥ 80% of n ≥ 3.
- **B14 Consistent:** w3wp CPU (Process V2 % ÷ cores) ≈ 1 core (≥ 0.8/cores of the total) with near-zero completions, **or** LocksAndThreads Current Queue Length ≥ 3 × control, in k ≥ 3.
- **B15 Consistent:** a Gen 2 delta ≥ 1 spanning the core with % Time in GC ≥ 50% at the next sample, **or** Pages/sec ≥ 3 × control with Available MBytes ≤ 5% of RAM.
- **B15 Contradicted:** Gen 2 delta = 0 across the core and Pages/sec ≤ control p95.
- **B16 / A12 Consistent:** the CPU of the top non-API, non-SQL process is ≥ 3 × its control and host CPU is ≥ 80% in k ≥ 3.
- **B16 / A12 Contradicted** (this is the §8.4 falsification): no non-SQL process is above its baseline in any core.
- **B8 Consistent:** TCPIP 4227 or 4231 inside [onset − 60 s, core end], or the Ops netstat CSV (AM3e) showing TIME_WAIT toward the proxy port ≥ 3 × control, in k ≥ 3.

**Degradation.**
- No P6: "needs perf counter CSV (P6, held)".
- Sample interval > 15 s: tolerance widens, and if it exceeds the core, Symptom only.
- Process (not V2) counters with instance-name reuse: per-process rules are INCONCLUSIVE ("PID ambiguous").

**Settled by:** a hang dump (P17).

**IDs:** `th:a7:queue-counters`, `th:b14:cpu-lock`, `th:b15:gc-paging`, `th:b16:neighbour-cpu`, `th:b8:tcpip`.

### XS5. Stall onset against scheduled boundaries (B13, A13, A9, B18, A10)

**Inputs.**
- P5: Cron with TimeZoneId; msdb schedules. AM5 adds execution history.
- P3: ERRORLOG 18264 backups.
- P6: Task Scheduler 100/102 and Defender 1000/1001.
- IIS `periodicRestart` time or schedule (P0 or P18).

**Join.**
- Onset within ± τ of a boundary b.
- τ = 60 s (cron minute) + skew + onset uncertainty, where onset uncertainty is the gap from onset to the previous completed request in the same stratum, capped at 120 s.
- Execution history, when present, replaces planned boundaries with actual [start, end] intervals, and the test becomes overlap with the core.

**Statistics.**
- p0 = the fraction of the day covered by ± τ windows around all boundaries of the candidate schedule.
- k = the number of episodes whose onset falls within ± τ of a boundary.

**Predicates.**
- **Consistent:** k ≥ 3, k/n ≥ 0.6 and k/n ≥ 3·p0. This is roadmap §8.10's rule.
- **Contradicted:** the schedule is complete (AM5 history, or a fixed schedule with a known TimeZoneId), n ≥ 5, and k/n ≤ 1.5·p0.
- **Unknown run time:** "unknown", never a match.
- **A9:** the last heartbeat or last state change for the server or job falls within ± 5 min of the X13 cessation point. Consistent is a point match.
- **B18:** the heartbeat server id's host and PID match the API w3wp PID taken from P6 Process V2 at the same time. Same process gives Consistent. A different host gives Contradicted.

**Degradation.**
- No P5: B13 can still use P3 backups and P6 Task Scheduler events.
- None of them: "needs schedule export (P5, held) or Task Scheduler events (P6, held)".
- TimeZoneId missing: refused.

**Settled by:** the execution history of the matching job across two more episodes.

**IDs:** `th:b13:boundary:<schedule-hash8>`, `th:a9:heartbeat`, `th:b18:same-process`.

### XS6. Recycle, shutdown and system events (A10, N6, N1, B17, B14, A7)

**Inputs.**
- P6 and P7 WAS events: 5074–5080 and 5117 (recycle by reason), 5186 (idle shutdown, not a recycle, but it produces a cold start), 5010 (ping failure, worker killed), 5011 (fatal communication error), 5013 (shutdown time limit), 5002 (rapid-fail disable).
- ASP.NET 1309, and 1002 health-monitoring lifetime events if enabled.
- .NET Runtime 1026, Application Error 1000.
- Kernel-General 1 (system time change, with old and new time), W32Time.
- Schannel 36887/36874 and CAPI2.

**Join.** Event to onset within [0, 5 min], or event inside the core. Tolerance 2 s; events are UTC.

**Predicates.**
- **A10 Consistent:** a recycle, 5186, 5010 or 5011 within [onset − 5 min, onset] **and** A10-a's warm-up present, in k ≥ 3.
- **A10 Contradicted:** no such event within 10 min before onset in ≥ 80% of n ≥ 3, **only if** the config shows every `logEventOnRecycle` flag on. Before IIS 10 only Time, Memory and PrivateMemory are logged by default, so a missing event proves nothing.
- **B14 / A7 (hang):** 5010 inside or right after a core of ≥ 90 s. WAS judged the worker unresponsive, which points to B14, A7 and A3 and away from a remote dependency (pings are answered by an idle thread) [verify ping threading].
- **N6 Consistent:** A10-a warm-up plus orphans, with **no** WAS event, and ASP.NET 1002 "configuration changed" or a recompilation-limit reason in k ≥ 3. Lifetime events are not logged by default [verify], so absence never contradicts.
- **N1 Consistent:** a Kernel-General 1 time change with |new − old| ≥ 1 s inside the span in k ≥ 1 per episode. The episode is then flagged and excluded from all duration checks (M3).
- **B17:** Schannel or CAPI2 retrieval-timeout events inside the core in k ≥ 3.

**Degradation.**
- No P6 or P7: "needs Windows System/Application export (P6, held)".
- CAPI2 or Task Scheduler not enabled before the incident: "Not observed (log disabled)".

**IDs:** `th:a10:recycle`, `th:n6:appdomain`, `th:n1:clock-step`, `th:b14:was-ping`, `th:b17:schannel`.

### XS7. Audit create against mongod.log (A8, B3, B19, N3, N7, N12)

**Inputs.**
- P4 lines (§6.1) and the source-quality record.
- Audit stalls from pattern rule B6.
- X14 own-tool log.

**Join.** M1: an insert into the audit collection whose log `t` minus `durationMillis` and `t` itself fall within [`RequestInitiatedAt`, `CreatedOn`] ± 1 s. Two or more candidates are ambiguous and never counted. mongod times are iso8601-local with an offset that must be parsed.

**Predicates.**
- **A8 / B3 server side, Consistent:** for audit stalls in k ≥ 3 episodes, a unique slow op with `durationMillis` ≥ 0.5 × AuditCreateTime. A sub-attribute shifts the pointer:
  - `flowControl.timeAcquiringMicros` > 0, or `waitForWriteConcernDurationMillis` large: N7
  - `storage.data.timeWaitingMicros` (cache) large, or a REPL transition in the span: B3
  - `appName` equal to the Unravis appName, or another tool's, at the same time: N12
- **Client side** (B19 / N3 / N2) **Consistent:** quality complete, **no** op above slowms in [RequestInitiatedAt, CreatedOn], and a `Connection accepted` (22943) from the API host's pseudonymised remote inside that interval, in k ≥ 3. For B19 specifically, also a `Connection ended` (22944) for that remote after a gap ≥ G and none before. For N3, ≥ 2 accepts per audit stall (pool refill, maxConnecting = 2).
- **B3 Contradicted:** in ≥ 80% of n ≥ 3 audit-stall episodes, quality is complete, no op is above slowms and there is no REPL line. Copy per §8.8: "No slow operation above \<slowms\> ms logged (server-side stall less likely, Correlation level only)". On 8.0+, add "ticket-queue time may be excluded (workingMillis)".

**Degradation.**
- Rotation gap over a core: that episode is excluded.
- slowms ≥ the predicted magnitude: no contradiction.
- No P4: "needs mongod.log copy (P4, held)".

**Settled by:** the P15 keep-alive canary removing the B19 lift.

**IDs:** `th:a8:mongod-match`, `th:b3:server-slow`, `th:b19:conn-accept`, `th:n3:refill`, `th:n7:flowctl`, `th:n12:tool-load`.

### XS8. App Insights dependency attribution (A6, A5, B8, B2, A1)

**Inputs.** P9 requests and dependencies with `sum(itemCount)`.

**Join.** Operation id within App Insights. Matching App Insights requests to audit rows is by path + time ± 1 s and is unique-or-skip.

**Statistics.** For each stall request: f_proxy = Σ duration of HTTP dependencies whose target is the proxy, divided by request duration. Likewise f_sql and f_other.

**Predicates.**
- **A6 Consistent:** median f_proxy ≥ 0.5 in k ≥ 3.
- **A1 / B2 client view:** f_sql ≥ 0.5 while XS2 shows short server time. That points to B2 (connection wait inside the dependency) [verify whether the SQL dependency includes open].
- **Falsification** (§8.11): Σf < 0.2 in ≥ 80% of stall requests means the time is in-process or uncollected. That points to A7, B14, B15, N4, or A8 (Mongo is uncollected).
- **No contradiction from missing samples.**

**Degradation.**
- No P9: "needs App Insights export (P9, held)".
- Empty result: "possibly no access" (D1).

**IDs:** `th:a6:ai-share`, `th:b2:ai-sql`, `th:a5:ai-unattributed`.

### XS9. Hang-dump stack summary (A5, A6, A7, A8, B2, B8, B14, B15, N4, B17)

**Inputs.** P17 summaries.

**Join.** The dump UTC time must fall inside a core, point-in-interval, tolerance = dump write duration. Each dump is one episode observation.

**Predicate.** Among request threads, a class holding ≥ 50% wins the dump. Two classes within 10 points of each other are a tie. The class map:

| Winning class | Points to |
|---|---|
| `http-client` or `socket-connect` | A6, B8, N2 (proxy leg) |
| `dns` | B17 |
| `mongo-driver` | A8, B3, B19, N3 |
| `sqlclient-pool-wait` | B2 |
| `sqlclient-exec` | A1 |
| `monitor-wait` on one lock | B14 |
| `session-state` | N4 |
| `gc` | B15 |
| Few request threads and a large ASP.NET queue | A7 |

- Consistent: the same class wins in k ≥ 3 dumps from different episodes.
- Label: Correlation.
- One dump is Symptom. It is "strong-eligible" at 3 of 3 dumps.
- Contradicted (for theories whose class never exceeds 10% of request threads): in ≥ 80% of ≥ 3 dumps.

**Degradation.** Dump outside the core: excluded. No P17: "needs hang-dump summary (P17, proposed)".

**IDs:** `th:<theory>:dump-class`.

### XS10. Proxy log (A6, B5, B7, N8) and Redis (B6)

**Inputs.** P16.

**Join.** Interval or point. Proxy host skew comes from T0.5.

**Predicates.**
- **A6:** the proxy's p95 `get` duration in the core ≥ 0.5 × the median stall duration in k ≥ 3.
- **B5:** a `clear-pattern` call in [onset − 120 s, onset] followed by a miss rate ≥ 3 × control, in k ≥ 3.
- **B7:** event-loop delay p99 ≥ 1 s in the core, plus one large value (≥ the p99 of value bytes) served just before onset, in k ≥ 3.
- **N8:** event-loop delay normal while `get` durations are long and the libuv-bound work (gzip, DNS lookup) is in progress.
- **B6:** NOT_TESTABLE_HERE ("needs Redis SLOWLOG/LATENCY (P10, deferred)"). P16 reconnect events in the core are recorded as Symptom.

**IDs:** `th:a6:proxy-p95`, `th:b5:clear`, `th:b7:loop-delay`, `th:n8:libuv`.

### XS11. Change history against step change (A4, B1)

**Inputs.** P18, and A4-a's split day.

**Predicates.**
- **B1 Consistent:** a recorded change in [split − 1 day, split] (or [split − 1 h, split] if the history is timestamped), and no comparable step at the other recorded change dates.
- **B1 Contradicted:** history coverage is attested complete for ± 3 days and there is no change, under §6.2 "not observed ≠ ruled out". Without attested completeness the result is only "Not observed".
- **Label:** Correlation. Single change-point, so strong-eligible is never set (n = 1).

**IDs:** `th:b1:change-match`, `th:a4:change-match`.

---

## 9. Research: new theories (N-series) attached to phases

Each entry gives the mechanism, its signature in latency data, an audit test, the external settle, the phase and its sources. Confidence tags: [verify] marks a constant or ID that is not confirmed against a primary source.

| ID | Mechanism | Latency signature | Audit test (now) | Settle (external) | Phase |
|---|---|---|---|---|---|
| N1 | **Wall-clock step.** W32Time steps the clock when the offset exceeds its limits, instead of slewing it. | False stalls or false fast requests. Negative durations. `CreatedOn < RequestInitiatedAt`. Rows from many operations shifted by the same amount. | M3 flags. A shift test: all rows started in a ± 5 s window show the same duration excess. | Kernel-General 1 old/new time; W32Time events | P6 (held) |
| N2 | **Stateful middlebox idle timeout** (balancer, NAT or firewall) silently drops idle flows. The next use waits out retransmission. | Slow first use after a gap ≥ the middlebox timeout (4 min by default on Azure's balancer). Durations cluster at the retransmit give-up time. A Linux peer holds half-open connections for about 13–30 min (`tcp_retries2` = 15). | B17-a for the proxy leg, B19-a for the Mongo leg, X9 peaks | Device idle-timeout config, packet capture, TCPv4 Segments Retransmitted/sec | P6 (held); device config is out of product |
| N3 | **Mongo pool cold refill.** After `MaxConnectionIdleTime` (10 min) idle pruning, the pool refills at most `MaxConnecting` (2) connections at a time, each with TCP + TLS + auth. Ties with B19. | A burst after idle where audit-create times stagger in pairs | B19-a plus a pairwise stagger: audit-create completion times among simultaneous starts form ≥ 2 steps | mongod 22943 count ≥ 2 per stall | P4 (held) |
| N4 | **ASP.NET session-state exclusive lock** serialises a user's concurrent requests. A read-only session still takes a reader lock, and the provider polls every 0.5 s. | One user's requests serialised. Other users unaffected. | Within the core, the same user's in-flight intervals never overlap while other users run normally. Lift of per-user serialisation against controls. Retired if T0.6 shows session state off. | Hang dump `session-state` class; config | Audit now; P17 |
| N5 | **Browser per-host HTTP/1.1 connection cap** (6 in Chromium) queues client-side. The wait is invisible to the server. | The user feels slowness the audit log never shows. Per-user in-flight capped at 6. Chained starts. | Per-user I(t) ≤ 6 with a next start ≤ 50 ms after a completion, in stall neighbourhoods | HAR "blocked/queueing" time | P19 (new) |
| N6 | **AppDomain restart without a WAS recycle.** File-change notification on bin or config, or reaching `numRecompilesBeforeAppRestart` (15 [verify]). | Orphan rows, then a cold-start warm-up, with no WAS event | A10-a orphans plus warm-up with no WAS event (once P6 or P7 exist) | ASP.NET 1002 reason "Configuration changed"; deploy log | P6 (held) |
| N7 | **Mongo write-concern wait or flow control.** The implicit default write concern is majority since 5.0 (with an arbiter exception). Flow control throttles writers when majority lag exceeds `flowControlTargetLagSeconds` (10). | Every insert slow together, for seconds, on a healthy primary | B3-a tie | mongod `flowControl.timeAcquiringMicros`, `waitForWriteConcernDurationMillis`, replica status | P4 (held) |
| N8 | **Node libuv thread pool saturation.** The default size is 4. `dns.lookup`, async zlib, fs and crypto share it, so gzip or DNS work queues even when the event loop is idle. | Proxy latency high, event-loop delay normal | none (A6 tie) | P16 fields + `UV_THREADPOOL_SIZE` | P16 (new) |
| N9 | **One degraded instance behind a balancer.** | Bimodal latency within a minute for the same operation | Within the core, the same operation shows both stalled and fast completions for other users (bimodal) | `s-ip` concentration (XS1); T3 G host field | P7 (held) |
| N10 | **SQL log-file autogrowth zero-initialisation.** IFI does not apply to log files, except growth of 64 MB or less on SQL Server 2022. Wait type PREEMPTIVE_OS_WRITEFILEGATHER. | SQL-touching requests stall for seconds at a growth event | none | Default-trace 92/93, ERRORLOG 5144/5145, file growth settings | P3 + AM2(c) |
| N11 | **VSS snapshot I/O freeze** (backup agents, VM snapshots). Normally sub-second, but it can hang. | All SQL writes freeze. Recurs at backup times. | A13-a | ERRORLOG 3197/3198, backup tool logs | P3 |
| N12 | **Analyst or tool load on the shared Mongo,** including Unravis broad scans and census queries. | Audit-create stalls during the tool's own heavy queries | X14 own-tool log against audit-stall times: the share of audit stalls inside tool-query windows against the time share of those windows (lift ≥ 3, k ≥ 3) | mongod `appName` on slow ops | Now (own log); P4 confirms |
| N13 | **SQL I/O stall ≥ 15 s** (error 833: storage or SAN path) | Every SQL-touching request stalls together | none | ERRORLOG 833 with file and duration | P3 |
| N14 | **PMTU black hole** (RFC 2923). Small packets pass, full-size segments vanish, so transfers above the path MTU stall until timeout. | Stall probability steps up with payload size above about 1.4 KB on one network leg | B10-a stratified by request size, looking for a step rather than a slope (inside-window part only) | P7 `sc-bytes` / `cs-bytes` against `time-taken`; ping with DF set | P7 (held) |
| N15 | **SQL client pool idle pruning plus a dead connection.** Idle connections are removed after about 4–8 min. A connection dropped by a middlebox can be handed out and fail or hang on first use. Variant of B2 and N2. | First SQL call after a quiet spell is slow or errors | X9 peak at 15 s or 30 s after gaps ≥ 4 min (B2-a × X7) | P6 pool counters, P12 app errors | P6 (held) |

**Thread-pool injection ramp.** Folded into A7 (`th:a7:ramp`), since it is A7's own signature and not a separate cause.

**Rejected candidates, with the reason for each:**
- **Synchronous auto-update of statistics.** Its scale is milliseconds to seconds, it would appear in Query Store compile time, and it cannot stall operations that do not touch SQL.
- **Nagle and delayed ACK.** About 200 ms scale, not seconds.
- **`PoolBlockingPeriod`.** It makes requests fail fast with a cached error. It does not stall them.
- **Kerberos or AD authentication latency.** The token filter is flat in stall rows, which A5-c checks.
- **Linux transparent huge pages on mongod.** Millisecond jitter, and current Mongo guidance changed.
- **HTTP/2 head-of-line blocking.** No evidence the API path uses HTTP/2. It would be indistinguishable from N5 anyway.
- **Antivirus on-access scanning.** Already covered by B13 and A12. No distinct signature.

---

## 10. Scorecard architecture

### 10.1 Package and registry (design only)

The package is `internal/theory`. It depends on `audit` (the report and an IntervalReader) and on the importer outputs. Nothing depends on it except the server handler and `evidence`.

```go
type SourceReq struct { Source, Phase string; Held, Proposed bool; Fields []string }
type Check struct {
    ID      string            // "inflight-peak"
    Kind    string            // "support" | "contradict"
    Needs   []string          // extractor ids X0..X15 and source ids
    Predicted *Magnitude      // for threshold-source contradiction rule (§3.3.5)
    Eval    func(*Inputs) Outcome
}
type Rule struct {
    Theory    string          // "a7" (theory list id or n-id), never free text from the private file
    Title     string          // generic copy-deck key
    Requires  []SourceReq
    Checks    []Check
    TieGroup  []string
    MaxLabel  string          // Symptom | Correlation
    Settle    string          // copy-deck key for "what would settle it"
}
var Registry = map[string]Rule{ /* fixed, versioned: EngineVersion */ }
type Outcome struct {
    Result        string      // SUPPORTED_BY_DATA | CONTRADICTED_BY_DATA | INCONCLUSIVE | NOT_TESTABLE_HERE
    Reason        string      // §6.6 template id + args
    K, N          int
    PerEpisode    []EpisodeOutcome
    Label         string
    StrongEligible bool
    Missing       *SourceReq  // set when NOT_TESTABLE_HERE
    JoinClass, Tolerance string
    Sources       []evidence.SourceQuality
    EvidenceID    string
}
```

- **Registry content.** The registry holds the generic titles from §5.1 and never reads `sql/theories.md`.
- **Analyst parameters**, entered per case and recorded in params so that they feed the evidence-ID hash:
  - the mapping of FilterName to role
  - the configured minimum worker threads
  - the permission-changing operation tags
  - the API process identity
  - Max Pool Size
  - slowms
  - the runtime

### 10.2 Extractors and budgets

- **Order.** `RunPatterns` runs first, then `theory.Run(report, readers, imports)`.
- **IntervalReader.**
  - One query per window. There are at most 20 episodes × (1 neighbourhood + 3 controls) = 80 windows. Adjacent windows are merged first.
  - Each query is narrow (indexed on `RequestInitiatedAt` or `_id`-hinted, ≤ 24 h), at concurrency 1, with `InteractiveQueryBudget` of 10 s and `appName` set.
  - The projection is minimal: the timestamps, operation, a UserId hash computed in memory, polling type, KB fields, AuditCreateTime, PreActionFilterTimings and StatusCode.
- **Row cap and time budget.**
  - The total cap is `MaxSummaryRows` (40,000), with a whole-run budget of `CensusQueryBudget` (60 s).
  - Overrun is fail-closed. Affected episodes are dropped from n with reason "budget", and docsExamined goes into provenance.
  - A live source never broad-scans. X10 multi-day series run only under P1b or on a restored copy.
- **Memoisation.** Imported sources are read from the case folder (§6.7). Extractor results are memoised per report and cleared when the report changes.

### 10.3 Integration

- **Patterns engine.** Each `Outcome` becomes an `audit.Finding` with:
  - `Rule = "th:" + theory + ":" + check`
  - `Label`
  - `Headline` (a §6.6 template)
  - `N` (episodes), `LowN` (n < 3 or decisive count < 20)
  - `EvidenceID`

  The theory findings go in a new `PatternReport.Theories []TheoryRow` (or `GET /api/theories?report=`) so that pattern findings stay unchanged.
- **Evidence (P13). Needed change:** `Record` holds a single `Source` and a single `Quality`, which cross-source records break. Proposal: **schema version 2**, adding `Sources []SourceRef{Label, Quality}`, plus per-finding `JoinClass` and `Tolerance` taken from the outcome. `Build` already masks counts, suppresses complements, rejects "proven" and canonicalises.
- **Ledger.** "Copy to ledger" calls `Store.Create(text)` with generic text, for example "Theory A7 (thread-pool starvation): check results attached". It then calls `Update` with `EvidenceIDs` only and state `open`.
  - The UI *shows* K, N, counterexample episodes and the tie group as suggestions. The analyst types `Facts`, because the ledger contract says the product derives none of them.
  - The ledger guards then judge strong correlation as they do today.
- **Copy lint.** Every rendered string passes the existing banned-word lint and the `containsProven` check.

### 10.4 UI sketch

```
Theory checks                      Engine v1 · 47 predeclared checks · window <start>–<end> (UTC)
Show: (•) all  ( ) testable now  ( ) blocked by held phase  ( ) proposed sources
Ordered by decisiveness (episodes with a determinate result), not by likelihood. No check names a cause.

 Theory                      Result                         Label    Episodes  What would settle it                         
 ─────────────────────────── ────────────────────────────── ──────── ───────── ──────────────────────────────────────────── ───────────────
 A3  Host or VM freeze       Data contradicts theory        Symptom  4 of 4    Off-host + on-host canary (P15, proposed)    [Copy to ledger]
       ▸ th:a3:core-progress:…  progress observed during the stall core in 4 episodes
 A7  Thread-pool starvation  Data contradicts theory        Symptom  4 of 4    Request-queue counters (P6, held)            [Copy to ledger]
       ▸ for audited traffic only; unaudited traffic needs IIS W3C (P7, held)
 ┌ tie: A6 · B5 · B7 · B8 · B17 · N2 · N8 ─────────────────────────────────────────────────────────────────────────────────────────────┐
 │ A6  Proxy stalls          Not enough data (tie)          Symptom  3 of 4    App Insights dependency time (P9, held)       [Copy]   │
 │ ...                                                                                                                                 │
 └──────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────┘
 B19 Idle Mongo drop         Consistent with theory         Symptom  <20 rows  Keep-alive canary removes the lift (P15)      [Copy to ledger]
 B9  Pre-filter queueing     Not testable here: needs IIS W3C with time-taken (P7, held)                                                
 B6  Redis latency           Not testable here: needs Redis SLOWLOG/LATENCY (P10, deferred)                                         
```

- Expanding a row shows per-episode outcomes, counterexamples, join class, tolerance, source quality and the evidence ID with a copy button.
- There is no numeric score anywhere. "Scorecard" is an internal name; the UI says "Theory checks".
- Ties are boxed and share a rank.
- Contradicted rows are not hidden. They are the most decisive output.

---

## 11. Build order (follows the roadmap phase order)

| Step | With roadmap phase | Ships | DoD addition for that phase |
|---|---|---|---|
| 1 | Ruling R-TE; P1a | Registry skeleton, copy deck, extractors X0–X9 and X12, IntervalReader with caps, NOT_TESTABLE_HERE rows for every rule | "Every theory shows its result or the source and phase it needs" |
| 2 | P2-lite + ledger + P13 + P14a | Audit-only rules of §7 (A2-a, A3-a, A5-a/b/c, A7-a, A8-a, A9-a, A10-a, A11-a, A13-a, B1-a, B2-a, B3-a, B4r, B5-a, B7-a, B8-a, B10-a, B11-a, B12-a, B17-a, B18-a, B19-a, N4, N5 server side, N12), UI, copy-to-ledger, evidence schema v2 | P2-lite: "theories A3, A5, A7 (contradiction), A8, B11, B12 and B19 become testable"; P13: "theory records export masked" |
| 3 | P3 (not held) | XS2: A1, B2 (SQL side), N10, N11, N13, B13 backups, B15 (17890), A12 (Symptom) | P3: "A1, N10, N11 and N13 become testable; B13 for SQL backups" |
| 4 | P6 (held) | XS4, XS6, the B13 host side of XS5, B2 pool | P6: "A7, B14 (partial), B15, B16, A12, B8, B17, A10, N1 and N6 become testable" |
| 5 | P7 (held) | XS1 | P7: "B9, B10, A2 (all traffic), N9 and N14 become testable" |
| 6 | P5 + P4 (held) | XS5 (full), XS7 | P5: "B13, A9 and B18 become testable"; P4: "A8 and B3 server side, B19, N3, N7 and N12 confirmation" |
| 7 | P9 (held) + P8 | XS8; theory overlays on the P8 timeline (display only) | P9: "A6 and A5 attribution become testable" |
| 8 | New P15–P19 (after slice cards and stop rules) | XS3, XS9, XS10, XS11, N5 HAR | each: its listed theories |

Steps 1–3 need no held approval. Steps 4–7 wait on third-party approval.

---

## 12. Tests

1. **Planted-truth fixtures, one or more per rule.** A seeded generator writes synthetic audit rows, and synthetic import files for cross-source rules, with one mechanism planted. Examples:
   - A7: a start dip with in-flight ≥ the thread minimum, then a staircase release.
   - A5-b shape S: starts continue while completions stop.
   - B13: onsets at one cron minute on 4 days.
   - B19: slow creates after 5-minute gaps.
   - XS1: residual growing with `sc-bytes`.
   - XS3: each decision-table row.
   - XS9: dump summaries with one dominant class.

   Assertions:
   - The target rule is Consistent.
   - Every rule outside the target's declared tie group is not Consistent.
   - The tie group members are INCONCLUSIVE("tie") or Consistent together.
2. **Falsification and null calibration.** For 200 seeds of a null world (random stalls, no mechanism), each rule's false-Consistent rate must be ≤ 5%. Report the registry-wide rate (≥ 1 false Consistent per seed), with a target of ≤ 20%. A rule that fails gets a higher floor and a version bump.
   - Also, for each planted mechanism, its *contradicting* rules must be Contradicted or INCONCLUSIVE, never Consistent. For example, a host-freeze fixture must make `th:a3:token-control` silent.
3. **Degradation fixtures:**
   - a missing source gives the exact NOT_TESTABLE_HERE text with phase and held flag
   - an unlabelled timezone is refused
   - ambiguous joins above 10% give INCONCLUSIVE
   - a coverage gap over a core removes that episode from n
   - a threshold source cannot contradict a sub-threshold prediction
   - a missing sampled-source sample is never counter-evidence
   - a clock anomaly (N1) excludes the episode
   - the budget or row cap fails closed
4. **Golden join fixture** (roadmap): one event aligned across XE, ERRORLOG, PDH, W3C, mongod.log and audit, covering both W3C anchor hypotheses (AM4b).
5. **Determinism:** identical inputs give byte-identical outcome JSON and evidence IDs. Every ID matches the regex.
6. **Copy and privacy:**
   - every rendered string passes the banned-word lint and `containsProven`
   - no count below 20 is unmasked
   - complements are suppressed
   - the registry contains no string from `sql/theories.md` (a test greps for its distinctive tokens and fails on any match)
7. **Ledger contract:** copy-to-ledger creates `open` with evidence IDs and empty `Facts`. The engine has no code path to `Update` with a state other than `open`.

---

## 13. Risks

| Risk | Mitigation |
|---|---|
| **Multiple comparisons.** About 47 checks at about 5% each gives about 2 false Consistent results in a null world. | Fixed registry, effect floors, k ≥ 3 episodes, null calibration with the registry-wide rate published in the UI header, contradiction-first display, and no p-values. |
| **Ties read as a winner.** | Tie groups are boxed and share a rank. Ordering is by decisiveness, not likelihood. The alternatives come from the tie group automatically. |
| **Results read as proof.** | No numeric score. Labels are capped at Correlation. The "Consistent with theory" wording comes from §6.6 and passes lint. Strong correlation is set only by the analyst in the ledger. Export rejects "proven". |
| **Most rules blocked by held phases**, so the page looks empty. | NOT_TESTABLE_HERE rows double as the evidence plan (filter "blocked by held phase"). Steps 1–3 ship value without approvals. |
| **Overfitting thresholds to known stalls.** | Thresholds come from vendor constants or are fixed in this document before data is viewed. Changes need a version bump plus a recalibration run. |
| **Wrong measurement assumptions** (W3C time anchor, `time-taken` with small responses, counter semantics, `workingMillis`, driver pool sharing). | Each one is tagged [verify]. The golden fixture and T0.6 settle them before the dependent rule ships. |
| **Survivorship and censoring (M1).** | Absence is never evidence. Incomplete rows are censored. Copy says "not observed". |
| **The tool's own load biasing Mongo results (N12).** | X14 runs before any Mongo-side result is shown. |
| **Private theory text leaking into the product.** | The registry uses generic titles. The privacy test in §12.6 enforces it. Ledger text from copy-to-ledger is generic. |
| **Governance (decision 15, C7, P8 composites).** | Nothing is built before R-TE. The fallback is a list with no result column. |

---

## 14. Sources

**ASP.NET and IIS:**
- Session state locking: https://learn.microsoft.com/en-us/previous-versions/aspnet/ms178581(v=vs.100) · https://learn.microsoft.com/en-us/dotnet/api/system.web.sessionstate.sessionstatestoreproviderbase
- `executionTimeout`: https://learn.microsoft.com/en-us/dotnet/api/system.web.configuration.httpruntimesection.executiontimeout
- `numRecompilesBeforeAppRestart`: https://learn.microsoft.com/en-us/previous-versions/dotnet/netframework-4.0/s10awwz0(v=vs.100)
- Health monitoring and event 1002: https://learn.microsoft.com/en-us/previous-versions/dotnet/articles/aa479026(v=msdn.10) · https://msdn2.microsoft.com/en-us/library/9ytec74h.aspx
- IIS process model, idle timeout and ping: https://learn.microsoft.com/en-us/iis/configuration/system.applicationhost/applicationpools/add/processmodel
- W3C log fields: https://learn.microsoft.com/en-us/iis/configuration/system.applicationhost/sites/site/logfile/
- `time-taken` [verify]: https://support.microsoft.com/kb/944884

**.NET networking:**
- `MaxServicePointIdleTime`: https://learn.microsoft.com/en-us/dotnet/api/system.net.servicepointmanager.maxservicepointidletime · https://learn.microsoft.com/archive/blogs/jpsanders/understanding-maxservicepointidletime-and-defaultconnectionlimit
- `HttpWebRequest` timeouts: https://learn.microsoft.com/en-us/dotnet/api/system.net.httpwebrequest.timeout · https://devblogs.microsoft.com/buckh/timeouts-on-the-httpwebrequest-and-thus-soap-proxies/

**CLR thread pool:**
- https://learn.microsoft.com/en-us/dotnet/core/diagnostics/debug-threadpool-starvation
- https://mattwarren.org/2017/04/13/The-CLR-Thread-Pool-Thread-Injection-Algorithm
- https://github.com/dotnet/runtime/issues/46077
- Lock and thread counters: https://learn.microsoft.com/en-us/dotnet/framework/debug-trace-profile/performance-counters

**SQL client:**
- Pooling: https://learn.microsoft.com/sql/connect/ado-net/sql-server-connection-pooling
- `ConnectionTimeout`: https://learn.microsoft.com/en-us/dotnet/api/system.data.sqlclient.sqlconnection.connectiontimeout
- `CommandTimeout`: https://learn.microsoft.com/en-us/dotnet/api/system.data.sqlclient.sqlcommand.commandtimeout
- ADO.NET performance counters and the detail switch: https://learn.microsoft.com/en-us/dotnet/framework/data/adonet/performance-counters

**SQL Server:**
- Error 833: https://learn.microsoft.com/en-us/sql/relational-databases/errors-events/mssqlserver-833-database-engine-error · https://learn.microsoft.com/en-us/troubleshoot/sql/database-engine/performance/troubleshoot-sql-io-performance
- Instant file initialisation: https://learn.microsoft.com/en-us/sql/relational-databases/databases/database-instant-file-initialization · https://www.sqlservercentral.com/blogs/reality-and-limits-of-instant-file-initialization-for-transaction-logs-in-sql-server-2022
- VSS freeze: https://support.microsoft.com/help/4040108 · https://learn.microsoft.com/en-us/answers/questions/1086497/is-i-o-frozen-in-vss-backup-increase-base-on-the-s

**MongoDB:**
- C# pool: https://www.mongodb.com/docs/drivers/csharp/current/connect/connection-options/connection-pools/
- Per-client pools FAQ: https://www.mongodb.com/docs/drivers/csharp/v2.x/faq/
- Node pool: https://www.mongodb.com/docs/drivers/node/current/connect/connection-options/connection-pools/
- Connection string timeouts: https://www.mongodb.com/docs/manual/reference/connection-string-options/
- 5.0 write-concern default: https://www.mongodb.com/docs/manual/release-notes/5.0-compatibility/
- Flow control: https://www.mongodb.com/docs/manual/tutorial/troubleshoot-replica-sets/ · https://www.percona.com/blog/mongodb-queries-slow-due-to-flow-control-but-no-replication-lag/
- Log message IDs [verify]: https://www.mongodb.com/docs/manual/reference/log-messages/
- WiredTiger: https://www.mongodb.com/docs/manual/core/wiredtiger/

**Node:**
- https://nodejs.org/api/cli.html#uv_threadpool_sizesize
- https://nodejs.org/api/dns.html#implementation-considerations

**Networking:**
- Windows SYN retransmission: https://technet.microsoft.com/en-us/library/cc938208.aspx · https://docs.catchpoint.com/docs/tcp-connection-retransmissions.md
- Azure balancer idle timeout and reset: https://learn.microsoft.com/en-us/azure/load-balancer/load-balancer-tcp-idle-timeout · https://learn.microsoft.com/en-us/azure/load-balancer/load-balancer-tcp-reset
- Linux `tcp_retries2`: https://man7.org/linux/man-pages/man7/tcp.7.html · https://www.kernel.org/doc/Documentation/networking/ip-sysctl.txt
- PMTU black holes: https://www.rfc-editor.org/rfc/rfc2923

**Windows time:** https://learn.microsoft.com/en-us/archive/blogs/w32time/configuring-the-time-service-maxposnegphasecorrection · https://support.microsoft.com/kb/884776

**Browser connection cap:** https://developer.mozilla.org/en-US/docs/Web/HTTP/Connection_management_in_HTTP_1.x · https://kb.coder.com/articles/7614098537-understanding-and-troubleshooting-queued-or-stalled-network-requests
