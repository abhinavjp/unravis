# Investigation Timeline: source pack for an explainer video

Upload this file to NotebookLM as a source, together with the ten images listed in section 9. It is written to be read by a machine that cannot see the product: everything the video needs is in the text.

## 0. Read me first

- **Product:** Unravis, an offline Windows tool that helps a support engineer or junior developer find out what went wrong during an incident on a .NET / IIS / SQL Server / MongoDB system.
- **Feature in the video:** the **Investigation Timeline** screen (earlier working name: "tracing"; do not use that name).
- **Audience:** beginners. Support engineers, implementation consultants, junior developers. They do not know IIS, P95, Query Store or HTTP.sys. Explain meaning first, jargon second.
- **Tone:** calm, plain, short sentences. No hype.
- **Length:** about 6 minutes.
- **Status of what is shown:** a clickable prototype. All numbers are made-up demo data. The video must say so near the start and near the end. It must never say the software "works", "proves", "finds the root cause" or "uses AI".

## 1. The story in one breath

When production slows down, three places hold clues: the application's request log (the audit log), the web server (IIS) and the database (SQL Server, and sometimes MongoDB). Today you open each one separately. The Investigation Timeline puts all three on one time axis, compares the problem with an earlier period, shows how much data is missing, and lists **suspects** (not causes) ranked by simple, fixed rules.

## 2. Words, in plain English

| Term | Say it like this |
|---|---|
| Investigation window | The stretch of time when you think the problem happened. Up to 30 days. Default: the last hour. |
| Comparison period | An earlier stretch to compare against ("Compare it with"). It is only a comparison. It may not have been healthy. |
| Bar | One slice of time on the chart. The screen always says how long a bar is ("Each bar = 5 minutes"). |
| P95 | About 95% of requests finished within this time. The screen calls it "estimated". |
| 5xx | The server said "something went wrong on my side". |
| IIS | Microsoft's web server that sits in front of the application. |
| Gap | A period with no log data at all. Drawn as a labelled hatched band, never as zeros. |
| Suspect | Something worth checking, listed with the evidence for and against. It never claims to be the cause. |
| Red / Orange / Green / Grey | Red: most likely (its part of the system clearly moved first). Orange: likely. Green: ruled out by the data we have. Grey: not sure, because it could not be tested. Grey is never a pass. |

## 3. The demo world (all made up)

- The demo clock is fixed at 20 Oct 2026, 12:00 UTC.
- The audit log keeps data from 13 Oct and has a 2-hour recording outage on 19 Oct (02:00 to 04:00).
- A slowdown on one API (Payroll.Run) runs from about 09:14 to 11:30 on 20 Oct.
- The database moved first (about 09:11), then the application (09:14), then the web server (09:15).
- The SQL and IIS log files cover 20 Oct 06:00 to 12:00 only.
- One SQL Server connection holds two databases: `orders` (Production) and `PayrollApp` (Staging). The audit log lives in MongoDB (`PayrollAudit`, Production).

## 4. The tour, scene by scene

Use these as scenes. Each has the key facts, what to show (image file) and a narration draft.

### Scene 1: The problem (about 0:30). Image 01
Show the whole screen. Narration: "When production slows down you look in three places: the application log, the web server and the database. The Investigation Timeline puts all three on one time axis. This is a prototype with made-up data."

### Scene 2: Choose the period (about 0:45). Image 02
Controls card: quick range chips (Last 15 min to Last 30 days), the **Investigation window** and **Compare it with** pickers side by side, the source chips, **Refresh timeline**.
Narration: "Pick the period when the problem happened. Unravis also picks a comparison period: by default the equal stretch just before. It is only a comparison, not a promise that things were healthy. The longest period is 30 days. If the two periods overlap you get a notice, not a block."

### Scene 3: It runs by itself (about 0:30). Images 01, 02
When the screen opens the timeline builds automatically. It only reads evidence already in the case. It never imports files, never contacts a server and never changes the case. **Refresh timeline** runs it again after you change something.
Narration: "Nothing to press. The figures, the charts and the suspects load by themselves."

### Scene 4: What Unravis sees (about 0:45). Image 05
Three plain-English tiles, one per layer, for the demo window 09:00 to 11:30 against 06:30 to 09:00:
- **Application:** "Slower than usual". Slow-request time 522 ms, 2.4 times the comparison period.
- **Web server:** "Returning errors and slower responses". 210 server errors (4.4% of requests).
- **Database:** "Many slow calls and blocking". 2,005 calls over one second (40%), 15 blocking or deadlock reports.
Narration: "A reading aid in plain words. The figures and charts are the evidence. The tiles only help you read them."

### Scene 5: The figures (about 0:45). Image 03
Seven cards, each with a plain title, a big number, one sentence of meaning, and the technical term in brackets:
- Requests to the application: 4,216 (1.1 times the comparison period).
- Slow-request time (P95, estimated): 522 ms (2.4 times).
- Application server errors (5xx): 185 (4.4% of requests).
- Web server errors (IIS 5xx): 210 (4.4% of 4,816 requests).
- Slow web requests (IIS P95, estimated): 1,046 ms (4.0 times).
- Slow database calls (over 1 second): 2,005 (40% of 5,059 calls; 14.6 times).
- Database errors and blocking: 130 failed calls, 15 blocking or deadlock reports.
When the earlier period had none, a card says "Cannot compare with the earlier period" instead of inventing a ratio. P95 is marked "estimated" because the prototype approximates it; the product must compute it from every request's duration.

### Scene 6: Reading the charts (about 1:15). Image 04
Three lanes on one shared time axis. Every panel has a title and a labelled y-axis; the x-axis says what each bar covers.
- **API requests (audit log):** bars are requests per bar (left axis), the line is slow-request time P95 (right axis), the dashed line is the comparison period's level. It is the same chart the existing analysis uses.
- **Database (SQL Server):** slow calls and failed calls per bar; how long database calls took (P95); blocking and deadlocks per bar.
- **Web server (IIS):** requests by result (OK, client error 4xx, server error 5xx); how long web requests took; web connection errors (HTTP.sys).
- All three bend at about 09:11 to 09:15. Hover shows one moment across all three sources. Click a bar to select that moment.
Narration: "Every panel says what it plots. All panels share one time axis, so you can see which layer moved first."

### Scene 7: Gaps (about 0:40). Image 09
Choose Last 7 days. Every lane spans all seven days. Where a lane has no logs it shows a hatched band labelled "No logs for this time", with the exact range and length (for the SQL and IIS lanes in the demo: 13 Oct 12:00 to 20 Oct 06:00, 6 days 18 hours). The audit lane also marks its own 2-hour recording outage.
A lane whose logs cover the period but contain no events is **not** a gap: it draws normal zero bars with the note "Logs cover this whole period and contain no events". Hatching only ever means "unknown".
Narration: "Seven days means seven days for every source. Missing data is shown as missing, never as zero."

### Scene 8: Suspects (about 1:15). Images 06, 08, 10
A slim panel on the right, sticky while you scroll.
- It loads by itself, one suspect at a time ("Checking 25 suspects… 6 of 25"), then shows a summary ("Checked 25 suspects: 1 red, 1 orange, 5 grey, 18 ruled out").
- The default view shows the top five: all red first, then the best orange, then grey. **See all** opens the full list in place. Ruled-out suspects stay folded below.
- Demo result for the main scenario: **D1, "Blocking chain (head blocker or open transaction)", is red** because the database moved first (09:11). Evidence rows: database call time up (1,795 ms against 70 ms), 11 blocking reports against 0, top wait is a lock wait.
- Several suspects are **grey** because their data is not collected: the .NET thread-pool one (no .NET counters), the audit-write one and the Mongo spill one (Mongo profiler off), change history (no change list typed in), shared host effect (no second workload measured). Grey means "not sure", never "fine".
- Each card shows the reading in a sentence, the evidence rows (Must or Expect, yes, no or unknown), the cheapest check, what would overturn it, and "Show on chart".
- The panel says: "Suspects, not causes. Nothing here says why it happened."
- All rules are fixed and written down. There is no AI. The same data always gives the same answer.
Narration: "These are suspects, ranked by simple fixed rules. Red means its part of the system clearly moved first. Grey means we could not test it. It never says why something happened."

### Scene 9: Where does the evidence point? (about 0:40). Image 07
A folded card: "The problem appears isolated to Production. Strong evidence." Opening it shows the server as a whole (disk, CPU, memory waits, with a Source line), a table by database, an unnamed "Other databases" row (names hidden unless asked), a MongoDB part and "Rules used (defaults, not universal laws)". A switch "View evidence for" changes only what is shown, not the conclusion. When history is missing it says "There isn't enough evidence to narrow it down" and lists what to switch on.

### Scene 10: Close (about 0:20). Image 01
Narration: "Everything here is prototype data. The Investigation Timeline reads all three sources on one time axis, shows what is missing, and lists suspects with the evidence for and against."

## 5. The 25 suspects in the demo (plain words)

Grouped by where the evidence comes from. Each has a fixed rule; none names a cause.
- **Data quality:** audit log coverage gap compared with the web server's request count.
- **Load:** traffic surge; one route or client dominates (mix shift).
- **Web server and application:** HTTP.sys or IIS queue backup; .NET thread-pool starvation; audit-write coupling; payload-handling slowdown; request-filtering rejections; application pool unavailable; header or body receive timeouts; slow client or proxy response drain.
- **SQL Server:** blocking chain; deadlock storm; memory-grant waits; disk latency; scheduler pressure; command timeout burst; login storm.
- **MongoDB:** ticket or lock queue; shared mongod (another database dominates); spill to disk; connection rejection.
- **Host and change:** host CPU; shared effect across tiers; deploy or configuration change.
The real product's rule catalog has 44 entries (42 active suspects). The prototype shows 25 of them (16 from the existing guided analysis, 9 added from a research report).

## 6. What to say about how it was decided

- The name "Investigation Timeline" replaced "tracing" because in IT operations "tracing" means following one request through many services, which is a different thing. "Timeline" is the common word for chronological investigation views.
- The period you inspect is called the **investigation window** and the earlier one the **comparison period**.
- Red, orange, green and grey come from the existing guided analysis in the app, so the new screen speaks the same language.

## 7. Honest limits (say them)

- Prototype with synthetic data; not proof the software works.
- P95 on the cards is an approximation, labelled "estimated".
- 25 of 42 active suspects are shown. Some need data the product does not collect yet (host counters, .NET counters, change history); those stay grey.
- Server-wide counters in the demo assume a background sampler; without one the screen shows no numbers and says so.
- The timeline starting by itself and the top-five default are recommendations not yet confirmed by the product owner.

## 8. Words to avoid

Do not say: "tracing", "root cause", "proves", "AI", "machine learning", "guaranteed", "automatically fixes". Do say: "suspects", "evidence", "comparison period", "not enough evidence", "prototype", "made-up data".

## 9. Image index (upload these; names are in `prototype/video/images/`)

| File | What it shows |
|---|---|
| 01-whole-screen.png | The whole Investigation Timeline at 1920 px width, rail on the right |
| 02-controls.png | Range chips, Investigation window, Compare it with, sources, Refresh timeline |
| 03-overall-figures.png | The seven overall figure cards |
| 04-charts.png | The three lanes on one shared time axis |
| 05-what-unravis-sees.png | The three plain-English layer tiles |
| 06-suspects-rail.png | The Suspects panel, top five, first card opened to show evidence rows |
| 07-where-does-the-evidence-point.png | The folded evidence card, opened |
| 08-suspects-what-if-and-see-all.png | "See all" expanded; a switched-on example (HTTP.sys receive timeout, orange) |
| 09-seven-days-with-gaps.png | Seven days, hatched gaps with labels |
| 10-no-history-mostly-grey.png | The "no history" situation: nothing red, most suspects grey with what is needed |

## 10. For humans: where everything lives

Repository `abhinavjp/unravis`, branch `prototype/tracing-paper-e2e`.
- Prototype: `prototype/tracing-paper-PROTOTYPE.html`
- Explainer (long form): `prototype/TRACING-SCREEN-EXPLAINER.md`
- Naming decision: `prototype/NAMING-DECISION.md`
- Flow coverage: `prototype/FLOW-COVERAGE.md`
- This pack and its images: `prototype/video/`
