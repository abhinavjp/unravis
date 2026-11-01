# How things are calculated in the Investigation Timeline

Source pack for an explainer video. Written for a complete beginner (support engineer, junior developer): plain English first, then the technical term. All demo numbers are made up.

## 0. How to read this file

Every statement carries one tag. If a statement has no tag, it is a plain-English explanation of the tagged statement before it.

| Tag | Meaning | Where to check it |
|---|---|---|
| **[Real app]** | Computed by the shipped Go or Svelte code on `origin/master` | `internal/investigate/*.go` (thresholds, facts, evaluate, origin), `internal/audit/*.go` (histogram, status, thresholds), `web/src/lib/zoom.ts`, `web/src/lib/time.ts`, `docs/user/guided-analysis.md` |
| **[Prototype]** | Computed only by the clickable prototype `prototype/tracing-paper-PROTOTYPE.html` (made-up data, no server) | functions `auditWindow`, `laneWin`, `traceStats`, `ladderWidth`, `axis`, `attrModel`, `attrVerdict`, `evaluateSuspects`, `layerRead` |
| **[Vendor doc]** | From the research notes in the repo (they cite official Microsoft and MongoDB pages, plus practitioner experience) | `buildout/research/investigation-walkthrough.md` |

Two words used all the time:

- **Investigation window** = the stretch of time you think the problem happened. **Comparison period** = an earlier stretch used only for comparison. It is not a promise that things were healthy.
- **Suspect** = something worth checking, shown with evidence for and against. The screen never states a cause. It says what fits the data, what does not, and what could not be tested.

Version note. **[Real app]** All real thresholds live in one table with a version (`2026-10-27`) and a fingerprint. A saved answer carries the version, so you can always see which rules produced it. Changing a number means changing the version.

---

## 1. Request count and rate

**Plain meaning.** How many requests arrived in the period, and how many per minute.

**Rule.**
- **[Prototype]** Count = sum of the per-minute counts inside the window. Rate = count divided by window length in minutes.
- **[Real app]** `PerSec` = audited requests per second over the *testable* minutes only. Minutes inside a gap are left out and never counted as zero.

**Example.** 3,000 requests in 60 minutes = 50 per minute.

**Why it matters.** A jump in volume (a traffic surge) is a different story from the same traffic running slower.

**Pitfalls.**
- The audit log may cover only a slice of the traffic, so its rate is not the whole system's rate.
- A raw count compared across windows of different length is misleading. Use the rate (next section).

**Differs.** **[Real app]** also needs the traffic to move by 30% before it reads "up" or "down" (section 10). The prototype just shows the ratio.

## 2. "N times the comparison period"

**Plain meaning.** How many times bigger the number is than in the comparison period, after allowing for the two periods being different lengths.

**Rule. [Prototype]** For counts (requests, 5xx errors, slow calls): `ratio = (count_now / minutes_now) / (count_before / minutes_before)`. For P95 the ratio is simply `P95_now / P95_before`.

**Example.** Window: 60 minutes, 3,000 requests = 50/min. Comparison: 120 minutes, 4,800 requests = 40/min. Ratio = 50 / 40 = **1.25 times**. Comparing raw counts would wrongly say 0.63.

**Why it matters.** The default comparison period is the equal stretch just before, but you can pick any length.

**Pitfalls.**
- A ratio below 1 can simply mean the comparison period was itself inside the slowdown. It is a comparison, not a baseline.
- With no usable comparison there is no ratio. The card says "Cannot compare with the earlier period" (section 7).
- A figure card turns red when its ratio is above 1.5. **[Prototype]** This is a display choice, not an engine rule.

**Differs.** **[Real app]** `P95 ratio = current P95 / reference P95`, computed only when both windows have at least 20 valid samples and the reference quantile is above zero (`audit/analyze.go`). Otherwise the state is `insufficient_data` or `undefined_baseline`, and no ratio is printed.

## 3. Percentiles P50, P95, P99

**Plain meaning.** Sort all request times from fastest to slowest. P50 (the median) is the middle one: half were faster. P95 is the time that 95 out of 100 requests beat: "the slowest 1 in 20 took about this long or longer". P99 is the slowest 1 in 100.

**20-number example** (milliseconds, already sorted): 12, 15, 18, 20, 22, 25, 27, 30, 32, 35, 38, 40, 45, 50, 55, 60, 80, 120, 300, 900.

| Percentile | Rule: take the value at rank `ceil(q x n)` | Rank | Value |
|---|---|---|---|
| P50 | ceil(0.50 x 20) | 10th | 35 ms |
| P95 | ceil(0.95 x 20) | 19th | 300 ms |
| P99 | ceil(0.99 x 20) = 20 | 20th, the maximum | 900 ms (not meaningful) |

The average of these 20 numbers is about 96 ms (sum 1,924 / 20). One 900 ms outlier drags the average but not P50. That is why percentiles are used.

**The method. [Real app]** It is the **nearest-rank method** (no interpolation between neighbours). It exists in two forms:
1. `internal/investigate/stats.go` (`nearestRank`) applies it to the exact sorted durations. This is the guided analysis.
2. `internal/audit/histogram.go` (the Analyze screen and the comparison tables) applies the same rank rule to a **logarithmic histogram**: each bin is 10% wider than the one before (bin edges are powers of 1.1; about 190 bins plus one overflow bin). The answer printed is the middle of the bin that holds that rank, clamped to the smallest and largest value seen. The app labels this: "Latency quantiles are estimated from logarithmic histogram buckets".
   Example: the true 19th value is 300 ms. It falls in the bin 276.8 to 304.5 ms, so the screen shows about 291 ms (a range of roughly plus or minus 5%).
3. The web code does not recompute percentiles. It only words the numbers it receives ("the slowest 1 in 20 took about ... or longer", `web/src/lib/copy/plainSummary.ts`).

**Minimum sample sizes. [Real app]** P95 needs **20** requests, P99 needs **100** (`audit.MinSampleCount = 20`, `audit.P99MinSampleCount = 100`, `investigate` `GroupSizeP95/P99`; a test keeps them equal). Why: with fewer than 20 values the nearest-rank P95 is the maximum, and with fewer than 100 the P99 is the maximum. A "percentile" that is just the maximum is misleading, so it is withheld.

**The prototype's "estimated" P95. [Prototype]** `auditWindow` does not read every request. It has one P95 per minute and combines them: `P95_window = sum(P95_minute x calls_minute) / sum(calls_minute)`, a call-weighted mean of per-minute P95s. Example: minute A has 100 calls with P95 200 ms, minute B has 300 calls with P95 600 ms. Estimate = (100x200 + 300x600) / 400 = **500 ms**.
Why it is only an estimate: the P95 of a whole period is not the average of the P95s of its minutes. If minute B holds the slow tail, the true window P95 depends on how many requests of each minute fall above the cut, which the per-minute figures do not tell. The card says "estimated" and its tooltip says: "The product will compute it from every request's duration."

**Pitfalls.** Never average percentiles in a real product. Never compare a P95 built from 5 requests with one built from 5,000.

## 4. 4xx and 5xx counts and their share

**Plain meaning.** 4xx = "the caller asked wrongly" (client error). 5xx = "something went wrong on the server's side" (server error). Share = errors divided by requests.

**Rule.**
- **[Prototype]** `share = 5xx count / request count`. Printed with one decimal under 10% (4.4%), whole numbers from 10% up.
- **[Real app]** Status names are mapped to numbers (`audit/status.go`). Only 2xx counts as success. A missing, unknown or unfinished status is never success. Error rates divide by *all documents of the group in the window*, not only the timed ones (`errorShare`), and are withheld when the outcome is unknown.

**Example.** 185 server errors in 4,216 requests = 4.4%.

**Why it matters.** 5xx points at a server-side failure. A burst of 4xx often points at a client, a login or a routing problem instead.

**Pitfalls.** The audit log stores the status as a .NET name (`NotFound`), not a number. A route may return 500 for a valid reason in one window and not in the other. Share and count tell different stories: a share can fall while the count rises.

## 5. Completion (requests with no final result)

**Plain meaning.** An audit row is written when a request starts and updated when it ends. If the second write never lands (crash, recycle, or an asynchronous job still running), the row has no final result. That is not a fast request.

**Rule.**
- **[Real app]** `completion = valid / (valid + incomplete + pending)`. Invalid rows are counted apart and are not in the denominator. In the guided analysis, a request with no end time inside a window is counted as incomplete and left out of every timing figure (`Counts.Incomplete`).
- **[Prototype]** `completion = 1 - missing / requests`. A grey note says "N of M requests had no final result in this period. They are not counted in the timing figures."

**Example.** 4,000 valid, 60 incomplete, 20 pending: completion = 4,000 / 4,080 = 98.0%.

**Pitfalls.** Counting unfinished requests as fast ones would hide the worst requests. A drop in completion during a slowdown is itself a clue.

## 6. Slow database calls, database errors, blocking and deadlocks

**Plain meaning.** A *slow database call* is one that took more than 1 second. Blocking = one session waits for a lock held by another. A deadlock = two sessions wait for each other and SQL Server kills one.

**Rule.**
- **[Prototype]** slow share = slow calls / database calls. "Slow" is over 1 s. Blocked-process reports and deadlock reports are summed as "blocking or deadlock reports" (`laneWin`: `slow`, `err`, `mark`).
- **[Real app]** The audit rollups use the same 1 s default for slow API requests (`DefaultRollupSlowMs = 1000`). The shipped SQL importers do **not** produce a per-call duration list or blocked-process reports (see `WHAT-EACH-DATA-SOURCE-BRINGS.md`, SQL Server extended events).

**Example.** 2,005 slow calls out of 5,059 = 39.6%, shown as 40%.

**Why it matters.** Slow calls plus blocking reports on the same minutes is the typical picture of a lock problem (suspect D1).

**Pitfalls.** A blocked-process report only exists if the server was set up to write it. No report may mean "not switched on", not "no blocking". The prototype therefore says "unknown" when the source does not cover the window.

---

## 7. Gaps, coverage % and "Cannot compare with the earlier period"

**Plain meaning.** A *gap* is a stretch with no log data at all. *Coverage* is how much of a period the logs actually cover. A gap is drawn as a hatched, labelled band, never as zeros. A lane whose logs cover the period but hold no events is **not** a gap: it draws honest zero bars.

**Rules.**
- **[Prototype]** `coverage = covered time / period length`, where covered time is the union of the stored log files' time ranges (`coverageFraction`). For the audit lane, coverage = minutes with data / total minutes.
- **[Prototype] Comparison rule** (`referenceState`, `refCmp`; marked in the code as a prototype default, "not in the spec"): the comparison period must hold **at least 30 events** and be **at least 80% covered**. Otherwise no ratio is shown and the card says "Cannot compare with the earlier period" with the reason (no data / too few events / only N% covered).
- **[Real app]** Different, minute-based rule (`facts.go markGaps`): a minute with no calls is a gap only when the comparison period's median is at least 5 calls per minute. A gap of **2 minutes or less** is a warning and the step runs on the minutes that exist. A **longer** gap makes those minutes "Cannot test yet". Missing minutes are never counted as zero.

**Example (prototype).** Comparison period 90% covered but only 12 events: no ratio, because 12 < 30.

**Why it matters.** A ratio built on almost nothing looks precise and is wrong. Saying "cannot compare" is the honest answer.

**Pitfalls.** The 30 and 80% figures are prototype defaults. They are open for the product owner to confirm.

## 8. "Change is meaningful" and "load change"

**Plain meaning.** A move is only called a real change if it is big in relative terms **and** big in absolute terms.

**Rules. [Real app]** (`stats.go compareMs`, `compareRate`)
- Time values (P50, P95, mean): meaningful only if the change is at least **30%** of the reference **and** at least **100 ms**. Both must hold.
- Rates and counts (requests per second, requests in flight, retried calls): "up" or "down" at **30%**. No millisecond half applies to a count.

**Example.** P95 goes 20 ms to 40 ms: +100% but only +20 ms, so **not** meaningful. P95 goes 1,000 ms to 1,200 ms: +200 ms but only +20%, so **not** meaningful. P95 goes 200 ms to 300 ms: +50% and +100 ms, so meaningful.

**Why it matters.** Without both halves, fast routes (tiny numbers) look dramatic and slow routes (large numbers) hide real harm.

**Differs.** **[Prototype]** reuses 30%, 100 ms and 30% in its suspect signals (`SUS_TH`), and adds more of its own (section 15).

## 9. Onset of a slowdown, and its shape

**Plain meaning.** *Onset* (t0) is the first minute where things clearly got slower and stayed slow.

**Rule.**
- **[Real app]** t0 = the first minute whose P95 stays above **2 x the reference P95** (and at least 100 ms above it) for **5 minutes in a row**. A minute needs at least **5 calls** to count. A minute inside a long gap breaks the run (`facts.go findRun`, `hotMinute`, `onset`).
- **[Prototype]** same 2x and 5 minutes, but a minute needs **20 samples** (`onsetMinSamples:20`) and there is no 100 ms floor.

**Example.** Reference P95 = 200 ms, so the line is 400 ms. Minutes 09:12 to 09:17 read 450, 470, 520, 610, 700, 720 ms. The run reaches 5 at 09:16, and t0 is where it began: **09:12**. A single 900 ms minute followed by a 300 ms minute does not count.

**Shape. [Real app]** (`facts.go shape`)
- *Periodic*: at least 3 separate slow bursts whose starts are a whole number of 15 or 60 minutes apart (give or take 2 minutes).
- *Step*: otherwise, if in the 2 minutes before t0 the P95 was at or below 1.5 x the reference (it jumped).
- *Ramp*: otherwise (it was already building).
- *Scope*: one route, a few routes (up to 3), many, or everything (at least 80% of testable routes).
**[Prototype]** does not compute shape or scope for display.

**Pitfalls.** If no start can be dated (too few busy minutes), the engine says "cannot date". It never invents one.

## 10. Time split of one request: A, C, P, O

**Plain meaning.** Where did a request's time go? **[Real app]** (`docs/user/guided-analysis.md` Step 3, `docs/api-audit-log-schema.md`)
- **A** = audit set-up: the audit filter's own work *plus the synchronous Mongo insert* of the audit row (`AuditCreateTime`).
- **C** = the action: the rest of the filter chain plus the action itself (`ApiTime`). It does not include the Mongo insert.
- **P** = payload capture: reading and cleaning the request and response bodies. It happens after the action and is **outside** the recorded time.
- **O** = time outside the application: IIS `time-taken` minus (A + C) minus P. It needs the IIS log.

**Formula.** `recorded time D = A + C` exactly. `O = IIS time-taken - D - P`. Each part's growth is shown as a share of the *extra* time. A part is **dominant** when it holds at least **50%** of the extra time and has risen by a meaningful amount.

**Example.** Typical request before: A 20, C 80 (D = 100), P 10, IIS time-taken 140, so O = 30. During the incident: A 20, C 380, P 10, IIS 440, O = 30. Extra time is 300 ms and C holds all of it, so C is dominant and the work inside the action is where to look first.

**Matching IIS rows to audit rows. [Real app]** By end time within **3 seconds**, same route, one to one. If fewer than **50%** of audit calls find a match, O is not shown. O is also not shown unless the matched pairs sit at one steady clock difference (80% within 1 second of the median). A negative O is set to zero and counted. Without IIS logs, O is "Cannot test yet", never zero.

**Pitfalls.** IIS `time-taken` includes sending the response to the client, so a constant offset is normal. A *change* in O is the signal. Fewer than 80% of calls carrying both A and C weakens the split.

**Differs.** **[Prototype]** does not compute the split. It is real-app only.

## 11. Concurrency by Little's law

**Plain meaning.** Requests in flight = arrival rate x time each one spends. `L = lambda x W`.

**Rule. [Real app]** (`facts_parts.go load`) lambda is a planning rate of **40 requests/second** for the whole API (the audit covers only a slice, so the measured audit rate is shown beside it). W is the mean duration. A pool limit is optional: if you type one, the engine says whether `lambda x W` reached **95%** of it. A *flat top* is the in-flight peak staying within **5%** of one level (at least 5) for **3 minutes**.

| lambda (arrivals) | W (time each) | L (in flight) | Reading |
|---|---|---|---|
| up | up | up | More work arrived |
| flat | up | up | Calls got slower |
| down | up | up or flat | Calls stalled |
| any | any | flat top, new or near the limit | A pool or cap looks full |

**Example.** 40/s x 0.2 s = 8 in flight. After the slowdown, 40/s x 1.0 s = 40 in flight, so five times as many requests are open at once.

**Pitfalls.** A flat top shows that something filled up, not what. A plateau that the comparison period also has is just normal traffic. **[Prototype]** does not compute this.

## 12. Tiers and the red rule ("clearly first by more than 61 seconds")

**Plain meaning.** Each *tier* (arrival rate, audit set-up, the action, outside time, SQL, Mongo, host) gets its own onset minute. The tier that moved clearly first is the **origin**. The others are *victims* or *amplifiers* of it.

**Rule. [Real app]** (`origin.go`, `evaluate.go colour`) Onsets are read by the minute and hosts' clocks may differ by up to 1 second. So two onsets closer than `60 s + 1 s = 61 s` cannot be put in order. A tier is "clearly first" only when every other tier is **61 seconds or more** later.

**Example.** SQL moved at 09:11, the application at 09:14, IIS at 09:15. SQL is 3 minutes ahead, which is more than 61 s, so SQL is clearly first. If SQL were 09:11:00 and the application 09:11:50, they would be "moved together" and no tier is named.

| Situation | Answer |
|---|---|
| One tier clearly first | That tier is the origin |
| Tiers within 61 s of each other | "No clear origin" |
| Only one tier could be dated | Named only if every other tier was read in every slow minute and stayed flat. Otherwise "not enough data" |
| A tier whose own data was not read in every minute | Nothing is named, because that tier may have moved first |

**Why it matters.** Red means "its part of the system clearly moved first". It is a pointer, not a verdict.

**Differs.** **[Prototype]** uses the same 61 s (`bucketMs + clockMs`) for suspect colours, but only dates the audit, IIS and SQL tiers.

---

## 13. Suspects: results, signals, Must and Expect, colours

**Plain meaning.** A *signal* answers a narrow question with **yes**, **no** or **unknown**. A *suspect* names the signals it needs (**Must**) and the signals that normally go with it (**Expect**).

**Result words. [Real app]**
| Result | Rule |
|---|---|
| Consistent | Every Must signal is yes, and no Expect signal is no |
| Partly consistent | Docs wording: the main signal reads yes, but one that normally goes with it reads no or unknown. Code: every Must fits but an Expect reads no, or a Must is unknown for a reason other than missing data |
| Contradicted | A Must signal read **no** on data that was actually read |
| Cannot test yet | A Must signal is unknown because data is missing |

**Colours.** Red = most likely, only when its tier moved clearly first (section 12). Orange = likely, or moved together with others. Green = ruled out by the data we have. Grey = not sure, could not be tested, or does not apply.

**Why grey is never a pass.** "Unknown" never counts as "no". Missing data cannot contradict a suspect. A SQL suspect with no SQL data stays grey: the audit going flat says nothing about the database. Grey means "we could not look".

**Example.** D1 (blocking chain) Must: database call time up = yes, blocked-process reports up = yes. Expect: top wait is a lock wait = yes. Result Consistent. SQL moved first by 3 minutes, so it is red.

**Differs.** **[Prototype]** follows the same order (no on a Must is green, missing data is grey, Expect no is orange, otherwise Consistent). It shows 25 suspects: 16 taken from the real catalog and 9 added from the research report. **[Real app]** the catalog has 44 entries, 42 of them active suspects (M3 and M4 are listed apart as not applicable in production). H2, M2 and H3 are held grey in this release because there is no reliable test yet. In the prototype, S9 can be orange at most (`cap:'orange'`) because counted rows do not date a start.

**Tie-break inside one colour. [Prototype]** earlier separable onset, direct evidence before inferred, saved events before aggregates before sampled gauges, resource-specific before generic, more satisfied Expects, cheaper check, catalog order. No score is used.

**Nothing to explain.** If no slowdown was found, every suspect is grey except data-quality ones, with the note "not ranked".

## 14. Evidence strength and "Where does the evidence point?"

**Plain meaning.** A folded card that asks: did one environment slow down, the whole database server, or a neighbour? It shows **Strong evidence**, **Some evidence** or **Not enough evidence**. **[Prototype]** only, with synthetic server figures; the rules come from the research notes **[Vendor doc]**.

**Rules. [Prototype]** (`attrModel`, `attrVerdict`)
- A database *slowed* when its P95 is at least **2x** its comparison period.
- A shared resource is *saturated* when disk latency is at least **15 ms** or CPU is at least **80%**.
- The unpicked databases are *taking over* when they hold at least **50%** of file I/O and at least **1.5x** their earlier share.

| Order | If | Verdict | Strength |
|---|---|---|---|
| 1 | No picked database has comparable history | "There isn't enough evidence to narrow it down" | Not enough |
| 2 | Slowed + saturated + others taking over | "Another database may be affecting yours" | Some |
| 3 | All picked slowed (2 or more) + saturated | "The whole database server appears affected" | Some |
| 4 | Some slowed + saturated | Title "There isn't enough evidence to narrow it down" (the data fits both a server-wide problem and one database starving the rest) | Some |
| 5 | Some slowed, not saturated | "The problem appears isolated to <environment>" | **Strong** |
| 6 | Saturated, none slowed | "The server is under pressure, your databases do not look slow" | Some |
| 7 | None of the above | "No clear performance problem" | Some |

**The cap.** Strong is lowered to Some when any picked database has only coarse history. Query Store keeps one row per interval (for example 60 minutes). If `interval x 60 s` is longer than the investigation window, it shows what happened in that step, not minute by minute. The same cap applies when a picked database has no history at all.

**File I/O share and the "Other databases" row.** Per database: its I/O stall counters (`sys.dm_io_virtual_file_stats`) as a share of the server's total. Databases you did not pick are summed into one unnamed **Other databases** row. Names are hidden unless you press "Show their names", because a server-wide permission can reveal databases that belong to other people. **[Prototype]** the card warns you to hide them again before sharing an export.

**A switch** "View evidence for" (this environment, all databases on the server, server only) changes only what is shown, never the verdict. With no history kept, the card lists what to switch on.

**Pitfalls.** Server counters count from the last restart, so they only help as differences between two samples. Without a sampler there are no server numbers for a past window, and the card says so.

## 15. Signal thresholds the prototype uses (all are defaults, not laws)

**[Prototype]** `SUS_TH`. Where the real app has the same rule it is marked.

| Signal | Rule |
|---|---|
| Meaningful change | 30% and 100 ms **[Real app: same]** |
| Load up | 30% **[Real app: same]** |
| Onset | 2x for 5 minutes, 20 samples/min (real: 5/min) |
| Scope narrow / wide | 3 or fewer routes / 80% or more **[Real app: same]** |
| Audit share of web requests fell | by 30% or more **[Real app: same]** |
| A listed change leads the start | within 30 minutes before it, not after **[Real app: same]** |
| Disk slow / CPU high | 15 ms / 80% |
| Disk latency rising | up 30% and 5 ms |
| Memory grants waiting | 10 or more (or above 0 in 3 snapshots in a row) |
| Blocked-process reports | 1 or more, and at least 2x the earlier rate |
| Deadlock storm | 5 or more, and at least 2x the earlier rate |
| HTTP.sys burst | 3 in 5 minutes, or 1% of requests |
| SQL attention burst | 3 in 5 minutes, or 2% of calls |
| Login storm | 5 in 5 minutes and at least 3x the earlier rate |
| Mongo spill to disk | 3 operations, or 5% |
| Mongo queue | 5 or more and above earlier (from MongoDB 7.0 only a waiting queue counts) |
| Scheduler pressure | runnable queue on half the schedulers, 5 samples in a row |
| Request-filtering rejections | 3 or more, 30% above the earlier rate |

## 16. "What Unravis sees" tiles

**Plain meaning.** Three one-line readings, one per layer, in plain words. A reading aid: the figures and charts are the evidence.

**Rules. [Prototype]** (`layerRead`)
| Tile | Red text if... |
|---|---|
| Application | P95 at least **1.5x** the comparison ("Slower than usual"), else 5xx share at least **2%** ("Returning errors"). Otherwise "About the same", or grey "Cannot compare" |
| Web server | 5xx share at least 2% and/or P95 at least 1.5x |
| Database | Slow-call share at least **25%** and/or any blocking or deadlock report |

**Pitfalls.** These cut-offs are reading-aid defaults and are not part of the real engine.

## 17. Bars, buckets, the ladder, the 120-bar limit, the UTC axis, boundary raw scans

**Plain meaning.** A *bar* (bucket) is one slice of time on a chart. The screen always says how long: "Each bar = 5 minutes". Too many bars would be unreadable and slow, so there is a limit of **120**.

**[Real app]** (`audit/models.go MaxTimeBuckets = 120`, `audit/query.go`, `web/src/lib/zoom.ts`, `time.ts`)
- Bucket length is 10 seconds to 86,400 seconds (one day).
- The web ladder: 10, 15, 30, 60, 120, 180, 300, 600, 900, 1,800, 3,600, 7,200, 10,800, 21,600, 43,200, 86,400 seconds. It picks the smallest step that keeps both the window and its comparison period at 120 bars or fewer.
- Buckets are aligned to the Unix epoch, so they are the same across reloads (midnight UTC for widths that divide a day).
- A window can be at most 7 days.

**[Prototype]** (`ladderWidth`, `axis`) The Investigation Timeline allows **30 days** and uses its own ladder:
| Window length | Bar length asked for |
|---|---|
| up to 2 hours | 1 minute |
| up to 24 hours | 5, 10 or 15 minutes (smallest that keeps 120 bars or fewer) |
| up to 5 days | 1 hour |
| up to 10 days | 2 hours |
| up to 20 days | 4 hours |
| up to 30 days | 6 hours |
The **axis** is then rounded out to whole UTC hours (start floored, end rounded up) and *never re-anchored*, so the same bar always means the same clock time. The bar length shown is the larger of the ladder length and the smallest standard length (60 s, 5 min, 10 min, 15 min, 1 h, 2 h, 4 h, 6 h, 8 h, 12 h, 1 day) that keeps the rounded axis at 120 bars or fewer. A 30-day window that starts off the hour can therefore become 8-hour bars (`prototype/TRACING-SCREEN-EXPLAINER.md`, line 283).

**Worked example (prototype).** Window 09:00 to 11:30 = 2 h 30 min. Over 2 h, so the ladder asks for 5 minutes (30 bars). Axis: 09:00 to 12:00 = 36 bars, of which 30 are inside the window and 6 are outside. "Each bar = 5 minutes, 36 bars".

**Boundary raw scans. [Prototype]** (modelled on a spec, not on shipped code) The prototype imagines that whole UTC hours come from pre-summed *rollup rows*, while a window edge that is **not** on the hour needs a *raw* query of just that fragment:
| Window edges | Raw scans |
|---|---|
| both on a UTC hour | 0 |
| exactly one off the hour, or both inside the same hour | 1 |
| both off the hour, in different hours | 2 |
The example above (start 09:00, end 11:30) needs 1 raw scan (11:00 to 11:30). If a scan hits a row cap (50,000 rows in the prototype), the affected bars are marked "not exact".
**[Real app]** has hourly and daily rollups (`audit/rollup.go`, up to 120 days) but the boundary-scan model above is not in shipped code.

**Pitfalls.** The last and first bars may be cut off by the window's edge. That is where the period ends, not missing data.

## 18. Where the prototype differs from the real app (summary)

| Item | Real app | Prototype |
|---|---|---|
| P95 | Exact nearest-rank (guided analysis); histogram nearest-rank, about plus or minus 5% (Analyze) | Call-weighted mean of per-minute P95s, "estimated" |
| P99 | Needs 100 requests | Not shown |
| Cannot-compare rule | Reference needs 20 valid samples; gap of more than 2 minutes untestable | 30 events and 80% coverage |
| Onset samples per minute | 5, plus 100 ms floor | 20, no floor |
| Shape, scope, A/C/P/O, Little's law, origin | Computed | Not computed (the red rule is) |
| Max window | 7 days per window | 30 days |
| Bar alignment | Unix-epoch aligned, ladder starts at 10 s | UTC-hour floor-aligned, starts at 1 min |
| SQL calls over 1 s, blocked-process reports | The shipped SQL importers do not produce them (they read `system_health`, the error log, Query Store and counters; see `WHAT-EACH-DATA-SOURCE-BRINGS.md`) | Simulated per-minute |
| Catalog | 44 entries, 42 active suspects | 25 suspects |
| Server counters | Needs a sampler | Simulated sampler (Blackbox, since 19 Oct) |

## 19. Numbers in the demo (all made up)

Window 09:00 to 11:30 on 20 Oct, compared with 06:30 to 09:00 (both 150 minutes, so counts compare directly). Figures are from the source pack `NOTEBOOKLM-SOURCE.md`; the arithmetic below is ours.

| Demo figure | How to read it |
|---|---|
| **4,216 requests**, 1.1 times | About 28 requests a minute (4,216 / 150). 1.1x means the comparison period had roughly 3,800. Volume barely moved, so this is not a surge |
| **522 ms**, 2.4 times | Estimated P95: about 95 in 100 requests finished in around 522 ms. The comparison period was about 217 ms (522 / 2.4). Above the 1.5 mark, so the figure is red and the Application tile says "Slower than usual" |
| **185** application 5xx, 4.4% | 185 / 4,216 = 4.39%, shown 4.4%. At or above the 2% tile mark |
| **210** IIS 5xx, 4.4% of 4,816 | 210 / 4,816 = 4.36%. The prototype models the web server as seeing more requests than the audit log (a real audit log may cover only a slice of the traffic) |
| **1,046 ms**, 4.0 times (IIS) | Estimated web P95. Both the web tile conditions hold (errors and slow), so it reads "Returning errors and slower responses" |
| **2,005 slow calls**, 40%, 14.6 times | 2,005 / 5,059 = 39.6%, rounded to 40% because it is 10% or more. Above the 25% tile mark. 14.6x is a *rate* ratio of slow-call counts, not of share |
| **130 failed calls, 15 reports** | Failed database calls, then blocking or deadlock reports. Any report turns the Database tile red |
| **Suspect D1 red** | Database call time 1,795 ms against 70 ms (25.6x, past 30% and 100 ms), 11 blocking reports against 0, top wait is a lock wait. SQL moved at 09:11, the application at 09:14, IIS at 09:15, so SQL is more than 61 s ahead and the rule makes D1 red |
| **Grey suspects** | Thread pool (.NET counters not collected), audit-write coupling (Mongo profiler off), change history (no list typed in) and shared host (no second workload measured). Grey means "could not test", not "fine" |

Remember for the video: this is a prototype with made-up data. The screen shows suspects, not causes.
