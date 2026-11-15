# Charts and stats in the tracing-paper wireframe: review document

Purpose: for every chart and every stat in the wireframe, say what it means, how it is calculated, where the data comes from, what it depends on, and whether it is worth showing. Written so a second reviewer (human or AI agent) can fact-check each entry against the code.

- Source of truth: `prototype/tracing-paper-PROTOTYPE.html` (branch `prototype/tracing-paper-e2e`). Line numbers below are for that file at commit `0a6a629`.
- Companion docs (prose, not per-item): `prototype/video/HOW-THINGS-ARE-CALCULATED.md`, `prototype/TRACING-SCREEN-EXPLAINER.md`, `prototype/video/WHAT-EACH-DATA-SOURCE-BRINGS.md`.
- Nothing in this document was run. It comes from reading the code and the docs. "Code-verified" means the formula was read in the prototype source, not that it was executed or that it is correct for real data.

---

## 0. How to read this

### 0.1 Fact-check status (one per item)

| Status | Meaning |
|---|---|
| **code-verified** | The formula or rule was read in the prototype source. It does what the entry says *in the prototype*. |
| **doc-only** | Stated in a spec or guide; no matching code in the prototype (it is a real-app behaviour). |
| **vendor-claim** | A threshold or behaviour taken from vendor guidance (Microsoft, MongoDB). Not checked against the vendor here. |
| **inference** | Reasonable, but nobody has confirmed it. The reviewer should challenge it. |

### 0.2 Data origin in the prototype (the key caveat)

All numbers in the wireframe are synthetic. They are deterministic functions of absolute time (`unit()`, `h53`). The demo incident is 09:14-11:30 on 20 Oct 2026. Within that, each figure falls into one of three groups:

| Origin | Meaning | Examples |
|---|---|---|
| **Computed** | Derived by real arithmetic from the synthetic minute series (`opMinute`, `metricMinute`), so changing the window changes the value in a believable way. | Request counts, 4xx/5xx, P95 estimates, SQL slow calls, IIS 5xx, lane buckets, coverage, onset, "sameMoment", "scopeNarrow" |
| **Hard-coded** | A fixed table or constant picked to tell the demo story. Changing the window does not change it. | The `ATTR` table (CPU, disk latency, memory grants, top wait, Mongo queue, lag, file-I/O shares), `ATTR_SAMPLER` text |
| **Fabricated** | A made-up number standing in for something the prototype does not have. | Record timing split (12% / 88% / 5%), `max = p95 x 1.6`, "0 unusable timing" |

Every entry below says which group applies. A reviewer should treat **Hard-coded** and **Fabricated** entries as "the design is shown, the evidence is not".

### 0.3 Wording

"Reference" and "comparison" mean the same thing: the earlier period the incident is measured against. The UI says "comparison period" or the reference label in `REF_LABEL`.

---

## 1. Summary table

Helpful: **Yes** = keep as is; **Yes, with a fix** = keep but change something; **Marginal** = low value or redundant; **Not yet** = depends on data the product does not collect yet.

| # | Item | Where | Origin | Helpful? | Fact-check |
|---|---|---|---|---|---|
| A1 | Requests | Run analysis, Pulse | Computed | Yes | code-verified |
| A2 | Completion | Run analysis, Pulse | Computed | Marginal | code-verified |
| A3 | Observed P95 | Run analysis, Pulse | Computed (estimate) | Yes, with a fix | code-verified |
| A4 | 4xx | Run analysis, Pulse | Computed | Marginal | code-verified |
| A5 | 5xx | Run analysis, Pulse | Computed | Yes | code-verified |
| A6 | Headline sentence | Run analysis, Pulse | Computed | Yes, with a fix | code-verified |
| A7 | Hero chart | Run analysis, Pulse | Computed (+ fabricated max) | Yes | code-verified |
| A8 | Where is it? ranking (4 lenses) | Run analysis, depth 1 | Computed | Yes | code-verified |
| A9 | Evidence tabs (Slowest, History, Failures) | Run analysis | Computed | Yes | code-verified (not fully read) |
| A10 | Record view timing split | Run analysis, record | Fabricated | Not yet | code-verified |
| A11 | Nearby activity | Run analysis, record | Computed from synthetic lane | Not yet | code-verified |
| B1 | Overall figures: 7 cards | Investigation Timeline | Computed | Yes | code-verified |
| B2 | Audit lane panel | Investigation Timeline | Computed | Yes | code-verified |
| B3 | IIS lane panel | Investigation Timeline | Computed | Yes | code-verified |
| B4 | SQL lane panel | Investigation Timeline | Computed | Yes (needs new importers) | code-verified |
| B5 | Time axis, buckets, gaps, clipped slots | Investigation Timeline | Computed | Yes | code-verified |
| B6 | Lane state stickers | Investigation Timeline | Computed | Yes | code-verified |
| B7 | Bucket panel | Investigation Timeline | Computed | Yes | code-verified |
| B8 | Coverage card | Investigation Timeline | Computed | Yes | code-verified |
| B9 | What Unravis sees | Investigation Timeline | Computed | Yes, with a fix | code-verified |
| B10 | Where does the evidence point? (server, per-database, Mongo tables, verdict) | Investigation Timeline | **Mixed: per-database P95 computed, server counters hard-coded** | Yes (design); evidence **Not yet** | code-verified |
| B11 | Suspects ranking, onset, signals | Investigation Timeline | **Mixed** | Yes (design); several signals **Not yet** | code-verified |
| B12 | Databases block | Investigation Timeline | Computed | Marginal | code-verified |
| C | Prototype-versus-real gaps and open issues | - | - | - | see section 5 |

---

## 2. Part A: Run analysis ("Pulse" screen)

Code: `computeAnalysis` (line 1045), `pulse` (1090), `heroChart` (1104), `whereView` (1118), `evidenceView` (1127), `recordView` (1140).

Common to A1-A5: they describe the **audit lane only** (the API's own audit rows), for the selected period against the reference period. Dependency for all: at least one audit source in the case, and an incident window. The comparison needs a reference window; with none, the delta lines disappear.

### A1. Requests

1. **Meaning.** How many API requests the audit recorded in the selected period.
2. **How.** Row count. When the incident and reference periods differ in length, the delta compares per-minute rates: `dn = (n / lengthRatio - refN) / refN`, with " per minute" appended to the label only if the lengths differ by more than 1%.
3. **From where.** Audit rows (MongoDB audit collection in the real app; synthetic `opMinute` series in the prototype).
4. **Dependencies.** Audit source; incident window; reference window for the delta. Audit gaps lower the count, and the figure does not say so (see B8 for the coverage card).
5. **Helpful?** Yes.
6. **Why.** Volume is the first thing to rule in or out: a traffic surge looks different from a slowdown (suspect L1 depends on it). The per-minute comparison stops a long window from looking busier than a short reference.
- **Fact-check.** code-verified (1092-1093).
- **Watch.** The count is raw. If the audit had an outage inside the window, "Requests" understates real traffic with no marker on the tile.

### A2. Completion

1. **Meaning.** The share of requests that have a final result (not left without a final state).
2. **How.** `1 - missing / total`, shown as a percentage; delta is the difference in percentage points against the reference.
3. **From where.** Audit rows with no final status/duration (`c.miss`).
4. **Dependencies.** Audit source. "Missing" is defined by the audit row shape (no final result recorded).
5. **Helpful?** Marginal.
6. **Why.** It matters only when it is low: a request with no result is not in the timing figures, so a drop explains an odd P95. When it is near 100% (the normal case) the tile adds noise. The grey notice under the tiles already says the same thing when `miss > 0`.
- **Fact-check.** code-verified (1094, 1101).
- **Suggestion for the reviewer.** Decide whether to keep the tile or show it only when below some threshold; the notice may be enough.

### A3. Observed P95

1. **Meaning.** The time within which about 95% of requests finished, measured as audit creation time plus API time ("Observed API time").
2. **How.** Prototype: a **call-weighted mean of per-minute P95 values** (`o.p95 = o.w / o.n`), labelled "estimated". Real app: exact nearest-rank percentile, or a log-histogram at about +/-5% error. The delta is `p95 / refP95` shown as "N.Nx the reference"; the tile turns red above 1.5x.
3. **From where.** Audit duration fields.
4. **Dependencies.** Audit source with timing; reference window for the delta; enough samples (completed requests only).
5. **Helpful?** Yes, with a fix.
6. **Why.** P95 captures the slow tail that averages hide, and the ratio to the reference is the clearest "did it get worse" number.
- **Fact-check.** code-verified for the prototype formula; **doc-only** for the real app's nearest-rank and histogram method.
- **Watch.** A weighted mean of per-minute P95s is not a percentile of the combined data. It can sit well below the true P95 when a few minutes are very slow. The "estimated" label is the only protection in the prototype; the real app must not ship this method. Also: the 1.5x red line is a prototype default, not a vendor number.

### A4. 4xx

1. **Meaning.** Requests that returned a client-side error status (400-499), as a count and a share of requests.
2. **How.** Count of audit rows with status 4xx; share is `e4 / n`. **No comparison shown.**
3. **From where.** Audit status code.
4. **Dependencies.** Audit source.
5. **Helpful?** Marginal.
6. **Why.** A jump in 4xx points at clients (bad tokens, validation, a changed client), not at the server, which is useful context. But there is no delta, no red state, and the Timeline's "Overall figures" omits 4xx altogether, so the two screens treat it inconsistently.
- **Fact-check.** code-verified (1096).

### A5. 5xx

1. **Meaning.** Requests that returned a server-side error (500-599), count and share.
2. **How.** Count; share is `e5 / n`. Delta is the **share** ratio: `(e5/n) / max(refE5/refN, 1e-9)`, shown as "N.Nx the reference". Red when the share is more than twice the reference share.
3. **From where.** Audit status code.
4. **Dependencies.** Audit source; reference window for the ratio.
5. **Helpful?** Yes.
6. **Why.** The clearest failure signal for the application, and the ratio is independent of window length.
- **Fact-check.** code-verified (1096).
- **Inconsistency to review.** Pulse turns 5xx red at **2x the share**; the Timeline's 5xx card turns red at **1.5x the per-minute rate** (B1). Same idea, two rules.

### A6. Headline sentence

1. **Meaning.** One plain sentence under the tiles: either "P95 is about N times what it was..." with the biggest-rise operation, or "Nothing changed much...".
2. **How.** If a comparison exists and the **top operation by impact** has ratio above 1.5, show the P95 sentence; else show "Nothing changed much". `N = round(c.p95 / rc.p95)` using the **overall** P95 ratio.
3. **From where.** `computeAnalysis` rows (A8) and the Pulse figures.
4. **Dependencies.** Reference window; at least one operation row.
5. **Helpful?** Yes, with a fix.
6. **Why.** Tells a non-expert what to look at next and points to "Where is it?".
- **Fact-check.** code-verified (1097-1098).
- **Bug-shaped issue.** The condition tests the *top operation's* ratio, but the sentence quotes the *overall* ratio. If the overall ratio is 1.2 and one operation is 3x, the sentence reads "about 1 times". Rounding also turns 1.5 into "2 times".

### A7. Hero chart ("Requests over time")

1. **Meaning.** How traffic and slow-tail latency moved across the period. Bars show requests per interval, the line shows P95 per interval, the dashed line shows the reference P95.
2. **How.** One bar per bucket (`r.buckets`), left axis = request count (max of buckets), right axis = P95 ms (max of bucket P95 and reference P95, times 1.15). Reference line is a single level, not a time series.
3. **From where.** Audit buckets built by `computeAnalysis`.
4. **Dependencies.** Audit source; bucket size (`bucketForWindows`). An empty period shows "No requests in this period".
5. **Helpful?** Yes.
6. **Why.** Shows when the slowdown started and whether it tracked volume. A rising P95 on flat bars means a slowdown, not a surge.
- **Fact-check.** code-verified (1104-1112).
- **Watch.** `auditBuckets` sets `max = max(s.max, p95 x 1.6)`, a fabricated maximum (not shown on this chart, but it feeds other views). The dashed reference line is a single value for the whole reference period, so it hides any trend inside the reference.

### A8. Where is it? ranking

1. **Meaning.** A table of operations (routes) ranked by one of four lenses, to find which operation is responsible.
2. **How.** Lenses: **Slowest (P95)**, **Most requests**, **Most 5xx**, **Biggest change**. Biggest change sorts by `impact = (p95 - refP95) x n`, i.e. extra milliseconds multiplied by how many calls.
3. **From where.** Audit rows grouped by operation.
4. **Dependencies.** Audit source with operation names; reference window for "Biggest change" and the ratio column.
5. **Helpful?** Yes.
6. **Why.** Turns "something is slow" into "this route". The `impact` lens avoids ranking a rarely called route above a busy one.
- **Fact-check.** code-verified (1118, 1045 for impact).
- **Watch.** `impact` mixes a latency difference with call count, so a high-volume route with a modest rise can outrank a low-volume route with a severe one. State this in the UI or let the reviewer decide if it is intended.

### A9. Evidence tabs (Slowest, History, Failures)

1. **Meaning.** Concrete requests behind a selected operation: the slowest ones, the history of the operation, and the failures.
2. **How.** Lists derived from the audit rows (`evidenceView`, line 1127).
3. **From where.** Audit rows.
4. **Dependencies.** An operation selected in A8.
5. **Helpful?** Yes. It is the bridge from a number to an actual request.
- **Fact-check.** code-verified at the header level only. **Not read line by line in this pass.**

### A10. Record view: timing split

1. **Meaning.** How one request's total time breaks down into `AuditCreateTime`, `ApiTime` and `PayloadRecordTime`.
2. **How (prototype).** Fixed fractions: 12% and 88% of the total, and 5% for the payload. These are **invented**.
3. **From where (real).** Three separate fields on the audit record.
4. **Dependencies.** Real audit rows that carry the three timing fields.
5. **Helpful?** Not yet. The idea is useful (it separates audit overhead from application work, which feeds suspects S5 and S8), but the prototype numbers prove nothing.
- **Fact-check.** code-verified that the split is fabricated (1140+).

### A11. Nearby activity

1. **Meaning.** What else happened in the minute around a selected record, across sources.
2. **How.** Counts from `laneMinute`, which is synthetic.
3. **From where.** Real app: the same stored lane events. In the prototype the real component `CallTrace.svelte` is not mounted, so this is unavailable.
4. **Dependencies.** Stored SQL/IIS events for the minute.
5. **Helpful?** Not yet. Useful once real lanes exist.
- **Fact-check.** code-verified that the data is synthetic; **doc-only** for the real behaviour.

---

## 3. Part B: Investigation Timeline

Code: `doTrace` (1188), `laneSVG` (1231), `lanePanels` (1272), `traceStats` (1316), `bucketPanel` (1337), `coverageCard` (1424), `layerRead` (1781), `attrCard` (1757).

The screen asks one question per lane (`LANE_ASK`, line 1265):

- Audit: "Was the application slow or failing?"
- IIS: "Were web requests slow or failing before reaching the app?"
- SQL: "Were database calls slow, blocked or failing?"

### B0. The comparison rule (applies to every delta on this screen)

A comparison is allowed only if the reference period has at least **80% coverage** and at least **30 events** (`SUFFICIENT_REF`). Otherwise the rate is `null`, the card says "Cannot compare with the earlier period", and the reason is one of: no data in the comparison period, only X% covered, or too few events.

The rate is `(incidentTotal / incidentMinutes) / (refTotal / refMinutes)`. Red above **1.5**.

- **Why it matters.** It stops the screen from showing "5x" when the baseline is one minute of data. 
- **Fact-check.** code-verified (`refCmp` in `doTrace`). The 0.8 and 30 limits are prototype defaults, not vendor numbers (inference that they are sensible).

### B1. Overall figures: seven cards (`traceStats`, line 1316)

Each card shows a plain title, a big value, a one-line meaning, a delta against the reference and the technical term in brackets. A source with no data shows a "none" card.

| Card | Meaning | How | From | Red when | Helpful? |
|---|---|---|---|---|---|
| **Requests to the application** (API requests) | Requests the app received | audit count; delta = per-minute ratio `pm()` | Audit | never | Yes: volume context |
| **Slow-request time, P95, estimated** (API P95) | Time within which ~95% of requests finished | weighted mean of minute P95s (estimate); delta = ratio of P95s | Audit | ratio > 1.5 | Yes, with the A3 caveat |
| **Application server errors, 5xx** (API 5xx) | App-side failures, with a % of requests | count; delta = per-minute ratio | Audit | rate > 1.5 | Yes |
| **Web server errors, IIS 5xx** | Failures at the web server, % of web requests | count; delta = per-minute ratio | IIS W3C logs | rate > 1.5 | Yes: separates "failed before the app" from "failed in the app" |
| **Slow web requests, IIS P95, estimated** | Web-server `time-taken` tail | weighted P95 estimate; ratio of P95s | IIS W3C logs | ratio > 1.5 | Yes |
| **Slow database calls, over 1 s** (SQL slow calls) | Calls over one second, with % of calls | count of calls with duration > 1000 ms; per-minute ratio | SQL Extended Events | rate > 1.5 | Yes, but see below |
| **Database errors and blocking** | Failed calls, then blocked-process / deadlock reports, shown as `err . mark` | counts; **no delta** | SQL XE (errors, blocked-process, deadlock graph) | `mark > 0` | Yes, but see below |

- **Dependencies.** Each card needs its lane stored for both windows; the delta needs B0 to pass. If a lane is missing, its cards become "none".
- **Fact-check.** code-verified (1316-1336). P95 tooltip states "Estimated from per-minute figures. The product will compute it from every request's duration."
- **Watch (SQL cards).** The shipped SQL importers (real app) **do not yet produce per-call durations or blocked-process reports**, so "slow calls over 1 s" and "blocking" cannot be produced by them today. The prototype shows the design; the data path is a gap (see section 5).
- **Watch (red rules).** `mark > 0` turns the last card red even for one blocked report. That is intentionally sensitive but differs from the suspect rules (blocking needs a rise; deadlocks need 5 and 2x).
- **Watch (colours).** Cards use `> 1.5`; "What Unravis sees" (B9) uses `>= 1.5`.

### B2. Audit lane panel

1. **Meaning.** Requests per bucket (bars, left axis) and P95 per bucket (line, right axis in ms), with the reference P95 as a dashed level.
2. **How.** `laneBuckets` builds counts per axis slot from `opMinute`; `auditSlots` (1185) accumulates `n` and `w = p95 x n` per slot, then `p95 = w / n`. One 190 px panel.
3. **From where.** Audit.
4. **Dependencies.** Axis (B5), audit coverage (B8).
5. **Helpful?** Yes.
6. **Why.** The anchor lane: it shows what users experienced. The other two lanes are read against it.
- **Fact-check.** code-verified.

### B3. IIS lane panel

1. **Meaning.** Web-server view: stacked bars by status class (OK, 4xx, 5xx), a P95 `time-taken` line, and a strip of HTTP.sys error rows.
2. **How.** `metricMinute('iis', m)` per minute, aggregated per slot. IIS lags the audit by 1 minute in the synthetic data.
3. **From where.** IIS W3C logs; HTTPERR logs for the strip.
4. **Dependencies.** IIS logs stored for the window; HTTPERR files for the strip (suspects S1, S10-S12 need them).
5. **Helpful?** Yes.
6. **Why.** Separates "slow inside the app" from "slow before the app" (time outside the application = IIS time minus audit duration).
- **Fact-check.** code-verified for the prototype; the IIS importer exists in the real app (**doc-only** here).

### B4. SQL lane panel

1. **Meaning.** Database calls as stacked "slow" and "error" bars, a P95 call-duration line, and a strip for blocked-process and deadlock reports.
2. **How.** `metricMinute('sql', m)` per minute. The SQL lane **leads** the audit by 3 minutes (`LANE_LEAD`) in the demo, meaning the database slows first.
3. **From where.** SQL Server Extended Events (completed calls, blocked_process_report, xml_deadlock_report).
4. **Dependencies.** Stored SQL events with per-call duration.
5. **Helpful?** Yes, once the importers can provide it.
6. **Why.** Shows whether the database moved first and whether blocking lines up with the slowdown. It is the lane that most directly names a cause (D1, D2).
- **Fact-check.** code-verified for the prototype. **Gap:** shipped importers lack per-call durations and blocked-process reports (**doc-only**, from the source-by-source guide).

### B5. Time axis, buckets, gaps, clipped slots

1. **Meaning.** The shared horizontal scale of all three lanes. Hatching marks time with no stored data. Narrow end slots are clipped by the window edge.
2. **How.** Axis is aligned to the UTC hour floor and never re-anchored; bucket width comes from the `RUNGS` ladder with at most **120 bars**. Boundary slots may need 0, 1 or 2 raw scans, capped at **50,000 rows** (`MaxEdgeScanRows`). `gapIv` computes the hatched intervals; when gaps cover more than **85%** of the window the lane switches to a compact form. Y-axes use `niceTop` / `stripTop`.
3. **From where.** Computed from the window and each lane's stored coverage.
4. **Dependencies.** Window; per-lane import intervals (coverage ledger).
5. **Helpful?** Yes.
6. **Why.** The shared axis is what lets a reader line up "SQL slowed, then API slowed". Gap hatching prevents the common error of reading empty time as "no problem".
- **Fact-check.** code-verified (1254-1262). The 120-bar and 50,000-row limits and the 85% rule are design defaults.

### B6. Lane state stickers

1. **Meaning.** A label per lane: *events*, *no data*, *none seen*, *out of range*, *clock unchecked*, *incomplete*.
2. **How.** Derived from coverage and event counts for the window. "None seen" is allowed only when the lane covers the whole window; otherwise "no data".
3. **From where.** Lane coverage and counts.
4. **Dependencies.** Coverage ledger (B8); a clock check for the "clock unchecked" state.
5. **Helpful?** Yes. It encodes the rule "absence is evidence only when the source covers the window".
- **Fact-check.** code-verified.

### B7. Bucket panel

1. **Meaning.** The counts of all three lanes for one bar, with its time range and whether it is clipped.
2. **How.** Hover or click a bar, or use the arrow keys. `counts` joins each lane's bucket value. Offers "Open Run analysis for this bucket", which uses the bar as the investigation window and moves the comparison to just before it if needed.
3. **From where.** Lane buckets.
4. **Helpful?** Yes: the hand-off from overview to detail.
- **Fact-check.** code-verified (1337-1339).

### B8. Coverage card ("What is stored for the investigation window")

1. **Meaning.** Ribbons showing which time ranges each SQL or IIS source has stored, and the percentage of the window covered. A second block shows audit coverage, including the 02:00-04:00 outage on 19 Oct.
2. **How.** `coverageFraction` and `gapsOf` over the import intervals. Tagged "MVP-2 coverage ledger".
3. **From where.** The import ledger (what was imported, from/to).
4. **Dependencies.** The ledger must record every import interval accurately.
5. **Helpful?** Yes. It is the main defence against reading missing data as healthy.
- **Fact-check.** code-verified for the arithmetic; the ledger itself is **doc-only** (MVP-2).

### B9. What Unravis sees

1. **Meaning.** Three plain-English rows (Application, Web server, Database), each with a status and one detail line.
2. **How (`layerRead`, line 1781).**
   - Application: ratio >= **1.5** -> "Slower than usual" (red); else 5xx share >= **2%** -> "Returning errors" (red); else ratio exists -> "About the same"; else grey "Cannot compare".
   - Web server: 5xx share >= 2% or ratio >= 1.5 -> red (combined wording); else "No clear problem".
   - Database: slow-call share >= **25%** or any blocked/deadlock report -> red; else "No clear problem".
3. **From where.** The same lane stats as B1.
4. **Dependencies.** B0 for the ratio; lane stored.
5. **Helpful?** Yes, with a fix.
6. **Why.** A first-read summary for non-experts. It is explicitly a reading aid: "the figures and charts are the evidence".
- **Fact-check.** code-verified (1781-1795).
- **Issues.** (1) The 2% and 25% limits are hard-coded in `layerRead` and are not in `SUS_TH` or the "Rules used" card, so they are invisible to the user. (2) For the Application, slowness is tested first, so an app that is both slow and erroring only says "slower". (3) `>=` here against `>` on the cards.

### B10. Where does the evidence point? (attribution card)

Question: is the slowdown caused by this environment, the server, a noisy neighbour, or is there not enough evidence? Code: `attrModel` (1446), `attrVerdict` (1462), `attrCard` (1757), `ATTR` (1433).

**B10a. Server table** ("The server as a whole", instance-wide).

| Signal | Meaning | How | From | Origin |
|---|---|---|---|---|
| Disk latency | How long the disk takes per I/O | delta of `io_stall` over delta of I/Os between two samples | `sys.dm_io_virtual_file_stats` | **Hard-coded** (`ATTR`) |
| CPU | CPU use of the server | counter | Performance counters | **Hard-coded** |
| Memory grants waiting | Queries waiting for memory | count | `sys.dm_exec_query_memory_grants` | **Hard-coded** |
| Top wait | The wait type with most time | difference between two samples | `sys.dm_os_wait_stats` | **Hard-coded** |

- **Thresholds.** Disk >= 15 ms is a bottleneck (vendor-claim: Microsoft says 10-15 ms; the prototype uses 15). CPU >= 80% is saturated (vendor-claim: Managed Instance guidance, applied to every edition). Memory grants >= 10 flagged (prototype default).
- **Dependencies.** Server samples. Wait and I/O counters count from the last restart, so only the difference between two samples means anything. Without samples the table prints "no samples were kept" and draws nothing.
- **Helpful?** Yes in design: this is the only way to tell "our database" from "the machine". **Not yet** in practice: the **Blackbox sampler is not built** and the Blackbox drawer is a stub with two fake hosts.
- **Fact-check.** code-verified that the values are a fixed table in four scenarios (`env`, `server`, `noisy`, `blind`); vendor-claim for the thresholds.

**B10b. By-database table.**

| Column | Meaning | How | From | Origin |
|---|---|---|---|---|
| P95 incident / comparison / change | Did this database slow? | If SQL events are stored and the comparison passes B0: the SQL lane's own P95 (`src:'events'`). Otherwise Query Store history (`qs`, `qs-mixed`). Otherwise "cannot compare". | SQL lane; Query Store | **Computed** |
| Share of file I/O (comparison in brackets) | This database's part of all disk I/O | per-database file stalls over the server total | `sys.dm_io_virtual_file_stats` | **Hard-coded** |
| History chip | How trustworthy the history is | Query Store on/off, interval size | database config | Computed |

- **Rule.** "Slowed" = ratio >= 2 (prototype default).
- **Dependencies.** Query Store on, and its interval small enough: if `interval x MIN > span` (history is coarser than the window) evidence is capped at "some".
- **Helpful?** Yes: per-database ratios are the only evidence that separates databases on a shared server. "Other databases on this server" is counted, not read, and their names are hidden unless the user asks.
- **Fact-check.** code-verified; vendor-claim for "Query Store keeps data in fixed intervals".

**B10c. MongoDB table.**

- Server: queued operations and replication lag. Per database: slow operations per hour (incident vs comparison).
- **How / From.** `serverStatus` queue counters; the Mongo profiler for slow operations.
- **Rules.** Persistent queueing means overload; a ticket count of 0 alone does not prove health on MongoDB 7.0+ (vendor-claim). The 8.0 slow threshold ignores lock-wait time (vendor-claim).
- **Origin.** **Hard-coded** (`ATTR`) for queue and lag; `unit()` noise for slow operations.
- **Helpful?** Yes in design; the product cannot read MongoDB `top` and the server's diagnostic capture is a file on disk, not readable over a connection (as the card itself states).
- **Fact-check.** code-verified for the table; vendor-claim for the version notes.

**B10d. Verdict (`attrVerdict`).** One of five conclusions, evaluated in this order:

1. No usable database -> `blind`.
2. Database affected + server saturated + other databases up -> `noisy`.
3. Affected + saturated + every picked database affected + at least two picked -> `server`.
4. Affected + saturated otherwise -> `blind` (ambiguous).
5. Affected only -> `env` (strong).
6. Saturated only -> `server` with `pressure: true`.
7. Otherwise -> `quiet`.

- "Affected" = ratio >= 2. "Saturated" = disk >= 15 ms or CPU >= 80%. "Noisy" = other databases' file-I/O share >= 50% and >= 1.5x the earlier share.
- **Strength.** "Strong" is capped to "some" when Query Store history is coarse or any picked database has no usable history.
- **Helpful?** Yes, if it stays honest about strength. It does not say *why* a database is slow, only where to look.
- **Fact-check.** code-verified (1462-1477). All thresholds are prototype defaults except the vendor-sourced disk and CPU values.
- **Issue.** In the prototype, saturation and noise come from the hard-coded `ATTR` table, so the verdict is a function of which demo scenario is picked, not of the window.

### B11. Suspects

Code: `SUS_CAT` (1502, 25 entries), `SUS_SIG` (1488), `SUS_TH` (1487), `evaluateSuspects` (1602), `susCompare` (1701), `susOnset` (1596).

**What it is.** A ranked list of possible causes. Each suspect has *Must* signals (all needed) and *Expect* signals (usually present). Each signal reads yes, no or unknown. Result: **Consistent**, **Partly consistent**, **Contradicted** or **Cannot test yet**.

**Colour rule.** Red only for the **earliest tier** among Consistent suspects, when it is clearly first and no tier is unread. Onsets less than 60 s plus a 1 s clock tolerance apart count as the same moment. Otherwise orange; green for Contradicted; grey for unknown. If nothing slowed ("explain" false) every suspect is grey and "not ranked". Suspects with `cap:'orange'` (S9) can never be red.

**Tie-break (`susCompare`).** Earlier onset group, then direct evidence, then event over aggregate over gauge, then resource-specific, then more Expect signals, then cheaper check, then catalogue order. The rail shows the top 5 plus every red one; Q1 and Q2 are pinned while not green.

**Onset (`susOnset`).** The first minute whose P95 stays above 2x the reference P95 for 5 minutes in a row, with at least 20 samples per minute (the real app uses 5). Only dated when window and reference are 3 days or less.

- **Helpful?** Yes. The "earliest tier" idea is the most distinctive thing in the product: it uses *who moved first* (SQL 3 minutes before the audit) as evidence, and it refuses to name a cause when a tier is unread. It also gives "what to check" and "what would overturn it" per suspect.
- **Fact-check.** code-verified for the engine. The theory behind "earliest tier is the cause" is **inference**: it holds for a clean cascade and can mislead when two things start together or a tier is sampled coarsely. The reviewer should challenge it.
- **Issue (confirmed by grep).** `SUS_PIN = ['Q1','Q2']` (line 1485) pins Q1, but no Q1 entry exists in `SUS_CAT`. The clock-check data-quality item is pinned in name only.

**B11a. Signals: where each comes from.**

| Origin | Signals |
|---|---|
| **Computed from the synthetic timeline** | `auditUp`, `iisUp`, `sqlUp`, `arrivalsUp`, `scopeNarrow`, `scopeAll`, `p50Flat`, `outsideLarge`, `httpSys`, `blocking`, `deadlocks`, `allSlow`, `sameMoment`, `auditDip`, `changesLead` |
| **Hard-coded (`ATTR`)** | `lockWait`, `memGrant`, `diskSlow`, `cpuHigh`, `cpuUp`, `runQueue`, `otherShare`, `mongoQueue`, `mongoSlow`, `insertUp`, `spill`, `mongoOpUp`, `connRejected` |
| **Synthetic noise, switchable** (`sevSignals`, `unit()`; `RT.sev[id]` "What-if" turns evidence on) | HTTPERR (`queueBurst`, `appPoolFail`, `recvTimeout`, `drain`), `filterReject`, `payloadUp`, `attention`, `loginStorm` |
| **Always unknown** | `dotnetQueue`, `sharedHost` |

So of the 25 suspects, those whose *Must* signals are all in the first row can be genuinely re-evaluated when the window changes: **Q2, L1, L2, C1** and, for the SQL ones, only through `sqlUp` plus `blocking`/`deadlocks` (**D1, D2**). Every other suspect's verdict is driven by hard-coded or switchable values.

**B11b. The 25 suspects.** Evidence kind: `event` = persisted event, `agg` = aggregate, `gauge` = sampled counter.

| ID | Suspect | Tier | Must signals | Needs (data) | Evidence kind | Prototype signal source |
|---|---|---|---|---|---|---|
| Q2 | Audit coverage gap or survivorship | - | `auditDip` | IIS logs | agg | Computed |
| L1 | Traffic surge | - | `arrivalsUp` | audit | agg | Computed |
| L2 | Mix shift: one route or client dominates | audit | `auditUp`, `scopeNarrow` | audit | agg | Computed |
| S1 | HTTP.sys or IIS queue backup | iis | `queueBurst` | HTTPERR logs | event | Switchable |
| S2 | .NET thread-pool starvation | iis / audit | `dotnetQueue` | .NET counters | agg | **Always unknown** |
| S5 | Audit-write coupling | audit | `auditUp`, `mongoSlow`, `insertUp` | Mongo profiler on | agg | Hard-coded |
| S8 | Payload capture or response-body slowdown | audit | `payloadUp` | audit with PayloadRecordTime | agg | Switchable |
| S9 | IIS request-filtering rejection (never red) | iis | `filterReject` | IIS logs with `sc-substatus` | event | Switchable |
| S10 | Application pool unavailable or worker failure | iis | `appPoolFail` | HTTPERR logs | event | Switchable |
| S11 | HTTP.sys header or body receive timeout | iis | `recvTimeout` | HTTPERR logs | event | Switchable |
| S12 | Slow client or proxy: response drain | iis | `drain` | HTTPERR + IIS logs | event | Switchable |
| D1 | Blocking chain | sql | `sqlUp`, `blocking` | SQL events (blocked-process report) | event | Computed |
| D2 | Deadlock storm and retries | sql | `sqlUp`, `deadlocks` | SQL events (deadlock graph) | event | Computed |
| D6 | Memory pressure or grant waits | sql | `sqlUp`, `memGrant` | server samples | gauge | Hard-coded |
| D9 | Disk IO latency | sql | `sqlUp`, `diskSlow` | server samples | gauge | Hard-coded |
| D10 | SQL scheduler pressure | sql | `sqlUp`, `runQueue` | server samples | gauge | Hard-coded |
| D16 | SQL command timeout or cancellation burst | sql | `attention` | SQL XE with attention events | event | Switchable |
| D17 | SQL login or authentication storm | sql | `loginStorm`, `sqlUp` | SQL XE with login events | event | Switchable |
| M5 | Ticket or lock queue | mongo | `auditUp`, `mongoQueue` | Mongo status / slow lines | gauge | Hard-coded |
| M10 | Shared mongod: another tenant or maintenance | mongo | `auditUp`, `otherShare` | Mongo status / slow lines | gauge | Hard-coded |
| M11 | MongoDB spill to disk | mongo | `spill`, `mongoOpUp` | Mongo profiler on (`usedDisk`) | event | Hard-coded |
| M12 | MongoDB incoming-connection rejection | mongo | `connRejected` | MongoDB 6.3+ with `serverStatus` sampled | gauge | Hard-coded |
| H1 | Host CPU contention | host | `cpuHigh` (+ `cpuUp`) | host counters | gauge | Hard-coded |
| H6 | Shared effect across tiers (VM, storage, network) | host | `sharedHost` | host counters | gauge | **Always unknown** |
| C1 | Deploy, configuration or infrastructure change | - | `changesLead` | typed-in change list | agg | Computed |

- **Helpful?** Yes as a catalogue: each entry has a cheapest check and a falsifier ("overturn"). Every suspect with a gauge source is **Not yet** real until the Blackbox sampler exists.
- **Fact-check.** code-verified for the table contents; the real app's catalogue is larger (44 entries, 42 active) so this is a subset.

**B11c. Thresholds (`SUS_TH`).** All prototype defaults unless marked.

| Key | Value | Used for |
|---|---|---|
| `chgPct` / `chgMs` | 30% and 100 ms | "meaningfully up" for P95 signals |
| `loadPct` | 30% | `arrivalsUp` |
| `onsetFactor` / `onsetRun` / `onsetMinSamples` | 2x / 5 minutes / 20 samples | onset dating |
| `scopeFewMax` / `scopeAllShare` | 3 / 80% | `scopeNarrow` / `scopeAll` |
| `auditDipShare` | 30% | `auditDip` |
| `changeLeadMin` | 30 min | `changesLead` |
| `diskMs` / `cpuPct` | 15 ms / 80% | vendor-claim |
| `memGrants` | 10 | `memGrant` |
| `mongoQueue` | 5 | `mongoQueue` |
| `otherShare` / `otherRise` | 50% / 1.5x | noisy neighbour |
| `blockMin` / `deadlockMin` | 1 / 5 and 2x | `blocking` / `deadlocks` |
| `burst5` / `burstShare` | 3 in 5 minutes / 1% | burst signals |
| `attnShare` | 2% | `attention` |
| `loginBurst` / `loginRise` | 5 / 3x | `loginStorm` |
| `filterMin` / `filterRise` | 3 / 1.3x | `filterReject` |
| `spillMin` / `spillShare` | 3 / 5% | `spill` |
| `queueTicketsFrom` / `rejectedFrom` / `mongoVer` | 7.0 / 6.3 / 7.0 | MongoDB version gates (vendor-claim) |
| `scanMaxDays` | 3 | onset only dated for short windows |

### B12. Databases block

1. **Meaning.** Lists every picked database per connection, its environment and a "history" chip (Query Store on/off and interval; Mongo profiler state).
2. **How.** Straight from case configuration (`dbBlock`, line 1803).
3. **Helpful?** Marginal as a "stat"; it is context for B10 and says why a database has "no usable history".
- **Fact-check.** code-verified.

---

## 4. Dependency matrix (what each item needs)

| Data | Needed by |
|---|---|
| Audit rows (count, status, duration) | A1-A9, B1 (3 cards), B2, B9 (Application), onset, suspects L1, L2, Q2, S5, S8 |
| Audit timing fields (`PayloadRecordTime`, `AuditCreateTime`) | A10, S8 |
| IIS W3C logs | B1 (2 cards), B3, B9 (Web server), Q2, S2, S9, S12 |
| HTTPERR logs | B3 strip, S1, S10, S11, S12 |
| SQL Extended Events (completed calls, blocked process, deadlock, attention, login) | B1 (2 cards), B4, B9 (Database), D1, D2, D16, D17 |
| Query Store | B10b, evidence strength |
| Server samples (Blackbox sampler) | B10a, D6, D9, D10, H1, H6, M10 (server side) |
| MongoDB `serverStatus` and profiler | B10c, S5, M5, M10, M11, M12 |
| Typed-in change list | C1 |
| Coverage ledger (import intervals) | B0, B5, B6, B8 and every "no data / none seen" claim |
| Reference window with >= 80% coverage and >= 30 events | every delta and every ratio |

---

## 5. Prototype versus real app, and open issues for the reviewer

### 5.1 What the prototype shows that the real app cannot produce yet

1. **SQL per-call durations and blocked-process reports.** Shipped SQL importers lack them (B1 SQL cards, B4, D1, D2).
2. **Server counters.** The Blackbox sampler is not built; the drawer is a stub. All B10a values and the gauge suspects rest on a hard-coded table.
3. **MongoDB `top`.** Not read.
4. **Call trace.** `CallTrace.svelte` is not mounted, so "Nearby activity" has no real data (A11).
5. **Real-app-only features** (shape, scope, A/C/P/O split, Little's law, origin engine) are not in the wireframe.
6. **Catalogue size.** 25 here against 44 (42 active) in the real app.

### 5.2 Fabrications and weak spots inside the prototype

1. `auditBuckets` `max = max(s.max, p95 x 1.6)`.
2. Record timing split 12% / 88% / 5%.
3. "Quality of the data" shows "0 unusable timing, 0 unclassified result" as a constant.
4. Completion tile uses `1 - miss / n`, so it ignores why a request has no result.
5. `ATTR_SAMPLER` says "Blackbox sampler (running in this demo since 19 Oct)", implying a running sampler.
6. "Nearby activity events in that minute" comes from synthetic `laneMinute`.

### 5.3 Inconsistencies to resolve

1. **5xx red rule.** Pulse: share > 2x reference. Timeline card: per-minute rate > 1.5x. (A5, B1)
2. **Red boundary.** Cards `> 1.5`; "What Unravis sees" `>= 1.5`. (B1, B9)
3. **Hidden thresholds.** 2% error share and 25% slow-call share in `layerRead` are not listed in the rules card. (B9)
4. **Headline mismatch.** Tests the top operation's ratio, prints the overall ratio, can say "about 1 times". (A6)
5. **4xx.** Shown on Pulse without a delta; absent from the Timeline. (A4)
6. **Blocking sensitivity.** Card red at one report; suspect `blocking` has its own rule. (B1, B11)
7. **Q1.** Pin rule names Q1; no Q1 in `SUS_CAT` (confirmed). (B11)
8. **P95 method.** Weighted mean of minute P95s is not a percentile; fine as a labelled estimate, wrong as a product method. (A3)

### 5.4 Questions the reviewing agent should answer

1. For each item marked **Marginal**, keep, hide behind a threshold, or remove?
2. Is "earliest tier among Consistent suspects" a defensible way to colour red? What failure cases (simultaneous onset, coarse sampling, a late-reporting source) does the 60 s + 1 s tolerance miss?
3. Are 80% coverage and 30 events the right gate for a comparison, or should the gate scale with window length?
4. Which thresholds should be user-adjustable in the product, and which should be fixed? (The card says "defaults, not universal laws".)
5. Which vendor-claim thresholds (15 ms disk, 80% CPU, MongoDB 7.0 queueing, 8.0 lock-wait threshold, Query Store intervals) hold up against current vendor docs?
6. Which signals are worth building a real data path for first? (Suggested order by value-per-effort, an **inference**: SQL per-call durations and blocked-process reports, then HTTPERR parsing, then the Blackbox sampler.)
7. Does any item mislead a non-expert, particularly the headline sentence (A6), "What Unravis sees" (B9) and the verdict (B10d)?

### 5.5 What was not verified in this pass

Spot-checked against the source after writing (lines 1090-1098, 1781-1791, 1485): the Pulse formulas and red rules (A1-A6), the headline mismatch, the `layerRead` thresholds (2%, 25%, `>= 1.5`) and the Q1 gap all match the text above. Everything else was read once, not re-checked.

- `evidenceView` internals and the History/Failures tabs (A9) were read at header level only.
- No item was executed. Values such as 4,216 requests, 522 ms at 2.4x, 185 5xx at 4.4%, 210 IIS 5xx, 1,046 ms at 4.0x and 2,005 slow calls at 40% come from the existing calculations guide, not from a fresh run.
- Vendor claims were not re-checked against the vendors.
- The three unread companion docs (`FLOW-COVERAGE.md`, `FLOWS-GUIDE.md`, `NOTEBOOKLM-SOURCE.md`) were not consulted.

---

## 6. Prompt for the reviewing agent

> Review `prototype/CHARTS-AND-STATS-REVIEW.md` against `prototype/tracing-paper-PROTOTYPE.html`. For each item, open the cited line range and confirm the formula, threshold and data origin. Report: (1) any entry whose description does not match the code, with the line; (2) any chart or stat in the wireframe that is missing from the document; (3) your verdict on each "Helpful?" judgement, agree or disagree with a reason; (4) the answers to section 5.4. Do not change the prototype. Mark anything you could not check as unverified.
