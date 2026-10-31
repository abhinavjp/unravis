# Independent review prompt for a cloud agent (paste this message; upload the 5 files listed at the end)

You are an independent reviewer. You did not write any of the material I am uploading. Use your own web research; do not trust the documents, check them. You cannot see my code or computer, only the uploaded files, so base everything on those and on public sources. If a file did not upload or a link will not open, say so plainly and do not guess.

## Background
Unravis is a local tool that helps engineers find why an API became slow. Its "Guided analysis" screen walks a user through steps: frame the time window, check the data can be trusted, compare slow vs normal minutes, look at SQL Server, MongoDB, the host machine and origin suspects, then a checklist and export. We are redesigning it first as a clickable HTML wireframe, built by small AI-agent work packets, each handed to a cheaper AI agent that must implement it blindly, without exploring.

The owner reported 15 issues on the current wireframe:
1 "+ Add a change" button squashed. 2 Step 1 "Trust data": the gap sentence makes no sense; purpose of the screen unclear. 3 "Change the window" button squashed. 4 Step 3 "Time split": unclear whether per API or an average; needs an API selector. 5 "Nearby activity" needs a timeline chart. 6 Step 4 "Load" chart is blank. 7 Step 0 frame: release should be optional; windows up to 3 months (incident) plus 3 months or more (reference); consequences, pagination and an overlap warning; recurring slowdowns such as "last Thursday of each month". 8 Step 7 SQL is text only: needs charts, evidence and a "Why" drill-down to a full timeline. 9 Step 8 Mongo "Add a source" button squashed. 10 Mongo same as SQL. 11 Step 9 Host has no chart or evidence; wants 2 to 3 example scenarios per colour (red, amber, green, gray) and an explanation of what each colour means wherever reused. 12 Step 10 origin suspects are only sentences. 13 Checklist "+ Add item" squashed. 14 Export "Download report" squashed. 15 Manual tool "Response time" chart blank; review ALL manual tools for missing, useless and extra tools.

## Uploaded files
1. `wireframe-v2-implementation-plan.md`: the plan, packets, dependency graph, and section 8 (the packet format and batching rules).
2. `W0-batch-bugfix.md`: a worked example of one packet in that format.
3. `research-sql-mongo-host-evidence.md`: research on SQL Server, MongoDB and host evidence.
4. `research-long-windows.md`: research on long time windows and recurring slowdowns.
5. `owner-issues-triage.md`: root causes found and themes.

## Your tasks
A. **Validate the research (files 3 and 4).** For each factual claim (SQL Server DMVs and views, wait types, thresholds, MongoDB serverStatus/currentOp fields, replication lag, tool names, UX patterns from Datadog, Grafana and others), check it against primary sources such as vendor docs. Mark each: correct, wrong, outdated, unverifiable, with a source link. Flag thresholds that are invented rather than documented.
B. **Find what the research missed.** Evidence or manual tools an engineer investigating slowdowns would expect for SQL Server, MongoDB, a host machine, and long-window or recurring-slowdown analysis.
C. **Review the plan (file 1).** Does each of the 15 issues have at least one packet that fixes it? Are dependencies right (wrong order, hidden dependency, two packets editing the same file at the same time)? Anything over-built or missing? Is the order sensible?
D. **Review the packet method (section 8 of file 1, and the example in file 2).** The aim: each packet is so detailed that a cheap AI agent can implement it with no exploring, yet small enough for one reply of about 16,000 output tokens, and batching saves tokens. Judge:
   - Is the packet template complete? What would an agent still have to guess or explore? Name the specific missing fields.
   - Is the example packet (file 2) really blind-implementable? Find ambiguity, wrong assumptions, or checks that would not catch a failure.
   - Are the batching rules safe (what to batch, size limits, shared files)? Does batching risk quality or conflicts?
   - Are the token-saving measures real (deterministic check script, one review per wave, shared context upload, identical shared preface for caching)? Which will not work, which are missing, and what are the usual ways such agent pipelines waste tokens or fail?
   - Is "reviewer from a different model family, once per wave" sound? Suggest a better review structure if you have one.
E. **Risks.** What could make the finished wireframe still not satisfy the owner? What would you change first?

## Output (short and structured)
1. Verdict in two lines.
2. Findings table: `#`, `severity (high/med/low)`, `file + section`, `problem`, `evidence or link`, `recommended change`.
3. Claim-check table (task A): claim, verdict, source.
4. Packet-method assessment (task D): what works, what is missing, an improved packet template if you propose changes.
5. Top 5 changes to the plan, ranked.
Do not rewrite the documents.
