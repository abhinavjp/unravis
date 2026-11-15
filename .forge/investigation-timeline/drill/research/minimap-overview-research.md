# Minimap / overview strip: research for the Slow call screen

Date: 2026-11-13. Question: keep, redesign or drop the "whole hour" minimap above the big chart.
Method note: pages were read through a fetch tool that summarises them, so descriptions are paraphrases of the official page text. Items I could not confirm are marked "unverified" (section 6).

## 1. Verdict

**Redesign, do not drop.** The overview+detail idea is well supported: every trace tool studied (Jaeger/Zipkin/Grafana/Datadog/Chrome DevTools) uses a draggable-selection strip over a long timeline, and Shneiderman's 1996 paper describes exactly this "overview with a movable box" as the standard way to control a detail view. Research also shows people *prefer* it and it orients them, but it costs screen space and attention, and it can be redundant. Unravis's current strip has four weaknesses for a beginner: (a) rows are scaled separately with no numbers, so a quiet row looks as dramatic as a busy one; (b) the caption talks about "taller means more running at once" but never says each row has its own scale; (c) missing data is only a hatch with no words; (d) nothing says *why* a beginner would use it. Fix those with small copy and CSS/JS changes, hide the strip when the window already covers the whole hour (the wireframe already has a `capped` flag), and add explicit "Earlier / Later / Back to this call" controls so dragging is optional.

## 2. What the tools actually do

| Tool | What it has | How it is labelled | Beginner aids | Source |
|---|---|---|---|---|
| Chrome DevTools Performance | Top strip over the recording (CPU, NET charts); click-drag a range in it; selected range can be "zoomed" into, which builds a breadcrumb trail | "Timeline overview"; breadcrumb trail at top; a duration label appears when measuring | Hovering the overview shows a vertical marker across the whole trace and screenshot of that moment; hovering an item in the main track highlights its range in the overview; keyboard W/A/S/D and trackpad gestures; breadcrumbs jump back to a broader level (docs describe no single "reset" button) | [A], [B] |
| Grafana time series panel | No overview/range-slider panel found. Drag across graph = zoom in; double-click = zoom out; drag on the x-axis = pan | Time picker shows the current range; hover shows exact timestamps | Zoom-out icon, double-click, and `t-` / `t+` keyboard shortcuts; time picker always states the range as text | [C], [D] |
| Grafana trace view | A minimap over the trace; drag across it to zoom the main timeline | "Minimap"; button named "Reset selection" appears on hover while zoomed | Explicit, named reset control (appears only when useful) | [E] |
| Datadog trace flame graph | Minimap used to zoom into the selected span or back out to the full trace; scroll = zoom, drag = move around; "focus" icon next to the minimap isolates a span and its children | "Minimap", "focus" | Docs say you "reset the focus" to get back to the full trace but not how (unverified detail) | [F] |
| Datadog timeseries graphs | Not confirmed from a primary page (see section 6). Search snippets say notebooks: dragging a graph changes the notebook's global time, with a reset button | n/a | n/a | [G] (secondary) |
| Jaeger UI | Minimap in the trace header; can be switched off with `uiTimelineHideMinimap=1`; collapsing the trace header hides summary and minimap; changelog (2026) says the minimap is now hidden when the timeline is hidden | "Minimap" (config name) | Docs describe only show/hide, not usage text | [H], [I] |
| Zipkin Lens | README: classic UI had zoom but it did not help navigate; Lens added a trace minimap, "similar to Jaeger", to zoom to a timeframe | "minimap" | Added because plain zoom was not enough for navigation | [J] |
| Kibana Discover (7.9 docs; current docs not confirmed) | Histogram of hits over time: click a bar to zoom to that interval; click-drag over the chart background to pick a custom range | Histogram; interval drop-down | Cursor turns into a plus sign where a drag can start; time picker at the top shows the range | [K], [L] |
| Honeycomb | Heatmap: draw a box around points to select, then choose BubbleUp. No separate overview strip found | "Selection" (yellow) vs "Baseline" (blue) | Advice: pick outliers not the dense middle; use a narrower time range; the yellow/blue split labels what you picked vs everything else | [M] |
| SolarWinds DPA | Trends charts: click a bar or x-axis label to drill from 30 days to 1 day to 1 hour; no timeline/range selector found on the docs page; the page does not say how to go back up | "Trends", "Timeslice" tab with 10-minute intervals and interval drop-down | Click-to-drill instead of drag; coarse-to-fine steps | [N] |
| Sentry | Docs: dragging across a chart's timeline selects a range (used for "Compare Attribute Breakdowns", beta). No minimap in trace waterfall/charts docs | n/a | n/a | [O] |

Takeaways: the minimap pattern lives in *trace* tools (one long thing with spans). Metrics tools (Grafana, Datadog timeseries, Kibana, Sentry) mostly skip a separate overview and use drag-to-zoom on the main chart plus a time picker that states the range in words. Nobody in this set shows many tiny per-source rows each scaled separately.

## 3. When an overview helps and when it does not

Supporting evidence:
- Shneiderman's mantra is "Overview first, zoom and filter, then details-on-demand". His overview task describes an overview with a movable field-of-view box that controls the detail view, giving zoom factors of about 3 to 30. [P]
- Cockburn, Karlson and Bederson's review: overview+detail interfaces are often *preferred*, and in one map study users preferred the overview even though it was slower, saying it helped them orient themselves. [Q]
- The same review lists the costs: it uses extra screen space, and switching between overview and detail "required mental effort and time". It suggests letting users show/hide the overview, letting users browse the overview without changing the detail, and showing detail changes immediately in the overview. [Q]
- One study found the overview slowed people down when the detail view already carried the needed information (made the overview redundant). [Q]

So the overview is worth it when:
1. The detail window is a small part of the whole (here: default window is a fraction of 60 minutes), so people need to know what happens before/after.
2. The overview shows something the detail does not, such as "was there a bigger spike 20 minutes earlier?".
3. The investigator will move the window (look earlier/later), not just read it.

It is NOT worth it when:
1. The window already covers the whole hour (the strip would repeat the main chart). Hide it.
2. Only one source is active and the main chart already shows the same shape.
3. It is so small and unlabelled that a beginner cannot tell what it adds. The cost (space + attention) then exceeds the benefit. This is the risk with the current thin 16-20 px rows.

## 4. Alternatives and trade-offs for a non-expert

| Option | Pros | Cons for a beginner | Evidence / note |
|---|---|---|---|
| (a) Minimap + draggable box (today) | Always shows context; one-gesture move; matches Shneiderman's field-of-view box | Two charts to relate; hard to discover drag; tiny rows hide detail; needs good caption | Standard in trace tools [E][F][H][J][P] |
| (b) Drag-to-zoom on the main chart + Reset button | No second chart; familiar from Grafana/Kibana; saves space | Drag-to-zoom is hard to discover; loses context of "where in the hour am I" unless a range text is shown; accidental zoom | Grafana and Kibana both show range text and give a zoom-out path [C][D][K] |
| (c) Range presets only (plus/minus 5/15/30/60 min) | Simplest, no dragging, easy to explain, keyboard friendly | Cannot look "earlier" without changing the centre; cannot see context beyond the preset | Unravis already has before/after minutes |
| (d) One chart that always shows the whole hour with the interesting window shaded | One chart, no relating two views; context always present | At 60 columns the detail is too small to read slow-call timing; defeats the zoom | Basically the wireframe's own minimap, but bigger. Reasonable only if the hour fits readably |
| (e) Anomaly highlights ("busiest minute", "no database data here") | Tells a beginner where to look; reduces need to interpret heights | Needs correct rules or it misleads; extra copy; do not claim cause | Honeycomb's selection-vs-baseline labelling shows the value of plain-word highlights [M] |

Combining (a) with small pieces of (c) and (e) gives the best result for beginners.

## 5. Redesign proposal for Unravis (all plain HTML/CSS/JS)

Keep the strip, make it explain itself.

**R1. New caption (replaces `capHtml`).**
"This is the whole hour, one column per minute. The outlined box is the part shown in the big chart below. The red line is the slow call. Drag the box, click a spot, or use the buttons to look earlier or later."
Second line, muted: "Each row is scaled to its own busiest minute, so compare shapes within a row, not between rows."

**R2. Say the scaling out loud, and show the number.** Keep separate row scaling (a quiet row such as the database would otherwise look flat) but add a small right-hand label per row: `peak 38 at once at 14:22` (use the existing `fmtConc`/`fmtHM`). This removes the "no numbers" problem without changing the bars. Optional tick: mark the peak column with a 1px darker bar. Rejected alternative: one shared scale across rows. It is more honest between rows but flattens rows with small counts; use it only if the owner prefers.

**R3. Taller rows and a clearer red line.** Raise `.ov-row` from 20px (16px in `.work`) to about 28px; the extra height makes the 60 columns readable. Label the red line directly: a small text "this call 14:30" above it (positioned with the same `left:` percent as `.ov-call`).

**R4. Show the window as text next to the box.** `winLine()` already prints "Showing 14:25 to 14:35". Keep it directly above the strip so the box and its words are adjacent. This copies the "time picker states the range" idea. [D]

**R5. Handle affordances.**
- Make the box edges visibly grabbable: 6-8px wide grips (d3-brush's default handle size is 6px) with `cursor: ew-resize` on the edges and `cursor: grab`/`grabbing` on the box. [R]
- Show the same `title` tooltip on the box: "Drag to look earlier or later".
- Add three small buttons under the strip: "Earlier", "Later", and the existing "Back to this call" (named like Grafana's "Reset selection" [E], but plainer). Keep "Back to this call" always visible but disabled when the window is already centred.
- Add keyboard arrows on a focusable box (role="slider" with `aria-valuetext="Showing 14:25 to 14:35"`).
- Keep the box wide-edge resize optional; the before/after minute pickers already resize it, so skip resizing to keep scope small.

**R6. Hide when redundant.** If `capped` (the window covers the whole hour), do not draw the strip at all and drop the caption. Replace it with the one line the wireframe already prints ("the whole hour is shown"). Also hide when only one source is active and the window covers more than about 80 percent of the hour (suggestion, not tested).

**R7. Missing data.** Keep the hatch for minutes with no data but add words: a legend chip "Hatched = no data recorded" shown only when at least one hatched cell exists, and on hover of a hatched column: "No database data for 14:41". Do not draw missing as zero; the wireframe already uses `null` vs a value correctly.

**R8. Highlights for beginners (small, optional).** Beside the caption, one sentence built from data: "Busiest minute: 14:22 (web server)." Only state facts the data supports; do not say "cause". This applies the "highlight the interesting thing" idea to the strip without a new collector.

**R9. Sync with the big chart.** When the user hovers a column in the strip, show the minute as text (reuse `readoutHtml(t, note)`), and when they hover the main chart, mark that minute in the strip. Chrome DevTools does both directions. [A] Cockburn also advises that detail changes show immediately in the overview. [Q]

**R10. Reduce what must be learned.** Show a one-time hint above the strip for the first visit only, dismissible: "Tip: the strip is a map of the hour. Drag the box to move the big chart." (Store the dismissal in app settings, not a new collector.)

Suggested order of work: R1 + R4 + R6 (copy and hiding, tiny), then R2 + R3 + R7 (labels, heights, legend), then R5 + R9 (interaction). R8 and R10 last.

Copy checklist for the beginner pass: avoid the words "concurrency", "P95", "lane" in the strip; use "running at once", "slowest 5 percent", "row".

## 6. Open questions and unverified items

- Chrome DevTools: official docs call the strip "Timeline overview" and describe breadcrumbs, but I found no single "reset zoom" control in the docs. [A]
- Datadog timeseries drag-to-zoom and reset behaviour: not confirmed from a primary page (only search snippets). The Datadog trace page does not say how to reset the minimap or focus. [F][G]
- Jaeger: the official docs only describe hiding the minimap; how the minimap is dragged is not described there. The Grafana trace-view description of drag/"Reset selection" is Grafana's own UI, not Jaeger's. [E][H]
- Zipkin Lens: only the README statement was found; no usage docs. [J]
- Kibana: the 7.9 docs were read; current 8.x/9.x Discover docs did not describe drag selection, so current behaviour is unverified. [K][L]
- SolarWinds DPA: no timeline/range selector found in the Trends docs page; other DPA pages were not read. [N]
- Sentry: no minimap in docs read; drag-select is described only for a beta comparison feature. [O]
- Nielsen Norman Group: I could not find an NN/g article on overview+detail; the 404 on a guessed URL says nothing. I used Shneiderman and Cockburn et al. instead. NN/g guidance remains unverified.
- No source found that tests per-row scaling versus shared scaling for stacked small rows; R2 is a design judgement, not research.
- Whether a beginner actually uses the strip is untested; a short think-aloud test with the owner on live data would settle R6 (hide threshold) and R10 (tip).

## 7. Sources

- [A] Chrome DevTools, Performance panel reference (Timeline overview, breadcrumbs): https://developer.chrome.com/docs/devtools/performance/reference
- [B] Chromium DevTools front end, TimelineMiniMap source (breadcrumbs bar + overview pane): https://chromium.googlesource.com/devtools/devtools-frontend.git/+/HEAD/front_end/panels/timeline/TimelineMiniMap.ts
- [C] Grafana time series panel (drag-to-zoom, double-click zoom out, x-axis pan): https://grafana.com/docs/grafana/latest/panels-visualizations/visualizations/time-series/
- [D] Grafana, use dashboards (time picker, zoom, shortcuts): https://grafana.com/docs/grafana/latest/dashboards/use-dashboards/
- [E] Grafana, trace integration (minimap, "Reset selection"): https://grafana.com/docs/grafana/latest/explore/trace-integration/
- [F] Datadog, Trace View (minimap, focus): https://docs.datadoghq.com/tracing/trace_explorer/trace_view/
- [G] Datadog, notebooks (drag changes global time; secondary search snippet only): https://docs.datadoghq.com/notebooks/
- [H] Jaeger, Frontend/UI configuration (minimap options): https://www.jaegertracing.io/docs/latest/frontend-ui/
- [I] Jaeger UI changelog (minimap hidden with timeline): https://raw.githubusercontent.com/jaegertracing/jaeger-ui/main/CHANGELOG.md
- [J] Zipkin Lens README (minimap added): https://github.com/openzipkin/zipkin/tree/master/zipkin-lens
- [K] Elastic Kibana 7.9, Setting the time filter (histogram drag, plus cursor): https://www.elastic.co/guide/en/kibana/7.9/set-time-filter.html
- [L] Elastic, Discover get started (current; chart described, no drag text): https://www.elastic.co/docs/explore-analyze/discover/discover-get-started
- [M] Honeycomb, Identify outliers with BubbleUp: https://docs.honeycomb.io/investigate/analyze/identify-outliers/
- [N] SolarWinds DPA, Trends charts: https://documentation.solarwinds.com/en/success_center/dpa/content/dpa-view-data-trends-chart.htm
- [O] Sentry, Trace Explorer docs: https://docs.sentry.io/product/explore/trace-explorer/
- [P] Shneiderman, "The Eyes Have It" (1996), copy: https://webspace.science.uu.nl/~telea001/uploads/VACourse/Shneiderman96.pdf ; record: https://drum.lib.umd.edu/handle/1903/466
- [Q] Cockburn, Karlson, Bederson, "A review of overview+detail, zooming, and focus+context interfaces" (ACM Computing Surveys, 2009): https://www.microsoft.com/en-us/research/wp-content/uploads/2008/01/cockburn-ComputingSurveys09.pdf
- [R] D3 d3-brush docs (handles, cursors, brush.move): https://d3js.org/d3-brush
- Highcharts Stock navigator docs (small chart below main chart showing the full dataset, zoom/pan, can be disabled): https://www.highcharts.com/docs/stock/navigator
- Wireframe reference (read only): D:\Projects\api-audit-explorer\prototype\slow-call-popup-WIREFRAME-v3.html (`overviewHtml`, `winLine`, `capHtml` at about lines 609-623 and 706)
