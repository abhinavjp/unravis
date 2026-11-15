# Slow call screen: is it the best way to answer "why was THIS call slow?"

Research note, 2026-11-15. Read-only study: no app code, tests or dist were changed.
Method: read the screen code and docs in this repo, then read primary vendor docs. Where a claim is
my own design judgement and not backed by a source, it is marked **(judgement)**. Where I could not
fetch a source, it is marked **(thin evidence)**.

## (a) Verdict

1. The screen answers "what else was happening around this call" well. It answers "where did this
   call's own time go" poorly. That second question is the one the investigator asked.
2. Concurrency lanes (calls running at once) are a sound load or saturation context signal. They
   cannot, on their own, tell you why one call was slow. Keep them, but as context, not as the answer.
3. The single biggest gap: the call's own breakdown. The page splits time only into "Filters" and
   "Everything else". Per-call SQL ms, Redis ms, web-call ms and counts are already wired
   (`internal/store/record_perf.go`, BL-006) but the copy still says they are "not recorded per call yet".
4. Second gap: nothing says "this call versus normal". The screen requests a reference window and
   then discards it (BL-040). The per-minute p50/p95/max are already in the data.
5. Add a one-sentence, non-causal lead line at the top, only when the data supports it. For example:
   "Most of this call's time (1.8 s of 2.4 s) was spent in the database." Say "not recorded" otherwise.
6. Minimap: keep, but show it only when the window does not already cover the whole strip. Evidence
   for it is moderate, not strong.
7. "Open in Analysis": keep as a secondary action and say what it does. Low confidence either way.
8. The right panel carries too much explanation and one demo-only control. Collapse it for real use.
9. Everything above uses data that already exists. New collectors are listed separately in (e) and
   need an owner go.
10. Evidence is mostly vendor documentation. There is no controlled study of beginners using any of
    these views, so the beginner-readability advice is design judgement backed by tool precedent.

## (b) Keep / Change / Add / Drop

| Action | Item | Reason (one line) | Confidence |
|---|---|---|---|
| Keep | Concurrency lanes for API, IIS, DB | Traffic and saturation context; matches golden signals and USE | high |
| Keep | Hover readout "under the hover line" | Cheap way to read exact values; no new data | med |
| Keep | Nearby calls table with Everyone / Only user / Only API | Same-API and same-user baselines are a real "normal" comparison | med |
| Keep | Honest "not recorded" gaps, never zero | Matches the repo rule; absence is not evidence | high |
| Change | "Where the time went" bar from Filters/Everything else to Filters / SQL / Redis / Web calls / Other | The data is wired; today's bar hides it | high |
| Change | Stale copy: "not recorded per call yet" for SQL, Redis, web | Contradicts BL-006 once live data confirms | high |
| Change | "While this call ran" shows start-time values only | Should cover the call's span (start to end), not one instant | med |
| Change | Minimap: hide when the window already covers the strip | Earlier research in `.forge/investigation-timeline/drill/research/minimap-overview-research.md` | med |
| Change | "Open in Analysis" label and place | Say what it does; make it secondary | low |
| Add | One-sentence non-causal lead line | Beginners read a sentence before a chart | med |
| Add | "This call vs normal" marker using existing p50/p95/max | Data exists; answers "was it an outlier here" | high |
| Add | Latency-over-time view (p50/p95/max) beside concurrency | Data exists in buckets; shows when it got slow | med |
| Add | List of overlapping events with overlap duration (IIS queue reasons, blocked process, deadlock, Query Store waits) | Data exists; currently only markers or hidden | med |
| Drop (for real users) | Demo-only "Pretend the database is" control | Owner tests on live data only (project CLAUDE.md) | high |
| Drop / collapse | Long "How to read" and "What this does not show" panels | Move to an on-demand help drawer | low |
| Drop | Unused `reference` request, unless the "vs normal" marker uses it | Wasted read, BL-040 | med |

## (c) Findings per question

### Q1. Concurrency y-axis vs latency over time, percentile bands, waterfall, heatmap

What the screen does. Each lane plots "calls running at once" per time step, computed as busy
milliseconds divided by step length (`atOnce` in `web/src/lib/drill/callLanes.ts`). That is Little's
law on summed durations. The tracing response already carries per-bucket count, errors, clientErrors,
p50/p95/max, busyMs and a complete flag (`internal/server/tracing.go`).

What the sources say.
- Google's SRE book names four golden signals: latency, traffic, errors, saturation. It says to track
  latency of successful and failed requests separately, warns that averages hide the tail, and
  recommends histogram-style latency buckets. It also says rising latency is often an early sign of
  saturation. Source: https://sre.google/sre-book/monitoring-distributed-systems/
- Brendan Gregg's USE method: for each resource check utilization, saturation (queue or wait) and
  errors. Queues are where waiting happens. A five-minute average can look healthy while seconds-long
  bursts hit 100%. Source: https://www.brendangregg.com/usemethod.html
- Grafana's heatmap shows a distribution over time (cell colour = count in a bucket) and is meant for
  spotting outliers and shifts. Source:
  https://grafana.com/docs/grafana/latest/panels-visualizations/visualizations/heatmap/
- Honeycomb: a heatmap of duration over time, then box the outliers and compare them to the baseline
  outside the box. Source: https://docs.honeycomb.io/investigate/analyze/identify-outliers/
- Datadog and Jaeger answer the per-call question with a trace view (flame graph, waterfall, span
  list) and a bottom panel of SQL, errors, host graphs and logs. Sources:
  https://docs.datadoghq.com/tracing/trace_explorer/trace_view/ and
  https://www.jaegertracing.io/docs/latest/frontend-ui/ (Jaeger config page confirms a minimap in the
  trace header and critical-path highlighting; it says little about layout).
- Chrome DevTools Performance: a CPU/NET overview strip, a flame chart, and a Summary tab that gives
  duration, self duration and a timings breakdown for the selected event. Source:
  https://developer.chrome.com/docs/devtools/performance/reference

Reading across these. There are three different questions and three different charts:

| Question | Best-fit view | Do we have the data? |
|---|---|---|
| Was the system busy or queued then? | Concurrency / saturation (what we draw) | yes |
| Was this call an outlier at that time? | Latency over time with p50/p95 band, or duration heatmap | p50/p95/max yes; full distribution no |
| Where did this call's own time go? | Per-call breakdown or waterfall | totals yes; steps no |

Judgement. The concurrency lane is a good answer to the first question and a weak answer to the
other two. A busy lane next to a slow call invites a causal reading ("it was busy, so it was slow")
that the data does not prove. The screen already avoids causal wording in its copy, which is right.

Specific weaknesses of the concurrency view in this repo:
- Busy time is attributed to the bucket where a call started (BL-037, `busyMs` is the summed duration
  of calls that started in the bucket). A long call inflates its start minute only. Thin and
  misleading for a screen whose subject is one long call.
- Each lane has its own y-scale, so heights are not comparable between lanes. Honest, but a beginner
  may compare them anyway **(judgement)**.
- The DB lane is whole-database and, in the Query Store live state, block-granular (about 15 min
  blocks per `regroupBlocks`). It cannot show a short stall.
- A duration heatmap needs per-call durations across the window. The audit read returns at most 50
  slowest rows, so a true heatmap would need a new server aggregate (histogram buckets per minute).
  That is a small change on an existing read, not a new collector, but it is new code **(judgement)**.

Recommendation. Keep concurrency. Add a p50/p95/max line view as a switch on the same lane (data
exists). Defer the heatmap: p95 plus max plus the call's own marker gives most of the value for much
less work. Only one source (Honeycomb) argues strongly for heatmaps over percentile lines, and that
page does not compare the two directly, so treat "heatmap beats lines" as unproven here.

### Q2. "What was different about this call vs normal", and the phase breakdown

Best practice, as far as the sources show:
- Honeycomb BubbleUp: select the outliers, compare them with everything else, field by field. They
  also advise narrowing the time range, since long ranges add unrelated variation. Source:
  https://docs.honeycomb.io/investigate/analyze/identify-outliers/ The standard charts there do not
  rank fields; ranking needs their paid "Intelligence" feature, so a plain list of differences is not
  something to expect for free.
- Datadog: span share of total time, plus host metrics overlaid on the same time range. Source:
  https://docs.datadoghq.com/tracing/trace_explorer/trace_view/
- Jaeger: critical path highlighting shows which operations drive total duration. Source:
  https://www.jaegertracing.io/docs/latest/frontend-ui/ Its docs page I fetched does not cover trace
  comparison, so I do not claim Jaeger has a diff view **(thin evidence)**.
- Sentry: waterfall with time compression of gaps, related errors in the same timeframe, and the
  option to pin an attribute as a waterfall column to compare values across spans. Source:
  https://docs.sentry.io/concepts/key-terms/tracing/trace-view/

What this repo already has.
- The screen does no comparison. `SlowCallScreen.svelte` builds a `reference` (equal period right
  before the window) and never draws it.
- Nearby calls with the Only user / Only API chips are a partial baseline (same API, same user). The
  table shows Took, Database, Redis, Web calls per neighbour.
- `internal/investigate` already compares incident against reference with Yes/No/Unknown signals
  (SigP95Up, SigArrivalsUp and others) and a suspect catalog (HTTP.sys queue backup, thread-pool
  starvation, SqlClient pool, slow downstream HTTP, and more). That is for the Analysis screen, not
  for one call.

Is the call's own phase breakdown shown well enough? No.
- Per-call fields wired: RequestHitCount, PreActionFilterTotalMilliseconds, SqlCommandCount,
  SqlMilliseconds, RedisCallCount, RedisMilliseconds, HttpCallCount, HttpMilliseconds, and
  PreActionFilterTimings (`internal/store/record_perf.go`, `web/src/lib/drill/perf.ts`).
- The UI uses only filters total. The copy for "everything else" says SQL, Redis and web are "not
  recorded per call yet" (`drillCopy.ts`, `partOther`). That is stale if live data carries them.
- BL-006 says the real field names are not yet confirmed on a live source. So this must be verified
  on live data first, per the project rule. I did not confirm it; the MCP read tools exist but I did
  not call them.
- The older `docs/api-audit-log-schema.md` says DB timings and per-phase breakdown are not stored.
  That doc predates the wired perf fields; the two disagree and the live source decides.
- Audit timing phases that are certain: AuditCreateTime, ApiTime, PayloadRecordTime (schema doc;
  user-felt latency is roughly AuditCreateTime + ApiTime).
- The IIS side adds a different view of the same call: `time-taken` starts when HTTP.sys receives the
  first byte and stops at the last send completion, and does not include network transit. Source:
  https://learn.microsoft.com/en-us/iis/configuration/system.applicationhost/sites/sitedefaults/logfile/
  (the TimeTaken note). So IIS time-taken minus API time is the time spent before the app started or
  after it finished, including HTTP.sys queueing and response sending. That gap is worth showing
  when both rows are matched, but matching an IIS row to an audit row is a join the repo does not do
  today **(judgement; needs a check that a safe join key exists)**.

Recommended presentation (all from data that exists):
1. A stacked bar for this call: Filters, SQL, Redis, Web calls, Other, each with ms and count. Any
   part not recorded is drawn as a hatched "not recorded" segment, never merged into Other.
   Chrome DevTools' summary of duration, self duration and breakdown is the precedent.
2. Beside the bar, the same split for the typical call of the same API in the window (median of the
   nearby same-API rows). Precedent: Honeycomb baseline-vs-selection. Needs enough rows; show nothing
   when there are fewer than about 5 comparable rows **(judgement on the threshold)**.
3. A "vs normal" marker on the lane: this call's duration against p50 and p95 of its minute and of the
   reference window.
4. Hit count (cold start hint) and filter timings stay as secondary detail.

Caveat that must stay in the copy: per-call SQL ms is the app's own measure of time waiting on SQL,
not the database's view. A high number means "the app waited", not "the database was slow".

### Q3. Missing data versus low-value or noisy data

Data that exists and is under-used (no new collector):

| Data | Where | Now | Suggested use |
|---|---|---|---|
| Per-call SQL / Redis / web ms and counts | `record_perf.go` | totals wired, UI ignores | Breakdown bar |
| PreActionFilterTimings | `record_perf.go` | listed | Keep as detail |
| p50/p95/max per bucket | `tracing.go` | partly used (p95 in readout) | Latency view, vs-normal marker |
| IIS time-taken, sc-substatus, sc-win32-status | `iis/w3c.go` | bars for slow rows only | Show status codes on bar tooltip |
| HTTPERR queue-related reasons | `iis/httperr.go` | markers | "Requests were queued at HTTP.sys" line, matches suspect S1 |
| XE blocked_process_report, xml_deadlock_report | `sqlserver/xe.go` | markers | Overlap list with duration |
| XE wait_info (lock >30 s, latch >15 s, preemptive >5 s) | `sqlserver/xe.go` | markers | Same, with threshold caveat |
| Query Store wait categories | `sqlserver/querystore.go` | "wait" events | Per-block top wait category |
| Hit count | `record_perf.go` | listed | Cold-start hint |

Noisy or low-value as currently shown:
- "While this call ran" in `CallInspector.svelte` shows API/IIS/DB values at the call's start instant
  only. For a 20-second call that is a poor sample. Use the maximum or average over the call's span.
- Three independently scaled y-axes (see Q1).
- The 50-row slow-call cap draws bars from a biased sample (slowest only). It is honest if labelled,
  and it is, but it cannot show "normal".
- Query Store live state is block-granular (Microsoft: runtime stats are aggregated over a fixed time
  window; wait stats per query need capture enabled and SQL Server 2017+). Source:
  https://learn.microsoft.com/en-us/sql/relational-databases/performance/monitoring-performance-by-using-the-query-store
  So it can say "this database spent time on lock waits in this block", not "this call waited".
- Server-wide wait stats are cumulative since start and server-wide (not per call). Source:
  https://learn.microsoft.com/en-us/sql/relational-databases/system-dynamic-management-views/sys-dm-os-wait-stats-transact-sql
  Brent Ozar's tooling samples short windows to get a live view. Source:
  https://www.brentozar.com/sql/wait-stats/ This is the argument for a live sampler (see (e)).

Absence must never read as "all clear". The system_health session only records lock waits over 30 s,
latch waits over 15 s and some preemptive waits, plus deadlocks. Source:
https://learn.microsoft.com/en-us/sql/relational-databases/extended-events/use-the-system-health-session
So "no SQL event in this window" must be worded "no long SQL wait was logged", not "SQL was fine".

Missing and only available with a new collector: see (e).

### Q4. Minimap, right panel, nearby-table order, "Open in Analysis"

Minimap.
- Overview plus detail is an old, well-supported pattern (Shneiderman 1996; Cockburn et al. review),
  per the earlier minimap research in this repo. Trace tools use it (Jaeger's header minimap, Grafana
  trace view, Datadog); metric tools mostly skip it. Chrome DevTools has a CPU/NET overview strip that
  scopes the panels below when you select a range
  (https://developer.chrome.com/docs/devtools/performance/reference).
- Here the minimap shows a two-hour strip around the call so the investigator can pan beyond the
  ±15 min window. That is a real need, because the window presets stop at 60 min and Custom at 120.
- Evidence for per-row separately scaled mini strips is thin: I know of no tool that does exactly
  that. It is a design call. Whether beginners use it is untested.
- Verdict: keep; hide when the window already covers the whole strip (the previous note's finding);
  label it with one plain phrase. Confidence medium.

Right panel (`CallInspector.svelte`, 320 px). Sections: Selected, While this call ran, Data sources,
What this does not show, How to read the chart, and the demo-only Pretend control.
- "Selected" and "While this call ran" are facts about the call: they belong first, and the breakdown
  bar could live here instead of in the left column so the answer sits next to the chart.
- "Data sources" is genuinely useful to an investigator who must say what the evidence covers.
  Keep, shortened.
- "What this does not show" and "How to read the chart" are long. Make them a collapsible "How to
  read this" drawer, open on first use only **(judgement; no source tests this)**.
- Pretend control: remove from the real screen. The project's own rule is live data only.

Nearby table. Current columns: Time, From this call, User, API, Took, Database, Redis, Web calls, with
chips Everyone / Only user / Only API, and a second tab "All sources in time order".
- Ordering by time is a neutral default and easy to read. For "why was this slow" I suggest an
  optional sort by "Took" and a default highlight of rows that overlapped this call's span, since
  those are the ones that could have competed with it **(judgement)**.
- Add one column: share of the call's time in the database (when recorded), so a beginner can scan for
  the odd row. Needs live data to confirm the fields.
- Datadog and Sentry both let you add or pin a column to compare values across rows, which supports
  this (Sentry "Pin to waterfall", linked above).
- Rows with unrecorded values should show "not recorded", as they do (BL-001).

"Open in Analysis".
- Analysis holds the incident-versus-reference comparison and the suspect catalog, which this screen
  lacks. So there is a real reason for the link: "compare this period with the period before".
- Today the button leaves the page with the generic label. Rename to say what it does, move it below
  the lead sentence, and keep "Back to timeline" as the primary exit. Confidence low: I have no data
  on how often investigators press it.
- If the "vs normal" marker (Q2) ships, the link matters less. Re-check its usage after that.

### Q5. Beginner readability

What I could verify: no study was fetched on beginners reading these exact charts **(thin evidence)**.
What I can cite is tool precedent: Datadog opens a trace on a header with total duration and a
default view; Sentry shows duration right on each span row; Chrome DevTools opens the Summary tab on
the selected item with a duration breakdown. All lead with the answer and put the chart second.

Recommendation: yes, lead with one plain sentence, built from facts, not guesses.
- When per-call phases are recorded: "This call took 2.4 s. 1.8 s of that was spent waiting on the
  database." Optionally add "typical for this API: 0.4 s".
- When only some phases are recorded: say so: "SQL time was recorded. Redis and web call time were
  not."
- When nothing is recorded: "This screen cannot say where this call's time went. It can show what
  else was running at the time."
- Wording must pass the repo's non-causal lint (`evidence.LintCausal`). "Spent 1.8 s of 2.4 s in the
  database" is a measurement; "the database caused the delay" is a cause and must not appear.
- Never name a single cause when two phases are close. Show the top phase only when it holds more than
  half the time **(judgement on "half")**, otherwise say the time was spread out.

Other beginner points:
- The axis explainer added in BL-050 is the right idea; keep it short and on the chart.
- Words like "P95" are already glossed as "slowest 5% (P95)". Keep that.
- Colour alone should not carry meaning; the hatched "not recorded" segment should differ in pattern.

## (d) Ranked changes (at most 7)

| # | Change | Effort | Data exists? |
|---|---|---|---|
| 1 | Real breakdown bar: Filters / SQL / Redis / Web calls / Other, with ms, counts, and "not recorded" segments. Fix the stale copy. Verify field names on live data first (BL-006). | S to M | yes in code; live confirmation pending |
| 2 | One-sentence non-causal lead line at the top of the call facts, built from change 1 and the call's total. | S | yes once #1 is verified |
| 3 | "This call vs normal": marker for this call's duration against the p50/p95/max of its minute and the reference window; reuse or remove the discarded `reference` request. | S to M | yes (bucket p50/p95/max) |
| 4 | "While this call ran" over the call's span (max and average), plus an overlap list: IIS queue reasons, blocked process, deadlock, Query Store waits, each with overlap duration and the "only long waits are logged" caveat. | M | yes |
| 5 | Latency-over-time (p50/p95/max) as a switch on each lane, with the call marked. | M | yes (buckets); no new server work |
| 6 | Tidy the right panel and header: remove the Pretend control from real use, collapse the two long explainers, rename "Open in Analysis", hide the minimap when the window covers the strip. | S | yes |
| 7 | Nearby table: highlight rows that overlapped the call; add a database-share column; optional sort by Took. Compare the breakdown with the median same-API neighbour when 5 or more exist. | M | yes (needs live field confirmation) |

Not ranked, but worth a ticket: fix BL-037 so busy time is spread across the buckets a call actually
spans. It changes the server aggregate and the meaning of every number on the lanes, so it is larger
than the rest and should get its own review **(judgement: effort L)**.

Order logic: #1 and #2 close the biggest gap with data already in hand. #3 adds the missing "vs
normal". #4 and #5 improve the context. #6 and #7 are cleanup and polish.

## (e) Needs a new collector or agent (deferred, owner go required)

Per `CLAUDE.md` and `docs/BACKLOG.md`, nothing here may be built without the owner's go. These are
the Blackbox items already listed in the backlog, mapped to the question each would answer:

| Backlog | What it would add | Why it would help "why was this call slow" |
|---|---|---|
| BL-101 | SQL `dm_exec_requests` sampler | Live waits and blocking at the time of the call, not just logged long waits |
| BL-102 | Wait-stats delta sampler | Per-interval wait mix; fixes the "cumulative since start" problem in `sys.dm_os_wait_stats` |
| BL-104 | HTTP.sys request-queue sampler | Direct evidence of queueing before the app saw the request (suspect S1) |
| BL-106 | w3wp active requests | Number of requests inside the worker process; thread-pool starvation signs |
| BL-108 | CPU and disk | Host saturation (USE method); Datadog overlays host graphs on the trace |
| BL-109 / BL-110 | CLR / EventPipe counters | GC pauses, thread-pool queue length |
| BL-111 | Event log tailer | App-pool recycle and crash markers |

Also outside current data, and not in the backlog as far as I read: true per-call phase steps (a
waterfall). The audit record has `steps` as a project-literal null today. If the real audit source
adds step data, the waterfall becomes a reader task, not a collector. If it does not, it would need
instrumentation in the app, which is outside this tool's read-only scope.

## Source list and confidence notes

Fetched and used:
- Honeycomb outliers: https://docs.honeycomb.io/investigate/analyze/identify-outliers/
- Datadog trace view: https://docs.datadoghq.com/tracing/trace_explorer/trace_view/
- Jaeger UI (config page only): https://www.jaegertracing.io/docs/latest/frontend-ui/
- Sentry trace view: https://docs.sentry.io/concepts/key-terms/tracing/trace-view/
- Chrome DevTools Performance: https://developer.chrome.com/docs/devtools/performance/reference
- Google SRE book, monitoring: https://sre.google/sre-book/monitoring-distributed-systems/
- Brendan Gregg, USE method: https://www.brendangregg.com/usemethod.html
- Grafana heatmap: https://grafana.com/docs/grafana/latest/panels-visualizations/visualizations/heatmap/
- Microsoft, IIS logFile (TimeTaken note): https://learn.microsoft.com/en-us/iis/configuration/system.applicationhost/sites/sitedefaults/logfile/
- Microsoft, Query Store: https://learn.microsoft.com/en-us/sql/relational-databases/performance/monitoring-performance-by-using-the-query-store
- Microsoft, system_health session: https://learn.microsoft.com/en-us/sql/relational-databases/extended-events/use-the-system-health-session
- Microsoft, sys.dm_os_wait_stats: https://learn.microsoft.com/en-us/sql/relational-databases/system-dynamic-management-views/sys-dm-os-wait-stats-transact-sql
- Brent Ozar, wait stats: https://www.brentozar.com/sql/wait-stats/
- Earlier in-repo minimap research: `.forge/investigation-timeline/drill/research/minimap-overview-research.md`

Not covered, so treat related points as thin: Zipkin and OpenTelemetry waterfall docs, Kibana,
SolarWinds DPA, Microsoft HTTP.sys queue docs (the page I tried returned 404), Grafana/Honeycomb
side-by-side heatmap-versus-line comparisons, and any controlled study of beginners reading
time-series or trace charts. The recommendations that depend on beginner behaviour (lead sentence,
collapsed explainers, minimap use) are design judgement supported by tool precedent only.

Repo files this note rests on: `web/src/components/SlowCallScreen.svelte`,
`web/src/components/DrillOverview.svelte`, `web/src/components/CallInspector.svelte`,
`web/src/lib/drill/callLanes.ts`, `callController.ts`, `drillCopy.ts`, `perf.ts`, `callReadout.ts`,
`internal/server/tracing.go`, `internal/store/record_perf.go`, `internal/calltrace/`,
`internal/investigate/`, `internal/importer/iis/`, `internal/importer/sqlserver/`,
`docs/api-audit-log-schema.md`, `docs/BACKLOG.md`.

Not verified on live data: whether the per-call SQL, Redis and web-call fields arrive populated on the
real audit source. Everything in changes #1, #2 and #7 depends on that check.
