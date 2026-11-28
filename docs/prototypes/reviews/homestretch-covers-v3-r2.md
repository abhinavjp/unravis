GOLDFISH REVIEW, round 2
Reviewer: claude-sonnet-5-5, effort: medium

## Tasks
1. Partly: On Overview, the develop row says "Slot free on local. Starts soon." and the Queue card says "local: 1 of 2 slots busy". So the machine is named (local). The slot is not: nothing says "slot 1" or "slot 2" for the waiting change. The "?" next to "local: 1 of 2 slots busy" says "Slots are numbered in the order jobs started", so a waiting job has no number yet. The words "which slot on that machine" never get an answer before the job starts. In the "Running" state the slot appears ("local · slot 1 of 2"), but only after it started.
2. Partly (No in the default state): Overview, develop row: "Slot free on local. Starts soon. Paused? Queue controls · waiting 2 h 25 min". It says a slot is free, it says "Starts soon", and it says it has waited 2 h 25 min. I cannot tell which is true. The only action is the link "Queue controls". It goes to the Queue page, which has a "Pause the queue" button but no sign of whether the queue is paused. "Pause the queue" only shows "Prototype only." Other machine states give a clear reason: "Waiting for a free slot on local (2 of 2 busy).", "local is not starting runs: disk is low. See machine", "local is offline (last seen 14 min ago). Nothing can start. See machine". But none of them says what to do. For low disk and offline there is no step like "free space" or "restart the machine". "See machine" is the only next step, and on the Machines page it does nothing (I am already there).
3. Yes (on the Machines page), Partly on the first screen. Machines page: "1 of 1 machines online, 1 free slot", chips "Online", "Accepting runs", "Busy 1 of 2", "Disk: ok", two small squares (one filled, one empty) and "1 of 2 slots busy". On the Overview, only "local: 1 of 2 slots busy" is shown. Nothing there about disk or whether the machine is online. In the Offline state the Overview Queue card still says "local: 1 of 2 slots busy", which looks healthy.

## Story test
| Screen / state | Where am I | What happened | Where is the problem | Steps | Everything fine | Next action |
|---|---|---|---|---|---|---|
| Overview, Waiting + Ready (first screen) | P: "Homestretch / Unravis / Overview" tell me the product, project and page. I could not tell what "Main branches" and "Pull requests" are for. | P: "The master branch is failing." (master row "Failed", "Go - tests (shard 2 of 3)"). Also develop is "Waiting". | P: the "Where it failed" column names the failed job for master. For develop's wait the column is "–", and the reason sits in a different column. | N: there is no order of steps. I only see rows with a status each. | N: red headline. The waiting row contradicts itself ("Slot free" yet "waiting 2 h 25 min"). | P: "Queue controls", "See which", the "master" link. None says what to do about the failure. |
| Overview, Low disk | P | P: the headline stays "The master branch is failing." Under it: "local is not starting runs: disk is low. See machine". | N: the disk line sits directly under the master headline, so it reads as the cause of master failing. | N | N: no explicit mark | P: "See machine" goes to Machines, where "Disk: low" and "Not accepting" are shown, with no fix. |
| Overview, Offline | P | P: "local is offline. Nothing can start. See machine". The Queue card still says "local: 1 of 2 slots busy". | P: the warning is clear. The card's slot line contradicts it. | N | N | P: "See machine" |
| Overview, Running / Done passed / Done failed / One change only / More than 6 | P | Y: the develop row changes to "Running local · slot 1 of 2", "Passed" or "Failed", the headline becomes "The develop and master branches are failing.", and the card says "Checking", "Checked" or "Next run checks N changes". | Y for Done failed ("Go - tests (shard 2 of 3)"). | N | Y or N, depending on state | P: "See which" opens the changes list. |
| Queue | Y: heading "Queue", "Now and next", "Recently finished". | P: "1 running", "1 queued", "The next run will check 5 changes." | N: the waiting row repeats the contradictory "Slot free on local. Starts soon. Paused? Queue controls". "Queue controls" does nothing (I am already on the Queue page). | P: "Running first" is an order, but the waiting one was queued 15:47 and PR 479 queued 18:10 started first. | N: nothing shows whether the queue is paused | P: "Move to top", "Cancel", "Retry", "Pause the queue". All answer "Prototype only." |
| Machines (Ready / Busy / Low disk / Offline) | Y: "A machine is a PC that runs checks. Each slot runs one job at a time." | Y: "1 of 1 machines online, 1 free slot" | P: Low disk gives "Disk: low" and "Not accepting", with no fix. Offline shows "Disk: unknown" next to "Busy 1 of 2". | P: "Running now" lists jobs with "slot 1". | Y: chips and squares | P: the links in "Running now" and "Waiting for this machine". Low disk and offline have no fix step. |
| Run page: Push develop, Waiting | P: breadcrumb "Runs / Push develop · ci.yml". The file name "ci.yml" is unexplained. | P: "Waiting", "Slot free on local. Starts soon...". The header chips show no machine pill. | N: same contradiction | N: "The run, in order: Jobs appear here once the run starts." | N | P: "Queue controls" |
| Run page: Push develop / master, Failed | Y | Y: big "Failed", "1 failed job recorded · 40 min 9 s", count "1 failed" | P: the card "Go tests (shard 2 of 3)" with "go-tests-matrix.shard-3-2". The table beside it marks all 5 changes "failed". | N: "The run, in order" shows one job and "Step 5" only. Steps 1 to 4 are not shown. | N | P: "See the log" only expands a line, "The full log opens on GitHub in the real app." There is no link to GitHub. |
| Run page: PR 479, Running | Y | Y: "Running on local, slot 1 of 2. Other slot: free." | Y (none) | N: "Jobs appear here as they start." | P: "Running" and "2 min 8 s" | N: no visible action except the "local" link |
| Run page: Stopped (from Runs list) | P | P: "Stopped", queued 15:45 and finished 15:47 | N: no reason given. The reason ("a newer head ... arrived") appears only in the Runs list. | N: "Per-job details were not recorded for this run." | ? | N |
| Runs | P | Y: a list with the 633 total | N | N | N | P: row links, the filter buttons "Failed 1", "Passed 3", "Stopped 24", "Search runs", "Show 18 more" |
| Releases | P | P: "rc channel 0.1.0-rc.68 → stable channel unknown", columns "Verify" and "Tag time" with "Not recorded here" | N | N | N | P: "Release on GitHub" answers "Prototype only." |
| Holds, Activity | N | N: "Not part of this prototype" | n/a | n/a | n/a | N: dead end |

## Scan test
Takeaway in one sentence: Master is failing, one change (develop) is waiting, one PR is running on "local", and the queue has 1 running and 1 waiting.
Scan or read: scan for the headline and the two row icons. I had to read for the develop row.
Visual or wall of text: Mostly scannable. The status icons, the red headline and the Queue card give clear groups. The heaviest text block is the develop row's reason line: "Slot free on local. Starts soon. Paused? Queue controls · waiting 2 h 25 min". The tiny bar glyphs in "Recent pushes" mean nothing to me.
Rough words on first screen: about 110

## Findings
1. [blocker] [wrong-signal] Overview develop row (Waiting state, machine Ready): "Slot free on local. Starts soon. Paused? Queue controls · waiting 2 h 25 min" (the same line on the Queue page, the Overview Queue card and the run page)
   Expected: one clear reason for a 2 h 25 min wait.
   What I saw / thought: it says a slot is free and "Starts soon", yet it has waited more than two hours and a later PR (PR 479) started first.
   Why it lost me: I cannot tell if it is stuck, paused or just slow. The question "why is it not starting" is answered with a guess ("Paused?") and a link.
2. [major] [no-next-step] Queue page: heading "Queue", with the "Pause the queue" button
   Expected: the page tells me if the queue is paused, and a button to resume.
   What I saw / thought: only "Pause the queue", with no paused or running mark. The button and the other actions ("Move to top", "Cancel", "Retry") answer "Prototype only.".
   Why it lost me: following "Paused? Queue controls" leads to a page that cannot confirm or deny pausing.
3. [major] [no-next-step] Overview, Low disk and Offline states: "local is not starting runs: disk is low. See machine" and "local is offline (last seen 14 min ago). Nothing can start. See machine"
   Expected: a step to take.
   What I saw / thought: "See machine" goes to the Machines page, which shows "Disk: low" and "Not accepting" and nothing more. On the Machines page itself, "See machine" and the "local" link do nothing.
   Why it lost me: the cause is clear but there is no action I can take from the screen.
4. [major] [disconnected] Overview, Low disk and Offline: line under "The master branch is failing."
   Expected: the machine warning is separate from the master failure.
   What I saw / thought: "local is not starting runs: disk is low. See machine" sits right below the headline, so it reads as the reason master is failing.
   Why it lost me: two different problems are joined under one headline.
5. [major] [wrong-signal] Overview, Queue card in Offline state: "local: 1 of 2 slots busy"
   Expected: an offline signal.
   What I saw / thought: it looks healthy while the machine is offline. The Machines page also shows "Busy 1 of 2" and "Disk: unknown" for an offline machine.
   Why it lost me: the card tells me the opposite of the banner under the headline.
6. [major] [disconnected] Run page, Failed: card text "This run failed. We cannot tell which change broke it." with the table "Result on GitHub" showing "failed" for #471, #468, #474, #477 and #478
   Expected: the changes are shown as unknown or "not yet known".
   What I saw / thought: all five say "failed", which contradicts "cannot tell which change broke it".
   Why it lost me: I would believe every change is broken.
7. [major] [no-next-step] Run page, Failed: "Open the failing job on GitHub, push a fix. See the log"
   Expected: a link to the job on GitHub.
   What I saw / thought: there is no link. "See the log" only expands text ("The full log opens on GitHub in the real app."). The sentence is broken across lines and the button is inline in the text. "Push a fix" is a developer step I cannot do without outside knowledge.
   Why it lost me: the one next action is not reachable.
8. [major] [disconnected] Overview, develop row and Queue card (Waiting state): the machine is named ("local") but no slot is
   Expected: "next slot: 2" or a "free" marker for a specific slot.
   What I saw / thought: slot numbers appear only after the job starts ("Slots are numbered in the order jobs started" in the "?").
   Why it lost me: Task 1 asks which slot. The screen cannot say.
9. [major] [hidden-state] Run page, Stopped: "Stopped", "Per-job details were not recorded for this run."
   Expected: the reason on the page.
   What I saw / thought: the reason ("a newer head 9888212cd2 arrived", "the pull request is no longer open") appears only in the Runs list and the Queue page, not on the run page itself.
   Why it lost me: arriving from a link, I do not learn why the run was stopped.
10. [major] [dead-end] Queue page buttons "Pause the queue", "Move to top", "Cancel", "Retry", the "PR 479 into develop" links; Releases "Release on GitHub"; run page "#471" ...
   Expected: an action or a navigation.
   What I saw / thought: each answers "Prototype only." right beside the button, easy to miss. "PR 479 into develop" looks like a link but only shows that text.
   Why it lost me: nothing happens beyond a small note.
11. [minor] [dead-end] Holds and Activity tabs
   Expected: a page.
   What I saw / thought: "Not part of this prototype" with no way forward.
   Why it lost me: two of seven tabs are empty.
12. [minor] [jargon] "Where it failed" column, "Go - tests (shard 2 of 3)", "go-tests-matrix.shard-3-2", "Step 5"
   Expected: a plain description.
   What I saw / thought: "shard" and the matrix id mean nothing. The same failed job is named three ways. The Queue page repeats it: "Go tests (shard 2 of 3) (go-tests-matrix.shard-3-2), step 5: Go - tests (shard 2 of 3)".
   Why it lost me: long, repeated and unreadable.
13. [minor] [wrong-signal] Overview, "Recent pushes" column (green bar with a grey stub; orange bar)
   Expected: a label, tooltip or legend.
   What I saw / thought: coloured bars with no explanation of what they count.
   Why it lost me: they look like data I cannot read.
14. [minor] [jargon] Priority chip "Normal", "Move to top", and the status words "Replaced", "Stopped", "Cancelled"
   Expected: a short explanation.
   What I saw / thought: "Replaced" (a newer head arrived), "Stopped" and "Cancelled" look similar. "Priority" has no other values.
   Why it lost me: I cannot tell the difference between the three ended states.
15. [minor] [wrong-signal] Mobile (390 px) Overview: the Waiting and Failed words vanish from the rows (icon only), the "Where it failed" column disappears (master row shows no failed job), the tab bar cuts off after "Mach", and the Queue card moves above the tables.
   Expected: the same information.
   What I saw / thought: on the phone the failing master row names no job, and "Holds" and "Activity" are off screen.
   Why it lost me: the failure location is lost on the small screen.
16. [minor] [too-much-text] Overview, develop row; Queue card repeats the same reason sentence and appends "Next run checks 5 changes · See which"
   Expected: one place for the reason.
   What I saw / thought: the same wait reason is printed in the table row and again in the card in the Busy, Low disk and Offline states.
   Why it lost me: the card and the row say the same thing.
17. [minor] [jargon] Runs page columns "Waited", "Took", "Commit" and the filter "Latest per PR or branch"; Releases "Verify" column "Not recorded here" and "stable channel unknown"
   Expected: plain meaning.
   What I saw / thought: "Verify" is never explained. "Not recorded here" repeats three times with no effect.
   Why it lost me: columns that say nothing.
18. [minor] [other] Run page title "Push develop · ci.yml" and "Queue" heading: a visible blue box (focus ring) appears around the title after navigating
   Expected: plain heading.
   What I saw / thought: it looks like an input or a selected item.
   Why it lost me: looks clickable and is not.

## Balance
too wordy. Worst three places: (1) the develop row reason line "Slot free on local. Starts soon. Paused? Queue controls · waiting 2 h 25 min" and its repeat in the Queue card; (2) the failed-run card "This run failed. We cannot tell which change broke it. Open the failing job on GitHub, push a fix. See the log"; (3) Queue "Recently finished" failure detail "Go tests (shard 2 of 3) (go-tests-matrix.shard-3-2), step 5: Go - tests (shard 2 of 3)". These could be a status word plus one icon, and the job name once.

## Words I could not explain
Fast gate: Runs, Queue, Machines pages
Full CI: Runs, Queue
CI / ci.yml / fast-gate.yml: run page title
shard / go-tests-matrix.shard-3-2: Overview, run page, Queue
slot: Overview, Machines, run page (only the "?" helps)
head ("a newer head ... arrived"): Runs, Queue
Replaced / Stopped / Cancelled: Runs, Queue
Priority "Normal": Queue
Move to top: Queue
Holds: top tab (empty)
Activity: top tab (empty)
Verify: Releases
rc channel / stable channel: Releases
Tag / Tagged: Releases
Recent pushes (bars): Overview
Accepting runs / Not accepting: Machines
captured 18:12 UTC: header
checks 5 changes (chip): Overview, Runs
Merged "change": run page (explained in a "?")
Waited: Runs

## Outside knowledge used
- "Pull request", "branch", "master", "develop", "merged" and "push a fix" are Git terms I would not know.
- "Fast gate" versus "Full CI": I assumed a quick check and a full check.
- "Slot" as a parallel lane on a machine: only half-explained by "Each slot runs one job at a time".
- That "local" is the PC running the checks: explained only on the Machines page.
- That a "newer head" means a newer commit replaced the old one.
- That "Disk: low" blocks runs and needs freeing space.
