# Unravis tracing paper: spec for review

## 0. One-paragraph summary (what it is, what MVP includes, what it excludes)

**Tracing paper** is a case-first investigation surface: add data sources, press **Start tracing**, then read **one shared timeline** with three lanes—**API audit** (Mongo audit), **SQL**, and **IIS**—on a single time axis. The owner asked to reduce scope: no new guided-step catalogue, no recurrence heatmaps, no evidence-card grids, and no replacement of the whole app shell in MVP. MVP is **sources → trace → one chart → drill-down to what already exists** (Run analysis, record detail, patterns). Out of scope for MVP: host/Blackbox lanes, deploy markers as a lane, call-trace tray UI, 11-step guided redesign, wireframe recurrence, and SQL live health charts as timeline lanes.

## 1. Goal and non-goals (table: In MVP | Later | Never)

| In MVP | Later | Never |
|--------|-------|-------|
| Create/open case; add source tags and import or read audit/SQL/IIS into the case | Recurrence selector, calendar heatmap, occurrence overlay (wireframe addendum) | Unravis running write queries against customer DBs from the tracing UI |
| **Start tracing** runs one read-only job for the chosen incident window | Full 14-step guided rail redesign (layout spec variant A/B/C) | Claiming causation on the timeline (join/timeline packages forbid this today) |
| **One timeline** with API audit + SQL + IIS lanes, shared UTC axis, labelled bucket size | Host CPU/memory lane, event-log markers, per-call trace overlay on the main chart | A single merged “story” score or auto culprit |
| Zoom/hover; click-through to existing **Run analysis** / **Evidence** / **record** views | Call trace UI wired from Evidence; deploy/release marker lane | Replacing case ledger, checklist, or export with tracing-only flows |
| Empty/missing lane states in plain words | Evidence cards on steps 7–9 (wireframe); dependency banners as timeline gates | Broad live SQL/Mongo scans without owner action (409 `broad_scan_refused` pattern stays) |
| Reuse case store events + audit aggregates; read-only guarantee | Estimate-before-run bar, background cancel (research doc patterns) | Storing raw secrets or payloads in tracing-specific APIs |

## 2. Where the app stands today (table: Capability | Status | Evidence | Matters for tracing because)

| Capability | Status | Evidence | Matters for tracing because |
|------------|--------|----------|------------------------------|
| Case create + **Sources** step (tags, connections) | Working | `web/src/components/CreateWizard.svelte` (per inputs, unverified); `internal/server/case_sources.go:22-38` | Tracing needs tagged Mongo/SQL/IIS/imports before run |
| Import IIS W3C logs into case events | Working | `internal/importer/iis/w3c.go:33-39`, `:175` | IIS lane plots `iis-w3c` events |
| Import SQL-family events (`sql-*` importers) | Working | `internal/calltrace/lanes.go:42-53` | SQL lane groups `sql-*` sources |
| **Run analysis** (Pulse/Where/Evidence), `POST /api/analyze` | Working | `internal/server/routes.go:192`; `web/src/App.svelte:112-131` (per inputs errata) | Audit lane buckets and drill-down already exist here |
| Audit timeline charts (P95, volume) per API | Working | `web/src/components/Pulse.svelte:68-80`; `web/src/components/Charts.svelte:44-47` | Reuse bucket logic, not a second audit chart product |
| **MultiTimeline** (compare APIs, audit only) | Working | `web/src/components/MultiTimeline.svelte:33-39` | Shows audit-only multi-series; not SQL/IIS |
| Case **text timeline** (interval ↔ overlap rows) | Partial | `internal/timeline/timeline.go:201-285`; `internal/server/case_views.go:93-145` | Backend joins audit intervals to imports; **no chart UI** for owner’s “one-shot view” |
| **Call trace** lanes (audit + SQL + IIS + …) | Partial | `internal/calltrace/lanes.go:210-282`; `internal/server/call_trace.go:18-21` | Lane model matches tracing lanes; **UI Not wired** |
| Call trace in the product UI | Not wired | `web/src/components/CallTrace.svelte:1-15`; `addToTray` only in `web/src/lib/callTraceTray.svelte.ts:21` (no callers) | Backend ready; users cannot open it |
| Unified API+SQL+IIS **one top-level chart** | Not built | Part 4 errata + `inputs/2026-10-30-current-app-flows.md` errata | Explicit product gap |
| Guided 11-step investigation | Working | `web/src/lib/guided/stepModel.ts:9-24`; `internal/server/investigation.go:17-29` | Separate surface from Run analysis; not the tracing entry |
| Guided tabs: Checklist, After fix, Nearby, Export | Working | `web/src/lib/components/guided/GuidedAnalysis.svelte:27-62` | Compete for attention; MVP tracing should not depend on them |
| Nearby activity (audit rows only in UI) | Partial | `web/src/lib/components/guided/NearbyActivity.svelte:37-38` (`api.activity`) | SQL/IIS lanes say “Cannot test yet” in copy; not a full lane chart |
| Time join overlap engine | Working | `internal/join/join.go:48-124` | Feasible for correlating events to windows |
| Clock / TZ validation for multi-source | Working | `internal/timeline/timeline.go:309-379` | Tracing must refuse or warn when lanes cannot align |
| Event read cap | Working | `internal/casestore/store.go:101-102` | Limits SQL/IIS density in window |
| Collect suggestions (“what to collect next”) | Working | `internal/server/collect.go:22-26` | Helps “add sources” before tracing |
| IIS `time` field semantics | Unknown | `internal/importer/iis/w3c.go:14-21` (`w3cTimeIsEnd`, UNVERIFIED) | Misalignment risks IIS lane vs audit |

## 3. The new flow (numbered steps the user sees)

### 1. Create or open a case

| | |
|--|--|
| **User** | Start screen → create wizard (Name → Sources → Review) or open existing case. |
| **App** | Creates case folder; optional source tags; no tracing until data exists. |
| **Can go wrong** | Locked master key (423); closed case read-only; failed imports listed under “Needs another try”. |

### 2. Add sources (and data)

| | |
|--|--|
| **User** | Tag Mongo audit DB, SQL connection, import IIS/discover logs, or read a lane day—same as today’s Sources / Add data / Import. |
| **App** | Tags via `POST /api/cases/{id}/sources`; imports via import jobs; events land in encrypted `case.db`. |
| **Can go wrong** | Mongo DB name not in recent test (`case_sources.go:36-37`); removing tag does not delete events (`:38`); IIS file missing `time-taken` → no durations (`iis/w3c.go:27`). |

### 3. Open tracing paper

| | |
|--|--|
| **User** | From case home, open **Tracing** (new primary action alongside Run analysis / Guided). |
| **App** | Shows incident/reference window controls (reuse Step 0 frame limits: 7-day span `web/src/lib/guided/frame.ts:23-25`), source checklist (which lanes can run), **Start tracing** button. |
| **Can go wrong** | No audit source → audit lane disabled; only one importer kind → timeline builder today disables (`timeline.go:365-368`). |

### 4. Start tracing

| | |
|--|--|
| **User** | Set windows (UTC), optional route filter; press **Start tracing**. |
| **App** | Read-only: loads audit buckets for window (`audit` query limits `internal/audit/query.go:36-40`, `:77`), loads case events for SQL/IIS importers, aligns on UTC; returns lane series + warnings (truncation, TZ, overlap). |
| **Can go wrong** | >50k events truncated (`timeline.go:198`, `casestore/store.go:101-102`); reference overlaps incident (wireframe sanity—should warn, not block per research doc); query >7 days rejected (`audit/query.go:77`). |

### 5. Read the one-shot timeline

| | |
|--|--|
| **User** | Scroll/zoom one chart; read lane legend; see bucket label (“5 min · counts + p95”). |
| **App** | Renders stacked lanes: **Audit** (request volume + p95 or error rate), **SQL** (wait/event counts or duration from imported events), **IIS** (request count + status-class stack or rate). Missing lane → gray strip + reason. |
| **Can go wrong** | Clock unchecked lane state (`calltrace/lanes.go:270-271`); IIS one-second resolution (`iis/w3c.go:28-30`); empty window → “Cannot test yet” messaging. |

### 6. Drill down

| | |
|--|--|
| **User** | Click interval or marker → existing surfaces. |
| **App** | Deep-link to Run analysis with window prefilled (`POST /api/analyze`), Evidence list, or single record; optional future link to call trace for one call. |
| **Can go wrong** | Stale analysis if windows differ (export already warns `stale`—same pattern). |

## 4. Requirements (table: ID TR-01.. | Requirement | Priority | MVP? | Evidence or rationale)

| ID | Requirement | Priority | MVP? | Evidence or rationale |
|----|-------------|----------|------|------------------------|
| TR-01 | Case home offers **Tracing** entry when the case can hold investigation work | Must | Yes | Case home already has analysis + guided buttons `CaseHome.svelte:452-463` |
| TR-02 | Tracing UI collects incident + reference windows with same validation as guided frame (≤7 days UTC) | Must | Yes | `frame.ts:23-25`; `investigation.go:25-26` |
| TR-03 | **Start tracing** is one primary action; read-only server work | Must | Yes | Investigation route read-only comment `investigation.go:17-21` |
| TR-04 | Response includes three lane keys: **audit**, **sql**, **iis** | Must | Yes | Owner verbatim; lane prefixes `calltrace/lanes.go:42-53` |
| TR-05 | Shared **UTC** time axis across lanes; UI may display local via existing toggle | Must | Yes | `TimeZoneToggle` (per inputs); events stored UTC `timeline.go:173-174` |
| TR-06 | Label **bucket size** on chart from window length (auto coarsen) | Must | Yes | Research doc §1; audit `MaxTimeBuckets=120` `audit/models.go:17` |
| TR-07 | Missing lane: show fixed reason, not zero data | Must | Yes | Lane states `calltrace/lanes.go:266-275`; timeline disabled reasons `timeline.go:46-49` |
| TR-08 | Warn when reference overlaps incident; offer one-click fix | Should | Yes | Wireframe addendum GL-24 (per inputs); non-blocking per research §4 |
| TR-09 | Click interval → open Run analysis with matching window | Must | Yes | `routes.go:192`; depth model `App.svelte:112-131` |
| TR-10 | Respect 50k event cap; surface **truncated** flag | Must | Yes | `timeline.go:198`; `call_trace.go` truncation pattern |
| TR-11 | Require validated TZ rules before multi-source trace | Must | Yes | `timeline.go:362-378` |
| TR-12 | Do not wire Call trace tray in MVP | Must | Yes | UI not wired errata |
| TR-13 | Tracing API must not execute grant scripts or imports | Must | Yes | Collect read-only `collect.go:22-26`; tags don’t read `case_sources.go:16-19` |
| TR-14 | Optional route/API filter on audit lane only | Should | Later | Frame route field exists `frame.ts:14-15` |
| TR-15 | Host / Blackbox fourth lane | Later | No | Owner scoped out |
| TR-16 | Hourly rollup note when window >24h | Should | Yes | Addendum GL-29 (per inputs) |
| TR-17 | Persist tracing window on case workspace | Should | Later | Workspace route exists (per inputs Part 5) |
| TR-18 | E2E: tracing with demo case shows three lanes | Must | Yes | New scenarios; demo lanes `demo_lanes.go` (per inputs) |

## 5. The one-shot timeline chart

### Lanes (MVP)

| Lane | Plots | Source data |
|------|-------|-------------|
| **API audit** | Stacked or dual axis: **request count** per bucket; **P95 latency** (ms) line; optional **4xx/5xx** tick marks | Live or imported Mongo audit via existing analyze buckets (`audit/analyze.go:142-210`) |
| **SQL** | **Event count** per bucket by severity/kind; optional **total wait ms** bar for `wait` kinds | Case events, importers matching `sql-*` (`calltrace/lanes.go:44-45`) |
| **IIS** | **Request count** per bucket; **status class** (2xx/4xx/5xx) stacked bars | Case events `iis-w3c` kind `request` (`iis/w3c.go:175`) |

### Shared time axis

- **Storage/compute:** UTC (`timeline.go:173-174`).
- **Display:** Existing UTC/Local toggle (per inputs Part 5).
- **Skew:** MVP uses join tolerance **1s resolution only**—no automatic skew correction (`timeline.go:156-158`; `join.go:19-23`). If sources lack validated TZ rules, tracing stays disabled with reason (`timeline.go:370-378`).
- **IIS caveat:** 1-second log resolution; order within a second unknown (`iis/w3c.go:28-30`).

### Bucket size by window length

| Window length | Suggested bucket | Rationale |
|---------------|------------------|-----------|
| ≤2 h | 60 s (or audit default) | Fits ≤120 buckets (`audit/models.go:17`) |
| 2 h–24 h | 5–15 min | Coarser to stay under cap |
| >24 h | 60 min | Matches wireframe hourly note (addendum GL-29); label “hourly averages” |

Bucket bounds already enforced: 10 s–86400 s (`audit/query.go:36-37`).

### Lane empty states

| Condition | User sees |
|-----------|-----------|
| No importer / tag for lane | “This case has no **SQL Server** / **IIS** source.” (align guided copy) |
| Importer present, no events in window | “Nothing recorded in this window.” |
| Clock rule missing | “Time zone rule not checked for **IIS log**.” (`timeline.go:49`) |
| Events truncated | “Only part of the events were read.” (`timeline.go:58`) |
| IIS logs without durations | Lane shows counts only; footnote on `time-taken` gap (`iis/w3c.go:27`) |

### Interactions

- **Zoom:** Reuse `zoomView` / brush pattern from Pulse (`Pulse.svelte:25-26`).
- **Hover:** Bucket time range, counts, p95, SQL/IIS event totals.
- **Click:** Set investigation window → navigate to Run analysis depth 0–2 (`App.svelte:112-131`).
- **Limits:** 7-day window; 120 buckets; 50k events; call-trace lane event cap 60 (`calltrace/types.go:91-92`) if reuse lane builder for detail drawer later.

## 6. Chart feasibility (table)

| Candidate chart | Sources | Data exists now? | Evidence | Effort | MVP/Later | Risk |
|-----------------|---------|------------------|----------|--------|-----------|------|
| Request volume / P95 lane (audit) | Mongo audit | Yes | `audit/analyze.go:194-210`; `Pulse.svelte:68-80` | S | MVP | Histogram approximation warning already in analyze |
| SQL wait / blocking lane | sql-xe, sql-errorlog, etc. | Partial | Events in case store; not live dm_exec series in timeline | M | MVP | Point-in-time imports only (feasibility doc #1-6 partial) |
| IIS request / status lane | iis-w3c | Partial | Importer Working; time semantics unverified `iis/w3c.go:14-21` | M | MVP | Alignment vs audit |
| Error markers (4xx/5xx) | Audit | Yes | Timeline failure series `audit/analyze_test.go:465+` | S | MVP | |
| Slow-call markers | Audit | Yes | Slowest API / records via `/api/slowest` | S | Later | Overlay clutter |
| Deploy / release markers | Step 0 change list | Partial | Frame releases `frame.ts:19`; not on chart today | M | Later | Needs user-entered times |
| Host CPU / memory lane | Blackbox, win-counters | Partial | `resource/metrics.go` (per feasibility #22-23) | L | Later | Blackbox placement |
| Event-log markers | win-events | Partial | Importer exists `timeline.go:85` | L | Later | |
| Per-call trace overlay | Audit + SQL + IIS | Partial | `call_trace.go` + `BuildLanes` | M | Later | UI Not wired |
| Multi-API compare lines | Audit | Yes | `MultiTimeline.svelte` | — | Later | Not owner MVP |
| Text overlap timeline | Imports + audit intervals | Yes | `timeline.Build` | S | Later | Not a chart; different UX |
| SQL live wait heatmap | SQL Server live | No | Feasibility #8-9 no | L | Later | Permissions |

## 7. What happens to existing features

| Existing feature | Keep / Fold / Hide / Retire | Reason |
|------------------|----------------------------|--------|
| Guided 11 steps | **Hide until Later** for tracing users; Keep for checklist-minded workflows | Owner MVP is tracing-first; steps remain Working |
| Manual tools (13) | **Keep** | Independent; link from tracing drill-down optional Later |
| Run analysis (Pulse/Where/Evidence) | **Fold into tracing** as drill-down | Primary audit visualization today |
| Guided **Nearby activity** tab | **Fold** concept into tracing lane overlap Later; tab **Keep** short term | Tab still in `GuidedAnalysis.svelte:27-62` |
| Patterns drawer | **Keep** | Episodes still valuable after tracing spots spike |
| Call trace | **Keep** backend; **Hide** UI until wired | Not wired errata |
| Checklist | **Keep** | Case data writes OK |
| After the fix | **Keep** | Not on tracing chart MVP |
| Export / report | **Keep** | No change |
| Saved views | **Keep** | Manual tools |
| Comparison table | **Keep** | Reference period analysis |
| Ledger | **Keep** | Unrelated |
| Coverage / collect panels | **Keep** | Feeds step 2 “add sources” |
| Wireframe guided layout (rail 0–13, recurrence) | **Drop** from tracing MVP | Owner revise; addendum features → Later backlog |
| Wireframe evidence cards steps 7–9 | **Later** | Not tracing timeline |
| Case timeline POST (text rows) | **Keep** API; **Later** UI surfacing | Different from chart MVP |

## 8. Data, API and architecture notes

| Topic | Note |
|-------|------|
| **Reuse** | `POST /api/analyze` for audit buckets; `casestore.Case.Events` for SQL/IIS; lane grouping `calltrace.LaneOf`; TZ gate `timeline.disabled`. |
| **New** | `POST /api/cases/{id}/tracing` (name TBD) returning `{ window, bucketSeconds, lanes[], warnings[], truncated }`—or extend `/timeline` with `mode: chart` (Open decision). |
| **Read-only** | Same as investigation: no case mutation except optional saved window Later. |
| **Privacy** | No payloads in tracing response; scrub like export/report. |
| **Performance** | Reuse `requestTimeout`; cap events 50k; cap buckets 120. |
| **Stored in case** | Imports + tags already; tracing run itself not stored in MVP (recompute on refresh). |

## 9. Acceptance criteria and test outline

| ID | Given / When / Then | TR |
|----|---------------------|-----|
| AC-01 | Given a case with Mongo audit + `iis-w3c` import, When user starts tracing for a 1h window, Then audit and IIS lanes render and SQL lane shows “no source”. | TR-04, TR-07 |
| AC-02 | Given only audit source, When tracing runs, Then SQL and IIS lanes show missing-source text, audit lane has points. | TR-07 |
| AC-03 | Given windows >7 days apart invalid span, When user starts tracing, Then 400 with plain error (same as analyze). | TR-02 |
| AC-04 | Given reference overlapping incident, When windows set, Then non-blocking overlap notice appears. | TR-08 |
| AC-05 | Given 50k+ events in window, When tracing completes, Then `truncated: true` and footnote. | TR-10 |
| AC-06 | Given import missing TZ validation, When tracing runs, Then chart disabled with `tz-rule` reason. | TR-11 |
| AC-07 | Given successful trace, When user clicks a bucket, Then Run analysis opens with that sub-window. | TR-09 |
| AC-08 | Given closed case, When opening tracing, Then Start tracing disabled read-only. | TR-03 |
| AC-09 | Given demo case, When tracing runs, Then bucket label visible and ≤120 buckets. | TR-06 |
| AC-10 | Given window >24h, When chart renders, Then hourly averages footnote shown. | TR-16 |
| AC-11 | Given master key locked, When tracing API called, Then 423. | TR-13 |
| AC-12 | Given tracing response, When inspected, Then no connection strings or payloads. | TR-13 |
| AC-13 | Given SQL imports in window, When tracing runs, Then SQL lane event counts >0. | TR-04 |
| AC-14 | Given IIS import without time-taken, When tracing runs, Then IIS lane counts only + gap note. | TR-07 |
| AC-15 | Given user on tracing, When Call trace tray searched, Then no navigation to CallTrace (MVP). | TR-12 |

**Test outline**

| Layer | What |
|-------|------|
| **Unit** | Bucket chooser; lane aggregation from fixture events; empty-state messages. |
| **API** | New tracing endpoint: validation, truncation, TZ disable, three lane keys. |
| **UI e2e** | Case with fixtures → open Tracing → Start → see lanes; click → analyze depth. |
| **Manual-only** | Real IIS file alignment; real SQL XE import density; SRC-12/13 style servers. |

## 10. Later backlog (ordered)

1. Wire **Call trace** UI to Evidence / tracing click (`CallTrace.svelte` exists).
2. Fourth **Host** lane (Blackbox / win-counters).
3. **Release/deploy** markers from frame change list.
4. Tracing **estimate line** before run (addendum GL-31).
5. **Recurrence** panel on window picker (addendum GL-32–37).
6. Guided **layout spec** variant A (rail 0–13) if owner still wants it—separate from tracing.
7. **Evidence cards** on SQL/Mongo/Host steps (wireframe GS-40+).
8. Background job + cancel for long traces (research §5).
9. Persist last tracing window on case workspace.
10. **Text timeline** view toggle (existing `timeline.Build` rows).

## 11. Open decisions

| ID | Question | Options | Recommendation | What breaks if wrong |
|----|----------|---------|----------------|----------------------|
| D-1 | One combined chart vs **swim lanes**? | A) Single canvas B) Stacked lanes (recommended) | **B** — matches call trace / owner “lanes” | Unreadable overlap on one axis |
| D-2 | Do **guided steps** stay primary for some users? | A) Tracing replaces guided entry B) Both equal C) Tracing primary | **C** on case home; tracing MVP does not delete guided | Confusion for checklist workflows |
| D-3 | **IIS in importer today**—enough for lane? | A) Counts only B) Counts + status + duration when `time-taken` | **B** with footnotes | False confidence on latency |
| D-4 | **Time window** max for tracing? | A) 7 days (audit limit) B) Shorter default 24h | **A** hard cap; default 1h incident | Server timeouts |
| D-5 | Audit lane from **live Mongo** vs case import? | A) Live only B) Case audit rows only C) Both prefer live | **C** align with Run analysis source picker | Empty lane when only imports |
| D-6 | New route vs extend **`/timeline`**? | A) `POST .../tracing` B) Extend timeline body | **A** clearer contract | Client breaks if conflated with text timeline |
| D-7 | SQL lane metric? | A) Event count B) Sum wait ms C) Both toggle | **A** MVP; **B** Later | Misleading if waits incomplete |

## 12. Risks and unknowns (ranked)

| Rank | Risk | Mitigation |
|------|------|------------|
| 1 | **IIS timestamp semantics** unverified (`iis/w3c.go:14-21`) | Label lane “approximate”; test on owner IIS files |
| 2 | **Two UIs** (tracing vs Run analysis) diverge | Tracing click-through must open same analyze query |
| 3 | **50k event cap** hides SQL/IIS spikes | Show truncated + link to import more / narrow window |
| 4 | **No live SQL** in MVP lane—imports only | Copy: “SQL lane shows imported logs, not live DMV” |
| 5 | **TZ skew** not auto-corrected | Disable multi-source until rules validated |
| 6 | Scope creep from **wireframe** (recurrence, evidence cards) | Explicit Later backlog; owner sign-off on MVP table |
| 7 | **Call trace** expectation when UI absent | Do not advertise until wired |
| 8 | Performance on **7-day** trace | Enforce bucket coarsening + timeout |

---

**Guided layout spec (revise, not implement in tracing MVP)**

| Part | Action |
|------|--------|
| Single back button, pane scroll contract | **Keep** when/if guided layout ships |
| Steps 11–13 in rail, remove top tabs | **Later** (layout spec §4) |
| Recurrence heatmap / overlay on step 0 | **Later** (addendum B) |
| Step 5 nearby swim-lane + suspect cards | **Later**; tracing MVP covers “one timeline” instead |
| Evidence card grid steps 7–9 | **Later** |
| Manual tools layout MT-L* | **Keep** as-is; no tracing dependency |
---

# Review errata (independent fact-check, job 31843; overrides the spec above where they differ)

Orchestrator verification of the two disputed claims:
- **Call trace UI: the spec is right, the reviewer is wrong.** `grep CallTrace|addToTray` over `web/src/**/*.svelte` finds only `CallTrace.svelte` itself; no component imports it. Keep "Not wired".
- **Case text timeline UI: the reviewer is right.** `web/src/lib/components/CaseTools.svelte:8,155-158` renders `Timeline.svelte` in the Import panel. Section 2/7 must say "text timeline has a UI; only the combined chart is missing".
- Other findings (live SQL/Mongo health lanes missing from section 6 and D-7; `/api/analyze` returns one series per cohort so the audit lane needs aggregation; `LaneEvent` has no HTTP status; demo has no case events so AC-09/TR-18 need a seeded demo case; "case audit rows" option in D-5 is not a real path; several citations a few lines off) are unverified by me; treat them as open items for the owner review.

## Verdict

**Needs fixes.** The spec's product scope matches the owner's words well (sources → trace → one API-audit/SQL/IIS timeline; host and deploy moved to Later), and most backend citations check out. But three load-bearing status claims are wrong or stale, and two Open Decisions rest on facts that are not true in the code: **Call trace is in fact wired into the product UI** (via Evidence), the **case text timeline already has a UI**, and the SQL/Mongo **health "lanes" that already read live wait/blocking/Query Store data are omitted entirely** while section 6 treats the SQL lane as "imported events only". Several `path:line` citations point a few lines off, and one `timeline.go:173-174` citation for "events stored UTC" points at a struct, not the UTC code. The demo cannot produce three lanes as AC-09/TR-18 require because the demo has no case events. With those corrected, the spec is a sound MVP plan.

## Wrong or overstated claims

| Doc claim | What the code shows | Evidence path:line | Corrected wording |
|---|---|---|---|
| §2 "Call trace in the product UI — **Not wired**"; TR-12; risk #7; backlog #1 | `ActivityContext.svelte` imports `CallTrace` and `addToTray`; a "Trace this call" button and Compare tray open the trace. It is mounted from `EvidenceView.svelte:105` | `web/src/components/ActivityContext.svelte:6,8,67,71,84`; `web/src/components/EvidenceView.svelte:105` | Call trace is reachable today from Evidence → Nearby activity ("Trace this call" / Compare). Status: **Working** (backend + UI). TR-12 and backlog #1 must be rewritten, e.g. "decide whether tracing reuses this tray" |
| §2 "Case text timeline … **no chart UI**"; §7 "Case timeline POST (text rows) — Keep API; **Later UI surfacing**" | A `Timeline.svelte` component is built from `fetchTimeline` and rendered in `CaseTools` inside the Import side panel | `web/src/lib/components/CaseTools.svelte:76,155,158`; `web/src/lib/components/Timeline.svelte:8-24`; `web/src/App.svelte:74` | Text timeline **has a working UI** today (Import panel → Build timeline). Only the *combined chart* is missing. §7 should say "Keep UI; not a chart" |
| §2/§6 SQL lane = "case events, `sql-*` importers only"; D-7 options A/B only | Live SQL/Mongo **health lanes already exist and read the DB for a window** (waits, Query Store, blocking), stored as health runs and shown in `LaneRunner`/`LaneResultView`/`HealthPanel` | `internal/server/routes.go:130-137`; `internal/server/lanes.go:16-18,40-63`; `web/src/components/sources/LaneRunner.svelte`, `LaneResultView.svelte` | Add a third SQL-lane source: existing live health lanes (`/api/cases/{id}/lanes/sql`). D-7 should include "live DB-health wait/Query Store series" as an option |
| §2 "Audit timeline charts … Working" for a single aggregate audit lane reused directly via `/api/analyze` | `/api/analyze` returns **one `Series` per cohort** (API/kind/outcome); no aggregate "all APIs" series. The hero chart aggregates client-side | `internal/audit/models.go:292-296,318`; `internal/audit/analyze.go:142-210`; `web/src/lib/charts.ts:80,136` | Audit lane needs explicit aggregation across cohorts; "reuse `/api/analyze`" is partial, not a drop-in lane |
| §5 "IIS lane: status class (2xx/4xx/5xx) stacked bars" from the existing lane model | `calltrace.LaneEvent` has ID, Source, Label, Start, End, DurationMs, Severity — **no HTTP status**; `EventLabel` maps kind `request` to the generic word "event" | `internal/calltrace/types.go:71-84`; `internal/calltrace/lanes.go:130-160` | IIS status classes must be added to the lane payload; existing lane model does not carry status |
| §4 TR-18 / §9 AC-09 "demo case shows three lanes" | Demo is `audit.NewDemo` (an audit repo). No seeded case event store with `iis-w3c`/`sql-*` events; capture demo is empty | `internal/server/server.go:481-497`; `internal/server/capture_cases_test.go:27-42`; no `iis-w3c`/`sql-xe` seeds in demo | Add "seed a demo case with IIS + SQL events" as a prerequisite, or drop AC-09/TR-18 to Later |
| §11 D-5 option B "case audit rows only" / C "both" | `/api/analyze` reads `h.repo` (live Mongo audit/demo) only; case events are importers, never audit rows. There is no imported-audit-rows path | `internal/server/server.go:587-604`; `internal/server/call_trace.go:18-21` (case events are SQL/IIS/logs) | Remove "case audit rows" option; audit lane is live-Mongo-or-demo only |
| §3 step 3 cites "`timeline.go:365-368`" for single-source disable | Correct logic, but lines 365-368 are inside `disabled()`; the single-source branch is line 368 | `internal/timeline/timeline.go:363-368` | Cite `timeline.go:363-368` |
| §5 "Storage/compute: UTC (`timeline.go:173-174`)" | Lines 173-174 are the `Row` struct fields; UTC storage/utc conversion is at `overlapRow`/`absenceRow` | `internal/timeline/timeline.go:237,447,508`; `:163` comment | Cite `timeline.go:447,508` (or the package comment `:163`) |
| §2 "Import SQL-family events Working — `calltrace/lanes.go:42-53`" | That range is only the `LaneOf` prefix map, not the importer. Importers are in `internal/importer/sqlserver` | `internal/calltrace/lanes.go:42-53`; `internal/importer/sqlserver/xe.go:110`, `querystore.go:233`; `internal/importer/all/all.go:56-59` | Cite the importer files/registry; status Working is right |
| §2 "Guided 11-step Working — `investigation.go:17-29`" | Lines 17-29 are investigation constants/budgets, not the step model | `internal/server/investigation.go:17-29`; `web/src/lib/guided/stepModel.ts:9,13-24` | Cite only `stepModel.ts`; the server citation proves the engine, not the steps |

## Missing items

| What exists | Where in code | Why it matters |
|---|---|---|
| Existing live SQL/Mongo health "lanes" (wait stats, Query Store, blocking, replication), stored per window | `internal/server/routes.go:130-137`; `internal/server/lanes.go`; `web/src/components/sources/LaneRunner.svelte`, `LaneResultView.svelte` | Probably the best source for the owner's "SQL lane" over a window; omitted from §6/§8 and D-7 |
| Working Call trace UI reachable from Evidence | `web/src/components/ActivityContext.svelte:8,67,84`; `EvidenceView.svelte:105` | §7 asks to keep backend and hide UI until wired — the UI is already wired; MVP plan to "not wire" is moot |
| Working text timeline UI (audit intervals ↔ overlapping events) | `web/src/lib/components/CaseTools.svelte:155-158`; `Timeline.svelte` | The closest thing to the owner's "one-shot view" already ships; the spec should decide to extend or replace it |
| Multiple IIS importers beyond W3C: HTTPERR, FREB, WAS classifier | `internal/importer/iis/httperr.go:144`; `freb.go`; `winevents/classify_app.go`; `all.go:34-35` | The IIS lane can include HTTP.sys errors / failed-request traces, not just W3C requests |
| `sql-querystore` and `sql-xe` emit `wait` events with `wait_class` + duration | `internal/importer/sqlserver/querystore.go:536`; `xe.go:36,386,482` | Confirms "total wait ms" SQL lane is feasible from imports, not only live |
| Per-database Mongo classification gate (recent test TTL) | `internal/server/case_sources.go:26-29`; guided uses only audit+iis | §3 "add sources" must explain that SQL/DB-health sources are chosen on a separate screen from tags |
| Retention: closed cases auto-erased after 30 days | `internal/casestore/store.go:94-96` | A user tracing an old closed case may find its data swept; not mentioned in §8 |
| `investigationRequest` already supports incident+reference windows, route filter, pool limit, releases | `internal/server/investigation.go:51-65` | §3/§4 could reuse this contract instead of inventing a new window shape |

## Claims confirmed

- Lane prefix grouping `sql-*`→SQL etc.: `internal/calltrace/lanes.go:42-53`.
- Lane states incl. `clock-unchecked`: `internal/calltrace/lanes.go:263-278`; `types.go:14-22`.
- Call-trace route reads case events and audit record, read-only, per-call windows: `internal/server/call_trace.go:18-21,238-274`.
- TZ validation returns `tz-rule` disable: `internal/timeline/timeline.go:312-318,363-378`.
- 50k event cap: `internal/casestore/store.go:101-102`; truncation flag `timeline.go:198`.
- `/api/analyze` at line 192: `internal/server/routes.go:192`.
- Depth model / Pulse / Where / Evidence / Record: `web/src/App.svelte:112-131`.
- Case home analysis + guided buttons: `web/src/lib/components/CaseHome.svelte:452-463`.
- Frame limits 7 days / 50 releases / 100000 pool: `web/src/lib/guided/frame.ts:19,23-25`.
- Bucket bounds 10s–86400s and 120 max: `internal/audit/query.go:36-40`; `internal/audit/models.go:17`.
- 7-day reject: `internal/audit/query.go:76-77`.
- IIS W3C: `time` unverified assumption, 1s resolution, no-`time-taken` gap, kind `request`, `DurationMs` from `time-taken`: `internal/importer/iis/w3c.go:14-21,27-31,175-187`.
- Join engine and 1s tolerance: `internal/join/join.go:19-23,48-124`; `internal/timeline/timeline.go:158`.
- `time-taken` duration "counts + duration" feasible (D-3 B): `internal/importer/iis/w3c.go:27,182-187`.
- Host CPU/mem/disk rules exist (Later lane feasible): `internal/resource/metrics.go:17-22,155-175`.
- `/api/slowest` exists: `internal/server/routes.go:195`; `slowest.go`.
- Master-key lock → 423; closed-case writes → 409: `internal/server/masterkey.go:363-382`; `internal/server/cases.go:446`.
- Case text timeline backend joins audit intervals to imports: `internal/timeline/timeline.go:206-285`; `internal/server/case_views.go:93-145`.
- Addendum scenario IDs GL-24/29/31/32-37/GS-40 exist as cited: `inputs/2026-10-30-guided-layout-spec-addendum.md:15,20,22,30,35,54`.
- Guided surfaces and tabs exist: `web/src/lib/components/guided/GuidedAnalysis.svelte:27-62`; `NearbyActivity.svelte:26-40`.
- Spec is under 5500 words (~3698) and uses only the allowed status words.

## Could not check

- Whether the new `POST /api/cases/{id}/tracing` endpoint is accepted by the owner (design only).
- Exact visual correctness of the proposed stacked/dual-axis lane rendering (no code yet).
- Whether `Pulse.svelte:68-80` is the precise chart block vs the surrounding `$effect` (range is plausible but I did not diff every line).
- Real IIS file alignment on the owner's servers (unverified in code by design) and real SQL XE import density.
- Whether every cited line still matches after future edits outside this checkout (I read the current commit only).