GOLDFISH REVIEW, round 1
Reviewer: claude-sonnet-5-5, effort: medium

## Tasks
1. Partly: The Overview "Pull requests" row reads "Running", "local · slot 1 of 2", "PR 479", "started 18:10 UTC". The right-hand "Queue" card says "Running on local, slot 1 of 2". "local" is underlined, but nothing on that screen says what "local" is. I only learned "A machine is a PC that runs checks. Each slot runs one job at a time." after I opened the Machines tab. "Slot" is not explained anywhere on the first screen. I also had to read the Overview, Queue and Machines pages as a pair to get the full answer.
2. Partly: The develop row shows "Waiting", "checks 5 changes", and "queued 15:47 UTC". The Queue card says "Starts next on local". On the Queue tab the same row says "Starts next on local / Will check 5 changes together ?". I only got a reason when I switched the review dropdown to "Busy (both slots)", "Low disk" or "Offline". In those states the text is "Waiting for a free slot on local (2 of 2 busy). Position 1.", "local is not starting runs: disk is low. See machine" and "local is offline (last seen 14 min ago). Nothing can start." In the default state a slot is free and the run has waited from 15:47 to 18:12, yet nothing says why it has not started. There is also no "Pause the queue" status and no hint that someone paused it. The only actions I saw were "Move to top" and "Cancel", and both showed "Prototype only." when I clicked them. "See machine" and "See which changes" are links, but I did not see "See machine" take me to a place that lists a fix.
3. Partly: Machines tab. I saw "1 of 1 machines online, 1 free slot", "Online", "Accepting runs", "Busy 1 of 2", "last seen 2 min ago". In the Low disk state: "Online / Not accepting / Not starting new runs: disk is low." In the Offline state: "Offline / Not accepting". Disk is only mentioned when it is bad. In the healthy state there is no disk figure, so "healthy" is inferred from the absence of a warning. I also found no explanation of "Accepting runs", and nothing to do about "low disk" or "offline" except words.

## Story test
| Screen / state | Where am I | What happened | Where is the problem | Steps | Everything fine | Next action |
|---|---|---|---|---|---|---|
| Overview (default, Waiting) | P: "Homestretch / Unravis / Overview" is clear. What the three blocks (Main branches, Pull requests, Queue) belong to is not. | "The master branch is failing." | master row, "Go - tests (shard 2 of 3)". Clear for master, but there is no link or action. | Not shown. Only "Running / Waiting / Failed" and times. | N: red headline. Develop "Waiting" is not mentioned in the headline. | Click "master" or "develop". No button says to do this, and nothing says what to do about the failure. |
| Overview, Running / Done passed / Done failed / One change only / More than 6 | P | Headline stays "The master branch is failing" even when develop is Running or Passed. Done failed changes it to "The develop and master branches are failing." | Same as default | Same | Unclear: headline red but develop is "Passed" | Same as default |
| Overview, machine Busy / Low disk / Offline | P | Only the small Queue card text changes ("Waiting for a free slot on local (2 of 2 busy).", "disk is low. See machine", "offline ... Nothing can start."). The top-right counters and the headline do not change. | In the card. It is easy to miss and sits far from the "Waiting" row. | Not shown | N, but only the card says so | "See machine" link in the low-disk case only. Offline has no link. |
| Runs | P | List of 30 runs, filters All 30 / Failed 1 / Passed 3 / Stopped 24 | master Failed row | Not shown | Unclear | Click a row |
| Run page: develop, Waiting | P | "Waiting", "Starts next on local" | Nothing marked as the problem. No reason is given for the wait. | "The run, in order: Jobs appear here once the run starts." Nothing is listed. | N: still queued after about 2.5 h, but the page reads calm | None visible. No button other than links. |
| Run page: develop Running / Passed / Failed | P | "Running on local, slot 1 of 2. Other slot: busy." / "Passed 40 min 9 s" / "Failed. 1 failed job recorded" | Failed: "Go tests (shard 2 of 3) go-tests-matrix.shard-3-2 See the log" | Failed shows one job, "Step 5". Passed shows "Per-job details were not recorded for this run." | Failed: N, "We cannot tell which change broke it." | "Open the failing job on GitHub, fix it, then merge again." No button or link is placed beside this text. |
| Run page: master (Failed) | P | Same wording as the develop Failed case | Job "Go tests (shard 2 of 3)", step 5 | One job only | N | "Open the failing job on GitHub, fix it, then merge again." |
| Run page: PR 479 | P | "Running on local, slot 1 of 2. Other slot: free." | None | "Jobs appear here as they start." Nothing listed yet. | Unclear | None |
| Queue | P | "1 running, 1 queued", "The next run will check 5 changes. See which changes" | None named in default state | Running first, then Waiting, then Recently finished | Unclear | "Cancel", "Move to top", "Retry", "Pause the queue". All show "Prototype only." |
| Machines | P | "1 of 1 machines online, 1 free slot" | None in the default state. In other states the problem is in the State cell. | Not applicable | Yes in default, read as "no warning shown" | None visible. Only the "PR 479" link under "Running now". |
| Releases | P | "rc channel 0.1.0-rc.68 → stable channel unknown" | None | Not applicable | Unclear: "Verify: Not recorded here", "stable channel: unknown" | Click a version row to see tag details and "Release on GitHub" |
| Holds / Activity | N | "Not part of this prototype" | n/a | n/a | n/a | Dead end. No next action. |
| Where this fits (bottom control) | N | A review diagram of how pages link, e.g. "Run page · Machine pill → Machines" | n/a | n/a | n/a | "Close" only |

## Scan test
Takeaway in one sentence: Master is red, develop is waiting, a pull request (PR 479) is running on "local", and there is a queue box on the right.
Scan or read: scan (shortened status words, colored icons, short rows), but a reader cannot tell why develop is waiting.
Visual or wall of text: Mostly visual (status icons, bars, a 2-row table). The heaviest block is the right Queue card, a stack of small sentences: "1 running, 1 queued. / Running on local, slot 1 of 2 / local: 1 of 2 slots busy / Starts next on local / Next run checks 5 changes. See which changes". Each line restates the other lines.
Rough words on first screen: about 150 (about 100 in the main area, about 50 in the header and bottom controls).

## Findings
1. [blocker] [no-next-step] Overview "Waiting" row for develop ("develop 9888212 checks 5 changes ... queued 15:47 UTC"), Queue card "Starts next on local", Queue tab row "Starts next on local", Run page "Starts next on local" (default state)
   Expected: A reason the run is waiting, and an action if it is stuck.
   What I saw / thought: The line says "Starts next", yet 2.5 hours have passed and a slot is free ("1 of 2 slots busy").
   Why it lost me: The only visible reason for waiting appears in the Busy, Low disk and Offline states. With a slot free and nothing starting, there is no reason shown and no action. I could not do task 2 from the default screen.
2. [major] [wrong-signal] Header counters "1 running · 1 waiting", "Queue 2" tab badge, Queue card "1 running, 1 queued."
   Expected: One count, repeated the same way.
   What I saw / thought: Three counts say almost the same thing in three different wordings. The Queue tab badge says "2" while the header says "1 running · 1 waiting". The Runs tab badge says "633" and the list says "30 of 30 loaded (633 in all)".
   Why it lost me: I could not tell whether 2 meant two waiting, or one running plus one waiting.
3. [major] [hidden-state] Overview, with the review control set to Busy, Low disk, Offline
   Expected: The machine problem to appear next to the waiting row, and in the headline or the top counters.
   What I saw / thought: Only a small line inside the right-hand Queue card changes. The headline stays "The master branch is failing.". The top counters stay "1 running · 1 waiting" (Busy shows "2 running").
   Why it lost me: A machine that is offline or out of disk is the biggest reason nothing starts, and it is buried in a box that looks like a side note.
4. [major] [no-next-step] Run page "Failed" (master and develop): "This run failed. We cannot tell which change broke it. Open the failing job on GitHub, fix it, then merge again."
   Expected: A button or link at that spot.
   What I saw / thought: The line tells me to open the job on GitHub, but the only link is "See the log" higher up, in a different block.
   Why it lost me: The advice and the link are in two places. "Fix it, then merge again" assumes I know Git and what "merge" means (outside knowledge).
5. [major] [disconnected] Overview vs Machines vs Queue in the Busy and Offline states
   Expected: The numbers to agree across pages.
   What I saw / thought: In Offline, the machine is "last seen 14 min ago" but still shows "PR 479 ... started 18:10 UTC" running (captured 18:12). In Busy, PR 477 "started 15:27 UTC" is running, yet in the default state PR 477 is "Passed ... finished 15:47 UTC". Overview Failed shows develop "started 16:21" while the Runs list says "Fast gate · Push to develop ... started 16:19".
   Why it lost me: The times and states differ from page to page, so I could not trust any one of them.
6. [major] [jargon] "slot", "local", "Machine", "Fast gate", "Full CI", "head", "shard", "step" (Overview, Queue card, Runs rows, Run page)
   Expected: Plain words on the first screen.
   What I saw / thought: "slot" is not explained at "local · slot 1 of 2". "local" is underlined but looks like a word, not a computer name. "Fast gate" and "Full CI" appear in Runs and Queue with no explanation.
   Why it lost me: Task 1 depends on understanding "local" and "slot". I only got them from the Machines tab.
7. [major] [wrong-signal] Queue tab buttons "Pause the queue", "Move to top", "Cancel", "Retry" and Prototype note "Prototype only."
   Expected: Something to happen.
   What I saw / thought: Each click showed "Prototype only." beside the button and nothing else.
   Why it lost me: I could not tell what "Pause the queue" does or whether it is already on. These buttons look like the answer to "nothing starts" and do nothing.
8. [minor] [disconnected] Run page, develop Waiting: "The run, in order / Jobs appear here once the run starts."
   Expected: The steps that will run.
   What I saw / thought: An empty box with one line of text.
   Why it lost me: "What are the steps?" is not answered, and the page shows no next step of any kind.
9. [minor] [jargon] Overview "Recent pushes" column (small colored bars), column header "Where it failed" with "–" in the waiting row
   Expected: A hint for the bars and for "–".
   What I saw / thought: Tiny green and orange bars with no label. "–" gives no hint whether it means not run yet or not applicable.
   Why it lost me: I could not read the bars at all.
10. [minor] [jargon] Overview "checks 5 changes" chip, and Queue "Will check 5 changes together ?"
   Expected: A plain meaning.
   What I saw / thought: The "?" says "A change is a pull request that was merged into develop. One run can check several changes together." That is two sentences of jargon ("pull request", "merged", "develop").
   Why it lost me: I had to click the "?" to see this, and then still needed Git knowledge. The chip itself looks clickable on the first screen and I could not tell where it goes. (outside knowledge)
11. [minor] [hidden-state] Header "captured 18:12 UTC", Overview "queued 15:47 UTC" / "started 15:47 UTC"
   Expected: Ages ("2 h 25 min ago") or a date.
   What I saw / thought: All times are clock times in UTC. I had to subtract to see that the waiting run is 2.5 hours old.
   Why it lost me: The one number that shows something is stuck is not shown.
12. [minor] [outside-knowledge] Run page change list: "Result is sent to GitHub when this run ends", "Marked passed on GitHub", "Part of the failed run"
   Expected: Short status words.
   What I saw / thought: Long amber sentences repeated on each of 5 rows.
   Why it lost me: Five identical sentences are text the layout already shows, and "GitHub" is assumed to be known. A single column heading or one icon could do this.
13. [minor] [disconnected] Runs tab counts: "All 30 / Failed 1 / Passed 3 / Stopped 24" and "Latest per PR or branch" switching to "All 12 ... 6 of 30 loaded"
   Expected: Counts that add up.
   What I saw / thought: 1 + 3 + 24 = 28, not 30 (running and waiting not shown as filters). With "Latest per PR or branch" ticked, "All 12" but the line reads "6 of 30 loaded (633 in all)". "Show 18 more" appears at the bottom and loads fewer than 18.
   Why it lost me: The numbers do not reconcile.
14. [minor] [dead-end] Holds and Activity tabs, "Not part of this prototype"
   Expected: Content, or the tabs hidden.
   What I saw / thought: Two empty tabs in the main nav.
   Why it lost me: I cannot tell whether "Holds" is where a stuck run would be explained (task 2).
15. [minor] [too-much-text] Overview Queue card (right panel)
   Expected: Fewer lines.
   What I saw / thought: "1 running, 1 queued. / Running on local, slot 1 of 2 / local: 1 of 2 slots busy / Starts next on local / Next run checks 5 changes. See which changes". The same facts already sit in the table beside it.
   Why it lost me: Repeats the layout; could be a count and one status word.
16. [minor] [wrong-signal] Machines tab, healthy state
   Expected: A visible sign that the machine is healthy (disk free number, last check result).
   What I saw / thought: Only "Online / Accepting runs / last seen 2 min ago". Disk appears only when low.
   Why it lost me: I could infer "healthy" only from no warning.
17. [minor] [other] Mobile 390x844 Overview
   Expected: The same status words.
   What I saw / thought: The words "Waiting" / "Failed" / "Running" and the "Where it failed" column are gone, leaving only icons. The Queue card moves above Main branches. The nav row cuts off "Overview" and "Holds/Activity".
   Why it lost me: A round orange icon alone does not say Waiting vs Running.
18. [minor] [outside-knowledge] Release page: "rc channel", "stable channel", "Verify: Not recorded here", "unknown", "Planned link: each change here will show the run that checked it."
   Expected: Plain explanation.
   What I saw / thought: Unknown words with no tooltip. "Planned link" speaks about a future feature.
   Why it lost me: I could not tell if "Not recorded here" is a problem.
19. [minor] [wrong-signal] Bottom bar "Show state", "Prototype: machine state", "Where this fits", and "Prototype. Dashed controls for review."
   Expected: Out of the way.
   What I saw / thought: They sit on top of the page bottom and hide table rows at the 1440 view and on mobile (they cover "Prototype. Dashed controls" line).
   Why it lost me: They look like part of the product.

## Balance
too wordy. Three worst places: (1) the Overview Queue card (five lines repeating the table), (2) the Run page "Changes this run checks" table (five identical "Result is sent to GitHub when this run ends" sentences), (3) the Failed run page text "This run failed. We cannot tell which change broke it. Open the failing job on GitHub, fix it, then merge again." Also too sparse in one place: the reason a run waits in the default state.

## Words I could not explain
slot / slot 1 of 2: Overview ("local · slot 1"), Queue card, Machines tab (the machine tab explains "Each slot runs one job at a time", the Overview does not)
local: Overview Pull requests row, Queue card, Runs table, Machines tab
Fast gate / Full CI: Runs, Queue
checks 5 changes: Overview chip, Queue card
Where it failed: Overview column
Recent pushes (colored bars): Overview column
Accepting runs / Not accepting: Machines tab
Waited / Took: Runs columns
Stopped: Runs filter and status ("a newer head 9888212cd2 arrived")
Replaced: Queue Recently finished
Verify / Not recorded here: Releases tab
rc channel / stable channel: Releases tab
Holds: nav tab, empty page
Latest per PR or branch: Runs checkbox
captured 18:12 UTC: header
go-tests-matrix.shard-3-2: failed job name on Run page

## Outside knowledge used
- That "master" and "develop" are branches, that a "pull request" is a proposed change, and that a "merge" combines it into develop.
- That a "run" is an automated check on a pushed change (CI), and that "Full CI" and "Fast gate" are two different check sets.
- That "GitHub" is where the code and checks live, and that "Open the failing job on GitHub" means a link outside this app.
- That "slot" means one job at a time on a machine, and that "local" is a PC name. The Machines tab text gave me this, but only after I opened that tab.
- That "rc" means release candidate.
- That a waiting run with a free slot most likely points to a paused queue or a stuck run, which nothing on screen said.
