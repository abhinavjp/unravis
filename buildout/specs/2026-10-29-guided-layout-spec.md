# Guided analysis layout: specification

Date: 2026-10-29. Status: DRAFT, awaiting owner pick of a variant. Guided steps 0–13 and all 13 Manual tools are in the same wireframe (`?mode=guided|manual`, `?step=`, `?tool=`).
Wireframe: `buildout/wireframes/guided-analysis-layout.html` (variants A, B, C; `?variant=`). Throwaway; this spec is what survives. Built from `buildout/wireframes/parts/` by `build-guided.js`.
Source screens: `web/src/lib/components/guided/GuidedAnalysis.svelte`, `CaseHome.svelte`, `StepRail.svelte`, `ManualTools.svelte`, `SavedViews.svelte`, `web/src/lib/guided/tools.ts`, `web/src/app.css`.
Scenario IDs `GL-nn` (layout), `GS-nn` (step states and charts) and `MT-nn` (Manual tools) are written so each becomes one UI test.

## 1. Problems found (today's behaviour)

| # | Owner report | Root cause in code |
|---|---|---|
| P1 | The whole window scrolls except the header; looks ugly | `.stage` (`app.css`) is the only scroll container. The Guided title, back button, tabs, step rail and run bar are all inside it, so they scroll away with the content. |
| P2 | Two back buttons | `CaseHome.svelte` draws "← Case" (`CASE_HOME_TEXT.back`) and `GuidedAnalysis.svelte` draws "← Back to the case" (`T.closeGuided`) at once. Both go to the case page. |
| P3 | Checklist and After the fix should be later steps | They are top tabs (`checklist`, `fix`) beside Steps, so they read as separate tools instead of the end of the analysis. |
| P4 | Nearby activity belongs near the API list | It is a top tab (`nearby`), far from `SlowCalls.svelte` where the owner picks a slow call to ask "what else ran then?". |

## 2. Decision requested: pick a variant

All three share the same rules in sections 3 to 7. They differ only in how steps are navigated.

| | A. Fixed rail (recommended) | B. Top stepper | C. One flow |
|---|---|---|---|
| Navigation | Left rail, grouped, always visible, scrolls on its own | Horizontal chips in four groups under the case bar | Folded list of steps; one open at a time; narrow progress column |
| Content width | Rail 300 px, content gets the rest (max reading width 1100 px) | Full width | Full width minus 170 px column |
| Strength | Closest to today (rail already exists), status of all steps visible at once, easiest keyboard model | Most content width | Reads as a story |
| Weakness | Takes 300 px | Chips scroll sideways on narrow windows | Long page; losing place; harder to test (open/closed state) |

Recommendation: **A**. It keeps what engineers already know, shows every step's status, and is the least change to `StepRail.svelte`. The rest of this spec is written for A; B and C change only section 4.

## 3. Layout contract (all variants)

Regions, top to bottom. The window itself never scrolls.

1. **App header** (existing, 56 px). Fixed.
2. **Case bar** (new, replaces both back buttons and the Guided title block). Fixed. Contains, left to right: one back control, case title (`Case 0042 · <name>`) with `Guided analysis` as the heading, and the mode switch ("Switch to Manual tools") at the right. The existing "Case actions" menu stays in the right slot.
3. **Body**: rail (A) or stepper (B) plus the **pane**.
4. **Pane** is the only scrolling region. Its first child is the **run bar** (incident window, reference window, stale notice, Edit windows, Run the steps) which is sticky at the top of the pane (A, C) or fixed above it (B).

Rules:
- L1. `.app-shell` stays `height:100dvh; overflow:hidden`. While Guided analysis is open, `.stage` is `overflow:hidden; padding:0` and the guided root fills it (`display:flex; flex-direction:column; min-height:0`). Other screens keep today's scrolling `.stage`.
- L2. The rail scrolls independently if it is taller than the window. The pane scrolls independently. Scrolling one never moves the other.
- L3. Exactly one back control on the Guided screen: the case bar's "← Case 0042" with `aria-label="Back to the case"`. `CaseHome.svelte` must not draw its own back button while Guided is open, and `GuidedAnalysis.svelte` must not draw `T.closeGuided`. Back always returns to the case page and keeps the chosen step for next time.
- L4. Scroll position of the pane resets to top when the step changes, and is kept when the same step re-renders (for example after Run).
- L5. Below 900 px the rail stacks above the pane, limited to 34 vh and scrolling itself. Header, case bar and run bar stay fixed. Nothing scrolls sideways.
- L6. Light and dark themes both supported (tokens already shared). No new colours; statuses keep today's stickers.

## 4. Step list and order

Steps 0 to 10 are unchanged in number, title and order. Three steps are appended; the top tabs `Checklist`, `After the fix`, `Nearby activity` and `Export` are removed. Manual tools stays reachable only through the mode switch in the case bar.

| Step | Title | Group in rail | Source component |
|---|---|---|---|
| 0 | Frame the question | Investigate | `Step0Frame` |
| 1 | Can I trust the data? | Investigate | existing |
| 2 | Shape | Investigate | existing |
| 3 | Time split | Investigate | existing (holds Slowest calls and now Nearby activity, see section 5) |
| 4 | Load | Investigate | existing |
| 5 | Changes | Investigate | existing |
| 6 | App tier | Investigate | existing |
| 7 | SQL Server | Sources | existing |
| 8 | MongoDB | Sources | existing |
| 9 | Host | Sources | existing |
| 10 | Origin and suspects | Conclude | `Step10Origin` |
| 11 | After the fix | Follow up | `AfterFix.svelte` |
| 12 | Checklist | Follow up | `Checklist.svelte` |
| 13 | Export | Follow up | `ReportExport.svelte` |

- S1. `stepModel.ts` gains steps 11 to 13. `railItems` returns the group label for each row. Groups are labelled with small caps headings (`Investigate`, `Sources`, `Conclude`, `Follow up`).
- S2. Steps 11 to 13 show a status sticker like the others (After the fix: `Not started` or `Compared`; Checklist: `n of m done`; Export: `Not started` or `Created`). They never block earlier steps and are not run by "Run the steps".
- S3. `Step10Origin`'s "Open the checklist" action becomes "Go to step 12" and moves the rail selection. `clampStep` accepts 0 to 13.
- S4. "Run the steps" runs steps 1 to 10 only. After the fix keeps its own "Compare with the incident" button. Only one primary button is shown per screen (existing rule `runIsPrimary`): on step 11 the compare button is primary and the run bar's button becomes secondary.
- S5. Previous/Next buttons (B only) walk 0 to 13.

## 5. Nearby activity inside the steps

- N1. The standalone Nearby activity tab is removed.
- N2. Every row of the Slowest calls table (`SlowCalls.svelte`) gets a row action "Nearby activity". Activating it marks the row selected (`aria-selected`) and opens the Nearby panel directly **below the table**, in the same step. One panel at a time; picking another row replaces it; "Close" removes it.
- N3. The panel title names the call: `Nearby activity · <API> at <time>`. It reuses today's `NearbyActivity` copy and controls (around/before/after minutes, quick windows 2/5/15 min, lanes API calls, SQL Server, MongoDB, Host, colour legend, "Cannot test yet" lines). Only its container changes.
- N4. If Slowest calls is shown in more than one step, the same row action and panel appear in each; today it is in step 3 (Time split). Implementation confirms which steps render `SlowCalls`.
- N5. The panel must make clear it shows what overlapped in time, not a cause (keep existing sentence).
- N6. Opening the panel does not move the rail selection, scroll position of the table, or any step status. Closing the step (navigating away) discards the open panel; returning shows the table with no row selected.

## 6. Behaviour and copy

- Back control text: `← Case 0042` (case code from the case). Tooltip and `aria-label`: `Back to the case`.
- Rail row: number, label, status sticker. Current row has the accent inset border and `aria-current="step"`. Rows for steps with nothing to test show `Cannot test yet`.
- Step heading shows `Step n · Title` for 0 to 10 and just the title for 11 to 13.
- Failed step 1 keeps today's alert block ("The audit source could not be read"), with the next action sentence from the audit-failure work (job 5182). Layout does not change that copy.
- Focus: on step change focus moves to `step-title-<n>` (existing). On opening Nearby activity, focus moves to the panel heading; on Close, focus returns to the row's button.
- Keyboard: arrow Up/Down in the rail moves between rows, Enter selects; Home/End jump to first/last. The pane is focusable (`tabindex=0`, labelled by the step heading) so keyboard users can scroll it.

## 7. Accessibility

- Landmarks: header, `nav aria-label="Steps"`, `main` for the pane. The case bar is a `header`-level region with the single back button first in tab order.
- Sticky run bar must not hide the focused element: pane has `scroll-padding-top` equal to the run bar height.
- Colour is never the only status cue: every sticker has text.
- Reduced motion: no scroll animation.

## 8. Scenario tests (UI)

Run at 1440x860 and 800x700 unless stated. Use the existing e2e harness with a seeded case that has audit data and one with an audit failure.

| ID | Scenario | Expected |
|---|---|---|
| GL-01 | Open Guided analysis, scroll the pane to the bottom | `document.scrollingElement.scrollTop` is 0; app header, case bar, rail and run bar rectangles are unchanged (top offsets equal before and after) |
| GL-02 | Scroll the pane, then scroll the rail (with the window short enough that the rail overflows) | Each moves alone; the other's scrollTop is unchanged |
| GL-03 | Count back controls on the Guided screen | Exactly one button named "Back to the case" |
| GL-04 | Activate the back control | Case page opens; reopening Guided returns to the same step |
| GL-05 | Rail rows | Rows 0 to 13 present, in order, in the four groups; Checklist and After the fix come after Origin |
| GL-06 | Top tabs | No tab named Checklist, After the fix, Nearby activity or Export exists |
| GL-07 | Select step 12 from the rail | Checklist content shows; heading focused; pane scrolled to top |
| GL-08 | Step 10 "Go to step 12" action | Rail selection moves to step 12 |
| GL-09 | Run the steps | Steps 1 to 10 get statuses; steps 11 to 13 statuses unchanged; one primary button on screen |
| GL-10 | Step 11 | The compare button is the only primary button; the run bar's run button is secondary |
| GL-11 | Nearby activity on a Slowest-calls row | Panel appears directly under the table with the call's API and time in its title; row has `aria-selected=true` |
| GL-12 | Pick a second row | The panel is replaced, not duplicated |
| GL-13 | Close the panel | Panel gone, focus on the row's button, table scroll position unchanged |
| GL-14 | Open the panel, switch step, come back | No row selected, no panel |
| GL-15 | Case with no Mongo or Host source | Those lanes show "Cannot test yet" |
| GL-16 | Short window, scroll pane, focus a control under the sticky run bar via Tab | Focused element is visible (not covered by the run bar) |
| GL-17 | Width 800 | Rail stacks above the pane, no horizontal page scroll, header, case bar and run bar still fixed |
| GL-18 | Light and dark theme | Layout identical; no text below 4.5:1 contrast on the case bar, rail, stickers |
| GL-19 | Keyboard in the rail | Down/Up/Home/End move the current row; Enter selects; `aria-current` follows |
| GL-20 | Failed audit read (seeded) | Step 1 shows the alert with the reason and next action; layout regions unchanged |
| GL-21 | Switch to Manual tools and back | Guided returns at the same step; the single back control rule still holds in Manual tools |
| GL-22 | Other screens (case list, Sources, Help) | Still scroll as before, `.stage` padding unchanged |

Existing tests to update: any e2e or unit test that references the `checklist`/`fix`/`nearby`/`export` tab panels, `T.closeGuided`, or `CASE_HOME_TEXT.back` while Guided is open; `stepModel` tests for `clampStep` and `railItems`.

## 9. Out of scope

Changing the audit-failure reasons or sign-in. Variant B or C details apply only if chosen; this spec's L, S, N and MT rules hold for all.

Manual tools layout is now in scope: the case-bar mode switch, the 13-tool home, saved views, each tool page, and the round trip back to Guided at the same step (sections 12 and 13, scenarios `MT-01` onward). Step content for the catalogue (what each step shows, which chart, which empty/failed copy) is in section 11 and is in scope for the wireframe; production step copy stays owned by `guidedCopy.ts`.

## 10. Open questions for the owner

1. Variant A, B or C?
2. Should step 13 Export stay a step, or become a button in the case bar ("Create report") so it is always one click away? (Spec assumes step.)
3. Should "After the fix" come before "Checklist" (as in the wireframe) or after?

## 11. Step catalogue

One table per step 0–13. Fields are those in `web/src/lib/guided/types.ts`. Empty / Cannot test yet / Failed / Out of date use the real copy. Buttons listed as live in the wireframe unless marked inert.

### Step 0. Frame the question

| | |
|---|---|
| Data shown | `frame.incidentStart/End`, `referenceStart/End`, `route`, `poolLimit`, `releases[]`, `releasesEntered`, `myOnset`; after a run `facts.onset.t0`, `facts.counts.used` |
| Tables | None. Form only (`Step0Frame.svelte`). Change rows: When (UTC), What changed |
| Chart | None |
| Empty | Empty windows: onset reads "The onset is found when the steps run." Change list: "No change listed." |
| Cannot test yet | No source: no-sources line and Add data |
| Failed | Alert "The steps could not run." with Retry reading |
| Out of date | Form always edits live windows; editing them marks steps 1–10 out of date |
| Buttons | Quick picks Last hour / Today / Yesterday (live). Add a change / Remove (live). I have listed every change I know of (live). Use suggested / Use my time / Widen / Carry on without an onset only when no onset is found |

### Step 1. Can I trust the data?

| | |
|---|---|
| Data shown | `facts.counts.incident/reference`, `facts.gaps[]`, `facts.clock.checked/anomalous`, `confidence.sourcesUsed/sourcesMissing`, `answer.read.requests/iisRows` |
| Tables | Sources (Source, State). Gaps as a list. Four trust cards: calls in incident, calls in reference, time gaps, clock difference |
| Chart | None |
| Empty | A window with no calls swaps the step for "Not enough data to continue" |
| Cannot test yet | The stop box, or per-source rows reading Cannot test yet |
| Failed | Alert "The steps could not run." with Retry reading |
| Out of date | Same content plus the out-of-date notice; sticker "Out of date, rerun" |
| Buttons | Change the window → step 0 (live). Add data (inert) |

### Step 2. Shape

| | |
|---|---|
| Data shown | `facts.routes[]` (route, incident.n, incident.p95, reference.p95, p95Change), `facts.retries`, `facts.shape.kind`, `thresholds.changeMinPercent/changeMinMs`, `facts.series[].p95` |
| Tables | By API: API, Calls, P95, Reference P95, Change |
| Chart | `MinuteLine` of `series[].p95`. Y: ms to the peak. X: minutes 08:00–09:59 UTC. Normal = blue, incident = red. A minute with no data breaks the line. Caption: range and peak. Legend: Normal / Incident |
| Empty | "This window holds no audit records, so there is nothing to list. Cannot test yet." No chart |
| Cannot test yet | Same as empty: cannot compare windows with no calls |
| Failed | Alert "The steps could not run." with Retry reading |
| Out of date | Same content plus the out-of-date notice |
| Buttons | Show every API / Show fewer (live) |

### Step 3. Time split

| | |
|---|---|
| Data shown | `facts.parts.items[]` (incMeanMs, refMeanMs, change, share, tested), `facts.parts.dominantWhy`; slow calls (route, initiatedAt, durationMs); nearby window around ± before/after |
| Tables | Parts: Part, Normal, Incident, Change, Share of the extra time. Slowest calls: API, Started (UTC), Time, Nearby activity |
| Chart | Two stacked horizontal bars (Normal, Incident) of average call time on one scale; segments A / C / P / O. Nearby duration bars: green under 1 s, orange 1–2 s, red striped over 2 s |
| Empty | Slowest calls before the button: only "Show the slowest calls". Quiet window: "This window holds no audit records…". Nearby with no rows: "Nothing was recorded in this window." |
| Cannot test yet | Whole step: reason list. Missing nearby lanes: "Cannot test yet: this case has no source of this kind." |
| Failed | Step: "The steps could not run." Nearby: "The activity could not be read." Slowest calls: "The tool could not run." |
| Out of date | Same content plus the out-of-date notice |
| Buttons | Nearby activity on a row (live). Close (live). Quick windows 2 / 5 / 15 min (live). Add the IIS logs (inert) |

### Step 4. Load

| | |
|---|---|
| Data shown | `facts.load` (refPerSec/incPerSec, refWms/incWms, refL/incL, refPeak/incPeak, reading), `signals.pool` (limit, plannedFill, reading), `series[].inFlight` |
| Tables | Measure, Normal, Incident, Change |
| Chart | `MinuteLine` of `series[].inFlight`, unit "calls". Same axes, colours and gap as the P95 chart. Caption: highest minute average |
| Empty | Table cells "No data"; no chart |
| Cannot test yet | Too few calls: reason list replaces table and chart |
| Failed | Alert "The steps could not run." with Retry reading |
| Out of date | Same content plus the out-of-date notice |
| Buttons | None of its own. Pool figure appears because Step 0 holds the pool limit |

### Step 5. Changes

| | |
|---|---|
| Data shown | `signals.changes.items[]` (at, label, relation, minutesBefore), `signals.changes.why/caveat/verdict`; list from `frame.releases[]` |
| Tables | When, What, Against the onset |
| Chart | None |
| Empty | "No change listed. Add the releases or changes you know of in Step 0." Tick "I have listed every change I know of" with an empty list: "You said there was no change to list." |
| Cannot test yet | No onset: the reason list replaces the table |
| Failed | Alert "The steps could not run." with Retry reading |
| Out of date | Same content plus the out-of-date notice; editing the list in Step 0 marks this step out of date |
| Buttons | Change the list → step 0 (live). Add its check on a suspect (live) |

### Step 6. App tier

| | |
|---|---|
| Data shown | `facts.load` + `signals.pool` rewritten as findings (`steps[5].findings`), suspects of group S |
| Tables | None. Findings list plus suspect cards |
| Chart | None |
| Empty | No findings block; suspect list "No suspect belongs to this step." |
| Cannot test yet | Reason list replaces findings |
| Failed | Alert "The steps could not run." with Retry reading |
| Out of date | Same content plus the out-of-date notice |
| Buttons | Add its check on a suspect card (live) |

### Step 7. SQL Server

| | |
|---|---|
| Data shown | Engine findings for the SQL group (`steps[6].findings`), suspects of group D. No per-wait table component in the repo |
| Tables | None in the product UI. Findings and suspects |
| Chart | None in the repo UI |
| Empty | A SQL source with a quiet window: findings say no blocking was recorded |
| Cannot test yet | "This case has no SQL Server source." plus Add a source |
| Failed | Alert "The steps could not run." with Retry reading |
| Out of date | Same content plus the out-of-date notice |
| Buttons | Add a source (inert). Add its check (live) |

### Step 8. MongoDB

| | |
|---|---|
| Data shown | Engine findings for the Mongo group (`steps[7].findings`), suspects of group M |
| Tables | None in the product UI |
| Chart | None in the repo UI |
| Empty | No findings block; "No suspect belongs to this step." |
| Cannot test yet | Default for this incident: "This case has no MongoDB source." plus Add a source |
| Failed | Alert "The steps could not run." with Retry reading |
| Out of date | Same content plus the out-of-date notice |
| Buttons | Add a source (inert) |

### Step 9. Host

| | |
|---|---|
| Data shown | Engine findings for the host group (`steps[8].findings`), suspects of group H |
| Tables | None in the product UI |
| Chart | None in the repo UI |
| Empty | No findings block; "No suspect belongs to this step." |
| Cannot test yet | Default for this incident: "This case has no Blackbox on a host." plus Place a Blackbox |
| Failed | Alert "The steps could not run." with Retry reading |
| Out of date | Same content plus the out-of-date notice |
| Buttons | Place a Blackbox (inert) |

### Step 10. Origin and suspects

| | |
|---|---|
| Data shown | `origin.roles[]` (tier, at, why, role), `suspects[]` grouped red+orange / grey / green, `confidence` (word, why, sourcesUsed, sourcesMissing, notTested, overturn), `cheapestCheck` |
| Tables | Origin: Tier, First moved (UTC), What moved, Role. How sure are we: Item, State |
| Chart | None. Colour legend and suspect cards |
| Empty | No red or orange: "No suspect is red or orange. See the grey ones: they could not be tested." Not enough data: "Not enough data to date any tier…" |
| Cannot test yet | The not-enough-data line replaces the origin table |
| Failed | Alert "The steps could not run." with Retry reading |
| Out of date | Same content plus the out-of-date notice |
| Buttons | Show the suspects set aside / Show fewer (live). Add its check (live). Go to step 12 (live) |

### Step 11. After the fix

| | |
|---|---|
| Data shown | A second engine run for the later day: `facts.change.p95/p50/mean` and `facts.load.incL` of both days, classified by `classifyDay` |
| Tables | Measure, Earlier day, Later day, Change vs the earlier day |
| Chart | None |
| Empty | Before the steps run: "Run the steps first. The comparison needs the incident day." A day with no calls: verdict "No data for that day" |
| Cannot test yet | Same as not run |
| Failed | "The later day could not be compared." |
| Out of date | A new main run makes an old verdict stale: it leaves the page (not shown as stale) |
| Buttons | Compare (live). Record with this warning (live). Not now (live). On this step the run bar's button is secondary |

### Step 12. Checklist

| | |
|---|---|
| Data shown | `ChecklistItem[]` (list, text, suspect). Counters: Done = learned + ruled out, Open = todo + doing |
| Tables | Four lists: Need to do, Working on, What we learned, Ruled out |
| Chart | None |
| Empty | A list with no items: "Nothing here yet." |
| Cannot test yet | Not a state of this step |
| Failed | Inline error, e.g. "That item could not be saved. Check the text and try again." |
| Out of date | Not a state of this step |
| Buttons | + Add item (live). Add suggested checks from Step 10 (live). Move to / Delete per item (live). Closed case: read-only |

### Step 13. Export

| | |
|---|---|
| Data shown | The run on screen plus `ReportInclude` (figures, suspects, checklist, sources). SHA-256 of the saved file |
| Tables | None. Include options, warnings, preview frame |
| Chart | None here; the report embeds charts as SVG |
| Empty | "Nothing has been analysed yet. The report will say so and hold only the checklist, if you include it." |
| Cannot test yet | Same as empty |
| Failed | "The report could not be built." Checksum mismatch: "The report did not match its checksum, so it was not saved. Try again." |
| Out of date | Warning box "The windows or lists changed after the last run…" HTML button gains "with these warnings" |
| Buttons | Include checkboxes (live). Download HTML / CSV (inert: no file). Preview / Close preview (live) |

## 12. Manual tools catalogue

Cards follow `TOOLS` in `web/src/lib/guided/tools.ts`. Status is `toolState(tool, { have, result })`. IIS is a soft need on any tool that lists more than one need (time-split: Partly ready without IIS). Slowest calls and Nearby activity read themselves (`READS_ITSELF`) so they are never Waiting. Dummy case: audit + SQL present, IIS + MongoDB + Host absent, steps not yet run.

| id | Title | Step it mirrors | Sources (card) | Needs | Filters | Body | States |
|---|---|---|---|---|---|---|---|
| response-time | Response time over time | 2 | Audit | audit | Optional focus API from windows | `MinuteLine` p95 + By API table (API, Calls, P95, Reference P95, Change) | Cannot test yet without audit. Waiting until a run. Ready: chart + table. Empty: noRecords. Failed: toolFailed. Out of date sticker after a stale run |
| slowest-apis | Slowest APIs | none (own table of `facts.routes`) | Audit | audit | Minimum calls (default 20). Only APIs matching | Ranked table. Four empty messages: jumpedAhead, noRecords, noApiMatch, noApiRows | Cannot test yet without audit. Waiting until a run. Ready: filtered table. Failed: toolFailed |
| slowest-calls | Slowest calls | SlowCalls in step 3 (without Nearby column) | Audit | audit | Incident window | `SlowCalls` table: API, Started (UTC), Time. Own Show the slowest calls button. **No Run bar** | Cannot test yet without audit. Ready without a guided run. Empty: noRecords. Failed: toolFailed |
| time-split | Time split A C P O | 3 (parts only) | Audit, IIS | audit, iis (iis soft) | Optional focus API | Parts table + stacked A/C/P/O bars | Cannot test yet without audit. Partly ready without IIS. Waiting until a run. Ready: full split |
| calls-in-flight | Calls in flight | 4 | Audit | audit | Pool limit from windows | Load table (Measure, Normal, Incident, Change) + `MinuteLine` inFlight | Cannot test yet without audit. Waiting until a run. Empty: too few calls. Failed: toolFailed |
| nearby-activity | Nearby activity | Nearby panel | Audit, IIS, SQL Server, MongoDB, Host | audit (others optional lanes) | Around, minutes before/after, quick 2/5/15 | Full nearby panel, four lanes. **No Run bar** | Cannot test yet without audit. Ready without a guided run. Missing lanes: laneNone. Failed: nearbyFailed |
| changes | Changes timeline | 5 | Audit, your change list | audit | Change list from windows | Timeline table: When, What, Against the onset | Cannot test yet without audit. Waiting until a run. Empty: no change listed / none entered |
| sql-waits | SQL blocking and waits | 7 | SQL Server | sql | Windows | Step 7 findings + suspects. Wait-class table is a labelled placeholder (repo has no per-wait component) | Cannot test yet without SQL. Waiting until a run. Ready: findings |
| mongo-health | MongoDB health | 8 | MongoDB | mongo | Windows | Step 8 findings + suspects. Replica/slow-ops table is a labelled placeholder | Cannot test yet without Mongo (default dummy). Waiting until a run once Mongo is present |
| host-resources | Host CPU, memory and disk | 9 (app host) | Blackbox | host | Windows | Step 9 findings + suspects. Host table is a labelled placeholder | Cannot test yet without Host (default dummy) |
| sql-host | SQL host disk, memory and network | 9 (SQL host) | Blackbox on the SQL host | host | Windows | Same host checks on the SQL host. Table is a labelled placeholder (no dedicated component) | Cannot test yet without Host (default dummy) |
| after-fix | After the fix | 11 | Audit | audit | Later day, optional route | After the fix form, verdict word, comparison table | Cannot test yet without audit. Waiting / not run: "Run the steps first. The comparison needs the incident day." Failed: fixFailed |
| suspect-summary | Suspect summary | 10 | Audit and any other source in the case | audit | Windows | Full Step 10 origin: table, colour legend, suspect cards, cheapest check, How sure are we | Cannot test yet without audit. Waiting until a run. Ready: origin page |

Every tool page, after the body: "Add a note to the checklist" (itemSave disabled when empty or over 300 characters; noteAdded after save; closedNoAdd when the case is closed) and Saved views.

## 13. Manual tools layout rules

- MT-L1. Mode lives in the URL as `?mode=manual` or `?mode=guided` (and `?step=` or `?tool=`). `history.replaceState` is wrapped in try/catch so the file can run from a data: URL.
- MT-L2. The case bar still holds **exactly one** back control: "← Case 0042" with `aria-label="Back to the case"`. Switching mode does not add a second back. "Back to tools" is a pane action, not a second case-bar back.
- MT-L3. "Switch to Manual tools" replaces the whole pane with Manual tools home (or the open tool). "Switch to Guided analysis" / "Run Guided analysis instead" returns to Guided at the same step.
- MT-L4. Manual tools home heading is "Manual tools", lead is `T.toolsLead`. All 13 tools are cards in `TOOLS` order. Each card: title, sticker, blurb, "Sources: …", needs hint when Cannot test yet, Waiting hint, "Open: \<title\>".
- MT-L5. The window never scrolls. In manual mode the pane (`#mpane`) is the only scrolling region. Header and case bar stay fixed.
- MT-L6. An open tool: Back to tools, title, sticker, blurb, missing-source box when a hard source is absent (no body, no windows, no run bar), else collapsible windows panel (Step0Frame look) and RunBar except for `slowest-calls` and `nearby-activity`.
- MT-L7. Saved views sit on the home and on every tool page. Columns: Name, Tool, Windows (UTC), Saved, Actions. Open / Delete. Save this view with both notes (`viewNameNote`, `viewSaves`). Empty: `viewsEmpty`. Full (200): `viewsFull`. Closed: `viewsClosed` and no Delete / Save.
- MT-L8. A note form is on every tool page. Closed case uses `S.closedNoAdd`. Over-length uses `T.itemTooLong`.

### Manual tools scenarios

| ID | Scenario | Expected |
|---|---|---|
| MT-01 | Open Manual tools home | 13 cards in `TOOLS` order: response-time, slowest-apis, slowest-calls, time-split, calls-in-flight, nearby-activity, changes, sql-waits, mongo-health, host-resources, sql-host, after-fix, suspect-summary |
| MT-02 | Dummy sources (audit + SQL on; IIS, Mongo, Host off; steps not run) | mongo-health, host-resources, sql-host: Cannot test yet. time-split: Partly ready. slowest-calls and nearby-activity: Ready. The rest that need a run: Waiting. sql-waits: Waiting (SQL present) |
| MT-03 | Tick IIS in Pretend this case has | time-split sticker becomes Waiting (no run) or Ready (after a run), not Partly ready |
| MT-04 | Tick MongoDB | mongo-health leaves Cannot test yet (Waiting or Ready depending on ranSteps) |
| MT-05 | Tick Host | host-resources and sql-host leave Cannot test yet |
| MT-06 | Untick audit | Every tool that needs audit shows Cannot test yet, including slowest-calls and nearby-activity |
| MT-07 | Untick SQL | sql-waits is Cannot test yet |
| MT-08 | Open response-time | Page has Back to tools, title, sticker, blurb, windows panel, Run bar, MinuteLine p95, By API table |
| MT-09 | Open slowest-apis | Filters Minimum calls = 20 and Only APIs matching are present. Table columns API, Calls, P95, Reference P95, Change |
| MT-10 | Slowest APIs, Minimum calls 5000 | Message `T.noApiRows(5000)` unless an API has ≥ 5000 calls |
| MT-11 | Slowest APIs, match a string no API contains | Message `S.noApiMatch(text)` |
| MT-12 | Slowest APIs, match "Pay" | Only Payments.Authorize remains |
| MT-13 | Open slowest-calls | No Run bar. Show the slowest calls reveals API, Started (UTC), Time. No Nearby activity column |
| MT-14 | Open nearby-activity | No Run bar. Lanes API calls, SQL Server, MongoDB, Host. Mongo and Host lanes read Cannot test yet on the dummy case |
| MT-15 | Open time-split without IIS | No missing-source box (iis is soft). Partly ready. Parts table + stacked bars. Add the IIS logs shown |
| MT-16 | Open mongo-health on the dummy case | Missing-source box "This tool cannot run yet" / "This tool needs a MongoDB source…". No windows, no run bar, no body table |
| MT-17 | Open sql-waits after a pretend run | Findings, placeholder wait table, D1 suspect. Missing-source box absent |
| MT-18 | Open after-fix before a run | "Run the steps first. The comparison needs the incident day." |
| MT-19 | Open suspect-summary after a run | Origin table, colour legend, ranked / not tested / set aside, cheapest check, How sure are we |
| MT-20 | Open calls-in-flight after a run | Load table + MinuteLine inFlight |
| MT-21 | Open changes with the listed releases | Timeline table When, What, Against the onset |
| MT-22 | Missing-source Add by location | Button present, labelled inert in the wireframe |
| MT-23 | Save this view with a name | A row is appended (Name, Tool, Windows (UTC), Saved, Open, Delete). Heading Saved views (n) increments |
| MT-24 | Save this view with an empty name | "Give the view a name." No row added |
| MT-25 | Save this view with 61 characters | "A view name holds up to 60 characters. Shorten it to save." Save disabled |
| MT-26 | Open a saved view | The named tool opens with that view's windows line |
| MT-27 | Delete a saved view | The row is gone. Count decrements |
| MT-28 | Toggle Saved views full (200) | Notice `T.viewsFull`. Table lists 200 rows in a 420 px scroll |
| MT-29 | Toggle Case is closed | `T.viewsClosed`. No Save / Delete. Note form reads `S.closedNoAdd` |
| MT-30 | Empty saved views (delete the seeds) | "No view is saved yet." |
| MT-31 | Add a note to the checklist (valid) | Button Add enabled; after submit, "Added to the checklist." |
| MT-32 | Add a note over 300 characters | "An item holds up to 300 characters." Add disabled |
| MT-33 | Switch Guided → Manual → Guided | Guided is on the same step as before the switch |
| MT-34 | Manual tools: count back controls | Exactly one button named "Back to the case". Back to tools is in the pane when a tool is open |
| MT-35 | Window never scrolls in manual home or any open tool | `document.scrollingElement.scrollTop` is 0; only `#mpane` moves |
| MT-36 | Open host-resources / sql-host with Host ticked and a run | Labelled placeholder tables; notes strip says the repo has no host-resource / sql-host component |
| MT-37 | Keyboard: arrow keys still cycle variants A/B/C in manual mode; T toggles theme | Variant label and `data-theme` change; tool page stays open |
| MT-38 | URL `?mode=manual&tool=slowest-apis` | Manual tools opens on Slowest APIs |

### Guided step-state and chart scenarios

| ID | Scenario | Expected |
|---|---|---|
| GS-01 | Step 0 Ready | Full frame form, onset 09:14 UTC, two listed changes, pool limit 100 |
| GS-02 | Step 0 Cannot test yet | No-sources line and Add data; form still present |
| GS-03 | Step 0 Failed | Alert "The steps could not run." with Retry reading |
| GS-04 | Step 1 Ready | Four trust cards (36,910 / 36,738 / 1 / Looks fine), Sources table, one long gap 09:31–09:44 |
| GS-05 | Step 1 Cannot test yet | Stop box "Not enough data to continue" with Change the window and Add data |
| GS-06 | Step 2 Ready | By API table, 3 of 12 moved, MinuteLine p95 with a break at 09:31–09:44, peak 2,100 ms |
| GS-07 | Step 2 chart colours | Normal line uses `--series-1`, incident uses `--series-4`. Caption names the range and the peak |
| GS-08 | Step 2 Cannot test yet | Reason list, no table, no chart |
| GS-09 | Step 3 Ready | Parts table, stacked bars A/C/P/O, Slowest calls, Nearby panel under the selected row |
| GS-10 | Step 3 stacked bars | Two tracks Normal and Incident on one scale; O is the dominant incident segment |
| GS-11 | Step 3 Cannot test yet | Reason list; no bars |
| GS-12 | Step 3 nearby lanes with no Mongo/Host | MongoDB and Host lanes: "Cannot test yet: this case has no source of this kind." |
| GS-13 | Step 4 Ready | Load table (arrival about the same, in-flight higher) and MinuteLine inFlight with the same gap |
| GS-14 | Step 4 Cannot test yet | Reason list, no table, no chart |
| GS-15 | Step 5 Ready with listed changes | Timeline table; C1 suspect |
| GS-16 | Step 5 empty list | "No change listed…" and Change the list |
| GS-17 | Step 6 Ready | Findings (pool never full, O rose) and S1 |
| GS-18 | Step 7 Ready | SQL findings and D1 |
| GS-19 | Step 7 Cannot test yet | "This case has no SQL Server source." Add a source |
| GS-20 | Step 8 default (no Mongo) | Cannot test yet, Add a source |
| GS-21 | Step 8 Ready (state switch) | Findings: no slow ops, replica stable, M1 |
| GS-22 | Step 9 default (no Host) | Cannot test yet, Place a Blackbox |
| GS-23 | Step 9 Ready (state switch) | Findings: CPU / disk / network inside range, H1 |
| GS-24 | Step 10 Ready | Origin table (SQL moved first), red D1, orange C1 and S1, grey M1 and H1, green set aside |
| GS-25 | Step 10 Cannot test yet | "Not enough data to date any tier…" |
| GS-26 | Step 10 Failed / Waiting | Failed alert or jumpedAhead |
| GS-27 | Step 11 Ready, Compare on 28 Oct | Verdict "Better, not fixed", comparison table, mix-of-APIs warning |
| GS-28 | Step 11 Compare on a day before 27 Oct | "The later day must come after the incident day." |
| GS-29 | Step 12 four lists | Need to do (4), Working on (1), What we learned (3), Ruled out (1); counters Done / Open |
| GS-30 | Step 12 Add suggested checks | Adds red/orange checks not already listed, or "Step 10 has no check that is not on the list already." |
| GS-31 | Step 13 with a stale step | Warning box and HTML button "Download report (HTML) with these warnings" |
| GS-32 | Step 13 Preview | Preview frame names Case 0042, windows, onset 09:14, SQL origin |
| GS-33 | Step 2 Out of date | Notice "The windows, sources or lists changed after this ran." Sticker "Out of date, rerun" |
| GS-34 | Show wireframe notes off | Every `.wfn` strip is hidden (`notes-off` on the root) |
| GS-35 | Per-step state switcher | Ready / Partly ready / Cannot test yet / Failed / Waiting / Out of date each swap that step's body using the copy above |
