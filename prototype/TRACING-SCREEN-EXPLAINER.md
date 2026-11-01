# Investigation Timeline screen: explainer

**Screen:** `tracing-paper-PROTOTYPE.html#/tracing/<case code>` (the Payroll.Run slowdown demo case). The route and file names keep the old working name "tracing"; the screen is now called the **Investigation Timeline** (see `NAMING-DECISION.md`).
**Branch:** `prototype/tracing-paper-e2e`
**Status of everything described here:** a throwaway prototype. All data is synthetic. Nothing here proves that the real software works.
**Revision:** after the design review (`unravis-tracing-wireframe-review.md`): plain-English labels, a "What Unravis sees" reading aid, evidence-strength wording, one evidence model, and honest empty states.

## 0. How to use this document

| Reader | What to read |
|---|---|
| Reviewing agent | §1–§10 describe what the screen shows and how each number is computed. §11 lists known weak spots. §12 is a review checklist. Tag every finding with the section number. |
| Video author | §1 gives the story, §3 the screen map, §6 a worked example with real numbers, §13 a scene-by-scene outline with narration. |

Evidence tags used below: **[Code]** read from the prototype source, **[Spec]** from the tracing specs (ids in brackets), **[Owner]** an explicit decision by the product owner in this project, **[Inference]** a prototype choice that no source states.

## 1. What the screen is for

The Investigation Timeline answers one question during an incident: **"What did my API, my web server and my database do, side by side, over the period I care about, and how does that compare with an earlier period?"**

- It draws three **lanes** on one shared time axis: **API audit** (live read of the audit database), **SQL** (SQL Server events from files stored in the case) and **IIS** (web-server logs from files stored in the case). [Spec TR-01, D-3, D-5]
- It compares an **incident window** (the period you inspect) with a **reference window**, always called the **comparison period** in the interface. [Spec TR-08; Owner]
- It adds one card that tries to say **where** a slowdown comes from: one environment, the whole server, or a neighbouring database. [Owner; not in any spec]
- A click on a bar opens the existing Run analysis for that bucket. [Spec TR-09, WP-61]

It replaces the "Guided" block on the case home. [Spec D-2 = A] The specs call it "tracing" (ids TR-xx, WP-xx); that is only the old working name.

### The demo world (all synthetic) [Code]

| Fact | Value |
|---|---|
| "Now" (the prototype clock never moves) | 20 Oct 2026, 12:00 UTC |
| Audit source keeps data from | 13 Oct 2026, 00:00 UTC |
| Audit source was **not recording** | 19 Oct 02:00–04:00 UTC (a 2-hour outage) |
| The slowdown (Payroll.Run) | 20 Oct 09:14–11:30 UTC |
| SQL and IIS files in the case cover | 20 Oct 08:00–12:00 UTC only |
| SQL source | PROD-SQL-01, databases `orders` (Production) and `PayrollApp` (Staging), both with Query Store on |
| Mongo source | `audit-mongo:27017`, database `PayrollAudit` (Production), profiler off |

Everything on the screen is a function of these facts, so the lanes agree with each other.

## 2. Vocabulary

| Term | Meaning |
|---|---|
| **Lane** | One source type drawn as a row of panels: API audit, SQL, IIS. |
| **Bucket / bar** | One slice of the time axis. Every bar is one bucket. Its length is stated on screen ("Each bar = 5 minutes"). |
| **Incident window** | The period being inspected. Default: the last hour. Maximum 30 days. [Spec D-4] |
| **Comparison period (reference window)** | An earlier period to compare against. It is a comparison, **not a promise that it was healthy**. |
| **P95** | The time 95% of requests finished within. In this prototype the window figure is an **approximation** (see §11, item 2). |
| **Stored vs live** | API audit is read live from the audit database each time. SQL and IIS come from files already saved in the case. |
| **Gap** | Time in the window for which a lane has no log data at all. Drawn as a labelled hatched band, never as zero-height bars. |
| **Floor-aligned axis** | The axis starts on a whole UTC hour at or before your start and never shifts, so the same bar always means the same clock time. [Spec §4.5, R6-02] |
| **Environment** | A free-text label on a database source (Production, Staging…). One connection can hold several. [Owner] |

## 3. Screen map, top to bottom

```
┌ Top bar: product name · Home · Case home · Patterns · Ledger · Export · Import · UTC/Local · Theme
├ (Prototype scenario banner and bottom dock: NOT product)
├ ① Title + "← Case home"
├ ② Range chips
├ ③ Incident window card | Reference window card
├ ④ Time-zone note, overlap/blocking notices
├ ⑤ Sources chips + Show timeline / Refresh timeline + Add evidence
├ ⑥ Summary line + banners
├ ⑥b "What Unravis sees" reading aid (new)
├ ⑦ Overall figures (7 cards)
├ ⑧ Trace board (three lanes, shared time axis)
├ ⑨ Bucket panel (hover/select a bucket, open Run analysis)
├ ⑩ "Where does the evidence point?" card
├ ⑪ Three collapsed detail sections
└ ⑫ Open the analysis · Export
```

Items ⑥–⑩ appear only **after** Show timeline has run. Section 3b below describes ⑥b.

### ① Title and back link
Shows "Investigation Timeline", the case title, the layout name, and **← Case home**. [Code]

### ② Range chips
**Last 15 min · Last hour (default) · Last 4 hours · Last 24 hours · Last 7 days · Last 30 days.** A chip sets the incident window to that period ending "now" and the comparison period to **the equal-length period right before it**. The chip that matches the current windows is highlighted. [Code; Owner asked for the same chips as the analysis, plus 30 days]

### ③ Investigation window and Compare it with cards
Headings are now plain English: **Investigation window** (the spec calls it the "incident window"; helper: "When was the problem happening?") and **Compare it with (comparison period)** ("This earlier period is only used for comparison. It may not have been healthy."). Each card holds a button showing the dates in the chosen zone, the length (e.g. "UTC · 2 h 30 min") and, beneath it, the UTC form `UTC 2026-10-20T09:00:00Z – …`. The two buttons sit on the same line. [Owner]

Pressing a button opens a **date-time range picker** with three parts: quick ranges, a calendar (click a start day, then an end day) and start/end times in the chosen zone, with Apply and Cancel. It closes on an outside click. [Owner]
- Incident quick ranges: the six chips above plus **"The slowdown in the demo data"** (09:00–12:00 on 20 Oct).
- Reference quick ranges: **"The equal period right before the incident"** and **"Same time yesterday"**.

### ④ Rules shown as notices
| Condition | What the screen does | Source |
|---|---|---|
| A window is missing its start or end | Blocking notice; Show timeline is switched off | [Code] |
| A window ends before it starts | Blocking notice | [Code] |
| A window is longer than 30 days | Blocking notice: "The longest window is 30 days" | [Spec D-4] |
| The two windows **overlap** | **Non-blocking** notice, with a button "Move it to just before the incident". The timeline still runs. | [Spec TR-08, WP-22] |
| Case is closed | Show timeline is off with the reason | [Code] |

Run analysis behaves differently: it **refuses** an overlapping reference and allows at most 7 days. After a bucket hand-off a user can therefore land on a refusal. This mismatch is open question 3 in `FLOW-COVERAGE.md`.

A note states that times are shown in the zone chosen by the **UTC / Local** switch while the timeline always reads and stores UTC.

### ⑤ Sources and the Start button
Chips toggle which sources are traced (API audit, IIS, SQL). Database connections added in the wizard (SQL `orders`, `PayrollApp`, Mongo `PayrollAudit`) are **not** chips: they feed card ⑩, not a lane. **Show timeline** becomes **Refresh timeline** once a trace exists. **Add evidence** opens the import drawer.

### ⑥ Summary line and banners
- **"Each bar = 5 minutes · 36 bars on the axis · window 2 h 30 min · 6 end slots outside the window"** (longer windows add "longer windows use coarser buckets"). Slots outside the window exist because the axis is rounded out to whole UTC hours. [Code, Spec §4.5]
- **Mixed-consistency banner** (cannot be dismissed): "Live and saved lanes are read at different times; totals across lanes are not a conservation check." Appears whenever any lane is a live read. [Spec §5.1, R6-07]
- **Incomplete banner** (red): a lane hit its time/row limit or a boundary scan went over its cap; hatched buckets are not exact. [Spec WS-18–25]
- **Overlap grey note** when the windows overlap: comparison figures are less meaningful.

### 3b. "What Unravis sees" (⑥b): the reading aid
A card above the figures that reads the same data in plain words (observation, meaning, confidence, next step). It is a reading aid; the figures and charts below remain the evidence. [Owner review]
- **Three layer tiles** (Application, Web server, Database), each with a status and one detail line. Rules (prototype defaults [Inference]): Application "Slower than usual" when P95 is at least 1.5 times the comparison period, "Returning errors" at 2% or more 5xx; Web server "Returning errors" at 2% or more 5xx and/or "slower responses" at 1.5 times; Database "Many slow calls" at 25% or more calls over 1 s and/or "blocking" when any blocked-process or deadlock report exists. Where the comparison period is not covered the tile says "Cannot compare with the earlier period" instead of judging.
- **Where the evidence points:** the same conclusion as card ⑩ (database and environment named) with a strength chip: Strong evidence / Some evidence / Not enough evidence.
- **Next step:** one sentence from the same model, plus a button **Look at <time> →** that selects the busiest moment (most slow database calls, or the highest API P95 if there is no SQL lane) in the chart.

## 4. The seven overall figure cards (⑦)

One row of cards sits above the charts, titled "Overall figures: investigation window against the comparison period". Each card has a lane-coloured dot, a **plain-English title**, a big value, a one-line **meaning**, a comparison line and the technical term in grey brackets. Plain titles: Requests to the application · Slow-request time (P95, estimated) · Application server errors (5xx) · Web server errors (IIS 5xx) · Slow web requests (IIS P95, estimated) · Slow database calls (over 1 s) · Database errors and blocking. The table below keeps the technical names.

| Card | Value | How it is computed [Code] | Comparison line | Turns red when |
|---|---|---|---|---|
| **API requests** | Count of audit requests in the incident window | Sum over the window of the audit generator's per-minute counts | "N.Nx the comparison period" = (events per minute now) ÷ (events per minute in the comparison period) | never (volume is neutral) |
| **API P95 time** | Observed API time, ms | Call-weighted mean of per-minute P95. **Labelled "estimated"** with a hover note; the product must compute it from every request's duration | incident P95 ÷ comparison P95 | ratio > 1.5 |
| **API 5xx** | Count of 5xx responses | Sum of per-minute 5xx; hint shows % of requests | per-minute ratio vs comparison | ratio > 1.5 |
| **IIS 5xx responses** | Count of 5xx in IIS logs | Sum of per-minute 5xx; hint "x% of N requests" | per-minute ratio | ratio > 1.5 |
| **IIS P95 time taken** | ms as logged | Weighted mean of per-minute P95; the request start is not verified | ratio vs comparison | ratio > 1.5 |
| **SQL slow calls** | Calls longer than 1 s | Sum of per-minute slow calls; hint "x% of N calls over 1 s" | per-minute ratio | ratio > 1.5 |
| **SQL errors and blocking** | "errors · reports" | Failed calls, then blocked-process reports plus deadlocks | none shown | — |

**When a comparison is withheld.** The comparison text reads "Cannot compare with the earlier period" (reason on hover, "earlier period not covered" visible). It appears when the comparison period has fewer than **30 events** or is less than **80% covered** by data. The reason is spelled out in the lane label (e.g. "the comparison period is only 40% covered"). [Inference: 30 events and 80% are prototype thresholds, not in any spec]

**If a lane has no data** its cards collapse into one muted card showing "–" and the reason (e.g. "This case has no file for this source").

## 5. The trace board (⑧)

### 5.1 Layout shared by every panel
- **Left column (lane label and legend):** lane name with colour square; a state sticker (see §7); event count; comparison text ("1.0x the comparison period rate", red above 1.5x, or "No comparison: …"); a warning when buckets are not exact; **"No log data for 6 d 20 h of this window (98%)"** when there is a gap; then the lane's **legend**.
- **Left axis:** titled by unit (requests, ms, calls, rows, reports) with five ticks (0, ¼, ½, ¾, top). The top value is rounded so the ticks are round numbers.
- **Right axis:** only on the API audit panel, for P95 in ms (drawn in the accent colour).
- **Panel title** (top-left inside the plot): names exactly what the panel plots, including the bar length, e.g. "Requests per 5 minutes, by status".
- **Gridlines:** horizontal at each tick; vertical at each x-axis tick.
- **X axis (bottom, shared by all panels):** up to 6 ticks labelled with date and time in the chosen zone, plus a caption: **"Time (UTC). Each bar covers 5 minutes, starting at the time on its left edge; every panel uses the same time axis."**
- **Grey dashes** along the baseline mark slots outside the window; **amber dashed outlines** mark bars clipped by a window edge. A clipped bar is geometry, not missing data.

### 5.2 Lane 1: "API requests (audit log)" (one panel)
Reproduces the analysis hero chart. [Owner: "I want that exact chart"] [Code: mirrors `heroChart`]
| Element | Meaning |
|---|---|
| Pale green bars (left axis) | Requests per bucket |
| Solid green line (right axis, ms) | P95 observed API time per bucket (API time = AuditCreateTime + ApiTime) |
| Grey dashed horizontal line, labelled "comparison period P95" | The P95 of the comparison period as one level, so you can see whether the line sits above it |

### 5.3 Lane 2: "Database (SQL Server)" (three panels)
| Panel | Plots | Why [Inference unless noted] |
|---|---|---|
| Slow calls (over 1 s) and errors per bucket | Stacked bars: amber = slow calls, rose = errors | Shows only the problem calls; total volume is in the cards |
| P95 call duration per bucket | Line, ms, with dashed comparison P95 | Duration trend on its own axis so it is not squashed |
| Blocked-process reports and deadlocks per bucket | Small bars, rose | Event strip. Deadlocks come from the always-on `system_health` capture; blocked-process reports need the owner to set a threshold |

Source of the data: SQL Server extended-event files, the error log and Query Store exports stored in the case. [Spec F-API-11; research]

### 5.4 Lane 3: "Web server (IIS)" (three panels)
| Panel | Plots | Source |
|---|---|---|
| Requests per bucket by status | Stacked bars: grey 2xx/3xx, amber 4xx, rose 5xx | [Spec D-3, F-API-09] status class |
| P95 time taken per bucket (as logged) | Line, ms, dashed comparison P95 | [Spec F-API-08]: time-taken is kept separate and the request start is **not** verified |
| HTTP.sys error rows per bucket | Small bars, rose | [Spec F-API-07]: reasons such as Timer_ConnectionIdle or Connection_Dropped |

Panel titles are plain English: *How many requests, and how slow they were* (API); *Slow database calls (over 1 s) and failed calls*, *How long database calls took (P95)*, *Database blocking and deadlocks* (SQL); *Web-server requests by result (OK / client error 4xx / server error 5xx)*, *How long web requests took (P95, as logged)*, *Web connection errors (HTTP.sys)* (IIS). Each lane's left column starts with a question: "Was the application slow or failing?", "Were database calls slow, blocked or failing?", "Were web requests slow or failing before reaching the app?". Lane keys and the tooltip still say "API audit".

### 5.5 Gaps (equal timeline for all lanes) and real zeros
Every lane is plotted across the **whole** window. Time with no log data is a **labelled hatched band**:
- Label text: "No logs for this time" + exact range + length, e.g. `13 Oct 12:00 → 20 Oct 08:00 · 6 d 20 h`. Bands narrower than about 7% of the width carry no text; the left-column line and the tooltip still say so.
- Sources of gaps: before a file's first row, after its last row, a hole between files, and the audit source's recording outage (19 Oct 02:00–04:00).
- P95 lines **break** at a gap instead of joining across it.
- A lane with no file at all is one hatched band over the same timeline: "No log data for the whole window".
- Hovering inside a gap reads "no log data for this time". [Owner request]
- **Zero events is not a gap.** A lane whose logs cover the whole window but contain no events draws normal panels with a flat zero baseline (no hatching) and the note "Logs cover this whole period and contain no events"; its tooltip says "no events". Hatching means only "unknown or unavailable". [Review fix 3.5]
- **Long gaps collapse.** When a lane has no log data for more than 85% of the window, it shrinks to one short panel (with a ribbon such as "Logs only for the last 4 h of this period") and a **Show all panels** button, on the same x axis. [Review fix 3.8]

### 5.6 Hover, selection, keyboard
- **Hover** any bar position: a tooltip lists the bucket's time range and one line per lane, e.g. `IIS: 118 requests (0 4xx, 6 5xx) · P95 time-taken 1100 ms · 1 HTTP.sys error`.
- **Click** (or Left/Right arrow, Home, End) selects a bucket; the selection outline spans all lanes.
- **Enter** or the button in ⑨ opens Run analysis for that bucket.

## 6. Worked example with real numbers

### 6.1 The slowdown, incident 09:00–11:30 vs comparison 06:30–09:00 (20 Oct)
Use prototype panel → **"The slowdown, with a real comparison"**, then Show timeline. [Code]

| Item | Value |
|---|---|
| Bar size / count | 5 minutes, 36 bars (6 end slots outside the window) |
| Warnings | none (windows touch but do not overlap) |
| API requests | 2,464 (1.0x the comparison rate) |
| API P95 | 505 ms, **2.2x** the comparison period's 228 ms (shown red) |
| API 5xx | 108 = 4.4% of requests (the earlier period had none, so no comparison) |
| IIS 5xx | 132 = 4.7% of 2,809 requests; no comparison |
| IIS P95 time taken | 1,050 ms; no comparison |
| SQL slow calls | 605 = 39% of 1,552 calls over 1 s; no comparison |
| SQL errors · blocking | 0 · 15 |
| Why "no comparison" for SQL and IIS | the files start at 08:00, so the 06:30–09:00 comparison period is only 40% covered |

**How to read it:** the API doubled its P95 while request volume stayed the same; the web server returned more 5xx and slower responses; SQL shows a large share of slow calls and 15 blocking/deadlock reports. All three lanes bend at **09:14**. The only lane with a usable comparison is API audit, because it keeps data from 13 Oct.

**Where does the evidence point?** **"The problem appears isolated to Production"** (`orders ×25.1`; `PayrollApp` flat; disk 4.9 ms and CPU 45% not saturated). See §8.

### 6.2 The default window (what a user sees first)
Last hour (11:00–12:00) vs 10:00–11:00. 60 one-minute bars. API 1,042 requests, P95 367 ms (0.7x). IIS 5xx 28 (0.5x). SQL slow calls 140 (0.5x). Ratios are **below 1** because the comparison hour was itself inside the slowdown (it ended 11:30). This is a good teaching moment: **the comparison period is only a comparison, not a baseline**.

### 6.3 Seven days (13 Oct 12:00 → 20 Oct 12:00)
Bars are 2 hours, 84 on the axis. API audit shows the daily rhythm and a hatched sliver for the 19 Oct outage. SQL and IIS show **"No logs for this time, 13 Oct 12:00 → 20 Oct 08:00, 6 d 20 h"** and only the last four hours drawn. 98% of those lanes' window is a gap, stated in each lane label.

## 7. Lane states (the sticker under the lane name)

| Sticker (new wording; internal state in brackets) | Meaning [Spec WS-18–25] | What is drawn |
|---|---|---|
| **Events found** (`events`) | Data seen in the window | Panels |
| **No logs** (`no-data`) | The case has no file for this source | Hatched band: "No logs cover this time" |
| **No events found** (`none-seen`) | Files cover the window and nothing happened in it | **Normal zero panels, not hatched** |
| **No logs cover this time** (`out-of-range`) | Files exist but not for this period | Hatched band |
| **Log time zone needs checking** (`clock-unchecked`) | A file has no validated time-zone rule | Hatched band; if more than one source is chosen **the whole chart is switched off** |
| **Some data could not be read** (`incomplete`) | Time/row budget exceeded, or a boundary scan went over 50,000 rows | Bars drawn, affected buckets hatched and flagged "not exact" |

## 8. "Where does the evidence point?" (⑩)

**Purpose.** One SQL Server or MongoDB connection can hold several environments. When something is slow, this card says where the evidence points: one environment, the whole server, or databases you did not pick. It states how strong the evidence is and says plainly when there is **not enough evidence**. It no longer claims a root cause or uses the word "Provable". [Review fix 3.3] [Owner; research round 7]

### 8.1 Controls
- **View evidence for** (segmented, formerly "Scope"): *This environment* (default), *All databases on this server*, *Server only*. A hint says it changes only what is shown below, not the conclusion. [Review fix 3.4]
- **Show their names / Hide their names** (in All databases): the share of databases you did **not** pick appears as one unnamed row; names appear only on request, with a warning to hide them before sharing an export. [Owner option (a)]

### 8.2 Verdict box
Each box shows: what we saw (observation), what it means, an evidence-strength chip (Strong evidence / Some evidence / Not enough evidence), and what to check next. Per-database evidence is capped at "Some evidence" when Query Store keeps steps longer than the window, and the box says so ("History … comes in 60-minute steps").
| Verdict (new title) | Rule [Inference; thresholds sourced as noted] |
|---|---|
| **The problem appears isolated to X** (`env`) | At least one picked database slowed (P95 ≥ 2x its comparison), no shared resource saturated |
| **The whole database server appears affected** (`server`) | Every picked database slowed (at least two are picked) and a shared resource is saturated |
| **Another database may be affecting yours** (`noisy`) | A picked database slowed, a shared resource is saturated, and databases you did not pick hold ≥ 50% of file I/O and ≥ 1.5x their earlier share |
| **There isn't enough evidence to narrow it down** (`blind`) | No picked database kept history for the window (Query Store off or full), **or** only some slowed under saturation, **or** only one database is picked and the server is saturated |
| **No clear performance problem was found in this period** (`quiet`) | Nothing slowed and nothing saturated (strength "Some evidence": absence of a problem is not proof) |
| **The server is under pressure, but your databases do not appear unusually slow** (`server`) | Saturation without a slowed database |

Saturated means **disk latency ≥ 15 ms** (Microsoft: sustained 10–15 ms is an I/O bottleneck) **or CPU ≥ 80%** (Microsoft guidance for Managed Instance, applied to every edition by inference). The not-enough-evidence box lists **What you can still do** (review the API and web-server lanes, inspect the logs that exist) and **For the next incident** (switch on Query Store, start recording server counters).

### 8.3 Tables
1. **The server as a whole** (instance-wide; cannot be split by database): disk latency, CPU, memory grants waiting, top wait. Every block carries a **Source** line (in the demo: "Blackbox sampler, running in this demo since 19 Oct"); with no sampler it shows no numbers and says so. Wait counters count from the last restart, so only differences between samples mean anything. [Review fix 4.3]
2. **By database:** database, environment, P95 incident vs comparison, change (×), share of file I/O (comparison in brackets), a **Numbers from** column (stored SQL events, the same figures as the SQL lane, or Query Store history; when the lane's comparison is withheld and Query Store is off the row says "cannot compare"), a **history chip** (Query Store on · 60 min steps / off / full). In All databases an unnamed **Other databases** row shows the unpicked share.
3. **MongoDB:** queued operations and replication lag (server signals), then per database slow operations per hour and a profiler chip. Notes: cache and connections are shared by all databases; the server's own diagnostic capture is a file on disk that cannot be read over a connection; on MongoDB 8.0 the slow-query threshold ignores time spent waiting for locks.
4. A **Coarse** note when the window is shorter than a Query Store step: the card can only say what happened in the step that contains the window.
5. **Rules used (defaults, not universal laws):** every threshold, tagged "from vendor guidance" or "prototype default", with a note that they may differ by platform and version and would be adjustable in the product. [Review fix 4.5]

**Five prepared situations** (prototype panel): One environment, Whole server, Noisy neighbour, Nothing kept, A quiet window. Where the SQL lane has a stored comparison the card uses the lane's own figures (one evidence model); server counters and databases without lane evidence come from the scenario table and are labelled with their source.

## 9. ⑪⑫ Collapsed details and exits

| Section | Content |
|---|---|
| **What each lane shows** | Per lane: which files were read and what the importer takes from them; how the lane is drawn |
| **How this axis was worked out** | The start/end you chose; axis start/end (whole UTC hours); the bucket size the window asks for; the smallest size that keeps the axis at ≤ 120 bars; the larger of the two (the one shown); the **boundary raw scans** (0, 1 or 2) and why |
| **Timeline details** | Flags: `allLanesStored`, `mixedConsistency`, `requestSeq`; per lane: origin (live/case), consistency, complete, as-of time, sources. A warning when `allLanesStored` is vacuously true with nothing stored |
| **Open the analysis** | Opens Run analysis with the current windows |
| **Export** | Opens the Export drawer (preview and confirm stamped with the case revision) |

**Hand-off to Run analysis (changed).** Run analysis needs the comparison period to end before the selected period starts and to be at most 7 days. When a bucket (or the whole period) is opened and the comparison period does not meet that, **Unravis moves it to just before the selected period** and the analysis banner says so, with the original range. **Open the analysis** is switched off for a period longer than 7 days, with the reason shown. The analysis screen itself still refuses an invalid range typed there. [Prototype decision; was open decision 3/4]

**Bucket-size ladder** [Spec §4.5, R6-02]: window ≤ 2 h → 1 min; ≤ 24 h → 5/10/15 min (smallest that keeps ≤ 120 bars); ≤ 5 d → 1 h; ≤ 10 d → 2 h; ≤ 20 d → 4 h; ≤ 30 d → 6 h; the axis is then rounded out to whole UTC hours and the size is raised to the smallest standard size that keeps ≤ 120 bars. A 30-day window that starts off the hour becomes 8-hour bars (91 on the axis).

**Boundary scans** [Spec §4.5, §7]: both window edges on a UTC hour → 0 raw scans; exactly one edge off the hour, or both inside the same hour → 1; both off the hour in different hours → 2.

### Layout variants (prototype choice; pick one)
- **A Report:** everything in one column, top to bottom (as mapped above).
- **B Workbench:** left side panel with the windows, range chips and sources; right side with figures, a taller chart, then bucket panel and details side by side.
- **C Lane ledger:** a table with one row per lane (name, trust stickers: state, live/stored, consistency, complete, as-of time, comparison; event count; sparkline). Opening a lane row shows its full panels.

## 10. Where the numbers come from (provenance)

| Layer | Origin |
|---|---|
| Lanes, window rules, bucket ladder, comparison naming, hand-off, mixed-consistency note, lane states, export stamp | [Spec] (ids above) |
| Three charts' specific panels, stat cards, gap bands, one-connection-many-databases, "Where does the evidence point?", "What Unravis sees" | [Owner] requests, built by the prototype |
| 30-event and 80%-coverage rule, 1.5x red, 2x slowed, 1.5x neighbour share, verdict combination | [Inference] |
| Disk ≥ 15 ms, CPU ≥ 80%, Query Store/profiler/log facts | Vendor documentation read in the owner's separate research (not re-opened by the prototype author) |

## 11. Known weak spots (after the review fixes)

Fixed since the first version of this document: API 5xx shown as 0 (the demo now shows 108 5xx against IIS's 132); P95 presented as exact (now "estimated"); attribution numbers disagreeing with the lane figures (one evidence model for the SQL database with stored events); server counters without a source; "Provable" wording; the misleading "Scope" label; zero events drawn like missing data; large hatched areas on long windows; hand-off landing on a refusal.

Still open, please test:
1. **Estimated P95** is still a call-weighted mean of per-minute P95s in the prototype; only the label changed. [Code]
2. **The comparison period is not a baseline.** In the default window both periods overlap the slowdown, so ratios below 1 read as "improvement". [Code]
3. **API 5xx comparison says "Cannot compare" when the earlier period had none.** "None in the earlier period" would be more accurate. [Code]
4. **Only the `orders` database has lane evidence.** Other picked databases and all server counters come from a scenario table, labelled as such; the demo cannot show a fully real model. [Code]
5. **Server-wide signals in MVP-1** still need a live read (MVP-2) or a sampler (Blackbox); the demo assumes a sampler. [Owner question]
6. **The 80% CPU rule** is documented for Managed Instance and applied to every SQL Server edition by inference. [Inference]
7. **Hiding other databases' names is display-only.** [Research]
8. **Provenance lines (live/stored, as-of time)** are in the collapsed Timeline details. [Code]
9. **Colour is the only carrier** of the 2xx/4xx/5xx split in the IIS bars (the legend names them). Accessibility not audited. [Code]
10. **Layout:** Report (A) is still the default; Workbench (B) was recommended by the review but not validated with users, so the default was not changed. [Owner decision]
11. **Terminology compromises:** "Investigation window" and "API requests (audit log)" keep the spec/source terms in brackets; renaming them outright would split the vocabulary between the Investigation Timeline and Run analysis. [Owner decision]
12. **The reading aid's layer thresholds** (1.5 times, 2%, 25%) are prototype defaults. [Inference]

## 12. Review checklist (for the other agent)

- [ ] For each card in §4: recompute the value from the generator functions (`metricMinute`, `auditWindow`, `laneWin`) and confirm the card matches.
- [ ] Verify every panel has a titled y-axis, ticks, a legend entry, and a stated bar length, at 15 min, 1 h, 24 h, 7 d and 30 d.
- [ ] Confirm all three lanes use the same x positions for the same time (resize the window; hover across lanes).
- [ ] Confirm gaps: SQL/IIS before 08:00 on 20 Oct; audit outage 19 Oct 02:00–04:00; no line joins across a gap.
- [ ] Try each blocking and warning case in §3 ④ and the lane states in §7.
- [ ] Check wording: any sentence that claims proof, certainty or that the software "works".
- [ ] Check §8 rules against the research claims (see `FLOW-COVERAGE.md` rows 64a–64e and decisions 11–15).
- [ ] Read §11 and say which items you agree are defects.

## 13. Video outline (about 5–6 minutes)

Recording notes: use a 1440x900 window; hide the bottom dock ("Hide"); load situations from the prototype panel before recording each scene. Show the words "prototype" and "synthetic data" in the first and last scenes. **Do not** say the tool "proves", "finds the root cause", or "works".

| # | Scene (≈ time) | On screen | Narration (draft) |
|---|---|---|---|
| 1 | The problem (0:30) | Case home → Investigation Timeline block, press **Open the Investigation Timeline**, then **Show timeline** | "When production slows down you have three places to look: the API audit log, the web server and the database. The Investigation Timeline puts them on one time axis." |
| 2 | Choose the period (0:45) | Range chips; open the incident picker; show the reference card | "Pick the period you care about. Unravis also picks a comparison period: by default the equal period just before. It is only a comparison, not a promise that it was healthy." Show overlap notice and the 30-day limit. |
| 3 | Show timeline (0:30) | Press **Show timeline**; summary line | "Each bar is one bucket. Here each bar is five minutes. Longer windows use bigger bars so the chart stays readable." |
| 4 | What Unravis sees, then the figures (1:00) | Scenario "The slowdown, with a real comparison"; start with the reading aid, then pan across the 7 cards | Use the §6.1 numbers: "The reading aid says the application is slower, the web server is returning errors and the database has many slow calls. The slow-request time roughly doubled: about 505 milliseconds against 228. IIS returned 132 server errors. SQL shows 605 calls over one second and 15 blocking reports. Where the comparison period isn't covered, the card says 'no comparison' instead of guessing." |
| 5 | Read the chart (1:15) | Scroll the board; point at axes, legend, panel titles | "Every panel says what it plots. The API chart is the one you know from the analysis: bars are requests, the line is P95, the dashed line is the comparison period. SQL shows slow calls and errors, call duration and blocking. IIS shows status codes, time taken and HTTP.sys errors. All panels share one time axis." |
| 6 | Hover and drill (0:30) | Hover, click a bar, press **Open Run analysis for this bucket** | "Hover to read one moment across all three sources. Click a bar to open the analysis for exactly that bucket." |
| 7 | Gaps (0:40) | Range chip **Last 7 days** | "Seven days means seven days for every source. Where a source has no logs, the chart says so, with the exact range, instead of drawing zeros." |
| 8 | Where does the evidence point? (1:00) | Load each situation: One environment → Whole server → Noisy neighbour → Nothing kept | "One server can host several environments. This card says where the evidence points: one environment, the whole server, or a neighbouring database, how strong that evidence is, and when there isn't enough, what to switch on." Show the **Other databases** row staying unnamed until you ask. |
| 9 | Close (0:20) | Open the collapsed details; show **Export** | "Everything here is prototype data. Open the details to see how the axis was worked out and what each lane read." |

**Fact sheet for narration (verified in the prototype):** 2,464 API requests; API P95 (estimated) 505 ms vs 228 ms (2.2 times); API 5xx 108 (4.4%); IIS 5xx 132 (4.7% of 2,809); SQL slow calls 605 (39% of 1,552); blocking/deadlock reports 15; slowdown 09:14–11:30; reading aid: Application slower than usual, Web server returning errors, Database many slow calls and blocking; evidence points to "The problem appears isolated to Production" (`orders` ×25.1), Strong evidence.

## 14. Reproduce the screens

| Screen | How |
|---|---|
| Default timeline | Open the HTML, open the case "Payroll.Run slowdown, 20 Oct", press **Show timeline** |
| Slowdown with comparison | Prototype panel → Investigation Timeline · lanes → "The slowdown, with a real comparison" |
| Attribution scenarios | Prototype panel → Investigation Timeline · where does the evidence point |
| Seven days | Range chip **Last 7 days**, **Show timeline** |
| Screenshots in `prototype/screenshots/` | `08b` trace board (2.5 h), `08c` seven days with gaps, `13` narrow, `14a`–`14f` attribution and case-home databases |
| Automated checks | `NODE_PATH=/opt/node22/lib/node_modules node prototype/flow-check.cjs` (150 checks; they test the prototype, not the product) |

Source locations in `tracing-paper-PROTOTYPE.html`: `doTrace` (lane data), `axis`/`ladderWidth` (axis), `chartHTML`/`lanePanels`/`panelSVG` (charts), `traceStats` (cards), `attrModel`/`attrVerdict`/`attrCard` (card ⑩), `tracingChecks` (rules).
