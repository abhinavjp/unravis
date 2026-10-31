# Investigating a recurring slowdown over long windows: UX patterns

## 1. Long ranges: downsample by default, drill down on demand
**Automatic rollup / resolution ladder.** Datadog caps how many points a graph can hold and increases aggregation as the window grows (for example, a 4-hour window is roughly one point per minute for a line graph, then coarser), and exposes `.rollup()` to override it. Grafana's **Max data points** (defaulting to the panel's pixel width) makes the data source aggregate to fit, and its **interval** variable (`$__interval`) coarsens automatically as you zoom out. Never fetch per-minute data across a 3-month window; fetch a bucket that fits the pixels and label it ("3 mo · hourly averages"). ([Datadog granularity](https://docs.datadoghq.com/metrics/guide/why-does-zooming-out-a-timeframe-also-smooth-out-my-graphs/), [Grafana query options](https://grafana.com/docs/grafana/latest/panels-visualizations/query-transform-data/))

**Three-level drill-down (day → hour → minute).** Show a daily/hourly rollup for the whole window, then let a click on a bucket re-scope the query to a finer sub-range. Grafana and Datadog Explore both work this way, and Honeycomb's temporal aggregation aligns raw events into time steps for the same purpose. ([Honeycomb temporal aggregation](https://docs.honeycomb.io/investigate/query/apply-temporal-aggregation))

**Tradeoff:** coarse buckets hide short spikes (a known Grafana/Datadog-plugin complaint that a 5-minute default rollup averages out brief anomalies). Mitigate: always display the current bucket size, allow forcing a finer interval, and offer max/percentile aggregation, not only average, for latency.

## 2. Recurring slowdowns: four named visualizations
- **Calendar heatmap (day-of-week × hour).** Aggregate all history into a 7×24 grid colored by the metric; the fastest read on "last Thursday of the month." Datadog's Heatmap widget and Honeycomb heatmaps plot distributions over time buckets, and the classic calendar view (FlowingData's crash example) reveals weekend upticks. Tradeoff: it collapses the month index, so "last Thu" versus "every Thu" needs separation. ([Datadog heatmap](https://docs.datadoghq.com/dashboards/widgets/heatmap/), [Honeycomb visualize events](https://docs.honeycomb.io/investigate/analyze/visualize-events), [FlowingData](https://flowingdata.com/2018/07/09/how-to-visualize-recurring-patterns/))
- **Overlay of N occurrences aligned to the same offset.** Align each occurrence to t=0 and draw all cycles on one axis; the repeated shape pops out because time is aligned. Tradeoff: overplotting, so cap at roughly 6 to 10 lines, use opacity, or switch to small multiples. ([FlowingData](https://flowingdata.com/2018/07/09/how-to-visualize-recurring-patterns/))
- **Small multiples / cycle plot (seasonal subseries).** One mini-panel per cycle, same scale, in order; a cycle plot shows, for example, all "last Thursdays" chronologically with a mean line, exposing both the recurring shape and drift. Tradeoff: no direct overlay comparison; best when there are many cycles. ([DataViz Catalogue](https://datavizcatalogue.com/blog/chart-snapshot-cycle-plot/))
- **Explicit recurrence selector.** Let the user define the recurrence ("last Thursday, monthly") and generate the occurrence list, then feed it to overlay/small multiples. This makes the pattern auditable rather than inferred.

## 3. Baselines and comparison
- **Seasonal baseline / confidence band.** Dynatrace's seasonal model learns daily/weekly patterns and draws a confidence band (per entity, trained on the last 14 days per minute) so normal weekday peaks do not fire and off-pattern spikes do. Copy this as a shaded band with a user-set tolerance. ([Dynatrace seasonal baseline](https://docs.dynatrace.com/docs/dynatrace-intelligence/reference/ai-models/seasonal-baseline))
- **Same-period-last-cycle comparison.** Datadog's `calendar_shift(-1mo/-30d/-4w)` and Grafana's **Time shift** overlay the matching prior period; Grafana warns to use calendar-aligned shifts (`1M/M`) rather than bare month offsets because months differ in length. Default the reference window to the same weekday/offset one cycle back, drawn as a ghost line. ([Datadog timeshift](https://docs.datadoghq.com/dashboards/functions/timeshift/), [Grafana](https://grafana.com/docs/grafana/latest/panels-visualizations/query-transform-data/))
- **Calendar-aligned window picker.** Datadog classifies windows as sliding, growing, or fixed and supports `last month`, `N weeks ago`; snap recurrence windows to calendar boundaries or "last Thursday" cannot be specified reliably. ([Datadog custom time frames](https://docs.datadoghq.com/dashboards/guide/custom_time_frames/))

**Tradeoff:** seasonal models need enough clean history per entity and train slowly; same-period comparison is confounded if the prior period was itself anomalous, so flag the prior period's own band or anomaly.

## 4. Overlap guard (incident vs reference)
**Baseline-contamination warning (non-blocking).** If the incident window and the reference window intersect, the comparison is invalid. Detect the intersection and show an inline banner on the reference control ("Reference overlaps incident by 3 days; comparison is confounded"), with a one-click fix ("use previous cycle"), but never block. This mirrors Grafana's explicit caution about ambiguous shifts and Datadog's explicit window semantics: surface them and let the engineer decide. ([Grafana](https://grafana.com/docs/grafana/latest/panels-visualizations/query-transform-data/))

## 5. Cost, progress, cancel, pagination (never block)
- **Dry-run / estimate-before-run.** BigQuery shows an estimated bytes-processed figure before execution and supports `--dry_run` and a maximum-bytes-billed cap that fails without charge; SQL Server's **Display Estimated Execution Plan** returns the plan and estimated rows without executing. On any filter/range change, show an inline "≈ N rows, ~X s, scans Y days," and only confirm when the estimate crosses a threshold. ([BigQuery](https://docs.cloud.google.com/bigquery/docs/best-practices-costs), [SSMS estimated plan](https://learn.microsoft.com/en-us/sql/relational-databases/performance/display-the-estimated-execution-plan))
- **Never block.** Do not modal-freeze the app. Run the query as a background job and keep the rest interactive. NN/g's guidelines: indicate progress and remaining steps, allow long processes to run in the background, and make completion salient. ([NN/g](https://www.nngroup.com/articles/designing-for-waits-and-interruptions/))
- **Progress + cancel.** Show a determinate bar (by buckets scanned where possible) and a Stop button beside it while running; killing a long query is a baseline expectation in SSMS and BigQuery. When real percent-done is unavailable, fall back to a stage list. ([UX StackExchange](https://ux.stackexchange.com/questions/155368/cancel-potentially-long-running-query))
- **Pagination.** Page the results table server-side. Pagination reduces the need to abort huge result sets and is the most reliable "cancel," since you simply stop fetching. Add a hard cap plus "load more."

## 6. Evidence for each suspect, and non-suspects
- **Suspect card.** One row per candidate cause: a one-line claim, the strongest supporting chart/metric, a confidence/impact badge, and a **"Why"** link.
- **"Why" opens the full incident timeline.** A drill-down that is the canonical artifact: a time-series **chart plus the underlying data table**, the window controls, the current rollup resolution, and the suspect's evidence overlaid on the incident. Datadog's graph/query inspector and Honeycomb's click-a-heatmap-cell-to-see-the-trace model this "chart, then raw rows" pairing, and Honeycomb links a histogram cell to a concrete span. ([Datadog heatmap](https://docs.datadoghq.com/dashboards/widgets/heatmap/), [Honeycomb](https://docs.honeycomb.io/investigate/analyze/visualize-events))
- **Non-suspects with a reason.** Keep ruled-out candidates collapsed, each with an explicit reason ("no signal change in window"; "correlates only at daily rollup, not per-minute"; "present in the reference period too") and note which view was checked. This distinguishes "we did not look" from "we checked and ruled out."

**Tradeoff:** non-suspects consume space; default them collapsed with a count ("4 ruled out") and a one-line reason each.

## 7. Minimal viable set for a wireframe
1. **Range bar**: rolling window label plus bucket-size label ("3 mo · hourly") and a resolution control.
2. **Recurrence panel**: calendar heatmap (day-of-week × hour) with a toggle to overlay/small multiples of selected occurrences, plus a "define recurrence" control.
3. **Baseline strip**: ghost line for same-period-last-cycle plus a shaded seasonal band; the overlap warning banner lives here.
4. **Run bar**: inline estimate ("≈ rows / ~time") and a Run button, never modal-blocking.
5. **Progress/cancel chip**: determinate-or-stage progress plus Stop, with a paginated data table below.
6. **Suspect list**: cards with evidence and a "Why" that expands to the chart-plus-table timeline.
7. **Ruled-out drawer**: collapsed list with one-line exclusion reasons.

## Sources
- Datadog: [graph granularity](https://docs.datadoghq.com/metrics/guide/why-does-zooming-out-a-timeframe-also-smooth-out-my-graphs/), [timeshift](https://docs.datadoghq.com/dashboards/functions/timeshift/), [custom time frames](https://docs.datadoghq.com/dashboards/guide/custom_time_frames/), [heatmap widget](https://docs.datadoghq.com/dashboards/widgets/heatmap/)
- Grafana: [query and transform data](https://grafana.com/docs/grafana/latest/panels-visualizations/query-transform-data/)
- Honeycomb: [visualize events](https://docs.honeycomb.io/investigate/analyze/visualize-events), [temporal aggregation](https://docs.honeycomb.io/investigate/query/apply-temporal-aggregation)
- Dynatrace: [seasonal baseline](https://docs.dynatrace.com/docs/dynatrace-intelligence/reference/ai-models/seasonal-baseline)
- FlowingData: [visualizing recurring patterns](https://flowingdata.com/2018/07/09/how-to-visualize-recurring-patterns/); DataViz Catalogue: [cycle plots](https://datavizcatalogue.com/blog/chart-snapshot-cycle-plot/)
- Google: [BigQuery estimate and control costs](https://docs.cloud.google.com/bigquery/docs/best-practices-costs); Microsoft: [display estimated execution plan](https://learn.microsoft.com/en-us/sql/relational-databases/performance/display-the-estimated-execution-plan)
- NN/g: [designing for long waits and interruptions](https://www.nngroup.com/articles/designing-for-waits-and-interruptions/); UX StackExchange: [cancel long-running query](https://ux.stackexchange.com/questions/155368/cancel-potentially-long-running-query)